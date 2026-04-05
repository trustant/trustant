import { tool } from "@opencode-ai/plugin"
import { readFileSync, writeFileSync, existsSync } from "fs"
import { join } from "path"

export default tool({
  description: "Add a secret to an endpoint's context. The secret must exist in .env.",
  args: {
    endpoint: tool.schema.string().describe("The endpoint path: 'name' (uses v1 package) or 'package/name'"),
    secret: tool.schema.string().describe("The secret name (e.g. MY_SECRET)"),
  },
  async execute(args) {
    const endpoint = args.endpoint.trim()
    const secret = args.secret.trim()
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

    // Check .env for the secret
    let envContent = ""
    if (existsSync(".env")) {
      envContent = readFileSync(".env", "utf-8")
    }
    if (!envContent.includes(`${secret}=`)) {
      return `Warning: secret '${secret}' not found in .env. Please add it to your environment before deploying.`
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

    if (content.includes(`#--param ${secret} `)) {
      return `Secret '${secret}' is already configured in endpoint '${pkg}/${name}'.`
    }

    const injection = `
#--param ${secret} "$${secret}"
builder.append(lambda args, ctx: setattr(ctx, '${secret}', args.get("${secret}", os.getenv("${secret}"))))`

    content = content.replace(marker, marker + injection)
    writeFileSync(mainPath, content)

    return `Added secret '${secret}' to endpoint '${pkg}/${name}'.\nAccess it via ctx.${secret} in your module.`
  },
})
