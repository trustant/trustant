import { tool } from "@opencode-ai/plugin"

export default tool({
  description: "Invoke an API action. Executes `ops action invoke` with the given endpoint and key=value parameters.",
  args: {
    endpoint: tool.schema.string().describe("The endpoint path: 'package/action'"),
    params: tool.schema.array(tool.schema.string()).optional().describe("Key=value pairs to pass as parameters (e.g. ['key1=value1', 'key2=value2'])"),
  },
  async execute(args) {
    const endpoint = args.endpoint.trim()
    const paramArgs: string[] = []
    if (args.params) {
      for (const kv of args.params) {
        const eqIdx = kv.indexOf("=")
        if (eqIdx === -1) {
          return `Error: invalid parameter '${kv}', expected key=value format`
        }
        const key = kv.substring(0, eqIdx)
        const value = kv.substring(eqIdx + 1)
        paramArgs.push("-p", key, value)
      }
    }

    const cmd = ["ops", "action", "invoke", endpoint, ...paramArgs]
    const proc = Bun.spawn(cmd, {
      stdout: "pipe",
      stderr: "pipe",
    })
    const stdout = await new Response(proc.stdout).text()
    const stderr = await new Response(proc.stderr).text()
    const exitCode = await proc.exited

    let result = ""
    if (stdout) result += stdout
    if (stderr) result += `\nSTDERR:\n${stderr}`
    if (exitCode !== 0) result += `\nExit code: ${exitCode}`
    return result || "(no output)"
  },
})
