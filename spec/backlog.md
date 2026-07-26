# Trustable development backlog

This file tracks approved improvements that are not implemented yet. Items in
this file are not current product behavior until their status is changed and
the matching feature specification is updated.

## Scheduled application activities

Status: backlog

Trustable Code must recognize application requirements that imply background or
periodic work, including settings such as "run every N minutes". It must not
implement only the settings UI or persist an interval without connecting it to
a real OpenServerless scheduled action.

The implementation must distinguish two cases:

1. A fixed development-time schedule is emitted in the generated action entry
   point as:

   ```python
   #--annotation cron "*/5 * * * *"
   ```

   For a multi-file Python action, the entry point is
   `packages/<package>/<action>/__main__.py`. The action is deployed only through
   `ops ide deploy`.
2. A schedule configurable by an application user requires a runtime design.
   Changing a stored setting does not rewrite or redeploy `__main__.py`.
   Implementation must either provide a supported scheduler-management API or
   run a fixed dispatcher schedule that reads persistent settings, determines
   whether work is due, and prevents duplicate concurrent execution.

Before implementation, extend the OpenServerless MCP action tool with a bounded
schedule/cron input. Trustable Code must not bypass the existing protection of
generated `__main__.py` files or create action ZIP files manually.

Completion requirements:

- include the scheduling design in the application's `spec.md` and execution
  plan;
- validate the five-field cron expression before deployment;
- deploy through `ops ide deploy` after creating or changing the schedule;
- inspect the deployed action and prove that its `cron` annotation matches the
  requested expression;
- for a short test interval, prove at least one scheduled activation and inspect
  its result or logs;
- add a completion-check failure when an application exposes periodic settings
  but has neither a scheduled action nor a supported dispatcher;
- add an E2E fixture covering fixed and runtime-configurable schedules.

## Disable the unused OpenCode cloud GitHub integration

Status: backlog

Trustable does not use the upstream `opencode github install` or
`opencode github run` automation, but the commands and their
`https://api.opencode.ai` token-exchange and installation-check endpoints are
still included in the Trustable Code source and compiled binary.

The managed personal GitHub account implemented for trustable-app issue #62 is
deliberately separate: it invokes the official `gh` CLI from the Trustable
backend with isolated persistent state and never calls `api.opencode.ai`.
Implementing #62 does not activate, retain, or justify the upstream OpenCode
cloud integration described in this backlog item.

Remove or explicitly disable this integration in the Trustable Code build so a
normal Trustable installation cannot contact the OpenCode cloud endpoint. This
must not remove Trustable's existing local repository operations such as
commit, pull, and push.

Completion requirements:

- remove the unused GitHub command registration from the shipped Trustable Code
  CLI, or protect it behind an explicit opt-in that is disabled by default;
- ensure `api.opencode.ai` is absent from the shipped binary unless an approved,
  configurable GitHub integration is deliberately enabled;
- keep Edit, chat, application generation, commit, pull, and push working;
- add a regression check proving that the default Trustable runtime makes no
  request to `api.opencode.ai`.
