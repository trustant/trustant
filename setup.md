Write a setup.sh script to setup the environment for Trustable

When I say add to the PATH, add to ~/.bashrc and on mac also on ~/.zshrc

If a check fails, abort and warn the user

1. check there is a .env and that the variables in .env.dist are set, load the .env with a bash loop
and check the $WORKSPACE_DIR exists
and the folder pointed by $WORKBENCH_DIR exists

2. check ops is in the path and the values of the env vars should be
- OPS_REPO=https://github.com/nuvolaris/bestia
- OPS_BRANCH=bestia

If not, recommend to set the variables, then install ops with:

curl -sL n7s.co/get-ops (mac/win)
powershell -c "irm n7s.co/get-ops | iex" (win)

3. Add to the path ~/.ops/<os>-<arch>/bin and check bun and uv are in path

4. if go is not in the path, installing first g:

curl -sSL https://raw.githubusercontent.com/voidint/g/master/install.sh | bash

then activate the go version in go.mod

then install air

5. check you can reach OpenWhisk

locate the <apihost>:
- first reading the env variables OPS_APIHOST/APIHOST/TRUSTABLE_DEFAULT_APIHOST if defined
- on mac, if there is the file ~/Library/Application Support/Trustable/apihost, use  the url in that file
- on Windows if there is a file %APPDATA%/Trustable/apihohst use  the url in that file
- otherwise use http://miniops.me

verify  curl -sL <apihost>/api/info | jq .description returns OpenWhisk

6. On mac, if there is the file ~/Library/Application Support/Trustable/id_ed25519,

IP="$(cat ~/Library/Application\ Support/Trustable/current.ip)"
./ssh.sh sudo cat /etc/rancher/k3s/k3s.yaml | sed -e "/server:/ s/127.0.0.1/$IP/" >~/.ops/tmp/kubeconfig

7. check you have administrative power
ensuring `ops admin listuser` does not return error

8. if it is not there, install opencode in ~/.opencode/bin with `curl -fsSL https://opencode.ai/install | bash`

9. Check you have a local ollama running with http://localhost:11434 returning Ollama is Running

10. preload the ollama image if it is not yet in the vm:

if ! ./ssh.sh sudo k3s images list | grep ollama
then
    OLLAMA=$(jq .config.images.ollama -r  olaris-bestia/opsroot.json)
    docker pull $OLLAMA
fi