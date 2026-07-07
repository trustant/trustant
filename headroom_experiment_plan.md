# Headroom experiment plan

Date: 2026-07-01

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

## Experiment scope

Run a manual prototype first in `trustable-0` or a development image. Do not
enable Headroom by default and do not include it in the issue-98 PR.

Candidate modes:

1. Wrap mode:
   - run `headroom wrap opencode` around the existing
     `opencode serve --port 4096 --hostname 0.0.0.0 --log-level DEBUG
     --print-logs` command.
2. Proxy mode:
   - run `headroom proxy --port 8787` on loopback;
   - generate or override an OpenAI-compatible provider whose `baseURL` points
     at `http://127.0.0.1:8787/v1`.

The winning mode must work with `opencode serve`, not only the interactive TUI.

## Prototype steps

1. Pin a Headroom version.
2. Install the smallest viable Python extras for proxy/wrap; avoid `[all]`
   until necessary.
3. Persist Headroom cache/state under the mounted workspace, for example:
   `/home/trustable/workspace/.trustable/headroom`.
4. Bind Headroom to loopback only: `127.0.0.1:8787`.
5. Start OpenCode through the candidate Headroom mode.
6. Launch a known app workbench.
7. Run the same long DB/action task with and without Headroom.
8. Compare behavior and metrics.

## Metrics

Collect:

- prompt/input tokens from provider usage, when available;
- OpenCode context/compaction signals, when available;
- Headroom stats/dashboard output;
- latency overhead;
- failed tool calls;
- failed MCP calls;
- whether reversible retrieval works when compressed context omits details;
- whether OpenCode still follows `.openserverless-contract.md` and
  `opencode.md`.

## Risks

- The Trustable image currently launches `opencode serve` directly from
  `launch.go`; Headroom wrapping may not preserve server-mode behavior.
- Proxying local Ollama, BestIA, or Trustable providers must preserve API keys,
  headers, model IDs, streaming, tool calls, and OpenAI-compatible paths.
- Installing broad Headroom extras may make the image much heavier.
- Compression may hide useful low-level logs unless retrieval is reliable.
- A new long-running process needs health checks, logs, restart behavior, and
  cache cleanup.

## Success criteria

Headroom can move from manual experiment to a spec-backed optional feature only
if:

- `opencode serve` works through it;
- model/tool-call streaming remains reliable;
- app deploy and local verification still use the correct pod-local targets,
  especially `localhost:5173`;
- token/context savings are measurable on a repeatable Trustable app task;
- latency overhead is acceptable;
- failure rate does not increase;
- the implementation can be disabled completely.

## Possible follow-up PR

Only after the prototype succeeds:

1. Add a disabled-by-default runtime or image option.
2. Update the matching specs.
3. Add tests for generated config changes.
4. Document operational rollback.
5. Keep the issue-98 guardrails independent.
