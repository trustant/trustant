package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedMCPConfigsKeepCredentialsOutsideWorkbench(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "workbench", "example")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(root, "runtime", "example", "mcp.json")
	launcherPath := filepath.Join(root, "bin", "trustable-mcp-launch")
	managedMCPConfigPathOverride = privatePath
	managedMCPLauncherInstallPathOverride = launcherPath
	t.Cleanup(func() {
		managedMCPConfigPathOverride = ""
		managedMCPLauncherInstallPathOverride = ""
	})

	const sentinel = "redis-password-sentinel"
	mcp := map[string]interface{}{
		"redis": map[string]interface{}{
			"type":    "local",
			"command": []string{"redis-mcp-server", "--password", sentinel},
			"environment": map[string]string{
				"REDIS_PWD": sentinel,
			},
		},
		"browser": map[string]interface{}{
			"type":    "local",
			"command": []string{"trustable-browser-mcp"},
		},
	}
	written, err := writeManagedMCPConfigs(projectDir, mcp)
	if err != nil {
		t.Fatal(err)
	}
	if written != privatePath {
		t.Fatalf("private path = %q, want %q", written, privatePath)
	}

	publicData, err := os.ReadFile(filepath.Join(projectDir, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicData), sentinel) {
		t.Fatal("credential leaked into model-readable .mcp.json")
	}
	var publicConfig struct {
		MCPServers map[string]map[string]interface{} `json:"mcpServers"`
	}
	if err := json.Unmarshal(publicData, &publicConfig); err != nil {
		t.Fatal(err)
	}
	if publicConfig.MCPServers["redis"]["command"] != "trustable-mcp-launch" {
		t.Fatalf("credential-bearing server did not use managed launcher: %#v", publicConfig.MCPServers["redis"])
	}

	privateData, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(privateData), sentinel) {
		t.Fatal("private config lost the server credential")
	}
	info, err := os.Stat(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("private config mode = %o, want 600", info.Mode().Perm())
	}
	launcherInfo, err := os.Stat(launcherPath)
	if err != nil {
		t.Fatal(err)
	}
	if launcherInfo.Mode().Perm() != 0755 {
		t.Fatalf("launcher mode = %o, want 755", launcherInfo.Mode().Perm())
	}
}
