import assert from "node:assert/strict"
import { createServer } from "node:http"
import { fileURLToPath } from "node:url"
import test from "node:test"
import { Client } from "@modelcontextprotocol/sdk/client/index.js"
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js"
import { TrustableBrowser, resolveBrowserTarget } from "./browser.ts"

test("target resolution keeps development on localhost and deployed on configured Vite", () => {
  assert.equal(resolveBrowserTarget("development", "/#/login"), "http://localhost:5173/#/login")
  assert.equal(
    resolveBrowserTarget("deployed", "/dashboard", "http://vite.miniops.me"),
    "http://vite.miniops.me/dashboard",
  )
  assert.throws(() => resolveBrowserTarget("deployed", "/", "http://example.com"), /vite/)
  assert.throws(() => resolveBrowserTarget("development", "https://example.com"), /app-local path/)
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
  await new Promise<void>((resolve) => server.listen(5173, "127.0.0.1", resolve))
  t.after(() => server.close())

  const browser = new TrustableBrowser("", `/tmp/trustable-browser-test-${Date.now()}`)
  t.after(() => browser.close())
  const opened = await browser.open("development", "/")
  assert.match(opened.aria, /ACCEDI/)
  const clicked = await browser.interact("click", { kind: "role", role: "link", target: "ACCEDI" })
  assert.match(clicked.url, /#\/login$/)
  assert.match(clicked.text, /#\/login/)
  await new Promise((resolve) => setTimeout(resolve, 200))
  const diagnostics = browser.diagnostics()
  assert.equal(diagnostics.console.some((line) => line.includes("browser-test-warning")), true)
  assert.equal(diagnostics.network.some((line) => line.includes("404")), true)
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
  await new Promise<void>((resolve) => server.listen(5173, "127.0.0.1", resolve))
  t.after(() => server.close())

  const browser = new TrustableBrowser("", `/tmp/trustable-browser-registration-${Date.now()}`)
  t.after(() => browser.close())
  await browser.open("development", "/")

  await assert.rejects(
    browser.interact("fill", { kind: "placeholder", target: "••••••••", value: "secret" }),
    /ambiguous locator: placeholder="••••••••" matched 2 elements; specify zero-based index 0\.\.1/,
  )
  await assert.rejects(
    browser.interact("fill", { kind: "role", role: "textbox", target: "Missing", value: "secret" }),
    /element not found: role=textbox name="Missing"/,
  )

  await browser.interact("fill", { kind: "label", target: "Email", value: "dj@example.test" })
  await browser.interact("fill", { kind: "placeholder", target: "••••••••", index: 0, value: "secret-123" })
  await browser.interact("fill", { kind: "placeholder", target: "••••••••", index: 1, value: "secret-123" })
  const submitted = await browser.interact("click", { kind: "role", role: "button", target: "Register" })

  assert.match(submitted.text, /registered/)
  assert.deepEqual(JSON.parse(registration), {
    email: "dj@example.test",
    password: "secret-123",
    confirmPassword: "secret-123",
  })
})

test("MCP stdio contract exposes index and forwards it to browser_interact", async (t) => {
  const server = createServer((_request, response) => {
    response.setHeader("content-type", "text/html; charset=utf-8")
    response.end(`<!doctype html><html><body>
      <input type="password" placeholder="••••••••">
      <input type="password" placeholder="••••••••">
    </body></html>`)
  })
  await new Promise<void>((resolve) => server.listen(5173, "127.0.0.1", resolve))
  t.after(() => server.close())

  const packageDir = fileURLToPath(new URL("..", import.meta.url))
  const transport = new StdioClientTransport({
    command: process.execPath,
    args: ["--import", "tsx", "src/index.ts"],
    cwd: packageDir,
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
