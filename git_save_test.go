package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitSaveGeneratedPathspecsExcludeRuntimeConfig(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.name", "Trustable Test"},
		{"config", "user.email", "trustable@example.test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %s: %s", strings.Join(args, " "), err, output)
		}
	}

	files := map[string]string{
		"src.ts":                      "export const ready = true\n",
		"AGENTS.md":                   managedAppAgentsContent(),
		".mcp.json":                   `{"mcpServers":{"redis":{"env":{"REDIS_PWD":"secret"}}}}`,
		".openserverless-contract.md": "generated contract\n",
		"opencode.md":                 "generated guidance\n",
		"opencode.json":               `{"provider":{"options":{"apiKey":"secret"}}}`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %s", name, err)
		}
	}

	add := exec.Command("git", gitSaveAddArgs(dir)...)
	add.Dir = dir
	if output, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s: %s", err, output)
	}

	status := exec.Command("git", "diff", "--cached", "--name-only")
	status.Dir = dir
	output, err := status.Output()
	if err != nil {
		t.Fatalf("git diff --cached: %s", err)
	}
	if got := strings.TrimSpace(string(output)); got != "src.ts" {
		t.Fatalf("unexpected staged files: %q", got)
	}
}
