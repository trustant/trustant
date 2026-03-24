#!/bin/bash
IMAGE="ghcr.io/trustable-ai/trustable-app"
cd "$(dirname "$0")"
git tag -d $(git tag)
git tag $(date +%Y.%m%d.%H%S)
TAG=$(git tag)

git commit -m "$TAG" -a
git push origin main --tags

OPSROOT="$PWD/olaris-trustable/opsroot.json"
jq --arg img "$IMAGE:$TAG" '.config.images.trustable = $img' "$OPSROOT" > "$OPSROOT.tmp" && mv "$OPSROOT.tmp" "$OPSROOT"
jq  -r '.ollama|keys[]' <trustable.json >olaris-trustable/model.lst

cd olaris-trustable
git commit -m "$TAG" -a
git push origin main
cd ..
git commit -m "sync" -a
git push origin main

