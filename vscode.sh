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


ID=~/Library/Application\ Support/Trustant/id_ed25519
IP="$(cat ~/Library/Application\ Support/Trustant/current.ip)"
IT=~/Library/Application\ Support/Trustant/id_trustant
test -e "$IT" || ssh-keygen -t ed25519 -N "" -f "$IT"

#ssh -i "$ID" trustant@$IP  "mkdir -p ~/.ssh && chmod 0700 ~/.ssh"
#cat "$IT" | ssh -i "$ID" trustant@$IP "tee ~/.ssh/id_trustant"
#>dev/null
#ssh -i "$ID" trustant@$IP  "chmod 0600 ~/.ssh/id_trustant"
#ssh -tt -i "$ID" trustant@$IP ssh -t  -i .ssh/id_trustant trustant@localhost -p 30222

echo "Expected: Warning: AND Unable to use a TTY"
cat "$IT".pub  |  ssh -i "$ID" trustant@$IP sudo k3s kubectl -n openserverless exec -ti trustant-0 -c trustant -- tee /home/trustant/.ssh/authorized_keys

#>/dev/null

sed -i.bak -e '/^Host trustant/,/End trustant$/d' ~/.ssh/config

cat <<EOF >>~/.ssh/config
Host trustant
    Hostname localhost
    Port 30222
    User trustant
    IdentityFile "$IT"
    ProxyJump trustant-vm

Host trustant-vm
    Hostname $IP
    User trustant
    IdentityFile "$ID"
# End trustant
EOF

echo "you can now 'ssh trustant' and 'ssh trustant-vm'"
