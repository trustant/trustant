# TruACP templates

A template is an ordered collection of predefined prompts loaded into the
existing TruACP conversation and executed through the normal ACP prompt path.
When no template is loaded, ordinary chat behavior is unchanged.

**Templates** is the user-visible name throughout the UI. `notebook` remains the
wire name — types, API routes, `NOTEBOOK_GITHUB_*` environment variables, CSS
classes, and the session sidecar — so existing workspaces and persisted sessions
continue to work without migration.

## Source and security

The default source is `trustable-ai/templates` on branch `main`. A source may be
entered as `owner/repository` or a `https://github.com/owner/repository` URL;
the branch/ref is independently configurable.

Public repository browsing and loading work without credentials. Mutations use
only `NOTEBOOK_GITHUB_TOKEN` read by the TruACP server from its process
environment or server `.env`.

- The browser never asks for, receives, persists, logs, or renders the token.
- API responses expose only `hasToken: boolean`.
- When the token is absent, save/add/remove controls are disabled and the panel
  tells the operator to set `NOTEBOOK_GITHUB_TOKEN` in the server `.env`.
- The token is not added to generated application `.env`, `.env.production`,
  application config maps, chat messages, model context, notebook Markdown, or
  commits.
- Repository owner/name, GitHub URL, branch/ref, and Markdown paths are
  validated server-side. Paths are repository-relative `.md` files and cannot
  traverse directories or target `README.md`.
- Writes compare the SHA loaded by the browser with the current GitHub SHA and
  send that SHA to the GitHub Contents API. Stale writes return a conflict and
  never silently overwrite a newer file.

Notebook-file and `README.md` mutations require separate GitHub commits. Add
creates the file before indexing it; remove updates the index before deleting
the file. If the second mutation fails, the API reports an explicit
`Partial mutation` error describing what succeeded.

## Repository formats

A notebook is a Markdown file whose prompts are separated by a line containing
only `---`:

```markdown
First prompt text

---

Second prompt text
```

Parsing preserves prompt text and normalizes line endings. Saving is
deterministic: prompts are joined with `\n\n---\n\n` and the file ends with one
newline.

`README.md` is the index. Entries have this shape:

```markdown
- [name](notebook-file.md) optional comment
```

Index mutation preserves unrelated headings and prose.

## Toolbar and panel

The toolbar adds:

| Control | Behavior |
| --- | --- |
| **Templates** | Opens the side panel and loads the default source on first open |
| **Run next step** | Runs the selected node; disabled without a selection |
| **Run all steps** | Runs every step from the selection to the end |

The panel shows the active source read-only with a Refresh action, the indexed
template list, load/save controls, and add/remove controls. There is no rename
operation: rename is deliberately remove plus recreate under the new name.
There is no token field or token prompt.

### The working copy

A template is edited as `template.md` at the root of the launched application's
workbench checkout. GitHub is the catalog it is copied from and saved back to;
the working copy is what the session runs and edits, so a template travels with
the application and is published with it.

The panel shows the working copy above the catalog, with its name and origin, an
**Open** action, and — when it has been edited — a **Changed** badge plus
editable name and file fields and a **Save to GitHub** button.

Selecting a catalog entry copies it into the workbench. Replacing a working copy
that has unsaved edits asks for confirmation first; replacing an unedited one is
silent.

### Repositories without a write token

Such a repository cannot be published to, but the catalog still reads and the
working copy still saves locally. The panel therefore **hides**, rather than
disables, only what cannot work:

- no read-only warning banner;
- no add-template section;
- no per-entry remove control;
- no Save to GitHub button.

A single unhighlighted note reads *"add in configuration your github token to
edit templates"*. It carries no border, background, or alert color — nothing is
wrong, the upstream controls are simply absent. The Changed badge and the
editable fields still appear, because that is how the user learns their edits
are local-only.

## Conversation model

Loading a notebook renders each prompt as a visually distinct notebook node
and selects the first node. Each notebook node has radio/select, run, edit, and
remove controls.

The conversation presents nodes as compact tasks rather than full prompt
documents. A node title is derived from the first Markdown heading, falling
back to the first meaningful line, and is bounded to a single concise label.
The complete unchanged prompt remains available under a collapsed, bounded
**Task details** disclosure.

Running the selected node, either from the node or **Run next**:

1. sends exactly that prompt through the existing ACP session;
2. streams reasoning, tool activity, and model output directly below the node;
3. advances exactly once to the next persisted notebook node, skipping unpinned
   inputs.

Assistant output is the primary highlighted content inside the node. Tool calls
are grouped separately under **Activity** in a scrollable window with exactly
three visible rows; the window follows the latest operation while preserving
the complete tool history. This presentation does not alter persisted output
order or the normal ACP event path.

Running the final node clears selection and disables **Run next step** while
keeping the template loaded and editable.

**Run all steps** runs every notebook node from the current selection to the
end, or from the first node when selection is clear. Steps are awaited one at a
time because they share a single ACP session; ad-hoc unpinned inputs are
skipped; each prompt is re-read at execution time so an edit applied mid-run is
the version that executes; and a failed step ends the run rather than firing the
remaining prompts into a broken session. Starting a new session stops an
in-flight run.

Edit is **in place**. It replaces the node's task body with a prompt editor and
swaps Run/Remove for **Save** and **Cancel**. Cancel discards the draft. Save
commits the prompt, marks the template dirty, and persists it; it does **not**
run the node — running stays on Run, Run next step, and Run all steps. The
composer is unaffected and remains available for ad-hoc input while a node is
being edited.

Normal composer input while a notebook is loaded becomes an ad-hoc input node:

1. it is inserted before the selected notebook node, or appended if selection
   is clear;
2. it runs through the normal ACP prompt path;
3. notebook selection does not advance.

An ad-hoc node can be pinned. Pinning promotes it to a notebook node with the
standard controls, writes the working copy, and includes it in saves. Unpinned
inputs and all model/tool output are excluded from GitHub saves.

A template can also start from nothing. With none loaded there are no ad-hoc
nodes to pin, so each of the user's own chat messages carries a **Pin** action:
the first pin creates a working copy with a blank name, converts that message
into the first step, and switches the conversation into template mode. The
message's assistant reply is carried across so pinning does not discard what the
step produced. The name is filled in later, when the template is saved.

## The template file

`template.md` carries front matter recording where it came from:

```markdown
---
name: Build
repo: trustable-ai/templates
file: flows/build.md
edited: false
---

first prompt

---

second prompt
```

All four keys are always emitted, even when empty — a blank `name:` is how a
template started from pinned chat records that it has no origin yet. Unknown
keys survive a round trip.

`edited` is what "changed" means: set by any local edit, cleared by a successful
save back to GitHub. It is a flag, never a content comparison, and only the
server clears it.

`repo` is **informational**. It records the origin so it stays visible, but it
never selects a write destination — save-back always resolves the repository
through the one configured in Trustable, so a template file cannot redirect an
authenticated write. A template copied from another catalog therefore saves into
the configured repository under its recorded file name.

Front matter is split from the body before prompts are parsed. The block
delimiter and the prompt separator are the same `---` token, so the block is
identified positionally, as a strict file prefix.

The local file name is a server-side constant; no part of it comes from the
request, so the local path has no traversal surface. In particular `file`
records a path inside the template repository and is never joined onto a
filesystem path. Serialization is shared with the GitHub path, so a template
saved locally is byte-identical to the same template saved upstream.

## Saving

Editing is write-through: every change to the prompt set writes `template.md`
immediately, so durability never depends on a panel button.

Staging is best-effort and stops at `git add` — the application's own save in
Trustable already commits and pushes, so the template rides along with the
user's other changes instead of producing commits they did not ask for. A
workbench that is not a git checkout, or a failing `git`, still leaves the
written file and reports `staged: false`.

**Save to GitHub** publishes the working copy under the name and file the user
chose, then clears `edited`. Prompts are read from `template.md` server-side, so
a stale browser cannot publish content the workbench never held. A renamed or
brand-new template also updates the repository's `README.md` index so it appears
in the catalog; the file and the index are separate commits, and a failure
between them is reported explicitly rather than left half-applied.

## APIs

TruACP exposes server-side routes:

- `POST /api/notebooks/index`
- `POST /api/notebooks/load`
- `POST /api/notebooks/select`
- `POST /api/notebooks/add`
- `POST /api/notebooks/remove`
- `POST /api/notebooks/local`
- `PUT /api/notebooks/save-local`
- `PUT /api/notebooks/save-template`
- `POST /api/sessions/notebook/get`
- `PUT /api/sessions/notebook`

The GitHub routes accept repository/ref/path/SHA metadata, never a token. The
local routes accept provenance and prompts only, and resolve the destination
server-side from the active project directory; `save-template` accepts a name
and file and reads the prompts from the working copy. The session routes persist
a whitelisted notebook sidecar under `.acp-data`; unknown fields are discarded.

## Session behavior

Template identity, source/ref, ordered notebook/ad-hoc nodes, execution outputs,
selected node, dirty state, and the provenance block are stored beside the ACP
session. Load/resume reads the same sidecar. Fork copies it to the new
session so subsequent changes diverge independently. Starting a new session
does not inherit notebook state.

ACP tool titles and statuses are adapter-owned display metadata. The browser
and persistence boundary normalize missing, empty, structured, or oversized
values to bounded strings (`(tool)` and `unknown` fallbacks) instead of
rejecting the complete notebook state. Structural node and tool-call IDs remain
strictly validated.

Errors remain visible and recoverable. A read, conflict, authentication, or
partial-mutation failure does not discard the loaded notebook or reorder the
conversation.
# Trustable-managed configuration ownership

This section supersedes earlier references in this specification to editable
source/ref controls in TruACP or to configuring `NOTEBOOK_GITHUB_TOKEN` in the
TruACP server `.env`.

- Trustable's main **Configure** screen owns the global template repository,
  branch/ref, and write token, under a **Template Repository** heading.
- Repository/ref are persisted in workspace `trustable.json`; the token is
  write-only and stored separately in a mode-`0600` file under
  `<WorkspaceDir>/.trustable/secrets/`.
- `GET /api/configuration` returns repository/ref plus only `has_token`.
  `POST /api/configuration` preserves an omitted token and supports an explicit
  clear action.
- Launch injects `NOTEBOOK_GITHUB_REPOSITORY`, `NOTEBOOK_GITHUB_REF`, and
  `NOTEBOOK_GITHUB_TOKEN` only into the TruACP process. These values never enter
  generated application env files, app config maps, notebook/session state,
  project assets, logs, commits, or model context.
- Managed TruACP treats the injected repository/ref as authoritative. Its
  template panel displays the active source read-only and offers Refresh, but
  no source configuration fields. This authority extends to save-back: the
  `repo` recorded in a template's front matter is informational and is
  overridden by the injected repository, so a template file can never redirect
  an authenticated write.
- Public index/load/select operations continue without a token. When `hasToken`
  is false the add, remove, and Save to GitHub controls are hidden; editing
  still writes the application's local `template.md` as described above.
