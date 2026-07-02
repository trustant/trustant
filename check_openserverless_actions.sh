#!/usr/bin/env bash

set -u

ROOT="${1:-$(pwd)}"
CONTRACT=".openserverless-contract.md"
ERRORS=0
WARNINGS=0

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
  warn "$CONTRACT not found" "Use opencode.md as fallback guidance and report the missing contract."
fi

if [ -d packages ]; then
  while IFS= read -r wrapper; do
    action_dir="$(dirname "$wrapper")"
    error "$action_dir" "Invalid nested action path. Use action or package/action only, for example v1/register."
  done < <(find packages -mindepth 4 -maxdepth 4 -type f -name __main__.py 2>/dev/null | sort)

  while IFS= read -r wrapper; do
    if grep -Eiq 'CREATE[[:space:]]+TABLE|ALTER[[:space:]]+TABLE|CREATE[[:space:]]+(OR[[:space:]]+REPLACE[[:space:]]+)?VIEW|INSERT[[:space:]]+INTO|UPDATE[[:space:]]+[A-Za-z_]|DELETE[[:space:]]+FROM|import[[:space:]]+(flask|fastapi)|from[[:space:]]+(flask|fastapi)[[:space:]]+import' "$wrapper"; then
      error "$wrapper" "Generated wrapper appears to contain business logic or direct DB/server code. Move logic to the editable module."
    fi
  done < <(find packages -mindepth 3 -maxdepth 3 -type f -name __main__.py 2>/dev/null | sort)

  while IFS= read -r module; do
    if grep -Eq 'os\.getenv\(["'\'']POSTGRES_URL["'\'']\)|args\.get\(["'\'']POSTGRES_URL["'\'']\)|psycopg2?\.connect\(.*POSTGRES_URL|psycopg2?\.connect\(.*getenv' "$module"; then
      warn "$module" "Business module reads POSTGRES_URL. Prefer ctx.POSTGRESQL when wrapper wiring provides it."
    fi
  done < <(find packages -type f -name '*.py' ! -name __main__.py 2>/dev/null | sort)

  while IFS= read -r seedfile; do
    if grep -Eiq 'INSERT[[:space:]]+INTO' "$seedfile" && ! grep -Eiq 'seed_state|seed_marker|demo_seed|applied_at|already_populated' "$seedfile"; then
      warn "$seedfile" "Seed-like action inserts rows but no durable seed marker was detected."
    fi
  done < <(find packages -type f -name '*.py' \( -path '*/setup/*' -o -iname '*seed*' -o -iname '*mocks*' -o -iname '*refresh*' -o -iname '*database*' \) 2>/dev/null | sort)

  while IFS= read -r viewfile; do
    if grep -Eiq 'CREATE[[:space:]]+OR[[:space:]]+REPLACE[[:space:]]+VIEW|ALTER[[:space:]]+VIEW' "$viewfile" && ! grep -Eiq 'DROP[[:space:]]+VIEW[[:space:]]+IF[[:space:]]+EXISTS' "$viewfile"; then
      warn "$viewfile" "View shape may change without DROP VIEW IF EXISTS."
    fi
  done < <(find packages -type f -name '*.py' 2>/dev/null | sort)

  while IFS= read -r module; do
    if grep -Eq '(__ow_method|request_method|method).*(PUT|PATCH|DELETE)|(PUT|PATCH|DELETE).*(request_method|__ow_method|method)' "$module" && grep -q '__ow_path' "$module"; then
      if ! grep -Eq 'extract_.*(id|path)|path_.*id|route_.*id|request_.*id' "$module"; then
        warn "$module" "CRUD action inspects __ow_path for item routes but no obvious route-id extraction helper was detected. Use the contract pattern and test PUT/DELETE /api/my/<resource>/<id> without relying only on body id."
      fi
    fi
  done < <(find packages -type f -name '*.py' ! -name __main__.py 2>/dev/null | sort)

  while IFS= read -r module; do
    if grep -Eq '<!DOCTYPE[[:space:]]+html|<html[[:space:]>]' "$module" && grep -Eq '["'\'']html["'\''][[:space:]]*:|return[[:space:]]+\{[^}]*html' "$module"; then
      warn "$module" "Module appears to generate full HTML but return it as application JSON. If a browser opens this endpoint directly, return text/html as the HTTP body or make the frontend fetch JSON and write/print the extracted HTML."
    fi
  done < <(find packages -type f -name '*.py' ! -name __main__.py 2>/dev/null | sort)
fi

if [ -d src ]; then
  while IFS= read -r ui_file; do
    if grep -Eq 'window\.open\([^)]*/api/my/|window\.open\([^)]*`/api/my/|window\.open\([^)]*"/api/my/|window\.open\([^)]*'\''/api/my/' "$ui_file"; then
      warn "$ui_file" "Frontend opens a /api/my action URL directly. For printable/download/browser flows, validate with curl -i that the target returns the expected browser content type, not application/json containing embedded HTML."
    fi
  done < <(find src -type f \( -name '*.js' -o -name '*.jsx' -o -name '*.ts' -o -name '*.tsx' \) 2>/dev/null | sort)
fi

py_files=()
for py_dir in packages tests; do
  if [ -d "$py_dir" ]; then
    while IFS= read -r py_file; do
      py_files+=("$py_file")
    done < <(find "$py_dir" -type f -name '*.py' 2>/dev/null | sort)
  fi
done
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
