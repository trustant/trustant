# OpenCode browser MCP

> **PARTLY HISTORICAL.** The bounded browser MCP, its target restrictions and
> strict locator contract remain relevant and are exposed to Pi through the
> generated `.mcp.json`. The versioned `TRUSTABLE_RUNTIME_CONFIG` workbench
> binding is active again through issue #57; references below to OpenCode
> sessions and automatic evidence gates remain historical. See
> [pi.md](pi.md), [4-launch.md](4-launch.md), and
> [trustable-pi-runtime.md](trustable-pi-runtime.md) for current wiring.

Trustable provides a bounded Playwright browser to the OpenCode session through
the generated `browser` MCP entry. It is an app-development diagnostic tool,
not a general web browser.

For React/Vite workbenches, managed Pi must first run the always-generated
read-only `react` MCP aggregate `react_validate` after the latest frontend
mutation. Browser verification remains responsible for the visible runtime
flow only after deterministic route/auth/TypeScript findings are resolved.
The optional Agentic React MCP is selection context and does not satisfy this
gate.

The React auth validator must reject browser-side JWT interpretation, including
`jwtDecode`/equivalent helpers and manual base64 payload decoding, even when the
component is named `Session` and contains no literal login route. Trustable app
session tokens are opaque Redis keys; identity is recovered only through the
backend `me`/session endpoint.

The strict locator and observable-audio contract is exposed by browser MCP
version `0.3.0`.

## Targets

- `development` resolves only to `http://localhost:5173` inside the Trustable
  pod. Trustable owns the `ops ide devel` process; OpenCode must not start a
  second foreground Vite or development server.
  Before navigation, the MCP rereads `TRUSTABLE_RUNTIME_CONFIG` and requires
  its canonical working directory to belong to the manifest's single current
  workbench. If another application launch has replaced the runtime manifest,
  it rejects the page instead of treating that application's shared port 5173
  as verification evidence.
  The parser requires host-manifest version `2`; the added private watcher-log
  field is consumed by Pi's runtime tool, while Browser MCP continues to use
  only the canonical workbench and classified development URL. A partially
  upgraded version-1 runtime fails closed before navigation.
- The same selected workbench carries a separate browser-visible `browserUrl`.
  Pi receives both classified surfaces in its mandatory host context;
  development-mode Browser MCP navigation remains on local port 5173 and never
  substitutes the external URL.
- `deployed` resolves only to `<protocol>://vite.<configured-apihost>`. OpenCode
  may use it only after the Trustable-managed watcher has deployed the current
  sources, when browser/ingress verification is relevant. Pi must not start a
  concurrent manual deploy to satisfy this precondition.
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
  warnings/errors, failed HTTP requests, a bounded control list with snapshot
  refs (`e0`, `e1`, ...), and observable Web Audio/media state;
- `browser_interact`: one click, fill, key press, reload, or back operation.
  A `ref` from the latest snapshot is the preferred exact locator. Locator
  resolution is strict: zero matches reports the complete locator,
  multiple matches require an explicit zero-based `index`, and a supplied
  index selects with Playwright `nth(index)`;
  `kind=role` also accepts a bounded shorthand: a role name in `target`, or an
  accessible name whose role is inferred from the interaction (`textbox` for
  fill/press and `button` for click). Repeating the same value in `role` and
  `target` is treated as a role-only locator. Strict match counting still applies;
- `browser_diagnostics`: return console/page/network failures and audio state;
- `browser_capture`: save a full-page screenshot plus structured JSON evidence;
- `browser_close`: close and discard the isolated browser context.

There is no arbitrary JavaScript evaluation tool. Snapshot and diagnostic
output is bounded so the browser does not recreate Issue98 context pressure.
The isolated headless context locks `window.__AGENTIC_REACT_CONFIG__` with the
selection toolkit disabled before application scripts execute. Snapshot control
collection also excludes hidden controls and descendants of Agentic React
toolkit/tuning roots. This affects only Browser MCP QA: it does not disable the
toolkit in the user's normal development browser.

## Image

The Trustable image installs the local `trustable-browser-mcp` package and its
pinned Playwright `1.56.1` Chromium runtime for both amd64 and arm64. The
browser runs headless and without the Chromium sandbox inside the container.
The image base hash includes the browser MCP source so local and CI builds do
not reuse a stale base image.

The Lima `setup.sh` path must mirror this installation for the guest user:
package `browser-mcp/`, install `trustable-browser-mcp` and `tsx` under
`~/.local`, and install the pinned Chromium runtime under the user's Playwright
cache. App launch must never advertise the browser MCP when its executable is
absent from the development PATH.

## Verification

`browser-mcp` unit/integration tests must verify URL restrictions, navigation,
accessibility snapshots, browser console capture, failed-network capture, and
real form submission when two password inputs share the same placeholder. The
duplicate locator must fail without `index`; indices `0` and `1` must fill the
two distinct inputs before the registration request is submitted.
The same form must also be fillable through two distinct snapshot refs. An
audio test must create and resume an `AudioContext` after a real click and prove
that the next snapshot reports it as running and active.
An Agentic React test must prove that page code cannot re-enable the toolkit in
the isolated context, that toolkit controls are absent from text, ARIA, and
control evidence, and that normal application controls remain available.
The generated OpenCode and Claude MCP configs must both include the browser
server. A live pod test must confirm `browser_open` can inspect
`http://localhost:5173` for the currently launched app. Unit tests must also
prove that a runtime manifest for another workbench blocks development
navigation before the shared Vite target is used.
Tests use an explicit test-only development origin on an ephemeral port so they
do not stop or collide with the managed application on port 5173; production
target resolution remains fixed to the runtime manifest and localhost port 5173.

See [opencode-browser-flow.svg](opencode-browser-flow.svg).
