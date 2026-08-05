#!/usr/bin/env bash

set -euo pipefail
export TRUSTABLE_E2E_COMMON_LIBRARY=1
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=tests/e2e_provider_common.sh
. "$ROOT/tests/e2e_provider_common.sh"
e2e_provider_main private "$@"
