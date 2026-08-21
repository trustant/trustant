ARG OLLAMA_VERSION=0.32.14
ARG OPS_BRANCH=0.1.0
ARG OPS_REPO=https://github.com/nuvolaris/bestia
# WHY: VM setup reads these exact values too, so image and development installs
# cannot drift back to the upstream Milvus MCP or a floating branch.
ARG MILVUS_MCP_REPO=https://github.com/trustable-ai/mcp-server-milvus.git
ARG MILVUS_MCP_REF=a7e624f3057a0d739528bca3ed92504943224ceb
ARG GH_VERSION=2.96.0
ARG GH_SHA_AMD64=83d5c2ccad5498f58bf6368acb1ab32588cf43ab3a4b1c301bf36328b1c8bd60
ARG GH_SHA_ARM64=06f86ec7103d41993b76cd78072f43595c34aaa56506d971d9860e67140bf909

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
ARG GH_VERSION
ARG GH_SHA_AMD64
ARG GH_SHA_ARM64
USER root

# collecting
COPY --from=loader /etc/environment /etc/environment
COPY --from=loader /usr/local/bin/ollama /usr/local/bin/ollama
COPY --from=loader /usr/local/lib/ollama/*.so* /usr/local/lib/ollama/

ENV DEBIAN_FRONTEND=noninteractive
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

# Install the official GitHub CLI from a checked release. WHY: managed account
# state is isolated at runtime, but VM and pod must execute the same verified
# client without inheriting a floating distro package.
RUN \
    case "${TARGETARCH}" in \
        amd64) GH_SHA="${GH_SHA_AMD64}" ;; \
        arm64) GH_SHA="${GH_SHA_ARM64}" ;; \
        *) echo "unsupported GitHub CLI architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac ;\
    GH_ARCHIVE="gh_${GH_VERSION}_linux_${TARGETARCH}.tar.gz" ;\
    curl -fsSL -o "/tmp/${GH_ARCHIVE}" \
        "https://github.com/cli/cli/releases/download/v${GH_VERSION}/${GH_ARCHIVE}" ;\
    echo "${GH_SHA}  /tmp/${GH_ARCHIVE}" | sha256sum -c - ;\
    tar -C /tmp -xzf "/tmp/${GH_ARCHIVE}" ;\
    install -m 0755 "/tmp/gh_${GH_VERSION}_linux_${TARGETARCH}/bin/gh" /usr/local/bin/gh ;\
    rm -rf "/tmp/${GH_ARCHIVE}" "/tmp/gh_${GH_VERSION}_linux_${TARGETARCH}" ;\
    gh --version


# Keep the upstream Milvus CLI pinned in /usr/local/bin. WHY: app launch replaces
# ~/.local/bin/milvus_cli with a configured wrapper and must not follow a
# floating package or overwrite the package-managed implementation.
# installing lsp: ts py, mcp: postgres, redis, milvus, mongodb, cli: milvus
# WHY the `mcp<2` constraint: postgres-mcp and redis-mcp-server declare an open
# upper bound on the MCP SDK (`mcp[cli]>=1.5.0` / `>=1.9.4`), but both still
# import `mcp.server.fastmcp`, which mcp 2.x renamed to `mcp.server.mcpserver`.
# Left unpinned they resolve mcp 2.x and die at import, which surfaces only as a
# silent "7/9 servers connected" in OpenCode. Keep this in step with setup.sh.
RUN set -eu ;\
    curl -LsSf https://astral.sh/uv/install.sh | \
    env UV_INSTALL_DIR="/usr/bin" INSTALLER_NO_MODIFY_PATH=1 sh ;\
    for tool in \
        milvus-cli==1.2.1 \
        'python-lsp-server[yapf]' \
        'postgres-mcp==0.3.0|--with|mcp<2' \
        'redis-mcp-server==0.5.0|--with|mcp<2' \
        "git+${MILVUS_MCP_REPO}@${MILVUS_MCP_REF}" ;\
    do \
        spec="${tool%%|*}" ;\
        extra="" ;\
        if [ "$tool" != "$spec" ] ; then \
            extra="$(echo "$tool" | cut -d'|' -f2- | tr '|' ' ')" ;\
        fi ;\
        env \
        UV_TOOL_BIN_DIR=/usr/local/bin \
        UV_TOOL_DIR=/opt/uv/tools \
        UV_CACHE_DIR=/opt/uv/cache \
        UV_PYTHON_PREFERENCE=only-system \
        UV_LINK_MODE=hardlink \
        uv tool install --python /usr/bin/python3 $extra "$spec" ;\
    done ;\
    # Fail the build on an import-time break rather than shipping a server that
    # only fails once a user launches an app (see the constraint note above).
    for probe in "postgres-mcp:postgres_mcp" \
                 "redis-mcp-server:src.main" \
                 "mcp-server-milvus:mcp_server_milvus" ;\
    do \
        /opt/uv/tools/"${probe%%:*}"/bin/python -c "import ${probe#*:}" \
        || { echo "FATAL: ${probe%%:*} fails at import" >&2 ; exit 1 ; } ;\
    done

COPY openserverless-mcp /tmp/openserverless-mcp
COPY trustable-react-mcp /tmp/trustable-react-mcp
RUN \
    npm install -g typescript-language-server typescript tsx mongodb-mcp-server@1.9.0 ;\
    cd /tmp/openserverless-mcp && npm pack --pack-destination /tmp ;\
    npm install -g tsx /tmp/openserverless-mcp-*.tgz ;\
    cd /tmp/trustable-react-mcp && npm pack --pack-destination /tmp ;\
    npm install -g /tmp/trustable-react-mcp-*.tgz ;\
    cd / ;\
    rm -rf /tmp/openserverless-mcp /tmp/openserverless-mcp-*.tgz /tmp/trustable-react-mcp /tmp/trustable-react-mcp-*.tgz ;\
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
# stages only the runtime artifacts; source and node_modules must never enter an
# image layer. The separately loaded policy file is pinned with this artifact
# set so managed Pi cannot start without the issue #57 host contract.
COPY --chown=trustable:trustable truacp-runtime/setup.sh /tmp/truacp/setup.sh
COPY --chown=trustable:trustable truacp-runtime/pi.version /tmp/truacp/pi.version
COPY --chown=trustable:trustable truacp-runtime/pi.integrity /tmp/truacp/pi.integrity
COPY --chown=trustable:trustable truacp-runtime/pi-acp-package.tgz /tmp/truacp/pi-acp-package.tgz
COPY --chown=trustable:trustable truacp-runtime/dist-bin/truacp.cjs /tmp/truacp/dist-bin/truacp.cjs
COPY --chown=trustable:trustable truacp-runtime/extensions/trustable-runtime.ts /tmp/truacp/extensions/trustable-runtime.ts
RUN \
    cd /tmp/truacp ;\
    sh setup.sh ;\
    test -x "$HOME/.local/bin/truacp" ;\
    test -x "$HOME/.local/bin/trustable-acp" ;\
    rm -rf /tmp/truacp

# workspace
#
# Drop an interactive terminal into the currently-launched app's checkout.
# launch.go's writeCurrentApp rewrites $WORKBENCH_DIR/current on every launch, so
# that file -- not the shell's starting directory -- is the source of truth for
# which app is live. screenshot.sh reads it the same way.
#
# Written as a heredoc rather than an echo of one long line: the previous
# single-line form hid three faults at once (a relative `cat` path that only
# resolved when the shell started in $HOME, an `export PWD` that sets a variable
# the next cd overwrites instead of changing directory, and an error on a fresh
# pod where no app has been launched yet).
#
# .bashrc, not .profile: the terminal spawns `$SHELL -i` (see spec/12-terminal.md),
# which reads .bashrc and never sources the profile files.
RUN \
    mkdir -p /home/trustable/workspace ;\
    cat >>$HOME/.bashrc <<'BASHRC'

# Start in the launched app's checkout, if there is one. Silent otherwise: no
# current app is the normal state on a fresh pod, and `current` can still name an
# app whose checkout has since been removed.
if [ -r "$HOME/workbench/current" ]; then
    __trustable_app="$(tr -d '[:space:]' <"$HOME/workbench/current" 2>/dev/null || true)"
    if [ -n "$__trustable_app" ] && [ -d "$HOME/workbench/$__trustable_app" ]; then
        cd "$HOME/workbench/$__trustable_app" || true
    fi
    unset __trustable_app
fi
BASHRC

########################################################## stage3-app
# start
USER root
RUN \
    if [ -x /usr/local/bin/mcp-s3 ] && [ ! -x /usr/local/bin/mcp-s3-real ]; then \
        mv /usr/local/bin/mcp-s3 /usr/local/bin/mcp-s3-real; \
    fi
COPY mcp-s3 /usr/local/bin/mcp-s3
COPY redis-mcp /usr/local/bin/trustable-redis-mcp
RUN chmod 0755 /usr/local/bin/mcp-s3 /usr/local/bin/trustable-redis-mcp
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
