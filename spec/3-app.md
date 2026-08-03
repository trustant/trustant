This file describe the code for the application page, put the code in `app.html`

# check the version

Invoke the version api at the end of the page and if it expired show a page with only a centered message saying "This version expired. Please Update. For info email: info@nuvolaris.io"

# Application

This page shows the current application
reading it the cookie LEFT, RIGHT, URLDIR, NAME and DEVICE

It shows a full page, with a top bar with 10% high.

In the bar, aligned to the left:

- the trustable logo (80% height)
- the app name in bold
- the **Credits box** and **Top-up** button (only when `provider == "trustable"`, see "Credits" below)
- the **"Config" pulldown** (purple, gear icon + chevron-down) — see "Config Pulldown" below
- the **"Utils" pulldown** (orange, chevron-down icon), immediately to the right of Config — see "Utils Pulldown" below
- the **"Sessions" pulldown** (history icon and persistent-session count) —
  see "Sessions Pulldown" below

Aligned to the right:

- a git status indicator (dot + text)
- the button "Commit" (blue, checkmark icon, disabled when no changes)
- the **device preview toggle** (three icon-only segmented buttons: desktop, tablet, phone) — see "Device Preview" below
- the button "Route: /" (teal, home icon) — displays the current value of the ROUTE cookie (defaults to "/"); opens the combined Route & Query popup (see "Query & Route" below)
- the button "Back" (gray, chevron left icon)

Toolbar buttons use inline SVG icons that inherit the current button text color.

# Config Pulldown

The **Terminal** button (gray, terminal icon) sits on the left side of the toolbar, immediately before the Config button — see "Terminal" below.

The **Config** pulldown groups the three configuration entries (Env, Skills, Memory) under a single button on the left side of the toolbar, immediately after the Credits box (or after the app name when the Credits box is not shown).

The button shows a gear icon, the label "Config", and a chevron-down icon. Clicking it toggles a dropdown containing, in this order:

1. **Env** (gear icon) — opens the editable environment-variables modal described in "Env (Editable)".
2. **Skills** (book icon) — opens the Skills modal described in "Skills" ([7-skills.md](7-skills.md)).
3. **Memory** (brain icon) — opens the AGENTS.md editor described in "Memory".

The pulldown closes after an item is selected, when the user clicks outside, or when the Escape key is pressed.

# Sessions Pulldown

The **Sessions** pulldown fetches `GET /api/opencode/sessions/<name>` on page
load, whenever it opens, and every 10 seconds. It lists up to 20 persistent root
OpenCode sessions for the current canonical workbench, newest first, with title
and update time. The active session is visibly marked.

The first menu action is **New session**. It sends
`POST /api/opencode/sessions/<name>`, stores the returned ID in the `SESSIONID`
cookie, and opens that session in the left iframe. While the request is in
progress the action is disabled; a creation error remains visible in the menu.

Selecting a session updates the `SESSIONID` cookie and reloads only the left
iframe at `<LEFT>/<B64DIR>/session/<session-id>`. It must not launch a new
OpenCode process, replace the application workbench, or modify the right Vite
iframe. An empty history and a fetch error have distinct, readable states.

See [opencode-session-history-flow.svg](opencode-session-history-flow.svg).

# Utils Pulldown

Replace the previous standalone "Revert", "Redeploy", and "Upload" toolbar buttons with a single **Utils** pulldown menu. It sits on the left side of the toolbar, immediately to the right of the Config pulldown, so the two pulldowns are grouped together. Its dropdown is left-anchored like the Config one.

The button shows the label "Utils" and a chevron-down icon. Clicking it toggles a dropdown containing, in this order:

1. **Revert** (orange undo-arrow icon) — disabled when there are no uncommitted changes (same condition as Commit).
2. **Redeploy** (indigo rocket icon) — always enabled.
3. **Clean** (sparkles icon) — always enabled; see "Clean" below.
4. **Debug** (blue terminal/log icon) — always enabled.
5. **Files** (document icon) — always enabled; opens the read-only file viewer described in "Files (Read-only)".
6. **Upload** (green upload-arrow icon) — always enabled.

Each item triggers the same behavior documented in the "Revert", "Redeploy", "Clean", "Debug", "Files", and "Upload" sections of this file. The pulldown closes after an item is selected, when the user clicks outside, or when the Escape key is pressed.

In the body there are two iframes, 50% width and 90% height (full page except for the top bar), resizable horizontally. The right iframe sits inside a preview pane that can constrain it to a device viewport — see "Device Preview" below.

The workbench chrome, menus, modals, and toolbar controls use the shared
Nuvolaris-style Trustable visual system defined in
[1-applist.md](1-applist.md) under "Shared Trustable visual system" and loaded
from `web/trustable-ui.css`. The top bar uses the shared near-paper surface,
thin border, Work Sans typography, compact app identity, status pill, restrained
buttons, and shared dropdown/menu styling. Dialogs, the environment editor and
read-only skills view, memory editor shell, top-up iframe shell, route/query popup, and
git/upload/revert/skills result boxes use the same modal, input, table, button,
and code-output primitives. Preserve the full-height two-iframe layout and all
existing ids, cookies, launch URLs, git/status polling, route/query controls,
credits/top-up behavior, and utility actions. Restyling should make the toolbar
more professional and compact without changing the command model.

Get the URLDIR from the cookie then show the iframe. URLDIR must be the raw or
URL-encoded absolute application directory. For compatibility, if URLDIR is a
base64-url-safe path or is missing while B64DIR is present, decode it first.
Before opening opencode, always regenerate B64DIR from the raw absolute path:

They will show:
- to the left: `<LEFT>/<B64DIR>/session/<SESSIONID>` when `SESSIONID` is
  available, otherwise `<LEFT>/<B64DIR>/session`
- to the right: `<RIGHT><ROUTE>#<ROUTE>` where `<ROUTE>` is the value of the ROUTE cookie (defaults to "/")

Write in console.log the values of the cookies B64DIR and URLDIR

# Git Status

On page load and every 10 seconds, invoke `GET /api/git/status/<name>` where `<name>` is read from the NAME cookie.

The API returns:
```
{ "changed": N, "added": N, "deleted": N, "clean": true/false }
```

Display in the toolbar a compact status indicator:
- If clean is true, show a green dot and "No changes"
- If clean is false, show an orange dot and a summary like "3 changed, 1 added, 2 deleted"

# Save

Add a Save button to the toolbar.
If the git status is clean (no changes), the button is disabled.
If you click on it:
- Ask for confirmation: "Save all changes to workspace?"
If the user confirms invoke

POST /api/git/save with

`{
    "name": <current app>
}`

Show a waiting indicator until the API responds.
Return the result if ok or fail and a Continue button.
After a successful save, refresh the git status indicator.

# Back

Clicking on the button back will:
- First, invoke `GET /api/git/status/<name>` to check for uncommitted changes
- If the status is not clean, show a warning modal:
  "You have unsaved changes. If you go back, changes will be lost."
  with buttons "Go back anyway" and "Cancel"
- If the user clicks "Cancel", do nothing (stay on page)
- If the user clicks "Go back anyway" or the status was clean:
  - remove the cookie B64DIR
  - invoke the DELETE /api/launch to stop running subprocess
  - navigate to applist.html

# Env (Editable)

Add an Env button to the toolbar. Clicking it opens a modal for editing the
app's environment variables. It is a full editor, not a viewer: it is where the
missing-variable flow lands, so the user can fix a blocked launch without
leaving the app.

The modal fetches `GET /api/appconfig/<name>` and renders a table with columns:
VARIABLE, Development, Production, Actions.

- Development and Production cells are `<input>`s, except rows flagged
  `readonly` (`OPS_USER`, `OPS_PASSWORD`, `OPS_APIHOST`, `OPS_REPO`,
  `OPS_SKILLS`) whose Development value stays a static label — the server
  regenerates it on every launch, so an edit would be discarded. Rows flagged
  `readonly` or `fixed` also keep a static name and no Remove button.
- **Add Variable** appends a blank row; each editable row has a **Remove**
  button.
- **Save** posts the whole `vars` array to `POST /api/appconfig/<name>`. No new
  API is introduced.
- Closing with unsaved changes asks for confirmation, matching
  [appconfig.html](../web/appconfig.html).

## Missing variables

When the modal is opened for missing variables, empty Development inputs get a
red border and a banner reads "N required variables have no value. Fill them in
to launch this app." (singular "1 required variable has no value"). The flag
clears as each value is filled in.

The blocked-launch flow itself does **not** live here — it belongs to the page
that owns the launch. [applist.html](../web/applist.html) renders the same
editor in place of the launch modal and resumes the launch on Save, in one
step (see [4-launch.md](4-launch.md)). This modal is the ordinary per-app
editor, reachable any time from the Config pulldown.

## Shared implementation

The render/edit/import/save logic lives in
[web/js/envtable.js](../web/js/envtable.js) and is shared with
[appconfig.html](../web/appconfig.html) and
[applist.html](../web/applist.html), so the editors cannot drift apart — which
is exactly how this modal previously ended up read-only while the app-list
editor was editable. The module owns the table; each host page supplies its own
element ids and buttons (the full page additionally offers `.env` /
`.env.production` file import; the app list adds the missing-variable banner and
resumes the launch on Save).

The modal can be closed with the X button, Escape key, or clicking the backdrop.

# Files (Read-only)

The **Files** entry in the Utils pulldown opens a read-only viewer for the files
of the current application, so the user can inspect what the assistant generated
without leaving Trustable. It never writes: there is no edit, create, rename, or
delete action.

The modal has a file tree on the left (fetched from `GET /api/files/<name>`,
directories collapsible) and a content pane on the right (fetched from
`GET /api/files/<name>?path=<relpath>`), plus a Copy button for the raw content
and a Refresh control for the tree.

File content is always inserted as text, never as HTML — markdown is shown as
its source. See [11-files.md](11-files.md) for the endpoint contract, the path
containment rules, the size and binary limits, and the reasoning behind not
rendering assistant-authored markup.

The modal can be closed with the X button, the Close button, Escape, or clicking
the backdrop.

# Memory

Add a Memory Button with Brain icon to the toolbar.

Clicking it will show the text editor codejar allowing to edit the file AGENTS.md with a popup centered and the buttons save and cancel

Create the file AGENTS.md if it is not there and add to git when creating.

# Revert

The **Revert** entry in the Utils pulldown is disabled when there are no uncommitted changes (same condition as Commit).

When selected, ask for confirmation: "Are you sure you want to revert all uncommitted changes?".

If confirmed, execute `POST /api/git` with body:

```
{
  "name": <current app>,
  "cmd":  "checkout ."
}
```

The backend handles `checkout .` by also running `git clean -fd` to remove untracked files.

Show ok or error result. After a successful revert, refresh the git status.

# Upload

The **Upload** entry in the Utils pulldown lets the user select a file to upload and then invokes `POST /api/upload`, passing the current `<name>` and the uploaded file.

It expects the backend to return a full path name; show the uploaded file in a popup allowing the user to copy the filename to the clipboard before closing.

# Query & Route

The Route button in the toolbar displays "Route: <route>" where `<route>` is the current value of the ROUTE cookie (defaults to "/").

When clicked, show a single combined popup titled "Route & Query" that lets the user edit both the route and the query string in one place:

- A text input labelled **Route**, pre-filled with the current ROUTE cookie value (defaults to "/").
- A list of **Query Parameters** as key/value pairs, pre-populated from the current QUERY cookie. The user can add new pairs (an "Add parameter" button) and remove existing ones (a × button on each row).
- Buttons "OK" and "Cancel".

If the user confirms (OK):
- Set the cookie `ROUTE` to the new route value.
- Set the cookie `QUERY` as the URL-encoded query string of the key/value pairs (skip rows whose key is empty).
- Update the toolbar button label to show the new route.
- Reload the right iframe using `<RIGHT><new_route>?<query>#<new_route>` as the URL (omit the `?<query>` segment if the query string is empty).

If the user cancels, the cookies and iframe are left unchanged.

# Device Preview

The right iframe shows the user's running application. So the user can check how
that application behaves at tablet and phone widths without leaving the
workbench, the right iframe lives inside a **preview pane** that can constrain it
to a device viewport.

A three-way segmented control sits in the right-hand toolbar group, immediately
before the Route button. The three buttons are icon-only (monitor, tablet, phone
inline SVG inheriting the button text color) with `title` and `aria-label`
attributes, and use the shared `nu-segment-btn` / `nu-segment-btn-active`
styling. The active mode is visibly marked.

| Mode | Viewport |
|---|---|
| Desktop | fills the pane (no device frame) |
| Tablet | 820 × 1180 |
| Mobile | 390 × 844 |

In Desktop mode the iframe fills the pane exactly as before: no frame, no
backdrop, no transform.

In Tablet and Mobile mode the iframe is laid out at the device's true pixel size
and centered in the pane. If it does not fit, it is **scaled down** with a CSS
`transform: scale(...)` so the whole device frame is always visible; the scale is
clamped at 1 so the frame is never enlarged past 1:1. The frame gets a thin
border, a small radius and a soft shadow, and the surrounding pane gets a muted
backdrop so the device reads as a device.

Scaling must be applied to the frame's transform only, never to its layout width.
The iframe therefore still reports the true device width to the previewed
application, so that application's own CSS media queries fire exactly as they
would on the real device. This is the point of the feature.

The scale is recomputed whenever the pane's box changes — window resize,
horizontal divider drag, and the terminal pane opening or being resized.

The chosen mode is stored in the `DEVICE` cookie (`desktop`, `tablet` or
`mobile`) and restored on page load. A missing or unrecognized value falls back
to `desktop`.

The device frame applies to whatever the right iframe is currently showing,
including the redeploy progress and result pages injected via `srcdoc`.

# Terminal

The **Terminal** button toggles a shell pane below the two iframes. Full
contract in [12-terminal.md](12-terminal.md).

- The body is a vertical column: the `leftFrame | divider | rightFrame` row on
  top, then a horizontal divider and the terminal pane below. Opening the
  terminal shrinks the iframe row; neither frame is replaced.
- The pane runs xterm.js in a `<div>`, connected to `/api/terminal/<name>` — a
  real PTY-backed shell in the app's workbench directory.
- The pane is resizable with the same drag behaviour as the vertical divider,
  and its height persists in `localStorage`. Visibility does **not** persist:
  closing the pane terminates the shell.
- The button shows an active state while the pane is open.

# Redeploy

The **Redeploy** entry in the Utils pulldown triggers a server-side redeploy cycle.

When clicked:

- Replace the right iframe content with a spinner and status message "Redeploying..."
- Open an EventSource to `GET /api/redeploy?name=<NAME>`
- The API streams SSE events (`event: status`) updating the iframe status text as each step progresses:
  - Terminating ops ide devel
  - Waiting for port 5173 to be free
  - Deploying actions (ops ide deploy)
  - Getting action list (ops action list)
  - Starting dev server (ops ide devel --fast)
  - Waiting for dev server to be ready (HTTP HEAD check)
- On `event: done`, show "Redeploy complete", the action list in a code block, and an OK link pointing to `<RIGHT><ROUTE>?<QUERY>#<ROUTE>`
- On `event: error`, stop the spinner and show the error in red

# Clean

The **Clean** entry in the Utils pulldown removes the local build artifacts of the
current application by running `ops ide clean` — and nothing else. It does **not**
redeploy. `ops ide clean` stops the devel watcher and removes the virtualenv,
`node_modules`, and `*.zip` artifacts, so the preview stays down until the user runs
**Utils > Redeploy**.

When clicked:

- Disable the Utils button and replace the right iframe content with a spinner and the
  status message "Cleaning...".
- `POST /api/clean` with `{"name": "<NAME>"}`.
- On success show "clean completed", the command output in a code block, and the note
  that the preview is stopped and Utils > Redeploy brings it back.
- On failure show the returned `error` (and `output` when present) in red.
- Re-enable the Utils button in both cases.

The internal `ops ide clean` steps that are part of launch, revert, commit, and Git
Pull > Deploy are unchanged — this entry only adds a way to invoke it on demand.

# Debug

The **Debug** entry in the Utils pulldown opens a separate browser window at `debug.html?app=<NAME>`.

`debug.html` is a first-party Trustable utility page in the visual rollout. It
uses the shared light top bar, Work Sans status text, restrained action buttons,
and a bordered log panel. The streamed activation output remains monospace
inside the log panel because it is command output.

The debug window opens an EventSource to `GET /api/activations/poll?name=<NAME>` and displays the streamed activation log.

The backend:
- Validates `<NAME>` with the same app-name rules used by the other per-app endpoints.
- Requires `$WORKBENCH_DIR/<NAME>` to exist.
- Regenerates the app `.env` before starting the command.
- Runs `ops activation poll` in `$WORKBENCH_DIR/<NAME>` with the generated `.env` values appended to the process environment, so `OPS_USER`, `OPS_PASSWORD`, and `OPS_APIHOST` match the development context of the app being edited.
- Streams stdout and stderr as SSE events.
- Terminates the spawned process group when the browser closes the debug window or stops the stream.

# Credits

A small **Credits box** is rendered in the toolbar immediately after the app name, but **only when the merged configuration's `provider` field equals `"trustable"`**. For Ollama (or any other provider) neither the box nor the Top-up button are rendered.

The box is a compact pill (rounded border, light background) showing the label `Credits:` followed by the current credit value (e.g. `Credits: 873`). While the value has not yet been fetched, show `Credits: …`. On error, show `Credits: —` and put the error text in the element's `title` attribute (tooltip).

If the proxy returns `credits: null` (server-side gate disabled — see [credit_check.md](credit_check.md) §`GET /api/v2/credits` "Notes for clients"), display `Credits: ∞` and treat the user as having unlimited credit; the Top-up button described below is hidden in that case.

Immediately to the right of the Credits box, render a **Top-up** button (yellow/amber, plus icon, label `Top-up`). Clicking it opens the Top-up modal described under "Top-up" below.

## AIP base URLs

Two environment variables drive all ai-proxy URLs. They are read at process startup, validated by preflight, and used directly — there is no URL rewriting, no `/v1` ↔ `/v2` swap, and no derived `AIP_API_BASE`/`AIP_ORIGIN`. The historical `/v1`-suffix contract on the proxy `base_url` is **removed**.

| Env var | Purpose | Example |
|---|---|---|
| `AIP_BASE_URL` | JSON API base. `/api/credits`, `/api/topup`, and `/api/status` all forward directly under this URL. | `https://api.nuvolaris.io/api/v2/` |
| `AIP_REGISTER_URL` | Registration UI base. The splash page loads it in an iframe; the top-up form lives at `<this>/top-up`. Exposed to the frontend as `register_url` on `GET /api/configuration`. | `https://api.nuvolaris.io/_register` |

The Trustable provider's `base_url` field on the workspace config is still the OpenAI-compatible inference base used by opencode and any model client — but it is **not** what the credit/top-up/status endpoints use. Those go through `AIP_BASE_URL`. The two are independent: changing the provider does not change `AIP_BASE_URL`.

URL summary:

- **Credits balance (JSON):** `GET $AIP_BASE_URL/credits`
- **Top-up (JSON):** `POST $AIP_BASE_URL/top-up`
- **Registration page (HTML):** `$AIP_REGISTER_URL`
- **Top-up form (HTML, used as the fallback link in the modal):** `$AIP_REGISTER_URL/top-up`

Both JSON endpoints are authenticated with the workspace config's `api_key` as a Bearer token. Response shapes and error semantics are documented in [credit_check.md](credit_check.md).

## Credits API

The frontend MUST NOT call the proxy directly (the API key must stay server-side). The backend exposes two local proxy endpoints that forward to `$AIP_BASE_URL` with `Authorization: Bearer <api_key>`:

### `GET /api/credits`

Proxies `GET $AIP_BASE_URL/credits` (response shape: see [credit_check.md](credit_check.md) — at minimum `credits`, `credit_total`, `currency`, `credit_value`, `out_of_credit`).

- If `provider != "trustable"`, return HTTP 404.
- If `AIP_BASE_URL` is not set, return HTTP 502 with `{"error": "AIP_BASE_URL is not set"}` (preflight should have already aborted startup; this is the defense-in-depth path).
- Otherwise issue `GET $AIP_BASE_URL/credits` with the bearer key and return the JSON body verbatim on 2xx.
- On non-2xx from the proxy, return `{"error": "<status>: <body>"}` with HTTP 502.

### `POST /api/topup`

Proxies `POST $AIP_BASE_URL/top-up` (see [credit_check.md](credit_check.md) §`POST /api/v2/top-up` for the response shape).

- If `provider != "trustable"`, return HTTP 404.
- If `AIP_BASE_URL` is not set, return HTTP 502 with `{"error": "AIP_BASE_URL is not set"}`.
- Request body: `{"amount": <integer>}`. The amount must be one of `1000`, `5000`, `10000` (the default `TOPUP_AMOUNTS` whitelist documented in [credit_check.md](credit_check.md)). The backend forwards the body unchanged.
- Forward the request as `POST $AIP_BASE_URL/top-up` with `Authorization: Bearer <api_key>` and `Content-Type: application/json`.
- On 2xx return the proxy's JSON body verbatim (`status`, `credits`, `credit_total`, `out_of_credit`).
- On `400 invalid_amount` from the proxy, return HTTP 400 with body `{"error": "invalid_amount"}` so the frontend can surface a precise message.
- On any other non-2xx, return `{"error": "<status>: <body>"}` with HTTP 502.

## Refresh cadence

When the page loads (and the provider is Trustable), the frontend calls `GET /api/credits`, populates the box from the `credits` field of the response, and then re-fetches **every 60 seconds** using `setInterval`. The interval is cleared when the user navigates away (Back button or page unload).

In addition, the Credits box must be re-fetched immediately after a successful top-up (see "Top-up" below).

## Top-up

Clicking the Top-up button opens a modal titled "Top up credits" containing:

- A short description: *"Add credits to your Trustable account. Each credit is worth `<credit_value> <currency>` (taken from the most recent `/api/credits` response — fall back to the literal text "—" if unknown)."*
- Three radio buttons / amount tiles: **1000**, **5000**, **10000** credits. Default-select `1000`.
- An "OK" button (yellow/amber, label `Top up`) and a "Cancel" button.

When the user confirms:

1. Disable the buttons and show a spinner.
2. `POST /api/topup` with body `{"amount": <selected>}`.
3. On 2xx response:
   - Replace the modal content with a success message including the new `credits` and `credit_total` from the response (e.g. *"Topped up. New balance: 1900 credits."*) and a single **Continue** button that closes the modal.
   - Immediately re-fetch `GET /api/credits` so the toolbar pill reflects the new balance (do not wait for the 60s interval).
4. On a `400 invalid_amount` response, show *"That amount is not allowed. Pick one of the listed options."* and re-enable the buttons (no modal close).
5. On any other error, show the error text in red and re-enable the buttons; do not close the modal automatically. Provide a fallback link at the bottom of the modal: *"Or top up via the web form: `<$AIP_REGISTER_URL/top-up>`"* (rendered as an `<a target="_blank">`).

The modal can be closed with the X button, the Cancel button, the Escape key, or by clicking the backdrop (only when no request is in flight).
