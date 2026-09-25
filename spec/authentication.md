# Trustant local administrator authentication

## Scope

Trustant authentication is disabled unless `TRUSTANT_AUTH_MODE=local` is
set. Disabled mode preserves the existing UI, routes and API behaviour.

Local mode protects first-party pages and APIs served by the Trustant host on
port `8910`. It is independent from:

- generated application authentication;
- OpenServerless application users and `ops ide login`;
- model-provider and Ollama credentials;
- per-application `.env` files.

This phase does not protect the Kubernetes ingresses that route
`opencode.<domain>` and `vite.<domain>` directly to ports `4096` and `5173`.
Those hosts require a later host-specific session bootstrap; a parent-domain
cookie must not be used.

See [trustant-auth-flow.svg](trustant-auth-flow.svg).

## Configuration

Local mode requires three explicit values. Each value may be supplied directly
or through the corresponding `_FILE` variable. `_FILE` takes precedence.

| Value | Direct environment | File environment |
| --- | --- | --- |
| Username | `TRUSTANT_AUTH_USERNAME` | `TRUSTANT_AUTH_USERNAME_FILE` |
| Password hash | `TRUSTANT_AUTH_PASSWORD_HASH` | `TRUSTANT_AUTH_PASSWORD_HASH_FILE` |
| Session key | `TRUSTANT_AUTH_SESSION_KEY` | `TRUSTANT_AUTH_SESSION_KEY_FILE` |

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

`web/trustant-auth.js` wraps first-party `fetch` calls and adds
`X-Trustant-CSRF` to mutating and effectful requests. The server requires both
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

## Development-mode stream

Authentication must be a deployment choice, not an implicit dependency of an
image or Git branch. A development deployment must be able to run with
`TRUSTANT_AUTH_MODE=disabled` without a reachable Keycloak, OIDC discovery,
OIDC Secret, or auth-specific volume. Switching between `main`, `devel`, and a
local image must preserve the explicitly selected mode and must not leave an
incompatible mode from a previous image active.

Implementation phases:

1. Add an installer/deployment setting with the explicit values `disabled`,
   `local`, and `oidc`; development installs default to `disabled` and
   production does not silently fall back from `oidc` after an OIDC error.
2. Render only the environment variables, Secret mounts, and volumes required
   by the selected mode. Changing to `disabled` removes stale OIDC deployment
   configuration but does not delete the operator-owned Kubernetes Secret.
3. Expose the same setting in the Trustant configuration UI after the
   deployment API can apply it safely. Enabling `oidc` requires validated
   Keycloak configuration before rollout; disabling it requires explicit user
   confirmation because it changes the access boundary.
4. Add rollout E2E coverage for `disabled -> oidc -> disabled`, including an
   unavailable Keycloak, branch/image changes, and verification that no auth
   value reaches generated application `.env` files.

The current local cluster demonstrated the required compatibility behavior:
the public `main` image predates OIDC and failed to start while the StatefulSet
still supplied `TRUSTANT_AUTH_MODE=oidc`. Setting the deployment explicitly to
`disabled` restored the server without changing the image or requiring
Keycloak. The final implementation must make this transition a supported,
idempotent product operation rather than a manual `kubectl set env` command.
