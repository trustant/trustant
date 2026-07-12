#!/bin/bash
#
# setup.sh — recreate the image/Dockerfile environment INSIDE the trudev VM.
#
# Runs as the mirrored guest user inside the Ubuntu VM created by ./start.sh
# (invoke via `./ssh.sh ./setup.sh` or from a login shell: `limactl shell trudev`).
# Everything is installed for the local user (~/.local/bin, ~/.config/opencode),
# no /opt/uv/*, no sudo except for system packages (the guest has passwordless
# sudo). See spec/setup.md — that is the source of truth for these steps.
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
esac

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
# The 127.0.0.1 in k3s.yaml is already correct inside the VM.
echo "--- Ensuring ops kubeconfig ---"
KUBECONFIG_FILE="$HOME/.ops/tmp/kubeconfig"
if KUBECONFIG="$KUBECONFIG_FILE" kubectl --raw='/readyz' &>/dev/null; then
  ok "kubeconfig already valid at $KUBECONFIG_FILE"
else
  mkdir -p "$HOME/.ops/tmp"
  sudo cat /etc/rancher/k3s/k3s.yaml > "$KUBECONFIG_FILE" || fail "failed to read /etc/rancher/k3s/k3s.yaml"
  chmod 600 "$KUBECONFIG_FILE"
  ok "kubeconfig written to $KUBECONFIG_FILE"
fi

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
if [[ ${#APT_MISSING[@]} -gt 0 ]]; then
  warn "installing missing apt packages: ${APT_MISSING[*]}"
  sudo apt-get update -qq || fail "apt-get update failed"
  sudo apt-get install -y "${APT_MISSING[@]}" || fail "apt-get install ${APT_MISSING[*]} failed"
fi
ok "psql, redis-cli, rclone available"

if ! command -v milvus_cli &>/dev/null && ! command -v milvus-cli &>/dev/null; then
  warn "milvus-cli not found, installing via uv..."
  env UV_TOOL_BIN_DIR="$LOCAL_BIN" uv tool install milvus-cli || fail "uv tool install milvus-cli failed"
fi
ok "milvus-cli available"

# --- 11. Check opencode version matches OPENCODE_VERSION, install if needed ---
echo "--- Checking opencode ---"
install_opencode() {
  curl -fsSL https://opencode.ai/install >opencode.sh
  bash opencode.sh --version "${OPENCODE_VERSION}" || fail "opencode install failed"
  mv "$HOME/.opencode/bin/opencode" "$HOME/.local/bin/" || fail "moving opencode to ~/.local/bin failed"
}

if ! command -v opencode &>/dev/null; then
  warn "opencode not found, installing ${OPENCODE_VERSION}..."
  install_opencode
else
  OPENCODE_ACTUAL=$(opencode -v 2>/dev/null | tr -d ' ' || true)
  if [[ "$OPENCODE_ACTUAL" != "$OPENCODE_VERSION" ]]; then
    warn "opencode version is '${OPENCODE_ACTUAL}', expected '${OPENCODE_VERSION}', reinstalling..."
    install_opencode
  fi
fi
command -v opencode &>/dev/null || fail "opencode installation failed"
ok "opencode ${OPENCODE_VERSION} is available"

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

# openserverless + mongodb MCP servers via npm (global, for the local user).
# Run from $HOME so npm's git fetch does not stumble into this repo's broken
# submodule worktree (.git/modules/...), and force the https transport so it
# never falls back to ssh://git@github.com (which needs SSH keys).
command -v npm &>/dev/null || fail "npm is required to install the npm MCP servers"
# --prefix "$HOME/.local" so binaries land in ~/.local/bin (already first in
# PATH) and packages under ~/.local/lib — never the root-owned /usr/lib.
( cd "$HOME" && GIT_CONFIG_COUNT=1 \
    GIT_CONFIG_KEY_0=url.https://github.com/.insteadOf \
    GIT_CONFIG_VALUE_0=ssh://git@github.com/ \
    npm install -g --prefix "$HOME/.local" git+https://github.com/apache/openserverless-mcp.git mongodb-mcp-server@1.13.0 ) \
  || fail "npm install of openserverless-mcp/mongodb-mcp-server failed"

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

ok "MCP servers (openserverless, postgres, redis, milvus, mongodb, s3) installed in $MCP_BIN"

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
