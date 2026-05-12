This file describes the configuration system and the configure.go module.

# Layered Configuration

Configuration uses a two-layer system based on `trustable.json`:

1. **Base config** (`trustable.json` in the app root directory) — immutable defaults shipped with the application
2. **Workspace config** (`<WorkspaceDir>/trustable.json`) — user overrides, created on first save or migration

Loading merges both layers: workspace fields override base fields. Maps (models, env) are merged key-by-key. The workspace file only needs to contain overrides and the `apps` section.

## trustable.json structure

```json
{
    "provider": "ollama" | "trustable",
    "base_url": "<provider base URL>",
    "api_key": "<provider API key>",
    "models": {
        "<model-name>": "<context-size>"
    },
    "opencode": {
        "default": "<chosen default model name>",
        "small": "<chosen small model name>"
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

- `provider` — `ollama` or `trustable`. Set on first run by the provider-choice screen on the splash page. Workspace-only field (not in base config). Whether the user can publish is determined at request time by verifying the Ed25519 signature on `api_key` (see [6-publish.md](6-publish.md) and [10-validate_key.md](10-validate_key.md)) — there is no separate `publishing` flag.
- `base_url` and `api_key` — top-level provider credentials. Set when a provider is chosen:
  - **Ollama** — `api_key = "dummy"`. `base_url` defaults to `http://localhost:11434/v1` but the user can change the hostname and port via the configure UI (see "Configuration UI"). Scheme is fixed to `http://` and path is fixed to `/v1`; only host and port are editable. The Ollama `base_url` must always parse as `http://<host>:<port>/v1` — no HTTPS, no auth, no other paths.
  - **Trustable** — taken from the registration message posted by the ai-proxy iframe (`{ base_url, api_key }`), see [1-index.md](1-index.md).
  There is **no** global `env` section in `trustable.json`. Environment variables live only inside each app under `apps.<name>.development` / `apps.<name>.production`.
- `models` — the model list for the **currently selected provider**, copied from the cached model catalog (see "Model catalog" below). The previous `ollama` key is removed; the same shape is now provider-agnostic and is rewritten when the user switches provider.
- `opencode.default` / `opencode.small` — must be names that exist as keys in `models`. The configurator UI (see "Configuration UI") presents these as dropdowns populated from `models`, not free-text fields.
- `AIP_REGISTER_URL` (environment variable, **mandatory** at startup; preflight fails if unset) — base URL of the ai-proxy registration UI. The splash page loads it in an iframe when the user picks Trustable Cloud; the top-up form lives at `<AIP_REGISTER_URL>/top-up`. The registration URL is configured **only** via this env var; there is no JSON field. `loadTrustableConfig` exposes it on the returned config as `register_url` (read-only, not persisted) so the frontend can read it via `GET /api/configuration`.
- `AIP_BASE_URL` (environment variable, **mandatory** at startup; preflight fails if unset) — base URL of the ai-proxy JSON API. The backend uses it directly for `/api/credits`, `/api/topup`, and `/api/status` — no `/v1`/`/v2` rewriting happens. Server-side only; not exposed on the config returned to the frontend.

The base config has no `apps` or `provider` section. Those live only in the workspace config.

There is no top-level `testmodel` field. The model used for the say-hello connection test is always `opencode.small`.

All fields in the workspace config use `omitempty` — absent fields inherit from the base.

## Model catalog

The proxy publishes a per-provider model catalog at `<AIP_REGISTER_URL origin>/.well-known/models.json` (see ai-proxy [SPEC §8](../../ai-proxy/spec/SPEC.md)). Shape:

```json
{
    "version": <integer>,
    "ollama":    { "models": { "<id>": "<ctx>", ... }, "opencode": { "default": "...", "small": "..." } },
    "trustable": { "models": { "<id>": "<ctx>", ... }, "opencode": { "default": "...", "small": "..." } }
}
```

### Fetching and caching

On startup the trustable-app fetches `models.json` from the proxy and caches it as `<WorkspaceDir>/models.json`. If the fetch fails the cached copy is used; if there is no cached copy and the fetch fails, startup fails (the choice screen cannot render meaningful dropdowns without a catalog).

### Version check

After each successful fetch, compare `version` against the version in the previously cached file:

- **Same version** — proceed normally.
- **Version changed** — overwrite the cache with the new file, then redirect the user to `configure.html?reselect=1` so they can re-pick `opencode.default` / `opencode.small` from the (possibly changed) model list. The redirect happens *before* the splash configuration flow runs.
- **First run / no cached version** — write the cache and proceed; the provider-choice screen seeds `models` and `opencode` from the catalog automatically when the user picks a provider, so no reselect screen is needed.

### Per-provider seeding

When the user picks a provider on the choice screen (or when the configure UI's "Change Provider" button completes a switch), the workspace `trustable.json` is rewritten with:

- `provider` — the chosen value
- `models` — `<catalog>.<provider>.models` verbatim
- `opencode` — `<catalog>.<provider>.opencode` verbatim (defaults; the user can override via the dropdowns in the configure UI)

This is what the user means by "change the OpenCode models to the Ollama models when switching back to Ollama" — switching provider replaces the model list **and** the opencode defaults from the catalog.

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

The function `generateAppEnvFiles(appName)` builds the workbench `.env` from:
1. Fixed vars: `OPS_USER=<appName>`, `OPS_PASSWORD=<from apps.password>`,

2. The OPS_APIHOST is set in this order
- by env variables OPS_APIHOST/APIHOST/TRUSTABLE_DEFAULT_APIHOST
- on Mac by the content of file ~/Library/Application Support/Trustable/apihost if the file is present
- on Windows by the content of %APPDATA%/Trustable/apihost  if it is present
- defaults to http://miniops.me

3. Per-app `development` overrides

Production `.env.production` is written from the per-app `production` values.

There is no global `env` section: environment variables for an app come only from the fixed vars above and the per-app `development` / `production` maps.

`regenerateAllAppEnvFiles()` iterates all apps and regenerates for each that has a workbench directory.

## Current app tracking

The `current` field in the workspace `trustable.json` stores the name of the currently launched app. It is set when an app is launched (`writeCurrentApp`) and cleared when the launch is stopped (`removeCurrentFile`). The `workbench/current` file is also maintained for backward compatibility.

## Password storage

Passwords are stored in `apps.<name>.password` in the workspace `trustable.json` (not in separate `.password` files). They are set when creating an app via `POST /api/repo` and read during .env generation. During migration, passwords are recovered via `ops util kubeget whiskuser/<name> .spec.password` (never from `.password` files).

# Configuration API: GET /api/configure

This endpoint streams progress to the client. It assumes a provider has already been chosen (`provider`, `base_url`, and `api_key` are set in the workspace `trustable.json`); it does **not** prompt for provider selection. Provider selection happens once on the splash page (see [1-index.md](1-index.md)) and is changed only via the **Change Provider** button in the configure UI. If `provider` is empty, the splash flow handles the choice — `/api/configure` is only invoked afterwards.

The behaviour depends on the merged config's `provider`:

- **`provider == "ollama"`** — run Step 1 and Step 2 below.
- **`provider == "trustable"`** — skip Step 1 and Step 2 entirely. Stream a single line `OK: Skipping Ollama setup (Trustable Cloud)` so the UI shows progress, then proceed to "Prepare opencode config".

In both cases the frontend separately calls `GET /api/testmodel` after the configure stream ends to run the say-hello connection test.

## Step 1: Check Ollama connectivity (with retries) — Ollama only

Before anything else, verify that Ollama is reachable. Try up to 12 times (2 minutes total) with 10-second intervals between attempts. Each attempt is streamed to the client:

- On success: `OK: Ollama is running`
- On retry: `Attempt N/12: Cannot reach Ollama at <endpoint> - retrying in 10 seconds...`
- On final failure: `ERROR: Cannot connect to Ollama at <endpoint> after 2 minutes. Please check that Ollama is running and try again.`

If Ollama cannot be reached, stop here — do not proceed to pull models. The frontend shows a **Retry** button so the user can fix the issue and try again.

## Step 2: Pull models — Ollama only

Read the merged config (base + workspace), connect to the Ollama endpoint and pull every model listed under `models`, returning in streaming mode messages "Pulling model XXX".

# Prepare opencode config

Create the OpenCode config in `~/.config/opencode/opencode.json` following the
structure (where `<provider>` is the active `provider` from `trustable.json` —
either `ollama` or `trustable`):

```
{
  "$schema": "https://opencode.ai/config.json",
  "instructions": ["~/.config/opencode/opencode.md"],
  "model": "<provider>/<opencode.default>",
  "small_model": "<provider>/<opencode.small>",
  "disabled_providers": [<default OpenCode providers, minus "<provider>">],
  "provider": {
    "<provider>": {
      "options": { "baseURL": "<base_url>", "apiKey": "<api_key>" },
      "models": { <one entry per model in trustable.json "models" map> }
    },
    <preserved custom user providers>
  }
}
```

Always generate the `opencode.json` from the merged `trustable.json`, regardless
of any existing `opencode.json`. The `<provider>` entry's `models` map contains
one entry per model listed under `models` in `trustable.json` (built from the
template below). The provider's `baseURL` and `apiKey` are taken **directly
from the top-level `base_url` and `api_key` fields** of `trustable.json`:

- Ollama → `baseURL = "http://localhost:11434/v1"`, `apiKey = "dummy"`.
- Trustable → `baseURL` and `apiKey` are the values posted by the ai-proxy
  registration iframe and stored in `trustable.json` (see [1-index.md](1-index.md)).

Because `<provider>` is now a generated provider it must not appear in
`disabled_providers`.

Always set top-level `model` and `small_model` to
`<provider>/<opencode.default>` and `<provider>/<opencode.small>` respectively,
using the values from `trustable.json`. Any user-selected `model`/`small_model`
from a previous `opencode.json` is overwritten.

Hide OpenCode's other automatic/default providers via `disabled_providers`,
but remove any ID from that disabled list when the same ID is present as a
preserved custom provider.

Preserve custom providers (i.e. anything other than the Trustable-managed
`ollama`, `trustable`, and `vllm` providers) from any existing `opencode.json`.
Do not emit `enabled_providers`: in OpenCode that field is a whitelist and
would prevent providers added later from being selectable.

Generated OpenCode keys that were introduced for experimental provider profiles
(`lsp`, `mcp`, `tools`, `agent`, `compaction`, `enabled_providers`) are not
carried forward when regenerating the Trustable config. This keeps the default
OpenCode flow minimal while still allowing users to add providers from the
OpenCode UI and keep them persisted across app launches.

In Ollama mode, to get the capabilities of a model use the OllamaEndPoint,
list the models then show their capabilities. In Trustable mode capability
discovery is not available; assume `tool_call: true, reasoning: false` and
let the user override later via the OpenCode UI.

To get the context size for a model look in trustable.json -  <value>K mean  <value> * 1024.

Model name is the model id, split in "-" and ":", capitalized, with numbers with extensions in parenthesis

Example: qwen3-coder:480b-cloud => Quen3 Coder (48OB) Cloud

Template:

```
"<model>": {
  "name": <model-name>,
  "tool_call": >true if you find tools in capabilities
  "reasoning": true if you find thinking>
  "temperature": true,
  "limit": {
    "context": <context size for model>
    "output": 32768
  },
  "options": {
    "maxTokens": 8192
  },
  "variants": {
    "fast": {
      "options": { "maxTokens": 2048 }
    },
    "deep": {
      "options": { "maxTokens": 16000 }
    },
    "disabled_variant": {
      "disabled": true
    }
  }
}
```

OpenCode state must be persistent across pod restarts. At container startup,
`~/.config/opencode`, `~/.cache/opencode`, and `~/.local/share/opencode` are
symlinked into the mounted workspace under `.trustable/opencode/`. After
generating `opencode.json`, write the embedded `opencode.md` to
`~/.config/opencode/opencode.md` (the instructions file referenced by absolute
path in the config). Also copy the embedded `tools` folder to
`~/.config/opencode/tools`, overwriting existing files.

# Manage configuration: GET /api/configuration

Returns the merged configuration (base + workspace overrides) as JSON.

# POST /api/configuration

Saves the configuration to the workspace `trustable.json`. Preserves the existing `apps` section if not included in the request. After saving, regenerates `.env` files for all apps that have a workbench directory.

Does not execute the configuration — you need to do a `GET /api/configure` for that.

# GET /api/testmodel

Tests the AI model connection by sending a "hello" prompt to `opencode.small` from the merged config, using the top-level `base_url` and `api_key`. There is no separate `testmodel` field — the small OpenCode model is always used for the connection test, in both Ollama and Trustable modes.

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

Sections (rendered top to bottom in this order):

- **Ollama Host** *(only when `provider == "ollama"`; this is the first section on the page)* — lets the user change the hostname and port of the local Ollama server. The row renders as a single line: the literal text `http://`, then a text `<input>` for **hostname** (placeholder `hostname`), then the literal `:`, then a text `<input>` for **port** (placeholder `port`), then the literal `/v1`. Below the row, show the help text: *"Specify hostname and port for your local Ollama host (HTTP only, no auth)."* On load, parse the existing `base_url` as `http://<host>:<port>/v1` and populate the two inputs from it; if parsing fails, fall back to host `localhost` and port `11434`. On save, recombine into `http://<host>:<port>/v1` and write it to `base_url`. Only host and port are editable — scheme is always `http://` and path is always `/v1`. This section is hidden when `provider == "trustable"` (Trustable's `base_url` is fixed by the registration payload).
- **`<Provider> Models`** — a table of the currently selected provider's models with context size. The heading text is `"Ollama Models"` when `provider == "ollama"` and `"Trustable Models"` when `provider == "trustable"`. Rows are read from the cached `models.json` for the active provider. Switching provider via **Change Provider** reseeds this section from the catalog (see "Per-provider seeding" above).
  - **Ollama** — editable. The user can add or remove rows; adds/removes only edit the workspace `models` map (they do not change the catalog). The header shows an **Add Model** button and each row has a **Remove** button.
  - **Trustable** — read-only. The model list is authoritative from the proxy catalog and the user cannot add or remove rows. The **Add Model** button and per-row **Remove** buttons are hidden. Instead, the header shows a **Refresh** button that calls `POST /api/models/refresh` to re-fetch the catalog from the proxy and rewrite the workspace `models` map (and `opencode` defaults) from `<catalog>.trustable`. The dropdowns repopulate from the new list.
- **OpenCode Models** — two `<select>` dropdowns labelled "Default Model" and "Small Model". Both are populated from the keys of the active provider's `models` map. Selected values are written to `opencode.default` and `opencode.small`. Free-text input is no longer accepted.
- **Git User** — name and email (unchanged).

If the URL has `?reselect=1` (set by the splash page when the catalog version changed — see "Model catalog → Version check"), show a banner at the top: *"Model catalog updated. Please re-select the default and small OpenCode models."* The banner clears once the user clicks **Save & Configure**.

The `buildConfig()` function preserves `provider`, `base_url`, `api_key`, and `apps` fields when saving. (The `register_url` field is exposed read-only by `loadTrustableConfig` from the `AIP_REGISTER_URL` env var and must not be sent back on save.)

Read the configuration with `GET /api/configuration`, save with `POST /api/configuration`, then execute `GET /api/configure` showing the progress downloading models.

# App config UI (appconfig.html)

Shows a table with columns: VARIABLE, Development, Production, Actions.
- Fixed readonly rows for OPS_USER, OPS_PASSWORD, OPS_APIHOST
- Custom variables that can be added/removed (per-app `development` / `production` only — there is no global `env` section)
