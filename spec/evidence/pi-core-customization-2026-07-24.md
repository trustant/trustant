# Trustable-owned Pi core: long-run failure evidence and customization plan

Tracking issue:
[trustable-ai/trustable-app#65](https://github.com/trustable-ai/trustable-app/issues/65).

## Decision requested

Trustable needs an explicitly owned and pinned Pi core instead of installing the
public `@earendil-works/pi-coding-agent` package unchanged. This issue is the
review and authorization boundary for that customization. No Pi, TruACP, or
`trustable-app` branch should be pushed for this work until the issue has been
reviewed and the push has been authorized explicitly.

Issue #57 remains the broader execution-competency plan. This issue is narrower:
it records why a Pi-core fork is necessary, which invariants belong in that
fork, how the fork is packaged, and how it is kept separate from
Trustable-specific OpenServerless policy.

The finite/maximum-step wording still present in issue #57 is superseded for
this component. The accepted design has no numeric provider-step, tool-call or
turn budget; only observed pathological behavior can trip the circuit.

## Published baseline

The version exercised by the failing long run was based on:

| Component | Published revision or package |
|---|---|
| `trustable-ai/trustable-app` | `748560172daaca7109bf727b0b2ff30524369333` |
| pinned `trustable-acp` submodule | `552735becc6801d7de6fedc3fefc5a5574ebf0dd` |
| Pi CLI/core | `@earendil-works/pi-coding-agent@0.80.10` |
| ACP adapter | pinned Trustable `pi-acp` fork |

The ACP adapter was already owned, but the agent loop was still the public Pi
package. Consequently, a failure produced within one streamed provider response
could run below the ACP and extension lifecycle.

The failing session ended at 16:09 local time. The locally patched Pi runtime
was installed into the VM later, at 16:48, so the transcript is evidence from
the published baseline rather than from the pending correction.

## Redacted evidence log

### Source

- Environment: running `trudev` Lima VM.
- App: `trulongrun`.
- Model: `trustable/qwen3-coder-next`, reasoning `high`.
- Raw source: the Pi JSONL session under
  `~/.pi/agent/sessions/<trulongrun-session>/`.
- Session start: `2026-07-24T13:58:24.442Z`.
- Last record: `2026-07-24T14:09:44.293Z`.
- SHA-256:
  `aec3b99351ca77460add10f445aa08e0568f4fff8e15d45041b822b62b79e7e2`.
- Raw size: 458,117 bytes in 258 JSONL records.

The raw JSONL is deliberately not copied into the repository: process output in
the session can contain cluster configuration and credentials. The evidence
below is a redacted structural extraction.

### Quantitative trace

| Observation | Value |
|---|---:|
| Assistant messages | 88 |
| Tool calls / results | 165 / 165 |
| `mcp` calls | 70 |
| `bash` calls | 51 |
| `read` calls | 26 |
| `write` calls | 14 |
| `edit` calls | 4 |
| Failed tool results | 23 |
| Failed MCP results | 17 |
| Final streamed text | 12,442 characters |
| Exact repetitions of `Dato che il watcher non sta completando` in the final response | 50 |
| Final stop reason | `aborted` |

The abort was initiated by the user after the loop was visible. It was not a
normal model completion or an automatic Trustable termination.

Of the 70 MCP calls, 64 were operations and 6 were discovery. Repeated invalid
calls included 4 `openserverless_action_new` requests without the required
`endpoint` and 13 `openserverless_action_requirements` requests without the
required `library`. The diagnostic phase also accumulated 12 guessed ZIP-path
checks, 8 sleep/poll loops, 2 process searches, 2 inappropriate
`ops ide setup` invocations, 5 checker executions and 8 HTTP requests.

### Failure sequence

1. Pi generated and edited the application, then began verifying generated
   actions.
2. It repeatedly searched for ZIP archives below action source directories,
   including paths shaped like:

   ```text
   packages/v1/projects/*.zip
   packages/v1/*/projects.zip
   ```

3. It issued progressively repeated polling, process-search and checker
   commands instead of reading an authoritative watcher status.
4. A process check at `2026-07-24T14:09:05Z` showed both the existing
   host-managed deploy watcher and Vite running. Pi nevertheless continued to
   assert that the watcher was not completing.
5. The last assistant record contained one streamed text response of 12,442
   characters. The same diagnosis sentence appeared 50 times before the user
   aborted it.

Representative redacted tool sequence:

```text
14:04:46  wait for packages/v1/projects/projects.zip
14:05:49  search running watcher processes
14:06:16  wait again for packages/v1/projects/*.zip
14:07:27  wait for packages/v1/*/projects.zip
14:08:54  repeat the same wait
14:09:04  observe the managed deploy watcher and Vite running
14:09:16  repeat the same wait
14:09:44  emit the repeated 12,442-character response; user aborts it
```

### Root cause and ownership boundary

There are two distinct defects:

1. **Missing authoritative watcher evidence.** The published runtime gave Pi no
   bounded, redacted interface to the stdout/stderr of the Trustable-owned
   `ops ide devel` process. The agent guessed archive paths and process state.
   This belongs to `trustable-app` plus the managed Trustable extension in
   `trustable-acp`.
2. **Unbounded repetition within one provider response.** The final loop was
   produced inside one streamed assistant message. ACP observes the completed
   response too late, and a normal Pi extension hook cannot reliably terminate
   text that never yields control. Detection therefore belongs inside the owned
   Pi agent loop.

OpenServerless archive grammar, MCP routing, managed deployment ownership and
completion evidence do **not** belong in generic Pi core. They remain
Trustable-specific extension/host policy under issue #57.

## Proposed Pi customization

### Repository and pinning

- Maintain `trustable-ai/pi` as the reviewed fork of the currently pinned
  upstream-compatible Pi source.
- Add it as a nested, exact-revision submodule of
  `trustable-ai/trustable-acp`.
- Build and install the lockstep `pi-ai`, `pi-agent`, `pi-tui`, and
  `pi-coding-agent` packages from that revision in both VM and image flows.
- Never silently fall back to the public coding-agent package in managed mode.

### Core invariant: behavior-based stream repetition protection

- Detect repeated normalized word windows while assistant text is streaming.
- Abort only the pathological provider response and return a clear error.
- Keep the detector request-local so a later healthy request is unaffected.
- Do not classify user intent or language with prompt regexes.
- Do not add a numeric provider-step, tool-call, or turn budget.
- Prove with regression coverage that a healthy run exceeding 300 provider
  turns is not truncated.

### Reproducible local release

The fork's normal upstream build refreshes model catalogs from live external
catalogs. During local packaging on 2026-07-24, a refreshed provider definition
introduced an incompatible schema and made an unchanged source revision fail to
compile. Trustable packaging must compile the checked-in catalogs. Catalog
refresh is a separate, explicit maintenance operation, never an implicit side
effect of VM or image setup.

## Companion Trustable runtime work

The following work is required for the same acceptance scenario but is not
generic Pi-core policy:

- `trustable-app` captures the existing `ops ide devel` stdout/stderr into a
  private, size-bounded per-app runtime log outside the workbench.
- The versioned, credential-free runtime manifest declares that canonical log.
- The Trustable Pi extension exposes a read-only
  `trustable_runtime_status` tool with a bounded, redacted tail.
- Managed instructions require this evidence before checker/HTTP verification
  and forbid guessed ZIP paths, process polling and a second deploy watcher.
- The raw log remains host-owned and is never supplied by browser or application
  content.

## Repository impact

| Repository | Responsibility |
|---|---|
| `trustable-ai/pi` | generic stream-repetition invariant and deterministic local release |
| `trustable-ai/trustable-acp` | exact nested Pi pin, build/install/package flow, managed extension |
| `trustable-ai/trustable-app` | watcher log, runtime manifest, launch wiring, integration tests |

No plugin repository is involved.

## Acceptance criteria

- [ ] The redacted fixture reproduces the 50-repeat streamed-response failure
      against the published Pi baseline.
- [ ] The owned Pi core terminates the repeated streamed response without user
      intervention.
- [ ] A healthy synthetic run exceeding 300 provider turns completes without a
      numeric step/turn budget.
- [ ] A later healthy request works after a prior repeated response is aborted.
- [ ] Managed VM and image setup install the five Pi packages built from the
      exact nested fork revision and cannot fall back to public Pi.
- [ ] Packaging the same commit uses checked-in model catalogs and is
      deterministic without live catalog refresh.
- [ ] `trustable_runtime_status` reports bounded, redacted watcher evidence from
      the canonical app log.
- [ ] The `trulongrun` scenario uses watcher evidence and real HTTP checks
      instead of ZIP-path/process speculation.
- [ ] Pi core, TruACP, browser MCP and Go integration regression suites pass in
      the Linux `trudev` VM.
- [ ] The production-style pod image uses the same pinned Pi artifacts and
      passes the same runtime contract.
- [ ] No secrets, raw process environments or unredacted session payloads are
      committed or exposed through the browser.

## Current local validation, not yet pushed

The pending implementation has been validated in `trudev` with:

- Pi core: 16 test files / 182 tests;
- healthy 301-provider-turn regression;
- compilation of `pi-ai`, `pi-agent`, `pi-tui`, and `pi-coding-agent`;
- TruACP: 12 test files / 91 tests plus build;
- Browser MCP: 8 tests plus typecheck;
- `go test ./...`;
- POSIX shell syntax checks for setup and image scripts;
- local release and clean-prefix installation of the five forked Pi packages.

These results authorize review of the approach only. They do not authorize a
push.
