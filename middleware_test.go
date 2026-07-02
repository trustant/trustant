package main

import (
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestRewriteOpenCodeHomeDirectorySearchToWorkbench(t *testing.T) {
	origHome := os.Getenv("HOME")
	origWorkbench := WorkbenchDir
	t.Cleanup(func() {
		os.Setenv("HOME", origHome)
		WorkbenchDir = origWorkbench
	})

	home := filepath.Join(t.TempDir(), "home")
	workbench := filepath.Join(home, "workbench")
	os.Setenv("HOME", home)
	WorkbenchDir = workbench

	req := httptest.NewRequest("GET", "/find/file?directory="+url.QueryEscape(home)+"&query=tru&type=directory", nil)
	rewriteOpenCodeHomeDirectoryRequest(req)

	if got := req.URL.Query().Get("directory"); got != workbench {
		t.Fatalf("directory = %q, want %q", got, workbench)
	}
}

func TestRewriteOpenCodeHomeDirectoryFileWorkbenchToWorkbenchRoot(t *testing.T) {
	origHome := os.Getenv("HOME")
	origWorkbench := WorkbenchDir
	t.Cleanup(func() {
		os.Setenv("HOME", origHome)
		WorkbenchDir = origWorkbench
	})

	home := filepath.Join(t.TempDir(), "home")
	workbench := filepath.Join(home, "workbench")
	os.Setenv("HOME", home)
	WorkbenchDir = workbench

	req := httptest.NewRequest("GET", "/file?directory="+url.QueryEscape(home)+"&path=workbench", nil)
	rewriteOpenCodeHomeDirectoryRequest(req)

	q := req.URL.Query()
	if got := q.Get("directory"); got != workbench {
		t.Fatalf("directory = %q, want %q", got, workbench)
	}
	if got := q.Get("path"); got != "." {
		t.Fatalf("path = %q, want .", got)
	}
}

func TestRewriteOpenCodeHomeDirectoryIgnoresOtherDirectories(t *testing.T) {
	origHome := os.Getenv("HOME")
	origWorkbench := WorkbenchDir
	t.Cleanup(func() {
		os.Setenv("HOME", origHome)
		WorkbenchDir = origWorkbench
	})

	home := filepath.Join(t.TempDir(), "home")
	other := filepath.Join(t.TempDir(), "other")
	os.Setenv("HOME", home)
	WorkbenchDir = filepath.Join(home, "workbench")

	req := httptest.NewRequest("GET", "/find/file?directory="+url.QueryEscape(other)+"&query=tru&type=directory", nil)
	rewriteOpenCodeHomeDirectoryRequest(req)

	if got := req.URL.Query().Get("directory"); got != other {
		t.Fatalf("directory = %q, want %q", got, other)
	}
}
