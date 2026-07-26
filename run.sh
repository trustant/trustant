#!/bin/bash
#
# run.sh — dev loop, run INSIDE the trudev VM (see spec/run.md, spec/setup.md).
#
# The Go app and MCP servers run in the VM host namespace, outside k3s. One
# namespace-wide kubefwd supplies the service-name reachability written by
# `ops ide login`; production pods continue to use native Kubernetes DNS.
# Run ./setup.sh first to provision the toolchain + MCP servers.
#
cd "$(dirname "$0")"

# Go embeds _build.txt at compile time. Release builds create it in build.sh,
# while a fresh worktree has no copy because the file is intentionally ignored.
# Generate local metadata before Air starts so development works from a clean
# checkout. Inside Lima/WSL the mounted .git indirection may be unavailable, so
# an explicit override or a truthful development fallback is used there.
write_dev_build_metadata() {
    local version expiry branch stream build
    version="$(cat version.txt 2>/dev/null || printf 'dev')"
    expiry="$(cat expiry.txt 2>/dev/null || printf '2099/12/31')"
    branch="${TRUSTABLE_BUILD_BRANCH:-$(git branch --show-current 2>/dev/null || true)}"
    branch="${branch:-development}"
    stream="${TRUSTABLE_BUILD_STREAM:-$branch}"
    build="${TRUSTABLE_BUILD_TAG:-local_$(date +%y.%j.%H%M)}"
    printf 'Version: %s\nBuild: %s\nBranch: %s\nStream: %s\nExpiry: %s\n' \
        "$version" "$build" "$branch" "$stream" "$expiry" > _build.txt
}

# On macOS everything lives in the trudev VM, not on the host. Do the whole
# lifecycle from here so the user only ever runs ./run.sh:
#   1. ./start.sh  — provision/boot the VM AND run setup.sh (idempotent)
#   2. re-invoke this same script INSIDE the VM (same dir — Lima mounts this repo
#      at the identical path; same user — start.sh mirrors the host user) to run
#      the dev loop below
#   3. on ^C (or when the loop exits), stop the VM with ./start.sh -s, keeping it
#      for a fast restart next time
if [[ "$(uname)" == "Darwin" ]]; then
    write_dev_build_metadata
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

[[ -s _build.txt ]] || write_dev_build_metadata

source ./.env

# setup.sh persists the selected user-owned npm prefix, but running this script
# from the same non-interactive shell does not reload ~/.bashrc. Reconstruct the
# setup-owned PATH here so TruACP can spawn the pinned pi-acp and Pi binaries on
# the first run after setup as well as after a fresh login.
command -v npm &>/dev/null \
    || { echo "npm is missing — run ./setup.sh first" >&2; exit 1; }
NPM_GLOBAL_PREFIX="${NPM_CONFIG_PREFIX:-$(npm config get prefix 2>/dev/null || true)}"
if [[ -z "$NPM_GLOBAL_PREFIX" || "$NPM_GLOBAL_PREFIX" != /* ]]; then
    echo "npm global prefix is invalid — run ./setup.sh first" >&2
    exit 1
fi
export NPM_CONFIG_PREFIX="$NPM_GLOBAL_PREFIX"
export PATH="$HOME/.local/bin:$NPM_GLOBAL_PREFIX/bin:$PATH"
for runtime_command in pi pi-acp; do
    command -v "$runtime_command" &>/dev/null \
        || { echo "$runtime_command is missing from the configured npm prefix $NPM_GLOBAL_PREFIX — run ./setup.sh first" >&2; exit 1; }
done

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

# Process handles are initialized before traps so an early readiness failure
# cannot leave kubefwd or Air behind.
AIR_PID=""
KUBEFWD_PID=""
KUBEFWD_SUDO_PID=""
KUBEFWD_PID_FILE=""
KUBEFWD_LOG=""
KUBEFWD_RUNTIME_DIR=""

# 6. wait until ^c and terminate everything
cleanup() {
    echo "Shutting down..."
    if [[ -n "$AIR_PID" ]]; then
        kill "$AIR_PID" 2>/dev/null
    fi
    if [[ "$KUBEFWD_PID" =~ ^[0-9]+$ ]]; then
        # WHY: $! belongs to sudo, not to the privileged kubefwd child. Signal
        # the recorded child directly so it restores /etc/hosts and releases
        # forwarded sockets before an immediate development restart.
        sudo -n kill -INT "$KUBEFWD_PID" 2>/dev/null || true
        for _ in $(seq 1 50); do
            sudo -n kill -0 "$KUBEFWD_PID" 2>/dev/null || break
            sleep 0.1
        done
        if sudo -n kill -0 "$KUBEFWD_PID" 2>/dev/null; then
            sudo -n kill -TERM "$KUBEFWD_PID" 2>/dev/null || true
        fi
    elif [[ -n "$KUBEFWD_SUDO_PID" ]]; then
        # WHY: an error before the child publishes its PID must still release
        # the sudo supervisor instead of making EXIT cleanup wait forever.
        kill -INT "$KUBEFWD_SUDO_PID" 2>/dev/null || true
    fi
    if [[ -n "$KUBEFWD_SUDO_PID" ]]; then
        for _ in $(seq 1 50); do
            kill -0 "$KUBEFWD_SUDO_PID" 2>/dev/null || break
            sleep 0.1
        done
        kill -TERM "$KUBEFWD_SUDO_PID" 2>/dev/null || true
        wait "$KUBEFWD_SUDO_PID" 2>/dev/null || true
    fi
    for port in 8910 5173 4096; do
        lsof -ti :"$port" | xargs kill 2>/dev/null
    done
    wait 2>/dev/null
    [[ -z "$KUBEFWD_RUNTIME_DIR" ]] || rm -rf "$KUBEFWD_RUNTIME_DIR"
    echo "Done."
}

# 2. SIGINT/SIGTERM exit through the single EXIT cleanup path, so normal
# failures and Ctrl-C have identical ownership semantics.
trap cleanup EXIT
trap 'exit 130' INT TERM

# 2b. sanity: k3s must be up and the nuvolaris namespace present. Do NOT call
#     ./start.sh here — that is macOS-only and provisions the VM from the host.
KUBECONFIG_FILE="$HOME/.ops/tmp/kubeconfig"
if [[ ! -f "$KUBECONFIG_FILE" ]]; then
    echo "kubeconfig missing at $KUBECONFIG_FILE — run ./setup.sh first"; exit 1
fi
# Prefer a standalone kubectl; k3s does not always symlink one, so fall back to
# `sudo k3s kubectl` (which reads the root-owned /etc/rancher/k3s/k3s.yaml).
if command -v kubectl &>/dev/null; then
    KUBECTL_CMD=(kubectl)
elif command -v k3s &>/dev/null; then
    KUBECTL_CMD=(sudo -n k3s kubectl)
else
    echo "neither kubectl nor k3s found — is the VM package healthy?"; exit 1
fi
kube() {
    KUBECONFIG="$KUBECONFIG_FILE" "${KUBECTL_CMD[@]}" "$@"
}
if ! kube get ns nuvolaris &>/dev/null; then
    echo "k3s not ready or nuvolaris namespace missing — is the VM package healthy?"; exit 1
fi

# 2c. Start one forwarder for the complete namespace. WHY: separate kubefwd
# processes each allocate their first service to the same loopback address and
# race for ports; trustable-svc is excluded because it would steal 8910/4096/5173.
command -v kubefwd &>/dev/null \
    || { echo "kubefwd is missing — run ./start.sh on macOS or install the pinned version before run.sh" >&2; exit 1; }
FORWARD_PROBE_SERVICE="$(
    kube -n nuvolaris get services \
        -o custom-columns=NAME:.metadata.name --no-headers 2>/dev/null \
        | awk '$1 != "trustable-svc" { print $1; exit }'
)"
if [[ -z "$FORWARD_PROBE_SERVICE" ]]; then
    echo "no nuvolaris service is available for kubefwd readiness (trustable-svc is intentionally excluded)" >&2
    exit 1
fi
KUBEFWD_RUNTIME_DIR="$(mktemp -d -t trustable-kubefwd.XXXXXX)"
KUBEFWD_LOG="$KUBEFWD_RUNTIME_DIR/kubefwd.log"
KUBEFWD_PID_FILE="$KUBEFWD_RUNTIME_DIR/kubefwd.pid"
: >"$KUBEFWD_LOG"
# WHY: sudo is only a supervisor here. The root shell records its own PID and
# then execs kubefwd, preserving that PID as the process run.sh must supervise.
# The PID file starts absent inside a private directory: Linux can reject root
# writes to a user-created regular file directly under sticky /tmp.
sudo -n sh -c 'printf "%s\n" "$$" >"$1"; shift; exec "$@"' sh \
    "$KUBEFWD_PID_FILE" kubefwd svc \
    -f 'metadata.name!=trustable-svc' \
    --kubeconfig "$KUBECONFIG_FILE" \
    -n nuvolaris >"$KUBEFWD_LOG" 2>&1 &
KUBEFWD_SUDO_PID=$!

for _ in $(seq 1 50); do
    [[ -s "$KUBEFWD_PID_FILE" ]] && break
    if ! kill -0 "$KUBEFWD_SUDO_PID" 2>/dev/null; then
        echo "kubefwd supervisor exited before recording its child PID:" >&2
        tail -n 80 "$KUBEFWD_LOG" >&2
        exit 1
    fi
    sleep 0.1
done
KUBEFWD_PID="$(cat "$KUBEFWD_PID_FILE" 2>/dev/null || true)"
if [[ ! "$KUBEFWD_PID" =~ ^[0-9]+$ ]]; then
    echo "kubefwd did not publish a valid child PID:" >&2
    tail -n 80 "$KUBEFWD_LOG" >&2
    exit 1
fi

KUBEFWD_READY=false
for _ in $(seq 1 60); do
    if ! sudo -n kill -0 "$KUBEFWD_PID" 2>/dev/null; then
        echo "kubefwd exited before it became ready:" >&2
        tail -n 80 "$KUBEFWD_LOG" >&2
        exit 1
    fi
    if getent ahostsv4 "$FORWARD_PROBE_SERVICE" 2>/dev/null \
        | awk '{print $1}' | grep -q '^127\.'; then
        KUBEFWD_READY=true
        break
    fi
    sleep 1
done
if [[ "$KUBEFWD_READY" != true ]]; then
    echo "kubefwd readiness timed out: ${FORWARD_PROBE_SERVICE} did not resolve to a loopback address" >&2
    tail -n 80 "$KUBEFWD_LOG" >&2
    exit 1
fi
echo "kubefwd ready for namespace nuvolaris (excluding trustable-svc)"

# 2d. ensure the local (CPU) ollama is serving on :11434 (OLLAMA_ENDPOINT).
#     start.sh installs it in the VM (owned by another user), so probe the port
#     system-wide with `ss` — unprivileged `lsof -i` only sees this user's own
#     sockets and would miss it, spawning a second server that fails to bind.
if ! ss -ltn 2>/dev/null | grep -q ':11434 '; then
    echo "Starting ollama serve..."
    ollama serve &
fi

# Own the roots themselves so empty directories and hidden Trustable state are
# handled without an unmatched `*` warning on the first clean run.
sudo chown -Rf "$(id -u):$(id -g)" "$WORKSPACE_DIR" "$WORKBENCH_DIR"

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
