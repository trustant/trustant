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
`mcp` submodule, the local browser MCP source, and four TruACP runtime artifacts:
`setup.sh`, `pi.version`, `dist-bin/truacp.cjs`, and
`pi-acp-package.tgz`. Before staging, it recursively initializes TruACP's pinned
`pi-acp` fork, runs its tests/build/package step, and builds the TruACP bundle.
The complete sources and `node_modules` must never enter the Docker build context
or an image layer. The base-image hash includes the base Dockerfile and all
staged runtime identities/content hashes, so any runtime, adapter, or
browser-tool change rebuilds the base image used by Trustable.

The deterministic Pi execution-policy extension planned by issue #57 is not
part of the current issue #58 runtime baseline. Image packaging must not require
or stage the former `trustable-guardrails.ts` placeholder before the complete
host/extension contract is implemented and tested by that work.

`trustable-acp` is tracked as a Git submodule from
`https://github.com/trustable-ai/trustable-acp.git`, following `main` while the
parent repository pins the exact commit. Trustable builds must consume that
checked-out revision and must not download a floating TruACP source archive or
depend on another developer worktree. The host build produces the portable
JavaScript bundle; the Docker build runs the staged `setup.sh` to install that
bundle and the Pi packages pinned by `trustable-acp/pi.version`. OpenCode is not
built or installed.

The Lima `setup.sh` development path mirrors the image: it builds the checked-out
TruACP source, installs the pinned Pi toolchain, builds the same nested
`pi-acp` fork, packages the local OpenServerless/browser MCP sources, and
installs pinned Playwright Chromium with its Linux runtime dependencies. It
installs the Milvus MCP from the exact
`MILVUS_MCP_REPO`/`MILVUS_MCP_REF` declared in `image/Dockerfile`; the checked-in
source is the `trustable-ai/mcp-server-milvus` fork and must not float or silently
fall back to the upstream repository. It must not install floating OpenCode,
public npm `pi-acp`, or OpenServerless MCP
sources, modify the VM DNS configuration, write
`systemd-resolved` drop-ins, or restart systemd services. Resolver policy is
owned by the prepared VM/k3s environment rather than repository setup. The
development setup verifies uv's installed Milvus MCP receipt and replaces a
same-name tool installed from an older source instead of accepting
package-name-only “already installed” output. The
upstream Milvus CLI is pinned and installed globally under `/usr/local/bin`;
`~/.local/bin/milvus_cli` remains reserved for the per-app configured wrapper.
Both environments install `lsof` explicitly because the shared TruACP/Vite
lifecycle uses it to reclaim listeners left without a valid process-group
marker; image behavior must not depend on an undeclared base-package accident.

For the macOS Lima flow, `start.sh` initializes `mcp`, `trustable-acp`, and its
nested `pi-acp` fork recursively on the host before starting the guest. It checks
the nested leaf even when the outer submodule was already populated. A
worktree's `.git` file may point outside the single mounted directory, so
`setup.sh` consumes ordinary mounted files and must never require guest access
to nested submodule Git metadata. The same `setup.sh` supports Ubuntu under
Lima and WSL with local k3s without requiring guest access to Git metadata or
changing guest system services.

New `trudev` instances use a 60 GiB virtual disk. The local k3s service stack,
containerd snapshots, and repeated Trustable image imports exceed the safe
kubelet eviction margin of the former 40 GiB disk during normal development.
Existing instances may be enlarged further in place and are not reduced by
`start.sh`; recreating one must not regress to the smaller allocation.

`start.sh` also creates the Lima-only `apihost-proxy` on port 8080. It rewrites
browser-visible `<label>.<lima-ip>.nip.io` hosts to the corresponding
`<label>.miniops.me` host before forwarding to the in-cluster Traefik service.
This additional Nginx hop must forward WebSocket `Upgrade` and `Connection`
headers, disable request/response buffering, and use bounded 600-second proxy
timeouts. Otherwise TruACP's `/ws` handshake becomes an ordinary HTTP request,
the UI stays idle even though Pi completes the prompt, and `/ws` returns 404
instead of `101 Switching Protocols`.

`start.sh` also installs the pinned official Linux `kubefwd` archive in
`trudev`, selecting amd64 or arm64 and verifying a checked-in SHA-256 before
placing it at `/usr/local/bin/kubefwd`. Repository-root `run.sh` owns exactly
one namespace-wide forwarder for `nuvolaris`, excludes `trustable-svc`, waits
for bounded readiness, and cleans it with the normal development process trap.
This is a VM-host process only: production pods use native Kubernetes Service
DNS and never start `kubefwd`.

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
