package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func initBareRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir repo parent: %s", err)
	}
	cmd := exec.Command("git", "init", "--bare", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %s output=%s", err, string(output))
	}
}

func TestRestoreMissingWorkbenchCheckoutsClonesWithoutOverwritingExisting(t *testing.T) {
	origWorkspace := WorkspaceDir
	origWorkbench := WorkbenchDir
	t.Cleanup(func() {
		WorkspaceDir = origWorkspace
		WorkbenchDir = origWorkbench
	})

	root := t.TempDir()
	WorkspaceDir = filepath.Join(root, "workspace-root")
	WorkbenchDir = filepath.Join(root, "workbench")

	initBareRepo(t, filepath.Join(WorkspaceDir, "workspace", "truk8s"))
	initBareRepo(t, filepath.Join(WorkspaceDir, "workspace", "truold"))

	existing := filepath.Join(WorkbenchDir, "truold")
	if err := os.MkdirAll(existing, 0755); err != nil {
		t.Fatalf("mkdir existing workbench: %s", err)
	}
	if err := os.WriteFile(filepath.Join(existing, "dirty.txt"), []byte("keep me\n"), 0644); err != nil {
		t.Fatalf("write dirty file: %s", err)
	}

	restoreMissingWorkbenchCheckouts()

	if _, err := os.Stat(filepath.Join(WorkbenchDir, "truk8s", ".git")); err != nil {
		t.Fatalf("expected missing workbench to be cloned: %s", err)
	}
	data, err := os.ReadFile(filepath.Join(existing, "dirty.txt"))
	if err != nil {
		t.Fatalf("expected existing workbench to be preserved: %s", err)
	}
	if string(data) != "keep me\n" {
		t.Fatalf("existing workbench was overwritten, got %q", string(data))
	}
}
