# OpenCode browser MCP

Trustable provides a bounded Playwright browser to the OpenCode session through
the generated `browser` MCP entry. It is an app-development diagnostic tool,
not a general web browser.

The strict indexed locator contract is exposed by browser MCP version `0.2.0`.

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
- `browser_interact`: one click, fill, key press, reload, or back operation.
  Locator resolution is strict: zero matches reports the complete locator,
  multiple matches require an explicit zero-based `index`, and a supplied
  index selects with Playwright `nth(index)`;
  `kind=role` also accepts a bounded shorthand: a role name in `target`, or an
  accessible name whose role is inferred from the interaction (`textbox` for
  fill/press and `button` for click). Repeating the same value in `role` and
  `target` is treated as a role-only locator. Strict match counting still applies;
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
accessibility snapshots, browser console capture, failed-network capture, and
real form submission when two password inputs share the same placeholder. The
duplicate locator must fail without `index`; indices `0` and `1` must fill the
two distinct inputs before the registration request is submitted.
The generated OpenCode and Claude MCP configs must both include the browser
server. A live pod test must confirm `browser_open` can inspect
`http://localhost:5173` for the currently launched app.

See [opencode-browser-flow.svg](opencode-browser-flow.svg).
