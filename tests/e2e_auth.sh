#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [ "${TRUSTABLE_E2E_APP:-}" = "" ] && [ "${TRUSTABLE_E2E_REPO:-}" = "" ]; then
  export TRUSTABLE_E2E_REPO=trustable-ai/trureact
fi

export TRUSTABLE_E2E_RUN_PROMPT="${TRUSTABLE_E2E_RUN_PROMPT:-1}"
export TRUSTABLE_E2E_AUTH=1
export TRUSTABLE_E2E_PROMPT_EXPECT_CHANGES="${TRUSTABLE_E2E_PROMPT_EXPECT_CHANGES:-1}"
export TRUSTABLE_E2E_EXPECT_OPENSERVERLESS_TOOL=1
export TRUSTABLE_E2E_EXPECT_ACTION_WORKFLOW=1
export TRUSTABLE_E2E_EXPECT_SETUP=1
export TRUSTABLE_E2E_PROMPT_TIMEOUT_MS="${TRUSTABLE_E2E_PROMPT_TIMEOUT_MS:-1800000}"
export TRUSTABLE_E2E_PROMPT_TEST_TIMEOUT_MS="${TRUSTABLE_E2E_PROMPT_TEST_TIMEOUT_MS:-2100000}"
export TRUSTABLE_E2E_AUTH_TIMEOUT_MS="${TRUSTABLE_E2E_AUTH_TIMEOUT_MS:-2700000}"
if [ "${TRUSTABLE_E2E_PROMPT:-}" = "" ]; then
  export TRUSTABLE_E2E_PROMPT="Voglio poter creare un account, accedere alla mia area personale e uscire. Dopo aver effettuato l'accesso devo restare dentro anche se ricarico la pagina. Le pagine personali non devono essere visibili prima dell'accesso. Controlla tu che registrazione, accesso e uscita funzionino davvero nel browser e sistema gli eventuali problemi."
fi

exec "$ROOT/tests/e2e_issue98.sh" --grep "generated authentication flow" "$@"
