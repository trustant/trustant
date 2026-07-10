import { mkdir, writeFile } from "node:fs/promises"
import { join } from "node:path"
import { chromium, type Browser, type BrowserContext, type Locator, type Page } from "playwright"

export type BrowserMode = "development" | "deployed"
export type LocatorKind = "role" | "text" | "label" | "placeholder" | "css"
export type Interaction = "click" | "fill" | "press" | "reload" | "back"

export interface BrowserSnapshot {
  url: string
  title: string
  aria: string
  text: string
  console: string[]
  network: string[]
}

const MAX_DIAGNOSTICS = 100
const MAX_SNAPSHOT_TEXT = 12_000

function normalizedPath(path: string | undefined): string {
  const value = (path || "/").trim()
  if (!value.startsWith("/") || value.startsWith("//")) {
    throw new Error("path must be an app-local path beginning with one '/'")
  }
  return value
}

export function resolveBrowserTarget(mode: BrowserMode, path: string | undefined, externalOrigin?: string): string {
  const appPath = normalizedPath(path)
  if (mode === "development") return new URL(appPath, "http://localhost:5173").toString()
  if (!externalOrigin) throw new Error("deployed browser target is not configured")

  const origin = new URL(externalOrigin)
  if (!["http:", "https:"].includes(origin.protocol) || !origin.hostname.startsWith("vite.")) {
    throw new Error("deployed browser target must be the configured vite.<domain> origin")
  }
  return new URL(appPath, `${origin.protocol}//${origin.host}`).toString()
}

export class TrustableBrowser {
  private browser?: Browser
  private context?: BrowserContext
  private page?: Page
  private readonly consoleMessages: string[] = []
  private readonly networkFailures: string[] = []

  constructor(
    private readonly externalOrigin = process.env.TRUSTABLE_BROWSER_EXTERNAL_ORIGIN || "",
    private readonly artifactDir = process.env.TRUSTABLE_BROWSER_ARTIFACT_DIR || "/tmp/trustable-browser",
  ) {}

  private remember(target: string[], value: string) {
    target.push(value)
    if (target.length > MAX_DIAGNOSTICS) target.splice(0, target.length - MAX_DIAGNOSTICS)
  }

  private async ensurePage(): Promise<Page> {
    if (this.page && !this.page.isClosed()) return this.page
    this.browser = await chromium.launch({
      headless: true,
      args: ["--no-sandbox", "--disable-dev-shm-usage"],
    })
    this.context = await this.browser.newContext({
      viewport: { width: 1440, height: 900 },
      ignoreHTTPSErrors: false,
    })
    this.page = await this.context.newPage()
    this.page.on("console", (message) => {
      if (["warning", "error"].includes(message.type())) {
        this.remember(this.consoleMessages, `${message.type().toUpperCase()} ${message.text()}`)
      }
    })
    this.page.on("pageerror", (error) => this.remember(this.consoleMessages, `PAGEERROR ${error.message}`))
    this.page.on("requestfailed", (request) => {
      this.remember(this.networkFailures, `FAILED ${request.method()} ${request.url()} ${request.failure()?.errorText || ""}`)
    })
    this.page.on("response", (response) => {
      if (response.status() >= 400) {
        this.remember(this.networkFailures, `HTTP ${response.status()} ${response.request().method()} ${response.url()}`)
      }
    })
    return this.page
  }

  async open(mode: BrowserMode, path?: string): Promise<BrowserSnapshot> {
    const page = await this.ensurePage()
    const target = resolveBrowserTarget(mode, path, this.externalOrigin)
    this.consoleMessages.length = 0
    this.networkFailures.length = 0
    await page.goto(target, { waitUntil: "domcontentloaded", timeout: 60_000 })
    return this.snapshot()
  }

  private locator(page: Page, kind: LocatorKind, target: string, role?: string): Locator {
    switch (kind) {
      case "role":
        if (!role) throw new Error("role is required when locator kind is role")
        return page.getByRole(role as Parameters<Page["getByRole"]>[0], { name: target, exact: false }).first()
      case "text": return page.getByText(target, { exact: false }).first()
      case "label": return page.getByLabel(target, { exact: false }).first()
      case "placeholder": return page.getByPlaceholder(target, { exact: false }).first()
      case "css": return page.locator(target).first()
    }
  }

  async interact(action: Interaction, options: {
    kind?: LocatorKind
    target?: string
    role?: string
    value?: string
  }): Promise<BrowserSnapshot> {
    const page = await this.ensurePage()
    if (action === "reload") {
      await page.reload({ waitUntil: "domcontentloaded", timeout: 60_000 })
      return this.snapshot()
    }
    if (action === "back") {
      await page.goBack({ waitUntil: "domcontentloaded", timeout: 60_000 })
      return this.snapshot()
    }
    if (!options.kind || !options.target) throw new Error(`${action} requires locator kind and target`)
    const locator = this.locator(page, options.kind, options.target, options.role)
    if (await locator.count() === 0) throw new Error(`element not found: ${options.kind}=${options.target}`)

    if (action === "click") await locator.click({ timeout: 10_000 })
    if (action === "fill") {
      if (options.value === undefined) throw new Error("fill requires value")
      await locator.fill(options.value, { timeout: 10_000 })
    }
    if (action === "press") {
      if (!options.value) throw new Error("press requires a key value")
      await locator.press(options.value, { timeout: 10_000 })
    }
    await page.waitForTimeout(200)
    return this.snapshot()
  }

  async snapshot(): Promise<BrowserSnapshot> {
    const page = await this.ensurePage()
    const body = page.locator("body")
    const [title, aria, text] = await Promise.all([
      page.title(),
      body.ariaSnapshot({ timeout: 5_000 }).catch(() => "(aria snapshot unavailable)"),
      body.innerText({ timeout: 5_000 }).catch(() => "(body text unavailable)"),
    ])
    return {
      url: page.url(),
      title,
      aria: aria.slice(0, MAX_SNAPSHOT_TEXT),
      text: text.slice(0, MAX_SNAPSHOT_TEXT),
      console: [...this.consoleMessages],
      network: [...this.networkFailures],
    }
  }

  diagnostics() {
    return {
      url: this.page?.url() || "about:blank",
      console: [...this.consoleMessages],
      network: [...this.networkFailures],
    }
  }

  async capture(name = "page"): Promise<string> {
    const page = await this.ensurePage()
    const safe = name.replace(/[^a-zA-Z0-9_.-]/g, "_") || "page"
    await mkdir(this.artifactDir, { recursive: true })
    const timestamp = new Date().toISOString().replace(/[:.]/g, "-")
    const screenshotPath = join(this.artifactDir, `${safe}-${timestamp}.png`)
    const snapshotPath = join(this.artifactDir, `${safe}-${timestamp}.json`)
    await page.screenshot({ path: screenshotPath, fullPage: true })
    await writeFile(snapshotPath, `${JSON.stringify(await this.snapshot(), null, 2)}\n`, { mode: 0o600 })
    return `${screenshotPath}\n${snapshotPath}`
  }

  async close() {
    await this.context?.close().catch(() => {})
    await this.browser?.close().catch(() => {})
    this.page = undefined
    this.context = undefined
    this.browser = undefined
  }
}
