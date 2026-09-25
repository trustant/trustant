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

# Ship a rebuilt binary (plus start.sh, .env and trustant.json) on top of the
# image already recorded in oplugins-truinst/opsroot.json.
#
# WHY: image/image.sh takes ~20 minutes -- submodule init, npm install/build for
# acp, npm ci/test/build/pack for the nested pi-acp fork, and three
# staged MCP context dirs, all before the first docker build line. When only the
# Go binary, the container entrypoint, the image .env or the base trustant.json
# changed, none of that work is needed. Layering those four files onto the
# existing image with `FROM <that image>` takes a minute or two instead.
set -euo pipefail

cd "$(dirname "$0")"

IMAGE="${TRUSTANT_IMAGE:-ghcr.io/trustant/trustant}"
KEY=trustant
OPSROOT="./oplugins-truinst/opsroot.json"
DOCKERFILE="Dockerfile.hotfix"

# ---------------------------------------------------------------- tag helpers

# The base tag this hotfix layers on: whatever opsroot.json currently records.
#
# WHY ${ref##*:} and not build.sh's `awk -F: '{print $2}'`: a registry with a
# port (localhost:5000/img:tag) has two colons and awk would return the port.
base_tag() {
    local ref
    ref="$(jq -r ".config.images.$KEY" "$OPSROOT")"
    if [ -z "$ref" ] || [ "$ref" = "null" ]; then
        echo "Cannot read .config.images.$KEY from $OPSROOT" >&2
        echo "Is the oplugins-truinst submodule initialized?" >&2
        exit 1
    fi
    local tag="${ref##*:}"
    # Strip any trailing -<n> so hotfixes chain off the ORIGINAL base rather
    # than nesting: ...2118-1-1 would grow unbounded and double-count.
    echo "${tag%-[0-9]*}"
}

# Next free hotfix number, counted from the REMOTE.
#
# WHY remote: build.sh deletes every local tag on each build, so a local count
# is always 1 and every hotfix would collide on -1.
next_hotfix_tag() {
    local base="$1" n
    n="$(git ls-remote --tags origin "refs/tags/${base}*" 2>/dev/null |
        sed 's|.*refs/tags/||' | grep -v '\^{}' | sort -u | wc -l | tr -d ' ')"
    echo "${base}-${n}"
}

# Create the tag. Never writes opsroot.json and never commits: opsroot keeps
# pointing at the base so the next hotfix chains off it, and the rollout below
# patches the StatefulSet directly instead of going through the plugin.
make_tag() {
    if [ -n "$(git status --porcelain)" ]; then
        echo "Working tree is not clean; commit or stash first." >&2
        echo "A hotfix tag must describe a committed tree." >&2
        exit 1
    fi
    local base tag
    base="$(base_tag)"
    tag="$(next_hotfix_tag "$base")"
    # Keep this exact literal form -- build_script_test.go asserts it. The
    # obvious `git tag -d "$(git tag)"` passes the whole list as ONE argument
    # and silently deletes nothing.
    git tag -l | xargs -r git tag -d
    git tag -f "$tag" >&2
    echo "$tag"
}

# Resolve the tag for a build: explicit override, then CI's ref, then the single
# local tag. Must already carry the -<n> suffix.
resolve_tag() {
    local tag="${TRUSTANT_HOTFIX_TAG:-}"
    if [ -z "$tag" ] && [ -n "${GITHUB_REF:-}" ]; then
        tag="${GITHUB_REF#refs/tags/}"
    fi
    if [ -z "$tag" ]; then
        tag="$(git tag -l | head -1)"
    fi
    if ! [[ "$tag" =~ -[0-9]+$ ]]; then
        echo "Not a hotfix tag: '${tag:-<none>}'" >&2
        echo "Run ./hotfix.sh --tag first." >&2
        exit 1
    fi
    echo "$tag"
}

# ------------------------------------------------------------------ build bits

# Go embeds _build.txt at compile time (main.go), so this MUST run before
# `go build` -- otherwise the hotfix binary reports the BASE build string and
# there is no way to tell from the running UI whether the hotfix is live.
# _build.txt is gitignored, so writing it cannot dirty the tree.
write_build_txt() {
    local tag="$1" version expiry branch stream
    version="$(cat version.txt)"
    expiry="$(cat expiry.txt)"
    branch="${TRUSTANT_BUILD_BRANCH:-$(git branch --show-current 2>/dev/null || true)}"
    branch="${branch:-detached}"
    stream="${TRUSTANT_BUILD_STREAM:-$branch}"
    # Five lines, matching build.sh: parseVersion reads Branch and Stream, and
    # without them the UI falls back to the mounted repo's branch.
    printf "Version: %s\nBuild: %s\nBranch: %s\nStream: %s\nExpiry: %s\n" \
        "$version" "$tag" "$branch" "$stream" "$expiry" >_build.txt
}

host_arch() {
    case "$(uname -m)" in
    x86_64) echo amd64 ;;
    aarch64 | arm64) echo arm64 ;;
    *) uname -m ;;
    esac
}

# The layer itself. Only files that can change without rebuilding the expensive
# MCP/TruACP stages.
#
# The ownership split is not cosmetic: the base image copies env and
# trustant.json as --chown=trustant:trustant into the user's home
# (image/Dockerfile), while the binary and start.sh go to /usr/local/bin as
# root. Root-owned files in that home break the running app, which writes there.
# Destinations are absolute because WORKDIR is set after these COPYs.
write_dockerfile() {
    local base="$1"
    cat >"image/$DOCKERFILE" <<EOF
# Generated by hotfix.sh -- do not edit, do not commit.
FROM $IMAGE:$base
# ARG is stage-scoped, so the base image's declaration does not carry over.
# BuildKit populates it per platform, which is what selects the right binary.
ARG TARGETARCH
USER root
COPY bin/trustant-\$TARGETARCH /usr/local/bin/trustant
COPY start.sh /usr/local/bin/start.sh
RUN chmod 0755 /usr/local/bin/trustant /usr/local/bin/start.sh
COPY --chown=trustant:trustant env /home/trustant/.env
COPY --chown=trustant:trustant trustant.json /home/trustant/trustant.json
CMD ["/usr/local/bin/start.sh"]
WORKDIR /home/trustant
EOF
}

cleanup() {
    rm -f "image/$DOCKERFILE"
}

# image/trustant.json is gitignored and generated -- without this the build
# ships a stale copy from an earlier build, or fails on a clean checkout.
stage_config() {
    cp -v trustant.json image/trustant.json
}

# ---------------------------------------------------------------- k8s rollout

# Resolve kubectl the way run.sh does: prefer a standalone client, fall back to
# k3s' bundled one (which reads the root-owned /etc/rancher/k3s/k3s.yaml). On a
# Mac the cluster lives in the VM, so everything goes over ssh.
kube() {
    if $MAC_VM; then
        ssh -i "$ID" trustant@"$IP" sudo k3s kubectl "$@"
    else
        "${KUBECTL_CMD[@]}" "$@"
    fi
}

detect_kubectl() {
    if $MAC_VM; then
        return 0
    fi
    if command -v kubectl >/dev/null 2>&1; then
        KUBECTL_CMD=(kubectl)
    elif command -v k3s >/dev/null 2>&1; then
        KUBECTL_CMD=(sudo -n k3s kubectl)
    else
        KUBECTL_CMD=()
    fi
}

# --------------------------------------------------------------------- usage

usage() {
    local tag
    tag="$(git tag -l | head -1)"
    cat <<'USAGE'
Usage: ./hotfix.sh <mode> [--no-deploy]

Layers the rebuilt binary, image/start.sh, image/env and trustant.json onto the
image recorded in oplugins-truinst/opsroot.json -- a minute or two instead of the
~20 minutes a full image build takes.

  --build [--no-deploy]   tag, compile the host arch, build the image locally,
                          import it into the cluster, patch the StatefulSet and
                          wait for the rollout.
                          --no-deploy stops after the image is built.
  --buildx                build both arches from the current tag and push the
                          multiarch manifest to the registry (used by CI).
  --tag                   generate the hotfix tag only; build nothing.

A hotfix patches the running StatefulSet directly and is NOT durable: the next
`ops truinst trustant redeploy` reverts it to the image in opsroot.json.
USAGE
    echo
    if [[ "$tag" =~ ^.*_.*_.*-[0-9]+$ ]]; then
        echo "Current hotfix tag: $tag"
        echo "Run 'git push --tags' to build this hotfix in the cloud."
    elif [ -n "$tag" ]; then
        echo "Current tag '$tag' is not a hotfix tag."
        echo "Run './hotfix.sh --tag' first."
    else
        echo "No tag yet. Run './hotfix.sh --tag' first."
    fi
}

# ---------------------------------------------------------------------- main

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
    TAG="$(make_tag)"
    echo "$TAG"
    echo
    echo "Run 'git push --tags' to build this hotfix in the cloud." >&2
    ;;

--buildx)
    # CI path: the tag already exists and is pushed. Never tags, never commits,
    # no clean-tree check -- CI runs on a detached checkout of that tag.
    TAG="$(resolve_tag)"
    BASE="$(base_tag)"
    echo "Hotfix $TAG on top of $BASE"

    write_build_txt "$TAG"
    mkdir -p image/bin
    env GOOS=linux GOARCH=amd64 go build -o image/bin/trustant-amd64
    env GOOS=linux GOARCH=arm64 go build -o image/bin/trustant-arm64
    stage_config

    # shellcheck source=image/runtime.sh
    . ./image/runtime.sh
    detect_runtime
    echo "Using container runtime: $RUNTIME"
    BUILDX_PREFIX=""
    [ "$RUNTIME" = "docker" ] && BUILDX_PREFIX="buildx"

    write_dockerfile "$BASE"
    trap cleanup EXIT
    (
        cd image
        # --push is mandatory: a multiarch manifest cannot be loaded into a
        # local image store.
        "${RUNTIME_CMD[@]}" $BUILDX_PREFIX build \
            --platform linux/amd64,linux/arm64 \
            --tag "$IMAGE:$TAG" \
            --file "$DOCKERFILE" \
            --push \
            .
    )
    echo "Pushed $IMAGE:$TAG"
    ;;

--build)
    TAG="$(make_tag)"
    BASE="$(base_tag)"
    echo "Hotfix $TAG on top of $BASE"

    MAC_DIR="${TRUSTANT_MAC_SUPPORT_DIR:-$HOME/Library/Application Support/Trustant}"
    MAC_ID="$MAC_DIR/id_ed25519"
    MAC_IP="$MAC_DIR/current.ip"
    MAC_VM=false
    if [ -e "$MAC_ID" ] && [ -e "$MAC_IP" ]; then
        MAC_VM=true
        ID="$MAC_ID"
        IP="$(cat "$MAC_IP")"
    fi

    write_build_txt "$TAG"
    ARCH="$(host_arch)"
    mkdir -p image/bin
    # Host arch only: the other binary is never copied into a single-arch image.
    env GOOS=linux GOARCH="$ARCH" go build -o "image/bin/trustant-$ARCH"
    stage_config

    # shellcheck source=image/runtime.sh
    . ./image/runtime.sh
    detect_runtime
    echo "Using container runtime: $RUNTIME"

    write_dockerfile "$BASE"
    trap cleanup EXIT
    (
        cd image
        "${RUNTIME_CMD[@]}" build \
            --build-arg "TARGETARCH=$ARCH" \
            --tag "$IMAGE:$TAG" \
            --file "$DOCKERFILE" \
            .
    )
    cleanup
    trap - EXIT
    echo "Built $IMAGE:$TAG"

    if $NO_DEPLOY; then
        echo "--no-deploy: built $IMAGE:$TAG, not shipped"
        exit 0
    fi

    # Ship the image. Unlike build.sh there is no undeploy and no
    # `ctr images prune --all`: pruning would evict the very base image this
    # hotfix layers on.
    if $MAC_VM; then
        echo "Importing $IMAGE:$TAG into the VM"
        "${RUNTIME_CMD[@]}" save "$IMAGE:$TAG" |
            ssh -i "$ID" trustant@"$IP" sudo k3s ctr images import -
    elif [ "$RUNTIME_LOADS_K3S" != "1" ]; then
        echo "Importing $IMAGE:$TAG into local k3s"
        "${RUNTIME_CMD[@]}" save "$IMAGE:$TAG" | sudo -n k3s ctr images import -
    else
        echo "Image $IMAGE:$TAG already in local k3s (built via $RUNTIME)"
    fi

    detect_kubectl
    if { ! $MAC_VM && [ ${#KUBECTL_CMD[@]} -eq 0 ]; } ||
        ! kube get ns openserverless >/dev/null 2>&1; then
        echo "k3s not reachable -- image built as $IMAGE:$TAG, not rolled out"
        exit 0
    fi

    OLD="$(kube -n openserverless get statefulset/trustant \
        -o jsonpath='{.spec.template.spec.containers[?(@.name=="trustant")].image}' 2>/dev/null || true)"
    echo "Current image: ${OLD:-unknown}"
    echo "New image:     $IMAGE:$TAG"

    kube -n openserverless set image statefulset/trustant "trustant=$IMAGE:$TAG"
    echo "StatefulSet patched, waiting for rollout..."
    kube -n openserverless rollout status statefulset/trustant --timeout=600s

    RUNNING="$(kube -n openserverless get pod trustant-0 \
        -o jsonpath='{.spec.containers[?(@.name=="trustant")].image}' 2>/dev/null || true)"
    echo "Pod trustant-0 now running: ${RUNNING:-unknown}"
    # _build.txt is embedded in the binary, not shipped as a file, so the live
    # build string comes from what the server logs at startup (repo.go).
    kube -n openserverless logs trustant-0 -c trustant --tail=200 2>/dev/null |
        grep -m1 'Build:' || true
    echo
    echo "Rolled out $IMAGE:$TAG"
    echo "NOTE: this patch is not durable -- the next 'ops truinst trustant"
    echo "redeploy' reverts to the image recorded in opsroot.json."
    ;;

*)
    echo "Unknown mode: $MODE" >&2
    echo >&2
    usage >&2
    exit 1
    ;;
esac
