#!/usr/bin/env bash

set -u

ROOT="${1:-$(pwd)}"
STATUS=0

run_check() {
  local checker="$1"
  if ! command -v "$checker" >/dev/null 2>&1; then
    echo "ERROR $checker is not available in PATH"
    STATUS=1
    return
  fi
  if ! timeout 60 "$checker" "$ROOT"; then
    STATUS=1
  fi
}

run_check check_openserverless_actions.sh
run_check check_trustable_frontend.sh

if [ "$STATUS" -ne 0 ]; then
  echo "Trustable app completion check failed."
  exit 1
fi

echo "Trustable app completion check passed."
