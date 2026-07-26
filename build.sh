#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

MODE="${TRUSTABLE_BUILD_MODE:-auto}"
MAC_DIR="${TRUSTABLE_MAC_SUPPORT_DIR:-$HOME/Library/Application Support/Trustable}"
MAC_ID="$MAC_DIR/id_ed25519"
MAC_IP="$MAC_DIR/current.ip"

if [ "$MODE" = "server" ]; then
    exec ./build-server.sh "$@"
fi

if [ "$MODE" = "auto" ] && { [ ! -e "$MAC_ID" ] || [ ! -e "$MAC_IP" ]; }; then
    exec ./build-server.sh "$@"
fi

if [ "$MODE" != "auto" ] && [ "$MODE" != "mac" ]; then
    echo "Unsupported TRUSTABLE_BUILD_MODE=$MODE (expected auto, mac, or server)" >&2
    exit 1
fi

if ! test -e "$MAC_ID"
then echo "This script must be run on Mac after installing Trustable, or on a Linux k3s server with ./build-server.sh"
     exit 1
fi

ID="$MAC_ID"
IP="$(cat "$MAC_IP")"

KEY=${1:-trustable}
VERSION="$(cat version.txt)"
EXPIRY="$(cat expiry.txt)"
IMAGE="${TRUSTABLE_IMAGE:-ghcr.io/trustable-ai/trustable-app}"
TAG="${TRUSTABLE_BUILD_TAG:-${KEY}_${VERSION}_$(date +%y.%j.%H%M)}"
BRANCH="${TRUSTABLE_BUILD_BRANCH:-$(git branch --show-current 2>/dev/null || true)}"
BRANCH="${BRANCH:-detached}"
STREAM="${TRUSTABLE_BUILD_STREAM:-$BRANCH}"
echo "New Tag: $TAG"

git tag -f "$TAG"
printf "Version: %s\nBuild: %s\nBranch: %s\nStream: %s\nExpiry: %s\n" \
    "$VERSION" "$TAG" "$BRANCH" "$STREAM" "$EXPIRY" > _build.txt

OPSROOT="./olaris-bestia/opsroot.json"
jq --arg img "$IMAGE:$TAG" '.config.images.'$KEY' = $img' "$OPSROOT" > "$OPSROOT.tmp" && mv "$OPSROOT.tmp" "$OPSROOT"
# reread tag
TAG=$(jq .config.images.$KEY <$OPSROOT -r | awk -F: '{print $2}')
echo "Stored Tag: $TAG"

git commit -m "build $TAG" -a || true

mkdir -p image/bin
env GOOS=linux GOARCH=amd64 go build -o image/bin/trustable-amd64
env GOOS=linux GOARCH=arm64 go build -o image/bin/trustable-arm64
cp -v trustable.json image/trustable.json

image/image.sh "$TAG"

if [ "${TRUSTABLE_BUILD_SKIP_DEPLOY:-}" = "1" ]; then
    echo "TRUSTABLE_BUILD_SKIP_DEPLOY=1, skipping Mac VM deploy"
    exit 0
fi

ops bestia trustable undeploy
ssh -i "$ID" trustable@"$IP" sudo k3s ctr images prune --all

echo "Saving $IMAGE:$TAG"
docker save $IMAGE:$TAG | ssh -i "$ID" trustable@"$IP" sudo k3s ctr images import -

echo "Listing Images"

ssh -i "$ID" trustable@"$IP" sudo k3s ctr images list | grep trustable

ops bestia trustable deploy


#cd olaris-bestia
#git commit -m "$TAG" -a
#git tag $TAG
