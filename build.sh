#!/bin/bash
VERSION=0.3.2-alpha
IMAGE=ghcr.io/trustable-ai/trustable-app
TAG=$(date +%y.%j.%H%S)

git tag -d $(git tag)
echo -e "Version: v${VERSION}\nBuild: $TAG\nExpiry: 2026/06/30\n" >version.txt

OPSROOT="$(dirname "$0")/olaris-trustable/opsroot.json"
jq --arg img "$IMAGE:$TAG" '.config.images.trustable = $img' "$OPSROOT" > "$OPSROOT.tmp" && mv "$OPSROOT.tmp" "$OPSROOT"
jq  -r '.ollama|keys[]' <trustable.json >olaris-trustable/model.lst

git commit -m "build $TAG" -a
git tag $TAG

mkdir -p image/bin
env GOOS=linux GOARCH=amd64 go build -o image/bin/trustable-amd64
env GOOS=linux GOARCH=arm64 go build -o image/bin/trustable-arm64
cp -v trustable.json image/trustable.json

image/image.sh "$TAG"
~/.ops/*-*/bin/kind load docker-image $IMAGE:$TAG -n nuvolaris
ops trustable trustable redeploy

cd olaris-trustable
git commit -m "$TAG" -a
git tag $TAG
sleep 3
ops trustable signin
echo "Delete and recreate the tests to avoid permission issues"
echo "do git push origin main --tags to trigger the build"
echo "check here https://github.com/trustable-ai/trustable-app/actions when the build completes"
echo "do cd olaris-trustable ; git push origin main --tags to publish the plugin when ready"


