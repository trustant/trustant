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

	cfg := &trustableConfig{Apps: map[string]*AppConfig{
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
		{"config", "user.name", "Trustable Test"},
		{"config", "user.email", "trustable@example.test"},
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

// .env.dist declares only what the user must supply: every custom dev and prod
// name, sorted, never a value — it is pushed to git, so a copied secret would
// leak. The server-supplied OPS_* keys are excluded entirely.
func TestEnvDistListsOnlyUserSuppliedKeys(t *testing.T) {
	workbenchPath := envDistTestApp(t, "demo",
		map[string]string{"STRIPE_KEY": "sk_live_secret", "ALPHA": "a", "MONGODB_URI": "mongodb://runtime"},
		map[string]string{"SENTRY_DSN": "https://sentry.example.test", "ALPHA": "a-prod"})

	if err := generateAppEnvFiles("demo"); err != nil {
		t.Fatalf("generateAppEnvFiles: %s", err)
	}

	data, err := os.ReadFile(filepath.Join(workbenchPath, ".env.dist"))
	if err != nil {
		t.Fatalf("read .env.dist: %s", err)
	}
	content := string(data)

	want := "ALPHA=\nSENTRY_DSN=\nSTRIPE_KEY=\n"
	if content != want {
		t.Fatalf(".env.dist =\n%s\nwant\n%s", content, want)
	}
	// The server supplies these on every launch; listing them would state a
	// requirement the user is never asked to satisfy.
	for _, fixed := range envDistFixedKeys {
		if strings.Contains(content, fixed) {
			t.Fatalf(".env.dist must not list the server-supplied key %s: %s", fixed, content)
		}
	}
	// Service runtime credentials are excluded everywhere, .env.dist included.
	if strings.Contains(content, "MONGODB_URI") {
		t.Fatalf(".env.dist must not list MONGODB_URI: %s", content)
	}
	for _, secret := range []string{"sk_live_secret", "secret-password", "sentry.example.test"} {
		if strings.Contains(content, secret) {
			t.Fatalf(".env.dist leaked a value (%s): %s", secret, content)
		}
	}
}

// An app that requires nothing of the user declares no contract: no manifest is
// written, and a stale one from an earlier config is removed.
func TestEnvDistAbsentWhenOnlyServerSuppliedKeys(t *testing.T) {
	workbenchPath := envDistTestApp(t, "demo", map[string]string{"STRIPE_KEY": "sk_live"}, nil)
	distPath := filepath.Join(workbenchPath, ".env.dist")

	if err := generateAppEnvFiles("demo"); err != nil {
		t.Fatalf("generateAppEnvFiles: %s", err)
	}
	if _, err := os.Stat(distPath); err != nil {
		t.Fatalf("manifest should exist while a custom key is configured: %s", err)
	}

	// The user removes their only custom variable.
	cfg, err := loadWorkspaceConfig()
	if err != nil {
		t.Fatalf("load config: %s", err)
	}
	cfg.Apps["demo"].Development = map[string]string{}
	if err := saveWorkspaceConfig(cfg); err != nil {
		t.Fatalf("save config: %s", err)
	}

	changed, err := writeEnvDistFile(distPath, appEnvVarNames(cfg.Apps["demo"]))
	if err != nil {
		t.Fatalf("writeEnvDistFile: %s", err)
	}
	if !changed {
		t.Fatal("removing the last custom key must report a change so it gets committed")
	}
	if _, err := os.Stat(distPath); !os.IsNotExist(err) {
		t.Fatalf("stale manifest not removed: err=%v", err)
	}
	// Already absent: nothing to do, and no commit to trigger.
	if changed, err = writeEnvDistFile(distPath, nil); err != nil || changed {
		t.Fatalf("absent manifest: changed=%v err=%v, want no change", changed, err)
	}
}

// An unchanged manifest must not be rewritten: the changed flag is what gates
// the auto-commit, so a false positive means an empty commit on every launch.
func TestWriteEnvDistFileReportsUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.dist")
	names := []string{"ALPHA", "STRIPE_KEY"}

	changed, err := writeEnvDistFile(path, names)
	if err != nil || !changed {
		t.Fatalf("first write: changed=%v err=%v, want changed", changed, err)
	}
	changed, err = writeEnvDistFile(path, names)
	if err != nil || changed {
		t.Fatalf("second write: changed=%v err=%v, want unchanged", changed, err)
	}
	changed, err = writeEnvDistFile(path, append(names, "SENTRY_DSN"))
	if err != nil || !changed {
		t.Fatalf("write after key added: changed=%v err=%v, want changed", changed, err)
	}
}

// The manifest is committed by the server as soon as it changes, and the commit
// contains that one file — never whatever else the user has dirty.
func TestGenerateAppEnvFilesCommitsEnvDistAlone(t *testing.T) {
	workbenchPath := envDistTestApp(t, "demo", map[string]string{"STRIPE_KEY": "sk_live"}, nil)
	initEnvDistRepo(t, workbenchPath)
	if _, err := ensureManagedGitignore(workbenchPath); err != nil {
		t.Fatalf("ensureManagedGitignore: %s", err)
	}

	// An unrelated dirty file: it must stay out of the .env.dist commit.
	if err := os.WriteFile(filepath.Join(workbenchPath, "README.md"), []byte("work in progress\n"), 0644); err != nil {
		t.Fatalf("write README: %s", err)
	}

	if err := generateAppEnvFiles("demo"); err != nil {
		t.Fatalf("generateAppEnvFiles: %s", err)
	}

	touched := envDistGitOutput(t, workbenchPath, "log", "-1", "--name-only", "--pretty=format:")
	if touched != ".env.dist" {
		t.Fatalf("commit touched %q, want only .env.dist", touched)
	}
	if status := envDistGitOutput(t, workbenchPath, "status", "--porcelain", "--", ".env.dist"); status != "" {
		t.Fatalf(".env.dist still dirty after generate: %q", status)
	}
	// The generated secrets stay ignored; only the manifest is tracked.
	if status := envDistGitOutput(t, workbenchPath, "status", "--porcelain", "--", ".env"); status != "" {
		t.Fatalf(".env must be ignored, got %q", status)
	}
	if untracked := envDistGitOutput(t, workbenchPath, "status", "--porcelain", "--", "README.md"); untracked == "" {
		t.Fatal("README.md should still be uncommitted; the commit was not scoped")
	}
}

// Regenerating with the same config must not add a commit per launch.
func TestGenerateAppEnvFilesDoesNotRecommitUnchangedEnvDist(t *testing.T) {
	workbenchPath := envDistTestApp(t, "demo", map[string]string{"STRIPE_KEY": "sk_live"}, nil)
	initEnvDistRepo(t, workbenchPath)

	if err := generateAppEnvFiles("demo"); err != nil {
		t.Fatalf("first generateAppEnvFiles: %s", err)
	}
	first := envDistGitOutput(t, workbenchPath, "rev-list", "--count", "HEAD")

	if err := generateAppEnvFiles("demo"); err != nil {
		t.Fatalf("second generateAppEnvFiles: %s", err)
	}
	if second := envDistGitOutput(t, workbenchPath, "rev-list", "--count", "HEAD"); second != first {
		t.Fatalf("commit count went %s -> %s, want no new commit", first, second)
	}
}

// Committing is best-effort: a workbench that is not a git repository must still
// get its .env.dist, and generation must not fail.
func TestGenerateAppEnvFilesEnvDistOutsideGitRepo(t *testing.T) {
	workbenchPath := envDistTestApp(t, "demo", map[string]string{"STRIPE_KEY": "sk_live"}, nil)

	if err := generateAppEnvFiles("demo"); err != nil {
		t.Fatalf("generateAppEnvFiles outside a repo must not fail: %s", err)
	}
	if _, err := os.Stat(filepath.Join(workbenchPath, ".env.dist")); err != nil {
		t.Fatalf(".env.dist not written: %s", err)
	}
}

// An app cloned into the workspace but never launched has no workbench checkout.
// Saving its config must not fail: the missing-variable flow sends the user to
// the env editor in exactly that state, and launch regenerates the files as soon
// as it clones workspace → workbench.
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
	if err := saveWorkspaceConfig(&trustableConfig{Apps: map[string]*AppConfig{
		"truchat": {Password: "p", Development: map[string]string{"STRIPE_KEY": "sk"}},
	}}); err != nil {
		t.Fatalf("save config: %s", err)
	}

	if err := generateAppEnvFiles("truchat"); err != nil {
		t.Fatalf("generateAppEnvFiles without a workbench checkout: %s", err)
	}
	// Nothing was created outside the checkout that does not exist.
	if _, err := os.Stat(filepath.Join(WorkbenchDir, "truchat")); !os.IsNotExist(err) {
		t.Fatalf("workbench checkout must not be created here: err=%v", err)
	}

	// Once the checkout exists (launch cloned it), generation proceeds normally.
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

// Missing = declared in .env.dist but with no development value.
//
// The manifest here deliberately lists OPS_* and MONGODB_URI even though the
// generator no longer emits them: a repo cloned from elsewhere may carry a
// hand-written or pre-existing .env.dist that does, and those keys must still
// never be treated as required — the server supplies them on every launch.
func TestMissingAppEnvKeys(t *testing.T) {
	workbenchPath := envDistTestApp(t, "demo", map[string]string{
		"STRIPE_KEY": "",
		"ALPHA":      "set",
		"SPACES":     "   ",
	}, nil)

	manifest := "OPS_USER=\nOPS_PASSWORD=\nALPHA=\nSTRIPE_KEY=\nSENTRY_DSN=\nSPACES=\nMONGODB_URI=\n"
	if err := os.WriteFile(filepath.Join(workbenchPath, ".env.dist"), []byte(manifest), 0644); err != nil {
		t.Fatalf("write .env.dist: %s", err)
	}

	missing, err := missingAppEnvKeys("demo")
	if err != nil {
		t.Fatalf("missingAppEnvKeys: %s", err)
	}
	// .env.dist order, empty/whitespace/absent values only.
	want := []string{"STRIPE_KEY", "SENTRY_DSN", "SPACES"}
	if strings.Join(missing, ",") != strings.Join(want, ",") {
		t.Fatalf("missing = %v, want %v", missing, want)
	}
}

func TestMissingAppEnvKeysWithoutManifest(t *testing.T) {
	envDistTestApp(t, "demo", map[string]string{"STRIPE_KEY": ""}, nil)

	// Nothing to compare against: an app with no .env.dist declares no contract.
	missing, err := missingAppEnvKeys("demo")
	if err != nil {
		t.Fatalf("missingAppEnvKeys: %s", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none", missing)
	}
}

// Seeding is what turns a declared-but-unset variable into a visible blank row
// in the env editor. As above, the manifest lists server-supplied keys on
// purpose: a foreign .env.dist may, and they must not be seeded into the
// editable config where they would shadow the generated values.
func TestSeedMissingEnvKeys(t *testing.T) {
	workbenchPath := envDistTestApp(t, "demo", map[string]string{"ALPHA": "set"}, nil)

	manifest := "OPS_USER=\nALPHA=\nSTRIPE_KEY=\nMONGODB_URI=\n"
	if err := os.WriteFile(filepath.Join(workbenchPath, ".env.dist"), []byte(manifest), 0644); err != nil {
		t.Fatalf("write .env.dist: %s", err)
	}

	seeded, err := seedMissingEnvKeys("demo")
	if err != nil {
		t.Fatalf("seedMissingEnvKeys: %s", err)
	}
	if strings.Join(seeded, ",") != "STRIPE_KEY" {
		t.Fatalf("seeded = %v, want [STRIPE_KEY]", seeded)
	}

	cfg, err := loadTrustableConfig()
	if err != nil {
		t.Fatalf("load config: %s", err)
	}
	dev := cfg.Apps["demo"].Development
	if v, ok := dev["STRIPE_KEY"]; !ok || v != "" {
		t.Fatalf("STRIPE_KEY = %q (present=%v), want seeded empty", v, ok)
	}
	if dev["ALPHA"] != "set" {
		t.Fatalf("seeding overwrote an existing value: ALPHA=%q", dev["ALPHA"])
	}
	// Fixed and service-runtime keys are supplied by the server, never seeded.
	for _, key := range []string{"OPS_USER", "MONGODB_URI"} {
		if _, ok := dev[key]; ok {
			t.Fatalf("%s must not be seeded into development config", key)
		}
	}

	// Idempotent: a second pass has nothing left to add.
	again, err := seedMissingEnvKeys("demo")
	if err != nil {
		t.Fatalf("second seedMissingEnvKeys: %s", err)
	}
	if len(again) != 0 {
		t.Fatalf("second seed added %v, want none", again)
	}
}

// A partial save must not delete a required variable the user has not filled in
// yet, otherwise it disappears from the editor and stops being reported missing.
func TestPostAppConfigPreservesEmptyValuedKeys(t *testing.T) {
	envDistTestApp(t, "demo", map[string]string{"STRIPE_KEY": ""}, nil)

	body := `{"vars":[
		{"name":"STRIPE_KEY","dev_value":"","prod_value":""},
		{"name":"SENTRY_DSN","dev_value":"","prod_value":"https://prod.example.test"},
		{"name":"ALPHA","dev_value":"a","prod_value":""},
		{"name":"","dev_value":"ignored","prod_value":""}
	]}`
	req := httptest.NewRequest("POST", "/api/appconfig/demo", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handlePostAppConfig(rec, req, "demo", filepath.Join(WorkspaceDir, "workspace", "demo"))

	if rec.Code != 200 {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	cfg, err := loadTrustableConfig()
	if err != nil {
		t.Fatalf("load config: %s", err)
	}
	dev := cfg.Apps["demo"].Development
	for _, key := range []string{"STRIPE_KEY", "SENTRY_DSN"} {
		if v, ok := dev[key]; !ok || v != "" {
			t.Fatalf("%s = %q (present=%v), want preserved as empty", key, v, ok)
		}
	}
	if dev["ALPHA"] != "a" {
		t.Fatalf("ALPHA = %q, want a", dev["ALPHA"])
	}
	// An unnamed row is not a variable.
	if _, ok := dev[""]; ok {
		t.Fatal("empty variable name must not be stored")
	}
	// The empty rows still count as missing, which is what keeps the gate closed.
	missingRoundTrip, err := json.Marshal(dev)
	if err != nil {
		t.Fatalf("marshal dev: %s", err)
	}
	if !strings.Contains(string(missingRoundTrip), `"STRIPE_KEY":""`) {
		t.Fatalf("dev config lost the empty key: %s", missingRoundTrip)
	}
}
