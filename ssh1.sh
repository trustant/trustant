#!/bin/bash

if ! test -e  ~/Library/Application\ Support/Trustable/id_ed25519
then echo "ensure you are on a Mac and you have Trustable installed" ; exit 1
fi

ID=~/Library/Application\ Support/Trustable/id_ed25519
IP="$(cat ~/Library/Application\ Support/Trustable/current.ip)"

ssh -i "$ID" -t trustable@$IP "$@" 

