Step 1 of 6 — Lovable conversion: routing (phase 3).

This app was imported from Lovable (TanStack Start SSR). Trustant has already converted the build system and the SPA shell; read `LOVABLE_CONVERSION.md` first — its Phase 0 inventory lists every route with its `component` and `head()` metadata, and what is still pending. `src/App.tsx` is a placeholder you now replace.

Convert TanStack file routes to a client-side SPA:
- Single screen (inventory `pages` is 1): no router. Move the route component to `src/pages/<page>.tsx` and render it from `src/App.tsx`.
- Multi-page: `HashRouter` + `<Routes>` from `react-router-dom` in `src/App.tsx`. Use logical paths only (`to="/history"`) — never `#/...` in router APIs and never root-relative `<a href="/...">` for internal navigation. Use `NavLink` for navigation, with `end` on the root entry, and add a `*` catch-all 404 page. Replace `@tanstack/react-router` `Link`/`useNavigate`/`useParams`/`useSearch` with their React Router equivalents.
- Per-page titles: `useEffect(() => { document.title = "…" }, [])` in each page component, using the route's `head()` title.
- State shared across routes (timers, stores, query clients): context providers placed above `<Routes>`.
- Delete `src/routes/`, `src/router.tsx` and `src/routeTree.gen.ts` when you are done.

Keep `src/components/ui/*`, `src/hooks/*`, `src/lib/utils.ts` and `src/styles.css` unchanged: the original design must stay intact. Run `npm run typecheck` and `npm run build` (output in `web/`) and fix every error before you finish. Do not run `vite`, `npm run dev`, `ops ide devel` or `ops ide deploy`: the Trustant watcher owns the dev server and deployment.

---

Step 2 of 6 — Lovable conversion: frontend logic layer (phase 4).

1. Delete Lovable-only modules that are still present (error reporting/capture/page helpers, route-tree tests).
2. Create a typed `src/lib/api.ts`:
   - relative `/api/my/<package>/<action>` URLs only (the browser keeps its origin);
   - unwrap the `{ "body": … }` envelope that generated action wrappers may add;
   - treat `{ "ok": false, "error": … }` as an application error (`ApiError`);
   - degrade gracefully when the backend is unreachable: a clean error state in the UI, never a crash.
3. Replace every Supabase call site listed in the inventory (`supabase_files`, `rpc_files`, `storage_files`, `realtime_files`):
   | Supabase call | Trustant equivalent |
   | --- | --- |
   | `from(t).select()` | `GET /api/my/v1/<resource>` |
   | `from(t).insert()` | `POST /api/my/v1/<resource>` |
   | `from(t).update()` | `PUT /api/my/v1/<resource>/<id>` |
   | `from(t).delete()` | `DELETE /api/my/v1/<resource>/<id>` |
   | `.rpc(fn, args)` | dedicated action `v1/<feature>` |
   | `storage.from(bucket)` | S3-backed action (step 4) |
   | realtime `.channel()` | polling (no websockets through the proxy) |
   | `.eq()`, `.order()`, `.limit()` | server-side query params or client-side filtering |
   Then remove `src/integrations/supabase*`, every `VITE_SUPABASE_*` reference and the `@supabase/supabase-js` dependency.
4. Form semantics pass: every control gets a stable `id` and `name`, every label uses `htmlFor`, and icon-only buttons and search inputs get `aria-label`. A placeholder is never a label.
5. If the app has auth: persist only the opaque token; keep an explicit auth loading state on full reload and revalidate through the `me` endpoint before rendering protected routes; login and register update the live auth store before navigating (never only `localStorage`).
6. Drop `@tanstack/react-query` from `package.json` only if no page uses it any more.

Run `npm run typecheck` and `npm run build` and fix every error. If the app has no database usage at all, say so; steps 3 and 4 then only confirm there is nothing to convert.

---

Step 3 of 6 — Lovable conversion: database and CRUD actions (phases 5a–5c).

Skip with a one-line note if the inventory has no Supabase files, migrations or server functions.

Service mapping: PostgreSQL via `ctx.POSTGRESQL` for relational data, Redis for sessions and cache, S3 for files, Milvus only for vectors. Never MongoDB; if MongoDB would be needed and is not configured, report `non configurato` and never substitute another service.

1. Schema and seeds → one idempotent **private** action `setup/database`, created with the platform action tool (`public: false`) and wired with the PostgreSQL service tool. Translate every `supabase/migrations/*.sql`:
   - `CREATE TABLE IF NOT EXISTS`, `ALTER TABLE … ADD COLUMN IF NOT EXISTS`, `CREATE INDEX IF NOT EXISTS` — every statement repeatable;
   - drop RLS references (`auth.uid()`, `auth.users`): identity now comes from the Redis session and is enforced in the public action modules;
   - enums → `CHECK` constraints or text validated in module code;
   - seeds use fixed ids and `INSERT … ON CONFLICT (id) DO NOTHING`, guarded by a durable marker (`CREATE TABLE IF NOT EXISTS app_seed (marker TEXT PRIMARY KEY)` + `INSERT … ('<app>-<resource>-v1') ON CONFLICT DO NOTHING`; insert the seed rows only when that reported rowcount 1);
   - every write commits; wrap in try/except with rollback. Never repair live state with `psql` or service-MCP writes.
2. Public CRUD actions in package `v1`, created with the platform action tools and wired with PostgreSQL right after creation:
   - flat, hyphenated names only: `v1/contacts`, `v1/employees-photo` (never `v1/auth/register`, `v1/contacts/list` or underscores);
   - one action per resource, branching on `__ow_method` inside the editable module `packages/<pkg>/<action>/<module>.py`; never edit the generated `__main__.py`;
   - merge `body` (dict or JSON string) with top-level fields (top-level wins), ignoring `__ow_*` and `POSTGRES_URL`;
   - item ids come from `__ow_path` suffixes (`123`, `/123`, `/contacts/123`), with the body `id` as fallback;
   - `conn = ctx.POSTGRESQL` as-is — never `psycopg2.connect` or `POSTGRES_URL`; parameterized SQL only; column names from whitelists; validate every field server-side;
   - commit after every write, rollback on error;
   - return `{ "ok": true, … }` / `{ "ok": false, "error": … }`.
3. Server functions (`createServerFn`, `*.server.ts`) and edge functions become public `v1` actions; delete the originals.

Check syntax with `python3 -m compileall packages` and delete any `__pycache__` it leaves. Do not redeploy yet: step 6 redeploys once.

---

Step 4 of 6 — Lovable conversion: auth, storage, secrets (phases 5d–5f).

Auth (only if the inventory lists `auth_files`):
1. Create the complete endpoint set first: `v1/register`, `v1/login`, `v1/me`, `v1/logout`, plus every protected-resource action.
2. Then call the platform `auth_setup` tool **once** with the complete token-issuing, protected/session and logout sets. Use `action_add_redis` only for individual non-auth endpoints that need Redis.
3. Sessions: a cryptographically random opaque token at login/registration; token→identity stored in Redis with a bounded TTL, keys built from `ctx.REDIS_PREFIX` + an app suffix; validated/refreshed on every protected request; deleted on logout. No JWT, no application signing secret.
4. Never accept a browser-supplied user id as authentication. Every RLS policy like `auth.uid() = user_id` becomes `WHERE user_id = %s` with the session user.
5. Registration logs in immediately with the same session contract as login.

Storage (only if the inventory lists `storage_files`): add S3 wiring with the platform S3 tool (`ctx.S3_CLIENT`, `ctx.S3_DATA` private, `ctx.S3_WEB` public); never call `list_buckets()`; private uploads/downloads go through actions; preload objects in a private `setup/upload` action only when missing or changed.

Secrets: strip Supabase URLs and anon/service keys from all source. Never read, write or regenerate `.env` / `.env.production`. Never bind `OPS_USER`, `OPS_PASSWORD`, `OPS_APIHOST`, `OPS_REPO`, `OPS_SKILLS` into actions. Actions never call sibling actions through `OPS_APIHOST`.

---

Step 5 of 6 — Lovable conversion: cleanup and report (phase 6).

- Remove any remaining SSR/Lovable artifacts from the Phase 0 list and any `__pycache__`.
- Run the project formatter (prettier, if present) and then `npm run lint`; fix what lint reports.
- Rewrite `README.md` for the workspace: architecture (Vite SPA + OpenServerless actions + PostgreSQL/Redis/S3 as used) and the valid endpoints only.
- Complete `LOVABLE_CONVERSION.md`: replace the "Agent phases (3–7)" placeholder with the changes applied per phase, everything flagged for manual attention (Lovable features with no OpenServerless equivalent go here — never fail silently), and leave a validation table for step 6.
- Commit normally. Never force-push or rewrite history: Lovable syncs this branch.

---

Step 6 of 6 — Lovable conversion: deploy and validate (phase 7).

The Trustant watcher is the sole owner of packaging, deployment and the dev server. Never run `ops ide deploy`, `ops action` workflows, `vite`, `npm run dev` or another `ops ide devel`, and never kill managed processes. In this order:

1. Frontend gates: `npm run typecheck`, `npm run build` (output in `web/`), `npm run lint`, and the React MCP validator (`react_validate`) — zero findings before touching the runtime.
2. Redeploy exactly once with the platform redeploy tool if steps 3–4 created actions or wiring (new action files are `add` events the watcher ignores). Until it succeeds, everything below stays blocked.
3. Watcher evidence: confirm a fresh watcher and Vite bound to `0.0.0.0:5173`.
4. Action checker, once, unmasked: `timeout 60 check_openserverless_actions.sh .` — no `|| true`, no `head`/`tail`.
5. If any `setup/*` action exists: `timeout 120 ops ide setup` must succeed.
6. HTTP smoke matrix on `http://localhost:5173`: `/` returns 200 with the shell markers (title, root div, entry script) and an asset returns 200; for each resource: GET list → POST create → read back → `PUT /api/my/v1/<resource>/<id>` (id in the URL) → read back → `DELETE /api/my/v1/<resource>/<id>` → confirm absence. `relation "…" does not exist` means setup has not run: go back to 5.
7. Auth flows, if any: root redirect, login, register, a protected route while logged out, protected navigation after login (no manual reload), logout.

Fill the validation table in `LOVABLE_CONVERSION.md` with each check and its result, and commit. Never hide a failing check: report blockers with the exact command and error.
