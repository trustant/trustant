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
	"os"
	"strings"
	"testing"
)

func TestNotebookConfigurationKeepsTokenPrivate(t *testing.T) {
	previousWorkspace := WorkspaceDir
	WorkspaceDir = t.TempDir()
	t.Cleanup(func() {
		WorkspaceDir = previousWorkspace
	})

	const token = "github-notebook-secret"
	if err := updateNotebookGitHubToken(token, false); err != nil {
		t.Fatalf("save notebook token: %v", err)
	}
	info, err := os.Stat(notebookGitHubTokenPath())
	if err != nil {
		t.Fatalf("stat notebook token: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("token mode = %o, want 600", info.Mode().Perm())
	}

	cfg, err := loadTrustableConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := normalizeNotebookConfig(cfg); err != nil {
		t.Fatalf("normalize notebook config: %v", err)
	}
	payload, err := configurationPayload(cfg)
	if err != nil {
		t.Fatalf("configuration payload: %v", err)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if strings.Contains(string(encoded), token) {
		t.Fatal("configuration response exposed the notebook token")
	}
	notebook := payload["notebook"].(map[string]interface{})
	if notebook["has_token"] != true {
		t.Fatalf("has_token = %#v, want true", notebook["has_token"])
	}

	env, err := notebookRuntimeEnvironment("")
	if err != nil {
		t.Fatalf("runtime environment: %v", err)
	}
	joined := strings.Join(env, "\n")
	for _, expected := range []string{
		"NOTEBOOK_GITHUB_REPOSITORY=trustable-ai/templates",
		"NOTEBOOK_GITHUB_REF=main",
		"NOTEBOOK_GITHUB_TOKEN=" + token,
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("runtime environment missing %q", expected)
		}
	}
}

func TestNotebookRuntimeEnvironmentUsesStarterTemplates(t *testing.T) {
	previousWorkspace := WorkspaceDir
	WorkspaceDir = t.TempDir()
	t.Cleanup(func() {
		WorkspaceDir = previousWorkspace
	})

	if err := saveWorkspaceConfig(&trustableConfig{
		Apps: map[string]*AppConfig{
			"fromstarter": {Templates: "trustable-ai/vue-templates"},
			"plainapp":    {},
		},
	}); err != nil {
		t.Fatalf("save workspace config: %v", err)
	}

	// The app created from a starter uses the starter's templates repository.
	env, err := notebookRuntimeEnvironment("fromstarter")
	if err != nil {
		t.Fatalf("runtime environment: %v", err)
	}
	if !strings.Contains(strings.Join(env, "\n"), "NOTEBOOK_GITHUB_REPOSITORY=trustable-ai/vue-templates") {
		t.Fatalf("starter templates not applied: %v", env)
	}

	// An app without a starter templates value keeps the global default, and so
	// does the global (empty app name) resolution.
	for _, app := range []string{"plainapp", "unknownapp", ""} {
		env, err := notebookRuntimeEnvironment(app)
		if err != nil {
			t.Fatalf("runtime environment for %q: %v", app, err)
		}
		if !strings.Contains(strings.Join(env, "\n"), "NOTEBOOK_GITHUB_REPOSITORY="+defaultNotebookRepository) {
			t.Fatalf("app %q did not fall back to the global default: %v", app, env)
		}
	}
}

func TestAppendEnvironmentOverridesRemovesStaleNotebookValues(t *testing.T) {
	env := appendEnvironmentOverrides(
		[]string{
			"PATH=/bin",
			"NOTEBOOK_GITHUB_TOKEN=stale",
			"NOTEBOOK_GITHUB_REF=old",
		},
		"NOTEBOOK_GITHUB_TOKEN=",
		"NOTEBOOK_GITHUB_REF=main",
	)
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "stale") || strings.Contains(joined, "REF=old") {
		t.Fatalf("stale notebook environment survived: %v", env)
	}
	if strings.Count(joined, "NOTEBOOK_GITHUB_TOKEN=") != 1 {
		t.Fatalf("token override count is not one: %v", env)
	}
}
