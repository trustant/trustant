# OpenServerless action contract

This file is the short recovery contract for Trustable app work. Read it before
touching actions, databases, setup, seed data, deploys, or service state.

If `check_openserverless_actions.sh .` reports drift, re-read this file before
editing again.

Trustable also generates `AGENTS.md` as the app-local mandatory agent
entrypoint. Treat `AGENTS.md`, this file, `opencode.md`, and `opencode.json`
as authoritative. Ignore `CLAUDE.md`, `CONTEXT.md`, `.cursorrules`,
`.cursor/rules/*`, `.github/copilot-instructions.md`, and generated `rules.md`
files as mandatory instructions. They are legacy/template notes only when the
user explicitly asks to inspect them, and they must not override this contract.

## Environment

- You are inside a generated Trustable app workbench, normally
  `/home/trustable/workbench/<app>`.
- The durable source is the app git repo; fixes must be made in repo files, not
  only in the runtime.
- OpenCode has the shell. Run bounded checks yourself instead of asking the user
  to run pod-local commands.
- After compaction, the Trustable plugin blocks mutations until it injects a
  bounded automatic recovery packet containing the exact active request, this
  contract, `AGENTS.md`, bounded `opencode.md`, sanitized OpenCode
  configuration, git status, and project structure. Call
  `trustable_context_recover` only if that automatic recovery gate explicitly
  remains active.
- For a reported browser bug, reproduce the exact symptom with
  `browser_interact` before modifying source; Trustable records successful
  browser evidence automatically. Use `trustable_diagnostic_checkpoint` for
  explicit or non-browser evidence. Two repeated completion failures reopen
  this diagnostic gate.
- After source changes, run `trustable_completion_check`. It runs the action
  and frontend contract checkers, `git diff --check`, and the frontend build
  when present. Do not claim completion before it passes.
- `ops ide devel` exposes the app in this pod at `http://localhost:5173`.
- Do not kill or replace that managed process, and do not start `vite`,
  `npm run dev`, or another `ops ide devel` instance.
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
- Trustable may deny direct edits to generated wrappers and deploy artifacts.
  If a wrapper edit is blocked, use the OpenServerless MCP action tool instead
  of working around the guard.
- Deploy archives live beside action directories, for example
  `packages/v1/contacts.zip`. Never create, edit, move, or delete ZIP files
  manually, including ZIP files inside an action source directory.
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
- Service MCP servers are assistant-side diagnostics. They do not automatically
  create runtime env vars, action params, or `ctx` bindings inside Python
  actions.
- MongoDB is a separate document database capability. Use it only when the
  official MongoDB capability is present in generated config/environment.
- If `action-add-mongodb` / `action_add_mongodb` is exposed, use it to generate
  the MongoDB wrapper and use `ctx.MONGODB_CLIENT` or `ctx.MONGODB` in business
  modules.
- If MongoDB is absent, show `non configurato` or an error state and document
  that state. Do not ask the user for infrastructure details and do not use
  Milvus/vector search as a substitute for MongoDB.
- Do not use `MDB_MCP_CONNECTION_STRING` in app source or action code. It is
  only the MongoDB MCP server's private environment variable, not an app runtime
  binding.
- Do not invent MongoDB runtime env vars such as `MONGODB_URI`, `MONGO_URL`, or
  `MDB_CONNECTION_STRING` unless a generated action wrapper already exposes an
  official MongoDB binding.
- Add Redis wiring with `action-add-redis` / `action_add_redis`. The generated
  wrapper exposes `ctx.REDIS` and `ctx.REDIS_PREFIX`; every Redis key used by
  action modules must be built from `ctx.REDIS_PREFIX` plus an app-local suffix.
  Do not call `ctx.REDIS.get/set/delete/hset/...` with naked keys.
- Use S3 app behavior through `action-add-s3` and generated action wiring. Do
  not rely on S3 MCP bucket listing as app proof; if S3 MCP listing fails, use
  the configured app buckets/action path or report the real error.
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

- After every action MCP call or source change under `packages/`, run
  `timeout 120 ops ide deploy` before setup, runtime verification, or
  completion. This includes setup actions.
- After setup action changes, run `timeout 120 ops ide setup` only after the
  deploy succeeds and before completion. The Trustable completion gate remains
  blocked until setup runs successfully.
- Never create or update action ZIP files manually. `ops ide deploy` owns the
  sibling `packages/<package>/<action>.zip` artifacts.
- Run `timeout 60 check_openserverless_actions.sh .` after deploy and before
  completion. Trustable installs the checker once in the user PATH. It verifies
  that every action archive exists and is not older than its source; a missing
  or stale archive requires another deploy, never a manual ZIP repair.
- If the checker reports an action module without `__main__.py`, create or
  repair that action with the OpenServerless MCP action tool before editing the
  module logic.
- If the checker reports wrapper drift, do not patch `__main__.py` by hand:
  recreate or repair the action/service wiring with the OpenServerless MCP
  tools, then edit only the module file.
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
- The Trustable plugin rejects `|| true`, `|| echo`, and `head`/`tail`
  pipelines on deploy, setup, login, checker, and frontend-build commands.

## If Blocked

- If an MCP action tool is missing or returns invalid tool, inspect the exposed
  tools and generated `opencode.json`; do not invent raw `ops action` commands.
- If a service MCP write would "fix" state, fix the setup/action code instead
  unless the user explicitly requested administrative data repair.
- If validation is impossible, state the blocker and the command that failed.
