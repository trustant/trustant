import { tool } from "@opencode-ai/plugin";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { basename, join } from "node:path";

const STATE_DIR = join(homedir(), ".cache", "trustable", "opencode-guardrails");
const COMPLETION_WORDS = /\b(done|complete|completed|fixed|resolved|working|success|risolto|risolta|completato|completata|funziona|prova ora)\b/i;
const DIAGNOSTIC_REQUEST = /(does(?:n't| not) work|not working|still (?:fails|broken|doesn't)|failed|broken|white page|blank page|error|bug|fix(?: this)?|non funziona|non funzionano|non fa|non fanno|ancora|errore|problema|pagina bianca|bloccato)/i;
const MUTATING_BASH = /(^|[;&|]\s*)(sed\s+-i|perl\s+-pi|rm\s|mv\s|cp\s|install\s|mkdir\s|touch\s|truncate\s|tee\s|git\s+(add|commit|merge|rebase|reset|checkout|switch|restore|clean)|npm\s+(install|uninstall|update)|ops\s+ide\s+(deploy|setup|redeploy)|python(?:3)?\s+-c\s+.*(?:write|unlink|remove|rename))\b|(^|[^>])>{1,2}[^&]/i;

const CRITICAL_SYSTEM = [
  "Trustable enforcement is active.",
  "After session compaction, call trustable_context_recover before any edit, write, action mutation, or deploy.",
  "For a reported bug, reproduce the exact user-visible symptom and record evidence with trustable_diagnostic_checkpoint before changing source.",
  "After source changes, call trustable_completion_check before claiming that work is fixed, complete, or ready for the user.",
  "Two repeated completion failures open a circuit breaker and require fresh reproduction evidence before more changes.",
].join(" ");

export function isDiagnosticRequest(text) {
  return DIAGNOSTIC_REQUEST.test(text || "");
}

export function isMutatingTool(toolID, args = {}) {
  if (["edit", "write", "patch", "apply_patch"].includes(toolID)) return true;
  if (/^(action[-_]|openserverless_action_)/.test(toolID)) return true;
  if (toolID !== "bash") return false;
  return MUTATING_BASH.test(String(args.command || ""));
}

function defaultState() {
  return {
    needsRecovery: false,
    diagnosticRequired: false,
    reproduced: false,
    dirty: false,
    verified: true,
    circuitOpen: false,
    failureSignature: "",
    repeatedFailures: 0,
    evidence: "",
  };
}

function statePath(sessionID) {
  return join(STATE_DIR, `${sessionID.replace(/[^a-zA-Z0-9_.-]/g, "_")}.json`);
}

function loadState(sessionID) {
  try {
    return { ...defaultState(), ...JSON.parse(readFileSync(statePath(sessionID), "utf8")) };
  } catch {
    return defaultState();
  }
}

function saveState(sessionID, state) {
  mkdirSync(STATE_DIR, { recursive: true });
  writeFileSync(statePath(sessionID), `${JSON.stringify(state, null, 2)}\n`, { mode: 0o600 });
}

function readGuidance(directory, name, maxBytes = 64 * 1024) {
  const path = join(directory, name);
  if (!existsSync(path)) return `${name}: missing`;
  const text = readFileSync(path, "utf8");
  return `===== ${name} =====\n${text.slice(0, maxBytes)}`;
}

function sanitizedOpenCodeConfig(directory) {
  const path = join(directory, "opencode.json");
  if (!existsSync(path)) return "opencode.json: missing";
  try {
    const config = JSON.parse(readFileSync(path, "utf8"));
    return [
      "===== opencode.json (sanitized) =====",
      `model: ${config.model || "unset"}`,
      `small_model: ${config.small_model || "unset"}`,
      `instructions: ${JSON.stringify(config.instructions || [])}`,
      `mcp servers: ${Object.keys(config.mcp || {}).sort().join(", ") || "none"}`,
      `plugins: ${JSON.stringify(config.plugin || [])}`,
      `permission groups: ${Object.keys(config.permission || {}).sort().join(", ") || "none"}`,
    ].join("\n");
  } catch (error) {
    return `opencode.json: invalid JSON (${error.message})`;
  }
}

async function run(command, cwd, timeoutMs = 180_000) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const proc = Bun.spawn(["/bin/bash", "-lc", command], {
      cwd,
      stdout: "pipe",
      stderr: "pipe",
      signal: controller.signal,
      env: process.env,
    });
    const [stdout, stderr, exitCode] = await Promise.all([
      new Response(proc.stdout).text(),
      new Response(proc.stderr).text(),
      proc.exited,
    ]);
    return { exitCode, output: `${stdout}${stderr ? `\nSTDERR:\n${stderr}` : ""}`.trim() };
  } catch (error) {
    return { exitCode: 124, output: `Command failed or timed out: ${error.message}` };
  } finally {
    clearTimeout(timer);
  }
}

async function completionChecks(directory) {
  const checks = [];
  checks.push(["git diff", await run("git diff --check", directory, 60_000)]);
  checks.push(["Trustable contracts", await run("timeout 120 check_trustable_app.sh .", directory, 140_000)]);

  const packagePath = join(directory, "package.json");
  if (existsSync(packagePath)) {
    try {
      const pkg = JSON.parse(readFileSync(packagePath, "utf8"));
      if (pkg.scripts?.build) {
        checks.push(["frontend build", await run("timeout 180 npm run build", directory, 200_000)]);
      }
    } catch (error) {
      checks.push(["package.json", { exitCode: 1, output: error.message }]);
    }
  }

  const failed = checks.filter(([, result]) => result.exitCode !== 0);
  const output = checks.map(([name, result]) => [
    `===== ${name}: ${result.exitCode === 0 ? "PASS" : "FAIL"} =====`,
    result.output || "(no output)",
  ].join("\n")).join("\n\n");
  return { passed: failed.length === 0, output };
}

export default async function TrustableGuardrails({ directory }) {
  const states = new Map();
  const stateFor = (sessionID) => {
    if (!states.has(sessionID)) states.set(sessionID, loadState(sessionID));
    return states.get(sessionID);
  };

  return {
    tool: {
      trustable_context_recover: tool({
        description: "Recover mandatory Trustable app context after OpenCode compaction. Reads authoritative instructions, sanitized MCP/model configuration, git status, and project layout, then unlocks mutations.",
        args: {},
        async execute(_args, context) {
          const current = stateFor(context.sessionID);
          const status = await run("git status --short --branch", context.directory, 30_000);
          const layout = await run("find . -maxdepth 2 -type d -not -path './.git*' -not -path './node_modules*' | sort | head -120", context.directory, 30_000);
          current.needsRecovery = false;
          saveState(context.sessionID, current);
          return [
            CRITICAL_SYSTEM,
            readGuidance(context.directory, "AGENTS.md"),
            readGuidance(context.directory, ".openserverless-contract.md"),
            readGuidance(context.directory, "opencode.md"),
            sanitizedOpenCodeConfig(context.directory),
            "===== git status =====",
            status.output || "(clean)",
            "===== project layout =====",
            layout.output || basename(context.directory),
          ].join("\n\n");
        },
      }),

      trustable_diagnostic_checkpoint: tool({
        description: "Record concrete reproduction evidence before modifying a reported bug. Use phase=reproduced only after observing the exact symptom through browser, HTTP, logs, or a deterministic test.",
        args: {
          phase: tool.schema.enum(["reproduced", "blocked"]),
          evidence: tool.schema.string().min(12).describe("Concise observed evidence: command/tool, URL or test, and actual result."),
        },
        async execute(args, context) {
          const current = stateFor(context.sessionID);
          current.evidence = args.evidence;
          current.reproduced = args.phase === "reproduced";
          current.diagnosticRequired = args.phase !== "reproduced";
          if (current.reproduced) {
            current.circuitOpen = false;
            current.repeatedFailures = 0;
            current.failureSignature = "";
          }
          saveState(context.sessionID, current);
          return args.phase === "reproduced"
            ? "Diagnostic checkpoint recorded. Source mutations are unlocked for one evidence-based fix attempt."
            : "Diagnostic blocker recorded. Do not modify source until the symptom can be reproduced.";
        },
      }),

      trustable_completion_check: tool({
        description: "Run the deterministic Trustable completion gate after source changes: git diff validation, OpenServerless and frontend contract checkers, and the frontend build when available.",
        args: {},
        async execute(_args, context) {
          const current = stateFor(context.sessionID);
          const result = await completionChecks(context.directory);
          if (result.passed) {
            current.dirty = false;
            current.verified = true;
            current.diagnosticRequired = false;
            current.reproduced = false;
            current.circuitOpen = false;
            current.failureSignature = "";
            current.repeatedFailures = 0;
            saveState(context.sessionID, current);
            return `Trustable completion gate passed.\n\n${result.output}`;
          }

          const signature = createHash("sha256").update(result.output).digest("hex").slice(0, 16);
          current.repeatedFailures = current.failureSignature === signature ? current.repeatedFailures + 1 : 1;
          current.failureSignature = signature;
          current.verified = false;
          current.dirty = true;
          if (current.repeatedFailures >= 2) {
            current.circuitOpen = true;
            current.diagnosticRequired = true;
            current.reproduced = false;
          }
          saveState(context.sessionID, current);
          const breaker = current.circuitOpen
            ? "\n\nCircuit breaker OPEN: the same completion failure occurred twice. Reproduce the remaining symptom and call trustable_diagnostic_checkpoint before another source change."
            : "\n\nFix the reported failure, then rerun trustable_completion_check.";
          return `Trustable completion gate failed.\n\n${result.output}${breaker}`;
        },
      }),
    },

    event: async ({ event }) => {
      if (event.type !== "session.compacted") return;
      const sessionID = event.properties?.sessionID;
      if (!sessionID) return;
      const current = stateFor(sessionID);
      current.needsRecovery = true;
      current.verified = false;
      saveState(sessionID, current);
    },

    "chat.message": async (input, output) => {
      if (output.message?.role !== "user") return;
      const text = (output.parts || []).filter((part) => part.type === "text").map((part) => part.text || "").join("\n");
      if (!isDiagnosticRequest(text)) return;
      const current = stateFor(input.sessionID);
      current.diagnosticRequired = true;
      current.reproduced = false;
      current.verified = false;
      current.evidence = "";
      saveState(input.sessionID, current);
    },

    "experimental.chat.system.transform": async (input, output) => {
      const current = input.sessionID ? stateFor(input.sessionID) : defaultState();
      let status = CRITICAL_SYSTEM;
      if (current.needsRecovery) status += " CONTEXT RECOVERY IS REQUIRED NOW.";
      if (current.diagnosticRequired && !current.reproduced) status += " DIAGNOSTIC REPRODUCTION IS REQUIRED BEFORE SOURCE CHANGES.";
      if (current.dirty && !current.verified) status += " THE CURRENT CHANGES HAVE NOT PASSED THE COMPLETION GATE.";
      output.system.push(status);
    },

    "experimental.session.compacting": async (_input, output) => {
      output.context.push(CRITICAL_SYSTEM);
      output.context.push("Compaction recovery state will block source mutations until trustable_context_recover is called in the continued turn.");
    },

    "tool.execute.before": async (input, output) => {
      if (["trustable_context_recover", "trustable_diagnostic_checkpoint", "trustable_completion_check"].includes(input.tool)) return;
      const current = stateFor(input.sessionID);
      if (!isMutatingTool(input.tool, output.args)) return;
      if (current.needsRecovery) {
        throw new Error("Trustable guardrail: session context was compacted. Call trustable_context_recover before modifying files, actions, or deployment state.");
      }
      if ((current.diagnosticRequired && !current.reproduced) || current.circuitOpen) {
        throw new Error("Trustable diagnostic circuit breaker: reproduce the exact symptom and call trustable_diagnostic_checkpoint with concrete evidence before modifying source.");
      }
    },

    "tool.execute.after": async (input, output) => {
      if (!isMutatingTool(input.tool, input.args)) return;
      if (/^(Error:|Could not find oldString|No changes to apply)/i.test(output.output || "")) return;
      const current = stateFor(input.sessionID);
      current.dirty = true;
      current.verified = false;
      saveState(input.sessionID, current);
    },

    "experimental.text.complete": async (input, output) => {
      const current = stateFor(input.sessionID);
      if (!COMPLETION_WORDS.test(output.text || "")) return;
      if (current.diagnosticRequired && !current.reproduced) {
        output.text = "Trustable diagnostic gate: the reported symptom has not been reproduced yet. Continue with read-only diagnostics and record evidence with trustable_diagnostic_checkpoint before changing source.";
        return;
      }
      if (current.dirty && !current.verified) {
        output.text = "Trustable completion gate: the current changes are not verified. Continue by calling trustable_completion_check; do not ask the user to test an unverified result.";
      }
    },
  };
}
