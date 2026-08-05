This file describes the index splash screen page, put the code in file `index.html`

# Version

When the page loads invoke the version api.

If it expired show a page with only a centered message saying "This version expired. Please Update. For info email: info@nuvolaris.io"

# Splash Screen

The page shows centered the Trustable logo (`trustable-logo.svg`) in large size, the text "Trustable Cloud" in large font, the subtitle "powered by Regolo.AI" in smaller font, and below it the version (e.g. "v1.2.3") in large font.

Show also in smaller font "Expiration date: <date>"

The splash and all provider/sign-in/configuration modals use the shared
Nuvolaris-style Trustable visual system defined in [1-applist.md](1-applist.md)
under "Shared Trustable visual system" and linked from `web/trustable-ui.css`.
This page is a real splash/choice page, so a centered first-run composition is
acceptable, but it should still use Work Sans, the shared palette tokens,
compact modal/card geometry, and restrained professional SaaS controls. Provider
selection behavior and iframe recovery flows must remain unchanged during visual
migration.

# Provider choice

After the version check, fetch the merged config via `GET /api/configuration`, then fetch `GET /api/status` (the backend's pass-through of the ai-proxy `/api/v2/status` response — see [status_check.md](status_check.md)). The response carries a per-provider `modelsVersion` integer plus the canonical `models`, `default`, and `small` for each provider.

Per-provider model-list freshness is tracked on the workspace config in `model_versions: { ollama?: int, trustable?: int }`. When `provider` is set, compare `status[provider].modelsVersion` against `config.model_versions[provider]`:

- **Bumped** — persist the new value via `POST /api/configuration` and redirect to `configure.html?reselect=1` so the user re-picks `pi.default` from the refreshed catalog. The configuration flow below does not run on this turn — it resumes after the user saves on the configure screen.
- **Same / first run** — proceed to the provider-choice / configuration flow below.

`status[provider].default` is only the seed used when a provider is first
selected or when the previously selected model disappeared from a refreshed
catalog. A valid saved `pi.default` may intentionally differ from that catalog
default and must not trigger a reselect redirect; treating that difference as
catalog drift creates a `configure.html` / `applist.html` loop.

**Exception — own-host Ollama:** the reselect redirect is suppressed when the saved provider is Ollama **and** `base_url` is not a localhost URL (see "Detecting own-host Ollama" in [2a-config.md](2a-config.md)). On own-host the model list is discovered via `POST /api/discover-models` against the user's machine, not from the proxy catalog, so catalog drift is irrelevant. Internal Ollama (localhost `base_url`) and Trustable are both catalog-backed and **do** trigger the redirect.

Then:

- If the merged config has no `provider` set, show the **Provider Choice** modal. This is the first-run / unconfigured state — Configuration cannot run without a provider.
- If the URL contains `?choose=1`, show the **Provider Choice** modal regardless of the current provider (used by configure.html's "Change Provider" button).
- If a provider exists but `pi.default` is absent or empty, redirect to
  `configure.html?setup=1` before invoking `/api/configure` or `/api/testmodel`.
  This is the expected hard-cutover path for an existing workspace containing
  only the ignored legacy `opencode` block; the splash must not expose the
  internal `pi.default not defined` diagnostic.
- Otherwise (provider is set and `?choose=1` is absent), skip the choice screen and go straight to the **Configuration** flow below.

Before opening a catalog-backed provider flow (Trustable Cloud or internal
Ollama), validate that its `/api/status` section has a non-empty `models`
object, a non-empty `default`, and that `default` is a key in `models`. An
incomplete section is unavailable and must not be persisted. Own-host Ollama
keeps its intentional empty intermediate configuration because its models are
discovered on `configure.html`.

The Provider Choice modal is centered, headed **Select your AI provider**, and
shows exactly three equal cards in a `grid-cols-1 md:grid-cols-3` row:

| Card | Subtitle | Attribution | Logo | Stored provider |
|---|---|---|---|---|
| **Cloud AI** | Subscription-based AI. | Powered by Ollama Cloud. | `ollama-head.png` | `ollama` |
| **Sovereign AI** | Credit-based AI. | Powered by Regolo.AI. | `regolo-head.png` | `trustable` |
| **Private AI** | No token required. | Your own AI hardware. | `privateai-head.png` | `private` |

All user-facing copy on this page uses sentence case with terminal punctuation.
Product names (Ollama Cloud, Regolo.AI, Trustable Cloud) keep their own
capitalization; progress messages end in an ellipsis character (`…`), not three
dots.

Note the naming: the card labelled **Cloud AI** stores `provider: "ollama"` and
**Sovereign AI** stores `provider: "trustable"`. Only the labels changed — the
persisted values are unchanged, so existing workspaces keep working.

Each card is laid out **heading → subtitle → attribution → body copy → logo**,
with the logo as the last element *inside* the card body and bottom-aligned
(`flex flex-col h-full` on the card, `mt-auto` on the `<img>`) so the three logos
line up across the row regardless of how much copy each card carries.

The Cloud AI and Sovereign AI logos are height-constrained (`h-20 w-auto`). The
Private AI logo is **width**-constrained instead (`w-[90%] h-auto`), occupying
about 90% of its card: its source art is squatter than the other two (aspect
ratio ~2.16 against ~2.64 and ~3.32), so at an equal height it would render
noticeably narrower than its neighbours.

Card body copy:

- **Cloud AI** — "Requires an Ollama Cloud account to use cloud models. Signing
  up is free and includes a free allowance, which you can upgrade for more
  capacity." Picking this card goes straight to the internal-Ollama flow below —
  there is no mode sub-modal.
- **Sovereign AI** — "100 credits free when you register.", and nothing else.
- **Private AI** — describes the user-supplied OpenAI-compatible endpoint. Opens
  the Private AI dialog (see "## Private AI selected").

## Cloud AI selected

Cloud AI is always the internal Ollama server with the recommended cloud models.
Pointing Trustable at a user-supplied Ollama host is covered by the **Private
AI** card, so the splash asks no further questions:

1. `POST /api/configuration` with the merged config plus:
   - `provider: "ollama"`
   - `base_url: "http://localhost:11434/v1"`
   - `api_key: "dummy"`
   - `models` and `pi.default` seeded from `status.ollama.models` and `status.ollama.default` — see "Per-provider seeding" in [2a-config.md](2a-config.md)
   - `model_versions.ollama = status.ollama.modelsVersion`

   The dummy `api_key` will fail Ed25519 verification at the publish endpoints, which is the intended behavior for Ollama — see [6-publish.md](6-publish.md).
2. Hide the modal and run the **full** Configuration flow (connectivity check + model pull + say-OK test).

There is no splash path that persists an own-host Ollama configuration. Existing
workspaces saved that way before this change keep working — `configure.html`
still honours `?ollama=own` and the reselect exemption below still applies to
them — but new users reach the same outcome through **Private AI**.

## Trustable Cloud selected

1. Open a centered iframe overlay covering 80% of the viewport (width and height, centered on the page) loading the URL from `register_url` on the merged config (sourced from the `AIP_REGISTER_URL` env var; see [2a-config.md](2a-config.md)). The overlay has a "Cancel" button that closes the iframe and returns to the choice modal without saving anything.
2. Listen for `message` events from the iframe. Expected payload (matches what ai-proxy `success.html` posts today):

   ```json
   {
     "pi": { "default": "..." },
     "env":      { "OPENAI_BASE_URL": "...", "OPENAI_API_KEY": "aip_..." }
   }
   ```

3. On message: close the iframe and merge into the workspace config:
   - `provider: "trustable"`
   - `models` from `status.trustable.models` (per "Per-provider seeding" in [2a-config.md](2a-config.md))
   - `pi.default` from a non-empty iframe Pi payload if present, else from
     `status.trustable.default`; legacy `opencode` payloads and empty Pi values
     from older registration pages are ignored
   - `base_url` and `api_key` from the iframe payload's `env.OPENAI_BASE_URL` and `env.OPENAI_API_KEY`
   - `model_versions.trustable = status.trustable.modelsVersion`

   then `POST /api/configuration`.
4. Hide the modal and run the **trimmed** Configuration flow: skip the Ollama connectivity check and the model-pull step entirely (the backend handles this based on `provider`); only the say-OK test runs.

## Private AI selected

Opens the **Private AI endpoint** dialog, a sub-modal of the choice screen with:

- **Base URL** — text input, placeholder `https://host/v1`, required.
- **API key** — text input, optional, placeholder "optional".
- **Cancel** (returns to the Provider Choice modal) and **Save**.

### Validation on Save

Checked in this order:

1. **Base URL format — blocking.** The value must match
   `^https?://[^\s]+/v1/?$`. If it does not, refuse to save and show *"That does
   not look like a valid endpoint. The format is usually `http(s)://.../v1`"*.
   The user cannot proceed until the URL matches.
2. **HTTPS without an API key — confirmation, not blocking.** When the scheme is
   `https://` and the API key is empty, confirm with *"Are you sure there is no
   API key required? An `https://` endpoint usually requires one."* Cancel
   returns to the dialog with the values preserved; OK proceeds.

An `http://` endpoint with no API key saves with no confirmation — that is the
normal local case, matching the card's "no token required" subtitle.

### On save

Model discovery happens **in the dialog**, so a broken endpoint is never
persisted:

1. `POST /api/discover-models` with `{base_url, api_key}`. On error, show the
   message inline in the dialog and keep it open.
2. Build `models` as `{name: {maxToken: 131072, maxOutput: 32768}}` for each
   discovered model. An endpoint that returns no models is an error: show *"The
   endpoint returned no models."* inline and keep the dialog open.
3. Set `pi.default` to `""`. The default model is **not** guessed — see
   "No default model is chosen" below.
4. `POST /api/configuration` with the merged config plus `provider: "private"`,
   `base_url` as entered, `api_key` as entered or `"dummy"` when left empty (Pi
   requires a non-empty value to consider the provider configured — see
   [pi.md](pi.md)), and the `models` / empty `pi.default` above. The legacy
   `opencode` / `model_version` / `env` fields are dropped as in the other flows.
5. Alert *"Connected. N models found — select the default model to finish the
   setup."*, then navigate to `configure.html?setup=1`.

### No default model is chosen

A user-supplied endpoint carries no catalog metadata saying which of its models
is suitable for coding, so picking the first discovered one would silently select
an embedding, rerank, or tiny model and fail later in a confusing place. The
splash therefore persists an empty `pi.default` and hands off to the configure
screen, where `?setup=1` already shows the banner *"Choose the Pi model to
complete the runtime setup, then click **Save & Configure**"*.

The splash **Configuration flow does not run on this turn** — with no
`pi.default` there is nothing to probe. This matches own-host Ollama, and it is
also what the existing splash guard does for any provider whose `pi.default` is
absent, so a page reload lands on the same screen.

`configure.html` blocks **Save & Configure** while no model is selected, showing
*"Please select the default model before saving."* Without that guard the save
reaches the probe and surfaces the internal `pi.default not defined` diagnostic.

# Configuration

Every time the index page is opened (after a provider has been chosen), invoke the configuration api.
Expect a streamed answer and show the messages with a modal while it is configuring.

When `provider == "trustable"` the backend skips the Ollama connectivity check and model-pull steps and streams a single `OK: Skipping Ollama setup (Trustable Cloud)` line — see [2a-config.md](2a-config.md).

The same applies when `provider == "private"`: the backend skips the Ollama connectivity check and model-pull and streams `OK: Skipping Ollama setup (Private AI)`. A user-supplied OpenAI-compatible endpoint must not go through the Ollama connectivity check and model-pull loop. Its model list comes from `POST /api/discover-models` (run in the Private AI dialog), not the status catalog.

Then invoke the OpenAI-compatible API using `base_url`, `api_key`, and
`pi.default` from `trustable.json`, prompting `Reply with exactly: OK` and
requiring the reply to contain `ok` (see "GET /api/testmodel" in
[2a-config.md](2a-config.md)).

If the connectivity probe returns an authentication error **and** `provider == "ollama"`, show a sign-in required popup. This applies both to the structured `testmodel.auth_required` result returned while saving configuration and to the `AUTH_REQUIRED:` marker emitted by the streamed `/api/configure` Pi gate. The backend treats common sign-in messages (`not logged in`, `unauthorized`, `401`, etc.) **and Ollama's `internal service error`** as auth failures — they all route through this same flow:

- Dev startup does not pre-run an external `ops trustable signin` / Docker-based Ollama signin helper. The sign-in flow belongs to Trustable itself and starts only after the app detects an Ollama auth failure.

- The backend executes `ollama signin` as a subprocess with `HOME=$WORKSPACE_DIR` and `OLLAMA_HOST=$OLLAMA_ENDPOINT`, then scrapes its output for the first URL starting with `https://ollama.com/connect`. The current page's query string is **not** forwarded — the URL returned by `ollama signin` is used verbatim. In the Trustable pod this writes the Ollama Cloud identity into `/home/trustable/workspace`, the same persistent home used by the pod-local `ollama serve` process.

- If a URL is found, show **"Click here to login to Ollama Cloud"** as a link pointing to that URL with `target="_blank"` (opens in a new tab) and a Retry button. Retry reruns the streamed Pi gate, rather than only the model probe, so a successful sign-in also writes Pi's global models, authentication reference, and default selection.

- If `ollama signin` produces no recognizable URL, show **"You are not logged in to Ollama Cloud. Please execute `ollama signin` in your terminal and click Retry."**

Repeat until the test succeeded.

When `provider == "trustable"` and `/api/testmodel` returns an error, run the **Trustable sign-in recovery**:

1. Re-open the registration iframe overlay (the same one used by the initial Trustable Cloud selection) pointing at `register_url`.
2. Wait for the iframe's `postMessage` payload containing `base_url`, `api_key`,
   and optionally `pi: { default }`.
3. On message, merge the new `base_url` and `api_key` into the workspace config, `POST /api/configuration`, then **re-run the health check** (`/api/testmodel`).
4. If the user closes the overlay with **Cancel**, show a generic error with a Retry button and stop the loop until the user clicks Retry (which re-opens the iframe).

Repeat until the test succeeds.

If the user cancels the recovery flow (closes the signin modal without retrying, or clicks **Cancel** in the Trustable iframe), or the test still fails after recovery, the splash **returns to the Provider Choice modal** rather than proceeding to `applist.html`. A failed health check always sends the user back to provider selection — the app is unusable without a working model.

Once configuration completes successfully, navigate to `applist.html`.
