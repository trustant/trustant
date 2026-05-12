# trustable-app

A Lovable-like development environment based on OpenServerless.

## Prerequisites

You need Docker and a running openserverless already installed:

Prereqs with this

```
curl -sL n7s.co/get-ops | bash
source ~/.bashrc
ops setup mini
```

## Development

### Environment variables

Copy `.env.dist` to `.env` and fill in the values:

```
WORKSPACE_DIR=$HOME/.ops-workspace
WORKBENCH_DIR=./workbench
OPENAI_BASE_URL=http://localhost:11434/v1
OPENAI_API_KEY=<your api key>
OLLAMA_ENDPOINT=http://localhost:11434
```

Ensure `$WORKSPACE_DIR` exists (it should if you installed openserverless)

### Setup

Run the setup script to verify and configure the development environment

```bash
./setup.sh
```

This checks all requirements, installs missing tools, and configures your shell PATH.

## Development

Start the app with live reload:

```bash
./run.sh
```

## Test

Execute `./build.sh` to build the single image, import it into the running local Trustable VM, and update the image reference in `olaris-bestia/opsroot.json`.

# Publish

Execute `./publish.sh` to push the tag, watch the GitHub Actions build, and push the `olaris-bestia` submodule (which is what ships the new version to the deployment plugin).

# Deploying

The file trustable-install.md describes the installation.

