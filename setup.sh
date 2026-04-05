#!/bin/bash
set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

ok()   { echo -e "${GREEN}✓ $1${NC}"; }
warn() { echo -e "${YELLOW}⚠ $1${NC}"; }
fail() { echo -e "${RED}✗ $1${NC}"; exit 1; }

# Change to script directory
cd "$(dirname "$0")"

# Detect OS and arch
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
esac

# Determine shell rc files to update
RC_FILES=("$HOME/.bashrc")
if [[ "$OS" == "darwin" ]]; then
  RC_FILES+=("$HOME/.zshrc")
fi

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

# --- Step 1: Check .env ---
echo "--- Checking .env ---"
if [[ ! -f .env ]]; then
  fail ".env file not found. Please create it based on .env.dist and fill in the required variables."
fi

while IFS='=' read -r key _; do
  [[ -z "$key" || "$key" =~ ^# ]] && continue
  key=$(echo "$key" | xargs)
  val=$(grep "^${key}=" .env | cut -d'=' -f2-)
  if [[ -z "$val" || "$val" == "<"*">" ]]; then
    fail "Variable $key is not set in .env (still has placeholder or is empty)"
  fi
done < .env.dist
ok ".env is present and all variables from .env.dist are set"

# Source .env for later checks
set -a
source ./.env
set +a

if [[ ! -d "${WORKSPACE_DIR}" ]]; then
  fail "WORKSPACE_DIR '${WORKSPACE_DIR}' does not exist or is not a directory"
fi
ok "WORKSPACE_DIR exists: ${WORKSPACE_DIR}"

# --- Step 2: Check OpenWhisk ---
echo "--- Checking OpenWhisk ---"
WHISK_DESC=$(curl -sf http://miniops.me/api/info | jq -r '.description' 2>/dev/null) || true
if [[ "$WHISK_DESC" != "OpenWhisk" ]]; then
  fail "Cannot reach OpenWhisk at http://miniops.me/api/info (got: ${WHISK_DESC:-no response})"
fi
ok "OpenWhisk is reachable"

# --- Step 3: Check admin power on minions ---
echo "--- Checking admin access ---"
if ! ops admin listuser &>/dev/null; then
  fail "You do not have administrative power on minions (ops admin listuser failed)"
fi
ok "Admin access confirmed (ops admin listuser)"

# --- Step 4: Check ops ---
echo "--- Checking ops ---"
if ! command -v ops &>/dev/null; then
  fail "ops is not in the PATH. Install it with: curl -sL n7s.co/get-ops | bash"
fi

if [[ "${OPS_REPO:-}" != "https://github.com/nuvolaris/bestia" ]]; then
  fail "OPS_REPO is '${OPS_REPO:-}', expected 'https://github.com/nuvolaris/bestia'"
fi
if [[ "${OPS_BRANCH:-}" != "bestia" ]]; then
  fail "OPS_BRANCH is '${OPS_BRANCH:-}', expected 'bestia'"
fi
ok "ops is installed with correct OPS_REPO and OPS_BRANCH"

# --- Step 5: Check OpenAI and Ollama models ---
echo "--- Checking OpenAI models ---"
if [[ -z "${OPENAI_BASE_URL:-}" || -z "${OPENAI_API_KEY:-}" ]]; then
  fail "OPENAI_BASE_URL or OPENAI_API_KEY not set in .env"
fi

MODELS_JSON=$(curl -sf "${OPENAI_BASE_URL}/models" \
  -H "Authorization: Bearer ${OPENAI_API_KEY}" 2>/dev/null) || fail "Cannot reach OpenAI endpoint at ${OPENAI_BASE_URL}/models"

AVAILABLE_MODELS=$(echo "$MODELS_JSON" | jq -r '.data[].id' 2>/dev/null) || fail "Failed to parse models response"

echo "$AVAILABLE_MODELS"
ok "OpenAI endpoint is reachable and models are available"

# Check Ollama models
echo "--- Checking Ollama models ---"
if [[ -z "${OLLAMA_ENDPOINT:-}" ]]; then
  fail "OLLAMA_ENDPOINT not set in .env"
fi

OLLAMA_MODELS=$(curl -sf "${OLLAMA_ENDPOINT}/api/tags" 2>/dev/null) || fail "Cannot reach Ollama at ${OLLAMA_ENDPOINT}/api/tags"
OLLAMA_MODEL_LIST=$(echo "$OLLAMA_MODELS" | jq -r '.models[].name' 2>/dev/null) || fail "Failed to parse Ollama models response"

echo "$OLLAMA_MODEL_LIST"
ok "Ollama endpoint is reachable and models are available"

# --- Step 6: Add ops bin to PATH and check bun ---
echo "--- Checking bun ---"
OPS_BIN="$HOME/.ops/${OS}-${ARCH}/bin"
add_to_path "$OPS_BIN"

if ! command -v bun &>/dev/null; then
  fail "bun not found in PATH (expected in $OPS_BIN)"
fi
ok "bun is available"

# --- Step 7: Check/install opencode ---
echo "--- Checking opencode ---"
OPENCODE_BIN="$HOME/.opencode/bin"
if ! command -v opencode &>/dev/null; then
  warn "opencode not found, installing..."
  curl -fsSL https://opencode.ai/install | bash
  add_to_path "$OPENCODE_BIN"
fi
if ! command -v opencode &>/dev/null; then
  fail "opencode installation failed"
fi
ok "opencode is available"

# --- Step 8: Check/install Go via g ---
echo "--- Checking Go ---"
GO_VERSION=$(grep '^go ' go.mod | awk '{print $2}')

if ! command -v go &>/dev/null; then
  warn "go not found, installing g (Go version manager)..."
  curl -sSL https://raw.githubusercontent.com/voidint/g/master/install.sh | bash
  add_to_path "$HOME/go/bin"
  add_to_path "$HOME/.g/go/bin"
fi

if ! command -v g &>/dev/null; then
  add_to_path "$HOME/.g"
  export GOROOT="$HOME/.g/go"
  export PATH="$GOROOT/bin:$PATH"
fi

if ! command -v g &>/dev/null; then
  fail "g (Go version manager) not found after installation"
fi

# Install the Go version from go.mod
if ! go version 2>/dev/null | grep -qF "go${GO_VERSION}"; then
  warn "Installing Go ${GO_VERSION}..."
  g install "$GO_VERSION"
fi
ok "Go ${GO_VERSION} is available"

# Install air
echo "--- Checking air ---"
if ! command -v air &>/dev/null; then
  warn "air not found, installing..."
  go install github.com/air-verse/air@latest
  add_to_path "$(go env GOPATH)/bin"
fi
if ! command -v air &>/dev/null; then
  fail "air installation failed"
fi
ok "air is available"

echo ""
echo -e "${GREEN}=== Setup complete! ===${NC}"
echo "You may need to restart your shell or run: source ~/.bashrc"
[[ "$OS" == "darwin" ]] && echo "  or: source ~/.zshrc"
