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


ID=~/Library/Application\ Support/Trustable/id_ed25519
IP="$(cat ~/Library/Application\ Support/Trustable/current.ip)"
IT=~/Library/Application\ Support/Trustable/id_trustable
test -e "$IT" || ssh-keygen -t ed25519 -N "" -f "$IT"

#ssh -i "$ID" trustable@$IP  "mkdir -p ~/.ssh && chmod 0700 ~/.ssh"
#cat "$IT" | ssh -i "$ID" trustable@$IP "tee ~/.ssh/id_trustable"
#>dev/null
#ssh -i "$ID" trustable@$IP  "chmod 0600 ~/.ssh/id_trustable"
#ssh -tt -i "$ID" trustable@$IP ssh -t  -i .ssh/id_trustable trustable@localhost -p 30222

echo "Expected: Warning: AND Unable to use a TTY"
cat "$IT".pub  |  ssh -i "$ID" trustable@$IP sudo k3s kubectl -n nuvolaris exec -ti trustable-0 -c trustable -- tee /home/trustable/.ssh/authorized_keys

#>/dev/null

sed -i.bak -e '/^Host trustable/,/End trustable$/d' ~/.ssh/config

cat <<EOF >>~/.ssh/config
Host trustable
    Hostname localhost
    Port 30222
    User trustable
    IdentityFile "$IT"
    ProxyJump trustable-vm

Host trustable-vm
    Hostname $IP
    User trustable
    IdentityFile "$ID"
# End trustable
EOF

echo "you can now 'ssh trustable' and 'ssh trustable-vm'"
