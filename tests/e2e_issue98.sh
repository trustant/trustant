#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NAMESPACE="${TRUSTABLE_E2E_NAMESPACE:-nuvolaris}"
POD="${TRUSTABLE_E2E_POD:-trustable-0}"
CONTAINER="${TRUSTABLE_E2E_CONTAINER:-trustable}"

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "ERROR missing required command: $1" >&2
    exit 1
  fi
}

detect_domain() {
  if [ "${TRUSTABLE_E2E_DOMAIN:-}" != "" ]; then
    echo "$TRUSTABLE_E2E_DOMAIN"
    return
  fi

  local host
  host="$(kubectl -n "$NAMESPACE" get ingress trustable-ing -o jsonpath='{.spec.rules[0].host}' 2>/dev/null || true)"
  if [ "$host" != "" ]; then
    echo "${host#trustable.}"
    return
  fi

  local apihost
  apihost="$(kubectl -n "$NAMESPACE" get cm config -o jsonpath='{.metadata.annotations.apihost}' 2>/dev/null || true)"
  apihost="${apihost#http://}"
  apihost="${apihost#https://}"
  apihost="${apihost%%/*}"
  if [ "$apihost" != "" ]; then
    echo "$apihost"
    return
  fi

  echo "miniops.me"
}

need kubectl
need npm
need node

kubectl -n "$NAMESPACE" get pod "$POD" >/dev/null

export TRUSTABLE_E2E_NAMESPACE="$NAMESPACE"
export TRUSTABLE_E2E_POD="$POD"
export TRUSTABLE_E2E_CONTAINER="$CONTAINER"
export TRUSTABLE_E2E_DOMAIN="$(detect_domain)"

echo "Trustable issue98 E2E"
echo "  domain:     $TRUSTABLE_E2E_DOMAIN"
echo "  namespace:  $TRUSTABLE_E2E_NAMESPACE"
echo "  pod:        $TRUSTABLE_E2E_POD"
echo "  app:        ${TRUSTABLE_E2E_APP:-<create if TRUSTABLE_E2E_REPO is set>}"
echo

if [ ! -d "$ROOT/node_modules/@playwright/test" ]; then
  (cd "$ROOT" && npm install --no-audit --no-fund)
fi

if [ "${TRUSTABLE_E2E_SKIP_BROWSER_INSTALL:-}" != "1" ]; then
  (cd "$ROOT" && npx playwright install chromium)
fi

cd "$ROOT"
npx playwright test --config tests/playwright.config.mjs tests/issue98.spec.mjs "$@"
