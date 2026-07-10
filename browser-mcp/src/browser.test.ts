import assert from "node:assert/strict"
import { createServer } from "node:http"
import test from "node:test"
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
    response.setHeader("content-type", "text/html")
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
