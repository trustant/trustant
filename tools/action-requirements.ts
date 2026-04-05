import { tool } from "@opencode-ai/plugin"
import { readFileSync, writeFileSync, existsSync } from "fs"
import { join } from "path"

const PREINSTALLED = [
  "requests",
  "ollama",
  "openai",
  "pymilvus",
  "redis",
  "pyyaml",
  "boto3",
  "psycopg",
  "beautifulsoup4",
  "pillow",
  "nltk",
  "httplib2",
  "kafka_python",
  "python-dateutil",
  "scrapy",
  "simplejson",
  "twisted",
  "netifaces",
  "pymongo",
  "minio",
  "langdetect",
  "plotly",
  "joblib",
  "lightgbm",
  "feedparser",
  "numpy",
  "scikit-learn",
  "langchain",
  "langchain-ollama",
  "langchain-openai",
  "bcrypt",
]

export default tool({
  description: "Add a library to an endpoint's requirements.txt. Skips if the library is preinstalled.",
  args: {
    endpoint: tool.schema.string().describe("The endpoint path: 'name' (uses v1 package) or 'package/name'"),
    library: tool.schema.string().describe("The Python library name to add"),
  },
  async execute(args) {
    const endpoint = args.endpoint.trim()
    const library = args.library.trim()
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

    const actionDir = join("packages", pkg, name)
    if (!existsSync(actionDir)) {
      return `Error: endpoint not found at ${actionDir}`
    }

    // Check if preinstalled
    const normalizedLib = library.toLowerCase().replace(/-/g, "_")
    const isPreinstalled = PREINSTALLED.some(
      (p) => p.toLowerCase().replace(/-/g, "_") === normalizedLib
    )
    if (isPreinstalled) {
      return `Library '${library}' is preinstalled and available. No action needed.`
    }

    const reqPath = join(actionDir, "requirements.txt")
    let existing = ""
    if (existsSync(reqPath)) {
      existing = readFileSync(reqPath, "utf-8")
    }

    // Check if already in requirements
    const lines = existing.split("\n").map((l) => l.trim()).filter(Boolean)
    const alreadyAdded = lines.some(
      (l) => l.toLowerCase().replace(/-/g, "_") === normalizedLib
    )
    if (alreadyAdded) {
      return `Library '${library}' is already in requirements.txt.`
    }

    lines.push(library)
    writeFileSync(reqPath, lines.join("\n") + "\n")

    return `Added '${library}' to ${reqPath}`
  },
})
