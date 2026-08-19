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

# The workbench is ephemeral scratch space and MUST NOT survive a pod restart:
# committing is the user's responsibility. Guarantee a clean, empty, real
# directory on every start. Deleting a leftover symlink matters — an older image
# pointed $HOME/workbench at the persistent volume, and that redirection would
# otherwise outlive this change. Anything already at
# $HOME/workspace/workbench is deliberately left alone; only the durable bare
# repos under $HOME/workspace/workspace/<name> are read, by the server's
# workbench restore at startup.
rm -rf "$HOME/workbench"
mkdir -p "$HOME/workbench"

echo "Changing permissions to workspace, file count:"
chown -Rvf trustable:trustable "$HOME" | wc -l
echo "Showing ops -info:"
sudo -u trustable bash -c "source ~/.bashrc && ~/.local/bin/ops -info"


# start supervisor
supervisord -c /etc/supervisord.ini
