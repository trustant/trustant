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
OPENAI_BASE_URL=http://localhost:11434/v1
OPENAI_API_KEY=<your api key>
OLLAMA_ENDPOINT=http://localhost:11434
OPENCODE_MODEL=qwen3-coder:480b-cloud
OPENCODE_SMALL_MODEL=qwen3:1.7b
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
It will build an image, deploy and open the application locally

# Publish

`git push --tags`
will trigger a build of an image
once the build is complete

`cd ollama-trustable ; git push origin main --tags`
will publish the references to the image