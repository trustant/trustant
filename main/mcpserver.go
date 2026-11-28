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

// mcpserver.go is the Trustant MCP entry point (spec/22-mcpserver.md): an MCP
// server at /mcp on the trustant.<domain> host that lets an external agent
// create, launch, edit, save, push and publish apps, use the current app's own
// MCP servers through proxies (mcpproxy.go), and read the OpenServerless
// build instructions.
//
// The app-lifecycle tools drive the existing /api handlers in process, so the
// MCP surface and the web UI share one implementation and cannot drift.

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---------------------------------------------------------------------------
// Token
// ---------------------------------------------------------------------------

// mcpTokenFileName is the server-side copy of the bearer token, in the
// workspace root. util/mcp.sh owns the value (it generates it on the client
// and PUTs it to /api/v1/token); the server only stores it.
const mcpTokenFileName = "token.mcp"

// mcpTokenPattern bounds what PUT /api/v1/token accepts: long enough to be a
// real secret, and limited to characters that survive a shell, a header and a
// TOML/JSON string without quoting.
var mcpTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{32,256}$`)

type mcpTokenStore struct {
	mu    sync.Mutex
	path  string // test override; empty means $WORKSPACE_DIR/token.mcp
	token string
	mtime time.Time
}

var mcpTokens = &mcpTokenStore{}

func (s *mcpTokenStore) filePath() string {
	if s.path != "" {
		return s.path
	}
	return filepath.Join(WorkspaceDir, mcpTokenFileName)
}

// current returns the active token. The file is re-read when its mtime
// changes, so an installer or a manual edit takes effect without a restart.
// With no file (or an empty one) a random token is generated and stored, so
// /mcp is never open: it stays unusable until a client PUTs its own value.
func (s *mcpTokenStore) current() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.filePath()
	if info, err := os.Stat(p); err == nil {
		if s.token != "" && info.ModTime().Equal(s.mtime) {
			return s.token, nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		if tok := strings.TrimSpace(string(data)); tok != "" {
			s.token, s.mtime = tok, info.ModTime()
			return tok, nil
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	tok, err := randomToken(rand.Reader, 32)
	if err != nil {
		return "", err
	}
	if err := s.writeLocked(tok); err != nil {
		return "", err
	}
	return tok, nil
}

func (s *mcpTokenStore) set(tok string) error {
	if !mcpTokenPattern.MatchString(tok) {
		return errors.New("token must be 32-256 characters of [A-Za-z0-9_-]")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeLocked(tok)
}

func (s *mcpTokenStore) writeLocked(tok string) error {
	p := s.filePath()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".token.mcp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(tok + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return err
	}
	info, err := os.Stat(p)
	if err != nil {
		return err
	}
	s.token, s.mtime = tok, info.ModTime()
	return nil
}

func mcpTokenFingerprint(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])[:8]
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func validMCPBearer(r *http.Request) bool {
	want, err := mcpTokens.current()
	got := bearerToken(r)
	return err == nil && got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// requestBaseURL is scheme://host of the request as the client addressed it.
func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if requestIsHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// handleMCPToken serves /api/v1/token.
//
//   - GET never returns the token, only whether one is set, its fingerprint
//     and the MCP URL (for the Configure page).
//   - PUT {"token": "..."} replaces it and closes every MCP session.
//
// /api/v1/token is public in the auth middleware because util/mcp.sh cannot
// hold a browser session. With local auth disabled (the default) it is as open
// as every other trustant.<domain> API. With local auth enabled a PUT needs
// either a UI session with a valid CSRF header, or the current token as bearer
// (rotation by util/mcp.sh refresh).
func handleMCPToken(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tok, err := mcpTokens.current()
		if err != nil {
			writeJSONStatus(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSONStatus(w, http.StatusOK, map[string]any{
			"set":         true,
			"url":         requestBaseURL(r) + "/mcp",
			"fingerprint": mcpTokenFingerprint(tok),
		})
	case http.MethodPut:
		if !mcpTokenPutAllowed(r) {
			writeJSONStatus(w, http.StatusUnauthorized, map[string]any{"error": "authentication required: log in to the UI or send the current token as bearer"})
			return
		}
		var req struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
			return
		}
		if err := mcpTokens.set(strings.TrimSpace(req.Token)); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		closeMCPSessions()
		writeJSONStatus(w, http.StatusOK, map[string]any{"ok": true})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// appAuth is the auth manager main() installed; nil in tests.
var appAuth *authManager

func mcpTokenPutAllowed(r *http.Request) bool {
	if appAuth == nil || !appAuth.enabled {
		return true
	}
	if validMCPBearer(r) {
		return true
	}
	session := appAuth.requestSession(r)
	return session != nil && appAuth.validCSRFRequest(r, session)
}

func writeJSONStatus(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(payload)
}

// ---------------------------------------------------------------------------
// /mcp endpoint
// ---------------------------------------------------------------------------

var (
	mcpServerOnce sync.Once
	mcpServer     *mcp.Server
	mcpHTTP       http.Handler
	// mcpBase is scheme://host of the latest authenticated /mcp request: the
	// in-process API calls and the app URLs are built against it.
	mcpBase atomic.Value
)

func trustantMCPServer() (*mcp.Server, http.Handler) {
	mcpServerOnce.Do(func() {
		mcpServer = newTrustantMCPServer()
		mcpHTTP = mcp.NewStreamableHTTPHandler(
			func(*http.Request) *mcp.Server { return mcpServer },
			// The bearer check in handleMCP already defeats DNS rebinding (a
			// rebinding page cannot know the token), and the default protection
			// would refuse a trustant.127.0.0.1.nip.io client on WSL.
			&mcp.StreamableHTTPOptions{DisableLocalhostProtection: true},
		)
	})
	return mcpServer, mcpHTTP
}

func closeMCPSessions() {
	if mcpServer == nil {
		return
	}
	for ss := range mcpServer.Sessions() {
		ss.Close()
	}
}

func handleMCP(w http.ResponseWriter, r *http.Request) {
	if !validMCPBearer(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="trustant"`)
		writeJSONStatus(w, http.StatusUnauthorized, map[string]any{"error": "missing or invalid bearer token (see util/mcp.sh)"})
		return
	}
	mcpBase.Store(requestBaseURL(r))
	_, handler := trustantMCPServer()
	mcpProxies.maybeSync()
	handler.ServeHTTP(w, r)
}

func currentMCPBase() string {
	if v, ok := mcpBase.Load().(string); ok && v != "" {
		return v
	}
	return "http://trustant.127.0.0.1.nip.io:8910"
}

// ---------------------------------------------------------------------------
// Server definition
// ---------------------------------------------------------------------------

const mcpInstructions = `Trustant hosts full-stack OpenServerless apps and controls them for you.

Workflow:
1. apps_list to see the apps; app_create to add one from a GitHub repo (owner/repo).
2. app_launch to check it out into the workbench and start its dev server. Launch makes the app "current".
3. Edit the current app with files_list / files_read / files_write / files_delete / files_search.
   The dev server reloads on save.
4. Show the app. Call ui_location to see what the user is looking at (app list or an app, and its
   route) and get the URLs. Open urls.split in your browser pane: the app on top, the ACP chat below,
   without the Trustant frame (urls.split_app / urls.split_acp for one pane, urls.app / urls.acp for
   the bare pages). After app_launch or files_write, reload it and check the result yourself.
5. Use the app's own MCP servers through the proxied tools named <server>__<tool>
   (openserverless__*, react__*, postgres__*, ...). They appear after a launch.
6. app_save commits the work; app_push pushes it to GitHub; app_deploy / app_publish deploy it.

Before writing code read the resources trustant://instructions/openserverless and
trustant://instructions/contract (or use the build_fullstack_app prompt): they are the rules
every Trustant app must follow. trustant://app/agents is the current app's AGENTS.md.`

func newTrustantMCPServer() *mcp.Server {
	version := appVersion
	if version == "" {
		version = "dev"
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "trustant", Title: "Trustant", Version: version},
		&mcp.ServerOptions{Instructions: mcpInstructions})
	registerMCPLifecycleTools(s)
	registerMCPFileTools(s)
	registerMCPResources(s)
	registerMCPLocationTool(s)
	return s
}

// ---------------------------------------------------------------------------
// In-process API calls
// ---------------------------------------------------------------------------

// mcpAPIResult is what an in-process /api call produced. For a streaming
// endpoint Body is the terminal done/error payload and Output the command
// output lines it emitted.
type mcpAPIResult struct {
	Status int
	Body   json.RawMessage
	Output []string
}

// mcpAPIHandler serves the in-process /api calls; main() registers every
// route on http.DefaultServeMux. Tests substitute their own mux.
var mcpAPIHandler http.Handler = http.DefaultServeMux

// mcpOutputLimit keeps a long launch from flooding the agent's context.
const mcpOutputLimit = 200

// callAPI runs method path against http.DefaultServeMux in process, with the
// Host of the MCP client so handlers build the same URLs the UI would see. The
// auth middleware wraps the mux, not the handlers, so it is not re-applied:
// the caller already passed the bearer check. stream asks the handler for
// server-sent events, which are relayed as MCP progress notifications.
func callAPI(ctx context.Context, req *mcp.CallToolRequest, method, path string, body any, stream bool) (*mcpAPIResult, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	base := currentMCPBase()
	httpReq, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return nil, err
	}
	if u, err := url.Parse(base); err == nil {
		httpReq.Host = u.Host
	}
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	rec := newMCPEventRecorder(ctx, req)
	mcpAPIHandler.ServeHTTP(rec, httpReq)
	return rec.result(), nil
}

// mcpEventRecorder is an http.ResponseWriter that understands the progress
// SSE format of progress.go ("event: X\ndata: Y\n\n"). progress events become
// MCP progress notifications, output lines are kept (bounded), and the
// done/error event is the result. A non-SSE response is kept verbatim.
type mcpEventRecorder struct {
	ctx     context.Context
	req     *mcp.CallToolRequest
	header  http.Header
	status  int
	buf     bytes.Buffer
	pending bytes.Buffer
	sse     bool
	final   []byte
	output  []string
	step    float64
}

func newMCPEventRecorder(ctx context.Context, req *mcp.CallToolRequest) *mcpEventRecorder {
	return &mcpEventRecorder{ctx: ctx, req: req, header: http.Header{}}
}

func (e *mcpEventRecorder) Header() http.Header { return e.header }
func (e *mcpEventRecorder) Flush()              {}

func (e *mcpEventRecorder) WriteHeader(status int) {
	if e.status == 0 {
		e.status = status
		e.sse = strings.HasPrefix(e.header.Get("Content-Type"), "text/event-stream")
	}
}

func (e *mcpEventRecorder) Write(p []byte) (int, error) {
	if e.status == 0 {
		e.WriteHeader(http.StatusOK)
	}
	if !e.sse {
		return e.buf.Write(p)
	}
	e.pending.Write(p)
	for {
		data := e.pending.Bytes()
		i := bytes.Index(data, []byte("\n\n"))
		if i < 0 {
			break
		}
		block := string(data[:i])
		e.pending.Next(i + 2)
		e.handleEvent(block)
	}
	return len(p), nil
}

func (e *mcpEventRecorder) handleEvent(block string) {
	var name string
	var data []string
	for _, line := range strings.Split(block, "\n") {
		switch {
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "event:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	payload := strings.Join(data, "\n")
	switch name {
	case "done", "error":
		e.final = []byte(payload)
	case "progress":
		var p struct {
			Stage   float64 `json:"stage"`
			Total   float64 `json:"total"`
			Message string  `json:"message"`
		}
		if json.Unmarshal([]byte(payload), &p) == nil {
			e.notify(p.Stage, p.Total, p.Message)
		}
	case "output":
		var o struct {
			Line string `json:"line"`
		}
		if json.Unmarshal([]byte(payload), &o) == nil {
			e.output = append(e.output, o.Line)
			if len(e.output) > mcpOutputLimit {
				e.output = e.output[len(e.output)-mcpOutputLimit:]
			}
		}
	}
}

func (e *mcpEventRecorder) notify(stage, total float64, message string) {
	if e.req == nil || e.req.Session == nil || e.req.Params == nil {
		return
	}
	tok := e.req.Params.GetProgressToken()
	if tok == nil {
		return
	}
	// Progress must increase; stages can repeat (sub-steps of one stage).
	if stage <= e.step {
		stage = e.step + 0.01
	}
	e.step = stage
	e.req.Session.NotifyProgress(e.ctx, &mcp.ProgressNotificationParams{
		ProgressToken: tok, Progress: stage, Total: total, Message: message,
	})
}

func (e *mcpEventRecorder) result() *mcpAPIResult {
	status := e.status
	if status == 0 {
		status = http.StatusOK
	}
	body := e.buf.Bytes()
	if e.sse {
		body = e.final
	}
	if len(bytes.TrimSpace(body)) == 0 {
		body = []byte("{}")
	}
	if !json.Valid(body) {
		quoted, _ := json.Marshal(strings.TrimSpace(string(body)))
		body = quoted
	}
	return &mcpAPIResult{Status: status, Body: json.RawMessage(body), Output: e.output}
}

// failed reports whether the API call failed: an HTTP error status, or a
// JSON object carrying an "error" key (the SSE path is always 200).
func (r *mcpAPIResult) failed() bool {
	if r.Status >= 400 {
		return true
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(r.Body, &obj) == nil {
		_, has := obj["error"]
		return has
	}
	return false
}

func mcpText(v any) *mcp.CallToolResult {
	var text string
	switch t := v.(type) {
	case string:
		text = t
	case json.RawMessage:
		var pretty bytes.Buffer
		if json.Indent(&pretty, t, "", "  ") == nil {
			text = pretty.String()
		} else {
			text = string(t)
		}
	default:
		data, _ := json.MarshalIndent(v, "", "  ")
		text = string(data)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func mcpError(format string, args ...any) *mcp.CallToolResult {
	res := mcpText(fmt.Sprintf(format, args...))
	res.IsError = true
	return res
}

// apiToolResult turns an API result into a tool result, flagging errors and
// appending the bounded command output.
func apiToolResult(r *mcpAPIResult) *mcp.CallToolResult {
	payload := map[string]any{"result": r.Body}
	if len(r.Output) > 0 {
		payload["output"] = r.Output
	}
	res := mcpText(payload)
	res.IsError = r.failed()
	return res
}

func currentAppOrError() (string, *mcp.CallToolResult) {
	app, err := readCurrentApp()
	if err != nil || app == "" {
		return "", mcpError("no app is launched: call app_launch first")
	}
	return app, nil
}

func validAppName(name string) *mcp.CallToolResult {
	if !namePattern.MatchString(name) {
		return mcpError("invalid app name %q", name)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Lifecycle tools
// ---------------------------------------------------------------------------

type mcpNoArgs struct{}

type mcpAppArgs struct {
	Name string `json:"name" jsonschema:"the app name"`
}

type mcpCreateArgs struct {
	Name      string `json:"name" jsonschema:"new app name: starts with a letter, alphanumeric"`
	Repo      string `json:"repo" jsonschema:"GitHub repository as owner/repo"`
	Templates string `json:"templates,omitempty" jsonschema:"optional notebook templates repository (owner/repo)"`
	Source    string `json:"source,omitempty" jsonschema:"optional: 'lovable' to import and convert a Lovable project repo"`
}

type mcpSaveArgs struct {
	Message string `json:"message,omitempty" jsonschema:"commit message"`
	Name    string `json:"name,omitempty" jsonschema:"app name (default: the current app)"`
}

type mcpPushArgs struct {
	Name string `json:"name,omitempty" jsonschema:"app name (default: the current app)"`
	Repo string `json:"repo,omitempty" jsonschema:"production GitHub repo (owner/repo), saved for next time"`
}

type mcpPublishArgs struct {
	Name     string `json:"name,omitempty" jsonschema:"app name (default: the current app)"`
	Apihost  string `json:"apihost,omitempty" jsonschema:"production OpenServerless apihost"`
	User     string `json:"user,omitempty" jsonschema:"production user"`
	Password string `json:"password,omitempty" jsonschema:"production password"`
}

type mcpLogsArgs struct {
	Lines int `json:"lines,omitempty" jsonschema:"how many trailing lines (default 200, max 2000)"`
}

func appOrCurrent(name string) (string, *mcp.CallToolResult) {
	if name == "" {
		return currentAppOrError()
	}
	if bad := validAppName(name); bad != nil {
		return "", bad
	}
	return name, nil
}

func registerMCPLifecycleTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "apps_list", Description: "List the Trustant apps (name, GitHub repo, production settings) and which one is currently launched."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ mcpNoArgs) (*mcp.CallToolResult, any, error) {
			r, err := callAPI(ctx, req, http.MethodGet, "/api/repo", nil, false)
			if err != nil {
				return nil, nil, err
			}
			if r.failed() {
				return apiToolResult(r), nil, nil
			}
			current, _ := readCurrentApp()
			return mcpText(map[string]any{"current": current, "apps": r.Body}), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "app_create", Description: "Create a new Trustant app from a GitHub repository (owner/repo). Set source to 'lovable' to import a Lovable project."},
		func(ctx context.Context, req *mcp.CallToolRequest, a mcpCreateArgs) (*mcp.CallToolResult, any, error) {
			if bad := validAppName(a.Name); bad != nil {
				return bad, nil, nil
			}
			body := map[string]any{"name": a.Name, "repo": a.Repo}
			if a.Templates != "" {
				body["templates"] = a.Templates
			}
			if a.Source != "" {
				body["source"] = a.Source
			}
			r, err := callAPI(ctx, req, http.MethodPost, "/api/repo", body, false)
			if err != nil {
				return nil, nil, err
			}
			return apiToolResult(r), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "app_launch", Description: "Launch an app: check it out into the workbench, regenerate its config and MCP servers, and start its dev server. The app becomes current. Takes up to a few minutes; progress is reported."},
		func(ctx context.Context, req *mcp.CallToolRequest, a mcpAppArgs) (*mcp.CallToolResult, any, error) {
			if bad := validAppName(a.Name); bad != nil {
				return bad, nil, nil
			}
			r, err := callAPI(ctx, req, http.MethodGet, "/api/launch/"+a.Name, nil, true)
			if err != nil {
				return nil, nil, err
			}
			res := apiToolResult(r)
			if !res.IsError {
				mcpProxies.maybeSync()
				res = mcpText(map[string]any{"result": r.Body, "output": r.Output, "urls": currentAppURLs(a.Name),
					"next": "open urls.split in your browser pane (app on top, ACP below); edit with files_*; use <server>__<tool> proxied tools"})
			}
			return res, nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "app_stop", Description: "Stop the running app (dev server and agent runtime)."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ mcpNoArgs) (*mcp.CallToolResult, any, error) {
			r, err := callAPI(ctx, req, http.MethodDelete, "/api/launch", nil, false)
			if err != nil {
				return nil, nil, err
			}
			mcpProxies.maybeSync()
			return apiToolResult(r), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "app_status", Description: "Status of the current app: name, git status, URLs to open in a browser, and the state of its proxied MCP servers."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ mcpNoArgs) (*mcp.CallToolResult, any, error) {
			app, bad := currentAppOrError()
			if bad != nil {
				return bad, nil, nil
			}
			status := map[string]any{"current": app, "urls": currentAppURLs(app), "mcp_servers": mcpProxies.statusSnapshot()}
			if r, err := callAPI(ctx, req, http.MethodGet, "/api/git/status/"+app, nil, false); err == nil {
				status["git"] = r.Body
			}
			return mcpText(status), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "app_view", Description: "How to see the current app: its URLs (urls.split = app on top, ACP below, no Trustant frame; urls.app; urls.acp) plus the HTTP status and title of the dev server's home page. Open the URLs in your browser pane to look at or screenshot the app."},
		func(ctx context.Context, req *mcp.CallToolRequest, _ mcpNoArgs) (*mcp.CallToolResult, any, error) {
			app, bad := currentAppOrError()
			if bad != nil {
				return bad, nil, nil
			}
			view := map[string]any{"current": app, "urls": currentAppURLs(app)}
			status, title, err := probeDevServer(ctx)
			if err != nil {
				view["dev_server"] = "not answering: " + err.Error()
			} else {
				view["dev_server"] = map[string]any{"status": status, "title": title}
			}
			return mcpText(view), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "app_logs", Description: "Tail of the current app's dev server / ops ide devel log."},
		func(ctx context.Context, req *mcp.CallToolRequest, a mcpLogsArgs) (*mcp.CallToolResult, any, error) {
			app, bad := currentAppOrError()
			if bad != nil {
				return bad, nil, nil
			}
			n := a.Lines
			if n <= 0 {
				n = 200
			}
			if n > 2000 {
				n = 2000
			}
			path, err := opsDevelLogPath(app)
			if err != nil {
				return mcpError("%s", err), nil, nil
			}
			lines, err := tailFile(path, n)
			if err != nil {
				return mcpError("no log yet for %s: %s", app, err), nil, nil
			}
			return mcpText(strings.Join(lines, "\n")), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "app_save", Description: "Commit the app's workbench changes into its Trustant repository (the UI's Save)."},
		func(ctx context.Context, req *mcp.CallToolRequest, a mcpSaveArgs) (*mcp.CallToolResult, any, error) {
			app, bad := appOrCurrent(a.Name)
			if bad != nil {
				return bad, nil, nil
			}
			msg := a.Message
			if msg == "" {
				msg = "Saved from MCP"
			}
			r, err := callAPI(ctx, req, http.MethodPost, "/api/git/save", map[string]any{"name": app, "message": msg}, false)
			if err != nil {
				return nil, nil, err
			}
			return apiToolResult(r), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "app_push", Description: "Push the app's saved commits to its production GitHub repo. If the repo is not configured the result says needs_config: call again with repo."},
		func(ctx context.Context, req *mcp.CallToolRequest, a mcpPushArgs) (*mcp.CallToolResult, any, error) {
			app, bad := appOrCurrent(a.Name)
			if bad != nil {
				return bad, nil, nil
			}
			body := map[string]any{"name": app}
			if a.Repo != "" {
				body["repo"] = a.Repo
			}
			r, err := callAPI(ctx, req, http.MethodPost, "/api/publish/push", body, true)
			if err != nil {
				return nil, nil, err
			}
			return apiToolResult(r), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "app_deploy", Description: "Deploy the app to the local (development) OpenServerless."},
		func(ctx context.Context, req *mcp.CallToolRequest, a mcpPushArgs) (*mcp.CallToolResult, any, error) {
			app, bad := appOrCurrent(a.Name)
			if bad != nil {
				return bad, nil, nil
			}
			r, err := callAPI(ctx, req, http.MethodPost, "/api/git/deploy", map[string]any{"name": app}, true)
			if err != nil {
				return nil, nil, err
			}
			return apiToolResult(r), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "app_publish", Description: "Publish the app to a production OpenServerless. Requires a valid Trustant license covering the apihost; missing settings come back as needs_config."},
		func(ctx context.Context, req *mcp.CallToolRequest, a mcpPublishArgs) (*mcp.CallToolResult, any, error) {
			app, bad := appOrCurrent(a.Name)
			if bad != nil {
				return bad, nil, nil
			}
			body := map[string]any{"name": app}
			for k, v := range map[string]string{"apihost": a.Apihost, "user": a.User, "password": a.Password} {
				if v != "" {
					body[k] = v
				}
			}
			r, err := callAPI(ctx, req, http.MethodPost, "/api/publish/remote", body, true)
			if err != nil {
				return nil, nil, err
			}
			return apiToolResult(r), nil, nil
		})
}

// devServerAddr is where the user app's Vite dev server listens; the
// vite.<domain> host is a reverse proxy to it (middleware.go).
var devServerAddr = "http://127.0.0.1:5173/"

var titlePattern = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

func probeDevServer(ctx context.Context) (int, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, devServerAddr, nil)
	if err != nil {
		return 0, "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	title := ""
	if m := titlePattern.FindSubmatch(data); m != nil {
		title = strings.TrimSpace(string(m[1]))
	}
	return resp.StatusCode, title, nil
}

func tailFile(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return lines, sc.Err()
}

// ---------------------------------------------------------------------------
// File tools
// ---------------------------------------------------------------------------

// mcpMaxWriteSize bounds files_write; it is for source files, not assets.
const mcpMaxWriteSize = 5 << 20

// mcpReadOnlyPaths are regenerated by launch or belong to its contract
// (spec/13-gitignore.md); an external agent editing them would be overwritten
// on the next launch or break the app's MCP/env wiring.
var mcpReadOnlyPaths = map[string]bool{
	".mcp.json":                   true,
	".env":                        true,
	".env.production":             true,
	".openserverless-contract.md": true,
	"CLAUDE.md":                   true,
	".claude":                     true,
}

var mcpReadOnlyPrefixes = []string{".git/", ".acp-data/", ".claude/"}

func mcpReadOnly(rel string) bool {
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean == ".git" || mcpReadOnlyPaths[clean] {
		return true
	}
	for _, p := range mcpReadOnlyPrefixes {
		if strings.HasPrefix(clean, p) {
			return true
		}
	}
	return false
}

// resolveWritablePath is resolveWorkbenchPath (lexical + symlink containment)
// plus a check on the deepest existing ancestor: a missing file under a
// symlinked directory that points outside the workbench must not be created.
func resolveWritablePath(root, rel string) (string, error) {
	target, err := resolveWorkbenchPath(root, rel)
	if err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", errInvalidPath
	}
	dir := filepath.Dir(target)
	for {
		if _, err := os.Lstat(dir); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errInvalidPath
		}
		dir = parent
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil || !withinRoot(realRoot, realDir) {
		return "", errInvalidPath
	}
	if target == realRoot || filepath.Clean(target) == filepath.Clean(root) {
		return "", errInvalidPath
	}
	return target, nil
}

type mcpListArgs struct {
	Path string `json:"path,omitempty" jsonschema:"only entries under this relative directory"`
}

type mcpPathArgs struct {
	Path string `json:"path" jsonschema:"path relative to the app root"`
}

type mcpWriteArgs struct {
	Path    string `json:"path" jsonschema:"path relative to the app root; parent folders are created"`
	Content string `json:"content" jsonschema:"the full new file content"`
}

type mcpDeleteArgs struct {
	Path      string `json:"path" jsonschema:"path relative to the app root"`
	Recursive bool   `json:"recursive,omitempty" jsonschema:"delete a non-empty folder"`
}

type mcpSearchArgs struct {
	Pattern string `json:"pattern" jsonschema:"text to find (a regular expression when regex is true)"`
	Path    string `json:"path,omitempty" jsonschema:"only search under this relative directory"`
	Regex   bool   `json:"regex,omitempty" jsonschema:"treat pattern as a Go regular expression"`
}

const mcpMaxSearchMatches = 200

func registerMCPFileTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "files_list", Description: "List the current app's files and folders (dependency and build folders are skipped)."},
		func(ctx context.Context, req *mcp.CallToolRequest, a mcpListArgs) (*mcp.CallToolResult, any, error) {
			app, bad := currentAppOrError()
			if bad != nil {
				return bad, nil, nil
			}
			r, err := callAPI(ctx, req, http.MethodGet, "/api/files/"+app, nil, false)
			if err != nil {
				return nil, nil, err
			}
			if r.failed() || a.Path == "" {
				return apiToolResult(r), nil, nil
			}
			var listing struct {
				Files     []FileEntry `json:"files"`
				Truncated bool        `json:"truncated"`
			}
			if json.Unmarshal(r.Body, &listing) != nil {
				return apiToolResult(r), nil, nil
			}
			prefix := strings.TrimSuffix(filepath.ToSlash(filepath.Clean(a.Path)), "/") + "/"
			filtered := []FileEntry{}
			for _, e := range listing.Files {
				if strings.HasPrefix(e.Path, prefix) {
					filtered = append(filtered, e)
				}
			}
			return mcpText(map[string]any{"files": filtered, "truncated": listing.Truncated}), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "files_read", Description: "Read a text file of the current app."},
		func(ctx context.Context, req *mcp.CallToolRequest, a mcpPathArgs) (*mcp.CallToolResult, any, error) {
			app, bad := currentAppOrError()
			if bad != nil {
				return bad, nil, nil
			}
			r, err := callAPI(ctx, req, http.MethodGet, "/api/files/"+app+"?path="+url.QueryEscape(a.Path), nil, false)
			if err != nil {
				return nil, nil, err
			}
			if r.failed() {
				return apiToolResult(r), nil, nil
			}
			var file struct {
				Content string `json:"content"`
				Binary  bool   `json:"binary"`
			}
			if json.Unmarshal(r.Body, &file) == nil && !file.Binary {
				return mcpText(file.Content), nil, nil
			}
			return apiToolResult(r), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "files_write", Description: "Create or overwrite a file of the current app with the given content. Launch-managed files (.mcp.json, .env*, CLAUDE.md, ...) are read-only; in AGENTS.md and .gitignore the Trustant-managed block is always preserved."},
		func(ctx context.Context, req *mcp.CallToolRequest, a mcpWriteArgs) (*mcp.CallToolResult, any, error) {
			app, bad := currentAppOrError()
			if bad != nil {
				return bad, nil, nil
			}
			if mcpReadOnly(a.Path) {
				return mcpError("%s is managed by Trustant launch and read-only", a.Path), nil, nil
			}
			if len(a.Content) > mcpMaxWriteSize {
				return mcpError("content exceeds %d bytes", mcpMaxWriteSize), nil, nil
			}
			root := filepath.Join(WorkbenchDir, app)
			target, err := resolveWritablePath(root, a.Path)
			if err != nil {
				return mcpError("%s: %s", a.Path, err), nil, nil
			}
			content := a.Content
			switch filepath.ToSlash(filepath.Clean(a.Path)) {
			case "AGENTS.md":
				content = mergeManagedAppAgents(content)
			case ".gitignore":
				content = mergeManagedGitignore(content)
			}
			mode := os.FileMode(0644)
			if info, err := os.Stat(target); err == nil {
				if info.IsDir() {
					return mcpError("%s is a folder", a.Path), nil, nil
				}
				mode = info.Mode().Perm()
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return mcpError("%s", err), nil, nil
			}
			if err := os.WriteFile(target, []byte(content), mode); err != nil {
				return mcpError("%s", err), nil, nil
			}
			return mcpText(fmt.Sprintf("wrote %s (%d bytes)", a.Path, len(content))), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "files_delete", Description: "Delete a file (or, with recursive, a folder) of the current app."},
		func(ctx context.Context, req *mcp.CallToolRequest, a mcpDeleteArgs) (*mcp.CallToolResult, any, error) {
			app, bad := currentAppOrError()
			if bad != nil {
				return bad, nil, nil
			}
			if mcpReadOnly(a.Path) || filepath.ToSlash(filepath.Clean(a.Path)) == "AGENTS.md" || filepath.ToSlash(filepath.Clean(a.Path)) == ".gitignore" {
				return mcpError("%s is managed by Trustant launch and cannot be deleted", a.Path), nil, nil
			}
			root := filepath.Join(WorkbenchDir, app)
			target, err := resolveWritablePath(root, a.Path)
			if err != nil {
				return mcpError("%s: %s", a.Path, err), nil, nil
			}
			info, err := os.Lstat(target)
			if err != nil {
				return mcpError("%s: not found", a.Path), nil, nil
			}
			if info.IsDir() && a.Recursive {
				err = os.RemoveAll(target)
			} else {
				err = os.Remove(target)
			}
			if err != nil {
				return mcpError("%s", err), nil, nil
			}
			return mcpText("deleted " + a.Path), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "files_search", Description: "Search the current app's text files; returns path:line: text matches (max 200)."},
		func(ctx context.Context, req *mcp.CallToolRequest, a mcpSearchArgs) (*mcp.CallToolResult, any, error) {
			app, bad := currentAppOrError()
			if bad != nil {
				return bad, nil, nil
			}
			if a.Pattern == "" {
				return mcpError("pattern is required"), nil, nil
			}
			match := func(line string) bool { return strings.Contains(line, a.Pattern) }
			if a.Regex {
				re, err := regexp.Compile(a.Pattern)
				if err != nil {
					return mcpError("invalid regex: %s", err), nil, nil
				}
				match = re.MatchString
			}
			root := filepath.Join(WorkbenchDir, app)
			start := root
			if a.Path != "" {
				p, err := resolveWorkbenchPath(root, a.Path)
				if err != nil {
					return mcpError("%s: %s", a.Path, err), nil, nil
				}
				start = p
			}
			matches, truncated := searchFiles(ctx, root, start, match)
			return mcpText(map[string]any{"matches": matches, "truncated": truncated}), nil, nil
		})
}

func searchFiles(ctx context.Context, root, start string, match func(string) bool) ([]string, bool) {
	matches := []string{}
	truncated := false
	filepath.WalkDir(start, func(path string, d fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path != start && skippedDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxViewableFileSize {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || isBinary(data) {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		for i, line := range strings.Split(string(data), "\n") {
			if match(line) {
				if len(matches) >= mcpMaxSearchMatches {
					truncated = true
					return fs.SkipAll
				}
				if len(line) > 300 {
					line = line[:300] + "…"
				}
				matches = append(matches, fmt.Sprintf("%s:%d: %s", filepath.ToSlash(rel), i+1, line))
			}
		}
		return nil
	})
	return matches, truncated
}

// ---------------------------------------------------------------------------
// Resources and prompt
// ---------------------------------------------------------------------------

var mcpSkillNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func markdownResource(uri, text string) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "text/markdown", Text: text}}}
}

func registerMCPResources(s *mcp.Server) {
	static := []struct {
		uri, name, desc string
		text            *string
	}{
		{"trustant://instructions/openserverless", "openserverless-instructions", "How to build full-stack OpenServerless apps in Trustant (read first).", &openserverlessInstructionsMd},
		{"trustant://instructions/contract", "openserverless-contract", "The source contract every Trustant app must satisfy.", &openserverlessContractMd},
		{"trustant://instructions/app-agents", "app-agents", "Agent rules for Trustant apps (the managed AGENTS.md block).", &appAgentsMd},
	}
	for _, r := range static {
		text := r.text
		uri := r.uri
		s.AddResource(&mcp.Resource{URI: uri, Name: r.name, Description: r.desc, MIMEType: "text/markdown"},
			func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				return markdownResource(uri, *text), nil
			})
	}

	s.AddResource(&mcp.Resource{URI: "trustant://app/agents", Name: "app-agents-md", Description: "The current app's AGENTS.md (managed block plus app-local notes).", MIMEType: "text/markdown"},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			app, err := readCurrentApp()
			if err != nil || app == "" {
				return nil, mcp.ResourceNotFoundError("trustant://app/agents")
			}
			data, err := os.ReadFile(filepath.Join(WorkbenchDir, app, "AGENTS.md"))
			if err != nil {
				return nil, mcp.ResourceNotFoundError("trustant://app/agents")
			}
			return markdownResource("trustant://app/agents", string(data)), nil
		})

	s.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: "trustant://app/skills/{name}", Name: "app-skill", Description: "A skill of the current app (.agents/skills/<name>/SKILL.md). List them with files_list path=.agents/skills.", MIMEType: "text/markdown"},
		func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			uri := req.Params.URI
			name := strings.TrimPrefix(uri, "trustant://app/skills/")
			app, err := readCurrentApp()
			if err != nil || app == "" || !mcpSkillNamePattern.MatchString(name) || name == "." || name == ".." {
				return nil, mcp.ResourceNotFoundError(uri)
			}
			data, err := os.ReadFile(filepath.Join(WorkbenchDir, app, ".agents", "skills", name, "SKILL.md"))
			if err != nil {
				return nil, mcp.ResourceNotFoundError(uri)
			}
			return markdownResource(uri, string(data)), nil
		})

	s.AddPrompt(&mcp.Prompt{Name: "build_fullstack_app", Description: "Instructions for building a full-stack OpenServerless app with Trustant.",
		Arguments: []*mcp.PromptArgument{{Name: "goal", Description: "what the app should do"}}},
		func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			goal := ""
			if req.Params != nil {
				goal = req.Params.Arguments["goal"]
			}
			var b strings.Builder
			b.WriteString(mcpInstructions)
			b.WriteString("\n\n# OpenServerless instructions\n\n")
			b.WriteString(openserverlessInstructionsMd)
			b.WriteString("\n\n# Source contract\n\n")
			b.WriteString(openserverlessContractMd)
			if goal != "" {
				b.WriteString("\n\n# Goal\n\n" + goal + "\n")
			}
			return &mcp.GetPromptResult{
				Description: "Build a full-stack OpenServerless app with Trustant",
				Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: b.String()}}},
			}, nil
		})
}

// sortedKeys is a small helper shared with mcpproxy.go.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
