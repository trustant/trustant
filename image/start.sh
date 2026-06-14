#!/bin/bash
export HOME=/home/trustable
export PATH="$HOME/.local/bin:$PATH"
source $HOME/.bashrc

mkdir -p /run/sshd
ssh-keygen -A

if test -n "$B64KUBECONFIG"
then
    # First `ops` run clones its tasks and prints "Cloning tasks..." to
    # stdout. Warm it up first so that chatter never lands in the kubeconfig.
    ops -t >/dev/null 2>&1 || true
    # Strip any stray pre-amble: a valid kubeconfig starts at `apiVersion:`.
    ops -base64 -d "$B64KUBECONFIG" | sed -n '/^apiVersion:/,$p' >$HOME/.ops/tmp/kubeconfig
fi

chown -R trustable:trustable "$HOME"

if [ -n "$USERID" ] && [ "$USERID" != "769" ]
then
    /usr/sbin/usermod -u $USERID trustable
    chown -Rf "$USERID" "$HOME"
fi

# start supervisor
supervisord -c /etc/supervisord.ini
