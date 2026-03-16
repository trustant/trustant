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

To test in OpenServerless

1. build a docker image with `./build.sh`
2. deploy with ops trustable start
3. play with http://trustable.miniops.me

# Publish

```
./publish.sh
```

will trigger a publish on github