# Shared service secrets

Implemented in [shared.go](../shared.go) and [web/js/shared.js](../web/js/shared.js).

## Why

Every app gets its own OpenServerless user, and `ops ide login` writes that
user's service credentials — postgres, redis, s3, milvus, mongodb — into the
single global `~/.ops/config.json`. The credentials are **regenerated on every
login**, so an app that needs another app's database cannot simply be told the
value once: it goes stale.

This feature lets one app **publish** selected secrets under stable names, and
lets other apps consume them without ever logging in as the producer.

## The five steps

1. The user edits an app's `.env.shared` through the **Share** button on the app
   list — a picker over that app's `~/.ops/config.json`.
2. Saving resolves the pointers and stores the values in the workspace pool,
   `predefined_env`, surfaced on the Configure page as **Shared Variables**.
   The same happens when an app carrying a `.env.shared` is installed, and
   before every launch.
3. Other apps pick the values up from that pool automatically, for a name they
   already declare.
4. The env editor's **Add from shared** button adds a name from the pool.
5. Production values are kept **per apihost** and a publish that needs a value
   the target host does not have is **refused**.

## `.env.shared` — a template of pointers

At the root of the producing app's workbench, in `.env` syntax:

```
<app>__POSTGRES_URL=<.postgres.url>
<app>__S3_KEY=<.s3.access.key>
```

Both halves are placeholders, written with literal angle brackets.

**`<app>` is the literal string, not the app's name.** The producing app is
implied by the repo the file lives in, so the name is never hard-coded and the
file survives a rename, a fork, or an install under a different name. On
resolution it is replaced by the app name **uppercased**: `APPSUITE__POSTGRES_URL`.

**`<.postgres.url>` is a dot-path into `~/.ops/config.json`**, leading dot, in
angle brackets. A value never appears here: the file is committed, exactly like
`.env.dist`, so a real secret would be published. `sharedPathOf` **rejects** a
value not in `<.path>` form rather than using it — a bare value in this file is
a leaked secret, not a pointer.

The double underscore separates the two halves. It is doubled so the producing
app stays machine-recoverable by splitting a resolved name on the **first**
occurrence, even when the app name itself contains an underscore.

Only leaves are shareable. An object node is a group of secrets, not a value,
and stringifying one would put a JSON blob into an env var.

### The app prefix is mandatory

Every variable an app shares must be named `<APPNAME>__<NAME>` once expanded.

**Why it is an error and not a convention.** Resolved values land in one flat,
workspace-wide pool. Two apps that both share `postgres.url` would both want
`POSTGRES_URL`, and one would silently lose — leaving a consumer reading one
app's database credentials while believing they were the other's. The prefix
makes the collision impossible by construction, and makes the pool
self-documenting: `APPSUITE__POSTGRES_URL` names its producer.

Enforcement is at two points, and the refusal is **total** — a submission
containing even one unprefixed name writes nothing. A partial save is exactly
what would put the colliding entry into the pool.

1. The picker flags an offending row as the user types and refuses Save before
   the request leaves the browser.
2. `handleSharedSave` refuses with **400** and
   `Shared variable "X" must start with "APPSUITE__"`. This is the real gate.

A `.env.shared` written by hand can carry an unprefixed name without passing the
save handler. `refreshSharedFrom` **skips** those with a log line rather than
failing — the app must stay installable — but they never enter the pool.

## The pool

`predefined_env` holds two kinds of entry: values the user typed on the
Configure page, and values resolved from apps' `.env.shared`. Ownership is
recoverable from the name (`sharedProducerOf`: a name is app-produced exactly
when its prefix matches an app that exists), so **nothing records it** — there is
no ownership map to keep in sync.

**The pool stores real secrets.** `trustable.json` under `$WORKSPACE_DIR` is
workspace-local and never committed, and this is what lets a consumer read a
value without logging in as the producer. It also makes the pool a **cache, not
a source of record**: `.env.shared` is the durable declaration.

Two folding rules, both in `foldIntoSharedPool`:

- an **app-produced** name is overwritten on refresh — it belongs to its
  producer, and a refresh that could not update it would serve a stale
  credential forever;
- a **hand-typed** name is kept — the palette is the user's.

Values are never logged, only names.

### When the pool is refreshed

- when the Share picker saves (`refreshSharedForApp`);
- when an app is installed (`handlePostRepo`), so another app can consume its
  values without waiting for it to be launched;
- **before every launch** (`refreshSharedPool`, in `handleLaunchGet` before the
  missing-variable gate). Credentials are regenerated on every `ops ide login`,
  so a pool refreshed only on edit would hand a consumer a stale secret and the
  failure would look like an application bug.

One `ops ide login` per producing app, not per variable, then a re-login as the
launching app so its own bindings are what the rest of the launch sees.

Refresh is **never fatal**: an unresolvable pointer, a missing service block or
a failed login leaves the previous pool value in place and logs the name.

## Consuming

This is the **import** side, and it has its own document:
[19-import.md](19-import.md). This section covers only how a consumed value
reaches `.env`; the declaration format, wildcard matching and the launch popup
live there.

App B resolves nothing. It declares `APPSUITE__POSTGRES_URL` among its own
variables — through **Add from shared**, or because its `.env.dist` imports it —
and `generateAppEnvFiles` fills any declared variable whose value is **empty**
from the pool.

An import need not name the pool entry literally: `.env.dist` may bind a
variable to a **shape**, `EXT_POSTGRESQLURL=*__POSTGRESDB`, and the user picks
which producer feeds it. That indirection is what lets a consumer keep its own
vocabulary instead of adopting the producer's.

Two rules make this safe:

- **empty-only**: a value the user typed always wins;
- **declared-only**: the pool is never a source of new variables. An app that
  does not name a pool variable never sees it.

This narrows, rather than drops, the rule in
[2a-config.md](2a-config.md) that the palette is never an input to
`generateAppEnvFiles`. `pendingImports` consults the pool for the same reason: a
name the pool can satisfy is not missing, and the launch gate must not block on a
variable that is about to be filled. Ordering matters — the refresh runs before
the gate.

**Add from shared** imports a variable **whole** — the name and the value it has
in the pool, app-produced entries included. After the import the app's row holds
exactly what the original variable holds; an imported row that showed only a name
reads as unset, which is not what the user picked.

The empty-only fill above still applies to every other row: a variable the app
declares but never got a value for is filled from the pool at launch, so a name
added by hand or by an app's `.env.dist` keeps tracking its producer's current
credentials. An imported row carries a value, so it is a snapshot — re-import it
to pick up a rotated credential.

A name the app already declares with a non-empty value is still not overwritten,
and a readonly row is still skipped.

## Production: one pool per apihost

Service credentials on `api.nuvolaris.io` have nothing to do with those on
`openserverless.dev` — same variable name, different cluster, different secret.
A single production pool would hand an app the wrong cluster's credentials, so
production values are keyed by host in `predefined_env_production`:

```json
"predefined_env_production": {
  "api.nuvolaris.io":   { "APPSUITE__POSTGRES_URL": "postgres://…" },
  "openserverless.dev": { "APPSUITE__POSTGRES_URL": "postgres://…" }
}
```

Keys are normalized by `sharedHostKey` — lowercased, scheme and trailing slash
stripped — so `https://api.nuvolaris.io/` and `api.nuvolaris.io` are one pool.
This is deliberately **not** license.go's `normalizeAPIHost`, which keeps the
scheme and rejects a schemeless host because a license names hosts exactly.

**Filled by the producer's own publish only.** After
`ops ide login --mode=production`, `resolveProductionShared` resolves the
published app's own `.env.shared` against that cluster and stores it under that
host. No other app is logged into: a production login is a real operation
against a real cluster, and sweeping every producer would log into clusters the
user never asked to touch.

A hand-typed host value is **kept**, not overwritten, by a later producer
publish — the same rule the development pool uses.

### The publish gate

`missingProductionShared` returns every production variable the app declares
that is app-produced by **another** app, has no value of its own, and is absent
from that host's pool. A non-empty list stops the publish at the existing
`needs_config` point, before anything touches the cluster:

```json
{"needs_config": true, "missing_shared": [
  {"name": "APPSUITE__POSTGRES_URL", "app": "appsuite", "host": "openserverless.dev"}
]}
```

An app never blocks on a variable it produces itself: its own publish resolves
it in the same request.

Two ways out, both surfaced by the frontend:

- publish the producing app to that host, which the message names;
- type the value in by hand for that host, in the Env editor's Production
  column. This is the escape hatch for a producer that lives on another
  installation, and it must exist or an app could become unpublishable through
  no fault of its own.

**Unlike development, this gate blocks.** A launch with a missing value costs a
broken dev server; a publish with one deploys an app pointed at nothing.

## Two rules that must not be skipped

### Serialization

Resolving runs `ops ide login` for another app against the single global
`~/.ops/config.json`. Doing that while an app's launch is mid-flight would hand
that launch the wrong service bindings, so every entry point holds
`lockRuntimeLifecycle`. Launch and publish already hold it; the `/api/shared`
endpoints and the `handlePostRepo` hook take it explicitly.

### The login starts from a clean file

`ops ide login` **merges** into `~/.ops/config.json` rather than replacing it, so
blocks written by a previous login for a different app survive and are
indistinguishable from this app's own.

This is not cosmetic. A path picked under a stale block resolves on the machine
that authored the `.env.shared` and resolves to **nothing** on a fresh
installation, where the only login that ran is that app's — a declaration that
works for its author and nobody else. The same file drives MCP generation and
`appServiceRuntimeEnv`, so a stale block is a wrong service binding.

`removeOpsConfig()` therefore runs before **every** `ops ide login` call site:

- `handleLaunchGet` ([launch.go](../launch.go))
- `handlePublishRemote` ([publish.go](../publish.go))
- `opsLoginForApp` ([shared.go](../shared.go))

A missing file is success. Cleaning only the share path would leave launch
repolluting the file for everything else that reads it.

**Consequence, stated plainly:** a failed login now leaves *no* config rather
than a stale one. That is the safer failure — nothing then reads a wrong
binding — but a failed restore leaves the file absent until the next login.

## Endpoints

All go through `expiredGuard` and validate the app name with `namePattern`.

| Route | Method | Purpose |
|---|---|---|
| `/api/shared/tree/<app>` | GET | Log in as the app, return `~/.ops/config.json` as a tree for the picker |
| `/api/shared/<app>` | GET | The app's declarations, in expanded form, so the picker can pre-check them |
| `/api/shared/<app>` | POST | `{"vars":[{"name","path"}]}` — validate, write `.env.shared`, commit it, resolve into the pool |
| `/api/shared` | GET | Every app-produced name with its producing app and path |

`POST` writes the **whole** declaration: a variable the user unchecked is
dropped. The picker therefore pre-loads the app's existing set, or saving one
new variable would silently discard the rest.

`.env.shared` is committed by `commitSharedEnv`, mirroring `commitEnvDist`:
scoped pathspec so the user's other dirty files are untouched, no-op when
nothing is staged, never pushes.

### The tree is not redacted

`opsConfigTree` returns values as they are, minus the `auth` block. The picker
exists precisely so the user can see and choose secrets, in a local file they
already own; the UI masks them on screen with a per-row reveal instead.

## Frontend

**Share** is on the app list, per app, right after **Env** — the pool it writes
into is workspace-wide, and the app.html modal is a glance at what the running
app got, not a second place to edit it. The dialog has **two tabs**: **Import**
(what this app takes from others, [19-import.md](19-import.md)) and **Export**
(what it publishes, this document). Each tab saves its own file, so an edit in
one is never an implicit commit of the other. One `SharedPicker` instance is mounted
and re-pointed by `openSharedPicker(name)` before opening, because the page
lists many apps.

The picker is a two-column modal: the config tree on the left (objects expand,
only leaves are selectable, values masked), the chosen variables on the right as
stacked rows — path above, editable name below.

**The app.html Env modal is read-only** (`EnvTable` gains a table-level
`readOnly` option): every cell renders as text, no Actions button, and `add`,
`save`, `importEnvText` and `applyPredefined` are inert. Editing belongs to the
app list's Env action.

**Configure** renames the palette card to **Shared Variables** and adds an
environment selector — Development, plus one entry per production host.
App-produced rows render read-only and masked, with a Show toggle; they still
emit hidden inputs, because the save path reads the DOM and a row without inputs
would be dropped from the set it posts. The production view never writes: it is
a different map, and posting its rows would replace the development palette.

**The rename is a label change on the wire.** The config key stays
`predefined_env` and the endpoint stays `/api/predefined-env`; renaming them
would break every existing installation's `trustable.json` for no user-visible
gain. `handlePostPredefinedEnv` carries app-produced keys over verbatim, so a
stale tab cannot drop or rewrite a derived value.
