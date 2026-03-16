FROM node:24

# Install basic development tools
RUN \
    echo "deb http://apt.postgresql.org/pub/repos/apt bookworm-pgdg main" > /etc/apt/sources.list.d/pgdg.list && \
    wget --quiet -O - https://www.postgresql.org/media/keys/ACCC4CF8.asc |  apt-key add - && \
    apt update && \
    apt install -y less man-db sudo vim jq python-is-python3 python3-virtualenv \
    locales postgresql-client-16 openssh-server tini supervisor

# setup env
RUN \
    echo "en_US.UTF-8 UTF-8" > /etc/locale.gen && \
    locale-gen en_US.UTF-8 && \
    update-locale ANG=en_US.UTF-8 LANGUAGE=en_US.UTF-8 LC_ALL=en_US.UTF-8

RUN echo 'node ALL=(ALL:ALL) NOPASSWD: ALL' >>/etc/sudoers

COPY trustable-$TARGETARCH /usr/bin/trustable

ENV HOME=/home/node
ENV OPS_BRANCH=bestia
ENV OPS_REPO=https://github.com/nuvolaris/bestia

USER node
RUN \
    echo "OPS_BRANCH=$OPS_BRANCH" >>$HOME/.bashrc ;\
    echo "OPS_REPO=$OPS_REPO" >>$HOME/.bashrc
    curl -sL https://raw.githubusercontent.com/apache/openserverless-cli/refs/heads/main/install.sh | bash ;\
    source ~/.bashrc ; ops -t ;\
    echo 'PATH="$HOME/.ops/linux-$(dpkg --print-architecture)/bin:$PATH"' >>$HOME/.bashrc

RUN \
    curl -fsSL https://opencode.ai/install | bash ;\
    echo 'PATH="$HOME/.opencode/bin:$PATH"' >>$HOME/.bashrc

RUN mkdir /home/node/workspace
WORKDIR /home/node/workspace

USER root
ADD supervisord.ini /etc/supervisord.ini
ADD start.sh /usr/local/bin/start.sh
ENTRYPOINT ["tini", "--"]
CMD ["/usr/local/bin/start.sh"]
