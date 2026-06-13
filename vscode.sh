#!/bin/bash

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
