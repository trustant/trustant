import assert from "node:assert/strict"
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises"
import { createServer } from "node:http"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { fileURLToPath } from "node:url"
import test from "node:test"
import { Client } from "@modelcontextprotocol/sdk/client/index.js"
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js"
import { captureHasEvidence, SerialTaskQueue, TrustableBrowser, resolveBrowserTarget, resolveManagedDevelopmentOrigin } from "./browser.ts"

async function listen(server: ReturnType<typeof createServer>) {
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve))
  const address = server.address()
  assert.ok(address && typeof address === "object")
  return `http://127.0.0.1:${address.port}/`
}

test("target resolution keeps development on localhost and deployed on configured Vite", () => {
  assert.equal(resolveBrowserTarget("development", "/#/login"), "http://localhost:5173/#/login")
  assert.equal(
    resolveBrowserTarget("deployed", "/dashboard", "http://vite.miniops.me"),
    "http://vite.miniops.me/dashboard",
  )
  assert.throws(() => resolveBrowserTarget("deployed", "/", "http://example.com"), /vite/)
  assert.throws(() => resolveBrowserTarget("development", "https://example.com"), /app-local path/)
})

test("persistent browser queue preserves call order and rejects empty evidence", async () => {
  const queue = new SerialTaskQueue()
  const order: string[] = []
  const first = queue.run(async () => {
    await new Promise((resolve) => setTimeout(resolve, 10))
    order.push("first")
  })
  const second = queue.run(async () => {
    order.push("second")
  })
  await Promise.all([first, second])
  assert.deepEqual(order, ["first", "second"])

  const empty = {
    url: "about:blank",
    title: "",
    aria: "",
    text: "",
    console: [],
    network: [],
    controls: [],
    audio: { contexts: [], media: [], active: false },
  }
  assert.equal(captureHasEvidence(empty, 128), false)
  assert.equal(
    captureHasEvidence({ ...empty, url: "http://localhost:5173/", text: "Ready" }, 128),
    true,
  )
})

test("managed development target is bound to the current runtime workbench", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "trustable-browser-runtime-"))
  t.after(() => rm(root, { recursive: true, force: true }))
  const current = join(root, "workbench", "trutest1")
  const other = join(root, "workbench", "otherapp")
  await mkdir(current, { recursive: true })
  await mkdir(other, { recursive: true })
  const runtimeConfig = join(root, "opencode-runtime.json")
  await writeFile(runtimeConfig, JSON.stringify({
    version: 2,
    workbenches: [{
      app: "trutest1",
      workspace: current,
      developmentUrl: "http://localhost:5173",
    }],
  }))

  assert.equal(await resolveManagedDevelopmentOrigin(runtimeConfig, current), "http://localhost:5173/")
  await assert.rejects(
    resolveManagedDevelopmentOrigin(runtimeConfig, other),
    /runtime manifest belongs to a different application.*do not use another app page as verification evidence/i,
  )
})

test("managed browser rejects stale version-1 runtime manifests", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "trustable-browser-runtime-"))
  t.after(() => rm(root, { recursive: true, force: true }))
  const current = join(root, "workbench", "trutest1")
  await mkdir(current, { recursive: true })
  const runtimeConfig = join(root, "runtime.json")
  await writeFile(runtimeConfig, JSON.stringify({
    version: 1,
    workbenches: [{ app: "trutest1", workspace: current, developmentUrl: "http://localhost:5173" }],
  }))
  await assert.rejects(resolveManagedDevelopmentOrigin(runtimeConfig, current), /invalid runtime manifest/)
})

test("browser observes navigation, console, and failed requests", async (t) => {
  const server = createServer((request, response) => {
    if (request.url === "/missing") {
      response.writeHead(404).end("missing")
      return
    }
    response.setHeader("content-type", "text/html; charset=utf-8")
    response.end(`<!doctype html><html><body>
      <a href="#/login">ACCEDI</a>
      <main id="view">Home</main>
      <script>
        console.warn('browser-test-warning')
        fetch('/missing')
        addEventListener('hashchange', () => document.querySelector('#view').textContent = location.hash)
      </script>
    </body></html>`)
  })
  const developmentOrigin = await listen(server)
  t.after(() => server.close())

  const browser = new TrustableBrowser("", `/tmp/trustable-browser-test-${Date.now()}`, "", process.cwd(), developmentOrigin)
  t.after(() => browser.close())
  const opened = await browser.open("development", "/")
  assert.match(opened.aria, /ACCEDI/)
  const clicked = await browser.interact("click", { kind: "role", role: "link", target: "ACCEDI" })
  assert.match(clicked.url, /#\/login$/)
  assert.match(clicked.text, /#\/login/)
  await new Promise((resolve) => setTimeout(resolve, 200))
  const diagnostics = await browser.diagnostics()
  assert.equal(diagnostics.console.some((line) => line.includes("browser-test-warning")), true)
  assert.equal(diagnostics.network.some((line) => line.includes("404")), true)
})

test("browser QA disables Agentic React controls without hiding application controls", async (t) => {
  const server = createServer((_request, response) => {
    response.setHeader("content-type", "text/html; charset=utf-8")
    response.end(`<!doctype html><html><body>
      <button type="button">Play note</button>
      <script>
        window.__AGENTIC_REACT_CONFIG__ = { toolkit: { enabled: true } }
        const toolkit = document.createElement('div')
        toolkit.dataset.agenticReactToolkit = 'true'
        toolkit.style.display = window.__AGENTIC_REACT_CONFIG__.toolkit.enabled ? 'flex' : 'none'
        const launcher = document.createElement('button')
        launcher.setAttribute('aria-label', 'Open Agentic React toolkit')
        toolkit.appendChild(launcher)
        document.body.appendChild(toolkit)
      </script>
    </body></html>`)
  })
  const developmentOrigin = await listen(server)
  t.after(() => server.close())

  const browser = new TrustableBrowser("", `/tmp/trustable-browser-agentic-${Date.now()}`, "", process.cwd(), developmentOrigin)
  t.after(() => browser.close())
  const opened = await browser.open("development", "/")

  assert.equal(opened.controls.some((control) => control.name.includes("Agentic React")), false)
  assert.equal(opened.controls.some((control) => control.name === "Play note"), true)
  assert.doesNotMatch(opened.aria, /Agentic React/i)
  assert.doesNotMatch(opened.text, /Agentic React/i)
})

test("browser submits registration with duplicate password placeholders by explicit index", async (t) => {
  let registration = ""
  const server = createServer((request, response) => {
    if (request.method === "POST" && request.url === "/register") {
      let body = ""
      request.setEncoding("utf8")
      request.on("data", (chunk) => { body += chunk })
      request.on("end", () => {
        registration = body
        response.setHeader("content-type", "text/plain")
        response.end("registered")
      })
      return
    }
    response.setHeader("content-type", "text/html; charset=utf-8")
    response.end(`<!doctype html><html><body>
      <form id="register">
        <label for="email">Email</label>
        <input id="email" name="email" type="email" placeholder="dj@example.test">
        <label for="password">Password</label>
        <input id="password" name="password" type="password" placeholder="••••••••">
        <label for="confirmPassword">Confirm password</label>
        <input id="confirmPassword" name="confirmPassword" type="password" placeholder="••••••••">
        <button type="submit">Register</button>
      </form>
      <output id="status"></output>
      <script>
        document.querySelector('#register').addEventListener('submit', async (event) => {
          event.preventDefault()
          const fields = Object.fromEntries(new FormData(event.currentTarget))
          const result = await fetch('/register', {
            method: 'POST',
            headers: { 'content-type': 'application/json' },
            body: JSON.stringify(fields),
          })
          document.querySelector('#status').textContent = await result.text()
        })
      </script>
    </body></html>`)
  })
  const developmentOrigin = await listen(server)
  t.after(() => server.close())

  const browser = new TrustableBrowser("", `/tmp/trustable-browser-registration-${Date.now()}`, "", process.cwd(), developmentOrigin)
  t.after(() => browser.close())
  const opened = await browser.open("development", "/")

  await assert.rejects(
    browser.interact("fill", { kind: "placeholder", target: "••••••••", value: "secret" }),
    /ambiguous locator: placeholder="••••••••" matched 2 elements; specify zero-based index 0\.\.1/,
  )
  await assert.rejects(
    browser.interact("fill", { kind: "role", role: "textbox", target: "Missing", value: "secret" }),
    /element not found: role=textbox name="Missing"/,
  )

  const email = opened.controls.find((control) => control.name === "Email")
  const password = opened.controls.find((control) => control.name === "Password")
  const confirmation = opened.controls.find((control) => control.name === "Confirm password")
  assert.ok(email?.ref)
  assert.ok(password?.ref)
  assert.ok(confirmation?.ref)
  await browser.interact("fill", { kind: "ref", target: email.ref, value: "dj@example.test" })
  await browser.interact("fill", { kind: "ref", target: password.ref, value: "secret-123" })
  await browser.interact("fill", { kind: "ref", target: confirmation.ref, value: "secret-123" })
  const submitted = await browser.interact("click", { kind: "role", role: "button", target: "Register" })

  assert.match(submitted.text, /registered/)
  assert.deepEqual(JSON.parse(registration), {
    email: "dj@example.test",
    password: "secret-123",
    confirmPassword: "secret-123",
  })
})

test("browser reports observable audio state after a user gesture", async (t) => {
  const server = createServer((_request, response) => {
    response.setHeader("content-type", "text/html; charset=utf-8")
    response.end(`<!doctype html><html><body>
      <button id="play" type="button">Play music</button>
      <script>
        document.querySelector('#play').addEventListener('click', async () => {
          const context = new AudioContext()
          const oscillator = context.createOscillator()
          const gain = context.createGain()
          gain.gain.value = 0.01
          oscillator.connect(gain).connect(context.destination)
          oscillator.start()
          await context.resume()
        })
      </script>
    </body></html>`)
  })
  const developmentOrigin = await listen(server)
  t.after(() => server.close())

  const browser = new TrustableBrowser("", `/tmp/trustable-browser-audio-${Date.now()}`, "", process.cwd(), developmentOrigin)
  t.after(() => browser.close())
  const opened = await browser.open("development", "/")
  assert.equal(opened.audio.active, false)
  const play = opened.controls.find((control) => control.name === "Play music")
  assert.ok(play?.ref)
  const playing = await browser.interact("click", { kind: "ref", target: play.ref })
  assert.equal(playing.audio.contexts.some((context) => context.state === "running"), true, JSON.stringify(playing))
  assert.equal(playing.audio.active, true, JSON.stringify(playing.audio))
})

test("role interactions accept bounded shorthand without weakening strict matches", async (t) => {
  const server = createServer((_request, response) => {
    response.setHeader("content-type", "text/html; charset=utf-8")
    response.end(`<!doctype html><html><body>
      <label for="email">Email</label><input id="email">
      <button type="button">Registrati</button>
      <button type="button">Annulla</button>
    </body></html>`)
  })
  const developmentOrigin = await listen(server)
  t.after(() => server.close())

  const browser = new TrustableBrowser("", `/tmp/trustable-browser-role-${Date.now()}`, "", process.cwd(), developmentOrigin)
  t.after(() => browser.close())
  await browser.open("development", "/")

  await browser.interact("fill", { kind: "role", target: "Email", value: "user@example.test" })
  await browser.interact("click", { kind: "role", target: "Registrati" })
  await assert.rejects(
    browser.interact("click", { kind: "role", target: "button" }),
    /ambiguous locator: role=button name="" matched 2 elements/,
  )
  await assert.rejects(
    browser.interact("click", { kind: "role", role: "button", target: "button" }),
    /ambiguous locator: role=button name="" matched 2 elements/,
  )
  await browser.interact("click", { kind: "role", target: "button", index: 1 })
})

test("MCP stdio contract exposes index and forwards it to browser_interact", async (t) => {
  const server = createServer((_request, response) => {
    response.setHeader("content-type", "text/html; charset=utf-8")
    response.end(`<!doctype html><html><body>
      <input type="password" placeholder="••••••••">
      <input type="password" placeholder="••••••••">
    </body></html>`)
  })
  const developmentOrigin = await listen(server)
  t.after(() => server.close())

  const packageDir = fileURLToPath(new URL("..", import.meta.url))
  const transport = new StdioClientTransport({
    command: process.execPath,
    args: ["--import", "tsx", "src/index.ts"],
    cwd: packageDir,
    env: {
      ...process.env,
      NODE_ENV: "test",
      TRUSTABLE_BROWSER_TEST_DEVELOPMENT_ORIGIN: developmentOrigin,
    },
    stderr: "pipe",
  })
  const client = new Client({ name: "trustable-browser-test", version: "1.0.0" })
  await client.connect(transport)
  t.after(() => client.close())

  const tools = await client.listTools()
  const interact = tools.tools.find((tool) => tool.name === "browser_interact")
  assert.ok(interact)
  const indexSchema = interact.inputSchema.properties?.index as { type?: string; minimum?: number }
  assert.equal(indexSchema.type, "integer")
  assert.equal(indexSchema.minimum, 0)
  const kindSchema = interact.inputSchema.properties?.kind as { enum?: string[] }
  assert.equal(kindSchema.enum?.includes("ref"), true)

  await client.callTool({ name: "browser_open", arguments: { mode: "development", path: "/" } })
  const ambiguous = await client.callTool({
    name: "browser_interact",
    arguments: { action: "fill", kind: "placeholder", target: "••••••••", value: "secret" },
  })
  assert.equal(ambiguous.isError, true)
  assert.match(JSON.stringify(ambiguous.content), /ambiguous locator.*matched 2 elements/)

  const selected = await client.callTool({
    name: "browser_interact",
    arguments: { action: "fill", kind: "placeholder", target: "••••••••", index: 1, value: "second-secret" },
  })
  assert.notEqual(selected.isError, true)
  assert.match(JSON.stringify(selected.content), /second-secret/)
  await client.callTool({ name: "browser_close", arguments: {} })
})
