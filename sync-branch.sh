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

me="$(realpath $0)"
if test -z "$1"
then
    cd "$(dirname $0)"
    git submodule --quiet foreach --recursive "$me \$toplevel/.gitmodules \$name" 
    #>_submodules
    #cat _submodules
else 
    #echo "*** $1 - $2 ***"
    #pwd
    BRANCH=$(git config -f "$1" "submodule.$2.branch")
    if test -z "$BRANCH"
    then echo "??? $(pwd) no branch: $2"
    else 
        CUR=$(git rev-parse --abbrev-ref HEAD)
        if test "$CUR" = "HEAD"
        then git checkout "origin/$BRANCH" -B "$BRANCH"
        else 
	     if test "$CUR" = "$BRANCH"
	     then echo "ok: $2@$CUR "
	     else echo "!!! $2: found: $CUR expected: $BRANCH"
             fi 
        fi
    fi
fi