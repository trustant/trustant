# Build Scripts

`build.sh` is the single entrypoint for building the Trustable image, on both
the macOS Trustable VM and the Linux k3s server. There is no separate server
script and no build mode to select: the host is detected from the presence of
`~/Library/Application Support/Trustable/id_ed25519` and `current.ip`.

Every build, on every host:

1. computes the tag, forces the git tag, and writes `_build.txt`
2. **writes the image tag into `olaris-bestia/opsroot.json`** via `jq`, then
   commits, so the deployment plugin always records the image just built
3. builds the Go binary for linux/amd64 and linux/arm64
4. builds the container image through `image/image.sh`
5. makes the image reachable by the cluster
6. deploys with `ops bestia trustable redeploy`

Only step 5 differs by host. On the macOS VM the cluster lives inside the VM
and cannot see the local image store, so the image is exported and piped over
ssh into the VM's containerd (preceded by `ops bestia trustable undeploy` and
`k3s ctr images prune --all`, which frees the old image so the VM's small disk
can reclaim it). On the k3s server, an image built by nerdctl is already in the
`k8s.io` namespace the kubelet reads and nothing is done; if Docker built it,
it is imported with `save ... | sudo -n k3s ctr images import -`.

Deployment is always `ops bestia trustable redeploy`, which is `undeploy` +
`deploy`; `deploy` reads the image from `opsroot.json`. The StatefulSet is
never patched directly with `kubectl set image`.

`TRUSTABLE_BUILD_SKIP_DEPLOY=1` stops after the image build, leaving the tag,
`_build.txt` and the `opsroot.json` update in place.

Note that updating `opsroot.json` only records the tag locally. Pushing the
`olaris-bestia` submodule is what actually ships a version, and that requires
explicit user authorization.

## Container runtime

The build scripts source `image/runtime.sh`, which selects the container
runtime once so both scripts agree on binary, socket and namespace. Docker
is preferred when installed; otherwise `nerdctl` is used, addressed at k3s'
containerd socket (`/run/k3s/containerd/containerd.sock`) in the `k8s.io`
namespace, starting `buildkit.service` if its socket is missing. Override with
`TRUSTABLE_CONTAINER_RUNTIME=docker|nerdctl`; the socket and namespace are
overridable with `TRUSTABLE_CONTAINERD_ADDRESS` and
`TRUSTABLE_CONTAINERD_NAMESPACE`. Multi-platform builds use `docker buildx`,
while nerdctl takes `--platform` on plain `build`.

`image/image.sh` builds the whole of `image/Dockerfile` in a single pass. There
is no base/current split: the former hash-tagged base image plus `FROM base`
layer only worked under Docker, whose builder shares Docker's image store.
buildkit — which nerdctl drives — resolves `FROM` against its own cache and the
registry only, so it could not see a base image that had just been loaded into
containerd. The builder's layer cache already keeps unchanged base stages from
being rebuilt, so the split bought nothing that cache does not.

It stages `openserverless-mcp` from the pinned
`mcp` submodule, the local browser and deterministic React MCP sources, and the
TruACP runtime artifacts:
`setup.sh`, `pi.version`, `dist-bin/truacp.cjs`,
`pi-acp-package.tgz`, and `extensions/trustable-runtime.ts`. Before staging, it
recursively initializes TruACP's pinned
`pi-acp` fork, runs its tests/build/package step, and builds the TruACP bundle.
The complete sources and `node_modules` must never enter the Docker build context
or an image layer. The staged runtime identities and content hashes are logged
for provenance; because they are part of the build context, any runtime,
adapter, or browser-tool change invalidates the builder's layer cache and
rebuilds the affected stages.

The issue #57 extension is a separately loaded, versioned runtime artifact. It
must be staged beside the matching TruACP bundle and pinned `pi-acp` package.
The image must not restore the former `trustable-guardrails.ts` placeholder or
claim policy capabilities beyond those documented in
[trustable-pi-runtime.md](trustable-pi-runtime.md).

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
`pi-acp` fork, packages the local OpenServerless/browser/React MCP sources, and
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

Both environments also install the official GitHub CLI from the pinned
`GH_VERSION=2.96.0` Linux archive. The Dockerfile owns checked-in SHA-256 values
for amd64 and arm64; both the image and repository-root setup verify the
matching checksum before installing `/usr/local/bin/gh`. Builds never resolve a
floating GitHub CLI release or copy a host developer's GitHub configuration.

Both environments install the same Trustable-owned Redis MCP stdio wrapper:
`image/redis-mcp` becomes `trustable-redis-mcp`, while the pinned upstream
`redis-mcp-server==0.5.0` remains its child process. The wrapper is part of the
image source context and clean VM setup payload; no build may substitute a
cached developer copy or bypass its fail-closed per-application namespace
policy.

Managed account state is runtime data under
`$WORKSPACE_DIR/.trustable/github`, not an image layer. The directory is on the
durable workspace mount in the pod and in `trudev`, so authentication survives
supported restarts while remaining isolated from the normal host account.

For the macOS Lima flow, `start.sh` initializes `mcp`, `trustable-acp`, and its
nested `pi-acp` fork recursively on the host before starting the guest. It checks
the nested leaf even when the outer submodule was already populated. A
worktree's `.git` file may point outside the single mounted directory, so
`setup.sh` consumes ordinary mounted files and must never require guest access
to nested submodule Git metadata. The same `setup.sh` supports Ubuntu under
Lima and WSL with local k3s without requiring guest access to Git metadata or
changing guest system services. Nested Pi dependency hydration therefore uses
`npm ci --ignore-scripts`: its explicit local-release command owns the package
build, while repository-only lifecycle hooks such as Husky are neither needed
nor allowed to follow host-only worktree administration paths.

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

Build environment:

- `TRUSTABLE_IMAGE`: image repository, default
  `ghcr.io/trustable-ai/trustable-app`.
- `TRUSTABLE_BUILD_TAG`: explicit image tag. If omitted, the tag is
  `<key>_<version>_<yy.jjj.HHMM>`.
- `TRUSTABLE_BUILD_SKIP_DEPLOY=1`: build the image but do not make it reachable
  by the cluster or redeploy.
- `TRUSTABLE_MAC_SUPPORT_DIR`: location of the macOS Trustable VM credentials,
  default `~/Library/Application Support/Trustable`. Its presence is what
  selects the VM shipping path.

Publishing environment:

- `TRUSTABLE_PUBLISH_BRANCH`: branch ref updated by `publish.sh`, defaulting to
  the current branch. Release branches can publish to `main` with
  `TRUSTABLE_PUBLISH_BRANCH=main ./publish.sh`.
  After image CI succeeds, `publish.sh` may push `olaris-bestia`, but any push
  to `olaris`, `olaris-bestia`, `olaris-trustable`, or another `olaris*` repo
  requires explicit user authorization first. If there are no local changes in
  that subrepo it skips the commit step and only pushes.
Every build updates `olaris-bestia/opsroot.json`, so the deployment plugin and
the running cluster always agree on which image was built. Recording the tag is
local; publishing it is a separate, authorization-gated push of the submodule.

## Upstream Pi image payload (#71)

This section supersedes earlier Pi-tarball staging requirements. `image.sh`
stages `setup.sh`, `pi.version`, `pi.integrity`, the managed runtime extension,
the built TruACP bundle, and the pinned `pi-acp` package. It must not build a Pi
monorepo or create a `pi-packages` directory. The Docker layer consumes this
same payload and verifies upstream package integrity through the ACP installer.

Notebook support adds no runtime package or image secret. Its React UI, parser,
server GitHub client, and session sidecar code are imported by the existing
truacp web/server entrypoints and therefore travel in the normal embedded
bundle. `NOTEBOOK_GITHUB_TOKEN`, when configured, is supplied only to the
running server process; it is never baked into an image layer or staged
artifact.
