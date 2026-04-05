import { tool } from "@opencode-ai/plugin"
import { mkdirSync, writeFileSync, existsSync } from "fs"
import { join } from "path"

export default tool({
  description: "Create a new API endpoint. Creates the action folder with __main__.py and module file.",
  args: {
    endpoint: tool.schema.string().describe("The endpoint path: 'name' (uses v1 package) or 'package/name'"),
    public: tool.schema.boolean().optional().describe("Whether the action is public (API endpoint). Defaults to true. Set false for private actions (e.g. init package)."),
  },
  async execute(args) {
    const endpoint = args.endpoint.trim()
    const parts = endpoint.split("/")

    if (parts.length > 2) {
      return "Error: endpoint must have at most one '/' (format: 'name' or 'package/name')"
    }

    let pkg: string
    let name: string
    if (parts.length === 1) {
      pkg = "v1"
      name = parts[0]
    } else {
      pkg = parts[0]
      name = parts[1]
    }

    const validPattern = /^[a-zA-Z][a-zA-Z0-9-]*$/
    if (!validPattern.test(pkg)) {
      return `Error: package '${pkg}' must only contain letters, numbers, '-' and start with a letter`
    }
    if (!validPattern.test(name)) {
      return `Error: name '${name}' must only contain letters, numbers, '-' and start with a letter`
    }

    const isPublic = args.public !== false
    const moduleName = name.replace(/-/g, "_")
    const actionDir = join("packages", pkg, name)

    if (existsSync(actionDir)) {
      return `Error: endpoint already exists at ${actionDir}`
    }

    mkdirSync(actionDir, { recursive: true })

    const mainPy = `#--kind python:default
#--web ${isPublic}
# Note: this timeout is 5 minutes - 10 minutes is max allowed
#--timeout 300000
import types, os, ${moduleName}

builder = []
## build-context ##

def main(args):
  try:
    ctx = types.SimpleNamespace()
    for fn in builder: fn(args, ctx)
    return { "body": ${moduleName}.main(args, ctx=ctx) }
  except Exception as e:
    import traceback
    traceback.print_exc()
    return {
      "body": {"error": str(e) },
      "statusCode": 500
    }
`

    const modulePy = `def main(args, ctx=None):
  inp = args.get("input", "${moduleName}")
  out = inp
  return out
`

    writeFileSync(join(actionDir, "__main__.py"), mainPy)
    writeFileSync(join(actionDir, `${moduleName}.py`), modulePy)

    return `Created endpoint at /api/my/${pkg}/${name}\nFiles:\n  ${actionDir}/__main__.py\n  ${actionDir}/${moduleName}.py`
  },
})
