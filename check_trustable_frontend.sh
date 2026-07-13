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

    while IFS= read -r hit; do
      error "$hit" "HashRouter APIs receive logical paths such as /login, not #/login. Remove the hash; React Router adds it to the browser URL."
    done < <(grep -En "(<(Link|NavLink|Navigate)[^>]*(to|href)[[:space:]]*=[[:space:]]*['\"]#/|navigate[[:space:]]*\\([[:space:]]*['\"]#/)" "${frontend_files[@]}" 2>/dev/null || true)
  fi

  while IFS= read -r hit; do
    error "$hit" "Do not put passwords in URLs or query strings. Send credentials in a JSON request body."
  done < <(grep -Ein '(fetch|axios).*(\?|&)[^[:space:]]*password|password[^[:space:]]*(\?|&).*(fetch|axios)' "${frontend_files[@]}" 2>/dev/null || true)

  while IFS= read -r hit; do
    error "$hit" "Do not hardcode browser-visible user_id values. Protected APIs must derive identity from the validated token/session."
  done < <(grep -Ein "user_id[[:space:]]*[=:][[:space:]]*['\"]?[0-9]+|[?&]user_id=[0-9]+" "${frontend_files[@]}" 2>/dev/null || true)

  if grep -Eqs 'Authorization[^\n]*Bearer|Bearer[^\n]*Authorization' "${frontend_files[@]}" &&
    grep -Eqs 'user_id[=:][[:space:]]*\$?\{|user_id=.*localStorage|localStorage.*user_id' "${frontend_files[@]}"; then
    error "frontend authentication identity" "The frontend sends a bearer token but also supplies user_id. Protected APIs must derive identity from the validated token instead of trusting a browser-provided id."
  fi

  if grep -Eqs 'localStorage\.(getItem|setItem)\([^)]*(token|session)' "${frontend_files[@]}" &&
    grep -Eqs 'isAuthenticated[[:space:]]*:[[:space:]]*false' "${frontend_files[@]}" &&
    ! grep -Eqs '(bootstrap|hydrate|restore|initialize|init)Auth|Auth(Bootstrap|Provider|Gate)' "${frontend_files[@]}"; then
    warn "authentication bootstrap" "A persisted token and an initially false auth state were found, but no obvious application-level auth bootstrap was detected. Verify persistence after a full page reload."
  fi

  while IFS= read -r hit; do
    error "$hit" "An asynchronously loaded auth/user state redirects while its initial value is still null. Keep an explicit loading state until the protected identity request completes, then decide whether to redirect."
  done < <(python3 - "${frontend_files[@]}" <<'PY'
import pathlib
import re
import sys

state = re.compile(r"const\s*\[\s*([A-Za-z_$][\w$]*)\s*,\s*set[A-Za-z_$][\w$]*\s*\]\s*=\s*useState(?:<[^;]+?>)?\(null\)")
for name in sys.argv[1:]:
    text = pathlib.Path(name).read_text(encoding="utf-8", errors="replace")
    if "useEffect" not in text or "fetch(" not in text or "<Navigate" not in text:
        continue
    for match in state.finditer(text):
        variable = re.escape(match.group(1))
        redirect = re.compile(rf"if\s*\([^)]*!\s*{variable}\b[^)]*\)\s*(?:\{{\s*)?return\s*<Navigate", re.S)
        redirect_match = redirect.search(text)
        if redirect_match:
            loading_states = [
                (loading, setter)
                for loading, setter in re.findall(
                    r"const\s*\[\s*([A-Za-z_$][\w$]*)\s*,\s*(set[A-Za-z_$][\w$]*)\s*\]\s*=\s*useState\s*\(true\)",
                    text,
                )
                if "loading" in loading.lower()
            ]
            guarded = any(
                re.search(rf"if\s*\(\s*{re.escape(loading)}\s*\)", text[:redirect_match.start()])
                and re.search(rf"{re.escape(setter)}\s*\(false\)", text)
                for loading, setter in loading_states
            )
            if guarded:
                continue
            line = text.count("\n", 0, match.start()) + 1
            print(f"{name}:{line}: async state '{match.group(1)}' starts null and immediately guards a Navigate")
PY
  )

  while IFS= read -r hit; do
    error "$hit" "A cached user/profile from localStorage is being used as authoritative authentication state without an observable backend session validation. Persist a token, bootstrap through a bounded me/session validation, keep an explicit loading state, and clear cached identity when validation fails."
  done < <(python3 - "${frontend_files[@]}" <<'PY'
import pathlib
import re
import sys

files = []
for name in sys.argv[1:]:
    text = pathlib.Path(name).read_text(encoding="utf-8", errors="replace")
    files.append((name, text))

all_text = "\n".join(text for _, text in files)
auth_surface = re.search(r"\b(?:login|logout|register|isAuthenticated|Authorization|Bearer|authToken)\b", all_text, re.I)
backend_validation = re.search(
    r"fetch\s*\([^;]{0,600}(?:/me\b|whoami|validate[-_/]?session|['\"]?(?:operation|op|action)['\"]?\s*:\s*['\"]me['\"])"
    r"|\b(?:bootstrapAuth|restoreAuth|validateSession|getCurrentUser|loadCurrentUser)\s*\(",
    all_text,
    re.I | re.S,
)
if auth_surface and not backend_validation:
    direct_cache = re.compile(
        r"(?:useState\s*\([^;]{0,300}|set(?:User|Profile|Account|CurrentUser)\s*\([^;]{0,300})"
        r"JSON\.parse\s*\(\s*(?:window\.)?localStorage\.getItem\s*\(\s*['\"][^'\"]*(?:user|profile|account)[^'\"]*['\"]",
        re.I | re.S,
    )
    for name, text in files:
        match = direct_cache.search(text)
        if match:
            line = text.count("\n", 0, match.start()) + 1
            print(f"{name}:{line}: cached browser identity initializes authenticated state")
PY
  )
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
