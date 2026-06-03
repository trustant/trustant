FROM golang:1.25 AS mcp-s3-builder

ENV PATH="/usr/local/go/bin:${PATH}"
RUN GOBIN=/out go install github.com/txn2/mcp-s3/cmd/mcp-s3@v1.1.0

FROM node:24 AS builder
USER root
RUN apt-get update && apt-get install -y zstd
RUN curl -sL https://ollama.com/install.sh | bash

FROM node:24
ARG TARGETARCH
USER root
COPY --from=builder /usr/local/bin/ollama /usr/local/bin/ollama
COPY --from=builder /usr/local/lib/ollama/*.so* /usr/local/lib/ollama/

# Install basic development tools
RUN \
    echo "deb http://apt.postgresql.org/pub/repos/apt bookworm-pgdg main" > /etc/apt/sources.list.d/pgdg.list && \
    wget --quiet -O - https://www.postgresql.org/media/keys/ACCC4CF8.asc |  apt-key add - && \
    apt update && \
    apt install -y less man-db sudo vim jq curl ca-certificates git python-is-python3 python3-pip python3-virtualenv \
    locales postgresql-client-16 openssh-server tini supervisor ripgrep

RUN \
    npm install -g typescript-language-server typescript && \
    python3 -m pip install --break-system-packages --no-cache-dir python-lsp-server && \
    rm -Rvf /var/lib/apt/lists/* /var/cache/apt/*

ENV UV_TOOL_DIR=/opt/uv/tools
ENV UV_TOOL_BIN_DIR=/usr/local/bin
ENV UV_PYTHON_INSTALL_DIR=/opt/uv/python

COPY --from=mcp-s3-builder /out/mcp-s3 /usr/local/bin/mcp-s3

# setup env
RUN \
    echo "en_US.UTF-8 UTF-8" > /etc/locale.gen && \
    locale-gen en_US.UTF-8 && \
    update-locale LANG=en_US.UTF-8 LANGUAGE=en_US.UTF-8 LC_ALL=en_US.UTF-8

RUN \
    useradd -d /home/trustable -u 769 -s /bin/bash -m trustable ;\
    echo 'trustable ALL=(ALL:ALL) NOPASSWD: ALL' >>/etc/sudoers

ENV HOME=/home/trustable
ENV OPS_BRANCH=bestia
ENV OPS_REPO=https://github.com/nuvolaris/bestia

USER trustable
WORKDIR /home/trustable

RUN \
    echo "export OPS_BRANCH=$OPS_BRANCH" >>$HOME/.bashrc ;\
    echo "export OPS_REPO=$OPS_REPO" >>$HOME/.bashrc ;\
    curl -sL n7s.co/get-ops | bash ;\
    bash -c "source $HOME/.bashrc ; $HOME/.local/bin/ops -t"

ENV PATH="/home/trustable/.ops/linux-${TARGETARCH}/bin:${PATH}"

USER root
RUN \
    HOME=/root uv python install 3.12 && \
    HOME=/root uv tool install --python 3.12 postgres-mcp==0.3.0 && \
    HOME=/root uv tool install redis-mcp-server==0.5.0 && \
    HOME=/root uv tool install git+https://github.com/zilliztech/mcp-server-milvus.git@ca21cc71f00ad61f7a79e77af7d1dc20de549dd3

USER trustable
RUN \
    curl -fsSL https://opencode.ai/install | bash ;\
    echo 'export PATH="$HOME/.opencode/bin:$PATH"' >>$HOME/.bashrc

RUN mkdir /home/trustable/workspace
RUN echo 'test -e $HOME/workbench/current && export PWD="$HOME/workbench/$(cat workbench/current)"' >>$HOME/.bashrc

USER root
CMD ["/usr/local/bin/start.sh"]
ENTRYPOINT ["tini", "--"]
###---###
COPY supervisord.ini /etc/supervisord.ini
COPY start.sh /usr/local/bin/start.sh
COPY mcp/trustable-mcp-postgres /usr/local/bin/trustable-mcp-postgres
COPY mcp/trustable-mcp-redis /usr/local/bin/trustable-mcp-redis
COPY mcp/trustable-mcp-milvus /usr/local/bin/trustable-mcp-milvus
COPY mcp/trustable-mcp-s3 /usr/local/bin/trustable-mcp-s3
RUN chmod +x /usr/local/bin/trustable-mcp-*
COPY env .env
COPY trustable.json trustable.json
COPY bin/trustable-$TARGETARCH /usr/local/bin/trustable
