# OpenServerless action contract

This file is the short recovery contract for Trustable app work. Read it before
touching actions, databases, setup, seed data, deploys, or service state.

If `check_openserverless_actions.sh .` reports drift, re-read this file before
editing again.

## Environment

- You are inside a generated Trustable app workbench, normally
  `/home/trustable/workbench/<app>`.
- The durable source is the app git repo; fixes must be made in repo files, not
  only in the runtime.
- OpenCode has the shell. Run bounded checks yourself instead of asking the user
  to run pod-local commands.
- `ops ide devel` exposes the app in this pod at `http://localhost:5173`.
- OpenCode serves in this pod at `http://localhost:4096`.
- Browser/ingress hosts such as `vite.<domain>` are external checks. Use them
  only after `ops ide deploy` succeeds and only when external routing matters.
- `OPS_APIHOST` is the configured OpenServerless API host. Do not replace it
  with localhost.

## Files

- Frontend code lives in `src/`.
- Public web assets live in `public/`.
- Action logic lives in `packages/<package>/<action>/<module>.py`.
- Generated wrappers live in `packages/<package>/<action>/__main__.py`.
  Do not edit wrappers for business logic.
- Setup and initialization actions live under `packages/setup/<action>/`.

## Action Names

Valid action names are only:

- `action`
- `package/action`

Use package `v1` for browser APIs unless the user explicitly asks otherwise.

Valid examples:

- `v1/prospect`
- `v1/issues`
- `setup/database`

Invalid examples:

- `v1/auth/register`
- `v1/contacts/list`
- `packages/v1/auth/register`

## Database Rules

- Add PostgreSQL wiring with the OpenServerless action tool.
- Use `conn = ctx.POSTGRESQL` in editable action modules.
- Do not reconnect with `POSTGRES_URL` when `ctx.POSTGRESQL` is provided.
- Do not hardcode database URLs, hosts, users, passwords, schemas, buckets, or
  service ports in source code.
- Every write must commit.
- Every demo seed must be idempotent and use a seed marker table or equivalent
  durable marker.
- Migrations must be repeatable. Use `IF NOT EXISTS` where possible.
- Drop/recreate derived views when their column shape changes.
- Do not make a live DB-only schema fix with `psql`, PostgreSQL MCP, or ad hoc
  SQL and then declare the app fixed. If you inspect or repair live state while
  debugging, put the equivalent idempotent migration in `setup/database`, run
  `ops ide setup`, then read back the schema/data.

## Web Action Route IDs

- The OpenServerless MCP tools create actions and service wiring; they do not
  replace this runtime contract.
- For item routes such as `/api/my/v1/contacts/123`, do not assume one fixed
  `__ow_path` shape.
- Use a helper that accepts body fallback and suffix forms such as `123`,
  `/123`, `/contacts/123`, and `/api/my/v1/contacts/123`.
- A `PUT` fix that works only because the frontend sends `id` in the JSON body
  is incomplete if `DELETE /api/my/v1/<resource>/<id>` still fails.
- For each CRUD resource, test create/list/update/delete through
  `http://localhost:5173`, including `PUT` and `DELETE` with id in the URL.

## Browser And Printable Responses

- If the frontend opens an action URL with `window.open(...)`, the target must
  be a browser response, not just app JSON.
- For printable HTML such as an invoice, return HTML as the HTTP body with
  `Content-Type: text/html; charset=utf-8`. Do not return
  `{"ok": true, "html": "<!DOCTYPE html>..."}` when the browser opens the URL
  directly.
- If the generated wrapper nests module returns under JSON and cannot pass
  headers/body through, use a frontend route that fetches JSON with
  `Authorization`, extracts the HTML, writes it to a new window/document, and
  then prints. Do not pretend that raw `window.open(/api/my/...)` will render
  embedded JSON HTML as a page.
- For opened/downloaded/printable URLs, verify with `curl -i` from inside the
  pod and check both status and `Content-Type`.
- Token-in-query is acceptable only when a new browser window cannot send the
  `Authorization` header; prefer short-lived or app-session tokens and validate
  with a real session token.

## Deploy And Verification

- After setup action changes, run `timeout 120 ops ide setup`.
- After public action changes, run `timeout 120 ops ide deploy`.
- Run `timeout 60 check_openserverless_actions.sh .` before deploy. Trustable
  installs the checker once in the user PATH.
- Verify app HTTP endpoints from inside the pod with:
  `curl http://localhost:5173/api/my/<package>/<action>`.
- For write paths, write and then read back the changed value.
- For delete paths, delete through the public HTTP route and then confirm the
  record is no longer returned.
- If you used `psql` or a service MCP to inspect/repair live data during
  debugging, also prove the source setup/action code recreates the same state.
- For printable/browser-opened paths, prove the response shape with
  `curl -i http://localhost:5173/...` and check that JSON endpoints return JSON
  while printable HTML endpoints return `text/html`.
- Use `vite.<domain>` only after deploy and only for external browser/ingress
  verification.
- Do not hide failures with `|| true` or output truncation that masks the first
  actionable error.

## If Blocked

- If an MCP action tool is missing or returns invalid tool, inspect the exposed
  tools and generated `opencode.json`; do not invent raw `ops action` commands.
- If a service MCP write would "fix" state, fix the setup/action code instead
  unless the user explicitly requested administrative data repair.
- If validation is impossible, state the blocker and the command that failed.
