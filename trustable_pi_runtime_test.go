package main

import (
	"crypto/tls"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBrowserVisibleDevelopmentURLUsesCallingTrustableOrigin(t *testing.T) {
	request := httptest.NewRequest("GET", "http://trustable.invalid/api/launch/example", nil)
	request.Host = "trustable.192.168.64.9.nip.io:8910"

	got, err := browserVisibleDevelopmentURL(request)
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://vite.192.168.64.9.nip.io:8910" {
		t.Fatalf("development URL = %q", got)
	}

	httpsRequest := httptest.NewRequest("GET", "https://trustable.example.test/api/launch/example", nil)
	httpsRequest.Host = "trustable.example.test"
	httpsRequest.TLS = &tls.ConnectionState{}
	got, err = browserVisibleDevelopmentURL(httpsRequest)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://vite.example.test" {
		t.Fatalf("HTTPS development URL = %q", got)
	}
}

func TestBrowserVisibleDevelopmentURLRejectsUnrelatedHost(t *testing.T) {
	request := httptest.NewRequest("GET", "http://localhost/api/launch/example", nil)
	request.Host = "localhost:8910"
	if _, err := browserVisibleDevelopmentURL(request); err == nil {
		t.Fatal("expected a non-Trustable request host to fail closed")
	}
}

func TestWriteTrustablePiRuntimeManifest(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "workbench", "example")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	mcpConfig := []byte(`{
  "mcpServers": {
    "redis": {"command": "redis-mcp"},
    "openserverless": {"command": "openserverless-mcp"}
  }
}`)
	if err := os.WriteFile(filepath.Join(projectDir, ".mcp.json"), mcpConfig, 0644); err != nil {
		t.Fatal(err)
	}
	privateMCPConfig := filepath.Join(root, "runtime", "example", "mcp.json")
	managedMCPConfigPathOverride = privateMCPConfig
	t.Cleanup(func() { managedMCPConfigPathOverride = "" })
	if err := os.MkdirAll(filepath.Dir(privateMCPConfig), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privateMCPConfig, mcpConfig, 0600); err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(root, "config", "pi-runtime.json")
	trustablePiRuntimeManifestPathOverride = manifestPath
	t.Cleanup(func() { trustablePiRuntimeManifestPathOverride = "" })
	watcherLog := filepath.Join(root, "runtime", "example", "ops-ide-devel.log")
	logWriter, err := openRotatingRuntimeLog(watcherLog)
	if err != nil {
		t.Fatal(err)
	}
	if err := logWriter.Close(); err != nil {
		t.Fatal(err)
	}

	written, err := writeTrustablePiRuntimeManifest(
		"example",
		projectDir,
		"http://vite.192.168.64.9.nip.io:8910",
		watcherLog,
	)
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
	var manifest trustablePiRuntimeManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	want := trustablePiRuntimeManifest{
		Version: trustablePiRuntimeManifestVersion,
		Workbenches: []trustablePiRuntimeWorkbench{{
			App:                "example",
			Workspace:          canonical,
			DevelopmentURL:     "http://localhost:5173",
			BrowserURL:         "http://vite.192.168.64.9.nip.io:8910",
			RequiredMCPServers: []string{"openserverless", "redis"},
			MCPConfig:          privateMCPConfig,
			WatcherLog:         watcherLog,
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

func TestWriteTrustablePiRuntimeManifestRequiresGeneratedMCPConfig(t *testing.T) {
	trustablePiRuntimeManifestPathOverride = filepath.Join(t.TempDir(), "pi-runtime.json")
	t.Cleanup(func() { trustablePiRuntimeManifestPathOverride = "" })

	if _, err := writeTrustablePiRuntimeManifest(
		"example",
		t.TempDir(),
		"http://vite.example.test",
		filepath.Join(t.TempDir(), "missing.log"),
	); err == nil {
		t.Fatal("expected missing .mcp.json to fail")
	}
}

func TestTrustablePiExtensionPathFailsClosedWhenArtifactIsMissing(t *testing.T) {
	trustablePiExtensionPathOverride = ""
	t.Setenv("HOME", t.TempDir())
	if _, err := trustablePiExtensionPath(); err == nil {
		t.Fatal("expected a missing managed extension to fail closed")
	}
}
