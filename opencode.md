# Trustable App Assistant Guide

You are working inside a user-created Trustable app. This is a
TypeScript/React frontend plus Python OpenServerless actions. It is not a
conventional backend server project.

## Serverless Operating Model

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

## Non-Negotiable Rules

- Never create a backend server. Create public or private actions instead.
- Never create or edit generated `__main__.py` files.
- Never run foreground dev servers or watchers such as `npm run dev`, `vite`,
  or `ops ide devel`.
- Never run unbounded commands. Use `timeout <seconds> ...` for checks that may
  hang.
- Do not build or deploy the Trustable product itself. When validating app
  action changes, use the app deploy/redeploy path described below.
- Put feature logic, request parsing, auth checks, and business behavior in the
  editable module file: `packages/<package>/<action>/<module>.py`.
- If setup actions change, run `ops ide setup`.
- If action modules change and runtime behavior must be verified, deploy or
  redeploy before testing the public endpoint.
- Do not leave the user with only "try it now" when you can run a bounded
  validation yourself.
- Do not declare a phase complete when the app code path is still failing,
  even if direct MCP or database commands can produce the desired data.
- Do not invent tool or `ops` command names. Use only tools exposed in the
  current OpenCode tool list or generated `opencode.json`.

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

Choose the backend shape this way:

- Use a public `v1` action for browser-facing APIs.
- Use a private `setup` action for idempotent initialization.
- Use S3 for object/file data.
- Use PostgreSQL for relational data.
- Use Redis for cache or ephemeral state.
- Use Milvus for vector search.
- Use AgentiReact MCP only when the app is configured with AgentiReact.

## OpenServerless Action Tools

Use the Trustable/OpenServerless MCP action tools instead of manually creating
platform scaffolding. Tool names may appear with hyphens or underscores,
depending on the client. Use the matching exposed tool:

- `action-new` / `action_new`: create public or private actions and generated
  wrappers.
- `action-invoke` / `action_invoke`: invoke private actions such as setup
  actions.
- `action-requirements` / `action_requirements`: add Python libraries.
- `action-add-secret` / `action_add_secret`: add an environment secret.
- `action-add-s3` / `action_add_s3`: add S3 service wiring.
- `action-add-postgresql` / `action_add_postgresql`: add PostgreSQL service
  wiring.
- `action-add-redis` / `action_add_redis`: add Redis service wiring.
- `action-add-milvus` / `action_add_milvus`: add Milvus service wiring.

If a tool call returns "Invalid Tool", stop and use one of the exposed tool
names. Do not retry with guessed aliases. If a shell command reports
`no command named ...`, do not keep guessing `ops` subcommands; use the MCP
action tools above or inspect the available task list with bounded commands.

For a new public HTTP endpoint, use package `v1` unless the user explicitly
asks for another package. The endpoint is reachable at
`/api/my/<package>/<action>`.

For initialization, create private actions in package `setup` with
`public: false`. After creating or changing setup actions, run `ops ide setup`.

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

Service MCP servers are generated from `~/.ops/config.json` after
`ops ide login`. If a service block is missing, the corresponding MCP server is
intentionally absent. Do not hardcode service hosts, ports, credentials, bucket
names, database names, or tokens when the MCP server or generated environment
already provides them.

The launch process also writes `.mcp.json` in Claude Code format with the same
MCP servers. OpenCode should rely on generated `opencode.json`.

Service MCP servers are diagnostic and verification aids. They must not replace
the app's own setup actions or public API paths. Do not use `postgres_execute_sql`
or other service MCP write operations to create schemas, seed records, repair
state, or mark a feature complete unless the user explicitly asks for an
administrative data repair. For normal app work, fix the setup/action code and
rerun the app path. Read-only MCP checks such as listing tables or selecting
rows are fine as supporting evidence after the app path succeeds.

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

## Setup And Data Initialization

- All initialization belongs in private actions in package `setup`.
- Setup actions must be incremental, idempotent, and non-destructive.
- Table creation belongs in `setup/database`.
- Redis key preparation belongs in `setup/cache`.
- Milvus collection creation belongs in `setup/collection`.
- Private S3 data preload belongs in `setup/upload`.
- Public web assets belong in `public/`, not in setup uploads.
- Run `ops ide setup` after creating or changing setup actions.
- `ops ide setup` must succeed before setup work is complete.
- If setup returns `Cannot start action. Check logs for details.`, immediately
  run `timeout <seconds> ops logs --last` and fix the first traceback. Do not
  proceed by mutating the service directly.
- Do not create missing tables or seed rows with PostgreSQL MCP write tools and
  then claim setup succeeded. The `setup/*` action must be able to recreate the
  state idempotently.

Examples of idempotent setup:

- `CREATE TABLE IF NOT EXISTS ...`
- `ALTER TABLE ... ADD COLUMN IF NOT EXISTS ...`
- Redis `SET ... NX` for keys that should only be seeded once.
- Check Milvus collection existence before creating it.
- Upload S3 objects only when missing or changed.

## Dependencies

- Add frontend dependencies to `package.json`, then run `npm install`.
- Add Python dependencies only with `action-requirements`.
- Before importing a non-stdlib Python package such as `bcrypt`, `jwt`,
  `requests`, or a database driver, add it with `action-requirements` and
  redeploy the action.
- If action logs show `ModuleNotFoundError`, fix the dependency or import before
  doing any other validation. Do not mark the feature complete.
- Add PostgreSQL, Redis, S3, Milvus, and secrets with the corresponding
  action/service tool. Do not hardcode credentials and do not manually edit
  generated wrapper code.

## Data And Service Restrictions

Retrieve the current user with `ops util whoami` when needed. These
restrictions are enforced by the platform:

- PostgreSQL database is named after the user; the default schema is
  `<user>_schema`.
- Milvus database is named after the user.
- Redis writable keys must be prefixed with `<user>:`.
- S3 writable buckets are `<user>-data` for private app data and `<user>-web`
  for public web assets.
- The S3 MCP cannot list buckets, so assume only the two user buckets above are
  writable.

## Validation Checklist

End backend-related work with proof:

- After changing an action module, run the appropriate deploy/redeploy path.
- After changing setup actions, run `ops ide setup`.
- Validate public actions with bounded HTTP checks against
  `/api/my/<package>/<action>`.
- `ops action invoke` by itself is not enough proof when it only prints an
  activation id such as `ok: invoked ...`; inspect the action result/logs or
  validate through the HTTP endpoint.
- If any action reports `Cannot start action`, `application error`, or
  `developer error`, run `timeout <seconds> ops logs --last` before changing
  strategy.
- Verify JSON request fields, method, and headers are visible to the action.
- Verify frontend fetch handling accepts the response shape actually returned.
- Use bounded checks such as `timeout <seconds> ...` and `curl`.
- If validation is impossible, state the blocker instead of asking the user to
  "try it now" with no local proof.
