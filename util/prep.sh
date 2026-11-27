# Copyright 2025-2026 Nuvolaris Inc
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU Affero General Public License as published
# by the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU Affero General Public License for more details.
#
# You should have received a copy of the GNU Affero General Public License
# along with this program.  If not, see <https://www.gnu.org/licenses/>.


# prep.sh — copy the Trustant VM's k3s kubeconfig to ~/.ops/tmp/kubeconfig on
# the macOS host, so host-side kubectl/ops reach the VM's cluster.
#
# k3s writes `server: https://127.0.0.1:6443`, which from the host is the Mac
# itself, so the server is rewritten to the VM IP (start.sh makes sure the k3s
# API certificate covers that IP).
set -euo pipefail

# Always run from the repo root, wherever this is invoked from.
cd "$(dirname "$0")/.."

DIR=~/Library/Application\ Support/Trustant
ID="$DIR/id_ed25519"
test -e "$ID" || { echo "prep.sh: $ID not found — run ./start.sh first (macOS host only)" >&2; exit 1; }
IP="$(cat "$DIR/current.ip")"
OUT=~/.ops/tmp/kubeconfig

mkdir -p "$(dirname "$OUT")"
ssh -i "$ID" "trustant@$IP" sudo cat /etc/rancher/k3s/k3s.yaml </dev/null \
  | sed "s#https://127\.0\.0\.1:6443#https://$IP:6443#" > "$OUT.tmp"
grep -q "https://$IP:6443" "$OUT.tmp" || { rm -f "$OUT.tmp"; echo "prep.sh: no k3s server URL in the fetched kubeconfig" >&2; exit 1; }
chmod 600 "$OUT.tmp"
mv "$OUT.tmp" "$OUT"
echo "kubeconfig for $IP written to $OUT"
