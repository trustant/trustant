#!/usr/bin/env -S npx tsx

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js"
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js"
import {
  inspectReactProject,
  resolveManagedReactRoot,
  validateReactAuthFlow,
  validateReactProject,
  validateReactRoutes,
} from "./analyzer.ts"

const root = resolveManagedReactRoot()
const server = new McpServer({ name: "trustable-react-mcp", version: "0.1.0" })

// WHY: keep the tool surface read-only and rootless from the model's point of
// view. Every operation analyzes the single workbench selected by Trustable's
// host-owned manifest, so it cannot drift to another mounted application.
function text(value: unknown) {
  return {
    content: [{
      type: "text" as const,
      text: typeof value === "string" ? value : JSON.stringify(value, null, 2),
    }],
  }
}

server.registerTool("react_project_inspect", {
  description: "Inspect the current Trustable React/Vite project, router kind, source count, and declared scripts without modifying files.",
  inputSchema: {},
}, async () => text(inspectReactProject(root)))

server.registerTool("react_validate_routes", {
  description: "AST-validate React Router usage for the current Trustable app. In particular, HashRouter APIs must receive logical paths and internal anchors must not reload the document.",
  inputSchema: {},
}, async () => text({ findings: validateReactRoutes(root) }))

server.registerTool("react_validate_auth_flow", {
  description: "AST-validate observable authentication invariants: backend session bootstrap, protected-route state, and accessible form controls.",
  inputSchema: {},
}, async () => text({ findings: validateReactAuthFlow(root) }))

server.registerTool("react_validate", {
  description: "Run the bounded TypeScript, React route, and authentication validation suite for the current Trustable workbench. This is read-only and returns structured findings.",
  inputSchema: {},
}, async () => text(validateReactProject(root)))

await server.connect(new StdioServerTransport())
