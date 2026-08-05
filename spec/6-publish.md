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
8. Return `{"output": "Published successfully"}` on success, or `{"error": "...", "output": "..."}` on failure
