# License and publishing authorization

Implemented by [license.go](../license.go). The issuing CLI, `trulicense`, lives
in the **trustant-installer** repo (`cmd/trulicense/`, run via `./trulicense.sh`)
— this repo only *verifies* licenses and never signs one. Replaces the ai-proxy
key check that used to live in `validate_key.go` (deleted; its spec was
`10-validate_key.md`).

# Why

Publishing used to be gated by verifying the proxy-issued `aip_` key against the
ai-proxy's public key, which required a network round trip and tied publishing to
the inference provider. A license decouples the two: it is an offline,
Ed25519-signed statement of *who* may publish and *to which OpenServerless
clusters*, verified against a public key embedded in the binary.

`api_key` keeps its other role — talking to the ai-proxy for inference and
credits — but has no bearing on publishing.

# License format

A compact two-part token, same shape family as `aip_`:

```
lic_<base64url(payload_json)>.<base64url(ed25519_sig_over_payload_bytes)>
```

Payload:

```json
{
  "v": 1,
  "sub": "customer name or email",
  "hosts": ["https://api.nuvolaris.io", "http://miniops.me"],
  "iat": "2026-08-04",
  "exp": "2027-08-04"
}
```

- `hosts` — the authoritative allow-list of apihosts. Entries are full apihost
  URLs **including the scheme**, matching the format of `OPS_APIHOST`.
- `exp` is optional. Absent means the license never expires. A license stays
  valid through the whole of its `exp` day and is invalid from the next day on.
  An unreadable `exp` invalidates the license rather than being ignored.
- `sub` / `iat` are informational and shown in the UI.

The signature covers the **exact payload bytes** as decoded from part 1, so
re-serialization can never change the verification result (`parseLicense`).

## Host matching

`normalizeAPIHost` reduces both sides to `scheme://host[:port]`: lowercased,
with path, query and any trailing slash stripped. `licenseAllowsHost` then
compares for **exact equality**.

- `https://api.nuvolaris.io` and `https://api.nuvolaris.io/` are the same host.
- `http://x` does **not** match `https://x` — the scheme is part of the identity.
- `https://x:8443` does not match `https://x` — the port is too.
- **No wildcards.** `https://example.com` covers only itself, never
  `https://a.example.com`. An entry containing `*` is rejected at signing time by
  the CLI and never matches on the server.
- An entry without an `http`/`https` scheme never matches (defensive: the CLI
  already rejects these at signing time).

# Storage

The token lives in the `license` field of the **workspace** config,
`$WORKSPACE_DIR/trustant.json`, so it survives rebuilds. Like `provider` and
`apps`, it is workspace-only: the base `./trustant.json` never carries it, and
`mergeConfigs` does not copy it.

`loadLicense` caches the parsed payload for the process lifetime;
`invalidateLicenseCache` is called whenever the license is set or deleted through
the API.

# The trusted public key

`master_key_pub` at the repo root is committed and embedded via `//go:embed`. It
is produced by `trulicense keygen` from the `MasterKey Trustant` item in the
1Password vault. The server never talks to 1Password: verification is fully
offline.

Replacing `master_key_pub` invalidates **every license already issued**, which is
why `trulicense keygen` refuses to overwrite a mismatched one without `-force`.

Because the CLI lives in another repo, the file is committed in **both**:
`trulicense keygen` writes the copy in trustant-installer, and this repo holds
the copy that gets embedded. Regenerating the key therefore means copying the new
`master_key_pub` here and rebuilding the binary — until that happens every newly
issued license is rejected. `trulicense.sh` compares the two copies whenever a
sibling `../trustable-app` checkout exists and warns loudly when they differ.

# APIs

Registered in [main.go](../main.go).

- `GET /api/license` →
  `{"present": bool, "sub": "...", "hosts": [...], "iat": "...", "exp": "...", "valid": bool, "reason": "..."}`.
  The raw token is **never** echoed back. When a stored license fails validation
  the descriptive fields are still returned, so the UI can say *which* license
  expired.
- `POST /api/license` `{"license": "lic_..."}` — validates signature and expiry
  **before** storing. On success writes `license` into the workspace config and
  returns the GET shape. On failure returns 400
  `{"error": "Invalid license: <reason>"}` and stores nothing.
- `DELETE /api/license` — removes it.

# Enforcement

Two gates, both in [license.go](../license.go), both returning **HTTP 402**.

## Feature flag: `ENABLE_LICENSE`

The whole mechanism is **off unless `ENABLE_LICENSE` is set to a non-empty,
non-false value** (see [0-preflight.md](0-preflight.md)). With it off:

- `requireValidLicense` and `requireLicensedHost` return `true` immediately,
  before loading or validating anything — git push and publishing are never
  blocked, whatever is or is not stored in the workspace config.
- `configure.html` keeps the License card hidden, so there is no UI for
  installing a license that would not be checked.

`/api/license` itself stays registered and functional in both states; only the
gates and the card are conditional. Everything below describes behaviour with
the flag **on**.

## 1. Valid-license gate — git push and publish

`requireValidLicense(w)` is called at the top of `handlePublishPush`,
`handlePublishForcePush` and `handlePublishRemote` (all in
[publish.go](../publish.go)). With no license stored, or one that fails
signature or expiry validation:

```json
{"error": "License required: <reason>"}
```

Once the license is valid, git push is unconditionally allowed — `hosts` is
never consulted for push.

## 2. Host gate — production deploy only

`requireLicensedHost(w, apihost)` is called in `handlePublishRemote` **after**
the production config is resolved (after the `needs_config` check) and
**before** `ops ide login --mode=production` runs, so nothing touches the target
cluster when the host is unlicensed. It matches `prod["OPS_APIHOST"]` against
the license `hosts`. On mismatch:

```json
{"error": "Host not licensed: <apihost> is not covered by your license"}
```

Order inside `handlePublishRemote`: `requireValidLicense` → config resolution /
`needs_config` → `requireLicensedHost` → env generation → login → deploy.

`/api/git/deploy` (development launch) is **not** gated: the license covers git
push and production deployment only.

## Local-host exemption — host gate only

`isLocalApihost` treats a fixed literal set of hostnames as always covered, so
publishing to the local mini cluster does not require listing it in every
license:

`miniops.me`, `localhost`, `127.0.0.1`, `::1`

Only the host part is compared, as a whole hostname — so any port and either
scheme are accepted, while `https://x.miniops.me` and
`https://miniops.me.evil.com` are **not** exempt.

The exemption is deliberately narrow: it skips **only** the host gate, never
`requireValidLicense`. A valid license is still required to publish to a local
host, and git push stays gated as always.

# Frontend contract

The error prefixes `License required` and `Host not licensed` are a **contract**
with [web/applist.html](../web/applist.html): `isLicenseError` keys off them, and
`showLicenseModal` picks which explanation to show. Changing the wording
server-side breaks the modal.

The modal offers a textarea that POSTs to `/api/license` and shows the purchase
path (info@nuvolaris.io / nuvolaris.io). For the host case it names the rejected
apihost and lists the licensed hosts.
[web/configure.html](../web/configure.html) carries a License card showing `sub`,
covered hosts and expiry, with controls to replace or remove the license.

# CLI: `trulicense`

Lives in the **trustant-installer** repo, not here — issuing is a release
operation, and keeping it out means this repo needs neither `op` nor the
1Password vault. Run it through the `trulicense.sh` wrapper at that repo's root,
which requires `go` (aborting if missing), installs a pinned, checksum-verified
`op` into `~/.local/bin`, checks `master_key_pub` against this repo's copy, then
execs `go run ./cmd/trulicense` with every argument passed through. All wrapper
diagnostics go to stderr, so redirecting stdout to a `.lic` file stays clean.

The signing key never touches the local disk in plaintext and issued licenses are
always archived. Both live in 1Password, vault `TrustantLicenses`, reached
through the `op` CLI with a service-account token. `.op.json` at the
trustant-installer root holds **only** the service token; it is gitignored and is
never a key store.

```
./trulicense.sh keygen                       bootstrap: op, service token, MasterKey, master_key_pub
./trulicense.sh                              interactive: asks email + hosts, prints and archives a license
./trulicense.sh -email a@b.c -hosts h1,h2    non-interactive, same effect
./trulicense.sh verify <token>               print payload + validity using master_key_pub
```

## `keygen` — bootstrap

Idempotent. In order:

1. **`op` on PATH** — otherwise exit non-zero; nothing else is attempted.
   `trulicense.sh` installs it into `~/.local/bin`, pinned and checksum-verified
   against the `OP_VERSION` / `OP_SHA_*` constants in that script. This repo no
   longer installs `op` at all, in the VM or the image, since nothing here uses
   it. The wrapper installs it the same way `setup.sh` installs the GitHub
   CLI.
2. **Service token** — read `{"service_token": "..."}` from `.op.json`, else
   `$OP_SERVICE_ACCOUNT_TOKEN` (the CI path), else prompt with terminal echo
   disabled. The prompt states that input is hidden and echoes the character
   count once the line is read, so a blank screen while pasting cannot be
   mistaken for a hang. The token must start with `ops_`; an empty or malformed one is
   rejected **before** `op` is invoked, because for such a token `op` reports
   only *"No accounts configured for use with 1Password CLI"* and offers to add
   an account — which would send the operator down a sign-in path they do not
   need. A token that passes the shape check is written to `.op.json` with mode
   `0600` **before** `op` is invoked, and is kept even when the vault check
   fails: re-pasting an ~850-character secret to retry is worse than leaving it
   on disk, and the error names the file to delete in order to enter a different
   one. `.op.json` is then the single source of `OP_SERVICE_ACCOUNT_TOKEN` for
   every `op` child process.

   When `op` answers the vault check with *"No accounts configured"* — which it
   does for any token it cannot decode, most often one truncated on paste — the
   CLI translates it, since the advice to add an account does not apply to a
   service account.

   A service account token is sufficient on its own to read and write the vault:
   no `op account add`, and no 1Password desktop app. `op` is therefore always
   run with **empty stdin** when no payload is piped, so it can never take over
   the operator's terminal with an interactive prompt, and `OP_ACCOUNT` is
   cleared from its environment so a stray account cannot shadow the token.
3. **`MasterKey Trustant`** — read the keypair if the item exists; otherwise
   generate a fresh Ed25519 pair and create an **`API Credential`** item with the
   base64 keys in `private_key` (concealed) and `public_key` (text).

   Items created before this category switch are `Password`-category and keep
   their secret in the primary password field, so `opItem.field` falls back to
   that field when `private_key` or `license` is absent. Existing vaults
   therefore need no migration.
4. **`master_key_pub`** — write the public key to the repo root, refusing to
   replace a mismatched one without `-force`.

The private key is never written to disk and never generated implicitly during
signing.

## Issuing

Prompts for the customer email (becomes `sub`) and the host list. Every host must
parse as an `http`/`https` URL with a hostname; an invalid one exits non-zero
**without emitting or archiving a token**. Hosts are normalized and deduplicated
before signing. `-email` / `-hosts` / `-exp` skip the prompts.

The private key is fetched from the vault at signing time and held in memory
only. The CLI then prints the token to stdout (summary on stderr, so
`./trulicense.sh ... > out.lic` stays clean) and archives it as an item named
`Trustant License: <email>`, category **`API Credential`**, with the token in a
concealed field named **`license`** and `email` / `hosts` as text fields.

The category matters: `Password` items are only valid with a primary password
field (`purpose: PASSWORD`), which would force the token to be stored a second
time under a misleading label. `API Credential` has no such requirement, so every
secret sits once, in a field named for what it is.

### Share link

After archiving, the CLI creates a 1Password share link for the item, restricted
to the customer's email and valid for **7 days**, and prints it on stderr.

1Password does **not** send any mail: `--emails` only restricts *who may open*
the link (the recipient verifies the address with a one-time code). Delivering it
is the operator's job.

Sharing is a delivery convenience, so a failure never fails the run — the token
is already printed and archived by then. The CLI reports the problem as a warning
and exits zero.

### Progressive suffix

An existing item is **never** overwritten. The CLI lists the vault and picks the
first free name in the sequence:

```
Trustant License: a@b.c        first issue
Trustant License: a@b.c 2      second
Trustant License: a@b.c 3      third
```

The suffix is a space plus the smallest integer ≥ 2 not already taken. Titles are
compared case-insensitively, so `A@B.C` and `a@b.c` share one sequence. Gaps left
by deletions are reused.

If the listing fails the CLI does not guess a suffix: it prints the token,
reports the failure and exits non-zero **without creating an item**. If the
create itself fails, the token is still printed and the exit is non-zero, so the
operator never assumes an unarchived license was recorded.

## Secret handling

- `OP_SERVICE_ACCOUNT_TOKEN` is passed through the child environment, never argv.
- Secrets (service token, private key, license body) go to `op` on **stdin**;
  item creation uses a JSON template, never assignment statements, so nothing
  sensitive lands in the process table or shell history.
- `op` stderr is surfaced verbatim on failure — vault-permission problems are the
  most likely error and must not be swallowed.

A 1Password **service account cannot create vaults**, so `TrustantLicenses` must
already exist and the token must be scoped to it with write access.

# Tests

[license_test.go](../license_test.go) covers sign/verify round trip, tampered
payload and signature, foreign signing key, expiry boundaries, host matching
(scheme, case, port, trailing slash, path, no-wildcard), the local-host
exemption and its lookalike rejections, the `/api/license` round trip, and the
402 responses from the publish handlers.

`cmd/trulicense/main_test.go`, in the trustant-installer repo, runs against an
in-memory fake `op`, covering the bootstrap, token persistence and mode, host
validation, the archived fields, the progressive suffix (including gap filling
and case folding), and the assertion that no secret ever appears in argv.
