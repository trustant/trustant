#!/bin/bash
# Build and push Docker images for trustable-app
# Usage: ./image.sh [tag]
set -euo pipefail
cd "$(dirname "$0")"

IMAGE="ghcr.io/trustable-ai/trustable-app"
DOCKERFILE="Dockerfile"
SEPARATOR='###---###'
MCP_CONTEXT_DIR="openserverless-mcp"
BROWSER_CONTEXT_DIR="trustable-browser-mcp"
TRUACP_ARTIFACT_DIR="truacp-runtime"

cleanup() {
    rm -f Dockerfile.base Dockerfile.current
    rm -rf "$MCP_CONTEXT_DIR" "$BROWSER_CONTEXT_DIR" "$TRUACP_ARTIFACT_DIR"
}
trap cleanup EXIT

# Determine the tag
TAG="${1:-}"
if [ -z "$TAG" ]; then
    # Delete all existing tags and generate a new one
    git tag -l | xargs -r git tag -d
    TAG=$(date +%y.%j.%H%S)
    echo "Generated new tag: $TAG"
    git tag "$TAG"
fi

# Detect architecture(s) to build
detect_platforms() {
    if [ -n "${GITHUB_ACTIONS:-}" ]; then
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
git submodule update --init ../mcp
# WHY: trustable-acp owns a pinned pi-acp fork. Recursive initialization is
# required so image builds cannot silently fall back to an npm adapter.
git submodule update --init --recursive ../trustable-acp

if [ ! -f ../mcp/package.json ]; then
    echo "Error: ../mcp is not initialized. Run: git submodule update --init mcp" >&2
    exit 1
fi

MCP_REF="$(git -C ../mcp rev-parse HEAD)"
echo "Using openserverless-mcp submodule: $MCP_REF"
rm -rf "$MCP_CONTEXT_DIR"
mkdir -p "$MCP_CONTEXT_DIR"
git -C ../mcp archive HEAD | tar -x -C "$MCP_CONTEXT_DIR"

if [ ! -f ../trustable-acp/package.json ]; then
    echo "Error: ../trustable-acp is not initialized. Run: git submodule update --init trustable-acp" >&2
    exit 1
fi
TRUACP_REF="$(git -C ../trustable-acp rev-parse HEAD)"
echo "Using trustable-acp submodule: $TRUACP_REF"
for required in setup.sh pi.version package-lock.json extensions/trustable-guardrails.ts; do
    if [ ! -f "../trustable-acp/$required" ]; then
        echo "Error: ../trustable-acp/$required is missing." >&2
        exit 1
    fi
done
for required in package.json package-lock.json; do
    if [ ! -f "../trustable-acp/pi-acp/$required" ]; then
        echo "Error: nested trustable-acp/pi-acp/$required is missing." >&2
        exit 1
    fi
done

# Build outside Docker, then stage only the self-contained JavaScript bundle,
# its pinned agent manifest, the tested pi-acp package, the reviewed Pi
# guardrail, and the sole installer. Shipping the remaining source or
# node_modules in an earlier image layer would retain them even after rm and
# would let Docker and VM installation paths drift apart.
(
    cd ../trustable-acp
    npm install
    npm run build
)
if [ ! -s ../trustable-acp/dist-bin/truacp.cjs ]; then
    echo "Error: trustable-acp build did not produce dist-bin/truacp.cjs" >&2
    exit 1
fi

rm -rf "$TRUACP_ARTIFACT_DIR"
mkdir -p "$TRUACP_ARTIFACT_DIR/dist-bin"
cp ../trustable-acp/setup.sh "$TRUACP_ARTIFACT_DIR/setup.sh"
cp ../trustable-acp/pi.version "$TRUACP_ARTIFACT_DIR/pi.version"
cp ../trustable-acp/dist-bin/truacp.cjs "$TRUACP_ARTIFACT_DIR/dist-bin/truacp.cjs"
cp ../trustable-acp/extensions/trustable-guardrails.ts "$TRUACP_ARTIFACT_DIR/trustable-guardrails.ts"
TRUACP_ARTIFACT_ABS="$PWD/$TRUACP_ARTIFACT_DIR"
(
    cd ../trustable-acp/pi-acp
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

if [ ! -f ../browser-mcp/package.json ]; then
    echo "Error: ../browser-mcp/package.json is missing." >&2
    exit 1
fi
rm -rf "$BROWSER_CONTEXT_DIR"
mkdir -p "$BROWSER_CONTEXT_DIR"
tar -C ../browser-mcp --exclude=node_modules --exclude='*.log' -cf - . | tar -x -C "$BROWSER_CONTEXT_DIR"
BROWSER_HASH="$(find "$BROWSER_CONTEXT_DIR" -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum | cut -c1-12)"
echo "Using trustable-browser-mcp source hash: $BROWSER_HASH"

# Split the Dockerfile at the separator
SEPARATOR_LINE=$(grep -n "^${SEPARATOR}$" "$DOCKERFILE" | cut -d: -f1)
if [ -z "$SEPARATOR_LINE" ]; then
    echo "Error: separator '$SEPARATOR' not found in $DOCKERFILE"
    exit 1
fi

head -n "$((SEPARATOR_LINE - 1))" "$DOCKERFILE" > Dockerfile.base
tail -n "+$((SEPARATOR_LINE + 1))" "$DOCKERFILE" > Dockerfile.current

# Calculate the base hash from the Dockerfile and every staged runtime source.
BASE_HASH=$({
    sha256sum Dockerfile.base
    printf 'openserverless-mcp=%s\n' "$MCP_REF"
    printf 'trustable-browser-mcp=%s\n' "$BROWSER_HASH"
    printf 'trustable-acp=%s:%s\n' "$TRUACP_REF" "$TRUACP_HASH"
} | sha256sum | cut -c1-12)
BASE_TAG="base-${BASE_HASH}"
echo "Base image hash: $BASE_TAG"

# Try to find existing base image, build if missing
BASE_EXISTS=false
if [ -n "${GITHUB_ACTIONS:-}" ]; then
    # Check registry
    docker manifest inspect "${IMAGE}:${BASE_TAG}" > /dev/null 2>&1 && BASE_EXISTS=true
else
    # Check local images
    docker image inspect "${IMAGE}:${BASE_TAG}" > /dev/null 2>&1 && BASE_EXISTS=true
fi

if $BASE_EXISTS; then
    echo "Base image ${IMAGE}:${BASE_TAG} already exists, skipping build"
else
    echo "Building base image ${IMAGE}:${BASE_TAG}..."
    if [ -n "${GITHUB_ACTIONS:-}" ]; then
        docker buildx build \
            --platform "$PLATFORMS" \
            --tag "${IMAGE}:${BASE_TAG}" \
            --file Dockerfile.base \
            --push \
            .
    else
        docker build \
            --tag "${IMAGE}:${BASE_TAG}" \
            --file Dockerfile.base \
            .
    fi
fi

# Build the current image on top of the base
# Prepend FROM instruction to Dockerfile.current
{
    echo "FROM ${IMAGE}:${BASE_TAG}"
    echo "ARG TARGETARCH"
    cat Dockerfile.current
} > Dockerfile.current.tmp
mv Dockerfile.current.tmp Dockerfile.current

echo "Building current image ${IMAGE}:${TAG}..."
if [ -n "${GITHUB_ACTIONS:-}" ]; then
    docker buildx build \
        --platform "$PLATFORMS" \
        --tag "${IMAGE}:${TAG}" \
        --file Dockerfile.current \
        --push \
        .
else
    # Detect local arch for TARGETARCH
    LOCAL_ARCH="$(uname -m)"
    case "$LOCAL_ARCH" in
        x86_64)  LOCAL_ARCH="amd64" ;;
        aarch64|arm64) LOCAL_ARCH="arm64" ;;
    esac
    docker build \
        --build-arg "TARGETARCH=${LOCAL_ARCH}" \
        --tag "${IMAGE}:${TAG}" \
        --file Dockerfile.current \
        .
fi

# Cleanup
cleanup
trap - EXIT

echo "Done: ${IMAGE}:${TAG}"
