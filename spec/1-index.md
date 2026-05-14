This file describes the index splash screen page, put the code in file `index.html`

# Version

When the page loads invoke the version api.

If it expired show a page with only a centered message saying "This version expired. Please Update. For info email: info@nuvolaris.io"

# Splash Screen

The page shows centered the Trustable logo (`trustable-logo.svg`) in large size, the text "Trustable" in large font, and below it the version (e.g. "v1.2.3") in large font.

Show also in smaller font "Expiration date: <date>"

# Provider choice

After the version check, fetch the merged config via `GET /api/configuration`, then fetch `GET /api/status` (the backend's pass-through of the ai-proxy `/api/v2/status` response — see [status_check.md](status_check.md)). The response carries a per-provider `modelsVersion` integer plus the canonical `models`, `default`, and `small` for each provider.

Per-provider model-list freshness is tracked on the workspace config in `model_versions: { ollama?: int, trustable?: int }`. When `provider` is set, compare `status[provider].modelsVersion` against `config.model_versions[provider]`:

- **Bumped** — persist the new value via `POST /api/configuration` and redirect to `configure.html?reselect=1` so the user re-picks `opencode.default` / `opencode.small` from the refreshed catalog. The configuration flow below does not run on this turn — it resumes after the user saves on the configure screen.
- **Same / first run** — proceed to the provider-choice / configuration flow below.

**Exception — own-host Ollama:** the reselect redirect is suppressed when the saved provider is Ollama **and** `base_url` is not a localhost URL (see "Detecting own-host Ollama" in [2a-config.md](2a-config.md)). On own-host the model list is discovered via `POST /api/discover-models` against the user's machine, not from the proxy catalog, so catalog drift is irrelevant. Internal Ollama (localhost `base_url`) and Trustable are both catalog-backed and **do** trigger the redirect.

Then:

- If the merged config has no `provider` set, show the **Provider Choice** modal. This is the first-run / unconfigured state — Configuration cannot run without a provider.
- If the URL contains `?choose=1`, show the **Provider Choice** modal regardless of the current provider (used by configure.html's "Change Provider" button).
- Otherwise (provider is set and `?choose=1` is absent), skip the choice screen and go straight to the **Configuration** flow below.

The Provider Choice modal is centered and shows two cards:

- **Ollama** — "Free forever. Publishing not available." Picking this card opens a second sub-modal that asks the user to pick between **Use internal Ollama with recommended cloud models** and **Use my own Ollama with currently installed models** (see "Ollama mode selection" in [2a-config.md](2a-config.md)).
- **Trustable Cloud** — "Free until 30 June 2026, then \$20/month. Includes 1000 credits/month and one app published on nuvolaris.dev."

## Ollama selected

After the user picks one of the two Ollama-mode cards, branch:

### Internal selected

1. `POST /api/configuration` with the merged config plus:
   - `provider: "ollama"`
   - `base_url: "http://localhost:11434/v1"`
   - `api_key: "dummy"`
   - `models` and `opencode` seeded from `status.ollama.models` and `status.ollama` (`default` / `small`) — see "Per-provider seeding" in [2a-config.md](2a-config.md)
   - `model_versions.ollama = status.ollama.modelsVersion`

   The dummy `api_key` will fail Ed25519 verification at the publish endpoints, which is the intended behavior for Ollama — see [6-publish.md](6-publish.md).
2. Hide the modal and run the **full** Configuration flow (connectivity check + model pull + say-hello test).

### My own selected

1. `POST /api/configuration` with the merged config plus:
   - `provider: "ollama"`
   - `base_url: ""`
   - `api_key: "dummy"`
   - `models: {}`
   - `opencode: { "default": "", "small": "" }`
   - `model_versions.ollama = status.ollama.modelsVersion` (recorded so a future bump doesn't trigger a reselect — own-host is exempt regardless)
2. Navigate to `configure.html?ollama=own`. The splash Configuration flow does **not** run; the user completes setup on the configure screen by entering their LAN host, clicking Test to discover models, picking default/small, and clicking Save & Configure.

## Trustable Cloud selected

1. Open a centered iframe overlay covering 80% of the viewport (width and height, centered on the page) loading the URL from `register_url` on the merged config (sourced from the `AIP_REGISTER_URL` env var; see [2a-config.md](2a-config.md)). The overlay has a "Cancel" button that closes the iframe and returns to the choice modal without saving anything.
2. Listen for `message` events from the iframe. Expected payload (matches what ai-proxy `success.html` posts today):

   ```json
   {
     "opencode": { "default": "...", "small": "..." },
     "env":      { "OPENAI_BASE_URL": "...", "OPENAI_API_KEY": "aip_..." }
   }
   ```

3. On message: close the iframe and merge into the workspace config:
   - `provider: "trustable"`
   - `models` from `status.trustable.models` (per "Per-provider seeding" in [2a-config.md](2a-config.md))
   - `opencode` from the iframe payload if present, else from `status.trustable` (`default` / `small`)
   - `base_url` and `api_key` from the iframe payload's `env.OPENAI_BASE_URL` and `env.OPENAI_API_KEY`
   - `model_versions.trustable = status.trustable.modelsVersion`

   then `POST /api/configuration`.
4. Hide the modal and run the **trimmed** Configuration flow: skip the Ollama connectivity check and the model-pull step entirely (the backend handles this based on `provider`); only the say-hello test runs.

# Configuration

Every time the index page is opened (after a provider has been chosen), invoke the configuration api.
Expect a streamed answer and show the messages with a modal while it is configuring.

When `provider == "trustable"` the backend skips the Ollama connectivity check and model-pull steps and streams a single `OK: Skipping Ollama setup (Trustable Cloud)` line — see [2a-config.md](2a-config.md).

Then invoke the openai ai api using informations in trustable.json, env.OPENAI_BASE_URL and env.OPENAI_API_KEY and the opencode.small model, asking hello.

If `/api/testmodel` returns an error **and** `provider == "ollama"`, show a sign-in required popup. The backend treats common sign-in messages (`not logged in`, `unauthorized`, `401`, etc.) **and Ollama's `internal service error`** as auth failures — they all route through this same flow:

- The backend executes `ollama signin` as a subprocess and scrapes its output for the first URL starting with `https://ollama.com/connect`. The current page's query string is **not** forwarded — the URL returned by `ollama signin` is used verbatim.

- If a URL is found, show **"Click here to login to Ollama Cloud"** as a link pointing to that URL with `target="_blank"` (opens in a new tab) and a Retry button.

- If `ollama signin` produces no recognizable URL, show **"You are not logged in to Ollama Cloud. Please execute `ollama signin` in your terminal and click Retry."**

Repeat until the test succeeded.

When `provider == "trustable"` and `/api/testmodel` returns an error, run the **Trustable sign-in recovery**:

1. Re-open the registration iframe overlay (the same one used by the initial Trustable Cloud selection) pointing at `register_url`.
2. Wait for the iframe's `postMessage` payload `{ env: { OPENAI_BASE_URL, OPENAI_API_KEY }, opencode?: { default, small } }`.
3. On message, merge the new `base_url` and `api_key` into the workspace config, `POST /api/configuration`, then **re-run the health check** (`/api/testmodel`).
4. If the user closes the overlay with **Cancel**, show a generic error with a Retry button and stop the loop until the user clicks Retry (which re-opens the iframe).

Repeat until the test succeeds.

If the user cancels the recovery flow (closes the signin modal without retrying, or clicks **Cancel** in the Trustable iframe), or the test still fails after recovery, the splash **returns to the Provider Choice modal** rather than proceeding to `applist.html`. A failed health check always sends the user back to provider selection — the app is unusable without a working model.

Once configuration completes successfully, navigate to `applist.html`.
