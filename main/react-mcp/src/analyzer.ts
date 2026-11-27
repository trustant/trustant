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

import { readFileSync, readdirSync, realpathSync, statSync } from "node:fs"
import { basename, isAbsolute, join, relative, resolve, sep } from "node:path"
import ts from "typescript"

export type FindingSeverity = "error" | "warning" | "info"

export interface ReactFinding {
  severity: FindingSeverity
  rule: string
  file: string
  line: number
  message: string
}

export interface ReactProjectInspection {
  root: string
  react: boolean
  vite: boolean
  typescript: boolean
  router: "hash" | "browser" | "data-hash" | "data-browser" | "none"
  sourceFiles: number
  scripts: Record<string, string>
}

interface RuntimeWorkbench {
  app: string
  workspace: string
}

interface RuntimeManifest {
  version: number
  workbenches: RuntimeWorkbench[]
}

const SOURCE_EXTENSIONS = new Set([".js", ".jsx", ".ts", ".tsx"])

function containsPath(root: string, target: string): boolean {
  const value = relative(root, target)
  return value === "" || (value !== ".." && !value.startsWith(`..${sep}`) && !isAbsolute(value))
}

function canonicalDirectory(value: string): string {
  const canonical = realpathSync(resolve(value))
  if (!statSync(canonical).isDirectory()) throw new Error(`React MCP project root is not a directory: ${value}`)
  return canonical
}

/**
 * Resolve the current project only from Trustant's host-owned manifest.
 *
 * WHY: a static-analysis server must not accept a model-selected root that
 * could inspect another application mounted in the same VM or pod.
 */
export function resolveManagedReactRoot(
  runtimeConfig = process.env.TRUSTANT_RUNTIME_CONFIG || "",
  directory = process.cwd(),
): string {
  if (!runtimeConfig) throw new Error("Trustant React MCP requires TRUSTANT_RUNTIME_CONFIG")
  const manifest = JSON.parse(readFileSync(resolve(runtimeConfig), "utf8")) as RuntimeManifest
  if (manifest.version !== 2 || !Array.isArray(manifest.workbenches)) {
    throw new Error("Trustant React MCP received an invalid runtime manifest")
  }
  const cwd = canonicalDirectory(directory)
  const matches = manifest.workbenches.filter((workbench) => {
    if (!workbench || typeof workbench.workspace !== "string" || !isAbsolute(workbench.workspace)) return false
    return containsPath(canonicalDirectory(workbench.workspace), cwd)
  })
  if (matches.length !== 1) {
    throw new Error(`Trustant React MCP expected one workbench for ${cwd}, found ${matches.length}`)
  }
  return canonicalDirectory(matches[0].workspace)
}

function extension(path: string): string {
  const index = path.lastIndexOf(".")
  return index >= 0 ? path.slice(index) : ""
}

function collectSourceFiles(root: string): string[] {
  const sourceRoot = join(root, "src")
  try {
    if (!statSync(sourceRoot).isDirectory()) return []
  } catch {
    return []
  }
  const files: string[] = []
  const visit = (directory: string) => {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      if (entry.name === "node_modules" || entry.name.startsWith(".")) continue
      const path = join(directory, entry.name)
      if (entry.isDirectory()) visit(path)
      else if (entry.isFile() && SOURCE_EXTENSIONS.has(extension(entry.name))) files.push(path)
    }
  }
  visit(sourceRoot)
  return files.sort()
}

function scriptKind(path: string): ts.ScriptKind {
  if (path.endsWith(".tsx")) return ts.ScriptKind.TSX
  if (path.endsWith(".jsx")) return ts.ScriptKind.JSX
  if (path.endsWith(".ts")) return ts.ScriptKind.TS
  return ts.ScriptKind.JS
}

function sourceFile(path: string): ts.SourceFile {
  return ts.createSourceFile(path, readFileSync(path, "utf8"), ts.ScriptTarget.Latest, true, scriptKind(path))
}

function lineOf(source: ts.SourceFile, node: ts.Node): number {
  return source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1
}

function fileLabel(root: string, path: string): string {
  return relative(root, path) || basename(path)
}

function jsxTagName(node: ts.JsxOpeningLikeElement): string {
  return node.tagName.getText()
}

function jsxStringAttribute(node: ts.JsxOpeningLikeElement, name: string): string | undefined {
  const attribute = node.attributes.properties.find(
    (candidate): candidate is ts.JsxAttribute =>
      ts.isJsxAttribute(candidate) && candidate.name.getText() === name,
  )
  if (!attribute?.initializer) return undefined
  if (ts.isStringLiteral(attribute.initializer)) return attribute.initializer.text
  if (
    ts.isJsxExpression(attribute.initializer)
    && attribute.initializer.expression
    && ts.isStringLiteralLike(attribute.initializer.expression)
  ) {
    return attribute.initializer.expression.text
  }
  return undefined
}

function callName(expression: ts.Expression): string {
  if (ts.isIdentifier(expression)) return expression.text
  if (ts.isPropertyAccessExpression(expression)) return expression.name.text
  return ""
}

function firstStringArgument(node: ts.CallExpression): string | undefined {
  const value = node.arguments[0]
  return value && ts.isStringLiteralLike(value) ? value.text : undefined
}

function detectRouter(files: ts.SourceFile[]): ReactProjectInspection["router"] {
  let router: ReactProjectInspection["router"] = "none"
  const visit = (node: ts.Node) => {
    if (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) {
      const tag = jsxTagName(node)
      if (tag === "HashRouter") router = "hash"
      else if (tag === "BrowserRouter" && router === "none") router = "browser"
    }
    if (ts.isCallExpression(node)) {
      const name = callName(node.expression)
      if (name === "createHashRouter") router = "data-hash"
      else if (name === "createBrowserRouter" && router === "none") router = "data-browser"
    }
    ts.forEachChild(node, visit)
  }
  for (const file of files) ts.forEachChild(file, visit)
  return router
}

export function inspectReactProject(root: string): ReactProjectInspection {
  const canonical = canonicalDirectory(root)
  const paths = collectSourceFiles(canonical)
  const files = paths.map(sourceFile)
  let packageJson: { dependencies?: Record<string, string>; devDependencies?: Record<string, string>; scripts?: Record<string, string> } = {}
  try {
    packageJson = JSON.parse(readFileSync(join(canonical, "package.json"), "utf8"))
  } catch {
    // A missing package manifest is reported by the inspection result instead
    // of turning a read-only diagnostic into an opaque MCP transport failure.
  }
  const dependencies = { ...packageJson.dependencies, ...packageJson.devDependencies }
  return {
    root: canonical,
    react: Boolean(dependencies.react),
    vite: Boolean(dependencies.vite),
    typescript: Boolean(dependencies.typescript) || paths.some((path) => path.endsWith(".ts") || path.endsWith(".tsx")),
    router: detectRouter(files),
    sourceFiles: paths.length,
    scripts: packageJson.scripts || {},
  }
}

export function validateReactRoutes(root: string): ReactFinding[] {
  const canonical = canonicalDirectory(root)
  const files = collectSourceFiles(canonical).map(sourceFile)
  const router = detectRouter(files)
  const hashRouter = router === "hash" || router === "data-hash"
  const findings: ReactFinding[] = []
  if (!hashRouter) return findings

  for (const source of files) {
    const visit = (node: ts.Node) => {
      if (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) {
        const tag = jsxTagName(node)
        if (["Link", "NavLink", "Navigate"].includes(tag)) {
          const target = jsxStringAttribute(node, "to")
          if (target?.startsWith("#/")) {
            findings.push({
              severity: "error",
              rule: "hash-router-logical-path",
              file: fileLabel(canonical, source.fileName),
              line: lineOf(source, node),
              message: `${tag} must receive a logical path such as /login; HashRouter adds the hash.`,
            })
          }
        }
        if (tag === "a") {
          const href = jsxStringAttribute(node, "href")
          if (href?.startsWith("/") && !href.startsWith("//") && !href.startsWith("/api/") && !href.startsWith("/assets/")) {
            findings.push({
              severity: "error",
              rule: "hash-router-root-anchor",
              file: fileLabel(canonical, source.fileName),
              line: lineOf(source, node),
              message: "Internal HashRouter navigation must use Link/NavLink instead of a root-relative anchor.",
            })
          }
        }
      }
      if (ts.isCallExpression(node) && callName(node.expression) === "navigate") {
        const target = firstStringArgument(node)
        if (target?.startsWith("#/")) {
          findings.push({
            severity: "error",
            rule: "hash-router-logical-path",
            file: fileLabel(canonical, source.fileName),
            line: lineOf(source, node),
            message: "navigate() must receive a logical path such as /login; HashRouter adds the hash.",
          })
        }
      }
      ts.forEachChild(node, visit)
    }
    ts.forEachChild(source, visit)
  }
  return findings
}

export function validateReactAuthFlow(root: string): ReactFinding[] {
  const canonical = canonicalDirectory(root)
  const files = collectSourceFiles(canonical).map(sourceFile)
  const findings: ReactFinding[] = []
  const combined = files.map((file) => file.text).join("\n")
  const browserTokenDecoding =
    /\b(?:jwtDecode|decodeJwt|jsonwebtoken)\b/i.test(combined)
    || /\batob\s*\([^)]*\.split\s*\(\s*["']\.["']\s*\)/is.test(combined)
  // WHY: a component can be named Session rather than Login and still decode
  // the application token. JWT decoding itself is sufficient evidence of an
  // auth surface and must not be skipped by the early no-auth fast path.
  const hasAuthSurface =
    /\b(login|register|logout|isAuthenticated|Authorization|Bearer)\b/i.test(combined)
    || browserTokenDecoding
  if (!hasAuthSurface) return findings

  const persistedToken = /localStorage\.(?:getItem|setItem)\([^)]*(?:token|session)/i.test(combined)
  const backendValidation = /fetch\s*\([^;]{0,600}(?:\/me\b|whoami|validate[-_/]?session)|\b(?:bootstrapAuth|restoreAuth|validateSession|getCurrentUser|loadCurrentUser)\s*\(/is.test(combined)
  if (persistedToken && !backendValidation) {
    findings.push({
      severity: "error",
      rule: "auth-session-bootstrap",
      file: "src",
      line: 1,
      message: "A persisted token exists without an observable backend me/session validation during auth bootstrap.",
    })
  }
  // WHY: Trustant sessions are opaque Redis records. Decoding a JWT in the
  // browser makes cached claims authoritative and bypasses the required
  // backend me/session lookup after a reload.
  if (browserTokenDecoding) {
    findings.push({
      severity: "error",
      rule: "auth-opaque-session-token",
      file: "src",
      line: 1,
      message: "Application session tokens must remain opaque in the browser and be validated by a backend me/session endpoint.",
    })
  }

  for (const source of files) {
    const visit = (node: ts.Node) => {
      if (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) {
        const tag = jsxTagName(node)
        if (["input", "select", "textarea"].includes(tag)) {
          const id = jsxStringAttribute(node, "id")
          const name = jsxStringAttribute(node, "name")
          if (!id || !name) {
            findings.push({
              severity: "warning",
              rule: "auth-control-semantics",
              file: fileLabel(canonical, source.fileName),
              line: lineOf(source, node),
              message: `${tag} should have stable id and name attributes for accessible browser verification.`,
            })
          }
        }
        if (tag === "label" && !jsxStringAttribute(node, "htmlFor")) {
          findings.push({
            severity: "warning",
            rule: "auth-label-semantics",
            file: fileLabel(canonical, source.fileName),
            line: lineOf(source, node),
            message: "Auth form labels should use htmlFor matching the control id.",
          })
        }
      }
      ts.forEachChild(node, visit)
    }
    ts.forEachChild(source, visit)
  }

  return findings
}

function typescriptDiagnostics(root: string): ReactFinding[] {
  const configPath = ts.findConfigFile(root, ts.sys.fileExists, "tsconfig.json")
  let fileNames: string[]
  let options: ts.CompilerOptions
  if (configPath) {
    const loaded = ts.readConfigFile(configPath, ts.sys.readFile)
    if (loaded.error) {
      return [{
        severity: "error",
        rule: "typescript-config",
        file: fileLabel(root, configPath),
        line: 1,
        message: ts.flattenDiagnosticMessageText(loaded.error.messageText, "\n"),
      }]
    }
    const parsed = ts.parseJsonConfigFileContent(loaded.config, ts.sys, root)
    fileNames = parsed.fileNames
    options = { ...parsed.options, noEmit: true }
  } else {
    fileNames = collectSourceFiles(root)
    options = {
      allowJs: true,
      checkJs: false,
      jsx: ts.JsxEmit.ReactJSX,
      module: ts.ModuleKind.ESNext,
      moduleResolution: ts.ModuleResolutionKind.Bundler,
      noEmit: true,
      skipLibCheck: true,
      target: ts.ScriptTarget.ESNext,
    }
  }
  const program = ts.createProgram({ rootNames: fileNames, options })
  return ts.getPreEmitDiagnostics(program)
    .filter((diagnostic) => diagnostic.category === ts.DiagnosticCategory.Error)
    .slice(0, 100)
    .map((diagnostic) => {
      const file = diagnostic.file
      const position = file && diagnostic.start !== undefined
        ? file.getLineAndCharacterOfPosition(diagnostic.start)
        : undefined
      return {
        severity: "error" as const,
        rule: `typescript-${diagnostic.code}`,
        file: file ? fileLabel(root, file.fileName) : "tsconfig.json",
        line: position ? position.line + 1 : 1,
        message: ts.flattenDiagnosticMessageText(diagnostic.messageText, "\n"),
      }
    })
}

export function validateReactProject(root: string): {
  ok: boolean
  inspection: ReactProjectInspection
  findings: ReactFinding[]
} {
  const inspection = inspectReactProject(root)
  const findings = [
    ...typescriptDiagnostics(inspection.root),
    ...validateReactRoutes(inspection.root),
    ...validateReactAuthFlow(inspection.root),
  ]
  return {
    ok: !findings.some((finding) => finding.severity === "error"),
    inspection,
    findings,
  }
}
