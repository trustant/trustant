You are building a go applications implementing a Lovable like environment based on OpenServerless

This file describes the prefligth.go module with the checks to execute

# Setup Env
When starting, before anything else:

- Read the .env in current dir and set the variables. If `.env` is missing, the app fails to start.

show in the log the variables set

propagate the env to all the subcommands invoked

use the provided environment to set internal variables
expanding them also using environment variables for:

WorkspaceDir
WorkbenchDir
OllamaEndpoint
OpsSkills (defaults to "trustable-ai/skills", not editable in development)

## Feature flags

Two optional variables switch on features that are **off by default**. Both are
read by `envFlag`, which treats unset, empty, `0`, `false`, `no` and `off` as
off, and anything else as on:

| Variable | Internal | Effect when on |
|---|---|---|
| `ENABLE_LICENSE` | `EnableLicense` | Enforce the license gates on git push and publishing, and show the License card in `configure.html` |
| `ENABLE_REGOLO` | `EnableRegolo` | Offer the **Sovereign AI** (Regolo.AI) card in the provider selector |

Neither variable appears in [.env.dist](../.env.dist) nor in the `.env` that
`setup.sh` generates, so a development checkout runs with both features off: no
license is needed to push, and the provider selector shows only Cloud AI and
Private AI. The distribution image turns both on in [image/env](../image/env).

Both flags are reported to the frontend by `/api/version` as the booleans
`license` and `regolo` — every page already fetches that endpoint on boot. See
[14-license.md](14-license.md) and [1-index.md](1-index.md).

## ops CLI probe

At startup, once, the server runs `ops -info` and caches the result
(`probeOpsInfo` in `repo.go`). It shells out, so it MUST NOT run per request —
`/api/version` serves the cached value. Failure is **non-fatal**: `ops` may
simply be absent, and the server has no business refusing to start over a
diagnostic panel. The values stay empty and the UI omits them.

The output is a list of `<key>: <value>` lines, parsed into **ordered** pairs.
Three properties of the real output dictate the parsing rule:

- The first key is literally `OPS & OPS_CMD` — it contains spaces and an
  ampersand, so keys are display text, **not** identifiers, and must not be
  validated as such.
- Values contain their own colons (`OPS_REPO` is a URL), so only the **first**
  `:` may be split on.
- `OPS_BRANCH` is routinely empty; a blank value **keeps its row** rather than
  being dropped.

Order is preserved with a slice, deliberately **not** a map: Go randomizes map
iteration, which would shuffle the Configure table between reloads. Lines with
no separator at all are skipped.

`/api/version` then carries two more fields:

| Field | Content |
|---|---|
| `tasks` | `OPS_OLARIS` truncated to **6 characters** — enough to identify the tasks in use at a glance. Shown in the app-list footer as `Task: <hash>`. |
| `opsinfo` | The full parsed table, as a JSON array of `{"key", "value"}` objects. Rendered by the Configure page; see [2a-config.md](2a-config.md). |

The ops **version** is not a separate field — it is the `OPS_VERSION` row of
`opsinfo`. The footer deliberately carries only the short task hash.

`OPENAI_BASE_URL` and `OPENAI_API_KEY` are deliberately **not** mirrored into
internal variables. The provider base URL is read from `cfg.BaseURL` in the
layered `trustant.json`, and the real key is resolved by Pi through `auth.json`
from the literal `$OPENAI_API_KEY` reference stored in its model catalog — the
server never substitutes it. Both variables are still propagated to subcommands
through the process environment, and both are stripped from the user-visible
shell by the terminal (see [12-terminal.md](12-terminal.md)).

- Run migration: if the workspace `trustant.json` does not yet have an `apps` section, scan `<WorkspaceDir>/workspace/*/` for existing app directories, retrieve each app's password via `ops util kubeget whiskuser/<name> .spec.password`, build `apps` entries, strip fields that match the base config, and save the updated workspace `trustant.json`.

# workbench is ephemeral

The workbench is scratch space, not durable state. In the distribution image the
entrypoint ([image/start.sh](../image/start.sh)) removes whatever is at
`$WORKBENCH_DIR` — including a symlink left by an older image, which is why it
must be deleted rather than followed — and recreates it as an **empty real
directory** on every container start. It MUST NOT be a link into the persistent
`/home/trustant/workspace` volume: uncommitted work is meant to be lost on
restart, and committing is the user's responsibility.

The entrypoint runs as **root**, so the directory it recreates is root-owned
while the server runs as `trustant`. It must therefore `chown
trustant:trustant "$HOME/workbench"` immediately after the `mkdir`, or the
launch clone writes into a directory it does not own. The chown is
**non-recursive** — the preceding `rm -rf` guarantees the directory is empty —
and **synchronous**, unlike the workspace chown below: it is a single inode, and
the server may clone into it as soon as `supervisord` starts. The background
workspace chown does not cover this path, by design; it walks
`$HOME/workspace` only.

The startup invariant the rest of the server relies on is therefore: after a
restart, `$WORKBENCH_DIR` exists, is a real directory, is empty, and is **owned
by `trustant`**. The
workbench restore described in [4-launch.md](4-launch.md) then clones each
durable bare repo back from `<WorkspaceDir>/workspace/<name>`; that path is
unaffected and stays on the volume. Any leftover
`/home/trustant/workspace/workbench` from an older image is ignored — never
migrated, read, or deleted.

# startup ownership of the workspace

`$HOME/workspace` is the only mounted `hostPath` volume
([oplugins-truinst/trustant/sts.yaml](../oplugins-truinst/trustant/sts.yaml)), so it
is the only tree whose ownership can actually be wrong on a container start.
The entrypoint ([image/start.sh](../image/start.sh)) therefore chowns **that
path only**. It MUST NOT recursively chown bare `$HOME`: everything else in the
container is image content already owned correctly by the `Dockerfile`'s
`COPY --chown` and its `USER`/`WORKDIR` setup, so a full-`$HOME` walk covers
`~/.local`, `~/.ops`, `~/.cache` and baked-in `node_modules` for no benefit.

The chown runs **in the background** so `supervisord` starts immediately rather
than after the walk. Progress is reported through a lock file at
`$HOME/workspace/.trustant/init.lock`, under the existing `.trustant/`
convention for server-side state, whose content is the running count of files
processed. The count is rewritten periodically rather than per file, so the
counter itself never becomes the bottleneck on a large volume.

`.trustant/` was called `.trustant/` before the rebrand. A pre-rebrand
directory is **ignored, not migrated**: `.trustant/` is created fresh and its
contents (the ssh key, GitHub auth, secrets and Pi agent config) are
regenerated under the new name. Any old `.trustant/` is left in place on the
volume for an operator to remove. Go callers reach the directory through the
`stateDir()` helper, which is its single definition.

Three properties are required of the lock:

1. **Created before backgrounding.** The lock must exist before the chown is
   put in the background, otherwise the splash can poll, find no lock, and
   wrongly conclude initialization has already finished.
2. **Readable by the server.** The entrypoint runs as `root` while the Go
   server reads the lock as `trustant`, so the lock file and its directory are
   given `trustant` ownership and read permission **at creation time** — not
   left for the background chown to reach. A lock that exists but cannot be
   read is indistinguishable from a stale one and would hang the splash.
3. **Always removed.** The lock is removed explicitly **at the end of the init
   loop**, once the final count has been written and the chown has genuinely
   finished. A `trap ... EXIT` inside the backgrounded subshell is the
   failure-path guarantee on top of that: an aborted or failing chown must
   never strand the lock and leave the splash waiting forever.

`start_script_test.go` guards all of the above against regression, since each is
a one-line change in a shell script and invisible at runtime.

## GET /api/initstatus

The browser cannot read the filesystem, so the server reports the lock. The
endpoint is **not gated by license or expiry** — it must answer before anything
else works — and returns:

```json
{ "initializing": true, "count": 12480 }
```

- Lock absent → `{"initializing": false}`. This is the normal steady state and
  **also the development case**: `start.sh` never runs there and no lock is
  ever created, so the splash must not wait on a development machine.
- Lock present but unreadable or holding garbage → `count: 0` but still
  `initializing: true`. A malformed counter must not abort the wait.

The splash gate that consumes this is specced in [1-index.md](1-index.md).

# cleanup

- if there is a file pgid in WorkspaceDir, read it and terminate the process group and remove the file

- use lsof -i and check for processes occupying in port 8910 4096 and 5173 and kill them

# import predefined environment variables

After the migration above — which is what guarantees a workspace
`trustant.json` exists to write into — and before the SSH key check, fold an
**optional** `.env.default` in the working directory into the `predefined_env`
palette, adding only names that are not already there.

This only populates the palette; nothing reaches an application. Failure is
**non-fatal** — log a warning and carry on, as with the pgid and port cleanup: a
malformed optional file must not stop the server from starting. Only variable
names are logged, never values.

The rules in full are in [2a-config.md](2a-config.md) under "Predefined
environment variables".

# check ssh key

Ensure a persistent ed25519 key exists under
`$WORKSPACE_DIR/.trustant/ssh/id_ed25519`. If an old ephemeral
`~/.ssh/id_ed25519` exists and the persistent key is missing, migrate it there.
Otherwise generate a passphrase-less keypair at the persistent path
(`ssh-keygen -t ed25519 -N "" -C "trustant" -f
$WORKSPACE_DIR/.trustant/ssh/id_ed25519`). Derive the matching `.pub` via
`ssh-keygen -y` when missing, chmod private/public key files 600, and expose
them at the compatibility paths `~/.ssh/id_ed25519` and
`~/.ssh/id_ed25519.pub` using symlinks when possible, falling back to copies.
Set `sshKeyAvailable` to true only when both persistent and compatibility paths
are ready.

# web server

the application itself is a web server:
- serving pages
- proxying ports according the host name
- implementing apis described in the spec

The web application requires you always access the application with a full fqdn like trustant.<domain>[:<port>]

- if you detect localhost  redirect to trustant.127.0.0.1.nip.io:<port>,
- if you detect an <ip> redirect to trustant.<ip>.nip.io:<port>

- if detect a fqdn like <host>.<domain> (no '.' in <host>, <domain> can include '.') do the following:
- if <host> is 'trustant', serve the folder `web`
- if <host> is 'opencode', proxy pass to the pod-local TruACP runtime at
  `127.0.0.1:4096`. The hostname keeps its historical `opencode` label so
  existing ingress, WAF rules and bookmarks stay valid; the runtime behind it is
  TruACP/Pi. In Kubernetes/browser deployments the `opencode.<domain>` ingress
  must route to the Trustant app port 8910, not directly to service port 4096.
  TruACP serves its own UI and owns its working directory and ACP session state
  internally, so the middleware performs no directory, session or project-route
  rewriting: requests are proxied through unchanged. App switching happens
  through Trustant `/api/launch/<app>`.
- if <host> is 'vite',  proxy pass to port 5173
