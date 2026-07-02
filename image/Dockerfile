ARG OLLAMA_VERSION=0.30.6
ARG OPENCODE_VERSION=1.17.13
ARG OPS_BRANCH=bestia
ARG OPS_REPO=https://github.com/nuvolaris/bestia

########################################################## loader
FROM ubuntu:24.04 AS loader
ARG TARGETARCH
ARG OLLAMA_VERSION
ARG OPENCODE_VERSION
ARG OPS_BRANCH
ARG OPS_REPO

# install ollama and opencode
USER root

RUN apt-get update && apt-get install -y curl zstd

RUN curl -fsSL https://ollama.com/install.sh >ollama.sh
RUN env OLLAMA_VERSION=${OLLAMA_VERSION} bash ollama.sh

RUN curl -fsSL https://opencode.ai/install >opencode.sh
RUN bash opencode.sh --version ${OPENCODE_VERSION}

RUN \
    echo "OLLAMA_VERSION=${OLLAMA_VERSION}" >>/etc/environment;\
    echo "OPENCODE_VERSION=${OPENCODE_VERSION}" >>/etc/environment ;\
    echo "OPS_REPO=${OPS_REPO}" >>/etc/environment ;\
    echo "OPS_BRANCH=${OPS_BRANCH}" >>/etc/environment

########################################################## stage1-root
FROM ubuntu:24.04 AS stage1-root
ARG TARGETARCH
USER root

# collecting
COPY --from=loader /etc/environment /etc/environment
COPY --from=loader /usr/local/bin/ollama /usr/local/bin/ollama
COPY --from=loader /usr/local/lib/ollama/*.so* /usr/local/lib/ollama/
COPY --from=loader /root/.opencode/bin/opencode /usr/local/bin/opencode

ENV DEBIAN_FRONTEND=noninteractive
RUN \
    apt update && \
    apt install -y --no-install-recommends \
        locales ca-certificates git sudo curl jq less vim libatomic1 \
        tini openssh-server supervisor iputils-ping inetutils-telnet ripgrep \
        python-is-python3  python3-pip \
        postgresql-client-16 redis-tools rclone  && \
    curl -fsSL https://deb.nodesource.com/setup_24.x | bash ;\
    apt update && apt -y install nodejs ;\
    rm -rf /var/lib/apt/lists/*

RUN \
    echo "en_US.UTF-8 UTF-8" > /etc/locale.gen && \
    locale-gen en_US.UTF-8 && \
    update-locale LANG=en_US.UTF-8 LANGUAGE=en_US.UTF-8 LC_ALL=en_US.UTF-8


# installing lsp: ts py, mcp: postgres, redis, milvus, cli: milvus
RUN \
    curl -LsSf https://astral.sh/uv/install.sh | \
    env UV_INSTALL_DIR="/usr/bin" INSTALLER_NO_MODIFY_PATH=1 sh ;\
    for tool in \
        milvus-cli \
        'python-lsp-server[yapf]' \
        postgres-mcp==0.3.0 \
        redis-mcp-server==0.5.0 \
        git+https://github.com/zilliztech/mcp-server-milvus.git@ca21cc71f00ad61f7a79e77af7d1dc20de549dd3 ;\
    do env \
        UV_TOOL_BIN_DIR=/usr/local/bin \
        UV_TOOL_DIR=/opt/uv/tools \
        UV_CACHE_DIR=/opt/uv/cache \
        UV_PYTHON_PREFERENCE=only-system \
        UV_LINK_MODE=hardlink \
        uv tool install --python /usr/bin/python3 $tool ;\
    done

RUN \
    npm install -g typescript-language-server typescript tsx ;\
    npm install -g tsx github:apache/openserverless-mcp ;\
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

# opencode
RUN \
    mkdir -p "$HOME/.config/opencode" ;\
    cd "$HOME/.config/opencode" ;\
    npm init -y >/dev/null ;\
    npm install  "@opencode-ai/plugin@$(/usr/local/bin/opencode --version)" ;\
    test -d "$HOME/.config/opencode/node_modules/@opencode-ai/plugin"

# workspace
RUN \
    mkdir -p /home/trustable/workspace ;\
    echo 'test -e $HOME/workbench/current && export PWD="$HOME/workbench/$(cat workbench/current)"' >>$HOME/.bashrc

########################################################## stage3-app
###---###
# start
USER root
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
