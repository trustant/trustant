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

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [ "${TRUSTABLE_E2E_APP:-}" = "" ] && [ "${TRUSTABLE_E2E_REPO:-}" = "" ]; then
  export TRUSTABLE_E2E_REPO=trustable-ai/trureact
fi

export TRUSTABLE_E2E_RUN_PROMPT=1
export TRUSTABLE_E2E_EXPECT_ACTION_WORKFLOW=1
export TRUSTABLE_E2E_EXPECT_SETUP=1
export TRUSTABLE_E2E_EXPECT_OPENSERVERLESS_TOOL=1
export TRUSTABLE_E2E_PROMPT_EXPECT_CHANGES=1
export TRUSTABLE_E2E_PROMPT_TIMEOUT_MS="${TRUSTABLE_E2E_PROMPT_TIMEOUT_MS:-2400000}"
export TRUSTABLE_E2E_PROMPT_TEST_TIMEOUT_MS="${TRUSTABLE_E2E_PROMPT_TEST_TIMEOUT_MS:-2700000}"
if [ "${TRUSTABLE_E2E_PROMPT:-}" = "" ]; then
  export TRUSTABLE_E2E_PROMPT="Arricchisci l'applicazione con una pagina chiamata Messaggio di benvenuto. Il messaggio deve essere letto da una vera funzione dell'app e deve essere preparato automaticamente con un valore iniziale quando l'app viene configurata. Mostra il valore nella pagina e aggiungi un pulsante per rileggerlo. Controlla tu che tutto funzioni davvero, senza chiedermi di usare la shell."
fi

exec "$ROOT/tests/e2e_issue98.sh" --grep "OpenCode prompt" "$@"
