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
before the `###---###` separator. It stages `openserverless-mcp` from the pinned
`mcp` submodule, TruACP from the pinned `trustable-acp` submodule, and the local
browser MCP source. The base-image hash includes the base Dockerfile and all
three staged runtime identities/content hashes, so any runtime or browser-tool
change rebuilds the base image used by Trustable.

`trustable-acp` is tracked as a Git submodule from
`https://github.com/trustable-ai/trustable-acp.git`, following `main` while the
parent repository pins the exact commit. Trustable builds must consume that
checked-out revision and must not download a floating TruACP source archive or
depend on another developer worktree. The Docker build compiles TruACP for the
target architecture and installs the Pi packages pinned by
`trustable-acp/pi.version`; OpenCode is not built or installed.

The Lima `setup.sh` development path mirrors the image: it builds the checked-out
TruACP source, installs the pinned Pi toolchain, packages the local
OpenServerless/browser MCP sources, installs pinned Playwright Chromium with
its Linux runtime dependencies, and
routes `cluster.local` DNS to the local k3s CoreDNS service. It must not install
floating OpenCode or OpenServerless MCP sources.

For the macOS Lima flow, `start.sh` initializes `mcp` and `trustable-acp` on the
host before starting the guest. A worktree's `.git` file may point outside the
single mounted directory, so `setup.sh` consumes ordinary mounted files and
must never require guest access to nested submodule Git metadata. The same
`setup.sh` supports Ubuntu under Lima and WSL with local k3s and
`systemd-resolved`.

`run.sh` must also work from a fresh worktree where the ignored `_build.txt`
does not exist. Before starting Air it writes local development build metadata;
the macOS wrapper records the real host worktree branch, while direct Linux/WSL
runs use `TRUSTABLE_BUILD_BRANCH` when supplied and otherwise report the
`development` fallback. Release metadata remains owned by the build scripts.

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
