Write a setup.sh script that recreates — INSIDE the Ubuntu VM created by
`./start.sh` (the `trudev` Lima VM) — the same environment that `image/Dockerfile`
builds, so that `./run.sh` can be run inside the VM with all MCP servers and
references ready.

setup.sh runs INSIDE the VM, as the mirrored guest user (the macOS user that
`start.sh` recreated in the VM with the same UID + passwordless sudo). `start.sh`
invokes it automatically after provisioning (`limactl shell --workdir "$PWD"
trudev ./setup.sh`), so the normal flow is just `./run.sh` on the host; you can
also run it manually via `./ssh.sh ./setup.sh` or from a login shell in the VM
(`limactl shell trudev`). It is idempotent — re-runs just verify. The repo is
virtiofs-mounted at its host path and is writable by this user.

Everything is installed for the local user — into `~/.local/bin` and
`~/.config/opencode`, no `/opt/uv/*`, no `sudo` except where a step needs a system
package (the guest user has passwordless sudo). This mirrors the Dockerfile's
per-user (`trustable`) stages but for the mirrored guest user.

When I say add to the PATH, add to ~/.bashrc (the VM is Ubuntu; bash login shell).

If a check fails, abort and warn the user.

0. Read from image/Dockerfile the variables in the format

ARG <VARIABLE>=<VALUE>

and set the env vars

- OLLAMA_VERSION
- OPENCODE_VERSION
- OPS_BRANCH
- OPS_REPO

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

Source `~/.g/env` first when it already exists, then activate the go version in
go.mod. Repeated setup runs must not reinstall `g` merely because a
non-interactive shell did not inherit its environment.

add the Go install bin dir to the PATH (GOBIN if set, else `go env GOPATH`/bin),
then install air (go install github.com/air-verse/air@latest)

5. if npm is not on the path, install Node 24 via NodeSource (matching the
Dockerfile; node may already be present from the VM package):

curl -fsSL https://deb.nodesource.com/setup_24.x | sudo -E bash -
sudo apt-get install -y nodejs

6. ensure $HOME/.local/bin is the first entry in the path and warn if not

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

8. Extract the kubeconfig for ops from the LOCAL k3s (no ssh, no IP rewrite — the
127.0.0.1 in k3s.yaml is already correct inside the VM):

mkdir -p ~/.ops/tmp
sudo cat /etc/rancher/k3s/k3s.yaml > ~/.ops/tmp/kubeconfig
chmod 600 ~/.ops/tmp/kubeconfig

Only do this if ~/.ops/tmp/kubeconfig is missing or invalid (the package may
already have wired ops). sudo is passwordless for the guest user.

After kubeconfig is valid, read the `kube-system/kube-dns` ClusterIP and create
`/etc/systemd/resolved.conf.d/trustable-k3s.conf` with that DNS server routed
only for `~cluster.local`. Restart `systemd-resolved` only when the file changes
and require `kubernetes.default.svc.cluster.local` to resolve. Host-side
Trustable Code and MCP processes consume service names from
`~/.ops/config.json`; ClusterIP routing alone is insufficient without this DNS
route.

9. check you have administrative power
ensuring `ops admin listuser` does not return error

10. check the CLI tools the image ships are available; install any that are
missing (apt via passwordless sudo, milvus-cli via uv into ~/.local/bin):

- psql       (postgresql-client-16)
- redis-cli  (redis-tools)
- rclone
- milvus-cli (uv tool install milvus-cli, UV_TOOL_BIN_DIR=$HOME/.local/bin)

(uv itself was ensured in step 3. There is no /opt/homebrew and no kubefwd in the
VM — cluster services are local, so kubefwd is not used; see run.md.)

11. Build and verify the pinned Trustable Code runtime.

- Read the Bun version from the `FROM oven/bun:<version>` builder in
  `image/Dockerfile` and install that exact version for the guest user when it
  is missing. Ensure `unzip` is installed before invoking the Bun installer.
- Require the initialized `trustable-code` submodule and read its current Git
  revision plus a content fingerprint of tracked and untracked working-tree
  changes.
- Rebuild when the installed revision/fingerprint differs, the reported
  OpenCode version differs from `$OPENCODE_VERSION`, or the binary lacks the
  literal `TRUSTABLE_RUNTIME_CONFIG` runtime contract.
- Mirror the Docker build: run `HUSKY=0 bun install --frozen-lockfile`, then
  `bun run script/build.ts --single --skip-install` in
  `trustable-code/packages/opencode`.
- Verify the built version and runtime marker, atomically install the binary as
  `~/.local/bin/opencode`, and record the revision plus working-tree fingerprint
  under `~/.local/share/trustable-code/ref`. This lets Lima validate an
  uncommitted Trustable Code fix without reusing a stale binary.

Do not use `https://opencode.ai/install`: upstream can report the same version
without containing the Trustable runtime contract.

12. install in ~/.local/bin the mcp servers for browser, openserverless, redis,
milvus, postgres, mongodb, and s3 using the same procedure in image/Dockerfile
(do not use `/opt/uv/*` vars and install everything for the local user).

The python-based mcp servers (postgres, redis, milvus) are installed with uv,
pointing the tool bin dir to ~/.local/bin:

```
for tool in \
    postgres-mcp==0.3.0 \
    redis-mcp-server==0.5.0 \
    git+https://github.com/zilliztech/mcp-server-milvus.git@ca21cc71f00ad61f7a79e77af7d1dc20de549dd3 ;
do
    env UV_TOOL_BIN_DIR="$HOME/.local/bin" uv tool install $tool
done
```

Package the repository `mcp` submodule and install that tarball together with
the mongodb server using npm (global, for the local user):

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

Pack `browser-mcp/` and install the resulting `trustable-browser-mcp` package
plus `tsx` under `~/.local`. Install Playwright `1.56.1` Chromium and its system
dependencies with `PLAYWRIGHT_BROWSERS_PATH=~/.cache/ms-playwright`. Setup must
verify that `trustable-browser-mcp` resolves on PATH before completing.

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

The Go server writes each app's opencode.json / .mcp.json referencing these
servers by command name (mcp-s3, postgres-mcp, redis-mcp-server, mcp-server-milvus,
mongodb-mcp-server, openserverless-mcp), so "MCP ready" means all of them resolve
on the guest's PATH.

13. Recreate the opencode plugin and PATH from the Dockerfile's user stage.

Install the opencode plugin matched to the opencode version:

```
mkdir -p "$HOME/.config/opencode"
cd "$HOME/.config/opencode"
npm init -y >/dev/null
npm install "@opencode-ai/plugin@$(opencode --version)"
test -d "$HOME/.config/opencode/node_modules/@opencode-ai/plugin"
```

This is the "references ready" prerequisite. Ensure the ~/.bashrc PATH matches the
image ordering, including the Go bin dir (GOBIN/GOPATH-bin) so a fresh login shell
(as run.sh uses) finds air:

~/.local/bin:~/.ops/linux-<arch>/bin:<go-bin>:/usr/local/bin:/usr/bin:/bin

Note (no action needed): the per-app opencode.md and .openserverless-contract.md
are written at launch by the Go binary, and skills come from OPS_SKILLS (default
trustable-ai/skills) cloned at launch by skills.go — setup does nothing for these,
but they are covered when an app is launched.
