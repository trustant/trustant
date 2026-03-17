#!/bin/bash
export HOME=/home/node

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

echo OPENCODE_MODEL="${OPENCODE_MODEL:-qwen3-coder:480b-cloud}" >>$HOME/.env
echo OPENCODE_SMALL_MODEL="${OPENCODE_SMALL_MODEL:-qwen3:1.7b}" >>$HOME/.env

chown -R node:node $HOME/.ssh $HOME/.env

if [ -n "$USERID" ] && [ "$USERID" != "1000"]
then
    /usr/sbin/usermod -u $USERID node
    chown -Rf "$USERID" "$HOME"
fi

# start supervisor
supervisord -c /etc/supervisord.ini
