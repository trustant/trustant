#!/bin/bash
ops trustable trustable undeploy
git tag -d $(git tag)

TAG=$(date +%Y.%m%d.%H%S)
IMAGE=ghrc.io/trustable-ai/trustable-app
echo -e "Version: v$TAG-beta1\nExpiry: 2026/06/30\n" >version.txt

OPSROOT="$(dirname "$0")/olaris-trustable/opsroot.json"
jq --arg img "$IMAGE:$TAG" '.config.images.trustable = $img' "$OPSROOT" > "$OPSROOT.tmp" && mv "$OPSROOT.tmp" "$OPSROOT"
jq  -r '.ollama|keys[]' <trustable.json >olaris-trustable/model.lst

git commit -m "build $TAG" -a
git tag $TAG

env GOOS=linux GOARCH=amd64 go build -o image/trustable-amd64
env GOOS=linux GOARCH=arm64 go build -o image/trustable-arm64
cp -v trustable.json image/trustable.json
cp -v opencode.md image/opencode.md

docker buildx build image -t "$IMAGE:$TAG" --load

~/.ops/*-*/bin/kind load docker-image $IMAGE:$TAG -n nuvolaris
ops trustable trustable deploy

