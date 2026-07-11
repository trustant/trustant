#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export TRUSTABLE_E2E_COMMON_LIBRARY=1
# shellcheck source=tests/e2e_provider_common.sh
. "$ROOT/tests/e2e_provider_common.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

test_noninteractive_missing_value() {
  unset TRUSTABLE_E2E_TEST_MISSING
  local output
  if output="$(e2e_require_value TRUSTABLE_E2E_TEST_MISSING "Missing test value" 1 2>&1 </dev/null)"; then
    fail "missing non-interactive value unexpectedly succeeded"
  fi
  [[ "$output" == *"TRUSTABLE_E2E_TEST_MISSING is required in non-interactive mode"* ]] ||
    fail "missing-value error did not identify the variable: $output"
}

test_profiles() {
  unset TRUSTABLE_E2E_BASE_URL
  TRUSTABLE_E2E_MODEL="coding-model"
  TRUSTABLE_E2E_API_KEY="regolo-secret"
  e2e_resolve_profile regolo
  [ "$E2E_PROVIDER" = "trustable" ] || fail "wrong Regolo provider"
  [ "$E2E_BASE_URL" = "https://api.nuvolaris.io/v1" ] || fail "wrong Regolo URL"

  unset TRUSTABLE_E2E_API_KEY
  TRUSTABLE_E2E_OLLAMA_API_KEY="ollama-secret"
  TRUSTABLE_E2E_MODEL="cloud-model"
  e2e_resolve_profile ollama-cloud
  [ "$E2E_PROVIDER" = "ollama" ] || fail "wrong Ollama provider"
  [ "$E2E_BASE_URL" = "http://localhost:11434/v1" ] || fail "wrong Ollama URL"
}

test_report() {
  local temp report
  temp="$(mktemp -d)"
  report="$temp/report.json"
  E2E_PROVIDER="bestia"
  E2E_MODEL="coding-model"
  E2E_BASE_URL="http://bestia:11434/v1"
  e2e_write_report "$report" "run-1" bestia "2026-01-01T00:00:00Z" \
    "2026-01-01T00:00:07Z" 7 0 "benchmark-results/run-1/run.log" \
    "benchmark-results/run-1/playwright"
  jq -e '.schema == "trustable-e2e-benchmark/v1" and .outcome == "passed" and
    .duration_seconds == 7 and .provider == "bestia" and (.checks | length == 5)' \
    "$report" >/dev/null || fail "invalid normalized report"
  if rg -q 'secret' "$report"; then
    fail "report contains a credential marker"
  fi
  rm -rf "$temp"
}

test_profile_is_restored() {
  local temp original_hash
  temp="$(mktemp -d)"
  printf '%s\n' '{"provider":"original","base_url":"http://original/v1","api_key":"keep","models":{"old":{}},"opencode":{"default":"old","small":"old"}}' >"$temp/config.json"
  original_hash="$(sha256sum "$temp/config.json" | awk '{print $1}')"
  mkdir "$temp/bin"
  cat >"$temp/bin/kubectl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
while [ "$#" -gt 0 ] && [ "$1" != "--" ]; do shift; done
shift
case "$1" in
  cat)
    cat "$FAKE_TRUSTABLE_CONFIG"
    ;;
  sh)
    cat >"$FAKE_TRUSTABLE_CONFIG"
    ;;
  jq)
    shift
    args=("$@")
    args[$((${#args[@]} - 1))]="$FAKE_TRUSTABLE_CONFIG"
    command jq "${args[@]}"
    ;;
  *)
    echo "unexpected fake kubectl command: $*" >&2
    exit 2
    ;;
esac
EOF
  chmod +x "$temp/bin/kubectl"

  (
    export PATH="$temp/bin:$PATH"
    export FAKE_TRUSTABLE_CONFIG="$temp/config.json"
    E2E_LOCK_DIR="$temp/lock"
    E2E_PROVIDER="trustable"
    E2E_BASE_URL="https://api.nuvolaris.io/v1"
    E2E_API_KEY="temporary-secret"
    E2E_MODEL="coding-model"
    E2E_MAX_TOKEN=131072
    E2E_MAX_OUTPUT=32768
    e2e_apply_profile
    jq -e '.provider == "trustable" and .api_key == "temporary-secret" and
      .opencode.default == "coding-model"' "$temp/config.json" >/dev/null
  )

  [ "$(sha256sum "$temp/config.json" | awk '{print $1}')" = "$original_hash" ] ||
    fail "provider profile did not restore the exact original config"
  rm -rf "$temp"
}

test_noninteractive_missing_value
test_profiles
test_report
test_profile_is_restored
echo "Provider E2E helper tests passed"
