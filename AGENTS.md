# AGENTS.md

Be brief. When changing code, update the matching spec under `spec/*.md`.

## Project

`trustable-app` is a Go single-binary web server for a Trustable/OpenServerless development environment. It serves `web/`, exposes `/api/*`, proxies OpenCode and Vite by hostname, and manages per-app workspace/workbench state.

## Commands

```bash
go test ./...
./run.sh
./build-server.sh
```

Use `./build-server.sh` on Linux servers. `./build.sh` is the compatibility entrypoint and delegates to the server build when the macOS Trustable VM files are absent.

## Conventions

- Everything is `package main`; keep feature code in the existing file-per-surface pattern.
- Specs are the source of truth when they disagree with code.
- Every behavioral change must add or update a nearby code comment explaining
  why the change exists, which runtime or compatibility constraint it protects,
  and why the less-obvious alternative was rejected. Do not add comments that
  merely restate syntax; tests, generated files, and mechanical renames are
  exempt when the rationale is already documented at the production boundary.
- Add or change API routes in `main.go` plus the matching feature file.
- The frontend is plain HTML/Tailwind in `web/`; there is no frontend build step.
- Browser tests must use FQDN-style hosts such as `trustable.<domain>`, `opencode.<domain>`, and `vite.<domain>`.
- Do not treat all `localhost` or all `miniops.me` references as equivalent; classify them as browser-visible, configured apihost, internal service, or sidecar/local checks.
- `$WORKSPACE_DIR/workspace/<name>` is the durable bare repo. `$WORKBENCH_DIR/<name>` is the active checkout for the running app.
- `opencode.md` is embedded guidance for assistants inside user-created apps, not guidance for editing this repo.
- Never add variables to generated app `.env` / `.env.production` files, app
  config env maps, or env-generation code unless the user explicitly authorizes
  that exact variable in the current conversation.

## Portability

- Changes to `setup.sh`, `run.sh`, `start.sh`, build/deploy scripts, and other
  system-facing components must be reproducible on a clean instance of their
  declared target environment. They do not need to support unrelated systems:
  for example, `setup.sh` and `run.sh` target the Ubuntu `trudev` Lima VM.
- Within that declared environment, never rely on the current developer
  machine's paths, cached tools, DNS state, credentials, unpublished Git
  objects, or other implicit local state. Detect only the target properties
  that are expected to vary, such as `amd64` versus `arm64`, and fail early with
  a clear prerequisite message when the declared environment is not present.
- Keep paths, hosts, ports, commands, dependency versions, and source refs
  configurable or derived from checked-in configuration when they vary within
  the target. Source commits needed by setup must be fetchable from the
  configured remote, not only from a developer worktree.
- Test system-facing changes in a clean instance of the declared environment.
  Cover multiple architectures only when the target explicitly supports them
  and the change is architecture-sensitive. Document the target environment in
  the matching specification.

## Git

The worktree may contain user changes or submodule pointer changes. Do not revert unrelated files. If a submodule changes, commit inside that submodule first, then commit the pointer update in this repo.

Never push any repository, branch, tag, submodule, or release artifact unless
the user explicitly authorizes that specific push in the current conversation.
Do not infer push authorization from a request to change, test, build, commit,
or prepare a pull request. Ask immediately before pushing when authorization is
not already explicit and unambiguous.

Never push the `olaris` subrepo unless the user explicitly authorizes that push in the current conversation.
