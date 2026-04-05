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

Execute `./build.sh`
It will build a development image `trustabledev`, deploy it and open the application locally

Execute `./build.sh trustable` to build the production image

# Publish

Execute `./publish.sh` to publish the image, building it on github actions and then updating the plugin

# Deploying

The file trustable-install.md describe the installation.

By default it installs the production image.

Use ops trustable redeploy KEY=trustabledev
to use the development image.

