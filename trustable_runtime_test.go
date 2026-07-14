package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWriteTrustableRuntimeManifest(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "workbench", "example")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	config := []byte(`{
  "mcp": {
    "redis": {"enabled": true},
    "disabled": {"enabled": false},
    "browser": {"enabled": true},
    "openserverless": {"type": "local"}
  }
}`)
	if err := os.WriteFile(filepath.Join(projectDir, "opencode.json"), config, 0644); err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(root, "config", "runtime.json")
	trustableRuntimeManifestPathOverride = manifestPath
	t.Cleanup(func() { trustableRuntimeManifestPathOverride = "" })

	written, err := writeTrustableRuntimeManifest("example", projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if written != manifestPath {
		t.Fatalf("manifest path = %q, want %q", written, manifestPath)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest trustableRuntimeManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	want := trustableRuntimeManifest{
		Version: 1,
		Workbenches: []trustableRuntimeWorkbench{{
			App:                "example",
			Workspace:          canonical,
			DevelopmentURL:     "http://localhost:5173",
			RequiredMCPServers: []string{"browser", "openserverless", "redis"},
		}},
	}
	if !reflect.DeepEqual(manifest, want) {
		t.Fatalf("manifest = %#v, want %#v", manifest, want)
	}
	info, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("manifest mode = %o, want 600", info.Mode().Perm())
	}
}

func TestWriteTrustableRuntimeManifestRequiresGeneratedConfig(t *testing.T) {
	projectDir := t.TempDir()
	trustableRuntimeManifestPathOverride = filepath.Join(t.TempDir(), "runtime.json")
	t.Cleanup(func() { trustableRuntimeManifestPathOverride = "" })

	if _, err := writeTrustableRuntimeManifest("example", projectDir); err == nil {
		t.Fatal("expected missing opencode.json to fail")
	}
}

func TestFileContainsTrustableRuntimeMarkerAcrossReadBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode")
	prefix := make([]byte, 64*1024-5)
	data := append(prefix, []byte(trustableCodeRuntimeMarker)...)
	if err := os.WriteFile(path, data, 0755); err != nil {
		t.Fatal(err)
	}

	present, err := fileContainsMarker(path, trustableCodeRuntimeMarker)
	if err != nil {
		t.Fatal(err)
	}
	if !present {
		t.Fatal("expected Trustable runtime marker")
	}
}

func TestFileContainsTrustableRuntimeMarkerRejectsUpstreamBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode")
	if err := os.WriteFile(path, []byte("upstream opencode"), 0755); err != nil {
		t.Fatal(err)
	}

	present, err := fileContainsMarker(path, trustableCodeRuntimeMarker)
	if err != nil {
		t.Fatal(err)
	}
	if present {
		t.Fatal("upstream binary unexpectedly matched Trustable runtime marker")
	}
}
