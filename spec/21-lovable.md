# Lovable project import

Issue #246. "Lovable project repo" in Add App ([1-applist.md](1-applist.md))
clones a Lovable app and converts it so it runs natively in Trustant.

Lovable apps are **TanStack Start SSR** projects: a Lovable Vite preset, a
Nitro/Cloudflare build target, bun lockfiles, file routes under `src/routes/`,
and optional Supabase / Lovable Cloud bindings. If they are cloned as-is, the
SSR build doesn't match the managed static runtime and the app never appears.

## Split between Trustant and the agent

| Phase | Who | When |
|---|---|---|
| 0 detect + inventory | Trustant ([lovable.go](../lovable.go)) | `POST /api/repo` with `source: "lovable"` ([2-repo.md](2-repo.md)) |
| 1 build system, 1.2 registry pass | Trustant | same |
| 2 SPA shell | Trustant | same |
| 6 deterministic cleanup (deleted files, ignore files, `LOVABLE:BEGIN` blocks) | Trustant | same |
| 3 routing, 4 logic layer, 5 database/backend, 6 README + report, 7 deploy/validate | agent, through `template.md` | auto-run on first launch |

The agent part needs judgement and platform tools (action creation, service
wiring, `auth_setup`, redeploy), so it cannot be done in Go.

## Trustant phase (`convertLovableProject`)

1. **Detect**: look for `@lovable.dev/vite-tanstack-config` or `@tanstack/react-start` in
   `package.json`, `src/routes/__root.tsx`, or `.lovable/`. If none is present,
   the request fails with `errNotLovable` (422) and nothing is written.
2. **Inventory**: collects the routes (file, path, `component`, `head()` title
   and meta), Supabase files, migrations, server functions, and storage, realtime,
   RPC and auth usage, react-query usage, and the lockfile.
3. **package.json**, rewritten with the original key order kept:
   - renamed to the app name;
   - scripts become `dev`/`build`/`preview`/`typecheck`, keeping `lint`/`format`;
   - adds `"openserverless": {"devel": "npm run dev", "deploy": "npm run build"}`;
   - removes the TanStack/Lovable/Nitro/Cloudflare/vitest/testing-library dependencies;
   - adds the missing build dependencies (react, vite, plugin-react, tailwind, typescript, @types/node), plus `react-router-dom` when there is more than one page;
   - keeps `@supabase/supabase-js` and `@tanstack/react-query`, and flags them.
4. **Registry pass**: `npm view <pkg>@<range>` checks every range. A range
   that does not resolve, or a `latest` placeholder, is pinned to `^<latest>`.
   If npm is missing the pass is skipped, and that is flagged.
5. Writes `vite.config.ts` (plain Vite, `outDir: web`, `0.0.0.0:5173`,
   `strictPort`, `allowedHosts: true`, an `/api/my` proxy to `OPS_HOST`/`OPSDEV_HOST`).
   Sets `tsconfig.json` types to `vite/client` + `node` and removes the vitest
   entries (JSONC is accepted). Adds `web` to the eslint ignores.
6. Writes `index.html` with meta merged from the route `head()` definitions: the
   index page wins over the root, and `og:image`/`twitter:image` are never
   inherited from the root. Also writes `src/main.tsx` and a **placeholder
   `src/App.tsx`**. The app must build at this point, because launch runs
   `ops ide deploy` and aborts if the build fails.
7. Deletes `bun.lock*`, `bunfig.toml`, `package-lock.json` (npm regenerates it),
   `.lovable/`, the vitest configs, alternative vite configs, wrangler files,
   `src/server.ts`, `src/start.ts`, `src/test/` and the Lovable error modules.
   `src/routes/`, `src/router.tsx` and `src/routeTree.gen.ts` **stay**: the agent
   reads them in step 1 and then deletes them.
8. Appends to `.gitignore` (`packages/**/*.zip`, `__pycache__/`, `*.pyc`,
   `.ops/`) and `.prettierignore` (`web/`), and strips `LOVABLE:BEGIN…END`
   blocks from `AGENTS.md`/`CLAUDE.md`/`README.md`.
9. Writes `template.md` (front matter `name: lovable-conversion` + the embedded
   [lovable-conversion.md](../lovable-conversion.md)) and `LOVABLE_CONVERSION.md`
   (the inventory, what was applied, what was flagged, and a placeholder for the
   agent's phases).

The result is committed as *"Convert Lovable project for Trustant (phases 0-2)"*
and pushed into the bare workspace repository on the imported branch. The
GitHub origin stays the Lovable repository. Nothing is force-pushed, and history
is never rewritten, because Lovable syncs that branch.

## Agent phase (`template.md`)

There are six steps, separated by `---`:
1. Routing: single screen, or `HashRouter` with a 404, per-page `document.title`
   and context providers. Then delete the TanStack route files.
2. `src/lib/api.ts` (envelope unwrap, `ApiError`, graceful offline), replacing
   the Supabase call sites, the form semantics pass, and the auth store contract.
3. The `setup/database` action (idempotent, seed marker) and the flat `v1/*` CRUD
   actions on `ctx.POSTGRESQL`.
4. Auth through `auth_setup`/Redis opaque tokens, storage through S3, and stripping secrets.
5. Cleanup, README, and completing `LOVABLE_CONVERSION.md`.
6. Frontend gates, a single redeploy, the action checker, `ops ide setup`,
   the HTTP smoke matrix, and auth flows. Failures are reported with the exact
   command and error.

It runs automatically once: the app entry carries
`bootstrap: "lovable-conversion"`, launch passes `TRUSTANT_BOOTSTRAP_NOTEBOOK=1`
to TruACP and clears the field ([4-launch.md](4-launch.md)), and TruACP runs all
steps when the chat is ready ([notebook.md](notebook.md)).

## Pitfalls encoded

- Without the `openserverless` block the watcher serves a static build on
  `127.0.0.1:5173`, which is invisible from the workspace.
- Action responses arrive as `{ "body": … }`; `api.ts` must unwrap them.
- `HashRouter` deep links (`#/history`) work on a cold load with no server routes.
- `.env`/`.env.production` are never read, copied or regenerated by the
  conversion.
