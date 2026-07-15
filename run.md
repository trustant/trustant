create run.sh invoking the app with air, run INSIDE the trudev VM (see setup.md):

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
VM from the host. k3s is local in the VM, so there is no kubefwd: the Go app and
the MCP servers it spawns reach cluster services directly.

2c. ensure the local (CPU) ollama is serving on :11434 (OLLAMA_ENDPOINT);
start.sh installs it in the VM, so only start one if nothing is listening.

3. launch air in background (hot-reloads the Go binary on :8910). Air watches
the root Go and JavaScript sources, but excludes the `trustable-code` subrepo:
that subrepo is built by setup and can contain ignored Bun `.bun-build` marker
files with mode `000`, which must not enter Air's checksum scan.

4. print the browser URL as http://trustable.<ip>.nip.io:8910/ where <ip> is the
host-reachable lima0 address (fall back to 127.0.0.1 if there is no lima0);
Trustable handles Ollama Cloud sign-in from the web UI via `/api/ollama-connect`

5. wait until you press ^c and terminate everything
