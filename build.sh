#!/usr/bin/env bash
# Copyright 2025-2026 Nuvolaris Inc
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU Affero General Public License as published
# by the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU Affero General Public License for more details.
#
# You should have received a copy of the GNU Affero General Public License
# along with this program.  If not, see <https://www.gnu.org/licenses/>.

set -euo pipefail

cd "$(dirname "$0")"

# One build path for both hosts. The only difference is how the built image
# reaches the cluster: on a Mac the cluster lives in the Trustant VM, so the
# image is shipped over ssh; on the k3s server the build already wrote it into
# the local containerd. Everything else -- tag, _build.txt, opsroot.json and the
# redeploy -- is identical, so the deployment plugin always records the image
# that was actually built.
MAC_DIR="${TRUSTANT_MAC_SUPPORT_DIR:-$HOME/Library/Application Support/Trustant}"
MAC_ID="$MAC_DIR/id_ed25519"
MAC_IP="$MAC_DIR/current.ip"

MAC_VM=false
if [ -e "$MAC_ID" ] && [ -e "$MAC_IP" ]; then
    MAC_VM=true
    ID="$MAC_ID"
    IP="$(cat "$MAC_IP")"
fi

KEY=trustant
VERSION="$(cat version.txt)"
EXPIRY="$(cat expiry.txt)"
IMAGE="${TRUSTANT_IMAGE:-ghcr.io/trustant/trustant}"
OPSROOT="./oplugins-truinst/opsroot.json"

host_arch() {
    case "$(uname -m)" in
    x86_64) echo amd64 ;;
    aarch64 | arm64) echo arm64 ;;
    *) uname -m ;;
    esac
}

# Compute the tag, replace every existing one, and write _build.txt.
#
# Delete every existing tag before creating this build's one. Quoting
# "$(git tag)" passed the whole list as a SINGLE argument with embedded
# newlines, so git reported `tag 'a\nb\nc' not found`, `|| true` swallowed it,
# and nothing was ever deleted -- tags accumulated on every build. xargs splits
# on newlines and -r skips the call when there are none; same form image.sh
# already uses.
make_tag() {
    # version.txt must be v<major>.<minor>(<letters>|.)<patch>; opsroot records
    # it as v<major>.<minor>.<patch> (v1.0beta1 -> v1.0.1). Checked before any
    # tag is touched.
    if [[ ! "$VERSION" =~ ^v([0-9]+)\.([0-9]+)([A-Za-z]+|\.)([0-9]+)$ ]]; then
        echo "version.txt: '$VERSION' must match v<major>.<minor>(<word>|.)<patch>" >&2
        exit 1
    fi
    local opsver="v${BASH_REMATCH[1]}.${BASH_REMATCH[2]}.${BASH_REMATCH[4]}"

    TAG="${TRUSTANT_BUILD_TAG:-${KEY}_${VERSION}_$(date +%y.%j.%H%M)}"
    BRANCH="${TRUSTANT_BUILD_BRANCH:-$(git branch --show-current 2>/dev/null || true)}"
    BRANCH="${BRANCH:-detached}"
    STREAM="${TRUSTANT_BUILD_STREAM:-$BRANCH}"
    echo "New Tag: $TAG"

    git tag -l | xargs -r git tag -d
    git tag -f "$TAG"
    write_build_txt "$TAG"

    jq --arg img "$IMAGE:$TAG" --arg ver "$opsver" \
        ".config.images.$KEY = \$img | .config.version.$KEY = \$ver | del(.config.$KEY)" "$OPSROOT" >"$OPSROOT.tmp" &&
        mv "$OPSROOT.tmp" "$OPSROOT"
    # reread tag
    TAG=$(jq ".config.images.$KEY" <"$OPSROOT" -r | awk -F: '{print $NF}')
    echo "Stored Tag: $TAG"
    echo "Stored Version: $(jq -r ".config.version.$KEY" <"$OPSROOT")"

    if [[ -f .env ]]; then
        source ./.env
        git config user.name "$GIT_USER"
        git config user.email "$GIT_EMAIL"
    fi
    git commit -m "build $TAG" -a || true
}

# Go embeds _build.txt at compile time (main.go), so this must run before
# `go build`. parseVersion reads Branch and Stream; without them the UI falls
# back to the mounted repo's branch.
write_build_txt() {
    local tag="$1" branch stream
    branch="${TRUSTANT_BUILD_BRANCH:-$(git branch --show-current 2>/dev/null || true)}"
    branch="${branch:-detached}"
    stream="${TRUSTANT_BUILD_STREAM:-$branch}"
    printf "Version: %s\nBuild: %s\nBranch: %s\nStream: %s\nExpiry: %s\n" \
        "$VERSION" "$tag" "$branch" "$stream" "$EXPIRY" >_build.txt
}

usage() {
    local tag
    tag="$(git tag -l | head -1)"
    cat <<'USAGE'
Usage: ./build.sh <mode> [--no-deploy]

Builds the full Trustant image (~20 minutes: submodules, TruACP/pi-acp and the
MCP stages). For a binary-only change see ./hotfix.sh, which layers onto the
existing image in a minute or two.

  --build [--no-deploy]   tag, compile the host arch, build the image, ship it
                          to the cluster and `ops truinst trustant redeploy`.
                          --no-deploy stops after the image is built.
  --buildx                build both arches from the current tag and push the
                          multiarch manifest to the registry (used by CI).
  --tag                   generate the tag and update opsroot.json only;
                          build nothing.
USAGE
    echo
    if [ -n "$tag" ]; then
        echo "Current tag: $tag"
        echo "Run 'git push --tags' to build this in the cloud."
    else
        echo "No tag yet. Run './build.sh --tag' first."
    fi
}

MODE="${1:-}"
SECOND="${2:-}"

NO_DEPLOY=false
if [ "$SECOND" = "--no-deploy" ]; then
    NO_DEPLOY=true
elif [ -n "$SECOND" ]; then
    echo "Unknown argument: $SECOND" >&2
    exit 1
fi
# Kept working for compatibility; --no-deploy is the documented form.
if [ "${TRUSTANT_BUILD_SKIP_DEPLOY:-}" = "1" ]; then
    NO_DEPLOY=true
fi

case "$MODE" in
--tag | --buildx)
    # --tag builds nothing and --buildx never deploys, so --no-deploy is
    # meaningless there. Reject it rather than ignoring it silently.
    if [ "$SECOND" = "--no-deploy" ]; then
        echo "--no-deploy is only valid with --build" >&2
        exit 1
    fi
    ;;
esac

case "$MODE" in
"")
    usage
    exit 0
    ;;

--tag)
    make_tag
    echo
    echo "Run 'git push --tags' to build this in the cloud."
    exit 0
    ;;

--buildx)
    # CI path: the tag already exists and is pushed. Never tags, never commits,
    # no opsroot write -- CI runs on a detached checkout of that tag.
    REF="${GITHUB_REF:-}"
    TAG="${TRUSTANT_BUILD_TAG:-${REF#refs/tags/}}"
    if [ -z "$TAG" ] || [ "$TAG" = "$REF" ]; then
        echo "--buildx needs a tag: set TRUSTANT_BUILD_TAG or run from a tag ref" >&2
        exit 1
    fi
    echo "Building $IMAGE:$TAG"

    write_build_txt "$TAG"
    mkdir -p image/bin
    env GOOS=linux GOARCH=amd64 go build -o image/bin/trustant-amd64
    env GOOS=linux GOARCH=arm64 go build -o image/bin/trustant-arm64
    cp -v trustant.json image/trustant.json
cp -v LICENSE NOTICE DEPS image/

    image/image.sh "$TAG" --push
    exit 0
    ;;

--build) ;;

*)
    echo "Unknown mode: $MODE" >&2
    echo >&2
    usage >&2
    exit 1
    ;;
esac

# ------------------------------------------------------------------- --build

# shellcheck source=image/runtime.sh
. ./image/runtime.sh
detect_runtime
echo "Using container runtime: $RUNTIME"

make_tag

ARCH="$(host_arch)"
mkdir -p image/bin
# Host arch only: a single-arch local image never uses the other binary.
env GOOS=linux GOARCH="$ARCH" go build -o "image/bin/trustant-$ARCH"
cp -v trustant.json image/trustant.json
cp -v LICENSE NOTICE DEPS image/

image/image.sh "$TAG"

if $NO_DEPLOY; then
    echo "--no-deploy: built $IMAGE:$TAG, not shipped"
    exit 0
fi

if $MAC_VM; then
    # The VM cluster cannot see the local image store, so export and import.
    # Prune first: the VM disk is small and old images accumulate.
    ops truinst trustant undeploy
    ssh -i "$ID" trustant@"$IP" sudo k3s ctr images prune --all

    echo "Saving $IMAGE:$TAG"
    "${RUNTIME_CMD[@]}" save "$IMAGE:$TAG" | ssh -i "$ID" trustant@"$IP" sudo k3s ctr images import -

    echo "Listing Images"
    ssh -i "$ID" trustant@"$IP" sudo k3s ctr images list | grep trustant
elif [ "$RUNTIME_LOADS_K3S" != "1" ]; then
    # Docker on the k3s server: the build landed in Docker's own store, so the
    # image still has to be handed to containerd.
    echo "Importing $IMAGE:$TAG into local k3s"
    "${RUNTIME_CMD[@]}" save "$IMAGE:$TAG" | sudo -n k3s ctr images import -
else
    echo "Image $IMAGE:$TAG already in local k3s (built via $RUNTIME)"
fi

ops truinst trustant redeploy

# Print where the deployed UI answers. The ingress serves it at
# trustant.<apihost domain> (oplugins-truinst), so that is the URL on a real
# cluster. A dev cluster's apihost is miniops.me, which resolves to 127.0.0.1
# and so is only reachable on the k3s machine itself: there go through the
# host-rewrite proxy start.sh deploys on :8080, at the address start.sh
# recorded in current.ip (lima0 as a fallback -- never eth0, Lima's NAT-only
# interface). Nothing here may fail the build: the deploy already succeeded.
APIHOST="$(ops util kubeget cm/config .metadata.annotations.apihost 2>/dev/null | tr -d '[:space:]' || true)"
URL=""
[ -z "$APIHOST" ] || URL="${APIHOST%%://*}://trustant.${APIHOST#*://}/"
if [ -z "$APIHOST" ] || [ "${APIHOST#*://}" = "miniops.me" ]; then
    VMIP="$(cat "$MAC_IP" "${XDG_CONFIG_HOME:-$HOME/.config}/trustant/current.ip" 2>/dev/null | head -1 | tr -d '[:space:]' || true)"
    [ -n "$VMIP" ] || VMIP="$(ip -4 -o addr show lima0 2>/dev/null | awk '{print $4}' | cut -d/ -f1 || true)"
    [ -z "$VMIP" ] || URL="http://trustant.${VMIP}.nip.io:8080/"
fi
echo
if [ -n "$URL" ]; then
    echo "Trustant is deployed. open this URL in your browser:"
    echo "    $URL"
else
    echo "Trustant is deployed, but its URL could not be determined."
fi
