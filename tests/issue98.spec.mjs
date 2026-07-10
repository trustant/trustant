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
  const result = await api(request, "GET", `/api/launch/${app}`, undefined, {
    timeout: 600_000,
  });
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
    const newParts = flattenMessageParts(newMessages);
    const runningTool = parts.some((part) => {
      const state = part.state?.status;
      return state === "pending" || state === "running";
    });
    const hasNewMessage = newMessages.length > 0;
    const hasAssistantText = newParts.some((part) => part.type === "text" && part.text);

    if (hasNewMessage && hasAssistantText && !runningTool && currentStatus !== "busy") {
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

function assertPromptToolSafety(messages) {
  const parts = flattenMessageParts(messages);
  const toolParts = parts.filter((part) => part.type === "tool");
  if (env.TRUSTABLE_E2E_PROMPT_EXPECT_TOOLS !== "0") {
    expect(toolParts.length, JSON.stringify(parts)).toBeGreaterThan(0);
  }

  const failedTools = toolParts.filter((part) => part.state?.status === "error");
  const hardFailedTools = failedTools.filter((part) => !isBenignPromptToolError(part));
  if (env.TRUSTABLE_E2E_PROMPT_ALLOW_TOOL_ERRORS !== "1") {
    expect(hardFailedTools, JSON.stringify(failedTools)).toEqual([]);
  }

  const rawOpsAction = toolParts.filter((part) => {
    if (part.tool !== "bash") {
      return false;
    }
    const command = part.state?.input?.command || "";
    return /(^|[;&|]\s*)ops\s+action(\s|$)/.test(command);
  });
  expect(rawOpsAction, JSON.stringify(rawOpsAction)).toEqual([]);

  if (env.TRUSTABLE_E2E_EXPECT_OPENSERVERLESS_TOOL === "1") {
    const usedOpenServerless = toolParts.some((part) => /^action[-_]/.test(part.tool || ""));
    expect(usedOpenServerless, JSON.stringify(toolParts.map((part) => part.tool))).toBeTruthy();
  }
}

function isBenignPromptToolError(part) {
  if (part.tool !== "list") {
    return false;
  }
  const state = part.state || {};
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
  assertPromptToolSafety(promptMessages);
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

  test("validates the generated authentication flow in a real browser", async ({ page, request }) => {
    test.skip(env.TRUSTABLE_E2E_AUTH !== "1", "Set TRUSTABLE_E2E_AUTH=1 to run the generated-app authentication flow.");
    test.setTimeout(Number(env.TRUSTABLE_E2E_AUTH_TIMEOUT_MS || 10 * 60 * 1000));

    let app = "";
    let created = false;
    try {
      const appInfo = await ensureApp(request);
      app = appInfo.app;
      created = appInfo.created;
      await launchApp(request, app);

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
