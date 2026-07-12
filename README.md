# trustable-app

> **TL;DR** — install [Lima](https://lima-vm.io/) (`brew install lima`), then start everything with `./run.sh`. It boots the VM, installs the toolchain, and launches the dev loop — after that you can just develop.

A Go single-binary web server that hosts a **"Lovable-like" development environment** on top of [OpenServerless](https://openserverless.apache.org/). From one executable it serves a local UI, reverse-proxies the user's running app and AI assistant, drives `ops` CLI subprocesses, and manages per-app workspaces and publishing.

The whole product is delivered as **one process listening on `:8910`**. There is no separate frontend server, no API gateway, and no build pipeline for the UI — the binary embeds its web assets and serves everything itself, which keeps deployment to a single artifact and development to a single hot-reload loop.

> **Note:** This repository can build against either the macOS **Trustable VM**
> or a Linux Trustable k3s server. The macOS app from
> [trustable.ai](https://trustable.ai) provisions a [k3s](https://k3s.io/) VM
> and writes its credentials (`id_ed25519`, `current.ip`, `apihost`) to
> `~/Library/Application Support/Trustable/`. When those files are absent,
> `build.sh` delegates to the Linux server build path (`build-server.sh`),
> which imports the image into local k3s and patches `StatefulSet/trustable`.

---

## What it does

`trustable-app` lets a user **create, edit, run, and publish** web applications backed by OpenServerless, with the [opencode](https://opencode.ai/) AI coding assistant wired into each app. The single `:8910` server:

- **Serves the local UI** (`web/`) — splash, app list, per-app editor, and config pages
- **Reverse-proxies** the user's running app (Vite on `:5173`) and the AI assistant (opencode on `:4096`), so all three surfaces share one origin
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
| [publish.go](publish.go) | [6-publish.md](spec/6-publish.md) | `/api/publish/{push,force-push,remote}` — every endpoint gated by `requirePublishingAuth` |
| [validate_key.go](validate_key.go) | [10-validate_key.md](spec/10-validate_key.md) | Ed25519 verification of the `aip_<id>.<sig>` API key |
| [skills.go](skills.go) | [7-skills.md](spec/7-skills.md) | `/api/skills/<name>` — clones skills into the app's `.agents/skills/` |
| [credits.go](credits.go) | [credit_check.md](spec/credit_check.md) | `/api/credits`, `/api/topup` — proxy to ai-proxy |
| [status.go](status.go) | [status_check.md](spec/status_check.md) | `/api/status` — provider model catalog |
| [memory.go](memory.go) | — | `/api/memory/` |

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
- For Linux server development, local access to the Trustable k3s cluster with `kubectl`, Docker, and passwordless `sudo -n k3s` for importing images into containerd. This is the path used by `build-server.sh`.
- **Go** (managed via [`g`](https://github.com/stefanmaric/g)), plus `ops`, `air`, `bun`, `uv`, and `opencode` — all installed and verified by `setup.sh`.
- A populated **`.env`** (see below). Startup fails preflight if it is missing.

## Getting started

On macOS, **just run `./run.sh`** — it does the whole lifecycle for you:

```bash
./run.sh
```

`./run.sh` provisions/boots the `trudev` VM and runs `setup.sh` inside it (via
`./start.sh`), then re-invokes itself in the VM to run the dev loop (free ports
8910/5173/4096, `air` hot reload, print the UI URL). Press **^C** to stop — it
tears down the dev loop and stops the VM (`./start.sh -s`), keeping it for a fast
restart next time. Ollama sign-in happens inside Trustable.

`start.sh`/`setup.sh` are invoked for you; run them directly only for a manual
step (`./start.sh -k` to destroy the VM, `./setup.sh` inside the VM to re-verify
the toolchain).

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
./start.sh       # Provision/boot the trudev VM and run setup.sh; -s stops it, -k destroys it
./setup.sh       # Run INSIDE the VM: install/verify the toolchain (ops/go/air/uv/node/opencode + MCP)
./build.sh       # Compatibility build entrypoint: Mac VM when available, Linux/k3s server otherwise
./build-server.sh # Force Linux/k3s server build, import, StatefulSet patch, rollout wait
./publish.sh     # Push the latest git tag, watch CI, then push the olaris-bestia submodule
./ssh.sh         # SSH into the running Trustable VM
go test ./...    # Unit tests (currently mostly configure_test.go)
go test -run TestManagedOllamaDetectionRequiresGeneratedModelMarker   # Run a single test
```

### Versioning & build

`_build.txt` is generated by [build.sh](build.sh) / [build-server.sh](build-server.sh) from `version.txt` (currently `v0.3.10`) plus `expiry.txt` (currently `2026/08/31`). It holds the multi-line `Version:` / `Build:` / `Expiry:` block that `parseVersion` reads at startup.

On macOS VM builds, `build.sh` also writes the new image tag into `olaris-bestia/opsroot.json` via `jq`; it is `publish.sh` pushing the submodule that actually ships the new version to the deployment plugin. On Linux server builds, `build-server.sh` does **not** update `olaris-bestia/opsroot.json`: it builds a local image, imports it into local k3s with `docker save ... | sudo -n k3s ctr images import -`, patches `StatefulSet/trustable`, and waits for rollout.

## API surface

All endpoints are JSON under `/api/*`, registered in [main.go](main.go):

| Endpoint | Handler file | Purpose |
|---|---|---|
| `GET /api/version` | main.go | Build / version info |
| `GET /api/status` | status.go | Provider model catalog (powers the splash screen) |
| `/api/repo`, `/api/upload` | repo.go | Per-app repo management & uploads |
| `/api/git`, `/api/git/status/`, `/api/git/save` | git.go / repo.go | Git status & save |
| `/api/launch`, `/api/launch/<name>` | launch.go | Clone → checkout, `ops ide` lifecycle |
| `/api/configuration`, `/api/configure`, `/api/testmodel`, `/api/appconfig/` | configure.go | Config & model testing |
| `/api/ollama-connect`, `/api/discover-models` | configure.go | Ollama discovery |
| `/api/publish/{push,force-push,remote}` | publish.go | Publishing (signature-gated) |
| `/api/skills/<name>` | skills.go | Install skills into an app |
| `/api/memory/` | memory.go | App memory |
| `/api/credits`, `/api/topup` | credits.go | Credits & top-up (ai-proxy) |
| `/api/sshkey` | — | SSH key handling |
| `/api/redeploy`, `/api/activations/poll`, `/api/bestia-check` | — | Deploy & activation polling |

### Publishing authorization

There is **no client-side gate** on publishing. The frontend always renders the Git Push / Publish controls and always calls the backend — the authorization decision lives entirely on the server. Every `/api/publish/*` handler calls `requirePublishingAuth` ([validate_key.go](validate_key.go)), which:

1. Reads the merged config's `api_key` and `base_url`.
2. Fetches `<origin>/.well-known/ai-proxy-pubkey` (cached for the lifetime of the process).
3. Validates the Ed25519 signature embedded in the `aip_<id>.<sig>` key against that public key.
4. On **any** failure, returns `403 {"error": "Publishing not authorized: <reason>"}`.

The frontend keys off the exact `"Publishing not authorized"` prefix to show a friendly modal instead of a raw error. **When changing this wording, keep the prefix intact** — otherwise the frontend gate breaks.

## Submodules

Five git submodules are declared in [.gitmodules](.gitmodules) — `mcp`, `olaris`, `olaris-bestia`, `skills`, and `support`. The `mcp` submodule points to the Nuvolaris fork of `openserverless-mcp` and is used by the image build to install the `openserverless-mcp` command. As noted above, the macOS build flow writes the new image tag into `olaris-bestia/opsroot.json`; pushing **that** submodule in `publish.sh` is the step that actually ships a new version to the deployment plugin. The Linux server build flow does not change `olaris-bestia/opsroot.json`.

Initialize them after cloning:

```bash
git submodule update --init --recursive
```

## Testing

- **Unit and guardrail tests:** `go test ./...` runs the Go test suite. `npm run test:guardrails`, `npm run test:e2e-providers`, and `npm --prefix browser-mcp test` cover the OpenCode guardrails, provider runner, and browser MCP contracts.
- **End-to-end scenarios:** [tests/](tests/) contains runnable cluster tests for issue 98, action workflows, compaction recovery, generated authentication, and the BestIA, Ollama Cloud, and Regolo providers. They are intentionally separate from `go test` because they launch applications and may invoke a model. See [tests/issue98-e2e.md](tests/issue98-e2e.md) and [spec/8-e2e.md](spec/8-e2e.md).

## Conventions

- **Adding a new API:** register the route in [main.go](main.go), add the handler in the matching feature file, and update the spec doc under [spec/](spec/) — the spec is iterated on first, then the code follows.
- **`opencode.md` is not repo guidance.** The `opencode.md` at the repo root is **embedded into the binary** and shown to the AI assistant running inside *user-created* apps. It is **not** instructions for editing this repository — that role belongs to `CLAUDE.md`. Don't confuse the two.
- **Process groups for cleanup.** Long-running subprocesses (`ops ide deploy`, `npm install`, `git push`) are spawned with their own process group; the `pgid` file in `$WORKBENCH_DIR` is how teardown finds and stops them.

## License

See [LICENSE](LICENSE).
