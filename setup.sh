#!/bin/bash
#
# setup.sh — recreate the image/Dockerfile environment INSIDE the trudev VM.
#
# Runs as the mirrored guest user inside the Ubuntu VM created by ./start.sh
# (invoke via `./ssh.sh ./setup.sh` or from a login shell: `limactl shell trudev`).
# Everything is installed for the local user (~/.local/bin, ~/.local/lib),
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

# WHY: source identity is owned by the image contract even in development.
# Reading the same pinned fork here keeps clean VM and pod installations equal.
for v in OLLAMA_VERSION OPS_BRANCH OPS_REPO MILVUS_MCP_REPO MILVUS_MCP_REF GH_VERSION GH_SHA_AMD64 GH_SHA_ARM64 OP_VERSION OP_SHA_AMD64 OP_SHA_ARM64; do
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

# `g` persists both its own bin directory and the active Go toolchain in this
# file. Non-interactive Lima/WSL shells do not source it automatically, so load
# it before deciding whether either executable must be installed again.
if [[ -s "$HOME/.g/env" ]]; then
  set +u
  # shellcheck disable=SC1090
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

# Repository-root setup owns the persistent npm policy for Lima/WSL. WHY: the
# nested TruACP installer must consume one caller-selected prefix instead of
# independently falling back to a second location and making PATH resolution
# depend on which installer ran first.
NPM_DEFAULT_PREFIX="$HOME/.npm-global"
NPM_CONFIGURED_PREFIX="${NPM_CONFIG_PREFIX:-}"
if [[ -z "$NPM_CONFIGURED_PREFIX" ]]; then
  NPM_CONFIGURED_PREFIX="$(npm config get prefix 2>/dev/null || true)"
fi

npm_prefix_is_compatible() {
  local prefix="$1"
  [[ -n "$prefix" && "$prefix" == /* ]] || return 1
  mkdir -p "$prefix/bin" "$prefix/lib/node_modules" 2>/dev/null || return 1
  [[ "$(stat -c '%u' "$prefix")" == "$(id -u)" ]] || return 1
  [[ -w "$prefix" && -w "$prefix/bin" && -w "$prefix/lib/node_modules" ]]
}

if npm_prefix_is_compatible "$NPM_CONFIGURED_PREFIX"; then
  NPM_GLOBAL_PREFIX="$NPM_CONFIGURED_PREFIX"
else
  if [[ -n "$NPM_CONFIGURED_PREFIX" && "$NPM_CONFIGURED_PREFIX" != "$NPM_DEFAULT_PREFIX" ]]; then
    warn "npm prefix '$NPM_CONFIGURED_PREFIX' is not an absolute user-owned writable directory; using $NPM_DEFAULT_PREFIX"
  fi
  NPM_GLOBAL_PREFIX="$NPM_DEFAULT_PREFIX"
  npm_prefix_is_compatible "$NPM_GLOBAL_PREFIX" \
    || fail "cannot create writable npm prefix $NPM_GLOBAL_PREFIX without sudo"
fi

NPM_GLOBAL_BIN="$NPM_GLOBAL_PREFIX/bin"
export NPM_CONFIG_PREFIX="$NPM_GLOBAL_PREFIX"
npm config set prefix "$NPM_GLOBAL_PREFIX" --location=user \
  || fail "could not persist npm prefix $NPM_GLOBAL_PREFIX"
case ":$PATH:" in
  *":$NPM_GLOBAL_BIN:"*) ;;
  *) export PATH="$NPM_GLOBAL_BIN:$PATH" ;;
esac
hash -r
ok "npm global prefix: $NPM_GLOBAL_PREFIX"

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
# The 127.0.0.1 in k3s.yaml is already correct inside Lima/WSL. Select either
# standalone kubectl or the client bundled with k3s.
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

# setup.sh validates the local k3s API but must not reconfigure the VM resolver
# or restart systemd services. DNS policy belongs to the VM/k3s image; changing
# it here makes a repository setup unexpectedly mutate the host environment.

# start.sh owns installation for trudev; setup validates the same prerequisite
# explicitly so WSL/local-k3s fails before run.sh can leave a partial dev loop.
KUBEFWD_VERSION="1.25.16"
command -v kubefwd &>/dev/null \
  || fail "kubefwd ${KUBEFWD_VERSION} is required (trudev: run start.sh on macOS; WSL: install the pinned Linux release)"
KUBEFWD_INSTALLED_VERSION="$(
  kubefwd version 2>/dev/null \
    | grep -oE 'v?[0-9]+\.[0-9]+\.[0-9]+' | head -1 | sed 's/^v//' || true
)"
[[ "$KUBEFWD_INSTALLED_VERSION" == "$KUBEFWD_VERSION" ]] \
  || fail "kubefwd version mismatch: expected ${KUBEFWD_VERSION}, got ${KUBEFWD_INSTALLED_VERSION:-unknown}"
ok "kubefwd ${KUBEFWD_VERSION} is available"

# --- 9. Check admin power ---
echo "--- Checking admin access ---"
ops admin listuser &>/dev/null || fail "No administrative power (ops admin listuser failed)"
ok "Admin access confirmed"

# --- 10. Ensure the image's CLI tools are available (install any missing) ---
# The upstream Milvus CLI lives globally. WHY: ~/.local/bin/milvus_cli is the
# per-app auto-connect wrapper and must never remain a uv-managed symlink.
echo "--- Checking CLI tools (psql, redis-cli, rclone, lsof, milvus-cli, gh) ---"
APT_MISSING=()
command -v psql      &>/dev/null || APT_MISSING+=(postgresql-client-16)
command -v redis-cli &>/dev/null || APT_MISSING+=(redis-tools)
command -v rclone    &>/dev/null || APT_MISSING+=(rclone)
# Port recovery is part of the shared launch lifecycle. WHY: the production
# image and a clean VM must both reclaim an orphaned TruACP/Vite listener rather
# than depend on lsof happening to exist in a developer's base environment.
command -v lsof      &>/dev/null || APT_MISSING+=(lsof)
# The 1Password CLI ships as a zip, so unzip is needed to install it below.
command -v unzip     &>/dev/null || APT_MISSING+=(unzip)
if [[ ${#APT_MISSING[@]} -gt 0 ]]; then
  warn "installing missing apt packages: ${APT_MISSING[*]}"
  sudo apt-get update -qq || fail "apt-get update failed"
  sudo apt-get install -y "${APT_MISSING[@]}" || fail "apt-get install ${APT_MISSING[*]} failed"
fi
ok "psql, redis-cli, rclone, lsof, unzip available"

case "$ARCH" in
  amd64) GH_SHA="$GH_SHA_AMD64" ;;
  arm64) GH_SHA="$GH_SHA_ARM64" ;;
  *) fail "unsupported GitHub CLI architecture: $ARCH" ;;
esac
GH_INSTALLED_VERSION="$(gh --version 2>/dev/null | awk 'NR==1{print $3}' || true)"
if [[ "$GH_INSTALLED_VERSION" != "$GH_VERSION" ]]; then
  warn "installing verified GitHub CLI ${GH_VERSION}..."
  GH_TMP="$(mktemp -d)"
  GH_ARCHIVE="gh_${GH_VERSION}_linux_${ARCH}.tar.gz"
  curl -fsSL -o "$GH_TMP/$GH_ARCHIVE" \
    "https://github.com/cli/cli/releases/download/v${GH_VERSION}/${GH_ARCHIVE}" \
    || fail "GitHub CLI ${GH_VERSION} download failed"
  echo "$GH_SHA  $GH_TMP/$GH_ARCHIVE" | sha256sum -c - \
    || fail "GitHub CLI ${GH_VERSION} checksum verification failed"
  tar -C "$GH_TMP" -xzf "$GH_TMP/$GH_ARCHIVE" \
    || fail "GitHub CLI ${GH_VERSION} extraction failed"
  sudo install -m 0755 "$GH_TMP/gh_${GH_VERSION}_linux_${ARCH}/bin/gh" /usr/local/bin/gh \
    || fail "GitHub CLI ${GH_VERSION} installation failed"
  rm -rf "$GH_TMP"
fi
[[ "$(gh --version 2>/dev/null | awk 'NR==1{print $3}')" == "$GH_VERSION" ]] \
  || fail "GitHub CLI version mismatch after installation"
ok "GitHub CLI ${GH_VERSION} available globally in /usr/local/bin"

# --- 1Password CLI (op) ---
# WHY: cmd/trulicense signs licenses with the Ed25519 master key held in the
# TrustableLicenses vault and never on disk, so `op` is required to issue one.
# See spec/14-license.md.
case "$ARCH" in
  amd64) OP_SHA="$OP_SHA_AMD64" ;;
  arm64) OP_SHA="$OP_SHA_ARM64" ;;
  *) fail "unsupported 1Password CLI architecture: $ARCH" ;;
esac
OP_INSTALLED_VERSION="$(op --version 2>/dev/null | tr -d 'v' || true)"
if [[ "$OP_INSTALLED_VERSION" != "$OP_VERSION" ]]; then
  warn "installing verified 1Password CLI ${OP_VERSION}..."
  OP_TMP="$(mktemp -d)"
  OP_ARCHIVE="op_linux_${ARCH}_v${OP_VERSION}.zip"
  curl -fsSL -o "$OP_TMP/$OP_ARCHIVE" \
    "https://cache.agilebits.com/dist/1P/op2/pkg/v${OP_VERSION}/${OP_ARCHIVE}" \
    || fail "1Password CLI ${OP_VERSION} download failed"
  echo "$OP_SHA  $OP_TMP/$OP_ARCHIVE" | sha256sum -c - \
    || fail "1Password CLI ${OP_VERSION} checksum verification failed"
  unzip -q -o -d "$OP_TMP" "$OP_TMP/$OP_ARCHIVE" \
    || fail "1Password CLI ${OP_VERSION} extraction failed"
  sudo install -m 0755 "$OP_TMP/op" /usr/local/bin/op \
    || fail "1Password CLI ${OP_VERSION} installation failed"
  # op refuses to run set-gid to a group it is not in, so create the group and
  # apply both together — matching 1Password's own documented Linux install.
  sudo groupadd -f onepassword-cli || fail "creating the onepassword-cli group failed"
  sudo chgrp onepassword-cli /usr/local/bin/op || fail "setting the op group failed"
  sudo chmod g+s /usr/local/bin/op || fail "setting the op set-gid bit failed"
  rm -rf "$OP_TMP"
fi
[[ "$(op --version 2>/dev/null | tr -d 'v')" == "$OP_VERSION" ]] \
  || fail "1Password CLI version mismatch after installation"
ok "1Password CLI ${OP_VERSION} available globally in /usr/local/bin"

MILVUS_CLI_VERSION="1.2.1"
UV_BIN="$(command -v uv)"
MILVUS_CLI_INSTALLED_VERSION="$(
  sudo env UV_TOOL_DIR=/opt/uv/tools "$UV_BIN" tool list 2>/dev/null \
    | awk '$1 == "milvus-cli" { sub(/^v/, "", $2); print $2; exit }'
)"
if [[ "$MILVUS_CLI_INSTALLED_VERSION" != "$MILVUS_CLI_VERSION" ]] ||
   [[ ! -x /usr/local/bin/milvus_cli ]]; then
  warn "installing global milvus-cli ${MILVUS_CLI_VERSION}..."
  sudo env \
    UV_TOOL_BIN_DIR=/usr/local/bin \
    UV_TOOL_DIR=/opt/uv/tools \
    UV_CACHE_DIR=/opt/uv/cache \
    UV_PYTHON_PREFERENCE=only-system \
    UV_LINK_MODE=hardlink \
    "$UV_BIN" tool install --force --python /usr/bin/python3 "milvus-cli==${MILVUS_CLI_VERSION}" \
    || fail "global milvus-cli ${MILVUS_CLI_VERSION} install failed"
fi
[[ -x /usr/local/bin/milvus_cli ]] \
  || fail "global milvus_cli entry point missing at /usr/local/bin/milvus_cli"
ok "milvus-cli ${MILVUS_CLI_VERSION} available globally in /usr/local/bin"

# --- 11. Install the pi coding-agent toolchain (pinned by trustable-acp/pi.version) ---
# Same pin file the image stages beside the standalone TruACP setup.sh. Format:
# one literal npm install spec per line, `#` comments and blank lines ignored,
# every entry MUST carry a version.
echo "--- Installing the pi coding-agent toolchain ---"
PI_VERSIONS_FILE="trustable-acp/pi.version"
[[ -f "$PI_VERSIONS_FILE" ]] || fail "$PI_VERSIONS_FILE not found — it lists the packages to install"
PI_INTEGRITY_FILE="trustable-acp/pi.integrity"
[[ -f "$PI_INTEGRITY_FILE" ]] || fail "$PI_INTEGRITY_FILE not found — it pins the reviewed upstream Pi artifacts"

command -v npm &>/dev/null || fail "npm is required to install the pi toolchain"

# Strip comments and surrounding whitespace, drop blank lines.
mapfile -t PI_PACKAGES < <(
  sed -e 's/#.*//' -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' "$PI_VERSIONS_FILE" | grep -v '^$'
)
[[ ${#PI_PACKAGES[@]} -gt 0 ]] || fail "$PI_VERSIONS_FILE lists no packages"

# Every entry must be pinned; an unpinned spec would silently install latest and
# break reproducibility. The leading @ of a scoped name is stripped first so only
# a real version separator counts (`@scope/name` unpinned, `@scope/name@1.2.3` pinned).
for pkg in "${PI_PACKAGES[@]}"; do
  case "${pkg#@}" in
    *@*) ;;
    *) fail "$PI_VERSIONS_FILE: '$pkg' has no version — every entry must be pinned as <module>@<version>" ;;
  esac
done

while IFS= read -r integrity_line; do
  integrity_line="$(sed -e 's/#.*//' -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' <<<"$integrity_line")"
  [[ -n "$integrity_line" ]] || continue
  read -r spec expected_integrity extra <<<"$integrity_line"
  [[ -n "$spec" && -n "$expected_integrity" && -z "$extra" ]] \
    || fail "$PI_INTEGRITY_FILE contains an invalid entry: $integrity_line"
  printf '%s\n' "${PI_PACKAGES[@]}" | grep -Fqx "$spec" \
    || fail "$PI_INTEGRITY_FILE pins $spec, but pi.version does not install it"
  actual_integrity="$(npm view "$spec" dist.integrity)" \
    || fail "could not read registry integrity for $spec"
  [[ "$actual_integrity" == "$expected_integrity" ]] \
    || fail "integrity mismatch for $spec (expected $expected_integrity, received $actual_integrity)"
done <"$PI_INTEGRITY_FILE"

echo "Packages pinned by $PI_VERSIONS_FILE:"
for pkg in "${PI_PACKAGES[@]}"; do
  printf '  %-45s %s\n' "${pkg%@*}" "${pkg##*@}"
done

# Use the setup-owned npm prefix so the selected user location applies to every
# global npm install. --force lets a re-run overwrite bin links left by a
# previously-installed adapter.
# Run from $HOME so npm's git fetch does not stumble into this repo's submodules.
( cd "$HOME" && npm install -g --force --prefix "$NPM_GLOBAL_PREFIX" "${PI_PACKAGES[@]}" ) \
  || fail "npm install of the pi toolchain failed"

hash -r
# pi.version pins the upstream Pi CLI together with the other managed agent
# CLIs, so all three commands must exist before setup proceeds to MCP tooling.
command -v pi &>/dev/null || fail "pi not on PATH after install (expected $NPM_GLOBAL_BIN/pi)"
command -v claude &>/dev/null || fail "claude not on PATH after install (expected $NPM_GLOBAL_BIN/claude)"
command -v codex &>/dev/null || fail "codex not on PATH after install (expected $NPM_GLOBAL_BIN/codex)"
ok "pi toolchain installed (${#PI_PACKAGES[@]} pinned packages)"

# --- 12. Install MCP servers (openserverless, redis, milvus, postgres, mongodb, s3) ---
# Mirrors image/Dockerfile but for the local user (~/.local/bin, no /opt/uv/*).
echo "--- Installing MCP servers for local use ---"
MCP_BIN="$HOME/.local/bin"
mkdir -p "$MCP_BIN"

command -v uv &>/dev/null || fail "uv is required to install MCP servers"

# postgres, redis, milvus MCP servers via uv tool (same pins as the Dockerfile)
MILVUS_MCP_SPEC="git+${MILVUS_MCP_REPO}@${MILVUS_MCP_REF}"
MILVUS_MCP_RECEIPT="$(uv tool dir)/mcp-server-milvus/uv-receipt.toml"

# WHY the extra pins below: postgres-mcp and redis-mcp-server declare an open
# upper bound on the MCP SDK (`mcp[cli]>=1.5.0` / `>=1.9.4`), but both still
# import `mcp.server.fastmcp`, which mcp 2.x renamed to `mcp.server.mcpserver`.
# Left unpinned, uv resolves mcp 2.x and each server dies at import; OpenCode
# only sees the stdio pipe close and reports `-32000: Connection closed`.
# The interpreter is pinned for the same class of reason: postgres-mcp requires
# pglast==7.2.0, which publishes no cp313 wheel, so a default interpreter that
# has moved on to 3.13 silently yields an incompatible pglast.
UV_PYTHON_PIN=3.12
declare -a MCP_TOOL_SPECS=(
  "postgres-mcp==0.3.0|--with|mcp<2"
  "redis-mcp-server==0.5.0|--with|mcp<2"
  "$MILVUS_MCP_SPEC"
)
for spec in "${MCP_TOOL_SPECS[@]}"; do
  IFS='|' read -r -a spec_parts <<<"$spec"
  tool="${spec_parts[0]}"
  UV_INSTALL_ARGS=("${spec_parts[@]:1}")
  if [[ "$tool" == "$MILVUS_MCP_SPEC" ]]; then
    if [[ ! -f "$MILVUS_MCP_RECEIPT" ]] ||
       ! grep -Fq "$MILVUS_MCP_REPO" "$MILVUS_MCP_RECEIPT" ||
       ! grep -Fq "$MILVUS_MCP_REF" "$MILVUS_MCP_RECEIPT"; then
      # WHY: uv identifies tools by package name. Without --force, a VM carrying
      # the former upstream install can remain "already installed" after the
      # repository pin changes, even though its executable still resolves.
      UV_INSTALL_ARGS+=(--force)
    fi
  else
    # WHY --force unconditionally: uv treats these as "already installed" by
    # package name, so a VM holding the previously-resolved mcp 2.x environment
    # would never re-resolve against the constraint added above.
    UV_INSTALL_ARGS+=(--force)
  fi
  env \
    UV_TOOL_BIN_DIR="$MCP_BIN" \
    UV_LINK_MODE=hardlink \
    uv tool install --python "$UV_PYTHON_PIN" "${UV_INSTALL_ARGS[@]}" "$tool" \
    || fail "uv tool install $tool failed"
done
grep -Fq "$MILVUS_MCP_REPO" "$MILVUS_MCP_RECEIPT" &&
  grep -Fq "$MILVUS_MCP_REF" "$MILVUS_MCP_RECEIPT" \
  || fail "installed Milvus MCP does not match ${MILVUS_MCP_REPO}@${MILVUS_MCP_REF}"

# OpenServerless, MongoDB, and browser MCP servers via npm. Package the checked
# out sources so Lima/WSL runs exactly what the image build consumes; no guest
# Git metadata is needed, which also supports host-mounted worktrees.
command -v npm &>/dev/null || fail "npm is required to install the npm MCP servers"
[[ -f mcp/package.json ]] || fail "mcp submodule is not initialized (run ./start.sh on the host or: git submodule update --init mcp)"
OPENSERVERLESS_MCP_PACK_DIR=$(mktemp -d)
( cd mcp && npm pack --pack-destination "$OPENSERVERLESS_MCP_PACK_DIR" >/dev/null ) \
  || fail "packing local openserverless-mcp failed"
OPENSERVERLESS_MCP_PACKAGE=$(find "$OPENSERVERLESS_MCP_PACK_DIR" -maxdepth 1 -name 'openserverless-mcp-*.tgz' -print -quit)
[[ -n "$OPENSERVERLESS_MCP_PACKAGE" ]] || fail "local openserverless-mcp package was not created"
( cd "$HOME" && npm install -g --prefix "$NPM_GLOBAL_PREFIX" tsx "$OPENSERVERLESS_MCP_PACKAGE" mongodb-mcp-server@1.9.0 ) \
  || fail "npm install of openserverless-mcp/mongodb-mcp-server failed"
rm -rf "$OPENSERVERLESS_MCP_PACK_DIR"
grep -qF 'secret-unbind' "$NPM_GLOBAL_PREFIX/lib/node_modules/openserverless-mcp/src/index.ts" \
  || fail "installed openserverless-mcp does not match the checked-out source"

[[ -f browser-mcp/package.json ]] || fail "browser-mcp source is missing"
BROWSER_MCP_PACK_DIR=$(mktemp -d)
( cd browser-mcp && npm pack --pack-destination "$BROWSER_MCP_PACK_DIR" >/dev/null ) \
  || fail "packing trustable-browser-mcp failed"
BROWSER_MCP_PACKAGE=$(find "$BROWSER_MCP_PACK_DIR" -maxdepth 1 -name 'trustable-browser-mcp-*.tgz' -print -quit)
[[ -n "$BROWSER_MCP_PACKAGE" ]] || fail "trustable-browser-mcp package was not created"
( cd "$HOME" && npm install -g --prefix "$NPM_GLOBAL_PREFIX" tsx "$BROWSER_MCP_PACKAGE" ) \
  || fail "installing trustable-browser-mcp failed"
rm -rf "$BROWSER_MCP_PACK_DIR"
command -v trustable-browser-mcp &>/dev/null || fail "trustable-browser-mcp is not in PATH"

# Install the deterministic React analyzer separately from Agentic React. WHY:
# the latter captures UI selection context and cannot validate router/auth AST
# invariants required before the bounded Browser MCP flow.
[[ -f react-mcp/package.json ]] || fail "react-mcp source is missing"
REACT_MCP_PACK_DIR=$(mktemp -d)
( cd react-mcp && npm pack --pack-destination "$REACT_MCP_PACK_DIR" >/dev/null ) \
  || fail "packing trustable-react-mcp failed"
REACT_MCP_PACKAGE=$(find "$REACT_MCP_PACK_DIR" -maxdepth 1 -name 'trustable-react-mcp-*.tgz' -print -quit)
[[ -n "$REACT_MCP_PACKAGE" ]] || fail "trustable-react-mcp package was not created"
( cd "$HOME" && npm install -g --prefix "$NPM_GLOBAL_PREFIX" tsx "$REACT_MCP_PACKAGE" ) \
  || fail "installing trustable-react-mcp failed"
rm -rf "$REACT_MCP_PACK_DIR"
command -v trustable-react-mcp &>/dev/null || fail "trustable-react-mcp is not in PATH"
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
install -m 0755 image/redis-mcp "$MCP_BIN/trustable-redis-mcp" \
  || fail "installing Trustable Redis MCP wrapper failed"

# Smoke-check the uv-installed Python MCP servers. WHY: a broken transitive pin
# (see the mcp<2 note above) makes these die at *import*, long before any
# connection is attempted. That failure is otherwise invisible here and only
# surfaces later as a silent "7/9 servers connected" inside OpenCode. Importing
# the module entrypoint exercises the whole import graph without needing live
# service credentials, so this stays valid on a VM with no cluster access.
echo "--- Verifying Python MCP servers can be imported ---"
for probe in "postgres-mcp:postgres_mcp" "redis-mcp-server:src.main" "mcp-server-milvus:mcp_server_milvus"; do
  probe_tool="${probe%%:*}"
  probe_module="${probe#*:}"
  "$(uv tool dir)/${probe_tool}/bin/python" -c "import ${probe_module}" 2>/tmp/mcp-probe.$$ \
    || fail "${probe_tool} fails at import: $(tail -1 /tmp/mcp-probe.$$)"
done
rm -f /tmp/mcp-probe.$$
ok "postgres, redis and milvus MCP servers import cleanly"

ok "MCP servers (browser, react, openserverless, postgres, redis, milvus, mongodb, s3) installed in $MCP_BIN"

# --- 13. Build and install truacp (the ACP server that fronts `pi`) ---
# truacp serves its own React UI on :4096 and spawns the `pi` coding agent over
# stdio via the `pi-acp` adapter. Trustable launches it as
# `truacp --port <n> --dir <workbench>` (see spec/4-launch.md,
# trustable-acp/SPEC.md §10a). Its setup.sh bundles the server + embedded web UI,
# then installs the launcher at ~/.local/bin/truacp. Development setup builds
# inside the VM so project dependencies match that declared environment; image
# builds separately stage the resulting portable JavaScript bundle.
#
# trustable-acp/setup.sh owns this step: it (re)installs the pinned agents and
# integrity-verified upstream Pi packages from pi.version, builds the nested
# Trustable pi-acp fork, then,
# because a package.json is present in the working directory, builds and installs
# the ~/.local/bin/truacp launcher. It MUST be run from inside trustable-acp/:
# the build/install phases key off a package.json in the *current* directory, so
# invoking it from here would install the agents and skip the build entirely
# (trustable-acp/SPEC.md §10b).
# WHY: NPM_CONFIG_PREFIX is inherited here so the nested installer consumes the
# root policy; configuring another prefix inside the submodule would duplicate
# packages and make clean VM behavior differ from the image workflow.
echo "--- Building truacp ---"
[[ -f trustable-acp/package.json && -f trustable-acp/pi.version ]] \
  || fail "trustable-acp submodule is not initialized (run ./start.sh on the host or: git submodule update --init trustable-acp)"
[[ -f trustable-acp/pi-acp/package.json ]] \
  || fail "nested pi-acp fork is not initialized (run: git submodule update --init --recursive trustable-acp)"
[[ -x trustable-acp/setup.sh ]] || chmod +x trustable-acp/setup.sh
(cd trustable-acp && ./setup.sh) || fail "truacp build/install failed"
command -v truacp &>/dev/null || fail "truacp not on PATH after install (expected ~/.local/bin/truacp)"
ok "truacp installed ($(command -v truacp))"

# Ensure the shell PATH matches the image ordering, including BOTH the Go toolchain
# dir (GOROOT/bin — where `go` itself lives, via g) and the Go install bin dir
# (GOBIN/GOPATH-bin — where air lands), so a fresh shell finds `go` AND `air`.
# Omitting GOROOT/bin makes `go` vanish, which in turn hides air.
#
# WHY both files: Ubuntu's stock ~/.bashrc returns early for non-interactive
# shells, so a PATH line appended there is dead code under `bash -lc` (and under
# `ssh <host> <cmd>`) — that is how `pi`/`claude`/`codex` end up "installed but
# not found". ~/.profile is read by login shells regardless of interactivity, so
# it is the file that actually carries the toolchain. ~/.bashrc keeps the same
# ordering for interactive non-login shells, which never source ~/.profile.
GO_ROOT_BIN="$(go env GOROOT)/bin"
IMAGE_PATH="\$HOME/.local/bin:${NPM_GLOBAL_BIN}:\$HOME/.ops/linux-${ARCH}/bin:${GO_ROOT_BIN}:${GO_BIN}:/usr/local/bin:/usr/bin:/bin"
for shell_rc in "$HOME/.profile" "$HOME/.bashrc"; do
  if ! grep -qF "$IMAGE_PATH" "$shell_rc" 2>/dev/null; then
    echo "export PATH=\"$IMAGE_PATH\"" >> "$shell_rc"
    ok "added image PATH ordering to ${shell_rc/#$HOME/\~}"
  fi
done

# Note: per-app AGENTS.md / .openserverless-contract.md are written at launch by
# the Go binary, and skills come from OPS_SKILLS (default trustable-ai/skills)
# cloned at launch by skills.go — setup does nothing for these.

echo ""
echo -e "${GREEN}=== Setup complete! ===${NC}"
echo "Restart your shell or run: source ~/.profile"
echo "Then run ./run.sh inside the VM."
