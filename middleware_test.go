package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func withSymlinkedCurrentWorkbench(t *testing.T, current string) (realDir, symlinkDir string) {
	t.Helper()
	origWorkbench := WorkbenchDir
	root := t.TempDir()
	realWorkbench := filepath.Join(root, "workspace", "workbench")
	symlinkWorkbench := filepath.Join(root, "workbench")
	WorkbenchDir = symlinkWorkbench
	t.Cleanup(func() { WorkbenchDir = origWorkbench })

	realDir = filepath.Join(realWorkbench, current)
	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatalf("mkdir real workbench: %s", err)
	}
	if err := os.Symlink(realWorkbench, symlinkWorkbench); err != nil {
		t.Fatalf("symlink workbench: %s", err)
	}
	if err := os.WriteFile(filepath.Join(symlinkWorkbench, "current"), []byte(current), 0644); err != nil {
		t.Fatalf("write current marker: %s", err)
	}
	return realDir, filepath.Join(symlinkWorkbench, current)
}

func TestOpenCodeDirectoryUsesCanonicalWorkbenchPath(t *testing.T) {
	realDir, _ := withSymlinkedCurrentWorkbench(t, "trutestdb2")

	encoded, dir := currentOpenCodeDirectory()
	if dir != realDir {
		t.Fatalf("current directory = %q, want canonical %q", dir, realDir)
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(encoded); err != nil || string(decoded) != realDir {
		t.Fatalf("encoded directory decodes to %q, %v; want %q", decoded, err, realDir)
	}
}

func TestRedirectOpenCodeSessionCanonicalizesSymlinkRoute(t *testing.T) {
	realDir, symlinkDir := withSymlinkedCurrentWorkbench(t, "trutestdb2")
	symlinkEncoded := base64.RawURLEncoding.EncodeToString([]byte(symlinkDir))
	canonicalEncoded := base64.RawURLEncoding.EncodeToString([]byte(realDir))
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
