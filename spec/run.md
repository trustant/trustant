# Repository-root `run.sh`

This specification defines the repository-root `run.sh`, which invokes the app
with Air inside the `trudev` VM (see [setup.md](setup.md)):

Before Air starts, every development invocation must regenerate `_build.txt`
from `version.txt` and `expiry.txt`; it must never reuse metadata left by an
earlier release or verification build. Branch/stream identity resolves in this
order: `TRUSTABLE_BUILD_BRANCH` / `TRUSTABLE_BUILD_STREAM` overrides, the
current Git branch when available, the suffix of a mounted worktree named
`trustable-app-<identity>`, then `development`. This makes the mounted
`trustable-app-trucode-integration` worktree report `trucode-integration` even
inside Lima, where the worktree's external Git administration path is not
mounted.

Before any managed runtime starts, the in-VM path must be rebuilt from the
setup-owned npm prefix (`NPM_CONFIG_PREFIX` when exported, otherwise the
persisted `npm config get prefix`). `~/.local/bin` remains first and the selected
`<npm-prefix>/bin` follows it, so `truacp`, `pi-acp`, and `pi` resolve even when
`run.sh` is launched from the same non-interactive shell that just completed
`setup.sh` and has not reloaded `~/.bashrc`. A missing npm runtime, invalid
prefix, missing `pi`, or missing `pi-acp` is an immediate prerequisite failure,
not a deferred browser-side `ACP connection closed`.

0. on macOS (uname == Darwin) everything lives in the VM, not the host — run.sh
is the single entrypoint and owns the whole lifecycle:
  a. `./start.sh` — provision/boot the trudev VM AND run setup.sh in it (both
     idempotent); fail early if limactl is missing.
  b. re-invoke this same script inside the VM: `limactl shell --workdir "$PWD"
     trudev "$PWD/run.sh"`. Lima mounts this repo at the identical path and
     start.sh mirrors the host user into the guest (same name/UID), so the in-VM
     run lands in the same directory as the same user and falls through to the
     steps below.
  c. `trap '' INT` on the host so ^C reaches the in-VM run.sh (same process
     group), which does its own teardown and returns; then always stop the VM
     with `./start.sh -s` (keep it for a fast restart) and exit.

1. terminate all the process listening in ports 8910, 5173 and 4096 found with lsof -i

2. trap the ^c; when you press ^c terminate everything

2b. sanity-check the local k3s (kubeconfig at ~/.ops/tmp/kubeconfig, nuvolaris
namespace present). Do NOT call ./start.sh — it is macOS-only and provisions the
VM from the host.

2c. start exactly one `kubefwd` for namespace `nuvolaris`, using
`~/.ops/tmp/kubeconfig` and field selector
`metadata.name!=trustable-svc`. Excluding `trustable-svc` prevents the forwarder
from stealing Trustable's local ports 8910, 4096, and 5173. Wait for bounded
readiness by proving that at least one forwarded Service name resolves to a
loopback address. If the process exits or readiness times out, print its
diagnostic log and abort. The same cleanup trap that owns Air must terminate
the one `kubefwd` process and remove its temporary log. Because `kubefwd`
requires `sudo`, `run.sh` records the PID of the privileged child created
behind the `sudo` supervisor, sends that exact child `SIGINT`, waits for it to
restore `/etc/hosts` and release forwarded sockets, and only then returns.
Tracking or killing only `$!` (the `sudo` wrapper) is invalid because it can
leave cleanup racing with an immediate restart. Never start one forwarder per
service and never modify resolver configuration.

2d. ensure the local (CPU) ollama is serving on :11434 (OLLAMA_ENDPOINT);
start.sh installs it in the VM, so only start one if nothing is listening.

3. launch air in background (hot-reloads the Go binary on :8910). Air watches
the root Go and JavaScript sources, but excludes the `trustable-code` subrepo:
that subrepo is built by setup and can contain ignored Bun `.bun-build` marker
files with mode `000`, which must not enter Air's checksum scan.

4. print the browser URL as http://trustable.<ip>.nip.io:8910/ where <ip> is the
host-reachable lima0 address (fall back to 127.0.0.1 if there is no lima0);
Trustable handles Ollama Cloud sign-in from the web UI via `/api/ollama-connect`

5. wait until you press ^c and terminate everything
