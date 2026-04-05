import { tool } from "@opencode-ai/plugin"
import { readFileSync, writeFileSync, existsSync } from "fs"
import { join } from "path"

export default tool({
  description: "Add PostgreSQL connection to an endpoint's context. Provides ctx.POSTGRESQL.",
  args: {
    endpoint: tool.schema.string().describe("The endpoint path: 'name' (uses v1 package) or 'package/name'"),
  },
  async execute(args) {
    const endpoint = args.endpoint.trim()
    const parts = endpoint.split("/")

    let pkg: string
    let name: string
    if (parts.length === 1) {
      pkg = "v1"
      name = parts[0]
    } else {
      pkg = parts[0]
      name = parts[1]
    }

    const mainPath = join("packages", pkg, name, "__main__.py")
    if (!existsSync(mainPath)) {
      return `Error: endpoint not found at ${mainPath}`
    }

    let content = readFileSync(mainPath, "utf-8")
    const marker = "## build-context ##"
    if (!content.includes(marker)) {
      return `Error: marker '${marker}' not found in ${mainPath}`
    }

    if (content.includes("init_postgresql")) {
      return `PostgreSQL is already configured in endpoint '${pkg}/${name}'.`
    }

    const injection = `
#--param POSTGRES_URL "$POSTGRES_URL"
import psycopg
def init_postgresql(args, ctx):
  dburl = args.get("POSTGRES_URL", os.getenv("POSTGRES_URL"))
  ctx.POSTGRESQL = psycopg.connect(dburl)
builder.append(init_postgresql)`

    content = content.replace(marker, marker + injection)
    writeFileSync(mainPath, content)

    return `Added PostgreSQL to endpoint '${pkg}/${name}'.\nAvailable in context:\n  ctx.POSTGRESQL — the psycopg connection`
  },
})
