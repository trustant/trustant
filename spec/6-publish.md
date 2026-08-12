This file describes the publish APIs.
Put the code in the file `publish.go`

# Publishing authorization

Authorization is an offline, Ed25519-signed **license**, fully specified in [14-license.md](14-license.md). The `api_key` is no longer involved: it keeps its inference and credits role only. The frontend does not gate these calls; every check below runs server-side, before any work is done.

Two gates, both answering **HTTP 402**:

1. `requireValidLicense` runs at the top of **all three** handlers — `/api/publish/push`, `/api/publish/force-push` and `/api/publish/remote`. It verifies the stored license's signature and expiry; the license's `hosts` list is not consulted. On failure: `{"error": "License required: <reason>"}`. An expired license is invalid outright, so git push stops too.

2. `requireLicensedHost` runs in `/api/publish/remote` only, **after** the production config is resolved (after the `needs_config` check) and **before** `ops ide login --mode=production`, so nothing touches the target cluster when the host is unlicensed. It matches `OPS_APIHOST` against the license `hosts` — exact match on scheme + host + port, no wildcards. On mismatch: `{"error": "Host not licensed: <apihost> is not covered by your license"}`.

The local apihosts (`miniops.me`, `localhost`, `127.0.0.1`, `::1`) skip the **host** gate only; a valid license is still required to publish to them.

There is no `needs_config` fallback for an authorization failure — the user must install a valid license, which the frontend offers through the license modal. The legacy `publishing` flag in the workspace config is removed.

Git-push authentication itself is unchanged: it comes from the managed personal GitHub account when connected, with the existing SSH key as fallback.

# Progress streaming

All three publish endpoints stream their progress as server-sent events, using
the same wire format and the same shared writer as launch
([4-launch.md](4-launch.md)). The writer lives in `progress.go`; `total` is
supplied per endpoint rather than fixed.

Streaming is negotiated by the `Accept` header:

- `Accept: text/event-stream` — the response is `text/event-stream` and the
  events below are emitted. The HTTP status is always 200, because the response
  is committed before the outcome is known; the real outcome travels in the
  terminal event.
- anything else — the plain JSON behaviour described per endpoint below, status
  codes included, and no events. The frontend's `needs_config` probe relies on
  this, and a client that cannot stream keeps working unchanged.

Events:

- `event: progress` — `{"stage": N, "total": T, "message": "..."}`
- `event: output` — `{"line": "..."}`, one event per complete line of command
  output, emitted as the command produces it. Partial lines are held until
  their newline arrives, so a line is never split across two events.
- `event: done` / `event: error` — the terminal JSON body, byte-for-byte what
  the non-streaming path would have returned. `error` is chosen exactly when
  the payload carries an `error` key, which preserves the
  `License required:` / `Host not licensed:` prefixes the frontend keys its
  license modal on.

Long-running subprocesses (`ops ide login`, `ops ide deploy`, `npm install`,
`git`) write into a tee that both emits `output` events and accumulates the
text used for the terminal payload's `output` field. They must not use
`CombinedOutput()`, which buffers to the end and is why publishing used to show
nothing until it finished.

**Ordering: decode the request body before upgrading to SSE.** The upgrade
flushes the response headers, and Go's HTTP server stops serving an unread
request body once the response is committed — so a `json.Decode(r.Body)` placed
after the upgrade fails with EOF and every publish reports `Invalid JSON`,
whatever was actually sent. Launch does not hit this because it is a `GET` with
no body. The `Invalid JSON` failure therefore predates the upgrade and is
reported as a plain HTTP 400, not as an SSE event.

**Redaction.** `OPS_PASSWORD` is user-supplied and must never reach the stream.
The production password — whether it arrives with the request or is already
stored in the config — is registered with the writer and replaced with `********`
in every `progress` message, every `output` line, and the terminal payload's
`output` field.

Stages for `/api/publish/push` and `/api/publish/force-push` (total 3):

1. Checking license and repository configuration
2. Configuring production remote
3. Pushing to GitHub

Stages for `/api/publish/remote` (total 6):

1. Checking license and production configuration
2. Preparing workbench
3. Installing dependencies — reported as skipped when there is no
   `package.json` or the workbench already exists
4. Generating environment files
5. Connecting to OpenServerless
6. Deploying application

# POST /api/publish/push

Push code to a production GitHub repository.

Request:
`{
  "name": "<app>",
  "repo": "<org/repo>"
}`

Where `repo` is optional (only sent when user provides it from the popup).

1. Validate `name` with the standard name pattern
2. If `repo` is provided, validate it (org/repo format) and save to `apps.<name>.production.OPS_REPO` in workspace config
3. Read `OPS_REPO` from production config. If empty, return `{"needs_config": true}`
4. Check workspace bare repo exists at `<WorkspaceDir>/workspace/<name>`. If not, return error.
5. In the workspace bare repo directory:
   - `git remote remove production` (ignore errors, may not exist)
   - resolve its symbolic default branch and reject an invalid/missing branch;
   - when the managed GitHub account is authenticated, validate `OPS_REPO` and
     add `production` with the sanitized HTTPS clone URL;
   - otherwise add `production` as `git@github.com:<OPS_REPO>.git`;
   - run `git push production <default-branch>` with the isolated managed Git
     environment. SSH fallback also adds
     `GIT_SSH_COMMAND=ssh -i ~/.ssh/id_ed25519 -o IdentitiesOnly=yes -o StrictHostKeyChecking=no`.
6. Return `{"output": "..."}` on success, or `{"error": "...", "output": "..."}` on failure

The same transport and default-branch rules apply to
`/api/publish/force-push`. Neither endpoint returns tokens, credential-helper
configuration, or raw `gh` output.

# POST /api/publish/remote

Deploy to a production OpenServerless environment.

Request:
`{
  "name": "<app>",
  "apihost": "<value>",
  "user": "<value>",
  "password": "<value>"
}`

Where `apihost`, `user`, `password` are optional (only sent when user provides them).

1. Validate `name` with the standard name pattern
2. If config fields are provided, save them to `apps.<name>.production` as `OPS_APIHOST`, `OPS_USER`, `OPS_PASSWORD`
3. Read all 3 from production config. If any are empty, return `{"needs_config": true}`
4. Check if workbench exists at `<WorkbenchDir>/<name>`:
   - If missing, clone from `<WorkspaceDir>/workspace/<name>`, run `npm install` if `package.json` exists
5. Generate env files via `generateAppEnvFiles(name)` (always, to ensure `.env.production` is current)
6. Run `ops ide login --mode=production` in the workbench directory
7. Run `ops ide deploy` in the workbench directory
8. Return `{"message": "Published successfully", "output": "..."}` on success, or
   `{"error": "...", "output": "..."}` on failure. On success `output` carries
   the accumulated command output (previously discarded), so the modal's
   expandable pane keeps it after the run ends; `message` is the headline.

# Publish progress modal (`web/applist.html`)

Both the publish modal and the git-push modal show the same progress UI while
their request is in flight, replacing the bare spinner caption:

- a labelled progress bar (`publishProgressBar` / `gitPushProgressBar`) with
  `role="progressbar"` and `aria-valuenow`, plus step and percent labels;
- a status line (`publishStatusMessage` / `gitPushStatusMessage`) carrying the
  current step's message from the `progress` event;
- an output disclosure, **collapsed by default**, toggled by a "Show output" /
  "Hide output" button and containing a scrollable monospace pane that appends
  each `output` line and auto-scrolls to the newest. It can be toggled at any
  time, during the run and after it ends, and **auto-expands when a terminal
  `error` arrives**.

The percentage is monotonic — it never moves backwards within a run.

While a publish is in flight the modal is not dismissable: the backdrop-click
handlers close it only when the spinner block is hidden, and the Escape handler
already requires the form or result view to be visible. When the run ends, the
result view replaces the progress block and reports `message` (falling back to
`output`) on success, or `error` plus `output` on failure.

`fetchPublishResponse(url, body, prefix)` mirrors `fetchLaunchResponse`: it
POSTs with `Accept: text/event-stream`, reads the body with
`response.body.getReader()`, parses blocks with the shared `parseStreamEvent`,
and routes `progress`/`output`/`done`/`error`. It returns the plain response
untouched when the server answers with JSON instead, so the non-streaming path
still works.

**Every** publish call site streams, including the entry points
`handlePublishRemote` and `handleGitPush`. Those are not mere `needs_config`
probes: the endpoints do double duty, so once an app is configured the entry
call performs the real publish. Running it as a plain `fetch` made an
already-configured app publish silently, with no progress bar and no output —
and made the modal look different on the second use than on the first. The
entry points therefore open the modal directly in its progress state via
`showPublishProgressModal(prefix, message)` and fall back to the configuration
form only when the terminal payload carries `needs_config`.

On completion the bar is filled against the total the stream actually reported,
not a synthetic `1 of 1`. A `needs_config` outcome leaves the bar where it is,
since nothing was published.
