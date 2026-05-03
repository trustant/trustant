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
    apt install -y less man-db sudo vim jq python-is-python3 python3-virtualenv \
    locales postgresql-client-16 openssh-server tini supervisor ripgrep

RUN \
    rm -Rvf /var/lib/apt/lists/* /var/cache/apt/*

# setup env
RUN \
    echo "en_US.UTF-8 UTF-8" > /etc/locale.gen && \
    locale-gen en_US.UTF-8 && \
    update-locale LANG=en_US.UTF-8 LANGUAGE=en_US.UTF-8 LC_ALL=en_US.UTF-8

RUN \
    echo 'node ALL=(ALL:ALL) NOPASSWD: ALL' >>/etc/sudoers

ENV HOME=/home/node
ENV OPS_BRANCH=bestia
ENV OPS_REPO=https://github.com/nuvolaris/bestia

USER node
WORKDIR /home/node

RUN \
    echo "export OPS_BRANCH=$OPS_BRANCH" >>$HOME/.bashrc ;\
    echo "export OPS_REPO=$OPS_REPO" >>$HOME/.bashrc ;\
    curl -sL n7s.co/get-ops | bash ;\
    bash -c "source $HOME/.bashrc ; $HOME/.local/bin/ops -t"

RUN \
    curl -fsSL https://opencode.ai/install | bash ;\
    echo 'export PATH="$HOME/.opencode/bin:$PATH"' >>$HOME/.bashrc

RUN mkdir /home/node/workspace
RUN echo 'test -e $HOME/workbench/current && export PWD="$HOME/workbench/$(cat workbench/current)"' >>$HOME/.bashrc

USER root
CMD ["/usr/local/bin/start.sh"]
ENTRYPOINT ["tini", "--"]
###---###
COPY supervisord.ini /etc/supervisord.ini
COPY start.sh /usr/local/bin/start.sh
COPY env .env
COPY trustable.json trustable.json
COPY bin/trustable-$TARGETARCH /usr/local/bin/trustable
