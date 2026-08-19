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

`OPENAI_BASE_URL` and `OPENAI_API_KEY` are deliberately **not** mirrored into
internal variables. The provider base URL is read from `cfg.BaseURL` in the
layered `trustable.json`, and the real key is resolved by Pi through `auth.json`
from the literal `$OPENAI_API_KEY` reference stored in its model catalog — the
server never substitutes it. Both variables are still propagated to subcommands
through the process environment, and both are stripped from the user-visible
shell by the terminal (see [12-terminal.md](12-terminal.md)).

- Run migration: if the workspace `trustable.json` does not yet have an `apps` section, scan `<WorkspaceDir>/workspace/*/` for existing app directories, retrieve each app's password via `ops util kubeget whiskuser/<name> .spec.password`, build `apps` entries, strip fields that match the base config, and save the updated workspace `trustable.json`.

# workbench is ephemeral

The workbench is scratch space, not durable state. In the distribution image the
entrypoint ([image/start.sh](../image/start.sh)) removes whatever is at
`$WORKBENCH_DIR` — including a symlink left by an older image, which is why it
must be deleted rather than followed — and recreates it as an **empty real
directory** on every container start. It MUST NOT be a link into the persistent
`/home/trustable/workspace` volume: uncommitted work is meant to be lost on
restart, and committing is the user's responsibility.

The startup invariant the rest of the server relies on is therefore: after a
restart, `$WORKBENCH_DIR` exists, is a real directory, and is empty. The
workbench restore described in [4-launch.md](4-launch.md) then clones each
durable bare repo back from `<WorkspaceDir>/workspace/<name>`; that path is
unaffected and stays on the volume. Any leftover
`/home/trustable/workspace/workbench` from an older image is ignored — never
migrated, read, or deleted.

# cleanup

- if there is a file pgid in WorkspaceDir, read it and terminate the process group and remove the file

- use lsof -i and check for processes occupying in port 8910 4096 and 5173 and kill them

# import predefined environment variables

After the migration above — which is what guarantees a workspace
`trustable.json` exists to write into — and before the SSH key check, fold an
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
`$WORKSPACE_DIR/.trustable/ssh/id_ed25519`. If an old ephemeral
`~/.ssh/id_ed25519` exists and the persistent key is missing, migrate it there.
Otherwise generate a passphrase-less keypair at the persistent path
(`ssh-keygen -t ed25519 -N "" -C "trustable" -f
$WORKSPACE_DIR/.trustable/ssh/id_ed25519`). Derive the matching `.pub` via
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

The web application requires you always access the application with a full fqdn like trustable.<domain>[:<port>]

- if you detect localhost  redirect to trustable.127.0.0.1.nip.io:<port>,
- if you detect an <ip> redirect to trustable.<ip>.nip.io:<port>

- if detect a fqdn like <host>.<domain> (no '.' in <host>, <domain> can include '.') do the following:
- if <host> is 'trustable', serve the folder `web`
- if <host> is 'opencode', proxy pass to the pod-local TruACP runtime at
  `127.0.0.1:4096`. The hostname keeps its historical `opencode` label so
  existing ingress, WAF rules and bookmarks stay valid; the runtime behind it is
  TruACP/Pi. In Kubernetes/browser deployments the `opencode.<domain>` ingress
  must route to the Trustable app port 8910, not directly to service port 4096.
  TruACP serves its own UI and owns its working directory and ACP session state
  internally, so the middleware performs no directory, session or project-route
  rewriting: requests are proxied through unchanged. App switching happens
  through Trustable `/api/launch/<app>`.
- if <host> is 'vite',  proxy pass to port 5173
