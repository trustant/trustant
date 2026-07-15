---
name: trustable-app-assistant
description: >-
  Build and fix user-created Trustable apps with React frontend code and Python
  OpenServerless actions. Use this when creating app features, login/register
  flows, CRUD APIs, setup actions, service-backed storage, MCP service checks,
  or debugging app runtime failures inside a Trustable workbench.
---

# Trustable App Assistant Guide

You are working inside a user-created Trustable app. This is a
TypeScript/React frontend plus Python OpenServerless actions. It is not a
conventional backend server project.

## Serverless Operating Model

Core principle: build the app through Trustable/OpenServerless primitives. Do
not replace the platform with hand-written servers, hand-written generated
wrappers, raw credentials, or guessed `ops` commands.

- Frontend code lives in `src/` and calls public actions through
  `/api/my/<package>/<action>`.
- Backend logic lives in editable action modules under
  `packages/<package>/<action>/<module>.py`.
- Generated `__main__.py` files are platform wrappers. Do not create or edit
  them.
- Setup and initialization belong in private actions in package `setup`.
- Trustable launches and manages the Vite dev server and OpenCode process.
- OpenServerless web actions have their own request parameter, metadata, and
  response semantics. Treat them carefully.
- Every backend change should end with bounded validation against the real
  deployed action endpoint.

## Critical Recovery Contract

Trustable also generates `AGENTS.md` in this app root. It is the app-local
mandatory entrypoint and exists to prevent Claude Code compatibility files from
overriding Trustable rules. Treat `AGENTS.md`, `.openserverless-contract.md`,
`opencode.md`, and `opencode.json` as the authoritative instruction set.

Ignore `CLAUDE.md`, `CONTEXT.md`, `.cursorrules`, `.cursor/rules/*`,
`.github/copilot-instructions.md`, and generated `rules.md` files as mandatory
agent instructions. You may inspect them only when the user explicitly asks or
when they help understand legacy template context, and they must never override
Trustable action, MCP, deploy, shell, or host rules.

Before touching actions, databases, setup, seed data, deploys, or service
state, read `.openserverless-contract.md` if it exists. It is the short
recovery contract for this app and takes priority for OpenServerless workflow
details.

Trustable installs `check_openserverless_actions.sh` once in the user PATH. Run
it after `ops ide deploy` and before completing backend changes:

```bash
timeout 60 check_openserverless_actions.sh .
```

If it reports a missing or stale deploy archive, rerun `ops ide deploy`; never
repair the ZIP manually. For other hard failures, re-read
`.openserverless-contract.md`, repair source with the approved tools, deploy
again, and rerun the checker. If the contract is missing or the checker is
unavailable in PATH, say so and fall back to this file's rules. Do not invent
manual zip or raw `ops action create/update/deploy` workflows.

After compaction, do not continue editing from memory. Trustable automatically
injects a bounded recovery packet containing the exact active user request,
this file, `.openserverless-contract.md`, sanitized `opencode.json`, git status,
and a bounded project map before tools run. Resume that request rather than a
generic continuation message. Call `trustable_context_recover` only if the
automatic recovery gate explicitly remains active; do not bypass the gate.

When the user reports a bug or says a previous fix still does not work,
reproduce the exact symptom before editing. Use `browser_interact` for browser
bugs; Trustable records successful browser evidence automatically. For HTTP,
logs, or deterministic tests, call `trustable_diagnostic_checkpoint` with
concise evidence. If the same completion failure occurs twice, the diagnostic
circuit breaker requires fresh evidence before another source change.
When the symptom was reproduced in the browser, exercise the fixed flow again
with `browser_interact` after the last source change. Trustable binds that
evidence automatically; sound fixes must show active audio state. Backend-only
checks do not verify a browser-visible flow.
Protected views that load identity asynchronously must keep a distinct loading
state; do not redirect merely because the initial user/profile value is null.

After source changes, call `trustable_completion_check` before claiming the
work is fixed or asking the user to try it. The completion tool runs the
OpenServerless checker, frontend checker, git diff validation, and the frontend
typecheck and build when available.

For frontend work, run the project typecheck before the build after each
coherent edit batch. A successful Vite build does not prove that every JSX
symbol is defined. After the first successful build or deploy, immediately open
the exact changed route with the Browser MCP, inspect the rendered page and
diagnostics, and exercise the visible flow before more speculative edits. Do
not clear caches or reinstall dependencies unless the observed failure points
to dependency state. Repeat the browser verification after later frontend
source changes.

## Non-Negotiable Rules

- Never create a backend server. Create public or private actions instead.
- Never create or edit generated `__main__.py` files.
- If OpenCode denies an edit to `packages/**/__main__.py`, `packages/**/*.zip`,
  or a raw shell command matching `ops action` / `ops action *`, treat that as
  a Trustable guardrail: use the OpenServerless MCP action tools and
  `ops ide deploy/setup` flow instead of trying to bypass it.
- Never run foreground dev servers or watchers such as `npm run dev`, `vite`,
  or `ops ide devel`.
- Never kill, restart, or replace Trustable-managed processes. Diagnose the
  existing `http://localhost:5173` server and its generated proxy configuration.
- Never run unbounded commands. Use `timeout <seconds> ...` for checks that may
  hang.
- Never append `|| true` or `|| echo` to deploy, setup, login, checker, or build
  commands, and never pipe those commands through `head` or `tail`; the plugin
  blocks output masking because it can turn a real failure into apparent success.
- Do not ask the user to run shell commands from inside this pod when you have
  shell access. Run bounded checks yourself, including `ops ide deploy`, `curl`,
  `npm run build`, `python3 -m compileall`, and `git diff --check`. Ask the
  user only when shell/tool access is missing or the task requires credentials
  or physical access only the user has.
- Do not build or deploy the Trustable product itself. When validating app
  action changes, use the app deploy/redeploy path described below.
- Put feature logic, request parsing, auth checks, and business behavior in the
  editable module file: `packages/<package>/<action>/<module>.py`.
- After every action MCP call or change under `packages/`, run
  `timeout 120 ops ide deploy` before setup, runtime verification, or
  completion. This includes setup actions.
- If setup actions change, run `timeout 120 ops ide setup` only after deploy.
- The completion gate remains blocked until required setup succeeds.
- Never create, edit, move, or delete action ZIP files manually. They are
  derived sibling artifacts owned by `ops ide deploy`.
- Do not leave the user with only "try it now" when you can run a bounded
  validation yourself.
- Do not declare a phase complete when the app code path is still failing,
  even if direct MCP or database commands can produce the desired data.
- Do not invent tool or `ops` command names. Use only tools exposed in the
  current OpenCode tool list or generated `opencode.json`.
- Do not use shell redirection to create or replace source files. Avoid
  `cat > file`, heredocs, `tee`, `printf >`, and `sed -i` for app source or
  generated wrappers; use file edit/write tools.
- Avoid stale `edit` tool errors. Before editing a file that was created or
  changed earlier in the session, re-read the file and use the current text for
  replacements. For small generated app modules or React pages that are being
  replaced wholesale, prefer the file write tool with the full final content
  over many incremental `edit` replacements. If an `edit` returns `oldString`
  not found, `No changes to apply`, or identical old/new content, do not retry
  the same edit; re-read the file, check whether the target change is already
  present, then either continue or rewrite the file once.
- Do not write project docs, plans, rules, or examples that recommend forbidden
  commands or invalid endpoint shapes. Documentation must not contain examples
  such as `ops action deploy`, `ops action update`, `v1/auth/register`, or
  `v1/contacts/list`.
- Do not read or copy secrets from `~/.ops/config.json` into app source. Never
  paste database URLs, passwords, tokens, service hosts, buckets, or ports into
  code when platform wiring can provide them.
- If an MCP action tool fails while creating or wiring an action, stop and fix
  that tool sequence. Do not manually create nested action directories,
  generated wrappers, or hardcoded service wiring as a workaround.

## Project Layout

- `src/`: React/TypeScript frontend.
- `public/`: public web assets uploaded automatically.
- `packages/<package>/<action>/`: Python action directories.
- `packages/<package>/<action>/<module>.py`: editable action logic.
- `packages/<package>/<action>/__main__.py`: generated wrapper, do not edit.
- `packages/setup/<action>/`: private setup actions.
- `.agents/skills/`: app-specific skills, when installed.
- `opencode.json`: generated OpenCode config for this app.
- `opencode.md`: this instruction file.
- `.mcp.json`: generated Claude-compatible MCP config with the same MCP
  servers.

## Application Development Workflow

1. Inspect existing `src/`, `packages/`, `public/`, `.agents/skills`, and
   available MCP servers before changing files.
2. Build frontend behavior in `src/` using the existing React/Tailwind style.
3. For backend behavior, create or update OpenServerless actions instead of
   starting a server process.
4. Add platform services with the action/service tools before writing code that
   depends on them.
5. Put schema, collection, cache, or seed initialization in private setup
   actions.
6. Use generated MCP servers and CLI wrappers to inspect service state during
   debugging.
7. Validate with bounded checks against the real public endpoint and
   browser-visible app host.

For frontend behavior, use the generated bounded browser MCP before declaring
a UI bug fixed. Start with `browser_open` in `development` mode, which targets
the Trustable-managed `http://localhost:5173`, then inspect URL, accessibility
snapshot, console, and network diagnostics. Use `browser_interact` to reproduce
the exact click/form/reload flow. Use `deployed` mode only after
`ops ide deploy` and only when external `vite.<domain>` ingress behavior is in
scope. Do not start another Vite server.

Use this execution loop for backend work:

1. Read `.openserverless-contract.md` if present. Run the checker after deploy
   and before completion when it exists.
2. Design the action endpoint names and reject invalid nested names before
   creating files.
3. Create actions with the OpenServerless MCP action tool.
4. If an action reads or writes a platform service, immediately add service
   wiring with the matching action/service tool after the action files exist
   and before editing the module logic.
5. Edit only the generated editable module, not `__main__.py`. If the module
   exists without a wrapper, stop and repair/create the action through the MCP
   action tool before continuing.
6. Add Python libraries with `action-requirements`.
7. Run `timeout 120 ops ide deploy` after every action change. If setup actions
   changed, run `timeout 120 ops ide setup` after deploy. Inspect failures,
   then validate via the real HTTP app path.

Choose the backend shape this way:

- Use a public `v1` action for browser-facing APIs.
- Use a private `setup` action for idempotent initialization.
- Use S3 for object/file data.
- Use PostgreSQL for relational data.
- Use Redis for cache or ephemeral state.
- Use MongoDB for document data only when the official MongoDB capability is
  configured.
- Use Milvus for vector search.
- Do not use Milvus as a replacement for MongoDB.
- Use AgentiReact MCP only when the app is configured with AgentiReact.

## OpenServerless Action Tools

Use the Trustable/OpenServerless MCP action tools instead of manually creating
platform scaffolding. Tool names may appear with hyphens or underscores,
depending on the client. Use the matching exposed tool:

- `action-new` / `action_new`: create public or private actions and generated
  wrappers. Repeated creation of a compatible existing action is a successful
  check/no-op; continue without retrying it.
- `action-invoke` / `action_invoke`: invoke private actions such as setup
  actions.
- `action-requirements` / `action_requirements`: add Python libraries.
- `action-add-secret` / `action_add_secret`: add an environment secret.
- `action-add-s3` / `action_add_s3`: add S3 service wiring.
- `action-add-postgresql` / `action_add_postgresql`: add PostgreSQL service
  wiring.
- `action-add-redis` / `action_add_redis`: add Redis service wiring.
- `action-add-milvus` / `action_add_milvus`: add Milvus service wiring.
- `action-add-mongodb` / `action_add_mongodb`: add MongoDB service wiring.
- `secret-unbind` / `secret_unbind`: remove an obsolete generated secret
  binding atomically without reading or deleting its value. Use it to repair a
  legacy invalid managed-variable binding; never edit `__main__.py` manually.
  After a removal, recreate each changed endpoint with
  `ops ide undeploy <endpoint>` followed by `ops ide deploy <endpoint>`: an
  action update alone preserves previously deployed parameters, while
  `ops ide clean` removes only local build artifacts.

If a tool call returns "Invalid Tool", stop and use one of the exposed tool
names. Do not retry with guessed aliases. If a shell command reports
`no command named ...`, do not keep guessing `ops` subcommands; use the MCP
action tools above or inspect the available task list with bounded commands.

Do not create or mutate ZIP files under `packages/`; they are generated beside
action directories by `ops ide deploy`. Do not use `ops action deploy`; it is
not an app workflow command. Do not use
`ops action update`, `ops action create`, or raw `ops action` commands as the
normal deploy path for edited app modules. After changing any action, including
setup actions, run `timeout <seconds> ops ide deploy`. After changing setup
actions, run `timeout <seconds> ops ide setup` only after deploy succeeds.

For a new public HTTP endpoint, use package `v1` unless the user explicitly
asks for another package. The endpoint is reachable at
`/api/my/<package>/<action>`.

For initialization, create private actions in package `setup` with
`public: false`. After creating or changing setup actions, run `ops ide deploy`
and then `ops ide setup`.

## Action Endpoint Grammar

OpenWhisk action names are namespace/package/action. In Trustable app code, the
MCP action endpoint must therefore be only:

- `action`
- `package/action`

Every package and action segment must start with a letter and contain only
letters, numbers, and hyphens. Use flat hyphenated names such as
`v1/employees-photo`; underscores and spaces are invalid. Never send
`v1/employees_photo` to an action MCP tool.

For browser-facing APIs, use package `v1`. Valid examples:

- `v1/register`
- `v1/login`
- `v1/me`
- `v1/contacts`
- `v1/orders`
- `setup/database`

Invalid examples:

- `v1/auth/register`
- `v1/contacts/list`
- `v1/orders/create`
- `packages/v1/auth/register`

If an API needs CRUD behavior, prefer one public action per resource, such as
`v1/contacts` or `v1/orders`, and branch inside the editable module using
`__ow_method` plus request data. If separate actions are clearer, keep names
flat and hyphenated, such as `v1/contacts-list` or `v1/orders-create`.

Never create nested directories under `packages/<package>/<group>/<action>` to
simulate routes. They are not valid Trustable/OpenServerless endpoints.

## MCP Servers And Service Access

`opencode.json` is generated at launch with an `mcp` section. Use available MCP
servers and generated CLI wrappers instead of inventing connection details.

- `openserverless`: always present; exposes action-management tools.
- `agentireact`: present only when the app's Vite config contains
  `AgentiReact()`; remote MCP at `http://localhost:5173/mcp`.
- `s3`: present only when S3 is configured; companion CLI wrapper: `rclone`.
- `postgres`: present only when PostgreSQL is configured; companion CLI
  wrapper: `psql`.
- `redis`: present only when Redis is configured; companion CLI wrapper:
  `redis-cli`.
- `milvus`: present only when Milvus is configured; companion CLI wrapper:
  `milvus_cli`.
- `mongodb`: present only when MongoDB is configured as an official
  OpenServerless capability in `~/.ops/config.json`.

Service MCP servers are generated from `~/.ops/config.json` after
`ops ide login`. If a service block is missing, the corresponding MCP server is
intentionally absent. Do not hardcode service hosts, ports, credentials, bucket
names, database names, or tokens when the MCP server or generated environment
already provides them.

MCP servers are assistant-side tools. They do not automatically create runtime
environment variables, action parameters, or `ctx` bindings inside Python
actions. A successful service MCP call proves only that the assistant can
inspect that service; it is not proof that the app action can use the same
connection. For action runtime access, use the OpenServerless action service
tools such as `action-add-redis`, `action-add-postgresql`, `action-add-s3`,
`action-add-milvus`, and `action-add-mongodb`. If no matching action service
tool or generated runtime binding exists, the app must expose a deterministic
`non configurato`/error state instead of inventing a backend connection.

MongoDB is a document database capability, separate from Milvus/vector search.
If the user asks for MongoDB and the `mongodb` MCP server or official MongoDB
environment is absent, implement a deterministic `non configurato`/error state
in the app and README. Do not ask the user how to configure MongoDB, do not
invent connection details, and do not use Milvus, `MILVUS_*`, `pymilvus`, or
`milvus_cli` as a substitute.

When `action-add-mongodb` / `action_add_mongodb` is exposed, use it to generate
the MongoDB wrapper before writing module code. The generated wrapper exposes
`ctx.MONGODB_CLIENT` and `ctx.MONGODB`; business modules should use those
context values instead of reading connection strings directly.

Do not use `MDB_MCP_CONNECTION_STRING` in app source, action modules, wrappers,
README instructions, or frontend code. That variable belongs only to the
generated MongoDB MCP server process. Do not invent `MONGODB_URI`, `MONGO_URL`,
`MONGO_CONNECTION_STRING`, `MDB_CONNECTION_STRING`, or similar MongoDB runtime
variables unless a generated action wrapper already exposes an official MongoDB
runtime binding. If MongoDB is visible only through the MCP server and not
through an action service tool/runtime binding, report MongoDB as
`non configurato` in the app path.

You may inspect `~/.ops/config.json` only to understand which services exist.
Do not copy values from it into app code, wrapper code, logs, docs, or frontend
configuration.

The launch process also writes `.mcp.json` in Claude Code format with the same
MCP servers. OpenCode should rely on generated `opencode.json`.

Service MCP servers are diagnostic and verification aids. They must not replace
the app's own setup actions or public API paths. Do not use `postgres_execute_sql`
or other service MCP write operations to create schemas, seed records, repair
state, or mark a feature complete unless the user explicitly asks for an
administrative data repair. For normal app work, fix the setup/action code and
rerun the app path. Read-only MCP checks such as listing tables or selecting
rows are fine as supporting evidence after the app path succeeds.

When inspecting PostgreSQL through MCP, use the exact exposed tool names. For
schemas use `postgres_list_schemas`. For tables/views in a schema use
`postgres_list_objects`. Do not call generic names such as `list_schemas`.

## Application Secrets And Authentication Setup

For token-based authentication, use the OpenServerless secret tools instead of
reading or editing `.env` or generated wrappers. Secret values are not model
context; use status/binding tools only:

- `secret_status` checks whether a named secret exists and which endpoints are
  bound, without reading its value.
- `secret_ensure` securely generates a missing authorized secret and persists
  it through Trustable without returning the value.
- `secret_bind` atomically binds the same existing secret to several actions.
- `auth_setup` is the preferred authentication workflow. Pass every endpoint
  that creates tokens and every endpoint that validates tokens, including the
  `me`/session action and protected resource actions. Rerun it when another
  protected action is added; it is idempotent.

If any secret/auth tool returns an MCP error, stop that sequence and correct the
tool inputs. Do not continue with only some endpoints configured. In editable
action modules, read the value only from `ctx.<SECRET>` and fail closed when it
is absent. Never use `os.getenv()` with a default, an app-name-derived key, or
another hardcoded fallback. Token creation and token validation must always use
the same context secret.

When using Redis in an action, first add Redis wiring with
`action-add-redis` / `action_add_redis`. The generated wrapper exposes
`ctx.REDIS` and `ctx.REDIS_PREFIX` and adds the required Python Redis client to
the action package. Always construct keys from the prefix and an app-local
suffix:

```python
def redis_key(ctx, name):
    return f"{getattr(ctx, 'REDIS_PREFIX', '') or ''}{name}"
```

Use `ctx.REDIS.get(redis_key(ctx, "cache:item"))`,
`ctx.REDIS.set(redis_key(ctx, "cache:item"), value)`, and the same pattern for
`delete`, `hset`, `hget`, `lpush`, `sadd`, `expire`, and similar commands. Do
not use naked Redis keys such as `"stack-e2e-..."` directly with `ctx.REDIS`;
Nuvolaris Redis ACLs only allow the configured user prefix.

For S3 app verification, use the OpenServerless action path created with
`action-add-s3` and the generated `ctx.S3_CLIENT` wiring. The credentials are
scoped to `ctx.S3_DATA` and `ctx.S3_WEB`: never call
`ctx.S3_CLIENT.list_buckets()`. Neither `head_bucket` nor bucket/object listing
proves read/write access. A read/write check must create a unique temporary key
in `ctx.S3_DATA` with `put_object`, retrieve it with `get_object`, compare the
returned body bytes, and remove it with `delete_object` in a `finally` block.
Report `read_write: OK` only after the byte comparison succeeds. If an S3 MCP
listing tool fails, treat it as a diagnostic tool failure and continue through
the configured app bucket/action path or `rclone` when available.

## Runtime Host Rules

OpenCode runs inside the Trustable pod. Classify hosts before using them:

- `localhost:5173` is the pod-local app dev server started by `ops ide devel`.
  Use it for normal app HTTP validation from this shell.
- `localhost:4096` is the pod-local OpenCode server.
- `trustable.<domain>` is the browser-visible Trustable UI/API host.
- `vite.<domain>` is the browser-visible app host through Trustable
  proxy/ingress. Use it only after `ops ide deploy` succeeds and only when
  external browser or ingress routing is in scope.
- `opencode.<domain>` is the browser-visible OpenCode host.
- `OPS_APIHOST` is the configured OpenServerless API host for Trustable and
  `ops ide` orchestration. It must never be bound into an action, exposed as
  `ctx.OPS_APIHOST`, read by an action module, or emitted as
  `#--param OPS_APIHOST "$OPS_APIHOST"`.

Frontend code calls actions with relative `/api/my/<package>/<action>` URLs so
the browser keeps its current origin. Action modules must not call sibling
actions through `OPS_APIHOST`, browser-visible hosts, or ingress URLs. For
multiple independent checks, let the frontend call the relative endpoints; for
server-side aggregation, add every required generated service binding to one
action and use its `ctx` clients directly.

Do not invent pod IPs, raw service names, public domains, or replacement
localhost URLs for app verification. For app endpoints from this shell, prefer:

```bash
curl http://localhost:5173/api/my/<package>/<action>
```

## PostgreSQL Action Pattern

After `action-add-postgresql` / `action_add_postgresql`, the generated wrapper
adds PostgreSQL wiring and exposes a live `psycopg` connection as
`ctx.POSTGRESQL`.

In the editable module:

- Use `conn = ctx.POSTGRESQL`.
- Do not import `psycopg2`.
- Do not call `psycopg2.connect(ctx.POSTGRESQL)` or reconnect using
  `ctx.POSTGRESQL`; it is already a connection object.
- Do not manually edit `__main__.py` to add database wiring. Use the
  PostgreSQL action tool.
- Do not hardcode PostgreSQL connection strings, usernames, passwords, hosts,
  or schemas in module code or wrappers.

Use this pattern or an equivalent one:

```python
def main(args, ctx=None):
    if not ctx or not hasattr(ctx, "POSTGRESQL"):
        return {"ok": False, "error": "Database not configured"}

    conn = ctx.POSTGRESQL
    with conn.cursor() as cur:
        cur.execute("CREATE TABLE IF NOT EXISTS users (id SERIAL PRIMARY KEY)")
    conn.commit()
    return {"ok": True}
```

## Skills

App-specific skills may be installed under `.agents/skills`. Read relevant
`SKILL.md` files before using them. Do not delete or replace `.agents/skills`
unless the user explicitly asks to update skills.

## Web Action Request Rules

OpenServerless web actions are Apache OpenWhisk web actions:

- Public web actions can be invoked over HTTP without an OpenWhisk API key.
- The action owner pays for the activation, so the action must implement its
  own application-level authorization when needed.
- Query parameters, form fields, and JSON object body fields can be passed as
  first-class action arguments.
- In normal OpenWhisk merging, body fields override query fields.
- HTTP context is exposed through reserved metadata keys such as
  `__ow_method`, `__ow_headers`, and `__ow_path`.
- Web actions support HTTP methods such as GET, POST, PUT, PATCH, DELETE, HEAD,
  and OPTIONS. Use `__ow_method` for method-based CRUD actions.
- Requests cannot override reserved `__ow_*` metadata names.

Trustable-generated Python actions should be defensive: some wrappers or
clients may also provide `args["body"]` as a dict or JSON string. Merge both
shapes and let top-level fields win, because a generated wrapper or previous
edit can create an empty `body = {}` while real request fields are top-level.

Use this pattern in editable modules when reading JSON fields:

```python
import json

def request_data(args):
    data = dict(args) if isinstance(args, dict) else {}
    body = data.get("body")
    if isinstance(body, str):
        try:
            body = json.loads(body)
        except Exception:
            body = {}
    merged = dict(body) if isinstance(body, dict) else {}
    ignored = {"body", "POSTGRES_URL", "__ow_method", "__ow_headers", "__ow_path"}
    merged.update({k: v for k, v in data.items() if k not in ignored})
    return merged
```

Read request metadata from OpenServerless keys first:

```python
def request_method(args):
    return (args.get("__ow_method") or args.get("method") or "GET").upper()

def request_headers(args):
    headers = args.get("__ow_headers") or args.get("headers") or {}
    return {str(k).lower(): v for k, v in headers.items()} if isinstance(headers, dict) else {}

headers = request_headers(args)
auth_header = headers.get("authorization", "")
```

If a raw or non-JSON request body is needed, handle `__ow_body` explicitly.
Most app JSON endpoints should not need raw body handling.

For REST-style item routes, do not assume `__ow_path` always contains the full
public URL. It can be a suffix or a different shape depending on the
OpenServerless web action route. Use body `id` only as a fallback, not as the
only way update/delete works.

Use this pattern or an equivalent one for item ids:

```python
def request_route_id(args, data, resource_name):
    for key in ("id", f"{resource_name}_id"):
        value = data.get(key)
        if value not in (None, ""):
            return str(value)

    raw_path = str(args.get("__ow_path") or args.get("path") or "").strip("/")
    if not raw_path:
        return ""

    parts = [part for part in raw_path.split("/") if part]
    if not parts:
        return ""

    if resource_name in parts:
        index = parts.index(resource_name)
        if index + 1 < len(parts):
            return parts[index + 1]

    return parts[-1]
```

For CRUD resources, test both update and delete through the public HTTP path:

```bash
curl -X PUT http://localhost:5173/api/my/v1/<resource>/<id> ...
curl -X DELETE http://localhost:5173/api/my/v1/<resource>/<id> ...
```

A test that only calls `/api/my/v1/<resource>` with `{"id": ...}` in the body
does not prove the REST-style item route works.

## Web Action Response Rules

OpenWhisk web actions can use top-level `headers`, `statusCode`, and `body` as
HTTP response instructions. Trustable-generated Python wrappers, however,
commonly call the editable module and return:

```python
{ "body": module.main(args, ctx=ctx) }
```

Because of that, a module return value such as:

```python
{"statusCode": 401, "body": {"error": "Token non fornito"}}
```

can reach the browser as HTTP 200 with that object nested inside JSON if the
wrapper did not pass it through.

Therefore:

- Do not edit `__main__.py` just to force HTTP status behavior.
- Treat editable module return values as application JSON unless the generated
  wrapper is known to pass web-action envelopes through.
- Prefer simple module payloads such as `{"ok": False, "error": "..."}` for
  app-level errors.
- Frontend fetch code should normalize both direct and wrapped payloads before
  reading fields.

Use this frontend pattern or an equivalent one:

```ts
const raw = await response.json();
const data = raw && typeof raw === "object" && "body" in raw ? raw.body : raw;
if (!response.ok || data?.ok === false || data?.error) {
  throw new Error(data?.error || `Request failed: ${response.status}`);
}
```

## Browser-Opened And Printable Actions

If the frontend opens an action URL directly with `window.open(...)`, an `<a>`
link, or a form target, the endpoint must return a browser-native response. Do
not return JSON that contains HTML and then claim the browser flow is complete.

For printable HTML such as invoices, receipts, labels, reports, or documents,
the correct behavior is one of these:

1. The action returns a real HTML response:

```python
return {
    "statusCode": 200,
    "headers": {"Content-Type": "text/html; charset=utf-8"},
    "body": html,
}
```

This only works if the generated wrapper passes the envelope through to
OpenWhisk. Verify with:

```bash
curl -i http://localhost:5173/api/my/v1/<action>/<id>...
```

The response must show `Content-Type: text/html`, and the body must begin with
HTML, not with JSON.

2. If the wrapper nests module output as application JSON, keep the action JSON
and change the frontend flow: fetch with `Authorization`, extract `data.html`,
open a new window, write the HTML into that window, and then call print. In
that case do not use raw `window.open("/api/my/...")` as proof that printing
works.

Avoid this broken pattern for direct browser-opened endpoints:

```python
return {"ok": True, "html": html}
```

That renders as JSON in a new browser window. It is not a printable page.

Token-in-query is acceptable only when a new window cannot send the
`Authorization` header. Prefer short-lived app-session tokens, and validate with
a real session token from the app database or login flow. Do not use the
OpenServerless `~/.ops/config.json` auth value as an app session token.

## Authentication UI Rules

When an app has login or registration:

- Treat login/register as the only public UI flows.
- Replace starter placeholder screens. The root route must redirect to login,
  render login, or render the authenticated app based on session state; it must
  not keep the Trustable starter/welcome template.
- Before marking auth UI complete, inspect the router and the component used by
  `/` or `#/`. Remove or replace generated starter content such as `Welcome`,
  `Try the following prompts to start`, `Powered by Trustable`, `trustable.png`,
  or sample prompt lists. A protected app is incomplete if the browser-visible
  home page still shows the starter screen.
- Hide protected navigation items such as dashboards, contacts, orders,
  settings, admin, or profile until the user is authenticated.
- Protect direct routes too. If an unauthenticated user opens a protected hash
  route directly, redirect to the login/register route or render the auth view,
  not the protected page.
- With React Router `HashRouter`, pass logical routes such as `/login` to
  `Link`, `NavLink`, `Navigate`, and `useNavigate`. The router adds `#/` to the
  browser URL. Never pass `#/login` to those APIs, and never use root-relative
  anchors such as `<a href="/login">` for internal navigation.
- After login, persist only the opaque session token needed by the frontend.
  A cached user/profile may improve rendering but is never authoritative proof
  of authentication.
- A successful login or registration must update the live authentication
  provider/store before navigating to a protected route. Writing token/user
  data only to `localStorage` leaves the current React render unauthenticated
  and commonly causes an immediate redirect back to login.
- On every full-page load, keep an explicit authentication loading state and
  validate the persisted token through a bounded backend `me`/session endpoint
  before rendering protected routes. The backend validates expiry and derives
  identity from the token. On any validation failure, clear the token and
  cached identity and render the public authentication flow; never fall back to
  a cached localStorage user as an authenticated session.
- A successful registration must establish the same authenticated session as
  login, either by returning session/token/user data directly or by performing
  an immediate login. Do not send the newly registered user back to a separate
  login step before entering the protected area.
- Add an explicit logout path when protected navigation is shown.
- Give every form control a stable `id` and `name`, and associate each label
  with `htmlFor` matching that `id`. A placeholder is not a label. When a
  browser locator matches multiple controls, fix missing form semantics when
  appropriate, use a `ref` from the latest browser snapshot, or pass the
  browser MCP's explicit zero-based `index`; never
  bypass the rendered registration/login flow with `curl` and claim the browser
  flow passed.
- For audio behavior, verification requires browser evidence after the user
  gesture. Confirm that the browser snapshot/diagnostics reports a running
  AudioContext or active unmuted media; source inspection and a visible music
  toggle are not proof that sound is produced.
- Do not hardcode browser-visible identity such as `user_id=1` in fetch URLs or
  request bodies. The backend must derive the current user from authenticated
  request state, such as a token/session header, not from a user id supplied by
  the browser.

## Setup And Data Initialization

- All initialization belongs in private actions in package `setup`.
- Setup actions must be incremental, idempotent, and non-destructive.
- Table creation belongs in `setup/database`.
- Redis key preparation belongs in `setup/cache`.
- Milvus collection creation belongs in `setup/collection`.
- MongoDB collection/index preparation belongs in an idempotent setup action
  only when MongoDB is configured.
- Private S3 data preload belongs in `setup/upload`.
- Public web assets belong in `public/`, not in setup uploads.
- Run `ops ide deploy` after creating or changing any action, then run
  `ops ide setup` when setup actions changed.
- `ops ide setup` must succeed before setup work is complete.
- If setup returns `Cannot start action. Check logs for details.`, immediately
  run `timeout <seconds> ops logs --last` and fix the first traceback. Do not
  proceed by mutating the service directly.
- Do not create missing tables or seed rows with PostgreSQL MCP write tools and
  then claim setup succeeded. The `setup/*` action must be able to recreate the
  state idempotently.
- Do not make a live DB-only schema fix with `psql`, PostgreSQL MCP, or ad hoc
  SQL and then claim the app is fixed. If you inspect or repair live state while
  debugging, put the equivalent idempotent migration in `setup/database`, run
  `ops ide setup`, and read back the schema/data through a bounded command.

Examples of idempotent setup:

- `CREATE TABLE IF NOT EXISTS ...`
- `ALTER TABLE ... ADD COLUMN IF NOT EXISTS ...`
- Redis `SET ... NX` for keys that should only be seeded once.
- Check Milvus collection existence before creating it.
- Check MongoDB collection/index existence before creating it.
- Upload S3 objects only when missing or changed.

## Dependencies

- Add frontend dependencies to `package.json`, then run `npm install`.
- Add Python dependencies only with `action-requirements`.
- Before importing a non-stdlib Python package such as `bcrypt`, `jwt`,
  `requests`, or a database driver, add it with `action-requirements` and
  redeploy the action.
- If action logs show `ModuleNotFoundError`, fix the dependency or import before
  doing any other validation. Do not mark the feature complete.
- Add PostgreSQL, Redis, S3, Milvus, MongoDB, and secrets with the corresponding
  action/service tool. Do not hardcode credentials and do not manually edit
  generated wrapper code.
- `OPS_USER`, `OPS_PASSWORD`, `OPS_APIHOST`, `OPS_REPO`, and `OPS_SKILLS` are
  Trustable-managed orchestration variables, not application secrets. Never
  pass them to `action-add-secret`, `secret-bind`, `secret-ensure`, or
  `auth-setup`; a secret tool must reject them.

## Data And Service Restrictions

Retrieve the current user with `ops util whoami` when needed. These
restrictions are enforced by the platform:

- PostgreSQL database is named after the user; the default schema is
  `<user>_schema`.
- Milvus database is named after the user.
- MongoDB is available only when the official post-login config exposes a
  MongoDB block or derived connection string.
- Redis keys used by actions must be built with the generated
  `ctx.REDIS_PREFIX`; do not guess `<user>:` manually and do not use naked keys.
- S3 writable buckets are `<user>-data` for private app data and `<user>-web`
  for public web assets.
- The S3 MCP cannot list buckets, so assume only the two user buckets above are
  writable.

## Validation Checklist

End backend-related work with proof:

- After changing an action module, run `ops ide deploy`.
- Run `timeout 60 check_openserverless_actions.sh .` after deploy when the
  checker is available.
- After changing setup actions, run `ops ide deploy` and then `ops ide setup`.
- Validate public actions with bounded HTTP checks against
  `http://localhost:5173/api/my/<package>/<action>` from inside this pod.
- For CRUD resources, validate the full create/list/update/delete matrix. Test
  `PUT /api/my/v1/<resource>/<id>` and
  `DELETE /api/my/v1/<resource>/<id>` without relying only on `id` in the JSON
  body, then read back to confirm the updated value or deleted absence.
- For browser-opened or printable endpoints, validate with `curl -i` and prove
  the response status and content type match the browser use case. A direct
  `window.open("/api/my/...")` target for printable HTML must not return
  `application/json`.
- Use `vite.<domain>` only after deploy and only for explicit external
  browser/ingress checks.
- `ops action invoke` by itself is not enough proof when it only prints an
  activation id such as `ok: invoked ...`; inspect the action result/logs or
  validate through the HTTP endpoint.
- If any action reports `Cannot start action`, `application error`, or
  `developer error`, run `timeout <seconds> ops logs --last` before changing
  strategy.
- If `psql` or a service MCP was used to inspect or repair live database state,
  prove the source setup/action code recreates that state. Runtime state alone
  is not completion proof.
- Verify JSON request fields, method, and headers are visible to the action.
- Verify frontend fetch handling accepts the response shape actually returned.
- Treat editor, LSP, TypeScript, lint, and tool diagnostics as validation
  failures when they mention generated or edited files. Fix the diagnostic, or
  explain why it is stale with a successful bounded command that proves it.
- Use bounded checks such as `timeout <seconds> ...` and `curl`.
- Do not hide validation failures with `|| true`, forced zero exits, or output
  truncation that can mask the first error. Let checks fail loudly, then fix the
  failure.
- For frontend auth flows, validate both route shape and route behavior: root
  path, login path, register path, direct protected route while logged out, and
  protected navigation after login. After submitting valid credentials, assert
  that the protected page is visibly rendered without requiring a reload.
- If validation is impossible, state the blocker instead of asking the user to
  "try it now" with no local proof.
