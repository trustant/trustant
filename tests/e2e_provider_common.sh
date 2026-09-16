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


set -euo pipefail

E2E_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
E2E_RESULTS_ROOT="${TRUSTABLE_E2E_RESULTS_DIR:-$E2E_ROOT/benchmark-results}"
E2E_NAMESPACE="${TRUSTABLE_E2E_NAMESPACE:-openserverless}"
E2E_POD="${TRUSTABLE_E2E_POD:-trustable-0}"
E2E_CONTAINER="${TRUSTABLE_E2E_CONTAINER:-trustable}"
E2E_CONFIG_PATH="${TRUSTABLE_E2E_CONFIG_PATH:-/home/trustable/workspace/trustable.json}"
E2E_LOCK_DIR="${TMPDIR:-/tmp}/trustable-e2e-provider.lock"
E2E_CONFIG_BACKUP=""
E2E_CONFIG_STAGED=""
E2E_LOCK_HELD=0
E2E_CREDENTIAL_REQUIRED=false
E2E_EFFECTIVE_CONFIG_JSON="{}"

e2e_error() {
  echo "ERROR: $*" >&2
}

e2e_need() {
  command -v "$1" >/dev/null 2>&1 || {
    e2e_error "missing required command: $1"
    return 1
  }
}

e2e_load_env() {
  local env_file="${TRUSTABLE_E2E_ENV_FILE:-$E2E_ROOT/.env.e2e}"
  if [ -f "$env_file" ]; then
    # The file is local, git-ignored test configuration and is trusted like any sourced shell env file.
    set -a
    # shellcheck disable=SC1090
    . "$env_file"
    set +a
  elif [ -n "${TRUSTABLE_E2E_ENV_FILE:-}" ]; then
    e2e_error "test env file does not exist: $env_file"
    return 1
  fi
}

e2e_require_value() {
  local name="$1"
  local label="$2"
  local secret="${3:-0}"
  local value="${!name:-}"
  if [ -n "$value" ]; then
    return 0
  fi
  if [ ! -t 0 ]; then
    e2e_error "$name is required in non-interactive mode; export it or put it in ${TRUSTABLE_E2E_ENV_FILE:-$E2E_ROOT/.env.e2e}"
    return 1
  fi
  if [ "$secret" = "1" ]; then
    read -r -s -p "$label: " value
    echo >&2
  else
    read -r -p "$label: " value
  fi
  if [ -z "$value" ]; then
    e2e_error "$name cannot be empty"
    return 1
  fi
  printf -v "$name" '%s' "$value"
  export "$name"
}

e2e_resolve_profile() {
  local mode="$1"
  e2e_require_value TRUSTABLE_E2E_MODEL "Model name"
  E2E_MODEL="$TRUSTABLE_E2E_MODEL"
  E2E_MAX_TOKEN="${TRUSTABLE_E2E_MAX_TOKEN:-131072}"
  E2E_MAX_OUTPUT="${TRUSTABLE_E2E_MAX_OUTPUT:-32768}"

  case "$mode" in
    private)
      # A user-supplied endpoint has no canonical host, so require the base URL
      # rather than guessing one.
      e2e_require_value TRUSTABLE_E2E_BASE_URL "Private AI base URL" 0
      E2E_PROVIDER="private"
      E2E_BASE_URL="$TRUSTABLE_E2E_BASE_URL"
      E2E_API_KEY=""
      E2E_CREDENTIAL_REQUIRED=false
      ;;
    ollama-cloud)
      if [ -z "${TRUSTABLE_E2E_OLLAMA_API_KEY:-}" ] && [ -n "${OLLAMA_API_KEY:-}" ]; then
        TRUSTABLE_E2E_OLLAMA_API_KEY="$OLLAMA_API_KEY"
      fi
      e2e_require_value TRUSTABLE_E2E_OLLAMA_API_KEY "Ollama Cloud API key" 1
      E2E_PROVIDER="ollama"
      E2E_BASE_URL="${TRUSTABLE_E2E_BASE_URL:-http://localhost:11434/v1}"
      E2E_API_KEY="$TRUSTABLE_E2E_OLLAMA_API_KEY"
      E2E_CREDENTIAL_REQUIRED=true
      ;;
    regolo)
      if [ -z "${TRUSTABLE_E2E_API_KEY:-}" ] && [ -n "${API_KEY:-}" ]; then
        TRUSTABLE_E2E_API_KEY="$API_KEY"
      fi
      e2e_require_value TRUSTABLE_E2E_API_KEY "Regolo API key" 1
      E2E_PROVIDER="trustable"
      E2E_BASE_URL="${TRUSTABLE_E2E_BASE_URL:-https://api.nuvolaris.io/v1}"
      E2E_API_KEY="$TRUSTABLE_E2E_API_KEY"
      E2E_CREDENTIAL_REQUIRED=true
      ;;
    *)
      e2e_error "unknown provider mode: $mode"
      return 1
      ;;
  esac
}

e2e_preflight_profile() {
  local config_json="$1"
  if ! jq -e \
    --arg provider "$E2E_PROVIDER" \
    --arg base_url "$E2E_BASE_URL" \
    --arg model "$E2E_MODEL" \
    --argjson max_token "$E2E_MAX_TOKEN" \
    --argjson max_output "$E2E_MAX_OUTPUT" \
    --argjson credential_required "$E2E_CREDENTIAL_REQUIRED" \
    '.provider == $provider
     and .base_url == $base_url
     and .model == $model
     and .limits.max_token == $max_token
     and .limits.max_output == $max_output
     and .credential.required == $credential_required
     and .credential.configured == $credential_required' \
    <<<"$config_json" >/dev/null; then
    e2e_error "temporary provider profile preflight failed"
    return 1
  fi
}

e2e_restore_config() {
  local status=$?
  trap - EXIT INT TERM
  if [ -n "$E2E_CONFIG_BACKUP" ] && [ -f "$E2E_CONFIG_BACKUP" ]; then
    if ! kubectl -n "$E2E_NAMESPACE" exec -i "$E2E_POD" -c "$E2E_CONTAINER" -- \
      sh -c 'cat > "$1"' sh "$E2E_CONFIG_PATH" <"$E2E_CONFIG_BACKUP"; then
      e2e_error "could not restore $E2E_CONFIG_PATH; backup remains at $E2E_CONFIG_BACKUP"
      status=1
    else
      rm -f "$E2E_CONFIG_BACKUP"
    fi
  fi
  [ -z "$E2E_CONFIG_STAGED" ] || rm -f "$E2E_CONFIG_STAGED"
  if [ "$E2E_LOCK_HELD" = "1" ]; then
    rmdir "$E2E_LOCK_DIR" 2>/dev/null || true
  fi
  exit "$status"
}

e2e_apply_profile() {
  if ! mkdir "$E2E_LOCK_DIR" 2>/dev/null; then
    e2e_error "another provider E2E is active (lock: $E2E_LOCK_DIR)"
    return 1
  fi
  E2E_LOCK_HELD=1
  trap e2e_restore_config EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM

  local backup
  backup="$(mktemp "${TMPDIR:-/tmp}/trustable-e2e-config.XXXXXX")"
  chmod 600 "$backup"
  if ! kubectl -n "$E2E_NAMESPACE" exec "$E2E_POD" -c "$E2E_CONTAINER" -- \
    cat "$E2E_CONFIG_PATH" >"$backup"; then
    rm -f "$backup"
    e2e_error "could not back up $E2E_CONFIG_PATH"
    return 1
  fi
  if ! jq -e 'type == "object"' "$backup" >/dev/null; then
    rm -f "$backup"
    e2e_error "$E2E_CONFIG_PATH is not a valid JSON object"
    return 1
  fi
  E2E_CONFIG_BACKUP="$backup"
  E2E_CONFIG_STAGED="$(mktemp "${TMPDIR:-/tmp}/trustable-e2e-staged.XXXXXX")"
  chmod 600 "$E2E_CONFIG_STAGED"
  jq \
    --arg provider "$E2E_PROVIDER" \
    --arg base_url "$E2E_BASE_URL" \
    --arg api_key "$E2E_API_KEY" \
    --arg model "$E2E_MODEL" \
    --argjson max_token "$E2E_MAX_TOKEN" \
    --argjson max_output "$E2E_MAX_OUTPUT" \
    '.provider = $provider
     | .base_url = $base_url
     | if $api_key == "" then del(.api_key) else .api_key = $api_key end
     | .models = {($model): {maxToken: $max_token, maxOutput: $max_output}}
     # The benchmark must exercise the same Pi-only schema as the product;
     # retaining opencode here would conceal a broken issue #51 cutover.
     | .pi = {default: $model}
     | del(.opencode)' \
    "$E2E_CONFIG_BACKUP" >"$E2E_CONFIG_STAGED"
  kubectl -n "$E2E_NAMESPACE" exec -i "$E2E_POD" -c "$E2E_CONTAINER" -- \
    sh -c 'cat > "$1"' sh "$E2E_CONFIG_PATH" <"$E2E_CONFIG_STAGED"
  rm -f "$E2E_CONFIG_STAGED"
  E2E_CONFIG_STAGED=""

  E2E_EFFECTIVE_CONFIG_JSON="$(kubectl -n "$E2E_NAMESPACE" exec "$E2E_POD" -c "$E2E_CONTAINER" -- \
    jq -c --argjson credential_required "$E2E_CREDENTIAL_REQUIRED" '
      {
        provider: .provider,
        base_url: .base_url,
        model: .pi.default,
        limits: {
          max_token: .models[.pi.default].maxToken,
          max_output: .models[.pi.default].maxOutput
        },
        credential: {
          required: $credential_required,
          configured: (((.api_key // "") | length) > 0)
        }
      }' "$E2E_CONFIG_PATH")"
  e2e_preflight_profile "$E2E_EFFECTIVE_CONFIG_JSON"
}

e2e_write_report() {
  local report="$1"
  local run_id="$2"
  local mode="$3"
  local started="$4"
  local ended="$5"
  local duration="$6"
  local exit_code="$7"
  local log_path="$8"
  local artifact_path="$9"
  local outcome="failed"
  [ "$exit_code" = "0" ] && outcome="passed"
  jq -n \
    --arg schema "trustable-e2e-benchmark/v1" \
    --arg run_id "$run_id" \
    --arg provider_mode "$mode" \
    --arg provider "$E2E_PROVIDER" \
    --arg model "$E2E_MODEL" \
    --arg base_url "$E2E_BASE_URL" \
    --argjson effective_config "$E2E_EFFECTIVE_CONFIG_JSON" \
    --arg started_at "$started" \
    --arg ended_at "$ended" \
    --argjson duration_seconds "$duration" \
    --arg outcome "$outcome" \
    --argjson exit_code "$exit_code" \
    --arg app "${TRUSTABLE_E2E_APP:-}" \
    --arg repo "${TRUSTABLE_E2E_REPO:-trustable-ai/trureact}" \
    --arg log_path "$log_path" \
    --arg artifact_path "$artifact_path" \
    '{schema: $schema, run_id: $run_id, provider_mode: $provider_mode,
      provider: $provider, model: $model, base_url: $base_url,
      effective_config: $effective_config,
      started_at: $started_at, ended_at: $ended_at,
      duration_seconds: $duration_seconds, outcome: $outcome, exit_code: $exit_code,
      target: {app: (if $app == "" then null else $app end), repo: $repo},
      artifacts: {log: $log_path, playwright: $artifact_path},
      checks: [
        {name: "provider_profile_preflight", result: "passed"},
        {name: "opencode_tool_safety", result: $outcome},
        {name: "openserverless_action_workflow", result: $outcome},
        {name: "deploy_setup_completion_order", result: $outcome},
        {name: "stack_read_write_request", result: $outcome},
        {name: "trustable_completion_checker", result: $outcome}
      ]}' >"$report"
}

e2e_provider_main() {
  local mode="$1"
  shift
  e2e_load_env
  E2E_RESULTS_ROOT="${TRUSTABLE_E2E_RESULTS_DIR:-$E2E_ROOT/benchmark-results}"
  E2E_NAMESPACE="${TRUSTABLE_E2E_NAMESPACE:-openserverless}"
  E2E_POD="${TRUSTABLE_E2E_POD:-trustable-0}"
  E2E_CONTAINER="${TRUSTABLE_E2E_CONTAINER:-trustable}"
  E2E_CONFIG_PATH="${TRUSTABLE_E2E_CONFIG_PATH:-/home/trustable/workspace/trustable.json}"
  e2e_need jq
  e2e_need kubectl
  e2e_need npm
  e2e_resolve_profile "$mode"

  local safe_model run_id run_dir log_path artifact_path report_path
  safe_model="$(printf '%s' "$E2E_MODEL" | tr -cs '[:alnum:]._-' '-')"
  run_id="$(date -u +%Y%m%dT%H%M%SZ)-${mode}-${safe_model}-$$"
  run_dir="$E2E_RESULTS_ROOT/$run_id"
  log_path="$run_dir/run.log"
  artifact_path="$run_dir/playwright"
  report_path="$run_dir/report.json"
  mkdir -p "$artifact_path"

  e2e_apply_profile
  # Provider credentials now live only in the temporary managed configuration;
  # do not expose them to Playwright, OpenCode, shell tools, or generated apps.
  unset TRUSTABLE_E2E_API_KEY TRUSTABLE_E2E_OLLAMA_API_KEY OLLAMA_API_KEY API_KEY

  export TRUSTABLE_E2E_RUN_PROMPT=1
  export TRUSTABLE_E2E_EXPECT_ACTION_WORKFLOW=1
  export TRUSTABLE_E2E_EXPECT_SETUP=1
  export TRUSTABLE_E2E_EXPECT_OPENSERVERLESS_TOOL=1
  export TRUSTABLE_E2E_PROMPT_EXPECT_CHANGES=1
  export TRUSTABLE_E2E_PROMPT_TIMEOUT_MS="${TRUSTABLE_E2E_PROMPT_TIMEOUT_MS:-2400000}"
  export TRUSTABLE_E2E_PROMPT_TEST_TIMEOUT_MS="${TRUSTABLE_E2E_PROMPT_TEST_TIMEOUT_MS:-2700000}"
  export TRUSTABLE_E2E_PROMPT="${TRUSTABLE_E2E_PROMPT:-Vorrei una pagina Stack Nuvolaris che mostri in modo semplice se Redis, MongoDB, PostgreSQL e S3 funzionano davvero. Per ogni servizio esegui una vera prova di scrittura e rilettura dello stesso valore e mostra il valore verificato. Prepara automaticamente quanto serve, controlla tutto tu e non chiedermi di usare la shell.}"
  if [ -z "${TRUSTABLE_E2E_APP:-}" ] && [ -z "${TRUSTABLE_E2E_REPO:-}" ]; then
    export TRUSTABLE_E2E_REPO=trustable-ai/trureact
  fi

  echo "Trustable provider benchmark"
  echo "  mode:       $mode"
  echo "  provider:   $E2E_PROVIDER"
  echo "  model:      $E2E_MODEL"
  echo "  results:    ${run_dir#$E2E_ROOT/}"
  echo

  local started ended started_epoch ended_epoch duration status
  started="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  started_epoch="$(date +%s)"
  set +e
  "$E2E_ROOT/tests/e2e_action_workflow.sh" --output "$artifact_path" "$@" 2>&1 | tee "$log_path"
  status=${PIPESTATUS[0]}
  set -e
  ended_epoch="$(date +%s)"
  ended="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  duration=$((ended_epoch - started_epoch))

  e2e_write_report "$report_path" "$run_id" "$mode" "$started" "$ended" "$duration" "$status" \
    "${log_path#$E2E_ROOT/}" "${artifact_path#$E2E_ROOT/}"
  mkdir -p "$E2E_RESULTS_ROOT"
  jq -c . "$report_path" >>"$E2E_RESULTS_ROOT/index.jsonl"
  if [ ! -s "$E2E_RESULTS_ROOT/summary.tsv" ]; then
    printf 'run_id\tprovider_mode\tprovider\tmodel\tstarted_at\tduration_seconds\toutcome\texit_code\treport\n' \
      >"$E2E_RESULTS_ROOT/summary.tsv"
  fi
  jq -r --arg report "${report_path#$E2E_ROOT/}" \
    '[.run_id, .provider_mode, .provider, .model, .started_at,
      (.duration_seconds | tostring), .outcome, (.exit_code | tostring), $report] | @tsv' \
    "$report_path" >>"$E2E_RESULTS_ROOT/summary.tsv"

  echo
  echo "Benchmark report: ${report_path#$E2E_ROOT/}"
  return "$status"
}

if [ "${TRUSTABLE_E2E_COMMON_LIBRARY:-0}" != "1" ]; then
  e2e_error "run one of tests/e2e_private_app.sh, tests/e2e_ollama_cloud_app.sh, or tests/e2e_regolo_app.sh"
  exit 2
fi
