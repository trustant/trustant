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

echo "Changing permissions to workspace, file count:"
chown -Rvf trustable:trustable "$HOME" | wc -l
echo "Showing ops -info:"
sudo -u trustable bash -c "source ~/.bashrc && ~/.local/bin/ops -info"


# start supervisor
supervisord -c /etc/supervisord.ini
