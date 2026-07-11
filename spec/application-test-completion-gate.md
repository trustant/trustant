# Application test completion gate

## Goal

`trustable_completion_check` must run application tests that already exist
before OpenCode can claim that a change is complete. The gate is automatic and
must never ask the application user to run shell commands.

The gate does not choose a testing framework for an application and does not
fail merely because an application has no tests. It never installs dependencies
or invokes package runners that may download code.

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

Action code under `packages/` and security-sensitive application components
such as authentication, authorization, upload, payment, and persistence receive
no framework-specific mandate. When they already have tests, those tests must be
discovered and pass; the gate must not downgrade their failure to a warning.

When no tests exist, the gate reports that fact without inventing a framework.
OpenCode's mandatory guidance still requires it to add focused regression tests
when it changes critical behavior and the application's existing test structure
provides a compatible place for them.

## Completion behavior

Application tests run after git validation, Trustable contract checks, required
action deploy/setup, and any frontend build. Completion remains blocked until
all executable discovered suites pass. The normal repeated-failure circuit
breaker applies to stable test failures.

See [application-test-completion-gate.svg](application-test-completion-gate.svg)
for the execution flow.
