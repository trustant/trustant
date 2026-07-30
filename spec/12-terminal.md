This file describes the terminal feature.
Put the backend code in `terminal.go`.

The terminal is a **real shell** attached to a PTY, reachable from a toolbar
button in the workbench. It runs in the current app's workbench directory.

# API

## GET /api/terminal/\<name\> (WebSocket)

Upgrades to a WebSocket and attaches it to a PTY-backed shell.

Preamble, matching every other handler:

1. `expiredGuard`
2. `namePattern.MatchString(name)` — otherwise 400 `Invalid app name`
3. `$WORKBENCH_DIR/<name>` must exist — otherwise 404 `Workbench not found`
4. origin check — otherwise 403 `Forbidden origin`

Then:

- `exec.Command(shell, "-i")` with `cmd.Dir = $WORKBENCH_DIR/<name>`, started
  with `pty.Start`. The shell is `$SHELL`, falling back to `/bin/sh`.
- **The working directory is server-chosen.** The client never sends a path, so
  the browser cannot turn shell access into arbitrary-directory access.
- The process gets its own process group (`Setpgid`), so teardown can reap
  children the shell spawned — the same discipline `launch.go` applies via `pgid`.
- Binary WS frames carry raw PTY bytes in both directions.
- A JSON **text** frame carries resize: `{"type":"resize","cols":N,"rows":N}` →
  `pty.Setsize`. Zero values are ignored.
- On socket close: SIGTERM the process group, then SIGKILL once the shell is
  reaped or `terminalShutdownGrace` (3s) lapses, and close the PTY. The reaping
  `cmd.Wait()` runs in its own goroutine and teardown waits on it — an
  exited-but-unreaped process is a zombie and still answers
  `syscall.Kill(pid, 0)`, so polling that signal would never observe a clean
  exit and would burn the full grace period on every close.
- **One terminal per app.** A second connection for the same `<name>` cancels
  the first rather than silently multiplying shells.

# Security

This endpoint is a remote shell — the most sensitive surface in the app. It is
protected by three independent gates:

1. **Host routing** — `hostnameMiddleware` serves `/api/*` only on the
   `trustable.<domain>` prefix, never on `vite.` or `opencode.`.
2. **Session auth** — `/api/terminal/` is not in `publicAuthPath`, so
   `authManager.middleware` requires a valid signed session whenever
   `TRUSTABLE_AUTH_MODE` is enabled.
3. **Origin check** — a WebSocket upgrade is a `GET`, so it does not reach the
   CSRF branch of `effectfulAuthRequest`, and WebSockets are not subject to
   CORS. `terminalSameOrigin` re-checks `Origin` against the request host so a
   third-party page cannot open a shell in a browser that holds a valid
   session. A request with no `Origin` header at all is a non-browser client
   (tests, curl) and is allowed; browsers always send it cross-origin.

The shell is user-visible, so the environment is scrubbed before it is handed
over: `OPENAI_API_KEY`, `OPENAI_BASE_URL`, the `TRUSTABLE_AUTH_*` secrets, and
GitHub tokens are removed. `TERM=xterm-256color` and `PWD` are set.

The publishing-auth gap tracked as #91 is **not** addressed here and must not be
assumed to protect this route.

# Frontend

`web/app.html`, wired in the same inline script as the rest of the page.

- A **Terminal** button in the top bar, right of the Route button, using the
  existing `nu-btn` primitives. It toggles the pane and shows an active state
  (`nu-btn-primary` while open).
- The body is a vertical column: the existing `leftFrame | divider | rightFrame`
  row on top, then a horizontal divider and the terminal pane **below**. Opening
  the terminal shrinks the iframe row rather than replacing either frame.
- The pane is resizable with the same drag behaviour as the vertical `#divider`.
  Its height persists in `localStorage` under `trustable.terminal.height`.
  **Visibility is not persisted** — closing the pane terminates the shell.
- xterm.js runs in a `<div>`, not an iframe. The existing frames are iframes
  only because they host other origins; a terminal needs no document boundary.
- On open: connect to `/api/terminal/<name>`, `term.onData` → send bytes,
  socket message → `term.write`. `FitAddon` runs on pane and window resize and
  sends the resize control frame so `$COLUMNS`/`$LINES` track the pane.
- Socket close writes a `[session ended]` line rather than leaving a dead black
  box.

# Vendoring

`web/` has **no build step**. `web/js/` holds vendored minified files loaded
with plain `<script>` tags, and xterm.js follows that convention:

- `web/js/xterm.min.js` — `@xterm/xterm@6.0.0` (UMD, exposes `window.Terminal`)
- `web/js/xterm.css` — its stylesheet
- `web/js/xterm-addon-fit.min.js` — `@xterm/addon-fit@0.11.0` (exposes
  `window.FitAddon.FitAddon`)

No npm dependency is added to the frontend.

# Dependencies

`go.mod` was dependency-free before this feature. It now has two, both pure Go
so `build.sh` can keep cross-compiling `linux/amd64` and `linux/arm64` with
`CGO_ENABLED=0`:

- `github.com/creack/pty` — the PTY
- `github.com/coder/websocket` — the WebSocket (this is the current import path
  for the library formerly published as `nhooyr.io/websocket`)

# Tests

`terminal_test.go`:

- invalid name → 400; missing workbench → 404
- rejects a cross-origin upgrade → 403
- a spawned shell reports `[ -t 0 ]` → yes (assert the TTY directly; this is the
  regression that motivated the PTY)
- a resize control frame reaches `pty.Setsize` (assert via `stty size`)
- closing the socket reaps the process group — no orphan after close

The last three need to spawn a shell with `Setpgid`. Sandboxed environments deny
that `fork/exec`, so those tests **skip** via `requirePTYSpawn` rather than
failing misleadingly. They must be run in the dev VM to be meaningful.
