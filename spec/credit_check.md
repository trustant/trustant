# Checking credits from a client

Client contract for the credit-gating endpoints exposed by the ai-proxy. Companion to [CREDITS.md](CREDITS.md) (which is the server-side spec). Offline publishing authorization is unrelated and lives in [14-license.md](14-license.md).

This document is implementation-language agnostic and aimed at SDK authors and integrators who already hold an API key (`aip_<id>.<sig>`) and want to:

1. read the user's current credit balance,
2. top the user up,
3. interpret the `402 out_of_credit` error returned by `/v1/*` when the gate fires.

## Endpoints

Two JSON endpoints, both authenticated with the bearer API key. Neither is gated — a user with zero credits can still call them (otherwise they could never recover).

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `GET`  | `/api/v2/credits` | Bearer API key | Read current balance. |
| `POST` | `/api/v2/top-up`  | Bearer API key | Add credits (machine-to-machine). |

Base URL is `PROXY_BASE_URL` as configured on the proxy — typically something like `https://ai.trustable.ai/v1`. The credits endpoints sit at the same origin (`https://ai.trustable.ai/api/v2/...`), **not** under `/v1`.

## Auth

Standard OpenAI-style bearer token:

```
Authorization: Bearer aip_<id>.<sig>
```

Failures:

- `401 invalid_api_key` — the key is unknown, malformed, or belongs to a soft-disabled (`active=0`) user.

The key is the same one the proxy issued at signup; clients typically already have it because they use it for `/v1/chat/completions` and friends.

## `GET /api/v2/credits`

### Request

```http
GET /api/v2/credits HTTP/1.1
Host: ai.trustable.ai
Authorization: Bearer aip_<id>.<sig>
```

No body, no query params.

### Response — `200 OK`

```json
{
  "credits":         900,
  "credit_total":    1000,
  "spent":           "5.000000",
  "credit_value":    "0.050000",
  "currency":        "€",
  "out_of_credit":   false
}
```

| Field | Type | Meaning |
|---|---|---|
| `credits` | integer or `null` | Whole credits the user can still spend (`available_credits`). `null` when the gate is disabled (`credit_value` is `0` or missing in `cost.json` server-side). |
| `credit_total` | integer | Lifetime credits granted (`SUM(top_ups.amount)`). Includes the signup `free_credit` grant. |
| `spent` | string | Decimal string with **exactly 6 decimal places**, in the catalog currency. No currency symbol. Parse as `Decimal`, never as `float`. |
| `credit_value` | string | Decimal string (6 places) — money per credit. Useful if you want to show "credits left ≈ €X". |
| `currency` | string | Single-rune symbol from `cost.json` (e.g. `"€"`, `"$"`). Display only — do not parse. |
| `out_of_credit` | boolean | `true` iff the gate would currently block a `/v1/*` request. Mirrors `users.out_of_credit`. |

### Error responses

| Status | `error.code` | When |
|---|---|---|
| `401` | `invalid_api_key` | Bad/unknown/disabled key. |
| `500` | `internal_error` | DB failure. Retry with backoff. |

Error body shape (shared across all credit endpoints):

```json
{ "error": { "code": "<code>", "message": "<human-readable>" } }
```

### Notes for clients

- The proxy returns `credits` **verbatim** from the stored column; it does not recompute on read. The value is updated by the accounting transaction that runs after each `/v1/*` call, so it can lag a single in-flight request (eventual consistency — see [CREDITS.md §4](CREDITS.md#4-accounting-integration)). Don't poll faster than ~1 Hz; you won't see anything new.
- `credits` can be `0` or **negative** when the user just crossed the threshold (the gate fires on the *next* request, not retroactively). Treat any non-positive value as "out of credit" and surface accordingly.
- `credits` can be `null` — that means the server has the gate disabled. In that case `out_of_credit` will be `false` and the user can spend without limit. UIs should show "—" or hide the credit widget entirely.
- Cache freely on the client; invalidate on any of: a successful top-up, a `402 out_of_credit` from `/v1/*`, or user-initiated refresh.

## `POST /api/v2/top-up`

Machine-to-machine top-up. The bearer key identifies the user; the resulting `top_ups` row is recorded with `actor='self'`.

> Humans typically use the web form at `/_register/top-up` (email + password) instead — see [CREDITS.md §8](CREDITS.md#8-web-ui). The JSON endpoint is for clients that already hold a key and want to top up programmatically.

### Request

```http
POST /api/v2/top-up HTTP/1.1
Host: ai.trustable.ai
Authorization: Bearer aip_<id>.<sig>
Content-Type: application/json

{ "amount": 1000 }
```

`amount` must be an integer count of **credits** (not money) and must match one of the values configured in `TOPUP_AMOUNTS` on the server (default `1000, 5000, 10000`). Anything else → `400 invalid_amount`.

There is no idempotency key in v1: a retried POST creates a second `top_ups` row. Clients that retry on network failure should either accept the possibility of a double top-up or check `credit_total` on `GET /api/v2/credits` before retrying.

### Response — `200 OK`

```json
{
  "status":         "ok",
  "credits":        1900,
  "credit_total":   2000,
  "out_of_credit":  false
}
```

| Field | Type | Meaning |
|---|---|---|
| `status` | string | Always `"ok"` on success. |
| `credits` | integer or `null` | New `available_credits` after the top-up. `null` when gate disabled. |
| `credit_total` | integer | New lifetime `SUM(top_ups.amount)` — i.e. previous total + `amount`. |
| `out_of_credit` | boolean | Almost always `false` on success (a top-up ≥ 1 credit will normally clear the flag). It can stay `true` only if the user spent more than the new top-up covers. |

### Error responses

| Status | `error.code` | When |
|---|---|---|
| `400` | `invalid_input` | Malformed JSON body. |
| `400` | `invalid_amount` | `amount` is not in the server's `TOPUP_AMOUNTS` whitelist. |
| `401` | `invalid_api_key` | Bad/unknown/disabled key. |
| `500` | `internal_error` | DB failure during the insert + recompute transaction. |

### Discovering allowed amounts

There is no JSON endpoint that returns `TOPUP_AMOUNTS` in v1. Two practical options:

1. Hard-code the same whitelist in your client and keep it in sync with the deployment.
2. Try a top-up and react to `400 invalid_amount` (the message echoes the requirement, but not the list).

If your client is a UI, prefer option 1 and render one button per allowed amount, mirroring the server-rendered form.

## Reacting to the gate from `/v1/*`

The gate runs on `/v1/*` calls (not on the credits endpoints themselves). When a user is `out_of_credit`, the proxy responds:

```http
HTTP/1.1 402 Payment Required
Content-Type: application/json

{ "error": { "code": "out_of_credit", "message": "out of credit; top up at /_register/top-up" } }
```

Recommended client behavior:

1. Stop the current request (no auto-retry — it will fail again).
2. Surface the message to the user, including a top-up affordance (link to `/_register/top-up`, or a button that POSTs to `/api/v2/top-up` if your app holds the key).
3. After a successful top-up, retry the original request once.

Note the status is **`402`**, not `401` or `429`. Treat it as "transient, user-actionable" rather than "fatal" — the same key will work again as soon as `out_of_credit` flips back to `0`.

## End-to-end example

```bash
KEY="aip_<id>.<sig>"
BASE="https://ai.trustable.ai"

# 1. Read balance.
curl -s -H "Authorization: Bearer $KEY" "$BASE/api/v2/credits"
# {"credits":0,"credit_total":100,"spent":"5.000000","credit_value":"0.050000","currency":"€","out_of_credit":true}

# 2. Try a /v1 call -> blocked.
curl -s -o /dev/null -w "%{http_code}\n" \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-oss-120b","messages":[{"role":"user","content":"hi"}]}' \
  "$BASE/v1/chat/completions"
# 402

# 3. Top up.
curl -s -H "Authorization: Bearer $KEY" \
     -H "Content-Type: application/json" \
     -d '{"amount":1000}' \
     "$BASE/api/v2/top-up"
# {"status":"ok","credits":900,"credit_total":1100,"out_of_credit":false}

# 4. Retry -> succeeds.
```

## Reference implementations

### Python

Requires `requests` (`pip install requests`).

```python
import requests
from decimal import Decimal

class OutOfCredit(Exception): pass

class CreditClient:
    def __init__(self, base_url: str, api_key: str):
        self.base = base_url.rstrip("/")
        self.h = {"Authorization": f"Bearer {api_key}"}

    def credits(self) -> dict:
        r = requests.get(f"{self.base}/api/v2/credits", headers=self.h, timeout=10)
        r.raise_for_status()
        body = r.json()
        # Normalize the decimal-as-string fields once at the boundary.
        body["spent"]        = Decimal(body["spent"])
        body["credit_value"] = Decimal(body["credit_value"])
        return body

    def top_up(self, amount: int) -> dict:
        r = requests.post(
            f"{self.base}/api/v2/top-up",
            headers={**self.h, "Content-Type": "application/json"},
            json={"amount": amount},
            timeout=10,
        )
        if r.status_code == 400 and r.json().get("error", {}).get("code") == "invalid_amount":
            raise ValueError(f"amount {amount} not in server TOPUP_AMOUNTS whitelist")
        r.raise_for_status()
        return r.json()

    def is_out_of_credit_response(self, resp: requests.Response) -> bool:
        if resp.status_code != 402:
            return False
        try:
            return resp.json().get("error", {}).get("code") == "out_of_credit"
        except ValueError:
            return False
```

### Node.js (built-in `fetch`, ≥ 18)

```js
class CreditClient {
  constructor(baseUrl, apiKey) {
    this.base = baseUrl.replace(/\/$/, '');
    this.headers = { Authorization: `Bearer ${apiKey}` };
  }

  async credits() {
    const r = await fetch(`${this.base}/api/v2/credits`, { headers: this.headers });
    if (!r.ok) throw new Error(`credits: ${r.status}`);
    return r.json(); // spent / credit_value remain strings — parse with a Decimal lib if needed.
  }

  async topUp(amount) {
    const r = await fetch(`${this.base}/api/v2/top-up`, {
      method: 'POST',
      headers: { ...this.headers, 'Content-Type': 'application/json' },
      body: JSON.stringify({ amount }),
    });
    const body = await r.json();
    if (r.status === 400 && body?.error?.code === 'invalid_amount') {
      throw new Error(`amount ${amount} not in server TOPUP_AMOUNTS whitelist`);
    }
    if (!r.ok) throw new Error(`top-up: ${r.status} ${body?.error?.code ?? ''}`);
    return body;
  }

  isOutOfCreditResponse(resp, body) {
    return resp.status === 402 && body?.error?.code === 'out_of_credit';
  }
}
```

### Go

```go
package credit

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
)

type Client struct {
    BaseURL string
    APIKey  string
    HTTP    *http.Client
}

type Credits struct {
    Credits     *int64 `json:"credits"`
    CreditTotal int64  `json:"credit_total"`
    Spent       string `json:"spent"`
    CreditValue string `json:"credit_value"`
    Currency    string `json:"currency"`
    OutOfCredit bool   `json:"out_of_credit"`
}

func (c *Client) Credits(ctx context.Context) (*Credits, error) {
    req, _ := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/api/v2/credits", nil)
    req.Header.Set("Authorization", "Bearer "+c.APIKey)
    resp, err := c.HTTP.Do(req)
    if err != nil { return nil, err }
    defer resp.Body.Close()
    if resp.StatusCode != 200 {
        return nil, fmt.Errorf("credits: %d", resp.StatusCode)
    }
    var out Credits
    return &out, json.NewDecoder(resp.Body).Decode(&out)
}

func (c *Client) TopUp(ctx context.Context, amount int64) (*Credits, error) {
    body, _ := json.Marshal(map[string]int64{"amount": amount})
    req, _ := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/api/v2/top-up", bytes.NewReader(body))
    req.Header.Set("Authorization", "Bearer "+c.APIKey)
    req.Header.Set("Content-Type", "application/json")
    resp, err := c.HTTP.Do(req)
    if err != nil { return nil, err }
    defer resp.Body.Close()
    if resp.StatusCode != 200 {
        return nil, fmt.Errorf("top-up: %d", resp.StatusCode)
    }
    var out Credits
    return &out, json.NewDecoder(resp.Body).Decode(&out)
}

// IsOutOfCredit reports whether the given /v1/* response is the gate firing.
func IsOutOfCredit(resp *http.Response) bool {
    if resp.StatusCode != 402 { return false }
    var body struct{ Error struct{ Code string } }
    _ = json.NewDecoder(resp.Body).Decode(&body)
    return body.Error.Code == "out_of_credit"
}
```

## Failure modes

| Symptom | Cause | Fix |
|---|---|---|
| `credits` is `null` | Server-side gate disabled (`credit_value` missing/zero in `cost.json`). | Treat as unlimited; hide the balance widget. Not a client bug. |
| `out_of_credit: true` immediately after a successful top-up | Stale read against an async accounting goroutine, or the user spent more than the top-up covers. | Re-`GET /api/v2/credits` after a short delay (~250ms); top up again if still true. |
| `400 invalid_amount` on top-up | `amount` not in server `TOPUP_AMOUNTS` whitelist. | Use one of the supported values; mirror the server config in your UI. |
| `401 invalid_api_key` on a key that worked yesterday | Admin soft-disabled the user (`active=0`) or rotated `SIGN_KEY`. | User must re-register / contact admin; the client cannot detect this offline. |
| `402 out_of_credit` keeps firing after top-up | Looking at `/v1/*` response cached by an intermediate proxy. | Don't cache `/v1/*` responses; retry the original request. |
| `spent` parses as `5` not `5.000000` | Client used `parseFloat` / `json.Number` and dropped trailing zeros. | Treat `spent` and `credit_value` as opaque decimal strings; parse with a `Decimal` type, never `float`. |

## Out of scope (v1)

- No webhook / push notification when `out_of_credit` flips. Clients must poll `/api/v2/credits` or react to `402` from `/v1/*`.
- No idempotency key on `POST /api/v2/top-up`.
- No partial-credit / fractional-amount top-ups.
- No JSON endpoint for `TOPUP_AMOUNTS` discovery.
- No per-model breakdown of `spent` (it is a single scalar across all models).
