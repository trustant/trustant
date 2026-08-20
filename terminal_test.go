package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// newTerminalTestServer points WorkbenchDir at a temp dir holding one app and
// returns a live server for /api/terminal/.
func newTerminalTestServer(t *testing.T, appName string) *httptest.Server {
	t.Helper()

	root := t.TempDir()
	if appName != "" {
		if err := os.MkdirAll(filepath.Join(root, appName), 0o755); err != nil {
			t.Fatalf("mkdir workbench: %v", err)
		}
	}

	previous := WorkbenchDir
	WorkbenchDir = root
	t.Cleanup(func() { WorkbenchDir = previous })

	mux := http.NewServeMux()
	mux.HandleFunc("/api/terminal/", handleTerminal)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// requirePTYSpawn skips the test when the environment cannot start a PTY shell
// at all. Setpgid alone being denied is not a reason to skip — startTerminalShell
// falls back to starting without it — so the probe mirrors that fallback.
func requirePTYSpawn(t *testing.T) {
	t.Helper()

	start := func(setpgid bool) error {
		probe := exec.Command("/bin/sh", "-c", "exit 0")
		if setpgid {
			probe.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		}
		file, err := pty.Start(probe)
		if err != nil {
			return err
		}
		file.Close()
		_ = probe.Wait()
		return nil
	}

	if start(true) == nil {
		return
	}
	if err := start(false); err != nil {
		t.Skipf("environment cannot start a PTY shell: %v", err)
	}
}

// dialTerminal opens the terminal WebSocket for name.
func dialTerminal(t *testing.T, server *httptest.Server, name string) (*websocket.Conn, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)

	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/terminal/" + name
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial terminal: %v", err)
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "test done") })
	return conn, ctx
}

// readUntil collects PTY output until substring appears or the deadline passes.
func readUntil(ctx context.Context, conn *websocket.Conn, substring string, deadline time.Duration) (string, bool) {
	readCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	var collected strings.Builder
	for {
		_, data, err := conn.Read(readCtx)
		if err != nil {
			return collected.String(), false
		}
		collected.Write(data)
		if strings.Contains(collected.String(), substring) {
			return collected.String(), true
		}
	}
}

func TestTerminalRejectsInvalidName(t *testing.T) {
	server := newTerminalTestServer(t, "goodapp")

	// "bad" fails namePattern (needs 6-20 chars starting with a letter).
	response, err := http.Get(server.URL + "/api/terminal/bad")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid name: got status %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
}

func TestTerminalMissingWorkbenchReturns404(t *testing.T) {
	server := newTerminalTestServer(t, "goodapp")

	response, err := http.Get(server.URL + "/api/terminal/otherapp")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusNotFound {
		t.Errorf("missing workbench: got status %d, want %d", response.StatusCode, http.StatusNotFound)
	}
}

func TestTerminalRejectsCrossOriginUpgrade(t *testing.T) {
	server := newTerminalTestServer(t, "goodapp")

	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/terminal/goodapp", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Origin", "http://evil.example.com")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin upgrade: got status %d, want %d", response.StatusCode, http.StatusForbidden)
	}
}

// TestTerminalShellHasTTY asserts the regression that motivated moving the
// terminal to a PTY: with pipes, [ -t 0 ] is false and interactive programs
// refuse to run.
func TestTerminalShellHasTTY(t *testing.T) {
	requirePTYSpawn(t)

	server := newTerminalTestServer(t, "goodapp")
	conn, ctx := dialTerminal(t, server, "goodapp")

	if err := conn.Write(ctx, websocket.MessageBinary, []byte("[ -t 0 ] && echo TTY_YES || echo TTY_NO\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	output, found := readUntil(ctx, conn, "TTY_YES", 15*time.Second)
	if !found {
		if strings.Contains(output, "TTY_NO") {
			t.Fatalf("shell has no TTY — stdin is not a terminal; output: %q", output)
		}
		t.Fatalf("did not observe TTY probe result; output: %q", output)
	}
}

// TestTerminalResizeReachesPTY asserts the resize control frame reaches
// pty.Setsize, observed through the shell's own view of the window.
func TestTerminalResizeReachesPTY(t *testing.T) {
	requirePTYSpawn(t)

	server := newTerminalTestServer(t, "goodapp")
	conn, ctx := dialTerminal(t, server, "goodapp")

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":123,"rows":45}`)); err != nil {
		t.Fatalf("write resize: %v", err)
	}
	// Let the resize land before asking the shell what size it sees.
	time.Sleep(300 * time.Millisecond)

	if err := conn.Write(ctx, websocket.MessageBinary, []byte("stty size\n")); err != nil {
		t.Fatalf("write stty: %v", err)
	}

	output, found := readUntil(ctx, conn, "45 123", 15*time.Second)
	if !found {
		t.Fatalf("resize did not reach the PTY; wanted \"45 123\" in output: %q", output)
	}
}

// TestTerminalShellLeadsItsOwnProcessGroup pins the property that makes the
// Setpgid fallback safe: pty.Start calls setsid(), so the shell leads its own
// process group even when Setpgid was denied. If this ever stopped holding,
// terminateProcessGroup's pgid == pid guard would be the only thing standing
// between a teardown and signalling the server's own process group.
func TestTerminalShellLeadsItsOwnProcessGroup(t *testing.T) {
	requirePTYSpawn(t)

	cmd, ptmx, err := startTerminalShell(t.TempDir())
	if err != nil {
		t.Fatalf("start shell: %v", err)
	}
	defer func() {
		ptmx.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	pid := cmd.Process.Pid
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		t.Fatalf("getpgid: %v", err)
	}
	if pgid != pid {
		t.Errorf("shell pgid %d != pid %d — it does not lead its own group, so teardown would signal a group it does not own", pgid, pid)
	}
	if serverPgid, err := syscall.Getpgid(os.Getpid()); err == nil && pgid == serverPgid {
		t.Errorf("shell shares the server's process group %d — teardown would signal the server", serverPgid)
	}
}

// TestTerminalClosingSocketReapsProcessGroup asserts no orphan shell outlives
// the socket.
func TestTerminalClosingSocketReapsProcessGroup(t *testing.T) {
	requirePTYSpawn(t)

	server := newTerminalTestServer(t, "goodapp")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/terminal/goodapp"
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Ask the shell for its own PID so we can watch for an orphan. The PTY
	// echoes the typed command back, so the marker is split across the echo to
	// keep it from matching the echoed line instead of the shell's output.
	if err := conn.Write(ctx, websocket.MessageBinary, []byte(`echo "PID""=$$"`+"\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	output, found := readUntil(ctx, conn, "PID=", 15*time.Second)
	if !found {
		t.Fatalf("never saw shell PID; output: %q", output)
	}

	pid := parseReportedPID(t, output)

	conn.Close(websocket.StatusNormalClosure, "closing")

	// The teardown path SIGTERMs then SIGKILLs; allow for the grace period.
	deadline := time.Now().Add(terminalShutdownGrace + 7*time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return // reaped
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("shell pid %d survived socket close — orphan process group", pid)
}

// parseReportedPID extracts the digits following the last "PID=" marker.
func parseReportedPID(t *testing.T, output string) int {
	t.Helper()

	index := strings.LastIndex(output, "PID=")
	if index < 0 {
		t.Fatalf("no PID marker in output: %q", output)
	}
	digits := strings.Builder{}
	for _, char := range output[index+len("PID="):] {
		if char < '0' || char > '9' {
			break
		}
		digits.WriteRune(char)
	}
	if digits.Len() == 0 {
		t.Fatalf("no PID digits in output: %q", output)
	}

	pid := 0
	for _, char := range digits.String() {
		pid = pid*10 + int(char-'0')
	}
	return pid
}

// The app list opens a terminal with no app name, which must land in
// $WORKBENCH_DIR itself — most apps there have no checkout yet.
func TestGlobalTerminalRunsInWorkbenchRoot(t *testing.T) {
	requirePTYSpawn(t)

	server := newTerminalTestServer(t, "goodapp")
	conn, ctx := dialTerminal(t, server, "")

	if err := conn.Write(ctx, websocket.MessageBinary, []byte("pwd\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The temp root is what newTerminalTestServer set WorkbenchDir to. macOS
	// resolves /var through a symlink, so compare on the base name.
	want := filepath.Base(WorkbenchDir)
	output, found := readUntil(ctx, conn, want, 15*time.Second)
	if !found {
		t.Fatalf("global shell did not start in the workbench root %q; output: %q", WorkbenchDir, output)
	}
}

// A missing WORKBENCH_DIR is a real error and must still 404 rather than
// silently dropping the shell somewhere else.
func TestGlobalTerminalMissingWorkbenchDirReturns404(t *testing.T) {
	server := newTerminalTestServer(t, "goodapp")

	previous := WorkbenchDir
	WorkbenchDir = filepath.Join(previous, "does-not-exist")
	t.Cleanup(func() { WorkbenchDir = previous })

	response, err := http.Get(server.URL + "/api/terminal/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusNotFound {
		t.Errorf("missing workbench dir: got status %d, want %d", response.StatusCode, http.StatusNotFound)
	}
}

// The global session key must not be reachable as an application name, or a
// crafted request could evict the app-list shell (or vice versa).
func TestGlobalTerminalSessionKeyIsUnreachable(t *testing.T) {
	if namePattern.MatchString(globalTerminalSessionKey) {
		t.Fatalf("%q matches namePattern and could collide with an app name", globalTerminalSessionKey)
	}
}
