#!/bin/bash
#
# run.sh — dev loop, run INSIDE the trudev VM (see spec/run.md, spec/setup.md).
#
# k3s is local in the VM, so there is no kubefwd and no kubeconfig loopback
# forwarding: the Go app and the MCP servers it spawns reach cluster services
# directly. Run ./setup.sh first to provision the toolchain + MCP servers.
#
cd "$(dirname "$0")"

# On macOS everything lives in the trudev VM, not on the host. Do the whole
# lifecycle from here so the user only ever runs ./run.sh:
#   1. ./start.sh  — provision/boot the VM AND run setup.sh (idempotent)
#   2. re-invoke this same script INSIDE the VM (same dir — Lima mounts this repo
#      at the identical path; same user — start.sh mirrors the host user) to run
#      the dev loop below
#   3. on ^C (or when the loop exits), stop the VM with ./start.sh -s, keeping it
#      for a fast restart next time
if [[ "$(uname)" == "Darwin" ]]; then
    command -v limactl >/dev/null 2>&1 || { echo "limactl not found (brew install lima)" >&2; exit 1; }
    ./start.sh || { echo "start.sh failed" >&2; exit 1; }
    # Ignore ^C on the host: the interrupt reaches the in-VM run.sh (same process
    # group), which handles its own teardown and returns here. We then always stop
    # the VM. `trap ''` keeps this outer script alive through the ^C so we reach it.
    trap '' INT
    limactl shell --workdir "$PWD" trudev "$PWD/run.sh"
    echo; echo "Stopping VM (./start.sh -s)..."
    ./start.sh -s
    exit 0
fi

source ./.env

# Put the Go dirs on PATH so `go` and `air` are found even in a non-interactive
# shell (Ubuntu's .bashrc returns early when non-interactive, so the PATH lines
# setup.sh appends there never run here). First source g's env if present to get
# GOROOT, then add the toolchain dir (GOROOT/bin) and the go install bin dir.
[ -s "$HOME/.g/env" ] && . "$HOME/.g/env"
add_go_dir() {
    [ -n "$1" ] || return 0
    case ":$PATH:" in *":$1:"*) ;; *) export PATH="$1:$PATH" ;; esac
}
if command -v go &>/dev/null; then
    add_go_dir "$(go env GOROOT)/bin"
    GO_BIN="$(go env GOBIN)"; [ -n "$GO_BIN" ] || GO_BIN="$(go env GOPATH)/bin"
    add_go_dir "$GO_BIN"
else
    # go not yet on PATH — fall back to g's default layout.
    add_go_dir "$HOME/.g/go/bin"
    add_go_dir "$HOME/go/bin"
fi


# 1. terminate all processes listening on ports 8910, 5173, 4096
for port in 8910 5173 4096; do
    lsof -ti :"$port" | xargs kill 2>/dev/null
done

# 6. wait until ^c and terminate everything
cleanup() {
    echo "Shutting down..."
    kill "$AIR_PID" 2>/dev/null
    for port in 8910 5173 4096; do
        lsof -ti :"$port" | xargs kill 2>/dev/null
    done
    wait 2>/dev/null
    echo "Done."
}

# 2. trap ^c
trap cleanup INT

# 2b. sanity: k3s must be up and the nuvolaris namespace present. Do NOT call
#     ./start.sh here — that is macOS-only and provisions the VM from the host.
KUBECONFIG_FILE="$HOME/.ops/tmp/kubeconfig"
if [[ ! -f "$KUBECONFIG_FILE" ]]; then
    echo "kubeconfig missing at $KUBECONFIG_FILE — run ./setup.sh first"; exit 1
fi
# Prefer a standalone kubectl; k3s does not always symlink one, so fall back to
# `sudo k3s kubectl` (which reads the root-owned /etc/rancher/k3s/k3s.yaml).
if command -v kubectl &>/dev/null; then
    kubectl_check() { KUBECONFIG="$KUBECONFIG_FILE" kubectl get ns nuvolaris &>/dev/null; }
elif command -v k3s &>/dev/null; then
    kubectl_check() { sudo k3s kubectl get ns nuvolaris &>/dev/null; }
else
    echo "neither kubectl nor k3s found — is the VM package healthy?"; exit 1
fi
if ! kubectl_check; then
    echo "k3s not ready or nuvolaris namespace missing — is the VM package healthy?"; exit 1
fi

# 2c. ensure the local (CPU) ollama is serving on :11434 (OLLAMA_ENDPOINT).
#     start.sh installs it in the VM (owned by another user), so probe the port
#     system-wide with `ss` — unprivileged `lsof -i` only sees this user's own
#     sockets and would miss it, spawning a second server that fails to bind.
if ! ss -ltn 2>/dev/null | grep -q ':11434 '; then
    echo "Starting ollama serve..."
    ollama serve &
fi

sudo chown -Rvf "$(id -u)" "$WORKSPACE_DIR"/*

# 3. launch air in background (hot-reloads the Go binary on :8910)
air &
AIR_PID=$!

# 5. Trustable handles Ollama Cloud sign-in from the web UI via
#    /api/ollama-connect. Do not call `ops trustable signin` here: that helper
#    can invoke legacy Docker-based Ollama commands that are not valid for Lima
#    dev environments.
#
# Resolve the host-reachable URL. lima0 is the host<->guest interface start.sh
# sets up; its address is what the Mac browser can reach. Fall back to 127.0.0.1
# when there is no lima0 (e.g. a plain Linux server VM).
IP="$(ip -4 -o addr show lima0 2>/dev/null | awk '{print $4}' | cut -d/ -f1)"
[[ -n "$IP" ]] || IP="127.0.0.1"
URL="http://trustable.${IP}.nip.io:8910/"

# Wait until air has built and the server is actually listening on :8910, then
# print the URL LAST so it is not buried under air's build output.
for _ in $(seq 1 60); do
    ss -ltn 2>/dev/null | grep -q ':8910 ' && break
    sleep 1
done
echo ""
echo "  ┌──────────────────────────────────────────────────────────────┐"
echo "  │  Trustable is running — open this URL in your browser:         │"
echo "  │                                                                │"
printf '  │    %-58s│\n' "$URL"
echo "  └──────────────────────────────────────────────────────────────┘"
echo ""

# wait until ^c
wait "$AIR_PID"
