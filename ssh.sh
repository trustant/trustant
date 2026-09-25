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


usage() {
  cat <<'USAGE'
Usage: ./ssh.sh [-p|-d|-i] [command...]

  -d   development access — log in as your own user ($USER) into your
       development files, starting in the current directory
  -p   production access — log in as 'trustant', with access to the
       Trustant folder
  -i   image access — enter the VM, then the running trustant container
       (sudo k3s kubectl -n openserverless exec trustant-0 -c trustant)
  -h   show this help

With no arguments at all this help is shown. Any extra arguments are run
as a command instead of an interactive shell.
USAGE
}

if test $# -eq 0
then usage ; exit 0
fi

MODE=dev
case "$1" in
  -p) MODE=prod  ; shift ;;
  -d) MODE=dev   ; shift ;;
  -i) MODE=image ; shift ;;
  -h|--help) usage ; exit 0 ;;
esac

if ! test -e  ~/Library/Application\ Support/Trustant/id_ed25519
then echo "ensure you are on a Mac and you have Trustant installed" ; exit 1
fi

ID=~/Library/Application\ Support/Trustant/id_ed25519
IP="$(cat ~/Library/Application\ Support/Trustant/current.ip)"

KUBECTL="sudo k3s kubectl -n openserverless exec -ti trustant-0 -c trustant --"

case "$MODE" in
  prod)
    if test $# -gt 0
    then ssh -i "$ID" -t trustant@$IP "$@"
    else ssh -i "$ID" -t trustant@$IP
    fi
    ;;
  image)
    if test $# -gt 0
    then ssh -i "$ID" -t trustant@$IP "$KUBECTL $*"
    else ssh -i "$ID" -t trustant@$IP "$KUBECTL bash"
    fi
    ;;
  *)
    if test $# -gt 0
    then ssh -i "$ID" -t $USER@$IP "cd '$PWD' && $*"
    else ssh -i "$ID" -t $USER@$IP "cd '$PWD' && exec bash"
    fi
    ;;
esac
