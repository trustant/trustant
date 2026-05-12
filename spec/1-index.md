This file describes the index splash screen page, put the code in file `index.html`

# Version

When the page loads invoke the version api.

If it expired show a page with only a centered message saying "This version expired. Please Update. For info email: info@nuvolaris.io"

# Splash Screen

The page shows centered the Trustable logo (`trustable-logo.svg`) in large size, the text "Trustable" in large font, and below it the version (e.g. "v1.2.3") in large font.

Show also in smaller font "Expiration date: <date>"

# Provider choice

After the version check, fetch the merged config via `GET /api/configuration`, then refresh the model catalog (see [2a-config.md](2a-config.md) "Model catalog"):

1. `GET <AIP_REGISTER_URL origin>/.well-known/models.json`. On success write the body to `<WorkspaceDir>/models.json`. On failure, fall back to the cached copy if any; if there's no cache, surface a fatal error and stop.
2. Compare the new `version` field against the cached one. If it changed, redirect immediately to `configure.html?reselect=1` (the configure UI shows a "Model catalog updated" banner and forces the user to re-pick the OpenCode default and small models). The flow below does not run on this turn — it resumes after the user saves on the configure screen.

Then:

- If the merged config has no `provider` set, show the **Provider Choice** modal. This is the first-run / unconfigured state — Configuration cannot run without a provider.
- If the URL contains `?choose=1`, show the **Provider Choice** modal regardless of the current provider (used by configure.html's "Change Provider" button).
- Otherwise (provider is set and `?choose=1` is absent), skip the choice screen and go straight to the **Configuration** flow below.

The Provider Choice modal is centered and shows two cards:

- **Ollama Cloud** — "Free forever. Publishing not available."
- **Trustable Cloud** — "Free until 30 June 2026, then \$20/month. Includes 1000 credits/month and one app published on nuvolaris.dev."

## Ollama Cloud selected

1. `POST /api/configuration` with the merged config plus:
   - `provider: "ollama"`
   - `models` and `opencode` copied verbatim from `<cached models.json>.ollama.models` and `<cached models.json>.ollama.opencode` (per "Per-provider seeding" in [2a-config.md](2a-config.md))
   - `env.OPENAI_BASE_URL = http://localhost:11434/v1`
   - `env.OPENAI_API_KEY = "dummy"`

   The dummy `OPENAI_API_KEY` will fail Ed25519 verification at the publish endpoints, which is the intended behavior for Ollama Cloud — see [6-publish.md](6-publish.md).
2. Hide the modal and run the **full** Configuration flow (connectivity check + model pull + say-hello test).

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
   - `models` from `<cached models.json>.trustable.models` (per "Per-provider seeding" in [2a-config.md](2a-config.md))
   - `opencode` from the iframe payload if present, else from `<cached models.json>.trustable.opencode`
   - `env.OPENAI_BASE_URL` and `env.OPENAI_API_KEY` from the iframe payload

   then `POST /api/configuration`.
4. Hide the modal and run the **trimmed** Configuration flow: skip the Ollama connectivity check and the model-pull step entirely (the backend handles this based on `provider`); only the say-hello test runs.

# Configuration

Every time the index page is opened (after a provider has been chosen), invoke the configuration api.
Expect a streamed answer and show the messages with a modal while it is configuring.

When `provider == "trustable"` the backend skips the Ollama connectivity check and model-pull steps and streams a single `OK: Skipping Ollama setup (Trustable Cloud)` line — see [2a-config.md](2a-config.md).

Then invoke the openai ai api using informations in trustable.json, env.OPENAI_BASE_URL and env.OPENAI_API_KEY and the opencode.small model, asking hello.

If `/api/testmodel` returns an error **and** `provider == "ollama"`, show a sign-in required popup. The backend treats common sign-in messages (`not logged in`, `unauthorized`, `401`, etc.) **and Ollama's `internal service error`** as auth failures — they all route through this same flow:

- If the page URL has a query string, show "Click here to login to Ollama Cloud" as a link pointing to `https://ollama.com/connect?<query_string>` and a Retry button.

- If no query string, try execute `ollama signin` and parse the output, looking for a string starting with `https://ollama.com/connect`, and show  "Click here to login to Ollama Cloud" with a link pointing to the found url

- if  there is not a login message, show "you are not logged in ollama cloud.\nPlease execute `ollama signin` and click Retry button.

Repeat until the test succeeded.

When `provider == "trustable"` and `/api/testmodel` returns an error, run the **Trustable sign-in recovery**:

1. Re-open the registration iframe overlay (the same one used by the initial Trustable Cloud selection) pointing at `register_url`.
2. Wait for the iframe's `postMessage` payload `{ env: { OPENAI_BASE_URL, OPENAI_API_KEY }, opencode?: { default, small } }`.
3. On message, merge the new `base_url` and `api_key` into the workspace config, `POST /api/configuration`, then **re-run the health check** (`/api/testmodel`).
4. If the user closes the overlay with **Cancel**, show a generic error with a Retry button and stop the loop until the user clicks Retry (which re-opens the iframe).

Repeat until the test succeeds.

If the user cancels the recovery flow (closes the signin modal without retrying, or clicks **Cancel** in the Trustable iframe), or the test still fails after recovery, the splash **returns to the Provider Choice modal** rather than proceeding to `applist.html`. A failed health check always sends the user back to provider selection — the app is unusable without a working model.

Once configuration completes successfully, navigate to `applist.html`.
