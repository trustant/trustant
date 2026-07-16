#!/bin/bash
#
# setup.sh — recreate the image/Dockerfile environment inside supported Ubuntu
# development targets: the trudev Lima VM or Ubuntu on WSL with local k3s.
#
# In Lima it runs as the mirrored guest user created by ./start.sh (invoke via
# `./ssh.sh ./setup.sh` or from a login shell: `limactl shell trudev`). In WSL,
# run it as the development user with passwordless sudo and systemd enabled.
# Everything is installed for the local user (~/.local/bin, ~/.config/opencode),
# no /opt/uv/*, no sudo except for system packages (the guest has passwordless
# sudo). See setup.md — that is the source of truth for these steps.
#
set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

ok()   { echo -e "${GREEN}✓ $1${NC}"; }
warn() { echo -e "${YELLOW}⚠ $1${NC}"; }
fail() { echo -e "${RED}✗ $1${NC}"; exit 1; }

cd "$(dirname "$0")"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)        ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) fail "unsupported architecture: $(uname -m) (expected amd64 or arm64)" ;;
esac

[[ "$OS" == "linux" ]] || fail "setup.sh supports Ubuntu Linux in Lima or WSL, not ${OS}"
[[ -r /etc/os-release ]] || fail "cannot identify the Linux distribution: /etc/os-release is missing"
# shellcheck disable=SC1091
source /etc/os-release
case " ${ID:-} ${ID_LIKE:-} " in
  *ubuntu*|*debian*) ;;
  *) fail "unsupported Linux distribution: ${PRETTY_NAME:-${ID:-unknown}} (expected Ubuntu/Debian)" ;;
esac

IS_WSL=false
if grep -qiE '(microsoft|wsl)' /proc/sys/kernel/osrelease /proc/version 2>/dev/null; then
  IS_WSL=true
fi

RC_FILES=("$HOME/.bashrc")

# Snapshot the shell's PATH before add_to_path mutates it, so the ~/.local/bin
# check reflects the user's environment, not our in-process changes.
ORIGINAL_PATH="$PATH"

add_to_path() {
  local dir="$1"
  for rc in "${RC_FILES[@]}"; do
    if ! grep -qF "$dir" "$rc" 2>/dev/null; then
      echo "export PATH=\"$dir:\$PATH\"" >> "$rc"
      ok "Added $dir to $rc"
    fi
  done
  export PATH="$dir:$PATH"
}

# --- 0. Read ARG <VAR>=<VALUE> versions from image/Dockerfile ---
echo "--- Reading versions from image/Dockerfile ---"
[[ -f image/Dockerfile ]] || fail "image/Dockerfile not found"

read_arg() {
  local var="$1"
  grep -m1 "^ARG ${var}=" image/Dockerfile | cut -d'=' -f2- | tr -d ' '
}

for v in OLLAMA_VERSION OPENCODE_VERSION OPS_BRANCH OPS_REPO; do
  val=$(read_arg "$v")
  [[ -n "$val" ]] || fail "ARG $v not found in image/Dockerfile"
  export "$v=$val"
  ok "$v=$val"
done

# --- 1. Ensure a proper .env exists (create if absent), then load it ---
# Three env sources: .env.dist (template), image/env (baked image), and the .env
# we generate here (image layout but rooted at the guest user's own $HOME).
echo "--- Checking .env ---"
[[ -f .env.dist ]] || fail ".env.dist not found"

if [[ ! -f .env ]]; then
  warn ".env not found — creating an in-VM .env"
  cat > .env <<ENV
WORKSPACE_DIR=$HOME/workspace
WORKBENCH_DIR=$HOME/workbench
OPENAI_BASE_URL=http://localhost:11434/v1
OPENAI_API_KEY=dummy
OLLAMA_ENDPOINT=http://localhost:11434
AIP_REGISTER_URL=https://api.nuvolaris.io/_register
AIP_BASE_URL=https://api.nuvolaris.io/api/v2/
GIT_USER=TrustableUser
GIT_EMAIL=noreply@example.com
ENV
  ok "created .env"
else
  # Present: do not overwrite — validate every .env.dist key is set.
  while IFS='=' read -r key _; do
    [[ -z "$key" || "$key" =~ ^# ]] && continue
    key=$(echo "$key" | xargs)
    val=$(grep "^${key}=" .env | cut -d'=' -f2-)
    if [[ -z "$val" || "$val" == "<"*">" ]]; then
      fail "Variable $key is not set in .env (still has placeholder or is empty)"
    fi
  done < .env.dist
  ok ".env is present and all variables from .env.dist are set"
fi

set -a
source ./.env
set +a

WORKSPACE_DIR_EXPANDED=$(eval echo "${WORKSPACE_DIR}")
mkdir -p "${WORKSPACE_DIR_EXPANDED}"
[[ -d "${WORKSPACE_DIR_EXPANDED}" ]] || fail "WORKSPACE_DIR '${WORKSPACE_DIR}' could not be created"
ok "WORKSPACE_DIR exists: ${WORKSPACE_DIR_EXPANDED}"

WORKBENCH_DIR_EXPANDED=$(eval echo "${WORKBENCH_DIR}")
mkdir -p "${WORKBENCH_DIR_EXPANDED}"
[[ -d "${WORKBENCH_DIR_EXPANDED}" ]] || fail "WORKBENCH_DIR '${WORKBENCH_DIR}' could not be created"
ok "WORKBENCH_DIR exists: ${WORKBENCH_DIR_EXPANDED}"

# --- 2. Check ops is in PATH and OPS_REPO/OPS_BRANCH match Dockerfile values ---
# ops may already be present from the VM's trustable package.
echo "--- Checking ops ---"
if ! command -v ops &>/dev/null; then
  warn "ops not found in PATH, installing..."
  export OPS_REPO OPS_BRANCH
  curl -sL n7s.co/get-ops | bash || fail "ops install failed"
  add_to_path "$HOME/.local/bin"
  add_to_path "$HOME/.ops/linux-${ARCH}/bin"
  hash -r
  command -v ops &>/dev/null || fail "ops is not installed"
  ok "ops installed"
fi

OPS_INFO=$(ops -info 2>/dev/null || true)
ops_info_value() { echo "$OPS_INFO" | grep -i "^$1[:=]" | head -1 | cut -d: -f2- | xargs; }
OPS_REPO_ACTUAL=$(ops_info_value OPS_REPO)
OPS_BRANCH_ACTUAL=$(ops_info_value OPS_BRANCH)
# fall back to the environment if ops -info does not expose them
OPS_REPO_ACTUAL="${OPS_REPO_ACTUAL:-${OPS_REPO:-}}"
OPS_BRANCH_ACTUAL="${OPS_BRANCH_ACTUAL:-${OPS_BRANCH:-}}"

if [[ "$OPS_REPO_ACTUAL" != "$OPS_REPO" ]]; then
  warn "ops OPS_REPO is '${OPS_REPO_ACTUAL}', expected '${OPS_REPO}'"
  warn "Recommend: export OPS_REPO=${OPS_REPO} and reinstall ops (curl -fsSL n7s.co/get-ops | bash)"
  fail "OPS_REPO mismatch"
fi
if [[ "$OPS_BRANCH_ACTUAL" != "$OPS_BRANCH" ]]; then
  warn "ops OPS_BRANCH is '${OPS_BRANCH_ACTUAL}', expected '${OPS_BRANCH}'"
  warn "Recommend: export OPS_BRANCH=${OPS_BRANCH} and reinstall ops (curl -fsSL n7s.co/get-ops | bash)"
  fail "OPS_BRANCH mismatch"
fi
ok "ops is installed with correct OPS_REPO and OPS_BRANCH"

# --- 3. Add ~/.ops/linux-<arch>/bin to PATH and ensure uv ---
echo "--- Checking ops bin dir and uv ---"
add_to_path "$HOME/.ops/linux-${ARCH}/bin"

if ! command -v uv &>/dev/null; then
  warn "uv not found, installing for the local user..."
  curl -LsSf https://astral.sh/uv/install.sh \
    | env UV_INSTALL_DIR="$HOME/.local/bin" INSTALLER_NO_MODIFY_PATH=1 sh \
    || fail "uv install failed"
  add_to_path "$HOME/.local/bin"
fi
command -v uv &>/dev/null || fail "uv not found in PATH"
ok "uv is available"

# --- 4. Check Go (install via g if missing), activate version from go.mod, air ---
echo "--- Checking Go ---"
GO_VERSION=$(grep '^go ' go.mod | awk '{print $2}')
if [ -s "$HOME/.g/env" ]; then
  set +u
  source "$HOME/.g/env"
  set -u
fi

if ! command -v go &>/dev/null; then
  warn "go not found, installing g (Go version manager)..."
  curl -sSL https://raw.githubusercontent.com/voidint/g/master/install.sh | bash || fail "g install failed"
  add_to_path "$HOME/.g/bin"
  export GOROOT="$HOME/.g/go"
  add_to_path "$GOROOT/bin"
fi

command -v g &>/dev/null || fail "g not found after installation"

if ! go version 2>/dev/null | grep -qF "go${GO_VERSION}"; then
  warn "Activating Go ${GO_VERSION}..."
  g install "$GO_VERSION" || fail "g install ${GO_VERSION} failed"
  g use "$GO_VERSION" || fail "g use ${GO_VERSION} failed"
fi
go version 2>/dev/null | grep -qF "go${GO_VERSION}" || fail "Go ${GO_VERSION} not active after g use"
ok "Go ${GO_VERSION} is available"

# go install drops binaries in GOBIN if set, else GOPATH/bin. Add that dir to
# PATH (and to the login rc below) so air and other go tools are found.
GO_BIN="$(go env GOBIN)"
[[ -n "$GO_BIN" ]] || GO_BIN="$(go env GOPATH)/bin"
add_to_path "$GO_BIN"

echo "--- Checking air ---"
if ! command -v air &>/dev/null; then
  warn "air not found, installing..."
  go install github.com/air-verse/air@latest || fail "go install air failed"
fi
command -v air &>/dev/null || fail "air installation failed"
ok "air is available"

# --- 5. Install Node 24 via NodeSource if npm is missing (may be from the .deb) ---
echo "--- Checking npm ---"
if ! command -v npm &>/dev/null; then
  warn "npm not found, installing Node 24 via NodeSource..."
  curl -fsSL https://deb.nodesource.com/setup_24.x | sudo -E bash - || fail "NodeSource setup failed"
  sudo apt-get install -y nodejs || fail "apt-get install nodejs failed"
fi
command -v npm &>/dev/null || fail "npm not in PATH after install"
ok "npm is available"

# --- 6. Ensure ~/.local/bin is the first entry in PATH ---
echo "--- Checking ~/.local/bin is first in PATH ---"
LOCAL_BIN="$HOME/.local/bin"
mkdir -p "$LOCAL_BIN"
FIRST_ENTRY="${ORIGINAL_PATH%%:*}"
if [[ "$FIRST_ENTRY" == "$LOCAL_BIN" ]]; then
  ok "~/.local/bin is the first entry in PATH"
else
  warn "~/.local/bin is not the first entry in PATH (first is: ${FIRST_ENTRY})"
  warn "Prepend it by adding to your shell rc: export PATH=\"\$HOME/.local/bin:\$PATH\""
fi

# --- 7. Reach OpenWhisk ---
# Inside the VM you talk to traefik on :80 directly (it matches the *.miniops.me
# ingress hosts). Do NOT use the host-side <ip>.nip.io:8080 reverse proxy.
echo "--- Locating OpenWhisk apihost ---"
if [[ -n "${OPS_APIHOST:-}" ]]; then
  APIHOST="$OPS_APIHOST"
elif [[ -n "${APIHOST:-}" ]]; then
  APIHOST="$APIHOST"
elif [[ -n "${TRUSTABLE_DEFAULT_APIHOST:-}" ]]; then
  APIHOST="$TRUSTABLE_DEFAULT_APIHOST"
else
  APIHOST="http://miniops.me"
fi
APIHOST="${APIHOST%/}"

echo "Using apihost: $APIHOST"
WHISK_DESC=$(curl -sL "${APIHOST}/api/info" | jq -r '.description' 2>/dev/null) || true
[[ "$WHISK_DESC" == "OpenWhisk" ]] || fail "Cannot reach OpenWhisk at ${APIHOST}/api/info (got: ${WHISK_DESC:-no response})"
ok "OpenWhisk reachable at ${APIHOST}"

# --- 8. Extract kubeconfig for ops from the LOCAL k3s (no ssh, no IP rewrite) ---
# The 127.0.0.1 in k3s.yaml is already correct inside Lima/WSL. The Trustable
# package guarantees `k3s kubectl`; some environments additionally provide a
# standalone `kubectl`, so select either without assuming one particular layout.
echo "--- Ensuring ops kubeconfig ---"
KUBECONFIG_FILE="$HOME/.ops/tmp/kubeconfig"

if command -v kubectl &>/dev/null; then
  KUBECTL_CMD=(kubectl)
elif command -v k3s &>/dev/null; then
  KUBECTL_CMD=(k3s kubectl)
else
  fail "no Kubernetes client found: install kubectl or the local k3s runtime"
fi
kube() {
  KUBECONFIG="$KUBECONFIG_FILE" "${KUBECTL_CMD[@]}" "$@"
}
ok "Kubernetes client: ${KUBECTL_CMD[*]}"

if kube get --raw='/readyz' &>/dev/null; then
  ok "kubeconfig already valid at $KUBECONFIG_FILE"
else
  mkdir -p "$HOME/.ops/tmp"
  [[ -r /etc/rancher/k3s/k3s.yaml ]] || sudo test -r /etc/rancher/k3s/k3s.yaml \
    || fail "local k3s kubeconfig is not readable at /etc/rancher/k3s/k3s.yaml"
  sudo cat /etc/rancher/k3s/k3s.yaml > "$KUBECONFIG_FILE" || fail "failed to read /etc/rancher/k3s/k3s.yaml"
  chmod 600 "$KUBECONFIG_FILE"
  ok "kubeconfig written to $KUBECONFIG_FILE"
fi

KUBE_READY=false
for _ in $(seq 1 30); do
  if kube get --raw='/readyz' &>/dev/null; then
    KUBE_READY=true
    break
  fi
  sleep 2
done
if [[ "$KUBE_READY" != true ]]; then
  warn "Kubernetes API diagnostic:"
  kube get --raw='/readyz' || true
  fail "local k3s API did not become ready using $KUBECONFIG_FILE"
fi
ok "local k3s API is ready"

# Host-side development processes use service names from ~/.ops/config.json,
# including *.svc.cluster.local. Route that DNS suffix through the local k3s
# CoreDNS service; ClusterIP routing is already available on the VM host.
echo "--- Configuring k3s service DNS ---"
CLUSTER_DNS_IP=""
for _ in $(seq 1 30); do
  CLUSTER_DNS_IP=$(kube -n kube-system get svc kube-dns -o jsonpath='{.spec.clusterIP}' 2>/dev/null || true)
  if [[ -z "$CLUSTER_DNS_IP" || "$CLUSTER_DNS_IP" == "None" ]]; then
    CLUSTER_DNS_IP=$(kube -n kube-system get svc -l k8s-app=kube-dns -o jsonpath='{.items[0].spec.clusterIP}' 2>/dev/null || true)
  fi
  [[ -n "$CLUSTER_DNS_IP" && "$CLUSTER_DNS_IP" != "None" ]] && break
  sleep 2
done
if [[ -z "$CLUSTER_DNS_IP" || "$CLUSTER_DNS_IP" == "None" ]]; then
  warn "Kubernetes services visible in kube-system:"
  kube -n kube-system get svc || true
  fail "cannot determine the local CoreDNS Service ClusterIP"
fi

if ! command -v systemctl &>/dev/null || ! systemctl is-active --quiet systemd-resolved; then
  if [[ "$IS_WSL" == true ]]; then
    fail "WSL requires systemd-resolved: enable systemd in /etc/wsl.conf, run 'wsl.exe --shutdown' from Windows, then retry"
  fi
  fail "systemd-resolved is required to route cluster.local inside this Ubuntu VM"
fi
RESOLVED_DIR=/etc/systemd/resolved.conf.d
RESOLVED_FILE="$RESOLVED_DIR/trustable-k3s.conf"
RESOLVED_CONTENT=$(printf '[Resolve]\nDNS=%s\nDomains=~cluster.local\n' "$CLUSTER_DNS_IP")
if [[ "$(sudo cat "$RESOLVED_FILE" 2>/dev/null || true)" != "$RESOLVED_CONTENT" ]]; then
  RESOLVED_TMP=$(mktemp)
  printf '%s\n' "$RESOLVED_CONTENT" > "$RESOLVED_TMP"
  sudo mkdir -p "$RESOLVED_DIR"
  sudo install -m 0644 "$RESOLVED_TMP" "$RESOLVED_FILE"
  rm -f "$RESOLVED_TMP"
  sudo systemctl restart systemd-resolved || fail "failed to restart systemd-resolved"
fi
for _ in $(seq 1 15); do
  getent hosts kubernetes.default.svc.cluster.local &>/dev/null && break
  sleep 1
done
getent hosts kubernetes.default.svc.cluster.local &>/dev/null \
  || fail "cluster.local DNS is not resolving through CoreDNS ${CLUSTER_DNS_IP}"
ok "cluster.local DNS resolves through CoreDNS ${CLUSTER_DNS_IP}"

# --- 9. Check admin power ---
echo "--- Checking admin access ---"
ops admin listuser &>/dev/null || fail "No administrative power (ops admin listuser failed)"
ok "Admin access confirmed"

# --- 10. Ensure the image's CLI tools are available (install any missing) ---
# No /opt/homebrew and no kubefwd in the VM — cluster services are local.
echo "--- Checking CLI tools (psql, redis-cli, rclone, milvus-cli) ---"
APT_MISSING=()
command -v psql      &>/dev/null || APT_MISSING+=(postgresql-client-16)
command -v redis-cli &>/dev/null || APT_MISSING+=(redis-tools)
command -v rclone    &>/dev/null || APT_MISSING+=(rclone)
python3 -c 'import pytest' &>/dev/null || APT_MISSING+=(python3-pytest)
python3 -c 'import dotenv' &>/dev/null || APT_MISSING+=(python3-dotenv)
if [[ ${#APT_MISSING[@]} -gt 0 ]]; then
  warn "installing missing apt packages: ${APT_MISSING[*]}"
  sudo apt-get update -qq || fail "apt-get update failed"
  sudo apt-get install -y "${APT_MISSING[@]}" || fail "apt-get install ${APT_MISSING[*]} failed"
fi
ok "psql, redis-cli, rclone, pytest, and python-dotenv available"

if ! command -v milvus_cli &>/dev/null && ! command -v milvus-cli &>/dev/null; then
  warn "milvus-cli not found, installing via uv..."
  env UV_TOOL_BIN_DIR="$LOCAL_BIN" uv tool install milvus-cli || fail "uv tool install milvus-cli failed"
fi
ok "milvus-cli available"

# --- 11. Build the pinned Trustable Code runtime used by the image ---
echo "--- Checking Trustable Code ---"
[[ -f trustable-code/packages/opencode/package.json ]] \
  || fail "trustable-code submodule is not initialized (run: git submodule update --init trustable-code)"

BUN_VERSION=$(sed -n 's/^FROM oven\/bun:\([^ ]*\).*/\1/p' image/Dockerfile | head -1)
[[ -n "$BUN_VERSION" ]] || fail "Bun version not found in image/Dockerfile"
add_to_path "$HOME/.bun/bin"

if ! command -v unzip &>/dev/null; then
  warn "installing unzip for the pinned Bun toolchain..."
  sudo apt-get update -qq || fail "apt-get update failed"
  sudo apt-get install -y unzip || fail "unzip install failed"
fi

if ! command -v bun &>/dev/null || [[ "$(bun --version 2>/dev/null || true)" != "$BUN_VERSION" ]]; then
  warn "installing Bun ${BUN_VERSION} for the Trustable Code build..."
  curl -fsSL https://bun.sh/install \
    | env BUN_INSTALL="$HOME/.bun" bash -s "bun-v${BUN_VERSION}" \
    || fail "Bun ${BUN_VERSION} install failed"
fi
command -v bun &>/dev/null || fail "bun is required to build Trustable Code"
[[ "$(bun --version)" == "$BUN_VERSION" ]] || fail "Bun version does not match image/Dockerfile"

BUILD_MISSING=()
command -v python3 &>/dev/null || BUILD_MISSING+=(python3)
command -v make    &>/dev/null || BUILD_MISSING+=(make)
command -v g++     &>/dev/null || BUILD_MISSING+=(g++)
if [[ ${#BUILD_MISSING[@]} -gt 0 ]]; then
  warn "installing Trustable Code build dependencies: ${BUILD_MISSING[*]}"
  sudo apt-get update -qq || fail "apt-get update failed"
  sudo apt-get install -y "${BUILD_MISSING[@]}" || fail "Trustable Code build dependency install failed"
fi

TRUSTABLE_CODE_REF=$(git -C trustable-code rev-parse HEAD) \
  || fail "cannot read trustable-code submodule revision"
TRUSTABLE_CODE_WORKTREE_HASH=$(
  (
    git -C trustable-code diff --binary HEAD --
    while IFS= read -r file; do
      printf 'untracked:%s\n' "$file"
      sha256sum "trustable-code/$file"
    done < <(git -C trustable-code ls-files --others --exclude-standard)
  ) | sha256sum | awk '{print $1}'
) || fail "cannot fingerprint trustable-code working tree"
TRUSTABLE_CODE_BUILD_REF="${TRUSTABLE_CODE_REF}:${TRUSTABLE_CODE_WORKTREE_HASH}"
TRUSTABLE_CODE_STATE_DIR="$HOME/.local/share/trustable-code"
TRUSTABLE_CODE_REF_FILE="$TRUSTABLE_CODE_STATE_DIR/ref"
INSTALLED_TRUSTABLE_CODE_REF=$(cat "$TRUSTABLE_CODE_REF_FILE" 2>/dev/null || true)
OPENCODE_ACTUAL=$(opencode --version 2>/dev/null | tr -d ' ' || true)
OPENCODE_BIN=$(command -v opencode 2>/dev/null || true)
TRUSTABLE_RUNTIME_PRESENT=false
if [[ -n "$OPENCODE_BIN" ]] && grep -aFq 'TRUSTABLE_RUNTIME_CONFIG' "$OPENCODE_BIN"; then
  TRUSTABLE_RUNTIME_PRESENT=true
fi

if [[ "$OPENCODE_ACTUAL" != "$OPENCODE_VERSION" \
   || "$INSTALLED_TRUSTABLE_CODE_REF" != "$TRUSTABLE_CODE_BUILD_REF" \
   || "$TRUSTABLE_RUNTIME_PRESENT" != true ]]; then
  warn "building Trustable Code ${TRUSTABLE_CODE_REF:0:10} (OpenCode ${OPENCODE_VERSION})..."
  (
    cd trustable-code
    HUSKY=0 bun install --frozen-lockfile
    cd packages/opencode
    bun run script/build.ts --single --skip-install
    binary=$(find dist -type f -path '*/bin/opencode' -print -quit)
    [[ -n "$binary" ]] || fail "Trustable Code build did not produce an opencode binary"
    [[ "$($binary --version)" == "$OPENCODE_VERSION" ]] \
      || fail "Trustable Code binary version does not match ${OPENCODE_VERSION}"
    grep -aFq 'TRUSTABLE_RUNTIME_CONFIG' "$binary" \
      || fail "built binary does not contain the Trustable runtime contract"
    install -m 0755 "$binary" "$HOME/.local/bin/opencode.new"
    mv "$HOME/.local/bin/opencode.new" "$HOME/.local/bin/opencode"
  ) || fail "Trustable Code build failed"
  mkdir -p "$TRUSTABLE_CODE_STATE_DIR"
  printf '%s\n' "$TRUSTABLE_CODE_BUILD_REF" > "$TRUSTABLE_CODE_REF_FILE"
  hash -r
fi

OPENCODE_BIN=$(command -v opencode 2>/dev/null || true)
[[ -n "$OPENCODE_BIN" ]] || fail "Trustable Code installation failed"
[[ "$(opencode --version 2>/dev/null | tr -d ' ')" == "$OPENCODE_VERSION" ]] \
  || fail "installed Trustable Code version does not match ${OPENCODE_VERSION}"
grep -aFq 'TRUSTABLE_RUNTIME_CONFIG' "$OPENCODE_BIN" \
  || fail "installed opencode does not contain the Trustable runtime contract"
ok "Trustable Code ${TRUSTABLE_CODE_REF:0:10} (OpenCode ${OPENCODE_VERSION}) is available"

# --- 12. Install MCP servers (openserverless, redis, milvus, postgres, mongodb, s3) ---
# Mirrors image/Dockerfile but for the local user (~/.local/bin, no /opt/uv/*).
echo "--- Installing MCP servers for local use ---"
MCP_BIN="$HOME/.local/bin"
mkdir -p "$MCP_BIN"

command -v uv &>/dev/null || fail "uv is required to install MCP servers"

# postgres, redis, milvus MCP servers via uv tool (same pins as the Dockerfile)
for tool in \
    postgres-mcp==0.3.0 \
    redis-mcp-server==0.5.0 \
    'git+https://github.com/zilliztech/mcp-server-milvus.git@ca21cc71f00ad61f7a79e77af7d1dc20de549dd3' ;
do
  env \
    UV_TOOL_BIN_DIR="$MCP_BIN" \
    UV_LINK_MODE=hardlink \
    uv tool install "$tool" || fail "uv tool install $tool failed"
done

# openserverless, mongodb, and the local browser MCP server via npm (global,
# for the local user). Package the checked-out OpenServerless MCP submodule so
# development runs exactly the source that the image build consumes.
command -v npm &>/dev/null || fail "npm is required to install the npm MCP servers"
# --prefix "$HOME/.local" so binaries land in ~/.local/bin (already first in
# PATH) and packages under ~/.local/lib — never the root-owned /usr/lib.
[[ -f mcp/package.json ]] || fail "mcp submodule is not initialized (run: git submodule update --init mcp)"
OPENSERVERLESS_MCP_PACK_DIR=$(mktemp -d)
( cd mcp && npm pack --pack-destination "$OPENSERVERLESS_MCP_PACK_DIR" >/dev/null ) \
  || fail "packing local openserverless-mcp failed"
OPENSERVERLESS_MCP_PACKAGE=$(find "$OPENSERVERLESS_MCP_PACK_DIR" -maxdepth 1 -name 'openserverless-mcp-*.tgz' -print -quit)
[[ -n "$OPENSERVERLESS_MCP_PACKAGE" ]] || fail "local openserverless-mcp package was not created"
( cd "$HOME" && npm install -g --prefix "$HOME/.local" tsx "$OPENSERVERLESS_MCP_PACKAGE" mongodb-mcp-server@1.9.0 ) \
  || fail "npm install of openserverless-mcp/mongodb-mcp-server failed"
rm -rf "$OPENSERVERLESS_MCP_PACK_DIR"
grep -qF 'secret-unbind' "$HOME/.local/lib/node_modules/openserverless-mcp/src/index.ts" \
  || fail "installed openserverless-mcp does not match the local Trustable source"

BROWSER_MCP_PACK_DIR=$(mktemp -d)
( cd browser-mcp && npm pack --pack-destination "$BROWSER_MCP_PACK_DIR" >/dev/null ) \
  || fail "packing trustable-browser-mcp failed"
BROWSER_MCP_PACKAGE=$(find "$BROWSER_MCP_PACK_DIR" -maxdepth 1 -name 'trustable-browser-mcp-*.tgz' -print -quit)
[[ -n "$BROWSER_MCP_PACKAGE" ]] || fail "trustable-browser-mcp package was not created"
( cd "$HOME" && npm install -g --prefix "$HOME/.local" tsx "$BROWSER_MCP_PACKAGE" ) \
  || fail "installing trustable-browser-mcp failed"
rm -rf "$BROWSER_MCP_PACK_DIR"
command -v trustable-browser-mcp &>/dev/null || fail "trustable-browser-mcp is not in PATH"
env PLAYWRIGHT_BROWSERS_PATH="$HOME/.cache/ms-playwright" \
  npx --yes playwright@1.56.1 install --with-deps chromium \
  || fail "installing Playwright Chromium failed"

# s3 MCP: the txn2/mcp-s3 release binary behind the repo's Python wrapper (as the
# Dockerfile does): release -> mcp-s3-real, wrapper (image/mcp-s3) -> mcp-s3. The
# wrapper blocks list_buckets tools and normalizes empty bucket lists.
if [[ ! -x "$MCP_BIN/mcp-s3-real" ]]; then
  MCP_S3_VER=1.3.0
  curl -sL "https://github.com/txn2/mcp-s3/releases/download/v${MCP_S3_VER}/mcp-s3_${MCP_S3_VER}_${OS}_${ARCH}.tar.gz" \
    | tar -C "$MCP_BIN" -xzf - mcp-s3 \
    || fail "mcp-s3 download failed"
  mv "$MCP_BIN/mcp-s3" "$MCP_BIN/mcp-s3-real" || fail "renaming mcp-s3 -> mcp-s3-real failed"
fi
install -m 0755 image/mcp-s3 "$MCP_BIN/mcp-s3" || fail "installing mcp-s3 wrapper failed"

ok "MCP servers (browser, openserverless, postgres, redis, milvus, mongodb, s3) installed in $MCP_BIN"

# --- 13. Recreate the opencode plugin and PATH (image stage2-user) ---
echo "--- Setting up opencode plugin ---"
mkdir -p "$HOME/.config/opencode"
(
  cd "$HOME/.config/opencode"
  npm init -y >/dev/null
  npm install "@opencode-ai/plugin@$(opencode --version)"
) || fail "opencode plugin install failed"
test -d "$HOME/.config/opencode/node_modules/@opencode-ai/plugin" \
  || fail "@opencode-ai/plugin not installed"
ok "opencode plugin installed"

# Ensure ~/.bashrc PATH matches the image ordering, including BOTH the Go toolchain
# dir (GOROOT/bin — where `go` itself lives, via g) and the Go install bin dir
# (GOBIN/GOPATH-bin — where air lands), so a fresh login shell (as run.sh uses)
# finds `go` AND `air`. Omitting GOROOT/bin makes `go` vanish in the login shell,
# which in turn hides air.
GO_ROOT_BIN="$(go env GOROOT)/bin"
IMAGE_PATH="\$HOME/.local/bin:\$HOME/.ops/linux-${ARCH}/bin:${GO_ROOT_BIN}:${GO_BIN}:/usr/local/bin:/usr/bin:/bin"
if ! grep -qF "$IMAGE_PATH" "$HOME/.bashrc" 2>/dev/null; then
  echo "export PATH=\"$IMAGE_PATH\"" >> "$HOME/.bashrc"
  ok "added image PATH ordering to ~/.bashrc"
fi

# Note: per-app opencode.md / .openserverless-contract.md are written at launch by
# the Go binary, and skills come from OPS_SKILLS (default trustable-ai/skills)
# cloned at launch by skills.go — setup does nothing for these.

echo ""
echo -e "${GREEN}=== Setup complete! ===${NC}"
echo "Restart your shell or run: source ~/.bashrc"
echo "Then run ./run.sh inside the VM."
