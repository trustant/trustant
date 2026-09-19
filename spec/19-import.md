# Importing variables from the shared pool

Implemented in [bind.go](../bind.go), [web/js/bind.js](../web/js/bind.js), and the
Import tab of the Share dialog in [web/js/shared.js](../web/js/shared.js).

## Export and import

The two halves of one story, and the UI uses these words everywhere:

- **Export** — the variables an app **publishes to other apps**. Declared in
  `.env.shared`, resolved from the app's own `~/.ops/config.json`.
  See [18-shared.md](18-shared.md).
- **Import** — the variables an app **consumes from other apps**. Declared in
  `.env.dist`, resolved by matching against the shared pool. This document.

Both live in one dialog with two tabs, opened by **Share** on the app list.

## Why

[18-shared.md](18-shared.md) lets an app publish secrets into the pool under
names like `APPSUITE__POSTGRES_URL`. Consuming them was *by exact name only*: the
consumer had to declare that literal string, so it was forced to adopt the
producer's vocabulary, and nothing recorded that the binding existed or let the
user point it somewhere else.

## `.env.dist` is a source file

It used to be **generated**: `writeEnvDistFile` rewrote it from the app's
configured variable names on every `generateAppEnvFiles` call, values blanked,
and committed it. That existed because the env editor was the only place
variables were declared, so the manifest had to track it or the two would drift.

It is now **written by the user**, in the Import tab, and the generator is gone.
A generator that rewrites a source file would erase the user's patterns on every
launch.

What replaced the drift protection is the **disjunction** below: the env editor
can no longer hold a variable without a value, so it has nothing left to
contribute to a manifest of *unresolved* names.

### Format

`.env.dist` keeps `.env` syntax. The value field, which the old parser discarded,
is the **matching pattern**:

```
DATABASE_PASSWORD=
EXT_POSTGRESQLURL=*__POSTGRESDB
MY_URL=APPSUITE__POSTGRES_URL
```

| Pattern | Meaning |
|---|---|
| empty | **exact**: the pool entry named `DATABASE_PASSWORD` |
| no `*` | **exact, renamed**: `MY_URL` takes the value of `APPSUITE__POSTGRES_URL` |
| contains `*` | **wildcard**: every pool entry matching the shape |

`*` matches any run of characters, including none, and is the only
metacharacter. A full glob or regex would turn a typo in a committed file into a
silent mismatch.

**Every `.env.dist` ever written is a file of empty values**, which is exactly
the exact-match case, so existing apps resolve as they always did. The one change
they see is that the file is no longer rewritten and committed under them.

Parsing keeps the **first** occurrence of a duplicate name: a later line would
silently reassign a variable declared above it, and the author of the first line
is not there to notice.

## The two editors are disjoint

| | `.env` (Env editor) | `.env.dist` (Import tab) |
|---|---|---|
| holds | variables **with a value** | variables **to be imported** |
| edited by | Env editor only | Import tab only |
| rejects | empty values | a name already valued in `.env` |

A variable is in exactly one of the two, and **migrates** from `.env.dist` to
`.env` when the launch popup resolves it.

Both refusals are **total**, matching the rule in [18-shared.md](18-shared.md): a
submission with one bad row writes nothing. A partial save is precisely what
would leave the two files overlapping, giving one variable two sources.

The frontend refuses first so the message is immediate; the server refusal is the
real gate.

## Resolution

`resolveImports` answers, for every variable the app has, what it will be set to.
Precedence, in `resolveOneImport`:

1. **A value typed by hand wins.** It is the escape hatch for a producer that is
   not installed on this machine, so it outranks the pool.
2. **A recorded choice tracks its producer.** The value is read from the pool on
   every resolve, so a rotated credential reaches the app rather than the copy
   frozen in the config. If the chosen entry has **left** the pool, the binding
   re-pends rather than falling back to another match — a silent fallback would
   point the app at a different database.
3. **An exact binding** reads the pool entry of its target name.
4. **A wildcard with exactly one match** resolves with no decision.
5. Anything else is **pending**: zero matches, or several with no choice.

### Where the choice is recorded

As a **reference in the variable's own value**, in the app config:

```json
"development": { "EXT_URL": "${{BILLING__POSTGRESDB}}" }
```

Not in `.env.dist`: that file is committed, and the choice is
**installation-specific** — the same repo on another machine has a different set
of producers installed, so exporting the choice would hand a clone a binding
naming a producer it does not have.

**The value is the binding.** There is no second entry recording which producer
was chosen, which means there is nothing to hide from `.env` generation, from the
env editor, or from a save that rebuilds the map from the posted rows. One
variable, one entry. A sidecar key would have been bookkeeping living in a map
that is also read as the app's variables, so it would have had to be filtered out
of all three, and carried over explicitly by `handlePostAppConfig` or every
wildcard import would unbind on the next save.

`${{...}}` rather than `${...}`: the single-brace form is shell syntax, and these
values are written into a `.env` file a shell may source.

A value is a reference only when the **whole** value is one — `importRefTarget`
rejects `prefix ${{X}} suffix` — so an app can still hold a literal string that
merely contains the delimiters.

Consequences that fall out of this, rather than being implemented separately:

- **A literal override just overwrites the reference.** Typing a value replaces
  the binding, because they occupy the same slot.
- **The reference never reaches `.env`.** `generateAppEnvFiles` resolves it and
  writes the value; the app receives a secret, never a `${{...}}` string.
- **The env editor shows the resolved value**, not the reference: the stored form
  is storage, and the row renders what the app will actually get.

## Launching resolves every variable

**On launch, every variable must end up with a value.** The popup guarantees it.

It replaces the old `missingAppEnvKeys` gate, which could only report names and
send the user off to fill them in by hand. Same gate, with a way to act on it.

`handleLaunchGet` resolves **after** `refreshSharedPool` — the pool must be
current before anything matches against it, or a variable about to be filled
would look unresolved. If anything is pending, the launch aborts **before**
`ops ide login`, so nothing is touched on the cluster, and returns the full
resolution:

```json
{"error": "Missing required environment variables",
 "missing_env": ["EXT_POSTGRESQLURL"],
 "imports": [ ... every variable, resolved or not ... ]}
```

The popup lists **all** of them, not only the pending ones, so the user sees the
whole picture and can re-point a binding that is already resolved:

| Row | Rendered as |
|---|---|
| already has a value | the value, masked |
| exact import | the value it takes from the pool, masked, naming its source |
| wildcard import | a **pull-down of the matching pool names** |
| import matching nothing | a **free-text field** |

The free-text field is not a nicety: without it an app whose producer is not
installed here could never be launched.

Confirming posts the answers, writes them into the app config, regenerates the
env files, and resumes the launch that was blocked.

## Re-opening

- the **Import tab** of the Share dialog, at any time;
- the **Env editor**, where an imported variable renders the pull-down instead of
  a text field. Editing the text would break the binding silently, so the
  pull-down is the only control offered. Choosing posts to `/api/imports/`
  immediately, because it writes the binding rather than just a value.

## Endpoints

Both go through `expiredGuard` and validate the app name with `namePattern`.

| Route | Method | Purpose |
|---|---|---|
| `/api/imports/<app>` | GET | declarations, their resolution, and the pool names |
| `/api/imports/<app>` | POST | `{"bindings":[...]}` rewrites `.env.dist`; `{"choices":{},"values":{}}` applies the popup's answers |

`POST` with `bindings` writes the **whole** declaration, like the Export picker: a
row the user removed must disappear, which a merge could not express. The Import
tab therefore pre-loads the existing set.

`.env.dist` is committed by `commitEnvDist` — unchanged, and for the reason it
always had: the manifest is the contract a clone reads. Scoped pathspec, no-op
when nothing is staged, never pushes.

## What was removed

- `writeEnvDistFile` / `appEnvVarNames` — the generator, and the `.env.dist`
  write inside `generateAppEnvFilesWith`.
- `seedMissingEnvKeys` — it seeded unresolved names into the config as blank rows
  so the env editor would show them. Blank rows are now illegal there, and
  unresolved names live in the Import tab.
- `missingAppEnvKeys` — replaced by `pendingImports`.
