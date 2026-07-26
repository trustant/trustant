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
	wantExisting, err := filepath.EvalSymlinks(filepath.Join(workspaceWorkbench, "existing"))
	if err != nil {
		t.Fatalf("resolve expected existing path: %s", err)
	}
	if existing != wantExisting {
		t.Fatalf("canonical existing path = %q, want %q", existing, wantExisting)
	}

	missing, err := canonicalWorkbenchPath("missing")
	if err != nil {
		t.Fatalf("canonical missing path: %s", err)
	}
	canonicalParent, err := filepath.EvalSymlinks(workspaceWorkbench)
	if err != nil {
		t.Fatalf("resolve expected missing parent: %s", err)
	}
	if want := filepath.Join(canonicalParent, "missing"); missing != want {
		t.Fatalf("canonical missing path = %q, want %q", missing, want)
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
