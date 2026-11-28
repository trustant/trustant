# MCP entry point (`/mcp`)

Trustant exposes one MCP server that lets an external agent (Claude Code,
Claude Desktop, Codex, Pi on the user's machine) drive Trustant end to end:
create apps from repos, launch them, edit the current app's files, use the
app's own MCP servers, save, push and publish. Code: `main/mcpserver.go`
(endpoint, token, tools, resources) and `main/mcpproxy.go` (proxied app MCP
servers). Client setup: `util/mcp.sh` / `util/mcp.ps1`. Issue #252.

## Endpoint

- Path **`/mcp`** on the `trustant.<domain>` host (`:8910`), MCP **Streamable
  HTTP** transport, stateful sessions. Built in the Go binary with
  `github.com/modelcontextprotocol/go-sdk` (pure Go, `CGO_ENABLED=0` safe).
- Every request needs `Authorization: Bearer <token>`. Missing or wrong: 401
  with `WWW-Authenticate: Bearer realm="trustant"`; the comparison is
  constant-time.
- `/mcp` and `/api/v1/token` are public in the session middleware
  (`publicAuthPath` in `auth.go`): they carry their own auth, and an agent
  cannot hold a browser session.
- The SDK's localhost DNS-rebinding protection is disabled: the bearer token
  already defeats rebinding, and the protection would refuse a
  `trustant.127.0.0.1.nip.io` client (WSL).

## Token

The **client owns the token**; the server only stores it.

- Client side: `util/mcp.sh` keeps it in
  `~/Library/Application Support/Trustant/token.mcp` (macOS, next to
  `current.ip`), `${XDG_CONFIG_HOME:-~/.config}/trustant/token.mcp` (Linux/WSL),
  `%APPDATA%\Trustant\token.mcp` (`mcp.ps1`). Generated when missing (32 random
  bytes, hex), mode `0600`.
- Server side: `$WORKSPACE_DIR/token.mcp`, one line, `0600`, written
  atomically. An installer may pre-seed it. It is re-read when its mtime
  changes. If it is missing or empty at first use, the server generates a
  random token so `/mcp` is never open (unusable until a client PUTs its own).
- There is **no `.env` variable** for it.

### `GET /api/v1/token`

`{"set": true, "url": "<scheme>://<host>/mcp", "fingerprint": "<8 hex>"}`.
The fingerprint is the first 8 hex chars of sha256(token). The token itself is
never returned. Used by the Configure page.

### `PUT /api/v1/token`

Body `{"token": "<value>"}`. The value must match `^[A-Za-z0-9_-]{32,256}$`
(400 otherwise). It is written to `token.mcp` and every MCP session is closed
(clients reconnect with the new token). Reply `{"ok": true}`.

Authorization: with local login disabled (`TRUSTANT_AUTH_MODE` unset, the
default) it is as open as every other `trustant.<domain>` API — whoever reaches
that host can already use the terminal and file APIs; the token protects
`/mcp` only. With local login enabled a PUT needs either a UI session with a
valid CSRF header, or `Authorization: Bearer <current token>` (that is how
`util/mcp.sh refresh` rotates).

## Initialize

The `initialize` result carries `instructions`: what Trustant is, the
workflow (apps_list → app_create → app_launch → files_* → app_save →
app_push/app_deploy/app_publish), that the agent looks at the app with its own
browser, and which resources to read first.

## Tools

The lifecycle tools run the existing `/api` handlers **in process**
(`callAPI`: an `http.Request` against `http.DefaultServeMux`, with the Host of
the MCP client), so the UI and the MCP server share one implementation. The
session middleware wraps the mux, not the handlers, so it is not re-applied.
Streaming endpoints are called with `Accept: text/event-stream`: `progress`
events become MCP progress notifications (when the client sent a progress
token), `output` lines are returned (last 200), and the `done`/`error` payload
is the result. A result is an error when the HTTP status is ≥ 400 or the JSON
carries an `error` key (license prefixes `License required:` / `Host not
licensed:` pass through verbatim).

| Tool | Does | API |
|---|---|---|
| `apps_list` | apps + the current one | `GET /api/repo` |
| `app_create` `{name, repo, templates?, source?}` | new app from GitHub `owner/repo` (`source: "lovable"` converts a Lovable repo) | `POST /api/repo` |
| `app_launch` `{name}` | launch; app becomes current; returns result, output, URLs | `GET /api/launch/<name>` (SSE) |
| `app_stop` | stop the running app | `DELETE /api/launch` |
| `app_status` | current app, git status, URLs, proxied server status | `GET /api/git/status/<name>` |
| `app_view` | URLs + HTTP status and `<title>` of the dev server home page | probes `127.0.0.1:5173` |
| `app_logs` `{lines?}` | tail (default 200, max 2000) of `ops-ide-devel.log` | `opsDevelLogPath` |
| `app_save` `{message?, name?}` | commit the workbench | `POST /api/git/save` |
| `app_push` `{name?, repo?}` | push to the production GitHub repo (`needs_config` → pass `repo`) | `POST /api/publish/push` (SSE) |
| `app_deploy` `{name?}` | deploy to the local OpenServerless | `POST /api/git/deploy` (SSE) |
| `app_publish` `{name?, apihost?, user?, password?}` | publish to production; license gates unchanged | `POST /api/publish/remote` (SSE) |

`name` defaults to the current app where optional.

**Seeing the app.** The server takes no screenshots and adds no browser
runtime (setup.sh forbids browser runtimes). `app_view`/`app_status`/
`app_launch` return `urls.dev` (`<scheme>://vite.<domain>/`), `urls.workbench`
(`trustant.<domain>/app.html`) and `urls.applist`; the agent opens them with its
own browser tooling to look at or screenshot the app.

### File tools (current app only)

`files_list {path?}`, `files_read {path}`, `files_write {path, content}`,
`files_delete {path, recursive?}`, `files_search {pattern, path?, regex?}`.

- Listing and reading reuse `GET /api/files/<name>` (skipped dirs, 1 MB view
  limit, binary detection).
- Every path is resolved inside `$WORKBENCH_DIR/<current>` with
  `resolveWorkbenchPath` (no absolute paths, no `..`, symlinks must resolve
  inside). Writes and deletes additionally resolve the **deepest existing
  ancestor** (`resolveWritablePath`), so a new file under a symlinked folder
  that points outside the workbench is refused. The app root itself cannot be
  deleted.
- Read-only (launch regenerates them or they are its contract,
  [13-gitignore.md](13-gitignore.md)): `.mcp.json`, `.env`, `.env.production`,
  `.openserverless-contract.md`, `CLAUDE.md`, `.claude`, and everything under
  `.git/`, `.acp-data/`, `.claude/` (write to `.agents/` instead).
- `AGENTS.md` and `.gitignore` are writable but cannot be deleted; a write is
  passed through `mergeManagedAppAgents` / `mergeManagedGitignore`, so the
  Trustant-managed block is always preserved.
- `files_write` max 5 MB; existing file mode is kept, new files are `0644`,
  parent folders are created. `files_search` skips binary and > 1 MB files and
  returns at most 200 `path:line: text` matches.

## Proxied app MCP servers

`mcpProxyManager` re-exposes the current app's MCP servers (openserverless,
react, postgres, redis, milvus, mongodb, s3, ...):

- Source: the **private** config launch writes (`managedMCPConfigPath`,
  `~/.config/trustant/runtime/<app>/mcp.json`, full credentials), never the
  public `.mcp.json`.
- Each server is connected as an MCP client: stdio servers as child processes
  in the app dir with the configured env, in their own process group (killed
  as a group on stop; on Linux also `Pdeathsig=SIGTERM` so they never outlive
  Trustant); http servers over Streamable HTTP. Servers start in parallel, each
  bounded by 90 s.
- Every remote tool is registered as **`<server>__<tool>`** (non
  `[A-Za-z0-9_-]` replaced by `_`, max 64 chars), description prefixed with
  `[<server>]`, original input schema and annotations. Calls and results are
  forwarded unchanged. Credentials stay on the host.
- Sync is lazy: on every `/mcp` request and after `app_launch`/`app_stop`, the
  manager compares the current app and the private config's mtime; on a change
  it restarts the proxies in the background. Nothing is spawned while no MCP
  client is connected. Tool changes emit `notifications/tools/list_changed`.
- A server that fails to start is reported in `app_status.mcp_servers` and its
  tools are omitted; the others keep working.

## Resources and prompt

| URI | Content |
|---|---|
| `trustant://instructions/openserverless` | embedded `openserverless-instructions.md` |
| `trustant://instructions/contract` | embedded `openserverless-contract.md` |
| `trustant://instructions/app-agents` | embedded `app-agents.md` |
| `trustant://app/agents` | the current app's `AGENTS.md` |
| `trustant://app/skills/{name}` | `.agents/skills/<name>/SKILL.md` of the current app |

Prompt **`build_fullstack_app`** `{goal?}`: the initialize instructions, the
OpenServerless instructions and the contract composed into one user message,
plus the goal.

## Configure page

An "MCP Server" card shows the MCP URL, the token fingerprint (from
`GET /api/v1/token`) and the `util/mcp.sh add` / `mcp.ps1 add` /
`mcp.sh refresh` commands. No token and no regenerate button: rotation is
`util/mcp.sh refresh`.

## `util/mcp.sh` and `util/mcp.ps1`

`util/mcp.sh <add|remove|refresh|show> [--claude] [--claude-desktop] [--codex]
[--url URL] [--token TOKEN] [--dry-run]`; `mcp.ps1` takes the same verbs with
`-Claude -ClaudeDesktop -Codex -Url -Token -DryRun`. Both `cd` to the repo root
first. Without agent flags every agent found (CLI on PATH or config dir present)
is configured.

- **URL**: `--url` (a trailing `/mcp` is accepted), else
  `http://trustant.<ip>.nip.io:8910` with `<ip>` from `current.ip` in the
  support dir, else `127.0.0.1` (WSL localhost forwarding).
- **add**: read the local token (or generate and save it; `--token` overrides
  and is saved), `PUT <base>/api/v1/token` (bearer = the same token), and only
  on success write the agents' config. Not answering → fail with a hint; 401 →
  local login is enabled, explain how to rotate.
- **refresh**: generate a new token, PUT it with the old one as bearer, save it,
  reconfigure the agents.
- **remove**: drop the `trustant` entry from the selected agents (the token
  file is kept). **show**: URL, token file, local and server fingerprints, and
  per-agent configured / not configured.
- Agents (idempotent — an existing `trustant` entry is replaced):
  - **Claude Code**: `claude mcp remove --scope user trustant`, then
    `claude mcp add --scope user --transport http trustant <url>/mcp --header
    "Authorization: Bearer <token>"`.
  - **Claude Desktop** (`~/Library/Application Support/Claude/` on macOS,
    `%APPDATA%\Claude\` on Windows, `~/.config/Claude/` on Linux):
    `mcpServers.trustant = {command: "npx", args: ["-y", "mcp-remote", <url>,
    "--header", "Authorization:${TRUSTANT_AUTH_HEADER}", "--allow-http"?],
    env: {TRUSTANT_AUTH_HEADER: "Bearer <token>"}}` — Desktop's file only
    launches local commands, and the header travels via env because launchers
    mangle a space inside an argument. `--allow-http` only for an `http://`
    URL. The file is backed up to `.bak`, other keys are kept; restart Desktop.
  - **Codex** (`${CODEX_HOME:-~/.codex}/config.toml`): any earlier entry is
    removed (the marked block, then `codex mcp remove trustant`, then stray
    markers `codex` leaves behind), and a block is appended between
    `# >>> trustant mcp >>>` / `# <<< trustant mcp <<<`:
    `[mcp_servers.trustant]`, `url = "..."`,
    `http_headers = { Authorization = "Bearer <token>" }`.
- The token is written only into the agents' own config in the user's home,
  never into the repo.
- `mcp_script_test.go` guards: repo-root `cd`, PUT before configuring, no
  `.env` token, replace-not-duplicate, no `{32,256}` bash regex (macOS caps
  repetition at 255).
