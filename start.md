Write a start.sh that provisions a local Trustable VM with Lima and wires it up
so the rest of the tooling (ssh.sh, setup.sh, build.sh, publish.sh) finds it in
~/Library/Application Support/Trustable/ — the same place the macOS app writes.

## Download + cache the package (host side, before booting the VM)

Pick the deb for the HOST arch and cache it under dist/:

- arm (Apple Silicon): curl -JLO landing2.nuvolaris.org/api/my/v1/download/linux-arm  -> trustable_<version>_arm64.deb
- intel:               curl -JLO landing2.nuvolaris.org/api/my/v1/download/linux-amd  -> trustable_<version>_amd64.deb

Cache as dist/trustable_<version>_<arch>.deb. If it already exists, skip the
~3.6GB download. Download to a .part file and move into place so an interrupted
download never leaves a truncated cache entry.

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

## k3s API cert (tls-san)

setup.sh extracts k3s.yaml and rewrites `server: https://127.0.0.1:6443` ->
`https://<ip>:6443` into ~/.ops/tmp/kubeconfig. But k3s's serving cert lists only
the node IP (eth0/vzNAT) + 127.0.0.1 — NOT the lima0 IP the host connects to — so
host-side kubectl/ops would fail TLS verification (cert valid for <node-ip>, not
<lima0-ip>). Add the lima0 IP to k3s's tls-san (write /etc/rancher/k3s/config.yaml,
drop the serving cert + dynamic-cert.json, restart k3s to reissue). Idempotent:
skip if the live cert already covers the IP. No change needed in setup.sh.

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

## Re-running when the VM already exists

`./start.sh` is idempotent — an existing VM is not an error:

- Running: re-read the IP and refresh current.ip/apihost/id_ed25519, then exit.
- Stopped: `limactl start` the existing instance (no reinstall), then refresh the files.

Use `./start.sh -k` first only when you want a clean rebuild.

---
./start.sh -k stops and deletes the VM.
