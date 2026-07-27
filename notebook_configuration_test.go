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

	env, err := notebookRuntimeEnvironment()
	if err != nil {
		t.Fatalf("runtime environment: %v", err)
	}
	joined := strings.Join(env, "\n")
	for _, expected := range []string{
		"NOTEBOOK_GITHUB_REPOSITORY=trustable-ai/notebooks",
		"NOTEBOOK_GITHUB_REF=main",
		"NOTEBOOK_GITHUB_TOKEN=" + token,
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("runtime environment missing %q", expected)
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
