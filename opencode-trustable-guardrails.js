import { tool } from "@opencode-ai/plugin";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { basename, dirname, join, relative } from "node:path";

const OPENCODE_DATA_DIR = join(process.env.XDG_DATA_HOME || join(homedir(), ".local", "share"), "opencode");
const STATE_DIR = join(OPENCODE_DATA_DIR, "trustable-guardrails");
const LEGACY_STATE_DIR = join(homedir(), ".cache", "trustable", "opencode-guardrails");
const DIAGNOSTIC_REQUEST = /(does(?:n't| not) work|not working|still (?:fails|broken|doesn't)|failed|broken|white page|blank page|error|bug|fix(?: this)?|non funziona|non funzionano|non fa|non fanno|non si (?:sente|vede|apre|salva|carica)|non (?:viene|vengono|riesce|riescono|resta|restano)|ancora|errore|problema|pagina bianca|bloccato)/i;
const BROWSER_DIAGNOSTIC_REQUEST = /(browser|pagina|form|login|log in|register|registration|registr|auth|session|reload|refresh|routing|route|redirect|music|audio|sound|suono|musica|schermata|pulsante|button|link)/i;
const SYNTHETIC_CONTINUATION = /^(?:continue if you have next steps.*|continue with the next steps.*|what did we do so far\??|prosegui se hai altri passaggi.*)$/i;
const UNBOUNDED_TASK_REQUEST = /(return|read|include|show|dump)\s+(?:the\s+)?full\s+(?:content|contents|text)|read\s+(?:all|every)\s+(?:files?|source)|explore\s+the\s+codebase\s+thoroughly/i;
const MUTATING_BASH = /(^|[;&|]\s*)(sed\s+-i|perl\s+-pi|rm|mv|cp|install|mkdir|touch|truncate|tee|git\s+(add|commit|merge|rebase|reset|checkout|switch|restore|clean)|npm\s+(install|uninstall|update)|ops\s+ide\s+(deploy|setup|redeploy)|python(?:3)?\s+-c\s+.*(?:write|unlink|remove|rename))\b|(^|[^>])>{1,2}[^&]/i;
const ACTION_TOOL = /^(?:action[-_](?!(?:invoke|list|get|inspect|status)(?:$|[-_]))|openserverless_action_(?!(?:invoke|list|get|inspect|status)(?:$|_)))/;
const FRONTEND_SOURCE_PATH = /(?:^|[\/\s'"`])(?:src|web|public)\/[^\s'"`]+\.(?:[cm]?[jt]sx?|css|scss|sass|less|html?)\b|(?:^|[\/\s'"`])(?:index\.html|vite\.config\.[cm]?[jt]s|tailwind\.config\.[cm]?[jt]s)\b/i;
const OPS_IDE_DEPLOY = /(^|[;&|]\s*)(?:timeout\s+\d+\s+)?ops\s+ide\s+deploy(?:\s|$)/i;
const OPS_IDE_SETUP = /(^|[;&|]\s*)(?:timeout\s+\d+\s+)?ops\s+ide\s+setup(?:\s|$)/i;
const TEST_DISCOVERY_MAX_DEPTH = 8;
const TEST_DISCOVERY_MAX_ENTRIES = 5_000;
const TEST_SUITE_LIMIT = 12;
const TEST_TIMEOUT_MS = 180_000;
const TEST_TOTAL_TIMEOUT_MS = 600_000;
const BROWSER_INTERACTION_BUDGET = 4;
const DIAGNOSTIC_READ_BUDGET = 8;
const RECOVERY_GUIDANCE_BYTES = 6 * 1024;
const MAX_ACTIVE_TASK_CHARS = 4_000;
const MAX_READ_OUTPUT_CHARS = 32_000;
const MAX_TASK_OUTPUT_CHARS = 12_000;
const COMPLETION_CHECK_BUDGET = 3;
const TEST_IGNORED_DIRECTORIES = new Set([
  ".git", ".hg", ".svn", ".cache", ".pytest_cache", ".mypy_cache",
  "__pycache__", "node_modules", "vendor", "coverage", "dist", "build",
  ".next", ".nuxt", ".svelte-kit",
]);
const JS_TEST_FILE = /(?:^|\/)(?:tests?\/.*\.[cm]?[jt]sx?|[^/]+\.(?:test|spec)\.[cm]?[jt]sx?)$/i;
const PYTHON_TEST_FILE = /(?:^|\/)(?:test_[^/]+|[^/]+_test)\.py$/i;
const CRITICAL_TEST_PATH = /(?:^|\/)(?:packages|auth(?:entication|orization)?|login|register|session|security|upload|payment|billing|storage|database|persistence)(?:\/|[_.-]|$)/i;

const CRITICAL_SYSTEM = [
  "Trustable enforcement is active.",
  "After session compaction, Trustable injects a bounded recovery packet containing the exact active user request before tools run. Resume that request; call trustable_context_recover only if the recovery gate explicitly remains active.",
  "For a reported browser bug, reproduce the exact user-visible symptom with browser_interact before changing source. Trustable records successful browser evidence automatically; trustable_diagnostic_checkpoint remains available for explicit evidence or non-browser diagnostics.",
  "Trustable already ran ops ide login and launched the managed dev server with the configured application environment. Never rerun ops ide login during the session.",
  "After any action MCP or packages/** source change, run ops ide deploy before setup or completion. Never create or modify action ZIP files manually.",
  "Never run raw shell ops action or wsk action commands, including invoke and list. Use OpenServerless MCP tools for action mutation, inspection, and invocation; use ops ide deploy and ops ide setup for lifecycle operations.",
  "After changing a setup action, run ops ide setup after deploy and before completion.",
  "Never mask deploy, setup, login, checker, or build failures with || true, || echo, or head/tail pipelines.",
  "Never kill Trustable-managed processes or start vite, npm run dev, or ops ide devel; use the already-running localhost:5173 server.",
  "With React Router HashRouter, pass logical routes such as /login to Link, NavLink, Navigate, and useNavigate. Never pass #/login to router APIs and never use root-relative anchors for internal navigation.",
  "After source changes, call trustable_completion_check before claiming that work is fixed, complete, or ready for the user.",
  "Every action endpoint created or modified in this session requires a focused executable application test under tests/actions/<endpoint> or packages/<endpoint> before completion. The completion gate runs it without installing dependencies; never ask the user to run tests.",
  "Browser work is one bounded QA pass over application controls. Agentic React Select, Multiselect, Done, Adjust selection, and toolkit controls are authoring UI, never application verification. If that overlay appears, close the browser and continue with typecheck, build, and runtime diagnostics instead of retrying it.",
  "Delegate only bounded questions. Never ask a subagent to read or return full files or the whole codebase; request concise findings with paths and line references.",
  "When a browser-reproduced bug is changed, verify the fixed flow with browser_interact before the completion gate. Trustable binds successful post-change browser evidence automatically; audio work requires observable active audio state.",
  "For frontend changes, run the project typecheck before the build. After a successful frontend build or deploy, perform one bounded QA pass on the exact changed route with application controls only. Stop after repeated interactions without an observable application-state change. Do not clear caches or reinstall dependencies unless the observed failure points to dependency state.",
  "Completion checks run once per source revision and at most three times per real request; do not create placeholder changes to rerun them.",
].join(" ");

const CORE_SYSTEM = [
  "Trustable is active. Work directly on the requested application and prioritize executable code over planning or documentation.",
  "Inspect only the files needed to implement the request; use a short plan only when it helps.",
  "Do not restart the managed development server.",
  "For OpenServerless action source changes, deploy and run setup when required near the end of implementation.",
  "For frontend changes, run the project typecheck before the build. After a successful frontend build or deploy, perform one bounded QA pass on the exact changed route and runtime diagnostics. Interact only with application controls; never use Agentic React selection-toolkit controls as verification. If the toolkit interferes, close the browser and continue with typecheck, build, and runtime diagnostics instead of retrying it.",
  "Run trustable_completion_check once after substantial implementation. Do not create placeholder tests or loop on validation; fix concrete failures and otherwise report them clearly.",
].join(" ");

export function isDiagnosticRequest(text) {
  return DIAGNOSTIC_REQUEST.test(text || "");
}

export function isBrowserDiagnosticRequest(text) {
  return isDiagnosticRequest(text) && BROWSER_DIAGNOSTIC_REQUEST.test(text || "");
}

export function isSyntheticContinuation(text) {
  return SYNTHETIC_CONTINUATION.test(String(text || "").trim());
}

export function isUnboundedTaskRequest(args = {}) {
  return UNBOUNDED_TASK_REQUEST.test(`${args.description || ""}\n${args.prompt || ""}`);
}

export function isStatusRequest(text) {
  return /(?:hai\s+(?:gi[aà]\s+)?risolto|cosa\s+hai\s+fatto|fammi\s+un\s+(?:recap|riepilogo)|(?:qual\s+è|dimmi)\s+lo\s+stato|a\s+che\s+punto\s+sei|what\s+did\s+you\s+do|is\s+it\s+fixed|status\s+update|give\s+me\s+(?:a\s+)?(?:recap|summary))/i.test(String(text || ""));
}

function continueInternally(output, text) {
  output.text = text;
  output.synthetic = true;
  output.continue = true;
}

export function isMutatingTool(toolID, args = {}) {
  if (["edit", "write", "patch", "apply_patch"].includes(toolID)) return true;
  if (ACTION_TOOL.test(toolID)) return true;
  if (toolID !== "bash") return false;
  if (isOpsIdeDeployCommand(args) || isOpsIdeSetupCommand(args)) return true;
  return MUTATING_BASH.test(String(args.command || ""));
}

export function isFrontendMutation(toolID, args = {}) {
  if (!isMutatingTool(toolID, args)) return false;
  if (["edit", "write", "patch"].includes(toolID)) {
    return FRONTEND_SOURCE_PATH.test(String(args.filePath || args.path || "").replaceAll("\\", "/"));
  }
  if (toolID === "apply_patch") {
    return FRONTEND_SOURCE_PATH.test(String(args.patch || args.input || "").replaceAll("\\", "/"));
  }
  if (toolID === "bash") {
    return FRONTEND_SOURCE_PATH.test(`${normalizedWorkingDirectory(args)}/${String(args.command || "")}`.replaceAll("\\", "/"));
  }
  return false;
}

export function isOpsIdeDeployCommand(args = {}) {
  return OPS_IDE_DEPLOY.test(String(args.command || ""));
}

export function isOpsIdeSetupCommand(args = {}) {
  return OPS_IDE_SETUP.test(String(args.command || ""));
}

function unwrappedShellSegments(args = {}) {
  return String(args.command || "").split(/&&|\|\||[;|\n]/).map((rawSegment) => {
    let segment = rawSegment.trim();
    let previous = "";
    while (segment && segment !== previous) {
      previous = segment;
      segment = segment
        .replace(/^(?:[A-Za-z_]\w*=(?:'[^']*'|"[^"]*"|\S+)\s+)+/, "")
        .replace(/^timeout\s+(?:(?:--(?:signal|kill-after)(?:=\S+|\s+\S+)|--(?:preserve-status|foreground)|-[ks]\s+\S+)\s+)*\S+\s+/i, "")
        .replace(/^env\s+(?:(?:--?\S+|[A-Za-z_]\w*=\S+)\s+)*/i, "")
        .replace(/^sudo\s+(?:(?:-[A-Za-z]+|--\S+)(?:\s+\S+)?\s+)*/i, "")
        .replace(/^command\s+/i, "")
        .trim();
    }
    return segment;
  });
}

export function isRawActionCommand(args = {}) {
  return unwrappedShellSegments(args).some((segment) => /^(?:ops|wsk)\s+action(?:\s|$)/i.test(segment));
}

export function isManagedIdeLoginCommand(args = {}) {
  return unwrappedShellSegments(args).some((segment) => /^ops\s+ide\s+login(?:\s|$)/i.test(segment));
}

export function isMaskedCriticalCommand(args = {}) {
  const command = String(args.command || "");
  const critical = unwrappedShellSegments(args).some((segment) =>
    /^(?:ops\s+ide\s+(?:login|deploy|setup)(?:\s|$)|npm\s+run\s+build(?:\s|$)|(?:(?:bash|sh)\s+)?(?:\S*\/)?check_(?:openserverless_actions|trustable_app|trustable_frontend)\.sh(?:\s|$))/i.test(segment),
  );
  const masked = /(?:\|\|\s*(?:true|echo\b)|\|\s*(?:head|tail)\b)/i.test(command);
  return critical && masked;
}

export function isManagedDevServerCommand(args = {}) {
  const command = String(args.command || "");
  const startsServer = /(?:^|[;&|]\s*)(?:(?:cd|env)\b[^;&|]*&&\s*)?(?:(?:npx|bunx)\s+)?vite\b|(?:^|[;&|]\s*)npm\s+run\s+dev\b|(?:^|[;&|]\s*)ops\s+ide\s+devel\b/i.test(command);
  const killsProcess = /(?:^|[;&|]\s*)(?:sudo\s+)?(?:kill|pkill|killall)\b/i.test(command);
  return startsServer || killsProcess;
}

function normalizedWorkingDirectory(args = {}) {
  return String(args.cwd || args.workdir || args.directory || "").replaceAll("\\", "/").replace(/\/+$/, "");
}

function isPackagesDirectory(path) {
  return /(^|\/)packages(?:\/|$)/i.test(path || "");
}

function isSetupPackagesDirectory(path) {
  return /(^|\/)packages\/setup(?:\/|$)/i.test(path || "");
}

export function isActionMutation(toolID, args = {}) {
  if (ACTION_TOOL.test(toolID)) return true;
  const path = String(args.filePath || args.path || "").replaceAll("\\", "/");
  if (["edit", "write", "patch"].includes(toolID) && /(^|\/)packages\//.test(path)) return true;
  if (toolID === "apply_patch" && /(?:^|[\s/])packages\//m.test(String(args.patch || args.input || ""))) return true;
  if (toolID === "bash" && isMutatingTool(toolID, args)) {
    const command = String(args.command || "");
    if (/(?:^|[\s'"`])packages\//i.test(command) || isPackagesDirectory(normalizedWorkingDirectory(args))) return true;
  }
  return false;
}

export function isSetupActionMutation(toolID, args = {}) {
  if (!isActionMutation(toolID, args)) return false;
  const path = String(args.filePath || args.path || "").replaceAll("\\", "/");
  if (/(^|\/)packages\/setup\//.test(path)) return true;
  const input = JSON.stringify(args).replaceAll("\\", "/");
  return isSetupPackagesDirectory(normalizedWorkingDirectory(args)) || /(?:packages\/setup\/|["':/]setup\/)/i.test(input);
}

export function isManualActionZipMutation(toolID, args = {}) {
  const path = String(args.filePath || args.path || "").replaceAll("\\", "/");
  if (["edit", "write", "patch"].includes(toolID)) {
    return /(^|\/)packages\/.*\.zip$/i.test(path);
  }
  if (toolID === "apply_patch") {
    return /(?:^|[\s/])packages\/[^\s\n]*\.zip\b/im.test(String(args.patch || args.input || ""));
  }
  if (toolID !== "bash") return false;
  const command = String(args.command || "");
  if (isOpsIdeDeployCommand(args)) return false;
  const touchesPackages = /(?:^|[\s'"`])packages\//i.test(command) || isPackagesDirectory(normalizedWorkingDirectory(args));
  if (!touchesPackages) return false;
  return /zipfile\.ZipFile\s*\([^)]*,\s*["'](?:w|a|x)["']|python(?:3)?\s+-m\s+zipfile\s+-c|(^|[;&|]\s*)zip\s+(?!info)|(^|[;&|]\s*)(?:rm|mv|cp|install|touch|truncate|tee)\b[^;&|\n]*\.zip\b|>{1,2}\s*[^;&|\n]*\.zip\b/i.test(command);
}

function normalizeActionEndpoint(value) {
  const endpoint = String(value || "").replaceAll("\\", "/").replace(/^\/+|\/+$/g, "");
  const parts = endpoint.split("/").filter(Boolean);
  if (parts.length < 2 || parts.slice(0, 2).some((part) => !/^[A-Za-z0-9_.-]+$/.test(part))) return "";
  return parts.slice(0, 2).join("/");
}

function endpointsFromPackagesText(value) {
  const text = String(value || "").replaceAll("\\", "/");
  const endpoints = new Set();
  const pattern = /(?:^|[\s'"`:/])packages\/([A-Za-z0-9_.-]+)\/([A-Za-z0-9_.-]+)\/([^\s'"`]+)/gm;
  for (const match of text.matchAll(pattern)) {
    const file = match[3].replace(/[),;:]+$/, "");
    if (basename(file) === "__main__.py" || file.endsWith(".zip")) continue;
    endpoints.add(`${match[1]}/${match[2]}`);
  }
  return endpoints;
}

function endpointsFromShellCd(value) {
  const endpoints = new Set();
  const pattern = /(?:^|&&|\|\||[;|\n])\s*cd\s+(?:--\s+)?(?:"([^"]+)"|'([^']+)'|([^\s;&|]+))/gim;
  for (const match of String(value || "").replaceAll("\\", "/").matchAll(pattern)) {
    const directory = match[1] || match[2] || match[3] || "";
    for (const endpoint of endpointsFromPackagesText(`${directory}/__trustable_source__`)) endpoints.add(endpoint);
  }
  return endpoints;
}

export function touchedActionEndpoints(toolID, args = {}) {
  const endpoints = new Set();
  if (ACTION_TOOL.test(toolID)) {
    const endpoint = normalizeActionEndpoint(args.endpoint || args.action);
    if (endpoint) endpoints.add(endpoint);
  }

  const path = String(args.filePath || args.path || "");
  for (const endpoint of endpointsFromPackagesText(path)) endpoints.add(endpoint);
  if (toolID === "apply_patch") {
    for (const endpoint of endpointsFromPackagesText(args.patch || args.input)) endpoints.add(endpoint);
  }
  if (toolID === "bash" && isActionMutation(toolID, args)) {
    const command = String(args.command || "");
    for (const endpoint of endpointsFromPackagesText(command)) endpoints.add(endpoint);
    for (const endpoint of endpointsFromShellCd(command)) endpoints.add(endpoint);
    if (!command.includes("__main__.py")) {
      for (const endpoint of endpointsFromPackagesText(`${normalizedWorkingDirectory(args)}/__trustable_source__`)) endpoints.add(endpoint);
    }
  }
  return [...endpoints].sort();
}

function defaultState() {
  return {
    needsRecovery: false,
    diagnosticRequired: false,
    reproduced: false,
    dirty: false,
    verified: true,
    circuitOpen: false,
    actionDeployRequired: false,
    actionSetupRequired: false,
    touchedActionEndpoints: [],
    browserInteractionsSinceEvidence: 0,
    diagnosticReadCount: 0,
    browserDiagnosisObserved: false,
    browserVerificationRequired: false,
    frontendBrowserVerificationRequired: false,
    browserEvidenceAfterMutation: false,
    browserDiagnosticRequired: false,
    browserSuccessfulInteractions: 0,
    lastBrowserInteractionRevision: -1,
    browserEvidenceSequence: 0,
    browserEvidence: [],
    browserApplicationFingerprint: "",
    browserOverlayInterference: false,
    browserVerificationUnavailableReason: "",
    mutationRevision: 0,
    failureSignature: "",
    repeatedFailures: 0,
    evidence: "",
    evidenceID: "",
    activeTask: "",
    activeTaskFingerprint: "",
    automaticRecoveryCount: 0,
    statusRequest: false,
    completionRecoveryAttempts: 0,
    lastCompletionFailure: "",
    lastCompletionRevision: -1,
    completionChecksThisTask: 0,
    completionPrompted: false,
  };
}

function statePath(sessionID) {
  return join(STATE_DIR, `${sessionID.replace(/[^a-zA-Z0-9_.-]/g, "_")}.json`);
}

function legacyStatePath(sessionID) {
  return join(LEGACY_STATE_DIR, `${sessionID.replace(/[^a-zA-Z0-9_.-]/g, "_")}.json`);
}

function parsedState(path) {
  const state = { ...defaultState(), ...JSON.parse(readFileSync(path, "utf8")) };
  state.touchedActionEndpoints = Array.isArray(state.touchedActionEndpoints)
    ? [...new Set(state.touchedActionEndpoints.map(normalizeActionEndpoint).filter(Boolean))].sort()
    : [];
  state.browserEvidence = Array.isArray(state.browserEvidence) ? state.browserEvidence.slice(-20) : [];
  return state;
}

function writeState(path, state) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, `${JSON.stringify(state, null, 2)}\n`, { mode: 0o600 });
}

function loadState(sessionID) {
  const durablePath = statePath(sessionID);
  if (existsSync(durablePath)) {
    try {
      return parsedState(durablePath);
    } catch {
      try {
        const state = parsedState(legacyStatePath(sessionID));
        state.needsRecovery = true;
        state.verified = false;
        return state;
      } catch {
        return { ...defaultState(), needsRecovery: true, verified: false };
      }
    }
  }

  try {
    const state = parsedState(legacyStatePath(sessionID));
    try {
      writeState(durablePath, state);
    } catch {
      // Keep using the readable legacy state when the durable location is unavailable.
    }
    return state;
  } catch {
    return defaultState();
  }
}

function saveState(sessionID, state) {
  try {
    writeState(statePath(sessionID), state);
  } catch (error) {
    try {
      writeState(legacyStatePath(sessionID), state);
    } catch {
      throw error;
    }
  }
}

export function normalizeCompletionFailureOutput(output) {
  return String(output || "")
    .replace(/\u001b\[[0-?]*[ -/]*[@-~]/g, "")
    .replace(/\b\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?\b/g, "<timestamp>")
    .replace(/\b\d{2}:\d{2}:\d{2}(?:\.\d+)?\b/g, "<time>")
    .replace(/\b(?:duration(?:_ms)?|elapsed|runtime|took)\s*[:=]\s*\d+(?:\.\d+)?\s*(?:ms|msec|s|sec|seconds?)?\b/gi, "$1=<duration>")
    .replace(/\b\d+(?:\.\d+)?\s*(?:milliseconds?|msecs?|ms|seconds?|secs?)\b/gi, "<duration>")
    .replace(/\bpid\s*[:=]?\s*\d+\b/gi, "pid=<pid>")
    .replace(/\b[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\b/gi, "<uuid>")
    .replace(/\/tmp\/[A-Za-z0-9_.-]*\d[A-Za-z0-9_.\/-]*/g, "/tmp/<volatile>")
    .replace(/[ \t]+$/gm, "")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}

export function completionFailureSignature(output) {
  return createHash("sha256").update(normalizeCompletionFailureOutput(output)).digest("hex").slice(0, 16);
}

function readGuidance(directory, name, maxBytes = RECOVERY_GUIDANCE_BYTES) {
  const path = join(directory, name);
  if (!existsSync(path)) return `${name}: missing`;
  const text = readFileSync(path, "utf8");
  return `===== ${name} =====\n${text.slice(0, maxBytes)}`;
}

function fingerprint(value) {
  return createHash("sha256").update(String(value || "")).digest("hex").slice(0, 16);
}

function boundedOutput(value, limit, label) {
  const text = String(value || "");
  if (text.length <= limit) return text;
  const head = Math.floor(limit * 0.75);
  const tail = limit - head;
  return `${text.slice(0, head)}\n\n[Trustable bounded ${label}: ${text.length - limit} characters omitted]\n\n${text.slice(-tail)}`;
}

async function recoveryPacket(directory, current) {
  const status = await run("git status --short --branch", directory, 30_000);
  const layout = await run("find . -maxdepth 3 -type f -not -path './.git/*' -not -path './node_modules/*' -not -path './web/assets/*' | sort | head -160", directory, 30_000);
  const activeTask = current.activeTask || "No active user request was captured. Re-read the latest real user message before acting.";
  return [
    "===== TRUSTABLE AUTOMATIC CONTEXT RECOVERY =====",
    "Resume the active task below. Do not summarize the session and do not interpret OpenCode's generic continuation message as a new user request.",
    `ACTIVE USER REQUEST (${current.activeTaskFingerprint || "unknown"}):\n${activeTask}`,
    `GATE STATE: diagnosticRequired=${current.diagnosticRequired}; reproduced=${current.reproduced}; dirty=${current.dirty}; mutationRevision=${current.mutationRevision}; deployRequired=${current.actionDeployRequired}; setupRequired=${current.actionSetupRequired}; browserVerificationRequired=${current.browserVerificationRequired}`,
    CRITICAL_SYSTEM,
    readGuidance(directory, "AGENTS.md"),
    readGuidance(directory, ".openserverless-contract.md"),
    readGuidance(directory, "opencode.md", 4 * 1024),
    sanitizedOpenCodeConfig(directory),
    "===== git status =====",
    status.output || "(clean)",
    "===== bounded project file map =====",
    layout.output || basename(directory),
  ].join("\n\n");
}

function toolOutputText(output) {
  if (typeof output?.output === "string") return output.output;
  if (output?.output === undefined || output?.output === null) return "";
  try {
    return JSON.stringify(output.output);
  } catch {
    return String(output.output);
  }
}

const AGENTIC_REACT_BROWSER_UI = /(?:agentic react toolkit|selection mode (?:active|enabled)|multiselect (?:mode active|enabled)|adjust selection|clear all selections|delete selection|click done to copy|captured and copied)/i;

function isAgenticReactBrowserActivity(input, output) {
  const args = Object.values(input.args || {}).filter((value) => typeof value === "string").join("\n");
  return AGENTIC_REACT_BROWSER_UI.test(args) || AGENTIC_REACT_BROWSER_UI.test(toolOutputText(output));
}

function browserSnapshotValue(output) {
  if (output?.output && typeof output.output === "object") return output.output;
  if (typeof output?.output !== "string") return undefined;
  try {
    return JSON.parse(output.output);
  } catch {
    return undefined;
  }
}

function browserApplicationFingerprint(output) {
  const snapshot = browserSnapshotValue(output);
  if (!snapshot || typeof snapshot !== "object") return "";
  const controls = Array.isArray(snapshot.controls)
    ? snapshot.controls.filter((control) => !AGENTIC_REACT_BROWSER_UI.test(String(control?.name || "")))
    : [];
  return fingerprint(JSON.stringify({
    url: snapshot.url || "",
    title: snapshot.title || "",
    aria: snapshot.aria || "",
    text: snapshot.text || "",
    console: snapshot.console || [],
    network: snapshot.network || [],
    controls,
    audio: snapshot.audio || {},
  }));
}

function updateBrowserApplicationProgress(current, output, interaction) {
  const next = browserApplicationFingerprint(output);
  if (!next) {
    if (interaction) current.browserInteractionsSinceEvidence += 1;
    return false;
  }
  const changed = !current.browserApplicationFingerprint || current.browserApplicationFingerprint !== next;
  current.browserApplicationFingerprint = next;
  if (changed) current.browserInteractionsSinceEvidence = 0;
  else if (interaction) current.browserInteractionsSinceEvidence += 1;
  return changed;
}

function recordBrowserEvidence(current, input, output, application = true) {
  current.browserEvidenceSequence += 1;
  const id = `browser-${current.browserEvidenceSequence}-${fingerprint(`${input.tool}\n${JSON.stringify(input.args || {})}\n${toolOutputText(output)}`)}`;
  const item = {
    id,
    tool: input.tool,
    action: String(input.args?.action || ""),
    revision: current.mutationRevision,
    taskFingerprint: current.activeTaskFingerprint,
    application,
  };
  current.browserEvidence = [...current.browserEvidence, item].slice(-20);
  if (application && input.tool === "browser_browser_interact") current.browserSuccessfulInteractions += 1;
  if (application && input.tool === "browser_browser_interact") current.lastBrowserInteractionRevision = current.mutationRevision;
  if (typeof output.output === "string") {
    const label = application ? "Trustable browser evidence ID" : "Trustable non-application browser observation ID";
    output.output = `${label}: ${id}\n\n${output.output}`;
  }
  return item;
}

function browserInteractionCanCheckpoint(input, output) {
  return ["click", "press", "reload", "back"].includes(String(input.args?.action || ""));
}

function taskRequiresActiveAudio(current) {
  return /(?:music|audio|sound|suono|musica)/i.test(current.activeTask || "");
}

function browserOutputShowsActiveAudio(output) {
  const text = toolOutputText(output);
  return /"active"\s*:\s*true/i.test(text) ||
    /"state"\s*:\s*"running"/i.test(text);
}

function applyAutomaticBrowserCheckpoint(current, input, output, evidence) {
  if (evidence.application === false) return;
  if (!browserInteractionCanCheckpoint(input, output)) return;
  if (current.diagnosticRequired && !current.reproduced) {
    current.evidence = `Automatic browser reproduction evidence from ${input.args?.action || "interaction"}.`;
    current.evidenceID = evidence.id;
    current.reproduced = true;
    current.diagnosticRequired = false;
    current.circuitOpen = false;
    current.repeatedFailures = 0;
    current.failureSignature = "";
    current.diagnosticReadCount = 0;
    return;
  }
  if ((!current.browserVerificationRequired && !current.frontendBrowserVerificationRequired) || !current.dirty) return;
  if (taskRequiresActiveAudio(current) && !browserOutputShowsActiveAudio(output)) return;
  current.evidence = `Automatic post-change browser verification from ${input.args?.action || "interaction"}.`;
  current.evidenceID = evidence.id;
  current.browserVerificationRequired = false;
  current.frontendBrowserVerificationRequired = false;
  current.browserEvidenceAfterMutation = false;
  current.diagnosticReadCount = 0;
  current.lastCompletionRevision = -1;
}

function browserEvidenceFor(current, evidenceID) {
  return current.browserEvidence.find((item) => item.id === evidenceID);
}

function latestBrowserEvidence(current, predicate) {
  return [...current.browserEvidence].reverse().find(predicate);
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
  const maxOutput = 1024 * 1024;
  let stdout = "";
  let stderr = "";
  const append = (current, chunk) => `${current}${chunk}`.slice(-maxOutput);

  return new Promise((resolve) => {
    let settled = false;
    let timer;
    const finish = (exitCode, error = "") => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      const output = `${stdout}${stderr ? `\nSTDERR:\n${stderr}` : ""}${error ? `\n${error}` : ""}`.trim();
      resolve({ exitCode, output });
    };
    const { NODE_TEST_CONTEXT: _nodeTestContext, ...childEnv } = process.env;
    const proc = spawn("/bin/bash", ["-lc", command], {
      cwd,
      env: childEnv,
      signal: controller.signal,
      stdio: ["ignore", "pipe", "pipe"],
    });
    proc.stdout.on("data", (chunk) => { stdout = append(stdout, chunk); });
    proc.stderr.on("data", (chunk) => { stderr = append(stderr, chunk); });
    proc.on("error", (error) => finish(124, `Command failed or timed out: ${error.message}`));
    proc.on("close", (code) => finish(Number.isInteger(code) ? code : 124));
    timer = setTimeout(() => controller.abort(), timeoutMs);
  });
}

function discoverFiles(directory) {
  const files = [];
  let entries = 0;
  let exceeded = false;

  const visit = (current, depth) => {
    if (exceeded || depth > TEST_DISCOVERY_MAX_DEPTH) return;
    let children;
    try {
      children = readdirSync(current, { withFileTypes: true });
    } catch {
      return;
    }
    children.sort((left, right) => left.name.localeCompare(right.name));
    for (const child of children) {
      entries += 1;
      if (entries > TEST_DISCOVERY_MAX_ENTRIES) {
        exceeded = true;
        return;
      }
      const path = join(current, child.name);
      if (child.isSymbolicLink()) continue;
      if (child.isDirectory()) {
        if (!TEST_IGNORED_DIRECTORIES.has(child.name)) visit(path, depth + 1);
      } else if (child.isFile()) {
        files.push(path);
      }
    }
  };

  visit(directory, 0);
  return { files, exceeded };
}

function nearestProjectFile(start, directory, candidates) {
  let current = dirname(start);
  while (current === directory || current.startsWith(`${directory}/`)) {
    for (const candidate of candidates) {
      const path = join(current, candidate);
      if (existsSync(path)) return path;
    }
    if (current === directory) break;
    current = dirname(current);
  }
  return "";
}

function readsAsPytest(path) {
  try {
    return /(?:^|\n)\s*(?:from\s+pytest\s+import|import\s+pytest\b)/m.test(readFileSync(path, "utf8").slice(0, 64 * 1024));
  } catch {
    return false;
  }
}

function projectDeclaresPytest(directory) {
  for (const name of ["pyproject.toml", "setup.cfg", "requirements.txt", "requirements-dev.txt", "pytest.ini"]) {
    const path = join(directory, name);
    if (!existsSync(path)) continue;
    try {
      if (name === "pytest.ini" || /(?:^|[^a-z])pytest(?:[^a-z]|$)/i.test(readFileSync(path, "utf8").slice(0, 128 * 1024))) return true;
    } catch {
      // An unreadable declaration will be reported by its runner if selected elsewhere.
    }
  }
  return false;
}

function unsafeTestScript(script) {
  return /(?:^|[;&|]\s*)(?:npm\s+(?:i|install|ci|update|uninstall)|yarn\s+(?:add|install)|pnpm\s+(?:add|install|update|dlx)|bun\s+(?:add|install)|pip(?:3)?\s+install|uv\s+sync|poetry\s+install|npx\b|bunx\b)/i.test(script);
}

export function discoverApplicationTestSuites(directory) {
  directory = normalizePluginDirectory(directory);
  const root = directory.replace(/\/+$/, "");
  const { files, exceeded } = discoverFiles(root);
  if (exceeded) {
    return {
      suites: [],
      skipped: [],
      error: `Application test discovery exceeded ${TEST_DISCOVERY_MAX_ENTRIES} filesystem entries. Narrow generated content or dependency trees before completion.`,
    };
  }

  const suites = [];
  const skipped = [];
  const fileSet = new Set(files);
  const goModules = new Set();
  const jsPackages = new Map();
  const pythonTests = [];

  for (const path of files) {
    const rel = relative(root, path).replaceAll("\\", "/");
    if (rel.endsWith("_test.go")) {
      const moduleFile = nearestProjectFile(path, root, ["go.mod"]);
      if (moduleFile) goModules.add(dirname(moduleFile));
    }
    if (PYTHON_TEST_FILE.test(rel)) pythonTests.push(path);
    if (JS_TEST_FILE.test(rel)) {
      const packageFile = nearestProjectFile(path, root, ["package.json"]);
      if (packageFile) {
        const packageRoot = dirname(packageFile);
        if (!jsPackages.has(packageRoot)) jsPackages.set(packageRoot, []);
        jsPackages.get(packageRoot).push(path);
      }
    }
  }

  for (const moduleRoot of [...goModules].sort()) {
    const testFiles = [...fileSet].filter((path) => path.startsWith(`${moduleRoot}/`) && path.endsWith("_test.go"));
    suites.push({
      name: `Go tests (${relative(root, moduleRoot) || "."})`,
      command: "GOTOOLCHAIN=local GOPROXY=off go test ./...",
      cwd: moduleRoot,
      testFiles,
      critical: testFiles.some((path) => CRITICAL_TEST_PATH.test(relative(root, path).replaceAll("\\", "/"))),
    });
  }

  if (pythonTests.length > 0) {
    const usesPytest = projectDeclaresPytest(root) || pythonTests.some(readsAsPytest);
    if (usesPytest) {
      suites.push({
        name: "Python tests (pytest)",
        command: "python3 -m pytest --disable-warnings --maxfail=1",
        cwd: root,
        testFiles: pythonTests,
        critical: pythonTests.some((path) => CRITICAL_TEST_PATH.test(relative(root, path).replaceAll("\\", "/"))),
      });
    } else {
      const testDirectories = new Map();
      for (const path of pythonTests) {
        const testDirectory = dirname(path);
        const current = testDirectories.get(testDirectory) || { prefix: false, suffix: false, critical: false };
        const name = basename(path);
        if (name.startsWith("test_")) current.prefix = true;
        else current.suffix = true;
        current.critical ||= CRITICAL_TEST_PATH.test(relative(root, path).replaceAll("\\", "/"));
        testDirectories.set(testDirectory, current);
      }
      for (const [testDirectory, patterns] of [...testDirectories.entries()].sort(([left], [right]) => left.localeCompare(right))) {
        for (const [present, pattern] of [[patterns.prefix, "test_*.py"], [patterns.suffix, "*_test.py"]]) {
          if (!present) continue;
          suites.push({
            name: `Python tests (unittest; ${relative(root, testDirectory) || "."}; ${pattern})`,
            command: `python3 -m unittest discover -s . -p '${pattern}'`,
            cwd: testDirectory,
            testFiles: pythonTests.filter((path) => dirname(path) === testDirectory && (pattern.startsWith("test_") ? basename(path).startsWith("test_") : !basename(path).startsWith("test_"))),
            critical: patterns.critical,
          });
        }
      }
    }
  }

  for (const [packageRoot, testFiles] of [...jsPackages.entries()].sort(([left], [right]) => left.localeCompare(right))) {
    let pkg;
    try {
      pkg = JSON.parse(readFileSync(join(packageRoot, "package.json"), "utf8"));
    } catch (error) {
      suites.push({
        name: `JavaScript tests (${relative(root, packageRoot) || "."})`,
        invalid: `invalid package.json: ${error.message}`,
        testFiles,
      });
      continue;
    }
    const scriptName = pkg.scripts?.["test:ci"] ? "test:ci" : pkg.scripts?.test ? "test" : "";
    if (!scriptName || /no test specified/i.test(pkg.scripts[scriptName])) {
      skipped.push(`JavaScript tests (${relative(root, packageRoot) || "."}): test files exist but no executable test/test:ci script is declared`);
      continue;
    }
    if (unsafeTestScript(pkg.scripts[scriptName])) {
      suites.push({
        name: `JavaScript tests (${relative(root, packageRoot) || "."})`,
        invalid: `the ${scriptName} script may install or download dependencies; use an already-installed project test runner`,
        testFiles,
      });
      continue;
    }
    suites.push({
      name: `JavaScript tests (${relative(root, packageRoot) || "."})`,
      command: `CI=1 npm_config_offline=true npm_config_update_notifier=false npm run ${scriptName}`,
      cwd: packageRoot,
      testFiles,
      critical: testFiles.some((path) => CRITICAL_TEST_PATH.test(relative(root, path).replaceAll("\\", "/"))),
    });
  }

  if (suites.length > TEST_SUITE_LIMIT) {
    return {
      suites: [],
      skipped,
      error: `Application test discovery found ${suites.length} executable suites; the bounded limit is ${TEST_SUITE_LIMIT}. Consolidate duplicate suite declarations before completion.`,
    };
  }
  return { suites, skipped, error: "" };
}

function endpointHasFocusedTest(directory, endpoint, suites) {
  const prefixes = [`packages/${endpoint}/`, `tests/actions/${endpoint}/`];
  return suites.some((suite) => (suite.testFiles || []).some((path) => {
    const rel = relative(directory, path).replaceAll("\\", "/");
    return basename(rel) !== "__main__.py" && prefixes.some((prefix) => rel.startsWith(prefix));
  }));
}

export async function runApplicationTests(directory, requiredActionEndpoints = []) {
  const discovered = discoverApplicationTestSuites(directory);
  if (discovered.error) return { passed: false, exitCode: 1, output: discovered.error };

  const reports = [];
  const required = [...new Set(requiredActionEndpoints.map(normalizeActionEndpoint).filter(Boolean))].sort();
  const missing = required.filter((endpoint) => !endpointHasFocusedTest(directory, endpoint, discovered.suites));
  let passed = missing.length === 0;
  if (missing.length > 0) {
    const expected = missing.flatMap((endpoint) => {
      const flattened = endpoint.replaceAll("/", "_");
      return [
        `Expected directory for ${endpoint}: tests/actions/${endpoint}/ or packages/${endpoint}/`,
        ...(flattened === endpoint
          ? []
          : [`Do not replace endpoint separators with underscores (wrong: tests/actions/${flattened}/).`]),
      ];
    });
    reports.push([
      "===== focused action tests: FAIL =====",
      `Missing executable focused application test for: ${missing.join(", ")}`,
      ...expected,
      "Move or add a recognized executable test in the exact endpoint directory; do not modify generated __main__.py.",
    ].join("\n"));
  } else if (required.length > 0) {
    reports.push(`===== focused action tests: PASS =====\nCovered endpoints: ${required.join(", ")}`);
  }
  const deadline = Date.now() + TEST_TOTAL_TIMEOUT_MS;
  for (const suite of discovered.suites) {
    const remaining = deadline - Date.now();
    const result = suite.invalid
      ? { exitCode: 1, output: suite.invalid }
      : remaining <= 0
        ? { exitCode: 124, output: `Application test budget of ${TEST_TOTAL_TIMEOUT_MS / 1000} seconds exhausted before this suite.` }
        : await run(suite.command, suite.cwd, Math.min(TEST_TIMEOUT_MS, remaining));
    if (result.exitCode !== 0) passed = false;
    reports.push([
      `===== ${suite.name}${suite.critical ? " [critical]" : ""}: ${result.exitCode === 0 ? "PASS" : "FAIL"} =====`,
      result.output || "(no output)",
    ].join("\n"));
  }
  for (const message of discovered.skipped) reports.push(`===== ${message}: SKIP =====`);
  if (reports.length === 0) reports.push("===== application tests: SKIP =====\nNo executable application test suites discovered; no framework is imposed.");
  return { passed, exitCode: passed ? 0 : 1, output: reports.join("\n\n") };
}

async function completionChecks(directory, requiredActionEndpoints = []) {
  const checks = [];
  checks.push(["git diff", await run("git diff --check", directory, 60_000)]);
  checks.push(["Trustable contracts", await run("timeout 120 check_trustable_app.sh .", directory, 140_000)]);

  const packagePath = join(directory, "package.json");
  if (existsSync(packagePath)) {
    try {
      const pkg = JSON.parse(readFileSync(packagePath, "utf8"));
      if (pkg.scripts?.typecheck) {
        checks.push(["frontend typecheck", await run("timeout 180 npm run typecheck", directory, 200_000)]);
      } else if (existsSync(join(directory, "tsconfig.json")) && existsSync(join(directory, "node_modules", ".bin", "tsc"))) {
        checks.push(["frontend typecheck", await run("timeout 180 ./node_modules/.bin/tsc -b --noEmit --pretty false --incremental false", directory, 200_000)]);
      }
      if (pkg.scripts?.build) {
        checks.push(["frontend build", await run("timeout 180 npm run build", directory, 200_000)]);
      }
    } catch (error) {
      checks.push(["package.json", { exitCode: 1, output: error.message }]);
    }
  }

  checks.push(["application tests", await runApplicationTests(directory, requiredActionEndpoints)]);

  const failed = checks.filter(([, result]) => result.exitCode !== 0);
  const output = checks.map(([name, result]) => [
    `===== ${name}: ${result.exitCode === 0 ? "PASS" : "FAIL"} =====`,
    result.output || "(no output)",
  ].join("\n")).join("\n\n");
  return { passed: failed.length === 0, output };
}

function pluginDirectoryCandidate(value) {
  if (typeof value === "string" && value.trim()) return value;
  if (value instanceof URL && value.protocol === "file:") return value.pathname;
  if (value && typeof value === "object") {
    for (const key of ["directory", "worktree", "path", "pathname", "cwd"]) {
      const normalized = pluginDirectoryCandidate(value[key]);
      if (normalized) return normalized;
    }
  }
  return "";
}

export function normalizePluginDirectory(value) {
  return pluginDirectoryCandidate(value) || process.cwd();
}

export default async function TrustableGuardrails(context = {}) {
  const directory = normalizePluginDirectory(context.directory ?? context.worktree ?? context.project);
  const coreManaged = Boolean(process.env.TRUSTABLE_RUNTIME_CONFIG);
  const states = new Map();
  const stateFor = (sessionID) => {
    if (!states.has(sessionID)) states.set(sessionID, loadState(sessionID));
    return states.get(sessionID);
  };

  return {
    tool: {
      trustable_context_recover: tool({
        description: "Fallback recovery for mandatory Trustable app context. Normal compaction recovery is injected automatically; call this only when the recovery gate explicitly remains active.",
        args: {},
        async execute(_args, context) {
          const current = stateFor(context.sessionID);
          if (!current.needsRecovery) {
            return "Trustable automatic context recovery is already complete. Resume the active user request without reinjecting guidance.";
          }
          const packet = await recoveryPacket(context.directory, current);
          current.needsRecovery = false;
          current.automaticRecoveryCount += 1;
          saveState(context.sessionID, current);
          return packet;
        },
      }),

      trustable_diagnostic_checkpoint: tool({
        description: "Record concrete diagnostic evidence. Use phase=reproduced before a fix. After changing a browser-reproduced bug, exercise the fixed flow in the browser and use phase=verified before completion.",
        args: {
          phase: tool.schema.enum(["reproduced", "verified", "blocked"]),
          evidence: tool.schema.string().min(12).describe("Concise observed evidence: command/tool, URL or test, and actual result."),
          evidence_id: tool.schema.string().optional().describe("Optional Trustable browser evidence ID. When omitted, Trustable binds the latest valid browser evidence for this task and mutation revision."),
        },
        async execute(args, context) {
          const current = stateFor(context.sessionID);
          if (current.circuitOpen && args.phase === "verified") {
            throw new Error("Trustable diagnostic circuit breaker: phase=verified cannot clear a repeated completion failure. Reproduce the exact remaining failure and call this tool with phase=reproduced, or use phase=blocked when reproduction is impossible.");
          }
          if (coreManaged && args.phase === "reproduced") {
            current.diagnosticRequired = false;
            current.reproduced = true;
            current.circuitOpen = false;
            current.repeatedFailures = 0;
            current.failureSignature = "";
            current.completionRecoveryAttempts = 0;
            saveState(context.sessionID, current);
            return "Diagnostic checkpoint accepted by Trustable core.";
          }
          let browserEvidence = args.evidence_id ? browserEvidenceFor(current, args.evidence_id) : undefined;
          if (!args.evidence_id && args.phase === "reproduced") {
            browserEvidence = latestBrowserEvidence(current, (item) => (
              item.tool === "browser_browser_interact" &&
              item.application !== false &&
              item.taskFingerprint === current.activeTaskFingerprint
            ));
          }
          if (!args.evidence_id && args.phase === "verified") {
            browserEvidence = latestBrowserEvidence(current, (item) => (
              item.application !== false &&
              item.revision === current.mutationRevision &&
              item.taskFingerprint === current.activeTaskFingerprint
            ));
          }
          if (args.phase === "verified") {
            if (!current.browserVerificationRequired) {
              return "No browser verification checkpoint is currently required.";
            }
            if (!browserEvidence || browserEvidence.application === false || browserEvidence.revision !== current.mutationRevision || browserEvidence.taskFingerprint !== current.activeTaskFingerprint || current.lastBrowserInteractionRevision !== current.mutationRevision) {
              throw new Error("Trustable browser verification gate: pass the evidence_id from a successful browser interaction or snapshot produced after the latest source change in this session.");
            }
            current.evidence = args.evidence;
            current.evidenceID = browserEvidence.id;
            current.browserVerificationRequired = false;
            current.browserEvidenceAfterMutation = false;
            saveState(context.sessionID, current);
            return "Browser verification checkpoint recorded. The deterministic completion gate may now run.";
          }
          if (args.phase === "reproduced" && current.browserDiagnosticRequired) {
            if (!browserEvidence || browserEvidence.application === false || browserEvidence.taskFingerprint !== current.activeTaskFingerprint || current.browserSuccessfulInteractions < 1) {
              throw new Error("Trustable diagnostic gate: reproduce the user-visible symptom with browser_interact, then call trustable_diagnostic_checkpoint again. Omit evidence_id to bind the latest valid browser interaction automatically. Source inspection or browser_open alone is not reproduction evidence.");
            }
          }
          current.evidence = args.evidence;
          current.evidenceID = browserEvidence?.id || args.evidence_id || "";
          current.reproduced = args.phase === "reproduced";
          current.diagnosticRequired = args.phase !== "reproduced";
          if (current.reproduced) {
            current.circuitOpen = false;
            current.repeatedFailures = 0;
            current.failureSignature = "";
            current.completionRecoveryAttempts = 0;
          }
          saveState(context.sessionID, current);
          return args.phase === "reproduced"
            ? "Diagnostic checkpoint recorded. Source mutations are unlocked for one evidence-based fix attempt."
            : "Diagnostic blocker recorded. Do not modify source until the symptom can be reproduced.";
        },
      }),

      trustable_completion_check: tool({
        description: "Run the deterministic Trustable completion gate after source changes: git diff validation, contract checkers, frontend typecheck and build, browser evidence when frontend changed, and bounded existing Go/Python/JavaScript application tests.",
        args: {},
        async execute(_args, context) {
          const current = stateFor(context.sessionID);
          if (current.lastCompletionRevision === current.mutationRevision) {
            saveState(context.sessionID, current);
            return "Trustable completion check already ran for the current source revision. Do not call it again without a real implementation change; continue coding or report the previous concrete result.";
          }
          if (current.completionChecksThisTask >= COMPLETION_CHECK_BUDGET) {
            saveState(context.sessionID, current);
            return `Trustable completion budget reached (${COMPLETION_CHECK_BUDGET} checks for this request). Stop calling this tool. Do not create placeholder tests; finish the requested code and report any remaining validation failure.`;
          }
          current.lastCompletionRevision = current.mutationRevision;
          current.completionChecksThisTask += 1;
          saveState(context.sessionID, current);
          const browserUnavailable = Boolean(current.browserVerificationUnavailableReason);
          let result = current.browserVerificationRequired && !browserUnavailable
            ? {
                passed: false,
                output: "===== browser verification: FAIL =====\nA browser-visible flow changed after the last evidence. Exercise the exact route and visible user flow with browser_interact; Trustable binds successful post-change evidence automatically. Audio fixes must report active audio state.",
              }
            : current.actionDeployRequired
            ? {
                passed: false,
                output: "===== action deploy: FAIL =====\nAction source changed after the last verified deployment. Run timeout 120 ops ide deploy; do not create ZIP files manually.",
              }
            : current.actionSetupRequired
              ? {
                  passed: false,
                  output: "===== action setup: FAIL =====\nA setup action changed and was deployed but setup has not run yet. Run timeout 120 ops ide setup before completion.",
                }
            : await completionChecks(context.directory, current.touchedActionEndpoints);
          if (result.passed && current.frontendBrowserVerificationRequired && !browserUnavailable) {
            result = {
              passed: false,
              output: "===== browser verification: FAIL =====\nFrontend source changed after the last browser evidence. Open the exact route, inspect the rendered page and diagnostics, then exercise the visible flow with browser_interact.",
            };
          }
          if (result.passed && browserUnavailable) {
            result.output = `===== browser verification: SKIP =====\n${current.browserVerificationUnavailableReason}\n\n${result.output}`;
          }
          if (result.passed) {
            current.dirty = false;
            current.verified = true;
            current.diagnosticRequired = false;
            current.reproduced = false;
            current.circuitOpen = false;
            current.actionDeployRequired = false;
            current.actionSetupRequired = false;
            current.browserVerificationRequired = false;
            current.frontendBrowserVerificationRequired = false;
            current.browserEvidenceAfterMutation = false;
            current.browserDiagnosisObserved = false;
            current.browserOverlayInterference = false;
            current.browserVerificationUnavailableReason = "";
            current.diagnosticReadCount = 0;
            current.touchedActionEndpoints = [];
            current.failureSignature = "";
            current.repeatedFailures = 0;
            current.completionRecoveryAttempts = 0;
            current.lastCompletionFailure = "";
            saveState(context.sessionID, current);
            return `Trustable completion gate passed.\n\n${result.output}`;
          }

          const signature = completionFailureSignature(result.output);
          current.repeatedFailures = current.failureSignature === signature ? current.repeatedFailures + 1 : 1;
          current.failureSignature = signature;
          current.lastCompletionFailure = result.output.match(/Missing executable focused application test for:[^\n]*/)?.[0]
            || "one or more automatic checks are still failing";
          current.verified = false;
          current.dirty = true;
          current.circuitOpen = false;
          current.completionRecoveryAttempts = 0;
          saveState(context.sessionID, current);
          return `Trustable completion gate failed.\n\n${result.output}\n\nFix only concrete failures related to the requested implementation. Do not create placeholder tests and do not rerun this check until source code has genuinely changed.`;
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
      const current = stateFor(input.sessionID);
      current.browserInteractionsSinceEvidence = 0;
      current.browserApplicationFingerprint = "";
      current.browserOverlayInterference = false;
      current.browserVerificationUnavailableReason = "";
      const text = (output.parts || []).filter((part) => part.type === "text").map((part) => part.text || "").join("\n");
      const synthetic = (output.parts || []).some((part) => part.synthetic === true) || isSyntheticContinuation(text);
      current.statusRequest = isStatusRequest(text);
      const realTask = text.trim() && !current.statusRequest && !synthetic;
      if (realTask) {
        current.activeTask = text.trim().slice(0, MAX_ACTIVE_TASK_CHARS);
        current.activeTaskFingerprint = fingerprint(current.activeTask);
        current.completionRecoveryAttempts = 0;
        current.lastCompletionFailure = "";
        current.lastCompletionRevision = -1;
        current.completionChecksThisTask = 0;
        current.completionPrompted = false;
      }
      if (coreManaged && realTask) {
        current.diagnosticRequired = false;
        current.reproduced = false;
        current.circuitOpen = false;
        current.browserDiagnosticRequired = false;
        current.diagnosticReadCount = 0;
      } else if (isDiagnosticRequest(text)) {
        current.diagnosticRequired = true;
        current.reproduced = false;
        current.verified = false;
        current.evidence = "";
        current.evidenceID = "";
        current.browserDiagnosticRequired = isBrowserDiagnosticRequest(text);
        current.browserSuccessfulInteractions = 0;
        current.browserEvidence = [];
        current.browserDiagnosisObserved = false;
        current.browserVerificationRequired = false;
        current.browserEvidenceAfterMutation = false;
        current.diagnosticReadCount = 0;
      }
      saveState(input.sessionID, current);
    },

    "experimental.chat.system.transform": async (input, output) => {
      const current = input.sessionID ? stateFor(input.sessionID) : defaultState();
      if (input.sessionID && current.needsRecovery) {
        const packet = await recoveryPacket(directory, current);
        current.needsRecovery = false;
        current.automaticRecoveryCount += 1;
        saveState(input.sessionID, current);
        output.system.push(packet);
      }
      let status = coreManaged ? CORE_SYSTEM : CRITICAL_SYSTEM;
      if (!coreManaged && current.diagnosticRequired && !current.reproduced) status += " DIAGNOSTIC REPRODUCTION IS REQUIRED BEFORE SOURCE CHANGES.";
      if (current.dirty && !current.verified) status += " THE CURRENT CHANGES HAVE NOT PASSED THE COMPLETION GATE.";
      if (current.actionDeployRequired) status += " ACTION DEPLOY IS REQUIRED BEFORE SETUP OR COMPLETION.";
      if (current.actionSetupRequired) status += " ACTION SETUP IS REQUIRED AFTER DEPLOY AND BEFORE COMPLETION.";
      if (current.touchedActionEndpoints?.length) status += ` FOCUSED TESTS ARE REQUIRED FOR ACTION ENDPOINTS: ${current.touchedActionEndpoints.join(", ")}.`;
      if (current.browserVerificationUnavailableReason) status += ` BROWSER QA IS UNAVAILABLE FOR THIS TURN: ${current.browserVerificationUnavailableReason} CLOSE IT AND USE THE COMPLETION GATE; DO NOT RETRY AGENTIC REACT.`;
      else if (current.browserVerificationRequired || current.frontendBrowserVerificationRequired) status += " FRONTEND BROWSER VERIFICATION IS REQUIRED BEFORE COMPLETION; OPEN THE EXACT ROUTE, INSPECT THE PAGE AND DIAGNOSTICS, THEN USE AT MOST ONE BOUNDED APPLICATION FLOW. NEVER USE AGENTIC REACT TOOLKIT CONTROLS AS EVIDENCE.";
      output.system.push(status);
    },

    "experimental.session.compacting": async (input, output) => {
      const current = input.sessionID ? stateFor(input.sessionID) : defaultState();
      output.context.push(coreManaged ? CORE_SYSTEM : CRITICAL_SYSTEM);
      output.context.push(`ACTIVE USER REQUEST TO RESUME AFTER COMPACTION (${current.activeTaskFingerprint || "unknown"}):\n${current.activeTask || "Re-read the latest real user request."}`);
      output.context.push("Trustable injects a bounded recovery packet automatically on the continued turn. Resume the active request; do not summarize and do not treat OpenCode's generic continuation text as a new user request.");
    },

    "tool.execute.before": async (input, output) => {
      const current = stateFor(input.sessionID);
      if (input.tool === "browser_browser_interact" && current.browserOverlayInterference) {
        throw new Error("Trustable browser QA stopped: Agentic React authoring controls are not application UI. Close the browser and continue with typecheck, build, runtime diagnostics, and the completion check; do not retry Select, Done, or Adjust selection.");
      }
      if (input.tool === "browser_browser_interact" && isAgenticReactBrowserActivity({ ...input, args: output.args }, {})) {
        throw new Error("Trustable browser QA blocked an Agentic React toolkit control. Interact only with the application under test; never use Select, Multiselect, Done, or Adjust selection as verification.");
      }
      if (input.tool === "browser_browser_interact" && current.browserInteractionsSinceEvidence >= BROWSER_INTERACTION_BUDGET) {
        throw new Error(`Trustable browser diagnostic circuit breaker: ${BROWSER_INTERACTION_BUDGET} consecutive interactions did not change observable application state. Take one fresh browser_browser_snapshot or browser_browser_diagnostics. If the application state is unchanged, close the browser and continue from typecheck, build, and runtime diagnostics instead of clicking again.`);
      }
      if (input.tool === "task") {
        if (isUnboundedTaskRequest(output.args)) {
          throw new Error("Trustable context budget: subagents may not read or return full files or the whole codebase. Delegate one bounded question, at most eight relevant files, and request concise findings with paths and line references.");
        }
        output.args.prompt = `${output.args.prompt || ""}\n\nTrustable bounds: inspect at most eight relevant files; do not return full file contents; report concise findings with paths and line references; do not infer that packages/ is absent from one empty listing.`;
      }
      if (input.tool === "bash" && isRawActionCommand(output.args)) {
        throw new Error("Trustable action guard: raw ops action/wsk action shell commands are forbidden, including invoke and list. Use OpenServerless MCP tools for action mutation, inspection, or invocation; use ops ide deploy and ops ide setup for lifecycle operations.");
      }
      if (input.tool === "bash" && isManagedIdeLoginCommand(output.args)) {
        throw new Error("Trustable process guard: Trustable already authenticated and configured this app with ops ide login and launched its managed dev server. Do not rerun ops ide login or replace/restart the managed server; continue with the launch environment already provided by Trustable.");
      }
      if (input.tool === "bash" && isManagedDevServerCommand(output.args)) {
        throw new Error("Trustable process guard: do not kill managed processes or start Vite/ops ide devel manually. Use the existing http://localhost:5173 server and diagnose its configured proxy without replacing it.");
      }
      if (input.tool === "bash" && isMaskedCriticalCommand(output.args)) {
        throw new Error("Trustable validation guard: do not mask critical command failures with || true, || echo, or head/tail pipelines. Run the bounded command directly and inspect its complete actionable error.");
      }
      if (isManualActionZipMutation(input.tool, output.args)) {
        throw new Error("Trustable action guard: manual ZIP creation or mutation under packages/ is forbidden. Edit action source and run ops ide deploy so ops generates and deploys the archive.");
      }
      if (["trustable_context_recover", "trustable_diagnostic_checkpoint", "trustable_completion_check"].includes(input.tool)) return;
      if (
        !coreManaged &&
        current.browserDiagnosticRequired &&
        current.diagnosticRequired &&
        !current.reproduced &&
        current.diagnosticReadCount >= DIAGNOSTIC_READ_BUDGET &&
        ![
          "browser_browser_open",
          "browser_browser_interact",
          "browser_browser_snapshot",
          "browser_browser_diagnostics",
          "trustable_context_recover",
          "trustable_diagnostic_checkpoint",
        ].includes(input.tool)
      ) {
        throw new Error(`Trustable browser diagnostic budget exhausted after ${DIAGNOSTIC_READ_BUDGET} read-only inspections. Browser-only phase is active: stop using shell, file, task, and editor tools; reproduce the user-visible symptom now with browser_interact. Successful evidence is recorded automatically.`);
      }
      if (current.actionDeployRequired && input.tool === "bash" && isOpsIdeSetupCommand(output.args)) {
        throw new Error("Trustable action guard: action source changed after the last deploy. Run timeout 120 ops ide deploy before ops ide setup.");
      }
      if (!isMutatingTool(input.tool, output.args)) return;
      if (current.needsRecovery) {
        throw new Error("Trustable guardrail: session context was compacted. Call trustable_context_recover before modifying files, actions, or deployment state.");
      }
      if (!coreManaged && ((current.diagnosticRequired && !current.reproduced) || current.circuitOpen)) {
        throw new Error("Trustable diagnostic circuit breaker: reproduce the exact symptom with a successful browser_interact before modifying source. Trustable records that browser evidence automatically; use trustable_diagnostic_checkpoint only for explicit or non-browser evidence.");
      }
    },

    "tool.execute.after": async (input, output) => {
      const current = stateFor(input.sessionID);
      if (input.tool === "browser_browser_close") {
        current.browserOverlayInterference = false;
        saveState(input.sessionID, current);
        return;
      }
      if (["browser_browser_open", "browser_browser_snapshot", "browser_browser_diagnostics"].includes(input.tool)) {
        const agenticReact = isAgenticReactBrowserActivity(input, output);
        const blockedByExistingOverlay = input.tool === "browser_browser_diagnostics" && current.browserOverlayInterference;
        if (agenticReact) {
          current.browserOverlayInterference = true;
          current.browserVerificationUnavailableReason = "Agentic React authoring UI interfered with the isolated Browser MCP; deterministic typecheck, build, and runtime diagnostics were used instead.";
          if (typeof output.output === "string") output.output = `Trustable ignored Agentic React authoring controls. Close this browser; do not interact with Select, Done, or Adjust selection.\n\n${output.output}`;
        } else if (input.tool !== "browser_browser_diagnostics") {
          current.browserOverlayInterference = false;
          current.browserVerificationUnavailableReason = "";
          updateBrowserApplicationProgress(current, output, false);
        }
        recordBrowserEvidence(current, input, output, !agenticReact && !blockedByExistingOverlay);
        if (current.diagnosticRequired || current.reproduced) current.browserDiagnosisObserved = true;
        if (!agenticReact && !blockedByExistingOverlay && (current.browserVerificationRequired || current.frontendBrowserVerificationRequired) && current.dirty) current.browserEvidenceAfterMutation = true;
        saveState(input.sessionID, current);
        return;
      }
      if (input.tool === "browser_browser_interact") {
        const agenticReact = isAgenticReactBrowserActivity(input, output);
        updateBrowserApplicationProgress(current, output, true);
        const evidence = recordBrowserEvidence(current, input, output, !agenticReact);
        if (agenticReact) {
          current.browserOverlayInterference = true;
          current.browserVerificationUnavailableReason = "Agentic React authoring UI interfered with the isolated Browser MCP; deterministic typecheck, build, and runtime diagnostics were used instead.";
          if (typeof output.output === "string") output.output = `Trustable ignored this Agentic React authoring interaction. Close the browser and do not retry the toolkit.\n\n${output.output}`;
          saveState(input.sessionID, current);
          return;
        }
        if (current.diagnosticRequired || current.reproduced) current.browserDiagnosisObserved = true;
        if ((current.browserVerificationRequired || current.frontendBrowserVerificationRequired) && current.dirty) current.browserEvidenceAfterMutation = true;
        applyAutomaticBrowserCheckpoint(current, input, output, evidence);
        saveState(input.sessionID, current);
        return;
      }
      if (input.tool === "task" && typeof output.output === "string") {
        output.output = boundedOutput(output.output, MAX_TASK_OUTPUT_CHARS, "subagent output");
        return;
      }
      if (input.tool === "read" && typeof output.output === "string") {
        if (current.browserDiagnosticRequired && current.diagnosticRequired && !current.reproduced) {
          current.diagnosticReadCount += 1;
          saveState(input.sessionID, current);
        }
        output.output = boundedOutput(output.output, MAX_READ_OUTPUT_CHARS, "read output");
        return;
      }
      if (["glob", "grep", "list"].includes(input.tool) && current.browserDiagnosticRequired && current.diagnosticRequired && !current.reproduced) {
        current.diagnosticReadCount += 1;
        saveState(input.sessionID, current);
        return;
      }
      if (!isMutatingTool(input.tool, input.args)) return;
      if (/^(Error:|Could not find oldString|No changes to apply)/i.test(output.output || "")) return;
      current.dirty = true;
      current.verified = false;
      current.mutationRevision += 1;
      if (isFrontendMutation(input.tool, input.args)) {
        current.frontendBrowserVerificationRequired = true;
        current.browserEvidenceAfterMutation = false;
      }
      if (current.reproduced && current.browserDiagnosisObserved) {
        current.browserVerificationRequired = true;
        current.browserEvidenceAfterMutation = false;
      }
      if (isActionMutation(input.tool, input.args)) {
        current.actionDeployRequired = true;
        current.touchedActionEndpoints = [...new Set([
          ...(Array.isArray(current.touchedActionEndpoints) ? current.touchedActionEndpoints : []),
          ...touchedActionEndpoints(input.tool, input.args),
        ])].sort();
      }
      if (isSetupActionMutation(input.tool, input.args)) {
        current.actionSetupRequired = true;
      }
      if (input.tool === "bash" && isOpsIdeDeployCommand(input.args)) {
        const deployCheck = await run("timeout 120 check_openserverless_actions.sh .", directory, 140_000);
        current.actionDeployRequired = deployCheck.exitCode !== 0;
      }
      if (input.tool === "bash" && isOpsIdeSetupCommand(input.args) && !current.actionDeployRequired) {
        current.actionSetupRequired = false;
      }
      saveState(input.sessionID, current);
    },

    "experimental.text.complete": async (input, output) => {
      const current = stateFor(input.sessionID);
      if (current.statusRequest) {
        const pending = current.needsRecovery
          ? "Context recovery is still required."
          : current.browserVerificationRequired || current.frontendBrowserVerificationRequired
            ? "Post-fix browser verification is still pending."
            : current.diagnosticRequired && !current.reproduced
              ? "Diagnostic reproduction is still pending."
              : current.dirty && !current.verified
                ? "The current changes have not passed the completion gate yet."
                : "No Trustable verification gate is pending.";
        if (!String(output.text || "").trim()) output.text = "Work is still in progress.";
        output.text = `${output.text}\n\nTrustable status: ${pending}`;
        current.statusRequest = false;
        saveState(input.sessionID, current);
        return;
      }
      if (current.needsRecovery) {
        continueInternally(output, `Internal Trustable gate: context recovery is pending. Resume the active request after recovery and do not answer the user yet. Active request: ${current.activeTask || "unknown"}`);
        return;
      }
      if (!coreManaged && current.diagnosticRequired && !current.reproduced) {
        continueInternally(output, "Internal Trustable gate: diagnostic reproduction is pending. Reproduce the exact reported symptom with the browser tools before editing, then continue the active request. Do not answer the user yet.");
        return;
      }
      if (!coreManaged && current.browserVerificationRequired && !current.browserVerificationUnavailableReason) {
        continueInternally(output, taskRequiresActiveAudio(current)
          ? "Internal Trustable gate: post-change browser verification has not observed active audio. Exercise the exact user flow until audio is observably running, then run the completion check. Do not answer the user yet."
          : "Internal Trustable gate: post-change browser verification is pending. Exercise the exact fixed user flow with browser tools, collect concrete evidence, then run the completion check. Do not answer the user yet.");
        return;
      }
      if (coreManaged && current.dirty && !current.verified && current.completionChecksThisTask === 0 && !current.completionPrompted) {
        current.completionPrompted = true;
        saveState(input.sessionID, current);
        continueInternally(output, "Internal Trustable gate: run trustable_completion_check exactly once before the final response. If Browser QA was stopped because Agentic React interfered, do not reopen it; the completion gate will use typecheck, build, tests, and runtime diagnostics. Do not answer the user yet.");
        return;
      }
      if (!coreManaged && current.dirty && !current.verified) {
        if (current.completionRecoveryAttempts >= 1) {
          output.text = "I saved the work, but I could not complete the final step in this session.";
          output.synthetic = false;
          output.continue = false;
          saveState(input.sessionID, current);
          return;
        }
        current.completionRecoveryAttempts += 1;
        saveState(input.sessionID, current);
        continueInternally(output, `Internal Trustable gate: the current changes are not verified. Resolve this exact remaining failure: ${current.lastCompletionFailure || "run trustable_completion_check and inspect its complete output"}. Use read for source inspection; do not inspect checker scripts with shell pipelines. Rerun trustable_completion_check after the fix. Do not answer the user yet.`);
        return;
      }
    },
  };
}
