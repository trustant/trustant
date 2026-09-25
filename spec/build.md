# Build Scripts

`build.sh` is the entrypoint for building the full Trustant image, on both the
macOS Trustant VM and the Linux k3s server. There is no separate server script:
the host is detected from the presence of
`~/Library/Application Support/Trustant/id_ed25519` and `current.ip`.
`hotfix.sh` is the fast path for changes that do not need the expensive image
stages rebuilt — see [Hotfix builds](#hotfix-builds).

Both scripts take the same modes. **A bare invocation prints help and builds
nothing**, so neither can start a 20-minute build or a cluster rollout by
accident:

| Mode | `build.sh` | `hotfix.sh` |
|---|---|---|
| *(no args)* | help, plus the current tag and a `git push --tags` hint | same, keyed on the `-<n>` tag shape |
| `--build [--no-deploy]` | full image, ship, `ops truinst trustant redeploy` | thin layer, ship, StatefulSet patch + rollout |
| `--buildx` | both arches, multiarch build, push to the registry (CI) | same, thin layer |
| `--tag` | tag + `opsroot.json` + commit; builds nothing | tag only; builds nothing |

`--no-deploy` is an optional **second** argument to `--build`: build the image
and stop, shipping nothing and deploying nothing. It is rejected with a usage
error on `--tag` (which builds nothing) and `--buildx` (which never deploys)
rather than silently ignored. `TRUSTANT_BUILD_SKIP_DEPLOY=1` remains honoured
for compatibility but is no longer the documented form.

`build.sh --build`, on every host:

1. computes the tag, **deletes every existing git tag**, forces the new one, and
   writes `_build.txt`
2. **writes the image tag into `oplugins-truinst/opsroot.json`** via `jq`, then
   commits, so the deployment plugin always records the image just built
3. builds the Go binary for the **host architecture** (a single-arch local image
   never uses the other binary; `--buildx` is where both are built)
4. builds the container image through `image/image.sh`
5. makes the image reachable by the cluster
6. deploys with `ops truinst trustant redeploy`

Only step 5 differs by host. On the macOS VM the cluster lives inside the VM
and cannot see the local image store, so the image is exported and piped over
ssh into the VM's containerd (preceded by `ops truinst trustant undeploy` and
`k3s ctr images prune --all`, which frees the old image so the VM's small disk
can reclaim it). On the k3s server, an image built by nerdctl is already in the
`k8s.io` namespace the kubelet reads and nothing is done; if Docker built it,
it is imported with `save ... | sudo -n k3s ctr images import -`.

Deployment is always `ops truinst trustant redeploy`, which is `undeploy` +
`deploy`; `deploy` reads the image from `opsroot.json`. The StatefulSet is
never patched directly with `kubectl set image`.

`--no-deploy` stops after the image build, leaving the tag, `_build.txt` and the
`opsroot.json` update in place.

`build.sh --buildx` is the CI path. It takes the tag from `GITHUB_REF`, and
**never tags, commits or writes `opsroot.json`**: CI runs on a detached checkout
of an already-pushed tag, so creating a tag there is meaningless and a commit
would be orphaned. It writes `_build.txt`, compiles both arches, stages
`trustant.json` and calls `image/image.sh "$TAG" --push`.

`image/image.sh` decides between a local single-arch build and a multiarch
registry push from that explicit `--push` argument, not by sniffing
`GITHUB_ACTIONS`: the caller knows which it wants.

Note that updating `opsroot.json` only records the tag locally. Pushing the
`oplugins-truinst` submodule is what actually ships a version, and that requires
explicit user authorization.

## Container runtime

The build scripts source `image/runtime.sh`, which selects the container
runtime once so both scripts agree on binary, socket and namespace. Docker
is preferred when installed; otherwise `nerdctl` is used, addressed at k3s'
containerd socket (`/run/k3s/containerd/containerd.sock`) in the `k8s.io`
namespace, starting `buildkit.service` if its socket is missing. Override with
`TRUSTANT_CONTAINER_RUNTIME=docker|nerdctl`; the socket and namespace are
overridable with `TRUSTANT_CONTAINERD_ADDRESS` and
`TRUSTANT_CONTAINERD_NAMESPACE`. Multi-platform builds use `docker buildx`,
while nerdctl takes `--platform` on plain `build`.

`image/image.sh` builds the whole of `image/Dockerfile` in a single pass. There
is no base/current split: the former hash-tagged base image plus `FROM base`
layer only worked under Docker, whose builder shares Docker's image store.
buildkit — which nerdctl drives — resolves `FROM` against its own cache and the
registry only, so it could not see a base image that had just been loaded into
containerd. The builder's layer cache already keeps unchanged base stages from
being rebuilt, so the split bought nothing that cache does not.

It stages `openserverless-mcp` from the pinned
`mcp` submodule, the local deterministic React MCP source, and the
TruACP runtime artifacts:
`setup.sh`, `pi.version`, `dist-bin/truacp.cjs`,
`pi-acp-package.tgz`, and `extensions/trustant-runtime.ts`. Before staging, it
recursively initializes TruACP's pinned
`pi-acp` fork, runs its tests/build/package step, and builds the TruACP bundle.
The complete sources and `node_modules` must never enter the Docker build context
or an image layer. The staged runtime identities and content hashes are logged
for provenance; because they are part of the build context, any runtime or
adapter change invalidates the builder's layer cache and
rebuilds the affected stages.

The issue #57 extension is a separately loaded, versioned runtime artifact. It
must be staged beside the matching TruACP bundle and pinned `pi-acp` package.
The image must not restore the former `trustant-guardrails.ts` placeholder or
claim policy capabilities beyond those documented in
[trustant-pi-runtime.md](trustant-pi-runtime.md).

`trustant-acp` is tracked as a Git submodule from
`https://github.com/trustable-ai/trustant-acp.git`, following `main` while the
parent repository pins the exact commit. Trustant builds must consume that
checked-out revision and must not download a floating TruACP source archive or
depend on another developer worktree. The host build produces the portable
JavaScript bundle; the Docker build runs the staged `setup.sh` to install that
bundle and the Pi packages pinned by `trustant-acp/pi.version`. OpenCode is not
built or installed.

The Lima `setup.sh` development path mirrors the image: it builds the checked-out
TruACP source, installs the pinned Pi toolchain, builds the same nested
`pi-acp` fork, and packages the local OpenServerless/React MCP sources. It
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

Both environments install the same Trustant-owned Redis MCP stdio wrapper:
`image/redis-mcp` becomes `trustant-redis-mcp`, while the pinned upstream
`redis-mcp-server==0.5.0` remains its child process. The wrapper is part of the
image source context and clean VM setup payload; no build may substitute a
cached developer copy or bypass its fail-closed per-application namespace
policy.

Managed account state is runtime data under
`$WORKSPACE_DIR/.trustant/github`, not an image layer. The directory is on the
durable workspace mount in the pod and in `trudev`, so authentication survives
supported restarts while remaining isolated from the normal host account.

For the macOS Lima flow, `start.sh` initializes `mcp`, `trustant-acp`, and its
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
containerd snapshots, and repeated Trustant image imports exceed the safe
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
one namespace-wide forwarder for `openserverless`, excludes `trustant-svc`, waits
for bounded readiness, and cleans it with the normal development process trap.
This is a VM-host process only: production pods use native Kubernetes Service
DNS and never start `kubefwd`.

`run.sh` must also work from a fresh worktree where the ignored `_build.txt`
does not exist. Before starting Air it writes local development build metadata;
the macOS wrapper records the real host worktree branch, while direct Linux/WSL
runs use `TRUSTANT_BUILD_BRANCH` when supplied and otherwise report the
`development` fallback. It skips that regeneration only when HEAD is exactly on
a tag whose name already appears in `_build.txt`, so a run from the tagged
commit keeps -- and reports -- the release metadata it was built with (see
[run.md](run.md)). Release metadata remains owned by the build scripts.

## Build tags

A build tag is disposable: it marks the working tree that produced one image and
is superseded by the next build. So both `build.sh` and `image/image.sh` delete
**every** existing tag before creating theirs, and the repo carries exactly one.

The deletion must be written as:

```bash
git tag -l | xargs -r git tag -d
```

Not as `git tag -d "$(git tag)"`. The quoted command substitution passes the
whole list as a **single argument** with embedded newlines, so git reports
`tag 'a\nb\nc' not found` and a trailing `|| true` swallows the non-zero exit —
the script prints its usual output while deleting nothing, and tags accumulate on
every build (69 had built up before this was spotted). `xargs` splits on newlines,
and `-r` skips the call when there is nothing to delete, which keeps the first
build in a fresh clone from failing. `build_script_test.go` asserts the correct
form in both scripts and rejects the quoted-substitution spelling.

This is not housekeeping. `.github/workflows/images.yml` triggers on
`push: tags: ['*_*_*']`, and `publish.sh` pushes with `--tags`, which pushes
**every** local tag rather than only the `$TAG` it selected. So each stale tag
that reaches the remote starts its own container build: with 47 such tags
present, one republish queues 47 builds. The single-tag invariant above is the
only thing standing between `--tags` and a build storm, which is why it is
tested rather than left to the comment in the script.

Deleting local tags does not delete remote ones — `git push --tags` never
removes anything, so a tag that has already reached the remote stays until it is
explicitly deleted with `git push origin :refs/tags/<name>`. Deletion itself does
not trigger CI, which keys on tag creation. Tags that cannot match `*_*_*`
(`v0.3.14`, `v0.3.4-beta`, the old date-only ones) are inert and are kept as
release markers.

Build environment:

- `TRUSTANT_IMAGE`: image repository, default
  `ghcr.io/trustant/trustant`.
- `TRUSTANT_BUILD_TAG`: explicit image tag. If omitted, the tag is
  `<key>_<version>_<yy.jjj.HHMM>`.
- `TRUSTANT_HOTFIX_TAG`: explicit hotfix tag for `hotfix.sh --buildx`,
  overriding `GITHUB_REF`.
- `TRUSTANT_BUILD_SKIP_DEPLOY=1`: legacy equivalent of `--no-deploy`, still
  honoured but no longer the documented form.
- `TRUSTANT_MAC_SUPPORT_DIR`: location of the macOS Trustant VM credentials,
  default `~/Library/Application Support/Trustant`. Its presence is what
  selects the VM shipping path.

Publishing environment:

- `TRUSTANT_PUBLISH_BRANCH`: branch ref updated by `publish.sh`, defaulting to
  the current branch. Release branches can publish to `main` with
  `TRUSTANT_PUBLISH_BRANCH=main ./publish.sh`.
  After image CI succeeds, `publish.sh` may push `oplugins-truinst`, but any push
  to `oplugins`, `oplugins-truinst`, or another plugin repo
  requires explicit user authorization first. If there are no local changes in
  that subrepo it skips the commit step and only pushes.
Every build updates `oplugins-truinst/opsroot.json`, so the deployment plugin and
the running cluster always agree on which image was built. Recording the tag is
local; publishing it is a separate, authorization-gated push of the submodule.

## Hotfix builds

`hotfix.sh` layers four files onto the image already recorded in
`oplugins-truinst/opsroot.json` via `FROM <that image>`:

| Source (context `image/`) | Destination | Owner |
|---|---|---|
| `bin/trustant-$TARGETARCH` | `/usr/local/bin/trustant` | root |
| `start.sh` | `/usr/local/bin/start.sh` | root |
| `env` | `/home/trustant/.env` | `trustant:trustant` |
| `trustant.json` | `/home/trustant/trustant.json` | `trustant:trustant` |

That set covers a Go change, a container entrypoint fix, a flag flip in `.env`
(`ENABLE_LICENSE`, `ENABLE_REGOLO`) and a base-config change — none of which
need the MCP/TruACP stages rebuilt. Everything `image/image.sh` does before its
first `docker build` line (submodule init, `npm install`/`build` for
trustant-acp, `npm ci`/`test`/`build`/`pack` for the nested pi-acp fork, three
staged MCP context dirs, ~20 minutes) is skipped.

The ownership split mirrors `image/Dockerfile` and is not cosmetic: root-owned
files in `/home/trustant` break the running app, which writes there.
Destinations are absolute because `WORKDIR` is set after the COPYs.
`image/trustant.json` is gitignored and generated, so the build stages it with
`cp trustant.json image/trustant.json` first; `image/env` and `image/start.sh`
are tracked. The generated Dockerfile must use the **dot** name
`image/Dockerfile.hotfix` — `.gitignore` ignores `image/Dockerfile.*` but not the
dash form, so a crashed run would otherwise leave an untracked file that fails
the next clean-tree check. It is removed on every exit path by a trap.

`ARG TARGETARCH` is redeclared in the generated file because ARGs are
stage-scoped; BuildKit populates it per platform, which is what selects
`trustant-amd64` vs `trustant-arm64` from a single context. `ENTRYPOINT
["tini", "--"]` is inherited from the base image and must not be redeclared.

### Hotfix tags

A hotfix tag is `<base>-<n>`, where `<base>` is the tag from `opsroot.json` with
any existing `-<n>` **stripped**, so hotfixes chain off the original base rather
than nesting into `...2118-1-1`.

The suffix is `-<n>` and not `+<n>` because the OCI tag grammar is
`[A-Za-z0-9_][A-Za-z0-9._-]{0,127}`: a `+` would produce an unpullable
`ghcr.io/...:tag+1`.

The base tag is read as `${ref##*:}`, **not** `awk -F: '{print $2}'`, which
returns the port for a registry like `localhost:5000/img:tag`.

`<n>` is counted from the **remote**:

```bash
git ls-remote --tags origin "refs/tags/${BASE}*" \
  | sed 's|.*refs/tags/||' | grep -v '\^{}' | sort -u | wc -l
```

Local counting cannot work: `build.sh` deletes every local tag on each build, so
a local count is always 1 and every hotfix would collide on `-1`. Because the
count is remote-derived and `--tag` does not push, running it twice yields the
same tag.

`--tag` requires a clean working tree: a hotfix tag must describe a committed
tree.

### Rollout, and why it is not `redeploy`

`hotfix.sh` **never writes `opsroot.json` and never commits.** opsroot therefore
keeps pointing at the base, which is what lets the next hotfix chain off it —
and it is also why the rollout cannot go through the deployment plugin:
`ops truinst trustant redeploy` resolves the image from `opsroot.json` and would
roll out the *base*, not the hotfix. Making redeploy work would mean dirtying the
`oplugins-truinst` submodule on every hotfix.

So `--build` patches the StatefulSet directly:

```bash
kubectl -n openserverless set image statefulset/trustant trustant="$IMAGE:$TAG"
kubectl -n openserverless rollout status statefulset/trustant --timeout=600s
```

This is a **scoped exception** to the rule above that the StatefulSet is never
patched directly with `kubectl set image`. That rule governs the release path and
stays true there.

**The patch is not durable.** The next `ops truinst trustant redeploy`, or any
`deploy` from the plugin, reverts the StatefulSet to the opsroot image. A hotfix
is a live patch, not a release; shipping one for real is still `build.sh --build`
plus an authorized `oplugins-truinst` push. `hotfix.sh` says so in its own output.

Shipping the image reuses `build.sh`'s host split, with one difference: there is
**no** `undeploy` and **no** `ctr images prune --all`. `build.sh` prunes to
reclaim disk before a full image lands, but pruning would evict the very base
image the hotfix layers on. If k3s is unreachable the script reports the built
image and exits 0 — building without a cluster is a supported outcome.

`_build.txt` is written **before** compiling, carrying the full `-<n>` tag on its
`Build:` line. `main.go` embeds that file at compile time, so writing it
afterwards would bake the *base* build string into a hotfix binary and leave no
way to tell from `/api/version` whether the hotfix is actually running.

### Publishing a hotfix

`publish.sh` reports whether it is watching a hotfix or a full image, and on a
hotfix tag it does **not touch `oplugins-truinst` at all** — not the `cd`, not the
commit, not the push. `hotfix.sh` never writes `opsroot.json`, so a
`git commit -a` there could only sweep up unrelated dirty files in that submodule
under a message naming the hotfix tag, and the push is exactly the plugin-repo push
that requires explicit authorization.

Note that `publish.sh` pushes with `--tags`, i.e. every local tag. The
single-tag invariant above is what keeps that to one CI build, and it is why the
scripts can recommend `git push --tags` in their help output.

## CI

`.github/workflows/images.yml` compiles nothing. It classifies the pushed tag and
calls one of the two scripts:

```yaml
- name: Build hotfix image
  if: steps.tag.outputs.hotfix == 'true'
  run: bash ./hotfix.sh --buildx

- name: Build full image
  if: steps.tag.outputs.hotfix == 'false'
  run: bash ./build.sh --buildx
```

The classification happens in a step, not in `on:`, because the
`push: tags: ['*_*_*']` filter matches both `..._2118` and `..._2118-1` and GHA
globs have no `[0-9]` character classes.

The checkout must set `submodules: recursive`: `opsroot.json` lives in the
`oplugins-truinst` submodule, and without it the hotfix path cannot resolve its
base image. That submodule is private, but it is in the same org as this repo
(`trustant`), so the job's own `GITHUB_TOKEN` reaches it and **no PAT is
involved**. Every other submodule is public.

The GHCR login uses that same `GITHUB_TOKEN`: the image publishes to
`ghcr.io/trustant/trustant`, the org that owns this repo, so the `packages:
write` permission the job already declares is sufficient.

Compilation lives in the scripts rather than the workflow so CI and a developer
machine produce the image the same way.

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
