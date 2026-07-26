# Repository-root `setup.sh`

This specification defines the repository-root `setup.sh`. The script recreates,
inside a supported Ubuntu development
target, the same environment that `image/Dockerfile` builds, so that `./run.sh`
can be run with all MCP servers and references ready. Supported targets are the
Ubuntu `trudev` Lima VM created by `./start.sh` and Ubuntu on WSL with local k3s,
passwordless sudo, and systemd enabled.

setup.sh runs INSIDE the VM, as the mirrored guest user (the macOS user that
`start.sh` recreated in the VM with the same UID + passwordless sudo). `start.sh`
invokes it automatically after provisioning (`limactl shell --workdir "$PWD"
trudev ./setup.sh`), so the normal flow is just `./run.sh` on the host; you can
also run it manually via `./ssh.sh ./setup.sh` or from a login shell in the VM
(`limactl shell trudev`). It is idempotent — re-runs just verify. The repo is
virtiofs-mounted at its host path and is writable by this user.

`start.sh` allocates 60 GiB to a new `trudev` instance. This is the minimum
supported development capacity for the local k3s service images, containerd
snapshots, and repeated Trustable image imports without the known 40 GiB
DiskPressure failure. Existing larger instances are left unchanged.

On WSL, run `setup.sh` manually as the Linux development user. The repository
should live in the WSL Linux filesystem, and `/etc/rancher/k3s/k3s.yaml` must
describe the k3s running in that same WSL instance. WSL remains an Ubuntu
local-k3s target, but service forwarding must be supplied explicitly with the
same pinned `kubefwd` used by `trudev`; repository setup must not rewrite the
Windows or Linux resolver configuration.

Runtime tools are installed for the local user. npm global packages use the
setup-owned user prefix (default `~/.npm-global`), while uv tools, wrappers, and
the TruACP launcher remain under `~/.local/bin` and
`~/.local/lib/truacp`; Pi state remains under `~/.pi/agent`. The pinned
upstream Milvus CLI is a system prerequisite under `/opt/uv/tools` with entry
points in `/usr/local/bin`; `~/.local/bin/milvus_cli` belongs exclusively to
Trustable's per-app wrapper. `sudo` is otherwise limited to system packages and
system prerequisites (the guest user has passwordless sudo). npm global
installation never uses `sudo`. This mirrors the Dockerfile's per-user
(`trustable`) stages while keeping the wrapper boundary identical in Lima and
the image.

When I say add to the PATH, add to ~/.bashrc (the VM is Ubuntu; bash login shell).

If a check fails, abort and warn the user.

0. Read from image/Dockerfile the variables in the format

ARG <VARIABLE>=<VALUE>

and set the env vars

- OLLAMA_VERSION
- OPS_BRANCH
- OPS_REPO
- MILVUS_MCP_REPO
- MILVUS_MCP_REF

1. Ensure a proper .env exists, then load it.

There are three env sources: `.env.dist` (the portable template), `image/env`
(baked into the container image: HOME=/home/trustable, workspace under it, AIP at
api.nuvolaris.io), and the .env we generate here (the image layout, but rooted at
the guest user's own $HOME).

- If .env is ABSENT, create it from the .env.dist key set with in-VM values:
  - WORKSPACE_DIR=$HOME/workspace
  - WORKBENCH_DIR=$HOME/workbench          (absolute, under the guest $HOME — not
    .env.dist's relative ./workbench, so the checks below and run.sh hold
    regardless of cwd)
  - OLLAMA_ENDPOINT=http://localhost:11434
  - OPENAI_BASE_URL=http://localhost:11434/v1
  - OPENAI_API_KEY=dummy
  - AIP_REGISTER_URL=https://api.nuvolaris.io/_register
  - AIP_BASE_URL=https://api.nuvolaris.io/api/v2/
    Use the PROD api.nuvolaris.io endpoints, NOT .env.dist's localhost:8080: the
    ai-proxy is not part of the local k3s cluster and nothing serves it on
    localhost:8080 in the VM. Both image/env and the repo's in-VM .env use
    api.nuvolaris.io. (A user running a local ai-proxy can override afterwards.)
  - GIT_USER=TrustableUser
  - GIT_EMAIL=noreply@example.com
- If .env is PRESENT, do NOT overwrite it — validate it as before: every key in
  .env.dist must be set (no empty value, no leftover <placeholder>), warn/abort on
  any missing.
- Then `mkdir -p "$WORKSPACE_DIR"` and `mkdir -p "$WORKBENCH_DIR"`, load .env with
  the bash loop, and confirm both dirs exist (they now will).

2. check ops is in the path and the values of the env vars should be the same as
OPS_BRANCH and OPS_REPO

ops may already be present from the VM's trustable package. If ops is missing,
export OPS_REPO/OPS_BRANCH and install it, then add ~/.ops/linux-<arch>/bin to
the path:

curl -sL n7s.co/get-ops | bash

If present but OPS_REPO/OPS_BRANCH mismatch, warn and recommend reinstalling.

3. Add to the path ~/.ops/linux-<arch>/bin (<arch> from dpkg --print-architecture:
arm64/amd64) and check uv is in path. If uv is missing, install it for the user:

curl -LsSf https://astral.sh/uv/install.sh | env UV_INSTALL_DIR="$HOME/.local/bin" INSTALLER_NO_MODIFY_PATH=1 sh

4. if go is not in the path, install first g:

curl -sSL https://raw.githubusercontent.com/voidint/g/master/install.sh | bash

Source `~/.g/env` first when it already exists, restoring both `~/.g/bin` and
the active Go toolchain to `PATH`, then activate the Go version in `go.mod`.
Repeated setup runs must not reinstall `g` or report it missing merely because
a non-interactive Lima/WSL shell did not inherit its environment.

add the Go install bin dir to the PATH (GOBIN if set, else `go env GOPATH`/bin),
then install air (go install github.com/air-verse/air@latest)

5. if npm is not on the path, install Node 24 via NodeSource (matching the
Dockerfile; node may already be present from the VM package):

curl -fsSL https://deb.nodesource.com/setup_24.x | sudo -E bash -
sudo apt-get install -y nodejs

After npm is available and before any global npm installation, repository-root
`setup.sh` owns one npm-prefix policy:

- use `NPM_CONFIG_PREFIX` when explicitly supplied and compatible;
- otherwise preserve the current `npm config get prefix` when it is an absolute,
  writable directory owned by the current user;
- otherwise default to `$HOME/.npm-global`;
- create `<prefix>/bin` and `<prefix>/lib/node_modules` without sudo;
- export `NPM_CONFIG_PREFIX=<prefix>` and prepend `<prefix>/bin` to the current
  setup process PATH;
- persist the prefix with `npm config set ... --location=user`;
- persist the PATH through the canonical `~/.bashrc` PATH line. Repeated setup
  runs must not duplicate that line.

An explicitly configured prefix that is relative, owned by another user, or
not writable without privilege is incompatible and falls back with a warning to
`$HOME/.npm-global`. `trustable-acp/setup.sh` consumes this exported/configured
prefix; it does not own another persistent default in the repository-root flow.
The image build remains reproducible: it invokes the same nested installer as
the image build user and uses that build context's writable npm prefix.

6. ensure $HOME/.local/bin is the first entry in the path and warn if not. The
selected npm `<prefix>/bin` must also be present before npm-installed commands
are verified.

7. check you can reach OpenWhisk.

locate the <apihost>:

- first reading the env variables OPS_APIHOST/APIHOST/TRUSTABLE_DEFAULT_APIHOST if
  defined (prefer whatever the in-VM ops is already configured with — the package
  may set OPS_APIHOST)
- otherwise use http://miniops.me

Inside the VM you talk to traefik on :80 directly, which matches the *.miniops.me
ingress hosts. Do NOT use the host-side <ip>.nip.io:8080 reverse proxy from
start.sh — that exists to serve the macOS host, not the VM.

verify curl -sL <apihost>/api/info | jq .description returns OpenWhisk

8. Select the Kubernetes client and extract the kubeconfig for ops from the
LOCAL k3s (no ssh, no IP rewrite — `127.0.0.1` in k3s.yaml is already correct
inside Lima or WSL).

Prefer a standalone `kubectl` when present; otherwise use the client guaranteed
by the Trustable package as `k3s kubectl`. Abort with a clear error if neither
exists. All subsequent commands must use that selected client with
`KUBECONFIG=~/.ops/tmp/kubeconfig`; do not invoke an assumed standalone
`kubectl` directly.

mkdir -p ~/.ops/tmp
sudo cat /etc/rancher/k3s/k3s.yaml > ~/.ops/tmp/kubeconfig
chmod 600 ~/.ops/tmp/kubeconfig

Only do this if ~/.ops/tmp/kubeconfig is missing or invalid (the package may
already have wired ops). sudo is passwordless for the guest user.

After writing the file, wait for `/readyz` and show the real client/API error if
the local cluster does not become ready. Do not hide a missing executable or
connection error behind a generic CoreDNS message.

After kubeconfig is valid, do not modify resolver configuration or restart
resolver services. Processes outside the cluster reach service names through
the single namespace-wide `kubefwd` process owned by repository-root `run.sh`.
The `trudev` provisioning path installs the pinned forwarder through
repository-root `start.sh`; WSL must provide that same executable before
`run.sh` starts.

9. check you have administrative power
ensuring `ops admin listuser` does not return error

10. check the CLI tools the image ships are available; install any that are
missing (apt via passwordless sudo, the pinned upstream Milvus CLI via uv):

- psql       (postgresql-client-16)
- redis-cli  (redis-tools)
- rclone
- lsof, required by Trustable's orphaned TruACP/Vite listener recovery
- milvus-cli 1.2.1, installed globally with its upstream entry points under
  `/usr/local/bin`
- GitHub CLI 2.96.0, installed from the official Linux tarball under
  `/usr/local/bin/gh`

(uv itself was ensured in step 3. There is no `/opt/homebrew` in the VM.
`~/.local/bin/milvus_cli` is reserved for the configured Trustable wrapper
generated at app launch; setup must not place the upstream executable there.)

The GitHub CLI release is owned by `GH_VERSION`, `GH_SHA_AMD64`, and
`GH_SHA_ARM64` in `image/Dockerfile`. Setup reads those exact values, selects
only amd64/arm64, verifies the archive SHA-256 before extraction, and rejects a
different installed version instead of accepting a floating distro package.
The CLI is only a runtime dependency at this stage: setup does not authenticate
an account and never reads a developer machine's normal `~/.config/gh`.

11. Install the pinned Pi coding-agent toolchain.

- Read literal npm install specifications from `trustable-acp/pi.version`,
  ignoring comments and blank lines.
- Reject any entry without an explicit version separator; setup must never
  resolve a floating Pi CLI or extension.
- Install the complete list globally under the selected user-owned npm prefix
  and verify `pi` is on `PATH`.

Do not install OpenCode, use `https://opencode.ai/install`, or replace the
checked-in Trustable ACP adapter with a public unpinned package.

12. install in ~/.local/bin the mcp servers for browser, openserverless, redis,
milvus, postgres, mongodb, and s3 using the same procedure in image/Dockerfile
(do not use `/opt/uv/*` vars and install everything for the local user).

The python-based mcp servers (postgres, redis, milvus) are installed with uv,
pointing the tool bin dir to ~/.local/bin:

```
for tool in \
    postgres-mcp==0.3.0 \
    redis-mcp-server==0.5.0 \
    git+https://github.com/trustable-ai/mcp-server-milvus.git@a7e624f3057a0d739528bca3ed92504943224ceb ;
do
    env UV_TOOL_BIN_DIR="$HOME/.local/bin" uv tool install $tool
done
```

The Milvus MCP source and commit are declared by
`MILVUS_MCP_REPO`/`MILVUS_MCP_REF` in `image/Dockerfile` and consumed by both
the image and this development setup. Use the Trustable fork at the exact
checked-in commit; never install its floating `main` or fall back directly to
the `zilliztech` upstream URL. On an existing VM, inspect uv's
`mcp-server-milvus/uv-receipt.toml`; if either repository or revision differs,
force-reinstall the pinned specification and verify the resulting receipt.
Package-name-only “already installed” output is not sufficient evidence because
it can leave an executable created from the previous upstream source.

Package the repository `mcp` submodule and install that tarball together with
the mongodb server using npm (global, under the selected user-owned npm prefix):

```
pack_dir=$(mktemp -d)
(cd mcp && npm pack --pack-destination "$pack_dir")
npm install -g tsx "$pack_dir"/openserverless-mcp-*.tgz mongodb-mcp-server@1.9.0
```

Do not install OpenServerless MCP directly from GitHub in development: that
would overwrite Trustable's checked-out protocol-error, secret, and connector
fixes whenever `run.sh` recreates the environment. Verify that the installed
source contains the local `secret-unbind` registration.

Pin MongoDB MCP to `1.9.0`: it is the last upstream release whose direct Zod
dependency (`^3.25.76`) satisfies the `@mongosh/arg-parser` peer contract.
Newer `1.10.0` through `1.13.0` releases require Zod 4 while the parser still
declares Zod 3 and therefore produce an invalid npm peer tree.

The VM development packages also include `python3-pytest` and
`python3-dotenv`, matching generated application tests without requiring the
assistant to modify the system Python environment during a session.

Pack `browser-mcp/` and install the resulting `trustable-browser-mcp` package,
then pack `react-mcp/` and install the resulting `trustable-react-mcp` package
plus `tsx` under the selected npm prefix. The React MCP is a read-only deterministic
validator and is separate from optional Agentic React. Install Playwright
`1.56.1` Chromium and its system dependencies with
`PLAYWRIGHT_BROWSERS_PATH=~/.cache/ms-playwright`. Setup must verify that both
managed MCP commands resolve on PATH before completing.

The S3 MCP is downloaded as a release binary and installed behind the repo's
Python wrapper (image/mcp-s3), exactly as the Dockerfile does: the release binary
becomes mcp-s3-real, and the wrapper is installed as mcp-s3 (it blocks the
list_buckets tools and normalizes empty bucket lists, delegating to the adjacent
mcp-s3-real in the local bin directory):

```
export VER=1.3.0 ARCH="$(uname -m | sed -e s/x86_64/amd64/ -e s/aarch64/arm64/)" OS="$(uname -s | tr A-Z a-z)"
curl -sL https://github.com/txn2/mcp-s3/releases/download/v${VER}/mcp-s3_${VER}_${OS}_${ARCH}.tar.gz |\
    tar -C "$HOME/.local/bin" -xzvf - mcp-s3
mv "$HOME/.local/bin/mcp-s3" "$HOME/.local/bin/mcp-s3-real"
install -m 0755 image/mcp-s3 "$HOME/.local/bin/mcp-s3"
```

The Go server writes each app's `.mcp.json` referencing these servers by command
name (mcp-s3, postgres-mcp, redis-mcp-server, mcp-server-milvus,
mongodb-mcp-server, openserverless-mcp), so "MCP ready" means all of them resolve
on the guest's PATH.

13. Build and install TruACP/Pi from the checked-out `trustable-acp` submodule.

Every Pi package must be pinned in `trustable-acp/pi.version`. Run
`trustable-acp/setup.sh` from inside that directory so it builds the bundled UI,
installs the pinned agents/adapters, and creates `~/.local/bin/truacp`. Verify
that `pi`, `pi-acp`, and `truacp` resolve from the guest PATH. Do not install an
OpenCode runtime or `@opencode-ai/plugin`: issue #51 is a hard Pi cutover.
After registering the pinned `pi-mcp-adapter@2.11.0`, the same installer applies
its version-guarded Streamable HTTP session-recovery transform even when
`pi install` was skipped as already registered. The transform must validate all
source targets before writing, remain idempotent, reconnect and refresh
tools/resources after an expired server session, and retry a tool call at most
once. A version or source-layout mismatch is a setup failure.
The same setup installs the reviewed issue #57 extension at
`~/.local/lib/truacp/extensions/trustable-runtime.ts`. A missing extension is
a setup failure because managed Trustable must not start an unguarded Pi
session. Standalone TruACP does not load this extension unless the host selects
it through the typed launch contract.

This is the "references ready" prerequisite. Ensure the ~/.bashrc PATH matches the
image ordering, including the selected npm prefix bin and the Go bin dir
(GOBIN/GOPATH-bin), so a fresh login shell (as run.sh uses) finds npm-installed
commands and air:

~/.local/bin:<npm-prefix>/bin:~/.ops/linux-<arch>/bin:<go-bin>:/usr/local/bin:/usr/bin:/bin

Note (no action needed): per-app `AGENTS.md`, its `CLAUDE.md` compatibility
mirror, `.openserverless-contract.md`, and `.mcp.json` are written at launch by
the Go binary. There is no project-local `opencode.md`. Skills come from
OPS_SKILLS (default `trustable-ai/skills`) cloned at launch by `skills.go`;
setup does nothing for these, but they are covered when an app is launched.

## Upstream Pi setup contract (#71)

This section supersedes earlier nested-Pi-build instructions. Clean `trudev`
setup installs the exact upstream Pi `0.82.0` package set from
`trustable-acp/pi.version` only after matching every entry in
`trustable-acp/pi.integrity`. It must not initialize or build a Pi source fork,
consume locally staged Pi tarballs, or rely on host npm caches. The nested
`trustable-acp/pi-acp` fork remains required and is built by the ACP installer.
