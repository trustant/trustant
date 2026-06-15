Write a setup.sh script to setup the environment for Trustable

When I say add to the PATH, add to ~/.bashrc and on mac also on ~/.zshrc

If a check fails, abort and warn the user

0. Read from image/Dockerfile search the variable in format

ARG <VARIABLE>=<VALUE>

and set the env vars

- OLLAMA_VERSION
- OPENCODE_VERSION
- OPS_BRANCH
- OPS_REPO

1. check there is a .env and that the variables in .env.dist are set, load the .env with a bash loop
and check the $WORKSPACE_DIR exists
and the folder pointed by $WORKBENCH_DIR exists

2. check ops is in the path and the values of the env vars should be the same as OPS_BRANCH and OPS_REPO

If not, recommend to set the variables and install ops

3. Add to the path ~/.ops/<os>-<arch>/bin and check bun and uv are in path

4. if go is not in the path, installing first g:

curl -sSL https://raw.githubusercontent.com/voidint/g/master/install.sh | bash

then activate the go version in go.mod

then install air

5. if npm is not on the path, install it with fnm

```
curl -o- https://fnm.vercel.app/install | bash
fnm install 24
```

6. ensure $HOME/.local/bin is the first entry in the path and warn if not

7. check you can reach OpenWhisk

locate the <apihost>:

- first reading the env variables OPS_APIHOST/APIHOST/TRUSTABLE_DEFAULT_APIHOST if defined
- on mac, if there is the file ~/Library/Application Support/Trustable/apihost, use  the url in that file
- on Windows if there is a file %LOCALAPPDATA%/Trustable/apihost use  the url in that file
- otherwise use http://miniops.me

verify  curl -sL <apihost>/api/info | jq .description returns OpenWhisk

8. On mac, if there is the file ~/Library/Application Support/Trustable/id_ed25519,

IP="$(cat ~/Library/Application\ Support/Trustable/current.ip)"
./ssh.sh sudo cat /etc/rancher/k3s/k3s.yaml | sed -e "/server:/ s/127.0.0.1/$IP/" >~/.ops/tmp/kubeconfig

9. check you have administrative power
ensuring `ops admin listuser` does not return error

10. check you have installed in /opt/homebrew/bin the following commands
- uv
- kubefwd
- rclone
- psql
- redis-cli
- milvus_cli

11. Check if opencode is in the path and if it there check the version `opencode -v` matches with the $OPENCODE_VERSION

 it does not match, install with

```
curl -fsSL https://opencode.ai/install >opencode.sh
bash opencode.sh --version ${OPENCODE_VERSION}
mv ~/.opencode/bin/opencode ~/.local/bin
```

if missing warn and ask to install them

12. implement in the commands to install in ~/.local/bin  the mcp servers for redis, milvus, postgres and s3
using the same procedure in images/Dockerfile (do not use /opt/uv/* vars and install everything for the local user)

