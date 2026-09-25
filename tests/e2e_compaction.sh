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

if [ "${TRUSTANT_E2E_APP:-}" = "" ] && [ "${TRUSTANT_E2E_REPO:-}" = "" ]; then
  export TRUSTANT_E2E_REPO=trustable-ai/trureact
fi

export TRUSTANT_E2E_COMPACTION=1
export TRUSTANT_E2E_PROMPT_TIMEOUT_MS="${TRUSTANT_E2E_PROMPT_TIMEOUT_MS:-1800000}"
export TRUSTANT_E2E_COMPACTION_TEST_TIMEOUT_MS="${TRUSTANT_E2E_COMPACTION_TEST_TIMEOUT_MS:-2400000}"

exec "$ROOT/tests/e2e_issue98.sh" --grep "real compaction" "$@"
