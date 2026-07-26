This file describes support git api
Put the code in the file `git.go`

# POST /api/git

`{
   "name": <name>
    "cmd" <command>
 }'


Change to the folder `<workbenchdir>/<name>`
and execute the command `git <command>`.
If the command is `checkout .`, also run `git clean -fd` to remove untracked files, then run `ops ide clean` followed by `ops ide deploy` (both in `<workbenchdir>/<name>`).
Return the output.

# GET /api/git/status/<name>

Change to the folder `<workbenchdir>/<name>`.
If the folder does not exist, return `{"error": "workbench not found"}`.

Execute `git status --porcelain` and parse the output.
Count the number of files by status:
- modified (M)
- added/untracked (??)
- deleted (D)

Return:
```
{
  "changed": <number of modified files>,
  "added": <number of new/untracked files>,
  "deleted": <number of deleted files>,
  "clean": <true if no changes, false otherwise>
}
```

# POST /api/git/save

`{
   "name": <name>
}`

Change to the folder `<workbenchdir>/<name>`.
If the folder does not exist, return `{"error": "workbench not found"}`.

Execute the following git commands in sequence:

1. preflight the app-owned staging command with `git add --dry-run -A`, then
   stage all app-owned changes with `git add -A`, excluding the launch-generated
   `.mcp.json`, `.openserverless-contract.md`, `opencode.md`, and
   `opencode.json` files, plus `AGENTS.md` when it contains only the
   Trustable-managed block. These files are regenerated on launch and must
   never be committed; `.mcp.json` can contain runtime service credentials.
   An ignored untracked generated file must be left to the repository ignore
   rules instead of being passed as an explicit negative pathspec: Git rejects
   an explicitly mentioned ignored path and may partially update the index
   before returning the error. After staging, restore every generated path and
   `*.tsbuildinfo` compiler artifact in the index to `HEAD`; this also repairs
   partial staging left by an older failed Commit without changing the working
   files.
2. inspect the staged index with `git diff --cached --name-only` to check if
   there are app-owned changes to commit. Excluded generated files may remain
   in the worktree and do not make this check dirty.
   - If no staged changes remain, return `{"message": "nothing to save"}`
3. `git commit -m "save from trustable"` to commit all changes
4. `git push origin` to push changes back to workspace/<name>
   (the origin remote points to workspace/<name> because workbench was cloned from it)

5. `ops ide clean` to clean the IDE state
6. `ops ide deploy` to deploy updated code

If any step fails, return `{"error": <error message>}`.
If successful, return `{"message": "saved successfully"}`.

# POST /api/git/pull

`{
   "name": <name>
}`

Synchronize the app from a remote repository into Trustable's local git state.
When the app has production `OPS_REPO` configured, pull from that same
repository used by the app-list Git Push action. Otherwise pull from the
workspace bare repository's original `origin` remote.

Trustable keeps two git locations for an app:

- `$WORKSPACE_DIR/workspace/<name>`: bare durable repository. Its `origin`
  remote points to the original GitHub repository used when the app was added.
  When `OPS_REPO` is configured, it may also have a `production` remote.
- `$WORKBENCH_DIR/<name>`: active checkout. Its `origin` remote points to the
  local bare workspace repository.

Trustable-managed Git commands receive the isolated environment from
[github.md](github.md). This makes HTTPS remotes use the managed `gh`
credential helper without placing credentials in the process-wide environment,
Pi/TruACP, MCP configuration, or application files. SSH remotes continue to use
the dedicated Trustable key.

The pull operation is intentionally fast-forward only and follows the bare
workspace repository's symbolic default branch instead of assuming `main`:

1. validate the app name and ensure the bare workspace repo exists;
2. if the workbench checkout exists, inspect `git status --porcelain`;
3. if the status contains app/user changes, fail and return only those dirty
   lines in `output`;
4. if the status contains only Trustable-generated launch files
   (`.mcp.json`, `.openserverless-contract.md`, `opencode.md`,
   `opencode.json`, or a generated-only `AGENTS.md` with no app-local notes),
   clean those generated files before continuing because they are regenerated
   on launch;
5. resolve and validate the bare workspace symbolic default branch;
6. if the workbench checkout exists, require it to be on that branch, fetch its
   local `origin`, and fail when the workbench `HEAD` is not an ancestor of the
   matching `origin/<default-branch>`, because that means
   there are local-only commits or divergent history that should be saved or
   resolved first;
7. in the bare workspace repo, configure `production` from `OPS_REPO` when
   present, choosing managed HTTPS when authenticated and SSH otherwise, then
   run `git fetch <remote> <default-branch>`;
8. if `refs/heads/<default-branch>` already equals `FETCH_HEAD`, report that the app is
   already up to date;
9. otherwise fail unless `refs/heads/<default-branch>` is an ancestor of
   `FETCH_HEAD`;
10. update `refs/heads/<default-branch>` to `FETCH_HEAD`;
11. if the workbench checkout exists, fetch from its local `origin` and run
   `git merge --ff-only origin/<default-branch>`;
12. If the workbench moved to a new commit, return
    `workbench_updated: true`. Git Pull MUST NOT run `ops ide clean` or
    `ops ide deploy`; deployment is a separate, explicit user choice.

## Optional deployment after Git Pull

`POST /api/git/deploy` accepts `{ "name": "<app>" }`, validates the app name
and requires an existing workbench. It runs `ops ide clean` followed by
`ops ide deploy` in that workbench and returns their output.

The applications UI offers this endpoint only after a successful pull with
`workbench_updated: true`. Declining the prompt performs no `ops` command;
the later Edit action remains responsible for the normal application launch
lifecycle.
