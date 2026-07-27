# TruACP notebooks

A notebook is an ordered collection of predefined prompts loaded into the
existing TruACP conversation and executed through the normal ACP prompt path.
When no notebook is loaded, ordinary chat behavior is unchanged.

## Source and security

The default source is `trustable-ai/notebooks` on branch `main`. A source may be
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
| **Notebook** | Opens the side panel and loads the default source on first open |
| **Run next** | Runs the selected notebook node; disabled without a selection |

The panel contains editable source and branch/ref fields, the indexed notebook
list, load/save controls, and add/remove controls. There is no rename
operation: rename is deliberately remove plus recreate under the new name.
There is no token field or token prompt.

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

Running the final node clears selection and disables **Run next** while keeping
the notebook loaded and editable.

Edit copies the node prompt into the normal composer. Submitting updates the
node, runs it immediately, appends its output, marks the notebook dirty, and
advances selection.

Normal composer input while a notebook is loaded becomes an ad-hoc input node:

1. it is inserted before the selected notebook node, or appended if selection
   is clear;
2. it runs through the normal ACP prompt path;
3. notebook selection does not advance.

An ad-hoc node can be pinned. Pinning promotes it to a notebook node with the
standard controls, marks the notebook dirty, and includes it in saves. Unpinned
inputs and all model/tool output are excluded from GitHub saves.

## APIs

TruACP exposes server-side routes:

- `POST /api/notebooks/index`
- `POST /api/notebooks/load`
- `PUT /api/notebooks/save`
- `POST /api/notebooks/add`
- `POST /api/notebooks/remove`
- `POST /api/sessions/notebook/get`
- `PUT /api/sessions/notebook`

The GitHub routes accept repository/ref/path/SHA metadata, never a token. The
session routes persist a whitelisted notebook sidecar under `.acp-data`; unknown
fields are discarded.

## Session behavior

Notebook identity, source/ref, file and README SHAs, ordered notebook/ad-hoc
nodes, execution outputs, selected node, and dirty state are stored beside the
ACP session. Load/resume reads the same sidecar. Fork copies it to the new
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

- Trustable's main **Configure** screen owns the global notebook repository,
  branch/ref, and write token.
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
  notebook panel displays the active source read-only and offers Refresh, but
  no source configuration fields.
- Public index/load operations continue without a token. Save, add, and remove
  are disabled whenever `hasToken` is false, with guidance to use Trustable
  Configure.
