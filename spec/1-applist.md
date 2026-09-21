This file describes the application list page, put the code in file `applist.html`

# Version

When the page loads invoke the version api.

If it expired show a page with only a centered message saying "This version expired. Please Update. For info email: info@nuvolaris.io"

# Provider guard

Before rendering, fetch `GET /api/configuration` and check the merged config's `provider` field. If absent (or the call fails), redirect to `index.html` so the user goes through the **Provider Choice** flow (see [1-index.md](1-index.md)). The same guard applies to all pages that assume a configured provider: `applist.html`, `app.html`, `appconfig.html`, `configure.html`.

## modelsVersion guard

After the provider guard passes, fetch `GET /api/status` and apply the same per-provider `modelsVersion` reselect check as the splash (see [1-index.md](1-index.md) §"Provider choice"). When `status[provider].modelsVersion` differs from `config.model_versions[provider]`, persist the new value and redirect to `configure.html?reselect=1`. Own-host Ollama is exempt — see §"Detecting own-host Ollama" in [2a-config.md](2a-config.md).

# App List Page

The page shows the Trustable logo (`trustable-logo.svg`), product name, and the
version returned by the version API as a compact release tag.

The release tag shows `<version> · <stream>`.
The tag tooltip reports the source branch and exact build label. This lets local,
development, feature-stream, and release images remain distinguishable without
hardcoding labels in the page.

Immediately **after the title** (below the version line, above the Applications card), show a **Credits box** — but only when the merged configuration's `provider` field equals `"trustable"`. For any other provider the box is not rendered. See "Credits" below.

Show also in smaller font at the end of the page "Expiration date: <date>"

The footer line built from the version API reads:

```
Build: <build> — Expires: <date> — Task: <6-char hash>
```

`Task` is the `tasks` field — the `OPS_OLARIS` commit shortened to six
characters, which identifies the ops tasks in use at a glance. The ops
**version** deliberately does not appear here; the full `ops -info` table lives
on the Configure page (see [2a-config.md](2a-config.md)). Each of the three
segments is conditional on its value being present, and the ` — ` separators
adapt when any is missing.

## Terminal

The top bar carries a **Terminal** button, before Configure. It opens a modal
with a real shell running in `$WORKBENCH_DIR` itself — **not** in any one app's
checkout, because an app has no workbench until it is launched, so a per-app
terminal here would be unavailable for most cards. It connects to
`/api/terminal/` with no app name. See [12-terminal.md](12-terminal.md).

It will list the applications, using the backend api. The page includes compact
summary metrics for total applications, production-configured applications,
development-only applications, and repositories. The list supports client-side
search, sort by name or repository, segmented filters for all/production/
development-only, a grid view, a denser list view as the default, and a visible count of
matching applications. Searching filters by app name, repository, production
host, and production user without calling the backend again.

The visual language follows the current Nuvolaris website system: Work Sans for
UI text, lightweight headings, a near-paper background, small 4px radii, thin
neutral rules, black primary actions, restrained outline secondary actions, and
controlled cyan/teal accents. JetBrains Mono is reserved only for rare symbolic
or code-like accents, not for ordinary operational labels. The application list
table keeps one consistent Work Sans family, compact size, medium weight, and
zero letter spacing across headers, cells, links, badges, and action buttons.
List table text is rendered in the same uppercase micro-label style as the
`APPLICATION` header cell; this is a visual transform only and must not mutate
repository names or app names in data.
Shared operational controls (`nu-btn`, `nu-chip`, `nu-status-pill`,
`nu-segment-btn`, menu items, and shared tables) use the same smaller uppercase
micro-label treatment so modal actions, toolbar controls, and secondary screens
do not look typographically unrelated. Generic table headers use the micro-label
style, while generic table body values preserve their original casing; the app
list is the explicit exception where the full list view uses uppercase as a
visual transform.

# Shared Trustable visual system

`applist.html` is the source of truth for the Trustable first-party visual
system. Apply the same Nuvolaris-style graphic language to these Trustable
pages as they are migrated:

- `web/index.html` — splash, provider choice, sign-in/configuration modals.
- `web/configure.html` — provider, model, OpenCode, and Git user settings.
- `web/app.html` — workbench top bar, menus, dialogs, and utility modals.
- `web/appconfig.html` — per-app environment editor.
- `web/debug.html` — activation log utility window; it uses the shared light
  shell and controls, with monospace reserved only for streamed log output.

Do not include `web/template.html` in this rollout. It is embedded as a
generated application placeholder (see [4-launch.md](4-launch.md)), not a
first-party Trustable page.

Shared primitives:

- Load Work Sans on every first-party page and keep JetBrains Mono only for
  code, logs, paths, keys, and command output.
- Keep the current Nuvolaris palette tokens: `--nu-ink`, `--nu-paper`,
  `--nu-muted`, `--nu-rule`, `--nu-brand`, `--nu-brand-soft`, `--nu-accent`,
  `--nu-signal`, and `--nu-danger`.
- Reuse compact shells, top bars, panels/cards, modal surfaces, tables, form
  controls, segmented controls/chips, status pills, dropdowns, and primary /
  secondary / danger buttons from the applist visual language.
- Use thin neutral borders, near-paper backgrounds, 4px radii for most
  rectangular UI, and restrained accent color. Keep saturated colors only for
  clear semantic states or existing product imagery.
- Preserve every existing DOM id, API call, cookie, URL, form field, button
  handler, provider choice path, model selection flow, launch link, git action,
  and app environment editing behavior while restyling.
- Keep the frontend plain HTML plus Tailwind loaded from `web/tailwind.js`.
  Shared visual primitives live in `web/trustable-ui.css`; pages should link
  that stylesheet rather than reintroducing page-local copies of the same
  palette, typography, button, panel, table, and input rules.
- **Never copy to the clipboard through `navigator.clipboard` alone.** Trustable
  is served over plain `http` on a `nip.io` host, which browsers do not treat as
  a secure context, so `navigator.clipboard` is **undefined** — reading
  `.writeText` off it throws rather than rejecting, and a copy button written
  that way silently does nothing. Every copy action must guard on
  `navigator.clipboard && window.isSecureContext` and otherwise fall back to
  selecting a real focusable node and calling `document.execCommand('copy')`.
  The fallback must `focus()` the node as well as selecting it, because
  `execCommand` copies the *focused* element's selection.

Authentication is a separate future stream. Do not add sign-in, account,
session, or authorization controls in this visual-system migration; leave
header/action layouts flexible enough that a later auth stream can add its own
hook without reworking the page structure.

For each application lists a <name>, a <repo> , a link "Development" to access the local application, and optionally a "Production" link and a "Repository" link. Development and Production links show an external-link icon so they are recognizable as links.
Application action buttons use restrained, light styling and fixed inline labels:
Edit, Env, Git Pull, Git Push, Publish, Undeploy, and Delete must not wrap inside the
list view. Undeploy sits between Publish and Delete in both the card and the list view.
In the list view the action buttons are left-aligned, so the row starts directly under
the "Actions" column header rather than being pushed to the right edge. The
Development/Production status badge is centered in its column, and the "Status" column
header is centered to match.
Delete keeps a distinct pale red treatment.

The application list uses the roomiest page shell (`nu-shell-full`, capped at 1760px)
with reduced horizontal page padding for both the top bar and the main content, so the
list view has enough width for the full action row without squeezing the Application
and Repository columns. Other pages keep the default `nu-shell`.

The Development link points to `<protocol>://<name>.<domain>` by replacing the
first label of the current Trustable host. For example, from
`http://trustable.<node-ip>.nip.io:8910` it points to
`http://<name>.<node-ip>.nip.io:8910`, and from
`https://trustable.<base-domain>` it points to
`https://<name>.<base-domain>`.

In the `run.sh` Lima development environment the app-specific nip.io request
reaches the Trustable server on port 8910, not the k3s ingress directly. For a
host prefix that matches an existing application, the hostname middleware must
reverse-proxy the request to `<name>.<development OPS_APIHOST>` inside the VM
(for example `trutest1.miniops.me`) and rewrite the upstream `Host` header so
the k3s ingress selects that application's routes. Unknown prefixes must still
return the Invalid Hostname page. In the packaged k3s deployment, the external
ingress normally routes `<name>.<base-domain>` before the request reaches this
fallback.
The Production link is shown when both `OPS_APIHOST` and `OPS_USER` are defined in `.env.production`. It points to `<protocol>://<opsuser>.<domain>` where `<protocol>://<domain>` comes from OPS_APIHOST and `<opsuser>` comes from OPS_USER.
The Repository link is shown when `OPS_REPO` is defined in `.env.production`. It points to `https://github.com/<opsrepo>` where `<opsrepo>` is the value of OPS_REPO.

You can
- configure (general) — opens `index.html?choose=1`, which forces the **Provider Choice** modal so the user can switch between Ollama (internal or own-host) and Trustable Cloud (see [1-index.md](1-index.md)). It does **not** open `configure.html` directly.
- add applications
- remove applications — this also removes the ops user, the bare repo, the
  workbench checkout, the `apps.<name>` config entry **and every variable the
  application shared** with the others (see "Leaving the pool" in
  [18-shared.md](18-shared.md#leaving-the-pool)). Leaving the shared variables
  behind would strand resolved secrets in the workspace config and let a later
  application of the same name inherit them.
- edit applications
- env (configure application environment variables)
- git pull (pull fast-forward updates from the configured production repository when available, otherwise the original application repository)
- git push (push code to a production GitHub repository) — server-side gated, see "Publishing authorization" in [6-publish.md](6-publish.md)
- publish (deploy to a production OpenServerless environment) — server-side gated, see "Publishing authorization" in [6-publish.md](6-publish.md)
- undeploy (remove the application's deployed actions and packages) — see "Undeploy" below

Application action buttons should use a restrained visual style: very light
cyan-tinted secondary backgrounds, soft brand-tinted borders/text, and subtle
hover states.
Avoid assigning a different saturated color to every action. Destructive
actions may keep a red text/border treatment, but should not use a solid red
background in the normal state.

Feedback UI on the app list uses the shared Trustable primitives from
`web/trustable-ui.css`: status banners, progress masks, confirmation dialogs,
result blocks, and error/success/warning messages are rendered as `nu-modal`,
`nu-btn`, `nu-input`, and `nu-feedback` surfaces. Destructive confirmations such
as app Delete remain clearly marked with the pale danger treatment, but the
confirming button is not a saturated solid-red button. Result text and command
output use JetBrains Mono inside the feedback block; ordinary dialog copy stays
in Work Sans.

## Publishing gate

The Git Push and Publish buttons are always rendered and always call their respective backend APIs. The backend verifies the installed license (see [6-publish.md](6-publish.md) and [14-license.md](14-license.md)) and returns HTTP 402 with `{"error": "License required: ..."}`, or `{"error": "Host not licensed: ..."}` when publishing to an apihost the license does not cover. `isLicenseError` recognizes both prefixes and `showLicenseModal` offers to paste a license; any other error is surfaced verbatim in the existing result modal.

## Adding an application

The modal is a **tabbed browser** over the published index
([15-starters.md](15-starters.md)). A horizontal, horizontally-scrollable tab
strip sits above the body:

- **Starter** — the first tab, selected by default, described below and
  unchanged by the catalog;
- one tab per group of `applications`, ordered by group name (which reproduces
  the published order, since `index.py` sorts groups by name): `Apps`, `Chat`,
  `Demo`, `Utilities`, …

When the index publishes no applications — or could not be loaded — **no tab
strip is rendered at all** and the modal is exactly the Starter form.

Switching tabs never loses the other tab's state: the Starter tab keeps its
radio selection and any typed name. Reopening the modal always resets to
**Starter** with the catalog back at its carousel.

### Application tabs

While an application tab is active the modal grows to **half the viewport width
and 80% of the viewport height**. It is centred in a full-viewport scrim, so the
80% height leaves **10% of the page free above and below**. The Starter tab
keeps the default modal size, so the larger size lasts only as long as the
catalog is being browsed — leaving the tab, closing the modal, or reopening it
all restore the default. Below 900px wide, where half a viewport would be
unusable, the width falls back to the near-full width the modal uses elsewhere.

Each tab shows **one application at a time**, full width, with a **left and
right arrow** either side stepping through the group in published order. The
arrows **wrap** at both ends — a group is a short ring, not a list with a start
and a finish — and are hidden entirely for a group of one, where they would do
nothing. `ArrowLeft` / `ArrowRight` step the carousel too, but only while the
carousel is what the modal is showing: never on the Starter tab and never while
the name panel is up. A **"<n> of <total>"** counter sits under the carousel,
and is omitted for a group of one.

Switching tabs restarts at the first application of the group now showing.

The application on show is a card filling the height the modal gives it, holding,
top to bottom:

- the **title** at the **top**, as a link to `https://github.com/<repo>` opening
  in a new tab. Following the link must **not** also open the name panel — the
  same boundary the starter rows observe between their link and their radio;
- the **icon**, lazily loaded, taking whatever height is left between the two.
  An entry whose icon is empty, or whose image fails to load, renders a neutral
  placeholder instead — `index.py` deliberately keeps an entry whose icon is not
  published yet, so a missing image is expected and must never leave a
  broken-image glyph;
- the **description** at the **bottom**, shown in full.

The icon is the element that absorbs the spare height, so the title stays pinned
to the top and the description to the bottom whatever the card's size and
however long the prose is.

The card — anywhere but the link — is clickable and keyboard-activatable (Enter
or Space) and opens the name panel.

### Name panel

Clicking the card replaces the carousel and its counter, in place, with a small
panel: a line naming the chosen application and the repository it will be
created from, an **Application Name** field, and **Cancel** / **Confirm**.

**Cancel returns to the carousel** rather than closing the modal — the user is
picking, not aborting. The modal's own Cancel and a scrim click still close it.

The name is prefilled with the entry's `name`, which the generator already
guarantees is a legal slug, so it is used as published. When an application of
that name already exists the smallest free integer is appended — `tetris`,
`tetris1`, `tetris2`. The search runs against the loaded application list, so it
is global: an entry listed under two groups (as `truk8s` is, under both `Demo`
and `Utilities`) is still counted once against the user's whole list. A slug
occupying all 20 characters is trimmed to leave room for the digits rather than
producing a name the pattern would reject.

The prefill is a starting point, not a constraint: the field is freely editable,
and the same `[a-zA-Z][a-zA-Z0-9]{5,19}` and duplicate-name checks the Starter
tab applies on Create run here on Confirm, rendering failures in the same inline
style.

On **Confirm** the application is created from **that entry's own repository**,
so two applications in one group produce different apps. **No `templates` is
sent** — the app falls back to the global `notebook.repository`. Everything
downstream (the creating modal, the `missing_env` path, the list refresh) is
shared with the Starter path.

The GitHub datalist notice, the GitHub connect form and the SSH key fallback are
**Starter-only**; a catalog application's repository is fixed and public.

### Starter tab

The Starter tab is starter-driven. Under the heading **"Add an application
starter"** it shows a vertical radio list built from `GET /api/starters`
([15-starters.md](15-starters.md)): one row per starter with the **Name** as a
link to `https://github.com/<repo>` (opens in a new tab and must not toggle the
radio), followed by the **Description**. The last row is always **"My
Application Starter"**.

The first row is preselected. When the starter list cannot be loaded the
endpoint returns a warning, which is shown under the list; "My Application
Starter" is then the only row and is preselected, so the modal stays usable.

Below the list there are two fields:

- **Application Name** — always editable. It follows the selected starter's
  name, defaulting to `myapp` for "My Application Starter", **until the user
  types into it**; from then on the user's value is never overwritten.
- **GitHub Repository** — in format `<org>/<repo>`. Filled from the selected
  starter and read-only (visibly greyed). Editable only when "My Application
  Starter" is selected.

There is no password field: the backend reuses an existing user's password or
generates one ([2-repo.md](2-repo.md)).

with a button "Create" and "Cancel"

Selecting **"My Application Starter"** opens a warning dialog:

> Warning: if you use your own starter, it must be derived by a standard
> application starter or be compatible with
> [Trustable Conventions](https://github.com/trustable-ai#how-can-i-make-a-template-compatible-with-trustable).
> The repo must exist. Connect your GitHub account below to access it; Trustable
> reads and pushes over HTTPS with the connected account. If you use a standard
> template you can save in your repo later.

Connecting an account here is what grants access to the user's own private
starter repository, so the dialog embeds the shared **GitHub account form**
([github.md](github.md)) rather than sending the user to the Configure page.
When an account is already connected the form is replaced by a single line
naming it. When it is not, connecting inside the dialog refreshes the
repository datalist in place.

Beneath the form, and only while no account is connected, a collapsed
**"Use an SSH key instead"** disclosure reveals a read-only textarea with the
SSH public key and a **Copy Key** button when `GET /api/sshkey` returns one. The
dialog is dismissed with "Understood".

On **Create** the browser checks, before calling the backend:

- the name matches `[a-zA-Z][a-zA-Z0-9]{5,19}` (6-20 alphanumeric characters
  starting with a letter);
- no application with that name already exists — compared case-insensitively
  against the loaded list and re-checked against `GET /api/repo`, since a
  workspace app may not be rendered in the current list.

A failed check shows an inline message under the name field asking for a
different name and does **not** submit.

When the selected starter carries a `templates` value it is sent to
`POST /api/repo` as the `templates` field, so the app inherits the starter's
notebook/templates repository.

The managed GitHub datalist and the SSH key notice described below apply **only
in "My Application Starter" mode**, where the user supplies the repository;
both are hidden while a starter is selected, since the repository is then fixed
and public. Note the boundary with starter discovery: the datalist uses the
authenticated managed-GitHub listing because it suggests the user's own,
possibly private, repositories, whereas `GET /api/starters` only reads a static
published index and never touches the GitHub API
([15-starters.md](15-starters.md)). The two must not share a code path.

When `GET /api/github/status` reports an authenticated managed GitHub account,
the repository input remains an editable `org/repo` field but is backed by a
datalist populated from bounded `GET /api/github/repos` results. The modal
shows the connected login, repository visibility, and default branch where
available. Public and private repositories are both selectable. Repository
listing failures do not disable manual input; they show an actionable message
and preserve the SSH fallback.

If the managed GitHub account is not connected, the Add Application modal offers
the shared **GitHub account form** ([github.md](github.md)) inside a notice
reading "Connect your GitHub account to read and save private repositories over
HTTPS." Connecting there reloads the repository datalist without leaving the
modal. The form is hidden once an account is connected, because the datalist
notice above it already names the account.

The existing SSH path remains available as a fallback, offered **only while no
account is connected**. If the SSH key is available (`GET /api/sshkey` returns
200), show a collapsed **"Use an SSH key instead"** disclosure beneath the
form. Expanding it reveals a notice reading "To save and read a private repo add
this ssh key to your GitHub account." containing:

- a read-only textarea with the `~/.ssh/id_ed25519.pub` content (fetched from `/api/sshkey`),
- a **Copy Key** button that copies the value to the clipboard.

The reveal is collapsed by default and reset to collapsed every time the Add Application modal is closed and reopened.

If the SSH key is not available, do not show this notice.

The Configure page owns a compact **GitHub Account** card. Its body is the same
shared GitHub account form the Add Application, My Application Starter and Git
Push flows mount ([github.md](github.md)); only the card's heading, description
and status pill belong to the page. It displays:

- unavailable, disconnected, connecting, connected, cancelled, expired/error;
- the authenticated `github.com` login, but never a token;
- Connect, Cancel, Retry, and Disconnect actions;
- the one-time device URL and code while login is active.

The browser polls the bounded backend login state. It never executes `gh`,
receives credential files, stores a GitHub token, or sends GitHub credentials
to Pi/TruACP. An authenticated account status takes precedence over stale
terminal device-flow state: the Configure card must not show a failed/expired
login error together with a connected account. While the managed account is
connected, the separate Git User card is hidden because it is not a second
authentication mechanism. Its saved commit-author values remain unchanged and
become visible again after disconnecting GitHub.

## SSH Key link

If the SSH key is available, show an "SSH Key" link below the applications list. Clicking it opens the same SSH key popup described above.

If you cancel, go back

If you confirm, create with the backend api /api/repo

Show a waiting modal until the api call completes. Once completed the modal should show the result and ask for the user to say ok.

# Edit application

Calculate LEFT and RIGHT url from the location.
Expect a domain in format `<protocol>://trustable.<domain>[:<port>]`,
show an error if it is not in this format. Let:
- LEFT is `<protocol>://opencode.<domain>:<port>`
- RIGHT is `<protocol>://vite.<domain>:<port>`
- URLDIR is the URL-encoded absolute path for the directory of the application
- B64DIR is the base64-url-safe encoded absolute path for compatibility

You can click the button `edit` to open an app
- show a launching dialog with the message `Launching `<name`
- invoke GET /api/launch/<name> to start it with a visual indicator you are waiting
- repeated Edit requests from the same or different browser tabs are safe: the
  server serializes the shared runtime lifecycle and reuses an already healthy
  runtime for the same application instead of returning a false 4096 conflict
- if there is an error, show the error and a button "continue"
- if it is ok, save in cookies:
  - the LEFT and RIGHT urls
  - the NAME in a cookie
  - the URLDIR in a cookie using the backend `encdir` value
  - the B64DIR in a cookie using the backend `b64dir` value
  navigate to the page app.html
  - the SESSIONID in a cookie using the backend `session_id` value

## Revert

The Revert action is part of the **Utils** pulldown in the application screen (`app.html`). See "Utils Pulldown" and "Revert" in [3-app.md](3-app.md) for the full behavior.

# Git Push

Each app card has a "Git Push" button. Clicking it calls `POST /api/publish/push` with the app name.

In both the grid and the list view the button carries
`data-tour-push="<app name>"`. No tutorial currently addresses it — the
walkthrough that did was replaced by the **Toolbar** tour (see
[16-tutorial.md](16-tutorial.md)) — but the marker is kept so a tutorial can
reach the button again without changing this page. The page still loads
`js/tutorial.js`, because the engine resumes a tutorial that crosses pages and
watches `appList` so a spotlight follows the list when it re-renders.

If the backend returns `{"needs_config": true}`, show a popup asking for:
- The production repository in org/repo format
- The shared **GitHub account form** ([github.md](github.md)), when no account is
  connected, above the repository field, introduced by "Connect your GitHub
  account to push. Trustable pushes over HTTPS with the connected account — no
  SSH key needed." Connecting inside the popup replaces the form with a line
  naming the account, so the user completes the push without reopening it.
- The SSH key fallback, offered only while no account is connected and only if
  the SSH key is available: a collapsed **"Use an SSH deploy key instead"**
  disclosure revealing the key, a **Copy Key** button, and the note "Add this
  SSH public key to the repository's deploy keys (with write access) or to your
  GitHub account."

Once the repo is set, the backend saves it as `OPS_REPO` in production config
and adds a `production` remote. With a connected managed GitHub account it uses
the isolated HTTPS credential helper. Otherwise it uses the existing SSH key.
It pushes the bare workspace repository's symbolic default branch rather than
assuming `main`.

If `needs_config` was not returned (already configured), skip the form and show the result directly.

Show spinner during the operation and result on completion.

# Git Pull

Each app card has a "Git Pull" button. Clicking it calls
`POST /api/git/pull` with the app name.

The backend pulls from the configured production repository (`OPS_REPO`) when
available, matching the Git Push target. If no production repository is
configured, it pulls from the app's original `origin` repository. The operation
updates Trustable's bare workspace repository and fast-forwards the active
workbench checkout when it exists.

The backend does not merge, rebase, hard reset, or overwrite unsaved workbench
changes. Trustable-generated launch files may be cleaned by the backend before
the pull because they are regenerated on app launch; app/user changes and
`AGENTS.md` with app-local notes remain blocking. If local changes or divergent
commits exist, show the backend error in the same modal/result style used by Git
Push.

Show spinner during the operation and result on completion. On success, reload
the app list so repository metadata remains current.

# Publish

Each app card has a "Publish" button. Clicking it calls `POST /api/publish/remote` with the app name.

If the backend returns `{"needs_config": true}`, show a popup asking for:
- OPS_APIHOST (placeholder: "https://your-openserverless-host.com")
- OPS_USER
- OPS_PASSWORD
- A note: "You need an OpenServerless environment for publishing. Contact info@nuvolaris.io or check https://openserverless.apache.org"

Once configured, the backend ensures the workbench exists (clones from workspace if needed), generates env files including `.env.production`, runs `ops ide login --mode=production`, then `ops ide deploy`.

If `needs_config` was not returned (already configured), skip the form and show the result directly.

Show spinner during the operation and result on completion.

# Undeploy

Each app card and list row has an "Undeploy" button placed between "Publish" and
"Delete". It removes the application's deployed actions and packages from
OpenServerless by running `ops ide undeploy`; it does not touch the workspace repo,
the workbench checkout, or the app entry in the configuration.

Clicking it opens a confirmation modal ("Undeploy \"<name>\"? This removes its
deployed actions and packages.") with Cancel and a pale-danger Undeploy button.
Confirming calls `POST /api/undeploy` with `{"name": "<name>"}`, shows a spinner, and
renders the result — `message` plus command `output` on success, `error` plus `output`
on failure — in the shared `nu-feedback` result block.

Undeploy requires an existing workbench, because `ops ide undeploy` runs inside
`$WORKBENCH_DIR/<name>` with the credentials written by `ops ide login`. When the app
has never been launched the backend returns HTTP 400 with
`{"error": "workbench not found - launch the app first"}` and the modal shows that
message. The frontend does not provision a workbench.

Undeploy is not part of the publishing gate: `/api/undeploy` does not call
`requireValidLicense`.

The app list is not reloaded after an undeploy — the set of applications is unchanged.

# Env (Configure Application Environment)

Each app card has an "Env" button that opens the environment configurator (`appconfig.html`).

The configurator allows to edit `.env` and `.env.production`

It is a table with 3 columts: "VARIABLE", "Development", "Production"

Each row shows a text filed to edit the variable and the value for development and production

Read the .env and the .env.production allowing to add and remove variables.
If a file is missing or a value is missing default to empty string.

The first 4 rows are:

- OPS_APIHOST
- OPS_USER
- OPS_PASSWORD
- OPS_REPO

those 4 cannot be changed or removed
the development value cannot be changed
the pruduction value can be changed

OPS_REPO is initialized to the git remote origin (org/repo) of the application.

Then there are the env vars listed in `trustable.json` in section `env`
there cannot be added or removed but both the development and pruduction value can be changed

then you can add and remove other variables set both development and production values.

There are the buttons:

- Import `.env`
- Import `.env.production`
- Save
- Close

The Import `.env` button lets the user select a local `.env` file. The browser
parses `KEY=VALUE` entries, ignores blank lines and comments, updates matching
editable Development values, and adds missing keys as custom variables with
empty Production values. Read-only Development fields remain unchanged.

The Import `.env.production` button lets the user select a local `.env` or
`.env.production` file and imports those values into the Production column on
Trustable. It updates matching Production values and adds missing keys as custom
variables with empty Development values. Imported values are not saved until the
user clicks Save/Commit.

The Save button will save both `.env` and `.env.production`
The Close will come back, warning if there are unsaved changes.

# Credits

A compact **Credits box** is shown on this page right after the title (below the version, above the Applications card), but **only when the merged configuration's `provider` field equals `"trustable"`**. For Ollama (or any other provider) the box is not rendered.

The box is a rounded pill (light background, border) showing the label `Credits:` followed by the current credit value (e.g. `Credits: 873`). While the value has not yet been fetched, show `Credits: …`. On error, show `Credits: —` and put the error text in the element's `title` attribute (tooltip).

The frontend fetches credits from the local backend endpoint `GET /api/credits` (which proxies the ai-proxy's `GET /api/v2/credits` — see [3-app.md](3-app.md) "Credits" for the endpoint contract). It calls the endpoint when the page loads and then re-fetches **every 60 seconds** with `setInterval`. Clear the interval on page unload.

## Optional deploy after Git Pull

After Git Pull succeeds with `workbench_updated: true`, the result modal
asks: "Git pull completed. Do you want to deploy too?"

- `Deploy` calls `POST /api/git/deploy`, shows the deployment spinner, and
  reports the command result.
- `Not now`, Escape, or a backdrop click closes the prompt without running
  `ops ide clean` or `ops ide deploy`.
- No deployment prompt appears when the workbench did not change.
- The application list reloads after the successful pull independently of
  the deployment choice.

## Launch Progress

The Edit launch modal requests `GET /api/launch/<name>` with
`Accept: text/event-stream` and displays an accessible progress bar while the
application lifecycle runs. Each progress event provides a monotonic
`stage`, the fixed `total`, and a user-facing `message`. The UI derives its
percentage only from those real lifecycle stages, never from elapsed-time
animation, and keeps the last reached stage visible when launch fails. A
terminal `done` event reaches 100% before navigation; a terminal `error`
event preserves the existing error and setup-required behavior.

If streaming is unavailable or the response is not `text/event-stream`, the
browser falls back to the existing JSON response contract. This preserves
compatibility with older servers and non-streaming clients. See issue #82.

## Tooltips

Every button on the page has a `title` that **explains what it does**, rather
than repeating its own label — the filters say what they filter on, and the
destructive actions (Delete) say that they are permanent, so hovering warns
before the click rather than the modal warning after it.

The per-card actions (Env, Git Pull, Git Push, Publish, Undeploy) are rendered
by the shared `actionButton()` helper and take their tooltip from one
`ACTION_TOOLTIPS` table, so the grid and list views — which both render through
it — cannot drift apart. `Edit` and `Delete` are written inline in each of the
two templates and carry the same text in both.

Unlike the editor toolbar ([3-app.md](3-app.md)), this page's button rows use
`flex-wrap`: when the window narrows they reflow onto another line rather than
being cut off, so they are **not** collapsed to icons.
