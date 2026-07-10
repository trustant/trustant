import assert from "node:assert/strict";
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
  assert.equal(guardrails.isActionMutation("openserverless_action_add_s3", {}), true);
  assert.equal(guardrails.isActionMutation("edit", { filePath: "/tmp/app/packages/v1/stack/stack.py" }), true);
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
    command: "cat packages/v1/stack.zip | head -c 20",
  }), false);
  assert.equal(guardrails.isManualActionZipMutation("bash", {
    command: "timeout 120 ops ide deploy",
  }), false);
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
