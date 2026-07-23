# Pi and TruACP runtime

Trustable uses `pi` (`@earendil-works/pi-coding-agent`) as its coding agent.
Trustable does not execute it directly: `truacp` serves the React UI on port
4096, starts `pi-acp` over stdio, and `pi-acp` starts Pi in RPC mode.

The browser-visible hostname remains `opencode.<domain>` for ingress and WAF
compatibility, but it is only a plain reverse proxy to TruACP. There is no
OpenCode process, session API, directory header, project database, or URL
rewriting behind that hostname.

## Source and version pinning

`trustable-acp` is a pinned Git submodule. The Pi CLI, ACP adapter, and Pi
extensions are pinned in `trustable-acp/pi.version`; every install entry must
carry an explicit version. `start.sh` initializes the submodule on the host
before Lima starts, while `setup.sh` consumes the mounted files without
following worktree Git metadata that may exist only on the host.

## Global model configuration

Pi model configuration is global under `${PI_CODING_AGENT_DIR}` or, when that
variable is unset, `~/.pi/agent`:

| File | Mode | Trustable-owned values |
|---|---:|---|
| `models.json` | `0600` | the active managed provider with base URL and allowed coding models |
| `settings.json` | `0644` | `defaultProvider`, `defaultModel`, and `enabledModels` for the active prefix |
| `auth.json` | `0600` | the real provider API key |

Provider identity describes the endpoint origin: the Trustable status catalog
uses `trustable`, the embedded/status-backed Ollama catalog uses `ollama`, and
user-provided endpoints (including own-host Ollama) plus BestIA use `local`.
`settings.enabledModels` contains only `<active-provider>/*`. `models.json`
stores the literal `$OPENAI_API_KEY` reference; the real key is only in
`auth.json`. Keyless providers use `dummy` so Pi considers the provider
configured.

TruACP resolves that reference server-side from `auth.json` for its `/models`
probe and never returns a stored key through `/api/pi/config/get`. Trustable
launches TruACP with `TRUSTABLE_MANAGED_RUNTIME=1`; a failed probe then directs
the user back to Trustable's main Configure screen instead of opening TruACP's
standalone credential form. The managed process also receives
`PI_SKIP_VERSION_CHECK=1`: Trustable owns the pinned Pi version through
`trustable-acp/pi.version`, so Pi must not advertise or initiate an independent
global npm upgrade from inside an application session. The currently pinned
`pi-acp` adapter implements a second registry check without supporting that
flag, so the TruACP installer adds a guarded, version-sensitive compatibility
patch to the installed adapter. This prevents the request and banner at their
source; setup fails if an adapter upgrade changes the expected patch location.

Writers merge Trustable-owned keys into existing JSON and preserve unrelated Pi
settings and providers. Invalid or missing JSON is treated as empty. Model
entries are sorted, embedding/reranking/non-coding models are filtered, and
limits fall back to 32768 when the catalog provides none.

The configure flow writes these files after the selected provider and model
pass the connectivity probe. App launch never writes or repairs global Pi
configuration: Edit is an app operation, while Configure is the single owner of
runtime settings and credentials.

The selected model is stored as `pi.default` in `trustable.json`. There is no
secondary/small model. Legacy `opencode.default` and `opencode.small` fields are
ignored rather than migrated; an existing installation without `pi.default`
is redirected to `configure.html?setup=1` for one explicit selection.
Trustable serves HTML documents with `Cache-Control: no-store` so an upgraded
browser cannot restore stale inline OpenCode redirect logic from history after
the Pi configuration has already been saved.

## Per-app project assets

Launch writes these files to `<workbench>/<app>` after `ops ide login`:

- `.mcp.json` in the standard `mcpServers` schema;
- `AGENTS.md` and an identical `CLAUDE.md`, with a replaceable
  Trustable-managed block and preserved app-local notes;
- `.openserverless-contract.md`.

It also installs the three Trustable checker scripts under `~/.local/bin`.
There is no `opencode.json`, OpenCode runtime manifest, or per-app model file.

Pi reaches MCP servers through `pi-mcp-adapter`. `.mcp.json` is fully regenerated
on every launch. `openserverless` and `browser` are always present;
`agentireact` is conditional on `AgentiReact()` in the Vite config; service MCP
servers are conditional on their blocks in `~/.ops/config.json`.

`setup.sh` must register `pi-mcp-adapter` and `pi-web-access` with `pi install`.
A global npm installation alone does not activate a Pi extension. The adapter
exposes a single `mcp` proxy, but every generated server uses
`lifecycle: "eager"` so connection is attempted when the Pi session starts.
Instructions still require `mcp({})` plus `.mcp.json.mcpServers` inspection
before reporting binding availability because tool discovery and process
connectivity are separate facts.

Pi still knows its built-in providers, and pi-acp currently advertises that
full catalog even when Pi model cycling is scoped. Trustable therefore writes
`enabledModels: ["<active-provider>/*"]` for runtime selection and TruACP
filters the header selector to the same active `local`, `ollama`, or `trustable`
prefix.

## Launch contract

For a canonical workbench path `<dir>`, Trustable starts:

```text
truacp --port 4096 --dir <dir>
```

TruACP, Pi, and `ops ide devel` share one process group so Stop terminates the
whole app runtime. TruACP owns its ACP session and working directory; Trustable
does not bootstrap, select, or persist agent sessions itself.

## Guardrails

The OpenCode session plugin and its deterministic tool-permission/completion
gates are not part of Pi. The managed instructions, OpenServerless contract,
browser MCP, and checker scripts remain advisory verification surfaces. This is
an explicit runtime simplification, not a silent fallback to upstream OpenCode.
The legacy JavaScript plugin source is not embedded in the Go binary,
`@opencode-ai/plugin` is not a project dependency, and launch has no dormant
`opencode.json` generator. Managed instructions must not tell Pi to call
`trustable_context_recover`, `trustable_diagnostic_checkpoint`, or
`trustable_completion_check`; verification uses the real MCP/browser tools and
checker commands directly.
