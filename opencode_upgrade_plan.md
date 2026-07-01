# OpenCode upgrade plan

Date: 2026-07-01

Related but separate plan: `improvement_plan_issue98.md`

## Goal

Evaluate upgrading Trustable's pinned OpenCode version from `1.16.2` to the
latest verified candidate, currently `1.17.12`, without mixing the version bump
into the issue-98 guardrail PR.

The upgrade looks useful, but it is not expected to be fully resolving by
itself. The issue-98 contract/checker/environment-recognition work remains the
primary fix for OpenServerless workflow drift.

## Current and candidate versions

- Current Trustable image ARG: `OPENCODE_VERSION=1.16.2`.
- Latest upstream/npm checked on 2026-07-01: `1.17.12`.
- `opencode-ai` npm: `1.17.12`.
- `@opencode-ai/plugin` npm: `1.17.12`.

Sources:

- https://opencode.ai/changelog
- https://github.com/anomalyco/opencode/releases
- https://www.npmjs.com/package/opencode-ai

## Potentially useful improvements since 1.16.2

Recent OpenCode releases include changes that look relevant to Trustable:

- sessions can recover once from provider context-overflow errors;
- large v2 tool outputs are bounded and expose retained output paths for
  follow-up inspection;
- MCP catalogs paginate instead of truncating larger lists;
- MCP servers can receive the current workspace as a client root;
- MCP server instructions are added to session context;
- MCP resource template listing and resource read tools were added;
- structured MCP/tool errors are surfaced more clearly;
- remote skills can be refreshed;
- skill resource paths are preserved;
- plugin client requests reuse the active server instead of assuming a default
  local port;
- plugin-provided shell environment variables apply to PTY sessions;
- ACP shell tool calls show command and working directory from the start.

These are strong reasons to test the upgrade. They do not remove the need for:

- `.openserverless-contract.md`;
- `scripts/check_openserverless_actions.sh`;
- post-compaction recovery rules;
- environment recognition before runtime checks;
- local `localhost:5173` verification discipline;
- no-user-shell-delegation rules.

## Proposed scope

Make this a separate improvement PR after, or parallel to, the guardrail PR:

1. Bump `OPENCODE_VERSION` in the image build to `1.17.12`.
2. Ensure the installed `@opencode-ai/plugin` version still matches
   `/usr/local/bin/opencode --version`.
3. Rebuild the Trustable image.
4. Run launch/config/MCP tests.
5. Validate a live app workflow inside `trustable-0`.

## Validation checklist

Static/local:

```bash
go test ./...
git diff --check
```

Image/runtime:

```bash
opencode --version
npm ls -g @opencode-ai/plugin || true
```

Trustable launch:

1. launch an existing app workbench;
2. verify generated `opencode.json`;
3. verify `openserverless` MCP is available;
4. verify service MCP servers still appear when configured;
5. verify OpenCode serves on `localhost:4096`;
6. verify `ops ide devel` serves the app on `localhost:5173`.

App workflow:

1. create or edit a simple public action through the OpenServerless MCP tool;
2. run `ops ide deploy`;
3. verify through `curl http://localhost:5173/api/my/<package>/<action>`;
4. run redeploy from Trustable UI/API;
5. verify `localhost:5173` still responds;
6. optionally verify `vite.<domain>` only after deploy and only as an
   external/browser route check.

Regression watch:

- session creation via Trustable launch still works;
- `X-Opencode-Directory` session bootstrap still resolves the right project;
- OpenCode does not assume the wrong localhost/port;
- MCP tool names remain compatible with `opencode.md`;
- `.mcp.json` compatibility for Claude-format clients is unaffected;
- long tool output behavior does not hide the first actionable error.

## Decision gate

Merge the upgrade only if:

- launch, redeploy, MCP, and local verification pass;
- OpenCode remains stable in server mode;
- plugin installation remains version-aligned;
- no Trustable routing assumptions change;
- any behavior differences are reflected in specs where needed.
