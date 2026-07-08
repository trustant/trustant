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
- Add or change API routes in `main.go` plus the matching feature file.
- The frontend is plain HTML/Tailwind in `web/`; there is no frontend build step.
- Browser tests must use FQDN-style hosts such as `trustable.<domain>`, `opencode.<domain>`, and `vite.<domain>`.
- Do not treat all `localhost` or all `miniops.me` references as equivalent; classify them as browser-visible, configured apihost, internal service, or sidecar/local checks.
- `$WORKSPACE_DIR/workspace/<name>` is the durable bare repo. `$WORKBENCH_DIR/<name>` is the active checkout for the running app.
- `opencode.md` is embedded guidance for assistants inside user-created apps, not guidance for editing this repo.

## Git

The worktree may contain user changes or submodule pointer changes. Do not revert unrelated files. If a submodule changes, commit inside that submodule first, then commit the pointer update in this repo.
Never push the `olaris` subrepo unless the user explicitly authorizes that push in the current conversation.
