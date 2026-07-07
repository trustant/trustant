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

mkdir -p "$HOME/workspace/workbench"
if [ -L "$HOME/workbench" ]; then
  :
elif [ -d "$HOME/workbench" ]; then
  if find "$HOME/workbench" -mindepth 1 -maxdepth 1 | read -r _
  then
    cp -a "$HOME/workbench"/. "$HOME/workspace/workbench"/
  fi
  rm -rf "$HOME/workbench"
fi
ln -sfn "$HOME/workspace/workbench" "$HOME/workbench"

persist_opencode_dir() {
  local src="$1"
  local dst="$2"
  mkdir -p "$(dirname "$src")" "$dst"
  if [ -L "$src" ]; then
    ln -sfn "$dst" "$src"
    return
  fi
  if [ -e "$src" ]; then
    if [ -d "$src" ] && ! find "$dst" -mindepth 1 -maxdepth 1 | read -r _
    then
      cp -a "$src"/. "$dst"/
    fi
    rm -rf "$src"
  fi
  ln -sfn "$dst" "$src"
}

persist_opencode_dir "$HOME/.config/opencode" "$HOME/workspace/.trustable/opencode/config"
persist_opencode_dir "$HOME/.cache/opencode" "$HOME/workspace/.trustable/opencode/cache"
persist_opencode_dir "$HOME/.local/share/opencode" "$HOME/workspace/.trustable/opencode/share"

echo "Changing permissions to workspace, file count:"
chown -Rvf trustable:trustable "$HOME" | wc -l
echo "Showing ops -info:"
sudo -u trustable bash -c "source ~/.bashrc && ~/.local/bin/ops -info"


# start supervisor
supervisord -c /etc/supervisord.ini
