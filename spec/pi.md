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
| `models.json` | `0600` | `providers.trustable` with base URL and allowed coding models |
| `settings.json` | `0644` | `defaultProvider` and `defaultModel` |
| `auth.json` | `0600` | the real provider API key |

The provider key is always `trustable`. `models.json` stores the literal
`$OPENAI_API_KEY` reference; the real key is only in `auth.json`. Keyless
providers use `dummy` so Pi considers the provider configured.

Writers merge Trustable-owned keys into existing JSON and preserve unrelated Pi
settings and providers. Invalid or missing JSON is treated as empty. Model
entries are sorted, embedding/reranking/non-coding models are filtered, and
limits fall back to 32768 when the catalog provides none.

The configure flow writes these files after selecting a working provider.
Launch writes the same deterministic content again from Trustable's durable
configuration so a recreated VM can recover its ephemeral `~/.pi` state.

During this integration phase, the selected default is read from the existing
`opencode.default` field in `trustable.json` for backward compatibility. This
field is configuration-schema debt only: it does not activate OpenCode and will
be renamed to `pi.default` in a separate migration that preserves existing
installations.

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
