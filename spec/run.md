# Repository-root `run.sh`

This specification defines the repository-root `run.sh`, which invokes the app
with Air inside the `trudev` VM (see [setup.md](setup.md)). On a native Ubuntu
Linux host there is no VM, so the same dev loop runs directly: step 0's Ubuntu
check passes and execution falls straight through to the steps below. That host
is initialized by the native path of `./start.sh` (see
[start.md](start.md)), which supplies the same kubeconfig, `kubefwd`, and
toolchain the VM path provides.

Before Air starts, a development invocation regenerates `_build.txt` from
`version.txt` and `expiry.txt`, with exactly one exception: when `git describe
--exact-match --tags HEAD` names a tag and that tag already appears in
`_build.txt`, the existing metadata is kept. The checkout is then the very
commit that produced the release build, so its metadata is the truthful
metadata, and keeping it is what lets a local run be checked against the build
it came from. In every other state -- an untagged HEAD, a tag that does not
match the file, or a missing/empty `_build.txt` -- the metadata is regenerated,
so a reused worktree never runs Air against stale metadata left by an earlier
release or verification build. An empty tag must never be treated as a match:
`grep ""` succeeds against any non-empty file, which would keep stale metadata
on every untagged checkout. Branch/stream identity resolves in this
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

0. `run.sh` runs on **Ubuntu only** and refuses to start anywhere else. The
check reads `/etc/os-release` and requires `ubuntu` in `ID` or `ID_LIKE`, so the
trudev Lima VM, a WSL2 trudev distro and a native Ubuntu host all pass, while
Ubuntu derivatives do too. The toolchain paths, apt packages and local-k3s
assumptions in the steps below hold nowhere else, so failing here is clearer
than failing midway. The refusal names the detected system and says what to use
instead, exiting 1:
  a. another Linux distribution — report that this Linux is not supported and
     point at Ubuntu 24.04 or the trudev VM.
  b. macOS — point at `./start.sh`, which boots the VM and runs setup.sh and the
     dev loop inside it; `./ssh.sh ./run.sh` restarts only the dev loop.
  c. Windows — point at `.\start.ps1`, which creates the WSL2 trudev distro and
     starts the dev loop inside it.

1. terminate all the process listening in ports 8910, 5173 and 4096 found with lsof -i

2. trap the ^c; when you press ^c terminate everything

2b. sanity-check the local k3s (kubeconfig at ~/.ops/tmp/kubeconfig, openserverless
namespace present). Do NOT call ./start.sh from here — it is the provisioning
entrypoint and calls run.sh itself, so invoking it would recurse.

2c. before starting the forwarder, terminate any pre-existing `kubefwd` with
`SIGINT` and wait for it to exit. A forwarder left behind by an earlier run
still holds the loopback addresses and `/etc/hosts` entries the new one needs,
so the new process exits immediately and its `sudo` supervisor is gone before it
can record a child PID. Step 1's port sweep cannot cover this, because the
forwarder deliberately avoids 8910/5173/4096. The signal must be `SIGINT`, never
`SIGKILL`: `kubefwd` restores `/etc/hosts` on `SIGINT`, and killing it outright
leaves stale entries that break name resolution for every later run. If a
forwarder somehow survives, warn and continue -- the readiness check below is
what reports the real failure.

Then start exactly one `kubefwd` for namespace `openserverless`, using
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
host-reachable address, resolved in this order: the `current.ip` written by
`start.sh` under `${XDG_CONFIG_HOME:-$HOME/.config}/trustable/` (on a native
Linux host this is the LAN address, so the printed URL also works from another
machine), then the lima0 address inside the VM, then 127.0.0.1;
alongside it, print the deployment URL http://trustable.<ip>.nip.io/ — the same
hostname without the :8910 port, i.e. the cluster ingress on port 80 — so it can
be clicked to reach the deployment;
Trustable handles Ollama Cloud sign-in from the web UI via `/api/ollama-connect`

5. wait until you press ^c and terminate everything
