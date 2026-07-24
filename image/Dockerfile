ARG OLLAMA_VERSION=0.30.6
ARG OPS_BRANCH=bestia
ARG OPS_REPO=https://github.com/nuvolaris/bestia
# WHY: VM setup reads these exact values too, so image and development installs
# cannot drift back to the upstream Milvus MCP or a floating branch.
ARG MILVUS_MCP_REPO=https://github.com/trustable-ai/mcp-server-milvus.git
ARG MILVUS_MCP_REF=a7e624f3057a0d739528bca3ed92504943224ceb

########################################################## loader
FROM ubuntu:24.04 AS loader
ARG TARGETARCH
ARG OLLAMA_VERSION
ARG OPS_BRANCH
ARG OPS_REPO

# install ollama
USER root

RUN apt-get update && apt-get install -y curl zstd

RUN curl -fsSL https://ollama.com/install.sh >ollama.sh
RUN env OLLAMA_VERSION=${OLLAMA_VERSION} bash ollama.sh

RUN \
    echo "OLLAMA_VERSION=${OLLAMA_VERSION}" >>/etc/environment;\
    echo "OPS_REPO=${OPS_REPO}" >>/etc/environment ;\
    echo "OPS_BRANCH=${OPS_BRANCH}" >>/etc/environment

########################################################## stage1-root
FROM ubuntu:24.04 AS stage1-root
ARG TARGETARCH
ARG MILVUS_MCP_REPO
ARG MILVUS_MCP_REF
USER root

# collecting
COPY --from=loader /etc/environment /etc/environment
COPY --from=loader /usr/local/bin/ollama /usr/local/bin/ollama
COPY --from=loader /usr/local/lib/ollama/*.so* /usr/local/lib/ollama/

ENV DEBIAN_FRONTEND=noninteractive
ENV PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright
RUN \
    apt update && \
    apt install -y --no-install-recommends \
        locales ca-certificates git sudo curl jq less vim lsof libatomic1 \
        tini openssh-server supervisor iputils-ping inetutils-telnet ripgrep \
        python-is-python3 python3-pip python3-pytest python3-dotenv \
        postgresql-client-16 redis-tools rclone  && \
    curl -fsSL https://deb.nodesource.com/setup_24.x | bash ;\
    apt update && apt -y install nodejs ;\
    rm -rf /var/lib/apt/lists/*

RUN \
    echo "en_US.UTF-8 UTF-8" > /etc/locale.gen && \
    locale-gen en_US.UTF-8 && \
    update-locale LANG=en_US.UTF-8 LANGUAGE=en_US.UTF-8 LC_ALL=en_US.UTF-8


# Keep the upstream Milvus CLI pinned in /usr/local/bin. WHY: app launch replaces
# ~/.local/bin/milvus_cli with a configured wrapper and must not follow a
# floating package or overwrite the package-managed implementation.
# installing lsp: ts py, mcp: postgres, redis, milvus, mongodb, cli: milvus
RUN \
    curl -LsSf https://astral.sh/uv/install.sh | \
    env UV_INSTALL_DIR="/usr/bin" INSTALLER_NO_MODIFY_PATH=1 sh ;\
    for tool in \
        milvus-cli==1.2.1 \
        'python-lsp-server[yapf]' \
        postgres-mcp==0.3.0 \
        redis-mcp-server==0.5.0 \
        "git+${MILVUS_MCP_REPO}@${MILVUS_MCP_REF}" ;\
    do env \
        UV_TOOL_BIN_DIR=/usr/local/bin \
        UV_TOOL_DIR=/opt/uv/tools \
        UV_CACHE_DIR=/opt/uv/cache \
        UV_PYTHON_PREFERENCE=only-system \
        UV_LINK_MODE=hardlink \
        uv tool install --python /usr/bin/python3 $tool ;\
    done

COPY openserverless-mcp /tmp/openserverless-mcp
COPY trustable-browser-mcp /tmp/trustable-browser-mcp
RUN \
    npm install -g typescript-language-server typescript tsx mongodb-mcp-server@1.9.0 ;\
    cd /tmp/openserverless-mcp && npm pack --pack-destination /tmp ;\
    npm install -g tsx /tmp/openserverless-mcp-*.tgz ;\
    cd /tmp/trustable-browser-mcp && npm pack --pack-destination /tmp ;\
    npm install -g /tmp/trustable-browser-mcp-*.tgz ;\
    npx --yes playwright@1.56.1 install --with-deps chromium ;\
    cd / ;\
    rm -rf /tmp/openserverless-mcp /tmp/openserverless-mcp-*.tgz /tmp/trustable-browser-mcp /tmp/trustable-browser-mcp-*.tgz ;\
    rm -rf /var/lib/apt/lists/* ;\
    npm cache clean --force ;\
    export VER=1.3.0 ARCH="$(dpkg --print-architecture)" ;\
    curl -sL https://github.com/txn2/mcp-s3/releases/download/v${VER}/mcp-s3_${VER}_linux_${ARCH}.tar.gz |\
    tar -C /usr/local/bin -xzvf -  mcp-s3

ENTRYPOINT ["tini", "--"]

# build the local part
########################################################## stage2-user
FROM stage1-root AS stage2-user
ARG TARGETARCH

# install cli tools
USER root

RUN \
    useradd -d /home/trustable -u 769 -s /bin/bash -m trustable ;\
    echo 'trustable ALL=(ALL:ALL) NOPASSWD: ALL' >>/etc/sudoers

ENV HOME=/home/trustable
WORKDIR /home/trustable
USER trustable

RUN \
    sed -e 's/^\(.*\)=\(.*\)/export \1="\2"/'  -e 's/""/"/g' >>$HOME/.bashrc /etc/environment  ;\
    echo "export PATH=\"/home/trustable/.local/bin:/home/trustable/.ops/linux-${TARGETARCH}/bin:/usr/local/bin:/usr/bin:/bin\"" >>$HOME/.bashrc ;\
    curl -fsSL n7s.co/get-ops | bash

RUN bash -ic "source $HOME/.bashrc ;  ops -t ; ops -info"

# TruACP serves the coding UI on :4096 and starts pi-acp/Pi. image.sh builds the
# self-contained JavaScript bundle and pi-acp package before Docker runs and
# stages only these four runtime inputs; source and node_modules must never
# enter an image layer. The planned issue #57 execution-policy extension is
# intentionally absent until its complete runtime contract is implemented.
COPY --chown=trustable:trustable truacp-runtime/setup.sh /tmp/truacp/setup.sh
COPY --chown=trustable:trustable truacp-runtime/pi.version /tmp/truacp/pi.version
COPY --chown=trustable:trustable truacp-runtime/pi-acp-package.tgz /tmp/truacp/pi-acp-package.tgz
COPY --chown=trustable:trustable truacp-runtime/dist-bin/truacp.cjs /tmp/truacp/dist-bin/truacp.cjs
RUN \
    cd /tmp/truacp ;\
    sh setup.sh ;\
    test -x "$HOME/.local/bin/truacp" ;\
    test -x "$HOME/.local/bin/trustable-acp" ;\
    rm -rf /tmp/truacp

# workspace
RUN \
    mkdir -p /home/trustable/workspace ;\
    echo 'test -e $HOME/workbench/current && export PWD="$HOME/workbench/$(cat workbench/current)"' >>$HOME/.bashrc

########################################################## stage3-app
###---###
# start
USER root
RUN \
    if [ -x /usr/local/bin/mcp-s3 ] && [ ! -x /usr/local/bin/mcp-s3-real ]; then \
        mv /usr/local/bin/mcp-s3 /usr/local/bin/mcp-s3-real; \
    fi
COPY mcp-s3 /usr/local/bin/mcp-s3
RUN chmod 0755 /usr/local/bin/mcp-s3
COPY bin/trustable-$TARGETARCH /usr/local/bin/trustable
COPY supervisord.ini /etc/supervisord.ini
COPY start.sh /usr/local/bin/start.sh
CMD ["/usr/local/bin/start.sh"]

COPY --chown=trustable:trustable env .env
COPY --chown=trustable:trustable trustable.json trustable.json

# start.sh / supervisord must run as root: start.sh chowns $HOME and
# supervisord (user=root) drops privilege per-program. Final user MUST be root.
USER root
WORKDIR /home/trustable
