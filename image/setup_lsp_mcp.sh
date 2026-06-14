#!/bin/bash
export PATH="$HOME"/.ops/*-*/bin:"$PATH"
set -euo pipefail
which typescript-language-server || sudo pnpm install -g typescript-language-server typescript
which pylsp || uv tool install --python 3.12 "python-lsp-server[yapf]"
which postgres-mcp || uv tool install --python 3.12 postgres-mcp==0.3.0
which redis-mcp-server || uv tool install --python 3.12 redis-mcp-server==0.5.0
which mcp-server-milvus || uv tool install --python 3.12 git+https://github.com/zilliztech/mcp-server-milvus.git@ca21cc71f00ad61f7a79e77af7d1dc20de549dd3

if ! which mcp-s3
then
    VER=1.3.0
    SUF=""
    case "$(uname)-$(arch)" in
      (Darwin-arm64) SUF=darwin_arm64 ;;
      (Linux-aarch64) SUF=linux_arm64 ;;
      (Linux-x86_64) SUF=linux_amd64 ;;
    esac
    if test -n "$SUF"
    then curl -sL https://github.com/txn2/mcp-s3/releases/download/v${VER}/mcp-s3_${VER}_${SUF}.tar.gz | tar -C ~/.local/bin -xzvf -  mcp-s3
    else echo not found mcp-s3
    fi
fi