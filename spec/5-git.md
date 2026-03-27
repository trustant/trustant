This file describes support git api
Put the code in the file `git.go`

# POST /api/git

`{
   "name": <name>
    "cmd" <command>
 }'


Change to the folder `<workbenchdir>/<name>`
and execute the command `git <command>`.
If the command is `checkout .`, also run `git clean -fd` to remove untracked files.
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

1. `git add -A` to stage all changes
2. `git status --porcelain` to check if there are changes to commit
   - If no changes, return `{"message": "nothing to save"}`
3. `git commit -m "save from trustable"` to commit all changes
4. `git push origin` to push changes back to workspace/<name>
   (the origin remote points to workspace/<name> because workbench was cloned from it)

If any step fails, return `{"error": <error message>}`.
If successful, return `{"message": "saved successfully"}`.
