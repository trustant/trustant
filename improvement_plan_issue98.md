# Improvement plan for issue 98

Source issue: https://github.com/trustable-ai/.github/issues/98

Date: 2026-07-01

## Goal

Prevent OpenCode from drifting away from the Trustable/OpenServerless workflow
after long sessions or compaction, especially when it touches database actions,
seed data, wrappers, deploys, and runtime verification.

The fix should not rely on a bigger context window alone. The durable fix is a
combination of:

- shorter critical instructions that survive or are reloaded after compaction;
- explicit DB/action contracts in generated app guidance;
- automatic checks that catch forbidden patterns before deploy;
- runtime verification paths that prove repo and deployed actions match.

Headroom/context compression and the OpenCode version upgrade are intentionally
out of scope for this issue-98 guardrail PR. Track them separately in
`headroom_experiment_plan.md` and `opencode_upgrade_plan.md` so the critical
OpenServerless workflow fix is not blocked by runtime/toolchain experiments.

## What issue 98 says

The incident in `verytamentor` was not one broken action. It was operational
drift:

- OpenCode lost or stopped applying the app workflow after long sessions and
  compaction.
- It created or changed actions outside the official tool/deploy path.
- Database code mixed wrapper logic, app logic, direct environment reads, and
  inconsistent PostgreSQL connection handling.
- Demo seed logic was not reliably idempotent.
- Request parsing and frontend response unwrapping were inconsistent across
  CLI invoke, public HTTP, JSON body, and form body.
- Runtime fixes risked diverging from source code.

High-priority guardrails from the issue:

- post-compaction recovery checklist;
- no raw action update/create deploy shortcuts and no hand-authored action zip
  deploy path; action zip files created by `ops ide deploy` are normal deploy
  artifacts and must not be treated as checker failures by themselves;
- thin wrappers only;
- `ctx.POSTGRESQL` for DB actions;
- idempotent seed markers;
- repeatable migrations;
- write/read verification through runtime and public HTTP where applicable;
- repo/runtime anti-drift checks.

## Current trustable-app state

The repo already contains a strong baseline:

- `opencode.md` is embedded into the binary and written into every app
  workbench as `<workbench>/<app>/opencode.md`.
- `opencode.json` is generated per app at launch and references that local
  `opencode.md` in `instructions`.
- The generated config includes provider/model defaults, `disabled_providers`,
  LSP settings, and MCP servers from `~/.ops/config.json`.
- `openserverless` MCP is always generated and is the official action tool
  path.
- Service MCP servers are documented as diagnostic aids, not a replacement for
  setup actions or public app paths.
- The embedded guidance already forbids editing generated `__main__.py`,
  creating backend servers, guessing `ops action` commands, hardcoding service
  secrets, using PostgreSQL MCP writes as normal app setup, and marking work
  complete without bounded validation.

## Lessons from `trudbtest`

The `trudbtest` session shows that the OpenServerless MCP tools are being used,
but their current output is not enough to prevent route/runtime assumptions.

Observed OpenCode tool usage in the session:

- `openserverless_action_new`: 7 calls;
- `openserverless_action_add_postgresql`: 7 calls;
- `openserverless_action_add_redis`: 4 calls;
- `openserverless_action_requirements`: 1 call.

The MCP tools successfully scaffolded actions and wired PostgreSQL/Redis. Their
outputs were mostly creation/wiring confirmations, for example endpoint paths,
generated file names, and available context variables such as `ctx.POSTGRESQL`.
They did not give a strong runtime contract for web action path parameters.

Concrete failure:

- the app spec expected REST routes such as
  `PUT /api/my/v1/contacts/{id}` and
  `DELETE /api/my/v1/contacts/{id}`;
- the generated backend tried to recover the id from `__ow_path` by assuming a
  path shape such as `/api/my/v1/contacts/123` or `/contacts/123`;
- in the OpenServerless web action runtime that assumption was not reliable;
- OpenCode fixed `PUT` only by adding `id` to the frontend request body;
- `DELETE` still failed because the frontend sent the id only in the URL.

This means the missing piece is not a generic frontend/backend skill. The model
can build the CRUD surface. The missing piece is a precise OpenServerless
runtime contract plus mandatory validation for route ids and CRUD symmetry.

Issue-98 guardrails should therefore add:

- explicit documentation that MCP action tools are the official creation/wiring
  path, but they do not replace reading the app contract;
- a standard route-id extraction rule/helper for web actions;
- required tests for both body-fallback and path-id behavior;
- a final CRUD matrix that checks create/list/update/delete for every resource
  before OpenCode declares the app finished.

## Lessons from `trutestdb2`

The `trutestdb2` session after the OpenCode upgrade shows a different class of
drift. OpenCode used the OpenServerless MCP tools correctly to create and wire
actions, but still made wrong assumptions about browser-facing HTTP semantics.

Observed good behavior:

- it created `v1/fattura` with `openserverless_action_new`;
- it added PostgreSQL wiring with `openserverless_action_add_postgresql`;
- it ran `check_openserverless_actions.sh` and `ops ide deploy` during the
  workflow.

Observed failures:

- for "stampa fattura", the frontend used
  `window.open("/api/my/v1/fattura/<id>?token=...")`, which expects a
  browser-renderable response;
- the action generated full HTML but returned it as app JSON:
  `{"ok": true, "html": "<!DOCTYPE html>..."}`;
- `curl -i` showed `Content-Type: application/json`, so a new browser window
  displays JSON instead of a printable page;
- OpenCode debugged authentication with invalid/system tokens before using a
  real app session token;
- it manually altered the live PostgreSQL schema with `psql` during debugging,
  then updated `setup/database` afterwards. That can leave runtime state ahead
  of source if setup/deploy validation is missed;
- final validation was not expressed as a response-mode matrix: JSON API,
  browser-opened printable HTML, setup migration, and app-session auth were not
  separately proven.

This indicates the missing guardrail is not simply "use the MCP". The MCP can
create the action correctly while the assistant still gets the HTTP response
contract wrong.

Issue-98 guardrails should therefore also add:

- a browser-opened/printable web action contract:
  direct `window.open`, links, or form targets need a browser-native response;
- explicit guidance that printable HTML must either return `text/html` with
  HTML in the HTTP body, or the frontend must fetch JSON with `Authorization`
  and write the extracted HTML into a new window before printing;
- checker warnings when a Python action contains full HTML but returns it as
  an `"html"` JSON field;
- checker warnings when frontend code directly opens `/api/my/...`, requiring
  `curl -i` proof of the expected content type;
- a validation matrix row for browser-opened/download/print endpoints:
  `curl -i`, status, content type, and body shape;
- explicit warning that `~/.ops/config.json` auth is not an app session token;
- a no-runtime-only-schema-drift rule: live `ALTER TABLE` during debugging must
  be followed by equivalent idempotent setup code, `ops ide setup`, deploy, and
  read-back proof before the task is complete.

Implemented in this refinement:

- `.openserverless-contract.md` now includes browser/printable response rules.
- Embedded `opencode.md` now includes browser-opened/printable action guidance.
- `check_openserverless_actions.sh` now warns on HTML returned as application
  JSON and direct `window.open("/api/my/...")` targets.
- Tests cover both checker warnings.
- Local pod rebuild validation used
  `ghcr.io/trustable-ai/trustable-app:local_issue98_webaction_26.183.0839`.
  `trutestdb2` was relaunched on the rebuilt pod; OpenCode and Vite responded
  on pod-local `localhost:4096` and `localhost:5173`, and the checker produced
  exactly the intended non-blocking warnings for `fattura.py` and
  `OrdersPage.tsx`.
- Follow-up environment drift found after the pod rebuild: OpenCode persists
  recent project paths, while `/home/trustable/workbench` was not on the
  persistent workspace volume. After a restart, recent projects could point to
  missing checkout paths such as `/home/trustable/workbench/truk8s`. The
  proposed product fix is to keep `~/workbench` backed by the persistent
  workspace volume and restore missing checkout directories from the durable
  bare repos at startup, while still doing full per-app login/deploy only in
  `/api/launch/<name>`.
- Correction from later testing: do not seed every restored checkout as an
  OpenCode project. That lets OpenCode open another Trustable app as a plain
  folder, bypassing `ops ide login`, generated env, deploy, app-local
  `opencode.json`, and MCP configuration. The OpenCode iframe must stay scoped
  to the app launched by Trustable; switching apps must go through
  `/api/launch/<app>`. The `opencode.<domain>` proxy should rewrite API
  requests carrying a non-current `directory` query back to the current app
  directory, and should scope `GET /project` to only the current app even if
  OpenCode's persistent DB still contains older projects.

Important measured sizes:

- `opencode.md`: 3,016 words, 20,854 bytes.
- `spec/opencode.md`: 3,370 words, 23,481 bytes.
- `spec/2a-config.md`: 5,013 words, 36,481 bytes.
- `spec/4-launch.md`: 2,340 words, 18,419 bytes.

The generated `opencode.json` itself is not likely the main context problem:
it references `opencode.md` by path instead of inlining it. Its size grows with
provider model entries and MCP service environment blocks, but the heavy stable
instruction payload is the markdown file.

## OpenCode context and config limits

Current implementation:

- `buildModelProvider` sets each model's `limit.context` from
  `trustable.json` model limits:
  - `maxToken` first;
  - else `maxInput`;
  - else fallback `32768`.
- `limit.output` comes from `maxOutput`, with fallback `32768`.
- generated model options set `maxTokens: 8192`, with variants:
  - `fast`: `2048`;
  - `deep`: `16000`.

OpenCode docs confirm that:

- `provider`, `model`, and `small_model` select models;
- provider options can set custom `baseURL` and `apiKey`;
- model `limit.context` is the maximum input tokens OpenCode uses to know how
  much context remains;
- model `limit.output` is the maximum generated output;
- `instructions` is an array of paths/globs to instruction files;
- `disabled_providers` prevents ambient providers from loading.

OpenCode docs do not currently expose a stable documented config knob for an
auto-compaction threshold. Treat compaction behavior as version-sensitive and
verify it against the pinned image version before depending on it.

Conclusion:

- Right-size `limit.context` so OpenCode has an honest budget.
- Do not advertise a huge context unless the selected provider and model really
  support it.
- Prefer instruction recovery and contract checks over hoping compaction keeps
  all operational details.

## Proposed PR scope

### Phase 1: instruction guardrail

Update embedded `opencode.md` and `spec/opencode.md`.

Add a very early section, before detailed workflow text:

```text
Post-compaction recovery

After compaction, do not continue editing from memory. Re-read this file,
inspect git status, inspect available MCP/tool names, and identify the official
repo workflow before touching actions, DB, setup, deploy, or service state.
If a previous tool is unavailable, stop and recover the official workflow.
Never invent manual zips or raw action create/update/deploy commands.
```

Strengthen DB/action rules:

- wrappers stay generated and thin;
- app modules use `ctx.POSTGRESQL`;
- modules must not read `POSTGRES_URL` as their normal connection path when
  wrapper wiring exists;
- every setup/seed action must be idempotent;
- demo seed requires a marker table or equivalent marker;
- migrations must be repeatable and ordered: schema, tables, columns, views,
  seed;
- view shape changes must use drop/recreate for derived views;
- web actions must parse top-level params, JSON body string/dict, form payloads
  where applicable, and `__ow_method`;
- web actions that expose REST-style item routes must not assume one fixed
  `__ow_path` shape. They must use a robust id extraction helper that supports
  body fallback and observed suffix forms such as `123`, `/123`,
  `/contacts/123`, and `/api/my/v1/contacts/123`;
- frontend code must normalize direct payloads and wrapped `{ body: ... }`
  payloads.

Acceptance:

- `opencode.md` first-screen text contains post-compaction recovery.
- `spec/opencode.md` mirrors every new rule.
- Existing guidance stays app-focused and does not become guidance for editing
  `trustable-app` itself.

### Phase 2: separate critical action contract

Decision: the critical action/DB recovery contract should be a separate file in
the generated app workbench, not only a section buried inside the longer
`opencode.md`.

For the first PR, use `.openserverless-contract.md` as that separate critical
file. Do not add a second `opencode-critical.md` yet unless we later find that
OpenCode needs an additional generic critical instruction file.

`opencode.md` remains the full reference and must explicitly tell OpenCode to
read `.openserverless-contract.md` before touching actions, DB, setup, seed,
deploy, or service state. The checker must also point back to this contract
when it detects drift.

Why:

- the critical action/DB rules stay small and easy to re-read after compaction;
- the long `opencode.md` can stay rich without hiding the operational protocol;
- checker failures can give OpenCode one concrete recovery file to read before
  editing again.

Implementation files:

- `configure.go` or launch/template code that writes the contract file;
- `main.go` embed block if the contract is embedded in the binary;
- `spec/2a-config.md`;
- `spec/4-launch.md`;
- `spec/opencode.md`;
- `configure_test.go`.

### Phase 3: discoverable action contract and static checks

Add a small, discoverable contract file inside each generated app workbench, plus
a lightweight checker that enforces the parts that can be checked
mechanically.

Proposed files:

- `.openserverless-contract.md`: short human-readable contract that OpenCode
  must read before touching actions, DB, setup, seed, deploy, or service state;
- `check_openserverless_actions.sh`: executable checker installed once in the
  Trustable user PATH and referenced by the contract and by `opencode.md`.

Update embedded `opencode.md` so OpenCode has an explicit recovery rule:

```text
Before touching OpenServerless actions or databases, look for
.openserverless-contract.md. If it exists, read it first and follow its checker
command. If the checker reports hard failures, fix them before deploy. If the
contract is missing, use the checklist in this file and report that the
contract file was not found.
```

Candidate checker command:

```bash
timeout 60 check_openserverless_actions.sh .
```

The contract should be intentionally small, probably under 150 lines. It should
contain only the operational rules OpenCode must recover quickly after
compaction:

- valid endpoint grammar is `action` or `package/action`;
- generated wrappers are not business logic files;
- service wiring is added with MCP/action tools;
- MCP action tools create and wire actions, but they are not the full runtime
  contract. Before implementing REST-style item routes, OpenCode must read the
  contract and use the documented path-id extraction pattern;
- DB modules use `ctx.POSTGRESQL`;
- setup/seed actions are idempotent;
- deploy uses `ops ide setup` / `ops ide deploy`;
- runtime proof requires write/read verification;
- the checker command is mandatory before deploy when present.

Checks:

- fail if `packages/**/__main__.py` has suspicious business logic edits beyond
  generated wrapper patterns;
- fail if generated wrappers are modified when only module files should be
  touched;
- warn or fail on `os.getenv("POSTGRES_URL")` in action business modules that
  already receive `ctx.POSTGRESQL`;
- fail on nested action endpoint directories like
  `packages/v1/auth/register`;
- do not fail merely because `ops ide deploy` created `.zip` action artifacts
  under source directories. Those files are normal deploy artifacts. The
  checker may ignore them, or at most emit a non-blocking hygiene warning if
  they appear to be committed source rather than deploy output;
- do not fail on standard generated wrapper service wiring. A normal
  `__main__.py` generated by action tools may read `POSTGRES_URL`, import
  `psycopg`, and assign `ctx.POSTGRESQL`; that is scaffolding, not business
  logic. Fail only when wrappers contain high-confidence drift such as SQL,
  backend servers, or hand-written business behavior;
- fail when code or docs instruct OpenCode to deploy hand-authored zip files or
  bypass `ops ide deploy` with raw action create/update shortcuts;
- warn when CRUD resource code has REST-style update/delete paths but no shared
  or obvious route-id extraction helper;
- check seed actions for a marker table/pattern when they insert demo data;
- check Python syntax without leaving `__pycache__` or `.pyc` files in the app
  repo.

Hard failure behavior:

- print file-specific errors with the rule violated and the expected fix;
- tell OpenCode to re-read `.openserverless-contract.md` before continuing;
- exit `1` for high-confidence problems;
- keep ambiguous heuristics as warnings during the first rollout.

High-confidence failures:

- invalid nested action paths;
- hand-authored zip deploy workflow or raw action create/update shortcuts;
- Python syntax failure;
- edited/generated wrapper files with obvious business logic beyond standard
  service wiring;
- missing checker execution when the contract requires it in a wrapper command.

Warnings at first:

- business modules reading `POSTGRES_URL`;
- seed logic without an obvious marker;
- migrations that update views without visible drop/recreate logic.
- generated `.zip` artifacts that are tracked by git or otherwise look like
  committed source hygiene problems, without blocking deploy verification.

Open questions before implementation:

- Decision: `.openserverless-contract.md` lives in every generated app repo; the
  checker is installed once in the Trustable user PATH and receives the app path
  as an argument. Do not duplicate the checker into every app repo.
- Should Trustable overwrite these files on launch, or preserve app-local
  customizations and only update when missing?
- Should the checker run automatically from the Trustable redeploy endpoint, or
  initially only be an OpenCode-required command?

### Phase 4: runtime anti-drift verification

Add a documented smoke-test protocol first, then automate where practical.

Be careful with host classification. OpenCode runs inside the Trustable pod and
`ops ide devel` exposes the app dev server inside that same pod on
`http://localhost:5173`. For app-level HTTP checks performed by OpenCode from
inside the pod, the default target should be the local dev server:

```bash
curl http://localhost:5173/api/my/<package>/<action>
```

Do not make OpenCode invent other IPs, pod names, service names, or public
domains for normal app verification. FQDN-style hosts such as
`vite.<domain>` are browser-visible/ingress checks. They should be used only
after `ops ide deploy` has succeeded and only when the task explicitly needs to
verify external routing or browser behavior.

Before any runtime smoke test, OpenCode should run an environment-recognition
preflight:

1. confirm current directory is the app workbench, normally
   `/home/trustable/workbench/<app>`;
2. read `opencode.md` and `.openserverless-contract.md` if present;
3. inspect `opencode.json` for generated MCP servers and model/provider shape;
4. inspect `.env` only for platform variables such as `OPS_APIHOST`, without
   copying secrets into code or logs;
5. identify the app shape from `src/`, `packages/`, `public/`, and setup
   actions;
6. confirm the local dev server is expected on `localhost:5173` and OpenCode on
   `localhost:4096`;
7. classify any concrete host before using it:
   - `localhost:5173`: explicit pod-local app dev server check;
   - `localhost:4096`: explicit pod-local OpenCode server check;
   - `trustable.<domain>`: browser-visible Trustable UI/API host;
   - `vite.<domain>`: browser-visible app host through Trustable proxy/ingress;
   - `opencode.<domain>`: browser-visible OpenCode host through proxy/ingress;
   - `OPS_APIHOST`: configured OpenServerless API host;
   - other raw IPs/hosts: suspicious unless justified by config or the user.

Minimum proof for DB write paths:

1. deploy from repo with `ops ide deploy`;
2. invoke setup/seed if changed with `ops ide setup`;
3. call the write action;
4. call the read action;
5. from inside OpenCode/the pod, verify browser-facing app endpoints through
   `http://localhost:5173/api/my/<package>/<action>`;
6. for CRUD resources, verify the full matrix for every resource:
   create/list/update/delete, including REST-style item routes such as
   `PUT /api/my/v1/<resource>/<id>` and
   `DELETE /api/my/v1/<resource>/<id>` without relying only on `id` in the
   request body;
7. only after a successful `ops ide deploy`, and only when external routing is
   in scope, repeat the check through the browser-visible `vite.<domain>` host;
8. verify the value read is the value written and the deleted value is no longer
   returned;
9. record the command, host classification, and result in the assistant
   summary.

OpenCode must execute shell checks itself when it has shell access. It should
not ask the user to run commands such as `ops ide deploy`, `curl`, `npm run
build`, `python3 -m compileall`, or `git diff --check` unless it truly lacks
the shell/tool permission or needs credentials/physical access only the user
has. Asking the user to run commands from inside the Trustable pod is normally
wrong: the user does not have that shell context, while OpenCode does.

Potential automation:

- add a generated `scripts/smoke_openserverless.sh` hook for common app shapes;
- add app-level examples but avoid fake endpoint names;
- expose a Trustable UI "Run backend smoke" later if enough patterns stabilize.

## OpenCode ingress scoping follow-up

The browser-visible `opencode.<domain>` host must not route directly to the
OpenCode service port `4096`. It must route to the Trustable app port `8910`,
where `middleware.go` can scope OpenCode requests to the current Trustable app
before proxying to the pod-local OpenCode server.

Failure mode observed with Playwright:

- Trustable launched `trutestdb2`, but the OpenCode project API exposed older
  projects such as `truk8s` because `opencode-ing` bypassed Trustable
  middleware and pointed directly at service port `4096`.
- When the browser path bypasses Trustable middleware, OpenCode can open another
  workbench as a plain folder, without that app's `ops ide login`, generated
  env, deploy, app-local `opencode.json`, and MCP context.
- After patching `opencode-ing` to service port `8910`, Playwright saw
  `/project` return only `trutestdb2`, and `/mcp` returned the expected
  connected servers (`openserverless`, `postgres`, `redis`, `s3`, `milvus`).

Host classification for this case:

- `localhost:4096`: pod-local OpenCode sidecar/API used by Trustable launch
  bootstrap;
- `opencode.<domain>`: browser-visible host that must enter Trustable
  middleware on port `8910`;
- `localhost:5173`: pod-local app dev server started by `ops ide devel`;
- `vite.<domain>`: browser-visible app host, used only after `ops ide deploy`
  when external ingress routing is in scope.

## Proposed commit structure

1. `docs: add issue 98 improvement plan`
2. `opencode: add post-compaction serverless guardrail`
3. `opencode: add db action contract checks`
4. `opencode: document runtime anti-drift smoke checks`

## Validation plan

For the immediate guardrail PR:

```bash
go test ./...
git diff --check
```

For generated config behavior:

```bash
go test ./... -run 'OpenCode|Opencode|MCP|Config'
```

For a live pod/app check:

```bash
ops ide deploy
ops action invoke v1/refresh-db --result
curl http://<app-host>/api/my/v1/<read-action>
```

## Decisions and open questions before implementation

- Decision: the first issue-98 PR should include embedded guidance plus the
  minimal discoverable contract/checker path, not guidance alone. Keep the first
  checker conservative: high-confidence failures only, ambiguous cases as
  warnings.
- Decision: the first PR should create/use `.openserverless-contract.md` as the
  separate critical file for action/DB workflow recovery. The checker should
  point OpenCode back to that file whenever it detects drift. Defer any
  additional generic `opencode-critical.md` until we see a separate need.
- Decision: `.openserverless-contract.md` should live in the same generated app
  repo so OpenCode can discover, commit, and review the critical workflow with
  the app code. The checker should be installed once by Trustable in the user
  PATH and run against the current app path, avoiding duplicated checker copies
  across apps. It should not live only in an external skills repo.
- Decision: keep the OpenCode version bump separate from the issue-98 guardrail
  PR. Track the upgrade from `OPENCODE_VERSION=1.16.2` to the latest candidate
  in `opencode_upgrade_plan.md`. The upgrade may help, but it is not a
  substitute for the `.openserverless-contract.md`, checker, and environment
  preflight work in this plan.
