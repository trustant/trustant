#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

export TRUSTABLE_E2E_RUN_PROMPT="${TRUSTABLE_E2E_RUN_PROMPT:-1}"
export TRUSTABLE_E2E_AUTH=1
export TRUSTABLE_E2E_PROMPT_EXPECT_CHANGES="${TRUSTABLE_E2E_PROMPT_EXPECT_CHANGES:-1}"
export TRUSTABLE_E2E_EXPECT_OPENSERVERLESS_TOOL="${TRUSTABLE_E2E_EXPECT_OPENSERVERLESS_TOOL:-0}"
export TRUSTABLE_E2E_PROMPT_TIMEOUT_MS="${TRUSTABLE_E2E_PROMPT_TIMEOUT_MS:-1800000}"
export TRUSTABLE_E2E_PROMPT_TEST_TIMEOUT_MS="${TRUSTABLE_E2E_PROMPT_TEST_TIMEOUT_MS:-2100000}"
export TRUSTABLE_E2E_PROMPT="${TRUSTABLE_E2E_PROMPT:-Voglio poter creare un account, accedere alla mia area personale e uscire. Dopo aver effettuato l'accesso devo restare dentro anche se ricarico la pagina. Le pagine personali non devono essere visibili prima dell'accesso. Controlla tu che registrazione, accesso e uscita funzionino davvero nel browser e sistema gli eventuali problemi.}"

exec "$ROOT/tests/e2e_issue98.sh" --grep "can drive an OpenCode prompt|generated authentication flow" "$@"
