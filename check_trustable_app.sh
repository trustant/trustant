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
