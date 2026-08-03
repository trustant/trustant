# Files (read-only viewer)

Read-only browser for the files of the currently-launched application, so the
user can inspect what the assistant generated without leaving Trustable.

Backend code lives in `files.go`, frontend in the Files modal of `app.html`.

The viewer never writes. There is no create, edit, rename, or delete path, and
that is what makes it safe to expose over the whole workbench.

# API

Both endpoints are `GET` only. Any other method returns 405. Each call starts
with the expiry guard and validates `<name>` against `namePattern`; an invalid
name is 400 and a missing workbench directory is 404.

## `GET /api/files/<name>`

Returns the file tree of `$WORKBENCH_DIR/<name>` as a flat list:

```json
{
  "files": [
    { "path": "src",         "dir": true,  "size": 0 },
    { "path": "package.json","dir": false, "size": 1234 },
    { "path": "src/App.tsx", "dir": false, "size": 517 }
  ],
  "truncated": false
}
```

`path` is always relative to the workbench root and slash-separated.
Directories sort before files; within each group, paths sort lexicographically.

These directories are never descended into, because they would swamp the
listing: `.git`, `node_modules`, `dist`, `build`, `.venv`, `__pycache__`,
`.next`.

Dot-prefixed entries are hidden except `.env`, `.env.production`, `.agents`,
and `.claude` — the last two so assistant-installed skills under
`.agents/skills/` stay visible. `.claude` is a symlink to `.agents`; the walk
does not follow symlinks, so it is listed as an entry but not descended into,
and its contents are reached through `.agents`.

The walk stops after 5000 entries and sets `"truncated": true`, so a runaway
tree cannot wedge the UI. An unreadable subdirectory is skipped rather than
failing the whole listing.

## `GET /api/files/<name>?path=<relpath>`

Returns one file:

```json
{ "path": "src/App.tsx", "content": "...", "size": 517, "binary": false }
```

- Files larger than 1 MB return 413. The viewer is for source files.
- Binary content — a NUL byte in the first 8 KB, or invalid UTF-8 — returns
  `"binary": true` with empty `content`, rather than dumping bytes into the
  browser.
- A missing file returns 404; a directory returns 400.

### Path containment

`<relpath>` must resolve to a location inside the workbench. Both of these are
rejected with a uniform 400 `Invalid path`:

1. **Lexical traversal** — the path is absolute, carries a volume name, or has
   any `..` element.
2. **Symlink escape** — after `filepath.EvalSymlinks` on both the workbench root
   and the target, the resolved target must still sit inside the resolved root.

The second check is the one that matters: the files being browsed are written by
the assistant, so a symlink pointing at `~/.ssh` or the host `.env` is a
plausible thing to find in a workbench, and without resolution it would read
straight through. A symlink that stays inside the workbench is allowed.

The error message is deliberately identical for every rejection, so responses
cannot be used to probe what exists outside the workbench.

# UI

The **Files** entry sits in the Utils pulldown of the workbench toolbar, between
*Debug* and *Upload* (see [3-app.md](3-app.md)). Utils holds the inspection
actions; the Config pulldown is for configuration.

Selecting it opens a modal built from the shared Trustable primitives, sized
`max-w-5xl max-h-[80vh]`, with two panes:

- **Left (~1/3, scrollable)** — the file tree, built from the flat list by
  splitting on `/`. Directories are collapsible, marked `▾` when open and `▸`
  when collapsed, and indent 12px per level. The selected file is highlighted.
  A truncated listing shows a notice above the tree.
- **Right** — the content pane. It shows the file path, a **Copy** button that
  copies the raw content to the clipboard, and the content itself.

**All content is inserted with `textContent`, never as HTML.** Markdown is shown
as its source rather than rendered: these files are written by the assistant, and
the workbench page shares an origin with the config and publish APIs, so
rendering assistant-authored markup would be an XSS vector against those
endpoints. Rendering may be reintroduced only alongside a real sanitizer.

Binary and oversize files show a plain message in place of content.

The tree is fetched when the modal opens; there is no polling. A **Refresh**
control re-fetches it for when the assistant has written new files while the
modal is open.

The modal closes via the X button, the Close button, Escape, or a backdrop
click.
