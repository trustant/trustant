# OpenCode browser MCP

Trustable provides a bounded Playwright browser to the OpenCode session through
the generated `browser` MCP entry. It is an app-development diagnostic tool,
not a general web browser.

## Targets

- `development` resolves only to `http://localhost:5173` inside the Trustable
  pod. Trustable owns the `ops ide devel` process; OpenCode must not start a
  second foreground Vite or development server.
- `deployed` resolves only to `<protocol>://vite.<configured-apihost>`. OpenCode
  may use it only after `ops ide deploy`, when browser/ingress verification is
  relevant.
- Callers provide only an app-local path. The MCP does not accept arbitrary
  origins, `OPS_APIHOST` replacements, or user-supplied infrastructure URLs.

The generated MCP environment contains only the derived external Vite origin
and an artifact directory under
`$WORKSPACE_DIR/.trustable/browser/<app>`. It must not copy credentials into
generated app `.env` files.

## Tool surface

The server exposes a deliberately small persistent browser surface:

- `browser_open`: open the development or deployed target;
- `browser_snapshot`: return URL, title, aria snapshot, visible text, console
  warnings/errors, and failed HTTP requests;
- `browser_interact`: one click, fill, key press, reload, or back operation;
- `browser_diagnostics`: return console/page/network failures;
- `browser_capture`: save a full-page screenshot plus structured JSON evidence;
- `browser_close`: close and discard the isolated browser context.

There is no arbitrary JavaScript evaluation tool. Snapshot and diagnostic
output is bounded so the browser does not recreate Issue98 context pressure.

## Image

The Trustable image installs the local `trustable-browser-mcp` package and its
pinned Playwright `1.56.1` Chromium runtime for both amd64 and arm64. The
browser runs headless and without the Chromium sandbox inside the container.
The image base hash includes the browser MCP source so local and CI builds do
not reuse a stale base image.

## Verification

`browser-mcp` unit/integration tests must verify URL restrictions, navigation,
accessibility snapshots, browser console capture, and failed-network capture.
The generated OpenCode and Claude MCP configs must both include the browser
server. A live pod test must confirm `browser_open` can inspect
`http://localhost:5173` for the currently launched app.

See [opencode-browser-flow.svg](opencode-browser-flow.svg).
