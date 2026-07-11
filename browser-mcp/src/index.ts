#!/usr/bin/env -S npx tsx

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js"
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js"
import { z } from "zod"
import { TrustableBrowser } from "./browser.ts"

const browser = new TrustableBrowser()
const server = new McpServer({ name: "trustable-browser-mcp", version: "0.2.0" })

function text(value: unknown) {
  return { content: [{ type: "text" as const, text: typeof value === "string" ? value : JSON.stringify(value, null, 2) }] }
}

server.registerTool("browser_open", {
  description: "Open the current Trustable app. Use development for the pod-local managed server at localhost:5173. Use deployed only after ops ide deploy to check the configured vite.<domain> ingress.",
  inputSchema: {
    mode: z.enum(["development", "deployed"]),
    path: z.string().optional().describe("App-local path beginning with /. Hash routes such as /#/login are supported."),
  },
}, async ({ mode, path }) => text(await browser.open(mode, path)))

server.registerTool("browser_snapshot", {
  description: "Read the current URL, title, accessibility snapshot, visible text, console warnings/errors, and failed HTTP requests without changing the page.",
  inputSchema: {},
}, async () => text(await browser.snapshot()))

server.registerTool("browser_interact", {
  description: "Perform one bounded browser interaction, then return a fresh structured snapshot. Prefer role, label, placeholder, or text locators over CSS. Locators are strict: when multiple elements match, retry with the explicit zero-based index reported by the error.",
  inputSchema: {
    action: z.enum(["click", "fill", "press", "reload", "back"]),
    kind: z.enum(["role", "text", "label", "placeholder", "css"]).optional(),
    target: z.string().optional().describe("Accessible name, visible text, label, placeholder, or CSS selector."),
    role: z.string().optional().describe("ARIA role when kind=role, for example button or link."),
    value: z.string().optional().describe("Text for fill or key name for press."),
    index: z.number().int().nonnegative().optional().describe("Zero-based match index. Required only when the locator is ambiguous."),
  },
}, async ({ action, kind, target, role, value, index }) => text(await browser.interact(action, { kind, target, role, value, index })))

server.registerTool("browser_diagnostics", {
  description: "Return the current URL plus collected console warnings/errors, page errors, failed requests, and HTTP responses with status >= 400.",
  inputSchema: {},
}, async () => text(browser.diagnostics()))

server.registerTool("browser_capture", {
  description: "Save a full-page screenshot and structured JSON snapshot for tester evidence. Returns pod-local artifact paths.",
  inputSchema: { name: z.string().optional() },
}, async ({ name }) => text(await browser.capture(name)))

server.registerTool("browser_close", {
  description: "Close the bounded browser session and discard its isolated browser state.",
  inputSchema: {},
}, async () => {
  await browser.close()
  return text("Browser session closed.")
})

process.on("SIGINT", async () => {
  await browser.close()
  process.exit(0)
})
process.on("SIGTERM", async () => {
  await browser.close()
  process.exit(0)
})

await server.connect(new StdioServerTransport())
