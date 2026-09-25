# Repository-root `start.sh`

This specification defines the repository-root `start.sh`, which brings up a
Trustant development environment. It supports two hosts, selected
automatically by `uname -s`:

- **macOS** — provisions a local Trustant VM with Lima and wires it up so the
  rest of the tooling (ssh.sh, setup.sh, build.sh, publish.sh) finds it in
  ~/Library/Application Support/Trustant/ — the same place the macOS app writes.
  This is the whole of the "macOS (Lima VM)" section below.
- **native Ubuntu Linux** — there is nothing to virtualize, so VM creation is
  skipped entirely and only the environment initialization runs, directly on
  this host. See "Native Ubuntu Linux" at the end.

Any other operating system is unsupported and aborts: the entire flow is
apt/dpkg-based.

Windows is reached through the same native-Linux path: `start.ps1` creates a
WSL2 Ubuntu and runs this script inside it. `start.sh` itself stays entirely
Windows-unaware — see "Windows (WSL2)" at the end.

The two paths share every provisioning payload — the k3s cert SAN, the
host-rewrite reverse proxy, ollama, kubefwd, and gh are the same Ubuntu bash on
both, differing only in whether they run in the VM or on this machine. They are
therefore implemented once and dispatched through a single indirection, rather
than duplicated per host.

# macOS (Lima VM)

## Download + cache the package (host side, before booting the VM)

The cluster package is the Apache OpenServerless deb, published to a bucket that
exposes a machine-readable index:

    https://openserverless.nuvolaris.download/index.json

The index is a two-level object, keyed by architecture and then by version, whose
values are the download URLs:

```json
{
  "amd64": {
    "0.1.0+f1553b": "https://openserverless.nuvolaris.download/openserverless_0.1.0+f1553b_amd64.deb",
    "latest":       "https://openserverless.nuvolaris.download/openserverless_0.1.0+f1553b_amd64.deb"
  },
  "arm64": { ...same shape... }
}
```

That bucket is a Cloudflare R2 custom domain and does **not** expose the S3
`ListObjects` API — `?list-type=2` returns Cloudflare's own 404 page. `index.json`
is the only supported way to discover what is published; never try to enumerate
the bucket.

Resolution order: detect the HOST arch, read the pinned version, look up
`.[<arch>][<version>]` in the index, download that URL.

`openserverless.txt` pins the version and is a **hard pin**. If the file is
missing or empty, `start.sh` fails immediately — it must never install an
unpinned package. If the pinned version is absent from the index, it fails with a
message naming the requested version and listing the versions the index does
offer, so a stale pin is loud and self-diagnosing. It must never silently fall
back to the index's `latest` key.

Cache as `dist/openserverless_<version>_<arch>.deb`, mirroring the bucket's own
naming. If it already exists, skip the ~3GB download **and skip fetching the
index** — a fully-cached run must not depend on the bucket being reachable.
Download to a `.part` file and move into place so an interrupted download never
leaves a truncated cache entry.

Resolve the index without `jq`. `ensure_deb` runs on the HOST and, on both the
macOS and native-Linux paths, *before* `ensure_jq` — which in any case installs jq
in the VM, so it cannot help a Mac parse the index. The index is a fixed,
generated shape, so a targeted `sed` over the flattened text is sufficient and
keeps the host dependency-free.

Two separate version files, deliberately not conflated:

- `openserverless.txt` — the **cluster package** version (`0.1.0+f1553b`). Selects
  what gets installed. Hard pin, no fallback.
- `version.txt` — the **Trustant app** release identity in tagged form (`v0.4.0`),
  the single source shared with `build.sh`/`hotfix.sh`/`run.sh`; `TRUSTANT_VERSION`
  strips the leading `v`. It must not hold a second, hand-maintained copy. It no
  longer names the cached deb, and a missing value only warns.

Keep `--retry` on the download.

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

ssh.sh connects as trustant@<ip> with the Lima identity, but the package creates
the 'trustant' user without any authorized_keys. Append the Lima pubkey(s) to
/home/trustant/.ssh/authorized_keys during install.

### ssh.sh has three access modes

`ssh.sh` is the single user-facing entry point into the VM, and it takes a mode
flag because there are three distinct places to land: your own development
files, the `trustant` user's installation, and the container running inside it.

- `-d` — **development access**. Logs in as the mirrored guest user (`$USER`)
  and `cd`s to `$PWD`, so the shell opens on your development files at the same
  path as on the host.
- `-p` — **production access**. Logs in as `trustant`, the user that owns the
  Trustant folder and the deployed installation. No `cd`; you land in that
  user's home.
- `-i` — **image access**. Logs in as `trustant` and immediately hops one level
  further in, into the running container:
  `sudo k3s kubectl -n openserverless exec -ti trustant-0 -c trustant -- bash`.
  This is the shell for inspecting the deployed image itself — the
  `supervisord`-managed processes, `/usr/local/bin/trustant`, the packaged
  toolchain — as opposed to the VM hosting it.
- `-h` — prints the usage above.

**Called with no arguments at all, `ssh.sh` prints the help and exits** rather
than opening a shell — the modes are not interchangeable, so which one you want
is always stated. `./ssh.sh -d` is the interactive development shell.

Extra arguments after the flag are run as a command instead of opening an
interactive shell — under `-i` they run inside the container. The flag is
optional and dev is the fallback mode, so the existing flagless command
invocations (`./ssh.sh ./setup.sh`, `./ssh.sh ./run.sh`, `./ssh.sh
./screenshot.sh`) keep their previous meaning.

### start.sh must never call ./ssh.sh

`ssh.sh` is the **user-facing** wrapper: with no command it always ends in an
interactive shell. `start.sh` once probed reachability with `./ssh.sh true` — the
argument was ignored, so the probe opened a shell and blocked the rest of the
run. `./start.sh -v` therefore never reached the step that opens VS Code, and the
user was dropped into a VM shell instead, with no error to explain it.

Reachability is probed by `probe_ssh` calling `ssh` directly with the identity in
the support dir, under `BatchMode=yes` so a missing or unauthorized key fails
instead of prompting for a password and hanging an unattended start.

It deliberately does **not** use `run_guest`/`limactl shell` either. What has to
be proven is that *ssh with that identity* works, because that is the path VS
Code Remote-SSH takes; `limactl` would succeed even when ssh could not connect,
and the failure would surface later as a Remote-SSH error with no context.

## Mount the current folder

Mount the folder start.sh runs from (`$(pwd)`) into the VM at the SAME path via a
writable virtiofs mount in the Lima config (`mounts: [{location, mountPoint, writable:true}]`).
Same path host-and-guest so absolute paths line up on both sides.

## Mirror the current user into the VM

Also create a guest account matching the current macOS user (`id -un`) with the
same UID (`id -u`), so files under the virtiofs mount keep the host's ownership
inside the VM. Give them passwordless sudo (`/etc/sudoers.d/90-<user>`) and copy
trustant's authorized_keys across so `ssh <user>@<ip>` works too.

Idempotent and re-run on every start (not only fresh installs), since an existing
VM may predate the user. Skip when the host user is already `trustant` or `root`;
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

- ~/Library/Application Support/Trustant/current.ip   -> <ip>
- ~/Library/Application Support/Trustant/apihost      -> http://<ip>.nip.io:8080
- ~/Library/Application Support/Trustant/id_ed25519   -> copy of the limactl key (~/.lima/_config/user)

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
~/Library/Application Support/Trustant/apihost, so echo that same string.

## Host-rewrite reverse proxy (in k3s)

traefik's ingresses only match the *.miniops.me hostnames, but from the macOS
host you reach the VM by IP. So deploy (idempotently, via kubectl apply) an nginx
reverse proxy into k3s, in the openserverless namespace, as a LoadBalancer Service on
:8080 that rewrites the Host header and forwards to traefik's ClusterIP:

- http://<apihost>            (Host: <ip> or <ip>.nip.io)        -> Host: miniops.me
- http://<host>.<apihost>     (Host: <host>.<ip>.nip.io)         -> Host: <host>.miniops.me

Reachable from the host at http://<ip>:8080 and http://<host>.<ip>.nip.io:8080.
Port 8080 is used (not 80) so traefik keeps owning the node's :80 untouched, and
8080 is outside the package's :80/:443/:6443 firewall DROP. The proxy is
re-applied on every run so it tracks the current VM IP.

After the `rollout restart` that picks up a changed ConfigMap, and **before**
waiting for the rollout, delete the proxy pods with `--force --grace-period=0
--ignore-not-found`. Under WSL the old pod can stay in `Terminating`
indefinitely — its sandbox teardown never completes against that kernel — and
`rollout status` then blocks on `1 old replicas are pending termination` until
it times out, aborting a run at a step that is otherwise idempotent. Deleting
lets the Deployment recreate the pod, so the wait only ever tracks a fresh
ReplicaSet; the wait itself allows 300s. This is unconditional rather than
gated on WSL — on macOS and native Linux the pods terminate immediately and the
delete costs nothing — and a rollout that still cannot converge remains a hard
failure, because the apihost genuinely does not work without this proxy.

## CPU ollama in the VM

Install ollama as a host process inside the VM (not a pod), pinned to the
image's OLLAMA_VERSION (the `ARG OLLAMA_VERSION=` line in image/Dockerfile), and
enable its systemd service so it serves on localhost:11434 — the app's
OLLAMA_ENDPOINT. Apple's vz gives the Linux guest no GPU passthrough, so this is
CPU-only; that's fine because the app mostly uses cloud models. Idempotent: skip
the install when ollama is already present at the pinned version. Runs on every
start (in the finish path), so an existing VM gets ollama too.

Cache the ~1.5GB release tarball on the HOST under `dist/`, next to the `.deb`
and for the same reason: `dist/` outlives the VM, so `./start.sh -k` followed by
a fresh start reinstalls from disk instead of re-downloading. Upstream's
`install.sh` always re-downloads and takes no local artifact, so the cached path
does the work itself — fetch
`https://github.com/ollama/ollama/releases/download/v<version>/ollama-linux-<arch>.tar.zst`
to `dist/ollama-<version>-linux-<arch>.tar.zst` via a `.part` temp file (so an
interrupted download leaves no truncated entry behind), then extract it into
`/usr/local` (`bin/ollama` + `lib/ollama/*`, clearing any stale `lib/ollama`
first) and write the `ollama` user and systemd unit that upstream would have
created. The guest reads the cache entry straight off the mount — same path in
the VM as on the host — so nothing is copied in. An existing cache entry is
reused as-is; the download runs only when the file is absent.

`OLLAMA_VERSION` is a plain variable near the top of `start.sh`, defaulting to
the image's `ARG OLLAMA_VERSION=` and overridable from the environment. It names
the cache entry, so changing it downloads that version once and leaves the
previous file in `dist/`. Anything that goes wrong with the cache — empty
version, unsupported arch, failed download — falls back to the upstream
installer, so the install never depends on the cache working. One consequence of
installing from the tarball: the upstream installer's GPU handling is skipped,
so on a native Linux host with a GPU the fallback path is what would set up
CUDA/ROCm drivers. In the VM this changes nothing — it is CPU-only either way.

## Pinned kubefwd in the VM

Install `kubefwd` 1.25.16 as `/usr/local/bin/kubefwd` on every finish path, so
both new and reused `trudev` VMs satisfy repository-root `run.sh`. Select the
official Linux archive for `x86_64` or `arm64`, verify its pinned SHA-256 before
installation, install atomically with mode `0755`, and verify the installed
version. Unsupported architectures, checksum failures, missing binaries, or
version mismatches abort provisioning with an actionable error. This
provisioning must not modify resolver configuration; `run.sh` owns the
forwarder's process lifetime.

## jq in the VM

Install `jq` with `apt-get install jq` during provisioning on every finish path,
alongside the other apt-installed tooling. It is a hard prerequisite of the
OpenWhisk wait below, which parses `/api/info`, so it must be in place before
that check runs rather than being discovered missing by it. Idempotent: skip when
`jq` already resolves.

## Removing the obsolete OPS_BRANCH / OPS_REPO exports

`ops` now resolves its task source from the binary itself: the installer
`n7s.co/get-ops-tru` pins `trustable-ai/openserverless-task`, and a clean `ops
-info` reports that repo with an empty `OPS_BRANCH`. Nothing needs to declare
the source in the environment any more.

`OPS_BRANCH` and `OPS_REPO` are therefore **obsolete, and actively harmful**:
the binary still lets both variables override its wired-in default, so any
leftover export silently points `ops` at the wrong fork. They must not be set —
not in `image/Dockerfile`, not in `/etc/environment`, not in a user shell.

Earlier versions of `start.sh` wrote a delimited managed block
(`# >>> trustant ops env >>>` … `# <<< trustant ops env <<<`) into
`~/.profile` and `~/.bashrc` of both the mirrored dev user and the `trustant`
account. That block is what pinned machines to the old `nuvolaris/bestia` fork.

`start.sh` therefore **strips that block** from both rc files of both accounts,
on every finish path, and never writes a replacement. Stripping is required
rather than merely dropping the writer: the block was rewritten whole on every
run, so existing machines carry it and nothing else would ever remove it.

Reuse the same account and file discovery the block itself used — resolve each
home from `getent passwd` rather than assuming `/home/<user>` (Lima gives the
mirrored user a suffixed home such as `/home/msciab.guest`), skip an account
that does not exist, skip an rc file that does not exist, and leave ownership
unchanged. Removal is idempotent and silent when there is no block to remove.

`image/env` must not carry them either: it is COPYed in as the container's own
`.env`, so a value left there reaches every deployed pod.

A pre-existing `/etc/environment` inside an already-built image is **not**
reachable from here; it clears on the next image build.

## Host-rewrite proxy catch-all

After `setup.sh` completes, on every finish path, `start.sh` runs the repository's
own [`./proxy.sh`](17-proxy.md) -- **not** `ops truinst proxy install`. The
configuration then lives in this repo, where it is reviewable and versioned with
the code it fronts, instead of inside the plugin.

It still runs after `setup.sh` (step 8 writes `~/.ops/tmp/kubeconfig`) and after
the OpenWhisk wait, not before: the surrounding steps depend on the toolchain
and kubeconfig `setup.sh` provides.

It runs as the mirrored user in the mounted repo dir, the same way `setup.sh` is
invoked, so the script executed is the worktree's own copy. Failure is
**fatal**: without the catch-all the app is unreachable through the reverse
proxy, and warning-only would defer that failure to the user. `proxy.sh`
regenerates its site file in full on every run, so it is idempotent and needs no
extra guard here.

## Waiting for OpenWhisk

A ready k3s API and a present `openserverless` namespace do not mean OpenWhisk serves
requests — the controller comes up minutes after the cluster does. So **before**
running `setup.sh`, on both hosts, `start.sh` blocks until the apihost is
actually usable: `setup.sh` step 7 curls the apihost and would otherwise fail
against a cluster that is merely booting. The check runs from where the cluster
is (inside the VM on macOS, on this host on Linux), in two stages so a failure
names the one that lost:

1. `curl http://miniops.me` answers at all — traefik and the ingress are wired;
2. `curl http://miniops.me/api/info | jq -r .description` returns `OpenWhisk` —
   the controller itself is serving, not merely some other backend reachable
   behind the same ingress.

Each stage retries every 4s for up to 10 minutes and then aborts the run with the
stage that timed out (the second one also reporting the last description seen).

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
already been gated up front (see below). The default final step is `./run.sh`
inside the VM (foreground, `limactl shell` in the mounted repo dir, as the
mirrored user); `./start.sh -v` opens VS Code over Remote-SSH instead, and
`./start.sh -n` finishes without either.

## .env seeding (first step)

Before anything else on either host, `start.sh` ensures a repository-root `.env`
exists, seeding it from `.env.dist` when absent. An existing `.env` is **never**
overwritten — it holds the user's real credentials. A missing `.env.dist` is a
hard failure.

The copy is verbatim. `.env.dist` ships no `<placeholder>` values, so the seeded
file already satisfies `setup.sh` step 1, which hard-fails on any value still in
that shape. As a guard against a placeholder being reintroduced to the template
later, any line matching `KEY=<...>` is reported as a warning naming the
offending lines — surfacing it at the start of the run rather than letting
`setup.sh` abort minutes in.

Note the two `.env` creators differ by design: `setup.sh` generates in-VM values
rooted at the guest `$HOME`, whereas this step only materializes the shipped
template so the file exists from the very start of the run.

## GitHub token gate (second step)

Immediately after `.env` seeding, on both hosts, `start.sh` checks the
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

On a **native Linux host** the preflight and the `gh` install come *before* this
gate and before the git credential helper — see "Ordering on a native host"
below. On macOS they do not: `gh` comes from brew there, and the VM the in-VM
`gh` install targets does not exist yet at this point.

## Re-running when the VM already exists

`./start.sh` is idempotent — an existing VM is not an error:

- Running: skip provisioning/setup, refresh support files, wait until a command
	can be executed in the VM over ssh, then take the selected final step (`run.sh`
	by default, VS Code with `-v`, neither with `-n`).
- Stopped: `limactl start` the existing instance, skip provisioning/setup,
	refresh support files, wait for SSH readiness, then take the same final step.

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
  step below is `apt-get`/`dpkg`, and the OpenServerless package is a `.deb`.
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

## Ordering on a native host

On this path the host *is* the target, so `gh` has to exist before the git
credential helper is wired and before the token gate, not after. The order is:

1. `.env` seeding — host-agnostic, first on both hosts.
2. **preflight** (the checks above).
3. **`gh` via apt.**
4. the git credential helper, which needs `gh` — `gh auth login --with-token`
   authenticates the CLI but installs no helper, so plain `git` still cannot
   read github.com without it.
5. the GitHub token gate.
6. the rest of the native flow, starting with the submodule initialization —
   which clones a **private** submodule over https and fails with
   `could not read Username for 'https://github.com'` when 3 and 4 have not run.

Preflight moves ahead of the `gh` install because that install goes through the
privileged runner, and `sudo -n` is exactly what preflight verifies. Preflight
is pure checks, so hoisting it costs nothing and an unsupported host now fails
before being asked for a token.

These two steps run **once**, here, and not again inside the native finish path.
Both are idempotent, so a duplicate call would be harmless, but a linear
provisioning script should call them where the ordering is visible.

The macOS path is unaffected by all of this.

## Cluster

If `dpkg -l openserverless` does not report `ii`, download and cache the `.deb`
and install it on this machine. The installed-package check names the package
actually being installed (`openserverless`); a host still carrying the older
`trustant` package is therefore treated as uninstalled and gets the new one.

The package must match the host architecture. On Linux, detect it with
`dpkg --print-architecture` — that is what governs whether `apt-get install`
will accept the package, and it stays correct on a multiarch host where `uname`
reports the kernel's architecture. It emits exactly the strings the filenames
use, so:

- arm64 -> `dist/openserverless_<version>_arm64.deb` (index key `arm64`)
- amd64 -> `dist/openserverless_<version>_amd64.deb` (index key `amd64`)

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
- **No authorized_keys grafting** for the package-created `trustant` user. That
  exists so `ssh.sh` can reach the VM; nobody ssh's into the machine they are
  sitting at.

If the package is already installed, skip straight to verification.

Then wait for the local k3s to serve `/readyz` and for the `openserverless` namespace
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
Write, under `${XDG_CONFIG_HOME:-$HOME/.config}/trustant/`:

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
- ollama on `localhost:11434`, pinned to the image's `OLLAMA_VERSION`, from the
  same `dist/` cache (see above). Unlike the VM, a native host is not restricted
  to CPU, but the cached tarball path installs no GPU drivers — only the
  upstream-installer fallback detects CUDA/ROCm;
- the pinned `kubefwd` 1.25.16 with its SHA-256 verification;
- the `gh` and `jq` apt packages.

Then wait for OpenWhisk on `http://miniops.me` (see above), run `./setup.sh`
directly (not through `limactl shell`), verify the pinned `gh` runtime version,
and attempt the warning-only `.ghtoken` login.

## Flags

- `-s` and `-k` are VM lifecycle operations with no native equivalent. They must
  exit non-zero with an explanatory message and must never be reinterpreted as
  "stop or destroy this machine's k3s".
- `-v` is accepted but never opens VS Code on this path (the sources are already
  local); because there is no hop to take, it means "prepare only" and stops
  before the dev loop, like `-n`.
- `-n` finishes after provisioning without starting the dev loop.

## Final step

A plain `./start.sh` finishes by running `./run.sh` on this host, in the
foreground, after `setup.sh` has installed the toolchain — the native
counterpart of the VM path's `run.sh`-inside-the-VM finish, so one command takes
a fresh Ubuntu host to a running dev server. `run.sh` owns `kubefwd` and `air`,
so Ctrl-C is how the dev server is stopped. `-n` and `-v` stop before this step
and print `./run.sh` as the next step instead.

## Summary

Print the apihost, the host-rewrite URL pattern, the ollama endpoint, and a
`vscode` line; when the dev loop is not being started, also print `./run.sh` as
the next step. Omit the ssh, mount, stop, and destroy lines — none of them exist
here.

The `vscode` line prints the command that opens the sources, not a Remote-SSH
hop: on a native host the repo is already local, so connecting is just
`code <repo dir>`. When `code` is not on `PATH`, print that same command as a
hint plus the "Shell Command: Install 'code' command in PATH" pointer, and carry
on. Unlike the macOS path — where a missing `code` is a hard `fail` because
Remote-SSH is the only way in — VS Code is never required here, so its absence
must never abort a run that has otherwise succeeded.

Re-running is fully idempotent: an installed package, a ready cluster, and an
already-provisioned toolchain are all verified rather than redone.

# Windows (WSL2) — `start.ps1`

Windows has no native path in `start.sh`. It is reached instead through
`start.ps1`, a PowerShell 5.1 script at the repository root that stands in for
Lima: it creates the Linux environment, mirrors the user into it, and then runs
`./start.sh` inside it, which takes the native-Linux path (`uname -s` = `Linux`)
described above. Nothing in `start.sh`, `setup.sh` or `run.sh` is
Windows-aware, and none of them may be taught to be.

## What it provisions

- A WSL2 distribution named `trudev`, installed from the `Ubuntu-24.04` image
  with

      wsl --install --distribution Ubuntu-24.04 --name trudev --no-launch

  The **instance** name (`-Distro` to override) and the **image** it is
  installed from (`-BaseDistro` to override) are two separate identities. The
  dedicated instance name mirrors Lima's `VM_NAME` on macOS and exists for the
  same reason: the dev environment can never be confused with anything else on
  the machine, this script can never adopt — and overwrite the `/etc/wsl.conf`
  of — a general-purpose Ubuntu the user installed themselves, and `-k` can
  never unregister a distribution the user created. `--no-launch` skips the
  interactive first-run account wizard: the account is created below with the
  name and rights we want. A WSL1 distribution is converted to WSL2 — WSL1 has
  no kernel and cannot run k3s.
- `%USERPROFILE%\.wslconfig` with the sizing `start.sh` gives the Lima VM
  (`memory=8GB`, `processors=4`, `swap=8GB`). This file is global to every
  distribution on the machine, so it is written only when absent; an existing
  one is reported and left alone.
- The mirrored user: the Windows account name folded to a valid Linux user name
  (`-User` to override), UID 1000 when free, in the `sudo` group, with
  `/etc/sudoers.d/90-<user>` granting `NOPASSWD:ALL`. This is the same mirroring
  the Lima path does for the same reason — the mounted worktree keeps one
  consistent owner — and `preflight_native`, `setup.sh` and `run.sh` all require
  `sudo -n`.
- `/etc/wsl.conf`:
  - `[boot] systemd=true` — k3s is a systemd service and `preflight_native`
    refuses a distribution without it.
  - `[user] default=<user>` — so a bare `wsl -d <distro>` lands as that user.
  - `[automount] options="metadata"` — keeps unix modes on `/mnt/<drive>`, so
    the scripts in the mounted worktree stay executable.
  - `[interop] appendWindowsPath=false` — `setup.sh` installs go, node, uv and
    npm into the distribution; a leaking Windows `PATH` would make `node` and
    `go` resolve to Windows `.exe` files inside Linux builds.
- The base packages the native path shells out to and the WSL image may lack:
  `sudo curl ca-certificates git iproute2 iptables zstd unzip gh`. `iptables` is
  what the package's postinst builds its `:80/:443/:6443` firewall dropin with;
  `zstd` and `unzip` unpack what the flow downloads. `gh` is belt and braces:
  the bootstrap runs as root before `start.sh` is invoked at all, so the
  distribution has the CLI whatever `start.sh`'s internal ordering does — it
  needs it for the git credential helper and for the private submodule clones.
  If the Ubuntu archive ever stops carrying `gh`, `ensure_gh_apt` is what gets
  fixed, not this list; `setup.sh` still owns the pinned-version convergence
  from the release tarball afterwards.

## The mount

There is no copy and no second clone: the worktree stays on the Windows
filesystem and the distribution reaches it through the automount, exactly as the
Lima VM reaches the macOS folder through virtiofs. `start.ps1` resolves the path
with `wslpath` and runs

    wsl -d <distro> -u <user> --cd <path> -- bash ./start.sh

`bash ./start.sh` rather than `./start.sh`, because the exec bit on a
Windows-hosted file depends on the automount options and this does not care.

### LF line endings are load-bearing

bash cannot run a CRLF script — it fails with
``syntax error near unexpected token $'in\r'`` — and a `.env` copied from a CRLF
`.env.dist` carries a trailing carriage return into every value. A Windows
checkout with `core.autocrlf=true` produces exactly that, so the repository
ships a `.gitattributes` with `* text=auto eol=lf` (plus explicit `binary` for
image types). `start.ps1` re-checks `start.sh` for CR bytes before provisioning
anything and aborts with the renormalization commands if any are found.

That covers this repository and nothing else: **`.gitattributes` does not cross a
submodule boundary.** Each submodule is its own repository with its own working
tree and its own attributes, so Git for Windows' system-config
`core.autocrlf=true` still checks them out as CRLF. The symptom is not the usual
`syntax error near unexpected token` — a CRLF *shebang* makes bash report the
interpreter as missing:

    ./setup.sh: line 686: ./setup.sh: cannot execute: required file not found

`ensure_source_submodules` therefore closes the gap on both hosts:

- the `git submodule update --init` runs under `-c core.autocrlf=false -c
  core.eol=lf`, which git propagates to the per-submodule clone and checkout, so
  a fresh checkout is LF from the start;
- afterwards `normalize_submodule_eol` walks every initialized submodule
  recursively, persists `core.autocrlf=false` / `core.eol=lf` in that
  submodule's own config, and repairs a working tree that is still CRLF
  (`git rm --cached -r .` + `git reset --hard`).

Only files stored as LF but checked out as CRLF count as damage — a submodule
that genuinely commits CRLF is left alone. Repair is skipped, with an actionable
warning rather than a hard failure, when the submodule has uncommitted changes:
the fix must never discard the user's work. The whole step is a no-op on macOS
and Linux, where `autocrlf` is off to begin with.

## The final step

`start.ps1` owns the finish, exactly as the macOS half of `start.sh` does, and
for a reason that cannot be worked around from the shell side: WSL reaches
`start.sh` through its **native-Linux** path, and that path deliberately
disables both final steps (`if $NATIVE_LINUX; then OPEN_VSCODE=0; RUN_APP=0;
fi`) because a native host has no VM to shell into. `-v`/`-n` handed to
`start.sh` from here would therefore be swallowed. The choice is made in
`start.ps1` after `start.sh` returns 0, and `start.sh` stays Windows-unaware:

- **default** — `./run.sh` inside the distribution, in the foreground, on the
  mount, as the mirrored user, with **no arguments**:
  `wsl -d <distro> -u <user> --cd <path> -- bash ./run.sh`. `bash ./run.sh` for
  the same reason `start.sh` is invoked that way. It stays in the foreground
  because `run.sh` owns `kubefwd` and `air`, so Ctrl-C is how the dev server is
  stopped; its exit code is the script's exit code.
- **`-v`** — open VS Code on the mounted worktree instead, over the **WSL remote
  authority**: `code --remote wsl+<distro> <linux path>`. Never `ssh-remote+` —
  there is no ssh hop here. The launch comes from Windows rather than a `code .`
  typed inside the distribution, because `[interop] appendWindowsPath=false`
  keeps `code` off the PATH in there. The user then runs `./run.sh` in the
  integrated terminal, which is already the mirrored user's bash on the mount.
- **`-n`** — finish with neither.

`code` must be on the **Windows** PATH for `-v`, with the WSL extension
(`ms-vscode-remote.remote-wsl`) installed. This is checked **before any
provisioning** — after the `-Stop`/`-Destroy` branches, which must never require
an editor — so a run that downloads a ~3GB package cannot end by discovering
the editor is missing. A `code --remote` that fails afterwards aborts naming the
extension.

`-NoStart` takes precedence over both: nothing has been provisioned to finish.

## Verification before handing over to start.sh

Before anything is provisioned, after the `wsl --version` gate and after the
`-Stop`/`-Destroy` branches:

- **`wsl --install --name` must be supported.** The dedicated instance name only
  exists if `wsl.exe` can be told a name at install time, and older builds
  cannot. `wsl --install --help` is captured and matched for `--name` on the
  **help text, never on the exit code** — on current builds both `wsl --help`
  and `wsl --install --help` print correct help and still exit non-zero. On
  absence the script fails with the actionable remedy, in the shape of the WSL
  gate above it: *this WSL is too old: `wsl --install --name` is not supported.
  Run "wsl --update" in an Administrator terminal, then re-run this script.* The
  check is **skipped when the distribution already exists** — a reused `trudev`
  never calls `--install`, and an old `wsl.exe` must not block a run that would
  not have needed the flag. It sits after `-Stop`/`-Destroy` for the same reason
  the `-v` preflight does: tearing a distribution down never installs one.
- **A distribution left behind by the old default is reported, never touched.**
  If `Ubuntu-24.04` is registered and `-Distro` is still the `trudev` default,
  the script warns that earlier versions of `start.ps1` used that name for the
  Trustant dev distro and prints `.\start.ps1 -k -Distro Ubuntu-24.04` as the
  way to remove it. There is no detection heuristic, no migration and no
  automatic deletion: the script must never unregister a distribution the user
  did not name.

After writing `/etc/wsl.conf` the distribution is restarted (`wsl --terminate`,
or `wsl --shutdown` when `.wslconfig` was just created) and then, in order:
systemd is waited for (`/run/systemd/system`, up to 60s), `sudo -n true` is
confirmed for the mirrored user, and the mount is confirmed to expose
`start.sh`. Each failure names the specific fix. This is deliberate: every one
of these is something `preflight_native` would otherwise reject much later, in
the middle of a run.

## Flags

- `-v` (`-VSCode`) — open VS Code on the mount instead of running `./run.sh`
  (see "The final step"). Given together with `-n`, `-v` wins, with a warning.
- `-n` (`-NoRun`) — finish after `start.sh`, running neither `./run.sh` nor
  VS Code. The short forms match `start.sh`'s. The script has no
  `[CmdletBinding()]`, so there are no common parameters and `-v` cannot collide
  with `-Verbose`; `-n` binds by exact alias match, ahead of any prefix match
  against `-NoStart`.
- `-NoStart` — provision only; print the `wsl … -- ./start.sh` command instead
  of running it.
- `-s` (`-Stop`) — `wsl --terminate <distro>`. The counterpart of
  `./start.sh -s`: keeps the distribution, a later run restarts it with no
  reinstall.
- `-k` (`-Destroy`) — `wsl --unregister <distro>`, the counterpart of
  `./start.sh -k`. It removes the distribution and its Linux filesystem with it,
  so it requires typing the distribution name to confirm. The repository is on
  Windows and is untouched.
- `-User` — override the mirrored user name.
- `-Distro` — override the **instance** name (default `trudev`).
- `-BaseDistro` — override the **image** the instance is installed from
  (default `Ubuntu-24.04`).

Every flag carries `start.sh`'s short form as an alias — `-v`, `-n`, `-s`, `-k`
— so the same muscle memory works on both hosts. They bind by exact alias match,
which PowerShell resolves ahead of prefix matching, so `-n` is `-NoRun` rather
than an ambiguous prefix of `-NoStart`, and `-s` is `-Stop`.

`-s` and `-k` act only on the WSL distribution and must never be extended to
touch the Windows host. Both are handled before the `-v` preflight and before
any provisioning: tearing a distribution down must not require an editor, a
mount, or even a repository.

## Constraints on the file itself

`start.ps1` must stay pure ASCII: Windows PowerShell 5.1 reads a BOM-less `.ps1`
as ANSI, so a non-ASCII character would be mangled. The bootstrap payload is
written to a temp file and executed with `bash <file>` rather than piped in,
because piping from PowerShell rewrites the line endings and bash refuses a
CRLF script. `$env:WSL_UTF8` is set before any `wsl --list` parse, otherwise the
output is UTF-16LE and every comparison fails.

## What it does not do

No ssh key, no `~/.ssh/config`, no VS Code **Remote-SSH**: the sources are on
the Windows filesystem, so `-v` attaches VS Code to the `wsl+<distro>` remote
instead of an `ssh-remote+` one, and nothing on this path ever ssh's anywhere.
Reachability from the Windows browser
is the WSL2 default — the distribution's `eth0` address is routable from the
host, and the apihost `start.sh` publishes is
`http://<wsl-ip>.nip.io:8080`, on the host-rewrite proxy port, which the
package's `:80/:443/:6443` firewall dropin does not cover.
