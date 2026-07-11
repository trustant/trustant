# Trustable local administrator authentication

## Scope

Trustable authentication is disabled unless `TRUSTABLE_AUTH_MODE=local` is
set. Disabled mode preserves the existing UI, routes and API behaviour.

Local mode protects first-party pages and APIs served by the Trustable host on
port `8910`. It is independent from:

- generated application authentication;
- OpenServerless application users and `ops ide login`;
- model-provider and Ollama credentials;
- per-application `.env` files.

This phase does not protect the Kubernetes ingresses that route
`opencode.<domain>` and `vite.<domain>` directly to ports `4096` and `5173`.
Those hosts require a later host-specific session bootstrap; a parent-domain
cookie must not be used.

See [trustable-auth-flow.svg](trustable-auth-flow.svg).

## Configuration

Local mode requires three explicit values. Each value may be supplied directly
or through the corresponding `_FILE` variable. `_FILE` takes precedence.

| Value | Direct environment | File environment |
| --- | --- | --- |
| Username | `TRUSTABLE_AUTH_USERNAME` | `TRUSTABLE_AUTH_USERNAME_FILE` |
| Password hash | `TRUSTABLE_AUTH_PASSWORD_HASH` | `TRUSTABLE_AUTH_PASSWORD_HASH_FILE` |
| Session key | `TRUSTABLE_AUTH_SESSION_KEY` | `TRUSTABLE_AUTH_SESSION_KEY_FILE` |

The password hash format is:

```text
pbkdf2-sha256$<iterations>$<base64-salt>$<base64-derived-key>
```

Iterations must be between `100000` and `2000000`; generated hashes should use
`210000` or more. The salt must contain 16-1024 bytes and the derived key 32-64
bytes. The session key must contain at least 32 bytes. A session key prefixed
with `base64:` is decoded before use.

Missing or invalid local-mode configuration is a startup error. Credentials
must come from an operator-managed Secret mount or explicit process environment
and must never be copied into workspace configuration, generated applications,
app environment maps or `ops ide login` configuration.

## HTTP contract

Public resources in local mode are limited to the login page and its static
assets, `POST /api/auth/login`, `GET /api/version`, and `GET /api/status`.
Unauthenticated page requests redirect to `/login.html?next=<path>`. Protected
API requests return HTTP `401` JSON.

Authentication endpoints:

- `POST /api/auth/login` accepts `{ "username": "...", "password": "..." }`;
- `GET /api/auth/session` returns the username, expiry and CSRF token;
- `POST /api/auth/logout` invalidates the server session and clears cookies.

Login requires a same-origin `Origin` header and is rate limited by the
transport peer address. Forwarded client-address headers are not trusted unless
a future deployment explicitly configures trusted proxies.
The response creates an opaque, signed, host-only cookie with `HttpOnly`,
`SameSite=Strict`, `Path=/`, and `Secure` whenever the external request uses
HTTPS. Sessions are stored only in memory, expire after eight hours, are
bounded to 256 entries, and are invalidated by a server restart.

`web/trustable-auth.js` wraps first-party `fetch` calls and adds
`X-Trustable-CSRF` to mutating and effectful requests. The server requires both
the session CSRF value and same-origin request evidence (`Origin`, or `Referer`
when browsers omit `Origin` on a same-origin GET) for:

- every method other than `GET`, `HEAD`, and `OPTIONS`;
- `GET /api/launch/<app>`;
- `GET /api/configure`;
- `GET /api/ollama-connect`;
- `GET /api/redeploy`.

Login is the only exception because no session exists yet; it still requires a
same-origin `Origin`. API clients must obtain a session and send the CSRF header
explicitly.

## Security properties and limits

- Session identifiers are random and authenticated with HMAC-SHA256 using the
  configured session key.
- Password verification uses PBKDF2-HMAC-SHA256 and constant-time comparison.
- Failed login state is bounded and old entries are removed automatically.
- Successful authentication rotates to a new session and clears the rate-limit
  bucket for that client.
- Logout invalidates server state, so a copied cookie cannot be reused.
- Authentication does not provide authorization roles; local mode is a single
  administrator boundary.
- HTTP deployments cannot provide confidentiality. Production exposure should
  terminate HTTPS so the `Secure` cookie form is used.

## Tests

Unit tests cover disabled compatibility, configuration loading, password hash
validation, login throttling, cookie flags, bounded/expiring sessions, page/API
authentication, CSRF on normal mutations and effectful GET routes, and logout.
