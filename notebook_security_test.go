package main

import "testing"

func TestSafeEnvLogValueRedactsNotebookToken(t *testing.T) {
	if got := safeEnvLogValue("NOTEBOOK_GITHUB_TOKEN", "github-secret"); got != "<redacted>" {
		t.Fatalf("token was not redacted: %q", got)
	}
	if got := safeEnvLogValue("WORKSPACE_DIR", "/workspace"); got != "/workspace" {
		t.Fatalf("non-secret value changed: %q", got)
	}
}
