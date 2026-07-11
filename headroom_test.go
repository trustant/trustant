package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func withWorkspaceDir(t *testing.T, dir string) {
	t.Helper()
	old := WorkspaceDir
	WorkspaceDir = dir
	t.Cleanup(func() {
		WorkspaceDir = old
	})
}

func TestHeadroomConfigDefaultsDisabled(t *testing.T) {
	withWorkspaceDir(t, t.TempDir())

	cfg, err := headroomConfigFromEnv()
	if err != nil {
		t.Fatalf("headroomConfigFromEnv returned error: %v", err)
	}
	if cfg.Enabled {
		t.Fatalf("Headroom should be disabled by default")
	}
	if cfg.Mode != "proxy" {
		t.Fatalf("default mode = %q, want proxy", cfg.Mode)
	}
	if cfg.Port != defaultHeadroomPort {
		t.Fatalf("default port = %d, want %d", cfg.Port, defaultHeadroomPort)
	}
	wantState := filepath.Join(WorkspaceDir, ".trustable", "headroom")
	if cfg.StateDir != wantState {
		t.Fatalf("default state dir = %q, want %q", cfg.StateDir, wantState)
	}
}

func TestHeadroomConfigEnabledCustomValues(t *testing.T) {
	withWorkspaceDir(t, t.TempDir())
	t.Setenv("TRUSTABLE_HEADROOM_ENABLED", "yes")
	t.Setenv("TRUSTABLE_HEADROOM_MODE", "proxy")
	t.Setenv("TRUSTABLE_HEADROOM_PORT", "18787")
	t.Setenv("TRUSTABLE_HEADROOM_STATE_DIR", "/tmp/headroom-state")

	cfg, err := headroomConfigFromEnv()
	if err != nil {
		t.Fatalf("headroomConfigFromEnv returned error: %v", err)
	}
	if !cfg.Enabled {
		t.Fatalf("Headroom should be enabled")
	}
	if cfg.Mode != "proxy" {
		t.Fatalf("mode = %q, want proxy", cfg.Mode)
	}
	if cfg.Port != 18787 {
		t.Fatalf("port = %d, want 18787", cfg.Port)
	}
	if cfg.StateDir != "/tmp/headroom-state" {
		t.Fatalf("state dir = %q, want /tmp/headroom-state", cfg.StateDir)
	}
}

func TestHeadroomConfigFromTrustableConfig(t *testing.T) {
	withWorkspaceDir(t, t.TempDir())
	cfg := &trustableConfig{
		Experimental: &experimentalConfig{
			Headroom: &headroomExperimentConfig{
				Enabled: true,
			},
		},
	}

	got, err := applyHeadroomEnvOverrides(headroomConfigFromTrustableConfig(cfg))
	if err != nil {
		t.Fatalf("applyHeadroomEnvOverrides returned error: %v", err)
	}
	if !got.Enabled {
		t.Fatalf("Headroom should be enabled from trustable config")
	}
	if got.Mode != "proxy" {
		t.Fatalf("mode = %q, want proxy", got.Mode)
	}
	if got.Port != defaultHeadroomPort {
		t.Fatalf("port = %d, want %d", got.Port, defaultHeadroomPort)
	}
}

func TestHeadroomEnvOverridesTrustableConfig(t *testing.T) {
	withWorkspaceDir(t, t.TempDir())
	t.Setenv("TRUSTABLE_HEADROOM_ENABLED", "false")
	cfg := &trustableConfig{
		Experimental: &experimentalConfig{
			Headroom: &headroomExperimentConfig{
				Enabled: true,
			},
		},
	}

	got, err := applyHeadroomEnvOverrides(headroomConfigFromTrustableConfig(cfg))
	if err != nil {
		t.Fatalf("applyHeadroomEnvOverrides returned error: %v", err)
	}
	if got.Enabled {
		t.Fatalf("Headroom env override should disable saved config")
	}
}

func TestHeadroomConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		key  string
		val  string
	}{
		{name: "bool", key: "TRUSTABLE_HEADROOM_ENABLED", val: "maybe"},
		{name: "mode", key: "TRUSTABLE_HEADROOM_MODE", val: "wrap"},
		{name: "port", key: "TRUSTABLE_HEADROOM_PORT", val: "70000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withWorkspaceDir(t, t.TempDir())
			t.Setenv("TRUSTABLE_HEADROOM_ENABLED", "true")
			t.Setenv(tt.key, tt.val)
			if _, err := headroomConfigFromEnv(); err == nil {
				t.Fatalf("expected error for %s=%q", tt.key, tt.val)
			}
		})
	}
}

func TestNormalizeHeadroomOpenAIUpstream(t *testing.T) {
	tests := []struct {
		name string
		base string
		want string
	}{
		{name: "local ollama", base: "http://localhost:11434/v1", want: "http://localhost:11434"},
		{name: "ollama cloud", base: "https://ollama.com/v1/", want: "https://ollama.com"},
		{name: "nuvolaris proxy", base: "https://api.nuvolaris.io/v1", want: "https://api.nuvolaris.io"},
		{name: "provider path", base: "https://gateway.example/openai/v1", want: "https://gateway.example/openai"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeHeadroomOpenAIUpstream(tt.base)
			if err != nil {
				t.Fatalf("normalizeHeadroomOpenAIUpstream returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("upstream = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeHeadroomOpenAIUpstreamRejectsUnsafeOrAmbiguousURLs(t *testing.T) {
	for _, base := range []string{
		"ollama:11434/v1",
		"ftp://provider.example/v1",
		"https://user:secret@provider.example/v1",
		"https://provider.example/v1?key=secret",
		"https://provider.example/openai",
	} {
		t.Run(base, func(t *testing.T) {
			if _, err := normalizeHeadroomOpenAIUpstream(base); err == nil {
				t.Fatalf("expected %q to be rejected", base)
			}
		})
	}
}

func TestHeadroomRoutingPreservesProviderIdentityAndCredentials(t *testing.T) {
	cfg := &trustableConfig{
		Provider: "trustable",
		BaseURL:  "https://api.nuvolaris.io/v1",
		APIKey:   "aip-secret",
		Models: map[string]*ModelLimits{
			"qwen3.6-27b": {MaxToken: 128000, MaxOutput: 8192},
		},
	}
	provider := buildModelProvider(cfg)
	headroomCfg := headroomLaunchConfig{Enabled: true, Port: 18787}
	if err := applyHeadroomRoutingToModelProvider(provider, headroomCfg); err != nil {
		t.Fatalf("applyHeadroomRoutingToModelProvider returned error: %v", err)
	}
	options := provider["options"].(map[string]interface{})
	if options["baseURL"] != "http://127.0.0.1:18787/v1" {
		t.Fatalf("baseURL = %v", options["baseURL"])
	}
	if options["apiKey"] != "aip-secret" {
		t.Fatalf("apiKey changed: %v", options["apiKey"])
	}
	if _, ok := provider["models"].(map[string]interface{})["qwen3.6-27b"]; !ok {
		t.Fatalf("selected provider models changed")
	}
}

func TestHeadroomGeneratedConfigRoutesModelButKeepsMCPDirect(t *testing.T) {
	isolateOpenServerlessCheckerInstall(t)
	projectDir := t.TempDir()
	cfg := &trustableConfig{
		Provider: "trustable",
		BaseURL:  "https://api.nuvolaris.io/v1",
		APIKey:   "aip-secret",
		Models: map[string]*ModelLimits{
			"qwen3.6-27b": {MaxToken: 128000, MaxOutput: 8192},
		},
		Opencode: &opencodeConfig{Default: "qwen3.6-27b", Small: "qwen3.6-27b"},
		Experimental: &experimentalConfig{Headroom: &headroomExperimentConfig{
			Enabled: true,
			Port:    18787,
		}},
	}
	mcp := map[string]interface{}{
		"probe": map[string]interface{}{
			"type":    "local",
			"command": []string{"probe-mcp"},
			"enabled": true,
		},
	}
	if err := generateOpencodeConfigInDir(cfg, projectDir, mcp); err != nil {
		t.Fatalf("generateOpencodeConfigInDir returned error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(projectDir, "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	var generated map[string]interface{}
	if err := json.Unmarshal(data, &generated); err != nil {
		t.Fatal(err)
	}
	if generated["model"] != "trustable/qwen3.6-27b" || generated["small_model"] != "trustable/qwen3.6-27b" {
		t.Fatalf("model identity changed: model=%v small_model=%v", generated["model"], generated["small_model"])
	}
	providers := generated["provider"].(map[string]interface{})
	provider := providers["trustable"].(map[string]interface{})
	options := provider["options"].(map[string]interface{})
	if options["baseURL"] != "http://127.0.0.1:18787/v1" || options["apiKey"] != "aip-secret" {
		t.Fatalf("unexpected routed provider options: %#v", options)
	}
	generatedMCP := generated["mcp"].(map[string]interface{})
	if _, ok := generatedMCP["probe"]; !ok {
		t.Fatalf("existing MCP entry was removed: %#v", generatedMCP)
	}
	if _, ok := generatedMCP["openserverless"]; !ok {
		t.Fatalf("OpenServerless MCP entry is missing: %#v", generatedMCP)
	}
}

func TestHeadroomDisabledLeavesProviderUnchanged(t *testing.T) {
	cfg := &trustableConfig{Provider: "ollama", BaseURL: "http://ollama:11434/v1", APIKey: "dummy"}
	provider := buildModelProvider(cfg)
	before, err := json.Marshal(provider)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyHeadroomRoutingToModelProvider(provider, headroomLaunchConfig{}); err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(provider)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("disabled Headroom changed provider:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestHeadroomProxyEnvSetsOriginalUpstreamWithoutChangingBaseEnv(t *testing.T) {
	cfg := headroomLaunchConfig{Port: 8787, StateDir: t.TempDir()}
	base := []string{"OPENAI_API_KEY=secret", "OPENAI_TARGET_API_URL=https://stale.invalid", "KEEP=value"}
	env := headroomProxyEnv(base, cfg, "https://api.nuvolaris.io")
	joined := strings.Join(env, "\n")
	for _, want := range []string{
		"OPENAI_API_KEY=secret",
		"KEEP=value",
		"OPENAI_TARGET_API_URL=https://api.nuvolaris.io",
		"HEADROOM_PORT=8787",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("environment does not contain %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "stale.invalid") || strings.Count(joined, "OPENAI_TARGET_API_URL=") != 1 {
		t.Fatalf("stale upstream override survived:\n%s", joined)
	}
}

func TestHeadroomProxyHealthRequiresHeadroomIdentity(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want bool
	}{
		{name: "healthy Headroom", body: `{"service":"headroom-proxy","status":"healthy"}`, want: true},
		{name: "unrelated listener", body: `{"service":"other","status":"healthy"}`, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			server.Listener = listener
			server.Start()
			defer server.Close()
			port, err := strconv.Atoi(strings.TrimPrefix(listener.Addr().String(), "127.0.0.1:"))
			if err != nil {
				t.Fatal(err)
			}
			if got := headroomProxyHealthy(port); got != tt.want {
				t.Fatalf("headroomProxyHealthy() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHeadroomRoutingDiagnosticContainsNoAPIKey(t *testing.T) {
	cfg := headroomLaunchConfig{Enabled: true, Port: 8787, StateDir: t.TempDir()}
	if err := writeHeadroomRoutingDiagnostic(cfg, "https://api.nuvolaris.io"); err != nil {
		t.Fatal(err)
	}
	diagnostic, err := readHeadroomRoutingDiagnostic(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostic.UpstreamURL != "https://api.nuvolaris.io" || diagnostic.ProxyBaseURL != "http://127.0.0.1:8787/v1" {
		t.Fatalf("unexpected diagnostic: %+v", diagnostic)
	}
}

func TestEnsureHeadroomProxyStartsConfiguredRoutingInLaunchProcessGroup(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	stateDir := filepath.Join(workspace, "headroom-state")
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatal(err)
	}
	withWorkspaceDir(t, workspace)

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	baseConfig := map[string]interface{}{
		"provider": "trustable",
		"base_url": "https://api.nuvolaris.io/v1",
		"experimental": map[string]interface{}{
			"headroom": map[string]interface{}{
				"enabled":   true,
				"mode":      "proxy",
				"port":      port,
				"state_dir": stateDir,
			},
		},
	}
	configData, _ := json.Marshal(baseConfig)
	if err := os.WriteFile(filepath.Join(root, "trustable.json"), configData, 0644); err != nil {
		t.Fatal(err)
	}

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	fakeHeadroom := `#!/usr/bin/env python3
import json, os, sys
from http.server import BaseHTTPRequestHandler, HTTPServer
port = int(sys.argv[sys.argv.index("--port") + 1])
with open(os.path.join(os.getcwd(), "observed-target"), "w") as f:
    f.write(os.environ.get("OPENAI_TARGET_API_URL", ""))
class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args): pass
    def do_GET(self):
        body = json.dumps({"service": "headroom-proxy", "status": "healthy"}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
HTTPServer(("127.0.0.1", port), Handler).serve_forever()
`
	if err := os.WriteFile(filepath.Join(binDir, "headroom"), []byte(fakeHeadroom), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDir) })

	leader := exec.Command("sleep", "60")
	leader.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := leader.Start(); err != nil {
		t.Fatal(err)
	}
	pgid, err := syscall.Getpgid(leader.Process.Pid)
	if err != nil {
		_ = leader.Process.Kill()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		_, _ = leader.Process.Wait()
	})

	if err := ensureHeadroomProxy(pgid); err != nil {
		t.Fatalf("ensureHeadroomProxy returned error: %v", err)
	}
	target, err := os.ReadFile(filepath.Join(stateDir, "observed-target"))
	if err != nil {
		t.Fatal(err)
	}
	if string(target) != "https://api.nuvolaris.io" {
		t.Fatalf("proxy target = %q", target)
	}
	diagnostic, err := readHeadroomRoutingDiagnostic(headroomLaunchConfig{Port: port, StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	if diagnostic.UpstreamURL != "https://api.nuvolaris.io" {
		t.Fatalf("unexpected diagnostic: %+v", diagnostic)
	}
}
