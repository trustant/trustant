This file describes application starter discovery (starters.go)

Put the code in the file `starters.go`

# What an application starter is

An application starter is a **public repository of the `trustable-ai` GitHub
organization** whose description begins with `Trustant:`. The Add Application
modal ([1-applist.md](1-applist.md)) lists them so the user picks a known-good,
convention-compatible repository instead of typing one.

# Where starters come from

Starters and applications are published as a **static `index.json`**, generated
from the `trustable-ai/.github` repository by `support/index.py` — its format
and generation rules are specified in [15a-index.md](15a-index.md).

The canonical URL is **`https://trustant.ai/index.json`**, served by GitHub
Pages.

**Trustant never calls the GitHub API.** It reads that one static file over
plain HTTPS, so discovery behaves identically on every installation.

**The host is part of the contract.** This was previously read over
`raw.githubusercontent.com`, which rate-limits: it was observed returning
`429: Too Many Requests`, which makes `fetchStarterIndex` fail and leaves the
Add Application modal empty with a "Could not load application starters"
warning. Pages exists to be served and does not throttle that way.

The choice covers the icons as well. Each application's `icon` is published
*inside* this document, and on `trustant.ai` those URLs are
`https://trustant.ai/images/trustable-ai-<name>.png` — so the browser's
per-tile image requests leave the throttled host together with the index. Under
the old host a 429 could blank the tiles even when the index itself came from
cache. `sanitizeApplications` still accepts any `https://` icon and is
deliberately not host-specific, so a future republish elsewhere needs no code
change.

# GET /api/starters

Returns the sanitized index — both halves of it:

```json
{ "starters": [ { "name": "...", "repo": "...",
                  "templates": "...", "description": "..." } ],
  "applications": { "Apps": [ { "name": "...", "title": "...", "repo": "...",
                                "icon": "...", "description": "..." } ] } }
```

`starters` is sorted by `name`. `applications` keeps the published order within
each group (`index.py` already sorts by `(title, name)`). An optional
`"warning"` string may accompany either.

The backend re-validates what it reads, because the index is a file that can be
hand-edited.

For a **starter**: entries without a name or with a `repo` that is not
`owner/repo` are dropped, a `templates` value that does not normalize falls back
to `trustable-ai/templates`, and descriptions are whitespace-collapsed.

For an **application**:

- `repo` is published as a full `https://github.com/<org>/<slug>` URL and is
  **reduced to `owner/repository`** here (a trailing `/` or `.git` is stripped),
  so the frontend and `POST /api/repo` see the same shape a starter carries. An
  entry whose `repo` does not reduce to `owner/repository` is dropped.
- an entry without a `name` is dropped;
- an empty `title` defaults to `name`, so a tile always has a label;
- an `icon` that is non-empty but not an `https://` URL drops the entry — the
  index is hand-editable and a junk value must not put a broken `<img>`, or a
  `javascript:` URL, into the modal. An **empty** icon is kept: `index.py`
  deliberately publishes an entry whose icon is not uploaded yet, and the tile
  falls back to a placeholder.
- `title` and `description` are whitespace-collapsed;
- a group with a blank name, or one left empty after filtering, disappears.

Both halves come from the same single fetch and share one in-process 5-minute
cache. The endpoint never fails the request: an unreachable or malformed index
returns an empty `starters` list, an empty `applications` object, and a
`warning`, so the browser can still offer "My Application Starter".
`applications` is **always an object and never `null`** — the frontend iterates
it unconditionally.

# Auto template

The `templates` value of the selected starter is sent to `POST /api/repo`
([2-repo.md](2-repo.md)) and stored as `apps.<name>.templates` in the workspace
`trustant.json`. At launch it overrides the global `notebook.repository` for
that app only — see [2a-config.md](2a-config.md) and [notebook.md](notebook.md).
