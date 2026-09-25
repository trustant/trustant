#!/bin/bash
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

# Build and push Docker images for trustable-app
# Usage: ./image.sh [tag] [--push]
set -euo pipefail
cd "$(dirname "$0")"

IMAGE="ghcr.io/trustant/trustant"
DOCKERFILE="Dockerfile"
# shellcheck source=runtime.sh
. ./runtime.sh
detect_runtime
echo "Using container runtime: $RUNTIME"
# WHY: multi-platform builds need `docker buildx`, but nerdctl accepts
# --platform on plain `build` and has no buildx subcommand.
BUILDX_PREFIX=""
[ "$RUNTIME" = "docker" ] && BUILDX_PREFIX="buildx"
MCP_CONTEXT_DIR="openserverless-mcp"
REACT_CONTEXT_DIR="trustable-react-mcp"
TRUACP_ARTIFACT_DIR="truacp-runtime"

cleanup() {
    # WHY: every staged local MCP must be ephemeral build context. Leaving the
    # React source behind can make a later image build hash stale local files.
    rm -rf "$MCP_CONTEXT_DIR" "$REACT_CONTEXT_DIR" "$TRUACP_ARTIFACT_DIR"
}
trap cleanup EXIT

# Determine the tag
TAG="${1:-}"
# WHY an explicit flag rather than sniffing GITHUB_ACTIONS: the caller decides
# whether this is a registry push. build.sh --buildx passes --push; a local
# build.sh --build does not.
PUSH=false
if [ "${2:-}" = "--push" ]; then
    PUSH=true
fi
if [ -z "$TAG" ]; then
    # Delete all existing tags and generate a new one
    git tag -l | xargs -r git tag -d
    TAG=$(date +%y.%j.%H%S)
    echo "Generated new tag: $TAG"
    git tag "$TAG"
fi

# Detect architecture(s) to build
detect_platforms() {
    if $PUSH; then
        echo "linux/amd64,linux/arm64"
        return
    fi

    local os
    os="$(uname -s)"
    case "$os" in
        Darwin)
            echo "linux/arm64"
            ;;
        MINGW*|MSYS*|CYGWIN*|Windows_NT)
            echo "linux/amd64"
            ;;
        Linux)
            local arch
            arch="$(uname -m)"
            case "$arch" in
                x86_64)  echo "linux/amd64" ;;
                aarch64) echo "linux/arm64" ;;
                *)       echo "linux/$arch" ;;
            esac
            ;;
        *)
            echo "Error: unsupported OS: $os" >&2
            exit 1
            ;;
    esac
}

PLATFORMS=$(detect_platforms)
echo "Building for platforms: $PLATFORMS"

# Local arch in Docker's naming, used for TARGETARCH on single-arch builds.
NERDCTL_ARCH="$(uname -m)"
case "$NERDCTL_ARCH" in
    x86_64)        NERDCTL_ARCH="amd64" ;;
    aarch64|arm64) NERDCTL_ARCH="arm64" ;;
esac
git submodule update --init ../mcp
# WHY: acp owns pinned Pi and pi-acp forks. Recursive initialization
# is required so image builds cannot silently fall back to npm runtimes.
git submodule update --init --recursive ../acp

if [ ! -f ../mcp/package.json ]; then
    echo "Error: ../mcp is not initialized. Run: git submodule update --init mcp" >&2
    exit 1
fi

MCP_REF="$(git -C ../mcp rev-parse HEAD)"
echo "Using openserverless-mcp submodule: $MCP_REF"
rm -rf "$MCP_CONTEXT_DIR"
mkdir -p "$MCP_CONTEXT_DIR"
git -C ../mcp archive HEAD | tar -x -C "$MCP_CONTEXT_DIR"

if [ ! -f ../acp/package.json ]; then
    echo "Error: ../acp is not initialized. Run: git submodule update --init acp" >&2
    exit 1
fi
TRUACP_REF="$(git -C ../acp rev-parse HEAD)"
echo "Using acp submodule: $TRUACP_REF"
# WHY: the managed runtime must package the issue #57 policy extension beside
# the exact TruACP and pi-acp versions that negotiate its typed launch path.
for required in setup.sh pi.version pi.integrity package-lock.json extensions/trustable-runtime.ts; do
    if [ ! -f "../acp/$required" ]; then
        echo "Error: ../acp/$required is missing." >&2
        exit 1
    fi
done
for required in package.json package-lock.json; do
    if [ ! -f "../acp/pi-acp/$required" ]; then
        echo "Error: nested acp/pi-acp/$required is missing." >&2
        exit 1
    fi
done
# Build outside Docker, then stage only the self-contained JavaScript bundle,
# its pinned agent manifest, the tested pi-acp package, and the sole installer.
# Shipping the remaining source or node_modules in an earlier image layer would
# retain them even after rm and would let Docker and VM installation paths drift
# apart.
(
    cd ../acp
    npm install
    npm run build
)
if [ ! -s ../acp/dist-bin/truacp.cjs ]; then
    echo "Error: acp build did not produce dist-bin/truacp.cjs" >&2
    exit 1
fi

rm -rf "$TRUACP_ARTIFACT_DIR"
mkdir -p "$TRUACP_ARTIFACT_DIR/dist-bin"
cp ../acp/setup.sh "$TRUACP_ARTIFACT_DIR/setup.sh"
cp ../acp/pi.version "$TRUACP_ARTIFACT_DIR/pi.version"
cp ../acp/pi.integrity "$TRUACP_ARTIFACT_DIR/pi.integrity"
cp ../acp/dist-bin/truacp.cjs "$TRUACP_ARTIFACT_DIR/dist-bin/truacp.cjs"
mkdir -p "$TRUACP_ARTIFACT_DIR/extensions"
cp ../acp/extensions/trustable-runtime.ts "$TRUACP_ARTIFACT_DIR/extensions/trustable-runtime.ts"
TRUACP_ARTIFACT_ABS="$PWD/$TRUACP_ARTIFACT_DIR"
(
    cd ../acp/pi-acp
    npm ci
    npm test
    npm run build
    npm pack --pack-destination "$TRUACP_ARTIFACT_ABS"
)
set -- "$TRUACP_ARTIFACT_DIR"/pi-acp-*.tgz
if [ "$#" -ne 1 ] || [ ! -f "$1" ]; then
    echo "Error: nested pi-acp build did not produce exactly one package archive." >&2
    exit 1
fi
mv "$1" "$TRUACP_ARTIFACT_DIR/pi-acp-package.tgz"

TRUACP_HASH="$(find "$TRUACP_ARTIFACT_DIR" -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum | cut -c1-12)"
echo "Using staged TruACP artifact hash: $TRUACP_HASH"

if [ ! -f ../react-mcp/package.json ]; then
    echo "Error: ../react-mcp/package.json is missing." >&2
    exit 1
fi
rm -rf "$REACT_CONTEXT_DIR"
mkdir -p "$REACT_CONTEXT_DIR"
tar -C ../react-mcp --exclude=node_modules --exclude='*.log' -cf - . | tar -x -C "$REACT_CONTEXT_DIR"
REACT_HASH="$(find "$REACT_CONTEXT_DIR" -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum | cut -c1-12)"
echo "Using trustable-react-mcp source hash: $REACT_HASH"

# Build the whole Dockerfile in one pass.
# WHY: this used to be split at a separator into a hash-tagged base image plus a
# thin `FROM base` layer, to skip rebuilding the expensive base stages. That
# split only worked under Docker, whose builder shares Docker's image store;
# buildkit (which nerdctl drives) resolves `FROM` against its own cache and the
# registry only, so it could never see the base that had just been built and
# failed with "not found". The builder's own layer cache already makes unchanged
# base stages a no-op, so the split bought nothing that cache does not, and the
# separator is no longer interpreted.
echo "Building image ${IMAGE}:${TAG}..."
if $PUSH; then
    "${RUNTIME_CMD[@]}" $BUILDX_PREFIX build \
        --platform "$PLATFORMS" \
        --tag "${IMAGE}:${TAG}" \
        --file "$DOCKERFILE" \
        --push \
        .
else
    "${RUNTIME_CMD[@]}" build \
        --build-arg "TARGETARCH=${NERDCTL_ARCH}" \
        --tag "${IMAGE}:${TAG}" \
        --file "$DOCKERFILE" \
        .
fi

# Cleanup
cleanup
trap - EXIT

echo "Done: ${IMAGE}:${TAG}"
