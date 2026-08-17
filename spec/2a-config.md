This file describes the configuration system and the configure.go module.

# Layered Configuration

Configuration uses a two-layer system based on `trustable.json`:

1. **Base config** (`trustable.json` in the app root directory) — immutable defaults shipped with the application
2. **Workspace config** (`<WorkspaceDir>/trustable.json`) — user overrides, created on first save or migration

Loading merges both layers: workspace fields override base fields. Maps (models, env) are merged key-by-key. The workspace file only needs to contain overrides and the `apps` section.

## trustable.json structure

```json
{
    "provider": "ollama" | "trustable" | "private",
    "base_url": "<provider base URL>",
    "api_key": "<provider API key>",
    "models": {
        "<model-name>": {
            "maxToken": 262144,
            "maxOutput": 8192
        }
    },
    "pi": {
        "default": "<chosen Pi model name>"
    },
    "notebook": {
        "repository": "trustable-ai/templates",
        "ref": "main"
    },
    "predefined_env": {
        "SHARED_API_KEY": "<value offered when an app asks for this name>"
    },
    "apps": {
        "<app-name>": {
            "password": "<ops user password>",
            "development": {
                "OPS_USER": "<app-name>",
                "OPS_APIHOST": "<cluster-apihost>",
                "CUSTOM_VAR": "dev-value"
            },
            "production": {
                "OPS_APIHOST": "https://prod.example.com"
            }
        }
    },
    "current": "<currently launched app name or empty>"
}
```

- `provider` — `ollama`, `trustable`, or `private`. Set on first run by the provider-choice screen on the splash page. Workspace-only field (not in base config). Whether the user can publish is determined at request time by the installed license (see [6-publish.md](6-publish.md) and [14-license.md](14-license.md)) — there is no separate `publishing` flag, and `api_key` has no bearing on publishing.
- `base_url` and `api_key` — top-level provider credentials. Set when a provider is chosen:
  - **Ollama** — `api_key = "dummy"`. `base_url` depends on the Ollama mode picked on the splash sub-modal (see "Ollama mode selection"). For **internal** Ollama it is fixed to `http://localhost:11434/v1`; for **own host** the user enters host and port and `base_url` becomes `http://<host>:<port>/v1`. Scheme is always `http://` and path is always `/v1` — no HTTPS, no auth, no other paths.
  - **Trustable** — taken from the registration message posted by the ai-proxy iframe (`{ base_url, api_key }`), see [1-index.md](1-index.md).
  - **Private AI** — both are supplied by the user in the Private AI dialog on the splash (see [1-index.md](1-index.md)). `base_url` is any OpenAI-compatible endpoint matching `^https?://[^\s]+/v1/?$` — the `/v1` suffix is mandatory so `/models` and `/chat/completions` resolve. `api_key` is optional and is persisted as `"dummy"` when left empty, because Pi requires a non-empty value to consider the provider configured (see [pi.md](pi.md)).
  There is **no** global `env` section in `trustable.json`, and no global block is ever merged into an application. Environment variables live only inside each app under `apps.<name>.development` / `apps.<name>.production`. The separate `predefined_env` map is **not** an exception: it is a palette of values the user is *offered*, never applied — see "Predefined environment variables" below.
- `models` — the model list for the **currently selected provider**, copied from the cached model catalog (see "Model catalog" below). The previous `ollama` key is removed; the same shape is now provider-agnostic and is rewritten when the user switches provider.

  Each entry is an object of optional limits. Two of them are **user-editable per model** in Configure, for every provider:

  - `maxToken` — the context window. Falls back to `maxInput`, then to **128000**.
  - `maxOutput` — the output budget. Falls back to **32768**.

  Both are written to Pi's `models.json` as `contextWindow` / `maxTokens` (see [pi.md](pi.md)). An absent key means "use the default"; clearing a field in the UI deletes the key rather than storing `0`, since the Go side treats absent and zero identically and a stored `0` would only mislead whoever reads this file.

  A legacy form exists where the whole entry is a string context size — `"<model-name>": "256K"` — which is still parsed into `maxToken` on read. It is never written.
- `pi.default` — must be a name that exists as a key in `models`. The
  configurator UI presents it as a single dropdown populated from `models`.
  Legacy `opencode.default` and `opencode.small` are ignored rather than
  migrated; a missing `pi.default` sends the user to `configure.html?setup=1`
  from both the splash and the application list, before any model probe runs.
- `apps.<name>.templates` — the notebook/templates repository inherited from the
  application starter this app was created from (see [15-starters.md](15-starters.md)).
  It overrides `notebook.repository` for that app only; when absent or empty the
  app uses the global value. Workspace-layer only, written by `POST /api/repo`
  and omitted from the JSON when empty.
- `notebook.repository` and `notebook.ref` — the global GitHub source consumed
  by notebook workflows in every launched TruACP session, unless the launched app
  overrides the repository via `apps.<name>.templates`. The defaults are
  `trustable-ai/templates` and `main`. The write token is deliberately absent
  from `trustable.json`: Configure stores it as a mode-`0600` workspace secret
  under `<WorkspaceDir>/.trustable/secrets/` and the API exposes only
  `notebook.has_token`.
- `AIP_REGISTER_URL` (environment variable, **mandatory** at startup; preflight fails if unset) — base URL of the ai-proxy registration UI. The splash page loads it in an iframe when the user picks Trustable Cloud; the top-up form lives at `<AIP_REGISTER_URL>/top-up`. The registration URL is configured **only** via this env var; there is no JSON field. `loadTrustableConfig` exposes it on the returned config as `register_url` (read-only, not persisted) so the frontend can read it via `GET /api/configuration`.
- `AIP_BASE_URL` (environment variable, **mandatory** at startup; preflight fails if unset) — base URL of the ai-proxy JSON API. The backend uses it directly for `/api/credits`, `/api/topup`, and `/api/status` — no `/v1`/`/v2` rewriting happens. Server-side only; not exposed on the config returned to the frontend.

The base config has no `apps` or `provider` section. Those live only in the workspace config.

There is no top-level `testmodel` field. The model used for the say-OK
connection test is always `pi.default`.

All fields in the workspace config use `omitempty` — absent fields inherit from the base.

## Model catalog

The trustable-app fetches the per-provider model catalog via `GET /api/status`, which proxies the ai-proxy's `/api/v2/status` response (see [status_check.md](status_check.md)). Shape (one block per provider):

```json
{
    "trustable": { "modelsVersion": <int>, "default": "<id>", "small": "<id>", "models": { ... } },
    "ollama":    { "modelsVersion": <int>, "default": "<id>", "small": "<id>", "models": { ... } }
}
```

Each `models` entry may use the legacy limits-only shape or the richer policy
shape:

```json
{
  "maxToken": 240000,
  "maxInput": 120000,
  "maxOutput": 120000,
  "reasoning": true,
  "thinkingLevelMap": {
    "high": "high",
    "xhigh": "xhigh"
  },
  "enabled": true,
  "recommended": true,
  "roles": ["coding", "agent"],
  "reason": ""
}
```

`enabled`, `recommended`, `roles`, and `reason` are optional and
backward-compatible. They describe whether a model is suitable for Pi
agent work, not whether the upstream provider exposes it. If `enabled` is
`false`, the model must remain visible in the provider model table for
diagnostics but must not be selectable as `pi.default`. Roles such as
`embedding`, `embed`, `rerank`, or `vector` also make a model non-selectable
for Pi. Roles such as `coding`,
`agent`, `chat`, or the historical catalog capability `opencode` explicitly
mark a model as selectable.

`reasoning` and `thinkingLevelMap` are optional Pi capability metadata.
`reasoning: true` enables Pi's standard effort levels through `high`.
Extended `xhigh` is available only when the model explicitly publishes a
non-null `thinkingLevelMap.xhigh`; a missing or null entry must never be
presented as Extra high. Trustable Cloud is the managed compatibility
exception: its OpenAI-compatible proxy supports the standard `high`
reasoning-effort contract for coding models, so the Pi writer treats a missing
`reasoning` value as `true` for provider `trustable`. Ollama and
user-provided endpoints (including Private AI) remain capability-driven and are not assumed to support
reasoning. An explicit `reasoning: false` always wins, including for Trustable
Cloud.

When a provider does not return policy metadata (for example Private AI or
own-host Ollama discovery), Trustable applies a conservative local policy:
embedding/rerank/vector models, obvious tiny/small non-agent models, vision,
audio, TTS/Whisper-style models, and models below roughly 20B parameters are
hidden from the Pi dropdown. They may still appear in the provider model
table because availability and suitability are different facts.

There is no longer a top-level `version` field driving reselect — the previous `<WorkspaceDir>/models.json` cache and the catalog-level version check have been retired in favor of the per-provider `modelsVersion` mechanism below.

### Version tracking (per provider)

The workspace `trustable.json` carries a `model_versions` map: `{ "ollama"?: int, "trustable"?: int }`. Each entry caches the `modelsVersion` that was current when the user last saved (or first seeded) that provider's models.

On every page load that depends on a chosen provider (splash, applist), the frontend fetches `/api/status` and compares `status[provider].modelsVersion` against `config.model_versions[provider]`:

- **First run / no recorded version** — write the value into `model_versions` on first save and proceed.
- **Same version** — proceed normally.
- **Version changed** — persist the new value to `model_versions[provider]` and redirect to `configure.html?reselect=1` so the user re-picks `pi.default` from the refreshed catalog. The redirect happens *before* the splash configuration flow runs.

The provider catalog's `default` value is not a freshness signal. It seeds
`pi.default` for a new provider and is used as a fallback only when the saved
model no longer exists after a real `modelsVersion` change. If the saved model
still exists, preserve it across the refresh. A user-selected `pi.default` that
differs from `status[provider].default` must not trigger a redirect.

### Exception — own-host Ollama

When `provider == "ollama"` **and** `base_url` resolves to a non-localhost host (see "Detecting own-host Ollama" below), the reselect redirect is suppressed. Own-host Ollama gets its models by calling `POST /api/discover-models` against the user's machine; the proxy catalog never appears in their UI, so catalog drift cannot invalidate their choices. `model_versions.ollama` is still recorded but never used to trigger a reselect for this mode.

Internal Ollama (localhost `base_url`) and Trustable are catalog-backed and **do** trigger the redirect when `modelsVersion` bumps. This is a deliberate change from the previous "all-Ollama is exempt" rule.

The `Refresh` button in the configure UI is also hidden whenever the active config is own-host Ollama (it re-fetches the catalog and would otherwise overwrite the user's discovered model list).

### Detecting own-host Ollama

The runtime distinguishes the two Ollama modes from `cfg.base_url` alone (no separate flag is stored on disk):

- **Internal** — `base_url` is empty, or its host is `localhost`, `127.0.0.1`, or `ollama` (the embedded-server hostname).
- **Own host** — `base_url` parses as a URL with any other host.

This single rule is reused by:

- the modelsVersion reselect-suppression check above,
- Step 2 of `GET /api/configure` (skip model-pull on non-localhost),
- `POST /api/discover-models`, which explicitly rejects `localhost` / `127.0.0.1` for Ollama mode (Trustable runs inside a VM and a loopback there is not the user's loopback).

### Per-provider seeding

When the user picks a provider on the choice screen (or when the configure UI's "Change Provider" button completes a switch), the workspace `trustable.json` is rewritten with:

- `provider` — the chosen value
- `models` — `status.<provider>.models` verbatim
- `pi.default` — `status.<provider>.default` (the user can override it in Configure)
- `model_versions[provider]` — `status.<provider>.modelsVersion`

Switching provider replaces the model list **and** the Pi default from the
catalog.

Trustable Cloud and internal Ollama are catalog-backed. Their status section
must contain at least one model, a non-empty default, and that default must
exist in the model map before the provider choice can be persisted. The backend
also rejects an empty Trustable Cloud catalog, so a stale frontend or partial
status response cannot save a configuration that fails later in
`/api/testmodel`.

**Own-host Ollama is the seeding exception:** an empty `models` map and empty
`pi.default` are persisted; the user populates both via the Test button on
`configure.html?ollama=own`. `model_versions.ollama` is still seeded from the
current `status.ollama.modelsVersion`.

## Ollama mode selection

Picking **Cloud AI** on the splash provider-choice modal always selects the
internal Ollama server: `base_url = "http://localhost:11434/v1"`, `models` and
`pi.default` seeded from `status.ollama` (see "Per-provider seeding"), and `GET
/api/configure` proceeds to check connectivity and pull each model. There is no
mode sub-modal.

Pointing Trustable at a user-supplied Ollama host is superseded by the **Private
AI** card, which accepts any OpenAI-compatible endpoint (Ollama's `/v1` included)
and discovers its models the same way. The splash therefore never persists a new
own-host Ollama configuration.

The own-host machinery below is retained for **workspaces already configured
that way** before this change: `configure.html?ollama=own` still renders the
host editor, `resolveOllamaRoot` still resolves a non-localhost host, and the
reselect exemption in "Exception — own-host Ollama" still applies. Those paths
are no longer reachable from a first-run provider choice.

On `configure.html?ollama=own` the Ollama Host section is shown with empty inputs, three help bullets ("Provide the IP of your local machine or intranet server (NOT 127.0.0.1)", "Enable network access on that machine", "It must be accessible via HTTP without authentication") and a **Test** button. Clicking **Test** builds `base_url = "http://<host>:<port>/v1"` from the inputs and calls `POST /api/discover-models` with that `base_url` and `api_key = "dummy"`. On success the frontend writes the same `base_url` into `config.base_url`, replaces `config.models` with one entry per discovered model using default limits `{maxToken: 128000, maxOutput: 32768}`, and resets `config.pi.default` so the user makes an explicit selection.

After **Save & Configure**, `GET /api/configure` reaches the user's host (via `cfg.base_url` stripped of `/v1`) for the connectivity check and for capability discovery via `/api/show`. The model-pull loop (Step 2) is **skipped** when the resolved host is not localhost — the user's host already has the models installed locally; pulling them again would be wasteful. The stream emits `OK: Skipping model pull (using your own Ollama host — models are already installed there)` instead.

## Private AI endpoint

The user supplies an OpenAI-compatible endpoint in the Private AI dialog on the
splash (see [1-index.md](1-index.md)). There is no reachability pre-check and no
registration iframe: the dialog itself calls `POST /api/discover-models`, so an
endpoint that answers with no models is never persisted.

`base_url` must match `^https?://[^\s]+/v1/?$` — the `/v1` suffix is mandatory so
`/models` and `/chat/completions` resolve. `api_key` is optional and is stored as
`"dummy"` when left empty. An `https://` endpoint with no key asks for
confirmation; an `http://` one does not.

Discovery runs in the dialog, but `pi.default` is **not** seeded from it: a
user-supplied endpoint publishes no metadata saying which model is suitable for
coding, so guessing the first one would silently select an embedding or tiny
model. The splash persists `pi.default: ""`, alerts that a default model must be
chosen, and routes to `configure.html?setup=1` — like own-host Ollama, setup
completes on the configure screen. The splash Configuration flow does not run on
that turn (there is nothing to probe without a default model).

On the configure screen the **Private AI Endpoint** section shows the stored
`base_url` as a **read-only** note (the value is user-supplied, unlike the fixed
internal-Ollama host, but is changed by re-picking the provider rather than
edited in place — keeping this issue's diff contained). `loadPrivateModels`
calls `POST /api/discover-models` with the stored `base_url` and `api_key`,
populating `config.models` with one entry per discovered model using default
limits `{maxToken: 128000, maxOutput: 32768}` while preserving any
previously-saved limits and Pi selection. The model table is **editable**
(Add/Remove, like own-host Ollama); the `/api/status` **Refresh** button is
hidden (there is no `status.private` catalog). `model_versions` and the
`?reselect=1` redirect do not apply (discover-models-backed, like own-host
Ollama). **Save & Configure** runs the say-OK test against the stored
endpoint's `/chat/completions`.

## POST /api/discover-models

Server-side proxy that lets the configure UI discover the models a given OpenAI-compatible provider exposes, without browser CORS or mixed-content issues. The endpoint is **provider-agnostic** — it hits `<base_url>/models` on whatever the caller passes. (The Test button in the UI is currently shown only for own-host Ollama; the endpoint itself does not enforce that.)

Request body (JSON):

```json
{ "base_url": "http://<host>:<port>/v1", "api_key": "..." }
```

- `base_url` — required. Must be a parseable URL. When the in-progress config indicates Ollama mode (`provider == "ollama"` on the saved config), the host must NOT be `127.0.0.1` / `localhost` — rejected with HTTP 400 because Trustable runs inside a VM and a loopback there is not the user's loopback. For non-Ollama providers any host is accepted.
- `api_key` — optional. If non-empty and not the literal string `"dummy"`, the backend sends `Authorization: Bearer <api_key>` on the upstream request. Otherwise no auth header is sent (matches the current Ollama-on-LAN behavior).

Backend behavior:

1. Issue `GET <base_url>/models` with a short timeout (5 seconds).
2. Parse the OpenAI-compatible response `{ "object": "list", "data": [{ "id": "<model>", ... }, ...] }`.
3. Return `{ "models": ["<id1>", "<id2>", ...] }` sorted alphabetically.

On any failure return `{ "error": "<message>" }` with an HTTP status reflecting the cause (400 for bad input, 502/504 for upstream failures, 500 for parse errors).

The endpoint is purely a read — it does not write to `trustable.json`. The frontend takes the returned model list, builds the new `config.models` map with default limits, resets `config.pi.default`, and saves via `POST /api/configuration` as usual.

This endpoint replaces the previous `GET /api/ollama-tags?host=&port=` (which was Ollama-specific in name only — it already hit the OpenAI-compatible `/v1/models` endpoint). The old route is removed; callers must use `POST /api/discover-models`.

## Config loading functions

- `loadBaseConfig()` — reads app-root `trustable.json`
- `loadWorkspaceConfig()` — reads `<WorkspaceDir>/trustable.json` (returns empty if missing)
- `mergeConfigs(base, override)` — merges workspace overrides onto base
- `loadTrustableConfig()` — returns the merged result (base + workspace)
- `saveWorkspaceConfig(cfg)` — writes only the workspace file

## Per-app .env generation

`.env` and `.env.production` files in the workbench are **generated artifacts**, not sources of truth. They are regenerated from the merged config:

- When an app is launched (`GET /api/launch/<name>`)
- When app config is saved (`POST /api/appconfig/<name>`)
- When global config is saved (`POST /api/configuration`)

Generation is a **no-op when the app has no workbench checkout**. An app can be
configured before it has ever been launched — a fresh clone lives in the
workspace with no checkout, and the missing-variable flow sends the user to the
env editor in exactly that state. There is nowhere to write yet and nothing is
lost: launch regenerates `.env`, `.env.production`, and `.env.dist` immediately
after cloning workspace → workbench. Saving config for a not-yet-launched app
must therefore succeed silently, not log a write failure.

The Trustable configuration UI and these server-side generators are the only
owners of application env files. Coding agents and MCP servers must treat
`.env` and `.env.production` as immutable: they may not read, create, edit,
import, synchronize, or regenerate them. A missing application value is
reported to the user, who may add it through the Trustable app config UI.

New variables must not be added to generated app `.env` / `.env.production`,
the per-app config `development` / `production` maps, or env-generation code
unless the user explicitly authorizes that exact variable in the current
conversation. Do not infer new env keys from MCP availability, service
capabilities, model behavior, or convenience for generated code.

The function `generateAppEnvFiles(appName)` builds the workbench `.env` from:
1. Fixed vars: `OPS_USER=<appName>`, `OPS_PASSWORD=<from apps.password>`,

2. The OPS_APIHOST is set in this order
- by env variables OPS_APIHOST/APIHOST/TRUSTABLE_DEFAULT_APIHOST/OPERATOR_CONFIG_APIHOST
- on Mac by the content of file ~/Library/Application Support/Trustable/apihost if the file is present
- on Windows by the content of %APPDATA%/Trustable/apihost  if it is present
- on Linux by the content of ${XDG_CONFIG_HOME:-$HOME/.config}/trustable/apihost if it is present.
  There is no Trustable macOS app on Linux, so this is where the native path of
  `start.sh` writes it (see [start.md](start.md)); this case must not be left
  unresolved, or the value written there is silently ignored in favour of the
  default below.
- defaults to http://miniops.me

`OPS_APIHOST` configures Trustable and `ops ide` orchestration. It is not an
application secret and must never be propagated into an action wrapper as
`#--param OPS_APIHOST "$OPS_APIHOST"` or exposed as `ctx.OPS_APIHOST`.
Browser application code uses relative `/api/my/...` URLs, while action modules
use generated service bindings directly.

3. Per-app `development` overrides

4. Service runtime bindings from official OpenServerless config are not written
   to `.env`. When `~/.ops/config.json` exposes an official MongoDB capability,
   Trustable may pass the resolved URI as `MONGODB_URI` only in the process
   environment of TruACP/Pi, `ops ide deploy`, and `ops ide devel`, so
   `action_add_mongodb` can bind it without exposing the credential in the
   editable app configuration. A casual per-app/workbench `MONGODB_URI` must not
   enable MongoDB by itself; the source of truth remains the official post-login
   config.

Production `.env.production` is written from the per-app `production` values.

There is no global `env` section: environment variables for an app come only from the fixed vars above and the per-app `development` / `production` maps. `predefined_env` is **not** an input to `generateAppEnvFiles` — a predefined value reaches a generated `.env` only after the user has copied it into the app's `development` map through the editor, at which point it is an ordinary per-app value like any other.

`regenerateAllAppEnvFiles()` iterates all apps and regenerates for each that has a workbench directory.

## .env.dist — the committed key manifest

`generateAppEnvFiles` also writes `$WORKBENCH_DIR/<name>/.env.dist`, the
**committed** counterpart of `.env`: it declares which variables the app needs
**from the user**, so a repo cloned anywhere else can be told what to supply.

- Content is the union of the development and production variable names, one
  `NAME=` per line, sorted alphabetically for a stable diff regardless of Go map
  iteration order.
- **Values are always empty.** `.env.dist` is committed and pushed, so no value
  is ever copied into it — not even one that looks non-secret.
- The fixed `OPS_*` keys (`OPS_USER`, `OPS_PASSWORD`, `OPS_APIHOST`, `OPS_REPO`,
  `OPS_SKILLS`) are **excluded**. The server supplies them on every launch from
  the workspace config, never from user input, so listing them would state a
  requirement the user is neither able nor ever asked to satisfy. They are not
  generated into the manifest and are not required when a foreign manifest
  happens to list them.
- Keys excluded from `.env` are excluded here too: `isServiceRuntimeEnvKey`
  (currently `MONGODB_URI`).
- When nothing is left to declare, **no file is written**, and a stale manifest
  from an earlier config is removed. An app that requires nothing of the user
  declares no contract; a zero-byte committed file would assert one.
- `writeEnvDistFile` reports whether the content changed. An unchanged manifest
  is not rewritten, so a launch that changes nothing leaves the working tree
  clean. Removing a stale manifest counts as a change, so the deletion is
  committed like any other.
- Like `.env`, it is written **only** by the server-side generator. Coding
  agents and MCP servers must treat it as immutable.

### Auto-commit

When the manifest changes, the server stages and commits it itself
(`commitEnvDist`), rather than leaving it dirty until the user next saves code.
The manifest is the contract a clone reads, so it must track the app's variable
set at all times. This mirrors the managed `.gitignore` commit described in
[13-gitignore.md](13-gitignore.md) and carries the same constraints:

- Only when the content actually changed — otherwise every launch would attempt
  an empty commit.
- Scoped pathspec (`git add -- .env.dist`, `git commit -- .env.dist`): the
  commit contains that one file and never sweeps up the user's dirty work.
- Best-effort and non-fatal. A missing git identity, a rejecting hook, or a
  workbench that is not yet a git repository is logged and ignored;
  `generateAppEnvFiles` still succeeds and the file is still written.
- It never pushes. The commit reaches the workspace bare repo through the
  normal save/push path.

`.gitignore` already ignores `.env` and `.env.production` and carries an
explicit `!.env.dist` negation after them (see [13-gitignore.md](13-gitignore.md)),
which is what keeps the manifest tracked while the generated secrets stay out.

## Missing variables

A variable is **missing** when `.env.dist` declares it but the merged config has
no development value for it. `missingAppEnvKeys(appName)`:

1. Reads `.env.dist` from the workbench, falling back to
   `git show HEAD:.env.dist` in the workspace bare repo so an app that has never
   been launched can still be inspected. No manifest → no missing keys: an app
   that declares no contract cannot violate one.
2. Skips the fixed `OPS_*` keys and `isServiceRuntimeEnvKey` keys. The generator
   no longer writes them, but a repo cloned from elsewhere may carry a
   hand-written manifest that lists them; they must still never be treated as
   required, or the gate would block every launch on values the user cannot
   provide. The same filter keeps them from being seeded into the editable
   config, where an empty entry would shadow the generated value.
3. Treats a key as missing when `apps.<name>.development[key]` is absent or
   empty after trimming.
4. Returns the keys in `.env.dist` order.

Only development values are checked. Production values are a publish-time
concern and are not gated at launch.

`seedMissingEnvKeys(appName)` inserts every such key into
`apps.<name>.development` with an **empty string value** and saves the workspace
config. The env editor renders one row per config entry, so seeding is what makes
a declared-but-unset variable appear as an editable blank row instead of being
invisible. It runs after a clone (`POST /api/repo`, see [2-repo.md](2-repo.md))
and after the workspace→workbench clone (see [4-launch.md](4-launch.md)), and is
idempotent.

### Empty values are preserved

`POST /api/appconfig/<name>` keeps a variable whose name is non-empty even when
its development and production values are both empty. Dropping empty values —
as it previously did — would silently delete a seeded row on the next save, so
an unfilled required variable would vanish from the editor and stop being
reported as missing. Rows with an empty **name** are still discarded, and the
fixed `OPS_*` keys are never stored as empty development entries because the
server regenerates them.

## Predefined environment variables

`predefined_env` is a workspace-level map of name→value pairs the user maintains
on the Configure page. It exists so that values shared across applications — an
API key, a shared endpoint — do not have to be retyped for every imported or
newly created app.

It is a **palette, not a source**. Nothing merges it into an application:

- `missingAppEnvKeys` does not consult it. A predefined value never satisfies a
  `.env.dist` key, so the variable still appears in the missing-variables
  editor. Were it otherwise, a value would reach an app without the user ever
  seeing it.
- `seedMissingEnvKeys` still seeds an **empty** string, not the predefined value.
- `generateAppEnvFiles` does not read it.

The only path from the palette into an application is the **Use predefined
values** button in the missing-variables editor (see
[4-launch.md](4-launch.md)), followed by the user pressing Save. This keeps
the rule stated under "Per-app .env generation": variables are not added to an
app's config without the user asking for them.

Layering follows `models` / `model_versions` — key-by-key, workspace over base —
rather than the whole-map replacement used for `apps`.

### GET /api/predefined-env

Returns `{"vars": [{"name": "...", "value": "..."}]}` from the **merged** config,
sorted by name so the table renders in a stable order.

### POST /api/predefined-env

Accepts the same shape and replaces the whole set, writing only `predefined_env`
on the workspace config.

- Names must match `^[A-Za-z_][A-Za-z0-9_]*$` — what a shell and a `.env` file
  accept. A bad name is **400** and the response names the offending key.
- Duplicate names are **400**.
- Rows with a blank name are dropped: that is how the table represents a row the
  user has not filled in yet, and it must not fail the save they just asked for.
- An empty **value** is kept. It records the name as predefined without yet
  having anything to offer, and will not satisfy a required variable.
- More than 256 entries is **400**, so the config file cannot become a data
  store.

This is a separate endpoint rather than part of `POST /api/configuration`
because that handler runs a model connectivity probe on every call and the
Configure page navigates to the app list when it succeeds. Editing an
environment variable must do neither.

### Preservation

`saveWorkspaceConfig` writes the whole struct, so any field a client omits is
erased. `predefined_env` is therefore preserved on **both** sides, and both are
required:

- `handlePostConfiguration` re-attaches it from the existing workspace config
  when the payload omits it, alongside `apps`, `current` and `notebook`;
- the Configure page's `buildConfig()` echoes it back, as it already does for
  `apps`, because that POST is a full-document write.

Without either half, saving a provider from the Configure page silently wipes
the user's predefined variables.

### Importing from a file

The Configure page's **Import from .env** button fills the table from a local
`.env`-style file instead of row-by-row typing. The file is read and parsed **in
the browser**; it is never uploaded, and no endpoint exists for it.

The parse rules are deliberately identical to `parseEnvFile` in
[configure.go](../configure.go), so the two import paths cannot drift: trim the
line, skip empty lines and lines starting with `#`, split on the **first** `=`,
trim both sides. There is no `export ` stripping and no quote unwrapping.

- **Merge rule: uploaded wins.** A parsed name already in the table replaces
  that row's value; a new name is appended. This is the opposite of the
  `.env.default` rule below, and correct here: the user just picked this file.
- The same limits the server enforces are checked client-side before anything is
  merged — names must match `^[A-Za-z_][A-Za-z0-9_]*$`, and the resulting table
  must not exceed 256 rows. A violation names the offending key and merges
  **nothing**: a partial import would leave the table in a state the user did
  not choose.
- Nothing is persisted. The status line says so, and the user reviews the table
  and presses the existing **Save Variables**, which POSTs to
  `/api/predefined-env` as usual.

### Automatic import of `.env.default`

At preflight, an **optional** `.env.default` in the server's working directory —
next to the mandatory `.env`, read with the same `parseEnvFile` — is folded into
`predefined_env`. The two files are unrelated: `.env` is server configuration,
`.env.default` only pre-fills this palette.

- **Conflict rule: keep existing.** Only names absent from `predefined_env` are
  added; a name the user already has is never overwritten. This is what makes
  the import idempotent and safe to run on every start. A name present with an
  **empty** value counts as present and is kept — that is the deliberate
  "recorded, no value yet" state described above.
- A name failing the name pattern is skipped and logged; the valid names in the
  same file still import.
- The 256-entry cap applies, and the remainder is dropped with a log line rather
  than writing a map that `POST /api/predefined-env` would then reject.
- If nothing was added, the config is **not written**, so the file does not
  churn on every restart.
- Failure is non-fatal: a malformed optional file logs a warning and the server
  starts. Values are never logged, only names — they may be credentials.

### Seeding the AI variables from Pi

When there is **no** `.env.default`, the three names below are seeded from the
provider settings Trustable has already resolved for Pi, so an application that
wants to talk to a model finds working values in the palette:

| Name | Source |
|---|---|
| `AI_BASE_URL` | `piBaseURL(cfg)` — already normalized, so Ollama carries its `/v1` suffix. Not raw `base_url`. |
| `AI_API_KEY` | `cfg.APIKey`, the real secret written to Pi's `auth.json` — **not** the `$OPENAI_API_KEY` reference stored in `models.json`. |
| `AI_CHAT_MODEL` | `piDefaultModel(cfg)`, i.e. `pi.default`. |

Values are read from the **merged** config (`base_url` may come from the base
layer); the write targets the **workspace** layer, as everywhere else here.

Preconditions, both required:

1. **No `.env.default` exists.** A present seed file is authoritative for the
   palette even when it never mentions these three names, and even when it is
   empty. The user placed a file; the server does not second-guess it.
2. **The name has no value** — absent, or present with an empty value. Each name
   is judged on its own, so a user who set only `AI_BASE_URL` keeps it and gets
   the other two filled.

Filling a name that is present with an **empty** value is the one deliberate
departure from the keep-existing rule above. There, an empty value means
"recorded, no value yet" and is preserved; here it is exactly the hole being
filled. A **non-empty** value is never touched.

A source value that is itself empty seeds nothing for that name — no provider
chosen yet, no `pi.default` — because writing empty over empty would only churn
the file. Seeding therefore does nothing on a first boot before the splash flow
picks a provider, and does its work on the next start.

Seeding runs at preflight **only**. Changing provider in Configure later does
not rewrite a palette entry that by then has a value.

`AI_API_KEY` is a live credential, and this is the first thing that copies it
into `predefined_env`. It is stored in the workspace `trustable.json` and echoed
verbatim by `GET /api/predefined-env`, so it is visible in the Configure page's
table. That is accepted — a placeholder would not work — and recorded here so it
is a decision rather than a surprise. It is not a new exposure boundary: the key
already reaches the same file as `api_key`. As with the file import, only names
are logged.

### Neither path applies anything

All three routes above populate the palette and go no further. The palette
remains **not a source**: `missingAppEnvKeys`, `seedMissingEnvKeys` and
`generateAppEnvFiles` are unchanged, and the only way a value reaches an
application is still the **Use predefined values** button followed by an
explicit save.

## Current app tracking

The `current` field in the workspace `trustable.json` stores the name of the currently launched app. It is set when an app is launched (`writeCurrentApp`) and cleared when the launch is stopped (`removeCurrentFile`). The `workbench/current` file is also maintained for backward compatibility.

## Password storage

Passwords are stored in `apps.<name>.password` in the workspace `trustable.json` (not in separate `.password` files). They are set when creating an app via `POST /api/repo` and read during .env generation. During migration, passwords are recovered via `ops util kubeget whiskuser/<name> .spec.password` (never from `.password` files).

# Configuration API: GET /api/configure

This endpoint streams progress to the client. It assumes a provider has already been chosen (`provider`, `base_url`, and `api_key` are set in the workspace `trustable.json`); it does **not** prompt for provider selection. Provider selection happens once on the splash page (see [1-index.md](1-index.md)) and is changed only via the **Change Provider** button in the configure UI. If `provider` is empty, the splash flow handles the choice — `/api/configure` is only invoked afterwards.

`/api/configure` is the streamed path used by the splash after a provider is freshly chosen: it runs the Ollama connectivity check and the model-pull loop. It is **not** invoked by the configure UI's Save & Configure button — that path is now `POST /api/configuration` (see below), which persists, runs testmodel, and writes global Pi configuration in a single call.

The behaviour depends on the merged config's `provider`:

- **`provider == "ollama"`** — run Step 1 and Step 2 below.
- **`provider == "trustable"`** — skip Step 1 and Step 2 entirely. Stream a single line `OK: Skipping Ollama setup (Trustable Cloud)` so the UI shows progress, then write global Pi configuration when `pi.default` is present.
- **`provider == "private"`** — same as Trustable: skip Step 1 and Step 2 entirely. Stream a single line `OK: Skipping Ollama setup (Private AI)`, then write global Pi configuration when `pi.default` is present. A user-supplied OpenAI-compatible endpoint must not go through the Ollama connectivity check and model-pull loop.

In both cases the frontend separately calls `GET /api/testmodel` after the configure stream ends to run the say-OK connection test.

## Step 1: Check Ollama connectivity (with retries) — Ollama only

Before anything else, verify that Ollama is reachable. The probe is `GET <root>/v1/models` (the OpenAI-compatible model-list endpoint, considered successful on any 2xx status). The root is derived from `cfg.base_url` (stripped of the `/v1` suffix); if `base_url` points at the embedded server (localhost / 127.0.0.1 / "ollama"), the `OLLAMA_ENDPOINT` env var is used instead. Try up to 12 times (2 minutes total) with 10-second intervals between attempts. Each attempt is streamed to the client:

- On success: `OK: Ollama is running`
- On retry: `Attempt N/12: Cannot reach Ollama at <endpoint> - retrying in 10 seconds...`
- On final failure: `ERROR: Cannot connect to Ollama at <endpoint> after 2 minutes. Please check that Ollama is running and try again.`

If Ollama cannot be reached, stop here — do not proceed to pull models. The frontend shows a **Retry** button so the user can fix the issue and try again.

## Step 2: Pull models — Ollama, internal mode only

For **internal** Ollama, connect to the resolved Ollama endpoint and pull the models listed under `models` that are **not already installed**.

Installed models are detected by a single `GET <root>/api/tags` call before the loop. Ollama reports a model pulled as `foo` under the name `foo:latest`, so both the raw name and the `:latest`-stripped name are indexed and the configured id is looked up under the same normalisation. Then, per model:

- already installed → stream `OK: <name> already installed`, no HTTP pull;
- missing → pull as before, streaming `Pulling model <name>` then `OK: <name> pulled`.

If `/api/tags` itself fails, stream `OK: Could not list installed models (<err>) — pulling all` and pull every model. Failing open keeps the previous behaviour on an unexpected endpoint rather than silently skipping an install that never happened.

This matters because the models are already present after the first configuration, while the flow reruns on every provider re-selection, every retry after an `AUTH_REQUIRED` sign-in, and every catalog version bump — at a 600s client timeout per model, on a screen the user sits and watches. No pull state is persisted in `trustable.json`: the `/api/tags` check is self-healing, so a manually deleted model is re-pulled and the detection cannot go stale.

For **own host** Ollama (i.e. `base_url` points at a non-localhost host) this step is skipped — the models were discovered via `POST /api/discover-models` against `<base_url>/models` on that host and are already installed there. The stream emits `OK: Skipping model pull (using your own Ollama host — models are already installed there)`.

# Prepare global Pi configuration

The Configure flow is the only owner of Pi's global configuration. Both the
streamed provider-setup endpoint and `POST /api/configuration` write only after
`pi.default` passes the provider connectivity probe:

- `${PI_CODING_AGENT_DIR:-~/.pi/agent}/models.json`;
- `${PI_CODING_AGENT_DIR:-~/.pi/agent}/settings.json`;
- `${PI_CODING_AGENT_DIR:-~/.pi/agent}/auth.json`.

The writer merges only Trustable-owned keys, preserves unrelated Pi providers
and settings, and uses the file modes and credential boundary defined in
[pi.md](pi.md). It writes the selected endpoint under `trustable` for the
Trustable status catalog, `ollama` for embedded/status-backed Ollama, or `local`
for user-provided endpoints including Private AI, then limits runtime selection to that
single active prefix. Generated model entries preserve `reasoning` and a
sanitized Pi `thinkingLevelMap`. A Trustable Cloud coding model with no
capability metadata receives the managed `reasoning: true` baseline, which
allows `high` but does not opt it into `xhigh`. `auth.json` is the only file
containing the real provider credential.

Configure writes these files only after a successful test-model request.
A failed probe preserves any previously working Pi files. App launch never
writes or repairs global Pi configuration and never creates `opencode.json`.
Project-local MCP and instruction assets are described in
[4-launch.md](4-launch.md).

After the live files are written, the same JSON content is persisted under
`<WorkspaceDir>/.trustable/pi-agent-config/`. Only `models.json`,
`settings.json`, and `auth.json` are persisted; Pi's npm package directory
continues to come from the current image/setup.

During server preflight, a valid durable snapshot is restored into the fresh
runtime directory and merged with the current image's package registration.
When upgrading an existing workspace that predates the snapshot, preflight may
recreate the three native files from the provider, model, limits, endpoint, and
credential already stored in the workspace `trustable.json`. This recovery does
not probe the provider or alter the saved selection. If `pi.default` is absent,
preflight does not invent one and the normal Configure guard remains mandatory.

# Manage configuration: GET /api/configuration

Returns the merged configuration (base + workspace overrides) as JSON. The
notebook block includes `repository`, `ref`, and `has_token`; it never contains
the GitHub token value.

# POST /api/configuration

The unified save endpoint used by `configure.html` and by the splash provider-choice handlers. Performs two steps in order and returns a single JSON result:

1. **Persist** — write the payload to the workspace `trustable.json`. Preserve the existing `apps` section if not included in the request. Regenerate `.env` and `.env.production` files for all apps that have a workbench directory.
   The optional `notebook.github_token` request field is write-only and is
   stored in the private workspace secret file, never in `trustable.json`.
   Omitting it preserves the current token; `notebook.clear_token=true`
   explicitly removes it. Repository/ref are validated and normalized before
   persistence.
2. **Run testmodel** — invoke the same logic as `GET /api/testmodel` (say-OK prompt against `pi.default` using the resolved provider URL and `api_key` of the just-saved merged config). For internal Ollama, `base_url` remains `http://localhost:11434/v1` on disk and server-side requests use `OLLAMA_ENDPOINT`; in the Trustable pod this resolves to the pod-local `ollama serve` process. An Ollama authentication failure is preserved as `testmodel.auth_required` in the save response and as `AUTH_REQUIRED:` in the streamed Configure gate. The browser opens the managed Ollama Cloud sign-in modal and reruns the complete gate after Retry, ensuring the Pi global configuration is written only after the model succeeds.
3. **Write global Pi configuration** — only when testmodel succeeds, merge the Trustable provider into `models.json`, `settings.json`, and `auth.json`. On test failure the previously working files remain unchanged.

This endpoint does not write any per-app agent configuration. App launch does
not repeat the global write.

Response shape on success:

```json
{ "status": "saved", "testmodel": { "ok": true } }
```

On testmodel failure (still HTTP 200 — the save succeeded, only the connectivity check failed):

```json
{ "status": "saved", "testmodel": { "ok": false, "error": "<message>" } }
```

Persist failures return HTTP 4xx/5xx with a plain-text error body (via `http.Error`). The frontend distinguishes a save failure (non-2xx) from a connection failure (2xx with `testmodel.ok == false`).

Rationale for the single endpoint: testmodel must see the just-persisted config. A single endpoint guarantees ordering and gives the caller one network round-trip and one error path to render.

`GET /api/configure` (the streamed splash flow) is unchanged — it remains the path for the Ollama connectivity check and the model-pull loop after a provider is freshly chosen.

# GET /api/testmodel

Tests the AI model connection by prompting `pi.default` from the merged config
with `Reply with exactly: OK`, using the resolved provider URL and `api_key`.

The reply is asserted, not merely its shape: the probe passes when
`choices[0].message.content`, trimmed and lowercased, **contains** `ok`. That
tolerates a small model adding punctuation or a stray word while still failing
on errors, refusals, and empty completions — a well-formed completion alone is
not proof the model is usable for coding. A non-confirming reply becomes
`model did not confirm: <first 80 chars of the reply>`.

The same probe is shared by `GET /api/configure` and `POST /api/configuration`,
so all three entry points apply one consistent gate. Authentication failures
still take precedence over the content assertion: an Ollama sign-in error is
classified `auth_required` so the splash opens the Ollama Cloud sign-in modal
instead of showing a generic failure.

For internal
Ollama, the resolved URL is derived from `OLLAMA_ENDPOINT` rather than the
persisted localhost `base_url`; in the Trustable pod that endpoint is the
pod-local `ollama serve` process. For own-host Ollama, Trustable, and Private AI it
is the configured provider URL. There is no separate `testmodel` field.

# Per-app configuration: GET /api/appconfig/<name>

Returns the environment variable configuration for a specific app, read from the merged config's `apps` section.

Response contains `EnvVar[]` with:
- **Readonly rows**: `OPS_USER`, `OPS_PASSWORD`, `OPS_APIHOST` (fixed dev values from the current cluster, editable prod values)
- **Custom rows**: any additional per-app development/production variables (there is no global `env` section)

# POST /api/appconfig/<name>

Accepts `{ "vars": [{ "name": "...", "dev_value": "...", "prod_value": "..." }] }`.

Saves the development and production values to `apps.<name>.development` and `apps.<name>.production` in the workspace config, then regenerates the workbench `.env` and `.env.production` files.

# Configuration UI (configure.html)

## Entry point

The **Configure** button on `applist.html` links directly to `configure.html`. Provider selection is **not** re-prompted here — once a provider has been chosen on first run (see [1-index.md](1-index.md)), the Configure button always opens the model-and-defaults editor.

If `configure.html` loads with no provider set in the workspace config (e.g. a corrupted or partially-migrated workspace), it redirects to `index.html?choose=1` so the user goes through the provider-choice flow before editing models.

To switch providers from this screen, the user clicks the **Change Provider** button (see below), which navigates to `index.html?choose=1` and re-runs the choice + reseed flow described in "Per-provider seeding".

## Layout

The page header shows a "Current provider: <Ollama|Trustable>" line and a **Change Provider** button (navigates to `index.html?choose=1`).

The page uses the shared Nuvolaris-style Trustable visual system defined in
[1-applist.md](1-applist.md) under "Shared Trustable visual system" and linked
from `web/trustable-ui.css`. Keep the configuration UI compact and operational:
neutral panels, thin table rules, Work Sans typography, restrained buttons, 4px
radii, and no marketing hero. Restyling must preserve all existing field ids,
provider switching,
own-host/Private AI discovery, model table editing, dropdown selection, save/test
behavior, and redirects.

Sections (rendered top to bottom in this order):

- **Ollama Host** *(only when `provider == "ollama"`; this is the first section on the page)* — lets the user change the hostname and port of the Ollama server. The row renders as a single line: the literal text `http://`, then a text `<input>` for **hostname** (placeholder `hostname`), then the literal `:`, then a text `<input>` for **port** (placeholder `port`), then the literal `/v1`, then a **Test** button. On save, recombine into `http://<host>:<port>/v1` and write it to `base_url`. Only host and port are editable — scheme is always `http://` and path is always `/v1`. This section is hidden when `provider == "trustable"`.
    - In **internal** mode (or when `base_url` parses as `http://(localhost|127.0.0.1|ollama):...`) the inputs are pre-filled from the existing `base_url`. The Test button is still available for re-validation but is not required.
    - In **own host** mode (URL `?ollama=own`, or when `base_url` is empty / non-localhost) the hostname input starts empty (port defaults to `11434`), three bullets are shown below the row ("Provide the IP of your local machine or intranet server (NOT 127.0.0.1)", "Enable network access on that machine", "It must be accessible via HTTP without authentication"), and the user must click **Test** before saving. **Test** calls `POST /api/discover-models` with `base_url = "http://<host>:<port>/v1"` and `api_key = "dummy"`; on success it replaces `config.models` with the discovered list (each model getting the default limits `maxToken=128000`, `maxOutput=32768`) and resets `config.pi.default` so the user chooses the Pi model.
- **`<Provider> Models`** — a table of the currently selected provider's models with editable **Context Size** and **Max Output** columns and a **For coding** column that shows whether each model can be selected for coding agent work. The heading text is `"Ollama Models"` when `provider == "ollama"` and `"Trustable Models"` when `provider == "trustable"`. Rows are read from the workspace `models` map (which was last seeded from `/api/status` per "Per-provider seeding" above). Switching provider via **Change Provider** reseeds this section from `/api/status`.
  - **Ollama** — editable. The user can add or remove rows; adds/removes only edit the workspace `models` map (they do not change the catalog). The header shows an **Add Model** button and each row has a **Remove** button.
  - **Trustable** — read-only. The model list is authoritative from `/api/status` and the user cannot add or remove rows. The **Add Model** button and per-row **Remove** buttons are hidden. Instead, the header shows a **Refresh** button that re-fetches `/api/status` and rewrites the workspace `models` map and `pi.default` from `status.trustable`. The dropdown repopulates from the new list. The button is also hidden whenever the active config is own-host Ollama (see §"Exception — own-host Ollama" in "Model catalog").
- **Pi Model** — one `<select>` labelled "Default Model", populated from the
  keys of the active provider's `models` map. The selected value is written to
  `pi.default`; there is no small/secondary model.
  The dropdown includes only models allowed by the Pi model policy above.
  The provider model table can still show hidden models with a short reason, so
  operators can diagnose provider inventory without letting a basic user choose
  an embedding, rerank, tiny, or otherwise unsuitable model. `POST
  /api/configuration` enforces the same policy server-side before writing
  `trustable.json`; UI filtering alone is not sufficient.
- **Notebook Repository** — global repository and branch/ref fields plus a
  write-only GitHub token field. The page shows only whether a token is already
  configured. Leaving the password field blank preserves it; an explicit
  remove checkbox clears it. Public notebook reads do not require a token, but
  TruACP save/add/remove remain disabled until one is configured.
- **Git User** — name and email used as commit-author defaults. The card is
  visible while no managed GitHub account is connected. It is hidden when
  `GET /api/github/status` reports an authenticated account, because showing it
  beside the connected account is misleading; hiding does not delete or alter
  its saved values, and disconnecting GitHub shows it again.
If the URL has `?reselect=1` (set by the splash or applist when
`status[provider].modelsVersion` bumped), show a banner asking the user to
re-select the Pi model. If `applist.html` finds no `pi.default`, it redirects to
`configure.html?setup=1`; that provider-independent banner explains that one
explicit selection is required by the hard cutover. Legacy `opencode` fields
are not used to satisfy this guard.

After **Save & Configure** succeeds and the selected model passes its
connection probe, navigate once to `applist.html?configured=1`. The app list
removes this marker from browser history without reloading and suppresses only
its immediate catalog-reselect redirect. This guarantees that a successful
configuration can exit the configure screen even if a status response changes
concurrently; normal `modelsVersion` checks resume on the next app-list load.

The `buildConfig()` function preserves `provider`, `base_url`, `api_key`, `apps`, and `predefined_env` fields when saving — this POST is a full-document write, so a field left out is erased. (The `register_url` field is exposed read-only by `loadTrustableConfig` from the `AIP_REGISTER_URL` env var and must not be sent back on save.)

The Configure page also owns a **Predefined Environment Variables** card, sitting
between Template Repository and Git User. It edits `predefined_env` through
`GET`/`POST /api/predefined-env` with its **own** Save button and inline status:
it does not go through **Save & Configure**, does not run a model probe, and does
not navigate away. See "Predefined environment variables" above.

Read the configuration with `GET /api/configuration`. **Save & Configure** calls
`POST /api/configuration`, which persists, runs testmodel, and writes Pi's
global native files after a successful probe. The button does **not** invoke
`GET /api/configure` — the streamed connectivity-check + model-pull flow runs
only on the splash, after a provider is freshly chosen.

On `testmodel.ok == true` → navigate to `applist.html`. On `testmodel.ok == false` → stay on `configure.html` and surface the error inline (see "Save & Configure UX" below). Do **not** bounce back to `index.html` on failure — the splash would just re-run configure with the same broken settings.

## Save & Configure UX

Clicking **Save & Configure** disables the button and renders an inline status strip that progresses through:

- `Saving configuration…`
- `Testing connection…`
- `Success` (brief, green) → navigate to `applist.html`

On any error: replace the strip with a red error box containing the error text and a **Retry** button that re-runs the same `POST /api/configuration` call. The page stays open; the user can edit the form and retry. No automatic redirect on failure.

# App config UI (appconfig.html)

Shows a table with columns: VARIABLE, Development, Production, Actions.

The app config editor uses the same shared Trustable visual system from
`web/trustable-ui.css`. Treat it as a dense data-entry page: compact table rows,
aligned inputs, restrained import / commit actions, and no decorative hero
content. Preserve all environment import, edit, save, unsaved-warning, and close
behavior while restyling.

- Fixed readonly rows for OPS_USER, OPS_PASSWORD, OPS_APIHOST
- Custom variables that can be added/removed (per-app `development` / `production` only — there is no global `env` section)
- Include separate Import `.env` and Import `.env.production` actions. Import
  `.env` reads a local `.env` file in the browser and writes parsed `KEY=VALUE`
  rows into editable Development values, adding missing keys as custom
  variables with empty Production values. Import `.env.production` reads a local
  `.env` or `.env.production` file and writes parsed values into Production,
  adding missing keys as custom variables with empty Development values.
  Read-only Development values are never overwritten. The import changes only
  the in-memory table until the user saves.
