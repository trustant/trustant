import { tool } from "@opencode-ai/plugin"
import { readFileSync, writeFileSync, existsSync } from "fs"
import { join } from "path"

export default tool({
  description: "Add Redis connection to an endpoint's context. Provides ctx.REDIS and ctx.REDIS_PREFIX.",
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

    if (content.includes("init_redis")) {
      return `Redis is already configured in endpoint '${pkg}/${name}'.`
    }

    const injection = `
#--param REDIS_URL "$REDIS_URL"
#--param REDIS_PREFIX "$REDIS_PREFIX"
import redis
def init_redis(args, ctx):
  ctx.REDIS = redis.from_url(args.get("REDIS_URL", os.getenv("REDIS_URL")))
  ctx.REDIS_PREFIX = args.get("REDIS_PREFIX", os.getenv("REDIS_PREFIX"))
builder.append(init_redis)`

    content = content.replace(marker, marker + injection)
    writeFileSync(mainPath, content)

    return `Added Redis to endpoint '${pkg}/${name}'.\nAvailable in context:\n  ctx.REDIS — the Redis client\n  ctx.REDIS_PREFIX — the key prefix`
  },
})
