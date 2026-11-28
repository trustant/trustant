// Copyright 2025-2026 Nuvolaris Inc
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const testMCPToken = "test-token-0123456789abcdefghijklmnopqrstuvwxyz"

// TestMain lets the test binary double as a stdio MCP server, so the proxy
// can be exercised against a real child process without extra tooling.
func TestMain(m *testing.M) {
	if os.Getenv("TRUSTANT_FAKE_MCP_SERVER") == "1" {
		runFakeMCPServer()
		return
	}
	os.Exit(m.Run())
}

func runFakeMCPServer() {
	s := mcp.NewServer(&mcp.Implementation{Name: "fake", Version: "1"}, nil)
	type echoArgs struct {
		Text string `json:"text"`
	}
	mcp.AddTool(s, &mcp.Tool{Name: "echo", Description: "echo text"},
		func(_ context.Context, _ *mcp.CallToolRequest, a echoArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "echo:" + a.Text + ":" + os.Getenv("FAKE_SECRET")}}}, nil, nil
		})
	s.Run(context.Background(), &mcp.StdioTransport{})
}

// mcpTestEnv points the workspace/workbench at a temp dir with one launched app.
func mcpTestEnv(t *testing.T) (app string, appDir string) {
	t.Helper()
	root := t.TempDir()
	savedWS, savedWB := WorkspaceDir, WorkbenchDir
	WorkspaceDir = filepath.Join(root, "workspace")
	WorkbenchDir = filepath.Join(root, "workbench")
	t.Cleanup(func() { WorkspaceDir, WorkbenchDir = savedWS, savedWB })
	app = "myapp01"
	appDir = filepath.Join(WorkbenchDir, app)
	for _, d := range []string{WorkspaceDir, appDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(WorkbenchDir, "current"), []byte(app), 0644); err != nil {
		t.Fatal(err)
	}
	savedTokens := mcpTokens
	mcpTokens = &mcpTokenStore{}
	t.Cleanup(func() { mcpTokens = savedTokens })
	if err := mcpTokens.set(testMCPToken); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/files/", handleFiles)
	mux.HandleFunc("/api/repo", handleRepo)
	savedAPI := mcpAPIHandler
	mcpAPIHandler = mux
	t.Cleanup(func() { mcpAPIHandler = savedAPI })
	return app, appDir
}

func TestMCPTokenStoreGeneratesAndReloads(t *testing.T) {
	dir := t.TempDir()
	s := &mcpTokenStore{path: filepath.Join(dir, "token.mcp")}
	tok, err := s.current()
	if err != nil || !mcpTokenPattern.MatchString(tok) {
		t.Fatalf("generated token %q err=%v", tok, err)
	}
	info, err := os.Stat(s.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("token file must be 0600, got %v err=%v", info, err)
	}
	if err := s.set("short"); err == nil {
		t.Fatal("a short token must be rejected")
	}
	// An external write (installer, manual edit) is picked up by mtime.
	time.Sleep(10 * time.Millisecond)
	external := "external-0123456789abcdefghijklmnopqrstuvwxyz"
	if err := os.WriteFile(s.path, []byte(external+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Second)
	os.Chtimes(s.path, future, future)
	if got, _ := s.current(); got != external {
		t.Fatalf("external token not reloaded: %q", got)
	}
}

func TestMCPTokenAPI(t *testing.T) {
	mcpTestEnv(t)
	get := httptest.NewRecorder()
	handleMCPToken(get, httptest.NewRequest(http.MethodGet, "http://trustant.example.com/api/v1/token", nil))
	if strings.Contains(get.Body.String(), testMCPToken) {
		t.Fatal("GET /api/v1/token must never return the token")
	}
	if !strings.Contains(get.Body.String(), mcpTokenFingerprint(testMCPToken)) || !strings.Contains(get.Body.String(), "/mcp") {
		t.Fatalf("GET should report fingerprint and url: %s", get.Body.String())
	}

	newTok := "rotated-0123456789abcdefghijklmnopqrstuvwxyz"
	put := httptest.NewRecorder()
	handleMCPToken(put, httptest.NewRequest(http.MethodPut, "/api/v1/token", strings.NewReader(`{"token":"`+newTok+`"}`)))
	if put.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", put.Code, put.Body.String())
	}
	data, _ := os.ReadFile(filepath.Join(WorkspaceDir, "token.mcp"))
	if strings.TrimSpace(string(data)) != newTok {
		t.Fatalf("token.mcp = %q", data)
	}
	bad := httptest.NewRecorder()
	handleMCPToken(bad, httptest.NewRequest(http.MethodPut, "/api/v1/token", strings.NewReader(`{"token":"no spaces allowed here at all, nope"}`)))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid token must be 400, got %d", bad.Code)
	}

	// With local auth enabled a PUT needs the current token (or a UI session).
	savedAuth := appAuth
	appAuth = &authManager{enabled: true, sessions: map[string]*authSession{}}
	t.Cleanup(func() { appAuth = savedAuth })
	denied := httptest.NewRecorder()
	handleMCPToken(denied, httptest.NewRequest(http.MethodPut, "/api/v1/token", strings.NewReader(`{"token":"`+testMCPToken+`"}`)))
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("PUT without credentials under auth must be 401, got %d", denied.Code)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/token", strings.NewReader(`{"token":"`+testMCPToken+`"}`))
	req.Header.Set("Authorization", "Bearer "+newTok)
	allowed := httptest.NewRecorder()
	handleMCPToken(allowed, req)
	if allowed.Code != http.StatusOK {
		t.Fatalf("PUT with the current bearer under auth: %d %s", allowed.Code, allowed.Body.String())
	}
}

func TestMCPEndpointRequiresBearer(t *testing.T) {
	mcpTestEnv(t)
	for _, auth := range []string{"", "Bearer wrong-token-0123456789abcdefghijklmnop"} {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		handleMCP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("auth %q: want 401, got %d", auth, rec.Code)
		}
	}
	if !publicAuthPath("/mcp") || !publicAuthPath("/api/v1/token") {
		t.Fatal("/mcp and /api/v1/token must bypass the session middleware (they carry their own auth)")
	}
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// connectTestMCP starts /mcp on a test server and returns a client session.
func connectTestMCP(t *testing.T) *mcp.ClientSession {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(handleMCP))
	t.Cleanup(srv.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   srv.URL,
		HTTPClient: &http.Client{Transport: bearerTransport{testMCPToken}},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %s", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func callText(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %s", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func TestMCPToolsResourcesAndFiles(t *testing.T) {
	_, appDir := mcpTestEnv(t)
	cs := connectTestMCP(t)

	if cs.InitializeResult().Instructions == "" {
		t.Fatal("initialize must carry the Trustant instructions")
	}
	tools := map[string]bool{}
	for tool, err := range cs.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		tools[tool.Name] = true
	}
	for _, want := range []string{"apps_list", "app_create", "app_launch", "app_status", "app_view", "app_save",
		"app_push", "app_deploy", "app_publish", "files_list", "files_read", "files_write", "files_delete", "files_search"} {
		if !tools[want] {
			t.Errorf("missing tool %s", want)
		}
	}

	res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "trustant://instructions/openserverless"})
	if err != nil || len(res.Contents) != 1 || res.Contents[0].Text != openserverlessInstructionsMd {
		t.Fatalf("instructions resource: %v", err)
	}
	prompt, err := cs.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: "build_fullstack_app", Arguments: map[string]string{"goal": "a todo app"}})
	if err != nil || !strings.Contains(prompt.Messages[0].Content.(*mcp.TextContent).Text, "a todo app") {
		t.Fatalf("prompt: %v", err)
	}

	// Lifecycle tools drive the real /api handlers in process.
	bare := filepath.Join(WorkspaceDir, "workspace", "myapp01")
	if out, err := exec.Command("git", "init", "--bare", "-q", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %s", err, out)
	}
	if out, err := exec.Command("git", "-C", bare, "remote", "add", "origin", "git@github.com:acme/myapp01.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote: %s %s", err, out)
	}
	if out, isErr := callText(t, cs, "apps_list", nil); isErr || !strings.Contains(out, `"current": "myapp01"`) || !strings.Contains(out, `"name": "myapp01"`) {
		t.Fatalf("apps_list: %s", out)
	}
	if out, isErr := callText(t, cs, "app_create", map[string]any{"name": "../bad", "repo": "a/b"}); !isErr {
		t.Fatalf("app_create must validate the name: %s", out)
	}

	// Write, read back, search, delete.
	if out, isErr := callText(t, cs, "files_write", map[string]any{"path": "src/new/App.tsx", "content": "export const x = 42\n"}); isErr {
		t.Fatalf("files_write: %s", out)
	}
	if out, _ := callText(t, cs, "files_read", map[string]any{"path": "src/new/App.tsx"}); out != "export const x = 42\n" {
		t.Fatalf("files_read: %q", out)
	}
	if out, _ := callText(t, cs, "files_search", map[string]any{"pattern": "x = 42"}); !strings.Contains(out, "src/new/App.tsx:1:") {
		t.Fatalf("files_search: %s", out)
	}
	if out, _ := callText(t, cs, "files_list", map[string]any{"path": "src"}); !strings.Contains(out, "src/new/App.tsx") {
		t.Fatalf("files_list: %s", out)
	}
	if out, isErr := callText(t, cs, "files_delete", map[string]any{"path": "src/new/App.tsx"}); isErr {
		t.Fatalf("files_delete: %s", out)
	}
	if _, err := os.Stat(filepath.Join(appDir, "src/new/App.tsx")); !os.IsNotExist(err) {
		t.Fatal("file not deleted")
	}

	// Containment: traversal, absolute paths and symlink escapes are refused.
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(appDir, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"../evil.txt", "/etc/evil", "escape/evil.txt", "src/../../evil.txt"} {
		if _, isErr := callText(t, cs, "files_write", map[string]any{"path": p, "content": "x"}); !isErr {
			t.Errorf("files_write %q must be refused", p)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("a write escaped the workbench: %v", entries)
	}

	// Launch-managed files are read-only; AGENTS.md keeps its managed block.
	for _, p := range []string{".mcp.json", ".env", "CLAUDE.md", ".git/config", ".openserverless-contract.md"} {
		if _, isErr := callText(t, cs, "files_write", map[string]any{"path": p, "content": "x"}); !isErr {
			t.Errorf("files_write %q must be refused (managed)", p)
		}
	}
	if _, isErr := callText(t, cs, "files_write", map[string]any{"path": "AGENTS.md", "content": "my notes\n"}); isErr {
		t.Fatal("AGENTS.md must be writable")
	}
	agents, _ := os.ReadFile(filepath.Join(appDir, "AGENTS.md"))
	if !strings.Contains(string(agents), trustantAgentsBegin) || !strings.Contains(string(agents), "my notes") {
		t.Fatalf("AGENTS.md must keep the managed block and the notes: %s", agents)
	}
}

func TestMCPEventRecorderParsesSSE(t *testing.T) {
	rec := newMCPEventRecorder(context.Background(), nil)
	rec.Header().Set("Content-Type", "text/event-stream")
	rec.WriteHeader(http.StatusOK)
	rec.Write([]byte("event: progress\ndata: {\"stage\":1,\"total\":3,\"message\":\"a\"}\n\nevent: output\ndata: {\"line\":\"hello\"}\n\nevent: do"))
	rec.Write([]byte("ne\ndata: {\"url\":\"x\"}\n\n"))
	r := rec.result()
	if string(r.Body) != `{"url":"x"}` || len(r.Output) != 1 || r.Output[0] != "hello" || r.failed() {
		t.Fatalf("unexpected result: %+v", r)
	}
	rec = newMCPEventRecorder(context.Background(), nil)
	rec.Header().Set("Content-Type", "text/event-stream")
	rec.Write([]byte("event: error\ndata: {\"error\":\"License required: x\"}\n\n"))
	if !rec.result().failed() {
		t.Fatal("an error event must fail the call")
	}
}

func TestAppURLs(t *testing.T) {
	u := appURLs("http://trustant.192.168.5.15.nip.io:8910")
	if u["dev"] != "http://vite.192.168.5.15.nip.io:8910/" || u["workbench"] != "http://trustant.192.168.5.15.nip.io:8910/app.html" {
		t.Fatalf("appURLs: %v", u)
	}
}

func TestMCPProxyReexposesAppServer(t *testing.T) {
	app, _ := mcpTestEnv(t)
	cfg := filepath.Join(t.TempDir(), "mcp.json")
	exe, _ := os.Executable()
	config := map[string]any{"mcpServers": map[string]any{
		"fake": map[string]any{"type": "stdio", "command": exe, "args": []string{"-test.run=^$"},
			"env": map[string]string{"TRUSTANT_FAKE_MCP_SERVER": "1", "FAKE_SECRET": "s3cr3t"}},
		"broken": map[string]any{"type": "stdio", "command": "/nonexistent/mcp-server"},
	}}
	data, _ := json.Marshal(config)
	if err := os.WriteFile(cfg, data, 0600); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	registered := map[string]mcp.ToolHandler{}
	m := &mcpProxyManager{
		cfgPath:  func(string) (string, error) { return cfg, nil },
		register: func(tool *mcp.Tool, h mcp.ToolHandler) { mu.Lock(); registered[tool.Name] = h; mu.Unlock() },
		remove: func(names ...string) {
			mu.Lock()
			for _, n := range names {
				delete(registered, n)
			}
			mu.Unlock()
		},
	}
	m.syncNow()
	t.Cleanup(m.stopAll)

	h := registered["fake__echo"]
	if h == nil {
		t.Fatalf("fake__echo not registered; status=%v", m.statusSnapshot())
	}
	args, _ := json.Marshal(map[string]string{"text": "hi"})
	res, err := h(context.Background(), &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "fake__echo", Arguments: args}})
	if err != nil || res.Content[0].(*mcp.TextContent).Text != "echo:hi:s3cr3t" {
		t.Fatalf("proxied call: %v %v", res, err)
	}
	if st := m.statusSnapshot(); !strings.HasPrefix(st["fake"], "ok") || !strings.HasPrefix(st["broken"], "error") {
		t.Fatalf("status: %v (one broken server must not break the rest)", st)
	}

	// Switching away from the app stops the proxies and removes their tools.
	os.WriteFile(filepath.Join(WorkbenchDir, "current"), []byte(""), 0644)
	m.syncNow()
	if len(registered) != 0 {
		t.Fatalf("tools left after the app changed: %v", registered)
	}
	_ = app
}

func TestProxyToolName(t *testing.T) {
	if got := proxyToolName("open serverless", "do.thing"); got != "open_serverless__do_thing" {
		t.Fatalf("proxyToolName: %s", got)
	}
	if got := proxyToolName(strings.Repeat("a", 60), "tool"); len(got) > 64 {
		t.Fatalf("name too long: %d", len(got))
	}
}
