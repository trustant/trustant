package main

import (
	"bytes"
	"encoding/json"
	"io"
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
	"time"
)

func withWorkspaceDir(t *testing.T, dir string) {
	t.Helper()
	old := WorkspaceDir
	WorkspaceDir = dir
	t.Cleanup(func() {
		WorkspaceDir = old
	})
}

func reserveHeadroomTestPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func writeHeadroomTestConfig(t *testing.T, path, upstream, stateDir string, port int, enabled bool) {
	t.Helper()
	cfg := map[string]interface{}{
		"provider": "trustable",
		"base_url": strings.TrimRight(upstream, "/") + "/v1",
		"api_key":  "aip-forward-me",
		"experimental": map[string]interface{}{
			"headroom": map[string]interface{}{
				"enabled":   enabled,
				"mode":      "proxy",
				"port":      port,
				"state_dir": stateDir,
			},
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func startHeadroomTestProcessGroup(t *testing.T) (*exec.Cmd, int) {
	t.Helper()
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
	return leader, pgid
}

func waitForHeadroomTestPortFree(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if isPortFree(port) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("port %d remained in use", port)
}

type headroomForwardObservation struct {
	Path          string
	Authorization string
	Model         string
	Stream        bool
	ToolName      string
}

func newHeadroomTestUpstream(t *testing.T) (*httptest.Server, <-chan headroomForwardObservation) {
	t.Helper()
	observed := make(chan headroomForwardObservation, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
			Tools  []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		observation := headroomForwardObservation{
			Path:          r.URL.Path,
			Authorization: r.Header.Get("Authorization"),
			Model:         body.Model,
			Stream:        body.Stream,
		}
		if len(body.Tools) > 0 {
			observation.ToolName = body.Tools[0].Function.Name
		}
		observed <- observation

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		frames := []string{
			`data: {"id":"chatcmpl-headroom","object":"chat.completion.chunk","created":1,"model":"qwen3.6-27b","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_weather","type":"function","function":{"name":"get_weather","arguments":"{\\\"city\\\":\\\"Rome\\\"}"}}]},"finish_reason":null}]}` + "\n\n",
			`data: {"id":"chatcmpl-headroom","object":"chat.completion.chunk","created":1,"model":"qwen3.6-27b","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n",
			"data: [DONE]\n\n",
		}
		for _, frame := range frames {
			_, _ = io.WriteString(w, frame)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(server.Close)
	return server, observed
}

func assertHeadroomForwardObservation(t *testing.T, observed <-chan headroomForwardObservation) {
	t.Helper()
	select {
	case got := <-observed:
		want := headroomForwardObservation{
			Path:          "/v1/chat/completions",
			Authorization: "Bearer aip-forward-me",
			Model:         "qwen3.6-27b",
			Stream:        true,
			ToolName:      "get_weather",
		}
		if got != want {
			t.Fatalf("upstream observation = %+v, want %+v", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not receive forwarded request")
	}
}

func exerciseHeadroomForwarding(t *testing.T, port int) {
	t.Helper()
	payload := []byte(`{
  "model":"qwen3.6-27b",
  "messages":[{"role":"user","content":"Call the weather tool for Rome"}],
  "stream":true,
  "tools":[{"type":"function","function":{"name":"get_weather","description":"Get weather","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}]
}`)
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(port)+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer aip-forward-me")
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("proxy status = %d, body=%s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	for _, want := range []string{`data: {`, `"tool_calls"`, `"get_weather"`, `data: [DONE]`} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatalf("streamed response does not contain %q:\n%s", want, body)
		}
	}
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

func TestEnsureHeadroomProxyEnableRelaunchDisableWithoutExternalNetwork(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	stateDir := filepath.Join(workspace, "headroom-state")
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatal(err)
	}
	withWorkspaceDir(t, workspace)
	for _, key := range []string{"TRUSTABLE_HEADROOM_ENABLED", "TRUSTABLE_HEADROOM_MODE", "TRUSTABLE_HEADROOM_PORT", "TRUSTABLE_HEADROOM_STATE_DIR"} {
		t.Setenv(key, "")
	}

	upstream, observed := newHeadroomTestUpstream(t)
	port := reserveHeadroomTestPort(t)
	configPath := filepath.Join(root, "trustable.json")
	writeHeadroomTestConfig(t, configPath, upstream.URL, stateDir, port, true)

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	fakeHeadroom := `#!/usr/bin/env python3
import json, os, sys, urllib.request
from http.server import BaseHTTPRequestHandler, HTTPServer
port = int(sys.argv[sys.argv.index("--port") + 1])
with open(os.path.join(os.getcwd(), "observed-target"), "w") as f:
    f.write(os.environ.get("OPENAI_TARGET_API_URL", ""))
with open(os.path.join(os.getcwd(), "proxy-pid"), "w") as f:
    f.write(str(os.getpid()))
class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args): pass
    def do_GET(self):
        body = json.dumps({"service": "headroom-proxy", "status": "healthy"}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        request = urllib.request.Request(
            os.environ["OPENAI_TARGET_API_URL"] + self.path,
            data=body,
            method="POST",
            headers={
                "Authorization": self.headers.get("Authorization", ""),
                "Content-Type": self.headers.get("Content-Type", "application/json"),
            },
        )
        with urllib.request.urlopen(request) as response:
            self.send_response(response.status)
            self.send_header("Content-Type", response.headers.get("Content-Type", "application/json"))
            self.end_headers()
            while True:
                chunk = response.read(128)
                if not chunk: break
                self.wfile.write(chunk)
                self.wfile.flush()
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

	_, firstPGID := startHeadroomTestProcessGroup(t)
	if err := ensureHeadroomProxy(firstPGID); err != nil {
		t.Fatalf("ensureHeadroomProxy returned error: %v", err)
	}
	target, err := os.ReadFile(filepath.Join(stateDir, "observed-target"))
	if err != nil {
		t.Fatal(err)
	}
	if string(target) != upstream.URL {
		t.Fatalf("proxy target = %q", target)
	}
	firstPIDData, err := os.ReadFile(filepath.Join(stateDir, "proxy-pid"))
	if err != nil {
		t.Fatal(err)
	}
	firstPID, err := strconv.Atoi(string(firstPIDData))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := syscall.Getpgid(firstPID); err != nil || got != firstPGID {
		t.Fatalf("first proxy process group = %d, %v; want %d", got, err, firstPGID)
	}

	exerciseHeadroomForwarding(t, port)
	assertHeadroomForwardObservation(t, observed)

	_, secondPGID := startHeadroomTestProcessGroup(t)
	if err := ensureHeadroomProxy(secondPGID); err != nil {
		t.Fatalf("relaunch ensureHeadroomProxy returned error: %v", err)
	}
	secondPIDData, err := os.ReadFile(filepath.Join(stateDir, "proxy-pid"))
	if err != nil {
		t.Fatal(err)
	}
	secondPID, err := strconv.Atoi(string(secondPIDData))
	if err != nil {
		t.Fatal(err)
	}
	if secondPID == firstPID {
		t.Fatalf("relaunch reused stale proxy pid %d", firstPID)
	}
	if got, err := syscall.Getpgid(secondPID); err != nil || got != secondPGID {
		t.Fatalf("relaunched proxy process group = %d, %v; want %d", got, err, secondPGID)
	}

	diagnostic, err := readHeadroomRoutingDiagnostic(headroomLaunchConfig{Port: port, StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	if diagnostic.UpstreamURL != upstream.URL {
		t.Fatalf("unexpected diagnostic: %+v", diagnostic)
	}

	if err := syscall.Kill(-secondPGID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitForHeadroomTestPortFree(t, port)
	writeHeadroomTestConfig(t, configPath, upstream.URL, stateDir, port, false)
	_, disabledPGID := startHeadroomTestProcessGroup(t)
	if err := ensureHeadroomProxy(disabledPGID); err != nil {
		t.Fatalf("disabled ensureHeadroomProxy returned error: %v", err)
	}
	if isPortListening(port) {
		t.Fatalf("disabled Headroom unexpectedly started a proxy on port %d", port)
	}
}

func TestHeadroomRealProxyForwardsAuthorizationModelStreamingAndToolCall(t *testing.T) {
	bin, err := exec.LookPath("headroom")
	if err != nil {
		t.Skip("real Headroom binary is not installed; run this test in the Trustable image")
	}

	stateDir := filepath.Join(t.TempDir(), "headroom-state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}

	upstream, observed := newHeadroomTestUpstream(t)
	port := reserveHeadroomTestPort(t)
	_, pgid := startHeadroomTestProcessGroup(t)
	cfg := headroomLaunchConfig{Enabled: true, Mode: "proxy", Port: port, StateDir: stateDir}
	args := []string{
		"proxy",
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"--no-telemetry",
		"--no-ccr-inject-tool",
		"--log-file", filepath.Join(stateDir, "proxy.jsonl"),
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = stateDir
	cmd.Env = headroomProxyEnv(os.Environ(), cfg, upstream.URL)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: pgid}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start real Headroom: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if err := waitForPort(port, 15*time.Second); err != nil {
		select {
		case exitErr := <-done:
			t.Fatalf("real Headroom exited before listening: %v", exitErr)
		default:
		}
		t.Fatal(err)
	}
	exerciseHeadroomForwarding(t, port)
	assertHeadroomForwardObservation(t, observed)
}
