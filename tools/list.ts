import { tool } from "@opencode-ai/plugin"
import { Glob } from "bun"

export default tool({
  description: "List directory contents. Accepts glob patterns to filter results. Supports **.",
  args: {
    path: tool.schema.string().describe("The directory path to list"),
    pattern: tool.schema.string().optional().describe("Glob pattern to filter results (e.g. '**/*.ts')"),
  },
  async execute(args) {
    const dir = args.path || "."
    const pattern = args.pattern || "*"
    const glob = new Glob(pattern)
    const results: string[] = []
    for await (const file of glob.scan({ cwd: dir, dot: false })) {
      results.push(file)
    }
    results.sort()
    if (results.length === 0) return "No files found."
    return results.join("\n")
  },
})
