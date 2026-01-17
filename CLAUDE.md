# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Run Commands

```bash
# Run the server (development mode - serves from ./web folder)
go run main.go

# Build the binary (embeds web/ folder into the binary)
go build -o trustable-app main.go

# Run with hot reload (requires Air)
air
```

The server runs on port 8910.

## Architecture

This is a Go web application that serves static files from the `web/` directory.

The application implements api calls in the `/api` prefix.

Each page is a separate applications, using tailwind and vanilla javascript for the implementatino.

**Development vs Production mode:**
- If `./web` folder exists on disk, files are served from disk (development mode)
- Otherwise, files are served from the embedded filesystem compiled into the binary (production mode)

The `//go:embed web` directive embeds the entire `web/` directory into the binary at compile time.

