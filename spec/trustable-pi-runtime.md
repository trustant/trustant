# Trustable-managed Pi runtime contract

Issue #57 owns the execution competencies that Trustable must add to Pi/TruACP.
This contract is separate from the generic session and UI work in issue #58.
The active runtime remains:

```
trustable-app -> TruACP -> pi-acp -> Pi
```

The repository-local `AGENTS.md`, `.openserverless-contract.md`, and `.mcp.json`
teach the application model and remain useful after compaction, but prose alone
is not an execution policy. In particular, Pi must not merely list configured
MCP servers and then perform the same work through hundreds of shell calls.

## Version 2 host manifest

After generating the exact workbench `.mcp.json`, Trustable writes a private
credential-free manifest outside the application checkout:

```json
{
  "version": 2,
  "workbenches": [
    {
      "app": "example",
      "workspace": "/absolute/canonical/workbench/example",
      "developmentUrl": "http://localhost:5173",
      "browserUrl": "http://vite.192.168.64.9.nip.io:8910",
      "requiredMcpServers": ["browser", "openserverless", "postgres"],
      "watcherLog": "/home/user/.config/trustable/runtime/example/ops-ide-devel.log"
    }
  ]
}
```

- `workspace` is the active checkout, never the durable bare repository.
- `developmentUrl` is the co-located Vite endpoint consumed only after the
  Browser MCP has matched the canonical current workbench.
- `browserUrl` is derived from the calling browser's
  `trustable.<domain>[:port]` origin. It is not `localhost`, `OPS_APIHOST`, or a
  guessed `miniops.me` URL.
- `requiredMcpServers` is the sorted set of server names in the generated
  `.mcp.json`, including the always-present deterministic `react` validator
  and conditional servers such as `agentireact`.
- `watcherLog` is the canonical private log written by the host-owned
  `ops ide devel` process. It remains outside the workbench, is mode `0600`,
  rotates at a bounded size, and is never supplied by browser or app content.
- The manifest contains no credentials and is atomically written with mode
  `0600` under `~/.config/trustable/`.

Trustable passes only these process-scoped variables to TruACP:

```
TRUSTABLE_MANAGED_RUNTIME=1
TRUSTABLE_RUNTIME_CONFIG=<absolute manifest path>
TRUSTABLE_PI_EXTENSION_PATH=<absolute installed extension path>
```

They must never be written to an application `.env`, `.env.production`, or
generated environment map.

The extension treats those application env files as immutable agent
boundaries: it blocks direct read/write/edit/shell access and automatic MCP
secret generation through `secret_ensure`. Only the user-facing Trustable
configuration flow owns application env values. For generated app
authentication it injects the Redis-backed opaque-session contract, blocks
JWT/secret auto-setup, and allows the corrected `auth_setup` tool because that
tool only adds Redis wiring and never reads or writes `.env`.

## Host and adapter validation

Managed TruACP validates the manifest, selected working directory, exact
`.mcp.json`, and extension file before creating a Pi session. It then uses the
versioned `_meta.trustable.piLaunch` contract to pass the extension path to the
pinned `pi-acp` fork. Raw browser requests and generic agent configuration
cannot inject extension paths or arbitrary Pi argv.

Standalone TruACP remains unchanged. Once `TRUSTABLE_MANAGED_RUNTIME=1` is set,
missing, malformed, stale, or mismatched inputs are fatal: managed mode must not
fall back to plain Pi.

The extension is installed by `trustable-acp/setup.sh` at:

```
~/.local/lib/truacp/extensions/trustable-runtime.ts
```

The same artifact is staged into the container image beside the pinned TruACP
bundle, `pi-acp` package, and five packages built from the nested
`trustable-ai/pi` fork: AI, TUI, agent core, SQLite session storage, and coding
agent. The managed executable must never fall back to the public coding-agent
package.

## Initial enforced policy

Version 2 revalidates the host contract inside Pi and:

- injects the selected app, canonical workbench, local development URL,
  browser-visible application URL, and exact required MCP names as mandatory
  host context before every agent turn;
- normalizes the supported `pi-mcp-adapter` call and discovery shapes,
  including `mcp({server:"..."})` and `mcp({connect:"..."})`, and requires
  successful MCP-proxy reachability plus tool discovery for every required
  manifest server before shell, source-mutation, or MCP application work. A
  successful `connect` proves both proxy reachability and discovery for that
  server; `mcp({})` plus per-server `server` calls remains compatible;
- blocks `write` and `edit` calls outside the selected workbench;
- blocks direct writes to generated `packages/**/__main__.py` wrappers and
  `packages/**/*.zip` artifacts, requiring OpenServerless MCP action/connectors
  and edits only to business modules;
- blocks mutating service-MCP operations such as PostgreSQL `execute_sql`,
  Redis writes, MongoDB writes, S3 writes, and Milvus writes during generated
  application work. Read-only service discovery and verification remain
  available; reproducible mutations belong in setup/public actions;
- blocks Pi shell calls that would run `ops ide deploy` or start another
  `ops ide devel` process. The existing Trustable-managed development watcher
  is the sole owner of live action packaging and deployment;
- keeps TruACP's explicit user `!` shell path outside Pi and its tool hooks.
  The browser never executes a process or supplies cwd: the Node host accepts
  only an active session id plus command, resolves the session-owned workbench,
  filters credential-bearing environment variables, bounds time/output, and
  renders command, stdout/stderr, nonzero status, timeout, or truncation in the
  conversation. Non-leading `!` remains an ordinary agent prompt;
- registers `trustable_runtime_redeploy`, which invokes the same co-located
  `/api/redeploy` SSE workflow as the Trustable UI. A successful
  OpenServerless `action_new` creation requires one call after the coherent
  action/wiring/source batch; a compatible idempotent no-op does not. Watcher
  status, checker, HTTP, and browser verification remain blocked until the
  required redeploy succeeds, while source and wiring work may continue;
- recovers the pinned MCP adapter after that redeploy restarts the co-located
  Vite/Agentic React server. A Streamable HTTP `Session not found` failure
  closes only the stale adapter connection, reconnects and refreshes discovered
  tools/resources, then retries the original tool call exactly once. Other
  failures and a failed replacement/retry are returned without another retry;
- blocks raw `ops action`/`wsk action`, direct service CLIs, manual action
  scaffolding/generated-wrapper manipulation, and ad-hoc dependency installs
  that would repair only the current managed VM;
- blocks shell-based writes under `src/` or `packages/` and destructive Git
  reset/restore/clean operations, keeping mutations on typed tools where
  workbench and generated-artifact policy can observe them;
- registers `trustable_runtime_status`, a read-only tool returning a bounded,
  redacted tail of the authoritative watcher log. Action diagnosis must use
  this evidence before one source-contract checker pass and real HTTP checks;
  managed checker mode ignores sibling ZIP existence/freshness, while the
  extension blocks direct shell inspection or polling of
  `packages/**/*.zip`. It also permits one unmasked checker invocation per
  relevant source/OpenServerless-wiring revision and rejects another until a
  new mutation occurs. Pi must not infer watcher state from archive paths,
  repeated checker calls, process searches, or escalating waits;
- enables behavior-based stream repetition protection in the owned Pi core.
  The detector can terminate one provider response that repeats the same
  normalized prose, below ACP and extension hooks;
- imposes **no provider-step or turn budget**. A healthy long run may exceed
  300 provider turns; circuit breakers must be based on repeated behavior, not
  an arbitrary global count;
- records actual tool results and rejects the fourth attempt of the same
  semantic strategy after three equivalent failures without a successful
  relevant source/OpenServerless-wiring mutation. Changed inputs, strategy, or
  a successful relevant mutation permit recovery;
- fails closed when the managed marker, manifest, workbench, `.mcp.json`, or
  extension is invalid.

This first increment does not claim the complete OpenCode guardrail parity.
The following issue #57 increments remain explicit:

- durable request/workflow state, bounded recovery after compaction, and
  completion evidence;
- long-run acceptance tests proving useful completion without runaway shell or
  verification loops.

## Managed project instructions

The full Trustable guidance is embedded directly in generated `AGENTS.md` and
mirrored in `CLAUDE.md`. There is no project-local `opencode.md`. The managed
instructions must therefore identify only these runtime sources:

1. the Trustable-managed block in `AGENTS.md`;
2. `.openserverless-contract.md`;
3. `.mcp.json`.

`CLAUDE.md` is a compatibility mirror, not a separate authority. Historical
specifications may retain the `opencode.md` name, but generated guidance must
never tell Pi that the absent file exists or is a recovery source.

## Regression coverage

Tests must cover:

- browser-origin to Vite-origin derivation;
- private atomic manifest generation from the exact `.mcp.json`;
- missing extension or MCP config failing before launch;
- managed manifest/workbench/MCP validation in TruACP and inside Pi;
- typed extension metadata and rejection of session cwd escape;
- corrected Redis-only `auth_setup` remains available while `secret_ensure`,
  generated-wrapper/archive writes, and mutating service-MCP tools are rejected;
- rejection of direct, timed, and chained `ops ide deploy`/`ops ide devel`
  shell calls without blocking read-only searches that mention those commands;
- managed checker mode ignoring sibling archive existence/freshness while
  retaining source-contract failures, plus deterministic rejection of direct
  `packages/**/*.zip` shell access, masked checker pipelines, and repeated
  checker calls without an intervening relevant mutation;
- extension installation and image staging;
- unchanged standalone TruACP behavior;
- direct `!` prefix classification, active-session cwd resolution,
  stdout/stderr and nonzero-exit rendering, bounded timeout/output, exact ready
  composer text, and unchanged ordinary prompts for Pi and non-Pi agents;
- successful per-server `connect` calls satisfying the managed MCP bootstrap
  without being misclassified as global status, while failed connects advance
  neither reachability nor server-discovery state;
- a real `action_new` result requiring one successful host redeploy before
  verification, while an idempotent no-op does not; successful, failed, and
  incomplete `/api/redeploy` SSE responses retain protocol-level status;
- deterministic, idempotent installation of the pinned adapter recovery across
  proxy, direct-tool, and MCP UI calls, with exactly one retry for an expired
  Streamable HTTP session and fail-closed source/version drift;
- a healthy run exceeding 300 provider turns is not truncated, while repeated
  prose inside one streamed response is terminated deterministically;
- watcher output is bounded, private, redacted by `trustable_runtime_status`,
  and available in both initial launch and explicit redeploy paths.
