import { tool } from "@opencode-ai/plugin"

export default tool({
  description: "Execute bash commands using /bin/bash -c",
  args: {
    command: tool.schema.string().describe("The bash command to execute"),
  },
  async execute(args) {
    const proc = Bun.spawn(["/bin/bash", "-c", args.command], {
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
