Write a setup.sh script to setup the environment for Trustable

When I say add to the PATH, add to ~/.bashrc and on mac also on ~/.zshrc

If a check fails, abort and warn the user

1. check there is a .env and that the variables in .env.dist are set, then load them with `source ./.env`
and check the $WORKSPACE_DIR exists

2. check you can reach OpenWhisk verifying curl ${OPS_APIHOST:-http://miniops.me}/api/info | jq .description returns OpenWhisk
   and the folder pointed by $WORKSPACE exists

3. check you have administrative power  ensuring `ops admin listuser` does not return error

4. check ops is in the path and the values of the env vars should be
- OPS_REPO=https://github.com/nuvolaris/bestia
- OPS_BRANCH=bestia

5. check that you can list the models on the OpenAI base url with OpenAI api and also you can list the models at the $OLLAMA_ENDPOINT/api/tags

6. Add to the path ~/.ops/<os>-<arch>/bin and check bun and uv is in path

7. if it is not there, install opencode in ~/.opencode/bin with `curl -fsSL https://opencode.ai/install | bash`

8. if go is not in the path installing first g;

curl -sSL https://raw.githubusercontent.com/voidint/g/master/install.sh | bash
and then activating the go version in go.mod
and then install air


