#!/bin/bash
# Build and push Docker images for trustable-app
# Usage: ./image.sh [tag]
set -euo pipefail
cd "$(dirname "$0")"

IMAGE="ghcr.io/trustable-ai/trustable-app"
DOCKERFILE="Dockerfile"
SEPARATOR='###---###'
MCP_CONTEXT_DIR="openserverless-mcp"

cleanup() {
    rm -f Dockerfile.base Dockerfile.current
    rm -rf "$MCP_CONTEXT_DIR"
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

if [ ! -f ../mcp/package.json ]; then
    echo "Error: ../mcp is not initialized. Run: git submodule update --init mcp" >&2
    exit 1
fi

MCP_REF="$(git -C ../mcp rev-parse HEAD)"
echo "Using openserverless-mcp submodule: $MCP_REF"
rm -rf "$MCP_CONTEXT_DIR"
mkdir -p "$MCP_CONTEXT_DIR"
git -C ../mcp archive HEAD | tar -x -C "$MCP_CONTEXT_DIR"

# Split the Dockerfile at the separator
SEPARATOR_LINE=$(grep -n "^${SEPARATOR}$" "$DOCKERFILE" | cut -d: -f1)
if [ -z "$SEPARATOR_LINE" ]; then
    echo "Error: separator '$SEPARATOR' not found in $DOCKERFILE"
    exit 1
fi

head -n "$((SEPARATOR_LINE - 1))" "$DOCKERFILE" > Dockerfile.base
tail -n "+$((SEPARATOR_LINE + 1))" "$DOCKERFILE" > Dockerfile.current

# Calculate hash of the base Dockerfile plus the MCP submodule commit because
# the base image installs openserverless-mcp from that local submodule.
BASE_HASH=$({
    sha256sum Dockerfile.base
    printf 'openserverless-mcp=%s\n' "$MCP_REF"
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
