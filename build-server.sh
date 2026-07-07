#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

need() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "Missing required command: $1" >&2
        exit 1
    fi
}

KEY="${1:-trustable}"
VERSION="$(cat version.txt)"
EXPIRY="$(cat expiry.txt)"
IMAGE="${TRUSTABLE_IMAGE:-ghcr.io/trustable-ai/trustable-app}"
TAG="${TRUSTABLE_BUILD_TAG:-${KEY}_${VERSION}_$(date +%y.%j.%H%M)}"
NAMESPACE="${TRUSTABLE_K8S_NAMESPACE:-nuvolaris}"
STATEFULSET="${TRUSTABLE_K8S_STATEFULSET:-trustable}"
CONTAINER="${TRUSTABLE_K8S_CONTAINER:-trustable}"

need docker
need go

echo "Server build tag: $TAG"
printf "Version: %s\nBuild: %s\nExpiry: %s\n" "$VERSION" "$TAG" "$EXPIRY" > _build.txt
git tag -f "$TAG"

mkdir -p image/bin
env GOOS=linux GOARCH=amd64 go build -o image/bin/trustable-amd64
env GOOS=linux GOARCH=arm64 go build -o image/bin/trustable-arm64
cp -v trustable.json image/trustable.json

image/image.sh "$TAG"

if [ "${TRUSTABLE_BUILD_SKIP_DEPLOY:-}" = "1" ]; then
    echo "TRUSTABLE_BUILD_SKIP_DEPLOY=1, skipping local k3s deploy"
    exit 0
fi

need kubectl
need sudo

if ! sudo -n k3s ctr images list >/dev/null 2>&1; then
    echo "Cannot access local k3s containerd with sudo -n k3s." >&2
    echo "Run this on the Trustable k3s server with passwordless sudo for k3s." >&2
    exit 1
fi

if ! kubectl -n "$NAMESPACE" get statefulset "$STATEFULSET" >/dev/null 2>&1; then
    echo "StatefulSet $NAMESPACE/$STATEFULSET not found." >&2
    exit 1
fi

echo "Importing $IMAGE:$TAG into local k3s"
docker save "$IMAGE:$TAG" | sudo -n k3s ctr images import -

echo "Patching $NAMESPACE/$STATEFULSET container $CONTAINER"
kubectl -n "$NAMESPACE" set image "statefulset/$STATEFULSET" "$CONTAINER=$IMAGE:$TAG"
kubectl -n "$NAMESPACE" rollout status "statefulset/$STATEFULSET" --timeout="${TRUSTABLE_K8S_ROLLOUT_TIMEOUT:-300s}"

echo "Done: $IMAGE:$TAG"
