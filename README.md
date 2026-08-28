# Trustable TL;DR

Welcome to Trustable source code

How to run it from sources:

## Before you start (every system)

The first run asks for a **GitHub token** — it clones private sources. Create one at
[github.com/settings/tokens](https://github.com/settings/tokens) with the `repo` and
`read:org` scopes, then either paste it at the prompt or save it in `.ghtoken` at the
repo root (git-ignored). Without it the run stops immediately.

`.env` needs no attention: it is seeded from `.env.dist` for you.

## Mac

You need a Mac (Apple Silicon or Intel) with at least 16GB of memory and 60GB disk space

- install [Lima](https://lima-vm.io/) with `brew install lima`
- clone sources and start the vm

```
git clone https://github.com/trustable-ai/trustable-app --recurse-submodules
cd trustable-app
./start.sh
```

`./start.sh` provisions the VM and then starts the dev loop, which prints the URL to open.

You can also

- open vscode on the folder in the vm with `./start.sh -v`
- stop the vm with `./start.sh -s`
- kill and remove the vm with `./start.sh -k`

## Windows

You need a Windows machine (Intel or ARM) with at least 16GB of memory and 60GB disk space

You need WSL 2: `wsl --version` must answer. If it does not, run `wsl --update` from an
Administrator terminal. The Ubuntu distribution itself is created for you.

- execute from PowerShell `.\start.ps1`

It creates the distribution, runs the same provisioning as Linux inside it, and then
starts the dev loop, which prints the URL to open.

You can also

- open vscode on the folder in the distro with `.\start.ps1 -v`
- stop the distro with `.\start.ps1 -s`
- kill and remove the distro with `.\start.ps1 -k`

## Linux

You need a Linux VM with Ubuntu 24.04 Intel or ARM with at least 16GB of memory

Create a user with passwordless sudo rights and execute:

```
git clone https://github.com/trustable-ai/trustable-app
cd trustable-app
./start.sh
```



## Update sources

It is recommended your first enter in the development enviroment with:

`./start.sh -v` or `./start.ps1 -v` 

then switch to main and execute

```
git swith main
git pull origin main --recurse-submodules
./setup.sh
```

then you can execute `./run.sh` 

# Trustable Introduction

## What it does

`trustable-app` lets a user **create, edit, run, and publish** web applications backed by OpenServerless, with TruACP and Pi wired into each app. The single `:8910` server:

- **Serves the local UI** (`web/`) — splash, app list, per-app editor, and config pages
- **Reverse-proxies** the user's running app (Vite on `:5173`) and the TruACP/Pi assistant (`:4096`), so all three surfaces share one origin
- **Manages per-app state** as **bare git repos** (durable workspaces) and **active checkouts** (working workbenches)
- **Drives `ops` CLI subprocesses** for login, deploy, and teardown of each app on the k3s VM
- **Handles configuration and billing** — provider/model selection, credits/top-up, and signature-gated publishing

The production binary embeds `web/`, `_build.txt`, and `opencode.md`, so a shipped release is a **single self-contained executable** with no external asset dependencies at runtime.

## Architecture at a glance

### One Go package, file-per-feature

Everything lives in `package main`. Rather than splitting into many packages, the codebase uses **one file per feature surface**, and each `*.go` file maps 1:1 to a spec doc under [spec/](spec/). The spec describes intended behavior; the Go file implements it. **When a spec and a `.go` file disagree, the spec is the source of truth** — specs are written and iterated on *first*, then the code follows.

| File | Spec | Responsibility |
|---|---|---|
| [main.go](main.go) | — | Embeds assets, registers `/api/*` routes, starts `:8910` behind `hostnameMiddleware` |
| [preflight.go](preflight.go) | [0-preflight.md](spec/0-preflight.md) | Loads `.env`, frees ports 8910/5173/4096, checks the ssh key, runs the workspace config migration |
| [middleware.go](middleware.go) | [0-preflight.md](spec/0-preflight.md) | Host-based routing — one port serves three apps by hostname prefix |
| [repo.go](repo.go) | [2-repo.md](spec/2-repo.md) | `/api/repo`, `/api/upload`, `/api/git*` — per-app bare repos |
| [configure.go](configure.go) | [2a-config.md](spec/2a-config.md) | Two-layer config, `/api/configuration`, `/api/configure`, `/api/testmodel`, `/api/appconfig/` |
| [launch.go](launch.go) | [4-launch.md](spec/4-launch.md) | `/api/launch/<name>` — clone → checkout, `ops ide` lifecycle, pgid teardown |
| [git.go](git.go) | [5-git.md](spec/5-git.md) | Git save / status APIs |
| [publish.go](publish.go) | [6-publish.md](spec/6-publish.md) | `/api/publish/{push,force-push,remote}` — every endpoint gated by `requireValidLicense` |
| [license.go](license.go) | [14-license.md](spec/14-license.md) | Offline Ed25519 license verification and the publishing gates |
| [skills.go](skills.go) | [7-skills.md](spec/7-skills.md) | `/api/skills/<name>` — clones skills into the app's `.agents/skills/` |
| [credits.go](credits.go) | [credit_check.md](spec/credit_check.md) | `/api/credits`, `/api/topup` — proxy to ai-proxy |
| [status.go](status.go) | [status_check.md](spec/status_check.md) | `/api/status` — provider model catalog |
| [memory.go](memory.go) | — | `/api/memory/` |
| [files.go](files.go) | [11-files.md](spec/11-files.md) | `/api/files/` — read-only workbench file viewer, path-containment checked |
| [terminal.go](terminal.go) | [12-terminal.md](spec/12-terminal.md) | `/api/terminal/<name>` — PTY-backed shell over a WebSocket |
| [gitignore.go](gitignore.go) | [13-gitignore.md](spec/13-gitignore.md) | Managed workbench `.gitignore` and the `CLAUDE.md`→`AGENTS.md` / `.claude`→`.agents` links |
| [auth.go](auth.go) | [authentication.md](spec/authentication.md) | Optional local auth — login, sessions, CSRF, rate limiting |
| [github.go](github.go) | [github.md](spec/github.md) | `/api/github/*` — GitHub login, logout, and repo listing |
| [starters.go](starters.go) | [15-starters.md](spec/15-starters.md) | `/api/starters` — starter application catalog |
| [support/index.py](support/index.py) | [15a-index.md](spec/15a-index.md) | Maintainer script that generates the published `index.json` |

### Host-based routing

The non-obvious core of the design: **a single listener on `:8910` serves three different surfaces**, distinguished entirely by the first label of the request hostname. [middleware.go](middleware.go) inspects the host and dispatches:

- `trustable.<domain>` → static `web/` assets **+** the Go `/api/*` handlers
- `opencode.<domain>` → reverse-proxy to `localhost:4096` (the opencode AI coding assistant)
- `vite.<domain>` → reverse-proxy to `localhost:5173` (the user's running app under Vite)
- Any other prefix → `400`, with a response pointing at the corrected URL

Bare `localhost` or raw-IP requests are **`307`-redirected** to `trustable.<ip>.nip.io:<port>`, so the fully-qualified form is always used in practice. This matters for testing: **every test must target an FQDN** — hitting plain `localhost:8910` only gets the redirect, never the app.

### Workspace vs. workbench

The app keeps two distinct on-disk representations of each project:

- **Workspace** — `$WORKSPACE_DIR/workspace/<name>/` is the **bare git repo** for an app. This is the durable source of truth and the thing that gets published.
- **Workbench** — `$WORKBENCH_DIR/<name>/` is the **active checkout** of whichever app is currently being edited. It is regenerated on launch and holds the volatile working state: `.env` / `.env.production`, `node_modules`, and `.agents/skills/`.

Launching an app **clones workspace → workbench** when the workbench is missing, and **regenerates the env files every time** so that configuration edits always propagate into the running app. A `pgid` file written into the workbench records the running `ops` process group, which is how teardown later finds and cleanly stops it.

### Configuration layering

Configuration is the result of **merging two `trustable.json` files** (see [spec/2a-config.md](spec/2a-config.md)):

1. **Base** — `./trustable.json`, the immutable defaults shipped with the binary.
2. **Workspace** — `$WORKSPACE_DIR/trustable.json`, holding user overrides plus the `apps` and `provider` data.

Maps are merged **key-by-key** (not wholesale-replaced), so the workspace layer only needs to carry deltas. `provider`, `apps`, and the chosen models live **only** in the workspace layer. Per-app environment variables live under `apps.<name>.development` and `apps.<name>.production`.

### Frontend (`web/`)

The UI is **plain HTML + Tailwind** (loaded via [web/tailwind.js](web/tailwind.js)) — **no build step and no React**. Each page maps to a spec doc and communicates with the backend **only** through `/api/*` JSON endpoints; there is no shared client framework or bundler.

| Page | Spec | Purpose |
|---|---|---|
| [web/index.html](web/index.html) | [1-index.md](spec/1-index.md) | Splash screen + provider choice |
| [web/applist.html](web/applist.html) | [1-applist.md](spec/1-applist.md) | App list |
| [web/app.html](web/app.html) | [3-app.md](spec/3-app.md) | Per-app workbench / editor |
| [web/appconfig.html](web/appconfig.html) | — | Per-app env editor |
| [web/configure.html](web/configure.html) | [2a-config.md](spec/2a-config.md) | Provider / model config |

## Prerequisites

- For macOS development, a **running Trustable VM** on the local machine — the macOS app from [trustable.ai](https://trustable.ai) provisions a k3s VM and writes `id_ed25519`, `current.ip`, and `apihost` into `~/Library/Application Support/Trustable/`. `setup.sh` reads these to extract the VM's kubeconfig so `ops` can talk to k3s directly.
- For Linux server development, local access to the Trustable k3s cluster with Docker or nerdctl and passwordless `sudo -n k3s`. `build.sh` detects this host automatically. Windows development is this same flow inside WSL2 — see [spec/start.md](spec/start.md).
- A **GitHub token** in `.ghtoken` at the repo root (git-ignored). `start.sh` checks it as its second step and prompts when it is missing.
- **Go** (managed via [`g`](https://github.com/stefanmaric/g)), plus `ops`, `air`, `bun`, `uv`, Node, and the TruACP/Pi versions pinned by `trustable-acp/pi.version` — all installed and verified by `setup.sh`.
- A populated **`.env`** (see below). Startup fails preflight if it is missing; `start.sh` seeds it from `.env.dist` on the first run.

## Getting started

See the TL;DR at the top for the per-system commands. In short:

- **macOS** — `./run.sh` (or `./start.sh`, which ends in the same place). `./run.sh`
  provisions/boots the `trudev` VM and runs `setup.sh` inside it via `./start.sh`, then
  re-invokes itself in the VM to run the dev loop (free ports 8910/5173/4096, `air` hot
  reload, print the UI URL). Press **^C** to stop — it tears down the dev loop and stops
  the VM (`./start.sh -s`), keeping it for a fast restart next time.
- **Windows** — `.\start.ps1`, which creates the WSL2 distribution and then runs the same
  Linux flow inside it, finishing with `./run.sh`.
- **Linux** — `./start.sh` to prepare the host, then `./run.sh` for the dev loop.

Ollama sign-in happens inside Trustable. `start.sh`/`setup.sh` are invoked for you; run
them directly only for a manual step (`./start.sh -k` to destroy the VM, `./setup.sh`
inside the VM to re-verify the toolchain).

`air` (configured in [.air.toml](.air.toml)) rebuilds `tmp/main` on **every `.go` change** and restarts the server on `:8910` — edit a Go file, save, and the running server reloads.

## Environment (`.env`)

`.env` is **mandatory** — preflight aborts startup if it is absent. Copy [.env.dist](.env.dist) and set every variable:

| Variable | Required | Description |
|---|---|---|
| `WORKSPACE_DIR` | ✅ | Must already exist (created by `ops setup mini`). Per-app bare repos live under `$WORKSPACE_DIR/workspace/<name>` |
| `WORKBENCH_DIR` | ✅ | Checkout area for the currently-launched app |
| `OPENAI_BASE_URL` | ✅ | Provider base URL (overwritten when the user picks a provider in the UI) |
| `OPENAI_API_KEY` | ✅ | Provider API key |
| `OLLAMA_ENDPOINT` | ✅ | Local Ollama endpoint for the Ollama provider |
| `AIP_REGISTER_URL` | ✅ | ai-proxy registration UI base. The splash loads this in an iframe for Trustable Cloud sign-up; the top-up form lives at `<this>/top-up`. Dev: `http://localhost:8080/_register`; Prod: `https://api.nuvolaris.io/_register` |
| `AIP_BASE_URL` | ✅ | ai-proxy JSON API base. `/api/credits`, `/api/topup`, and `/api/status` proxy directly under this URL. Dev: `http://localhost:8080/api/v2/`; Prod: `https://api.nuvolaris.io/api/v2/` |
| `GIT_USER` | ✅ | Author name for commits made on behalf of the user |
| `GIT_EMAIL` | ✅ | Author email for commits made on behalf of the user |

## Common commands

```bash
./run.sh         # macOS entrypoint: start VM + setup.sh, run dev loop, ^C stops the VM
./start.sh       # Provision/boot the trudev VM, run setup.sh, then run.sh; -v vscode, -s stop, -k destroy
.\start.ps1      # WINDOWS ONLY (PowerShell): create the WSL2 distro, run start.sh in it, then run.sh
./setup.sh       # Run INSIDE the VM: install/verify the toolchain (ops/go/air/uv/node/TruACP+Pi + MCP)
./build.sh       # No args: help. --build [--no-deploy] full image + deploy, --buildx CI multiarch push, --tag tag only
./hotfix.sh      # Same modes; layers a rebuilt binary + start.sh/env/trustable.json on the existing image (minutes, not ~20 min)
./publish.sh     # Push the latest git tag and watch CI; pushes olaris-bestia only with explicit authorization (never for a hotfix)
./ssh.sh         # SSH into the running Trustable VM; no args prints help, -d development (your files), -p production (trustable user), -i inside the container
go test ./...    # Unit tests
go test -run TestGenerateProjectAssetsForTruACP   # Run a single test
```

### Versioning & build

`_build.txt` is generated by [build.sh](build.sh) from `version.txt` (currently `v0.4.0`) plus `expiry.txt` (currently `2026/08/31`). It holds the multi-line `Version:` / `Build:` / `Branch:` / `Stream:` / `Expiry:` block that `parseVersion` reads at startup. In local Air development, missing branch metadata falls back to the current mounted Git branch so the app-list badge identifies the code being served.

`build.sh --build` and `build.sh --tag` **always** write the new image tag into `olaris-bestia/opsroot.json` via `jq`, on every host; it is `publish.sh` pushing the submodule that actually ships the new version to the deployment plugin. Deployment is always `ops bestia trustable redeploy`, which reads the tag from that file, so the cluster and the plugin never disagree.

[hotfix.sh](hotfix.sh) is the exception: it **never** writes `opsroot.json` and never commits, so it patches the running StatefulSet directly (`kubectl set image` + `rollout status`) rather than going through the plugin, which would resolve the base image and roll out the wrong thing. That patch is **not durable** — the next `ops bestia trustable redeploy` reverts it. A hotfix is a live patch, not a release; promoting one means a normal `./build.sh --build` plus an authorized `olaris-bestia` push. Its tag is `<base>-<n>`, counted from the remote because `build.sh` deletes every local tag on each build.

Both scripts take an optional `--no-deploy` after `--build` to stop once the image is built.

## API surface

All endpoints are JSON under `/api/*`, registered in [main.go](main.go):

| Endpoint | Handler file | Purpose |
|---|---|---|
| `GET /api/version` | main.go | Build / version info |
| `GET /api/status` | status.go | Provider model catalog (powers the splash screen) |
| `/api/repo`, `/api/upload` | repo.go | Per-app repo management & uploads |
| `/api/git`, `/api/git/status/`, `/api/git/save`, `/api/git/pull`, `/api/git/deploy` | git.go / repo.go | Git status, save, pull, and the ungated development deploy |
| `/api/launch`, `/api/launch/<name>`, `/api/clean`, `/api/undeploy` | launch.go | Clone → checkout, `ops ide` lifecycle, teardown |
| `/api/configuration`, `/api/configure`, `/api/testmodel`, `/api/appconfig/` | configure.go | Config & model testing |
| `/api/ollama-connect`, `/api/discover-models` | configure.go | Ollama discovery |
| `/api/publish/{push,force-push,remote}` | publish.go | Publishing (license-gated) |
| `/api/license` | license.go | License install & status |
| `/api/skills/<name>` | skills.go | Install skills into an app |
| `/api/starters` | starters.go | Starter application catalog |
| `/api/files/` | files.go | Read-only workbench file viewer |
| `/api/terminal/<name>` | terminal.go | PTY shell over a WebSocket |
| `/api/github/{status,login,login/cancel,logout,repos}` | github.go | GitHub login and repo listing |
| `/api/memory/` | memory.go | App memory |
| `/api/credits`, `/api/topup` | credits.go | Credits & top-up (ai-proxy) |
| `/api/sshkey` | — | SSH key handling |
| `/api/redeploy`, `/api/activations/poll` | — | Deploy & activation polling |

### Publishing authorization

There is **no client-side gate** on publishing. The frontend always renders the Git Push / Publish controls and always calls the backend — the authorization decision lives entirely on the server. It is an offline, Ed25519-signed **license** (`lic_<payload>.<sig>`) held in the workspace config and verified against `master_key_pub`, which is committed and embedded in the binary, so no network access is involved. Two gates live in [license.go](license.go), both answering **HTTP 402**:

1. `requireValidLicense` — called by **all three** `/api/publish/*` handlers. It checks the signature and the expiry only; the license's `hosts` list is not consulted. On failure: `{"error": "License required: <reason>"}`.
2. `requireLicensedHost` — called by `handlePublishRemote` alone, after the production config is resolved and before `ops ide login` runs, so nothing touches an unlicensed cluster. It matches `OPS_APIHOST` against the license `hosts` exactly — no wildcards. On mismatch: `{"error": "Host not licensed: <apihost> is not covered by your license"}`.

An expired license is invalid outright, so git push stops too. The local apihosts (`miniops.me`, `localhost`, `127.0.0.1`, `::1`) skip **only** the host gate — a valid license is still required to publish to them. `/api/git/deploy` (development launch) is not gated at all.

The frontend keys off the exact `"License required"` and `"Host not licensed"` prefixes to show a license modal instead of a raw error. **When changing this wording, keep the prefixes intact** — otherwise the frontend gate breaks.

Licenses are issued with the `trulicense` CLI, which lives in the **trustable-installer** repo (run `./trulicense.sh` there). It keeps the Ed25519 signing key in 1Password (vault `TrustableLicenses`, item `MasterKey Trustable`) rather than on disk, and archives every issued license in the same vault. This repo only *verifies* licenses. See [spec/14-license.md](spec/14-license.md).

## Submodules

Seven git submodules are declared in [.gitmodules](.gitmodules) — `mcp`, `trustable-acp`, `olaris`, `olaris-bestia`, `olaris-truinst`, `skills`, and `support`. The `mcp` submodule points to the Nuvolaris fork of `openserverless-mcp` and is used by the image build to install the `openserverless-mcp` command; `trustable-acp` carries the TruACP/Pi runtime sources and the `pi.version` pins. As noted above, the macOS build flow writes the new image tag into `olaris-bestia/opsroot.json`; pushing **that** submodule is the step that actually ships a new version to the deployment plugin, and it requires explicit authorization.

`start.sh` initializes `mcp` and `trustable-acp` by itself on every host, so a plain clone is enough to start. To initialize all of them:

```bash
git submodule update --init --recursive
```

## Testing

- **Unit and component tests:** `go test ./...` runs the Go test suite. `npm run test:e2e-providers` covers the legacy provider runner. Pi has no OpenCode guardrail-plugin test suite.
- **End-to-end scenarios:** [tests/](tests/) contains runnable cluster tests for issue 98, action workflows, compaction recovery, generated authentication, and the Private AI, Ollama Cloud, and Regolo providers. They are intentionally separate from `go test` because they launch applications and may invoke a model. See [tests/issue98-e2e.md](tests/issue98-e2e.md) and [spec/8-e2e.md](spec/8-e2e.md).

## Conventions

- **Adding a new API:** register the route in [main.go](main.go), add the handler in the matching feature file, and update the spec doc under [spec/](spec/) — the spec is iterated on first, then the code follows.
- **`opencode.md` is not repo guidance.** The `opencode.md` at the repo root is **embedded into the binary** and shown to the AI assistant running inside *user-created* apps. It is **not** instructions for editing this repository — that role belongs to `CLAUDE.md`. Don't confuse the two.
- **Process groups for cleanup.** Long-running subprocesses (`ops ide deploy`, `npm install`, `git push`) are spawned with their own process group; the `pgid` file in `$WORKBENCH_DIR` is how teardown finds and stops them.

## License

See [LICENSE](LICENSE).
