#!/bin/bash
echo TODO in 2 STEPS
#$IMAGE="ghcr.io/trustable-ai/trustable-app"
#cd "$(dirname "$0")"
#TAG=$(git tag)

#OPSROOT="$PWD/olaris-trustable/opsroot.json"
#jq --arg img "$IMAGE:$TAG" '.config.images.trustable = $img' "$OPSROOT" > "$OPSROOT.tmp" && mv "$OPSROOT.tmp" "$OPSROOT"
#jq  -r '.ollama|keys[]' <trustable.json >olaris-trustable/model.lst


#git push origin main --tags

#echo "Building $TAG"

#cd olaris-trustable
#git commit -m "$TAG" -a
#git push origin main
#cd ..
#git commit -m "sync" -a
#git push origin main

