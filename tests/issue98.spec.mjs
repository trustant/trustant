import { test, expect } from "@playwright/test";
import { execFileSync } from "node:child_process";

const env = process.env;

const namespace = env.TRUSTABLE_E2E_NAMESPACE || "nuvolaris";
const pod = env.TRUSTABLE_E2E_POD || "trustable-0";
const container = env.TRUSTABLE_E2E_CONTAINER || "trustable";
const domain = env.TRUSTABLE_E2E_DOMAIN || "miniops.me";
const trustableURL = env.TRUSTABLE_E2E_TRUSTABLE_URL || `http://trustable.${domain}`;
const opencodeURL = env.TRUSTABLE_E2E_OPENCODE_URL || `http://opencode.${domain}`;
const viteURL = env.TRUSTABLE_E2E_VITE_URL || `http://vite.${domain}`;
const authURL = env.TRUSTABLE_E2E_AUTH_URL || viteURL;
const defaultPrompt = [
  "Issue98 E2E check: do not modify files.",
  "You must use the bash tool now. Do not answer from memory or from previous session results.",
  "Run exactly: printf 'issue98-e2e-nonce={nonce}\\n' && timeout 60 check_openserverless_actions.sh .",
  "Then answer with one line that includes the nonce and whether the check passed.",
  "If you cannot call bash, answer exactly CANNOT_USE_BASH.",
].join(" ");

function kubectl(args, options = {}) {
  return execFileSync("kubectl", ["-n", namespace, ...args], {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
    timeout: options.timeout || 120_000,
  }).trim();
}

function podShell(command, options = {}) {
  return kubectl([
    "exec",
    pod,
    "-c",
    container,
    "--",
    "sh",
    "-lc",
    command,
  ], options);
}

function shellQuote(value) {
  return `'${String(value).replace(/'/g, "'\\''")}'`;
}

function workbenchGitStatus(workbenchDir) {
  const safeDirectory = shellQuote(`safe.directory=${workbenchDir}`);
  return podShell(`cd ${shellQuote(workbenchDir)} && git -c ${safeDirectory} status --porcelain`);
}

function uniqueAppName() {
  const suffix = Date.now().toString(36).replace(/[^a-z0-9]/g, "").slice(-8);
  return `trudg${suffix}`.slice(0, 20);
}

async function api(request, method, path, body, options = {}) {
  const response = await request.fetch(`${trustableURL}${path}`, {
    method,
    data: body,
    timeout: options.timeout || 300_000,
  });
  const text = await response.text();
  let json = null;
  try {
    json = text ? JSON.parse(text) : null;
  } catch {
    // Keep plain text for error reporting.
  }
  return { response, text, json };
}

async function waitForTrustableReady(request) {
  const deadline = Date.now() + Number(env.TRUSTABLE_E2E_READY_TIMEOUT_MS || 60_000);
  let lastStatus = 0;
  let lastText = "";
  while (Date.now() < deadline) {
    try {
      const response = await request.get(`${trustableURL}/applist.html`, { timeout: 5_000 });
      lastStatus = response.status();
      lastText = await response.text();
      if (response.ok()) return;
    } catch (error) {
      lastText = error instanceof Error ? error.message : String(error);
    }
    await new Promise((resolve) => setTimeout(resolve, 2_000));
  }
  throw new Error(`Trustable did not become ready: HTTP ${lastStatus} ${lastText.slice(0, 500)}`);
}

async function ensureApp(request) {
  const configured = env.TRUSTABLE_E2E_APP;
  if (configured) {
    return { app: configured, created: false };
  }

  const repo = env.TRUSTABLE_E2E_REPO;
  if (!repo) {
    throw new Error(
      "Set TRUSTABLE_E2E_APP=<existing-app> or TRUSTABLE_E2E_REPO=<org/repo> to create a fresh app.",
    );
  }

  const app = env.TRUSTABLE_E2E_NEW_APP || uniqueAppName();
  const password = env.TRUSTABLE_E2E_PASSWORD || `E2e${Date.now()}!`;
  const result = await api(request, "POST", "/api/repo", { name: app, repo, password }, {
    timeout: 300_000,
  });
  expect(result.response.status(), result.text).toBe(201);
  return { app, created: true };
}

async function launchApp(request, app) {
  let result;
  for (let attempt = 1; attempt <= 3; attempt += 1) {
    result = await api(request, "GET", `/api/launch/${app}`, undefined, {
      timeout: 600_000,
    });
    if (!result.json?.error?.includes("Repeat the login")) break;
    if (attempt < 3) await new Promise((resolve) => setTimeout(resolve, attempt * 5_000));
  }
  expect(result.response.ok(), result.text).toBeTruthy();
  expect(result.json, result.text).toBeTruthy();
  expect(result.json.error, result.text).toBeFalsy();
  expect(result.json.encdir, result.text).toContain(`/workbench/${app}`);
  expect(result.json.b64dir, result.text).toBeTruthy();
  expect(result.json.left, result.text).toBe(4096);
  expect(result.json.right, result.text).toBe(5173);
  return result.json;
}

function flattenMessageParts(messages) {
  return messages.flatMap((message) => message.parts || []);
}

async function sessionMessages(request, sessionID, workbenchDir, limit = 40) {
  const response = await request.get(
    `${opencodeURL}/session/${sessionID}/message?directory=${encodeURIComponent(workbenchDir)}&limit=${limit}`,
    { timeout: 30_000 },
  );
  expect(response.ok(), await response.text()).toBeTruthy();
  return response.json();
}

async function sessionStatus(request) {
  const response = await request.get(`${opencodeURL}/session/status`, { timeout: 30_000 });
  if (!response.ok()) {
    return {};
  }
  return response.json();
}

async function pendingSessionPermissions(request, sessionID) {
  const response = await request.get(`${opencodeURL}/api/session/${sessionID}/permission`, {
    timeout: 30_000,
  });
  if (!response.ok()) {
    return [];
  }
  const body = await response.json();
  return body.data || [];
}

async function pendingSessionQuestions(request, sessionID) {
  const response = await request.get(`${opencodeURL}/api/session/${sessionID}/question`, {
    timeout: 30_000,
  });
  if (!response.ok()) {
    return [];
  }
  const body = await response.json();
  return body.data || [];
}

async function replyToPermission(request, sessionID, requestID, reply) {
  const response = await request.post(`${opencodeURL}/api/session/${sessionID}/permission/${requestID}/reply`, {
    data: { reply },
    timeout: 30_000,
  });
  expect(response.ok(), await response.text()).toBeTruthy();
}

async function waitForPromptIdle(request, sessionID, workbenchDir, baselineMessageIDs) {
  const timeoutMs = Number(env.TRUSTABLE_E2E_PROMPT_TIMEOUT_MS || 10 * 60 * 1000);
  const deadline = Date.now() + timeoutMs;
  let stableIdleSince = 0;
  let lastMessages = [];

  while (Date.now() < deadline) {
    const permissions = await pendingSessionPermissions(request, sessionID);
    if (permissions.length > 0) {
      if (env.TRUSTABLE_E2E_AUTO_APPROVE === "1") {
        for (const permission of permissions) {
          await replyToPermission(request, sessionID, permission.id, "once");
        }
      } else {
        throw new Error(`OpenCode is waiting for permission: ${JSON.stringify(permissions)}`);
      }
    }

    const questions = await pendingSessionQuestions(request, sessionID);
    if (questions.length > 0) {
      throw new Error(`OpenCode asked a question during E2E: ${JSON.stringify(questions)}`);
    }

    const status = await sessionStatus(request);
    const currentStatus = status[sessionID]?.type || "idle";
    const messages = await sessionMessages(request, sessionID, workbenchDir);
    lastMessages = messages;
    const newMessages = messages.filter((message) => !baselineMessageIDs.has(message.info?.id));
    const parts = flattenMessageParts(messages);
    const newAssistantMessages = newMessages.filter((message) => message.info?.role === "assistant");
    const runningTool = parts.some((part) => {
      const state = part.state?.status;
      return state === "pending" || state === "running";
    });
    const hasNewMessage = newMessages.length > 0;
    const hasFinalAssistantResponse = newAssistantMessages.some((message) => {
      const finish = message.info?.finish;
      return Boolean(message.info?.time?.completed && finish && finish !== "tool-calls");
    });

    if (hasNewMessage && hasFinalAssistantResponse && !runningTool && currentStatus !== "busy") {
      if (stableIdleSince === 0) {
        stableIdleSince = Date.now();
      }
      if (Date.now() - stableIdleSince >= 5_000) {
        return messages;
      }
    } else {
      stableIdleSince = 0;
    }

    await new Promise((resolve) => setTimeout(resolve, 2_000));
  }

  throw new Error(`Timed out waiting for OpenCode prompt to finish. Last messages: ${JSON.stringify(lastMessages).slice(0, 4000)}`);
}

function unwrappedShellSegments(command) {
  return String(command || "").split(/&&|\|\||[;|\n]/).map((rawSegment) => {
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

function assertPromptToolSafety(messages) {
  const parts = flattenMessageParts(messages);
  const toolParts = parts.filter((part) => part.type === "tool");
  if (env.TRUSTABLE_E2E_PROMPT_EXPECT_TOOLS !== "0") {
    expect(toolParts.length, JSON.stringify(parts)).toBeGreaterThan(0);
  }

  const failedTools = toolParts.filter((part) => part.state?.status === "error");
  const hardFailedTools = failedTools.filter((part) => !isBenignPromptToolError(part, toolParts));
  if (env.TRUSTABLE_E2E_PROMPT_ALLOW_TOOL_ERRORS !== "1") {
    expect(hardFailedTools, JSON.stringify(failedTools)).toEqual([]);
  }

  const rawOpsAction = toolParts.filter((part) => {
    if (part.tool !== "bash") {
      return false;
    }
    const command = part.state?.input?.command || "";
    return unwrappedShellSegments(command).some((segment) => /^(?:ops|wsk)\s+action(?:\s|$)/i.test(segment));
  });
  expect(rawOpsAction, JSON.stringify(rawOpsAction)).toEqual([]);

  const managedLogin = toolParts.filter((part) => {
    if (part.tool !== "bash") return false;
    const command = part.state?.input?.command || "";
    return unwrappedShellSegments(command).some((segment) => /^ops\s+ide\s+login(?:\s|$)/i.test(segment));
  });
  expect(managedLogin, JSON.stringify(managedLogin)).toEqual([]);

  const maskedCritical = toolParts.filter((part) => {
    if (part.tool !== "bash" || part.state?.status !== "completed") return false;
    const command = part.state?.input?.command || "";
    return /(?:ops\s+ide\s+(?:login|deploy|setup)|check_(?:openserverless_actions|trustable_app|trustable_frontend)\.sh|npm\s+run\s+build)/i.test(command) && /(?:\|\|\s*(?:true|echo\b)|\|\s*(?:head|tail)\b)/i.test(command);
  });
  expect(maskedCritical, JSON.stringify(maskedCritical)).toEqual([]);

  const managedProcessCommands = toolParts.filter((part) => {
    if (part.tool !== "bash") return false;
    const command = part.state?.input?.command || "";
    return /(?:^|[;&|]\s*)(?:(?:cd|env)\b[^;&|]*&&\s*)?(?:(?:npx|bunx)\s+)?vite\b|(?:^|[;&|]\s*)npm\s+run\s+dev\b|(?:^|[;&|]\s*)ops\s+ide\s+devel\b|(?:^|[;&|]\s*)(?:sudo\s+)?(?:kill|pkill|killall)\b/i.test(command);
  });
  expect(managedProcessCommands, JSON.stringify(managedProcessCommands)).toEqual([]);

  if (env.TRUSTABLE_E2E_EXPECT_OPENSERVERLESS_TOOL === "1") {
    const usedOpenServerless = toolParts.some((part) => /^(?:openserverless_)?action[-_]/.test(part.tool || ""));
    expect(usedOpenServerless, JSON.stringify(toolParts.map((part) => part.tool))).toBeTruthy();
  }
}

function commandPosition(command, pattern) {
  const match = pattern.exec(command);
  return match ? match.index : -1;
}

function compareTracePosition(left, right) {
  return left.partIndex - right.partIndex || left.commandIndex - right.commandIndex;
}

function assertActionWorkflow(messages) {
  const parts = flattenMessageParts(messages);
  const toolParts = parts.filter((part) => part.type === "tool");
  const mutations = [];
  const deploys = [];
  const setups = [];
  const completions = [];
  const manualZip = [];

  toolParts.forEach((part, partIndex) => {
    const tool = part.tool || "";
    const input = part.state?.input || {};
    const command = String(input.command || "");
    const path = String(input.filePath || input.path || "").replaceAll("\\", "/");
    const patch = String(input.patch || input.input || "");
    const position = (commandIndex = 0) => ({ partIndex, commandIndex, tool, command });

    if (/^(?:openserverless_)?action[-_](?!invoke(?:$|[-_]))/i.test(tool)) {
      mutations.push(position());
    }
    if (["edit", "write", "patch"].includes(tool) && /(^|\/)packages\//.test(path)) {
      mutations.push(position());
    }
    if (tool === "apply_patch" && /(?:^|[\s/])packages\//m.test(patch)) {
      mutations.push(position());
    }
    if (tool === "bash") {
      const deployIndex = commandPosition(command, /(?:^|[;&|]\s*)(?:timeout\s+\d+\s+)?ops\s+ide\s+deploy(?:\s|$)/i);
      const setupIndex = commandPosition(command, /(?:^|[;&|]\s*)(?:timeout\s+\d+\s+)?ops\s+ide\s+setup(?:\s|$)/i);
      if (deployIndex >= 0) deploys.push(position(deployIndex));
      if (setupIndex >= 0) setups.push(position(setupIndex));
      if (/(?:^|[\s'"`])packages\//i.test(command) && /(?:zipfile\.ZipFile|python(?:3)?\s+-m\s+zipfile\s+-c|(?:^|[;&|]\s*)zip\s|\.zip\b[^\n]*(?:>|rm|mv|cp|touch|truncate|tee))/i.test(command)) {
        manualZip.push(position());
      }
    }
    if (["edit", "write", "patch"].includes(tool) && /(^|\/)packages\/.*\.zip$/i.test(path)) {
      manualZip.push(position());
    }
    if (tool === "trustable_completion_check") completions.push(position());
  });

  expect(manualZip, JSON.stringify(manualZip)).toEqual([]);
  expect(mutations.length, JSON.stringify(toolParts.map((part) => part.tool))).toBeGreaterThan(0);
  expect(deploys.length, JSON.stringify(toolParts)).toBeGreaterThan(0);
  expect(completions.length, JSON.stringify(toolParts.map((part) => part.tool))).toBeGreaterThan(0);

  for (const mutation of mutations) {
    expect(
      deploys.some((deploy) => compareTracePosition(mutation, deploy) < 0),
      `No deploy follows action mutation ${JSON.stringify(mutation)} in ${JSON.stringify(toolParts)}`,
    ).toBeTruthy();
  }

  const finalMutation = mutations.slice().sort(compareTracePosition).at(-1);
  const finalDeploy = deploys.filter((deploy) => compareTracePosition(finalMutation, deploy) < 0).sort(compareTracePosition).at(0);
  const finalCompletion = completions.slice().sort(compareTracePosition).at(-1);
  expect(finalDeploy, `No deploy follows final action mutation in ${JSON.stringify(toolParts)}`).toBeTruthy();
  expect(compareTracePosition(finalMutation, finalDeploy)).toBeLessThan(0);
  expect(compareTracePosition(finalDeploy, finalCompletion)).toBeLessThan(0);

  if (env.TRUSTABLE_E2E_EXPECT_SETUP === "1") {
    expect(setups.length, JSON.stringify(toolParts)).toBeGreaterThan(0);
    const setupAfterDeploy = setups.find((setup) => compareTracePosition(finalDeploy, setup) < 0);
    expect(setupAfterDeploy, `No setup follows final deploy in ${JSON.stringify(toolParts)}`).toBeTruthy();
    expect(compareTracePosition(setupAfterDeploy, finalCompletion)).toBeLessThan(0);
  }
}

function assertFinalAssistantResponse(messages) {
  const finalAssistantMessages = messages.filter((message) =>
    message.info?.role === "assistant" &&
    message.info?.time?.completed &&
    message.info?.finish &&
    message.info.finish !== "tool-calls");
  const finalAssistantText = finalAssistantMessages.flatMap((message) => message.parts || [])
    .filter((part) => part.type === "text")
    .map((part) => part.text || "")
    .join("\n")
    .trim();
  expect(finalAssistantText, JSON.stringify(finalAssistantMessages)).not.toBe("");
}

function isBenignPromptToolError(part, toolParts = []) {
  const state = part.state || {};
  const failedIndex = toolParts.indexOf(part);
  const later = toolParts.slice(failedIndex + 1);
  if (part.tool === "bash" && /do not mask critical command failures/i.test(state.error || "")) {
    const critical = /(?:ops\s+ide\s+(?:login|deploy|setup)|check_(?:openserverless_actions|trustable_app|trustable_frontend)\.sh|npm\s+run\s+build)/i;
    const masked = /(?:\|\|\s*(?:true|echo\b)|\|\s*(?:head|tail)\b)/i;
    return later.some((candidate) => {
      const command = String(candidate.state?.input?.command || "");
      return candidate.tool === "bash" && candidate.state?.status === "completed" && critical.test(command) && !masked.test(command);
    });
  }
  if (/session context was compacted.*trustable_context_recover/i.test(state.error || "")) {
    return later.some((candidate) => candidate.tool === "trustable_context_recover" && candidate.state?.status === "completed");
  }
  if (part.tool === "read" && /File not found:/i.test(state.error || "")) {
    const path = state.input?.filePath || state.input?.path || "";
    const parent = path.replace(/\/[^/]+$/, "");
    const parentRelative = parent.replace(/^\/home\/trustable\/workspace\/workbench\/[^/]+\//, "");
    const inspectedParent = later.some((candidate) => {
      const input = JSON.stringify(candidate.state?.input || {});
      return ["list", "glob", "bash"].includes(candidate.tool) && (input.includes(parent) || input.includes(parentRelative)) && candidate.state?.status === "completed";
    });
    const readSibling = later.some((candidate) => candidate.tool === "read" && (candidate.state?.input?.filePath || candidate.state?.input?.path || "").startsWith(`${parent}/`) && candidate.state?.status === "completed");
    return path.startsWith("/home/trustable/workspace/workbench/") && inspectedParent && readSibling;
  }
  if (["edit", "write"].includes(part.tool) && /(?:oldString.*not found|Could not find oldString|No changes to apply|oldString == newString)/i.test(state.error || "")) {
    const path = state.input?.filePath || state.input?.path || "";
    const rereadIndex = later.findIndex((candidate) => candidate.tool === "read" && (candidate.state?.input?.filePath || candidate.state?.input?.path) === path && candidate.state?.status === "completed");
    if (rereadIndex < 0) return false;
    const rereadOutput = String(later[rereadIndex].state?.output || "");
    const oldString = String(state.input?.oldString || "");
    const newString = String(state.input?.newString || "");
    const alreadyApplied = Boolean(newString) && rereadOutput.includes(newString) && (!oldString || !rereadOutput.includes(oldString));
    const laterEdit = later.slice(rereadIndex + 1).some((candidate) => ["edit", "write"].includes(candidate.tool) && (candidate.state?.input?.filePath || candidate.state?.input?.path) === path && candidate.state?.status === "completed");
    return alreadyApplied || laterEdit;
  }
  if (part.tool === "edit" && /invalid arguments:.*Missing key|SchemaError\(Missing key/i.test(state.error || "")) {
    const path = state.input?.filePath || state.input?.path || "";
    return Boolean(path) && later.some((candidate) =>
      ["edit", "write"].includes(candidate.tool) &&
      (candidate.state?.input?.filePath || candidate.state?.input?.path) === path &&
      candidate.state?.status === "completed");
  }
  if (part.tool?.startsWith("browser_") && /(?:role is required|element not found|requires locator|ambiguous locator|locator index out of range|execution context was destroyed)/i.test(state.error || "")) {
    const failedIndex = toolParts.indexOf(part);
    return toolParts.slice(failedIndex + 1).some((later) => later.tool === part.tool && later.state?.status === "completed");
  }
  if (part.tool !== "list") return false;
  const inputPath = state.input?.path || "";
  const error = state.error || "";
  return (
    inputPath.startsWith("/home/trustable/workspace/workbench/") &&
    error.includes("ENOENT: no such file or directory")
  );
}

async function runPromptStep(request, app, launch) {
  const workbenchDir = launch.encdir;
  const sessionID = launch.session_id;
  const promptNonce = `issue98-${Date.now().toString(36)}`;
  const prompt = (env.TRUSTABLE_E2E_PROMPT || defaultPrompt).replace("{nonce}", promptNonce);
  const { messages, promptMessages, beforeStatus } = await sendPromptAndWait(request, app, launch, prompt);

  assertPromptToolSafety(promptMessages);
  assertFinalAssistantResponse(promptMessages);
  if (env.TRUSTABLE_E2E_EXPECT_ACTION_WORKFLOW === "1") {
    assertActionWorkflow(promptMessages);
  }
  if (!env.TRUSTABLE_E2E_PROMPT) {
    const promptParts = flattenMessageParts(promptMessages);
    const toolContainsNonce = promptParts.some((part) => {
      return part.type === "tool" && JSON.stringify(part).includes(promptNonce);
    });
    expect(toolContainsNonce, JSON.stringify(promptParts)).toBeTruthy();
  }

  const checkerOutput = podShell(
    `cd ${shellQuote(workbenchDir)} && timeout 120 check_trustable_app.sh .`,
    { timeout: 150_000 },
  );
  expect(checkerOutput).toContain("Trustable app completion check passed");

  const afterStatus = workbenchGitStatus(workbenchDir);
  if (env.TRUSTABLE_E2E_PROMPT_EXPECT_CHANGES === "1") {
    expect(afterStatus, `No new worktree changes detected for ${app}`).not.toBe(beforeStatus);
  }

  return { messages, beforeStatus, afterStatus, checkerOutput };
}

async function validateExistingPromptStep(request, app, launch) {
  const messages = await sessionMessages(request, launch.session_id, launch.encdir, 2_000);
  expect(messages.some((message) => message.info?.role === "user"), JSON.stringify(messages)).toBeTruthy();
  assertPromptToolSafety(messages);
  assertFinalAssistantResponse(messages);
  if (env.TRUSTABLE_E2E_EXPECT_ACTION_WORKFLOW === "1") assertActionWorkflow(messages);

  const checkerOutput = podShell(
    `cd ${shellQuote(launch.encdir)} && timeout 120 check_trustable_app.sh .`,
    { timeout: 150_000 },
  );
  expect(checkerOutput).toContain("Trustable app completion check passed");
  if (env.TRUSTABLE_E2E_PROMPT_EXPECT_CHANGES === "1") {
    expect(workbenchGitStatus(launch.encdir), `No worktree changes found for retained app ${app}`).not.toBe("");
  }
}

async function sendPromptAndWait(request, app, launch, prompt) {
  const workbenchDir = launch.encdir;
  const sessionID = launch.session_id;
  const beforeMessages = await sessionMessages(request, sessionID, workbenchDir);
  const beforeMessageIDs = new Set(beforeMessages.map((message) => message.info?.id).filter(Boolean));
  const beforeStatus = workbenchGitStatus(workbenchDir);

  const response = await request.post(
    `${opencodeURL}/session/${sessionID}/prompt_async?directory=${encodeURIComponent(workbenchDir)}`,
    {
      data: {
        agent: env.TRUSTABLE_E2E_PROMPT_AGENT || "build",
        parts: [{ type: "text", text: prompt }],
      },
      timeout: 30_000,
    },
  );
  expect(response.status(), await response.text()).toBe(204);

  const messages = await waitForPromptIdle(request, sessionID, workbenchDir, beforeMessageIDs);
  const promptMessages = messages.filter((message) => !beforeMessageIDs.has(message.info?.id));
  return { messages, promptMessages, beforeMessages, beforeMessageIDs, beforeStatus, app };
}

function isTraceMutation(part) {
  if (part.type !== "tool" || part.state?.status !== "completed") return false;
  const tool = part.tool || "";
  const input = part.state?.input || {};
  const command = String(input.command || "");
  if (["edit", "write", "patch", "apply_patch"].includes(tool)) return true;
  if (/^(?:openserverless_)?action[-_]/.test(tool)) return true;
  if (tool !== "bash") return false;
  return /(?:sed\s+-i|perl\s+-pi|rm\s|mv\s|cp\s|mkdir\s|touch\s|truncate\s|tee\s|git\s+(?:add|commit|merge|rebase|reset|checkout|switch|restore|clean)|npm\s+(?:install|uninstall|update)|ops\s+ide\s+(?:deploy|setup|redeploy)|>{1,2})/i.test(command);
}

function assertCompactionRecovery(messages) {
  const parts = flattenMessageParts(messages);
  expect(parts.some((part) => part.type === "compaction"), JSON.stringify(parts)).toBeTruthy();
  const tools = parts.filter((part) => part.type === "tool");
  const recoveryIndex = tools.findIndex((part) => part.tool === "trustable_context_recover" && part.state?.status === "completed");
  expect(recoveryIndex, JSON.stringify(tools.map((part) => [part.tool, part.state?.status]))).toBeGreaterThanOrEqual(0);
  const firstMutation = tools.findIndex(isTraceMutation);
  expect(firstMutation, JSON.stringify(tools.map((part) => part.tool))).toBeGreaterThan(recoveryIndex);
  let completionIndex = -1;
  tools.forEach((part, index) => {
    if (part.tool === "trustable_completion_check" && part.state?.status === "completed") completionIndex = index;
  });
  expect(completionIndex, JSON.stringify(tools.map((part) => part.tool))).toBeGreaterThan(firstMutation);
}

async function compactOpenCodeSession(request, launch) {
  const headers = { "X-Opencode-Directory": launch.encdir };
  const current = await request.post(`${opencodeURL}/api/session/${launch.session_id}/compact`, {
    headers,
    timeout: 300_000,
  });
  if (current.status() === 204) return "compact";

  const currentError = await current.text();
  if (current.status() !== 503 || !currentError.includes("Session compact is not available yet")) {
    expect(current.status(), currentError).toBe(204);
  }

  const modelRef = podShell(`jq -r '.model' ${shellQuote(`${launch.encdir}/opencode.json`)}`);
  const separator = modelRef.indexOf("/");
  expect(separator, `Invalid OpenCode model reference: ${modelRef}`).toBeGreaterThan(0);
  const providerID = modelRef.slice(0, separator);
  const modelID = modelRef.slice(separator + 1);
  const legacy = await request.post(
    `${opencodeURL}/session/${launch.session_id}/summarize?directory=${encodeURIComponent(launch.encdir)}`,
    {
      data: { providerID, modelID },
      headers,
      timeout: 300_000,
    },
  );
  expect(legacy.status(), await legacy.text()).toBe(200);
  return "summarize";
}

async function cleanupCreatedApp(request, app, created) {
  await api(request, "DELETE", "/api/launch", undefined, { timeout: 60_000 }).catch(() => {});
  if (!created || env.TRUSTABLE_E2E_KEEP_APP === "1") {
    return;
  }
  await api(request, "DELETE", "/api/repo", { name: app }, { timeout: 300_000 }).catch(() => {});
}

test.describe("issue98 guardrail E2E", () => {
  test("launches an app with scoped OpenCode, MCP, checker, and generated guardrails", async ({ page, request }) => {
    test.setTimeout(15 * 60 * 1000);
    let app = "";
    let created = false;

    try {
      await test.step("Trustable UI is reachable through FQDN", async () => {
        await page.goto(`${trustableURL}/applist.html`, { waitUntil: "domcontentloaded" });
        await expect(page.locator("h1")).toContainText("Trustable");
      });

      const appInfo = await test.step("select or create app", async () => ensureApp(request));
      app = appInfo.app;
      created = appInfo.created;

      const launch = await test.step("launch app", async () => launchApp(request, app));
      const workbenchDir = launch.encdir;

      await test.step("browser-visible OpenCode is scoped to the launched app", async () => {
        const projects = await request.get(`${opencodeURL}/project`, { timeout: 30_000 });
        expect(projects.ok(), await projects.text()).toBeTruthy();
        const body = await projects.json();
        expect(Array.isArray(body), JSON.stringify(body)).toBeTruthy();
        expect(body.length, JSON.stringify(body)).toBe(1);
        expect(JSON.stringify(body)).toContain(app);
        expect(JSON.stringify(body)).toContain(workbenchDir);
      });

      await test.step("OpenCode session exists for canonical workbench path", async () => {
        const sessions = await request.get(
          `${opencodeURL}/session?directory=${encodeURIComponent(workbenchDir)}&roots=true&limit=10`,
          { timeout: 30_000 },
        );
        expect(sessions.ok(), await sessions.text()).toBeTruthy();
        const body = await sessions.json();
        expect(Array.isArray(body), JSON.stringify(body)).toBeTruthy();
        expect(JSON.stringify(body)).toContain(workbenchDir);
      });

      await test.step("Trustable session picker shows persistent OpenCode history", async () => {
        const historyTitle = `Issue98 history ${Date.now().toString(36)}`;
        const created = await request.post(
          `${opencodeURL}/session?directory=${encodeURIComponent(workbenchDir)}`,
          {
            data: { title: historyTitle },
            headers: { "X-Opencode-Directory": workbenchDir },
            timeout: 30_000,
          },
        );
        expect(created.ok(), await created.text()).toBeTruthy();
        const historySession = await created.json();

        try {
          const history = await request.get(`${trustableURL}/api/opencode/sessions/${app}`, {
            timeout: 30_000,
          });
          expect(history.ok(), await history.text()).toBeTruthy();
          const sessions = await history.json();
          expect(sessions.map((session) => session.id)).toContain(launch.session_id);
          expect(sessions.map((session) => session.id)).toContain(historySession.id);

          await page.context().addCookies([
            { name: "LEFT", value: opencodeURL, url: trustableURL },
            { name: "RIGHT", value: viteURL, url: trustableURL },
            { name: "NAME", value: app, url: trustableURL },
            { name: "URLDIR", value: workbenchDir, url: trustableURL },
            { name: "B64DIR", value: launch.b64dir, url: trustableURL },
            { name: "SESSIONID", value: launch.session_id, url: trustableURL },
          ]);
          await page.goto(`${trustableURL}/app.html`, { waitUntil: "domcontentloaded" });
          const picker = page.getByRole("button", { name: /Sessions/ });
          await expect(picker).toContainText(/\(\d+\)/);
          await picker.click();
          const historyItem = page.getByRole("menuitem", { name: new RegExp(historyTitle) });
          await expect(historyItem).toBeVisible();
          await historyItem.click();
          await expect(page.locator("#leftFrame")).toHaveAttribute("src", new RegExp(`/session/${historySession.id}$`));
          await page.evaluate((sessionID) => switchOpenCodeSession(sessionID), launch.session_id);
          await expect(page.locator("#leftFrame")).toHaveAttribute("src", new RegExp(`/session/${launch.session_id}$`));
        } finally {
          await request.delete(
            `${opencodeURL}/session/${historySession.id}?directory=${encodeURIComponent(workbenchDir)}`,
            { headers: { "X-Opencode-Directory": workbenchDir }, timeout: 30_000 },
          ).catch(() => {});
        }
      });

      await test.step("MCP list includes OpenServerless and the bounded browser", async () => {
        const mcp = await request.get(`${opencodeURL}/mcp`, { timeout: 30_000 });
        expect(mcp.ok(), await mcp.text()).toBeTruthy();
        const body = await mcp.text();
        expect(body).toContain("openserverless");
        expect(body).toContain("browser");
      });

      await test.step("app-local OpenCode config has issue98 guardrails", async () => {
        const configRaw = podShell(`cat ${JSON.stringify(`${workbenchDir}/opencode.json`)}`);
        const config = JSON.parse(configRaw);
        const agents = podShell(`cat ${JSON.stringify(`${workbenchDir}/AGENTS.md`)}`);
        expect(agents).toContain("TRUSTABLE-MANAGED-AGENTS-BEGIN");
        expect(agents).toContain("Ignore `CLAUDE.md`");
        expect(agents).toContain(".openserverless-contract.md");
        expect(config.instructions).toEqual([
          `${workbenchDir}/.openserverless-contract.md`,
          `${workbenchDir}/opencode.md`,
        ]);
        const contractRealpath = podShell(`realpath ${shellQuote(config.instructions[0])}`);
        const opencodeRealpath = podShell(`realpath ${shellQuote(config.instructions[1])}`);
        expect(contractRealpath).toBe(`${workbenchDir}/.openserverless-contract.md`);
        expect(opencodeRealpath).toBe(`${workbenchDir}/opencode.md`);
        expect(config.mcp.openserverless.command).toEqual(["openserverless-mcp"]);
        expect(config.mcp.browser.command).toEqual(["trustable-browser-mcp"]);
        expect(config.mcp.browser.environment.TRUSTABLE_BROWSER_ARTIFACT_DIR).toContain(`/.trustable/browser/${app}`);
        expect(config.mcp.browser.environment.TRUSTABLE_BROWSER_EXTERNAL_ORIGIN).toContain("vite.");
        expect(config.permission.edit["packages/**/__main__.py"]).toBe("deny");
        expect(config.permission.edit["packages/**/*.zip"]).toBe("deny");
        expect(config.permission.bash["ops action"]).toBe("deny");
        expect(config.permission.bash["ops action *"]).toBe("deny");
        const guardrailPlugin = podShell("test -f ~/.config/opencode/plugins/trustable-guardrails.js && echo present");
        expect(guardrailPlugin).toBe("present");
      });

      await test.step("action and frontend checkers pass inside the pod", async () => {
        const output = podShell(
          `cd ${JSON.stringify(workbenchDir)} && timeout 120 check_trustable_app.sh .`,
          { timeout: 150_000 },
        );
        expect(output).toContain("Trustable app completion check passed");
      });

      await test.step("pod-local Vite/dev server responds", async () => {
        const status = podShell("curl -fsS -o /dev/null -w '%{http_code}' http://localhost:5173/", {
          timeout: 30_000,
        });
        expect(status).toMatch(/^2|3/);
      });

      await test.step("browser-visible Vite host is reachable after launch/deploy", async () => {
        const response = await request.get(`${viteURL}/`, { timeout: 30_000 });
        expect(response.ok(), await response.text()).toBeTruthy();
      });
    } finally {
      if (app) {
        await cleanupCreatedApp(request, app, created);
      }
    }
  });

  test("can drive an OpenCode prompt and enforce issue98 safety", async ({ request }) => {
    test.skip(env.TRUSTABLE_E2E_RUN_PROMPT !== "1", "Set TRUSTABLE_E2E_RUN_PROMPT=1 to run the model-driven prompt step.");
    test.setTimeout(Number(env.TRUSTABLE_E2E_PROMPT_TEST_TIMEOUT_MS || 15 * 60 * 1000));

    let app = "";
    let created = false;

    try {
      const appInfo = await ensureApp(request);
      app = appInfo.app;
      created = appInfo.created;
      const launch = await launchApp(request, app);

      await test.step("send prompt and wait for model/tool loop", async () => {
        await runPromptStep(request, app, launch);
      });
    } finally {
      if (app) {
        await cleanupCreatedApp(request, app, created);
      }
    }
  });

  test("recovers mandatory context after real compaction and preserves the session", async ({ request }) => {
    test.skip(env.TRUSTABLE_E2E_COMPACTION !== "1", "Set TRUSTABLE_E2E_COMPACTION=1 to run the long-session compaction test.");
    test.setTimeout(Number(env.TRUSTABLE_E2E_COMPACTION_TEST_TIMEOUT_MS || 30 * 60 * 1000));

    let app = "";
    let created = false;
    try {
      const appInfo = await ensureApp(request);
      app = appInfo.app;
      created = appInfo.created;
      const launch = await launchApp(request, app);
      const marker = `issue98-compaction-${Date.now().toString(36)}`;

      await test.step("establish a real pre-compaction conversation", async () => {
        const initial = await sendPromptAndWait(
          request,
          app,
          launch,
          `Leggi le regole principali del progetto senza modificare file e rispondi includendo ${marker}.`,
        );
        expect(JSON.stringify(initial.promptMessages)).toContain(marker);
      });

      const beforeCompact = await sessionMessages(request, launch.session_id, launch.encdir, 100);
      const beforeCompactIDs = new Set(beforeCompact.map((message) => message.info?.id).filter(Boolean));
      await compactOpenCodeSession(request, launch);

      const afterCompact = await waitForPromptIdle(request, launch.session_id, launch.encdir, beforeCompactIDs);
      const compactMessages = afterCompact.filter((message) => !beforeCompactIDs.has(message.info?.id));

      const followup = await test.step("continue with a source change after recovery", async () => {
        return sendPromptAndWait(
          request,
          app,
          launch,
          `Ora rendi il titolo della pagina iniziale più accogliente e aggiungi il testo ${marker}. Controlla tu il risultato e completa il lavoro.`,
        );
      });
      assertPromptToolSafety(followup.promptMessages);
      assertCompactionRecovery([...compactMessages, ...followup.promptMessages]);

      const checkerOutput = podShell(
        `cd ${shellQuote(launch.encdir)} && timeout 120 check_trustable_app.sh .`,
        { timeout: 150_000 },
      );
      expect(checkerOutput).toContain("Trustable app completion check passed");

      await api(request, "DELETE", "/api/launch", undefined, { timeout: 60_000 });
      const relaunched = await launchApp(request, app);
      expect(relaunched.session_id).toBe(launch.session_id);
      const persisted = await sessionMessages(request, relaunched.session_id, relaunched.encdir, 100);
      expect(JSON.stringify(persisted)).toContain(marker);
    } finally {
      if (app) await cleanupCreatedApp(request, app, created);
    }
  });

  test("validates the generated authentication flow in a real browser", async ({ page, request }) => {
    test.skip(env.TRUSTABLE_E2E_AUTH !== "1", "Set TRUSTABLE_E2E_AUTH=1 to run the generated-app authentication flow.");
    test.setTimeout(Number(env.TRUSTABLE_E2E_AUTH_TIMEOUT_MS || 10 * 60 * 1000));

    let app = "";
    let created = false;
    try {
      await waitForTrustableReady(request);
      const appInfo = await ensureApp(request);
      app = appInfo.app;
      created = appInfo.created;
      const launch = await launchApp(request, app);

      if (env.TRUSTABLE_E2E_RUN_PROMPT === "1") {
        await test.step("build and verify authentication in this app", async () => {
          await runPromptStep(request, app, launch);
        });
      } else if (env.TRUSTABLE_E2E_VALIDATE_EXISTING_PROMPT === "1") {
        await test.step("revalidate the persisted OpenCode trace", async () => {
          await validateExistingPromptStep(request, app, launch);
        });
      }

      const suffix = Date.now().toString(36);
      const username = `Tester ${suffix}`;
      const email = `trustable-e2e-${suffix}@example.test`;
      const password = `Trustable-${suffix}-A1!`;
      const loginName = /accedi|login|sign in|entra/i;
      const registerName = /crea account|registrati|register|sign up/i;
      const submitRegisterName = /crea|registrati|register|sign up|continua/i;
      const submitLoginName = /accedi|login|sign in|entra/i;
      const logoutName = /esci|logout|sign out/i;

      await test.step("public home reaches the login form", async () => {
        await page.goto(authURL, { waitUntil: "domcontentloaded" });
        const loginControl = page.getByRole("link", { name: loginName }).or(page.getByRole("button", { name: loginName })).first();
        await expect(loginControl).toBeVisible();
        await loginControl.click();
        await expect(page.locator('input[type="email"]')).toBeVisible();
        await expect(page.locator('input[type="password"]').first()).toBeVisible();
      });

      await test.step("registration creates a real authenticated session", async () => {
        await page.goto(authURL, { waitUntil: "domcontentloaded" });
        const registerControl = page.getByRole("link", { name: registerName }).or(page.getByRole("button", { name: registerName })).first();
        await expect(registerControl).toBeVisible();
        await registerControl.click();

        const emailInput = page.locator('input[type="email"]');
        const passwordInputs = page.locator('input[type="password"]');
        await expect(emailInput).toBeVisible();
        await expect(passwordInputs.first()).toBeVisible();

        const usernameInput = page.locator('input[name*="user" i], input[name*="name" i], input[type="text"]').first();
        if (await usernameInput.count()) await usernameInput.fill(username);
        await emailInput.fill(email);
        await passwordInputs.first().fill(password);
        if (await passwordInputs.count() > 1) await passwordInputs.nth(1).fill(password);

        const submit = page.getByRole("button", { name: submitRegisterName }).first();
        await expect(submit).toBeVisible();
        await submit.click();
        await expect(page).not.toHaveURL(/(?:#\/)?(?:login|register)(?:[/?#]|$)/i);
      });

      await test.step("authenticated state survives a full reload", async () => {
        const protectedURL = page.url();
        await page.reload({ waitUntil: "domcontentloaded" });
        await expect(page).not.toHaveURL(/(?:#\/)?(?:login|register)(?:[/?#]|$)/i);
        expect(page.url()).toBe(protectedURL);
        await expect(page.locator('input[type="password"]')).toHaveCount(0);
      });

      await test.step("logout protects the private area", async () => {
        const logout = page.getByRole("button", { name: logoutName }).or(page.getByRole("link", { name: logoutName })).first();
        await expect(logout).toBeVisible();
        await logout.click();
        await expect(page.getByRole("link", { name: loginName }).or(page.getByRole("button", { name: loginName })).first()).toBeVisible();
      });

      await test.step("the created account can log in again", async () => {
        const loginControl = page.getByRole("link", { name: loginName }).or(page.getByRole("button", { name: loginName })).first();
        await loginControl.click();
        await page.locator('input[type="email"]').fill(email);
        await page.locator('input[type="password"]').first().fill(password);
        await page.getByRole("button", { name: submitLoginName }).first().click();
        await expect(page).not.toHaveURL(/(?:#\/)?login(?:[/?#]|$)/i);
        await page.reload({ waitUntil: "domcontentloaded" });
        await expect(page).not.toHaveURL(/(?:#\/)?login(?:[/?#]|$)/i);
      });
    } finally {
      if (app) await cleanupCreatedApp(request, app, created);
    }
  });
});
