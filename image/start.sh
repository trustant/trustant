#!/bin/bash

mkdir -p /run/sshd
ssh-keygen -A

export HOME=/home/trustable
export PATH="$HOME/.local/bin:$PATH"
source $HOME/.bashrc

if test -n "$B64KUBECONFIG"
then echo "$B64KUBECONFIG" | base64 -d -w0 >$HOME/.ops/tmp/kubeconfig
fi

if [ -n "$USERID" ] && [ "$USERID" != "769" ]
then /usr/sbin/usermod -u $USERID trustable
fi

chown -Rvf trustable:trustable "$HOME"

# start supervisor
supervisord -c /etc/supervisord.ini
