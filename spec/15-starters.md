This file describes application starter discovery (starters.go)

Put the code in the file `starters.go`

# What an application starter is

An application starter is a **public repository of the `trustable-ai` GitHub
organization** whose description begins with `Trustable:`. The Add Application
modal ([1-applist.md](1-applist.md)) lists them so the user picks a known-good,
convention-compatible repository instead of typing one.

# The static index

Starters are published as a **static `index.json`** in the `trustable-ai/.github`
repository (the `support` submodule), served at

```
https://raw.githubusercontent.com/trustable-ai/.github/refs/heads/main/index.json
```

```json
{
  "generated": "2026-08-06T07:33:04Z",
  "starters": [
    { "name": "trureact",
      "repo": "trustable-ai/trureact",
      "templates": "trustable-ai/trureact-templates",
      "description": "React Generic Starter" }
  ],
  "applications": {
    "Applications": [
      { "name": "documentchatai",
        "title": "AI Document Manager",
        "repo": "https://github.com/trustable-ai/documentchatai",
        "icon": "https://raw.githubusercontent.com/trustable-ai/documentchatai/refs/heads/main/screenshot.png",
        "description": "Manage Documents with AI" }
    ]
  }
}
```

**Trustable never calls the GitHub API.** It reads this one static file over
plain HTTPS. There is no rate limit on raw.githubusercontent.com, no
authentication, and no dependency on the user's GitHub account, so discovery
behaves identically on every installation. `generated` is informational.

## Generating the index — `support/index.py`

`support/index.py` is the **only** thing that talks to the GitHub API. It runs
on the maintainer's machine with the `gh` CLI, so it uses the maintainer's
credentials and rate limit.

It lists the org's public repositories, keeps those whose description starts
with `Trustable:` (case-insensitive marker, colon required), and for each one:

- extracts every `<key>=<value>` token and **removes** it from the display text;
- `name` is the repository name without the organization
  (`trustable-ai/trureact` → `trureact`);
- `repo` is the GitHub path without `https://github.com/`;
- `templates` is the `templates=` parameter normalized to `owner/repository`;
- `description` is the remaining text with whitespace collapsed.

`templates=` is **required to be a starter**: a repository whose description
carries the marker but no `templates=`, or one whose value does not normalize to
`owner/repository`, is left out of `starters`. There is no default templates
repository. Such a repository is not ignored, though — it still contributes to
the application list below, reading the conventional `<name>-templates`
repository (`trutil` → `trutil-templates`) and falling back to the repository
itself when that does not exist. That is how a plain collection of applications
is published without offering a starter.

A `<value>` may be `"quoted"` to carry spaces; a bare one is
whitespace-delimited.

Unknown `<key>=<value>` tokens are stripped and otherwise ignored, so new
parameters can be added to descriptions without breaking older builds. Private,
archived, and disabled repositories are skipped. Entries are sorted by name.

## The application list

Applications are read from an `_index.md` on the main branch of the repository
that holds them: the **templates** repository of a starter, or — for a marked
repository with no `templates=` — its conventional **`<name>-templates`**
sibling, falling back to the repository itself. It lists them one per line:

```markdown
# Applications

- [AI Document Manager](aidocumentmanager.md) Manage Documents with AI
- [AI Email Manager](aiemailmanager.md) Manager Email with AI
```

The linked file **names the template**: `- [<title>](<template>.md) <description>`.
The application itself lives in the repository of that name in the organization,
`https://github.com/trustable-ai/<template>`, not in the starter or templates
repository — those only publish the listing. Each line therefore
carries all three of the entry's text fields: the template is its identity, the
link text its display name, and the trailing prose its description.

`index.py` fetches that file over raw.githubusercontent.com (the same static,
unauthenticated path Trustable itself uses) and turns every matching line into an
entry under the group named by the file's **first `# ` heading** (`Applications`
above, with the `# ` marker removed):

- `name` is the linked `.md` basename without its extension — the template name
  (`aidocumentmanager.md` → `aidocumentmanager`), not the link text;
- `title` is the link text, whitespace-collapsed;
- `repo` is the application's own repository, `https://github.com/trustable-ai/`
  followed by `name`
  (`aidocumentmanager.md` → `https://github.com/trustable-ai/aidocumentmanager`);
- `icon` is the `screenshot.png` published by the application's **own**
  repository,
  `https://raw.githubusercontent.com/trustable-ai/<name>/refs/heads/main/screenshot.png`
  — the file `./screenshot.sh` stages in a workbench, so an application ships
  its own image;
- `description` is the text following the link, whitespace-collapsed.

Lines that do not match the `- [title](template.md) description` shape are ignored, so
headings and prose in `_index.md` are harmless. A repository without an
`_index.md` contributes no applications; if it is a starter it stays in
`starters` regardless, and if it is a marked repository without `templates=` it
contributes nothing at all.

`applications` is therefore an **object keyed by group name**, not a flat list.
Only a top-level `# ` heading names a group — `##` and deeper are ignored, and
the first one wins even if it appears below the list. Two templates repositories
whose `_index.md` share a heading share the group, which is how related starters
are presented together. A file with entries but no `# ` heading falls back to the
starter's own name and says so on stderr. Groups are sorted by name, and entries
within a group by title then name.

Every `icon` is checked with a `HEAD` request and a missing one is **warned
about, not fatal**:

```
warning: trustable-ai/aiemailmanager: no icon for AI Email Manager — https://raw.git…/aiemailmanager/refs/heads/main/screenshot.png

4 of 7 applications have no icon published.
```

The entry stays in the index with its `icon` set, so the image can be added to
the application's repository later without regenerating anything. Applications
missing an icon are also marked `(no icon)` in the run summary. A network failure
during the check counts as missing, so a connectivity problem produces noisy
warnings rather than a failed run.

Every application `repo` is checked the same way and is likewise **warned about,
not fatal** — it is derived from the template name, so a typo in `_index.md`, or
a template listed before its repository was created, points at nothing:

```
warning: no repository for AI Email Manager — https://github.com/trustable-ai/aiemailmanager

4 of 16 applications point at a repository that does not exist.
```

Those entries are marked `(no repo)` in the run summary and stay in the index,
so the repository can be created later without regenerating anything. The check
uses `gh api repos/<owner>/<repo>` rather than an anonymous request, because a
private repository is 404 to an anonymous caller yet matters here — an
application the user cannot clone is as broken as one that does not exist. Each
distinct repository is probed once even when several `_index.md` list it. Only a
definite 404 is reported as missing: when the probe cannot answer at all (no
`gh`, not authenticated, network down) the run says how many repositories went
unchecked instead of claiming they are absent.

```
./support/index.py          # regenerate index.json and show what changed
./support/index.py --push   # also commit and push it to trustable-ai/.github
```

A plain run never publishes. `--push` is a no-op when the starter and application
lists are both unchanged (`generated` alone is not a reason to commit). The
script refuses to write an index with no starters, so an API hiccup cannot blank
the published list.

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
`trustable.json`. At launch it overrides the global `notebook.repository` for
that app only — see [2a-config.md](2a-config.md) and [notebook.md](notebook.md).
