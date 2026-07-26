This file describes the api for launching.
Put the code in the file `launch.go`

`<local.prefix>` is `/usr/bin` on Linux and `/opt/homebrew/bin/` on Mac for the
`rclone`, `psql`, and `redis-cli` wrappers. The Milvus implementation has a
separate invariant: the upstream `milvus_cli` entry point is installed
globally at `/usr/local/bin/milvus_cli`, while the Trustable-managed
auto-connect wrapper is a regular executable at `~/.local/bin/milvus_cli`.

# GET /api/launch/<name>

When invoking this api it should check the folder
<workspacedir>/workspace/<name> exists, if not return

```
{ "error": <error> }
```

## serialize the shared runtime lifecycle

Launch, `DELETE /api/launch`, and `/api/redeploy` share one lifecycle lock
because every application uses the same truacp port 4096, opsdevel port 5173,
`pgid`, and `current` files. A second browser tab or repeated Edit click must
wait for the in-flight operation instead of racing its deploy and port checks.

After acquiring the lock, a duplicate launch of the same application returns
the existing 4096/5173 response without redeploying when all of the following
are true:

- `current` names the requested application;
- `pgid` identifies a live Trustable-owned process group;
- both truacp and opsdevel accept loopback connections.

Do not reuse a runtime based on a listening port alone. If any ownership or
health condition fails, perform the normal teardown/relaunch path so unrelated
or incomplete listeners are reclaimed.

## terminate leftover processes

If a `<workbenchdir>/pgid` file exists, forcefully terminate the process
group pointed to by that file (same teardown as `DELETE /api/launch`), then
remove the `pgid` file.

If there is **no** `pgid` file but the truacp (4096) or opsdevel (5173) port
is still being listened on, an orphaned process from a previous launch is
holding the port — reclaim each busy port by killing whatever is listening on
it (looked up by port, e.g. via `lsof`), so the later "check ports" step does
not fail spuriously.

Either way, also remove a stale `<workbenchdir>/current` file (a `current`
without a matching running session is left over from a previous launch).

The "check ports" step below applies the same port reclaim as a last-resort
fallback before returning a "port not available" error.

`lsof` is a declared runtime dependency in both the development setup and the
production image; orphan recovery must not depend on it being accidentally
present in one base environment.

## clone to workbench

`<workbenchdir>` is exposed as the stable path used by Trustable and the agent,
normally `/home/trustable/workbench`. In the pod it must survive image rebuilds
and restarts by pointing into the persistent workspace volume
(`/home/trustable/workspace/workbench`). This keeps the agent's persistent recent
project paths valid.

At server startup, after stale process cleanup, scan
`<workspacedir>/workspace/*` for valid local git repos. For each app whose
`<workbenchdir>/<name>` checkout is missing, clone the durable workspace repo
back into the workbench and regenerate `.env` files. Do not run `ops ide login`,
`ops ide deploy`, or start Vite/truacp during this restore; the full per-app
runtime setup still happens only when `/api/launch/<name>` is called.

If `<workbenchdir>/<name>` already exists, keep it (continue previous work) and skip to the login step.

Otherwise, clone the workspace into the workbench:

`git clone <workspacedir>/workspace/<name> <workbenchdir>/<name>`

Then set up the workbench:

- Generate `.env` and `.env.production` in `<workbenchdir>/<name>` from the merged config using `generateAppEnvFiles(<name>)`.
The `.env` contains `OPS_USER`, `OPS_PASSWORD`, `OPS_APIHOST` (fixed), global env defaults, and per-app development overrides. The `.env.production` contains per-app production values.
- These are Trustable-owned generated artifacts. Agents and MCP servers may
  not read, create, edit, import, synchronize, or regenerate them; only the
  user-facing Trustable configuration flow changes their source values.
- If `<workbenchdir>/<name>/package.json` exists, run `npm install` in `<workbenchdir>/<name>`

When the workbench already exists (reuse path), also regenerate the `.env` files
to keep them in sync with the current config. If a restored checkout has
`package.json` but no `node_modules`, run `npm install` during launch before the
app runtime starts.

## agent project bookkeeping

None. Under pi there is no project-identity bookkeeping at launch: no
deterministic project id written into the workbench git dir, and no agent
database to prune. Both steps existed only for OpenCode's `opencode.db` project
registry and were removed with it.

The only path resolution that remains is `canonicalWorkbenchPath`, which
resolves `<workbenchdir>/<app>` through symlinks (the pod's
`/home/trustable/workbench` may point into the persistent workspace volume) so
truacp is always launched with the same absolute `--dir`.

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

## generate the per-app project assets

The generation must happen AFTER `ops ide login` (which refreshes
`~/.ops/config.json`, the source of the MCP servers) and BEFORE truacp is
started.

There is **no per-app agent config file**. `opencode.json` is not written. Pi's
model configuration is global under `~/.pi/agent/` and is produced only by the
configure flow (see [pi.md](pi.md)). Launch does not mutate or repair global Pi
state; users without `pi.default` must complete Configure before opening an app.
Both `applist.html` and the launch API enforce this guard so a stale/direct tab
is routed back to `configure.html?setup=1`.

The server preflight may restore those global JSON files from
`<WorkspaceDir>/.trustable/pi-agent-config/`, or reconstruct a missing initial
snapshot from an already-selected workspace configuration after a pod image
replacement. This happens before serving launch requests and does not make
launch a configuration writer.

The removed OpenCode generator is not retained as an unreachable compatibility
path: the Go binary does not embed a session-enforcement JavaScript plugin and
the Node test package does not depend on `@opencode-ai/plugin`. This keeps the
hard cutover observable in both shipped code and dependency metadata.

What launch writes into `<workbenchdir>/<app>/` is:

- `.mcp.json` — the standard MCP config (`mcpServers` map), built from
  `~/.ops/config.json` plus the always-present managed servers. This is the
  single MCP surface; pi reads it via the `pi-mcp-adapter` extension. It is
  **fully regenerated** on every launch, so a stale managed value (e.g. an old
  postgres `DATABASE_URI`) is never carried forward and hand-edits are
  discarded. Every entry uses `lifecycle: "eager"` so its initial connection is
  attempted when the Pi session starts;
- `AGENTS.md` and `CLAUDE.md` — the Trustable-managed instruction block. The
  long assistant guidance (formerly a separate `opencode.md`) is folded into the
  managed block; `CLAUDE.md` is a full duplicate. Existing app-local notes
  outside the markers are preserved;
- `.openserverless-contract.md` — the short critical action/DB recovery
  contract.

After these files exist, launch writes the credential-free issue #57 runtime
manifest described in [trustable-pi-runtime.md](trustable-pi-runtime.md). Its
required server names come from the exact generated `.mcp.json`, its workspace
is the canonical active checkout, and its browser-visible `browserUrl` is
derived from the incoming `trustable.<domain>[:port]` request by replacing only
the `trustable` label with `vite`. It must not use `OPS_APIHOST`, `localhost`, or
an inferred deployment hostname for that browser-facing field. The separate
`developmentUrl` remains `http://localhost:5173`, which Browser MCP may use only
after matching that canonical current workbench.
The same version-2 manifest declares the private host-owned
`ops ide devel` log under `~/.config/trustable/runtime/<app>/`. Launch creates
it with mode `0600` before managed Pi starts; app content cannot supply or
modify the path.

Plus, once per Trustable user in `~/.local/bin`: `check_openserverless_actions.sh`,
`check_trustable_frontend.sh`, and `check_trustable_app.sh`, all executable and
not duplicated into every app repo. The generated app contract tells the agent to
run the OpenServerless checker with the current app path for source-contract
validation. During managed Edit it runs after canonical watcher evidence and
does not act as an archive/deploy gate.

There is no `lsp` configuration: pi has no language-server config surface, so
`typescript-language-server` and `pylsp` are no longer configured or supervised
by Trustable.

The OpenServerless action tools are not written as embedded plugin files; they
are provided by the `openserverless` MCP server wired into `.mcp.json` (see
below).

`.mcp.json` must always contain the local `trustable-browser-mcp` server. Its
environment contains a browser artifact directory under
`$WORKSPACE_DIR/.trustable/browser/<app>` and the external origin derived as
`<protocol>://vite.<configured-apihost>`. No browser credentials or generated app
`.env` variables are added. Browser snapshots expose bounded stable control refs
and observable AudioContext/media state so frontend verification can prove form
and sound behavior instead of relying on source inspection.

> Note: the evidence-gating behaviour that used to accompany the browser MCP
> (automatic recording of evidence-bearing interactions, the
> reproduction-before-fix unlock, the post-change verification gate, the
> diagnostic checkpoint fallback, and the eight-read-only-call budget before a
> browser-only phase) was implemented by the OpenCode session plugin and is
> **gone** — see "Guardrails" in [pi.md](pi.md). The browser
> tools themselves are unchanged.

## MCP servers

`.mcp.json` is emitted in the standard form below. The implementation may reuse
the established service-config builder internally and translate its entries at
the final write boundary. Launcher-only fields (`enabled`, `timeout`,
`type: "local"`/`"remote"`, command arrays, and `environment`) must never appear
in the written file.

```
{
  "mcpServers": {
    "<name>": { "type": "stdio", "command": "<cmd>", "args": [...], "env": { ... }, "lifecycle": "eager" },
    "<name>": { "type": "http",  "url": "<url>", "lifecycle": "eager" }
}
```

`args` and `env` are omitted when empty. `lifecycle: "eager"` is mandatory for
every generated server; unlike `keep-alive`, it starts the initial connection
without imposing permanent automatic reconnect behavior.

Read <config> values from ~/.ops/config.json and add the mcp servers and command line utils
as follows:

# always add the openserverless MCP server:

This server exposes the OpenServerless action tools (`action_new`, `action_invoke`,
`action_requirements`, and the `action_add_*` connectors) over MCP, replacing the
old embedded `tools/` plugins. It is installed globally in the image as
`openserverless-mcp` from the repository's `mcp` submodule and is added
unconditionally, independent of `~/.ops/config.json`:

```
"openserverless": {
  "type": "stdio",
  "lifecycle": "eager",
  "command": "openserverless-mcp"
}
```

The entry deliberately has no writable persistent-secret-store environment.
Application env values remain owned by the Trustable configuration UI; the
agent reports a missing variable instead of synthesizing or synchronizing it.

Pi extensions (`pi-mcp-adapter`, `pi-web-access`) are pinned in
[trustable-acp/pi.version](../trustable-acp/pi.version), one
`<module>@<version>` npm install spec per line. The `pi` executable/core and
`pi-acp` are built from separately pinned nested Trustable forks under
`trustable-acp/pi` and `trustable-acp/pi-acp`; setup must never replace either
with a public npm runtime. An unpinned manifest entry aborts the install.

The runtime image installs `openserverless-mcp` from the local `mcp` submodule,
not from a direct `github:apache/openserverless-mcp` npm reference. The image
build stages that submodule into the Docker build context and includes the
submodule commit in the base-image hash, so changing the MCP pointer forces the
base image to rebuild.

# always add the deterministic React MCP server:

This read-only server statically validates the current React/Vite workbench.
It resolves its root exclusively from the version-2 Trustable runtime manifest
and exposes `react_project_inspect`, `react_validate_routes`,
`react_validate_auth_flow`, and aggregate `react_validate`:

```
"react": {
  "type": "stdio",
  "lifecycle": "eager",
  "command": "trustable-react-mcp"
}
```

It is independent from Agentic React. After a frontend mutation, managed Pi
must run aggregate `react_validate` and resolve error findings before Browser
MCP verification.

# if the app uses Agentic React add the agentireact MCP server:

Check the app's Vite config — `<workbenchdir>/<app>/vite.config.*` (either
`vite.config.js` or `vite.config.ts`). Enable this server only when executable
config code both imports/references `@agentic-react/vite` and invokes the
plugin as `AgenticReact()`; comments, strings, the obsolete `AgentiReact()`
spelling, a wrong package, or only one of the two conditions are not an opt-in.
When enabled, the running app exposes an MCP endpoint over HTTP at
`http://localhost:5173/mcp` (the co-located `opsdevel` dev server on port
5173). This `localhost` is intentional runtime-local connectivity, not a
browser-visible or configured API host. Add an HTTP MCP server pointing at it:

```
"agentireact": {
  "type": "http",
  "lifecycle": "eager",
  "url": "http://localhost:5173/mcp"
}
```

If no supported Vite config exists or the complete import-plus-invocation
contract is absent, skip this server. Detection occurs when project assets are
regenerated at app launch; installing or removing the plugin during a live
session requires relaunching the app before `.mcp.json` changes.

# if config.s3.host is defined and not empty add:

```
"s3": {
  "type": "stdio",
  "lifecycle": "eager",
  "command": "mcp-s3",
  "env": {
    "S3_ENDPOINT": "http://<config.s3.host>:<config.s3.port>",
    "AWS_ACCESS_KEY_ID": "<config.s3.access.key>",
    "AWS_SECRET_ACCESS_KEY": "<config.s3.secret.key>",
    "S3_USE_PATH_STYLE": "true"
  }
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
  "type": "stdio",
  "lifecycle": "eager",
  "command": "postgres-mcp",
  "args": ["--access-mode=unrestricted"],
  "env": {
    "DATABASE_URI": "<config.postgres.url>"
  }
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
  "type": "stdio",
  "lifecycle": "eager",
  "command": "redis-mcp-server",
  "args": [
    "--host", "<config.redis.service>",
    "--port", "<config.redis.port>",
    "--username", "<config.redis.prefix with last char removed>",
    "--password", "<config.redis.password>"
  ],
  "env": {
    "REDIS_USERNAME": "<config.redis.prefix with last char removed>",
    "REDIS_HOST": "<config.redis.service>",
    "REDIS_PORT": "<config.redis.port>",
    "REDIS_PWD": "<config.redis.password>"
  }
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
The checker must also fail an editable module that uses `ctx.REDIS` when the
generated wrapper lacks the Redis connector, directing complete authentication
surfaces to `auth_setup` and unrelated single endpoints to `action_add_redis`.

Generated authentication guidance uses Redis-backed opaque sessions. Every
login, registration, `me`/session, protected-resource, and logout action must
be created before one `auth-setup` / `auth_setup` call receives the complete
token, protected/session, and logout endpoint sets. That tool atomically adds
Redis wiring without reading or writing `.env`; `action-add-redis` /
`action_add_redis` remains the single-endpoint connector for unrelated Redis
use. Session tokens are cryptographically random and opaque; the
token-to-identity mapping lives in Redis with a bounded TTL, every key uses
`ctx.REDIS_PREFIX`, and logout deletes the mapping. JWT/application-secret
authentication is not the generated app contract.

# if config.milvus is defined and not empty add:

```
"milvus": {
  "type": "stdio",
  "lifecycle": "eager",
  "command": "mcp-server-milvus",
  "args": ["--milvus-token", "<config.milvus.token>", "--milvus-db", "<config.milvus.db.name>", "--milvus-uri", "http://<config.milvus.host>:<config.milvus.port>"],
  "env": {
    "MILVUS_URI": "http://<config.milvus.host>:<config.milvus.port>"
  }
},
```

and create `~/.local/bin/milvus_cli` as an atomically installed regular
executable rendering this template:

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

{{.PythonVenv}} = interpreter from the first line of the exact global entry
point `/usr/local/bin/milvus_cli`
{{.Host}} = <config.milvus.host>
{{.Port}} = <config.milvus.port>
{{.Token}} = <config.milvus.token>
{{.DbName}} = <config.milvus.db.name>

The generator must never search for `milvus_cli` through
`PATH`: `~/.local/bin` is first and would rediscover the wrapper itself. A
missing or unreadable `/usr/local/bin/milvus_cli` is a launch configuration
error. If `~/.local/bin/milvus_cli` is an existing symlink, replace the link
itself with an atomic rename; never follow it and overwrite the upstream
package-managed target. Wrapper-generation failures are returned by launch
without logging credentials.

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
environment used by truacp and `ops ide deploy`/`ops ide devel`; it must not
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
  "type": "stdio",
  "lifecycle": "eager",
  "command": "mongodb-mcp-server",
  "env": {
    "MDB_MCP_CONNECTION_STRING": "<resolved mongodb connection string>"
  }
}
```

The Trustable runtime image installs the official MongoDB MCP server at build
time as `mongodb-mcp-server`, so launch must not use `npx` or download packages
at runtime.

The Trustable runtime image wraps the external `mcp-s3` binary. The real binary
is kept as `/usr/local/bin/mcp-s3-real`, while `/usr/local/bin/mcp-s3` filters
known-invalid bucket-listing tools and normalizes `buckets: null` to `[]`.
The wrapper must relay partial stdio reads immediately: MCP initialization
messages are normally smaller than the relay buffer and must not wait for 8 KiB
or end-of-file before reaching the real server.
This prevents agent sessions from failing on S3 MCP schema validation while
keeping non-bucket-listing S3 diagnostics available.


## clean

Execute `ops ide clean` in `<workbenchdir>/<app>`.

If it terminates with 0 continue otherwise return error.

## deploy

Execute `ops ide deploy` in `<workbenchdir>/<app>`.

If it terminates with 0 continue otherwise return error.

This is the one initial launch-time deploy and occurs before `ops ide devel`
starts. Once launch starts the development watcher, assistants must not invoke
another `ops ide deploy`: the watcher owns live action packaging/deployment and
the managed Pi extension blocks a concurrent deploy command.

## check ports

Assume `truacp` port will be 4096 (this is where truacp listens; `pi-acp`
is its stdio child and does not bind a port).

Assume `opsdevel`  port will be 5173.

Check if ports for `truacp` and `opsdevel` are free,
otherwise return error.

## prepare the agent configuration and environment

Before starting truacp, write the per-app project assets into
`<workbenchdir>/<app>/` as described in "generate the per-app project assets"
above: `.mcp.json`, the managed `AGENTS.md`/`CLAUDE.md`, and
`.openserverless-contract.md`, plus the three `~/.local/bin` checkers.

There is **no per-app agent config file** to write. Pi's model configuration is
global (`~/.pi/agent/models.json`, `settings.json`, `auth.json`) and is written
only by the configure flow. Launch must not change those files. Do not generate,
symlink, or copy any `opencode.json`.

`AGENTS.md` is the Trustable-managed app-local rules entrypoint (with the long
assistant guidance folded into its managed block); `CLAUDE.md` is a full
duplicate of the same managed content so Claude Code sessions pick up the same
rules. If an app already has either file, Trustable updates only its managed
block and preserves app-local notes below it. The action tools come from the
`openserverless` MCP server, not from an embedded `tools/` folder.

`.mcp.json` is written in the standard `mcpServers` form (see "MCP servers"
above) and contains `openserverless`, `browser`, `react`, the optional
`agentireact`, and any of `s3`/`postgres`/`redis`/`milvus`/`mongodb` whose config
block is present. It is the **only** MCP surface: pi reads it via the
`pi-mcp-adapter` extension and Claude-format clients read it natively. There is
no second agent-specific MCP file; any internal legacy-shaped data is translated
only at this write boundary. All listed servers are generated with eager
lifecycle so the session starts with their real connected/error state.

Before spawning TruACP, launch must also validate the installed issue #57
extension, initialize the private rotating watcher log, and atomically publish
the versioned host manifest. It then appends
`TRUSTABLE_MANAGED_RUNTIME=1`, `TRUSTABLE_RUNTIME_CONFIG`, and
`TRUSTABLE_PI_EXTENSION_PATH` only to the TruACP process environment. Missing
or inconsistent inputs abort launch; these values must never enter generated
application env files or maps. Standalone TruACP does not receive this managed
contract.

The companion `rclone`, `psql`, `redis-cli`, and `milvus_cli` wrappers and the
service MCP entries are generated from the same post-login `~/.ops/config.json`
object in one launch. For Redis in particular, the wrapper and MCP must use the
same `service` host and `port`; VM reachability is supplied by the one
namespace-wide `kubefwd` owned by repository-root `run.sh`. The production pod
uses native Kubernetes Service DNS and never starts that VM-only forwarder.

The checker must always reject ZIP files created inside action source
directories and accept generated sibling `.zip` files such as
`packages/v1/contacts.zip`. Outside managed live mode, missing sibling deploy
archives and action source files newer than their archive remain deploy-workflow
errors. With `TRUSTABLE_MANAGED_RUNTIME=1`, it deliberately skips those two
archive-state checks because `trustable_runtime_status` and real HTTP behavior
are authoritative; Pi must never inspect or repair ZIP files manually.
It must not flag standard generated `__main__.py` PostgreSQL wiring as business
logic merely because the wrapper imports `psycopg`, reads `POSTGRES_URL`, and
assigns `ctx.POSTGRESQL`.
For setup/seed modules, bulk `INSERT INTO` logic should warn only when no
obvious idempotency guard exists. Explicit seed markers and
`SELECT COUNT(*) FROM ...` checks are accepted as low-noise guards.

The aggregate `check_trustable_app.sh` runs the OpenServerless checker plus
high-confidence frontend checks, including root-relative internal anchors used
with `HashRouter` and passwords placed in request URLs. With `HashRouter`, it
must also reject `Link`, `NavLink`, `Navigate`, or `navigate(...)` targets
beginning with `#/`; router APIs receive logical paths such as `/login` and add
the hash themselves.
It must reject hardcoded `user_id` values in frontend requests and
bearer-authenticated requests that also send a browser-controlled `user_id`;
protected actions derive identity from the validated token/session.
It must also reject a frontend that initializes authenticated state directly
from a cached localStorage user/profile without an observable backend
`me`/session validation. A full reload keeps an explicit loading state,
validates the token and its expiry, and clears cached identity on failure.

### Narrow secret-access guardrail

Launch installs no OpenCode permission block and no session-enforcement plugin.
The generated `permission` deny rules (`ops action` shell commands, edits to
`packages/**/__main__.py` and `packages/**/*.zip`) and the old
`~/.config/opencode/plugins/trustable-guardrails.js` recovery/completion state
machine are gone. The checkers remain advisory: they can catch implementation
drift after the fact and cannot block an ordinary code or deploy tool call.

Issue #57 installs a deterministic Pi execution-policy extension and the owned
Pi core. Launch supplies only the typed manifest and installed extension path;
the removed `TRUSTABLE_PI_EXTENSION` placeholder remains forbidden. See
"Guardrails" in [pi.md](pi.md).

> [action-deploy-guard-flow.svg](action-deploy-guard-flow.svg) still depicts the
> removed OpenCode guardrail state machine and no longer reflects the
> implementation.

There is also no OpenCode agent-metadata normalization: `.opencode/agent/*.md`
`color:` frontmatter is neither read nor rewritten.

Launch `truacp` (see trustable-acp/SPEC.md §10a) with the variables from the
workbench `.env` appended to the process environment. truacp is the standalone
ACP server that serves its own React UI on `:4096`; it spawns `pi-acp` over
stdio, which spawns the `pi` binary. Trustable never execs the agent directly,
and there is no launch-time session bootstrap (truacp creates the ACP session, in
the `--dir` cwd, on the first prompt; SPEC §10e).

The Trustable `pi-acp` fork always owns `--mode rpc --no-themes`; a versioned,
typed ACP launch extension may add validated extension/skill/prompt/session
paths as discrete arguments, but never arbitrary argv or shell fragments. Pi's
other command-line switches cannot be injected through the browser request.

## start process group

Let <directory> be the canonical absolute path of `<workbenchdir>/<app>` after
resolving symlinks. This matters in the pod because `/home/trustable/workbench`
can point at the persistent `/home/trustable/workspace/workbench`. truacp is
launched with `--dir <directory>`, so the agent's cwd is this resolved path.

Execute truacp changing to this directory as

```
truacp --port 4096 --dir <directory>
```

with out and err in stdout and stderr.

Get its process group. (Killing this group tears down truacp **and** the
`pi-acp`/`pi` children it spawned.)

Ensure it does not terminate within .5 seconds
If it terminates return error

Write the process group in `<workbenchdir>/pgid`

Write the app name in `<workbenchdir>/current`

Execute `ops ide devel` in <directory> using the same process group as truacp.
Mirror stdout and stderr to the normal Trustable console and the private,
two-generation bounded watcher log declared by the manifest.

Check the command does not terminate within .5 seconds.

If it terminates, kill the whole process group and remove  `<workbenchdir>/pgid`, and return error

Wait that both the processes are up and running and ports are listening.

From this point until the app runtime stops, `ops ide devel` is the sole live
deploy owner. Pi reads its redacted authoritative tail through
`trustable_runtime_status`, then runs the action checker once for
source-contract validation and verifies real HTTP routes. Managed live checker
mode ignores sibling ZIP existence/freshness, and the Pi extension blocks
direct shell inspection or polling of `packages/**/*.zip`, masked checker
pipelines, and a second checker call without a relevant source or
OpenServerless-wiring mutation. Pi does not run `ops ide deploy`, another
watcher, repeated checker calls, or infer state from archive paths.

truacp owns all session/cwd state internally, so there is no session POST and no
`X-Opencode-Directory` header. Browser-visible traffic reaches truacp through
the Trustable ingress/proxy path on port 8910: `opencode.<domain>` is a plain
reverse proxy to the pod-local truacp on `:4096` (the middleware no longer
rewrites session/project/directory URLs).

then return:

`{
  "left" : <truacp-port>,
  "right": <opsdevel-port>,
  "skills_added": <true if skills were freshly added this launch>
}`

The app UI iframe loads truacp at `opencode.<domain>/`; no base64-dir/session
URL is constructed anymore.

# DELETE /api/launch

Read the app name from `<workbenchdir>/current`.

Terminate forcefully the process group you started
and written in `<workbenchdir>/pgid`.
Delete the file `<workbenchdir>/pgid`.
Delete the file `<workbenchdir>/current`.

Do NOT remove the `<workbenchdir>/<name>` directory (it persists for reuse on next launch).

# GET /api/redeploy?name=<app>

This endpoint redeploys actions and restarts the `ops ide devel` process without restarting truacp. It streams progress via Server-Sent Events (SSE).

This Trustable-owned endpoint is the safe full-redeploy path because it stops
the watcher before running `ops ide deploy` and restarts it afterward. It is
not equivalent to a Pi shell call that races the active watcher.

The managed Pi extension exposes this same operation as
`trustable_runtime_redeploy`, using the co-located
`http://127.0.0.1:8910/api/redeploy` endpoint. After one or more successful
OpenServerless `action_new` creations, Pi calls it once after the coherent
action/wiring/source batch and before watcher status, checker, HTTP, or browser
verification. The extension treats an SSE `error`, non-success HTTP response,
timeout, or missing `done` event as a tool failure and keeps redeploy required.
An idempotent `action_new` no-op does not trigger this requirement.

Response content type: `text/event-stream`

## Validate

- Check `name` query parameter is valid and workbench exists at `<workbenchdir>/<name>`
- Check that a pgid file exists (there must be a running session to redeploy)

## Step 1: Terminate ops ide devel

Find and terminate only the `ops ide devel` child process (not the entire process group — truacp must keep running):

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

- Execute `ops ide devel --fast` in `<workbenchdir>/<name>`, joining the existing process group (same `pgid` as truacp)
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
