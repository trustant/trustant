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

1. stage all app-owned changes with `git add -A`, excluding the launch-generated
   `.mcp.json`, `.openserverless-contract.md`, `opencode.md`, and
   `opencode.json` files, plus `AGENTS.md` when it contains only the
   Trustable-managed block. These files are regenerated on launch and must
   never be committed; `.mcp.json` can contain runtime service credentials.
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

The pull operation is intentionally fast-forward only:

1. validate the app name and ensure the bare workspace repo exists;
2. if the workbench checkout exists, inspect `git status --porcelain`;
3. if the status contains app/user changes, fail and return only those dirty
   lines in `output`;
4. if the status contains only Trustable-generated launch files
   (`.mcp.json`, `.openserverless-contract.md`, `opencode.md`,
   `opencode.json`, or a generated-only `AGENTS.md` with no app-local notes),
   clean those generated files before continuing because they are regenerated
   on launch;
5. if the workbench checkout exists, fetch its local `origin` and fail when the
   workbench `HEAD` is not an ancestor of `origin/main`, because that means
   there are local-only commits or divergent history that should be saved or
   resolved first;
6. in the bare workspace repo, configure `production` from `OPS_REPO` when
   present, then run `git fetch <remote> main`;
7. if `refs/heads/main` already equals `FETCH_HEAD`, report that the app is
   already up to date;
8. otherwise fail unless `refs/heads/main` is an ancestor of
   `FETCH_HEAD`;
9. update `refs/heads/main` to `FETCH_HEAD`;
10. if the workbench checkout exists, fetch from its local `origin` and run
   `git merge --ff-only origin/main`;
11. when the workbench checkout was updated, run `ops ide clean` and
   `ops ide deploy` in the workbench so the local dev server reflects the
   pulled code.

Return JSON:

```
{
  "message": <summary>,
  "output": <combined command output>,
  "updated": <true when new commits were pulled>,
  "workbench_updated": <true when the active checkout moved>
}
```

On failures return JSON with `error` and `output` when command output is
available. The endpoint must not perform an implicit merge commit, rebase, or
hard reset.

See [git-pull-flow.svg](git-pull-flow.svg).
