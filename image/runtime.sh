#!/usr/bin/env bash
# Container runtime detection shared by image.sh and build.sh.
#
# WHY: build hosts are not uniform. The Mac VM flow has Docker, but the Linux
# k3s server ships nerdctl against k3s' own containerd and no Docker at all.
# Detecting once, here, keeps the three build scripts from drifting apart on
# which binary, socket and namespace they use.
#
# Source this file, then call detect_runtime. It exports:
#   RUNTIME       - "docker" or "nerdctl"
#   RUNTIME_CMD   - full command array prefix, use as "${RUNTIME_CMD[@]}"
#   RUNTIME_LOADS_K3S - "1" when builds land directly in k3s' image store,
#                       so `save | k3s ctr images import` is redundant.
#
# Override with TRUSTABLE_CONTAINER_RUNTIME=docker|nerdctl.

# k3s keeps its containerd socket out of the default location, and images must
# land in the k8s.io namespace or the kubelet cannot see them.
K3S_CONTAINERD_SOCK="${TRUSTABLE_CONTAINERD_ADDRESS:-/run/k3s/containerd/containerd.sock}"
NERDCTL_NAMESPACE="${TRUSTABLE_CONTAINERD_NAMESPACE:-k8s.io}"

# nerdctl needs buildkitd running; on the k3s server the unit ships disabled.
ensure_buildkit() {
    if [ -S /run/buildkit/buildkitd.sock ] || sudo -n test -S /run/buildkit/buildkitd.sock 2>/dev/null; then
        return 0
    fi
    if command -v systemctl >/dev/null 2>&1 && systemctl list-unit-files buildkit.service >/dev/null 2>&1; then
        echo "Starting buildkit for nerdctl builds" >&2
        sudo -n systemctl start buildkit || {
            echo "Could not start buildkit.service; nerdctl build needs it." >&2
            return 1
        }
        # buildkitd is Type=notify, but the socket can lag the unit by a moment.
        for _ in 1 2 3 4 5 6 7 8 9 10; do
            sudo -n test -S /run/buildkit/buildkitd.sock 2>/dev/null && return 0
            sleep 1
        done
    fi
    echo "buildkitd socket not available; nerdctl build requires buildkitd." >&2
    return 1
}

detect_runtime() {
    local want="${TRUSTABLE_CONTAINER_RUNTIME:-auto}"

    if [ "$want" = "docker" ] || { [ "$want" = "auto" ] && command -v docker >/dev/null 2>&1; }; then
        if ! command -v docker >/dev/null 2>&1; then
            echo "TRUSTABLE_CONTAINER_RUNTIME=docker but docker is not installed" >&2
            exit 1
        fi
        RUNTIME="docker"
        RUNTIME_CMD=(docker)
        RUNTIME_LOADS_K3S="0"
        export RUNTIME RUNTIME_LOADS_K3S
        return 0
    fi

    if [ "$want" = "nerdctl" ] || { [ "$want" = "auto" ] && command -v nerdctl >/dev/null 2>&1; }; then
        if ! command -v nerdctl >/dev/null 2>&1; then
            echo "TRUSTABLE_CONTAINER_RUNTIME=nerdctl but nerdctl is not installed" >&2
            exit 1
        fi
        ensure_buildkit || exit 1
        RUNTIME="nerdctl"
        RUNTIME_CMD=()
        # The k3s containerd socket is root-owned; unprivileged users need sudo.
        if [ ! -w "$K3S_CONTAINERD_SOCK" ]; then
            RUNTIME_CMD+=(sudo -n)
        fi
        RUNTIME_CMD+=(nerdctl)
        if [ -S "$K3S_CONTAINERD_SOCK" ]; then
            RUNTIME_CMD+=(--address "$K3S_CONTAINERD_SOCK" --namespace "$NERDCTL_NAMESPACE")
            # Building here writes straight into the store the kubelet reads.
            RUNTIME_LOADS_K3S="1"
        else
            RUNTIME_LOADS_K3S="0"
        fi
        export RUNTIME RUNTIME_LOADS_K3S
        return 0
    fi

    echo "No container runtime found: install docker or nerdctl." >&2
    exit 1
}
