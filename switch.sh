#!/bin/bash
set -euo pipefail
BRANCH=${1:?branch} 
git fetch --all --prune
git switch "$BRANCH" 
git submodule sync --recursive
git submodule update --init --recursive
exec ./setup.sh
