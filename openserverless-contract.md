# OpenServerless action contract

This file is the short recovery contract for Trustable app work. Read it before
touching actions, databases, setup, seed data, deploys, or service state.

If `check_openserverless_actions.sh .` reports drift, re-read this file before
editing again.

Trustable also generates `AGENTS.md` as the app-local mandatory agent
entrypoint. Treat the Trustable-managed block in `AGENTS.md`, this file, and
`.mcp.json` as authoritative. There is no project-local `opencode.md`;
`CLAUDE.md` is only a compatibility mirror. Ignore `CONTEXT.md`, `.cursorrules`,
`.cursor/rules/*`, `.github/copilot-instructions.md`, and generated `rules.md`
files as mandatory instructions. They are legacy/template notes only when the
user explicitly asks to inspect them, and they must not override this contract.

Before the first shell command, source mutation, or application MCP operation,
discover the actual capability surface for every server declared in
`.mcp.json.mcpServers`. Use `mcp({})` followed by
`mcp({server: "<name>"})` per server, or use
`mcp({connect: "<name>"})` per server. A successful `connect` proves both MCP
proxy reachability and that server's tool discovery. Use the exact returned
tool names and schemas. Do not guess a tool contract from memory or skip
discovery because a server is lazy.

## Environment

- You are inside a generated Trustable app workbench, normally
  `/home/trustable/workbench/<app>`.
- The durable source is the app git repo; fixes must be made in repo files, not
  only in the runtime.
- Pi has the shell. Run bounded checks yourself instead of asking the user
  to run pod-local commands.
- After compaction, re-read the exact active request, this contract,
  `AGENTS.md`, git status, and the relevant project files before resuming.
  Pi has no Trustable session-enforcement plugin or recovery tool.
- For a reported browser bug, reproduce the exact symptom with
  `browser_interact` before modifying source when the browser MCP is available.
  Use bounded HTTP, log, or deterministic tests for non-browser evidence. If a
  check fails repeatedly, stop repeating it and revise the diagnosis.
- After source changes, run the relevant action and frontend checker commands,
  `git diff --check`, and the frontend typecheck/build when present. Verify
  user-visible frontend changes through the exact changed route. Pi has no
  `trustable_completion_check` tool.
- `ops ide devel` exposes the app in this pod at `http://localhost:5173`.
- It is also the sole owner of live action packaging and deployment. Never run
  `ops ide deploy` or start another `ops ide devel` instance. Do not kill or
  replace the watcher or start `vite` or `npm run dev`.
- TruACP/Pi serves in this pod at `http://localhost:4096`.
- Browser/ingress hosts such as `vite.<domain>` are external checks. Use them
  only after the managed watcher has deployed the current sources and only
  when external routing matters.
- `OPS_APIHOST` is the configured OpenServerless API host used by Trustable and
  `ops ide` for login, deploy, and development proxy orchestration. It is not
  an application secret or action runtime parameter. Never bind it into an
  action, expose it as `ctx.OPS_APIHOST`, read it from an action module, or
  generate `#--param OPS_APIHOST "$OPS_APIHOST"`.
- Browser code calls actions with relative `/api/my/<package>/<action>` URLs so
  the browser preserves its own origin. Actions must not call sibling actions
  through `OPS_APIHOST`, a browser-visible host, or an ingress URL; either let
  the frontend call the endpoints independently or give one action the
  generated service bindings it needs.

## Files

- Frontend code lives in `src/`.
- Public web assets live in `public/`.
- Action logic lives in `packages/<package>/<action>/<module>.py`.
- Generated wrappers live in `packages/<package>/<action>/__main__.py`.
  Do not edit wrappers for business logic.
- Direct edits to generated wrappers and deploy artifacts are unsupported and
  the managed Pi policy blocks them. Use the OpenServerless MCP action tool
  instead of working around artifact ownership.
- Deploy archives live beside action directories, for example
  `packages/v1/contacts.zip`. Never create, edit, move, or delete ZIP files
  manually, including ZIP files inside an action source directory.
- Setup and initialization actions live under `packages/setup/<action>/`.

## Action Names

Valid action names are only:

- `action`
- `package/action`

Each package and action segment must start with a letter and contain only
letters, numbers, and hyphens. Use flat hyphenated names; underscores and
spaces are invalid.

Use package `v1` for browser APIs unless the user explicitly asks otherwise.

Valid examples:

- `v1/prospect`
- `v1/issues`
- `v1/employees-photo`
- `setup/database`

Invalid examples:

- `v1/auth/register`
- `v1/contacts/list`
- `v1/employees_photo`
- `packages/v1/auth/register`

## Database Rules

- Application `.env` and `.env.production` are immutable agent boundaries.
  Never read, create, edit, import, synchronize, or regenerate them. Only the
  user may change application environment values through the Trustable
  configuration interface. Report a missing variable without creating it.
- Application authentication uses Redis-backed opaque sessions, not JWT or an
  application signing secret. Create every login, registration, `me`/session,
  protected-resource, and logout endpoint, then call `auth-setup` /
  `auth_setup` once with the complete endpoint sets. It atomically adds Redis
  wiring without reading or writing `.env`; use `action-add-redis` /
  `action_add_redis` only for individual non-authentication Redis endpoints.
  Generate an opaque random token, store its token-to-identity mapping in Redis
  with a bounded TTL, validate it on every protected request, and delete it on
  logout.
- Every authentication/session key must use `ctx.REDIS_PREFIX`. The browser
  stores only the opaque token; the backend derives identity from the Redis
  record and never trusts a browser-supplied user id.
- `OPS_USER`, `OPS_PASSWORD`, `OPS_APIHOST`, `OPS_REPO`, and `OPS_SKILLS` are
  Trustable-managed runtime variables, not application secrets. Secret tools
  must reject them and must not add them to generated action wrappers.
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
- One action may legitimately use MongoDB and Milvus as separate capabilities.
  In that case use the official `ctx.MONGODB_CLIENT` / `ctx.MONGODB` binding for
  MongoDB and `ctx.MILVUS` for vector operations; the presence of both services
  is not, by itself, a MongoDB substitution error.
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
- Use S3 app behavior through `action-add-s3` and generated action wiring.
  Credentials are bucket-scoped: never call `ctx.S3_CLIENT.list_buckets()`.
  `head_bucket` and listing do not prove read/write access. Verify with a unique
  temporary key in `ctx.S3_DATA`: `put_object`, `get_object` and compare bytes,
  then `delete_object` in `finally`. Report read/write success only after the
  comparison succeeds; otherwise report the real error.
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

- After one or more successful `action_new` creations, finish the coherent
  action/wiring/source batch and call `trustable_runtime_redeploy` exactly
  once. It invokes the same safe Trustable host workflow as the UI Redeploy
  action: stop the watcher, run the full deploy, restart the watcher, and wait
  for readiness. A compatible idempotent `action_new` no-op does not require
  it. Do not run a concurrent `ops ide deploy`.
- After the required redeploy succeeds, read the canonical watcher state with
  `trustable_runtime_status`. Watcher status, checker, HTTP, and browser
  verification are blocked while redeploy remains required.
- Run `timeout 60 check_openserverless_actions.sh .` once after that evidence
  and before setup, runtime verification, or completion. In managed live mode,
  the checker validates source and contract invariants without treating sibling
  ZIP existence or freshness as watcher state.
- If setup actions changed, run `timeout 120 ops ide setup` only after watcher
  evidence shows a successful action update and the checker passes. Do not
  claim completion until setup runs successfully.
- Never create or update action ZIP files manually. The managed watcher owns
  the sibling `packages/<package>/<action>.zip` artifacts.
- Never inspect, list, search, stat, or poll those sibling ZIPs. If
  `trustable_runtime_status` reports an error or no progress, use that exact
  evidence to repair the source/tool sequence or report a managed failure. Do
  not repeat the checker without a relevant mutation or watcher change, run a
  manual deploy, increase the timeout, or repair a ZIP.
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
- Use `vite.<domain>` only after managed deployment is confirmed and only for
  external browser/ingress verification.
- Do not hide failures with `|| true` or output truncation that masks the first
  actionable error.
- Do not use `|| true`, `|| echo`, or `head`/`tail` pipelines on deploy, setup,
  login, checker, and frontend-build commands; output masking can turn a real
  failure into apparent success.

## If Blocked

- If an MCP action tool is missing or returns invalid tool, call `mcp({})` and
  inspect generated `.mcp.json` plus that server's exact tool list; do not
  invent raw `ops action` commands.
- After three semantically equivalent failures with no successful relevant
  source or wiring mutation, stop that strategy and revise the diagnosis.
  There is no numeric global step/turn budget while work makes real progress.
- If a service MCP write would "fix" state, fix the setup/action code instead
  unless the user explicitly requested administrative data repair.
- If validation is impossible, state the blocker and the command that failed.
