#!/bin/bash

git tag -d $(git tag)
git tag $(date +%Y.%m%d.%H%S)
TAG=$(git tag)
IMAGE=ghrc.io/trustable-ai/trustable-app

env GOOS=linux GOARCH=amd64 go build -o image/trustable-amd64
env GOOS=linux GOARCH=arm64 go build -o image/trustable-arm64

ops trustable ollama models >image/models.lst

docker  buildx build image -t "$IMAGE:$TAG" --load

~/.ops/*-*/bin/kind load docker-image $IMAGE:$TAG -n nuvolaris


# write the $IMAGE:TAG in olaris-trustable/opsroot.json in config.images.trustable
OPSROOT="$(dirname "$0")/olaris-trustable/opsroot.json"
jq --arg img "$IMAGE:$TAG" '.config.images.trustable = $img' "$OPSROOT" > "$OPSROOT.tmp" && mv "$OPSROOT.tmp" "$OPSROOT"
