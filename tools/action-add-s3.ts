import { tool } from "@opencode-ai/plugin"
import { readFileSync, writeFileSync, existsSync } from "fs"
import { join } from "path"

export default tool({
  description: "Add S3 connection to an endpoint's context. Provides ctx.S3_CLIENT, ctx.S3_DATA, ctx.S3_WEB, ctx.S3_PUBLIC.",
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

    if (content.includes("init_s3")) {
      return `S3 is already configured in endpoint '${pkg}/${name}'.`
    }

    const injection = `
#--param S3_HOST "$S3_HOST"
#--param S3_PORT "$S3_PORT"
#--param S3_ACCESS_KEY "$S3_ACCESS_KEY"
#--param S3_SECRET_KEY "$S3_SECRET_KEY"
#--param S3_BUCKET_DATA "$S3_BUCKET_DATA"
#--param S3_BUCKET_STATIC "$S3_BUCKET_STATIC"
#--param S3_PUBLIC "$OPSDEV_S3"
import boto3
from botocore.client import Config
def init_s3(args, ctx):
  host = args.get("S3_HOST", os.getenv("S3_HOST"))
  port = args.get("S3_PORT", os.getenv("S3_PORT"))
  url = f"http://{host}:{port}"
  key = args.get("S3_ACCESS_KEY", os.getenv("S3_ACCESS_KEY"))
  sec = args.get("S3_SECRET_KEY", os.getenv("S3_SECRET_KEY"))
  cfg = Config(signature_version='s3v4')
  ctx.S3_CLIENT = boto3.client('s3', region_name='us-east-1', endpoint_url=url, aws_access_key_id=key, aws_secret_access_key=sec, config=cfg)
  ctx.S3_DATA = args.get("S3_BUCKET_DATA", os.getenv("S3_BUCKET_DATA"))
  ctx.S3_WEB = args.get("S3_BUCKET_STATIC", os.getenv("S3_BUCKET_STATIC"))
  ctx.S3_PUBLIC = args.get("S3_PUBLIC", os.getenv("OPSDEV_S3"))
builder.append(init_s3)`

    content = content.replace(marker, marker + injection)
    writeFileSync(mainPath, content)

    return `Added S3 to endpoint '${pkg}/${name}'.\nAvailable in context:\n  ctx.S3_CLIENT — the S3 client\n  ctx.S3_DATA — the S3 data bucket (private)\n  ctx.S3_WEB — the S3 web bucket (public)\n  ctx.S3_PUBLIC — the public URL to access S3`
  },
})
