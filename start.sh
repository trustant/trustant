#!/bin/bash
#
# start.sh — provision a local Trustable VM with Lima and wire it up so the
# rest of the tooling (ssh.sh, setup.sh, build.sh, publish.sh) finds it exactly
# where the macOS app would put it: ~/Library/Application Support/Trustable/.
#
# Plain run:   boots a plain Ubuntu VM (vz), then installs the Trustable .deb
#              (k3s + helpers) inside it, installs a CPU-only ollama host
#              (localhost:11434, pinned to the image's OLLAMA_VERSION), and
#              writes the VM ip, apihost and ssh key to the Trustable support dir.
#   ./start.sh
#
# Stop:        stops the VM without deleting it, so a later ./start.sh restarts
#              it (no reinstall).
#   ./start.sh -s
#
# Teardown:    stops and deletes the VM.
#   ./start.sh -k
#
# The ~3.6GB package is downloaded+cached on the HOST (under dist/) before the
# VM boots, then copied in and installed over `limactl shell`. Installing this
# way — rather than as a Lima `provision` script — avoids limactl start's ~10min
# readiness timeout, and caching on the host makes re-runs skip the download.
#
set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
ok()   { echo -e "${GREEN}✓ $1${NC}"; }
warn() { echo -e "${YELLOW}⚠ $1${NC}"; }
fail() { echo -e "${RED}✗ $1${NC}"; exit 1; }

cd "$(dirname "$0")"

VM_NAME="trudev"
SUPPORT_DIR="$HOME/Library/Application Support/Trustable"
LIMA_KEY="$HOME/.lima/_config/user"          # shared identity limactl ssh uses
DOWNLOAD_BASE="https://landing2.nuvolaris.org/api/my/v1/download"
TRUSTABLE_VERSION="0.3.10"
DIST_DIR="dist"                              # host-side cache for the .deb

# CPU-only ollama is installed as a host process in the VM (localhost:11434); the
# app mostly uses cloud models. Pin to the same version as the image (ARG line in
# image/Dockerfile) so the VM matches the container.
OLLAMA_VERSION="$(grep -m1 '^ARG OLLAMA_VERSION=' image/Dockerfile 2>/dev/null | cut -d= -f2 | tr -d ' ')"

# The current macOS user + the folder start.sh runs from. Both are mirrored into
# the VM: a guest user with the same name and UID owns a virtiofs mount of this
# folder at the same path, so files edited in the VM keep the host's ownership.
HOST_USER="$(id -un)"
HOST_UID="$(id -u)"
MOUNT_DIR="$(pwd)"

# Ensure the k3s API serving cert covers the host-reachable lima0 IP, so the
# kubeconfig setup.sh extracts (server: https://<ip>:6443) verifies. k3s's cert
# only lists the node IP (eth0/vzNAT) + 127.0.0.1 by default, NOT the lima0 IP
# the host connects to — so without this, host-side `kubectl`/`ops` fail TLS
# verification. Idempotent: only regenerates the cert when the IP isn't a SAN yet.
ensure_tls_san() {
  local IP="$1"
  echo "--- Ensuring k3s API cert covers $IP ---"
  limactl shell "$VM_NAME" sudo IP="$IP" bash -euo pipefail -s <<'GUEST'
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
  limactl shell "$VM_NAME" sudo IP="$IP" IPRE="$ipre" bash -euo pipefail -s <<'GUEST'
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

# Roll the deployment so a changed ConfigMap (new IP) is picked up, then wait.
k3s kubectl -n nuvolaris rollout restart deploy/apihost-proxy
k3s kubectl -n nuvolaris rollout status deploy/apihost-proxy --timeout=120s
echo "reverse proxy deployed (upstream traefik ${TRAEFIK_IP}:80)"
GUEST
  ok "reverse proxy listening on :8080"
}

# Install a CPU-only ollama as a host process inside the VM, pinned to
# OLLAMA_VERSION, and enable its service so it serves on localhost:11434 (the
# app's OLLAMA_ENDPOINT). Apple's vz gives the Linux guest no GPU passthrough, so
# this is CPU-only — fine, since the app mostly uses cloud models. Idempotent:
# skips the install when ollama is already present at the pinned version.
ensure_ollama() {
  echo "--- Ensuring CPU ollama in the VM (localhost:11434) ---"
  limactl shell "$VM_NAME" sudo OLLAMA_VERSION="${OLLAMA_VERSION:-}" bash -euo pipefail -s <<'GUEST'
have="$(command -v ollama >/dev/null 2>&1 && ollama --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true)"
if [ -n "$have" ] && { [ -z "$OLLAMA_VERSION" ] || [ "$have" = "$OLLAMA_VERSION" ]; }; then
  echo "ollama already installed (${have})"
else
  curl -fsSL https://ollama.com/install.sh >/tmp/ollama-install.sh
  env OLLAMA_VERSION="$OLLAMA_VERSION" bash /tmp/ollama-install.sh
  rm -f /tmp/ollama-install.sh
fi
# The installer registers a systemd service; make sure it is up on 127.0.0.1:11434.
systemctl enable --now ollama 2>/dev/null || true
GUEST
  ok "ollama serving on localhost:11434 in the VM"
}

# Read the host-reachable IP from the running VM and write the Trustable support
# files. Used both by the fresh-install path and when the VM already exists.
finish() {
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

  if ./ssh.sh true 2>/dev/null; then
    ok "ssh.sh reaches trustable@$IP"
  else
    warn "ssh.sh could not connect yet (key auth may need a moment)"
  fi

  ensure_guest_user
  ensure_ollama
  ensure_tls_san "$IP"
  apply_reverse_proxy "$IP"

  # Provision the in-VM toolchain (ops/go/air/uv/node/opencode + MCP servers) by
  # running setup.sh INSIDE the VM as the mirrored current user, in this repo dir
  # (Lima mounts it at the same path). Idempotent — re-runs just verify.
  echo "--- Running setup.sh in the VM as $HOST_USER ---"
  limactl shell --workdir "$MOUNT_DIR" "$VM_NAME" ./setup.sh \
    || fail "setup.sh failed in the VM"
  ok "setup.sh completed"

  echo
  echo -e "${GREEN}=== Trustable VM ready ===${NC}"
  echo "  apihost:      $APIHOST"
  echo "  host-rewrite: http://<label>.$IP.nip.io:8080  ->  <label>.miniops.me"
  echo "  ssh:          ./ssh.sh <cmd>"
  echo "  ollama:       http://localhost:11434  (CPU, in-VM)"
  echo "  mount:        $MOUNT_DIR  (owned by $HOST_USER in the VM)"
  echo "  stop:         ./start.sh -s   (keep the VM; restart with ./start.sh)"
  echo "  destroy:      ./start.sh -k"
}

# Resolve + cache the .deb for the host arch into dist/, downloading if absent.
# Sets the global DEB_FILE. The deb arch matches the HOST arch (the VM runs the
# host arch under vz): arm64 on Apple Silicon, amd64 on Intel.
ensure_deb() {
  local DEB_ARCH DL_SUFFIX TMP_DEB
  case "$(uname -m)" in
    arm64|aarch64) DEB_ARCH=arm64; DL_SUFFIX=linux-arm ;;
    x86_64|amd64)  DEB_ARCH=amd64; DL_SUFFIX=linux-amd ;;
    *) fail "unsupported host arch: $(uname -m)" ;;
  esac
  DEB_FILE="${DIST_DIR}/trustable_${TRUSTABLE_VERSION}_${DEB_ARCH}.deb"
  mkdir -p "$DIST_DIR"
  if [[ -s "$DEB_FILE" ]]; then
    ok "Using cached package: $DEB_FILE"
  else
    echo "--- Downloading Trustable package (~3.6GB) -> $DEB_FILE ---"
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

# Mirror the current macOS user into the VM: a guest account with the same name
# and UID, so files under the virtiofs mount (mounted at the same path) keep the
# host's ownership. Give them passwordless sudo and authorize the same Lima key
# so `ssh <host_user>@<ip>` works too. Idempotent — safe to run on every start.
# Skip when the host user is 'trustable' or 'root' (already present in the VM).
ensure_guest_user() {
  [ -n "$HOST_USER" ] || return 0
  case "$HOST_USER" in trustable|root) return 0 ;; esac
  echo "--- Mirroring host user '$HOST_USER' into the VM ---"
  limactl shell "$VM_NAME" sudo HOST_USER="$HOST_USER" HOST_UID="$HOST_UID" \
    bash -euo pipefail -s <<'GUEST'
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
install -d -o "$HOST_USER" -g "$HOST_USER" -m 0700 "/home/$HOST_USER/.ssh"
# Authorize the same key(s) trustable trusts, so ssh as the host user works.
if [ -f /home/trustable/.ssh/authorized_keys ]; then
  cp /home/trustable/.ssh/authorized_keys "/home/$HOST_USER/.ssh/authorized_keys"
  chown "$HOST_USER:$HOST_USER" "/home/$HOST_USER/.ssh/authorized_keys"
  chmod 0600 "/home/$HOST_USER/.ssh/authorized_keys"
fi
GUEST
  ok "guest user '$HOST_USER' ready (mount owner)"
}

# True if the trustable package is installed in the running VM.
package_installed() {
  limactl shell "$VM_NAME" dpkg -l trustable 2>/dev/null | grep -q '^ii'
}

command -v limactl >/dev/null 2>&1 || fail "limactl not found (brew install lima)"

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

[[ "$(uname -s)" == "Darwin" ]] || fail "start.sh is macOS-only (needs the Trustable support dir + vz)"

# If the VM already exists, don't re-provision — just make sure it's running and
# refresh the support files (the IP can change across restarts). Use ./start.sh -k
# first if you actually want a clean rebuild.
if limactl list --quiet 2>/dev/null | grep -qx "$VM_NAME"; then
  STATUS="$(limactl list --format '{{.Status}}' "$VM_NAME" 2>/dev/null)"
  if [[ "$STATUS" != "Running" ]]; then
    echo "--- VM '$VM_NAME' exists ($STATUS) — starting it ---"
    limactl start "$VM_NAME" || fail "failed to start existing VM '$VM_NAME'"
  else
    echo "--- VM '$VM_NAME' already running ---"
  fi
  # The VM can come back blank (e.g. a reset/reprovisioned disk drops the whole
  # install). If the package isn't there, (re)install it before finishing —
  # otherwise finish() would try to configure a k3s that doesn't exist.
  if package_installed; then
    echo "--- Trustable package present — refreshing support files ---"
  else
    warn "Trustable package missing in existing VM — reinstalling"
    ensure_deb
    install_package
  fi
  finish
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
disk: "40GiB"
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
