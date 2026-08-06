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
  ]
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
- `templates` is the `templates=` parameter normalized to `owner/repository`,
  defaulting to `trustable-ai/templates` when absent or malformed;
- `description` is the remaining text with whitespace collapsed.

Unknown `<key>=<value>` tokens are stripped and otherwise ignored, so new
parameters can be added to descriptions without breaking older builds. Private,
archived, and disabled repositories are skipped. Entries are sorted by name.

```
./support/index.py          # regenerate index.json and show what changed
./support/index.py --push   # also commit and push it to trustable-ai/.github
```

A plain run never publishes. `--push` is a no-op when the starter list is
unchanged (`generated` alone is not a reason to commit). The script refuses to
write an empty index, so an API hiccup cannot blank the published list.

# GET /api/starters

Returns the sanitized index:

```json
{ "starters": [ { "name": "...", "repo": "...",
                  "templates": "...", "description": "..." } ] }
```

sorted by `name`, plus an optional `"warning"` string.

The backend re-validates what it reads, because the index is a file that can be
hand-edited: entries without a name or with a `repo` that is not `owner/repo`
are dropped, a `templates` value that does not normalize falls back to
`trustable-ai/templates`, and descriptions are whitespace-collapsed.

The result is cached in-process for 5 minutes. The endpoint never fails the
request: an unreachable or malformed index returns an empty `starters` list plus
a `warning`, so the browser can still offer "My Application Starter".

# Auto template

The `templates` value of the selected starter is sent to `POST /api/repo`
([2-repo.md](2-repo.md)) and stored as `apps.<name>.templates` in the workspace
`trustable.json`. At launch it overrides the global `notebook.repository` for
that app only — see [2a-config.md](2a-config.md) and [notebook.md](notebook.md).
