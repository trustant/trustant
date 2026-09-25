# Pi and TruACP runtime

Trustant uses `pi` (`@earendil-works/pi-coding-agent`) as its coding agent.
Trustant does not execute it directly: `truacp` serves the React UI on port
4096, starts `pi-acp` over stdio, and `pi-acp` starts Pi in RPC mode.

The browser-visible hostname remains `opencode.<domain>` for ingress and WAF
compatibility, but it is only a plain reverse proxy to TruACP. There is no
OpenCode process, session API, directory header, project database, or URL
rewriting behind that hostname.

## Source and version pinning

`trustant-acp` is a pinned Git submodule. Pi extensions are pinned in
`trustant-acp/pi.version`; every npm install entry must carry an explicit
version. The customized Pi executable/core and ACP adapter are the nested
`trustant-acp/pi` and `trustant-acp/pi-acp` submodules, each pinned to an
exact fork revision. `setup.sh` builds the five lockstep Pi packages
(`pi-ai`, `pi-tui`, `pi-agent-core`, `pi-storage-sqlite-node`, and
`pi-coding-agent`) from the owned fork in source/VM mode or consumes the same
prebuilt tarballs in the image. It never installs the public coding-agent
package as a fallback. Dependency hydration for this nested source uses
`npm ci --ignore-scripts`; the explicit local-release command owns package
generation, while repository-only lifecycle hooks must not require the host
worktree's Git administration path from inside Lima, WSL, or an image builder.

In the repository-root Lima/WSL flow, root `setup.sh` is the sole owner of the
persistent npm global-prefix policy. It selects a compatible user-owned prefix
(default `~/.npm-global`), exports it before invoking this installer, and places
its bin directory on the current and Bash-login PATH. `trustant-acp/setup.sh`
consumes npm's selected writable prefix and must not introduce a competing
`~/.npm-global` default. Its standalone/image fallback remains scoped to those
entrypoints, where the caller's writable build prefix is authoritative.

## Global model configuration

Pi model configuration is global under `${PI_CODING_AGENT_DIR}` or, when that
variable is unset, `~/.pi/agent`:

| File | Mode | Trustant-owned values |
|---|---:|---|
| `models.json` | `0600` | the active managed provider with base URL and allowed coding models |
| `settings.json` | `0644` | `defaultProvider`, `defaultModel`, and `enabledModels` for the active prefix |
| `auth.json` | `0600` | the real provider API key |

Provider identity describes the endpoint origin: the Trustant status catalog
uses `trustant`, the embedded/status-backed Ollama catalog uses `ollama`, and
user-provided endpoints (including own-host Ollama and Private AI) use `local`.
`settings.enabledModels` contains only `<active-provider>/*`. `models.json`
stores the literal `$OPENAI_API_KEY` reference; the real key is only in
`auth.json`. Keyless providers use `dummy` so Pi considers the provider
configured.

TruACP resolves that reference server-side from `auth.json` for its `/models`
probe and never returns a stored key through `/api/pi/config/get`. Trustant
launches TruACP with `TRUSTANT_MANAGED_RUNTIME=1`; a failed probe then directs
the user back to Trustant's main Configure screen instead of opening TruACP's
standalone credential form. The managed process also receives
`PI_SKIP_VERSION_CHECK=1`: Trustant owns the pinned Pi version through
`trustant-acp/pi.version`, so Pi must not advertise or initiate an independent
global npm upgrade from inside an application session. The nested Trustant
`pi-acp` fork honors both `PI_SKIP_VERSION_CHECK` and `PI_OFFLINE` natively, so
setup does not patch installed JavaScript and does not fall back to the public
npm adapter.

Writers merge Trustant-owned keys into existing JSON and preserve unrelated Pi
settings and providers. Invalid or missing JSON is treated as empty. Model
entries are sorted and embedding/reranking/non-coding models are filtered.

The two limits are resolved independently, each from the model's entry in the
workspace `models` map:

- `contextWindow` ← `maxToken`, else `maxInput`, else **128000**
  (`piDefaultContextWindow`).
- `maxTokens` ← `maxOutput`, else **32768** (`piDefaultMaxOutput`).

They are separate constants deliberately. The context default was raised from
32768 because a provider whose catalog reports no size — an own-host
OpenAI-compatible endpoint, or any model whose `/api/status` entry omits limits
— was pinned to 32K against a model that usually supports far more. The output
default stays 32768, since raising it would over-claim output capacity on
models that cap lower.

Both limits are user-editable per model in Configure (see
[2a-config.md](2a-config.md)), for every provider: a catalog that reports no
size, or a wrong one, is exactly the case this has to fix. An absent value in
`trustant.json` means "use the default"; zero and absent are equivalent.

Pi reasoning capability is written per model. Explicit catalog values for
`reasoning` and `thinkingLevelMap` are preserved after validating Pi's known
level keys. For the managed `trustant` provider only, missing `reasoning`
defaults to `true` because the Trustant Cloud proxy supports Pi's standard
`high` reasoning-effort contract for coding models. Other providers default to
non-reasoning unless their catalog explicitly opts in. `xhigh` is never
inferred: it is supported only when `thinkingLevelMap.xhigh` is present and
non-null, so Pi and TruACP cannot silently downgrade an Extra high selection.

The configure flow writes these files after the selected provider and model
pass the connectivity probe. App launch never writes or repairs global Pi
configuration: Edit is an app operation, while Configure is the single owner of
runtime settings and credentials.

After a successful Configure write, Trustant snapshots only these three JSON
files under `<WorkspaceDir>/.trustant/pi-agent-config/`, with a private
directory and the same per-file modes. The npm extension tree is deliberately
not persisted: every replacement image must supply the versions pinned by that
image rather than inherit packages from an older pod.

At server preflight, Trustant overlays the durable JSON snapshot onto the fresh
image's Pi directory while preserving the current image's `settings.packages`.
For the first upgrade from an image that did not create a snapshot, preflight
may materialize the already-selected provider from the durable
`trustant.json`. This is state recovery, not a second configuration owner: it
does not change provider/model selection, run a model probe, or run from app
launch. A missing `pi.default` still requires the explicit Configure flow.

The selected model is stored as `pi.default` in `trustant.json`. There is no
secondary/small model. Legacy `opencode.default` and `opencode.small` fields are
ignored rather than migrated; an existing installation without `pi.default`
is redirected to `configure.html?setup=1` for one explicit selection.
Trustant serves HTML documents with `Cache-Control: no-store` so an upgraded
browser cannot restore stale inline OpenCode redirect logic from history after
the Pi configuration has already been saved.

## Per-app project assets

Launch writes these model-readable files to `<workbench>/<app>` after
`ops ide login`:

- `.mcp.json` in the standard `mcpServers` schema, containing server names and
  credential-free launch descriptors only;
- `AGENTS.md`, with a replaceable Trustant-managed block and preserved
  app-local notes. `CLAUDE.md` is a symlink to it, not a second file
  (see [13-gitignore.md](13-gitignore.md));
- `.openserverless-contract.md`.

It also installs the three Trustant checker scripts under `~/.local/bin`.
There is no `opencode.json`, OpenCode runtime manifest, or per-app model file.
`AGENTS.md` remains active Pi project context, but `pi-acp` does not mirror its
absolute host path or a redundant **Context** section into the browser-visible
startup prelude.

The complete MCP process configuration is atomically regenerated outside the
workbench under `~/.config/trustant/runtime/<app>/mcp.json`, mode `0600`.
Service credentials exist only there and are injected into the corresponding
server process. Credential-bearing entries in `.mcp.json` invoke the fixed
`trustant-mcp-launch <server>` host launcher; the launcher resolves the current
workbench through the versioned runtime manifest and never prints its private
configuration. Pi reaches those entries through `pi-mcp-adapter`. `.mcp.json`
is fully regenerated on every launch. `openserverless`, `browser`, and the deterministic read-only
`react` validator are always present;
`agentireact` is conditional on the supported `@agentic-react/vite` import plus
`AgenticReact()` invocation in the Vite config; service MCP servers are
conditional on their blocks in `~/.ops/config.json`.

The `react` server is separate from Agentic React. It resolves only the
manifest-selected workbench and exposes bounded project inspection,
route/auth-flow checks, and aggregate TypeScript/React validation. The managed
extension requires aggregate `react_validate` after frontend mutations.

The managed extension also enforces reproducible OpenServerless ownership. It
allows the corrected Redis-only `auth_setup` tool, blocks the obsolete
environment-mutating `secret_ensure`, rejects writes to generated
`packages/**/__main__.py` wrappers and `packages/**/*.zip` artifacts, and
rejects mutating PostgreSQL/Redis/MongoDB/S3/Milvus MCP calls. Service MCP
reads remain available for discovery and verification; schema, seed, and
application writes belong in setup or public OpenServerless actions.

`setup.sh` must register `pi-mcp-adapter` and `pi-web-access` with `pi install`.
A global npm installation alone does not activate a Pi extension. The adapter
exposes a single `mcp` proxy, but every generated server uses
`lifecycle: "eager"` so connection is attempted when the Pi session starts.
Before reporting binding availability, instructions require actual inspection
of every `.mcp.json.mcpServers` entry. The compatible sequence is `mcp({})`
plus `mcp({server:"<name>"})` per server; the adapter's successful
`mcp({connect:"<name>"})` form proves both proxy reachability and discovery for
that server, so successful connects for every required server are equivalent.

Codex and Claude receive the exact same host-selected private server set through
the ACP `session/new`, `session/load`, `session/resume`, and `session/fork`
`mcpServers` field. Pi receives an empty ACP list because its pinned adapter owns
the proxy. TruACP keeps only one initialized agent process at a time, so an
agent switch deterministically closes that agent's persistent MCP children.
Managed agents never need to launch MCP commands manually.

The shared private Redis entry launches `trustant-redis-mcp`, which enforces
the current application's configured prefix before forwarding any reviewed
key, scan, channel, or index operation and hides global Redis introspection.
Unknown tools fail closed. The shared S3 entry sets
`S3_CONNECTION_NAME=default`, so `s3_list_connections` exposes exactly one
primary connection reconstructed from private host configuration after every
MCP restart. These guarantees are server-side and therefore do not depend on
Pi, Codex, or Claude following prompt guidance.

The pinned `pi-mcp-adapter@2.11.0` receives a deterministic setup-time recovery
patch. If a co-located Streamable HTTP server restarts and rejects the cached
session ID, the manager closes that stale connection, initializes a replacement
connection (refreshing tools and resources), and retries the original tool call
exactly once. Proxy, direct-tool, and MCP UI calls all use this manager path.
Other failures are returned unchanged, and a failed replacement or retry is not
retried again. Setup validates the installed version and every source target
before atomically replacing files, remains idempotent, and fails closed when the
pinned package layout changes.

The optional `agentireact` server is generated only when `vite.config.js` or
`vite.config.ts` contains executable configuration that both references the
`@agentic-react/vite` package and invokes `AgenticReact()`. Detection ignores
comment-only or string-only text, the obsolete `AgentiReact()` spelling, and
wrong packages. Its standard Pi entry is HTTP/eager at
`http://localhost:5173/mcp`; adding the plugin during a live session requires
an app relaunch so launch can regenerate `.mcp.json`.

Pi still knows its built-in providers, and pi-acp currently advertises that
full catalog even when Pi model cycling is scoped. Trustant therefore writes
`enabledModels: ["<active-provider>/*"]` for runtime selection and TruACP
filters the header selector to the same active `local`, `ollama`, or `trustant`
prefix.

## Launch contract

For a canonical workbench path `<dir>`, Trustant starts:

```text
truacp --port 4096 --dir <dir>
```

TruACP, Pi, and `ops ide devel` share one process group so Stop terminates the
whole app runtime. TruACP owns its ACP session and working directory; Trustant
does not bootstrap, select, or persist agent sessions itself.

During that live Edit runtime, the already-running `ops ide devel` child is the
sole owner of action packaging and deployment. Pi must not invoke
`ops ide deploy` or start a second `ops ide devel`; the issue #57 extension
blocks both command shapes. After one or more real `action_new` creations, Pi
finishes the coherent action/wiring/source batch and calls
`trustant_runtime_redeploy` once. The extension tool invokes the same
co-located `/api/redeploy` SSE path as the Trustant UI, so the watcher is
stopped before the full deploy and restarted afterward; an idempotent
`action_new` no-op does not request it. Watcher status, checker, HTTP, and
browser verification are blocked until the required redeploy succeeds.
Trustant mirrors the restarted watcher output into a private, bounded log
outside the workbench and declares it in the version-2 managed manifest. Pi
then calls `trustant_runtime_status` for a redacted authoritative tail, runs
`check_openserverless_actions.sh` once for source-contract validation, and
validates the real `localhost:5173/api/my/...` endpoint. Managed live checker
mode deliberately ignores sibling ZIP existence and freshness. The issue #57
extension blocks direct shell inspection or polling of `packages/**/*.zip`,
rejects masked checker pipelines, and permits one checker call per relevant
source or OpenServerless-wiring revision. Pi does not diagnose the watcher
through archive paths, repeated checker calls, process searches, or increasing
waits.

The owned Pi core also detects repeated normalized prose inside one streamed
provider response, where ACP and turn hooks cannot intervene. It aborts only
that pathological response. There is deliberately no global provider-step or
turn budget: a healthy long run may exceed 300 turns.

Inside a live turn, the TruACP Stop control sends ACP `session/cancel` to the
current Pi session. The fork bounds the Pi abort request, reports explicit
stopping/idle activity, exposes retry and compaction as control-plane status
rather than assistant prose, and includes Pi extension commands in the command
catalog. The UI shows activity plus elapsed time, not an invented completion
percentage, and can create or resume Pi sessions through the ACP session APIs.
Inactive Pi sessions can also be removed through standard ACP `session/delete`.
The fork resolves the ID inside Pi's configured session directory and refuses
to delete the currently loaded session; TruACP removes its secondary metadata
only after Pi confirms the deletion.

## Trustant execution policy

The OpenCode session plugin and its deterministic completion/recovery state
machine are not part of Pi. The managed instructions, OpenServerless contract,
and checker scripts remain important application inputs, but prose alone is not
an execution policy. This is an explicit runtime simplification,
not a silent fallback to upstream OpenCode.
The legacy JavaScript plugin source is not embedded in the Go binary,
`@opencode-ai/plugin` is not a project dependency, and launch has no dormant
`opencode.json` generator. Managed instructions must not tell Pi to call
`trustant_context_recover`, `trustant_diagnostic_checkpoint`, or
`trustant_completion_check`; verification uses the real MCP/browser tools and
checker commands directly.

Issue #57 introduces a versioned, reviewed Pi execution-policy extension rather
than reviving the former `trustant-guardrails.ts` placeholder. Its first
increment binds the managed process to the host-selected workbench, exact MCP
configuration, classified local and browser-visible application URLs, and
installed extension; it injects that immutable context before every turn and
blocks writes outside the workbench. Managed mode fails closed when any part of
that contract is missing.

The next issue #57 increment normalizes the real `pi-mcp-adapter` proxy shape
(`openserverless_action_new`, JSON-string `args`, and the `server`/`connect`
discovery forms) and enforces a capability bootstrap before application work:
the MCP proxy must be proven reachable and every required manifest server must
be successfully discovered. A successful `connect` proves both reachability
and discovery for that server. It blocks
raw action/service administration, manual wrapper/scaffold generation,
ad-hoc dependency installation, service-MCP writes, shell-based source
mutation/destructive Git recovery, and piecemeal Redis wiring for
authentication endpoints. Tool results drive state: after three
semantically equivalent failures with no successful relevant source/wiring
mutation, that strategy is rejected until the hypothesis or revision changes.
There is still no global step/turn budget.

Context-continuity state and completion evidence remain subsequent issue #57
increments. The complete versioned contract and acceptance matrix are
specified in [trustant-pi-runtime.md](trustant-pi-runtime.md).

## Upstream runtime ownership (#71)

This section supersedes earlier fork-source packaging language. Trustant pins
all five upstream Pi packages at `0.82.0` in `trustant-acp/pi.version` and pins
their reviewed SHA-512 values in `trustant-acp/pi.integrity`. Setup and image
builds must fail on any version or integrity mismatch and must not require a Pi
source submodule or cached `pi-packages` tarballs. The Trustant `pi-acp` fork
remains a separate pinned component.

The managed repeated-stream invariant is implemented by
`trustant-acp/extensions/trustant-runtime.ts` through upstream Pi's
`message_update`, `message_end`, and `ctx.abort()` extension API. Detection is
scoped to one assistant response and does not impose a turn or step budget.

## Notebook prompts

TruACP notebook workflows are client/server orchestration, not a Pi extension.
Each selected notebook or ad-hoc node is sent through the same ACP prompt path
as ordinary composer input. No notebook index, source metadata, GitHub token, or
unexecuted prompt is injected into Pi or its model context. See
[notebook.md](notebook.md).
