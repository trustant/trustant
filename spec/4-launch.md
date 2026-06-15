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

If `<workbenchdir>/<name>` already exists, keep it (continue previous work) and skip to the login step.

Otherwise, clone the workspace into the workbench:

`git clone <workspacedir>/workspace/<name> <workbenchdir>/<name>`

Then set up the workbench:

- Generate `.env` and `.env.production` in `<workbenchdir>/<name>` from the merged config using `generateAppEnvFiles(<name>)`.
The `.env` contains `OPS_USER`, `OPS_PASSWORD`, `OPS_APIHOST` (fixed), global env defaults, and per-app development overrides. The `.env.production` contains per-app production values.
- If `<workbenchdir>/<name>/package.json` exists, run `npm install` in `<workbenchdir>/<name>`

When the workbench already exists (reuse path), also regenerate the `.env` files to keep them in sync with the current config.

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

- Read the stored password from the merged config (`apps.<name>.password`).
- Read the live password with `ops util kubeget whiskuser/<name> .spec.password`.
- If the user does **not** exist: recreate it with the stored password via
  `ops admin adduser <name> <name>@n7s.co <storedPassword> --all`. If there is no
  stored password, return `{ "error": ... }` (cannot recreate).
- If the user **does** exist but the live password differs from the stored one,
  adopt the live password: persist it to the workspace config
  (`apps.<name>.password`) and regenerate the `.env` files.

## login

Change to `<workbenchdir>/<app>` folder
and execute `ops ide login`

If it terminates with 0 continue otherwise return error

## generate an opencode.json in project directory as follows

the generation must happen AFTER the ops ide login (to retrieve the config) but BEFORE launching opencode (otherwise it won't start)

There is a **single, self-contained** `opencode.json` written into the app's
project directory `<workbenchdir>/<app>/opencode.json`. There is **no** global
`~/.config/opencode/opencode.json` — it is not generated and not referenced.
The project file holds the entire config: provider, model defaults
(`model` / `small_model`), `disabled_providers`, `instructions`, `lsp`, and the
`mcp` servers built from `~/.ops/config.json`. The provider block, model
defaults, and the merge rules are exactly those in
[2a-config.md](2a-config.md#prepare-opencode-config), except the file is written
to the project directory instead of `~/.config/opencode/`.

`opencode.md` and the embedded `tools/` folder are written **alongside** the
config in the project directory, and `instructions` references the project's own
`<workbenchdir>/<app>/opencode.md` (not an absolute `~/.config` path).

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
  "command": ["redis-mcp-server"],
  "environment:" {
    "REDIS_HOST": "<config.redis.service>",
    "REDIS_PORT": "<config.redis.port>",
    "REDIS_PWD": "<config.redis.password>",
    "REDIS_SSL": "false",
    "REDIS_CLUSTER_MODE": "false"
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

Note: `opencode.md` and the embedded `tools/` folder are written into the
project directory alongside `opencode.json`, and `instructions` references the
project's own `<workbenchdir>/<app>/opencode.md`.

Also normalize any OpenCode agent metadata under
`<workbenchdir>/<app>/.opencode/agent/*.md`: rewrite each agent's `color:`
frontmatter to one of OpenCode's accepted values (`primary`, `secondary`,
`accent`, `success`, `warning`, `error`, `info`, or a `#rrggbb` hex), mapping
common color names (e.g. `blue`→`primary`, `green`→`success`, `red`→`error`) and
defaulting anything unrecognized to `primary`. Best-effort — failures are logged.

Launch `opencode serve` with the variables from the workbench `.env` appended to
the process environment.

## start process group

Let <directory> be the absolute path of `<workbenchdir>/<app>`

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

The POST must target the **opencode host**, not localhost: take the request host,
strip its port, swap the `trustable.` hostname prefix for `opencode.`, and POST to
`http://opencode.<domain>:4096/session/`. In production opencode is reached through
the ingress (which routes by the `opencode.` hostname prefix — see middleware.go),
not over the loopback, so localhost would not resolve to the right server.

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

Send repeated HTTP HEAD requests to `http://localhost:5173/` every 500ms until the server responds (timeout: 30 seconds). If it doesn't respond, send `event: error` and return.

Stream: `event: status` / `data: Waiting for dev server to be ready...`

## Done

Send the action list captured in step 4 as the data of the `done` event.

Stream: `event: done` / `data: <output of ops action list>`

## On error

At any step, if an error occurs:

Stream: `event: error` / `data: <error message>`
