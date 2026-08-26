#!/bin/bash
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

#
# proxy.sh — install and configure nginx as a host-rewriting reverse proxy.
#
# Debian/Ubuntu only. Installs nginx with apt-get and drops a single site that
# listens on 8911, accepts any <host>.<domain> name, and forwards to the app on
# 127.0.0.1 with the Host header rewritten to <host>.miniops.me.
#
# Why the rewrite: middleware.go routes by the FIRST label of the hostname
# (trustable./opencode./vite.) and rejects everything else with a 400. This lets
# the app be reached under any domain (a LAN IP's nip.io name, a public DNS
# name, a tunnel) while the backend keeps seeing the canonical miniops.me form
# it expects. Only the first label is preserved; the rest of the name is
# replaced by miniops.me.
#
set -euo pipefail

LISTEN_PORT="${LISTEN_PORT:-8911}"
UPSTREAM_HOST="${UPSTREAM_HOST:-127.0.0.1}"
UPSTREAM_PORT="${UPSTREAM_PORT:-80}"
TARGET_DOMAIN="${TARGET_DOMAIN:-miniops.me}"
SITE_NAME="trustable-proxy"

if [[ $EUID -ne 0 ]]; then
    if command -v sudo >/dev/null 2>&1; then
        SUDO="sudo"
    else
        echo "proxy.sh: must run as root or have sudo available" >&2
        exit 1
    fi
else
    SUDO=""
fi

if [[ ! -f /etc/debian_version ]]; then
    echo "proxy.sh: this script only supports Debian/Ubuntu (apt-get)" >&2
    exit 1
fi

echo "==> Installing nginx"
export DEBIAN_FRONTEND=noninteractive
$SUDO apt-get update -qq
$SUDO apt-get install -y -qq nginx

echo "==> Writing /etc/nginx/sites-available/$SITE_NAME"
# The map extracts the first label of the requested host, stripping any :port
# the client may have sent. A request with no dot (bare "localhost") yields an
# empty label, which we fall back to "trustable" for so the app still answers.
$SUDO tee "/etc/nginx/sites-available/$SITE_NAME" >/dev/null <<EOF
# Managed by proxy.sh — regenerated on every run, do not edit by hand.

map \$host \$trustable_label {
    default                 "trustable";
    "~^(?<label>[^.:]+)\\."  \$label;
}

server {
    listen ${LISTEN_PORT};
    listen [::]:${LISTEN_PORT};

    server_name _;

    # Uploads (repo zips) and AI responses are large and slow.
    client_max_body_size 0;
    proxy_read_timeout   3600s;
    proxy_send_timeout   3600s;

    location / {
        proxy_pass http://${UPSTREAM_HOST}:${UPSTREAM_PORT};

        # The whole point: <host>.<anything> becomes <host>.${TARGET_DOMAIN}.
        proxy_set_header Host              \$trustable_label.${TARGET_DOMAIN};
        proxy_set_header X-Real-IP         \$remote_addr;
        proxy_set_header X-Forwarded-For   \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_set_header X-Forwarded-Host  \$host;

        # WebSockets: the terminal (/api/terminal) and Vite HMR both need this.
        proxy_http_version 1.1;
        proxy_set_header Upgrade    \$http_upgrade;
        proxy_set_header Connection \$connection_upgrade;

        proxy_buffering off;
        proxy_redirect  off;
    }
}
EOF

# nginx only defines \$connection_upgrade if we map it; keep it in conf.d so it
# lives in the http{} context alongside the default site includes.
$SUDO tee /etc/nginx/conf.d/trustable-upgrade.conf >/dev/null <<'EOF'
# Managed by proxy.sh — regenerated on every run, do not edit by hand.
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}
EOF

$SUDO ln -sfn "/etc/nginx/sites-available/$SITE_NAME" "/etc/nginx/sites-enabled/$SITE_NAME"

echo "==> Testing configuration"
$SUDO nginx -t

echo "==> Reloading nginx"
if $SUDO systemctl is-active --quiet nginx 2>/dev/null; then
    $SUDO systemctl reload nginx
else
    $SUDO systemctl enable --now nginx 2>/dev/null || $SUDO nginx
fi

echo
echo "Proxy ready on port ${LISTEN_PORT}."
echo "  <host>.<any-domain>:${LISTEN_PORT}  ->  ${UPSTREAM_HOST}:${UPSTREAM_PORT}  (Host: <host>.${TARGET_DOMAIN})"
echo
echo "Try:  curl -s -o /dev/null -w '%{http_code}\\n' -H 'Host: trustable.example.com' http://127.0.0.1:${LISTEN_PORT}/api/version"
