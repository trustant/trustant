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
