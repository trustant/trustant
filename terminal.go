package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// Terminal implements GET /api/terminal/<name> — a PTY-backed shell attached to
// a WebSocket, scoped to the app's workbench directory. See spec/12-terminal.md.
//
// This endpoint is a remote shell and is the most sensitive surface in the app.
// It is protected by three independent gates:
//
//  1. hostnameMiddleware — reachable only on the trustable.<domain> prefix,
//     never on the vite. or opencode. proxies.
//  2. authManager.middleware — /api/terminal/ is not a publicAuthPath, so a
//     valid signed session is required whenever auth is enabled.
//  3. terminalSameOrigin — a WebSocket upgrade is a GET and therefore skips the
//     CSRF branch in effectfulAuthRequest, and WebSockets are not subject to
//     CORS. The origin is re-checked here so a third-party page cannot open a
//     shell against a browser that holds a valid session.
const (
	// terminalShutdownGrace is how long the shell's process group has to exit
	// after SIGTERM before it is SIGKILLed.
	terminalShutdownGrace = 3 * time.Second
	// terminalWriteTimeout bounds a single frame write to a stalled client.
	terminalWriteTimeout = 10 * time.Second
	// terminalReadLimit caps a control frame; PTY input is small by nature.
	terminalReadLimit = 32 * 1024
)

// terminalControl is the JSON control frame sent by the client to resize the PTY.
type terminalControl struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// terminalSession is one live shell. The pointer identity is the session key,
// so a late-finishing predecessor cannot evict its replacement.
type terminalSession struct {
	cancel context.CancelFunc
}

// terminalSessions tracks the live shell per app name so a second connection
// replaces the first rather than silently multiplying shells.
var terminalSessions = struct {
	sync.Mutex
	active map[string]*terminalSession
}{active: map[string]*terminalSession{}}

// registerTerminalSession installs session for name, cancelling any predecessor.
func registerTerminalSession(name string, session *terminalSession) {
	terminalSessions.Lock()
	previous := terminalSessions.active[name]
	terminalSessions.active[name] = session
	terminalSessions.Unlock()
	if previous != nil && previous.cancel != nil {
		previous.cancel()
	}
}

// releaseTerminalSession removes session for name if it is still the current one.
func releaseTerminalSession(name string, session *terminalSession) {
	terminalSessions.Lock()
	defer terminalSessions.Unlock()
	if terminalSessions.active[name] == session {
		delete(terminalSessions.active, name)
	}
}

// terminalSameOrigin reports whether the upgrade request came from this origin.
// A WebSocket handshake carries Origin but is not subject to CORS, so this is
// the only thing standing between a hostile page and the user's shell.
func terminalSameOrigin(r *http.Request) bool {
	if strings.TrimSpace(r.Header.Get("Origin")) == "" {
		// No Origin at all is a non-browser client (tests, curl). Browsers
		// always send it on a cross-origin upgrade, which is the case we guard.
		return true
	}
	return sameRequestOrigin(r)
}

// handleTerminal handles GET /api/terminal/<name>.
func handleTerminal(w http.ResponseWriter, r *http.Request) {
	if expiredGuard(w) {
		return
	}

	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/terminal/"), "/")
	if name == "" || !namePattern.MatchString(name) {
		http.Error(w, "Invalid app name", http.StatusBadRequest)
		return
	}

	workbenchPath := filepath.Join(WorkbenchDir, name)
	if _, err := os.Stat(workbenchPath); os.IsNotExist(err) {
		http.Error(w, "Workbench not found", http.StatusNotFound)
		return
	}

	if !terminalSameOrigin(r) {
		http.Error(w, "Forbidden origin", http.StatusForbidden)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// The origin is verified above against the request's own host, which is
		// stricter than the library's host list.
		InsecureSkipVerify: true,
	})
	if err != nil {
		// Accept has already written a response.
		log.Printf("terminal: upgrade failed for %s: %v", name, err)
		return
	}
	conn.SetReadLimit(terminalReadLimit)

	if err := runTerminalSession(r.Context(), conn, name, workbenchPath); err != nil {
		log.Printf("terminal: session for %s ended: %v", name, err)
	}
}

// runTerminalSession spawns the PTY-backed shell and pumps bytes until either
// side closes, then tears the process group down.
func runTerminalSession(parent context.Context, conn *websocket.Conn, name, workbenchPath string) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	session := &terminalSession{cancel: cancel}
	registerTerminalSession(name, session)
	defer releaseTerminalSession(name, session)

	cmd, ptmx, err := startTerminalShell(workbenchPath)
	if err != nil {
		conn.Close(websocket.StatusInternalError, "failed to start shell")
		return err
	}

	// Reap the shell as soon as it exits. This must run concurrently with
	// teardown: an exited-but-unreaped process is a zombie and still answers
	// syscall.Kill(pid, 0), so terminateProcessGroup could never observe a
	// clean exit and would always burn the full grace period before SIGKILL.
	waited := make(chan struct{})
	go func() {
		defer close(waited)
		_ = cmd.Wait()
	}()

	var closeOnce sync.Once
	shutdown := func() {
		closeOnce.Do(func() {
			terminateProcessGroup(cmd, waited)
			ptmx.Close()
		})
	}
	defer shutdown()

	// PTY -> socket.
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 32*1024)
		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				writeCtx, cancelWrite := context.WithTimeout(ctx, terminalWriteTimeout)
				writeErr := conn.Write(writeCtx, websocket.MessageBinary, buf[:n])
				cancelWrite()
				if writeErr != nil {
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	// Socket -> PTY. Returns when the client closes or the shell exits.
	go func() {
		<-done
		cancel()
	}()

	err = pumpClientToPTY(ctx, conn, ptmx)
	shutdown()
	<-done
	<-waited

	conn.Close(websocket.StatusNormalClosure, "session ended")
	return err
}

// pumpClientToPTY forwards client frames to the PTY, handling resize controls.
func pumpClientToPTY(ctx context.Context, conn *websocket.Conn, ptmx *os.File) error {
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || websocket.CloseStatus(err) != -1 {
				return nil
			}
			return err
		}

		// A text frame is a JSON control message; binary frames are raw input.
		if typ == websocket.MessageText {
			var control terminalControl
			if json.Unmarshal(data, &control) == nil && control.Type == "resize" {
				applyTerminalResize(ptmx, control)
				continue
			}
			// Not a control frame we understand — treat as input.
		}

		if _, err := ptmx.Write(data); err != nil {
			return err
		}
	}
}

// applyTerminalResize sets the PTY window size, ignoring nonsensical values.
func applyTerminalResize(ptmx *os.File, control terminalControl) {
	if control.Cols == 0 || control.Rows == 0 {
		return
	}
	_ = pty.Setsize(ptmx, &pty.Winsize{Cols: control.Cols, Rows: control.Rows})
}

// startTerminalShell starts the interactive shell on a PTY in workbenchPath.
//
// The shell normally gets its own process group so teardown can reap children
// it spawned — the same discipline launch.go applies via pgid. Some hardened
// environments deny that fork/exec outright ("operation not permitted"), which
// would make the terminal unusable there, so a denial falls back to starting
// without Setpgid. In that mode only the shell itself is signalled on teardown;
// see terminateProcessGroup.
func startTerminalShell(workbenchPath string) (*exec.Cmd, *os.File, error) {
	build := func(setpgid bool) *exec.Cmd {
		cmd := exec.Command(terminalShell(), "-i")
		cmd.Dir = workbenchPath
		cmd.Env = terminalEnvironment(workbenchPath)
		if setpgid {
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		}
		return cmd
	}

	cmd := build(true)
	ptmx, err := pty.Start(cmd)
	if err == nil {
		return cmd, ptmx, nil
	}
	if !errors.Is(err, syscall.EPERM) && !errors.Is(err, syscall.EACCES) {
		return nil, nil, err
	}

	log.Printf("terminal: Setpgid denied (%v) — starting shell in the server's process group", err)
	cmd = build(false)
	ptmx, err = pty.Start(cmd)
	if err != nil {
		return nil, nil, err
	}
	return cmd, ptmx, nil
}

// terminalShell picks the user's shell, falling back to /bin/sh.
func terminalShell() string {
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return shell
	}
	return "/bin/sh"
}

// terminalEnvironment builds the shell environment. The shell is user-visible,
// so provider credentials and auth secrets are stripped rather than inherited.
func terminalEnvironment(workbenchPath string) []string {
	blocked := map[string]bool{
		"OPENAI_API_KEY":         true,
		"OPENAI_BASE_URL":        true,
		authUsernameEnv:          true,
		authPasswordHashEnv:      true,
		authSessionKeyEnv:        true,
		"GITHUB_TOKEN":           true,
		"GH_TOKEN":               true,
		"TRUSTABLE_GITHUB_TOKEN": true,
	}
	environment := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		key, _, found := strings.Cut(entry, "=")
		if found && blocked[key] {
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(environment, "PWD="+workbenchPath, "TERM=xterm-256color")
	return environment
}

// terminateProcessGroup SIGTERMs the shell's process group, then SIGKILLs any
// survivor once the leader is reaped or the grace period lapses, so no orphan
// outlives the socket.
func terminateProcessGroup(cmd *exec.Cmd, reaped <-chan struct{}) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid

	// Signal the whole group only when the shell actually leads its own. If
	// Setpgid was denied it shares the server's group, and signalling that
	// would kill the server — target just the shell instead.
	target := pid
	if pgid, err := syscall.Getpgid(pid); err == nil && pgid == pid {
		target = -pgid
	}

	_ = syscall.Kill(target, syscall.SIGTERM)

	// Wait for the leader to be reaped, then SIGKILL whatever is left in the
	// group. Polling Kill(-pgid, 0) cannot stand in for this: the leader stays
	// visible as a zombie until cmd.Wait() collects it.
	select {
	case <-reaped:
	case <-time.After(terminalShutdownGrace):
	}
	_ = syscall.Kill(target, syscall.SIGKILL)
}
