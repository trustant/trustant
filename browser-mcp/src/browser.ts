import { mkdir, readFile, realpath, writeFile } from "node:fs/promises"
import { isAbsolute, join, relative, resolve, sep } from "node:path"
import { chromium, type Browser, type BrowserContext, type Locator, type Page } from "playwright"

export type BrowserMode = "development" | "deployed"
export type LocatorKind = "ref" | "role" | "text" | "label" | "placeholder" | "css"
export type Interaction = "click" | "fill" | "press" | "reload" | "back"

export interface BrowserControl {
  ref: string
  tag: string
  role: string
  name: string
  type: string
  placeholder: string
}

export interface BrowserAudioStatus {
  contexts: Array<{ state: string; sampleRate: number }>
  media: Array<{ tag: string; paused: boolean; muted: boolean; volume: number; currentTime: number }>
  active: boolean
}

export interface BrowserSnapshot {
  url: string
  title: string
  aria: string
  text: string
  console: string[]
  network: string[]
  controls: BrowserControl[]
  audio: BrowserAudioStatus
}

interface RuntimeWorkbench {
  app: string
  workspace: string
  developmentUrl: string
}

interface RuntimeManifest {
  version: number
  workbenches: RuntimeWorkbench[]
}

const MAX_DIAGNOSTICS = 100
const MAX_SNAPSHOT_TEXT = 12_000
const COMMON_ARIA_ROLES = new Set([
  "button", "checkbox", "combobox", "dialog", "heading", "link", "listbox",
  "menuitem", "option", "radio", "searchbox", "slider", "spinbutton", "switch",
  "tab", "textbox", "treeitem",
])

function normalizedPath(path: string | undefined): string {
  const value = (path || "/").trim()
  if (!value.startsWith("/") || value.startsWith("//")) {
    throw new Error("path must be an app-local path beginning with one '/'")
  }
  return value
}

function containsPath(root: string, target: string): boolean {
  const value = relative(root, target)
  return value === "" || (value !== ".." && !value.startsWith(`..${sep}`) && !isAbsolute(value))
}

async function canonicalPath(value: string): Promise<string> {
  const absolute = resolve(value)
  return realpath(absolute).catch(() => absolute)
}

export async function resolveManagedDevelopmentOrigin(
  runtimeConfig = process.env.TRUSTABLE_RUNTIME_CONFIG || "",
  directory = process.cwd(),
): Promise<string | undefined> {
  if (!runtimeConfig) return undefined

  const source = resolve(runtimeConfig)
  let manifest: RuntimeManifest
  try {
    manifest = JSON.parse(await readFile(source, "utf8")) as RuntimeManifest
  } catch (error) {
    throw new Error(`Trustable browser could not read runtime manifest ${source}: ${error instanceof Error ? error.message : String(error)}`)
  }
  // WHY: version 2 adds host-owned watcher evidence for Pi while preserving
  // the browser workbench/developmentUrl fields. Accept only the coordinated
  // contract so a partially upgraded runtime fails before using shared :5173.
  if (manifest.version !== 2 || !Array.isArray(manifest.workbenches)) {
    throw new Error(`Trustable browser received an invalid runtime manifest: ${source}`)
  }

  const current = await canonicalPath(directory)
  const matches: RuntimeWorkbench[] = []
  for (const workbench of manifest.workbenches) {
    if (!workbench || typeof workbench.workspace !== "string" || !isAbsolute(workbench.workspace)) {
      throw new Error(`Trustable browser runtime contains an invalid workbench path: ${String(workbench?.workspace || "")}`)
    }
    if (containsPath(await canonicalPath(workbench.workspace), current)) matches.push(workbench)
  }
  if (matches.length !== 1) {
    throw new Error(
      `Trustable browser blocked development verification: runtime manifest belongs to a different application than ${current}. Reopen the intended Trustable application; do not use another app page as verification evidence.`,
    )
  }

  let origin: URL
  try {
    origin = new URL(matches[0].developmentUrl)
  } catch {
    throw new Error(`Trustable browser runtime has an invalid development URL for ${matches[0].app}`)
  }
  if (origin.protocol !== "http:" || origin.hostname !== "localhost" || origin.port !== "5173") {
    throw new Error(`Trustable browser development URL must be http://localhost:5173 for ${matches[0].app}`)
  }
  return "http://localhost:5173/"
}

export function resolveBrowserTarget(
  mode: BrowserMode,
  path: string | undefined,
  externalOrigin?: string,
  developmentOrigin = "http://localhost:5173/",
): string {
  const appPath = normalizedPath(path)
  if (mode === "development") return new URL(appPath, developmentOrigin).toString()
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
    private readonly runtimeConfig = process.env.TRUSTABLE_RUNTIME_CONFIG || "",
    private readonly directory = process.cwd(),
    private readonly testDevelopmentOrigin = process.env.NODE_ENV === "test"
      ? process.env.TRUSTABLE_BROWSER_TEST_DEVELOPMENT_ORIGIN || ""
      : "",
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
    await this.context.addInitScript({ content: `(() => {
      Object.defineProperty(window, "__AGENTIC_REACT_CONFIG__", {
        configurable: false,
        enumerable: false,
        writable: false,
        value: { toolkit: { enabled: false } },
      });
      window.__trustableAudioContexts = [];
      const NativeAudioContext = window.AudioContext || window.webkitAudioContext;
      if (!NativeAudioContext) return;
      function TrackedAudioContext(...args) {
        const context = Reflect.construct(NativeAudioContext, args, new.target || NativeAudioContext);
        window.__trustableAudioContexts.push(context);
        return context;
      }
      Object.setPrototypeOf(TrackedAudioContext, NativeAudioContext);
      TrackedAudioContext.prototype = NativeAudioContext.prototype;
      Object.defineProperty(window, "AudioContext", { configurable: true, value: TrackedAudioContext });
      if (window.webkitAudioContext) {
        Object.defineProperty(window, "webkitAudioContext", { configurable: true, value: TrackedAudioContext });
      }
    })();` })
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
    const developmentOrigin = mode === "development"
      ? await resolveManagedDevelopmentOrigin(this.runtimeConfig, this.directory) || this.testDevelopmentOrigin || undefined
      : undefined
    const target = resolveBrowserTarget(mode, path, this.externalOrigin, developmentOrigin)
    this.consoleMessages.length = 0
    this.networkFailures.length = 0
    await page.goto(target, { waitUntil: "domcontentloaded", timeout: 60_000 })
    return this.snapshot()
  }

  private locator(page: Page, kind: LocatorKind, target: string, role?: string): Locator {
    switch (kind) {
      case "ref":
        if (!/^e\d+$/.test(target)) throw new Error("browser ref must match e<number> from the latest snapshot")
        return page.locator(`[data-trustable-browser-ref="${target}"]`)
      case "role":
        if (!role) throw new Error("role is required when locator kind is role")
        return target
          ? page.getByRole(role as Parameters<Page["getByRole"]>[0], { name: target, exact: false })
          : page.getByRole(role as Parameters<Page["getByRole"]>[0])
      case "text": return page.getByText(target, { exact: false })
      case "label": return page.getByLabel(target, { exact: false })
      case "placeholder": return page.getByPlaceholder(target, { exact: false })
      case "css": return page.locator(target)
    }
  }

  private locatorDescription(kind: LocatorKind, target: string, role?: string): string {
    if (kind === "role") return `role=${role || "(missing)"} name=${JSON.stringify(target)}`
    return `${kind}=${JSON.stringify(target)}`
  }

  private async selectedLocator(page: Page, options: {
    kind: LocatorKind
    target: string
    role?: string
    index?: number
  }): Promise<Locator> {
    const locator = this.locator(page, options.kind, options.target, options.role)
    const description = this.locatorDescription(options.kind, options.target, options.role)
    const count = await locator.count()
    if (count === 0) throw new Error(`element not found: ${description}`)
    if (options.index === undefined && count > 1) {
      throw new Error(`ambiguous locator: ${description} matched ${count} elements; specify zero-based index 0..${count - 1}`)
    }
    const index = options.index ?? 0
    if (!Number.isInteger(index) || index < 0) throw new Error("locator index must be a non-negative integer")
    if (index >= count) {
      throw new Error(`locator index out of range: ${description} matched ${count} elements; received index ${index}`)
    }
    return locator.nth(index)
  }

  async interact(action: Interaction, options: {
    kind?: LocatorKind
    target?: string
    role?: string
    value?: string
    index?: number
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
    if (!options.kind || (!options.target && !options.role)) throw new Error(`${action} requires locator kind and target`)
    let target = options.target || ""
    let role = options.role
    if (options.kind === "role" && !role) {
      if (COMMON_ARIA_ROLES.has(target.toLowerCase())) {
        role = target.toLowerCase()
        target = ""
      } else {
        role = action === "fill" || action === "press" ? "textbox" : "button"
      }
    }
    if (options.kind === "role" && role && target.toLowerCase() === role.toLowerCase()) {
      target = ""
    }
    const locator = await this.selectedLocator(page, {
      kind: options.kind,
      target,
      role,
      index: options.index,
    })

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
    const [title, aria, text, controls, audio] = await Promise.all([
      page.title(),
      body.ariaSnapshot({ timeout: 5_000 }).catch(() => "(aria snapshot unavailable)"),
      body.innerText({ timeout: 5_000 }).catch(() => "(body text unavailable)"),
      page.locator("a,button,input,select,textarea,[role]").evaluateAll((elements) => elements.filter((element) => {
        const node = element as HTMLElement
        if (node.closest('[data-agentic-react-toolkit="true"]')) return false
        if (node.closest('[data-agentic-react-tuning-modal="true"]')) return false
        const style = getComputedStyle(node)
        return !node.hidden && style.display !== "none" && style.visibility !== "hidden"
      }).map((element, index) => {
        const node = element as HTMLElement
        const input = element as HTMLInputElement
        const ref = `e${index}`
        node.dataset.trustableBrowserRef = ref
        const labels = "labels" in input && input.labels
          ? Array.from(input.labels).map((label) => label.textContent?.trim() || "").filter(Boolean).join(" ")
          : ""
        return {
          ref,
          tag: node.tagName.toLowerCase(),
          role: node.getAttribute("role") || "",
          name: node.getAttribute("aria-label") || labels || node.innerText?.trim() || node.getAttribute("title") || input.name || "",
          type: input.type || "",
          placeholder: input.placeholder || "",
        }
      })).catch(() => [] as BrowserControl[]),
      page.evaluate(() => {
        const target = window as typeof window & { __trustableAudioContexts?: AudioContext[] }
        const contexts = (target.__trustableAudioContexts || []).map((context) => ({
          state: context.state,
          sampleRate: context.sampleRate,
        }))
        const media = Array.from(document.querySelectorAll("audio,video")).map((element) => {
          const item = element as HTMLMediaElement
          return {
            tag: item.tagName.toLowerCase(),
            paused: item.paused,
            muted: item.muted,
            volume: item.volume,
            currentTime: item.currentTime,
          }
        })
        return {
          contexts,
          media,
          active: contexts.some((context) => context.state === "running") || media.some((item) => !item.paused && !item.muted && item.volume > 0),
        }
      }).catch(() => ({ contexts: [], media: [], active: false })),
    ])
    return {
      url: page.url(),
      title,
      aria: aria.slice(0, MAX_SNAPSHOT_TEXT),
      text: text.slice(0, MAX_SNAPSHOT_TEXT),
      console: [...this.consoleMessages],
      network: [...this.networkFailures],
      controls,
      audio,
    }
  }

  async diagnostics() {
    return {
      url: this.page?.url() || "about:blank",
      console: [...this.consoleMessages],
      network: [...this.networkFailures],
      audio: this.page
        ? await this.page.evaluate(() => {
            const target = window as typeof window & { __trustableAudioContexts?: AudioContext[] }
            const contexts = (target.__trustableAudioContexts || []).map((context) => ({ state: context.state, sampleRate: context.sampleRate }))
            const media = Array.from(document.querySelectorAll("audio,video")).map((element) => {
              const item = element as HTMLMediaElement
              return { tag: item.tagName.toLowerCase(), paused: item.paused, muted: item.muted, volume: item.volume, currentTime: item.currentTime }
            })
            return { contexts, media, active: contexts.some((context) => context.state === "running") || media.some((item) => !item.paused && !item.muted && item.volume > 0) }
          }).catch(() => ({ contexts: [], media: [], active: false }))
        : { contexts: [], media: [], active: false },
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
