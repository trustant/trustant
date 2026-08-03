# Managed .gitignore and shared agent configuration

Implemented by [gitignore.go](../gitignore.go). Called from launch, between
"ensure required folders" and "skills" (see [4-launch.md](4-launch.md)).

# Why

Launch regenerates a set of files in the workbench on every run. Two failures
follow if Git is unaware of them:

- Revert (`POST /api/git` with `cmd=checkout .`) runs `git clean -fd`, which
  deletes untracked, non-ignored files. That destroyed `.mcp.json` and truacp's
  `.acp-data/` session store, leaving the app unable to reach its services.
- The same files appear as spurious diffs on every launch, because they embed
  absolute host paths and per-launch values.

Ignoring them fixes both at once: an ignored file survives `git clean -fd` and
never shows up in a commit.

# The managed block

`ensureManagedGitignore` writes a marker-delimited block into
`<workbenchdir>/<app>/.gitignore`:

```
# >>> trustable managed — do not edit <<<
.acp-data/
.env
.env.production
!.env.dist
node_modules/
.mcp.json
CLAUDE.md
CLAUDE.md.removed
.claude
.claude.removed
.openserverless-contract.md
# >>> end trustable managed <<<
```

Rules:

- Only the block between the markers is rewritten. User lines above and below it
  are preserved byte-for-byte.
- The file is created when absent.
- When the block already matches, the function reports `changed == false` and
  launch makes no commit. Launch must not produce a commit on every run.
- `!.env.dist` must follow `.env`, or Git keeps ignoring the template file.

`AGENTS.md` and `.agents/` are deliberately **absent** from the block. They are
real committed content, and revert must be free to restore them from HEAD.

# Shared agent configuration

`ensureAgentConfigLinks` gives Pi, Codex, and Claude Code one configuration:

| Path | Kind | Git |
|---|---|---|
| `AGENTS.md` | real file, managed block | committed |
| `CLAUDE.md` | symlink → `AGENTS.md` | ignored |
| `.agents/` (incl. `skills/`) | real directory | committed |
| `.claude` | symlink → `.agents` | ignored |

Both links follow the same rule: if the path is already the correct symlink, do
nothing; otherwise rename any real entry to `<name>.removed` and create the
link. Targets are relative, so the checkout stays relocatable. A `.removed`
entry left by an earlier launch is replaced, so a relaunch cannot fail on a name
collision.

## Rescuing app-local notes

Before `CLAUDE.md` is renamed aside, `rescueClaudeAppLocalNotes` folds any
app-local notes it carries into `AGENTS.md`.

This is required, not cosmetic. Older versions wrote `CLAUDE.md` through the
same managed-block merge as `AGENTS.md`, so an existing file may hold user
content that exists nowhere else. Because `CLAUDE.md.removed` is ignored, notes
left there would never be committed again and would be invisible in
`git status`. `AGENTS.md` stays tracked, so merging there preserves them.

The rescue is skipped when the file holds only the managed block (pure launch
output, nothing to keep), and is idempotent: notes already present in
`AGENTS.md` are not appended twice.

# Untracking files committed before this change

Adding a path to `.gitignore` does nothing while Git still tracks it. Apps
created before this change have `.env`, `.mcp.json`, `CLAUDE.md` and
`.openserverless-contract.md` committed, so launch untracks them once:

```
git rm --cached -r --quiet -- <path>
```

The working tree is untouched, so the running app keeps its `.env` and
`.mcp.json`. The step is idempotent — later launches find nothing tracked.

`AGENTS.md` is never untracked: it may already carry committed app-local notes.

# The commit

When the block changed or anything was untracked, launch commits only the
ignore file, reusing `ensureGitIdentity`:

```
git add .gitignore
git commit -m "trustable: manage generated files"
```

Best-effort and non-fatal: a failure is logged and launch continues, consistent
with the other scaffolding steps. It does **not** push — the skills step remains
the only launch-time pusher.

Note that this commit, like the existing `ensureRequiredWorkbenchFolders` and
skills commits, makes the workbench diverge from its remote until something
pushes. That is pre-existing launch behaviour, not introduced here.

# Interaction with git save and pull

`.gitignore` silences only files Git does not track, so both
`gitPullGeneratedFiles` and `gitSaveGeneratedFiles` in [git.go](../git.go) must
keep listing `.mcp.json` and `.openserverless-contract.md`. A repo created
before the migration still tracks them, and Git always reports a tracked
modification; dropping them would make pull refuse forever on those repos.

`gitSaveExcludedFiles` keeps the one rule `.gitignore` cannot express, because
it depends on file content rather than path: an `AGENTS.md` holding nothing but
the managed block is launch output and must not be committed, while one with
app-local notes is real content. The rule is not extended to `CLAUDE.md` — an
ignored symlink is never staged by `git add -A`, so there is no churn to
exclude.
