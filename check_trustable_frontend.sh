#!/usr/bin/env bash

set -u

ROOT="${1:-$(pwd)}"
ERRORS=0
WARNINGS=0

cd "$ROOT" || {
  echo "ERROR cannot enter $ROOT"
  exit 1
}

error() {
  ERRORS=$((ERRORS + 1))
  echo "ERROR $1"
  if [ "${2:-}" != "" ]; then
    echo "      $2"
  fi
}

warn() {
  WARNINGS=$((WARNINGS + 1))
  echo "WARN  $1"
  if [ "${2:-}" != "" ]; then
    echo "      $2"
  fi
}

frontend_files=()
if [ -d src ]; then
  while IFS= read -r file; do
    frontend_files+=("$file")
  done < <(find src -type f \( -name '*.js' -o -name '*.jsx' -o -name '*.ts' -o -name '*.tsx' \) 2>/dev/null | sort)
fi

if [ "${#frontend_files[@]}" -gt 0 ]; then
  if grep -Eq 'HashRouter|createHashRouter' "${frontend_files[@]}"; then
    while IFS= read -r hit; do
      case "$hit" in
        *'href="/api/'*|*"href='/api/"*|*'href="/assets/'*|*"href='/assets/"*|*'href="//'*|*"href='//"*)
          continue
          ;;
      esac
      error "$hit" "HashRouter internal navigation must use Link/NavLink/useNavigate or a #/ route. A root-relative anchor reloads the document and the hash router sees '/'."
    done < <(grep -En "href[[:space:]]*=[[:space:]]*['\"]/" "${frontend_files[@]}" 2>/dev/null || true)

    while IFS= read -r hit; do
      case "$hit" in
        *'/api/'*|*'/assets/'*) continue ;;
      esac
      error "$hit" "HashRouter navigation must not assign a root-relative browser path. Use the router navigation API."
    done < <(grep -En "window\\.location\\.(href[[:space:]]*=|assign\\(|replace\\()[[:space:]]*['\"]/" "${frontend_files[@]}" 2>/dev/null || true)
  fi

  while IFS= read -r hit; do
    error "$hit" "Do not put passwords in URLs or query strings. Send credentials in a JSON request body."
  done < <(grep -Ein '(fetch|axios).*(\?|&)[^[:space:]]*password|password[^[:space:]]*(\?|&).*(fetch|axios)' "${frontend_files[@]}" 2>/dev/null || true)

  if grep -Eqs 'Authorization[^\n]*Bearer|Bearer[^\n]*Authorization' "${frontend_files[@]}" &&
    grep -Eqs 'user_id[=:][[:space:]]*\$?\{|user_id=.*localStorage|localStorage.*user_id' "${frontend_files[@]}"; then
    warn "frontend authentication identity" "The frontend sends a bearer token but also supplies user_id. Protected APIs should derive identity from the validated token instead of trusting a browser-provided id."
  fi

  if grep -Eqs 'localStorage\.(getItem|setItem)\([^)]*(token|session)' "${frontend_files[@]}" &&
    grep -Eqs 'isAuthenticated[[:space:]]*:[[:space:]]*false' "${frontend_files[@]}" &&
    ! grep -Eqs '(bootstrap|hydrate|restore|initialize|init)Auth|Auth(Bootstrap|Provider|Gate)' "${frontend_files[@]}"; then
    warn "authentication bootstrap" "A persisted token and an initially false auth state were found, but no obvious application-level auth bootstrap was detected. Verify persistence after a full page reload."
  fi
fi

python_files=()
if [ -d packages ]; then
  while IFS= read -r file; do
    python_files+=("$file")
  done < <(find packages -type f -name '*.py' ! -name __main__.py 2>/dev/null | sort)
fi

if [ "${#python_files[@]}" -gt 0 ] &&
  grep -Eqs 'token_urlsafe|secrets\.token|uuid.*token' "${python_files[@]}" &&
  ! grep -Eiq 'INSERT[[:space:]]+INTO[[:space:]]+(sessions?|auth_tokens?|tokens?)|UPDATE[[:space:]]+(sessions?|auth_tokens?|tokens?)' "${python_files[@]}"; then
  warn "authentication token persistence" "A token is generated but no obvious session/token persistence was found. Verify that protected requests validate a stored, expiring token."
fi

if [ "$ERRORS" -gt 0 ]; then
  echo "Trustable frontend contract check failed: $ERRORS error(s), $WARNINGS warning(s)."
  exit 1
fi

echo "Trustable frontend contract check passed: $WARNINGS warning(s)."
exit 0
