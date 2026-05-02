#!/bin/bash
cd "$(dirname $0)"
if ! test -e ~/Library/Application\ Support/Trustable/id_ed25519
then echo "This script must be run on Mac after installing Trustable
     exit 1
fi
ID=~/Library/Application\ Support/Trustable/id_ed25519
IP="$(cat ~/Library/Application\ Support/Trustable/current.ip)"

KEY=${1:-trustable}
VERSION=0.3.3-alpha
IMAGE=ghcr.io/trustable-ai/trustable-app
TAG="${KEY}_${VERSION}_$(date +%y.%j.%H%S)"

git tag -d $(git tag)
echo -e "Version: v${VERSION}\nBuild: $TAG\nExpiry: 2026/06/30\n" >version.txt

OPSROOT="./olaris-bestia/opsroot.json"
jq --arg img "$IMAGE:$TAG" '.config.images.'$KEY' = $img' "$OPSROOT" > "$OPSROOT.tmp" && mv "$OPSROOT.tmp" "$OPSROOT"

git commit -m "build $TAG" -a
git tag $TAG

mkdir -p image/bin
env GOOS=linux GOARCH=amd64 go build -o image/bin/trustable-amd64
env GOOS=linux GOARCH=arm64 go build -o image/bin/trustable-arm64
cp -v trustable.json image/trustable.json

image/image.sh "$TAG"


ssh -i "$ID" trustable@"$IP" sudo k3s ctr images

#~/.ops/*-*/bin/kind load docker-image $IMAGE:$TAG -n nuvolaris


ops trustable trustable redeploy KEY=$KEY

cd olaris-trustable
git commit -m "$TAG" -a
git tag $TAG
sleep 3
ops trustable signin
echo "Delete and recreate the tests to avoid permission issues"
echo "do git push origin main --tags to trigger the build"
echo "check here https://github.com/trustable-ai/trustable-app/actions when the build completes"
echo "do cd olaris-trustable ; git push origin main --tags to publish the plugin when ready"


