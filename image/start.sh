#!/bin/bash
export HOME=/home/node
export PATH="$HOME/.local/bin:$PATH"
set -a
source $HOME/.env
set +a

ops -update

#setup sshd
mkdir -p /run/sshd
ssh-keygen -A

# add ssh key
if test -n "$ID_ED25519"
then
    mkdir -p $HOME/.ssh
    touch $HOME/.ssh/authorized_keys
    if ! test -e $HOME/.ssh/id_ed25519
    then printf  "%s\n" "$ID_ED25519" > $HOME/.ssh/id_ed25519
    else echo "id_ed25519 already exists"
    fi
    chmod 600 $HOME/.ssh/id_ed25519
    if ! test -e $HOME/.ssh/id_ed25519.pub
    then ssh-keygen -y -f $HOME/.ssh/id_ed25519 > $HOME/.ssh/id_ed25519.pub
    fi
    if ! grep -F "$(cat $HOME/.ssh/id_ed25519.pub)" $HOME/.ssh/authorized_keys >/dev/null
    then cat "$HOME/.ssh/id_ed25519.pub" >>$HOME/.ssh/authorized_keys
    fi
    chmod 600 $HOME/.ssh/authorized_keys $HOME/.ssh/id_ed25519  $HOME/.ssh/id_ed25519.pub
    chmod 700 $HOME/.ssh
fi

if test -n "$B64KUBECONFIG"
then ops -base64 -d "$B64KUBECONFIG" >$HOME/.ops/tmp/kubeconfig
fi

mkdir -p "$HOME/.config/opencode" "$HOME/.cache/opencode" "$HOME/.local/share/opencode"
chown -R node:node "$HOME/.ssh" "$HOME/.env" "$HOME/.ops" "$HOME/.config/opencode" "$HOME/.cache/opencode" "$HOME/.local/share/opencode"

if [ -n "$USERID" ] && [ "$USERID" != "1000" ]
then
    /usr/sbin/usermod -u $USERID node
    chown -Rf "$USERID" "$HOME"
fi

# start supervisor
supervisord -c /etc/supervisord.ini
