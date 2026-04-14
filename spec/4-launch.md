This file describes the api for launching.
Put the code in the file `launch.go`

# GET /api/launch/<name>

When invoking this api it should check the folder
<workspacedir>/workspace/<name> exists, if not return

```
{ "error": <error> }
```

## terminate leftover processes

Then, check if exists a `<workbenchdir>/pgid` file
invoke `DELETE /api/launch` to ensure the group is terminated

If it is still there, forcefully terminate the process group
pointed by that file.

## clone to workbench

If `<workbenchdir>/<name>` already exists, keep it (continue previous work) and skip to the login step.

Otherwise, clone the workspace into the workbench:

`git clone <workspacedir>/workspace/<name> <workbenchdir>/<name>`

Then set up the workbench:

- Generate `.env` and `.env.production` in `<workbenchdir>/<name>` from the merged config using `generateAppEnvFiles(<name>)`. The `.env` contains `OPS_USER`, `OPS_PASSWORD`, `OPS_APIHOST` (fixed), global env defaults, and per-app development overrides. The `.env.production` contains per-app production values.
- If `<workbenchdir>/<name>/package.json` exists, run `npm install` in `<workbenchdir>/<name>`

When the workbench already exists (reuse path), also regenerate the `.env` files to keep them in sync with the current config.

## login

Change to `<workbenchdir>/<app>` folder
and execute `ops ide login`

If it terminates with 0 continue otherwise return error

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

## copy opencode configuration files

Before starting opencode, copy the opencode JSON config to the workbench directory:

If the file `~/.config/opencode/opencode.json` does not exist, generate it first by calling `generateOpencodeConfig()` (which also writes `opencode.md` to `~/.config/opencode/opencode.md`).

Copy the file `~/.config/opencode/opencode.json` to `<workbenchdir>/<app>/opencode.json`, overwriting existing files.

Note: `opencode.md` is written to `~/.config/opencode/opencode.md` during the configure step and referenced by absolute path in `opencode.json`, so it does not need to be copied to the workbench.

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

When ok, execute a POST to <domain>:4006/session/ with header "X-Opencode-Directory: <directory>" and log the result of this invocation,

then return:

`{
  "left" : <opencode-port>,
  "right": <opsdeve-port>,
  "b64dir": <base64-urlsafe-encoded directory>
  "encdir": <absolute directory>
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
