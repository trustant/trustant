#!/bin/bash
cd "$(dirname "$0")"

source ./.env

# 1. terminate all processes listening on ports 8910, 5173, 4096
for port in 8910 5173 4096; do
    lsof -ti :"$port" | xargs kill 2>/dev/null
done

# 6. wait until ^c and terminate everything
cleanup() {
    echo "Shutting down..."
    kill "$AIR_PID" 2>/dev/null
    sudo kill "${KUBEFWD_PIDS[@]}" 2>/dev/null
    for port in 8910 5173 4096; do
        lsof -ti :"$port" | xargs kill 2>/dev/null
    done
    wait 2>/dev/null
    echo "Done."
}

# 2. trap ^c
trap cleanup INT

# 2b. ensure the backend VM is up (kubefwd below needs its kubeconfig).
#     start.sh is idempotent: no-op if healthy, starts it if stopped,
#     reinstalls the package if the VM came back blank.
./start.sh || { echo "Failed to start the backend VM (./start.sh)"; exit 1; }

sudo chown -Rvf "$(id -u)" "$WORKSPACE_DIR"/*

# 3. launch air in background
air &
AIR_PID=$!

# 4. launch kubefwd in background, forwarding the nuvolaris services.
#    Run a SINGLE kubefwd for the whole namespace: one process allocates a
#    distinct loopback IP per service. Running one process per service (with
#    a --field-selector each) made every process independently claim
#    127.1.27.1 for its first service, so they collided on the bind and some
#    forwards (notably redis) silently lost the race. kubefwd also ignores
#    positional service-name args and forwards the whole namespace regardless,
#    so filtering is only honoured via --field-selector / --selector.
#
#    Exclude trustable-svc: it exposes 8910/4096/5173, the exact ports used
#    locally by air, opencode and vite — forwarding it would steal those binds.
#    Everything else (redis, postgres, milvus, seaweedfs, mongodb, ...) is
#    forwarded as before.
KUBEFWD_PIDS=()
sudo kubefwd svc \
    -f 'metadata.name!=trustable-svc' \
    --kubeconfig "$HOME/.ops/tmp/kubeconfig" \
    -n nuvolaris  &
KUBEFWD_PIDS+=($!)

# 5. open the browser
ops trustable signin http://localhost:8910

# wait until ^c
wait "$AIR_PID"
