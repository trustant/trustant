# `GET /api/v2/status` — response format

Public, unauthenticated endpoint that lets clients fetch:

1. A per-version operator message (e.g. "please update", "deprecated").
2. The catalog of model ids the proxy is willing to serve, grouped by provider, with their upstream context-window limits.

This document is the contract for client implementers. Companion to `spec/SPEC.md` §4.

## Request

```
GET /api/v2/status?version=<client-version>
```

- `version`: **the client should always send the version it is currently running** (its own release tag — `v0.3.4-beta`, `2026-05-08`, `stable`, whatever string identifies its build). The server uses it to pick the right operator message — e.g. "this version is up to date" vs "please update".
- Resolution order for `message`:
  1. Exact match in the config's `status` map → that value.
  2. Otherwise the reserved key `"default"` in the same map → that value.
  3. Otherwise the empty string `""`.
- No auth. Cache-Control is `no-store` (operators expect immediate effect on config reload).
- Only `GET`. Other methods return `405`.

## Response

`200 application/json`. **Shape is stable**; clients can rely on every documented field being present.

```json
{
  "message":   "<operator string for this version, or \"\">",
  "trustable": { "modelsVersion": <int>, "default": "<id>", "small": "<id>", "models": { ... } },
  "ollama":    { "modelsVersion": <int>, "default": "<id>", "small": "<id>", "models": { ... } }
}
```

### `message` (string)

- Looked up in the operator-controlled `status` map of `AIP_CONFIG`. Resolution: `status[version]` → else `status["default"]` → else `""`.
- **Always a string** — never `null`, never missing.
- The `"default"` key is the operator's catch-all for unknown / unreleased client versions. Typical use: `"default": "Unknown client version — please check for an update."` Operators can also leave it empty (`"default": ""`) to silence the unknown-version case while still messaging specific versions.
- Empty rather than 404 is deliberate: the proxy doesn't presume to know which versions exist. Clients have a single parse path.

### Provider blocks (`trustable`, `ollama`, …)

Each provider declared in `AIP_CONFIG` appears as a top-level key with the same shape:

```json
"<provider>": {
  "modelsVersion": <int>,
  "default":       "<model-id>",
  "small":         "<model-id>",
  "models": { "<model-id>": { "maxToken": N, "maxInput": N, "maxOutput": N }, ... }
}
```

#### `modelsVersion` (integer)

Opaque per-provider counter. The operator bumps it any time they edit that provider's `models`. Clients cache the model list per provider and invalidate when the value changes. Defaults to `0` when the operator hasn't set it.

#### `default` and `small` (string, required)

Two model ids picked by the operator for that provider:

- **`default`** — the model a client should use when the user hasn't picked one explicitly.
- **`small`** — the lightweight / cheaper model for low-stakes traffic (typing assistants, throwaway completions, ranking).

Both values are **always present** for every provider in the response (they are required in the config; the proxy refuses to start otherwise). Both point to ids that exist in the **same provider's** `models` map — clients can index `provider.models[provider.default]` to get its max-* hints in one lookup. The proxy validates this at startup and aborts on a dangling reference.

Operators bumping `default` or `small` should also bump `modelsVersion` for the same provider so clients invalidate their cached choice.

#### `models` (object)

Map of **model id** → per-model object. The id is what clients pass as `"model"` in `/v1/chat/completions` etc. Iteration order is **not** guaranteed (JSON object semantics); sort client-side if you need a stable order.

Each per-model object has three optional integer limits plus optional policy and
Pi reasoning-capability fields:

| Field | Meaning |
|---|---|
| `maxToken`  | Total tokens the upstream model accepts (prompt + completion + any system tokens). |
| `maxInput`  | Max tokens the upstream will accept in the prompt. |
| `maxOutput` | Max tokens the upstream will return. |
| `reasoning` | Whether the model supports Pi reasoning controls. |
| `thinkingLevelMap` | Optional Pi level map. `null` hides a level; `xhigh` is supported only when its entry is present and non-null. |
| `enabled` / `recommended` / `roles` / `reason` | Optional coding-agent selection policy consumed by Trustable. |

**All fields are optional.** Integer fields that are `0` in the config are
omitted from the response (via `omitempty`). A `trustable` model typically has
all three limits; an `ollama` (self-hosted) model often declares only
`maxInput`. Capability fields are declarations, not guesses: clients must not
infer `xhigh` from `reasoning: true`.

These are **hints**, not enforcement. The proxy doesn't reject oversized requests on the basis of these values — it forwards to upstream and lets upstream do the rejection. Clients use them to size their own requests (e.g. truncate before sending).

**Prices are not exposed.** Per-token input/output rates live in the same catalog server-side but are deliberately omitted from `/api/v2/status`. Use `/api/v2/credits` to see your spend.

## Examples

### Minimal (one model per provider, no per-version message)

```json
{
  "message": "",
  "trustable": {
    "modelsVersion": 1,
    "default": "gpt-oss-20b",
    "small":   "gpt-oss-20b",
    "models":  { "gpt-oss-20b": { "maxToken": 120000, "maxInput": 30000, "maxOutput": 90000 } }
  },
  "ollama": {
    "modelsVersion": 1,
    "default": "gpt-oss:20b-cloud",
    "small":   "gpt-oss:20b-cloud",
    "models":  { "gpt-oss:20b-cloud": { "maxInput": 128000 } }
  }
}
```

### Mixed providers

```json
{
  "message": "you are on the current version",
  "trustable": {
    "modelsVersion": 3,
    "default": "gpt-oss-120b",
    "small":   "gpt-oss-20b",
    "models": {
      "gpt-oss-20b":  { "maxToken": 120000, "maxInput": 30000, "maxOutput": 90000 },
      "gpt-oss-120b": { "maxToken": 120000, "maxInput": 30000, "maxOutput": 90000 }
    }
  },
  "ollama": {
    "modelsVersion": 1,
    "default": "qwen3-coder:480b-cloud",
    "small":   "gpt-oss:20b-cloud",
    "models": {
      "qwen3-coder:480b-cloud": { "maxInput": 256000 },
      "gpt-oss:20b-cloud":      { "maxInput": 128000 }
    }
  }
}
```

Note how the ollama models carry only `maxInput` — the other two fields default to `0` and are omitted.

### Operator-sent update prompt

```
GET /api/v2/status?version=v0.3.3-alpha
```

```json
{
  "message": "A new version has been released. Please update.",
  "trustable": { "modelsVersion": 1, "default": "...", "small": "...", "models": { ... } },
  "ollama":    { "modelsVersion": 1, "default": "...", "small": "...", "models": { ... } }
}
```

### Unknown version → falls back to `default`

Given an `AIP_CONFIG` with:

```json
"status": {
  "v0.3.3-alpha": "A new version has been released. Please update.",
  "v0.3.4-beta":  "you are on the current version",
  "default":      "Unknown client version — please check for an update."
}
```

A client identifying as `v0.2.0-old` sends:

```
GET /api/v2/status?version=v0.2.0-old
```

and gets:

```json
{ "message": "Unknown client version — please check for an update.", ... }
```

If `default` were absent or empty, `message` would be `""`.

## Provider naming

The set of provider names is whatever the operator declares in `AIP_CONFIG`. `trustable` and `ollama` are the names used in v1 — clients should treat the set as **open-ended**: new providers may appear in future config edits without an API version bump. Iterate over the top-level keys excluding `message` rather than hard-coding the two names.

A given model id appears under **exactly one** provider: the proxy rejects collisions at startup.

## Client recommendations

- **Caching**: cache the response keyed by `(provider, modelsVersion)`. Poll periodically; when `modelsVersion` for a provider changes, refresh that provider's model list, `default`, and `small` together.
- **Showing models in a UI**: don't display the *provider* to end-users unless that distinction matters to them (e.g. for routing or pricing tiers). The provider grouping is primarily a config / ops concern.
- **Picking a model**: prefer the user's previous choice if available; otherwise use the provider's `default`. For low-stakes background traffic (typing assistants, ranking, cheap probes) use `small` instead. Always cross-check the chosen id against `models` for the max-* hints before sending.

## Errors

The handler is infallible in v1 (in-memory map lookup + precomputed provider blocks). It always returns `200` with the shape above. Future error modes will be added explicitly to this document.

## Stability

This response shape is stable within `/api/v2`. Backwards-incompatible changes will land under `/api/v3` (or behind a versioned route) so clients can adopt at their own pace.
