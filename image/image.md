Implement an image.sh script

cd to the directory of the script at the beginning

the <image> name is ghcr.io/trustable-ai/trustable-app

if no arguments: delete all tags and generate one with date +%y.%j.%H%S
otherwise use the first argument as tag

Detects the architecture to build:
- if it is a Mac builds for arm64
- if it is windows builds for amd64
- if it is Linux uses arch to detect the arc to build for

- if it is a GITHUB_ACTION builds for both and use as action --push otherwise execute a --load

then split in 2:
- Dockerfile.base is the text before the separator '###---###'
- Dockerfile.current  accept an argument for the base, also the targetarch, builds FROM base and then use what is after the separator.

Before splitting the Dockerfile, stage the pinned `mcp` and `trustable-acp`
submodules plus the local `browser-mcp` source into the Docker context. Do not
download a floating agent runtime while building.

Calculate the <hash> of the Dockerfile.base, the pinned OpenServerless MCP and
TruACP revisions/content, and the local `browser-mcp` source tree.
Try to pull the <image>:<hash>
If the hash does not exist build the Dockerfile.base with that hash for the archs selected

Then build the Dockerfile.current passing as argument the <image>:<hash> as <image>:<tag>

The base image installs `trustable-browser-mcp` and Playwright `1.56.1` with
its Chromium runtime under `/opt/ms-playwright`. The browser package and runtime
must work on both amd64 and arm64 and are installed at image build time, never
downloaded when a user launches an app.

The base image builds TruACP for the target architecture and installs the Pi
toolchain pinned by `trustable-acp/pi.version` under the `trustable` user's
`~/.local/bin`. It does not compile or install OpenCode.
