ARG OLLAMA_VERSION=0.30.6
ARG OPENCODE_VERSION=1.16.2
ARG PNPM_VERSION=11.5.2
ARG NODE_VERSION=24.16.0
ARG OPS_BRANCH=bestia
ARG OPS_REPO=https://github.com/nuvolaris/bestia

########################################################## loader
FROM ubuntu:24.04 AS loader
ARG TARGETARCH
ARG OLLAMA_VERSION
ARG OPENCODE_VERSION

# install ollama and opencode
USER root

RUN apt-get update && apt-get install -y curl zstd
RUN curl -fsSL https://ollama.com/install.sh >ollama.sh

RUN env OLLAMA_VERSION=${OLLAMA_VERSION} bash ollama.sh

RUN curl -fsSL https://opencode.ai/install >opencode.sh
RUN bash opencode.sh --version ${OPENCODE_VERSION}

########################################################## stage1-root
FROM ubuntu:24.04 AS stage1-root
ARG TARGETARCH
ARG PNPM_VERSION
ARG NODE_VERSION
ARG OLLAMA_VERSION
ARG OPENCODE_VERSION

USER root
ENV DEBIAN_FRONTEND=noninteractive
ENV PNPM_HOME=/usr/local

RUN \
    echo "OLLAMA_VERSION=${OLLAMA_VERSION}" >>/etc/environment;\
    echo "OPENCODE_VERSION=${OPENCODE_VERSION}" >>/etc/environment;\
    echo "PNPM_HOME=/usr/local" >>/etc/environment

RUN \
    apt-get update && \
    apt-get install -y \
        locales ca-certificates git sudo curl jq less vim libatomic1 \
        tini openssh-server supervisor iputils-ping inetutils-telnet ripgrep \
        python-is-python3  python3-pip  python3-virtualenv pipx \
        postgresql-client-16 redis-tools rclone ffmpeg && \
    env PIPX_BIN_DIR=/usr/bin pipx install milvus-cli

RUN \
    echo "en_US.UTF-8 UTF-8" > /etc/locale.gen && \
    locale-gen en_US.UTF-8 && \
    update-locale LANG=en_US.UTF-8 LANGUAGE=en_US.UTF-8 LC_ALL=en_US.UTF-8

RUN \
    curl -fsSL https://get.pnpm.io/install.sh |\
    env PNPM_VERSION=${PNPM_VERSION} bash ;\
    pnpm runtime set node ${NODE_VERSION} -g

COPY --from=loader /usr/local/bin/ollama /usr/local/bin/ollama
COPY --from=loader /usr/local/lib/ollama/*.so* /usr/local/lib/ollama/


# build the local part
########################################################## stage2-user
FROM stage1-root AS stage2-user
ARG TARGETARCH
ARG OLLAMA_VERSION
ARG OPENCODE_VERSION
ARG PNPM_VERSION
ARG NODE_VERSION
ARG OPS_BRANCH
ARG OPS_REPO

# install cli tools
USER root
ENV PNPM_HOME=/usr/local

RUN \
    useradd -d /home/trustable -u 769 -s /bin/bash -m trustable ;\
    echo 'trustable ALL=(ALL:ALL) NOPASSWD: ALL' >>/etc/sudoers

USER trustable
WORKDIR /home/trustable

RUN \
    echo "source /etc/environment" >>$HOME/.bashrc ;\
    echo "export OPS_BRANCH=${OPS_BRANCH}" >>$HOME/.bashrc ;\
    echo "export OPS_REPO=${OPS_REPO}" >>$HOME/.bashrc ;\
    source $HOME/.bashrc ; curl -sL n7s.co/get-ops | bash ;\
    bash -c "source $HOME/.bashrc ; $HOME/.local/bin/ops -t"

COPY --from=loader /root/.opencode/bin/opencode /home/trustable/.local/bin/opencode

ENV PATH="/home/trustable/.local/bin:/home/trustable/.ops/linux-${TARGETARCH}/bin:${PATH}"

COPY setup_lsp_mcp.sh .local/bin/setup_lsp_mcp.sh
RUN bash .local/bin/setup_lsp_mcp.sh

RUN \
    mkdir -p "$HOME/.config/opencode" ;\
    cd "$HOME/.config/opencode" ;\
    pnpm init -y >/dev/null ;\
    pnpm install  "@opencode-ai/plugin@$($HOME/.local/bin/opencode --version)" ;\
    test -d "$HOME/.config/opencode/node_modules/@opencode-ai/plugin"

RUN \
    mkdir -p /home/trustable/workspace ;\
    echo 'test -e $HOME/workbench/current && export PWD="$HOME/workbench/$(cat workbench/current)"' >>$HOME/.bashrc

USER root
CMD ["/usr/local/bin/start.sh"]
ENTRYPOINT ["tini", "--"]

########################################################## stage3-app
###---###
USER root
COPY supervisord.ini /etc/supervisord.ini
COPY start.sh /usr/local/bin/start.sh
USER trustable
WORKDIR /home/trustable
COPY env .env
COPY bin/trustable-$TARGETARCH /usr/local/bin/trustable
COPY trustable.json trustable.json
