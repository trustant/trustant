package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalWorkbenchPathResolvesSymlinkParent(t *testing.T) {
	root := t.TempDir()
	workspaceWorkbench := filepath.Join(root, "workspace", "workbench")
	if err := os.MkdirAll(filepath.Join(workspaceWorkbench, "existing"), 0755); err != nil {
		t.Fatalf("create persistent workbench: %s", err)
	}

	link := filepath.Join(root, "workbench")
	if err := os.Symlink(workspaceWorkbench, link); err != nil {
		t.Fatalf("create workbench symlink: %s", err)
	}

	origWorkbench := WorkbenchDir
	t.Cleanup(func() { WorkbenchDir = origWorkbench })
	WorkbenchDir = link

	existing, err := canonicalWorkbenchPath("existing")
	if err != nil {
		t.Fatalf("canonical existing path: %s", err)
	}
	if want := filepath.Join(workspaceWorkbench, "existing"); existing != want {
		t.Fatalf("canonical existing path = %q, want %q", existing, want)
	}

	missing, err := canonicalWorkbenchPath("missing")
	if err != nil {
		t.Fatalf("canonical missing path: %s", err)
	}
	if want := filepath.Join(workspaceWorkbench, "missing"); missing != want {
		t.Fatalf("canonical missing path = %q, want %q", missing, want)
	}
}

func TestChooseOpenCodeSessionIDPrefersNewestActiveSession(t *testing.T) {
	sessions := []opencodeSession{
		{
			ID:    "empty-new",
			Title: "New session - 2026-07-07T06:52:03.334Z",
			Time: struct {
				Created int64 `json:"created"`
				Updated int64 `json:"updated"`
			}{Updated: 200},
		},
		{
			ID:    "active-old",
			Title: "\"Homepage E2E visible on this page\" [Headline]",
			Time: struct {
				Created int64 `json:"created"`
				Updated int64 `json:"updated"`
			}{Updated: 100},
		},
	}

	if got := chooseOpenCodeSessionID(sessions); got != "active-old" {
		t.Fatalf("chosen session = %q, want active-old", got)
	}
}

func TestIsPortListeningDetectsIPv4LoopbackListener(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on IPv4 loopback: %s", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	if !isPortListening(port) {
		t.Fatalf("expected IPv4 loopback listener on port %d to be detected", port)
	}
}
