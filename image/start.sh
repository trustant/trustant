#!/bin/bash
export HOME=/home/trustable
export PATH="$HOME/.local/bin:$PATH"
source $HOME/.bashrc

mkdir -p /run/sshd
ssh-keygen -A

if test -n "$B64KUBECONFIG"
then ops -base64 -d "$B64KUBECONFIG" >$HOME/.ops/tmp/kubeconfig
fi

chown -R trustable:trustable "$HOME"

if [ -n "$USERID" ] && [ "$USERID" != "769" ]
then
    /usr/sbin/usermod -u $USERID trustable
    chown -Rf "$USERID" "$HOME"
fi

# start supervisor
supervisord -c /etc/supervisord.ini
