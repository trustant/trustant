#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

# One build path for both hosts. The only difference is how the built image
# reaches the cluster: on a Mac the cluster lives in the Trustable VM, so the
# image is shipped over ssh; on the k3s server the build already wrote it into
# the local containerd. Everything else -- tag, _build.txt, opsroot.json and the
# redeploy -- is identical, so the deployment plugin always records the image
# that was actually built.
MAC_DIR="${TRUSTABLE_MAC_SUPPORT_DIR:-$HOME/Library/Application Support/Trustable}"
MAC_ID="$MAC_DIR/id_ed25519"
MAC_IP="$MAC_DIR/current.ip"

MAC_VM=false
if [ -e "$MAC_ID" ] && [ -e "$MAC_IP" ]; then
    MAC_VM=true
    ID="$MAC_ID"
    IP="$(cat "$MAC_IP")"
fi

# shellcheck source=image/runtime.sh
. ./image/runtime.sh
detect_runtime
echo "Using container runtime: $RUNTIME"

KEY=trustable
VERSION="$(cat version.txt)"
EXPIRY="$(cat expiry.txt)"
IMAGE="${TRUSTABLE_IMAGE:-ghcr.io/trustable-ai/trustable-app}"
TAG="${TRUSTABLE_BUILD_TAG:-${KEY}_${VERSION}_$(date +%y.%j.%H%M)}"
BRANCH="${TRUSTABLE_BUILD_BRANCH:-$(git branch --show-current 2>/dev/null || true)}"
BRANCH="${BRANCH:-detached}"
STREAM="${TRUSTABLE_BUILD_STREAM:-$BRANCH}"
echo "New Tag: $TAG"

git tag -d "$(git tag)" || true
git tag -f "$TAG"
printf "Version: %s\nBuild: %s\nBranch: %s\nStream: %s\nExpiry: %s\n" \
    "$VERSION" "$TAG" "$BRANCH" "$STREAM" "$EXPIRY" > _build.txt

OPSROOT="./olaris-bestia/opsroot.json"
jq --arg img "$IMAGE:$TAG" '.config.images.'$KEY' = $img' "$OPSROOT" > "$OPSROOT.tmp" && mv "$OPSROOT.tmp" "$OPSROOT"
# reread tag
TAG=$(jq .config.images.$KEY <$OPSROOT -r | awk -F: '{print $2}')
echo "Stored Tag: $TAG"

git commit -m "build $TAG" -a || true

if test "$1" == "--tag"
then echo "Only tagging, exiting" ; exit 0
if

mkdir -p image/bin
env GOOS=linux GOARCH=amd64 go build -o image/bin/trustable-amd64
env GOOS=linux GOARCH=arm64 go build -o image/bin/trustable-arm64
cp -v trustable.json image/trustable.json

image/image.sh "$TAG"

if [ "${TRUSTABLE_BUILD_SKIP_DEPLOY:-}" = "1" ]; then
    echo "TRUSTABLE_BUILD_SKIP_DEPLOY=1, skipping deploy"
    exit 0
fi

if $MAC_VM; then
    # The VM cluster cannot see the local image store, so export and import.
    # Prune first: the VM disk is small and old images accumulate.
    ops bestia trustable undeploy
    ssh -i "$ID" trustable@"$IP" sudo k3s ctr images prune --all

    echo "Saving $IMAGE:$TAG"
    "${RUNTIME_CMD[@]}" save "$IMAGE:$TAG" | ssh -i "$ID" trustable@"$IP" sudo k3s ctr images import -

    echo "Listing Images"
    ssh -i "$ID" trustable@"$IP" sudo k3s ctr images list | grep trustable
elif [ "$RUNTIME_LOADS_K3S" != "1" ]; then
    # Docker on the k3s server: the build landed in Docker's own store, so the
    # image still has to be handed to containerd.
    echo "Importing $IMAGE:$TAG into local k3s"
    "${RUNTIME_CMD[@]}" save "$IMAGE:$TAG" | sudo -n k3s ctr images import -
else
    echo "Image $IMAGE:$TAG already in local k3s (built via $RUNTIME)"
fi

ops bestia trustable redeploy
