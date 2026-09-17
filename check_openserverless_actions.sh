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


set -u

ROOT="${1:-$(pwd)}"
CONTRACT=".openserverless-contract.md"
ERRORS=0
WARNINGS=0
package_python_sources=()
package_action_modules=()
MANAGED_LIVE_MODE=0

# WHY: in Trustable Edit, ops ide devel owns packaging asynchronously and its
# private log is the deployment authority. Treating sibling ZIP timestamps as
# a source contract made Pi poll derived artifacts and race the watcher.
if [ "${TRUSTABLE_MANAGED_RUNTIME:-0}" = "1" ]; then
  MANAGED_LIVE_MODE=1
fi

cd "$ROOT" || {
  echo "ERROR cannot enter $ROOT"
  exit 1
}

note_contract() {
  echo "      Re-read $CONTRACT before continuing."
}

error() {
  ERRORS=$((ERRORS + 1))
  echo "ERROR $1"
  if [ "${2:-}" != "" ]; then
    echo "      $2"
  fi
  note_contract
}

warn() {
  WARNINGS=$((WARNINGS + 1))
  echo "WARN  $1"
  if [ "${2:-}" != "" ]; then
    echo "      $2"
  fi
}

if [ ! -f "$CONTRACT" ]; then
  warn "$CONTRACT not found" "Use the Trustable-managed block in AGENTS.md as fallback guidance and report the missing contract."
fi

if [ -d packages ]; then
  while IFS= read -r python_source; do
    package_python_sources+=("$python_source")
    if [ "$(basename "$python_source")" != "__main__.py" ]; then
      package_action_modules+=("$python_source")
    fi
  done < <(find packages \
    \( -type d \( -name virtualenv -o -name .venv -o -name venv -o -name node_modules -o -name __pycache__ \) -prune \) -o \
    \( -type f -name '*.py' -print \) 2>/dev/null | sort)

  while IFS= read -r archive; do
    error "$archive" "Action ZIP files must not be created or edited inside an action source directory. Remove it; generated deploy archives are owned by the active deploy workflow."
  done < <(find packages -mindepth 3 -maxdepth 3 -type f -name '*.zip' 2>/dev/null | sort)

  declare -A action_dirs_without_wrapper=()
  for module in "${package_action_modules[@]}"; do
    action_dir="$(dirname "$module")"
    if [ ! -f "$action_dir/__main__.py" ]; then
      action_dirs_without_wrapper["$action_dir"]=1
    fi
  done
  for action_dir in "${!action_dirs_without_wrapper[@]}"; do
    error "$action_dir" "Action module exists without generated __main__.py. Create or repair the action with the OpenServerless MCP action tool before editing module logic."
  done

  while IFS= read -r wrapper; do
    action_dir="$(dirname "$wrapper")"
    error "$action_dir" "Invalid nested action path. Use action or package/action only, for example v1/register."
  done < <(find packages -mindepth 4 -maxdepth 4 -type f -name __main__.py 2>/dev/null | sort)

  while IFS= read -r wrapper; do
    action_dir="$(dirname "$wrapper")"
    if [ "$MANAGED_LIVE_MODE" -ne 1 ]; then
      deploy_archive="${action_dir}.zip"
      if [ ! -f "$deploy_archive" ]; then
        error "$action_dir" "Deploy archive is missing. Run the declared deploy workflow after creating or changing an action."
      elif find "$action_dir" -maxdepth 1 -type f ! -name '*.zip' -newer "$deploy_archive" -print -quit 2>/dev/null | grep -q .; then
        error "$action_dir" "Action source is newer than its deploy archive. Run the declared deploy workflow before setup or completion."
      fi
    fi

    if grep -Eq '^[[:space:]]*def[[:space:]]+main[[:space:]]*\(' "$wrapper" &&
      ! grep -Eq '#--(kind|web|param|timeout)|##[[:space:]]*build-context[[:space:]]*##|init_(postgresql|redis|s3|milvus)' "$wrapper"; then
      error "$wrapper" "Wrapper defines main() but lacks generated action/service markers. Do not hand-author generated wrappers; recreate or repair the action with the OpenServerless MCP action tool."
    fi
    if grep -Eiq 'CREATE[[:space:]]+TABLE|ALTER[[:space:]]+TABLE|CREATE[[:space:]]+(OR[[:space:]]+REPLACE[[:space:]]+)?VIEW|INSERT[[:space:]]+INTO|UPDATE[[:space:]]+[A-Za-z_]|DELETE[[:space:]]+FROM|import[[:space:]]+(flask|fastapi)|from[[:space:]]+(flask|fastapi)[[:space:]]+import' "$wrapper"; then
      error "$wrapper" "Generated wrapper appears to contain business logic or direct DB/server code. Move logic to the editable module."
    fi
    if grep -Eq '^#--param[[:space:]]+(OPS_USER|OPS_PASSWORD|OPS_APIHOST|OPS_REPO|OPS_SKILLS)([[:space:]]|$)' "$wrapper"; then
      error "$wrapper" "Trustable-managed runtime variables must not be bound into actions. OPS_APIHOST is for Trustable/ops ide orchestration; browser code uses relative /api/my URLs and actions use generated service bindings. Regenerate the wrapper with the OpenServerless MCP tools."
    fi
  done < <(find packages -mindepth 3 -maxdepth 3 -type f -name __main__.py 2>/dev/null | sort)

  for module in "${package_action_modules[@]}"; do
    if grep -Eq 'os\.getenv\(["'\'']POSTGRES_URL["'\'']\)|args\.get\(["'\'']POSTGRES_URL["'\'']\)|psycopg2?\.connect\(.*POSTGRES_URL|psycopg2?\.connect\(.*getenv' "$module"; then
      warn "$module" "Business module reads POSTGRES_URL. Prefer ctx.POSTGRESQL when wrapper wiring provides it."
    fi
    if grep -Eiq 'mongo(db)?|MONGODB|MONGO_' "$module" &&
      grep -Eq 'MILVUS|pymilvus|milvus_cli|MilvusClient' "$module" &&
      ! grep -Eq 'ctx\.MONGODB(_CLIENT)?([^A-Za-z0-9_]|$)' "$module"; then
      error "$module" "MongoDB is a separate document database capability. Do not use Milvus/vector tooling as a MongoDB substitute; report MongoDB as non configurato when the official capability is absent."
    fi
    if grep -Eq 'MDB_MCP_CONNECTION_STRING' "$module"; then
      error "$module" "MDB_MCP_CONNECTION_STRING is private to the MongoDB MCP server and is not an app action runtime binding. Use an official action/runtime MongoDB binding when available, otherwise report MongoDB as non configurato."
    fi
    action_dir="$(dirname "$module")"
    wrapper="$action_dir/__main__.py"
    # WHY: trulongrun3 implemented Redis session checks in business modules,
    # but several generated wrappers never received Redis wiring. Source-only
    # auth logic is unusable when ctx.REDIS is absent at runtime.
    if grep -Eq 'ctx\.REDIS(_PREFIX)?([^A-Za-z0-9_]|$)' "$module" &&
      { [ ! -f "$wrapper" ] || ! grep -Eq 'init_redis|ctx\.REDIS(_PREFIX)?([^A-Za-z0-9_]|$)' "$wrapper"; }; then
      error "$module" "Action module uses ctx.REDIS but its generated wrapper has no Redis connector. Call auth_setup with the complete authentication endpoint set, or action_add_redis for one non-authentication endpoint; never edit __main__.py manually."
    fi
    if grep -Eq 'MONGODB_URI|MONGO_URL|MONGO_CONNECTION_STRING|MDB_CONNECTION_STRING' "$module" &&
      { [ ! -f "$wrapper" ] || ! grep -Eq 'init_mongo|init_mongodb|MONGODB|MONGO_|MDB_' "$wrapper"; }; then
      error "$module" "MongoDB runtime environment was guessed in action code, but no generated MongoDB action binding was detected. MCP MongoDB visibility is diagnostic only; report MongoDB as non configurato unless an official runtime binding exists."
    fi
    if grep -Eq 'ctx\.REDIS|REDIS' "$module" &&
      grep -Eq '\.(get|set|delete|exists|expire|hset|hget|hmset|hmget|lpush|rpush|sadd|zadd|incr|decr|scan|keys)[[:space:]]*\(' "$module" &&
      ! grep -Eq 'REDIS_PREFIX|redis_key' "$module"; then
      error "$module" "Redis action code uses Redis keys without the generated ctx.REDIS_PREFIX. Build keys as ctx.REDIS_PREFIX plus an app-local suffix; naked keys are outside the Nuvolaris Redis ACL."
    fi
    if grep -Eq '\.list_buckets[[:space:]]*\(' "$module"; then
      error "$module" "S3 action code must never call list_buckets(). The generated credentials are scoped to ctx.S3_DATA and ctx.S3_WEB; verify only the configured bucket."
    fi
    if grep -Eiq 'S3_CLIENT|S3_DATA|S3_WEB' "$module" &&
      grep -Eiq 'read[_ -]?write|write[_ -]?read' "$module" &&
      { ! grep -Eq '\.put_object[[:space:]]*\(' "$module" ||
        ! grep -Eq '\.get_object[[:space:]]*\(' "$module" ||
        ! grep -Eq '["'\'']Body["'\'']' "$module" ||
        ! grep -Eq '\.read[[:space:]]*\(' "$module" ||
        ! grep -Eq '\.delete_object[[:space:]]*\(' "$module" ||
        ! grep -Eq 'finally[[:space:]]*:' "$module"; }; then
      error "$module" "S3 read/write status requires a real ctx.S3_DATA check: put_object, get_object and Body.read comparison, then delete_object in finally. head_bucket or listing cannot justify read_write: OK."
    fi
    if grep -Eq 'OPS_APIHOST' "$module"; then
      error "$module" "Action modules must not read or use OPS_APIHOST. Frontends call relative /api/my endpoints; action composition uses generated service bindings instead of browser-facing or orchestration hosts."
    fi
  done

  for seedfile in "${package_action_modules[@]}"; do
    lower_seedfile="${seedfile,,}"
    case "$lower_seedfile" in
      */setup/*|*seed*|*mocks*|*refresh*|*database*) ;;
      *) continue ;;
    esac
    if grep -Eiq 'INSERT[[:space:]]+INTO' "$seedfile" &&
      ! grep -Eiq 'seed_state|seed_marker|demo_seed|applied_at|already_populated|SELECT[[:space:]]+COUNT[[:space:]]*\([[:space:]]*\*[[:space:]]*\)[[:space:]]+FROM' "$seedfile"; then
      warn "$seedfile" "Seed-like action inserts rows but no durable seed marker was detected."
    fi
  done

  for viewfile in "${package_python_sources[@]}"; do
    if grep -Eiq 'CREATE[[:space:]]+OR[[:space:]]+REPLACE[[:space:]]+VIEW|ALTER[[:space:]]+VIEW' "$viewfile" && ! grep -Eiq 'DROP[[:space:]]+VIEW[[:space:]]+IF[[:space:]]+EXISTS' "$viewfile"; then
      warn "$viewfile" "View shape may change without DROP VIEW IF EXISTS."
    fi
  done

  for module in "${package_action_modules[@]}"; do
    if grep -Eq '(__ow_method|request_method|method).*(PUT|PATCH|DELETE)|(PUT|PATCH|DELETE).*(request_method|__ow_method|method)' "$module" && grep -q '__ow_path' "$module"; then
      if ! grep -Eq 'extract_.*(id|path)|path_.*id|route_.*id|request_.*id' "$module"; then
        warn "$module" "CRUD action inspects __ow_path for item routes but no obvious route-id extraction helper was detected. Use the contract pattern and test PUT/DELETE /api/my/<resource>/<id> without relying only on body id."
      fi
    fi
  done

  for module in "${package_action_modules[@]}"; do
    if grep -Eq '<!DOCTYPE[[:space:]]+html|<html[[:space:]>]' "$module" && grep -Eq '["'\'']html["'\''][[:space:]]*:|return[[:space:]]+\{[^}]*html' "$module"; then
      warn "$module" "Module appears to generate full HTML but return it as application JSON. If a browser opens this endpoint directly, return text/html as the HTTP body or make the frontend fetch JSON and write/print the extracted HTML."
    fi
  done
fi

if [ -d src ]; then
  while IFS= read -r ui_file; do
    if grep -Eq 'window\.open\([^)]*/api/my/|window\.open\([^)]*`/api/my/|window\.open\([^)]*"/api/my/|window\.open\([^)]*'\''/api/my/' "$ui_file"; then
      warn "$ui_file" "Frontend opens a /api/my action URL directly. For printable/download/browser flows, validate with curl -i that the target returns the expected browser content type, not application/json containing embedded HTML."
    fi
  done < <(find src -type f \( -name '*.js' -o -name '*.jsx' -o -name '*.ts' -o -name '*.tsx' \) 2>/dev/null | sort)
fi

py_files=("${package_python_sources[@]}")
if [ -d tests ]; then
  while IFS= read -r py_file; do
    py_files+=("$py_file")
  done < <(find tests \
    \( -type d \( -name virtualenv -o -name .venv -o -name venv -o -name node_modules -o -name __pycache__ \) -prune \) -o \
    \( -type f -name '*.py' -print \) 2>/dev/null | sort)
fi
if [ "${#py_files[@]}" -gt 0 ]; then
  if ! python3 - "${py_files[@]}" <<'PY'
import pathlib
import sys

ok = True
for name in sys.argv[1:]:
    path = pathlib.Path(name)
    try:
        compile(path.read_text(), str(path), "exec")
    except Exception as exc:
        print(f"{path}: {exc}", file=sys.stderr)
        ok = False

sys.exit(0 if ok else 1)
PY
  then
    error "Python syntax check" "Python syntax check failed for packages/tests."
  fi
fi

if [ "$ERRORS" -gt 0 ]; then
  echo "OpenServerless action contract check failed: $ERRORS error(s), $WARNINGS warning(s)."
  note_contract
  exit 1
fi

echo "OpenServerless action contract check passed: $WARNINGS warning(s)."
exit 0
