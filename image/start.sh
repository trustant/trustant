#!/bin/bash

mkdir -p /run/sshd
ssh-keygen -A

export HOME=/home/trustable
export PATH="$HOME/.local/bin:$PATH"
source $HOME/.bashrc
ops -info

if test -n "$B64KUBECONFIG"
then ops -base64 -d "$B64KUBECONFIG"  >$HOME/.ops/tmp/kubeconfig
fi
echo "Checking kubeconfig"
ls -l ~/.ops/tmp/kubeconfig

if [ -n "$USERID" ] && [ "$USERID" != "769" ]
then /usr/sbin/usermod -u $USERID trustable
fi

chown -Rvf trustable:trustable "$HOME"

# start supervisor
supervisord -c /etc/supervisord.ini
