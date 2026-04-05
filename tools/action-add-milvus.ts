import { tool } from "@opencode-ai/plugin"
import { readFileSync, writeFileSync, existsSync } from "fs"
import { join } from "path"

export default tool({
  description: "Add Milvus vector DB connection to an endpoint's context. Provides ctx.MILVUS.",
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

    if (content.includes("init_milvus")) {
      return `Milvus is already configured in endpoint '${pkg}/${name}'.`
    }

    const injection = `
#--param MILVUS_HOST "$MILVUS_HOST"
#--param MILVUS_PORT "$MILVUS_PORT"
#--param MILVUS_DB_NAME "$MILVUS_DB_NAME"
#--param MILVUS_TOKEN "$MILVUS_TOKEN"
from pymilvus import MilvusClient
def init_milvus(args, ctx):
  host = args.get('MILVUS_HOST', os.getenv('MILVUS_HOST'))
  port = args.get('MILVUS_PORT', os.getenv('MILVUS_PORT'))
  uri = f"http://{host}:{port}"
  token = args.get("MILVUS_TOKEN", os.getenv("MILVUS_TOKEN"))
  db_name = args.get("MILVUS_DB_NAME", os.getenv("MILVUS_DB_NAME"))
  ctx.MILVUS = MilvusClient(uri=uri, token=token, db_name=db_name)
builder.append(init_milvus)`

    content = content.replace(marker, marker + injection)
    writeFileSync(mainPath, content)

    return `Added Milvus to endpoint '${pkg}/${name}'.\nAvailable in context:\n  ctx.MILVUS — the MilvusClient instance`
  },
})
