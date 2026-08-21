# Repository-root `proxy.sh`

This specification defines the repository-root `proxy.sh`, which installs and
configures nginx as a host-rewriting reverse proxy in front of the Go app.

## Why it exists

[middleware.go](../middleware.go) routes by the **first label** of the request
hostname -- `trustable.`, `opencode.`, `vite.` -- and rejects every other prefix
with a 400 that names the corrected URL (see
[0-preflight.md](0-preflight.md)). The rest of the hostname is not free: the app
expects the canonical `miniops.me` form, so reaching it under a LAN address, a
public DNS name, or a tunnel hostname would otherwise fail.

`proxy.sh` resolves this without changing the app. It terminates the connection
under *any* domain, preserves the first label, and forwards upstream with the
`Host` header rewritten to `<label>.miniops.me`. The backend keeps seeing the
name it expects while the client uses whatever name reaches the machine.

## Platform

Debian/Ubuntu only. The script exits non-zero with a diagnostic when
`/etc/debian_version` is absent, because it installs with `apt-get`. It runs as
root, or re-invokes privileged steps through `sudo` when available; with neither
it exits non-zero rather than emitting a partial configuration.

## Parameters

All are environment overrides with the defaults shown:

| Variable | Default | Meaning |
|---|---|---|
| `LISTEN_PORT` | `8911` | Port nginx listens on, IPv4 and IPv6 |
| `UPSTREAM_HOST` | `127.0.0.1` | Address of the Go app |
| `UPSTREAM_PORT` | `80` | Port of the upstream |
| `TARGET_DOMAIN` | `miniops.me` | Domain substituted into the `Host` header |

The listen port is deliberately **not** 80: that is the upstream, and a proxy
listening on its own upstream would loop. It is also not 8910, which the Go app
owns directly and on which `run.sh` kills leftover processes at startup.

## What it writes

Two managed files, both regenerated in full on every run and both carrying a
header saying so:

- `/etc/nginx/sites-available/trustable-proxy`, symlinked into
  `sites-enabled/` -- the `map` that extracts the label, and the `server` block.
- `/etc/nginx/conf.d/trustable-upgrade.conf` -- the `$connection_upgrade` map.
  It lives in `conf.d` because nginx only defines that variable if mapped, and
  the map must sit in the `http{}` context alongside the site includes.

The label is extracted from `$host` with `~^(?<label>[^.:]+)\.`, and a `default`
branch of `trustable` covers a request whose host has no dot -- a bare
`localhost` still reaches the app rather than dead-ending. Excluding `:` from
the character class keeps an explicit port in the `Host` header from leaking
into the rewritten value.

The proxy must preserve behaviour the app depends on:

- **WebSockets** -- `/api/terminal` ([12-terminal.md](12-terminal.md)) and Vite
  HMR both upgrade, so `proxy_http_version 1.1` plus the `Upgrade` and
  `Connection` headers are required.
- **Large and slow bodies** -- repo zip uploads ([2-repo.md](2-repo.md)) and
  streamed AI responses. `client_max_body_size 0` and one-hour read/send
  timeouts; `proxy_buffering off` so streamed output is not held back.
- **`X-Forwarded-Host`** carries the name the client actually used, since `Host`
  no longer does.

## Ordering

The configuration is validated with `nginx -t` before nginx is reloaded, so a
bad render never takes the running proxy down. A already-active nginx is
reloaded; an inactive one is enabled and started, falling back to a bare `nginx`
invocation where systemd is unavailable (a container).

## Verification

The rewrite is observable end to end, and this is the check to run:

```
curl -s -H 'Host: trustable.example.com' http://127.0.0.1:8911/api/version
```

Against an echoing upstream, the mapping holds for every shape:
`trustable.example.com`, `vite.<ip>.nip.io`, and a multi-label
`opencode.foo.bar.baz` all arrive as `<label>.miniops.me`, a bare `localhost`
arrives as `trustable.miniops.me`, and an explicit `:8911` in the request's
`Host` is stripped rather than carried through.
