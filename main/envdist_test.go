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
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// envDistTestApp wires WorkspaceDir/WorkbenchDir at temp locations, saves an app
// config, and returns the workbench path for the app.
func envDistTestApp(t *testing.T, name string, dev, prod map[string]string) string {
	t.Helper()

	root := t.TempDir()
	origWorkspace, origWorkbench := WorkspaceDir, WorkbenchDir
	WorkspaceDir = filepath.Join(root, "workspace")
	WorkbenchDir = filepath.Join(root, "workbench")
	t.Cleanup(func() {
		WorkspaceDir = origWorkspace
		WorkbenchDir = origWorkbench
	})

	workbenchPath := filepath.Join(WorkbenchDir, name)
	if err := os.MkdirAll(workbenchPath, 0755); err != nil {
		t.Fatalf("create workbench: %s", err)
	}

	cfg := &trustantConfig{Apps: map[string]*AppConfig{
		name: {Password: "secret-password", Development: dev, Production: prod},
	}}
	if err := saveWorkspaceConfig(cfg); err != nil {
		t.Fatalf("save workspace config: %s", err)
	}
	return workbenchPath
}

func initEnvDistRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.name", "Trustant Test"},
		{"config", "user.email", "trustant@example.test"},
		{"commit", "--allow-empty", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %s: %s", strings.Join(args, " "), err, output)
		}
	}
}

func envDistGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %s", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(output))
}

// .env.dist is a SOURCE file now, not a generated one: it declares what the app
// imports from the shared pool, and the Import tab owns it. A generator that
// rewrote it would erase the user's patterns on every launch, which is the whole
// point of this change (spec/19-import.md).
func TestGenerateAppEnvFilesLeavesEnvDistAlone(t *testing.T) {
	workbenchPath := envDistTestApp(t, "demo",
		map[string]string{"STRIPE_KEY": "sk_live_secret", "ALPHA": "a"},
		map[string]string{"SENTRY_DSN": "https://sentry.example.test"})

	// A hand-written manifest carrying a wildcard pattern.
	declared := "EXT_POSTGRESQLURL=*__POSTGRESDB\nDATABASE_PASSWORD=\n"
	distPath := filepath.Join(workbenchPath, ".env.dist")
	if err := os.WriteFile(distPath, []byte(declared), 0644); err != nil {
		t.Fatalf("write .env.dist: %s", err)
	}

	if err := generateAppEnvFiles("demo"); err != nil {
		t.Fatalf("generateAppEnvFiles: %s", err)
	}

	data, err := os.ReadFile(distPath)
	if err != nil {
		t.Fatalf("read .env.dist: %s", err)
	}
	if string(data) != declared {
		t.Fatalf(".env.dist was rewritten:\n%s\nwant\n%s", data, declared)
	}
}

// An app with no manifest must not acquire one from its configured variables.
func TestGenerateAppEnvFilesDoesNotCreateEnvDist(t *testing.T) {
	workbenchPath := envDistTestApp(t, "demo", map[string]string{"STRIPE_KEY": "sk_live"}, nil)

	if err := generateAppEnvFiles("demo"); err != nil {
		t.Fatalf("generateAppEnvFiles: %s", err)
	}
	if _, err := os.Stat(filepath.Join(workbenchPath, ".env.dist")); !os.IsNotExist(err) {
		t.Fatalf(".env.dist must not be generated, err=%v", err)
	}
}

// writeEnvDistBindings reports whether it changed anything: the flag gates the
// auto-commit, so a false positive means an empty commit on every save.
func TestWriteEnvDistBindingsReportsUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.dist")
	bindings := []EnvBinding{{Name: "ALPHA"}, {Name: "EXT_URL", Pattern: "*__POSTGRESDB"}}

	changed, err := writeEnvDistBindings(path, bindings)
	if err != nil || !changed {
		t.Fatalf("first write: changed=%v err=%v, want changed", changed, err)
	}
	changed, err = writeEnvDistBindings(path, bindings)
	if err != nil || changed {
		t.Fatalf("second write: changed=%v err=%v, want unchanged", changed, err)
	}

	// The pattern is what makes this file a source: it must survive the round trip.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %s", err)
	}
	if string(data) != "ALPHA=\nEXT_URL=*__POSTGRESDB\n" {
		t.Fatalf("serialized form = %q", data)
	}

	// Emptying it removes the file: an app that imports nothing declares nothing.
	if changed, err = writeEnvDistBindings(path, nil); err != nil || !changed {
		t.Fatalf("emptying: changed=%v err=%v, want changed", changed, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stale manifest not removed: err=%v", err)
	}
	if changed, err = writeEnvDistBindings(path, nil); err != nil || changed {
		t.Fatalf("absent manifest: changed=%v err=%v, want no change", changed, err)
	}
}

// An app cloned into the workspace but never launched has no workbench checkout.
// Saving its config must not fail.
func TestGenerateAppEnvFilesSkipsMissingWorkbench(t *testing.T) {
	root := t.TempDir()
	origWorkspace, origWorkbench := WorkspaceDir, WorkbenchDir
	WorkspaceDir = filepath.Join(root, "workspace")
	WorkbenchDir = filepath.Join(root, "workbench")
	t.Cleanup(func() {
		WorkspaceDir = origWorkspace
		WorkbenchDir = origWorkbench
	})
	if err := os.MkdirAll(WorkbenchDir, 0755); err != nil {
		t.Fatalf("create workbench root: %s", err)
	}
	if err := saveWorkspaceConfig(&trustantConfig{Apps: map[string]*AppConfig{
		"truchat": {Password: "p", Development: map[string]string{"STRIPE_KEY": "sk"}},
	}}); err != nil {
		t.Fatalf("save config: %s", err)
	}

	if err := generateAppEnvFiles("truchat"); err != nil {
		t.Fatalf("generateAppEnvFiles without a workbench checkout: %s", err)
	}
	if _, err := os.Stat(filepath.Join(WorkbenchDir, "truchat")); !os.IsNotExist(err) {
		t.Fatalf("workbench checkout must not be created here: err=%v", err)
	}

	if err := os.MkdirAll(filepath.Join(WorkbenchDir, "truchat"), 0755); err != nil {
		t.Fatalf("create checkout: %s", err)
	}
	if err := generateAppEnvFiles("truchat"); err != nil {
		t.Fatalf("generateAppEnvFiles after checkout exists: %s", err)
	}
	env, err := os.ReadFile(filepath.Join(WorkbenchDir, "truchat", ".env"))
	if err != nil {
		t.Fatalf("read .env: %s", err)
	}
	if !strings.Contains(string(env), "STRIPE_KEY=sk") || !strings.Contains(string(env), "OPS_USER=truchat") {
		t.Fatalf(".env not fully generated: %s", env)
	}
}

// The env editor holds only variables that have a value. A blank row is not an
// under-specified variable to carry along: it belongs in .env.dist, and this is
// the half of the disjunction that enforces it.
func TestPostAppConfigRejectsEmptyValue(t *testing.T) {
	envDistTestApp(t, "demo", map[string]string{"ALPHA": "a"}, nil)

	body, err := json.Marshal(map[string]interface{}{
		"vars": []EnvVar{{Name: "ALPHA", DevValue: "a"}, {Name: "BLANK"}},
	})
	if err != nil {
		t.Fatalf("marshal: %s", err)
	}
	req := httptest.NewRequest("POST", "/api/appconfig/demo", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	handlePostAppConfig(rec, req, "demo", filepath.Join(WorkspaceDir, "workspace", "demo"))

	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "BLANK") {
		t.Fatalf("error must name the offending variable: %s", rec.Body.String())
	}

	// Total refusal: the good row must not have been written either.
	cfg, err := loadWorkspaceConfig()
	if err != nil {
		t.Fatalf("load config: %s", err)
	}
	if _, ok := cfg.Apps["demo"].Development["BLANK"]; ok {
		t.Fatal("BLANK must not be stored")
	}
}

// An unresolved import legitimately arrives with no value: it is declared in
// .env.dist, so the editor must pass it through rather than refusing the save.
func TestPostAppConfigAllowsBlankImportRow(t *testing.T) {
	workbenchPath := envDistTestApp(t, "demo", map[string]string{"ALPHA": "a"}, nil)
	if err := os.WriteFile(filepath.Join(workbenchPath, ".env.dist"),
		[]byte("EXT_URL=*__POSTGRESDB\n"), 0644); err != nil {
		t.Fatalf("write .env.dist: %s", err)
	}

	body, err := json.Marshal(map[string]interface{}{
		"vars": []EnvVar{{Name: "ALPHA", DevValue: "a"}, {Name: "EXT_URL"}},
	})
	if err != nil {
		t.Fatalf("marshal: %s", err)
	}
	req := httptest.NewRequest("POST", "/api/appconfig/demo", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	handlePostAppConfig(rec, req, "demo", filepath.Join(WorkspaceDir, "workspace", "demo"))

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// A save rebuilds the development map from the posted rows. The binding is the
// variable's own value, so it travels with its row and survives — there is no
// sidecar entry for the editor to drop.
func TestPostAppConfigPreservesImportChoices(t *testing.T) {
	envDistTestApp(t, "demo", map[string]string{
		"ALPHA": "a",
	}, nil)

	body, err := json.Marshal(map[string]interface{}{
		"vars": []EnvVar{
			{Name: "ALPHA", DevValue: "a2"},
			{Name: "EXT_URL", DevValue: importRef("APPSUITE__POSTGRESDB")},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %s", err)
	}
	req := httptest.NewRequest("POST", "/api/appconfig/demo", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	handlePostAppConfig(rec, req, "demo", filepath.Join(WorkspaceDir, "workspace", "demo"))
	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	cfg, err := loadWorkspaceConfig()
	if err != nil {
		t.Fatalf("load config: %s", err)
	}
	if got := cfg.Apps["demo"].Development["EXT_URL"]; got != importRef("APPSUITE__POSTGRESDB") {
		t.Fatalf("stored value = %q, want the reference preserved", got)
	}
}
