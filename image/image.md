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

Calculate the <hash> of the Dockerfile.base
Try to pull the <image>:<hash>
If the hash does not exist build the Dockerfile.base with that hash for the archs selected

Then build the Dockerfile.current passing as argument the <image>:<hash> as <image>:<tag>
