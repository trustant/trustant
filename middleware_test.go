package main

import (
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
