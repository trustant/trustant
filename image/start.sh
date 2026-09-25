#!/bin/bash
# Copyright 2025-2026 Nuvolaris Inc
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU Affero General Public License as published
# by the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU Affero General Public License for more details.
#
# You should have received a copy of the GNU Affero General Public License
# along with this program.  If not, see <https://www.gnu.org/licenses/>.


mkdir -p /run/sshd
ssh-keygen -A

export HOME=/home/trustant
export PATH="$HOME/.local/bin:$PATH"
source $HOME/.bashrc

if test -n "$B64KUBECONFIG"
then echo "$B64KUBECONFIG" | base64 -d -w0 >$HOME/.ops/tmp/kubeconfig
fi

if [ -n "$USERID" ] && [ "$USERID" != "769" ]
then /usr/sbin/usermod -u $USERID trustant
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
# We are root here, so the directory just created is root-owned, while the
# server runs as trustant and clones into it on launch. The background chown
# below deliberately covers only $HOME/workspace (the hostPath volume), so it
# never reaches this path. Non-recursive is sufficient — the rm -rf above
# guarantees the directory is empty — and it runs synchronously because the
# server may clone into it as soon as supervisord starts.
chown trustant:trustant "$HOME/workbench"

# Only $HOME/workspace is a mounted hostPath volume whose ownership can
# actually be wrong (oplugins-truinst/trustable/sts.yaml). Everything else in the
# container is image content, already owned correctly by the Dockerfile's
# COPY --chown and its USER/WORKDIR setup, so chowning bare $HOME walked
# ~/.local, ~/.ops, ~/.cache and baked-in node_modules for nothing.
#
# Run it in the background so supervisord starts immediately, and report
# progress through a lock file the splash screen polls via /api/initstatus.
INIT_LOCK="$HOME/workspace/.trustant/init.lock"
mkdir -p "$(dirname "$INIT_LOCK")"
echo 0 > "$INIT_LOCK"
# We are root here but the server reads this as trustant. Make the lock
# readable at creation time rather than leaving it to the background chown to
# reach: an existing-but-unreadable lock is indistinguishable from a stale one
# and would hang the splash.
chown trustant:trustant "$(dirname "$INIT_LOCK")" "$INIT_LOCK"
chmod 755 "$(dirname "$INIT_LOCK")"
chmod 644 "$INIT_LOCK"

echo "Changing permissions to workspace in background, lock: $INIT_LOCK"
(
  # The trap is the failure-path guarantee: an aborted or failing chown must
  # never strand the lock and leave the splash waiting forever.
  trap 'rm -f "$INIT_LOCK"' EXIT
  chown -Rvf trustant:trustant "$HOME/workspace" | {
    n=0
    while IFS= read -r _; do
      n=$((n + 1))
      # Throttled: one write per file would hammer the volume with thousands
      # of tiny writes and become the bottleneck itself.
      if [ $((n % 500)) -eq 0 ]; then echo "$n" > "$INIT_LOCK"; fi
    done
    # Written inside the pipeline's subshell, which is the only scope where n
    # is visible; otherwise the last partial batch is lost.
    echo "$n" > "$INIT_LOCK"
  }
  # Normal-path removal at the end of the init loop; the trap above only
  # covers the error and signal paths.
  rm -f "$INIT_LOCK"
) &
echo "Showing ops -info:"
sudo -u trustant bash -c "source ~/.bashrc && ~/.local/bin/ops -info"


# start supervisor
supervisord -c /etc/supervisord.ini
