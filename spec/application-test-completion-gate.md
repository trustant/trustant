# Application test completion gate

> **REMOVED — historical record only.** This entire gate was implemented by the
> OpenCode session-enforcement plugin (`trustable_completion_check` and its
> siblings). The plugin is gone and has no Pi replacement: Pi is reached through
> `pi-acp`, which passes no tool flags, so no tool call can be intercepted or
> blocked. Nothing below is enforced today. See "No tool-permission guardrail" in
> [pi.md](pi.md).
>
> What survives is advisory only: the `check_trustant_app.sh` /
> `check_openserverless_actions.sh` / `check_trustant_frontend.sh` scripts, which
> the managed `AGENTS.md` tells the agent to run but cannot compel it to.
>
> The description below is retained so the behaviour can be re-specified if a
> guardrail mechanism is reintroduced.

## Goal

`trustable_completion_check` must run application tests that already exist
before OpenCode can claim that a change is complete. The gate is automatic and
must never ask the application user to run shell commands.

The gate does not choose a testing framework for an application and does not
fail merely because a frontend-only application has no tests. It never installs
dependencies or invokes package runners that may download code. A created or
modified OpenServerless action is different: each action endpoint touched in the
current session requires one focused executable application test.

## Discovery

Discovery is deterministic, bounded, and ignores generated or dependency trees:
`.git`, `node_modules`, `vendor`, build output, coverage output, caches, and
OpenServerless ZIP files.

The gate recognizes:

- Go modules containing at least one `*_test.go` file and runs `go test ./...`
  from each module root;
- Python suites containing `test_*.py` or `*_test.py`; pytest is used only when
  the project explicitly declares pytest or a discovered test imports it,
  otherwise the standard-library unittest discovery runner is used;
- JavaScript/TypeScript packages containing test files and a non-placeholder
  `test` or `test:ci` package script. The declared script is run with `CI=1`.

Nested suites are de-duplicated. Discovery has a fixed directory-depth and
suite-count limit. A project that exceeds the limit fails with an actionable
message instead of silently skipping tests.

## Execution

Every discovered suite runs without stdin, with a fixed per-suite timeout, a
fixed total test budget, and bounded captured output. Tests run sequentially so
their output remains attributable to one suite. Test commands use only tools
and dependencies already present in the workbench or image. Go runs with module
downloads disabled and JavaScript runners execute through declared package
scripts in offline mode.

The completion report lists every suite as `PASS`, `FAIL`, or `SKIP`. `SKIP` is
allowed only when no executable test contract exists, such as test-looking JS
files without a declared package script. A discovered executable suite that
fails, times out, or lacks its declared runner is a blocking failure.

## Critical components and actions

The session guard records normalized action endpoints from mutating MCP calls
using `args.endpoint` and from source edits below
`packages/<package>/<action>/...`. Generated `__main__.py` files do not create
or satisfy test debt. Duplicate endpoint observations collapse to one persisted
entry and survive compaction or plugin restart.

Before completion, every touched endpoint must have at least one recognized test
file in either:

- `packages/<endpoint>/...`, alongside the action; or
- `tests/actions/<endpoint>/...`, in the dedicated application test tree.

Failure output must print the exact expected directories for every endpoint and
must explicitly warn against flattening endpoint separators into underscores.
For example, `v1/stackcheck` maps to `tests/actions/v1/stackcheck/`, not
`tests/actions/v1_stackcheck/`.

The test must belong to an executable suite discovered by this gate. A JS test
without a declared `test` or `test:ci` script does not satisfy the endpoint.
After coverage is established, the bounded runner executes the suite and its
failure remains blocking. A successful completion clears the persisted endpoint
list; a failed completion retains it.

Integrated Trustable Code does not force an internal completion-recovery turn
after a final answer. If the provider stops without text, the session shows a
concise visible status. Inspecting a checker file with a read-only
`cat ... | head` command is not classified as masked checker execution;
executing the checker and piping its result to `head` or `tail` remains
forbidden.

Security-sensitive frontend components such as authentication, authorization,
upload, payment, and persistence receive no framework-specific mandate when no
action endpoint was touched. Existing tests are still discovered and failures
remain blocking, but a frontend-only change does not cause a new global test
framework requirement.

## Completion behavior

Application tests run after git validation, Trustable contract checks, required
action deploy/setup, and any frontend typecheck and build. The gate uses the
project `typecheck` script when present; otherwise it runs the installed local
TypeScript compiler with `tsc -b --noEmit --incremental false` when
`tsconfig.json` exists. This catches undefined JSX symbols that a transpile-only
Vite build accepts without leaving `.tsbuildinfo` artifacts in the workbench.
Completion remains blocked until
all touched endpoints have focused tests and all executable discovered suites
pass. The gate runs at most once for the current source revision and at most
three times for one real user request. A repeated call does not rerun the suite;
the agent must continue the requested implementation or report the concrete
failure, never add placeholder tests merely to satisfy or reset the gate.

Legacy non-integrated OpenCode may still replace an unverified final response
with one bounded recovery turn. Integrated Trustable Code may request exactly
one internal continuation when source changed but the model never called
`trustable_completion_check`; a second final response is never intercepted for
the same request.
Fallback status text emitted directly by the plugin is English, consistent
with the rest of its control-plane messages; model-authored responses may still
use the user's language.

Every successful frontend source mutation creates verification debt, even for
feature work that did not begin as a bug report. The assistant should run
typecheck then build, then confirm the exact changed route through
`react_validate` and bounded HTTP checks against the managed development
server. Completion rests on typecheck, build, tests, and runtime diagnostics.

## Raw action shell commands

The plugin rejects shell execution of `ops action ...` and `wsk action ...`
before shell execution, independently of OpenCode permission pattern matching.
This includes read-looking operations such as `list` and runtime operations such
as `invoke`. Detection also applies when the command is wrapped by `timeout`,
`env`, `sudo`, or `command`, or follows a `cd ... &&` segment. OpenCode must use
the OpenServerless MCP for action mutation, inspection, and invocation, and
`ops ide deploy` / `ops ide setup` for lifecycle operations.

## Managed login lifecycle

Trustable launch already runs `ops ide login`, configures the application, and
starts the managed development server with the resulting environment. OpenCode
must not run `ops ide login` again during the session: doing so can change the
workspace credentials and bindings while the running server still uses its
previous launch environment.

The process guard rejects `ops ide login` before shell execution, including
commands prefixed by `timeout`, `env`, inline environment assignments, `sudo`,
or `command`, and login commands appearing in a concatenated shell segment.
The diagnostic states that Trustable already authenticated and configured the
application. The agent must not retry login or replace/restart the managed dev
server. `ops ide deploy` and `ops ide setup` remain allowed where their existing
action lifecycle guards require them.

See [application-test-completion-gate.svg](application-test-completion-gate.svg)
for the execution flow.
