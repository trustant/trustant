# Third-party components

Trustant itself is licensed under the **GNU AGPL v3 or later** (see
[LICENSE](LICENSE) and [NOTICE](NOTICE)). This file lists the third-party
software it includes, bundles into the container image, or installs into the
development VM, with each component's license.

Versions are the pinned ones where the repo pins them (`go.mod`,
`image/Dockerfile`, `acp/pi.version`, `acp/extensions/requirements.txt`,
`start.sh`); "latest" means installed unpinned. Licenses are as declared by
the upstream project (SPDX identifiers where available). Keep this file in step
when adding, removing or upgrading a dependency.

## 1. Go binary (`trustant`)

Compiled into the single executable.

| Component | Version | License |
|---|---|---|
| Go standard library / toolchain | 1.25.5 | BSD-3-Clause |
| [github.com/coder/websocket](https://github.com/coder/websocket) | 1.8.15 | ISC |
| [github.com/creack/pty](https://github.com/creack/pty) | 1.1.24 | MIT |

## 2. Frontend assets vendored in `web/`

Embedded into the binary.

| Component | File | Version | License |
|---|---|---|---|
| [Tailwind CSS browser build](https://github.com/tailwindlabs/tailwindcss) (`@tailwindcss/browser`) | `web/tailwind.js` | 4.1.18 | MIT |
| [marked](https://github.com/markedjs/marked) | `web/js/marked.min.js` | 15.0.12 | MIT |
| [CodeJar](https://github.com/antonmedv/codejar) | `web/js/codejar.min.js` | 4.x | MIT |
| [xterm.js](https://github.com/xtermjs/xterm.js) (`@xterm/xterm`) | `web/js/xterm.min.js`, `web/js/xterm.css` | 6.0.0 | MIT |
| xterm.js fit addon (`@xterm/addon-fit`) | `web/js/xterm-addon-fit.min.js` | 0.11.0 | MIT |

## 3. Git submodules

| Submodule | Upstream | License |
|---|---|---|
| `acp` (TruACP) | github.com/trustant/trustant-acp | AGPL-3.0 |
| `oplugins-truinst` | github.com/trustant/oplugins-truinst | AGPL-3.0 |
| `oplugins` | github.com/trustable-ai/openserverless-task | Apache-2.0 |
| `mcp` (openserverless-mcp) | github.com/trustant/openserverless-mcp (fork of apache/openserverless-mcp) | Apache-2.0 |
| `skills` | github.com/trustable-ai/skills | Apache-2.0 |
| `packages` | github.com/trustant/openserverless-packages | Apache-2.0 |

### TruACP (`acp`) runtime dependencies

Bundled into `truacp.cjs` / the TruACP web UI.

| Component | Version | License |
|---|---|---|
| @agentclientprotocol/sdk | 0.28.1 | Apache-2.0 |
| @codemirror/state | 6.5.0 | MIT |
| @codemirror/view | 6.38.x | MIT |
| @tanstack/react-virtual | 3.14.x | MIT |
| diff | 8.0.x | BSD-3-Clause |
| react, react-dom | 18.3.1 | MIT |
| react-markdown | 10.1.0 | MIT |
| remark-gfm | 4.0.1 | MIT |
| semver | 7.x | ISC |
| ws | 8.21.1 | MIT |
| zod | 4.4.3 (pi-acp: 3.25) | MIT |

### MCP servers built from this repo

| Component | Dependencies | License |
|---|---|---|
| `mcp` (openserverless-mcp) | @modelcontextprotocol/sdk (MIT), zod (MIT) | Apache-2.0 |
| `react-mcp` (trustant-react-mcp) | @modelcontextprotocol/sdk (MIT), zod (MIT), typescript (Apache-2.0) | AGPL-3.0 (this repo) |

## 4. Container image (`image/Dockerfile`)

### Base system (Ubuntu 24.04 packages)

| Component | License |
|---|---|
| Ubuntu 24.04 base image | various (mostly GPL / LGPL / BSD / MIT, per package) |
| git | GPL-2.0 |
| sudo | ISC |
| curl | curl (MIT-style) |
| jq | MIT |
| less | GPL-3.0 or Less License |
| vim | Vim License |
| lsof | lsof license (BSD-style) |
| tini | MIT |
| openssh-server | BSD |
| supervisor | BSD-derived (Repoze) |
| iputils-ping | BSD-3-Clause / GPL-2.0 |
| inetutils-telnet | GPL-3.0 |
| ripgrep | MIT OR Unlicense |
| python3, python-is-python3, python3-pip | PSF-2.0 / MIT |
| python3-pytest | MIT |
| python3-dotenv | BSD-3-Clause |
| postgresql-client-16 | PostgreSQL License |
| redis-tools | BSD-3-Clause |
| rclone | MIT |
| locales | LGPL-2.1 |
| libatomic1 | GPL-3.0 with GCC Runtime Library Exception |
| zstd (build stage only) | BSD-3-Clause OR GPL-2.0 |
| Node.js 24 (NodeSource) | MIT |

### Tools and runtimes

| Component | Version | License |
|---|---|---|
| [Ollama](https://github.com/ollama/ollama) | 0.32.14 | MIT |
| [GitHub CLI](https://github.com/cli/cli) (`gh`) | 2.96.0 | MIT |
| [uv](https://github.com/astral-sh/uv) | latest | Apache-2.0 OR MIT |
| [ops](https://github.com/apache/openserverless-cli) (OpenServerless CLI) | via n7s.co/get-ops-tru | Apache-2.0 |
| [milvus-cli](https://github.com/zilliztech/milvus_cli) | 1.2.1 | Apache-2.0 |
| [python-lsp-server](https://github.com/python-lsp/python-lsp-server) (+ yapf) | latest | MIT (yapf: Apache-2.0) |
| [typescript-language-server](https://github.com/typescript-language-server/typescript-language-server) | latest | Apache-2.0 |
| [TypeScript](https://github.com/microsoft/TypeScript) | latest | Apache-2.0 |
| [tsx](https://github.com/privatenumber/tsx) | latest | MIT |

### MCP servers

| Component | Version | License |
|---|---|---|
| [postgres-mcp](https://github.com/crystaldba/postgres-mcp) | 0.3.0 | MIT |
| [redis-mcp-server](https://github.com/redis/mcp-redis) | 0.5.0 | MIT |
| [mcp-server-milvus](https://github.com/trustable-ai/mcp-server-milvus) (fork, pinned commit `a7e624f`) | — | Apache-2.0 |
| [mongodb-mcp-server](https://github.com/mongodb-js/mongodb-mcp-server) | 1.9.0 | Apache-2.0 |
| [mcp-s3](https://github.com/txn2/mcp-s3) | 1.3.0 | Apache-2.0 |
| [mcp](https://github.com/modelcontextprotocol/python-sdk) (Python MCP SDK, `<2`) | 1.x | MIT |

### Coding agents (`acp/pi.version`)

| Component | Version | License |
|---|---|---|
| @earendil-works/pi-coding-agent | 0.82.0 | MIT |
| @earendil-works/pi-ai | 0.82.0 | MIT |
| @earendil-works/pi-tui | 0.82.0 | MIT |
| @earendil-works/pi-agent-core | 0.82.0 | MIT |
| @earendil-works/pi-storage-sqlite-node | 0.82.0 | MIT |
| pi-mcp-adapter | 2.11.0 | MIT |
| pi-web-access | 0.13.0 | MIT |
| @anthropic-ai/claude-code | 2.1.216 | **Proprietary** (Anthropic Commercial Terms — "SEE LICENSE IN README.md") |
| @agentclientprotocol/claude-agent-acp | 0.60.0 | Apache-2.0 |
| @openai/codex | 0.144.6 | Apache-2.0 |
| @agentclientprotocol/codex-acp | 1.1.4 | Apache-2.0 |

### Python packages for user apps (`acp/extensions/requirements.txt`)

| Package | Version | License |
|---|---|---|
| annotated-types | 0.8.0 | MIT |
| anyio | 4.15.1 | MIT |
| argon2-cffi | 25.1.0 | MIT |
| argon2-cffi-bindings | 26.1.0 | MIT |
| asn1crypto | 1.5.1 | MIT |
| bcrypt | 5.0.0 | Apache-2.0 |
| beautifulsoup4 | 4.15.0 | MIT |
| boto3 | 1.43.97 | Apache-2.0 |
| botocore | 1.43.97 | Apache-2.0 |
| cachetools | 7.2.0 | MIT |
| certifi | 2026.7.22 | MPL-2.0 |
| cffi | 2.1.1 | MIT-0 |
| charset-normalizer | 3.5.1 | MIT |
| click | 8.5.0 | BSD-3-Clause |
| cloudpickle | 3.1.2 | BSD-3-Clause |
| defusedxml | 0.7.1 | PSF-2.0 |
| distro | 1.9.0 | Apache-2.0 |
| dnspython | 2.8.0 | ISC |
| feedparser | 6.0.14 | BSD-2-Clause |
| feedparser-sgmllib | 2.1.0 | PSF-2.0 |
| grpcio | 1.84.0 | Apache-2.0 |
| h11 | 0.16.0 | MIT |
| httpcore | 1.0.9 | BSD-3-Clause |
| httpcore2 | 2.13.0 | BSD-3-Clause |
| httplib2 | 0.32.0 | MIT |
| httpx | 0.28.1 | BSD-3-Clause |
| httpx2 | 2.13.0 | BSD-3-Clause |
| idna | 3.20 | BSD-3-Clause |
| jiter | 0.17.0 | MIT |
| jmespath | 1.1.0 | MIT |
| joblib | 1.6.0 | BSD-3-Clause |
| jsonpatch | 1.33 | BSD-3-Clause |
| jsonpointer | 3.1.1 | BSD-3-Clause |
| kafka-python | 3.0.11 | Apache-2.0 |
| langchain | 1.4.1 | MIT |
| langchain-core | 1.6.3 | MIT |
| langchain-protocol | 0.0.19 | MIT |
| langdetect | 1.0.9 | Apache-2.0 |
| langgraph | 1.2.11 | MIT |
| langgraph-checkpoint | 4.2.0 | MIT |
| langgraph-prebuilt | 1.1.0 | MIT |
| langgraph-sdk | 0.4.4 | MIT |
| langsmith | 0.12.6 | MIT |
| minio | 7.2.20 | Apache-2.0 |
| narwhals | 2.26.0 | MIT |
| nltk | 3.10.3 | Apache-2.0 |
| numpy | 2.5.3 | BSD-3-Clause AND 0BSD AND MIT AND Zlib AND CC0-1.0 |
| ollama | 0.6.2 | MIT |
| openai | 3.15.0 | Apache-2.0 |
| orjson | 3.12.0 | MPL-2.0 AND (Apache-2.0 OR MIT) |
| ormsgpack | 1.12.2 | Apache-2.0 OR MIT |
| packaging | 26.3 | Apache-2.0 OR BSD-2-Clause |
| pandas | 3.0.6 | BSD-3-Clause |
| pg8000 | 1.31.5 | BSD-3-Clause |
| plotly | 7.1.0 | MIT |
| protobuf | 7.36.2 | BSD-3-Clause |
| psycopg, psycopg-binary | 3.3.5 | **LGPL-3.0-only** |
| pycparser | 3.0 | BSD-3-Clause |
| pycryptodome | 3.23.0 | BSD-2-Clause AND Public Domain |
| pydantic | 2.13.5 | MIT |
| pydantic-core | 2.46.5 | MIT |
| pymilvus | 3.0.2 | Apache-2.0 |
| pymongo | 4.18.1 | Apache-2.0 |
| pyparsing | 3.3.2 | MIT |
| python-dateutil | 2.9.0.post0 | Apache-2.0 AND BSD-3-Clause |
| python-dotenv | 1.2.3 | BSD-3-Clause |
| pyyaml | 6.0.3 | MIT |
| redis | 8.1.0 | MIT |
| regex | 2026.9.10 | Apache-2.0 AND CNRI-Python |
| requests | 2.34.2 | Apache-2.0 |
| requests-toolbelt | 1.0.0 | Apache-2.0 |
| s3transfer | 0.19.2 | Apache-2.0 |
| scramp | 1.4.17 | MIT-0 |
| six | 1.17.0 | MIT |
| sniffio | 1.3.1 | MIT OR Apache-2.0 |
| soupsieve | 2.9.2 | MIT |
| tenacity | 9.1.4 | Apache-2.0 |
| tqdm | 4.70.1 | MPL-2.0 AND MIT |
| truststore | 0.10.4 | MIT |
| typing-extensions | 4.16.0 | PSF-2.0 |
| typing-inspection | 0.4.4 | MIT |
| urllib3 | 2.8.0 | MIT |
| uuid-utils | 0.17.1 | BSD-3-Clause |
| websockets | 16.1.1 | BSD-3-Clause |
| xxhash | 4.0.1 | BSD-2-Clause |
| zstandard | 0.25.0 | BSD-3-Clause |

## 5. Development environment only (not shipped)

Installed by `start.sh` / `start.ps1` / `setup.sh` / `run.sh` on a developer
machine or dev VM. Not part of the binary or the image.

| Component | Version | License |
|---|---|---|
| [Lima](https://github.com/lima-vm/lima) | latest | Apache-2.0 |
| [k3s](https://github.com/k3s-io/k3s) | via ops | Apache-2.0 |
| [kubefwd](https://github.com/txn2/kubefwd) | 1.25.16 | Apache-2.0 |
| [air](https://github.com/air-verse/air) | latest | GPL-3.0 |
| [g](https://github.com/voidint/g) (Go version manager) | latest | MIT |
| ffmpeg (`screenshot.sh`) | distro | LGPL-2.1+ / GPL-2.0+ (build-dependent) |
| [@playwright/test](https://github.com/microsoft/playwright) (e2e tests) | 1.56.1 | Apache-2.0 |
| WSL2 / Ubuntu-24.04 distro (Windows) | — | Microsoft / Ubuntu terms |

## License notes

- **Claude Code** (`@anthropic-ai/claude-code`) is the only proprietary
  component. It is installed from npm into the image at build time, never
  vendored into this repo, and is governed by Anthropic's terms.
- **psycopg** is LGPL-3.0; it is installed as an unmodified Python package and
  dynamically imported, which LGPL permits.
- **air** is GPL-3.0 but is a development-only file watcher; it is never
  distributed with Trustant.
- **MPL-2.0** components (certifi, orjson, tqdm) are file-level copyleft and are
  shipped unmodified.
