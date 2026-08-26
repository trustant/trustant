// Copyright 2025-2026 Nuvolaris Inc
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func installFakeGitHubCLI(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	script := `#!/bin/sh
case "$1:$2" in
  api:user)
    if [ "${TRUSTABLE_GH_UNAUTH:-}" = "1" ]; then
      if [ -z "${TRUSTABLE_GH_AUTH_MARKER:-}" ] || [ ! -f "$TRUSTABLE_GH_AUTH_MARKER" ]; then exit 1; fi
    fi
    printf '{"login":"alice"}\n'
    ;;
  api:repos/*)
    printf '{"full_name":"alice/private-app","visibility":"private","default_branch":"trunk","clone_url":"https://github.com/alice/private-app.git"}\n'
    ;;
  api:--method)
    printf '[{"full_name":"alice/private-app","visibility":"private","default_branch":"trunk"}]\n'
    ;;
  auth:login)
    printf 'First copy your one-time code: ABCD-EFGH\n' >&2
    printf 'Open https://github.com/login/device\n' >&2
    sleep "${TRUSTABLE_GH_LOGIN_SLEEP:-0}"
    if [ -n "${TRUSTABLE_GH_AUTH_MARKER:-}" ]; then : > "$TRUSTABLE_GH_AUTH_MARKER"; fi
    if [ -n "${TRUSTABLE_GH_LOGIN_EXIT:-}" ]; then exit "$TRUSTABLE_GH_LOGIN_EXIT"; fi
    ;;
  auth:setup-git)
    if [ -n "${TRUSTABLE_GH_SETUP_FAIL_ONCE:-}" ] && [ ! -f "$TRUSTABLE_GH_SETUP_FAIL_ONCE" ]; then
      : > "$TRUSTABLE_GH_SETUP_FAIL_ONCE"
      exit 1
    fi
    cat > "$GIT_CONFIG_GLOBAL" <<EOF
[credential "https://github.com"]
	helper =
	helper = !$0 auth git-credential
EOF
    ;;
  auth:logout)
    ;;
  *)
    exit 1
    ;;
esac
`
	path := filepath.Join(binDir, "gh")
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func setManagedGitHubTestWorkspace(t *testing.T) {
	t.Helper()
	original := WorkspaceDir
	WorkspaceDir = t.TempDir()
	managedGitHubLogin.reset()
	t.Cleanup(func() {
		managedGitHubLogin.reset()
		WorkspaceDir = original
	})
}

func environmentMap(environment []string) map[string]string {
	result := map[string]string{}
	for _, entry := range environment {
		if index := strings.IndexByte(entry, '='); index >= 0 {
			result[entry[:index]] = entry[index+1:]
		}
	}
	return result
}

func TestManagedGitHubEnvironmentIsIsolated(t *testing.T) {
	setManagedGitHubTestWorkspace(t)
	t.Setenv("GH_TOKEN", "topsecret")
	t.Setenv("GITHUB_TOKEN", "othersecret")
	environment, err := managedGitHubEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	values := environmentMap(environment)
	if values["GH_TOKEN"] != "" || values["GITHUB_TOKEN"] != "" {
		t.Fatal("managed environment inherited a process GitHub token")
	}
	if !strings.HasPrefix(values["GH_CONFIG_DIR"], filepath.Join(WorkspaceDir, ".trustable", "github")) {
		t.Fatalf("GH_CONFIG_DIR is outside managed workspace: %q", values["GH_CONFIG_DIR"])
	}
	if values["HOME"] == os.Getenv("HOME") {
		t.Fatal("managed GitHub command inherited the normal user HOME")
	}
	info, err := os.Stat(values["GIT_CONFIG_GLOBAL"])
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("managed gitconfig mode = %o, want 600", info.Mode().Perm())
	}
}

func TestGitHubStatusAndRepositoryListAreSanitized(t *testing.T) {
	setManagedGitHubTestWorkspace(t)
	installFakeGitHubCLI(t)
	t.Setenv("GH_TOKEN", "must-not-appear")

	statusRequest := httptest.NewRequest(http.MethodGet, "/api/github/status", nil)
	statusRecorder := httptest.NewRecorder()
	handleGitHubStatus(statusRecorder, statusRequest)
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("status code = %d: %s", statusRecorder.Code, statusRecorder.Body.String())
	}
	if strings.Contains(statusRecorder.Body.String(), "must-not-appear") {
		t.Fatal("status response exposed a token")
	}
	var status githubAccountStatus
	if err := json.Unmarshal(statusRecorder.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Authenticated || status.Login != "alice" {
		t.Fatalf("unexpected status: %#v", status)
	}

	reposRequest := httptest.NewRequest(http.MethodGet, "/api/github/repos?page=1&per_page=50", nil)
	reposRecorder := httptest.NewRecorder()
	handleGitHubRepos(reposRecorder, reposRequest)
	if reposRecorder.Code != http.StatusOK {
		t.Fatalf("repos code = %d: %s", reposRecorder.Code, reposRecorder.Body.String())
	}
	if !strings.Contains(reposRecorder.Body.String(), `"name":"alice/private-app"`) ||
		!strings.Contains(reposRecorder.Body.String(), `"default_branch":"trunk"`) {
		t.Fatalf("unexpected repository response: %s", reposRecorder.Body.String())
	}
}

func TestGitHubLoginRejectsConcurrentProcessAndCanCancel(t *testing.T) {
	setManagedGitHubTestWorkspace(t)
	installFakeGitHubCLI(t)
	t.Setenv("TRUSTABLE_GH_UNAUTH", "1")
	t.Setenv("TRUSTABLE_GH_LOGIN_SLEEP", "5")

	first, err := managedGitHubLogin.start()
	if err != nil || first.State != "connecting" {
		t.Fatalf("first login = %#v, %v", first, err)
	}
	if _, err := managedGitHubLogin.start(); !errors.Is(err, errGitHubLoginInProgress) {
		t.Fatalf("second login error = %v, want in-progress", err)
	}
	deadline := time.Now().Add(time.Second)
	for managedGitHubLogin.snapshot().Code == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	snapshot := managedGitHubLogin.snapshot()
	if snapshot.Code != "ABCD-EFGH" || snapshot.URL != "https://github.com/login/device" {
		t.Fatalf("device metadata = %#v", snapshot)
	}
	if !managedGitHubLogin.cancelLogin() {
		t.Fatal("expected active login cancellation")
	}
}

func TestGitHubLoginStatusReconcilesAuthenticatedAccount(t *testing.T) {
	setManagedGitHubTestWorkspace(t)
	installFakeGitHubCLI(t)
	managedGitHubLogin.mu.Lock()
	managedGitHubLogin.state = "failed"
	managedGitHubLogin.deviceURL = "https://github.com/login/device"
	managedGitHubLogin.deviceCode = "ABCD-EFGH"
	managedGitHubLogin.mu.Unlock()

	request := httptest.NewRequest(http.MethodGet, "/api/github/login", nil)
	recorder := httptest.NewRecorder()
	handleGitHubLogin(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login status code = %d: %s", recorder.Code, recorder.Body.String())
	}
	var snapshot githubLoginSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.State != "connected" || snapshot.Error != "" ||
		snapshot.URL != "" || snapshot.Code != "" {
		t.Fatalf("reconciled login state = %#v", snapshot)
	}
}

func TestGitHubLoginCompletesCredentialSetupAfterNonzeroExit(t *testing.T) {
	setManagedGitHubTestWorkspace(t)
	installFakeGitHubCLI(t)
	marker := filepath.Join(t.TempDir(), "authenticated")
	t.Setenv("TRUSTABLE_GH_UNAUTH", "1")
	t.Setenv("TRUSTABLE_GH_AUTH_MARKER", marker)
	t.Setenv("TRUSTABLE_GH_LOGIN_EXIT", "1")

	first, err := managedGitHubLogin.start()
	if err != nil || first.State != "connecting" {
		t.Fatalf("login start = %#v, %v", first, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for managedGitHubLogin.snapshot().State == "connecting" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	snapshot := managedGitHubLogin.snapshot()
	if snapshot.State != "connected" {
		t.Fatalf("login state after authenticated non-zero exit = %#v", snapshot)
	}
	_, _, gitConfig, _, err := managedGitHubPaths()
	if err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(gitConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), "gh auth git-credential") {
		t.Fatalf("credential helper was not configured: %s", config)
	}
}

func TestManagedGitHubRemoteUsesAuthenticatedHTTPS(t *testing.T) {
	setManagedGitHubTestWorkspace(t)
	installFakeGitHubCLI(t)
	setupMarker := filepath.Join(t.TempDir(), "setup-attempted")
	t.Setenv("TRUSTABLE_GH_SETUP_FAIL_ONCE", setupMarker)
	remote, managed, err := managedGitHubRemoteURL("alice/private-app")
	if err != nil {
		t.Fatal(err)
	}
	if !managed || remote != "https://github.com/alice/private-app.git" {
		t.Fatalf("remote = %q managed=%v", remote, managed)
	}
	_, _, gitConfig, _, err := managedGitHubPaths()
	if err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(gitConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), "gh auth git-credential") {
		t.Fatalf("managed remote did not repair credential helper: %s", config)
	}
	if _, err := os.Stat(setupMarker); err != nil {
		t.Fatalf("credential helper setup did not exercise the retry path: %v", err)
	}
}

func TestGitHubUIContainsNoTokenSurface(t *testing.T) {
	for _, path := range []string{"web/configure.html", "web/applist.html"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		content := string(data)
		if strings.Contains(content, "gh auth token") || strings.Contains(content, "api.opencode.ai") {
			t.Fatalf("%s exposes a forbidden GitHub integration surface", path)
		}
	}
	configure, err := os.ReadFile("web/configure.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configure), `setGitHubElementVisible('gitUserCard', !status.authenticated)`) {
		t.Fatal("configure UI does not hide the Git User card for a connected managed account")
	}
}
