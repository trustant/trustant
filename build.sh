#!/bin/bash
cd "$(dirname $0)"
if ! test -e ~/Library/Application\ Support/Trustable/id_ed25519
then echo "This script must be run on Mac after installing Trustable"
     exit 1
fi

ID=~/Library/Application\ Support/Trustable/id_ed25519
IP="$(cat ~/Library/Application\ Support/Trustable/current.ip)"

KEY=${1:-trustabledev}
VERSION=0.3.3-alpha
IMAGE=ghcr.io/trustable-ai/trustable-app
TAG="${KEY}_${VERSION}_$(date +%y.%j.%H%S)"

git tag -d $(git tag)
echo -e "Version: v${VERSION}\nBuild: $TAG\nExpiry: 2026/06/30\n" >version.txt
git tag $TAG

OPSROOT="./olaris-bestia/opsroot.json"
jq --arg img "$IMAGE:$TAG" '.config.images.'$KEY' = $img' "$OPSROOT" > "$OPSROOT.tmp" && mv "$OPSROOT.tmp" "$OPSROOT"
# reread tag
TAG=$(jq .config.images.$KEY <$OPSROOT -r | awk -F: '{print $2}')

git commit -m "build $TAG" -a

mkdir -p image/bin
env GOOS=linux GOARCH=amd64 go build -o image/bin/trustable-amd64
env GOOS=linux GOARCH=arm64 go build -o image/bin/trustable-arm64
cp -v trustable.json image/trustable.json

image/image.sh "$TAG"

docker save $IMAGE:$TAG | ssh -i "$ID" trustable@"$IP" sudo k3s ctr images import -
ssh -i "$ID" trustable@"$IP" sudo k3s ctr images list | grep $TAG

exit 1

#ops trustable trustable redeploy KEY=$KEY

#cd olaris-trustable
#git commit -m "$TAG" -a
#git tag $TAG
#sleep 3


