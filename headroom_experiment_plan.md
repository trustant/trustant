# Headroom experiment plan

Date: 2026-07-02

Related but separate plan: `improvement_plan_issue98.md`

## Goal

Evaluate whether Headroom can reduce OpenCode context pressure and token usage
inside the `trustable-0` pod without changing the Trustable/OpenServerless
development contract.

This is not part of the issue-98 guardrail PR. The issue-98 work should stand
on its own through instructions, discoverable contracts, static checks, and
runtime verification.

## Why separate

Headroom may help with context size, repeated file reads, logs, and long tool
outputs. It does not solve the core operational bug by itself: OpenCode must
still recognize the Trustable workbench, use the generated OpenServerless MCP
tools, keep wrappers thin, deploy through `ops ide deploy`, and verify through
the right local/pod routes.

Keeping this separate avoids blocking critical guardrails on:

- image size changes;
- proxy/wrapper compatibility with `opencode serve`;
- provider streaming/tool-call compatibility;
- cache/state persistence decisions;
- operational support for another process in the pod.

## Headroom summary

Headroom is a context compression layer for AI agents. Its README describes:

- proxy mode: `headroom proxy --port 8787`;
- agent wrapping including OpenCode;
- MCP server tools;
- local-first cache and reversible retrieval;
- reported token savings in the 60-95% range, depending on workload.

Current upstream notes checked on 2026-07-02:

- the Python package is the CLI-bearing package; latest PyPI version observed
  locally is `headroom-ai==0.28.0`;
- the npm package is currently library-oriented; latest npm `headroom-ai`
  observed locally is `0.22.4`;
- upstream README lists OpenCode support, but there is also an open issue about
  `headroom wrap opencode` appearing active while OpenCode traffic is not
  recorded and explicit `headroom/*` models fail. Treat direct wrapping as
  unproven until our pod test shows traffic in Headroom stats.

Sources:

- https://github.com/headroomlabs-ai/headroom
- https://github.com/headroomlabs-ai/headroom/issues

## Implementation objective

Build a disabled-by-default Headroom experiment that can run inside
`trustable-0`, can be enabled for one test pod/image without changing normal
Trustable behavior, and can prove or disprove whether Headroom sees real
OpenCode traffic in server mode.

The first implementation must be reversible:

- no default routing through Headroom;
- no change to the Trustable/OpenServerless contract;
- no change to `localhost:5173` app validation;
- no global `~/.config/opencode/opencode.json`;
- no dependency on the user running shell commands.

## Implementation status

2026-07-02:

- Phase 1 code is implemented on branch `improvement/headroom-phase1`.
- The implementation installs Headroom in the image definition and adds
  disabled-by-default launch support for a pod-local proxy.
- The enable/disable switch is exposed in the Configure UI and saved in the
  workspace `trustable.json` as `experimental.headroom.enabled`. Environment
  variables remain only developer/operator overrides.
- The current implementation does not route OpenCode traffic through Headroom
  and does not modify generated `opencode.json`.
- Local image `ghcr.io/trustable-ai/trustable-app:local_headroom_phase1_26.183.2050`
  was built and rolled to `trustable-0` for Phase 1 verification.
- Disabled mode was verified: Headroom logs as disabled, no proxy process is
  left running, and the normal OpenCode launch path remains available.
- Enabled mode was verified with `TRUSTABLE_HEADROOM_ENABLED=true` on
  `trutestdb2`: `headroom proxy` starts on loopback port `8787`, `/health` and
  `/stats` respond, OpenCode remains on `4096`, `ops ide devel` remains on
  `5173`, and DELETE `/api/launch` terminates the shared process group.
- Headroom state was verified under
  `/home/trustable/workspace/.trustable/headroom`: `proxy_savings.json`,
  `ccr_store.db`, logs, cache, and update check state are written there; no
  new container-layer `/home/trustable/.headroom` files were created after the
  final env override.

## Proposed design

### Installation

Install Headroom in the image with the smallest viable Python extra:

```Dockerfile
RUN uv tool install --python /usr/bin/python3 "headroom-ai[proxy]==0.28.0"
```

If `headroom wrap opencode` requires extras not included in `[proxy]`, broaden
only as needed and record the image-size delta. Avoid `[all]` unless the
prototype proves it is required.

### Runtime switch

Add a user-facing switch in Configure:

```json
{
  "experimental": {
    "headroom": {
      "enabled": false,
      "mode": "proxy"
    }
  }
}
```

The setting is stored in the workspace `trustable.json`. This makes the
experiment controllable from the same place where the user configures providers
and models, without asking the user to run shell commands.

Keep environment variables as developer/operator overrides:

```bash
TRUSTABLE_HEADROOM_ENABLED=false
TRUSTABLE_HEADROOM_MODE=proxy
TRUSTABLE_HEADROOM_PORT=8787
TRUSTABLE_HEADROOM_STATE_DIR=/home/trustable/workspace/.trustable/headroom
```

Rules:

- absent or false saved config means current behavior exactly: launch
  `opencode serve` directly;
- true saved config enables the experiment for app launches;
- if `TRUSTABLE_HEADROOM_ENABLED` is set, it overrides the saved UI value;
- unsupported mode returns a clear launch error;
- state/cache lives under the mounted workspace, not the container layer;
- the proxy binds only to `127.0.0.1`.

### Phase 1: observed proxy probe

Start with proxy mode, not direct wrap mode.

Reason: OpenCode wrapping is listed as supported upstream, but an open upstream
issue specifically questions whether OpenCode traffic is actually recorded. A
plain proxy gives us a simpler, observable target first.

Implementation outline:

1. Add helper functions in `launch.go`:
   - `headroomConfigForLaunch()`
   - `headroomConfigFromTrustableConfig()`
   - `applyHeadroomEnvOverrides()`
   - `ensureHeadroomProxy(ctx, env)`.
2. When enabled, start or reuse:

   ```bash
   headroom proxy --host 127.0.0.1 --port <port>
   ```

3. Wait for a local readiness endpoint or, if no stable health endpoint exists,
   wait until the port is listening.
4. Keep Headroom in the same process group as OpenCode when possible, so
   `DELETE /api/launch` and stale-port cleanup do not leave it orphaned.
5. Log:
   - mode;
   - port;
   - state dir;
   - binary path;
   - whether Headroom was started or reused.

Phase 1 does not yet route OpenCode through Headroom by default. Its purpose is
to prove the binary runs in the image, state persists in the right place, and
the process lifecycle is controllable.

### Phase 2: OpenCode routing experiment

Only after Phase 1 is stable, test one routing path at a time.

Candidate A: `headroom wrap opencode`

```bash
headroom wrap opencode -- serve --port 4096 --hostname 0.0.0.0 --log-level DEBUG --print-logs
```

Acceptance for candidate A:

- OpenCode server mode starts on port `4096`;
- Trustable can still create the session with `X-Opencode-Directory`;
- Headroom stats show real OpenCode requests;
- generated `opencode.json` remains the source of provider/model/MCP truth;
- the workbench path and project identity remain correct.

Candidate B: generated Headroom provider

If wrap mode does not capture traffic, generate an additional temporary
provider in the project `opencode.json` only when Headroom is enabled:

```json
"headroom": {
  "npm": "@ai-sdk/openai-compatible",
  "name": "Headroom Proxy",
  "options": {
    "baseURL": "http://127.0.0.1:8787/v1"
  },
  "models": {
    "<model>": {
      "name": "<model>",
      "limit": {
        "context": <same as selected upstream model>,
        "output": <same as selected upstream model>
      }
    }
  }
}
```

Then set `model` / `small_model` to `headroom/<selected-model>` only in the
experiment run.

Acceptance for candidate B:

- OpenCode sends model traffic to the local proxy;
- Headroom forwards to the original configured provider with the right API key,
  headers, model id, and OpenAI-compatible path. This must work for local
  Ollama, Ollama Cloud/Trustable Cloud, BestIA, and the Nuvolaris
  `api.nuvolaris.io` proxy path; a solution that works only for local Ollama is
  not acceptable;
- streaming and tool calls still work;
- MCP tools remain unaffected;
- disabling the flag fully restores the original generated `opencode.json`.

Candidate B is riskier because Trustable currently uses provider-specific
configuration generated from `trustable.json`; do not implement it until
candidate A is proven insufficient.

### Phase 3: optional MCP tools

If proxy or wrap works, evaluate adding Headroom MCP tools to `opencode.json`:

- `headroom_stats`;
- `headroom_retrieve`;
- any tool needed to retrieve original compressed content.

This should be a separate step because the OpenServerless MCP tools are already
critical and should not be mixed with new context-retrieval tooling until the
proxy path is stable.

## Code changes by file

Expected first PR files:

- `image/Dockerfile`
  - install pinned `headroom-ai[proxy]`;
  - verify `headroom --help` at build time.
- `preflight.go`
  - log the effective Headroom setting from workspace config plus optional
    `TRUSTABLE_HEADROOM_*` overrides;
  - do not fail startup when Headroom is disabled.
- `launch.go`
  - add Headroom config parsing and process startup helpers;
  - keep normal `opencode serve` path unchanged when disabled;
  - add logs and lifecycle cleanup for enabled mode.
- `configure.go`
  - persist `experimental.headroom.enabled` in workspace config;
  - preserve the experimental block when older clients post config without it.
- `web/configure.html`
  - add an Experimental section with a Headroom checkbox.
- `spec/2a-config.md`
  - document the Configure UI switch.
- `spec/4-launch.md`
  - document disabled default, lifecycle, and exact launch behavior.
- `headroom_experiment_plan.md`
  - keep validation results updated after each pod test.

## Branch and PR strategy

Use three small PRs if the experiment progresses:

1. `improvement/headroom-image-probe`
   - install Headroom;
   - add disabled config;
   - prove `headroom proxy` can run in the pod;
   - no OpenCode routing yet.
2. `improvement/headroom-opencode-routing`
   - choose wrap or provider injection;
   - route one test launch through Headroom behind the flag;
   - collect stats.
3. `improvement/headroom-mcp-observability`
   - add retrieval/stats MCP tools only if they improve recovery from
     compressed context.

This keeps rollback simple. If Phase 1 or Phase 2 fails, the branch can be
closed without touching the issue-98 guardrails or the OpenCode upgrade.

## Prototype steps

1. Build the image-probe PR.
2. Rebuild the local image and roll `trustable-0`.
3. Verify disabled mode:
   - `GET /api/launch/<app>` starts OpenCode exactly as today;
   - no Headroom process is running;
   - generated `opencode.json` is unchanged.
4. Enable Headroom from Configure for the test workspace.
5. Verify Phase 1:
   - `headroom --version` or `headroom --help` works;
   - `headroom proxy` binds only to `127.0.0.1:<port>`;
   - proxy startup/shutdown follows launch/delete lifecycle;
   - `HEADROOM_WORKSPACE_DIR` and `HEADROOM_CONFIG_DIR` point under
     `.trustable/headroom`, so state, logs, savings, cache, and config are not
     written to the container-layer `~/.headroom`;
   - `HEADROOM_CCR_SQLITE_PATH` points to `.trustable/headroom/ccr_store.db`,
     so compression retrieval state is also persisted in the workspace.
6. Disable Headroom again from Configure and verify relaunch returns to the
   direct OpenCode path.
7. Use `TRUSTABLE_HEADROOM_ENABLED=true|false` only for operator/dev override
   tests, not as the normal user workflow.
8. Test candidate A (`headroom wrap opencode`) in a local branch.
9. If candidate A does not show OpenCode traffic in stats, stop and design
   candidate B explicitly.
10. Run the same app-generation task with and without Headroom.
11. Compare behavior and metrics before proposing default inclusion.

## Metrics

Collect:

- prompt/input tokens from provider usage, when available;
- OpenCode context/compaction signals, when available;
- Headroom stats/dashboard output;
- whether Headroom stats show OpenCode traffic at all;
- latency overhead;
- failed tool calls;
- failed MCP calls;
- whether reversible retrieval works when compressed context omits details;
- whether OpenCode still follows `.openserverless-contract.md` and
  `opencode.md`.

## Risks

- The Trustable image currently launches `opencode serve` directly from
  `launch.go`; Headroom wrapping may not preserve server-mode behavior.
- Upstream currently has open Headroom/OpenCode reports, so direct wrap mode may
  appear to start while not actually routing traffic.
- Proxying local Ollama, Ollama Cloud/Trustable Cloud, BestIA, or Nuvolaris
  `api.nuvolaris.io` providers must preserve API keys, headers, model IDs,
  streaming, tool calls, and OpenAI-compatible paths.
- Installing broad Headroom extras may make the image much heavier.
- Compression may hide useful low-level logs unless retrieval is reliable.
- A new long-running process needs health checks, logs, restart behavior, and
  cache cleanup.

## Success criteria

Headroom can move from manual experiment to a spec-backed optional feature only
if:

- `opencode serve` works through it;
- Headroom stats prove OpenCode model traffic actually traverses Headroom;
- local Ollama, Ollama Cloud/Trustable Cloud, BestIA, and Nuvolaris
  `api.nuvolaris.io` provider configurations all still work through the chosen
  routing mode;
- model/tool-call streaming remains reliable;
- app deploy and local verification still use the correct pod-local targets,
  especially `localhost:5173`;
- token/context savings are measurable on a repeatable Trustable app task;
- latency overhead is acceptable;
- failure rate does not increase;
- the implementation can be disabled completely.

## Stop criteria

Do not continue to the next phase if any of these happen:

- disabled mode changes generated config or launch behavior;
- OpenCode does not start reliably in server mode;
- session creation with `X-Opencode-Directory` breaks;
- `ops ide devel` or `localhost:5173` verification is affected;
- Headroom shows no OpenCode traffic after routing is enabled;
- tool-call streaming or OpenServerless MCP calls regress;
- image size or startup time increases enough to make normal Trustable startup
  noticeably worse.

## Possible follow-up PR

Only after the prototype succeeds:

1. Add a disabled-by-default runtime or image option.
2. Update the matching specs.
3. Add tests for generated config changes.
4. Document operational rollback.
5. Keep the issue-98 guardrails independent.
