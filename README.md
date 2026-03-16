# trustable-app

A Lovable-like development environment based on OpenServerless.

## Requirements

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

Ensure `$WORKSPACE_DIR` exists.

### OpenServerless (ops)

Install `ops` and ensure the following env vars are set:

```
OPS_REPO=https://github.com/nuvolaris/bestia
OPS_BRANCH=bestia
```

Verify OpenWhisk is reachable:

```bash
curl http://miniops.me/api/info | jq .description
# should return "OpenWhisk"
```

Verify you have admin access:

```bash
ops admin listuser
```

### Ollama

An Ollama instance must be running at `$OLLAMA_ENDPOINT`. Verify:

```bash
curl $OLLAMA_ENDPOINT
# should return "Ollama is running"
```

Models listed in `model.lst` will be pulled automatically on startup.

### bun

Add `~/.ops/<os>-<arch>/bin` to your PATH. Bun must be available.

### opencode

If not already installed:

```bash
curl -fsSL https://opencode.ai/install | bash
```

Add `~/.opencode/bin` to your PATH.

### Go

Go 1.25.5+ is required (see `go.mod`). If not installed, use [g](https://github.com/voidint/g):

```bash
curl -sSL https://raw.githubusercontent.com/voidint/g/master/install.sh | bash
g install 1.25.5
```

### air (live reload)

Install air for development:

```bash
go install github.com/air-verse/air@latest
```

## Setup

Run the setup script to verify and configure all prerequisites:

```bash
./setup.sh
```

This checks all requirements, installs missing tools, and configures your shell PATH.

## Development

Start the app with live reload:

```bash
./run.sh
```

This uses [air](https://github.com/air-verse/air) with the config in `.air.toml` to watch for Go file changes, rebuild, and restart the server on port 8910.

## Build

Build cross-platform binaries and Docker image:

```bash
# Build binaries for linux/arm64 and linux/amd64
GOOS=linux GOARCH=arm64 go build -o trustable-app-linux-arm64 .
GOOS=linux GOARCH=amd64 go build -o trustable-app-linux-amd64 .

# Build and tag Docker image
docker build -t ghcr.io/trustable-ai/trustable-app:$(date +%Y.%m%d.%H%M) .
```

The Docker image is published to `ghcr.io/trustable-ai/trustable-app` with timestamp tags in `YYYY.MMDD.HHMM` format.
