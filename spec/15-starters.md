This file describes application starter discovery (starters.go)

Put the code in the file `starters.go`

# What an application starter is

An application starter is a **public repository of the `trustable-ai` GitHub
organization** whose description begins with `Trustable:`. The Add Application
modal ([1-applist.md](1-applist.md)) lists them so the user picks a known-good,
convention-compatible repository instead of typing one.

# GET /api/starters

Returns

```json
{ "starters": [ { "name": "trureact",
                  "repo": "trustable-ai/trureact",
                  "templates": "trustable-ai/trureact-templates",
                  "description": "React Generic Starter" } ] }
```

sorted by `name`, plus an optional `"warning"` string.

## Source: always the anonymous public GitHub API

Discovery uses `https://api.github.com/orgs/trustable-ai/repos?type=public`
with a plain `http.Client`, paginated (100 per page) until a short page and
bounded to 10 pages.

It **never** uses `gh` and **never** uses the managed GitHub credentials from
[github.md](github.md). Starters are public, so the list must be identical on a
connected and a disconnected installation, and must never depend on — or
consume the rate limit of — the user's own GitHub account.

- No `Authorization` header.
- `Accept: application/vnd.github+json` and a `User-Agent` are sent; GitHub
  rejects requests without a User-Agent.

Private, archived, and disabled repositories are skipped.

## Filter and parse

Keep only repositories whose description starts with `Trustable:`
(case-insensitive on the marker, the colon is required). For those, everything
after the marker is parsed:

- every `<key>=<value>` token is extracted into parameters and **removed** from
  the display text;
- `Name` is the repository name without the organization
  (`trustable-ai/trureact` → `trureact`);
- `Repo` is the GitHub path without `https://github.com/`, i.e.
  `trustable-ai/<name>`;
- `Templates` is the `templates=` parameter, normalized with the same rules as
  `notebook.repository` ([2a-config.md](2a-config.md)). It defaults to
  `trustable-ai/templates` when absent or malformed;
- `Description` is the remaining text with whitespace collapsed.

Unknown `<key>=<value>` tokens are stripped from the description and otherwise
ignored, so new parameters can be added to descriptions without breaking older
builds.

## Caching and failure

Anonymous requests are limited to 60/hour per IP, so the result is cached
in-process for 5 minutes. A stale entry is kept for 24 hours and served when a
refresh fails (rate limit, network error, malformed payload).

The endpoint never fails the request. With no usable cache it returns an empty
`starters` list plus a `warning`, so the browser can still offer "My
Application Starter".

# Auto template

The `templates` value of the selected starter is sent to `POST /api/repo`
([2-repo.md](2-repo.md)) and stored as `apps.<name>.templates` in the workspace
`trustable.json`. At launch it overrides the global `notebook.repository` for
that app only — see [2a-config.md](2a-config.md) and [notebook.md](notebook.md).
