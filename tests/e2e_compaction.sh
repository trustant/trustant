#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [ "${TRUSTABLE_E2E_APP:-}" = "" ] && [ "${TRUSTABLE_E2E_REPO:-}" = "" ]; then
  export TRUSTABLE_E2E_REPO=trustable-ai/trureact
fi

export TRUSTABLE_E2E_COMPACTION=1
export TRUSTABLE_E2E_PROMPT_TIMEOUT_MS="${TRUSTABLE_E2E_PROMPT_TIMEOUT_MS:-1800000}"
export TRUSTABLE_E2E_COMPACTION_TEST_TIMEOUT_MS="${TRUSTABLE_E2E_COMPACTION_TEST_TIMEOUT_MS:-2400000}"

exec "$ROOT/tests/e2e_issue98.sh" --grep "real compaction" "$@"
