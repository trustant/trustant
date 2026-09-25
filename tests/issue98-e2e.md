# Issue 98 E2E Guardrail Test

This is an opt-in end-to-end test for the Trustant/OpenCode issue 98
guardrails. It is not part of `go test ./...` because it launches a real app in
the local Trustant pod, may create/delete an app user, starts OpenCode/Vite,
and uses a browser.

Run against an existing app:

```bash
TRUSTANT_E2E_APP=trutestdb2 ./tests/e2e_issue98.sh
```

Run with an explicit browser-facing domain:

```bash
TRUSTANT_E2E_DOMAIN=192.168.3.100.nip.io \
TRUSTANT_E2E_APP=trutestdb2 \
./tests/e2e_issue98.sh
```

Create a fresh app from a GitHub template and delete it afterwards:

```bash
TRUSTANT_E2E_REPO=trustable-ai/trureact \
TRUSTANT_E2E_PASSWORD=test12345 \
./tests/e2e_issue98.sh
```

Keep a freshly created app for debugging:

```bash
TRUSTANT_E2E_REPO=trustable-ai/trureact \
TRUSTANT_E2E_KEEP_APP=1 \
./tests/e2e_issue98.sh
```

Run the optional OpenCode prompt step against an existing app:

```bash
TRUSTANT_E2E_RUN_PROMPT=1 \
TRUSTANT_E2E_APP=trutestdb2 \
./tests/e2e_issue98.sh --grep 'OpenCode prompt'
```

Run the prompt step with a custom user-style request:

```bash
TRUSTANT_E2E_RUN_PROMPT=1 \
TRUSTANT_E2E_APP=trutestdb2 \
TRUSTANT_E2E_PROMPT='Aggiungi una piccola funzione di prova e verifica che funzioni.' \
TRUSTANT_E2E_PROMPT_EXPECT_CHANGES=1 \
./tests/e2e_issue98.sh --grep 'OpenCode prompt'
```

Run the model-driven action ordering proof on a temporary app:

```bash
./tests/e2e_action_workflow.sh
```

This requires a real action mutation, a later `ops ide deploy`, setup after
deploy, and `trustant_completion_check` after both. It rejects manual action
ZIP operations and deletes the temporary app when finished.

Run the real long-session compaction and persistence proof:

```bash
./tests/e2e_compaction.sh
```

This invokes OpenCode compaction and requires the first continued model turn to
receive automatic bounded recovery containing the exact active request. The
test verifies durable `automaticRecoveryCount`, a post-recovery mutation and
completion without a manual recovery-tool call, then restarts the app and
checks that the same session and marker are still present. This is a real
compacted OpenCode session and model turn, not a replay of the unit test.

Run the generated authentication proof:

```bash
./tests/e2e_auth.sh
```

The prompt and browser assertions use one app lifecycle. The OpenCode wait loop
requires a completed assistant message whose finish reason is not `tool-calls`;
an intermediate idle transition remains in progress. A final `stop` without
visible assistant text fails immediately. On reload the browser also requires
a successful backend session/identity validation; localStorage identity alone
does not count as persistence. The browser records the generated
private URL after registration, proves direct access is denied in a fresh
anonymous context that has never logged in, and retries that same URL after
logout. Same-origin private-looking GET APIs are tested after logout only when
they were observed as successful fetch/XHR traffic while authenticated; no API
path is invented by the runner.

Resume a retained app after a runner-only assertion failure while still
revalidating its persisted model trace:

```bash
TRUSTANT_E2E_APP=<app> \
TRUSTANT_E2E_RUN_PROMPT=0 \
TRUSTANT_E2E_VALIDATE_EXISTING_PROMPT=1 \
./tests/e2e_auth.sh
```

Environment variables:

- `TRUSTANT_E2E_DOMAIN`: browser-visible base domain. Defaults to the
  `trustant-ing` host suffix, then the Nuvolaris apihost, then `miniops.me`.
- `TRUSTANT_E2E_APP`: existing app to launch and verify.
- `TRUSTANT_E2E_REPO`: GitHub `org/repo` template used when creating a fresh
  app.
- `TRUSTANT_E2E_NEW_APP`: optional deterministic app name for fresh app mode.
- `TRUSTANT_E2E_PASSWORD`: password for a freshly created app.
- `TRUSTANT_E2E_KEEP_APP=1`: keep a freshly created app after the test.
- `TRUSTANT_E2E_NAMESPACE`: Kubernetes namespace, default `openserverless`.
- `TRUSTANT_E2E_POD`: Trustant pod, default `trustant-0`.
- `TRUSTANT_E2E_CONTAINER`: Trustant container, default `trustant`.
- `TRUSTANT_E2E_SKIP_BROWSER_INSTALL=1`: skip Playwright Chromium install.
- `TRUSTANT_E2E_RUN_PROMPT=1`: enable the slower model-driven OpenCode prompt
  step. Without this, the prompt test is skipped.
- `TRUSTANT_E2E_PROMPT`: prompt text to send to OpenCode. Defaults to a
  read-only checker prompt.
- `TRUSTANT_E2E_PROMPT_AGENT`: OpenCode agent, default `build`.
- `TRUSTANT_E2E_PROMPT_TIMEOUT_MS`: wait timeout for the OpenCode loop,
  default 10 minutes.
- `TRUSTANT_E2E_PROMPT_TEST_TIMEOUT_MS`: Playwright timeout for the prompt
  test, default 15 minutes.
- `TRUSTANT_E2E_AUTO_APPROVE=1`: reply `once` to pending OpenCode permission
  prompts. Leave unset when permission prompts should fail the test.
- `TRUSTANT_E2E_PROMPT_EXPECT_TOOLS=0`: allow prompts that do not use tools.
  By default the prompt step expects at least one tool call.
- `TRUSTANT_E2E_PROMPT_ALLOW_TOOL_ERRORS=1`: allow failed tool calls. Without
  it, the runner still tolerates only `list`/`ENOENT` probes for missing
  optional directories inside the app workbench, such as `packages/setup`.
- `TRUSTANT_E2E_EXPECT_OPENSERVERLESS_TOOL=1`: require an
  OpenServerless action MCP tool call in the prompt step.
- `TRUSTANT_E2E_PROMPT_EXPECT_CHANGES=1`: require new workbench changes after
  the prompt.
- `TRUSTANT_E2E_EXPECT_ACTION_WORKFLOW=1`: require the ordered action
  mutation/deploy/completion trace.
- `TRUSTANT_E2E_EXPECT_SETUP=1`: additionally require setup after the final
  deploy and before completion.

The test verifies:

- Trustant UI is reachable through `trustant.<domain>`.
- The selected app launches through `/api/launch/<app>`.
- Browser-visible `opencode.<domain>` is scoped to the launched app.
- An OpenCode session exists for the canonical workbench path.
- OpenCode MCP includes `openserverless`.
- App-local `AGENTS.md` exists and explicitly demotes `CLAUDE.md` and other
  legacy/template agent files from mandatory Trustant instructions.
- App-local `opencode.json` contains issue 98 permissions and MCP config.
- `check_openserverless_actions.sh .` passes inside the pod.
- Pod-local `http://localhost:5173` responds from inside `trustant-0`.
- Browser-visible `vite.<domain>` responds after launch/deploy.

When `TRUSTANT_E2E_RUN_PROMPT=1` is set, the second test also:

- sends a prompt to `opencode.<domain>/session/<id>/prompt_async`;
- waits for OpenCode to become idle without pending questions or permissions;
- verifies the new prompt messages used tools unless explicitly disabled;
- fails if the prompt uses raw `ops action` / `ops action *` from shell;
- optionally requires OpenServerless MCP tool use;
- reruns `check_openserverless_actions.sh .` after the prompt.

Cleanup behavior:

- the runner always calls `DELETE /api/launch` at the end to stop the OpenCode
  and Vite processes it launched;
- in fresh app mode, the runner deletes the created app unless
  `TRUSTANT_E2E_KEEP_APP=1` is set;
- in existing app mode, the app repository is never deleted.
