# Embedded `opencode.md`

This file specifies the `opencode.md` guidance embedded into the Trustable
binary and written into every launched app as
`<workbenchdir>/<app>/opencode.md`. The generated `opencode.json` must reference
that project-local file in its `instructions` array.

The embedded guidance is for coding assistants working inside user-created
apps. It is not guidance for editing `trustable-app` itself.

## Purpose

The embedded `opencode.md` must teach the assistant the Trustable serverless
mental model before listing detailed rules. The first substantive section must
make clear that a Trustable app is not a conventional backend server and not a
set of free-form Python modules:

- frontend code lives in `src/` and calls public actions through
  `/api/my/<package>/<action>`;
- backend logic lives in editable action modules under
  `packages/<package>/<action>/<module>.py`;
- generated `__main__.py` wrappers are platform scaffolding and must not be
  edited;
- setup work lives in private setup actions and must be idempotent;
- OpenServerless web actions have specific request parameter, metadata, and
  response-shape semantics;
- validation must use bounded checks and real action invocations.

## Required Structure

The embedded document must be written as an agent instruction document, similar
in clarity and directness to a `SKILL.md`, not as a broad project README.

It may start with `SKILL.md`-style YAML frontmatter containing only `name` and
`description`. The description must explain that the guidance is for building
and fixing user-created Trustable apps with React frontend code, Python
OpenServerless actions, setup actions, services, MCP checks, and runtime
debugging inside a Trustable workbench.

It must then contain these sections, in this order:

1. Serverless operating model.
2. Non-negotiable rules.
3. Project layout.
4. Application development workflow.
5. OpenServerless action tools.
6. Action endpoint grammar.
7. MCP servers and service access.
8. PostgreSQL action pattern.
9. Skills.
10. Web action request rules.
11. Web action response rules.
12. Authentication UI rules.
13. Setup and service initialization.
14. Dependencies.
15. Data/service restrictions.
16. Validation checklist.

## Non-Negotiable Rules

The embedded guidance must include these rules:

- Never create a backend server. Create public or private actions instead.
- Never create or edit generated `__main__.py` wrappers.
- Never run foreground dev servers or unbounded watchers such as
  `npm run dev`, `vite`, or `ops ide devel`.
- Never build or deploy the whole product manually; Trustable manages the
  long-running dev server. Use bounded checks and action deploy/setup commands
  only when needed for validation.
- Put feature code, parsing fixes, auth checks, and business behavior in the
  editable module file:
  `packages/<package>/<action>/<module>.py`.
- If setup actions change, explicitly run `ops ide setup`.
- If action modules change and runtime behavior must be verified, run the
  appropriate deploy/redeploy path before testing the public endpoint.
- Assistants must not use shell redirection to create or replace source files.
  The embedded guidance must explicitly forbid `cat > file`, heredocs, `tee`,
  `printf >`, and `sed -i` for app source or generated wrappers, and should
  tell assistants to use file edit/write tools instead.
- Assistants must not write project docs, plans, rules, or examples that
  recommend forbidden commands or invalid endpoint shapes. The embedded
  guidance must explicitly say documentation must not contain examples such as
  `ops action deploy`, `ops action update`, `v1/auth/register`, or
  `v1/contacts/list`.
- Assistants must not read or copy secrets from `~/.ops/config.json` into app
  source. They must never paste database URLs, passwords, tokens, service
  hosts, buckets, or ports into code when platform wiring can provide them.
- If an MCP action tool fails while creating or wiring an action, assistants
  must stop and fix the tool sequence instead of manually creating nested
  action directories, generated wrappers, or hardcoded service wiring.

## Application Development Workflow

The embedded guidance must describe the normal way to build a Trustable app:

1. Inspect existing `src/`, `packages/`, `public/`, `.agents/skills`, and
   available MCP servers before changing files.
2. Build the frontend in `src/` using the existing React/Tailwind conventions.
3. For every backend capability, create or update OpenServerless actions rather
   than starting a server process.
4. Add platform services with the action/service tools before writing code that
   depends on them.
5. Put schema, collection, cache, or seed initialization in private setup
   actions.
6. Use the generated MCP servers and CLI wrappers to inspect service state
   during debugging.
7. Run bounded validation against the real public endpoint and browser-visible
   app host.

The embedded guidance must include a concrete backend execution loop:

1. Design action endpoint names and reject invalid nested names before creating
   files.
2. Create actions with the OpenServerless MCP action tool.
3. If an action reads or writes a platform service, immediately add service
   wiring with the matching action/service tool after the action files exist and
   before editing the module logic.
4. Edit only the generated editable module, not `__main__.py`.
5. Add Python libraries with `action-requirements`.
6. Run `ops ide setup` for setup actions or `ops ide deploy` for public action
   changes, inspect logs on failure, then validate via the real HTTP app path.

The guidance must tell assistants how to choose the backend shape:

- use a public `v1` action for browser-facing APIs;
- use a private `setup` action for idempotent initialization;
- use S3 for object/file data;
- use PostgreSQL for relational data;
- use Redis for cache/ephemeral state;
- use Milvus for vector search;
- use AgentiReact MCP only when the app is configured with AgentiReact.

## OpenServerless Action Tools

The embedded guidance must tell assistants to use the Trustable/OpenServerless
MCP action tools instead of manually creating platform scaffolding.

The `openserverless` MCP server is always generated in `opencode.json`. It
exposes the action tools, replacing the old embedded `tools/` plugin files.
Depending on the client, tool names may appear with hyphens or underscores; the
instructions should name the Trustable concepts and tell the assistant to use
the matching exposed tool:

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

The embedded guidance must tell assistants not to invent tool or `ops` command
names. If a tool call returns "Invalid Tool", assistants must switch to one of
the exposed tools in the current tool list or generated `opencode.json`. If an
`ops` command reports `no command named ...`, assistants must not keep guessing
subcommands; they should use the MCP action tools above or inspect the available
task list with bounded commands.

The embedded guidance must explicitly say not to use `ops action deploy`
because it is not an app workflow command. It must also say not to use
`ops action update`, `ops action create`, or raw `ops action` commands as the
normal deploy path for edited app modules. After changing public action modules,
assistants must run `timeout <seconds> ops ide deploy`. After changing setup
actions, assistants must run `timeout <seconds> ops ide setup`.

For new public HTTP endpoints, the guidance must say to use package `v1` unless
the user explicitly asks for another package. A public action is reachable at
`/api/my/<package>/<action>`.

For setup and initialization, the guidance must say to create private actions
under package `setup` with `public: false`, and to invoke the complete setup
set with `ops ide setup` after creating or changing them.

## Action Endpoint Grammar

The embedded guidance must explain the Trustable/OpenWhisk endpoint grammar.
OpenWhisk action names are namespace/package/action, so the action endpoint
accepted by the Trustable action tools must be only:

- `action`;
- `package/action`.

For browser-facing APIs, the guidance must prefer package `v1`. It must include
valid examples such as:

- `v1/register`;
- `v1/login`;
- `v1/me`;
- `v1/contacts`;
- `v1/orders`;
- `setup/database`.

It must explicitly mark nested endpoint forms as invalid, including:

- `v1/auth/register`;
- `v1/contacts/list`;
- `v1/orders/create`;
- `packages/v1/auth/register`.

The guidance must say that CRUD resources should normally use one public action
per resource, such as `v1/contacts` or `v1/orders`, and branch inside the
editable module using `__ow_method` plus request data. If separate actions are
clearer, names must remain flat, such as `v1/contacts_list` or
`v1/orders_create`.

The guidance must explicitly forbid creating nested directories under
`packages/<package>/<group>/<action>` to simulate routes, because they are not
valid Trustable/OpenServerless endpoints.

## MCP Servers And Service Access

The embedded guidance must explain that `opencode.json` is generated at launch
with an `mcp` section. Assistants should use the available MCP servers and CLI
wrappers instead of inventing connection details.

Required MCP/service guidance:

- `openserverless` is always present and exposes action-management tools.
- `agentireact` is present only when the Vite config contains `AgentiReact()`;
  it is a remote MCP server at `http://localhost:5173/mcp`.
- `s3` is present only when S3 is configured. The companion CLI wrapper is
  `rclone`.
- `postgres` is present only when PostgreSQL is configured. The companion CLI
  wrapper is `psql`.
- `redis` is present only when Redis is configured. The companion CLI wrapper
  is `redis-cli`.
- `milvus` is present only when Milvus is configured. The companion CLI wrapper
  is `milvus_cli`.

The guidance must say that service MCP servers are generated from
`~/.ops/config.json` after `ops ide login`, and that missing service blocks mean
the corresponding MCP server is intentionally absent. Assistants must not
hardcode service hosts, ports, credentials, bucket names, database names, or
tokens when the MCP server or generated environment already provides them.
Assistants may inspect `~/.ops/config.json` only to understand which services
exist; they must not copy values from it into app code, wrapper code, logs,
docs, or frontend configuration.

The launch process also writes `<workbenchdir>/<app>/.mcp.json` in Claude Code
format with the same MCP servers. The embedded guidance can mention this for
compatibility, but OpenCode should rely on the generated `opencode.json`.

The embedded guidance must say that service MCP servers are diagnostic and
verification aids. They must not replace the app's own setup actions or public
API paths. Assistants must not use `postgres_execute_sql` or other service MCP
write operations to create schemas, seed records, repair state, or mark a
feature complete unless the user explicitly asks for an administrative data
repair. For normal app work, they must fix the setup/action code and rerun the
app path. Read-only MCP checks such as listing tables or selecting rows are
allowed as supporting evidence after the app path succeeds.

The embedded guidance must tell assistants to use exact PostgreSQL MCP tool
names when inspecting PostgreSQL. For schemas, use `postgres_list_schemas`. For
tables/views in a schema, use `postgres_list_objects`. It must explicitly forbid
generic invented names such as `list_schemas`.

## PostgreSQL Action Pattern

The embedded guidance must explain the generated PostgreSQL action wiring. After
`action-add-postgresql` / `action_add_postgresql`, the generated wrapper exposes
a live `psycopg` connection as `ctx.POSTGRESQL`.

The embedded guidance must say:

- editable modules should use `conn = ctx.POSTGRESQL`;
- modules must not import `psycopg2`;
- modules must not call `psycopg2.connect(ctx.POSTGRESQL)` or reconnect using
  `ctx.POSTGRESQL`, because it is already a connection object;
- assistants must not manually edit `__main__.py` to add database wiring; they
  must use the PostgreSQL action tool.
- assistants must not hardcode PostgreSQL connection strings, usernames,
  passwords, hosts, or schemas in module code or wrappers.

The guidance must include this pattern or an equivalent one:

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

The embedded guidance must explain that app-specific skills may be installed
under `.agents/skills`. Assistants should read relevant skill `SKILL.md` files
before using them. They should not delete or replace `.agents/skills` unless
the user explicitly asks to update skills.

## Web Action Request Rules

The embedded guidance must include a compact explanation of Apache OpenWhisk
web action semantics as used by OpenServerless:

- public web actions can be invoked over HTTP without an OpenWhisk API key;
- the action owner pays for the activation, so the action must implement its
  own application-level authorization when needed;
- query parameters, form fields, and JSON object body fields can be passed as
  first-class action arguments;
- body fields override query fields in OpenWhisk's normal merge behavior;
- HTTP context is exposed through reserved metadata keys such as
  `__ow_method`, `__ow_headers`, and `__ow_path`;
- web actions support HTTP methods such as GET, POST, PUT, PATCH, DELETE, HEAD,
  and OPTIONS, and method-based CRUD actions should use `__ow_method`;
- requests cannot override reserved `__ow_*` metadata names.

For Trustable-generated Python actions, the instructions must be defensive:
some wrappers or clients may also provide `args["body"]` as a dict or JSON
string. Modules must merge both shapes and let top-level fields win, because a
generated wrapper or previous edit can create an empty `body = {}` while the
real request fields are top-level.

The embedded guidance must include this pattern or an equivalent one:

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

The embedded guidance must tell assistants to read request metadata from the
OpenServerless keys first:

```python
def request_method(args):
    return (args.get("__ow_method") or args.get("method") or "GET").upper()

def request_headers(args):
    headers = args.get("__ow_headers") or args.get("headers") or {}
    return {str(k).lower(): v for k, v in headers.items()} if isinstance(headers, dict) else {}

headers = request_headers(args)
auth_header = headers.get("authorization", "")
```

If a raw or non-JSON request body is needed, the guidance must mention
`__ow_body` and say to handle decoding explicitly. Most app JSON endpoints
should not need raw body handling.

## Web Action Response Rules

The embedded guidance must distinguish OpenWhisk web action envelopes from
Trustable-generated wrappers.

OpenWhisk web actions can use top-level `headers`, `statusCode`, and `body` as
HTTP response instructions. However, generated Trustable Python wrappers call
the editable module and commonly return `{ "body": <module>.main(...) }`.
Because of that, a module return value such as
`{"statusCode": 401, "body": {"error": "Token non fornito"}}` can reach the
browser as HTTP 200 with that object nested inside JSON if the wrapper did not
pass it through.

The embedded guidance must therefore say:

- do not edit `__main__.py` just to force HTTP status behavior;
- treat editable module return values as application JSON unless the generated
  wrapper is known to pass web-action envelopes through;
- prefer simple module payloads such as `{"ok": False, "error": "..."}` for
  app-level errors;
- frontend fetch code should normalize both direct and wrapped payloads before
  reading fields.

The guidance must include this TypeScript pattern or an equivalent one:

```ts
const raw = await response.json();
const data = raw && typeof raw === "object" && "body" in raw ? raw.body : raw;
if (!response.ok || data?.ok === false || data?.error) {
  throw new Error(data?.error || `Request failed: ${response.status}`);
}
```

## Authentication UI Rules

When an app has login or registration, the embedded guidance must say:

- login/register are the only public UI flows;
- starter placeholder screens must be replaced. The root route must redirect to
  login, render login, or render the authenticated app based on session state;
  it must not keep the Trustable starter/welcome template;
- before marking auth UI complete, assistants must inspect the router and the
  component used by `/` or `#/`, and must remove or replace generated starter
  content such as `Welcome`, `Try the following prompts to start`, `Powered by
  Trustable`, `trustable.png`, or sample prompt lists. A protected app is
  incomplete if the browser-visible home page still shows the starter screen;
- protected navigation items such as dashboards, contacts, orders, settings,
  admin, or profile must be hidden until the user is authenticated;
- direct protected routes must also be guarded: an unauthenticated user opening a
  protected hash route directly must be redirected to login/register or shown
  the auth view, not the protected page;
- after login, the frontend should store only the returned session/token/user
  data it needs and derive authenticated UI state from that data or from a
  bounded `me` check;
- protected navigation must include an explicit logout path;
- browser-visible identity such as `user_id=1` must not be hardcoded in fetch
  URLs or request bodies. The backend must derive the current user from
  authenticated request state, such as a token/session header, not from a user id
  supplied by the browser.

## Setup And Data Initialization

The embedded guidance must say:

- all initialization belongs in private actions in package `setup`;
- setup actions must be incremental, idempotent, and non-destructive;
- table creation belongs in `setup/database`;
- Redis key preparation belongs in `setup/cache`;
- Milvus collection creation belongs in `setup/collection`;
- private S3 data preload belongs in `setup/upload`;
- public web assets belong in `public/`, not in setup uploads;
- run `ops ide setup` after creating or changing setup actions.

The embedded guidance must also say that `ops ide setup` must succeed before
setup work is complete. If setup returns `Cannot start action. Check logs for
details.`, assistants must immediately run `timeout <seconds> ops logs --last`
and fix the first traceback. They must not create missing tables or seed rows
with PostgreSQL MCP write tools and then claim setup succeeded; the `setup/*`
action must be able to recreate the state idempotently.

## Dependencies

The embedded guidance must say:

- frontend dependencies are added to `package.json`, then installed with
  `npm install`;
- Python dependencies are added only with `action-requirements`;
- before importing a non-stdlib Python package such as `bcrypt`, `jwt`,
  `requests`, or a database driver, assistants must add it with
  `action-requirements` and redeploy the action;
- if action logs show `ModuleNotFoundError`, assistants must fix the dependency
  or import before doing any other validation and must not mark the feature
  complete;
- editor, LSP, TypeScript, lint, and tool diagnostics that mention generated or
  edited files are validation failures. Assistants must fix the diagnostic or
  explain why it is stale with a successful bounded command that proves it;
- PostgreSQL, Redis, S3, Milvus, and secrets are added with the corresponding
  action/service tool, not by manually editing generated wrapper code or
  hardcoding credentials.

## Data And Service Restrictions

The embedded guidance must tell assistants to retrieve the current user with
`ops util whoami` when needed. It must include these restrictions:

- PostgreSQL database is named after the user; the default schema is
  `<user>_schema`.
- Milvus database is named after the user.
- Redis writable keys must be prefixed with `<user>:`.
- S3 writable buckets are `<user>-data` for private app data and `<user>-web`
  for public web assets.
- The S3 MCP cannot list buckets, so assistants should assume only the two
  user buckets above are writable.

## Validation Checklist

The embedded guidance must end backend-related changes with local proof:

- after changing an action module, run the appropriate deploy/redeploy path;
- after changing setup actions, run `ops ide setup`;
- validate public actions with bounded HTTP checks against
  `/api/my/<package>/<action>`;
- treat `ops action invoke` as insufficient proof when it only prints an
  activation id such as `ok: invoked ...`; assistants must inspect the action
  result/logs or validate through the HTTP endpoint;
- if any action reports `Cannot start action`, `application error`, or
  `developer error`, run `timeout <seconds> ops logs --last` before changing
  strategy;
- verify JSON request fields, method, and headers are visible to the action;
- verify frontend fetch handling accepts the response shape actually returned;
- treat editor, LSP, TypeScript, lint, and tool diagnostics that mention
  generated or edited files as validation failures unless a successful bounded
  command proves they are stale;
- use bounded checks such as `timeout <seconds> ...` and `curl`;
- never hide validation failures with `|| true`, forced zero exits, or output
  truncation that can mask the first error. Checks must fail loudly so the
  assistant fixes the failure;
- for frontend auth flows, validate route shape and behavior for the root path,
  login path, register path, direct protected route while logged out, and
  protected navigation after login;
- never leave the user with only "try it now" unless validation was impossible
  and the blocker is stated.

## Matching Spec References

This file is the source of truth for embedded assistant guidance. Related
runtime generation details live in:

- `spec/2a-config.md`: `opencode.json` generation and writing embedded
  `opencode.md`;
- `spec/4-launch.md`: MCP server generation, service CLI wrappers, and launch
  behavior;
- `spec/7-skills.md`: installed app skills under `.agents/skills`.
