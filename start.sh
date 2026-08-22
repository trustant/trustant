#!/bin/bash
#
# start.sh — bring up a Trustable development environment. Two hosts, one script,
# selected by `uname -s` (see spec/start.md):
#
#   macOS  — provisions a local Trustable VM with Lima and wires it up so the
#            rest of the tooling (ssh.sh, setup.sh, build.sh, publish.sh) finds
#            it exactly where the macOS app would put it:
#            ~/Library/Application Support/Trustable/.
#   Linux  — no VM. Ubuntu/Debian already IS the environment, so the same
#            initialization runs directly on this host: install the Trustable
#            .deb when k3s is absent, then ollama/kubefwd/gh, the k3s host-rewrite
#            proxy, the support files, and setup.sh.
#
# Plain run:   macOS boots a plain Ubuntu VM (vz), then installs the Trustable
#              .deb (k3s + helpers) inside it, installs a CPU-only ollama host
#              (localhost:11434, pinned to the image's OLLAMA_VERSION), ensures
#              gh via apt in-VM, writes the VM ip/apihost/ssh key to the
#              Trustable support dir, runs setup.sh in-VM, and finally runs
#              ./run.sh in the VM (foreground — Ctrl-C stops the dev server).
#              Linux does the same minus everything that only makes sense
#              against a VM, and stops before run.sh (just run it yourself).
#   ./start.sh
#
# VS Code:     opens this folder in the VM over Remote-SSH instead of running
#              run.sh. Accepted and ignored on Linux, where the sources are
#              already local.
#   ./start.sh -v
#
# Neither:     same as a plain run, but finishes without run.sh or VS Code.
#   ./start.sh -n
#
# Stop:        stops the VM without deleting it, so a later ./start.sh restarts
#              it (no reinstall). macOS only.
#   ./start.sh -s
#
# Teardown:    stops and deletes the VM. macOS only.
#   ./start.sh -k
#
# The ~4GB package is downloaded+cached on the HOST (under dist/) before the
# VM boots, then copied in and installed over `limactl shell`. Installing this
# way — rather than as a Lima `provision` script — avoids limactl start's ~10min
# readiness timeout, and caching on the host makes re-runs skip the download.
# On Linux the same cached .deb is installed straight into this machine.
#
# The ~1.5GB ollama release tarball is cached in dist/ the same way and for the
# same reason: dist/ outlives the VM, so `./start.sh -k && ./start.sh` reinstalls
# both from disk instead of re-downloading ~5.5GB.
#
set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
ok()   { echo -e "${GREEN}✓ $1${NC}"; }
warn() { echo -e "${YELLOW}⚠ $1${NC}"; }
fail() { echo -e "${RED}✗ $1${NC}"; exit 1; }

cd "$(dirname "$0")"

# Host dispatch. macOS runs the Lima path; Ubuntu/Debian runs everything
# natively. Any other OS is unsupported — the whole flow is apt/dpkg-based.
NATIVE_LINUX=false
case "$(uname -s)" in
  Darwin) ;;
  Linux)  NATIVE_LINUX=true ;;
  *) fail "start.sh supports macOS (Lima VM) and Ubuntu Linux, not $(uname -s)" ;;
esac

VM_NAME="trudev"
# The macOS app's support dir is a fixed Apple location; on Linux there is no
# such app, so the same two files (current.ip, apihost) live under XDG config.
# configure.go's apihostFilePath() resolves the identical path per OS.
if $NATIVE_LINUX; then
  SUPPORT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/trustable"
else
  SUPPORT_DIR="$HOME/Library/Application Support/Trustable"
fi
LIMA_KEY="$HOME/.lima/_config/user"          # shared identity limactl ssh uses
DOWNLOAD_BASE="https://landing.nuvolaris.org/api/my/v1/download"
# The release identity lives in version.txt (tagged form, e.g. v0.4.0) and is the
# single source shared with build.sh/hotfix.sh/run.sh. The deb filename uses the
# numeric form, so strip the leading "v". The endpoint takes no version selector
# — it serves the current release — so this only names the dist/ cache entry.
TRUSTABLE_VERSION="$(head -n1 version.txt 2>/dev/null | tr -d '[:space:]')"
TRUSTABLE_VERSION="${TRUSTABLE_VERSION#v}"
if [[ -z "$TRUSTABLE_VERSION" ]]; then
  TRUSTABLE_VERSION="unknown"
  warn "version.txt not found or empty — caching the package as trustable_${TRUSTABLE_VERSION}_<arch>.deb"
fi
DIST_DIR="dist"                              # host-side cache (.deb + ollama tarball)

# CPU-only ollama is installed as a host process in the VM (localhost:11434); the
# app mostly uses cloud models. Defaults to the image's version (the ARG line in
# image/Dockerfile) so the VM matches the container — set OLLAMA_VERSION here or
# in the environment to override it. The ~1.5GB release tarball is cached on the
# HOST under dist/ (like the .deb), so destroying and recreating the VM installs
# from disk instead of downloading again; changing the version below just names a
# different cache entry and downloads that one once.
OLLAMA_VERSION="${OLLAMA_VERSION:-$(grep -m1 '^ARG OLLAMA_VERSION=' image/Dockerfile 2>/dev/null | cut -d= -f2 | tr -d ' ')}"
GH_VERSION="$(grep -m1 '^ARG GH_VERSION=' image/Dockerfile 2>/dev/null | cut -d= -f2 | tr -d ' ')"

# Keep the release identity and both supported archive digests in source so a
# clean VM never depends on a mutable "latest" asset.
KUBEFWD_VERSION="1.25.16"
KUBEFWD_SHA_AMD64="07275cad05b2427069071160125b8cb29e94dd44582f685ce6d966fa9e7fb7d7"
KUBEFWD_SHA_ARM64="e01ade02d919be2c7e306543f0a65de2e629c254ef16b51ecb45830b0044a3e8"

# The current macOS user + the folder start.sh runs from. Both are mirrored into
# the VM: a guest user with the same name and UID owns a virtiofs mount of this
# folder at the same path, so files edited in the VM keep the host's ownership.
HOST_USER="$(id -un)"
HOST_UID="$(id -u)"
MOUNT_DIR="$(pwd)"
GH_TOKEN_FILE="$MOUNT_DIR/.ghtoken"
ENV_FILE="$MOUNT_DIR/.env"
ENV_DIST_FILE="$MOUNT_DIR/.env.dist"

# setup.sh runs inside a VM that only mounts this worktree. A worktree's .git
# file may point outside that mount, so source submodules must be initialized on
# the macOS host before the guest starts; setup.sh then consumes plain files and
# never follows host-only Git metadata.
ensure_source_submodules() {
  # WHY: trustable-acp may already be populated while its nested pi-acp fork is
  # still empty. Test the leaf explicitly so a reused worktree cannot reach the
  # VM setup with an incomplete runtime source tree.
  if [[ ! -f "$MOUNT_DIR/mcp/package.json" ||
        ! -f "$MOUNT_DIR/trustable-acp/package.json" ||
        ! -f "$MOUNT_DIR/trustable-acp/pi-acp/package.json" ]]; then
    echo "--- Initializing runtime source submodules on the host ---"
    # -c ... is inherited by the clone/checkout git runs per submodule, so a
    # fresh Windows checkout lands as LF instead of needing the repair below.
    git -C "$MOUNT_DIR" -c core.autocrlf=false -c core.eol=lf \
      submodule update --init --recursive mcp trustable-acp \
      || fail "failed to initialize mcp/trustable-acp submodules"
  fi
  [[ -f "$MOUNT_DIR/mcp/package.json" ]] || fail "mcp submodule source is unavailable"
  [[ -f "$MOUNT_DIR/trustable-acp/package.json" ]] || fail "trustable-acp submodule source is unavailable"
  [[ -f "$MOUNT_DIR/trustable-acp/pi-acp/package.json" ]] || fail "nested pi-acp fork source is unavailable"
  ok "runtime source submodules are available"
  normalize_submodule_eol
}

# Git for Windows ships core.autocrlf=true in its SYSTEM config, and .gitattributes
# does not cross a submodule boundary — each submodule is its own repository with
# its own working tree and its own attributes. So the repo-root `* text=auto
# eol=lf` protects this repo only, and every submodule still checks out CRLF.
# bash then reads `#!/bin/sh\r` and reports the shebang interpreter as missing:
#   ./setup.sh: line NNN: ./setup.sh: cannot execute: required file not found
# Pin LF per submodule and repair any working tree that is still CRLF. A no-op on
# macOS and Linux, where autocrlf is off to begin with.
normalize_submodule_eol() {
  git -C "$MOUNT_DIR" submodule foreach --quiet --recursive '
    git config core.autocrlf false
    git config core.eol lf
    # Only files stored as LF but checked out as CRLF are damaged; a submodule
    # that genuinely commits CRLF must be left exactly as it is.
    if git ls-files --eol | grep -q "^i/lf[[:space:]]\{1,\}w/crlf"; then
      if [ -n "$(git status --porcelain)" ]; then
        echo "WARNING: $displaypath has CRLF line endings but uncommitted changes." >&2
        echo "         Commit or stash them, then: git -C $displaypath checkout --force -- ." >&2
      else
        git rm --cached -rq .
        git reset --hard -q
        echo "repaired CRLF line endings in $displaypath"
      fi
    fi
  ' || fail "could not normalize submodule line endings"
  ok "submodules are checked out with LF line endings"
}

# The provisioning payloads below are plain Ubuntu bash and identical on both
# hosts — the only difference is WHERE they run. These two wrappers are that
# difference, so every payload heredoc stays host-agnostic.
#
#   run_privileged VAR=val ... <<'GUEST'   — as root, in the VM (macOS) or here (Linux)
#   run_guest      <cmd> [args...]         — as the normal user, same choice
run_privileged() {
  if $NATIVE_LINUX; then
    sudo -n env "$@" bash -euo pipefail -s
  else
    limactl shell "$VM_NAME" sudo env "$@" bash -euo pipefail -s
  fi
}

run_guest() {
  if $NATIVE_LINUX; then
    "$@"
  else
    limactl shell "$VM_NAME" "$@"
  fi
}

# Ensure the k3s API serving cert covers the host-reachable lima0 IP, so any
# host-side kubeconfig using that address verifies. The in-VM setup keeps its
# local 127.0.0.1 endpoint; this SAN is only for host-side `kubectl`/`ops`.
# Idempotent: only regenerates the cert when the IP isn't a SAN yet.
ensure_tls_san() {
  local IP="$1"
  echo "--- Ensuring k3s API cert covers $IP ---"
  run_privileged IP="$IP" <<'GUEST'
# Already a SAN on the live cert? then nothing to do.
if echo | openssl s_client -connect "127.0.0.1:6443" 2>/dev/null \
     | openssl x509 -noout -text 2>/dev/null \
     | grep -q "IP Address:${IP}\b"; then
  echo "cert already covers ${IP}"
  exit 0
fi
mkdir -p /etc/rancher/k3s
# Merge the SAN into any existing tls-san config (don't clobber other entries).
if [ -f /etc/rancher/k3s/config.yaml ] && grep -q '^tls-san:' /etc/rancher/k3s/config.yaml; then
  grep -qF "- ${IP}" /etc/rancher/k3s/config.yaml || \
    sed -i "/^tls-san:/a\\  - ${IP}" /etc/rancher/k3s/config.yaml
else
  printf 'tls-san:\n  - %s\n' "${IP}" >> /etc/rancher/k3s/config.yaml
fi
# Force k3s to reissue the serving cert with the new SAN: drop the dynamic-cert
# state and the on-disk serving cert so k3s regenerates both on restart.
rm -f /var/lib/rancher/k3s/server/tls/dynamic-cert.json \
      /var/lib/rancher/k3s/server/tls/serving-kube-apiserver.crt \
      /var/lib/rancher/k3s/server/tls/serving-kube-apiserver.key
systemctl restart k3s
# Wait for the API to come back.
for _ in $(seq 1 30); do
  k3s kubectl get --raw='/readyz' >/dev/null 2>&1 && break
  sleep 2
done
echo "cert now covers ${IP}"
GUEST
  ok "k3s API cert covers $IP"
}

# Deploy (idempotently) an nginx reverse proxy into k3s that captures requests
# addressed to the VM by IP and rewrites the Host header onto the *.miniops.me
# names traefik's ingresses actually match. Reachable from the host at:
#   http://<ip>:8080                    -> Host: miniops.me
#   http://<label>.<ip>.nip.io:8080     -> Host: <label>.miniops.me
# It forwards to traefik's in-cluster ClusterIP, so traefik keeps owning :80
# untouched. Port 8080 is outside the package's :80/:443/:6443 firewall DROP.
apply_reverse_proxy() {
  local IP="$1"
  local ipre="${IP//./\\.}"   # dotted IP escaped for the nginx regex
  echo "--- Deploying host-rewrite reverse proxy into k3s ---"
  run_privileged IP="$IP" IPRE="$ipre" <<'GUEST'
# Resolve traefik's ClusterIP:port (the upstream we proxy to).
TRAEFIK_IP="$(k3s kubectl get svc -n kube-system traefik -o jsonpath='{.spec.clusterIP}')"
[ -n "$TRAEFIK_IP" ] || { echo "traefik ClusterIP not found" >&2; exit 1; }

# nginx maps the incoming Host to the miniops.me name traefik expects:
#   <ip> / <ip>.nip.io                 -> miniops.me
#   <label>.<ip>.nip.io                -> <label>.miniops.me
# The default (anything else) is passed through unchanged.
k3s kubectl apply -f - <<YAML
apiVersion: v1
kind: ConfigMap
metadata:
  name: apihost-proxy
  namespace: nuvolaris
data:
  nginx.conf: |
    user nginx;
    worker_processes auto;
    events { worker_connections 1024; }
    http {
      resolver 10.43.0.10 valid=30s;
      map \$host \$upstream_host {
        default                                      \$host;
        "~^(?:www\\.)?${IPRE}(?::\\d+)?\$"            miniops.me;
        "~^(?:www\\.)?${IPRE}\\.nip\\.io(?::\\d+)?\$" miniops.me;
        "~^(?<label>[^.]+)\\.${IPRE}\\.nip\\.io(?::\\d+)?\$" \${label}.miniops.me;
      }
      server {
        listen 8080;
        location / {
          proxy_http_version 1.1;
          proxy_set_header Host \$upstream_host;
          proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
          proxy_set_header X-Forwarded-Proto \$scheme;
          # TruACP streams session updates over /ws. HTTP/1.1 alone does not
          # preserve an Upgrade across this extra Lima proxy hop, so forwarding
          # the browser headers is required to avoid turning /ws into a 404 GET.
          proxy_set_header Upgrade \$http_upgrade;
          proxy_set_header Connection "upgrade";
          proxy_buffering off;
          proxy_request_buffering off;
          proxy_read_timeout 600s;
          proxy_send_timeout 600s;
          proxy_pass http://${TRAEFIK_IP}:80;
        }
      }
    }
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: apihost-proxy
  namespace: nuvolaris
spec:
  replicas: 1
  selector:
    matchLabels: { app: apihost-proxy }
  template:
    metadata:
      labels: { app: apihost-proxy }
    spec:
      containers:
        - name: nginx
          image: nginx:1.27-alpine
          ports: [{ containerPort: 8080 }]
          volumeMounts:
            - name: conf
              mountPath: /etc/nginx/nginx.conf
              subPath: nginx.conf
      volumes:
        - name: conf
          configMap: { name: apihost-proxy }
---
apiVersion: v1
kind: Service
metadata:
  name: apihost-proxy
  namespace: nuvolaris
spec:
  type: LoadBalancer
  selector: { app: apihost-proxy }
  ports:
    - name: http
      port: 8080
      targetPort: 8080
YAML

# Roll the deployment so a changed ConfigMap (new IP) is picked up.
k3s kubectl -n nuvolaris rollout restart deploy/apihost-proxy

# WSL can leave the old pod wedged in Terminating — its sandbox teardown never
# completes against that kernel — and `rollout status` then sits on
#   Waiting for deployment "apihost-proxy" rollout to finish:
#   1 old replicas are pending termination...
# until it times out, which under `set -e` aborts the whole run at a step that is
# otherwise idempotent. Drop the pods outright (the Deployment recreates them) so
# the wait only ever tracks a fresh ReplicaSet. --ignore-not-found keeps this a
# no-op on a first install, where there is no pod yet.
k3s kubectl -n nuvolaris delete pod -l app=apihost-proxy \
  --force --grace-period=0 --ignore-not-found

k3s kubectl -n nuvolaris rollout status deploy/apihost-proxy --timeout=300s
echo "reverse proxy deployed (upstream traefik ${TRAEFIK_IP}:80)"
GUEST
  ok "reverse proxy listening on :8080"
}

# Resolve + cache the ollama release tarball for the guest arch into dist/,
# downloading it only when the cache entry is absent. Sets the global
# OLLAMA_TARBALL (empty when there is nothing usable, which leaves ensure_ollama
# on its upstream-installer path).
#
# WHY this is cached at all: upstream's install.sh always re-downloads ~1.5GB and
# offers no local-artifact option, so `./start.sh -k && ./start.sh` used to pay
# for the whole thing again. dist/ lives on the host — outside the VM's lifetime
# — so a recreated VM reuses it, exactly as it does for the ~4GB .deb.
ensure_ollama_tarball() {
  OLLAMA_TARBALL=""
  [[ -n "${OLLAMA_VERSION:-}" ]] || {
    warn "OLLAMA_VERSION is empty — not caching the ollama tarball"
    return 0
  }

  local ARCH URL TMP
  # The guest runs the host arch (vz on macOS, this machine on Linux/WSL).
  case "$(uname -m)" in
    arm64|aarch64) ARCH=arm64 ;;
    x86_64|amd64)  ARCH=amd64 ;;
    *) warn "unsupported ollama architecture: $(uname -m) — falling back to the upstream installer"; return 0 ;;
  esac

  OLLAMA_TARBALL="${DIST_DIR}/ollama-${OLLAMA_VERSION}-linux-${ARCH}.tar.zst"
  mkdir -p "$DIST_DIR"

  if [[ -s "$OLLAMA_TARBALL" ]]; then
    ok "Using cached ollama tarball: $OLLAMA_TARBALL"
    return 0
  fi

  URL="https://github.com/ollama/ollama/releases/download/v${OLLAMA_VERSION}/ollama-linux-${ARCH}.tar.zst"
  echo "--- Downloading ollama ${OLLAMA_VERSION} (~1.5GB) -> $OLLAMA_TARBALL ---"
  # Download to a temp file then move into place, so an interrupted download
  # never leaves a truncated cache entry that later runs would trust.
  TMP="${OLLAMA_TARBALL}.part"
  if ! curl -fL --retry 3 -o "$TMP" "$URL"; then
    rm -f "$TMP"
    warn "ollama download failed — falling back to the upstream installer"
    OLLAMA_TARBALL=""
    return 0
  fi
  mv "$TMP" "$OLLAMA_TARBALL"
  ok "Downloaded $OLLAMA_TARBALL"
}

# Install ollama as a host process, pinned to OLLAMA_VERSION, and enable its
# service so it serves on localhost:11434 (the app's OLLAMA_ENDPOINT).
# In the VM this is necessarily CPU-only — Apple's vz gives the Linux guest no
# GPU passthrough — which is fine since the app mostly uses cloud models.
# Idempotent: skips the install when ollama is already present at the pinned version.
#
# Installs from the host-cached tarball (ensure_ollama_tarball) when there is
# one, so a rebuilt VM never re-downloads. The extraction and the systemd unit
# below reproduce what upstream's install.sh does for this case; when no cache
# entry is available it falls back to that installer unchanged.
ensure_ollama() {
  if $NATIVE_LINUX; then
    echo "--- Ensuring ollama on this host (localhost:11434) ---"
  else
    echo "--- Ensuring CPU ollama in the VM (localhost:11434) ---"
  fi

  ensure_ollama_tarball

  # The guest sees the worktree at the same path it has on the host, so the
  # cache entry needs no copy — pass its path straight through.
  run_privileged \
    OLLAMA_VERSION="${OLLAMA_VERSION:-}" \
    OLLAMA_TARBALL="${OLLAMA_TARBALL:+$MOUNT_DIR/$OLLAMA_TARBALL}" \
    <<'GUEST'
have="$(command -v ollama >/dev/null 2>&1 && ollama --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true)"
if [ -n "$have" ] && { [ -z "$OLLAMA_VERSION" ] || [ "$have" = "$OLLAMA_VERSION" ]; }; then
  echo "ollama already installed (${have})"
elif [ -n "$OLLAMA_TARBALL" ] && [ -s "$OLLAMA_TARBALL" ]; then
  echo "installing ollama ${OLLAMA_VERSION} from the host cache"
  command -v zstd >/dev/null 2>&1 || {
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq && apt-get install -y -qq zstd
  }
  # Same layout upstream installs: bin/ollama + lib/ollama/* under /usr/local,
  # with the stale lib/ollama removed first so an upgrade cannot mix versions.
  rm -rf /usr/local/lib/ollama
  install -o0 -g0 -m755 -d /usr/local/bin /usr/local/lib/ollama
  zstd -d -c "$OLLAMA_TARBALL" | tar -xf - -C /usr/local
  [ -x /usr/local/bin/ollama ] || { echo "ollama tarball did not contain bin/ollama" >&2; exit 1; }

  # The service account and unit upstream's install.sh would have created.
  id ollama >/dev/null 2>&1 || useradd -r -s /bin/false -U -m -d /usr/share/ollama ollama
  cat >/etc/systemd/system/ollama.service <<'UNIT'
[Unit]
Description=Ollama Service
After=network-online.target

[Service]
ExecStart=/usr/local/bin/ollama serve
User=ollama
Group=ollama
Restart=always
RestartSec=3
Environment="PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

[Install]
WantedBy=default.target
UNIT
  systemctl daemon-reload
else
  echo "no cached ollama tarball — using the upstream installer"
  curl -fsSL https://ollama.com/install.sh >/tmp/ollama-install.sh
  env OLLAMA_VERSION="$OLLAMA_VERSION" bash /tmp/ollama-install.sh
  rm -f /tmp/ollama-install.sh
fi
# Make sure the service is up on 127.0.0.1:11434, however it was installed.
systemctl enable --now ollama 2>/dev/null || true
GUEST
  if $NATIVE_LINUX; then
    ok "ollama serving on localhost:11434"
  else
    ok "ollama serving on localhost:11434 in the VM"
  fi
}

# Install the exact Linux kubefwd consumed by repository-root run.sh. WHY:
# Trustable and its MCP children execute outside k3s in development, while
# production runs inside a pod; a checked release binary gives the development
# host temporary service reachability without mutating its permanent resolver
# configuration.
ensure_kubefwd() {
  if $NATIVE_LINUX; then
    echo "--- Ensuring kubefwd ${KUBEFWD_VERSION} on this host ---"
  else
    echo "--- Ensuring kubefwd ${KUBEFWD_VERSION} in the VM ---"
  fi
  run_privileged \
    KUBEFWD_VERSION="$KUBEFWD_VERSION" \
    KUBEFWD_SHA_AMD64="$KUBEFWD_SHA_AMD64" \
    KUBEFWD_SHA_ARM64="$KUBEFWD_SHA_ARM64" \
    <<'GUEST'
installed_version="$(
  /usr/local/bin/kubefwd version 2>/dev/null \
    | grep -oE 'v?[0-9]+\.[0-9]+\.[0-9]+' | head -1 | sed 's/^v//' || true
)"
if [ "$installed_version" = "$KUBEFWD_VERSION" ]; then
  echo "kubefwd ${installed_version} already installed"
  exit 0
fi

case "$(uname -m)" in
  x86_64|amd64)
    archive_arch="x86_64"
    expected_sha="$KUBEFWD_SHA_AMD64"
    ;;
  aarch64|arm64)
    archive_arch="arm64"
    expected_sha="$KUBEFWD_SHA_ARM64"
    ;;
  *)
    echo "unsupported kubefwd architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT
archive="$tmp_dir/kubefwd.tar.gz"
url="https://github.com/txn2/kubefwd/releases/download/v${KUBEFWD_VERSION}/kubefwd_Linux_${archive_arch}.tar.gz"
curl -fsSL --retry 3 -o "$archive" "$url"
printf '%s  %s\n' "$expected_sha" "$archive" | sha256sum -c -
tar -C "$tmp_dir" -xzf "$archive"
[ -x "$tmp_dir/kubefwd" ] || {
  echo "kubefwd archive did not contain an executable" >&2
  exit 1
}

# Install then rename on the same filesystem so an interrupted update cannot
# leave /usr/local/bin/kubefwd partially written.
install -m 0755 "$tmp_dir/kubefwd" "/usr/local/bin/.kubefwd-${KUBEFWD_VERSION}.tmp"
mv -f "/usr/local/bin/.kubefwd-${KUBEFWD_VERSION}.tmp" /usr/local/bin/kubefwd
installed_version="$(
  /usr/local/bin/kubefwd version 2>/dev/null \
    | grep -oE 'v?[0-9]+\.[0-9]+\.[0-9]+' | head -1 | sed 's/^v//' || true
)"
[ "$installed_version" = "$KUBEFWD_VERSION" ] || {
  echo "kubefwd validation failed: expected ${KUBEFWD_VERSION}, got ${installed_version:-unknown}" >&2
  exit 1
}
GUEST
  ok "kubefwd ${KUBEFWD_VERSION} installed at /usr/local/bin/kubefwd"
}

# Bootstrap gh with apt during provisioning so a fresh environment has the CLI
# even before setup.sh applies the pinned runtime convergence.
ensure_gh_apt() {
  if $NATIVE_LINUX; then
    echo "--- Ensuring GitHub CLI via apt on this host ---"
  else
    echo "--- Ensuring GitHub CLI via apt in the VM ---"
  fi
  run_privileged <<'GUEST'
export DEBIAN_FRONTEND=noninteractive
if dpkg-query -W -f='${Status}' gh 2>/dev/null | grep -q 'install ok installed'; then
  echo "gh apt package already installed"
  exit 0
fi
apt-get update -qq
apt-get install -y -qq gh
GUEST
  if $NATIVE_LINUX; then
    ok "GitHub CLI apt package is installed"
  else
    ok "GitHub CLI apt package is installed in the VM"
  fi
}

# jq is installed during provisioning — not only because the tooling reads JSON,
# but because wait_for_openwhisk below parses /api/info with it and must not be
# the thing that discovers jq is missing.
ensure_jq() {
  if $NATIVE_LINUX; then
    echo "--- Ensuring jq via apt on this host ---"
  else
    echo "--- Ensuring jq via apt in the VM ---"
  fi
  run_privileged <<'GUEST'
export DEBIAN_FRONTEND=noninteractive
if command -v jq >/dev/null 2>&1; then
  echo "jq already installed ($(jq --version 2>/dev/null))"
  exit 0
fi
apt-get update -qq
apt-get install -y -qq jq
GUEST
  if $NATIVE_LINUX; then
    ok "jq is installed"
  else
    ok "jq is installed in the VM"
  fi
}

# Read an `ARG <VAR>=<VALUE>` default out of image/Dockerfile. Same reader
# setup.sh uses (its step 0), on purpose: the Dockerfile is the single source of
# BestIA source identity, so start.sh must never carry its own copy of these
# values. A missing ARG is fatal — exporting an empty OPS_REPO would send an
# interactive `ops` at the wrong fork silently.
read_dockerfile_arg() {
  local var="$1" val
  [[ -f "$MOUNT_DIR/image/Dockerfile" ]] || fail "image/Dockerfile not found"
  val=$(grep -m1 "^ARG ${var}=" "$MOUNT_DIR/image/Dockerfile" | cut -d'=' -f2- | tr -d ' ')
  [[ -n "$val" ]] || fail "ARG $var not found in image/Dockerfile"
  printf '%s' "$val"
}

# Export OPS_BRANCH/OPS_REPO from the shell rc files of both accounts that get a
# shell here: the mirrored dev user and the package's 'trustable' user. The image
# already bakes both into /etc/environment, and setup.sh reads the same ARGs to
# install ops and to hard-fail on a mismatch — but neither reaches a user shell,
# so an interactive `ops` ran without the pinned fork.
#
# WHY both ~/.profile and ~/.bashrc — the same split setup.sh documents at its
# image-PATH step. Ubuntu's stock ~/.bashrc returns early for non-interactive
# shells (`case $- in *i*) ;; *) return;;` at the top), so a block appended there
# is dead code under `bash -lc` and under `ssh <host> <cmd>` — which is exactly
# how ssh.sh invokes ops. ~/.profile is what login shells read regardless of
# interactivity, so it is the file that actually carries these values; ~/.bashrc
# covers interactive non-login shells, which never source ~/.profile.
#
# A managed block, rewritten whole on every run, rather than the append-if-absent
# guard add_to_path uses: that one matches on the line it already wrote, so it
# would pin the first-ever version forever and leave the rc files quietly
# contradicting the Dockerfile after a bump — the drift this is meant to prevent.
ensure_ops_env() {
  local OPS_BRANCH OPS_REPO
  OPS_BRANCH="$(read_dockerfile_arg OPS_BRANCH)"
  OPS_REPO="$(read_dockerfile_arg OPS_REPO)"

  local where=" in the VM"
  if $NATIVE_LINUX; then where=""; fi
  echo "--- Exporting OPS_BRANCH/OPS_REPO in .profile/.bashrc${where} ---"

  # HOST_USER is the mirrored account on macOS; on a native host it is just this
  # user. 'trustable' is handled by the same loop, so neither account is special.
  run_privileged HOST_USER="$HOST_USER" OPS_BRANCH="$OPS_BRANCH" OPS_REPO="$OPS_REPO" <<'GUEST'
BEGIN="# >>> trustable ops env >>>"
END="# <<< trustable ops env <<<"
seen=""

for u in "$HOST_USER" trustable; do
  # Skip an account that does not exist: 'trustable' is created by the package,
  # and on a native host HOST_USER may already BE trustable.
  id "$u" >/dev/null 2>&1 || continue
  case " $seen " in *" $u "*) continue ;; esac
  seen="$seen $u"

  # Resolve the real home from passwd — never assume /home/$u. Lima hands the
  # mirrored user a suffixed home (e.g. /home/msciab.guest) to avoid colliding
  # with the virtiofs mount, so the assumed path would write a .bashrc that no
  # login shell ever reads.
  home="$(getent passwd "$u" | cut -d: -f6)"
  [ -n "$home" ] && [ -d "$home" ] || { echo "skipping $u: no home directory"; continue; }
  group="$(id -gn "$u")"

  for rc in "$home/.profile" "$home/.bashrc"; do
    [ -f "$rc" ] || install -o "$u" -g "$group" -m 0644 /dev/null "$rc"

    # Drop any previous managed block, then append the current one.
    awk -v b="$BEGIN" -v e="$END" '
      $0==b {skip=1} !skip {print} $0==e {skip=0}' "$rc" > "$rc.tmp"
    {
      cat "$rc.tmp"
      echo "$BEGIN"
      echo "# Written by start.sh from image/Dockerfile ARGs. Edits are overwritten."
      printf 'export OPS_BRANCH=%s\n' "$OPS_BRANCH"
      printf 'export OPS_REPO=%s\n' "$OPS_REPO"
      echo "$END"
    } > "$rc"
    rm -f "$rc.tmp"
    chown "$u:$group" "$rc"
    echo "$rc: OPS_BRANCH=$OPS_BRANCH OPS_REPO=$OPS_REPO"
  done
done
GUEST
  ok "OPS_BRANCH=$OPS_BRANCH OPS_REPO=$OPS_REPO exported in .profile/.bashrc"
}

# Install the host-rewrite proxy catch-all with ./proxy.sh (see
# spec/17-proxy.md) rather than `ops bestia proxy install`, so the configuration
# lives in this repo and is reviewable here. It installs nginx with apt-get and
# serves :8911, rewriting Host to <label>.miniops.me for the upstream.
#
# Must still run AFTER setup.sh, which supplies the toolchain and kubeconfig the
# surrounding steps depend on. Idempotent — the site file is regenerated in full
# on every run.
#
# Fatal on failure: without the catch-all the app is unreachable through the
# reverse proxy, and a warning here would only defer that confusion to the user.
ensure_bestia_proxy() {
  local where=" in the VM"
  if $NATIVE_LINUX; then where=""; fi
  echo "--- Installing the host-rewrite proxy catch-all${where} ---"
  # `bash ./proxy.sh` rather than `./proxy.sh`: on WSL the exec bit of a
  # Windows-hosted file depends on the automount options, so a checkout can
  # present the script as non-executable however git records its mode.
  if $NATIVE_LINUX; then
    bash ./proxy.sh || fail "proxy.sh failed"
  else
    # Same invocation style as the setup.sh call: as the mirrored user, in the
    # mounted repo dir, so the script is the worktree's own copy.
    limactl shell --workdir "$MOUNT_DIR" "$VM_NAME" bash ./proxy.sh \
      || fail "proxy.sh failed in the VM"
  fi
  ok "host-rewrite proxy catch-all installed"
}

# A ready k3s API and a present nuvolaris namespace do not mean OpenWhisk serves
# requests yet — the controller comes up minutes later. Block here until the
# apihost is actually usable, in two stages, so the failure says which one lost:
#   1. http://miniops.me answers at all (traefik + ingress are wired)
#   2. http://miniops.me/api/info reports description "OpenWhisk" (the controller
#      itself is serving, not just some other backend behind the same ingress)
# Runs where the cluster is: inside the VM on macOS, on this host on Linux.
wait_for_openwhisk() {
  local where=" in the VM"
  if $NATIVE_LINUX; then where=""; fi
  echo "--- Waiting for OpenWhisk on http://miniops.me${where} ---"
  run_guest bash -euo pipefail -s <<'GUEST'
TRIES=150      # x4s = 10 minutes per stage
DELAY=4

up=0
for _ in $(seq 1 $TRIES); do
  if curl -fsS -m 5 -o /dev/null http://miniops.me; then up=1; break; fi
  sleep $DELAY
done
[ "$up" = 1 ] || { echo "http://miniops.me never answered" >&2; exit 1; }
echo "http://miniops.me is answering"

ready=0
for _ in $(seq 1 $TRIES); do
  desc="$(curl -fsS -m 5 http://miniops.me/api/info 2>/dev/null | jq -r .description 2>/dev/null || true)"
  if [ "$desc" = "OpenWhisk" ]; then ready=1; break; fi
  sleep $DELAY
done
[ "$ready" = 1 ] || {
  echo "http://miniops.me/api/info never reported description OpenWhisk (last: ${desc:-none})" >&2
  exit 1
}
GUEST
  ok "OpenWhisk is serving on http://miniops.me${where}"
}

# setup.sh enforces the pinned gh runtime version from image/Dockerfile. start.sh
# must fail before declaring the environment ready when gh is missing or drifted.
ensure_gh() {
  if $NATIVE_LINUX; then
    echo "--- Verifying GitHub CLI ---"
  else
    echo "--- Verifying GitHub CLI in the VM ---"
  fi
  run_privileged GH_VERSION="${GH_VERSION:-}" <<'GUEST'
command -v gh >/dev/null 2>&1 || {
  echo "gh is missing after setup.sh" >&2
  exit 1
}
if [ -n "${GH_VERSION:-}" ]; then
  have="$(gh --version 2>/dev/null | awk 'NR==1{print $3}' || true)"
  [ "$have" = "$GH_VERSION" ] || {
    echo "gh version mismatch: expected ${GH_VERSION}, got ${have:-unknown}" >&2
    exit 1
  }
fi
GUEST
  local where=" in the VM"
  if $NATIVE_LINUX; then where=""; fi
  if [[ -n "${GH_VERSION:-}" ]]; then
    ok "GitHub CLI ${GH_VERSION} available${where}"
  else
    ok "GitHub CLI available${where}"
  fi
}

# Seed .env from .env.dist when absent, so the file exists before anything reads
# it. Never overwrite an existing .env — it holds the user's real credentials.
#
# A straight copy: .env.dist ships no <placeholder> values, so the result already
# satisfies setup.sh step 1, which hard-fails on any value still in that shape.
# Should a placeholder ever be reintroduced there, the check below names it
# rather than letting setup.sh abort mid-run.
ensure_env_file() {
  echo "--- Checking .env ---"
  if [[ -f "$ENV_FILE" ]]; then
    ok ".env already present at $ENV_FILE"
    return 0
  fi
  [[ -f "$ENV_DIST_FILE" ]] || fail ".env.dist not found at $ENV_DIST_FILE — cannot seed .env"

  cp "$ENV_DIST_FILE" "$ENV_FILE" || fail "could not write $ENV_FILE"
  ok "created .env from .env.dist"

  # Anything <placeholder>-shaped would abort setup.sh; surface it now.
  if grep -qE '^[A-Za-z_][A-Za-z0-9_]*=<.*>$' "$ENV_FILE"; then
    warn "set these values in .env before continuing:"
    grep -nE '^[A-Za-z_][A-Za-z0-9_]*=<.*>$' "$ENV_FILE" | sed 's/^/    /'
  fi
}

# Install gh as the HOST's git credential helper for github.com. Distinct from
# login_github_from_token, which runs via run_guest and so only ever configures
# the VM: the private submodule clones in ensure_source_submodules happen on the
# host, before the VM exists. Warning-only — a host without gh can still start,
# it just cannot clone private submodules.
setup_git_credential_helper() {
  echo "--- Configuring git credential helper ---"
  if ! command -v gh >/dev/null 2>&1; then
    warn "gh is not installed on this host; private submodule clones may fail"
    return 0
  fi
  if gh auth setup-git --hostname github.com >/dev/null 2>&1; then
    ok "git will authenticate to github.com through gh"
  else
    warn "could not configure gh as a git credential helper"
  fi
}

# First real step of every start: the run provisions a cluster and clones private
# sources, so a missing GitHub token is worth catching up front rather than 10
# minutes later at login_github_from_token. Prompt for it when absent and persist
# it to .ghtoken; stop the run outright when nothing usable is provided.
require_gh_token() {
  echo "--- Checking GitHub token ---"
  if [[ -s "$GH_TOKEN_FILE" ]]; then
    ok ".ghtoken found at $GH_TOKEN_FILE"
    return 0
  fi

  if [[ -f "$GH_TOKEN_FILE" ]]; then
    warn ".ghtoken at $GH_TOKEN_FILE is empty"
  else
    warn ".ghtoken not found at $GH_TOKEN_FILE"
  fi

  # No TTY (CI, piped run): there is nobody to ask, so fail with the fix.
  [[ -t 0 ]] || fail "no .ghtoken and no terminal to prompt on — create $GH_TOKEN_FILE with a GitHub token (https://github.com/settings/tokens) and re-run"

  echo "  Create one at https://github.com/settings/tokens (scopes: repo, read:org)."
  echo "  It will be saved to $GH_TOKEN_FILE (git-ignored)."
  local token=""
  # -s: the token is a credential and must not echo or land in scrollback.
  read -r -s -p "  GitHub token (empty to abort): " token < /dev/tty || true
  echo

  [[ -n "$token" ]] || fail "no GitHub token provided — aborting"

  ( umask 077; printf '%s\n' "$token" > "$GH_TOKEN_FILE" ) \
    || fail "could not write $GH_TOKEN_FILE"
  chmod 600 "$GH_TOKEN_FILE" 2>/dev/null || true
  ok "token saved to $GH_TOKEN_FILE"
}

# Attempt a non-interactive gh login from a repo-local token file. Missing gh or
# login errors are warning-only so start.sh can still continue; the token itself
# is already guaranteed by require_gh_token.
login_github_from_token() {
  local where=" in VM"
  if $NATIVE_LINUX; then where=""; fi
  echo "--- Attempting GitHub login${where} from .ghtoken ---"
  if [[ ! -f "$GH_TOKEN_FILE" ]]; then
    warn ".ghtoken not found at $GH_TOKEN_FILE; cannot login gh"
    return 0
  fi
  if [[ ! -s "$GH_TOKEN_FILE" ]]; then
    warn ".ghtoken is empty; cannot login gh"
    return 0
  fi
  if ! run_guest command -v gh >/dev/null 2>&1; then
    warn "gh is not installed${where}; cannot login gh"
    return 0
  fi
  if run_guest bash -euo pipefail -c 'gh auth login --with-token >/dev/null 2>&1' < "$GH_TOKEN_FILE"; then
    ok "GitHub CLI authenticated${where}"
  else
    warn "GitHub CLI login failed using .ghtoken"
  fi
}

# Refresh host-visible VM connection metadata and SSH access files.
refresh_support_files() {
  echo "--- Reading VM IP ---"
  local IP
  IP="$(limactl shell "$VM_NAME" hostname -I 2>/dev/null | tr ' ' '\n' \
          | grep -E '^192\.168\.252\.' | head -1 || true)"
  if [[ -z "$IP" ]]; then
    # Fallback: address on the lima0 interface, whatever its subnet.
    IP="$(limactl shell "$VM_NAME" bash -c \
      "ip -4 -o addr show lima0 2>/dev/null | awk '{print \$4}' | cut -d/ -f1" 2>/dev/null || true)"
  fi
  [[ -n "$IP" ]] || fail "could not determine host-reachable VM IP"
  ok "VM IP: $IP"

  echo "--- Writing Trustable support files ---"
  mkdir -p "$SUPPORT_DIR"

  printf '%s' "$IP" > "$SUPPORT_DIR/current.ip"
  ok "wrote current.ip -> $IP"

  # Point the apihost through the host-rewrite proxy (:8080), not bare <ip>:80.
  # A plain http://<ip>/api/info would send Host: <ip>, which traefik doesn't
  # match (404). Via <ip>.nip.io:8080 the proxy rewrites Host -> miniops.me, and
  # subdomains derived from this base (e.g. <x>.<ip>.nip.io:8080) also route.
  local APIHOST="http://${IP}.nip.io:8080"
  printf '%s' "$APIHOST" > "$SUPPORT_DIR/apihost"
  ok "wrote apihost -> $APIHOST"

  [[ -f "$LIMA_KEY" ]] || fail "Lima identity not found at $LIMA_KEY"
  cp "$LIMA_KEY" "$SUPPORT_DIR/id_ed25519"
  chmod 0600 "$SUPPORT_DIR/id_ed25519"
  [[ -f "$LIMA_KEY.pub" ]] && cp "$LIMA_KEY.pub" "$SUPPORT_DIR/id_ed25519.pub"
  ok "copied Lima key -> id_ed25519"

  if probe_ssh "$IP"; then
    ok "ssh reaches $USER@$IP"
  else
    warn "ssh could not connect yet (key auth may need a moment)"
  fi

  ensure_guest_user
  ensure_ssh_config "$IP"
}

# Can we run a command in the VM over plain ssh, with the identity that was just
# copied into the support dir?
#
# This deliberately does NOT go through ./ssh.sh. That script is the user-facing
# wrapper: it takes no command and always `exec bash`, so calling it here opened
# an interactive shell and blocked the rest of the start — `./start.sh -v` never
# reached the step that opens VS Code, and the user was left in a VM shell.
#
# It also does not go through run_guest/limactl, because what needs proving is
# specifically that *ssh* works with *that identity*: it is the path VS Code
# Remote-SSH takes, and limactl would succeed even when ssh could not connect.
#
# BatchMode keeps a missing or unauthorized key a failure instead of a password
# prompt that would hang an unattended start.
probe_ssh() {
  local IP="$1"
  ssh -i "$SUPPORT_DIR/id_ed25519" \
      -o BatchMode=yes -o StrictHostKeyChecking=no \
      -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 \
      "$USER@$IP" true >/dev/null 2>&1
}

# Wait until a command can be executed in the VM over ssh. Used by the
# existing-VM fast path after start, so VS Code Remote-SSH opens against a ready
# endpoint instead of racing the guest boot.
wait_for_ssh_ready() {
  local retries=30
  local delay_secs=2
  local IP
  IP="$(cat "$SUPPORT_DIR/current.ip")"
  echo "--- Waiting for SSH readiness ---"
  for _ in $(seq 1 "$retries"); do
    if probe_ssh "$IP"; then
      ok "SSH is ready"
      return 0
    fi
    sleep "$delay_secs"
  done
  fail "ssh could not reach the VM after $((retries * delay_secs))s"
}

# Read the host-reachable IP from the running VM and write the Trustable support
# files. Used both by the fresh-install path and when the VM already exists.
finish() {
  refresh_support_files
  local IP APIHOST
  IP="$(cat "$SUPPORT_DIR/current.ip")"
  APIHOST="$(cat "$SUPPORT_DIR/apihost")"
  ensure_ollama
  ensure_kubefwd
  ensure_gh_apt
  ensure_jq
  # After refresh_support_files (which runs ensure_guest_user) and the package
  # install, so both accounts this writes to exist.
  ensure_ops_env
  ensure_tls_san "$IP"
  apply_reverse_proxy "$IP"

  # setup.sh step 7 curls the apihost, so OpenWhisk must already be serving
  # before it runs — not merely k3s being up.
  wait_for_openwhisk

  # Provision the in-VM toolchain (ops/go/air/uv/node/pi + MCP servers) by
  # running setup.sh INSIDE the VM as the mirrored current user, in this repo dir
  # (Lima mounts it at the same path). Idempotent — re-runs just verify.
  echo "--- Running setup.sh in the VM as $HOST_USER ---"
  limactl shell --workdir "$MOUNT_DIR" "$VM_NAME" bash ./setup.sh \
    || fail "setup.sh failed in the VM"
  ok "setup.sh completed"
  ensure_bestia_proxy
  ensure_gh
  login_github_from_token

  echo
  echo -e "${GREEN}=== Trustable VM ready ===${NC}"
  echo "  apihost:      $APIHOST"
  echo "  host-rewrite: http://<label>.$IP.nip.io:8080  ->  <label>.miniops.me"
  echo "  ssh:          ./ssh.sh   |   ssh $HOST_USER@$IP   |   ssh trudev"
  echo "  next:         ./run.sh in the VM, started below (./start.sh -n to skip)"
  echo "  vscode:       ./start.sh -v (opens this folder over Remote-SSH instead)"
  echo "  ollama:       http://localhost:11434  (CPU, in-VM)"
  echo "  mount:        $MOUNT_DIR  (owned by $HOST_USER in the VM)"
  echo "  stop:         ./start.sh -s   (keep the VM; restart with ./start.sh)"
  echo "  destroy:      ./start.sh -k"

  if [[ "$OPEN_VSCODE" == 1 ]]; then open_vscode; fi
  if [[ "$RUN_APP" == 1 ]]; then run_in_vm; fi
}

# Native-Linux counterpart of finish(). Same ordering and the same shared
# helpers, minus everything that only exists to bridge a Mac to a VM: no Lima
# boot, no virtiofs mount, no mirrored guest user, no ssh key or ~/.ssh/config,
# no VS Code Remote-SSH.
finish_native() {
  # preflight_native and ensure_gh_apt already ran at the top level, before the
  # credential helper and the token gate — see the block near the end of this
  # file. They are idempotent, but a linear provisioning script should call them
  # once, where the ordering is visible.
  ensure_source_submodules

  # Install the cluster only when it is absent; otherwise just confirm it runs.
  if run_guest dpkg -l trustable 2>/dev/null | grep -q '^ii'; then
    ok "Trustable package already installed"
  else
    ensure_deb   # sets DEB_FILE
    install_package_native
  fi
  wait_for_local_k3s

  refresh_support_files_native
  local IP APIHOST
  IP="$(cat "$SUPPORT_DIR/current.ip")"
  APIHOST="$(cat "$SUPPORT_DIR/apihost")"

  ensure_ollama
  ensure_kubefwd
  ensure_jq
  # After the package install above, so the 'trustable' account exists.
  ensure_ops_env
  ensure_tls_san "$IP"
  apply_reverse_proxy "$IP"

  # setup.sh step 7 curls the apihost, so OpenWhisk must already be serving
  # before it runs — not merely k3s being up.
  wait_for_openwhisk

  # Provision the toolchain (ops/go/air/uv/node/pi + MCP servers). Same script
  # the VM path runs, just invoked directly. Idempotent — re-runs just verify.
  echo "--- Running setup.sh as $(id -un) ---"
  bash ./setup.sh || fail "setup.sh failed"
  ok "setup.sh completed"
  ensure_bestia_proxy
  ensure_gh
  login_github_from_token

  echo
  echo -e "${GREEN}=== Trustable environment ready ===${NC}"
  echo "  apihost:      $APIHOST"
  echo "  host-rewrite: http://<label>.$IP.nip.io:8080  ->  <label>.miniops.me"
  echo "  ollama:       http://localhost:11434"
  # There is no Remote-SSH hop on a native host: the sources are right here, so
  # the "connect" step is just opening this directory. Print the command rather
  # than failing when `code` is not on PATH — it is not required on this path.
  if command -v code >/dev/null 2>&1; then
    echo "  vscode:       code $MOUNT_DIR"
  else
    echo "  vscode:       'code' not on PATH — open this folder with:  code $MOUNT_DIR"
    echo "                (VS Code: Command Palette > Shell Command: Install 'code' command in PATH)"
  fi
  echo "  next:         ./run.sh"
}

# Resolve + cache the .deb for the host arch into dist/, downloading if absent.
# Sets the global DEB_FILE. The deb arch matches the HOST arch (the VM runs the
# host arch under vz): arm64 on Apple Silicon, amd64 on Intel.
ensure_deb() {
  local DEB_ARCH DL_SUFFIX TMP_DEB HOST_ARCH
  # On Linux the package is installed by dpkg on THIS machine, so ask dpkg which
  # architecture it will accept — that is what governs `apt-get install`, and it
  # is authoritative on a multiarch host where uname reports the kernel's arch.
  # macOS has no dpkg (the .deb is installed inside the VM), so fall back to
  # uname -m there; the VM runs the host arch under vz.
  if $NATIVE_LINUX; then
    HOST_ARCH="$(dpkg --print-architecture 2>/dev/null || true)"
    [[ -n "$HOST_ARCH" ]] || fail "dpkg --print-architecture failed — is this a Debian/Ubuntu host?"
  else
    HOST_ARCH="$(uname -m)"
  fi
  case "$HOST_ARCH" in
    arm64|aarch64) DEB_ARCH=arm64; DL_SUFFIX=linux-arm ;;
    x86_64|amd64)  DEB_ARCH=amd64; DL_SUFFIX=linux-amd ;;
    *) fail "unsupported host arch: $HOST_ARCH" ;;
  esac
  ok "package architecture: $DEB_ARCH (detected: $HOST_ARCH)"
  DEB_FILE="${DIST_DIR}/trustable_${TRUSTABLE_VERSION}_${DEB_ARCH}.deb"
  mkdir -p "$DIST_DIR"
  if [[ -s "$DEB_FILE" ]]; then
    ok "Using cached package: $DEB_FILE"
  else
    echo "--- Downloading Trustable package (~4GB) -> $DEB_FILE ---"
    # Download to a temp file then move into place, so an interrupted download
    # never leaves a truncated cache entry.
    TMP_DEB="${DEB_FILE}.part"
    curl -fL --retry 3 -o "$TMP_DEB" "${DOWNLOAD_BASE}/${DL_SUFFIX}" \
      || { rm -f "$TMP_DEB"; fail "download failed"; }
    mv "$TMP_DEB" "$DEB_FILE"
    ok "Downloaded $DEB_FILE"
  fi
}

# Copy the cached .deb into the running VM and install it (k3s + helpers), set
# the netplan route so the firewall DROP lands on vzNAT, and authorize the Lima
# ssh key for the package-created 'trustable' user. No-op if already installed.
# Requires DEB_FILE (call ensure_deb first) and a running VM.
install_package() {
  echo "--- Copying package into the VM ---"
  limactl copy "$DEB_FILE" "${VM_NAME}:/tmp/trustable.deb" \
    || fail "failed to copy $DEB_FILE into the VM"

  echo "--- Installing the Trustable package ---"
  limactl shell "$VM_NAME" sudo bash -euo pipefail -s <<'GUEST'
export DEBIAN_FRONTEND=noninteractive

if dpkg -l trustable 2>/dev/null | grep -q '^ii'; then
  echo "trustable already installed"; exit 0
fi

apt-get update -qq
apt-get install -y -qq iptables

# Make vzNAT (eth0) the primary default route BEFORE installing. The package's
# postinst reads the default-route interface (`ip route show default`, lowest
# metric first) and installs a firewall dropin that DROPs :80/:443/:6443 on it.
# Lima's netplan gives eth0 metric 200 and the host-facing lima0 metric 100, so
# by default the DROP lands on lima0 and blocks the host. Override eth0 to a
# lower metric than lima0 so the DROP lands on the non-host-reachable vzNAT.
cat >/etc/netplan/99-trustable.yaml <<'NET'
network:
  version: 2
  ethernets:
    eth0:
      dhcp4-overrides:
        route-metric: 50
NET
chmod 0600 /etc/netplan/99-trustable.yaml
netplan apply
# Wait for the default route to actually move onto eth0 before installing.
for _ in $(seq 1 10); do
  ip -4 route show default | awk 'NR==1{print $5}' | grep -qx eth0 && break
  sleep 1
done
DEF_IFACE=$(ip -4 route show default | awk 'NR==1{print $5}')
echo "default-route interface is now: ${DEF_IFACE}"
[ "$DEF_IFACE" = eth0 ] || echo "WARNING: default route is $DEF_IFACE, not eth0 — port 80 may be blocked from host" >&2

[ -s /tmp/trustable.deb ] || { echo "package not found in guest" >&2; exit 1; }
apt-get install -y /tmp/trustable.deb
rm -f /tmp/trustable.deb

# ssh.sh reaches the VM as trustable@<ip> with the Lima identity, so authorize
# the Lima pubkey(s) for the package-created 'trustable' user.
install -d -o trustable -g trustable -m 0700 /home/trustable/.ssh
: > /tmp/authkeys
for ak in /home/*/.ssh/authorized_keys; do
  [ "$ak" = /home/trustable/.ssh/authorized_keys ] && continue
  [ -f "$ak" ] && cat "$ak" >> /tmp/authkeys
done
[ -f /home/trustable/.ssh/authorized_keys ] && cat /home/trustable/.ssh/authorized_keys >> /tmp/authkeys
sort -u /tmp/authkeys > /home/trustable/.ssh/authorized_keys
rm -f /tmp/authkeys
chown -R trustable:trustable /home/trustable/.ssh
chmod 0600 /home/trustable/.ssh/authorized_keys
echo "install complete"
GUEST
  ok "Trustable package installed and ssh key authorized"
}

# Native-Linux counterpart of install_package: install the cached .deb straight
# into THIS machine. Two deliberate carve-outs versus the VM path:
#
#  * No netplan override. The VM needs one only because Lima gives it two
#    interfaces and defaults the route to the host-facing lima0, which would put
#    the package's :80/:443/:6443 firewall DROP between the Mac and k3s. A native
#    host has no such split — its real default route is the correct one to
#    protect, which is exactly what the package's postinst already picks.
#  * No authorized_keys grafting for the package's 'trustable' user. That exists
#    so ssh.sh can reach the VM; nobody ssh's into the machine they are sitting at.
#
# Requires DEB_FILE (call ensure_deb first). No-op if already installed.
install_package_native() {
  echo
  warn "About to install the Trustable package on THIS machine:"
  warn "  package:  $DEB_FILE"
  warn "  installs: k3s + the Trustable service stack, and a firewall dropin"
  warn "            that DROPs :80/:443/:6443 on the default-route interface"
  echo

  run_privileged DEB_FILE="$(cd "$(dirname "$DEB_FILE")" && pwd)/$(basename "$DEB_FILE")" <<'GUEST'
export DEBIAN_FRONTEND=noninteractive

if dpkg -l trustable 2>/dev/null | grep -q '^ii'; then
  echo "trustable already installed"; exit 0
fi

apt-get update -qq
apt-get install -y -qq iptables

DEF_IFACE=$(ip -4 route show default | awk 'NR==1{print $5}')
echo "default-route interface: ${DEF_IFACE:-none}"

[ -s "$DEB_FILE" ] || { echo "package not found at $DEB_FILE" >&2; exit 1; }
apt-get install -y "$DEB_FILE"
echo "install complete"
GUEST
  ok "Trustable package installed on this host"
}

# Wait for the local k3s to serve /readyz and for the nuvolaris namespace to
# exist. A fresh package install needs 60-90s before OpenWhisk is up, and
# setup.sh step 7 curls the apihost, so it must not run against a booting cluster.
wait_for_local_k3s() {
  echo "--- Waiting for local k3s ---"
  # Prefer `k3s kubectl`: it points itself at /etc/rancher/k3s/k3s.yaml. A plain
  # kubectl under `sudo -n` runs as root with no KUBECONFIG and would talk to the
  # default localhost:8080 instead, failing against a perfectly healthy cluster.
  # This runs before setup.sh writes ~/.ops/tmp/kubeconfig, so k3s.yaml is the
  # only kubeconfig that exists yet.
  local kubectl_cmd
  if command -v k3s >/dev/null 2>&1; then
    kubectl_cmd=(sudo -n k3s kubectl)
  elif command -v kubectl >/dev/null 2>&1; then
    kubectl_cmd=(sudo -n kubectl --kubeconfig /etc/rancher/k3s/k3s.yaml)
  else
    fail "neither k3s nor kubectl is installed — the Trustable package did not install correctly"
  fi

  local ready=false
  for _ in $(seq 1 90); do
    if "${kubectl_cmd[@]}" get --raw='/readyz' >/dev/null 2>&1; then
      ready=true
      break
    fi
    sleep 2
  done
  $ready || {
    echo "last probe: ${kubectl_cmd[*]} get --raw=/readyz" >&2
    "${kubectl_cmd[@]}" get --raw='/readyz' || true
    fail "local k3s API did not become ready (probed with: ${kubectl_cmd[*]})"
  }
  ok "k3s API is ready"

  ready=false
  for _ in $(seq 1 90); do
    if "${kubectl_cmd[@]}" get ns nuvolaris >/dev/null 2>&1; then
      ready=true
      break
    fi
    sleep 2
  done
  $ready || fail "the nuvolaris namespace never appeared — is the Trustable package healthy?"
  ok "nuvolaris namespace is present"
}

# Resolve the address other machines (and the browser) can reach this host on.
#
# lima0 comes first: this path also runs INSIDE a Lima guest (uname says Linux,
# so the native branch is taken), and there the default route belongs to eth0 —
# the vmnet NAT segment, which the host cannot route back into. lima0 carries a
# second default route at a higher metric, so `route get` always loses to eth0
# and would hand the browser an unreachable 192.168.5.x. Preferring lima0 makes
# the in-guest result agree with refresh_support_files, which greps for exactly
# this subnet on the macOS side.
#
# On bare-metal Linux there is no lima0 and the default-route source is correct.
# Falls back to loopback on a host with no default route, which still serves a
# local-only browser correctly.
native_host_ip() {
  local ip
  ip="$(ip -4 -o addr show lima0 2>/dev/null | awk '{print $4}' | cut -d/ -f1)"
  [[ -n "$ip" ]] || ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<NF;i++) if($i=="src") {print $(i+1); exit}}')"
  [[ -n "$ip" ]] || ip="127.0.0.1"
  printf '%s' "$ip"
}

# Native counterpart of refresh_support_files. Writes the same two files the Go
# server and the tooling read, at the XDG location resolved by SUPPORT_DIR.
# There is no id_ed25519: nothing ssh's anywhere on this path.
refresh_support_files_native() {
  echo "--- Writing Trustable support files ---"
  local IP APIHOST
  IP="$(native_host_ip)"
  ok "host IP: $IP"

  mkdir -p "$SUPPORT_DIR"
  printf '%s' "$IP" > "$SUPPORT_DIR/current.ip"
  ok "wrote current.ip -> $IP"

  # Same reasoning as the VM path: a bare http://<ip>/api/info sends Host: <ip>,
  # which traefik does not match. Go through the host-rewrite proxy on :8080.
  APIHOST="http://${IP}.nip.io:8080"
  printf '%s' "$APIHOST" > "$SUPPORT_DIR/apihost"
  ok "wrote apihost -> $APIHOST"
}

# Preflight for the native path: everything the rest of the flow assumes.
preflight_native() {
  echo "--- Checking this host ---"
  [[ -r /etc/os-release ]] || fail "cannot identify the Linux distribution: /etc/os-release is missing"
  # shellcheck disable=SC1091
  source /etc/os-release
  case " ${ID:-} ${ID_LIKE:-} " in
    *ubuntu*|*debian*) ;;
    *) fail "unsupported Linux distribution: ${PRETTY_NAME:-${ID:-unknown}} (expected Ubuntu/Debian)" ;;
  esac
  ok "distribution: ${PRETTY_NAME:-${ID:-unknown}}"

  # Same source of truth as ensure_deb, so the preflight gate and the package
  # actually selected can never disagree.
  local dpkg_arch
  dpkg_arch="$(dpkg --print-architecture 2>/dev/null || true)"
  [[ -n "$dpkg_arch" ]] || fail "dpkg --print-architecture failed — is this a Debian/Ubuntu host?"
  case "$dpkg_arch" in
    amd64|arm64) ;;
    *) fail "unsupported architecture: $dpkg_arch (expected amd64 or arm64)" ;;
  esac
  ok "architecture: $dpkg_arch"

  # setup.sh, run.sh and the package install all assume passwordless sudo.
  sudo -n true 2>/dev/null \
    || fail "passwordless sudo is required (add: $(id -un) ALL=(ALL) NOPASSWD:ALL to /etc/sudoers.d/)"
  ok "passwordless sudo works"

  # k3s is a systemd service; a container/WSL instance without systemd cannot run it.
  local systemd_state
  systemd_state="$(systemctl is-system-running 2>/dev/null || true)"
  [[ "$systemd_state" != "offline" && -d /run/systemd/system ]] \
    || fail "systemd is not running — k3s cannot be managed on this host"
  ok "systemd is available"
}

# A dedicated host key for passwordless access as the mirrored user. The Lima
# identity would also work, but `code --remote ssh-remote+<user>@<ip>` shells out
# to plain `ssh` with no -i, so the key has to be discoverable from ~/.ssh/config.
# Generated once (no passphrase) and reused across VMs.
HOST_KEY="$HOME/.ssh/id_trudev"

ensure_host_key() {
  [[ -f "$HOST_KEY" ]] && return 0
  echo "--- Generating ssh key for $HOST_USER -> VM ($HOST_KEY) ---"
  mkdir -p "$HOME/.ssh"; chmod 0700 "$HOME/.ssh"
  ssh-keygen -t ed25519 -N '' -C "$HOST_USER@trudev" -f "$HOST_KEY" >/dev/null \
    || fail "ssh-keygen failed"
  ok "generated $HOST_KEY"
}

# Maintain a managed block in ~/.ssh/config so `ssh <user>@<ip>` — and therefore
# VS Code Remote-SSH, which cannot be handed an -i — picks up the key without a
# passphrase prompt. Rewritten on every run because the VM IP can change.
ensure_ssh_config() {
  local IP="$1" CFG="$HOME/.ssh/config"
  local BEGIN="# >>> trustable trudev >>>" END="# <<< trustable trudev <<<"
  touch "$CFG"; chmod 0600 "$CFG"
  # Drop any previous managed block, then append the current one.
  awk -v b="$BEGIN" -v e="$END" '
    $0==b {skip=1} !skip {print} $0==e {skip=0}' "$CFG" > "$CFG.tmp"
  {
    cat "$CFG.tmp"
    echo "$BEGIN"
    printf 'Host %s %s.nip.io trudev\n' "$IP" "$IP"
    printf '  User %s\n' "$HOST_USER"
    printf '  HostName %s\n' "$IP"
    printf '  IdentityFile %s\n' "$HOST_KEY"
    printf '  IdentitiesOnly yes\n'
    printf '  StrictHostKeyChecking no\n'
    printf '  UserKnownHostsFile /dev/null\n'
    echo "$END"
  } > "$CFG"
  rm -f "$CFG.tmp"
  ok "ssh config entry for $HOST_USER@$IP (alias: trudev)"
}

# Mirror the current macOS user into the VM: a guest account with the same name
# and UID, so files under the virtiofs mount (mounted at the same path) keep the
# host's ownership. Give them passwordless sudo and authorize the same Lima key
# so `ssh <host_user>@<ip>` works too. Idempotent — safe to run on every start.
# Skip when the host user is 'trustable' or 'root' (already present in the VM).
ensure_guest_user() {
  [ -n "$HOST_USER" ] || return 0
  case "$HOST_USER" in trustable|root) return 0 ;; esac
  echo "--- Mirroring host user '$HOST_USER' into the VM ---"
  ensure_host_key
  limactl shell "$VM_NAME" sudo HOST_USER="$HOST_USER" HOST_UID="$HOST_UID" \
    HOST_PUBKEY="$(cat "$HOST_KEY.pub")" bash -euo pipefail -s <<'GUEST'
if ! id "$HOST_USER" >/dev/null 2>&1; then
  # Only pin the UID if it isn't already taken by another account.
  if [ -n "${HOST_UID:-}" ] && ! getent passwd "$HOST_UID" >/dev/null 2>&1; then
    useradd -g sudo -m -s /bin/bash -u "$HOST_UID" "$HOST_USER"
  else
    useradd -g sudo -m -s /bin/bash "$HOST_USER"
  fi
  echo "created guest user $HOST_USER ($(id -u "$HOST_USER"))"
fi
printf '%s ALL=(ALL) NOPASSWD:ALL\n' "$HOST_USER" > "/etc/sudoers.d/90-$HOST_USER"
chmod 0440 "/etc/sudoers.d/90-$HOST_USER"

# Resolve the account's REAL home and group from passwd — never assume
# /home/$HOST_USER. Lima may already own the account and give it a suffixed home
# (e.g. /home/msciab.guest, with /home/msciab.linux symlinked to it) to avoid
# colliding with the virtiofs mount. sshd reads authorized_keys from the passwd
# home, so writing to the assumed path silently authorizes nothing and every
# publickey auth fails with "Permission denied (publickey)".
HOME_DIR="$(getent passwd "$HOST_USER" | cut -d: -f6)"
[ -n "$HOME_DIR" ] || { echo "could not resolve home for $HOST_USER" >&2; exit 1; }
HOST_GROUP="$(id -gn "$HOST_USER")"
echo "authorizing keys in ${HOME_DIR}/.ssh (group ${HOST_GROUP})"

install -d -o "$HOST_USER" -g "$HOST_GROUP" -m 0700 "$HOME_DIR/.ssh"
# Authorize the same key(s) trustable trusts, plus the dedicated host key, so
# both `ssh <user>@<ip>` and VS Code Remote-SSH connect without a passphrase.
AK="$HOME_DIR/.ssh/authorized_keys"
: > /tmp/hostkeys
[ -f /home/trustable/.ssh/authorized_keys ] && cat /home/trustable/.ssh/authorized_keys >> /tmp/hostkeys
[ -f "$AK" ] && cat "$AK" >> /tmp/hostkeys
[ -n "${HOST_PUBKEY:-}" ] && printf '%s\n' "$HOST_PUBKEY" >> /tmp/hostkeys
sort -u /tmp/hostkeys > "$AK"
rm -f /tmp/hostkeys
chown "$HOST_USER:$HOST_GROUP" "$AK"
chmod 0600 "$AK"
GUEST
  ok "guest user '$HOST_USER' ready (mount owner)"
}

# True if the trustable package is installed in the running VM.
package_installed() {
  limactl shell "$VM_NAME" dpkg -l trustable 2>/dev/null | grep -q '^ii'
}

# -s and -k are VM lifecycle operations. On a native host there is no VM, and
# they must never be reinterpreted as "stop/destroy this machine's k3s" — refuse
# instead of silently succeeding.
if $NATIVE_LINUX; then
  case "${1:-}" in
    -s|-k) fail "$1 manages the Lima VM and does not apply on a native Linux host" ;;
  esac
else
  command -v limactl >/dev/null 2>&1 || fail "limactl not found (brew install lima)"
fi

# --- stop (keep the VM): ./start.sh -s --------------------------------------
if [[ "${1:-}" == "-s" ]]; then
  if limactl list --quiet 2>/dev/null | grep -qx "$VM_NAME"; then
    STATUS="$(limactl list --format '{{.Status}}' "$VM_NAME" 2>/dev/null)"
    if [[ "$STATUS" == "Running" ]]; then
      echo "--- Stopping VM '$VM_NAME' (keeping it) ---"
      limactl stop "$VM_NAME" || fail "failed to stop VM '$VM_NAME'"
      ok "VM '$VM_NAME' stopped — run ./start.sh to restart it (no reinstall)"
    else
      warn "VM '$VM_NAME' is not running ($STATUS) — nothing to do"
    fi
  else
    warn "VM '$VM_NAME' does not exist — nothing to do"
  fi
  exit 0
fi

# --- final step: run.sh in the VM (default) or VS Code (./start.sh -v) -------
# The default finish is `./run.sh` inside the VM, which is what you actually want
# after a start: the dev server up on :8910. `-v` opens VS Code over Remote-SSH
# instead; Remote-SSH shells out to plain `ssh` with no -i, so it relies on the
# managed ~/.ssh/config block (written by ensure_ssh_config on every start) to
# supply the identity. `-n` finishes without doing either.
OPEN_VSCODE=0
RUN_APP=1
case "${1:-}" in
  -n) RUN_APP=0; shift ;;
  -v) OPEN_VSCODE=1; RUN_APP=0; shift ;;
  # -s/-k never reach the finish path, so they must not require `code` on PATH.
  -s|-k) RUN_APP=0 ;;
esac
# The native path has no VM to shell into and no Remote-SSH hop (you are already
# on the machine), so both flags are accepted and ignored there.
if $NATIVE_LINUX; then OPEN_VSCODE=0; RUN_APP=0; fi
if [[ "$OPEN_VSCODE" == 1 ]]; then
  command -v code >/dev/null 2>&1 \
    || fail "'code' not found — enable it in VS Code: Shell Command: Install 'code' command in PATH, or run ./start.sh -n"
fi

# Opens VS Code on the mounted folder in the VM. Called at the end of the normal
# start path (which has already booted the VM and written the ssh config).
open_vscode() {
  local IP
  IP="$(cat "$SUPPORT_DIR/current.ip")"
  echo "--- Opening VS Code on $HOST_USER@$IP:$MOUNT_DIR ---"
  code --remote "ssh-remote+$HOST_USER@$IP" "$MOUNT_DIR" \
    || fail "code --remote failed (is the Remote-SSH extension installed?)"
  ok "VS Code opening — first connect installs the remote server, give it a moment"
}

# Runs ./run.sh inside the VM, in the mounted repo dir, as the mirrored user.
# This is the default finish and it stays in the foreground: run.sh owns kubefwd
# and `air`, so Ctrl-C here is how you stop the dev server.
run_in_vm() {
  echo "--- Running ./run.sh in the VM as $HOST_USER (Ctrl-C to stop) ---"
  limactl shell --workdir "$MOUNT_DIR" "$VM_NAME" bash ./run.sh
}

# --- teardown: ./start.sh -k -----------------------------------------------
if [[ "${1:-}" == "-k" ]]; then
  if limactl list --quiet 2>/dev/null | grep -qx "$VM_NAME"; then
    echo "--- Stopping and deleting VM '$VM_NAME' ---"
    limactl stop -f "$VM_NAME" 2>/dev/null || true
    limactl delete -f "$VM_NAME" || fail "failed to delete VM '$VM_NAME'"
    ok "VM '$VM_NAME' terminated"
  else
    warn "VM '$VM_NAME' does not exist — nothing to do"
  fi
  exit 0
fi

# First steps of a real start on either host. Placed after the -s/-k branches,
# which exit above and must not need a .env or a token to stop or destroy a VM.
ensure_env_file
# On a native Linux host (the path WSL takes) the host IS the target, so `gh`
# has to exist before the credential helper is wired and before the token gate:
# setup_git_credential_helper needs the CLI, and ensure_source_submodules then
# clones a PRIVATE submodule over https. preflight_native comes first because
# ensure_gh_apt installs through run_privileged, which is the `sudo -n` that
# preflight is what verifies — it is pure checks, so an unsupported host now
# fails before being asked for a token. On macOS neither applies: gh comes from
# brew and the VM ensure_gh_apt would target does not exist yet.
if $NATIVE_LINUX; then
  preflight_native
  ensure_gh_apt
fi
# WHY: `gh auth login --with-token` authenticates the gh CLI but does NOT install
# a git credential helper, so plain `git` still has no way to read github.com.
# ensure_source_submodules runs on the HOST and clones private submodules
# (trustable-acp, olaris-bestia) over https, which then fails with
# "could not read Username for 'https://github.com'". This wires gh in as the
# host's credential helper so those fetches authenticate with the existing token.
setup_git_credential_helper
require_gh_token

# --- native Linux: no VM, initialize this host directly ----------------------
if $NATIVE_LINUX; then
  finish_native
  exit 0
fi

ensure_source_submodules

# If the VM already exists, don't re-provision. Start it when needed, refresh
# support files, wait for SSH readiness, then open VS Code. Use ./start.sh -k
# first if you actually want a clean rebuild.
if limactl list --quiet 2>/dev/null | grep -qx "$VM_NAME"; then
  STATUS="$(limactl list --format '{{.Status}}' "$VM_NAME" 2>/dev/null)"
  if [[ "$STATUS" != "Running" ]]; then
    echo "--- VM '$VM_NAME' exists ($STATUS) — starting it ---"
    limactl start "$VM_NAME" || fail "failed to start existing VM '$VM_NAME'"
  else
    echo "--- VM '$VM_NAME' already running ---"
  fi
  refresh_support_files
  wait_for_ssh_ready
  # Existing VM flow intentionally skips setup/provisioning.
  if [[ "$OPEN_VSCODE" == 1 ]]; then open_vscode; fi
  if [[ "$RUN_APP" == 1 ]]; then run_in_vm; fi
  exit 0
fi

# --- download + cache the .deb on the host -----------------------------------
ensure_deb   # sets DEB_FILE

# --- lima config ------------------------------------------------------------
# vz gives the guest two interfaces:
#   * lima0 (192.168.252.x) — Lima's shared bridge, REACHABLE from the macOS host
#   * eth0  (vzNAT)         — outbound NAT, NOT host-reachable
# The Trustable .deb installs a firewall dropin that DROPs :80/:443/:6443 on the
# DEFAULT-route interface. We therefore force the vzNAT interface to hold the
# default route (see the routefix provision below) so the DROP lands there and
# lima0:80 stays reachable — that lima0 address is what we publish as the apihost.
LIMA_CONFIG="$(mktemp -t trustable-lima-XXXX).yaml"
trap 'rm -f "$LIMA_CONFIG"' EXIT

# The mount is writable and lands at the same path inside the VM as on the host,
# so a guest user with the host's UID (created in install_package) owns the files.
cat >"$LIMA_CONFIG" <<YAML
vmType: vz
os: Linux
images:
  - location: "https://cloud-images.ubuntu.com/releases/24.04/release/ubuntu-24.04-server-cloudimg-arm64.img"
    arch: "aarch64"
  - location: "https://cloud-images.ubuntu.com/releases/24.04/release/ubuntu-24.04-server-cloudimg-amd64.img"
    arch: "x86_64"
cpus: 4
memory: "8GiB"
# Trustable keeps the full k3s service stack and imports multi-layer development
# images locally; 60 GiB restores headroom over the DiskPressure-prone 40 GiB
# default without imposing the larger 100 GiB allocation on every new VM.
disk: "60GiB"
networks:
  - vzNAT: true
mountType: virtiofs
mounts:
  - location: "${MOUNT_DIR}"
    mountPoint: "${MOUNT_DIR}"
    writable: true
ssh:
  loadDotSSHPubKeys: false
YAML

# --- create + start (fast: no heavy install here) --------------------------
echo "--- Booting Trustable VM ---"
limactl start --name "$VM_NAME" --tty=false "$LIMA_CONFIG" \
  || fail "limactl start failed"
ok "VM '$VM_NAME' booted"

# Install the package (copy the cached .deb in + apt install), then read the IP
# and write the support files.
install_package
finish
