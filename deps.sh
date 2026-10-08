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

# Regenerate DEPS, LICENSE and NOTICE (see spec/20-legal.md). Run it whenever a
# dependency is added, removed or upgraded, and commit the three files.
#
# The inventory is read from legal/deps.manual.txt plus go.mod, the npm
# lockfiles, acp/pi.version, acp/extensions/requirements.txt and
# oplugins/prereq.yml. Components' own LICENSE/NOTICE files come from
# node_modules / the Go module cache, or are downloaded once into the
# gitignored legal/.cache (npm, uv and network access needed). `--clean`
# drops that cache first.
cd "$(dirname "$0")"

if [ "${1:-}" = "--clean" ]; then
    rm -rf legal/.cache
fi

exec python3 legal/deps.py
