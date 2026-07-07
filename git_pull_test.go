package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(output))
	}
	return strings.TrimSpace(string(output))
}

func testCommitFile(t *testing.T, repo, name, content, message string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	testGit(t, repo, "add", name)
	testGit(t, repo, "commit", "-m", message)
	return testGit(t, repo, "rev-parse", "HEAD")
}

func testRemoteRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	testGit(t, repo, "init", "-b", "main")
	testGit(t, repo, "config", "user.email", "test@example.com")
	testGit(t, repo, "config", "user.name", "Test User")
	testCommitFile(t, repo, "README.md", "initial\n", "initial")
	return repo
}

func testSetWorkspaceDirs(t *testing.T) (string, string) {
	t.Helper()
	origWorkspace := WorkspaceDir
	origWorkbench := WorkbenchDir
	workspace := t.TempDir()
	workbench := t.TempDir()
	WorkspaceDir = workspace
	WorkbenchDir = workbench
	t.Cleanup(func() {
		WorkspaceDir = origWorkspace
		WorkbenchDir = origWorkbench
	})
	if err := os.MkdirAll(filepath.Join(workspace, "workspace"), 0755); err != nil {
		t.Fatal(err)
	}
	return workspace, workbench
}

func testCloneBareWorkspace(t *testing.T, remote, workspace, name string) string {
	t.Helper()
	path := filepath.Join(workspace, "workspace", name)
	testGit(t, "", "clone", "--bare", remote, path)
	return path
}

func testGitPullRequest(t *testing.T, name string) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	body := bytes.NewBufferString(`{"name":"` + name + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/git/pull", body)
	rec := httptest.NewRecorder()
	handleGitPull(rec, req)
	var payload map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON response: %s", rec.Body.String())
	}
	return rec, payload
}

func TestGitPullUpdatesBareWorkspaceFromRemote(t *testing.T) {
	workspace, _ := testSetWorkspaceDirs(t)
	remote := testRemoteRepo(t)
	app := "apppull"
	bare := testCloneBareWorkspace(t, remote, workspace, app)
	remoteHead := testCommitFile(t, remote, "README.md", "remote update\n", "remote update")

	rec, payload := testGitPullRequest(t, app)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %#v", rec.Code, payload)
	}
	if payload["updated"] != true {
		t.Fatalf("expected updated=true, got %#v", payload)
	}
	if got := testGit(t, bare, "rev-parse", "refs/heads/main"); got != remoteHead {
		t.Fatalf("workspace main = %s, want %s", got, remoteHead)
	}
}

func TestGitPullRejectsDirtyWorkbench(t *testing.T) {
	workspace, workbench := testSetWorkspaceDirs(t)
	remote := testRemoteRepo(t)
	app := "dirtyapp"
	bare := testCloneBareWorkspace(t, remote, workspace, app)
	workbenchPath := filepath.Join(workbench, app)
	testGit(t, "", "clone", bare, workbenchPath)
	if err := os.WriteFile(filepath.Join(workbenchPath, "local.txt"), []byte("dirty\n"), 0644); err != nil {
		t.Fatal(err)
	}
	testCommitFile(t, remote, "README.md", "remote update\n", "remote update")

	rec, payload := testGitPullRequest(t, app)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %#v", rec.Code, payload)
	}
	if !strings.Contains(payload["error"].(string), "unsaved changes") {
		t.Fatalf("unexpected error: %#v", payload)
	}
}

func TestGitPullCleansGeneratedWorkbenchFiles(t *testing.T) {
	workspace, workbench := testSetWorkspaceDirs(t)
	remote := testRemoteRepo(t)
	testCommitFile(t, remote, ".openserverless-contract.md", "tracked\n", "tracked generated file")
	app := "generatedapp"
	bare := testCloneBareWorkspace(t, remote, workspace, app)
	workbenchPath := filepath.Join(workbench, app)
	testGit(t, "", "clone", bare, workbenchPath)
	if err := os.WriteFile(filepath.Join(workbenchPath, ".openserverless-contract.md"), []byte("generated update\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workbenchPath, ".mcp.json"), []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workbenchPath, "AGENTS.md"), []byte(managedAppAgentsContent()), 0644); err != nil {
		t.Fatal(err)
	}

	rec, payload := testGitPullRequest(t, app)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %#v", rec.Code, payload)
	}
	if status := testGit(t, workbenchPath, "status", "--porcelain"); status != "" {
		t.Fatalf("expected generated files to be cleaned, got status %q", status)
	}
}

func TestGitPullRejectsAgentsWithLocalNotes(t *testing.T) {
	workspace, workbench := testSetWorkspaceDirs(t)
	remote := testRemoteRepo(t)
	app := "agentsnotes"
	bare := testCloneBareWorkspace(t, remote, workspace, app)
	workbenchPath := filepath.Join(workbench, app)
	testGit(t, "", "clone", bare, workbenchPath)
	agents := managedAppAgentsContent() + "\n## App-local notes\n\nkeep this\n"
	if err := os.WriteFile(filepath.Join(workbenchPath, "AGENTS.md"), []byte(agents), 0644); err != nil {
		t.Fatal(err)
	}

	rec, payload := testGitPullRequest(t, app)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %#v", rec.Code, payload)
	}
	if !strings.Contains(payload["output"].(string), "AGENTS.md") {
		t.Fatalf("expected AGENTS.md in dirty output, got %#v", payload)
	}
}

func TestGitPullFastForwardsWorkbenchAndRefreshesOps(t *testing.T) {
	workspace, workbench := testSetWorkspaceDirs(t)
	remote := testRemoteRepo(t)
	app := "ffpull"
	bare := testCloneBareWorkspace(t, remote, workspace, app)
	workbenchPath := filepath.Join(workbench, app)
	testGit(t, "", "clone", bare, workbenchPath)
	remoteHead := testCommitFile(t, remote, "README.md", "remote update\n", "remote update")

	binDir := t.TempDir()
	opsLog := filepath.Join(t.TempDir(), "ops.log")
	opsPath := filepath.Join(binDir, "ops")
	if err := os.WriteFile(opsPath, []byte("#!/bin/sh\necho \"$@\" >> \"$OPS_LOG\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPS_LOG", opsLog)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	rec, payload := testGitPullRequest(t, app)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %#v", rec.Code, payload)
	}
	if payload["updated"] != true || payload["workbench_updated"] != true {
		t.Fatalf("expected workspace and workbench update, got %#v", payload)
	}
	if got := testGit(t, workbenchPath, "rev-parse", "HEAD"); got != remoteHead {
		t.Fatalf("workbench HEAD = %s, want %s", got, remoteHead)
	}
	logData, err := os.ReadFile(opsLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "ide clean") || !strings.Contains(string(logData), "ide deploy") {
		t.Fatalf("expected ops ide clean/deploy, got %q", string(logData))
	}
}
