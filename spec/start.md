# Repository-root `start.sh`

This specification defines the repository-root `start.sh`, which brings up a
Trustable development environment. It supports two hosts, selected
automatically by `uname -s`:

- **macOS** — provisions a local Trustable VM with Lima and wires it up so the
  rest of the tooling (ssh.sh, setup.sh, build.sh, publish.sh) finds it in
  ~/Library/Application Support/Trustable/ — the same place the macOS app writes.
  This is the whole of the "macOS (Lima VM)" section below.
- **native Ubuntu Linux** — there is nothing to virtualize, so VM creation is
  skipped entirely and only the environment initialization runs, directly on
  this host. See "Native Ubuntu Linux" at the end.

Any other operating system is unsupported and aborts: the entire flow is
apt/dpkg-based.

The two paths share every provisioning payload — the k3s cert SAN, the
host-rewrite reverse proxy, ollama, kubefwd, and gh are the same Ubuntu bash on
both, differing only in whether they run in the VM or on this machine. They are
therefore implemented once and dispatched through a single indirection, rather
than duplicated per host.

# macOS (Lima VM)

## Download + cache the package (host side, before booting the VM)

Pick the deb for the HOST arch and cache it under dist/:

- arm (Apple Silicon): curl -JLO landing2.nuvolaris.org/api/my/v1/download/linux-arm  -> trustable_<version>_arm64.deb
- intel:               curl -JLO landing2.nuvolaris.org/api/my/v1/download/linux-amd  -> trustable_<version>_amd64.deb

Cache as dist/trustable_<version>_<arch>.deb. If it already exists, skip the
~3.6GB download. Download to a .part file and move into place so an interrupted
download never leaves a truncated cache entry.

The release identity must stay aligned across the repository: `version.txt`
contains the tagged form (`v0.4.0`) used by builds, while `TRUSTABLE_VERSION` in
`start.sh` contains the numeric form (`0.4.0`) used to download the VM package.

## Boot the VM, then install (NOT via cloud-init provision)

Create an Ubuntu VM with Lima using vmType: vz and a vzNAT network. Do NOT install
the package as a Lima `provision` script: that runs inside `limactl start`'s
readiness wait, which times out after ~10min on the big package. Instead boot a
bare VM fast, then copy the cached deb in (`limactl copy`) and `apt install` it
over `limactl shell`, where the install is not time-bounded.

## Networking (so the apihost is actually reachable)

The VM gets two interfaces: lima0 (192.168.252.x, reachable from the macOS host)
and eth0 (vzNAT, NOT host-reachable). The package's postinst installs a firewall
dropin that DROPs :80/:443/:6443 on the DEFAULT-route interface. Lima defaults the
default route to lima0, which would block the host from reaching k3s on :80.

So, BEFORE installing, write a netplan override giving eth0 a lower route-metric
than lima0 (e.g. 50 vs 100) and `netplan apply`, so the default route moves to
eth0 and the firewall DROP lands on the non-host-reachable vzNAT interface,
leaving lima0:80 open. Publish the lima0 (192.168.252.x) address as the apihost.

## Authorize ssh

ssh.sh connects as trustable@<ip> with the Lima identity, but the package creates
the 'trustable' user without any authorized_keys. Append the Lima pubkey(s) to
/home/trustable/.ssh/authorized_keys during install.

## Mount the current folder

Mount the folder start.sh runs from (`$(pwd)`) into the VM at the SAME path via a
writable virtiofs mount in the Lima config (`mounts: [{location, mountPoint, writable:true}]`).
Same path host-and-guest so absolute paths line up on both sides.

## Mirror the current user into the VM

Also create a guest account matching the current macOS user (`id -un`) with the
same UID (`id -u`), so files under the virtiofs mount keep the host's ownership
inside the VM. Give them passwordless sudo (`/etc/sudoers.d/90-<user>`) and copy
trustable's authorized_keys across so `ssh <user>@<ip>` works too.

Idempotent and re-run on every start (not only fresh installs), since an existing
VM may predate the user. Skip when the host user is already `trustable` or `root`;
only pin the UID when it isn't already taken by another account.

## k3s API cert (tls-san)

The in-VM setup keeps the local k3s kubeconfig on `127.0.0.1`, but host-side
tools may use the host-reachable lima0 address. k3s's serving cert lists only
the node IP (eth0/vzNAT) plus 127.0.0.1 by default, not lima0, so those
host-side clients would fail TLS verification. Add the lima0 IP to k3s's
tls-san (write `/etc/rancher/k3s/config.yaml`, drop the serving cert plus
`dynamic-cert.json`, restart k3s to reissue). Idempotent: skip if the live cert
already covers the IP. No change is needed in setup.sh.

## Write the support files

Read the host-reachable ip (the lima0 / 192.168.252.x address from hostname -I) and write:

- ~/Library/Application Support/Trustable/current.ip   -> <ip>
- ~/Library/Application Support/Trustable/apihost      -> http://<ip>.nip.io:8080
- ~/Library/Application Support/Trustable/id_ed25519   -> copy of the limactl key (~/.lima/_config/user)

The apihost points through the host-rewrite proxy (see above), NOT bare <ip>:80 —
a plain http://<ip>/api/info sends Host: <ip>, which traefik does not match (404).
Via <ip>.nip.io:8080 the proxy rewrites Host -> miniops.me, so setup.sh's step-7
check (curl <apihost>/api/info -> .description == "OpenWhisk") passes unchanged, and
subdomains ops derives from this base also route.

Success check: curl http://<ip>.nip.io:8080/api/info returns .description == "OpenWhisk"
(OpenWhisk takes ~60-90s to come up after install).

The final "VM ready" summary must print the actual resolved apihost — the real
lima0 IP substituted in (e.g. http://192.168.252.3.nip.io:8080) — never the
literal 127.0.0.1 or an unexpanded <ip> placeholder. It's the value written to
~/Library/Application Support/Trustable/apihost, so echo that same string.

## Host-rewrite reverse proxy (in k3s)

traefik's ingresses only match the *.miniops.me hostnames, but from the macOS
host you reach the VM by IP. So deploy (idempotently, via kubectl apply) an nginx
reverse proxy into k3s, in the nuvolaris namespace, as a LoadBalancer Service on
:8080 that rewrites the Host header and forwards to traefik's ClusterIP:

- http://<apihost>            (Host: <ip> or <ip>.nip.io)        -> Host: miniops.me
- http://<host>.<apihost>     (Host: <host>.<ip>.nip.io)         -> Host: <host>.miniops.me

Reachable from the host at http://<ip>:8080 and http://<host>.<ip>.nip.io:8080.
Port 8080 is used (not 80) so traefik keeps owning the node's :80 untouched, and
8080 is outside the package's :80/:443/:6443 firewall DROP. The proxy is
re-applied on every run so it tracks the current VM IP.

## CPU ollama in the VM

Install ollama as a host process inside the VM (not a pod), pinned to the
image's OLLAMA_VERSION (the `ARG OLLAMA_VERSION=` line in image/Dockerfile), and
enable its systemd service so it serves on localhost:11434 — the app's
OLLAMA_ENDPOINT. Apple's vz gives the Linux guest no GPU passthrough, so this is
CPU-only; that's fine because the app mostly uses cloud models. Idempotent: skip
the install when ollama is already present at the pinned version. Runs on every
start (in the finish path), so an existing VM gets ollama too.

## Pinned kubefwd in the VM

Install `kubefwd` 1.25.16 as `/usr/local/bin/kubefwd` on every finish path, so
both new and reused `trudev` VMs satisfy repository-root `run.sh`. Select the
official Linux archive for `x86_64` or `arm64`, verify its pinned SHA-256 before
installation, install atomically with mode `0755`, and verify the installed
version. Unsupported architectures, checksum failures, missing binaries, or
version mismatches abort provisioning with an actionable error. This
provisioning must not modify resolver configuration; `run.sh` owns the
forwarder's process lifetime.

## GitHub CLI availability in the VM

`start.sh` must explicitly ensure GitHub CLI is installed in the VM with
`apt-get install gh` during provisioning, for both fresh and reused VMs.

Before printing the final "VM ready" summary, `start.sh` must also verify that
`gh` resolves and matches the pinned runtime version. Runtime convergence is
enforced by the mandatory in-VM `setup.sh` run in the finish path, using the
release identity defined by `GH_VERSION`, `GH_SHA_AMD64`, and `GH_SHA_ARM64`
from `image/Dockerfile`.

This applies to provisioning flows that execute `setup.sh` (fresh VM creation).

On provisioning flows that execute `setup.sh` (fresh VM creation), `start.sh`
attempts non-interactive GitHub
authentication inside the VM using repository-root `.ghtoken`
(`gh auth login --with-token`). At that late point, missing `gh` or a login
failure are warning-only and must not block startup — the token itself has
already been gated up front (see below). VS Code opens by
default; `./start.sh -n` skips opening VS Code.

## GitHub token gate (first step)

The **first step** of any real start, on both hosts, is checking the
repository-root `.ghtoken`. A run provisions a cluster and clones private
sources, so an absent token must surface immediately rather than minutes later
at the login step.

- Present and non-empty: report it and continue.
- Absent or empty, with a terminal: prompt for the token on `/dev/tty` with echo
  **off** (it is a credential and must not reach scrollback), then persist it to
  `.ghtoken` created under `umask 077` / mode `0600`. `.ghtoken` is git-ignored.
- Empty input at the prompt: **stop the run** non-zero without writing a file.
- Absent or empty with no TTY (CI, piped): do not hang waiting on input — fail
  immediately, naming the file to create and where to get a token.

The check sits after the `-s`/`-k` branches, which exit earlier: stopping or
destroying a VM must never require a token.

## Re-running when the VM already exists

`./start.sh` is idempotent — an existing VM is not an error:

- Running: skip provisioning/setup, refresh support files, wait until `ssh.sh`
	can execute in the VM, then open VS Code when enabled.
- Stopped: `limactl start` the existing instance, skip provisioning/setup,
	refresh support files, wait for SSH readiness, then open VS Code when enabled.

Use `./start.sh -k` first only when you want a clean rebuild.

---
./start.sh -s stops the VM without deleting it, so a later `./start.sh` restarts
it (no reinstall) — the Stopped path above.
./start.sh -k stops and deletes the VM.


---
add user in group sudo and do not create another group (use useradd -g sudo)

# Native Ubuntu Linux

On a Linux host `./start.sh` **skips VM creation entirely** and initializes this
machine directly, so that `./setup.sh` and `./run.sh` are then usable exactly as
they are inside `trudev`.

## Preflight

Abort with an actionable message unless all of the following hold:

- `/etc/os-release` identifies Ubuntu or Debian (`ID`/`ID_LIKE`). Every install
  step below is `apt-get`/`dpkg`, and the Trustable package is a `.deb`.
- the architecture reported by `dpkg --print-architecture` is amd64 or arm64.
  This is the same source `ensure_deb` uses, so the preflight gate and the
  package actually selected can never disagree.
- `sudo -n true` succeeds. Passwordless sudo is already assumed by `setup.sh`,
  by `run.sh`'s kubefwd supervisor, and by the package install.
- systemd is running (`/run/systemd/system` exists and `systemctl
  is-system-running` is not `offline`). k3s is a systemd service.

Then initialize the runtime source submodules, exactly as the macOS path does —
this step is host-agnostic.

`limactl` is NOT required, and neither is `code`.

## Cluster

If `dpkg -l trustable` does not report `ii`, download and cache the `.deb` and
install it on this machine.

The package must match the host architecture. On Linux, detect it with
`dpkg --print-architecture` — that is what governs whether `apt-get install`
will accept the package, and it stays correct on a multiarch host where `uname`
reports the kernel's architecture. It emits exactly the strings the filenames
use, so:

- arm64 -> `dist/trustable_<version>_arm64.deb` (download `linux-arm`)
- amd64 -> `dist/trustable_<version>_amd64.deb` (download `linux-amd`)

macOS has no `dpkg` — the `.deb` is installed inside the VM, which runs the host
architecture under vz — so the macOS path keeps mapping from `uname -m`
(`arm64`/`aarch64` -> arm64, `x86_64`/`amd64` -> amd64) onto the same two
filenames. Anything else aborts as an unsupported architecture. The resolved
architecture is echoed before the download so a wrong-arch cache hit is visible. Print an explicit banner naming the package and what it installs
first — this is the one step that mutates the host outside the repository.

Two deliberate differences from the in-VM install:

- **No netplan override.** The VM needs one only because Lima gives it two
  interfaces and defaults the route to the host-facing lima0, which would put
  the package's :80/:443/:6443 firewall DROP between the Mac and k3s. A native
  host has no such split — its real default route is the correct one to protect,
  which is what the package's postinst already selects.
- **No authorized_keys grafting** for the package-created `trustable` user. That
  exists so `ssh.sh` can reach the VM; nobody ssh's into the machine they are
  sitting at.

If the package is already installed, skip straight to verification.

Then wait for the local k3s to serve `/readyz` and for the `nuvolaris` namespace
to appear. A fresh install needs 60–90s before OpenWhisk is up, and `setup.sh`
step 7 curls the apihost, so it must not run against a booting cluster.

Probe with `sudo -n k3s kubectl`, which points itself at
`/etc/rancher/k3s/k3s.yaml`. Only fall back to a plain `kubectl` when `k3s` is
absent, and then pass `--kubeconfig /etc/rancher/k3s/k3s.yaml` explicitly: this
step runs before `setup.sh` writes `~/.ops/tmp/kubeconfig`, so k3s.yaml is the
only kubeconfig that exists yet, and a bare `sudo -n kubectl` would run as root
with no `KUBECONFIG` and probe the default `localhost:8080` — hanging the full
180s timeout against a perfectly healthy cluster. Hosts with a snap-installed
`kubectl` on `PATH` hit exactly this. The timeout message names the command it
probed with, so the failure is diagnosable.

## Support files

Resolve the host-reachable address as the source address of the default route
(`ip -4 route get`), falling back to `127.0.0.1` on a host with no default route.
Write, under `${XDG_CONFIG_HOME:-$HOME/.config}/trustable/`:

- `current.ip` -> `<ip>`
- `apihost`    -> `http://<ip>.nip.io:8080`

There is no `id_ed25519`: nothing ssh's anywhere on this path.

The apihost goes through the host-rewrite proxy for the same reason as on macOS —
a bare `http://<ip>/api/info` sends `Host: <ip>`, which traefik does not match.

`configure.go`'s `apihostFilePath()` resolves this identical location for
`GOOS=linux`, so the Go server honours the written value. Leaving that case
empty would make the server silently fall back to `http://miniops.me`.

`build.sh` deliberately needs no change: it keys its "ship the image over ssh to
the Mac VM" branch on `id_ed25519` **and** `current.ip` both existing under the
*macOS* support path. The Linux support dir is elsewhere and carries no key, so
`build.sh` keeps taking its local-k3s branch. Do not "fix" this by teaching
`build.sh` about the Linux support dir.

## Shared provisioning

Run the same helpers the macOS finish path runs, against this host:

- the k3s API cert SAN for the resolved IP;
- the host-rewrite reverse proxy on `:8080`, so
  `http://<label>.<ip>.nip.io:8080` reaches `<label>.miniops.me`. This is what
  makes the app reachable from another machine on the network;
- ollama on `localhost:11434`, pinned to the image's `OLLAMA_VERSION`. Unlike
  the VM, a native host is not restricted to CPU — the upstream installer
  detects CUDA/ROCm by itself, which needs no special handling here;
- the pinned `kubefwd` 1.25.16 with its SHA-256 verification;
- the `gh` apt package.

Then run `./setup.sh` directly (not through `limactl shell`), verify the pinned
`gh` runtime version, and attempt the warning-only `.ghtoken` login.

## Flags

- `-s` and `-k` are VM lifecycle operations with no native equivalent. They must
  exit non-zero with an explanatory message and must never be reinterpreted as
  "stop or destroy this machine's k3s".
- `-n` is accepted and ignored: VS Code is never opened on this path, so a
  habitual `./start.sh -n` still works.

## Summary

Print the apihost, the host-rewrite URL pattern, the ollama endpoint, a `vscode`
line, and `./run.sh` as the next step. Omit the ssh, mount, stop, and destroy
lines — none of them exist here.

The `vscode` line prints the command that opens the sources, not a Remote-SSH
hop: on a native host the repo is already local, so connecting is just
`code <repo dir>`. When `code` is not on `PATH`, print that same command as a
hint plus the "Shell Command: Install 'code' command in PATH" pointer, and carry
on. Unlike the macOS path — where a missing `code` is a hard `fail` because
Remote-SSH is the only way in — VS Code is never required here, so its absence
must never abort a run that has otherwise succeeded.

Re-running is fully idempotent: an installed package, a ready cluster, and an
already-provisioned toolchain are all verified rather than redone.
