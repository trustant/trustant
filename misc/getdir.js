function base64Decode(value) {
  const binary = atob(value.replace(/-/g, "+").replace(/_/g, "/"))
  const bytes = Uint8Array.from(binary, (c) => c.charCodeAt(0))
  return new TextDecoder().decode(bytes)
}

const url = process.argv[2]
if (!url) {
  console.error("Usage: node getdir.js <url>")
  process.exit(1)
}

const encoded = new URL(url).pathname.split("/")[1]
console.log(base64Decode(encoded))
