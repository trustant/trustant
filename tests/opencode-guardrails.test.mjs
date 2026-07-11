import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { chmodSync, mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

const isolatedHome = mkdtempSync(join(tmpdir(), "trustable-guardrails-home-"));
process.env.HOME = isolatedHome;

const guardrails = await import("../opencode-trustable-guardrails.js");

test("diagnostic request detection covers the observed issue98 language", () => {
  assert.equal(guardrails.isDiagnosticRequest("crea account e accedi non funzionano"), true);
  assert.equal(guardrails.isDiagnosticRequest("still gives a white page"), true);
  assert.equal(guardrails.isDiagnosticRequest("aggiungi una pagina profilo"), false);
});

test("mutation detection keeps read-only diagnostics available", () => {
  assert.equal(guardrails.isMutatingTool("read", { filePath: "src/App.tsx" }), false);
  assert.equal(guardrails.isMutatingTool("bash", { command: "curl -i http://localhost:5173" }), false);
  assert.equal(guardrails.isMutatingTool("edit", {}), true);
  assert.equal(guardrails.isMutatingTool("action-new", {}), true);
  assert.equal(guardrails.isMutatingTool("bash", { command: "ops ide deploy" }), true);
  assert.equal(guardrails.isMaskedCriticalCommand({ command: "timeout 120 ops ide deploy 2>&1 || true" }), true);
  assert.equal(guardrails.isMaskedCriticalCommand({ command: "timeout 120 check_trustable_app.sh . | tail -20" }), true);
  assert.equal(guardrails.isMaskedCriticalCommand({ command: "timeout 120 ops ide setup" }), false);
  assert.equal(guardrails.isManagedDevServerCommand({ command: "cd /tmp/app && npx vite --host 0.0.0.0 --port 5173 &" }), true);
  assert.equal(guardrails.isManagedDevServerCommand({ command: "kill 1234; npm run dev" }), true);
  assert.equal(guardrails.isManagedDevServerCommand({ command: "curl -i http://localhost:5173" }), false);
  assert.equal(guardrails.isActionMutation("openserverless_action_add_s3", {}), true);
  assert.equal(guardrails.isActionMutation("action-invoke", {}), false);
  assert.equal(guardrails.isActionMutation("openserverless_action_list", {}), false);
  assert.equal(guardrails.isActionMutation("openserverless_action_get", { endpoint: "v1/profile" }), false);
  assert.equal(guardrails.isActionMutation("openserverless_action_invoke", {}), false);
  assert.equal(guardrails.isActionMutation("edit", { filePath: "/tmp/app/packages/v1/stack/stack.py" }), true);
  assert.equal(guardrails.isActionMutation("bash", {
    cwd: "/tmp/app/packages/v1/stack",
    command: "sed -i 's/old/new/' stack.py",
  }), true);
  assert.equal(guardrails.isActionMutation("bash", {
    workdir: "packages/v1/stack",
    command: "printf 'value' > stack.py",
  }), true);
  assert.equal(guardrails.isSetupActionMutation("action-new", { action: "setup/database" }), true);
  assert.equal(guardrails.isSetupActionMutation("edit", { filePath: "/tmp/app/packages/setup/database/database.py" }), true);
  assert.equal(guardrails.isSetupActionMutation("bash", {
    directory: "/tmp/app/packages/setup/database",
    command: "sed -i 's/old/new/' database.py",
  }), true);
  assert.equal(guardrails.isSetupActionMutation("edit", { filePath: "/tmp/app/packages/v1/stack/stack.py" }), false);
});

test("action endpoint tracking is precise and ignores generated entrypoints", () => {
  assert.deepEqual(
    guardrails.touchedActionEndpoints("openserverless_action_add_mongodb", { endpoint: "/v1/profile/" }),
    ["v1/profile"],
  );
  assert.deepEqual(
    guardrails.touchedActionEndpoints("edit", { filePath: "/tmp/app/packages/v1/profile/profile.py" }),
    ["v1/profile"],
  );
  assert.deepEqual(
    guardrails.touchedActionEndpoints("bash", {
      workdir: "/tmp/app/packages/setup/db",
      command: "sed -i 's/old/new/' db.py",
    }),
    ["setup/db"],
  );
  assert.deepEqual(
    guardrails.touchedActionEndpoints("edit", { filePath: "/tmp/app/packages/v1/profile/__main__.py" }),
    [],
  );
  assert.deepEqual(
    guardrails.touchedActionEndpoints("openserverless_action_get", { endpoint: "v1/profile" }),
    [],
  );
});

test("raw ops and wsk action shell commands are recognized through wrappers", () => {
  assert.equal(guardrails.isRawActionCommand({ command: "ops action invoke setup/db" }), true);
  assert.equal(guardrails.isRawActionCommand({ command: "timeout 60 ops action invoke setup/db" }), true);
  assert.equal(guardrails.isRawActionCommand({ command: "env OPS_DEBUG=1 timeout 60 wsk action list" }), true);
  assert.equal(guardrails.isRawActionCommand({ command: "OPS_DEBUG=1 timeout 60 ops action invoke setup/db" }), true);
  assert.equal(guardrails.isRawActionCommand({ command: "cd /tmp/app && command ops action get v1/profile" }), true);
  assert.equal(guardrails.isRawActionCommand({ command: "sudo -u ops wsk action update v1/profile profile.zip" }), true);
  assert.equal(guardrails.isRawActionCommand({ command: "cd /tmp/app && timeout 60 wsk action invoke setup/db" }), true);
  assert.equal(guardrails.isRawActionCommand({ command: "printf '%s' 'ops action invoke is documentation'" }), false);
  assert.equal(guardrails.isRawActionCommand({ command: "timeout 120 ops ide deploy" }), false);
});

test("raw action shell commands are rejected before shell execution", async () => {
  const directory = mkdtempSync(join(tmpdir(), "trustable-raw-action-"));
  const plugin = await guardrails.default({ directory });
  for (const [index, command] of [
    "timeout 60 ops action invoke setup/db",
    "cd /tmp/app && env DEBUG=1 timeout 60 wsk action list",
  ].entries()) {
    await assert.rejects(
      plugin["tool.execute.before"](
        { tool: "bash", sessionID: `ses_raw_action_${Date.now()}_${index}`, callID: `call_raw_action_${index}` },
        { args: { command } },
      ),
      /raw ops action\/wsk action shell commands are forbidden.*OpenServerless MCP/s,
    );
  }
});

test("managed ops ide login is recognized through prefixes and concatenated commands", () => {
  assert.equal(guardrails.isManagedIdeLoginCommand({ command: "ops ide login" }), true);
  assert.equal(guardrails.isManagedIdeLoginCommand({ command: "timeout 60 ops ide login" }), true);
  assert.equal(guardrails.isManagedIdeLoginCommand({ command: "env OPS_DEBUG=1 timeout 60 ops ide login" }), true);
  assert.equal(guardrails.isManagedIdeLoginCommand({ command: "cd /tmp/app && OPS_DEBUG=1 command ops ide login" }), true);
  assert.equal(guardrails.isManagedIdeLoginCommand({ command: "echo 'ops ide login is managed'" }), false);
  assert.equal(guardrails.isManagedIdeLoginCommand({ command: "timeout 120 ops ide deploy" }), false);
  assert.equal(guardrails.isManagedIdeLoginCommand({ command: "timeout 120 ops ide setup" }), false);
});

test("managed ops ide login is rejected before shell execution", async () => {
  const directory = mkdtempSync(join(tmpdir(), "trustable-managed-login-"));
  const plugin = await guardrails.default({ directory });
  for (const [index, command] of [
    "timeout 60 ops ide login",
    "cd /tmp/app && env OPS_DEBUG=1 timeout 60 ops ide login",
  ].entries()) {
    await assert.rejects(
      plugin["tool.execute.before"](
        { tool: "bash", sessionID: `ses_managed_login_${Date.now()}_${index}`, callID: `call_managed_login_${index}` },
        { args: { command } },
      ),
      /Trustable already authenticated and configured this app.*Do not rerun ops ide login/s,
    );
  }
});

test("browser interactions require fresh evidence after a bounded run", async () => {
  const directory = mkdtempSync(join(tmpdir(), "trustable-browser-budget-"));
  const plugin = await guardrails.default({ directory });
  const sessionID = `ses_browser_budget_${Date.now()}`;

  for (let index = 0; index < 12; index++) {
    await plugin["tool.execute.before"](
      { tool: "browser_browser_interact", sessionID, callID: `before_${index}` },
      { args: { action: "fill", kind: "label", target: "Password", value: "secret" } },
    );
    await plugin["tool.execute.after"](
      { tool: "browser_browser_interact", sessionID, callID: `after_${index}`, args: { action: "fill", kind: "label", target: "Password", value: "secret" } },
      { output: "ok" },
    );
  }

  await assert.rejects(
    plugin["tool.execute.before"](
      { tool: "browser_browser_interact", sessionID, callID: "blocked" },
      { args: { action: "click", kind: "role", role: "button", target: "Accedi" } },
    ),
    /browser diagnostic circuit breaker.*snapshot or browser_browser_diagnostics/s,
  );

  await plugin["tool.execute.after"](
    { tool: "browser_browser_snapshot", sessionID, callID: "snapshot", args: {} },
    { output: "fresh state" },
  );
  await plugin["tool.execute.before"](
    { tool: "browser_browser_interact", sessionID, callID: "unblocked" },
    { args: { action: "click", kind: "role", role: "button", target: "Accedi" } },
  );
});

test("manual action ZIP mutations are denied but read-only inspection is allowed", () => {
  assert.equal(guardrails.isManualActionZipMutation("write", {
    filePath: "/tmp/app/packages/v1/stack/stack.zip",
  }), true);
  assert.equal(guardrails.isManualActionZipMutation("edit", {
    filePath: "/tmp/app/packages/v1/stack/stack.py",
  }), false);
  assert.equal(guardrails.isManualActionZipMutation("bash", {
    command: "cd packages/setup/seed && python3 -c \"import zipfile; zipfile.ZipFile('seed.zip', 'w')\"",
  }), true);
  assert.equal(guardrails.isManualActionZipMutation("bash", {
    command: "rm -f packages/v1/stack.zip",
  }), true);
  assert.equal(guardrails.isManualActionZipMutation("bash", {
    workdir: "/tmp/app/packages/v1/stack",
    command: "python3 -m zipfile -c stack.zip .",
  }), true);
  assert.equal(guardrails.isManualActionZipMutation("bash", {
    command: "cat packages/v1/stack.zip | head -c 20",
  }), false);
  assert.equal(guardrails.isManualActionZipMutation("bash", {
    command: "timeout 120 ops ide deploy",
  }), false);
});

test("critical validation commands cannot hide failures", async () => {
  const directory = mkdtempSync(join(tmpdir(), "trustable-guardrails-masking-"));
  const plugin = await guardrails.default({ directory });
  await assert.rejects(
    plugin["tool.execute.before"](
      { tool: "bash", sessionID: `ses_mask_${Date.now()}`, callID: "call_mask" },
      { args: { command: "timeout 120 ops ide deploy 2>&1 || true" } },
    ),
    /do not mask critical command failures/,
  );
});

test("Trustable-managed dev processes cannot be killed or replaced", async () => {
  const directory = mkdtempSync(join(tmpdir(), "trustable-guardrails-process-"));
  const plugin = await guardrails.default({ directory });
  await assert.rejects(
    plugin["tool.execute.before"](
      { tool: "bash", sessionID: `ses_process_${Date.now()}`, callID: "call_process" },
      { args: { command: "kill 1234; cd /tmp/app && npx vite --port 5173 &" } },
    ),
    /do not kill managed processes or start Vite/,
  );
});

test("action changes require deploy before setup and completion", async (t) => {
  const directory = mkdtempSync(join(tmpdir(), "trustable-guardrails-action-"));
  const fakeBin = join(directory, "bin");
  mkdirSync(fakeBin);
  const checker = join(fakeBin, "check_openserverless_actions.sh");
  writeFileSync(checker, "#!/usr/bin/env bash\nexit 0\n");
  chmodSync(checker, 0o755);
  const oldPath = process.env.PATH;
  process.env.PATH = `${fakeBin}:${oldPath}`;
  t.after(() => { process.env.PATH = oldPath; });

  const plugin = await guardrails.default({ directory });
  const sessionID = `ses_deploy_${Date.now()}`;
  await plugin["tool.execute.after"](
    { tool: "edit", sessionID, callID: "call_action", args: { filePath: join(directory, "packages/v1/stack/stack.py") } },
    { output: "Edit applied successfully." },
  );

  await assert.rejects(
    plugin["tool.execute.before"](
      { tool: "bash", sessionID, callID: "call_setup" },
      { args: { command: "timeout 120 ops ide setup" } },
    ),
    /deploy before ops ide setup/,
  );

  const blocked = await plugin.tool.trustable_completion_check.execute(
    {},
    { sessionID, directory, worktree: directory },
  );
  assert.match(blocked, /Action source changed after the last verified deployment/);

  await plugin["tool.execute.after"](
    { tool: "bash", sessionID, callID: "call_deploy", args: { command: "timeout 120 ops ide deploy" } },
    { output: "ok: updated action v1/stack" },
  );
  await plugin["tool.execute.before"](
    { tool: "bash", sessionID, callID: "call_setup_after" },
    { args: { command: "timeout 120 ops ide setup" } },
  );
});

test("setup action changes require setup after deploy before completion", async (t) => {
  const directory = mkdtempSync(join(tmpdir(), "trustable-guardrails-setup-"));
  const fakeBin = join(directory, "bin");
  mkdirSync(fakeBin);
  const checker = join(fakeBin, "check_openserverless_actions.sh");
  writeFileSync(checker, "#!/usr/bin/env bash\nexit 0\n");
  chmodSync(checker, 0o755);
  const oldPath = process.env.PATH;
  process.env.PATH = `${fakeBin}:${oldPath}`;
  t.after(() => { process.env.PATH = oldPath; });

  const plugin = await guardrails.default({ directory });
  const sessionID = `ses_setup_${Date.now()}`;
  await plugin["tool.execute.after"](
    { tool: "edit", sessionID, callID: "call_setup_edit", args: { filePath: join(directory, "packages/setup/database/database.py") } },
    { output: "Edit applied successfully." },
  );
  await plugin["tool.execute.after"](
    { tool: "bash", sessionID, callID: "call_setup_deploy", args: { command: "timeout 120 ops ide deploy" } },
    { output: "ok: updated action setup/database" },
  );

  const blocked = await plugin.tool.trustable_completion_check.execute(
    {},
    { sessionID, directory, worktree: directory },
  );
  assert.match(blocked, /setup action changed and was deployed but setup has not run/i);

  await plugin["tool.execute.after"](
    { tool: "bash", sessionID, callID: "call_setup_run", args: { command: "timeout 120 ops ide setup" } },
    { output: "Setup completed." },
  );
  const system = { system: [] };
  await plugin["experimental.chat.system.transform"]({ sessionID }, system);
  assert.doesNotMatch(system.system.join(" "), /ACTION SETUP IS REQUIRED/);
});

test("shell mutations inherit action and setup scope from their working directory", async () => {
  const directory = mkdtempSync(join(tmpdir(), "trustable-guardrails-cwd-"));
  const plugin = await guardrails.default({ directory });
  const sessionID = `ses_cwd_${Date.now()}`;

  await plugin["tool.execute.after"](
    {
      tool: "bash",
      sessionID,
      callID: "call_cwd_mutation",
      args: {
        workdir: join(directory, "packages/setup/database"),
        command: "sed -i 's/old/new/' database.py",
      },
    },
    { output: "updated database.py" },
  );

  const blocked = await plugin.tool.trustable_completion_check.execute(
    {},
    { sessionID, directory, worktree: directory },
  );
  assert.match(blocked, /Action source changed after the last verified deployment/);

  const system = { system: [] };
  await plugin["experimental.chat.system.transform"]({ sessionID }, system);
  assert.match(system.system.join(" "), /ACTION DEPLOY IS REQUIRED/);
  assert.match(system.system.join(" "), /ACTION SETUP IS REQUIRED/);
});

test("reported bugs require reproduction before edits", async () => {
  const directory = mkdtempSync(join(tmpdir(), "trustable-guardrails-app-"));
  const plugin = await guardrails.default({ directory });
  const sessionID = `ses_diag_${Date.now()}`;

  await plugin["chat.message"](
    { sessionID },
    {
      message: { role: "user" },
      parts: [{ type: "text", text: "login non funziona e torna alla homepage" }],
    },
  );

  await assert.rejects(
    plugin["tool.execute.before"](
      { tool: "edit", sessionID, callID: "call_1" },
      { args: { filePath: "src/App.tsx" } },
    ),
    /reproduce the exact symptom/,
  );

  await plugin.tool.trustable_diagnostic_checkpoint.execute(
    { phase: "reproduced", evidence: "Clicked ACCEDI; URL became /login and the homepage remained visible." },
    { sessionID, directory, worktree: directory },
  );

  await plugin["tool.execute.before"](
    { tool: "edit", sessionID, callID: "call_2" },
    { args: { filePath: "src/App.tsx" } },
  );
});

test("compaction blocks mutations and unverified claims", async () => {
  const directory = mkdtempSync(join(tmpdir(), "trustable-guardrails-app-"));
  const plugin = await guardrails.default({ directory });
  const sessionID = `ses_compact_${Date.now()}`;

  await plugin.event({ event: { type: "session.compacted", properties: { sessionID } } });
  await assert.rejects(
    plugin["tool.execute.before"](
      { tool: "write", sessionID, callID: "call_3" },
      { args: { filePath: "src/App.tsx" } },
    ),
    /trustable_context_recover/,
  );

  await plugin["tool.execute.after"](
    { tool: "edit", sessionID, callID: "call_4", args: { filePath: "src/App.tsx" } },
    { output: "Edit applied successfully." },
  );
  const text = { text: "Fixed, prova ora." };
  await plugin["experimental.text.complete"](
    { sessionID, messageID: "msg", partID: "part" },
    text,
  );
  assert.match(text.text, /completion gate|diagnostic gate/);
});

test("dirty sessions cannot stop with neutral final wording", async () => {
  const directory = mkdtempSync(join(tmpdir(), "trustable-neutral-final-"));
  const plugin = await guardrails.default({ directory });
  const sessionID = `ses_neutral_final_${Date.now()}`;

  await plugin["tool.execute.after"](
    { tool: "edit", sessionID, callID: "edit", args: { filePath: join(directory, "src", "App.tsx") } },
    { output: "updated" },
  );
  const text = { text: "Ho aggiornato la pagina come richiesto." };
  await plugin["experimental.text.complete"](
    { sessionID, messageID: "msg", partID: "part" },
    text,
  );
  assert.match(text.text, /completion gate.*not verified/i);
});

test("application test discovery does not impose a framework on apps without tests", async () => {
  const directory = mkdtempSync(join(tmpdir(), "trustable-app-tests-empty-"));
  writeFileSync(join(directory, "package.json"), JSON.stringify({ scripts: { build: "echo build" } }));

  const discovered = guardrails.discoverApplicationTestSuites(directory);
  assert.deepEqual(discovered.suites, []);
  assert.equal(discovered.error, "");

  const result = await guardrails.runApplicationTests(directory);
  assert.equal(result.passed, true);
  assert.match(result.output, /SKIP/);
  assert.match(result.output, /no framework is imposed/i);
});

test("application test gate executes existing Go, Python, and JavaScript suites", async () => {
  const directory = mkdtempSync(join(tmpdir(), "trustable-app-tests-mixed-"));

  writeFileSync(join(directory, "go.mod"), "module example.test/app\n\ngo 1.22\n");
  writeFileSync(join(directory, "value.go"), "package app\nfunc Value() int { return 42 }\n");
  writeFileSync(join(directory, "value_test.go"), "package app\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value() != 42 { t.Fatal(\"wrong value\") } }\n");

  mkdirSync(join(directory, "python"));
  writeFileSync(join(directory, "python", "test_value.py"), [
    "import unittest",
    "class ValueTest(unittest.TestCase):",
    "    def test_value(self):",
    "        self.assertEqual(42, 42)",
    "if __name__ == '__main__':",
    "    unittest.main()",
    "",
  ].join("\n"));

  mkdirSync(join(directory, "frontend"));
  writeFileSync(join(directory, "frontend", "package.json"), JSON.stringify({
    type: "module",
    scripts: { test: "node --test" },
  }));
  writeFileSync(join(directory, "frontend", "value.test.js"), [
    "import assert from 'node:assert/strict';",
    "import test from 'node:test';",
    "test('value', () => assert.equal(42, 42));",
    "",
  ].join("\n"));

  const discovered = guardrails.discoverApplicationTestSuites(directory);
  assert.equal(discovered.error, "");
  assert.equal(discovered.suites.length, 3);

  const result = await guardrails.runApplicationTests(directory);
  assert.equal(result.passed, true, result.output);
  assert.match(result.output, /Go tests .*: PASS/);
  assert.match(result.output, /Python tests \(unittest; python; test_\*\.py\): PASS/);
  assert.match(result.output, /JavaScript tests .*: PASS/);
});

test("existing critical action tests are blocking", async () => {
  const directory = mkdtempSync(join(tmpdir(), "trustable-app-tests-critical-"));
  const action = join(directory, "packages", "v1", "auth");
  mkdirSync(action, { recursive: true });
  writeFileSync(join(action, "test_auth.py"), [
    "import unittest",
    "class AuthTest(unittest.TestCase):",
    "    def test_token_identity(self):",
    "        self.assertEqual('browser-user', 'token-user')",
    "if __name__ == '__main__':",
    "    unittest.main()",
    "",
  ].join("\n"));

  const discovered = guardrails.discoverApplicationTestSuites(directory);
  assert.equal(discovered.suites.length, 1);
  assert.equal(discovered.suites[0].critical, true);

  const result = await guardrails.runApplicationTests(directory, ["v1/auth"]);
  assert.equal(result.passed, false);
  assert.match(result.output, /focused action tests: PASS/);
  assert.match(result.output, /Python tests \(unittest; packages\/v1\/auth; test_\*\.py\) \[critical\]: FAIL/);
  assert.match(result.output, /browser-user.*token-user|token-user.*browser-user/s);
});

test("JavaScript test scripts cannot install or download dependencies", async () => {
  const directory = mkdtempSync(join(tmpdir(), "trustable-app-tests-download-"));
  writeFileSync(join(directory, "package.json"), JSON.stringify({
    scripts: { test: "npx vitest run" },
  }));
  writeFileSync(join(directory, "app.test.js"), "throw new Error('must not execute');\n");

  const result = await guardrails.runApplicationTests(directory);
  assert.equal(result.passed, false);
  assert.match(result.output, /may install or download dependencies/);
});

test("trustable completion is blocked by a failing existing application test", async (t) => {
  const directory = mkdtempSync(join(tmpdir(), "trustable-completion-tests-"));
  execFileSync("git", ["init", "--quiet"], { cwd: directory });
  const fakeBin = join(directory, "bin");
  mkdirSync(fakeBin);
  const checker = join(fakeBin, "check_trustable_app.sh");
  writeFileSync(checker, "#!/usr/bin/env bash\necho contracts-pass\nexit 0\n");
  chmodSync(checker, 0o755);
  writeFileSync(join(directory, "test_completion.py"), [
    "import unittest",
    "class CompletionTest(unittest.TestCase):",
    "    def test_completion(self):",
    "        self.fail('regression remains')",
    "",
  ].join("\n"));

  const oldPath = process.env.PATH;
  process.env.PATH = `${fakeBin}:${oldPath}`;
  t.after(() => { process.env.PATH = oldPath; });

  const plugin = await guardrails.default({ directory });
  const result = await plugin.tool.trustable_completion_check.execute(
    {},
    { sessionID: `ses_application_tests_${Date.now()}`, directory, worktree: directory },
  );
  assert.match(result, /Trustable completion gate failed/);
  assert.match(result, /application tests: FAIL/);
  assert.match(result, /regression remains/);
});

test("trustable completion accepts a passing existing application test", async (t) => {
  const directory = mkdtempSync(join(tmpdir(), "trustable-completion-tests-pass-"));
  execFileSync("git", ["init", "--quiet"], { cwd: directory });
  const fakeBin = join(directory, "bin");
  mkdirSync(fakeBin);
  const checker = join(fakeBin, "check_trustable_app.sh");
  writeFileSync(checker, "#!/usr/bin/env bash\necho contracts-pass\nexit 0\n");
  chmodSync(checker, 0o755);
  writeFileSync(join(directory, "test_completion.py"), [
    "import unittest",
    "class CompletionTest(unittest.TestCase):",
    "    def test_completion(self):",
    "        self.assertEqual('token-user', 'token-user')",
    "",
  ].join("\n"));

  const oldPath = process.env.PATH;
  process.env.PATH = `${fakeBin}:${oldPath}`;
  t.after(() => { process.env.PATH = oldPath; });

  const plugin = await guardrails.default({ directory });
  const result = await plugin.tool.trustable_completion_check.execute(
    {},
    { sessionID: `ses_application_tests_pass_${Date.now()}`, directory, worktree: directory },
  );
  assert.match(result, /Trustable completion gate passed/);
  assert.match(result, /application tests: PASS/);
});

test("action completion remains blocked until every persisted endpoint has a focused test", async (t) => {
  const directory = mkdtempSync(join(tmpdir(), "trustable-action-test-required-"));
  execFileSync("git", ["init", "--quiet"], { cwd: directory });
  const fakeBin = join(directory, "bin");
  mkdirSync(fakeBin);
  for (const name of ["check_trustable_app.sh", "check_openserverless_actions.sh"]) {
    const checker = join(fakeBin, name);
    writeFileSync(checker, "#!/usr/bin/env bash\nexit 0\n");
    chmodSync(checker, 0o755);
  }
  const action = join(directory, "packages", "v1", "profile");
  mkdirSync(action, { recursive: true });
  writeFileSync(join(action, "profile.py"), "def main(args):\n    return {'ok': True}\n");

  const oldPath = process.env.PATH;
  process.env.PATH = `${fakeBin}:${oldPath}`;
  t.after(() => { process.env.PATH = oldPath; });

  const sessionID = `ses_action_test_required_${Date.now()}`;
  const plugin = await guardrails.default({ directory });
  await plugin["tool.execute.after"](
    { tool: "openserverless_action_add_mongodb", sessionID, callID: "call_action", args: { endpoint: "v1/profile" } },
    { output: "Action source created." },
  );
  await plugin["tool.execute.after"](
    { tool: "action-new", sessionID, callID: "call_second_action", args: { endpoint: "v1/session" } },
    { output: "Second action source created." },
  );
  await plugin["tool.execute.after"](
    { tool: "bash", sessionID, callID: "call_deploy", args: { command: "timeout 120 ops ide deploy" } },
    { output: "Deployment completed." },
  );

  const reloadedPlugin = await guardrails.default({ directory });
  const result = await reloadedPlugin.tool.trustable_completion_check.execute(
    {},
    { sessionID, directory, worktree: directory },
  );
  assert.match(result, /Trustable completion gate failed/);
  assert.match(result, /Missing executable focused application test for: v1\/profile, v1\/session/);

  const system = { system: [] };
  await reloadedPlugin["experimental.chat.system.transform"]({ sessionID }, system);
  assert.match(system.system.join(" "), /FOCUSED TESTS ARE REQUIRED FOR ACTION ENDPOINTS: v1\/profile, v1\/session/);
});

test("focused action test is executed and clears endpoint debt after completion", async (t) => {
  const directory = mkdtempSync(join(tmpdir(), "trustable-action-test-pass-"));
  execFileSync("git", ["init", "--quiet"], { cwd: directory });
  const fakeBin = join(directory, "bin");
  mkdirSync(fakeBin);
  for (const name of ["check_trustable_app.sh", "check_openserverless_actions.sh"]) {
    const checker = join(fakeBin, name);
    writeFileSync(checker, "#!/usr/bin/env bash\nexit 0\n");
    chmodSync(checker, 0o755);
  }
  const action = join(directory, "packages", "v1", "profile");
  mkdirSync(action, { recursive: true });
  writeFileSync(join(action, "profile.py"), "def main(args):\n    return {'ok': True}\n");
  const tests = join(directory, "tests", "actions", "v1", "profile");
  mkdirSync(tests, { recursive: true });
  writeFileSync(join(tests, "test_profile.py"), [
    "import unittest",
    "class ProfileActionTest(unittest.TestCase):",
    "    def test_profile_contract(self):",
    "        self.assertEqual({'ok': True}, {'ok': True})",
    "",
  ].join("\n"));

  const oldPath = process.env.PATH;
  process.env.PATH = `${fakeBin}:${oldPath}`;
  t.after(() => { process.env.PATH = oldPath; });

  const sessionID = `ses_action_test_pass_${Date.now()}`;
  const plugin = await guardrails.default({ directory });
  await plugin["tool.execute.after"](
    { tool: "openserverless_action_add_mongodb", sessionID, callID: "call_action", args: { endpoint: "v1/profile" } },
    { output: "Action source and focused test created." },
  );
  await plugin["tool.execute.after"](
    { tool: "bash", sessionID, callID: "call_deploy", args: { command: "timeout 120 ops ide deploy" } },
    { output: "Deployment completed." },
  );

  const result = await plugin.tool.trustable_completion_check.execute(
    {},
    { sessionID, directory, worktree: directory },
  );
  assert.match(result, /Trustable completion gate passed/);
  assert.match(result, /focused action tests: PASS/);
  assert.match(result, /Covered endpoints: v1\/profile/);
  assert.match(result, /Python tests .*: PASS/);

  const system = { system: [] };
  await plugin["experimental.chat.system.transform"]({ sessionID }, system);
  assert.doesNotMatch(system.system.join(" "), /FOCUSED TESTS ARE REQUIRED/);
});
