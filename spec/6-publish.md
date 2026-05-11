This file describes the publish APIs.
Put the code in the file `publish.go`

# Publishing authorization

Every publish endpoint below (`/api/publish/push`, `/api/publish/force-push`, `/api/publish/remote`) **must** validate that the user's ai-proxy API key authorizes publishing **before** doing any work. This is a server-side check; the frontend does not gate these calls.

The check:

1. Load the merged config and read `env.OPENAI_API_KEY` (the `aip_...` bearer) and `env.OPENAI_BASE_URL`.
2. Derive the proxy origin by stripping a trailing `/v1` from `OPENAI_BASE_URL`. The well-known is then `<origin>/.well-known/ai-proxy-pubkey`.
3. Fetch the public key once and cache it in process memory for the lifetime of the process. Do not re-fetch on verification failure.
4. Verify the key signature per [10-validate_key.md](10-validate_key.md) (Ed25519 over `id_bytes`).

If any step fails — missing key, missing/unparseable base URL, well-known fetch error, malformed key, signature mismatch — return HTTP 403 with `{"error": "Publishing not authorized: <reason>"}` and **do not** perform any publish action. There is no `needs_config` fallback for an authorization failure; the user must reconfigure to a publishing-capable provider.

The legacy `publishing` flag in the workspace config is removed. Whether publishing is allowed is determined entirely by signature verification at request time.

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
   - `git remote add production git@github.com:<OPS_REPO>.git`
   - `git push production main` with `GIT_SSH_COMMAND=ssh -i ~/.ssh/id_ed25519 -o IdentitiesOnly=yes -o StrictHostKeyChecking=no`
6. Return `{"output": "..."}` on success, or `{"error": "...", "output": "..."}` on failure

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
