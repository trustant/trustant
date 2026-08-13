// screenshot-route.mjs — ask the app's MCP server which route the user is on.
//
// Driven by ../screenshot.sh; usage: node tests/screenshot-route.mjs <url> <metaId>
// Prints the route (e.g. "/#/dashboard") on stdout, or nothing when it cannot be
// determined — the caller then falls back to "/".
//
// The route comes from the meta tag the injected Vite reporter keeps in sync
// with window.location. It is read over MCP rather than by loading the page,
// because MCP reflects the tab the user actually has open; loading the page in
// our own browser would only ever report the URL we just asked for.
//
// No SDK: the transport is plain HTTP with SSE-framed responses, so fetch is
// enough and the script stays dependency-free.

const [base, metaId] = process.argv.slice(2);
if (!base || !metaId) process.exit(0);

const endpoint = new URL("/mcp", base).toString();
const HEADERS = { "Content-Type": "application/json", Accept: "application/json, text/event-stream" };

// Responses arrive as `event: message\ndata: {...}` frames.
const parseSSE = text => {
  for (const line of text.split("\n")) {
    if (!line.startsWith("data: ")) continue;
    try {
      return JSON.parse(line.slice(6));
    } catch {
      /* keep scanning: a frame may be split or non-JSON */
    }
  }
  return null;
};

const rpc = async (session, body) => {
  const headers = session ? { ...HEADERS, "mcp-session-id": session } : HEADERS;
  const response = await fetch(endpoint, { method: "POST", headers, body: JSON.stringify(body) });
  return { response, payload: parseSSE(await response.text()) };
};

// A short deadline throughout: this runs between a keypress and the shutter, so
// a hung or absent MCP server must not stall the recorder.
const withTimeout = promise => Promise.race([
  promise,
  new Promise(resolve => setTimeout(() => resolve(null), 5000)),
]);

try {
  const init = await withTimeout(rpc(null, {
    jsonrpc: "2.0", id: 1, method: "initialize",
    params: {
      protocolVersion: "2024-11-05",
      capabilities: {},
      clientInfo: { name: "trustable-screenshot", version: "1" },
    },
  }));
  const session = init?.response?.headers.get("mcp-session-id");
  if (!session) process.exit(0);

  await withTimeout(rpc(session, { jsonrpc: "2.0", method: "notifications/initialized" }));

  const result = await withTimeout(rpc(session, {
    jsonrpc: "2.0", id: 2, method: "tools/call",
    params: { name: "get-html-elements", arguments: { queries: [metaId], maxMatches: 5 } },
  }));

  // The tool answers with a JSON document inside a text content block, and each
  // match carries the element's markup as domPreview.
  const text = result?.payload?.result?.content?.find(c => c.type === "text")?.text;
  if (!text) process.exit(0);

  const previews = (JSON.parse(text).matches || []).map(m => m.domPreview || "");
  for (const preview of previews) {
    if (!preview.includes(metaId)) continue;
    const route = preview.match(/content="([^"]*)"/)?.[1];
    // Only absolute in-app paths: anything else is not a route we can capture.
    if (route && route.startsWith("/")) {
      console.log(route);
      break;
    }
  }
} catch {
  /* no MCP server, no reporter, or no open tab: the caller falls back to "/" */
}
process.exit(0);
