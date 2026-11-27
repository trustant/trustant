/*
 * Copyright 2025-2026 Nuvolaris Inc
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published
 * by the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <https://www.gnu.org/licenses/>.
 */

import assert from "node:assert/strict"
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import test from "node:test"
import { inspectReactProject, validateReactAuthFlow, validateReactRoutes } from "./analyzer.ts"

function project(source: string): string {
  const root = mkdtempSync(join(tmpdir(), "trustant-react-mcp-"))
  mkdirSync(join(root, "src"), { recursive: true })
  writeFileSync(join(root, "package.json"), JSON.stringify({
    dependencies: { react: "19.0.0", "react-router-dom": "7.0.0" },
    devDependencies: { typescript: "5.5.0", vite: "6.0.0" },
    scripts: { build: "vite build" },
  }))
  writeFileSync(join(root, "src", "App.tsx"), source)
  return root
}

test("detects HashRouter paths that already contain a hash", (t) => {
  const root = project(`
    import { HashRouter, Navigate, useNavigate } from "react-router-dom"
    export function App() {
      const navigate = useNavigate()
      return <HashRouter><Navigate to="#/login" /><button onClick={() => navigate("#/home")}>Go</button></HashRouter>
    }
  `)
  t.after(() => rmSync(root, { recursive: true, force: true }))

  assert.equal(inspectReactProject(root).router, "hash")
  const findings = validateReactRoutes(root)
  assert.equal(findings.filter((finding) => finding.rule === "hash-router-logical-path").length, 2)
})

test("accepts logical HashRouter paths", (t) => {
  const root = project(`
    import { HashRouter, Navigate, Link } from "react-router-dom"
    export function App() {
      return <HashRouter><Navigate to="/login" /><Link to="/home">Home</Link></HashRouter>
    }
  `)
  t.after(() => rmSync(root, { recursive: true, force: true }))
  assert.deepEqual(validateReactRoutes(root), [])
})

test("reports persisted auth without backend bootstrap and inaccessible controls", (t) => {
  const root = project(`
    export function Login() {
      const token = localStorage.getItem("session_token")
      return <form><label>Email</label><input /><button>Login</button>{token}</form>
    }
  `)
  t.after(() => rmSync(root, { recursive: true, force: true }))
  const rules = validateReactAuthFlow(root).map((finding) => finding.rule)
  assert.ok(rules.includes("auth-session-bootstrap"))
  assert.ok(rules.includes("auth-control-semantics"))
  assert.ok(rules.includes("auth-label-semantics"))
})

test("rejects browser-side JWT interpretation for Redis-backed sessions", (t) => {
  const root = project(`
    import { jwtDecode } from "jwt-decode"
    export function Session() {
      const token = localStorage.getItem("session_token")
      const claims = token ? jwtDecode(token) : null
      return <div>{String(claims)}</div>
    }
  `)
  t.after(() => rmSync(root, { recursive: true, force: true }))
  const rules = validateReactAuthFlow(root).map((finding) => finding.rule)
  assert.ok(rules.includes("auth-opaque-session-token"))
})
