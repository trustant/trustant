This file describes the publish APIs.
Put the code in the file `publish.go`

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
4. Check workbench exists at `<WorkbenchDir>/<name>`. If not, return error: "Please launch (Edit) the app at least once."
5. In the workbench directory:
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
