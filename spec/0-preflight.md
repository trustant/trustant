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
OpenAIBaseUrl
OpenAIApiKey
OllamaEndpoint
OpsSkills (defaults to "trustable-ai/skills", not editable in development)

- Run migration: if the workspace `trustable.json` does not yet have an `apps` section, scan `<WorkspaceDir>/workspace/*/` for existing app directories, retrieve each app's password via `ops util kubeget whiskuser/<name> .spec.password`, build `apps` entries, strip fields that match the base config, and save the updated workspace `trustable.json`.

# cleanup

- if there is a file pgid in WorkspaceDir, read it and terminate the process group and remove the file

- use lsof -i and check for processes occupying in port 8910 4096 and 5173 and kill them

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
- if <host> is 'opencode', proxy pass to the pod-local OpenCode server on port
  4096. In Kubernetes/browser deployments the `opencode.<domain>` ingress must
  route to the Trustable app port 8910, not directly to service port 4096, so
  this middleware can scope OpenCode requests before proxying. Before proxying,
  normalize
  OpenCode document routes that lost their explicit session id:
  - `/` redirects to the latest root session for the app named by the current
    workbench marker, when one exists.
  - `/<B64DIR>/session` redirects to `/<B64DIR>/session/<SESSIONID>` using the
    latest root session for the decoded directory, when one exists.
  - any OpenCode document route whose decoded `<B64DIR>` does not match the
    current workbench marker redirects to the latest current-app session. This
    handles stale browser tabs that still point at a previously edited app.
  This keeps OpenCode from falling back to stale global project state after the
  user exits a session view.
- The OpenCode iframe is scoped to the app launched by Trustable. Before
  proxying any `opencode.<domain>` request with a `directory` query parameter,
  rewrite non-current directories to the current app directory from
  `<workbenchdir>/current`. This prevents OpenCode's internal project switcher
  from opening another Trustable app as a plain folder without the required
  `ops ide login`, generated env, deploy, app-local `opencode.json`, and MCP
  configuration. App switching must happen through Trustable `/api/launch/<app>`.
- For the same reason, `GET /project` through `opencode.<domain>` returns only
  the current app's OpenCode project, even if OpenCode's persistent DB contains
  older projects from previous launches.
- if <host> is 'vite',  proxy pass to port 5173
