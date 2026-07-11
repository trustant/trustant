import { tool } from "@opencode-ai/plugin";
import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { basename, dirname, join, relative } from "node:path";

const STATE_DIR = join(homedir(), ".cache", "trustable", "opencode-guardrails");
const DIAGNOSTIC_REQUEST = /(does(?:n't| not) work|not working|still (?:fails|broken|doesn't)|failed|broken|white page|blank page|error|bug|fix(?: this)?|non funziona|non funzionano|non fa|non fanno|ancora|errore|problema|pagina bianca|bloccato)/i;
const MUTATING_BASH = /(^|[;&|]\s*)(sed\s+-i|perl\s+-pi|rm\s|mv\s|cp\s|install\s|mkdir\s|touch\s|truncate\s|tee\s|git\s+(add|commit|merge|rebase|reset|checkout|switch|restore|clean)|npm\s+(install|uninstall|update)|ops\s+ide\s+(deploy|setup|redeploy)|python(?:3)?\s+-c\s+.*(?:write|unlink|remove|rename))\b|(^|[^>])>{1,2}[^&]/i;
const ACTION_TOOL = /^(?:action[-_](?!(?:invoke|list|get|inspect|status)(?:$|[-_]))|openserverless_action_(?!(?:invoke|list|get|inspect|status)(?:$|_)))/;
const OPS_IDE_DEPLOY = /(^|[;&|]\s*)(?:timeout\s+\d+\s+)?ops\s+ide\s+deploy(?:\s|$)/i;
const OPS_IDE_SETUP = /(^|[;&|]\s*)(?:timeout\s+\d+\s+)?ops\s+ide\s+setup(?:\s|$)/i;
const TEST_DISCOVERY_MAX_DEPTH = 8;
const TEST_DISCOVERY_MAX_ENTRIES = 5_000;
const TEST_SUITE_LIMIT = 12;
const TEST_TIMEOUT_MS = 180_000;
const TEST_TOTAL_TIMEOUT_MS = 600_000;
const BROWSER_INTERACTION_BUDGET = 12;
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
  "After session compaction, call trustable_context_recover before any edit, write, action mutation, or deploy.",
  "For a reported bug, reproduce the exact user-visible symptom and record evidence with trustable_diagnostic_checkpoint before changing source.",
  "Trustable already ran ops ide login and launched the managed dev server with the configured application environment. Never rerun ops ide login during the session.",
  "After any action MCP or packages/** source change, run ops ide deploy before setup or completion. Never create or modify action ZIP files manually.",
  "Never run raw shell ops action or wsk action commands, including invoke and list. Use OpenServerless MCP tools for action mutation, inspection, and invocation; use ops ide deploy and ops ide setup for lifecycle operations.",
  "After changing a setup action, run ops ide setup after deploy and before completion.",
  "Never mask deploy, setup, login, checker, or build failures with || true, || echo, or head/tail pipelines.",
  "Never kill Trustable-managed processes or start vite, npm run dev, or ops ide devel; use the already-running localhost:5173 server.",
  "With React Router HashRouter, pass logical routes such as /login to Link, NavLink, Navigate, and useNavigate. Never pass #/login to router APIs and never use root-relative anchors for internal navigation.",
  "After source changes, call trustable_completion_check before claiming that work is fixed, complete, or ready for the user.",
  "Every action endpoint created or modified in this session requires a focused executable application test under tests/actions/<endpoint> or packages/<endpoint> before completion. The completion gate runs it without installing dependencies; never ask the user to run tests.",
  "Browser work is bounded: after repeated interactions, take a fresh browser snapshot or diagnostics and reason from that evidence instead of continuing blind clicks or fills.",
  "Two repeated completion failures open a circuit breaker and require fresh reproduction evidence before more changes.",
].join(" ");

export function isDiagnosticRequest(text) {
  return DIAGNOSTIC_REQUEST.test(text || "");
}

export function isMutatingTool(toolID, args = {}) {
  if (["edit", "write", "patch", "apply_patch"].includes(toolID)) return true;
  if (ACTION_TOOL.test(toolID)) return true;
  if (toolID !== "bash") return false;
  if (isOpsIdeDeployCommand(args) || isOpsIdeSetupCommand(args)) return true;
  return MUTATING_BASH.test(String(args.command || ""));
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
  const critical = /(?:ops\s+ide\s+(?:login|deploy|setup)|check_(?:openserverless_actions|trustable_app|trustable_frontend)\.sh|npm\s+run\s+build)/i.test(command);
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
    const state = { ...defaultState(), ...JSON.parse(readFileSync(statePath(sessionID), "utf8")) };
    state.touchedActionEndpoints = Array.isArray(state.touchedActionEndpoints)
      ? [...new Set(state.touchedActionEndpoints.map(normalizeActionEndpoint).filter(Boolean))].sort()
      : [];
    return state;
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
    reports.push([
      "===== focused action tests: FAIL =====",
      `Missing executable focused application test for: ${missing.join(", ")}`,
      "Add a recognized test under tests/actions/<endpoint>/ or packages/<endpoint>/; do not modify generated __main__.py.",
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
        description: "Run the deterministic Trustable completion gate after source changes: git diff validation, contract checkers, frontend build, and bounded existing Go/Python/JavaScript application tests.",
        args: {},
        async execute(_args, context) {
          const current = stateFor(context.sessionID);
          const result = current.actionDeployRequired
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
          if (result.passed) {
            current.dirty = false;
            current.verified = true;
            current.diagnosticRequired = false;
            current.reproduced = false;
            current.circuitOpen = false;
            current.actionDeployRequired = false;
            current.actionSetupRequired = false;
            current.touchedActionEndpoints = [];
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
      const current = stateFor(input.sessionID);
      current.browserInteractionsSinceEvidence = 0;
      const text = (output.parts || []).filter((part) => part.type === "text").map((part) => part.text || "").join("\n");
      if (isDiagnosticRequest(text)) {
        current.diagnosticRequired = true;
        current.reproduced = false;
        current.verified = false;
        current.evidence = "";
      }
      saveState(input.sessionID, current);
    },

    "experimental.chat.system.transform": async (input, output) => {
      const current = input.sessionID ? stateFor(input.sessionID) : defaultState();
      let status = CRITICAL_SYSTEM;
      if (current.needsRecovery) status += " CONTEXT RECOVERY IS REQUIRED NOW.";
      if (current.diagnosticRequired && !current.reproduced) status += " DIAGNOSTIC REPRODUCTION IS REQUIRED BEFORE SOURCE CHANGES.";
      if (current.dirty && !current.verified) status += " THE CURRENT CHANGES HAVE NOT PASSED THE COMPLETION GATE.";
      if (current.actionDeployRequired) status += " ACTION DEPLOY IS REQUIRED BEFORE SETUP OR COMPLETION.";
      if (current.actionSetupRequired) status += " ACTION SETUP IS REQUIRED AFTER DEPLOY AND BEFORE COMPLETION.";
      if (current.touchedActionEndpoints?.length) status += ` FOCUSED TESTS ARE REQUIRED FOR ACTION ENDPOINTS: ${current.touchedActionEndpoints.join(", ")}.`;
      output.system.push(status);
    },

    "experimental.session.compacting": async (_input, output) => {
      output.context.push(CRITICAL_SYSTEM);
      output.context.push("Compaction recovery state will block source mutations until trustable_context_recover is called in the continued turn.");
    },

    "tool.execute.before": async (input, output) => {
      const current = stateFor(input.sessionID);
      if (input.tool === "browser_browser_interact" && current.browserInteractionsSinceEvidence >= BROWSER_INTERACTION_BUDGET) {
        throw new Error(`Trustable browser diagnostic circuit breaker: ${BROWSER_INTERACTION_BUDGET} interactions ran without fresh evidence. Call browser_browser_snapshot or browser_browser_diagnostics, inspect the result, then continue with a bounded next action.`);
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
      if (current.actionDeployRequired && input.tool === "bash" && isOpsIdeSetupCommand(output.args)) {
        throw new Error("Trustable action guard: action source changed after the last deploy. Run timeout 120 ops ide deploy before ops ide setup.");
      }
      if (!isMutatingTool(input.tool, output.args)) return;
      if (current.needsRecovery) {
        throw new Error("Trustable guardrail: session context was compacted. Call trustable_context_recover before modifying files, actions, or deployment state.");
      }
      if ((current.diagnosticRequired && !current.reproduced) || current.circuitOpen) {
        throw new Error("Trustable diagnostic circuit breaker: reproduce the exact symptom and call trustable_diagnostic_checkpoint with concrete evidence before modifying source.");
      }
    },

    "tool.execute.after": async (input, output) => {
      const current = stateFor(input.sessionID);
      if (["browser_browser_open", "browser_browser_snapshot", "browser_browser_diagnostics"].includes(input.tool)) {
        current.browserInteractionsSinceEvidence = 0;
        saveState(input.sessionID, current);
        return;
      }
      if (input.tool === "browser_browser_interact") {
        current.browserInteractionsSinceEvidence += 1;
        saveState(input.sessionID, current);
        return;
      }
      if (!isMutatingTool(input.tool, input.args)) return;
      if (/^(Error:|Could not find oldString|No changes to apply)/i.test(output.output || "")) return;
      current.dirty = true;
      current.verified = false;
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
      if (current.diagnosticRequired && !current.reproduced) {
        output.text = "Trustable diagnostic gate: the reported symptom has not been reproduced yet. Continue with read-only diagnostics and record evidence with trustable_diagnostic_checkpoint before changing source.";
        return;
      }
      if (current.dirty && !current.verified) {
        output.text = "Trustable completion gate: the current changes are not verified. Continue by calling trustable_completion_check; do not ask the user to test an unverified result.";
        return;
      }
    },
  };
}
