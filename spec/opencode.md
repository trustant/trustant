# Embedded `opencode.md`

> **HISTORICAL RECORD.** Trustable no longer launches OpenCode and no longer
> generates `opencode.json` or a project-local `opencode.md`. The still-relevant
> application guidance is now folded into the managed `AGENTS.md` and
> `CLAUDE.md` files used by TruACP/Pi. References below to the OpenCode runtime,
> plugin, session gates and generated OpenCode files are not active behaviour.
> See [pi.md](pi.md) and [4-launch.md](4-launch.md) for the current contract.
>
> The active projection embeds the full managed block directly in `AGENTS.md`
> and mirrors it in `CLAUDE.md`. Generated guidance must identify the managed
> `AGENTS.md` block, `.openserverless-contract.md`, and `.mcp.json` as its only
> separate project sources; it must explicitly say that project-local
> `opencode.md` does not exist. Immutable workbench, browser-origin, and MCP
> requirements come from the host-owned issue #57 runtime manifest described in
> [trustable-pi-runtime.md](trustable-pi-runtime.md).
>
> The active Pi projection also supersedes every historical instruction below
> that tells an assistant to run `ops ide deploy`. Launch performs one initial
> deploy before starting `ops ide devel`; during a live Edit session that
> managed watcher is the sole deploy owner. Pi reads its canonical log, runs
> the action checker once for source-contract validation, and performs real
> HTTP checks. Managed live checker mode ignores sibling ZIP
> existence/freshness. The issue #57 extension blocks manual
> `ops ide deploy`, additional `ops ide devel`, and direct shell inspection or
> polling of `packages/**/*.zip`.

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

The runtime guardrail plugin must normalize the project directory supplied by
the pinned OpenCode plugin API. It accepts a path string, file URL, or the
directory/worktree/path fields of a structured context and falls back to the
process working directory. A plugin API shape change must not disable the
issue98 guardrails during startup.

Verification and recovery gates may block an unverified completion claim, but
must not suppress a user's explicit request for status or a recap. For such a
request the assistant's truthful response remains visible and the plugin adds a
short deterministic statement of the still-pending gate instead of replacing
the entire response with an instruction loop.
When a normal turn stops at a diagnostic, browser verification, or completion
gate, the plugin must replace the premature answer with synthetic internal
feedback and request another provider turn. Trustable Code persists this
feedback for the model, hides it from the session UI, and keeps the active user
request running. The user sees only the final verified answer or an explicitly
requested status response, never control-plane gate instructions.

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
- the checker analyzes application action sources, but must prune generated or
  vendored dependency trees such as `virtualenv`, `.venv`, `venv`,
  `node_modules`, and `__pycache__`;
- missing or stale archives require another `ops ide deploy`, never a manual
  ZIP repair; other hard failures require contract recovery, a source repair,
  another deploy, and another checker run;
- if the contract is missing or the checker is unavailable in PATH, assistants
  must report that and fall back to `opencode.md`;
- after compaction, assistants must not continue from memory. The generated
  Trustable plugin must automatically inject a bounded recovery packet with the
  exact active real user request, `opencode.md`,
  `.openserverless-contract.md`, sanitized `opencode.json`, git status, and a
  bounded project map before tools run;
- the plugin must block source/action/deploy mutations while that automatic
  recovery is pending. `trustable_context_recover` remains a fallback only
  when the automatic gate explicitly remains active;
- guardrail state must live under the OpenCode durable data root,
  `$XDG_DATA_HOME/opencode/trustable-guardrails` or
  `~/.local/share/opencode/trustable-guardrails`, not under an expendable cache.
  Existing state under `~/.cache/trustable/opencode-guardrails` must be read and
  migrated when the durable file is absent. If durable state is corrupt, the
  plugin must fail closed and require recovery; if durable writes fail, it may
  conservatively fall back to the legacy location;
- reported bugs must be reproduced before source changes. A successful,
  evidence-bearing `browser_interact` records browser reproduction
  automatically; `trustable_diagnostic_checkpoint` remains available for
  explicit or non-browser evidence. The completion tool runs at most once for
  each source revision and at most three times for one real user request. A
  repeated call must return concise guidance without rerunning checks; changing
  placeholder tests solely to reset the revision is forbidden;
- with the integrated Trustable Code runtime, task classification and
  diagnostic transitions belong to the core agent state machine rather than
  the plugin. Feature requests containing labels such as `Problems` or
  `Errors` must not activate diagnostic mode. Pending diagnostics remove
  no longer hide or reject mutation tools. The integrated runtime must not
  repeatedly replace a normal final answer with automatic completion recovery.
  When source changed and no completion check ran, it may request exactly one
  internal turn to call `trustable_completion_check`; browser debt alone does
  not create a hidden continuation. If the provider stops without text,
  Trustable renders a concise visible status instead of leaving an empty
  assistant message. Stricter recovery behavior remains limited to legacy
  non-integrated OpenCode runtimes;
- when the reproduction used the browser, every subsequent source change must
  require fresh post-change `browser_interact` evidence bound automatically to
  the current task and mutation revision before completion. Audio fixes must
  expose active audio state; suspended audio is not valid verification
  evidence. The manual checkpoint remains a fallback and must bind the latest
  valid evidence deterministically when its internal ID is omitted;
- every frontend source mutation must require fresh Browser MCP evidence before
  completion, including feature work that did not begin as a reported bug.
  After a successful build or deploy, the embedded workflow must direct the
  assistant to inspect the exact changed route and runtime diagnostics
  immediately, then exercise the visible flow before speculative source edits.
  A browser open without a subsequent evidence-bearing interaction is not
  sufficient. Only application controls may supply evidence: Agentic React
  Select, Multiselect, Done, Adjust selection, and toolkit controls must be
  disabled in the headless QA context and rejected by the guardrail. A stale
  runtime that still exposes them must close the browser and use an explicit
  typecheck/build/test/runtime-diagnostics fallback rather than retry them;
- subagent work must be bounded to one question, at most eight relevant files,
  concise paths/line references, and capped tool output. Full-file or whole
  codebase delegation must be rejected before it consumes session context;
- browser bug diagnosis must allow at most eight `read`, `glob`, `grep`, or
  `list` inspections before reproduction. Once exhausted, a browser-only phase
  must reject shell, file, task, and editor tools until a successful
  `browser_interact` supplies observable evidence;
- the frontend checker must reject protected views that initialize user/session
  data to null, load it asynchronously, and redirect on that null value before
  the request has completed; such views need an explicit loading state;
- the frontend checker must reject localStorage user/profile data used as
  authoritative authentication without a backend `me`/session validation;
- after source changes, `trustable_completion_check` must pass the action and
  frontend checkers, `git diff --check`, the project typecheck, and the
  available frontend build before the assistant claims completion. When no
  `typecheck` script exists but a local TypeScript compiler and `tsconfig.json`
  do, the gate runs `tsc -b --noEmit --incremental false` so Vite-only builds
  cannot hide undefined JSX symbols or leave `.tsbuildinfo` artifacts.

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
- use Redis for cache, ephemeral state, and authenticated app sessions;
- use MongoDB for document data only when the official MongoDB capability is
  configured;
- use Milvus for vector search;
- never use Milvus as a replacement for MongoDB;
- use the Agentic React MCP only when the Vite config imports/references
  `@agentic-react/vite` and invokes `AgenticReact()`.

## OpenServerless Action Tools

The embedded guidance must tell assistants to use the Trustable/OpenServerless
MCP action tools instead of manually creating platform scaffolding.

The `openserverless` MCP server is always generated in `opencode.json`. It
exposes the action tools, replacing the old embedded `tools/` plugin files.
Depending on the client, tool names may appear with hyphens or underscores; the
instructions should name the Trustable concepts and tell the assistant to use
the matching exposed tool:

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
- `auth-setup` / `auth_setup`: after every authentication endpoint exists,
  atomically add Redis wiring to the complete token-issuing,
  protected/session, and logout endpoint sets. It never reads or writes
  `.env`, and it must not configure JWT or an application signing secret.
- `action-add-milvus` / `action_add_milvus`: add Milvus service wiring.
- `secret-unbind` / `secret_unbind`: atomically remove an obsolete generated
  secret binding without reading or deleting the value. This is the supported
  recovery path for a legacy invalid managed-variable binding. A successful
  removal must be followed, for every changed endpoint, by
  `ops ide undeploy <endpoint>` and `ops ide deploy <endpoint>`, because an
  action update alone preserves parameters already present in OpenWhisk.
  `ops ide clean` is not a replacement because it removes only local artifacts.

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

Every package and action segment must start with a letter and contain only
letters, numbers, and hyphens. The guidance and MCP schema must recommend flat
hyphenated names such as `v1/employees-photo`, and must reject underscores,
spaces, and forms such as `v1/employees_photo` before any action mutation.

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
clearer, names must remain flat and hyphenated, such as `v1/contacts-list` or
`v1/orders-create`.

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
- `react` is always present and runs `trustable-react-mcp`. It resolves only
  the manifest-selected workbench and exposes read-only project inspection,
  route/auth validation, and aggregate TypeScript/React validation. After
  frontend mutations, assistants must resolve aggregate `react_validate`
  errors before Browser MCP verification.
- `agentireact` is present only when `vite.config.js` or `vite.config.ts`
  imports/references `@agentic-react/vite` and invokes `AgenticReact()` in
  executable config code; it is an HTTP MCP server at
  `http://localhost:5173/mcp`. Comments, strings, wrong packages, and the
  obsolete `AgentiReact()` spelling do not enable it. Adding the plugin while
  an app is running requires relaunching the app so Trustable regenerates
  `.mcp.json`.
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
`ctx.REDIS_PREFIX` and adds the required Python Redis client dependency to the
action package. Assistants must build every Redis action key from
`ctx.REDIS_PREFIX` plus an app-local suffix, using a helper such as
`redis_key(ctx, name)`. The guidance must forbid naked Redis keys passed
directly to `ctx.REDIS.get/set/delete/hset/hget/lpush/sadd/expire/...`, because
Nuvolaris Redis ACLs allow only the configured user prefix.
The checker must also reject any editable module that uses `ctx.REDIS` or
`ctx.REDIS_PREFIX` when its generated wrapper lacks the Redis connector.
Authentication recovery must point to `auth_setup` with the complete endpoint
set; unrelated single-endpoint Redis use points to `action_add_redis`.

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

The embedded guidance must declare application `.env` and `.env.production`
immutable to agents and MCP servers. Only the user-facing Trustable
configuration interface may change their source values. Assistants must not
read, create, edit, import, synchronize, regenerate, or automatically populate
those files; they report a missing variable to the user.

For authenticated pages, the guidance must require Redis-backed opaque
sessions rather than JWT/application-secret authentication. After every login,
registration, `me`/session, protected-resource, and logout action exists, the
assistant calls `auth-setup` / `auth_setup` once with the complete endpoint
sets. The tool atomically adds Redis wiring to all of them and never reads or
writes `.env`; `action-add-redis` / `action_add_redis` remains the
single-endpoint connector for non-authentication Redis use.
Login/registration creates a cryptographically random token, Redis stores its
token-to-identity mapping with a bounded TTL under a `ctx.REDIS_PREFIX` key,
protected actions validate that record and derive identity from it, and logout
deletes it. The browser stores only the opaque token.

The managed Pi extension must allow this Redis-only `auth_setup` tool while
blocking the obsolete environment-mutating `secret_ensure`. It must also block
direct writes to generated `packages/**/__main__.py` wrappers and
`packages/**/*.zip` artifacts, and block mutating service-MCP calls such as
`postgres_execute_sql`. Read-only service discovery and verification remain
available. Schema, seed, and application writes must be reproducible through
setup or public OpenServerless actions.

The embedded guidance must say that S3 app verification uses the OpenServerless
action path created with `action-add-s3`, generated action wiring, configured
user buckets, or the companion `rclone` wrapper. S3 credentials are
bucket-scoped, so assistants must never call `ctx.S3_CLIENT.list_buckets()`.
Neither `head_bucket` nor bucket/object listing proves read/write access. A
read/write check must use a unique temporary key in `ctx.S3_DATA`, execute
`put_object`, execute `get_object` and compare the returned body bytes, and
execute `delete_object` in a `finally` block. It may report `read_write: OK`
only after the byte comparison succeeds. S3 MCP list-bucket schema failures are
diagnostic tool failures, not sufficient reason to abandon the app
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
- `OPS_APIHOST` is the configured OpenServerless API host used by Trustable and
  `ops ide` for login, deploy, and development proxy orchestration. It is not
  an application secret or action parameter. The guidance and checker must
  forbid `#--param OPS_APIHOST "$OPS_APIHOST"`, `ctx.OPS_APIHOST`, and action
  module reads of `OPS_APIHOST`.

The guidance must tell frontend code to call actions through relative
`/api/my/<package>/<action>` URLs so the browser preserves its current origin.
Actions must not call sibling actions through `OPS_APIHOST`, browser-visible
hosts, or ingress URLs. Independent endpoints should be called by the frontend;
server-side aggregation must use generated service bindings in one action.

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
- the browser must treat the session token as opaque. The deterministic React
  validator must reject `jwtDecode`, equivalent JWT libraries, and manual
  base64 claim decoding even when no component or route is literally named
  login; identity comes from the backend `me`/session endpoint;
- successful login and registration must update the live authentication
  provider/store before protected-route navigation; writing token/user data
  only to browser storage is insufficient because the current render remains
  unauthenticated and may redirect back to login;
- successful registration must establish the same authenticated state as
  login, either from returned session/token/user data or an immediate login;
  the user must not be sent through a second manual login before reaching the
  protected area;
- protected navigation must include an explicit logout path;
- login, registration, `me`/session, every protected action, and logout must
  share the Redis opaque-session contract and generated Redis wiring. JWT or
  an application signing secret is not an alternative;
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
- `OPS_USER`, `OPS_PASSWORD`, `OPS_APIHOST`, `OPS_REPO`, and `OPS_SKILLS` are
  Trustable-managed orchestration variables and must be rejected by generic
  secret tools rather than bound into action wrappers.

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
  protected navigation after login; submitting valid credentials must visibly
  render the protected page without a manual reload;
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

## Trustable Code Core Workflow

When `TRUSTABLE_RUNTIME_CONFIG` is active, Trustable Code owns the execution
workflow independently of generated prompt files. Every real user message
updates the persisted active task. Source and deployment mutation tools remain
available immediately: no `todowrite` call is a prerequisite for writing code.
For genuinely multi-step work the agent may keep an optional plan of three to
seven meaningful milestones, but a failed, interrupted, empty, malformed, or
stale plan never blocks implementation.

The workflow prioritizes executable application code over planning,
documentation, speculative architecture, or placeholder tests. Project
governance and specifications are still honored, but documentation is updated
only when implementation introduces a real behavior or design decision.
Genuinely missing functional input is requested with OpenCode's structured
`question` tool; the agent must never delegate shell commands to the user.

Integrated compaction is deterministic. It creates a bounded checkpoint from
the persisted task and plan, successful file mutations, recent actual tool
outcomes, and mandatory host context without a provider call. Failed or
interrupted mutation bodies and unsigned historical reasoning are omitted from
future model history. The session resumes by rereading current source and
continuing the current plan.

While a turn is busy, the Trustable Code UI derives visible progress from real
tool events and displays planning, exploration, editing, verification, or
context-preparation activity with elapsed time. It must not show private
reasoning or control-plane gate text as progress.

Starting a new user turn clears the previous turn's todo dock immediately. The
dock remains hidden until the current turn writes its own plan, preventing stale
tasks from being presented as current work.

The integrated runtime never injects a hidden completion-recovery turn after a
normal final answer. `trustable_completion_check` is used near the end of
substantial implementation, once per source revision and no more than three
times per real request. Failures are reported concretely and must not cause
placeholder-test churn. Read-only inspection of a checker path is distinct from
executing and masking that checker's exit status.

Generated Trustable configurations cap the primary `build` agent at 128
provider steps and the `plan` agent at 64 provider steps per user turn. On the
last step Trustable Code must remove ordinary tools from the provider request
and end the loop even if the provider still emits a tool call. A maximum-step
instruction without enforcement is not a valid loop guard. Trustable Code must
also count identical repaired `invalid`
tool calls across separate provider steps. Three consecutive calls with the
same requested tool and normalized error must end the turn immediately with a
concise visible explanation; any successful different tool or new user request
resets that circuit.

Verification repetition must also be compared semantically across provider
steps. Equivalent requests to the same endpoint with different `curl` flags,
or alternating an endpoint request with activation-log inspection, are not
fresh progress. Three occurrences of the same target and failure class without
a source mutation must stop the turn with the concrete target and failure. A
deploy or another environment-only mutation is not a source mutation and must
not reset the failure count. A successful matching verification, a source
mutation, or a new user request resets this state.

The generic MCP resource tools must advertise the exact connected servers that
declare the MCP `resources` capability. Requests naming a connected tools-only
server, such as S3, must be skipped before reaching that server and return a
successful explanatory result listing the resource-capable servers. Capability
mismatches must not appear as MCP connection failures or start a retry loop.

Errors generated by Trustable's internal guards are agent control-plane input
and must not appear as tool-error rows in the user timeline. Provider
self-repair attempts such as unavailable tools, schema-invalid inputs, and
rejected endpoint names are also hidden while remaining available in model
history. Application and ordinary tool failures remain visible.

Trustable Code sanitizes final assistant text before persistence and the app
sanitizes it again while rendering. Internal completion checks, gates,
guardrails, circuit breakers, checkpoints, and continuation state must never be
shown to the user; useful result and limitation text remains visible. When a
provider text block contains only control-plane narration, it is suppressed
instead of being replaced with a misleading failure message while the turn is
still running.

Scheduled application activities are not yet part of the implemented Trustable
Code contract. Their approved requirements are tracked in `spec/backlog.md`.
