#!/bin/bash
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
[[ "$OS" == "darwin" ]] && RC_FILES+=("$HOME/.zshrc")

# Snapshot the shell's PATH before add_to_path mutates it, so step 13 can
# check what the user's environment actually has, not our in-process changes.
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

for v in OLLAMA_VERSION OPENCODE_VERSION PNPM_VERSION NODE_VERSION OPS_BRANCH OPS_REPO; do
  val=$(read_arg "$v")
  [[ -n "$val" ]] || fail "ARG $v not found in image/Dockerfile"
  export "$v=$val"
  ok "$v=$val"
done

# --- 1. Check .env and .env.dist, load .env, check WORKSPACE_DIR/WORKBENCH_DIR ---
echo "--- Checking .env ---"
[[ -f .env ]] || fail ".env file not found. Create it based on .env.dist."
[[ -f .env.dist ]] || fail ".env.dist not found"

while IFS='=' read -r key _; do
  [[ -z "$key" || "$key" =~ ^# ]] && continue
  key=$(echo "$key" | xargs)
  val=$(grep "^${key}=" .env | cut -d'=' -f2-)
  if [[ -z "$val" || "$val" == "<"*">" ]]; then
    fail "Variable $key is not set in .env (still has placeholder or is empty)"
  fi
done < .env.dist
ok ".env is present and all variables from .env.dist are set"

set -a
source ./.env
set +a

WORKSPACE_DIR_EXPANDED=$(eval echo "${WORKSPACE_DIR}")
[[ -d "${WORKSPACE_DIR_EXPANDED}" ]] || fail "WORKSPACE_DIR '${WORKSPACE_DIR}' does not exist"
ok "WORKSPACE_DIR exists: ${WORKSPACE_DIR_EXPANDED}"

WORKBENCH_DIR_EXPANDED=$(eval echo "${WORKBENCH_DIR}")
[[ -d "${WORKBENCH_DIR_EXPANDED}" ]] || fail "WORKBENCH_DIR '${WORKBENCH_DIR}' does not exist"
ok "WORKBENCH_DIR exists: ${WORKBENCH_DIR_EXPANDED}"

# --- 2. Check ops is in PATH and OPS_REPO/OPS_BRANCH match Dockerfile values ---
echo "--- Checking ops ---"
if ! command -v ops &>/dev/null; then
  warn "ops not found in PATH"
  warn "Set these and install ops:"
  warn "  export OPS_REPO=${OPS_REPO}"
  warn "  export OPS_BRANCH=${OPS_BRANCH}"
  warn "  curl -sL n7s.co/get-ops | bash"
  fail "ops is not installed"
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
  warn "Recommend: export OPS_REPO=${OPS_REPO} and reinstall ops (curl -sL n7s.co/get-ops | bash)"
  fail "OPS_REPO mismatch"
fi
if [[ "$OPS_BRANCH_ACTUAL" != "$OPS_BRANCH" ]]; then
  warn "ops OPS_BRANCH is '${OPS_BRANCH_ACTUAL}', expected '${OPS_BRANCH}'"
  warn "Recommend: export OPS_BRANCH=${OPS_BRANCH} and reinstall ops (curl -sL n7s.co/get-ops | bash)"
  fail "OPS_BRANCH mismatch"
fi
ok "ops is installed with correct OPS_REPO and OPS_BRANCH"

# --- 3. Add ~/.ops/<os>-<arch>/bin to PATH and check bun, uv ---
echo "--- Checking ops bin tools (bun, uv) ---"
add_to_path "$HOME/.ops/${OS}-${ARCH}/bin"

command -v bun &>/dev/null || fail "bun not found in PATH"
ok "bun is available"

command -v uv &>/dev/null || fail "uv not found in PATH"
ok "uv is available"

# --- 4. Check Go (install via g if missing), activate version from go.mod, install air ---
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

echo "--- Checking air ---"
if ! command -v air &>/dev/null; then
  warn "air not found, installing..."
  go install github.com/air-verse/air@latest
  add_to_path "$(go env GOPATH)/bin"
fi
command -v air &>/dev/null || fail "air installation failed"
ok "air is available"

# --- 5. Install pnpm if missing ---
echo "--- Checking pnpm ---"
if ! command -v pnpm &>/dev/null; then
  warn "pnpm not found, installing..."
  curl -fsSL https://get.pnpm.io/install.sh | env PNPM_VERSION=${PNPM_VERSION} bash || fail "pnpm install failed"
  set +eu; source "$HOME/.bashrc"; set -eu
  pnpm runtime set node "${NODE_VERSION}" || fail "pnpm runtime set node ${NODE_VERSION} failed"
fi
command -v pnpm &>/dev/null || fail "pnpm not in PATH after install"
ok "pnpm is available"

# --- 6. Reach OpenWhisk ---
echo "--- Locating OpenWhisk apihost ---"
APIHOST_ENV="${APIHOST:-}"
APIHOST=""
if [[ -n "${OPS_APIHOST:-}" ]]; then
  APIHOST="$OPS_APIHOST"
elif [[ -n "$APIHOST_ENV" ]]; then
  APIHOST="$APIHOST_ENV"
elif [[ -n "${TRUSTABLE_DEFAULT_APIHOST:-}" ]]; then
  APIHOST="$TRUSTABLE_DEFAULT_APIHOST"
elif [[ "$OS" == "darwin" && -f "$HOME/Library/Application Support/Trustable/apihost" ]]; then
  APIHOST="$(cat "$HOME/Library/Application Support/Trustable/apihost")"
elif [[ "$OS" == "msys" || "$OS" == "cygwin" || "$OS" == mingw* ]] && [[ -f "${LOCALAPPDATA:-}/Trustable/apihost" ]]; then
  APIHOST="$(cat "${LOCALAPPDATA}/Trustable/apihost")"
else
  APIHOST="http://miniops.me"
fi
APIHOST="${APIHOST%/}"

echo "Using apihost: $APIHOST"
WHISK_DESC=$(curl -sL "${APIHOST}/api/info" | jq -r '.description' 2>/dev/null) || true
[[ "$WHISK_DESC" == "OpenWhisk" ]] || fail "Cannot reach OpenWhisk at ${APIHOST}/api/info (got: ${WHISK_DESC:-no response})"
ok "OpenWhisk reachable at ${APIHOST}"

# --- 7. Extract kubeconfig (mac only, when id_ed25519 is present) ---
if [[ "$OS" == "darwin" ]]; then
  ID_FILE="$HOME/Library/Application Support/Trustable/id_ed25519"
  IP_FILE="$HOME/Library/Application Support/Trustable/current.ip"
  if [[ -f "$ID_FILE" ]]; then
    echo "--- Extracting kubeconfig ---"
    mkdir -p "$HOME/.ops/tmp"
    IP="$(cat "$IP_FILE")"
    ./ssh.sh sudo cat /etc/rancher/k3s/k3s.yaml \
      | sed -e "/server:/ s/127.0.0.1/$IP/" \
      > "$HOME/.ops/tmp/kubeconfig" \
      || fail "Failed to extract kubeconfig"
    ok "kubeconfig written to ~/.ops/tmp/kubeconfig"
  fi
fi

# --- 8. Check admin power ---
echo "--- Checking admin access ---"
ops admin listuser &>/dev/null || fail "No administrative power (ops admin listuser failed)"
ok "Admin access confirmed"

# --- 9. Check opencode version matches OPENCODE_VERSION, install if needed ---
echo "--- Checking opencode ---"
install_opencode() {
  curl -fsSL https://opencode.ai/install >opencode.sh
  bash opencode.sh --version "${OPENCODE_VERSION}" || fail "opencode install failed"
  add_to_path "$HOME/.opencode/bin"
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

# --- 10. Check kubefwd is in PATH ---
echo "--- Checking kubefwd ---"
command -v kubefwd &>/dev/null || fail "kubefwd not found in PATH (it is an error if missing)"
ok "kubefwd is available"

# --- 11. Check CLI tools are installed and in PATH (ask to install via brew/pipx) ---
echo "--- Checking CLI tools (kubefwd, rclone, psql, redis-cli, milvus_cli) ---"
check_cli() {
  local cmd="$1" brew_pkg="$2" pipx_pkg="$3"
  if command -v "$cmd" &>/dev/null; then
    ok "$cmd is available"
    return
  fi
  warn "$cmd not found in PATH"
  if [[ -n "$brew_pkg" ]]; then
    warn "  install with: brew install ${brew_pkg}"
  fi
  if [[ -n "$pipx_pkg" ]]; then
    warn "  or with: pipx install ${pipx_pkg}"
  fi
  fail "$cmd is required"
}

check_cli kubefwd   kubefwd          ""
check_cli rclone    rclone           ""
check_cli psql      libpq            ""
check_cli redis-cli redis            ""
check_cli milvus_cli ""              milvus-cli

# --- 12. Run image/setup_lsp_mcp.sh ---
echo "--- Running image/setup_lsp_mcp.sh ---"
[[ -f image/setup_lsp_mcp.sh ]] || fail "image/setup_lsp_mcp.sh not found"
bash image/setup_lsp_mcp.sh || fail "image/setup_lsp_mcp.sh failed"
ok "LSP/MCP tools installed"

# --- 13. Ensure ~/.local/bin is the first entry in PATH ---
echo "--- Checking ~/.local/bin is first in PATH ---"
LOCAL_BIN="$HOME/.local/bin"
FIRST_ENTRY="${ORIGINAL_PATH%%:*}"
if [[ "$FIRST_ENTRY" == "$LOCAL_BIN" ]]; then
  ok "~/.local/bin is the first entry in PATH"
else
  warn "~/.local/bin is not the first entry in PATH (first is: ${FIRST_ENTRY})"
  warn "Prepend it by adding to your shell rc: export PATH=\"\$HOME/.local/bin:\$PATH\""
fi

echo ""
echo -e "${GREEN}=== Setup complete! ===${NC}"
echo "Restart your shell or run: source ~/.bashrc"
[[ "$OS" == "darwin" ]] && echo "  or: source ~/.zshrc"
