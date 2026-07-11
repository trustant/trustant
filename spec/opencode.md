# Embedded `opencode.md`

This file specifies the `opencode.md` guidance embedded into the Trustable
binary and written into every launched app as
`<workbenchdir>/<app>/opencode.md`. The generated `opencode.json` must reference
that project-local file in its `instructions` array, after the generated
`<workbenchdir>/<app>/.openserverless-contract.md` critical contract.
Trustable also writes an app-local `AGENTS.md` guard file that OpenCode uses as
the project rules entrypoint. `AGENTS.md`, `.openserverless-contract.md`,
`opencode.md`, and `opencode.json` are the authoritative Trustable instruction
sources.

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
- validation must use bounded checks, pod-local app checks through
  `localhost:5173`, and real action invocations.

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
2. Critical recovery contract.
3. Non-negotiable rules.
4. Project layout.
5. Application development workflow.
6. OpenServerless action tools.
7. Action endpoint grammar.
8. MCP servers and service access.
9. Runtime host rules.
10. PostgreSQL action pattern.
11. Skills.
12. Web action request rules.
13. Web action response rules.
14. Authentication UI rules.
15. Setup and service initialization.
16. Dependencies.
17. Data/service restrictions.
18. Validation checklist.

## Critical Recovery Contract

The embedded guidance must have an early section named
`Critical Recovery Contract`.

It must say:

- Trustable generates `AGENTS.md` in the app root as the mandatory app-local
  entrypoint, so Claude Code compatibility files cannot override Trustable
  rules;
- assistants must treat `AGENTS.md`, `.openserverless-contract.md`,
  `opencode.md`, and `opencode.json` as the authoritative instruction set;
- assistants must ignore `CLAUDE.md`, `CONTEXT.md`, `.cursorrules`,
  `.cursor/rules/*`, `.github/copilot-instructions.md`, and generated
  `rules.md` files as mandatory instructions. They may inspect those files only
  when the user explicitly asks or when they are useful legacy/template context,
  and they must never override Trustable action, MCP, deploy, shell, or host
  rules;
- before touching actions, databases, setup, seed data, deploys, or service
  state, assistants must read `.openserverless-contract.md` if it exists;
- `.openserverless-contract.md` is the short recovery contract and takes
  priority for OpenServerless workflow details;
- Trustable installs `check_openserverless_actions.sh` once in the user PATH;
  assistants must run `timeout 60 check_openserverless_actions.sh .` after
  deploy and before completion when the checker is available;
- missing or stale archives require another `ops ide deploy`, never a manual
  ZIP repair; other hard failures require contract recovery, a source repair,
  another deploy, and another checker run;
- if the contract is missing or the checker is unavailable in PATH, assistants
  must report that and fall back to `opencode.md`;
- after compaction, assistants must not continue from memory; they must re-read
  `opencode.md`, `.openserverless-contract.md` if present, `opencode.json`,
  git status, and available MCP/tool names before touching action or service
  code;
- the generated Trustable OpenCode plugin must enforce that rule by blocking
  source/action/deploy mutations after `session.compacted` until
  `trustable_context_recover` reloads authoritative guidance, sanitized config,
  git status, and project layout;
- while recovery is pending, the plugin must also replace any attempted final
  response with an instruction to call `trustable_context_recover`;
- guardrail state must live under the OpenCode durable data root,
  `$XDG_DATA_HOME/opencode/trustable-guardrails` or
  `~/.local/share/opencode/trustable-guardrails`, not under an expendable cache.
  Existing state under `~/.cache/trustable/opencode-guardrails` must be read and
  migrated when the durable file is absent. If durable state is corrupt, the
  plugin must fail closed and require recovery; if durable writes fail, it may
  conservatively fall back to the legacy location;
- reported bugs must be reproduced before source changes and recorded through
  `trustable_diagnostic_checkpoint`; two repeated completion failures must open
  a circuit breaker that requires new reproduction evidence. The failure
  signature must normalize volatile timestamps, durations, process/request IDs,
  and temporary paths while preserving the semantic failure text;
- when the reproduction used the browser, every subsequent source change must
  require fresh post-change browser evidence and a
  `trustable_diagnostic_checkpoint` with `phase=verified` before completion;
- the frontend checker must reject protected views that initialize user/session
  data to null, load it asynchronously, and redirect on that null value before
  the request has completed; such views need an explicit loading state;
- after source changes, `trustable_completion_check` must pass the action and
  frontend checkers, `git diff --check`, and the available frontend build before
  the assistant claims completion.

## Non-Negotiable Rules

The embedded guidance must include these rules:

- Never create a backend server. Create public or private actions instead.
- Never create or edit generated `__main__.py` wrappers.
- If OpenCode denies an edit to `packages/**/__main__.py`, `packages/**/*.zip`,
  or a raw shell command matching `ops action` / `ops action *`, assistants must
  treat that as a Trustable guardrail and use the OpenServerless MCP action
  tools plus `ops ide deploy/setup` instead of trying to bypass it.
- Never run foreground dev servers or unbounded watchers such as
  `npm run dev`, `vite`, or `ops ide devel`.
- Never kill, restart, or replace Trustable-managed OpenCode/Vite processes;
  the plugin must reject kill/pkill/killall and manual dev-server starts.
- Never mask deploy, setup, login, checker, or frontend-build failures with
  `|| true`, `|| echo`, or `head`/`tail` pipelines; the generated plugin must
  reject those commands before execution.
- Assistants must not ask the user to run shell commands from inside the
  Trustable pod when the assistant has shell access. They must run bounded
  checks themselves, including `ops ide deploy`, `curl`, `npm run build`,
  `python3 -m compileall`, and `git diff --check`. They may ask the user only
  when shell/tool access is missing or the task requires credentials or
  physical access only the user has.
- Never build or deploy the whole product manually; Trustable manages the
  long-running dev server. Use bounded checks and action deploy/setup commands
  only when needed for validation.
- Put feature code, parsing fixes, auth checks, and business behavior in the
  editable module file:
  `packages/<package>/<action>/<module>.py`.
- After every action MCP call or change under `packages/`, explicitly run
  `timeout 120 ops ide deploy` before setup, verification, or completion.
- A shell mutation remains an action change when its command names only a local
  file and the shell `cwd`/`workdir` is already inside `packages/`; the plugin
  must still require deploy, and setup when the directory is under
  `packages/setup/`. The same endpoint tracking applies when the command enters
  the action inline with `cd packages/<package>/<action> && ...`.
- If setup actions change, run `timeout 120 ops ide setup` only after deploy.
- The generated completion gate must remain blocked until required setup runs.
- Never create, edit, move, or delete action ZIP files manually; deploy owns
  the sibling `packages/<package>/<action>.zip` artifacts.
- Assistants must not use shell redirection to create or replace source files.
  The embedded guidance must explicitly forbid `cat > file`, heredocs, `tee`,
  `printf >`, and `sed -i` for app source or generated wrappers, and should
  tell assistants to use file edit/write tools instead.
- The embedded guidance must reduce stale edit failures. Assistants should
  re-read any file created or changed earlier in the same session before using
  replacement-based edits. For small generated app modules or React pages that
  are being replaced wholesale, assistants should prefer the file write tool
  with the full final content over many incremental `edit` replacements. If an
  `edit` reports `oldString` not found, `No changes to apply`, or identical
  old/new content, assistants must not retry the same edit; they should re-read
  the file, check whether the change is already present, then continue or
  rewrite the file once.
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
7. Run bounded validation against the pod-local app endpoint at
   `localhost:5173`, and use browser-visible FQDN hosts only when external
   routing is in scope.

Frontend diagnosis must use the generated bounded browser MCP when behavior
depends on real navigation, forms, reload, console errors, or network failures.
The embedded guide must tell OpenCode to use `development` mode for the managed
`http://localhost:5173` server and `deployed` mode only after `ops ide deploy`
for the derived `vite.<domain>` host. It must not ask OpenCode to start another
Vite server or browse arbitrary infrastructure URLs.

The embedded guidance must include a concrete backend execution loop:

1. Read `.openserverless-contract.md` if present and run the checker after
   deploy and before completion when it exists.
2. Design action endpoint names and reject invalid nested names before creating
   files.
3. Create actions with the OpenServerless MCP action tool.
4. If an action reads or writes a platform service, immediately add service
   wiring with the matching action/service tool after the action files exist and
   before editing the module logic.
5. Edit only the generated editable module, not `__main__.py`. If the module
   exists without a wrapper, stop and repair/create the action through the MCP
   action tool before continuing.
6. Add Python libraries with `action-requirements`.
7. Run `ops ide deploy` after every action change. If setup actions changed,
   run `ops ide setup` after deploy, inspect failures, then validate via the
   real HTTP app path.

The guidance must tell assistants how to choose the backend shape:

- use a public `v1` action for browser-facing APIs;
- use a private `setup` action for idempotent initialization;
- use S3 for object/file data;
- use PostgreSQL for relational data;
- use Redis for cache/ephemeral state;
- use MongoDB for document data only when the official MongoDB capability is
  configured;
- use Milvus for vector search;
- never use Milvus as a replacement for MongoDB;
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

The embedded guidance must explicitly say not to create or mutate ZIP files
under `packages/`; those sibling artifacts are owned by `ops ide deploy`.
It must explicitly say not to use `ops action deploy`
because it is not an app workflow command. It must also say not to use
`ops action update`, `ops action create`, or raw `ops action` commands as the
normal deploy path for edited app modules. After changing any action, including
setup actions, assistants must run `timeout <seconds> ops ide deploy`. After
changing setup actions, assistants must run `timeout <seconds> ops ide setup`
only after deploy succeeds.

For new public HTTP endpoints, the guidance must say to use package `v1` unless
the user explicitly asks for another package. A public action is reachable at
`/api/my/<package>/<action>`.

For setup and initialization, the guidance must say to create private actions
under package `setup` with `public: false`, and to invoke the complete setup
set with `ops ide deploy` followed by `ops ide setup` after creating or changing
them.

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
- `browser` is always present and runs `trustable-browser-mcp`. It receives
  only the derived deployed Vite origin and an artifact directory outside the
  app repo; development navigation is fixed inside the MCP to
  `http://localhost:5173`.
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
- `mongodb` is present only when MongoDB is configured as an official
  OpenServerless capability in `~/.ops/config.json`.

The guidance must say that service MCP servers are generated from
`~/.ops/config.json` after `ops ide login`, and that missing service blocks mean
the corresponding MCP server is intentionally absent. Assistants must not
hardcode service hosts, ports, credentials, bucket names, database names, or
tokens when the MCP server or generated environment already provides them.
Assistants may inspect `~/.ops/config.json` only to understand which services
exist; they must not copy values from it into app code, wrapper code, logs,
docs, or frontend configuration.

The guidance must say that Redis runtime access comes from
`action-add-redis` / `action_add_redis`, which exposes `ctx.REDIS` and
`ctx.REDIS_PREFIX`. Assistants must build every Redis action key from
`ctx.REDIS_PREFIX` plus an app-local suffix, using a helper such as
`redis_key(ctx, name)`. The guidance must forbid naked Redis keys passed
directly to `ctx.REDIS.get/set/delete/hset/hget/lpush/sadd/expire/...`, because
Nuvolaris Redis ACLs allow only the configured user prefix.

The guidance must say that service MCP servers are assistant-side diagnostics,
not automatic runtime bindings. A successful service MCP call does not prove
that a Python action has an env var, action parameter, or `ctx` binding for the
same service. Runtime service access must come from the OpenServerless action
service tools (`action-add-redis`, `action-add-postgresql`, `action-add-s3`,
`action-add-milvus`, `action-add-mongodb`, and secret wiring) or generated
runtime contract; otherwise the app must report a
deterministic `non configurato`/error state instead of inventing connection
details.

The guidance must say that MongoDB is a document database capability, separate
from Milvus/vector search. If the user asks for MongoDB and the `mongodb` MCP
server or official MongoDB environment is absent, assistants must implement a
deterministic `non configurato`/error state in the app and README. They must
not ask the user how to configure MongoDB, invent connection details, or use
Milvus, `MILVUS_*`, `pymilvus`, or `milvus_cli` as a substitute.
When `action-add-mongodb` / `action_add_mongodb` is exposed, assistants must use
it to generate the MongoDB wrapper and then use `ctx.MONGODB_CLIENT` /
`ctx.MONGODB` from business modules instead of reading connection strings
directly.
The guidance and checker must explicitly forbid app source/action code from
using `MDB_MCP_CONNECTION_STRING`, because it belongs only to the generated
MongoDB MCP server process and is not an app runtime binding.
The checker must also fail action modules that guess MongoDB runtime env vars
such as `MONGODB_URI`, `MONGO_URL`, `MONGO_CONNECTION_STRING`, or
`MDB_CONNECTION_STRING` when no generated MongoDB wrapper/binding exists.

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

The embedded guidance must say that S3 app verification should use the
OpenServerless action path created with `action-add-s3`, generated action
wiring, configured user buckets, or the companion `rclone` wrapper. Assistants
must not rely on S3 MCP bucket listing as app proof; S3 MCP list-bucket schema
failures are diagnostic tool failures, not sufficient reason to abandon the app
implementation.

## Runtime Host Rules

The embedded guidance must classify runtime hosts from OpenCode's point of
view inside the Trustable pod:

- `localhost:5173` is the pod-local app dev server started by `ops ide devel`
  and is the default target for app HTTP validation from OpenCode's shell;
- `localhost:4096` is the pod-local OpenCode server;
- `trustable.<domain>` is the browser-visible Trustable UI/API host;
- `vite.<domain>` is the browser-visible app host through Trustable
  proxy/ingress and must be used only after `ops ide deploy` succeeds and only
  when external browser or ingress routing is in scope;
- `opencode.<domain>` is the browser-visible OpenCode host;
- `OPS_APIHOST` is the configured OpenServerless API host.

The embedded guidance must tell assistants not to invent pod IPs, raw service
names, public domains, or replacement localhost URLs for app verification.
For app endpoint checks from OpenCode's shell, it must prefer:

```bash
curl http://localhost:5173/api/my/<package>/<action>
```

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

For REST-style item routes, the embedded guidance must say not to assume one
fixed `__ow_path` shape. It must tell assistants to use body `id` only as a
fallback, not as the only way update/delete works. It must include a compact
route-id helper or equivalent guidance that supports suffix forms such as
`123`, `/123`, `/contacts/123`, and `/api/my/v1/contacts/123`.

The embedded guidance must also say that CRUD validation is incomplete if it
only calls `/api/my/v1/<resource>` with `{"id": ...}` in the body. Assistants
must test REST-style item routes such as:

```bash
curl -X PUT http://localhost:5173/api/my/v1/<resource>/<id> ...
curl -X DELETE http://localhost:5173/api/my/v1/<resource>/<id> ...
```

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

## Browser-Opened And Printable Actions

The embedded guidance must explicitly cover endpoints opened directly by the
browser through `window.open(...)`, links, or form targets.

It must say:

- direct browser-opened endpoints must return browser-native responses, not
  JSON that merely contains HTML;
- printable HTML features such as invoices, receipts, labels, reports, or
  documents must either return `Content-Type: text/html; charset=utf-8` with
  HTML in the HTTP body, or the frontend must fetch JSON with `Authorization`
  and write the extracted HTML into a new window/document;
- `{"ok": true, "html": html}` is a broken response shape for a direct
  `window.open("/api/my/...")` target;
- assistants must verify opened/downloaded/printable endpoints with
  `curl -i http://localhost:5173/...` and check status plus content type;
- the OpenServerless `~/.ops/config.json` auth value is not an app session
  token;
- token-in-query is acceptable only when a new window cannot send
  `Authorization`, and it must be validated with a real app session token.

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
- with React Router `HashRouter`, `Link`, `NavLink`, `Navigate`, and
  `useNavigate` must receive logical routes such as `/login`, never `#/login`;
  React Router adds the hash. Root-relative internal anchors and router API
  targets beginning with `#/` are blocking frontend checker errors;
- after login, the frontend should store only the returned session/token/user
  data it needs and derive authenticated UI state from that data or from a
  bounded `me` check;
- successful registration must establish the same authenticated state as
  login, either from returned session/token/user data or an immediate login;
  the user must not be sent through a second manual login before reaching the
  protected area;
- protected navigation must include an explicit logout path;
- every form control must have a stable `id`/`name` and an associated label
  (`label htmlFor` matching the input `id`). Repeated placeholders are not
  semantic names. If a browser locator is ambiguous, assistants must fix the
  form accessibility when appropriate or use the browser MCP's explicit
  zero-based `index`; they must not bypass the UI with direct API calls and
  claim the browser flow passed;
- browser-visible identity such as `user_id=1` must not be hardcoded in fetch
  URLs or request bodies. The backend must derive the current user from
  authenticated request state, such as a token/session header, not from a user id
  supplied by the browser. The frontend checker must treat hardcoded user IDs,
  or bearer-authenticated requests that also send browser-controlled `user_id`,
  as blocking errors.

## Setup And Data Initialization

The embedded guidance must say:

- all initialization belongs in private actions in package `setup`;
- setup actions must be incremental, idempotent, and non-destructive;
- table creation belongs in `setup/database`;
- Redis key preparation belongs in `setup/cache`;
- Milvus collection creation belongs in `setup/collection`;
- MongoDB collection/index preparation belongs in an idempotent setup action
  only when MongoDB is configured;
- private S3 data preload belongs in `setup/upload`;
- public web assets belong in `public/`, not in setup uploads;
- run `ops ide deploy` after creating or changing any action, then run
  `ops ide setup` when setup actions changed.

The embedded guidance must also say that `ops ide setup` must succeed before
setup work is complete. If setup returns `Cannot start action. Check logs for
details.`, assistants must immediately run `timeout <seconds> ops logs --last`
and fix the first traceback. They must not create missing tables or seed rows
with PostgreSQL MCP write tools and then claim setup succeeded; the `setup/*`
action must be able to recreate the state idempotently.

The embedded guidance must also forbid live DB-only schema fixes as completion
proof. If assistants use `psql`, PostgreSQL MCP, or ad hoc SQL to inspect or
repair live state while debugging, they must put the equivalent idempotent
migration in `setup/database`, run `ops ide setup`, and read back the
schema/data before marking the task complete.

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
- PostgreSQL, Redis, S3, Milvus, MongoDB, and secrets are added with the corresponding
  action/service tool, not by manually editing generated wrapper code or
  hardcoding credentials.

## Data And Service Restrictions

The embedded guidance must tell assistants to retrieve the current user with
`ops util whoami` when needed. It must include these restrictions:

- PostgreSQL database is named after the user; the default schema is
  `<user>_schema`.
- Milvus database is named after the user.
- MongoDB is available only when the official post-login config exposes a
  MongoDB block or derived connection string.
- Redis action keys must be built with the generated `ctx.REDIS_PREFIX`; the
  assistant must not guess `<user>:` manually or use naked Redis keys.
- S3 writable buckets are `<user>-data` for private app data and `<user>-web`
  for public web assets.
- The S3 MCP cannot list buckets, so assistants should assume only the two
  user buckets above are writable.

## Validation Checklist

The embedded guidance must end backend-related changes with local proof:

- after changing any action module, run `ops ide deploy`;
- run `timeout 60 check_openserverless_actions.sh .` after deploy when the
  checker is available;
- after changing setup actions, run `ops ide deploy` and then `ops ide setup`;
- validate public actions with bounded HTTP checks against
  `http://localhost:5173/api/my/<package>/<action>` from inside the pod;
- for CRUD resources, validate the full create/list/update/delete matrix,
  including `PUT /api/my/v1/<resource>/<id>` and
  `DELETE /api/my/v1/<resource>/<id>` without relying only on `id` in the JSON
  body;
- for browser-opened or printable endpoints, validate with `curl -i` and prove
  the response status and content type match the browser use case. A direct
  `window.open("/api/my/...")` target for printable HTML must not return
  `application/json`;
- use `vite.<domain>` only after deploy and only for explicit external
  browser/ingress checks;
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

- `spec/2a-config.md`: `opencode.json` generation, writing embedded
  `.openserverless-contract.md` / `opencode.md`, and installing
  `check_openserverless_actions.sh`;
- `spec/4-launch.md`: MCP server generation, service CLI wrappers, and launch
  behavior;
- `spec/7-skills.md`: installed app skills under `.agents/skills`;
- `spec/opencode-guardrail-flow.svg`: flow diagram for contract, checker,
  deploy, and runtime verification.
