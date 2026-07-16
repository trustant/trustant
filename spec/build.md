# Build Scripts

`build.sh` is the compatibility entrypoint for building the Trustable image.

Modes:

- `TRUSTABLE_BUILD_MODE=auto` (default): use the macOS Trustable VM flow when
  `~/Library/Application Support/Trustable/id_ed25519` and `current.ip` exist;
  otherwise delegate to `build-server.sh`.
- `TRUSTABLE_BUILD_MODE=mac`: require the macOS Trustable VM files and use the
  SSH/import/deploy flow.
- `TRUSTABLE_BUILD_MODE=server`: run the Linux/k3s server flow directly.

`build-server.sh` is for running on the Trustable k3s server. It builds the Go
binary for linux/amd64 and linux/arm64, builds the Docker image through
`image/image.sh`, imports the image into local k3s containerd with
`docker save ... | sudo -n k3s ctr images import -`, then patches
`StatefulSet/trustable` in namespace `nuvolaris`.

`image/image.sh` builds the base image from the part of `image/Dockerfile`
before the `###---###` separator. The base image installs
`openserverless-mcp` from the repository `mcp` submodule and compiles OpenCode
from the pinned `trustable-code` submodule. Before building, the script stages
both submodules into the Docker build context. The base-image hash includes the
base Dockerfile, both submodule commits, and the browser MCP source hash. This
guarantees that any runtime pointer or browser-tool change rebuilds the base
image used by Trustable.

The Lima `setup.sh` development path mirrors the image: it installs the pinned
Bun toolchain, builds the same `trustable-code` commit, verifies the expected
OpenCode version and Trustable runtime marker, and atomically installs the
result in `~/.local/bin/opencode`. It packages both the local OpenServerless MCP
submodule and the local browser MCP, installs pinned Playwright Chromium, and
routes `cluster.local` DNS to the local k3s CoreDNS service. It must not use the
upstream OpenCode installer or a direct Apache OpenServerless MCP Git install
as a substitute for either checked-out source tree.
The Lima build-state identifier includes both the pinned Trustable Code commit
and a working-tree content fingerprint. This keeps release/image builds pinned
to commits while allowing an uncommitted local Trustable Code change to be
compiled and validated during development.

For the macOS Lima flow, `start.sh` resolves the Trustable Code commit on the
host and passes it explicitly to `setup.sh`. The guest must not follow a nested
submodule `.git` path into host-only Git metadata. `setup.sh` computes the
working-tree fingerprint directly from the mounted source while excluding Git
metadata, dependencies, build output, caches, and Bun build markers. A direct
Ubuntu/WSL setup resolves the commit locally when Git metadata is available;
otherwise it uses the source fingerprint as a stable fallback and continues.

Server build environment:

- `TRUSTABLE_IMAGE`: image repository, default
  `ghcr.io/trustable-ai/trustable-app`.
- `TRUSTABLE_BUILD_TAG`: explicit image tag. If omitted, the tag is
  `<key>_<version>_<yy.jjj.HHMM>`.
- `TRUSTABLE_BUILD_SKIP_DEPLOY=1`: build the image but do not require local
  k3s/kubectl access, import the image, or patch k3s.
- `TRUSTABLE_K8S_NAMESPACE`: namespace, default `nuvolaris`.
- `TRUSTABLE_K8S_STATEFULSET`: StatefulSet name, default `trustable`.
- `TRUSTABLE_K8S_CONTAINER`: container name, default `trustable`.

Publishing environment:

- `TRUSTABLE_PUBLISH_BRANCH`: branch ref updated by `publish.sh`, defaulting to
  the current branch. Release branches can publish to `main` with
  `TRUSTABLE_PUBLISH_BRANCH=main ./publish.sh`.
  After image CI succeeds, `publish.sh` may push `olaris-bestia`, but any push
  to `olaris`, `olaris-bestia`, `olaris-trustable`, or another `olaris*` repo
  requires explicit user authorization first. If there are no local changes in
  that subrepo it skips the commit step and only pushes.
- `TRUSTABLE_K8S_ROLLOUT_TIMEOUT`: rollout timeout, default `300s`.

The server flow does not update `olaris-bestia/opsroot.json`; that file belongs
to the release/plugin publishing flow. Local server builds are for testing the
image currently being developed.
