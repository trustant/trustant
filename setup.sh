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

# --- 1. Check .env and .env.dist ---
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

# --- 2. Check ops is in PATH and OPS_REPO/OPS_BRANCH ---
echo "--- Checking ops ---"
if [[ "${OPS_REPO:-}" != "https://github.com/nuvolaris/bestia" ]]; then
  warn "OPS_REPO is '${OPS_REPO:-}', expected 'https://github.com/nuvolaris/bestia'"
  warn "Set: export OPS_REPO=https://github.com/nuvolaris/bestia"
  fail "OPS_REPO must be set before installing ops"
fi
if [[ "${OPS_BRANCH:-}" != "bestia" ]]; then
  warn "OPS_BRANCH is '${OPS_BRANCH:-}', expected 'bestia'"
  warn "Set: export OPS_BRANCH=bestia"
  fail "OPS_BRANCH must be set before installing ops"
fi

if ! command -v ops &>/dev/null; then
  warn "ops not found, installing..."
  if [[ "$OS" == "darwin" || "$OS" == "linux" ]]; then
    curl -sL n7s.co/get-ops | bash || fail "ops install failed"
  else
    powershell -c "irm n7s.co/get-ops | iex" || fail "ops install failed"
  fi
  add_to_path "$HOME/.ops/${OS}-${ARCH}/bin"
fi
command -v ops &>/dev/null || fail "ops still not in PATH after install"
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

# --- 5. Reach OpenWhisk ---
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
elif [[ "$OS" == "msys" || "$OS" == "cygwin" || "$OS" == mingw* ]] && [[ -f "${APPDATA:-}/Trustable/apihost" ]]; then
  APIHOST="$(cat "${APPDATA}/Trustable/apihost")"
else
  APIHOST="http://miniops.me"
fi
APIHOST="${APIHOST%/}"

echo "Using apihost: $APIHOST"
WHISK_DESC=$(curl -sf "${APIHOST}/api/info" | jq -r '.description' 2>/dev/null) || true
[[ "$WHISK_DESC" == "OpenWhisk" ]] || fail "Cannot reach OpenWhisk at ${APIHOST}/api/info (got: ${WHISK_DESC:-no response})"
ok "OpenWhisk reachable at ${APIHOST}"

# --- 6. Extract kubeconfig (mac only, when id_ed25519 is present) ---
if [[ "$OS" == "darwin" ]]; then
  ID_FILE="$HOME/Library/Application Support/Trustable/id_ed25519"
  IP_FILE="$HOME/Library/Application Support/Trustable/current.ip"
  if [[ -f "$ID_FILE" && -f "$IP_FILE" ]]; then
    echo "--- Extracting kubeconfig ---"
    mkdir -p "$HOME/.ops/tmp"
    IP="$(cat "$IP_FILE")"
    ssh -i "$ID_FILE" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
      "trustable@$IP" sudo cat /etc/rancher/k3s/k3s.yaml \
      | sed -e "/server:/ s/127.0.0.1/$IP/" \
      > "$HOME/.ops/tmp/kubeconfig" \
      || fail "Failed to extract kubeconfig from trustable@$IP"
    ok "kubeconfig written to ~/.ops/tmp/kubeconfig"
  fi
fi

# --- 7. Check admin power ---
echo "--- Checking admin access ---"
ops admin listuser &>/dev/null || fail "No administrative power (ops admin listuser failed)"
ok "Admin access confirmed"

# --- 8. Install opencode if missing ---
echo "--- Checking opencode ---"
if ! command -v opencode &>/dev/null; then
  warn "opencode not found, installing..."
  curl -fsSL https://opencode.ai/install | bash
  add_to_path "$HOME/.opencode/bin"
fi
command -v opencode &>/dev/null || fail "opencode installation failed"
ok "opencode is available"

echo ""
echo -e "${GREEN}=== Setup complete! ===${NC}"
echo "Restart your shell or run: source ~/.bashrc"
[[ "$OS" == "darwin" ]] && echo "  or: source ~/.zshrc"
