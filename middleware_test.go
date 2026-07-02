package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func withCurrentWorkbench(t *testing.T, current string) string {
	t.Helper()
	origWorkbench := WorkbenchDir
	root := t.TempDir()
	WorkbenchDir = filepath.Join(root, "workbench")
	t.Cleanup(func() { WorkbenchDir = origWorkbench })

	currentDir := filepath.Join(WorkbenchDir, current)
	if err := os.MkdirAll(currentDir, 0755); err != nil {
		t.Fatalf("mkdir current workbench: %s", err)
	}
	if err := os.WriteFile(filepath.Join(WorkbenchDir, "current"), []byte(current), 0644); err != nil {
		t.Fatalf("write current marker: %s", err)
	}
	return currentDir
}

func TestRewriteOpenCodeDirectoryQueryToCurrentApp(t *testing.T) {
	currentDir := withCurrentWorkbench(t, "trutestdb2")
	otherDir := filepath.Join(filepath.Dir(currentDir), "truk8s")
	if err := os.MkdirAll(otherDir, 0755); err != nil {
		t.Fatalf("mkdir other workbench: %s", err)
	}

	req := httptest.NewRequest("GET", "/project/current?directory="+url.QueryEscape(otherDir), nil)
	rewriteOpenCodeDirectoryQueryToCurrent(req)

	if got := req.URL.Query().Get("directory"); got != currentDir {
		t.Fatalf("directory = %q, want %q", got, currentDir)
	}
}

func TestRewriteOpenCodeDirectoryQueryLeavesCurrentApp(t *testing.T) {
	currentDir := withCurrentWorkbench(t, "trutestdb2")

	req := httptest.NewRequest("GET", "/file?directory="+url.QueryEscape(currentDir)+"&path=.", nil)
	before := req.URL.RawQuery
	rewriteOpenCodeDirectoryQueryToCurrent(req)

	if got := req.URL.RawQuery; got != before {
		t.Fatalf("query changed to %q, want %q", got, before)
	}
}

func TestOpenCodeDirectoryUsesCanonicalWorkbenchPath(t *testing.T) {
	origWorkbench := WorkbenchDir
	root := t.TempDir()
	realWorkbench := filepath.Join(root, "workspace", "workbench")
	symlinkWorkbench := filepath.Join(root, "workbench")
	WorkbenchDir = symlinkWorkbench
	t.Cleanup(func() { WorkbenchDir = origWorkbench })

	currentRealDir := filepath.Join(realWorkbench, "trutestdb2")
	if err := os.MkdirAll(currentRealDir, 0755); err != nil {
		t.Fatalf("mkdir real workbench: %s", err)
	}
	if err := os.Symlink(realWorkbench, symlinkWorkbench); err != nil {
		t.Fatalf("symlink workbench: %s", err)
	}
	if err := os.WriteFile(filepath.Join(symlinkWorkbench, "current"), []byte("trutestdb2"), 0644); err != nil {
		t.Fatalf("write current marker: %s", err)
	}

	currentSymlinkDir := filepath.Join(symlinkWorkbench, "trutestdb2")
	req := httptest.NewRequest("GET", "/session?directory="+url.QueryEscape(currentSymlinkDir), nil)
	rewriteOpenCodeDirectoryQueryToCurrent(req)

	if got := req.URL.Query().Get("directory"); got != currentRealDir {
		t.Fatalf("directory = %q, want canonical %q", got, currentRealDir)
	}

	encoded, dir := currentOpenCodeDirectory()
	if dir != currentRealDir {
		t.Fatalf("current directory = %q, want canonical %q", dir, currentRealDir)
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(encoded); err != nil || string(decoded) != currentRealDir {
		t.Fatalf("encoded directory decodes to %q, %v; want %q", decoded, err, currentRealDir)
	}
}

func TestRedirectOpenCodeSessionCanonicalizesSymlinkRoute(t *testing.T) {
	origWorkbench := WorkbenchDir
	root := t.TempDir()
	realWorkbench := filepath.Join(root, "workspace", "workbench")
	symlinkWorkbench := filepath.Join(root, "workbench")
	WorkbenchDir = symlinkWorkbench
	t.Cleanup(func() { WorkbenchDir = origWorkbench })

	currentRealDir := filepath.Join(realWorkbench, "trutestdb2")
	if err := os.MkdirAll(currentRealDir, 0755); err != nil {
		t.Fatalf("mkdir real workbench: %s", err)
	}
	if err := os.Symlink(realWorkbench, symlinkWorkbench); err != nil {
		t.Fatalf("symlink workbench: %s", err)
	}
	if err := os.WriteFile(filepath.Join(symlinkWorkbench, "current"), []byte("trutestdb2"), 0644); err != nil {
		t.Fatalf("write current marker: %s", err)
	}

	symlinkDir := filepath.Join(symlinkWorkbench, "trutestdb2")
	symlinkEncoded := base64.RawURLEncoding.EncodeToString([]byte(symlinkDir))
	canonicalEncoded := base64.RawURLEncoding.EncodeToString([]byte(currentRealDir))
	req := httptest.NewRequest("GET", "/"+symlinkEncoded+"/session/ses_123", nil)
	rec := httptest.NewRecorder()

	if !redirectOpenCodeSession(rec, req) {
		t.Fatal("expected redirect")
	}
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTemporaryRedirect)
	}
	if got, want := rec.Header().Get("Location"), "/"+canonicalEncoded+"/session/ses_123"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}
