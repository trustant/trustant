This file describes the terminal feature.
Put the backend code in `terminal.go`.

The terminal is a **real shell** attached to a PTY, reachable from a toolbar
button. It comes in two shapes, served by the same handler:

- **Per app** — from the workbench toolbar, running in that app's checkout.
- **Global** — from the app-list toolbar, running in `$WORKBENCH_DIR` itself.
  The app list needs this shape because an app has **no workbench until it is
  launched**, so a per-app terminal there would be unavailable for most cards.

# API

## GET /api/terminal/\<name\> (WebSocket)

Upgrades to a WebSocket and attaches it to a PTY-backed shell.

Preamble, matching every other handler:

1. `expiredGuard`
2. `namePattern.MatchString(name)` — otherwise 400 `Invalid app name`
3. `$WORKBENCH_DIR/<name>` must exist — otherwise 404 `Workbench not found`
4. origin check — `Origin` must fall under the domain the request arrived on
   (direct), or carry a valid routing label when the request came through the
   proxy, otherwise 403 `Forbidden origin`

Then:

- `exec.Command(shell, "-i")` with `cmd.Dir = $WORKBENCH_DIR/<name>`, started
  with `pty.Start`. See **Shell selection** below for how `shell` is chosen.
- **The working directory is server-chosen.** The client never sends a path, so
  the browser cannot turn shell access into arbitrary-directory access.
- The process gets its own process group (`Setpgid`), so teardown can reap
  children the shell spawned — the same discipline `launch.go` applies via `pgid`.
  Some hardened environments deny that `fork/exec` outright ("operation not
  permitted"); rather than leave the terminal unusable there, `startTerminalShell`
  retries without `Setpgid`. The downgrade is logged **once** per process, not
  per session — it is a fixed property of the environment, not an error. `pty.Start` calls `setsid()`
  regardless, so the shell still leads its own group either way — teardown
  confirms `pgid == pid` before signalling the group, so it can never signal the
  server's own group.
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

## GET /api/terminal/ (WebSocket, no name)

The **global terminal**. Identical in every respect except the directory and the
name check:

- The name is empty, so `namePattern` is not applied — there is nothing to
  validate.
- `cmd.Dir = $WORKBENCH_DIR` itself. The existence check still runs against that
  path: a missing `WORKBENCH_DIR` is a real error and must 404 rather than
  dropping the shell somewhere else.
- The session key is the reserved string `<global workbench>`. `namePattern`
  requires a leading letter and 6-20 alphanumerics, so the spaces and angle
  brackets make it **unreachable as an application name** — a crafted request
  cannot evict the app-list shell, nor an app terminal be evicted by it. The
  one-terminal-per-key rule then applies to the global shell exactly as it does
  per app.
- Every gate below applies unchanged. This is not an escalation: the shell
  already ran as the server user with the same scrubbed environment, and the
  working directory stays **server-chosen**.

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
   CORS. `terminalSameOrigin` re-checks `Origin` so a third-party page cannot
   open a shell in a browser that holds a valid session. A request with no
   `Origin` header at all is a non-browser client (tests, curl) and is allowed;
   browsers always send it cross-origin.

   There are three request shapes, and the check accepts the first two:

   1. **Direct** — a browser on `trustable.<ip>.nip.io:8910` reaches the server
      with no proxy in between, so `Origin` and `Host` agree and are compared
      directly by `originSharesDomain`. That helper accepts the host itself and
      any label beneath it, then climbs **at most one label** and repeats, so a
      request arriving on `trustable.<domain>` also accepts a sibling
      `vite.<domain>` or the bare `<domain>`. The climb is refused when the
      remainder is not itself dotted, so `trustable.miniops.me` widens to
      `miniops.me` but never to `.me`. Every suffix test requires a non-empty
      label before a literal leading dot, rejecting `miniops.me.evil.com`,
      `notminiops.me` and `.miniops.me`.

   2. **Proxied** — port 80 is the cluster LoadBalancer, so every request there
      goes through nginx, which rewrites `Host` to `<label>.miniops.me` so the
      ingress rules match (`ensure_openserverless_proxy` in `start.sh`). It leaves
      `Origin` alone, and **cannot** do otherwise: the browser computes `Origin`
      from the address bar, and the terminal socket is built from
      `location.host` (`web/app.html`, `web/applist.html`). `Host` and `Origin`
      therefore never agree, and the hostname the user typed survives *only*
      inside `Origin` — the header being validated.

      So on a proxied request only the Origin's **first label** is checked, by
      `isRoutingLabelHost`, against the same routing labels
      `hostnameMiddleware` accepts (`routingLabels` in `middleware.go` — shared,
      so the router and this check cannot drift). A label still needs a real
      dotted domain under it, which rejects a bare `trustable` or a
      trailing-dot name.

      A request is known to be proxied by `r.Host` being under `miniops.me`.
      nginx is what sets that, so a client connecting directly cannot produce
      it.

   3. **Hostile** — anything else, rejected.

   Each half was tried alone and each broke a case: comparing `r.Host` only
   rejected every upgrade behind the proxy, and pinning to `miniops.me` only
   rejected local development on an `nip.io` name. Preserving `Host` at the
   proxy is not an option, since translating the hostname is the proxy's entire
   purpose.

   **Trade-off.** On a proxied request the Origin's *domain* is not checked,
   only its label — nginx has already discarded the domain the user typed, so
   there is nothing left to compare it against. A page on any domain that can
   reach the proxy could therefore open a terminal socket in a browser holding a
   valid session. This is the same class of trust the earlier unconditional
   `miniops.me` acceptance granted, and it is now at least confined to requests
   that genuinely arrived through the proxy: a direct connection no longer
   accepts a `miniops.me` Origin at all.

   **The recorded tightening**, if this is later judged too loose: have nginx
   send `proxy_set_header X-Forwarded-Host $host` and trust it *only* when
   `r.Host` is `*.miniops.me`. That restores the one fact the rewrite destroys
   and makes the domain checkable again. It is not required for correctness —
   the label rule above is sufficient — and it was not taken because it needs a
   ConfigMap change deployed by `ensure_openserverless_proxy`. Note the header is
   client-settable, which is why the `r.Host` condition is load-bearing: a
   request reaching `trustable-svc:8910` directly could otherwise forge it on a
   remote-shell endpoint.

   Independent of all this, a request with no `Origin` header at all is a
   non-browser client (tests, curl) and is allowed; browsers always send it
   cross-origin.

## Shell selection

The terminal opens the shell the user has **configured**, resolved in order,
first usable candidate winning:

1. **`$SHELL`** — the user's explicit choice for this process.
2. **The passwd entry** for the current uid — field 7 of `/etc/passwd`.
3. **`/bin/sh`** — the last resort, assumed to exist.

`$SHELL` alone is not enough, and relying on it was a bug. `$SHELL` is exported
by a *login* shell, but the server is normally started by an init script rather
than a login, so in the deployed image it is empty while `/etc/passwd` records
the real shell:

```
$ kubectl exec -n openserverless trustable-0 -c trustable -- sh -c 'echo "SHELL=$SHELL"; getent passwd "$(id -u)"'
SHELL=
root:x:0:0:root:/root:/bin/bash
```

Falling straight through to `/bin/sh` there gives the user **dash** on Debian —
no history, no completion, a bare `$` prompt — even though bash is both
installed and configured. passwd is the authoritative record of what the user
configured, so it is consulted before the fallback.

`/etc/passwd` is parsed directly rather than through `os/user`, which has no
shell accessor and whose cgo-backed resolver would break the `CGO_ENABLED=0`
cross-compile in `build.sh --buildx`. Malformed and comment lines are skipped
rather than treated as errors, matching `getpwuid`. The path is a variable so
tests can use a fixture instead of the host's real database.

Every candidate is validated before use (`usableShell`): it must be an absolute
path to an existing, executable, non-directory file. A stale `$SHELL` or a
passwd entry naming a removed interpreter therefore falls through to the next
candidate instead of failing the terminal at spawn time.

**`nologin`, `false`, `true` and `sync` are skipped, not honoured.** A passwd
entry naming one of these is a refusal to grant a shell rather than a shell;
exec'ing it would open a terminal that prints a message or exits immediately,
which is worse than the `/bin/sh` fallback. Matching is on the base name, since
the path varies (`/sbin/nologin`, `/usr/sbin/nologin`). Note this means the
terminal deliberately does not honour that particular OS-level setting — it is
not an access-control gate, and it is not one here either: the three gates above
are what authorize the terminal.

The resolved shell is logged **once** per process (`resolvedShellOnce`), since
it is a fixed property of the environment rather than a per-session event, and a
terminal opening the wrong shell is otherwise only diagnosable by rebuilding.

The shell is started **interactive (`-i`) but not login (`-l`)**, deliberately.
`-i` reads `~/.bashrc` (and `~/.zshrc` for zsh), which is what a user expects of
a terminal pane; adding `-l` would also source the profile files but changes
PATH handling and tends to surprise. Recorded here so the choice is deliberate
rather than accidental.

The shell is user-visible, so the environment is scrubbed before it is handed
over: `OPENAI_API_KEY`, `OPENAI_BASE_URL`, the `TRUSTABLE_AUTH_*` secrets, and
GitHub tokens are removed. `TERM=xterm-256color` and `PWD` are set.

## Working directory, and the image's `.bashrc`

Two mechanisms place the shell:

- **`cmd.Dir`**, set by the server, is authoritative. It is
  `$WORKBENCH_DIR/<name>` for a per-app terminal and `$WORKBENCH_DIR` itself for
  the global one, and `terminalEnvironment` exports a matching `PWD`.
- **`.bashrc`** in the image (`image/Dockerfile`) then `cd`s into the currently
  launched app's checkout, read from `$WORKBENCH_DIR/current` — the file
  `launch.go`'s `writeCurrentApp` rewrites on every launch. This is what the
  **global** terminal needs, since its `cmd.Dir` is the workbench root; for a
  per-app terminal it resolves to the directory the shell is already in.

The `.bashrc` block is deliberately silent when it cannot place the shell: no
launched app (the normal state on a fresh pod), an empty `current`, or a
`current` naming an app whose checkout has since been removed. In each case the
shell simply stays in `cmd.Dir`. It also trims the file, which `writeCurrentApp`
writes untrimmed, and guards the target directory before `cd`-ing.

This block regressed once, and the failure is worth recording because nothing
else in the build would catch it. The original was a single line that guarded on
the absolute `$HOME/workbench/current` but read the **relative**
`workbench/current`. Relative to `PWD`, which the server sets to `cmd.Dir` — so
a per-app terminal looked for `$WORKBENCH_DIR/<name>/workbench/current` and every
terminal opened with a `cat: workbench/current: No such file or directory`
banner. It then used `export PWD=`, which assigns a variable the shell itself
maintains and overwrites on the next `cd` rather than changing directory, so the
prompt advertised a directory the shell was not in. Both routes were affected.
It is shell embedded in a Dockerfile, so nothing compiles it and no test ran it;
`dockerfile_test.go` now asserts on its content the way `setup_test.go` and
`screenshot_script_test.go` guard their scripts.

Note that a change here ships only with a **full `./build.sh --build`**.
`hotfix.sh` layers the binary plus `image/start.sh`, `image/env` and
`trustant.json` onto the existing image and never re-runs the Dockerfile stage
that writes `.bashrc`.

The publishing-auth gap tracked as #91 is **not** addressed here and must not be
assumed to protect this route.

# Frontend

## `web/app.html` — the per-app pane

Wired in the same inline script as the rest of the page.

- A **Terminal** button in the top bar's left group, immediately before the
  Config button, using the existing `nu-btn` primitives. It toggles the pane and
  shows an active state (`nu-btn-primary` while open).
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

## `web/applist.html` — the global modal

The app list has no split layout to shrink, so the global terminal is a
**modal** rather than a resizable pane:

- A **Terminal** button in the top bar, before Configure, using the same
  `nu-btn` primitives and terminal icon as `app.html`. It goes
  `nu-btn-primary` while the modal is open.
- The modal uses the page's existing `nu-modal` / `nu-modal-scrim` primitives at
  `max-w-4xl` and `60vh`, with the xterm host on a black background.
- Connect / data / resize / `[session ended]` behaviour is identical to the pane,
  against `/api/terminal/` with **no name** — the client never sends a path.
- Escape, the Close button and a backdrop click all close the modal **and the
  socket**; closing terminates the shell, the same contract as the pane. The
  Escape handler returns early so closing the terminal does not also dismiss the
  page's other modals.
- The same vendored xterm assets are loaded; no new dependency.

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
- covers both deployment shapes: direct access on `trustable.<ip>.nip.io:8910`
  where `Origin` and `Host` agree, and the proxied case where `Host` was
  rewritten to `<label>.miniops.me` while `Origin` kept the hostname the user
  typed (the reported bug)
- accepts sibling labels and the bare domain under the host the request
  arrived on (`vite.<domain>`, `<domain>`)
- proxied: accepts `trustable.`/`vite.`/`opencode.` over any domain, rejects an
  unroutable label (`evil.<ip>.nip.io`), a bare label and a trailing-dot name
- the proxied relaxation does not leak into the direct case: on an `nip.io`
  Host, `trustable.evil.com` and `trustable.miniops.me` are both rejected
- rejects the suffix-match traps `miniops.me.evil.com`, `notminiops.me` and
  `.miniops.me`, an unrelated `<ip>.nip.io`, and `evil.me` — proving the climb
  strips at most one label and never widens to a TLD
- a spawned shell reports `[ -t 0 ]` → yes (assert the TTY directly; this is the
  regression that motivated the PTY)
- shell resolution: `$SHELL` wins when usable; an empty `$SHELL` falls back to
  the passwd entry (the reported bug); a stale, relative or non-executable
  `$SHELL` falls through rather than being used; a `nologin`/`false` passwd
  entry is skipped; nothing usable → `/bin/sh`
- `passwdShell` on a missing file or an absent uid returns "" rather than
  erroring, so resolution continues
- a resize control frame reaches `pty.Setsize` (assert via `stty size`)
- closing the socket reaps the process group — no orphan after close
- the shell leads its own process group in both modes (the property that makes
  the `Setpgid` fallback safe)

The last three spawn a real shell. `requirePTYSpawn` skips them only when the
environment cannot start a PTY shell at all; a mere `Setpgid` denial is not a
reason to skip, since the handler falls back the same way.
