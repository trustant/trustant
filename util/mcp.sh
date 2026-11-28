#!/usr/bin/env bash
# Copyright 2025-2026 Nuvolaris Inc
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU Affero General Public License as published
# by the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU Affero General Public License for more details.
#
# You should have received a copy of the GNU Affero General Public License
# along with this program.  If not, see <https://www.gnu.org/licenses/>.

# mcp.sh — connect local agents (Claude Code, Claude Desktop, Codex) to the
# Trustant MCP server of the running VM / host (spec/22-mcpserver.md).
#
# The token lives on THIS machine (token.mcp next to current.ip). It is
# generated when missing, pushed to the server with PUT /api/v1/token, and
# only then written into the agents' own config, so they never drift.
set -euo pipefail

# Always run from the repo root, wherever this is invoked from.
cd "$(dirname "$0")/.."

usage() {
  cat <<'USAGE'
Usage: ./util/mcp.sh <command> [options]

Commands:
  add       push the token to Trustant and configure the agents
  remove    remove the Trustant MCP entry from the agents
  refresh   generate a new token, push it, and reconfigure the agents
  show      print URL, token fingerprint, and what each agent has configured

Options:
  --claude           Claude Code (user scope)
  --claude-desktop   Claude Desktop (via mcp-remote; restart Desktop after)
  --codex            Codex (~/.codex/config.toml)
  --url URL          Trustant base URL (default: http://trustant.<vm-ip>.nip.io:8910)
  --token TOKEN      use and save this token instead of the stored/generated one
  --dry-run          print what would be done, change nothing
  -h, --help         this help

Without agent flags every agent found on this machine is configured.
USAGE
}

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
ok()   { echo -e "${GREEN}✓ $1${NC}"; }
warn() { echo -e "${YELLOW}⚠ $1${NC}"; }
fail() { echo -e "${RED}✗ $1${NC}" >&2; exit 1; }

CMD="${1:-}"
[[ -n "$CMD" ]] || { usage; exit 0; }
shift || true
case "$CMD" in
  add|remove|refresh|show) ;;
  -h|--help) usage; exit 0 ;;
  *) usage; exit 1 ;;
esac

WANT_CLAUDE=false; WANT_DESKTOP=false; WANT_CODEX=false
URL_ARG=""; TOKEN_ARG=""; DRY=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --claude) WANT_CLAUDE=true ;;
    --claude-desktop) WANT_DESKTOP=true ;;
    --codex) WANT_CODEX=true ;;
    --url) URL_ARG="${2:?--url needs a value}"; shift ;;
    --token) TOKEN_ARG="${2:?--token needs a value}"; shift ;;
    --dry-run) DRY=true ;;
    -h|--help) usage; exit 0 ;;
    *) fail "unknown option: $1" ;;
  esac
  shift
done

# --- Where things live ------------------------------------------------------
if [[ "$(uname -s)" == "Darwin" ]]; then
  SUPPORT_DIR="$HOME/Library/Application Support/Trustant"
  DESKTOP_CONFIG="$HOME/Library/Application Support/Claude/claude_desktop_config.json"
else
  SUPPORT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/trustant"
  DESKTOP_CONFIG="${XDG_CONFIG_HOME:-$HOME/.config}/Claude/claude_desktop_config.json"
fi
TOKEN_FILE="$SUPPORT_DIR/token.mcp"
CODEX_CONFIG="${CODEX_HOME:-$HOME/.codex}/config.toml"
NAME="trustant"
CODEX_BEGIN="# >>> trustant mcp >>>"
CODEX_END="# <<< trustant mcp <<<"

# --- Base URL ---------------------------------------------------------------
# Same resolution as run.sh: the address start.sh recorded, else 127.0.0.1
# (WSL localhost forwarding, or a browser on the k3s host itself).
if [[ -n "$URL_ARG" ]]; then
  BASE="${URL_ARG%/}"
  BASE="${BASE%/mcp}"
else
  IP="$(tr -d '[:space:]' 2>/dev/null < "$SUPPORT_DIR/current.ip" || true)"
  [[ -n "$IP" ]] || IP="127.0.0.1"
  BASE="http://trustant.${IP}.nip.io:8910"
fi
MCP_URL="$BASE/mcp"

# --- Agents -----------------------------------------------------------------
has_claude()  { command -v claude >/dev/null 2>&1; }
has_desktop() { [[ -d "$(dirname "$DESKTOP_CONFIG")" ]]; }
has_codex()   { command -v codex >/dev/null 2>&1 || [[ -d "$(dirname "$CODEX_CONFIG")" ]]; }

if ! $WANT_CLAUDE && ! $WANT_DESKTOP && ! $WANT_CODEX; then
  has_claude  && WANT_CLAUDE=true
  has_desktop && WANT_DESKTOP=true
  has_codex   && WANT_CODEX=true
  if [[ "$CMD" != "show" ]] && ! $WANT_CLAUDE && ! $WANT_DESKTOP && ! $WANT_CODEX; then
    fail "no agent found (claude, Claude Desktop, codex); pass --claude/--claude-desktop/--codex"
  fi
fi

run() {
  if $DRY; then printf '[dry-run]'; printf ' %q' "$@"; echo; else "$@"; fi
}

# --- Token ------------------------------------------------------------------
new_token() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
  else
    od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
  fi
}

# Length is checked separately: macOS regex caps repetition counts at 255.
valid_token() { [[ ${#1} -ge 32 && ${#1} -le 256 && "$1" =~ ^[A-Za-z0-9_-]+$ ]]; }

save_token() {
  if $DRY; then echo "[dry-run] write token to $TOKEN_FILE"; return; fi
  mkdir -p "$SUPPORT_DIR"
  ( umask 077; printf '%s\n' "$1" > "$TOKEN_FILE.tmp" )
  mv "$TOKEN_FILE.tmp" "$TOKEN_FILE"
}

read_token() { tr -d '[:space:]' 2>/dev/null < "$TOKEN_FILE" || true; }

fingerprint() {
  if command -v sha256sum >/dev/null 2>&1; then printf '%s' "$1" | sha256sum | cut -c1-8
  else printf '%s' "$1" | shasum -a 256 | cut -c1-8; fi
}

# put_token NEW [CURRENT]: CURRENT is the bearer, needed when the server has
# local login enabled (it proves we own the token being replaced).
put_token() {
  local new="$1" current="${2:-$1}" code body
  if $DRY; then echo "[dry-run] PUT $BASE/api/v1/token (fingerprint $(fingerprint "$new"))"; return; fi
  body="$(mktemp)"
  code="$(curl -sS -o "$body" -w '%{http_code}' --max-time 15 -X PUT \
    -H 'Content-Type: application/json' -H "Authorization: Bearer $current" \
    --data "{\"token\":\"$new\"}" "$BASE/api/v1/token" 2>&1)" || {
      rm -f "$body"; fail "Trustant is not answering at $BASE (is it running? use --url)"; }
  if [[ "$code" != "200" ]]; then
    local msg; msg="$(cat "$body")"; rm -f "$body"
    if [[ "$code" == "401" ]]; then
      fail "Trustant refused the token ($msg). Local login is enabled: run 'refresh' from a machine holding the current token, or set it from the UI."
    fi
    fail "PUT $BASE/api/v1/token failed: HTTP $code $msg"
  fi
  rm -f "$body"
  ok "token pushed to $BASE (fingerprint $(fingerprint "$new"))"
}

# --- Claude Code ------------------------------------------------------------
claude_add() {
  has_claude || { warn "claude CLI not found, skipping Claude Code"; return; }
  run claude mcp remove --scope user "$NAME" >/dev/null 2>&1 || true
  run claude mcp add --scope user --transport http "$NAME" "$MCP_URL" --header "Authorization: Bearer $1"
  $DRY || ok "Claude Code: $NAME -> $MCP_URL"
}

claude_remove() {
  has_claude || return 0
  run claude mcp remove --scope user "$NAME" >/dev/null 2>&1 || true
  $DRY || ok "Claude Code: $NAME removed"
}

# --- Claude Desktop ---------------------------------------------------------
# Desktop's config file only launches local commands, so a remote server goes
# through the mcp-remote bridge. The header value is passed via env because
# a space inside an argument is mangled by some launchers (notably Windows).
desktop_add() {
  command -v jq >/dev/null 2>&1 || { warn "jq not found, skipping Claude Desktop"; return; }
  local allow=() tmp
  [[ "$MCP_URL" == http://* ]] && allow=(--allow-http)
  local args
  args="$(printf '%s\n' -y mcp-remote "$MCP_URL" --header 'Authorization:${TRUSTANT_AUTH_HEADER}' "${allow[@]}" | jq -R . | jq -s .)"
  if $DRY; then echo "[dry-run] set mcpServers.$NAME in $DESKTOP_CONFIG: npx $(echo "$args" | jq -r 'join(" ")')"; return; fi
  mkdir -p "$(dirname "$DESKTOP_CONFIG")"
  [[ -s "$DESKTOP_CONFIG" ]] || echo '{}' > "$DESKTOP_CONFIG"
  cp "$DESKTOP_CONFIG" "$DESKTOP_CONFIG.bak"
  tmp="$(mktemp)"
  jq --arg name "$NAME" --argjson args "$args" --arg hdr "Bearer $1" \
    '.mcpServers = (.mcpServers // {}) | .mcpServers[$name] = {command: "npx", args: $args, env: {TRUSTANT_AUTH_HEADER: $hdr}}' \
    "$DESKTOP_CONFIG" > "$tmp" || { rm -f "$tmp"; fail "could not update $DESKTOP_CONFIG"; }
  chmod 600 "$tmp"; mv "$tmp" "$DESKTOP_CONFIG"
  ok "Claude Desktop: $NAME -> $MCP_URL (restart Claude Desktop; backup in $(basename "$DESKTOP_CONFIG").bak)"
}

desktop_remove() {
  [[ -f "$DESKTOP_CONFIG" ]] && command -v jq >/dev/null 2>&1 || return 0
  if $DRY; then echo "[dry-run] delete mcpServers.$NAME from $DESKTOP_CONFIG"; return; fi
  local tmp; tmp="$(mktemp)"
  cp "$DESKTOP_CONFIG" "$DESKTOP_CONFIG.bak"
  jq --arg name "$NAME" 'if .mcpServers then .mcpServers |= del(.[$name]) else . end' "$DESKTOP_CONFIG" > "$tmp" && mv "$tmp" "$DESKTOP_CONFIG"
  ok "Claude Desktop: $NAME removed"
}

# --- Codex ------------------------------------------------------------------
# A managed block in config.toml with a static Authorization header. Any
# earlier trustant entry (marked or added by `codex mcp add`) is removed first.
codex_strip() {
  [[ -f "$CODEX_CONFIG" ]] || return 0
  local tmp; tmp="$(mktemp)"
  awk -v b="$CODEX_BEGIN" -v e="$CODEX_END" '
    $0 == b { skip = 1; next }
    $0 == e { skip = 0; next }
    !skip' "$CODEX_CONFIG" > "$tmp" && mv "$tmp" "$CODEX_CONFIG"
  if command -v codex >/dev/null 2>&1; then
    codex mcp remove "$NAME" >/dev/null 2>&1 || true
    # codex rewrites the file and can leave a stray marker behind.
    tmp="$(mktemp)"
    grep -vxF -e "$CODEX_BEGIN" -e "$CODEX_END" "$CODEX_CONFIG" > "$tmp" || true
    mv "$tmp" "$CODEX_CONFIG"
  fi
}

codex_add() {
  local block
  block="$(printf '%s\n[mcp_servers.%s]\nurl = "%s"\nhttp_headers = { Authorization = "Bearer %s" }\n%s\n' \
    "$CODEX_BEGIN" "$NAME" "$MCP_URL" "$1" "$CODEX_END")"
  if $DRY; then echo "[dry-run] write to $CODEX_CONFIG:"; echo "${block//Bearer $1/Bearer <token>}"; return; fi
  mkdir -p "$(dirname "$CODEX_CONFIG")"
  touch "$CODEX_CONFIG"
  codex_strip
  [[ -s "$CODEX_CONFIG" && -n "$(tail -c1 "$CODEX_CONFIG")" ]] && echo >> "$CODEX_CONFIG"
  printf '%s\n' "$block" >> "$CODEX_CONFIG"
  chmod 600 "$CODEX_CONFIG"
  ok "Codex: $NAME -> $MCP_URL"
}

codex_remove() {
  if $DRY; then echo "[dry-run] remove $NAME from $CODEX_CONFIG"; return; fi
  codex_strip
  ok "Codex: $NAME removed"
}

configure_all() {
  $WANT_CLAUDE  && claude_add "$1"
  $WANT_DESKTOP && desktop_add "$1"
  $WANT_CODEX   && codex_add "$1"
  return 0
}

# --- Commands ---------------------------------------------------------------
case "$CMD" in
  add)
    TOKEN="$TOKEN_ARG"
    [[ -n "$TOKEN" ]] || TOKEN="$(read_token)"
    if [[ -z "$TOKEN" ]]; then
      TOKEN="$(new_token)"
      ok "generated a new token in $TOKEN_FILE"
    fi
    valid_token "$TOKEN" || fail "token must be 32-256 characters of [A-Za-z0-9_-]"
    save_token "$TOKEN"
    put_token "$TOKEN"
    configure_all "$TOKEN"
    ;;
  refresh)
    OLD="$(read_token)"
    TOKEN="${TOKEN_ARG:-$(new_token)}"
    valid_token "$TOKEN" || fail "token must be 32-256 characters of [A-Za-z0-9_-]"
    put_token "$TOKEN" "${OLD:-$TOKEN}"
    save_token "$TOKEN"
    configure_all "$TOKEN"
    ;;
  remove)
    $WANT_CLAUDE  && claude_remove
    $WANT_DESKTOP && desktop_remove
    $WANT_CODEX   && codex_remove
    ;;
  show)
    TOKEN="$(read_token)"
    echo "MCP URL:      $MCP_URL"
    echo "Token file:   $TOKEN_FILE"
    if [[ -n "$TOKEN" ]]; then echo "Fingerprint:  $(fingerprint "$TOKEN") (local)"; else echo "Fingerprint:  - (no local token: run add)"; fi
    SERVER="$(curl -sS --max-time 5 "$BASE/api/v1/token" 2>/dev/null | sed -n 's/.*"fingerprint":"\([0-9a-f]*\)".*/\1/p' || true)"
    echo "Server:       ${SERVER:-not answering at $BASE}"
    if has_claude; then
      if claude mcp get "$NAME" >/dev/null 2>&1; then echo "Claude Code:  configured"; else echo "Claude Code:  not configured"; fi
    fi
    if [[ -f "$DESKTOP_CONFIG" ]] && command -v jq >/dev/null 2>&1; then
      if jq -e --arg n "$NAME" '.mcpServers[$n]' "$DESKTOP_CONFIG" >/dev/null 2>&1; then echo "Claude Desktop: configured"; else echo "Claude Desktop: not configured"; fi
    fi
    if [[ -f "$CODEX_CONFIG" ]]; then
      if grep -q "^\[mcp_servers\.$NAME\]" "$CODEX_CONFIG"; then echo "Codex:        configured"; else echo "Codex:        not configured"; fi
    fi
    ;;
esac
