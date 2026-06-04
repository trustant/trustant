#!/bin/bash
cd "$(dirname "$0")"

source ./.env

# terminate all processes listening on ports 8910, 5173, 4096
for port in 8910 5173 4096; do
    lsof -ti :"$port" | xargs kill 2>/dev/null
done

cleanup() {
    echo "Shutting down..."
    kill "$AIR_PID" 2>/dev/null
    sudo kill "$KUBEFWD_PID" 2>/dev/null
    for port in 8910 5173 4096; do
        lsof -ti :"$port" | xargs kill 2>/dev/null
    done
    wait 2>/dev/null
    echo "Done."
}

trap cleanup INT

sudo chown -Rvf $(id -u) "$WORKSPACE_DIR"/*

# launch air in background
air &
AIR_PID=$!

# launch kubefwd in background
#sudo kubefwd svc --kubeconfig ~/.ops/tmp/kubeconfig -n nuvolaris &
KUBEFWD_PID=$!

# open the browser
ops trustable signin http://localhost:8910

# wait until ^c
wait "$AIR_PID"
