#!/bin/bash
killall -9 opencode
set -e
DIR="${1:?app}"
test -d "workspace/$DIR"
cp -v opencode.json "workspace/$DIR/"
cd "workspace/$DIR"
opencode serve --port 4096 --hostname 0.0.0.0 --log-level DEBUG --print-logs
