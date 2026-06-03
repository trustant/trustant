import { tool } from "@opencode-ai/plugin"

const DEFAULT_TIMEOUT_SECONDS = 120
const MAX_TIMEOUT_SECONDS = 600

function timeoutSeconds(value: unknown) {
  if (typeof value !== "number" || !Number.isFinite(value) || value <= 0) {
    return DEFAULT_TIMEOUT_SECONDS
  }
  return Math.min(Math.ceil(value), MAX_TIMEOUT_SECONDS)
}

function hasTimeoutWrapper(command: string) {
  return /\btimeout\s+(-[a-zA-Z]+\s+)*\d+/.test(command)
}

function looksLongRunning(command: string) {
  const normalized = command.toLowerCase()
  return [
    /\bnpm\s+run\s+(dev|watch)\b/,
    /\bnpm\s+(start|run-script\s+(dev|watch))\b/,
    /\bbun\s+run\s+(dev|watch)\b/,
    /\byarn\s+(dev|watch|start)\b/,
    /\bpnpm\s+(dev|watch|start)\b/,
    /\bops\s+ide\s+devel\b/,
    /(^|[;&|]\s*)vite(\s|$)/,
    /\btail\s+-f\b/,
    /\bwhile\s+true\b/,
    /\bsleep\s+infinity\b/,
  ].some((pattern) => pattern.test(normalized))
}

export default tool({
  description: "Execute bounded bash commands using /bin/bash -c. Long-running dev servers and watchers are blocked unless wrapped in timeout.",
  args: {
    command: tool.schema.string().describe("The bash command to execute"),
    timeoutSeconds: tool.schema.number().optional().describe("Maximum execution time in seconds. Defaults to 120 and caps at 600."),
  },
  async execute(args) {
    const command = args.command.trim()
    if (!command) return "Error: empty command"

    if (looksLongRunning(command) && !hasTimeoutWrapper(command)) {
      return [
        "Blocked long-running foreground command.",
        "Trustable already manages the dev server for this workbench.",
        "Use bounded checks such as `npm run build`, `curl http://localhost:5173`, or wrap the command with `timeout <seconds> ...`.",
      ].join("\n")
    }

    const timeout = timeoutSeconds(args.timeoutSeconds)
    const proc = Bun.spawn([
      "timeout",
      "--kill-after=2s",
      `${timeout}s`,
      "/bin/bash",
      "-lc",
      command,
    ], {
      stdout: "pipe",
      stderr: "pipe",
    })
    const stdoutPromise = new Response(proc.stdout).text()
    const stderrPromise = new Response(proc.stderr).text()
    const exitCode = await proc.exited
    let result = ""
    const stdout = await stdoutPromise
    const stderr = await stderrPromise
    if (stdout) result += stdout
    if (stderr) result += `\nSTDERR:\n${stderr}`
    if (exitCode === 124) result += `\nCommand exited with timeout status 124; outer limit is ${timeout}s.`
    if (exitCode !== 0) result += `\nExit code: ${exitCode}`
    return result || "(no output)"
  },
})
