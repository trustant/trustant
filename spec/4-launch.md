This file describes the api for launching.
Put the code in the file `launch.go`

<local.prefix> is `/usr/bin` on Linux and `/opt/homebrew/bin/` on Mac

# GET /api/launch/<name>

When invoking this api it should check the folder
<workspacedir>/workspace/<name> exists, if not return

```
{ "error": <error> }
```

## terminate leftover processes

If a `<workbenchdir>/pgid` file exists, forcefully terminate the process
group pointed to by that file (same teardown as `DELETE /api/launch`), then
remove the `pgid` file.

If there is **no** `pgid` file but the opencode (4096) or opsdevel (5173) port
is still being listened on, an orphaned process from a previous launch is
holding the port — reclaim each busy port by killing whatever is listening on
it (looked up by port, e.g. via `lsof`), so the later "check ports" step does
not fail spuriously.

Either way, also remove a stale `<workbenchdir>/current` file (a `current`
without a matching running session is left over from a previous launch).

The "check ports" step below applies the same port reclaim as a last-resort
fallback before returning a "port not available" error.

## clone to workbench

`<workbenchdir>` is exposed as the stable path used by Trustable and OpenCode,
normally `/home/trustable/workbench`. In the pod it must survive image rebuilds
and restarts by pointing into the persistent workspace volume
(`/home/trustable/workspace/workbench`). This keeps OpenCode's persistent recent
project paths valid.

At server startup, after stale process cleanup, scan
`<workspacedir>/workspace/*` for valid local git repos. For each app whose
`<workbenchdir>/<name>` checkout is missing, clone the durable workspace repo
back into the workbench and regenerate `.env` files. Do not run `ops ide login`,
`ops ide deploy`, or start Vite/OpenCode during this restore; the full per-app
runtime setup still happens only when `/api/launch/<name>` is called.

If `<workbenchdir>/<name>` already exists, keep it (continue previous work) and skip to the login step.

Otherwise, clone the workspace into the workbench:

`git clone <workspacedir>/workspace/<name> <workbenchdir>/<name>`

Then set up the workbench:

- Generate `.env` and `.env.production` in `<workbenchdir>/<name>` from the merged config using `generateAppEnvFiles(<name>)`.
The `.env` contains `OPS_USER`, `OPS_PASSWORD`, `OPS_APIHOST` (fixed), global env defaults, and per-app development overrides. The `.env.production` contains per-app production values.
- If `<workbenchdir>/<name>/package.json` exists, run `npm install` in `<workbenchdir>/<name>`

When the workbench already exists (reuse path), also regenerate the `.env` files
to keep them in sync with the current config. If a restored checkout has
`package.json` but no `node_modules`, run `npm install` during launch before the
app runtime starts.

## opencode project bookkeeping

After the workbench is ready (clone or reuse), bind the workbench to a stable
OpenCode project identity and clear stale links from previous launches:

- Write a deterministic project id (`sha1("trustable:" + <name>)`, hex) into the
  workbench's git dir as `<gitdir>/opencode`, so OpenCode always resolves this
  app to the same project.
- Open OpenCode's local DB at `~/.local/share/opencode/opencode.db` (if present)
  and remove any `project_directory` row or `project.sandboxes` entry that points
  at this workbench path but belongs to a *different* project id. This prevents an
  old project from claiming the directory.

Both steps are best-effort — failures are logged, never fatal to a launch.

## skills

Clone/refresh the app's bundled skills into `<workbenchdir>/<name>/.agents/skills/`
(see [7-skills.md](7-skills.md)). Track whether anything was added so it can be
reported in the launch response (`skills_added`).

## ensure OpenWhisk user

Make sure the OpenWhisk/whisk user backing this app exists and its password is in
sync before logging in:

- Read the live password with `ops util kubeget whiskuser/<name> .spec.password`.
- If the user does **not** exist: recreate it using the password from the
  workbench `.env` (`OPS_PASSWORD`, generated above), falling back to the stored
  config password (`apps.<name>.password`) only if `.env` has none, via
  `ops admin adduser <name> <name>@n7s.co <password> --all`. If neither source
  has a password, return `{ "error": ... }` (cannot recreate).
- If the user **does** exist but the live password differs from the stored one,
  adopt the live password: persist it to the workspace config
  (`apps.<name>.password`) and regenerate the `.env` files.

## login

Change to `<workbenchdir>/<app>` folder
and execute `ops ide login`

If it terminates with 0 continue otherwise return error

After `ops ide login` succeeds, regenerate the app `.env` before `ops ide clean`
and `ops ide deploy`. Login refreshes `~/.ops/config.json` with the current app
service bindings. Service credentials such as MongoDB must stay out of the
editable `.env`; if an action wrapper needs `MONGODB_URI`, Trustable derives it
from the post-login config and passes it only to the launch/deploy process
environment.

## generate an opencode.json in project directory as follows

the generation must happen AFTER the ops ide login (to retrieve the config) but BEFORE launching opencode (otherwise it won't start)

There is a **single, self-contained** `opencode.json` written into the app's
project directory `<workbenchdir>/<app>/opencode.json`. There is **no** global
`~/.config/opencode/opencode.json` — it is not generated and not referenced.
The project file holds the entire config: provider, model defaults
(`model` / `small_model`), `disabled_providers`, `instructions`, `lsp`,
`permission`, a 64-step limit for the primary `build` and `plan` agents, and
the `mcp` servers built from `~/.ops/config.json`. The
provider block, model
defaults, and the full-regeneration rules (no merge — the file is overwritten
every launch) are exactly those in
[2a-config.md](2a-config.md#prepare-opencode-config), except the file is written
to the project directory instead of `~/.config/opencode/`.

`.openserverless-contract.md` and `opencode.md` are written **alongside** the
config in the project directory. `instructions` references the project's own
canonical, symlink-resolved `<workbenchdir>/<app>/.openserverless-contract.md`
first and `<workbenchdir>/<app>/opencode.md` second (not absolute `~/.config`
paths). These paths must use the same canonical root as the OpenCode session;
otherwise OpenCode treats its mandatory rules as external files and asks the
user for an unnecessary permission.
The OpenServerless action tools are no longer written as embedded plugin files;
they are provided by the `openserverless` MCP server wired into the `mcp`
section (see below).

The generated `mcp` section must also always contain the local
`trustable-browser-mcp` server. Its environment contains a browser artifact
directory under `$WORKSPACE_DIR/.trustable/browser/<app>` and the external
origin derived as `<protocol>://vite.<configured-apihost>`. No browser
credentials or generated app `.env` variables are added.
Browser snapshots expose bounded stable control refs and observable
AudioContext/media state so frontend verification can prove form and sound
behavior instead of relying on source inspection. The session plugin records
successful evidence-bearing browser interactions automatically. It unlocks a
diagnostic fix after reproduction and clears post-change verification only
after fresh evidence for the same task and mutation revision; sound fixes also
require observable active audio state. The manual checkpoint remains a
fallback and binds the latest valid interaction when its internal evidence ID
is omitted. For a reported browser bug, at most eight read-only file discovery
calls are allowed before a browser-only phase makes `browser_interact`
mandatory and rejects shell, file, task, and editor tools.

The launch/config generation also installs
`~/.local/bin/check_openserverless_actions.sh` with executable mode. This
checker is installed once per Trustable user, not duplicated into every app
repo. The generated app contract tells OpenCode to run it with the current app
path before deploy.

The full file looks like (lsp + mcp shown; provider/model/instructions sections
per the rules above):

```
{
  "lsp": {
    "typescript": {
      "command": ["typescript-language-server", "--stdio"],
      "extensions": [".js", ".jsx", ".ts", ".tsx", ".mjs", ".mts", ".cjs", ".cts"]
    },
    "python": {
      "command": ["pylsp"],
      "extensions": [".py"]
    }
  },
  "mcp": <add the servers as follows>
}
```

Read  <config> values ~/.ops/config.json and add the mcp servers and command line utils
as follows:

# always add the openserverless MCP server:

This server exposes the OpenServerless action tools (`action_new`, `action_invoke`,
`action_requirements`, and the `action_add_*` connectors) over MCP, replacing the
old embedded `tools/` plugins. It is installed globally in the image as
`openserverless-mcp` from the repository's `mcp` submodule and is added
unconditionally, independent of `~/.ops/config.json`:

```
"openserverless": {
  "type": "local",
  "command": ["openserverless-mcp"],
  "enabled": true
}
```

The runtime image pins Trustable Code through the `trustable-code` Git subrepo
and verifies its reported version against `OPENCODE_VERSION` in
`image/Dockerfile`. The base-image hash includes the exact subrepo commit, so a
pointer change always rebuilds the agent binary. The image must compile that
pinned source and must not download a separate OpenCode binary from
`opencode.ai`. Native compiler dependencies needed by upstream packages belong
only to the disposable Trustable Code builder stage and must not be copied into
the runtime image.

The Lima development setup must follow the same rule: `setup.sh` builds the
current pinned `trustable-code` submodule with the Bun version declared by the
Dockerfile and installs it as the user's `~/.local/bin/opencode`. Matching only
`OPENCODE_VERSION` is insufficient because upstream OpenCode can report the
same version. Setup must also record the installed submodule revision and
verify that the binary contains the `TRUSTABLE_RUNTIME_CONFIG` contract.

`GET /api/launch/<name>` must fail before cloning, login, deploy, or process
cleanup when the resolved `opencode` binary does not contain that Trustable
runtime contract. Edit must never silently fall back to upstream OpenCode.

Whenever that pin changes, the image build must install
`@opencode-ai/plugin` at the exact version returned by
`/usr/local/bin/opencode --version`; a mismatch is a build/runtime regression.

After generating the app-local `opencode.json`, Trustable writes a protected
runtime manifest to `~/.config/trustable/opencode-runtime.json` with mode
`0600`. It contains the current app, canonical workbench path, managed local
development URL (`http://localhost:5173`), and the names of MCP servers enabled
in the generated config. It contains no credentials. Trustable passes its path
to the child process through `TRUSTABLE_RUNTIME_CONFIG`; this is process
configuration and must never be copied into the app `.env` or
`.env.production`.

`trustable-app` consumes `trustable-ai/trustable-code` as a pinned Git subrepo.
The pointer advances only after Trustable Code core tests and Trustable E2E
tests pass for the candidate commit.

See [trustable-code-integration.svg](trustable-code-integration.svg) for the
source, build, launch, and runtime-contract flow.

The runtime image installs `openserverless-mcp` from the local `mcp` submodule,
not from a direct `github:apache/openserverless-mcp` npm reference. The image
build stages that submodule into the Docker build context and includes the
submodule commit in the base-image hash, so changing the MCP pointer forces the
base image to rebuild.

# if the app uses AgentiReact add the agentireact MCP server:

Check the app's Vite config — `<workbenchdir>/<app>/vite.config.*` (either
`vite.config.js` or `vite.config.ts`). If that file exists and its contents
contain `AgentiReact()`, the running app exposes an MCP endpoint over HTTP at
`http://localhost:5173/mcp` (the `opsdevel` dev server on port 5173). Add a
remote MCP server pointing at it:

```
"agentireact": {
  "type": "remote",
  "url": "http://localhost:5173/mcp",
  "enabled": true
}
```

If no `vite.config.*` exists or none contains `AgentiReact()`, skip this server.

# if config.s3.host is defined and not empty add:

```
"s3": {
  "type": "local",
  "command": ["mcp-s3"],
  "environment": {
    "S3_ENDPOINT": "http://<config.s3.host>:<config.s3.port>",
    "AWS_ACCESS_KEY_ID": "<config.s3.access.key>",
    "AWS_SECRET_ACCESS_KEY": "<config.s3.secret.key>",
    "S3_USE_PATH_STYLE": "true"

  },
  "enabled": true,
  "timeout": 30000
}
```

and create in ~/.local/bin/rclone the script:

```
#!/bin/bash
export PATH=<local.prefix>
export RCLONE_CONFIG_S3_TYPE=s3
export RCLONE_CONFIG_S3_PROVIDER=SeaweedFS
export RCLONE_CONFIG_S3_ACCESS_KEY_ID='<config.s3.access.key>'
export RCLONE_CONFIG_S3_SECRET_ACCESS_KEY='<config.s3.secret.key>'
export RCLONE_CONFIG_S3_ENDPOINT='http://<config.s3.host>:<config.s3.port>'
export RCLONE_CONFIG_S3_REGION=us-east-1
export RCLONE_CONFIG_WEB_TYPE=alias
export RCLONE_CONFIG_WEB_REMOTE=s3:<config.s3.bucket.static>
export RCLONE_CONFIG_DATA_TYPE=alias
export RCLONE_CONFIG_DATA_REMOTE=s3:<config.s3.bucket.data>
mkdir -p ~/.config/rclone && touch ~/.config/rclone/rclone.conf
exec rclone "$@"
```

# if config.postgres.database is defined and not empty add:

```
"postgres": {
  "type": "local",
  "command": ["postgres-mcp", "--access-mode=unrestricted"],
  "environment": {
    "DATABASE_URI": "<config.postgres.url>"
  },
  "enabled": true,
  "timeout": 30000
}
```

and create ~/.local/bin/psql with the body

```
#!/bin/bash
export PATH=<local.prefix>
exec psql "<config.postgres.url>" "$@"
```

# if config.redis is defined and not empty add:

```
"redis": {
  "type": "local",
  "command": ["redis-mcp-server",
    "--host", "<config.redis.service>",
    "--port", "<config.redis.port>",
    "--username",  '<config.redis.prefix with last char removed>',
    "--password", '<config.redis.password>'
  ],
   "environment": {
    "REDIS_USERNAME": "<config.redis.prefix with last char removed>",
    "REDIS_HOST": "<config.redis.service>",
    "REDIS_PORT": "<config.redis.port>",
    "REDIS_PWD": "<config.redis.password>",
   },
  "enabled": true,
  "timeout": 30000
}
```

and create in ~/.local/bin/redis-cli like this:

```
#!/bin/bash
export PATH=<local.prefix>
export REDISCLI_AUTH='<config.redis.password>'
exec redis-cli -h '<config.redis.service>' --user '<config.redis.prefix with last char removed>' -p '<config.redis.port>' "$@"
```

The `action-add-redis` / `action_add_redis` connector injects `ctx.REDIS` and
`ctx.REDIS_PREFIX` into action wrappers. Generated app guidance and the checker
must require editable action modules to build every Redis key from
`ctx.REDIS_PREFIX` plus an app-local suffix. Naked Redis keys are invalid
because Nuvolaris Redis ACLs only allow the configured user prefix.

# if config.milvus is defined and not empty add:

```
"milvus": {
  "type": "local",
  "command": ["mcp-server-milvus", "--milvus-token", "<config.milvus.token>", "--milvus-db", "<config.milvus.db.name>", "--milvus-uri", "http://<config.milvus.host>:<config.milvus.port>"],
  "environment": {
    "MILVUS_URI": "http://<config.milvus.host>:<config.milvus.port>"
  },
  "enabled": true,
  "timeout": 30000
},
```

and create in ~/.local/bin/milvus_cli rendering this template:

```
#!{{.PythonVenv}}
import sys
from pymilvus import MilvusClient

from milvus_cli.scripts.init_client_cli import get_milvus_cli_obj
from milvus_cli.scripts.milvus_client_cli import runCliPrompt

HOST = "{{.Host}}"
PORT = {{.Port}}
TOKEN = "{{.Token}}"
DB_NAME = "{{.DbName}}"

def auto_connect():
    """Connect to the configured host and database before the REPL starts."""
    uri = HOST if "://" in HOST else f"http://{HOST}:{PORT}"

    # Pass db_name at construction time: switching to a db *after* connecting
    # to `default` can trip PERMISSION_DENIED for db-scoped users.
    params = {"uri": uri}
    if TOKEN:
        params["token"] = TOKEN
    if DB_NAME:
        params["db_name"] = DB_NAME

    obj = get_milvus_cli_obj()
    conn = obj.connection
    try:
        conn.client = MilvusClient(**params)
    except Exception as e:
        print(f"Auto-connect failed: {e}", file=sys.stderr)
        return

    conn.uri = uri
    conn.connection_params = params
    conn._is_connected = True
    if DB_NAME:
        conn.set_current_database(DB_NAME)

    print(f"Connected to {uri}" + (f" (db: {DB_NAME})" if DB_NAME else ""))


if __name__ == "__main__":
    if sys.argv[0].endswith(".exe"):
        sys.argv[0] = sys.argv[0][:-4]
    if "--version" not in sys.argv:
        auto_connect()
    sys.exit(runCliPrompt())

```

with:

{{.PythonVenv}} = first line of <local.prefix>/milvus_client
{{.Host}} = <config.milvus.host>
{{.Port}} = <config.milvus.port>
{{.Token}} = <config.milvus.token>
{{.DbName}} = <config.milvus.db.name>

# if config.mongodb is defined and has a connection string add:

MongoDB follows the same service-MCP gating as S3, PostgreSQL, Redis, and
Milvus. It is generated only from the official post-login config surface read
from `~/.ops/config.json`, never from arbitrary workbench environment
variables. Enable it when either:

- `config.mongodb.uri`, `config.mongodb.url`, or
  `config.mongodb.connection_string` is non-empty;
- top-level `config.MONGODB_URI` is non-empty and was produced by the
  OpenServerless login/config path;
- `config.mongodb.host` and `config.mongodb.database` are both present, in
  which case derive `mongodb://<user>:<password>@<host>:<port>/<database>` from
  the same official block, with optional `auth_source` rendered as
  `authSource`.

Do not enable MongoDB MCP from a casual `MONGODB_URI` in a copied `.env` file.
If the official MongoDB capability is absent, the app agent must treat MongoDB
as `non configurato` rather than asking the user for infrastructure details or
mapping MongoDB to Milvus/vector search.

`MDB_MCP_CONNECTION_STRING` is private to the generated MongoDB MCP server
process. It must not be documented or used as an app action runtime variable.
When the same official MongoDB capability is present, launch/deploy may expose
`MONGODB_URI=<resolved mongodb connection string>` only in the internal process
environment used by OpenCode and `ops ide deploy`/`ops ide devel`; it must not
be written to the app `.env` or shown in the app environment editor. This is the
action runtime binding consumed by `action-add-mongodb` / `action_add_mongodb`;
the assistant must use that tool to generate a wrapper that reads the official
`MONGODB_URI` action parameter and exposes `ctx.MONGODB_CLIENT` plus
`ctx.MONGODB` to business modules.
The app agent and checker must also reject guessed action env vars such as
`MONGODB_URI`, `MONGO_URL`, `MONGO_CONNECTION_STRING`, or
`MDB_CONNECTION_STRING` unless a generated action wrapper exposes an official
MongoDB binding.

```
"mongodb": {
  "type": "local",
  "command": ["mongodb-mcp-server"],
  "environment": {
    "MDB_MCP_CONNECTION_STRING": "<resolved mongodb connection string>"
  },
  "enabled": true,
  "timeout": 30000
}
```

The Trustable runtime image installs the official MongoDB MCP server at build
time as `mongodb-mcp-server`, so launch must not use `npx` or download packages
at runtime.

The Trustable runtime image wraps the external `mcp-s3` binary. The real binary
is kept as `/usr/local/bin/mcp-s3-real`, while `/usr/local/bin/mcp-s3` filters
known-invalid bucket-listing tools and normalizes `buckets: null` to `[]`.
When installed for Lima development under `~/.local/bin`, the wrapper must
discover an adjacent `mcp-s3-real` before falling back to the image path.
The wrapper must relay partial stdio reads immediately: MCP initialization
messages are normally smaller than the relay buffer and must not wait for 8 KiB
or end-of-file before reaching the real server.
This prevents OpenCode sessions from failing on S3 MCP schema validation while
keeping non-bucket-listing S3 diagnostics available.

Lima setup must route the `cluster.local` DNS suffix to the live
`kube-system/kube-dns` ClusterIP through `systemd-resolved`. OpenCode and its
MCP subprocesses run on the VM host, while generated service URLs use
`*.svc.cluster.local`; reachable ClusterIPs without service-name resolution do
not constitute a working local environment.


## clean

Execute `ops ide clean` in `<workbenchdir>/<app>`.

If it terminates with 0 continue otherwise return error.

## deploy

Execute `ops ide deploy` in `<workbenchdir>/<app>`.

If it terminates with 0 continue otherwise return error.

## check ports

Assume `opencode` port will be 4096.

Assume `opsdevel`  port will be 5173.

Check if ports for `opencode` and `opsdevel` are free,
otherwise return error.

## prepare opencode configuration and environment

Before starting opencode, (over)write the single self-contained
`<workbenchdir>/<app>/opencode.json` in the project directory from the current
Trustable config, as described in "generate an opencode.json" above (provider,
model/small_model defaults, `disabled_providers`, `instructions`, `lsp`, and the
`mcp` servers from `~/.ops/config.json`). There is no global
`~/.config/opencode/opencode.json` — do not generate, symlink, or copy one.

Note: `AGENTS.md`, `opencode.md`, and `.openserverless-contract.md` are written
into the project directory alongside `opencode.json`. `AGENTS.md` is the
Trustable-managed app-local rules entrypoint and must explicitly demote
template compatibility files such as `CLAUDE.md` to non-authoritative legacy
notes. If an app already has `AGENTS.md`, Trustable updates only its managed
block and preserves app-local notes below it. The checker is installed once at
`~/.local/bin/check_openserverless_actions.sh`. The `instructions` array
references the project's own canonical, symlink-resolved
`<workbenchdir>/<app>/.openserverless-contract.md` first and
`<workbenchdir>/<app>/opencode.md` second, matching the OpenCode session root.
The action tools come from the
`openserverless` MCP server, not from an embedded `tools/` folder.
The checker must accept sibling `.zip` files created by `ops ide deploy`, such
as `packages/v1/contacts.zip`. It must fail on ZIP files created inside action
source directories, missing sibling deploy archives, and action source files
newer than their deploy archive. These failures require `ops ide deploy`; ZIP
files must never be repaired manually.
It must not flag standard generated `__main__.py` PostgreSQL wiring as business
logic merely because the wrapper imports `psycopg`, reads `POSTGRES_URL`, and
assigns `ctx.POSTGRESQL`.
For setup/seed modules, bulk `INSERT INTO` logic should warn only when no
obvious idempotency guard exists. Explicit seed markers and
`SELECT COUNT(*) FROM ...` checks are accepted as low-noise guards.

The generated OpenCode `permission` block must allow normal edits while denying
direct assistant edits to `packages/**/__main__.py` and `packages/**/*.zip`, and
must deny raw shell commands matching `ops action` / `ops action *`. These
guards keep action creation/repair on the OpenServerless MCP path and keep
deployment on
`ops ide deploy/setup`. The checker is still authoritative for drift that a
shell command or copied file could create: it must fail on action modules
without generated wrappers and on hand-authored wrappers that define `main()`
without generated action/service markers.

Trustable must install the session-enforcement plugin as the auto-loaded local
plugin `~/.config/opencode/plugins/trustable-guardrails.js`. The plugin listens for
`session.compacted`, injects a short critical contract into every system prompt,
and exposes `trustable_context_recover`, `trustable_diagnostic_checkpoint`, and
`trustable_completion_check`. After compaction it automatically injects a
bounded recovery packet with the exact active real user request, contracts,
sanitized config, git status, and a bounded file map before tools run. The
manual recovery tool is a fallback only if that gate explicitly remains
active. It blocks mutations while recovery is pending, blocks
speculative edits for a reported bug until reproduction evidence is recorded,
opens a circuit breaker after two equal completion failures, and prevents
unverified completion claims. Any action MCP call or source mutation under
`packages/` marks action deployment as required. Until a successful
`ops ide deploy` is followed by a passing action checker, the plugin blocks
`ops ide setup` and the completion gate. A setup-action mutation additionally
marks setup as required; deploy does not clear that state, and completion stays
blocked until a successful `ops ide setup` runs after deploy.
For shell tools, action detection must inspect both the command text and the
normalized `cwd`/`workdir`/`directory` argument. A mutating command such as
`sed -i module.py` executed from `packages/v1/action` still requires deploy;
the same command from `packages/setup/action` also requires setup. Manual ZIP
guards must use the same working-directory detection.
The plugin must also reject failure-masking forms of critical commands,
including `|| true`, `|| echo`, and `head`/`tail` pipelines applied to login,
deploy, setup, Trustable checkers, or frontend builds.
It must reject shell commands that kill processes or start `vite`,
`npm run dev`, or `ops ide devel`, because those processes are owned by the
Trustable launch lifecycle.

See [action-deploy-guard-flow.svg](action-deploy-guard-flow.svg).

Trustable must also install `check_trustable_frontend.sh` and the aggregate
`check_trustable_app.sh` once in `~/.local/bin`. The aggregate checker runs the
existing OpenServerless checker plus high-confidence frontend checks, including
root-relative internal anchors used with `HashRouter` and passwords placed in
request URLs. With `HashRouter`, it must also reject `Link`, `NavLink`,
`Navigate`, or `navigate(...)` targets beginning with `#/`; router APIs receive
logical paths such as `/login` and add the hash themselves.
It must reject hardcoded `user_id` values in frontend requests and
bearer-authenticated requests that also send a browser-controlled `user_id`;
protected actions derive identity from the validated token/session.
It must also reject a frontend that initializes authenticated state directly
from a cached localStorage user/profile without an observable backend
`me`/session validation. A full reload keeps an explicit loading state,
validates the token and its expiry, and clears cached identity on failure.

After generating `opencode.json`, also generate `<workbenchdir>/<app>/.mcp.json`
in the **Claude Code** format, containing every MCP server from the generated
opencode.json `mcp` section (including `openserverless`, `browser`, the optional
`agentireact`, and any of `s3`/`postgres`/`redis`/`milvus`/`mongodb` that were added). This
keeps the same servers available to Claude-format clients for compatibility.

Translate each opencode server entry to Claude's `mcpServers` schema:

- a `type: "local"` server with `command: [cmd, arg1, ...]` and an optional
  `environment` map becomes a stdio server: `{ "type": "stdio", "command": cmd,
  "args": [arg1, ...], "env": { ... } }` (omit `env` when there is no
  environment block).
- a `type: "remote"` server with a `url` (e.g. `agentireact`) becomes
  `{ "type": "http", "url": "<url>" }`.

Drop opencode-only fields (`enabled`, `timeout`). The file shape is:

```
{
  "mcpServers": {
    "openserverless": { "type": "stdio", "command": "openserverless-mcp", "args": [] },
    "agentireact":    { "type": "http",  "url": "http://localhost:5173/mcp" }
  }
}
```

Also normalize any OpenCode agent metadata under
`<workbenchdir>/<app>/.opencode/agent/*.md`: rewrite each agent's `color:`
frontmatter to one of OpenCode's accepted values (`primary`, `secondary`,
`accent`, `success`, `warning`, `error`, `info`, or a `#rrggbb` hex), mapping
common color names (e.g. `blue`→`primary`, `green`→`success`, `red`→`error`) and
defaulting anything unrecognized to `primary`. Best-effort — failures are logged.

Launch `opencode serve` with the variables from the workbench `.env` appended to
the process environment.

## start process group

Let <directory> be the canonical absolute path of `<workbenchdir>/<app>` after
resolving symlinks. This matters in the pod because `/home/trustable/workbench`
can point at the persistent `/home/trustable/workspace/workbench`; OpenCode
stores sessions by the resolved worktree path, so the launch response and
session lookup must use the same canonical value to preserve/reopen previous
sessions.

Execute  opencode changing to this directory as

```
opencode serve --port 4096 --hostname 0.0.0.0 --log-level DEBUG --print-logs
```

with out and err in stdout and stderr.

Get its process group.

Ensure it does not terminate within .5 seconds
If it terminates return error

Write the process group in `<workbenchdir>/pgid`

Write the app name in `<workbenchdir>/current`

Execute `ops ide devel`  in <directory> using the same process group as opencode

Check the command does not terminate within .5 seconds.

If it terminates, kill the whole process group and remove  `<workbenchdir>/pgid`, and return error

Wait that both the processes are up and running and ports are listening.

When ok, execute a POST to the opencode session endpoint with header
"X-Opencode-Directory: <directory>" and log the result of this invocation.

This launch bootstrap is a pod-local sidecar call and must target
`http://127.0.0.1:4096/session/`. Browser-visible OpenCode traffic is different:
`opencode.<domain>` must route through the Trustable ingress/proxy path on port
8910, where the middleware scopes project and directory requests before
proxying to the same pod-local OpenCode server.

then return:

`{
  "left" : <opencode-port>,
  "right": <opsdeve-port>,
  "b64dir": <base64-urlsafe-encoded directory>,
  "encdir": <absolute directory>,
  "session_id": <id returned by the opencode session POST, or "">,
  "skills_added": <true if skills were freshly added this launch>
}`

Base64-Url-Safe encode is as follows:
btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=/g, "")

# GET, POST /api/opencode/sessions/<app>

Validate `<app>`, resolve its canonical existing workbench directory, and list
up to 20 persistent root sessions from the pod-local OpenCode server using the
same directory scope and `X-Opencode-Directory` header as launch. Return the
OpenCode session array as JSON for `GET`.

For `POST`, create a new OpenCode session with the same canonical directory
scope and return HTTP 201 with `{ "id": <session-id> }`. The endpoint powers
the session picker in `app.html`; it never starts a second OpenCode process,
deletes history, or replaces the application workbench.


# DELETE /api/launch

Read the app name from `<workbenchdir>/current`.

Terminate forcefully the process group you started
and written in `<workbenchdir>/pgid`.
Delete the file `<workbenchdir>/pgid`.
Delete the file `<workbenchdir>/current`.

Do NOT remove the `<workbenchdir>/<name>` directory (it persists for reuse on next launch).

# GET /api/redeploy?name=<app>

This endpoint redeploys actions and restarts the `ops ide devel` process without restarting opencode. It streams progress via Server-Sent Events (SSE).

Response content type: `text/event-stream`

## Validate

- Check `name` query parameter is valid and workbench exists at `<workbenchdir>/<name>`
- Check that a pgid file exists (there must be a running session to redeploy)

## Step 1: Terminate ops ide devel

Find and terminate only the `ops ide devel` child process (not the entire process group — opencode must keep running):

- Run `pgrep -g <pgid>` to list all PIDs in the process group
- For each PID, check if its command line (via `ps -p <pid> -o args=`) contains `ops ide devel`, `vite`, or `5173`
- Send SIGTERM to the matched devel PIDs, then wait up to 5 seconds for them to exit
- If they don't exit, send SIGKILL to the matched PIDs

Stream: `event: status` / `data: Terminating ops ide devel...`

## Step 2: Wait for port 5173 to be free

Poll until port 5173 is no longer listening (timeout: 10 seconds). If it doesn't free up, send `event: error` and return.

Stream: `event: status` / `data: Waiting for port 5173 to be free...`

## Step 3: Deploy actions

Execute `ops ide deploy` in `<workbenchdir>/<name>`. If it fails, send `event: error` and return.

Stream: `event: status` / `data: Deploying actions (ops ide deploy)...`

## Step 4: Get action list

Execute `ops action list` in `<workbenchdir>/<name>` and capture the output.

Stream: `event: status` / `data: Getting action list...`

## Step 5: Start ops ide devel --fast

- Execute `ops ide devel --fast` in `<workbenchdir>/<name>`, joining the existing process group (same `pgid` as opencode)
- Redirect stdout/stderr to os.Stdout/os.Stderr
- Check the command does not terminate within 0.5 seconds; if it does, send `event: error` and return

Stream: `event: status` / `data: Starting dev server (ops ide devel --fast)...`

## Step 6: Wait for dev server to be ready

Send repeated HTTP HEAD requests to `http://127.0.0.1:5173/` every 500ms until the server responds (timeout: 30 seconds). If it doesn't respond, send `event: error` and return.

Stream: `event: status` / `data: Waiting for dev server to be ready...`

## Done

Send the action list captured in step 4 as the data of the `done` event.

Stream: `event: done` / `data: <output of ops action list>`

## On error

At any step, if an error occurs:

Stream: `event: error` / `data: <error message>`
