#!/bin/bash
cd "$(dirname "$0")"

cleanup() {
    echo "Shutting down..."
    kill "$AIR_PID" 2>/dev/null
    for port in 5173 4096; do
        lsof -ti :"$port" | xargs kill 2>/dev/null
    done
    wait
    echo "Done."
}

trap cleanup INT

air &
AIR_PID=$!
wait "$AIR_PID"
