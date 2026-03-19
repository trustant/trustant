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
if test -n "$SSHKEY"
then
    mkdir -p $HOME/.ssh
    touch $HOME/.ssh/authorized_keys
    if ! grep "$SSHKEY" $HOME/.ssh/authorized_keys >/dev/null
    then echo "$SSHKEY" >>$HOME/.ssh/authorized_keys
    fi
    chmod 600 $HOME/.ssh/authorized_keys
    chmod 700 $HOME/.ssh
fi

if test -n "$B64KUBECONFIG"
then ops -base64 -d "$B64KUBECONFIG" >$HOME/.ops/tmp/kubeconfig
fi

chown -R node:node $HOME/.ssh $HOME/.env

if [ -n "$USERID" ] && [ "$USERID" != "1000"]
then
    /usr/sbin/usermod -u $USERID node
    chown -Rf "$USERID" "$HOME"
fi

# start supervisor
supervisord -c /etc/supervisord.ini
