# uncut-core

Stealth VPN node in one container: a relay core behind nginx on a single
host, several transports sharing TCP 443, an AmneziaWG (wireguard) endpoint
on its own UDP port, a Telegram proxy bridge, client subscriptions and
self-renewing TLS.

Handing this repository to an agent? Point it at AGENTS.md first.

State lives in one SQLite file on the host, and all configuration changes go
through `raw` on that host.

## How it fits together

The firewall opens `80`, `443/tcp`, `443/udp`, the Hysteria2 UDP hop range
(`$HOP/udp`, default `20000:30000`) redirected onto `443/udp`, and the
AmneziaWG UDP port (`45820` by default) plus SSH. Every other listener stays
on loopback:

| Port | Owner | What |
|------|-------|------|
| 80/tcp | nginx | ACME HTTP-01 challenge; everything else `301` to https |
| 443/tcp | nginx `stream` | split by TLS SNI with `proxy_protocol` |
| 443/udp | sing-box | Hysteria2 UDP relay; unauthenticated HTTP/3 probes get the same stock 404 as TCP (masquerade) |
| 45820/udp | sing-box | AmneziaWG (wireguard) relay; the default port, change with `raw set-awg-port` |
| 8443/tcp | sing-box | VLESS Reality inbound; reachable through `127.0.0.1:18443` stripper from the 443 SNI split |

nginx sits in front because sing-box cannot demultiplex one TCP port between
several inbounds. The splitter routes the Reality SNI via the `127.0.0.1:18443`
`proxy_protocol` stripper to `127.0.0.1:8443`, the optional MTProto FakeTLS
mask domain via `127.0.0.1:12399` stripper to `127.0.0.1:2399`, and everything
else to `127.0.0.1:8442`, where the node's own domain is served and any other
SNI is rejected with `ssl_reject_handshake on;`.

The remaining loopback listeners are `10001-10004` (VLESS transports), `8440`
(non-proxy_protocol HTTPS fallback for self-SNI Reality), `8441` (Hysteria2
masquerade 404), `2398` (plain `mtproto-proxy` backend for `tproxy-server` on
`8080`), and `3053` (CoreDNS DoH).

The node also serves a **private DoH endpoint** at
`https://<domain><transport-random-path>/dns-query` (printed by `raw info`,
same random secret family as the subscription URLs). It is the operator's
reserve resolver: TLS terminates on the already-exposed 443, wrong paths are
404 (banned by fail2ban), and the upstream chain round-robins plain 53
foreign resolvers, so a wholesale ban of popular public DoH does not kill
it as long as plain DNS reaches them.

The node publishes nothing: no landing page, no health endpoint, no API
document; an unknown path gets nginx's stock 404. Unauthenticated HTTP/3
probes on UDP 443 get the same stock 404, through the Hysteria2 masquerade;
the masquerade is not rendered when a salamander obfs password is set,
because obfs hides the HTTP layer entirely.

## Deploy

One command deploys the node: it installs Docker if missing, fetches the
repository, writes `.env` from the flags, starts Compose, and waits for the
process probe:

```bash
curl -fsSL https://raw.githubusercontent.com/rawizhere/uncut-core/main/deployments/install.sh \
  | sudo sh -s -- --domain node.example.com --email admin@example.com
# optional: --bandwidth 500 --tag de --tz Europe/Moscow --ref main --image <image> --dir /opt/uncut
```

Manual path, when you want to read every step:

```bash
cp deployments/.env.example deployments/.env
$EDITOR deployments/.env          # DOMAIN and EMAIL are the only required values
docker compose -f deployments/docker-compose.yml up -d
```

`DOMAIN` must already have a DNS-only A record pointing at the host. A
Cloudflare-proxied record breaks the transports. Ports 80, 443/tcp, and 443/udp
must be reachable from the internet for the certificate and the transports.

On first boot the node generates its relay keys, relay credentials, random
transport paths and proxy secrets, and seeds a client named `admin` (set
`CLIENTS` to a comma-separated list to seed different names).

## Certificates

Issuance runs in the daemon and renews 30 days before expiry. Let's Encrypt is
the only CA; the challenge is HTTP-01, so port 80 must stay open. On first
boot the nginx render omits the ssl server, and the sing-box render omits the
UDP relay, until the first certificate lands (HTTP-01 needs nginx up; both
need the cert). The TLS relay needs no certificate and works meanwhile. To
install a wildcard or private CA certificate instead:

```bash
raw set-cert --cert-base64 "$(base64 -w0 fullchain.pem)" \
               --key-base64  "$(base64 -w0 privkey.pem)"
raw renew-cert        # force a renewal now
```

## Commands

```bash
raw menu                    # interactive administration menu
raw daemon                  # the supervisor: sing-box, nginx, mtproto-proxy, tproxy, coredns
raw version                 # print the binary version
raw add phone               # add a client, prints its subscription URL
raw list                    # clients with subscription URLs and disabled markers
raw disable phone           # turn a client off without losing UUID, AWG keys or sub token
raw enable phone            # re-enable a disabled client
raw del phone --yes         # remove a client and its subscription file
raw rotate-sub phone --yes  # new subscription token for one client; the old URL dies
raw set-protocols --protocols ...             # set active server transports
raw set-client-protocols phone --protocols ... # explicit per-client allowlist (or --inherit)
raw set-sni example.com --yes                 # set Reality SNI
raw set-mtproto-tls --domain mask.com --yes   # set or clear MTProto FakeTLS mask
raw set-obfs <pass> --yes                     # set Hysteria2 salamander obfs (--clear drops it)
raw set-hop-ports 30000:40000 --yes           # set Hysteria2 UDP hop range (--default resets)
raw set-awg-port 51820 --yes                  # new AmneziaWG UDP port; issued wireguard:// links die
raw hysteria2-bandwidth --up 500 --down 500   # set brutal caps (0 = BBR)
raw singbox-version <ver>                     # install upstream release (--reset restores image muxfix)
raw info                    # version, tag, DoH endpoint, transport revision, cert expiry
raw doctor                  # health checks: daemon, configs, dns, ports, cert, transports, tg bridge, subscriptions
raw rotate-paths --yes      # re-generate random paths; re-issue subscriptions
raw change-domain node2.example.com --yes     # new domain + a fresh certificate
raw sync-ip [--force <ip>]                    # pin the detected public IP
raw backup                  # snapshot SQLite store to --data-dir/backup_<ts>.db
raw restore <file> --yes    # restore SQLite store from backup and rebuild
```

Every command takes `--data-dir` and `--install-dir`; `info`, `list`, `add`,
`disable`, `enable`, `set-protocols`, `set-client-protocols`, `backup`, and
`doctor` also take `--json`. Commands that kill or overwrite issued client
links/state (`del`, `rotate-sub`, `rotate-paths`, `change-domain`, `set-sni`,
`set-mtproto-tls`, `set-obfs`, `set-hop-ports`, `set-awg-port`, `restore`)
refuse to run without `--yes`.

Commands only change the SQLite store and ask the daemon (`POST
127.0.0.1:8088/reload`) to re-render. The daemon checks each config with its
consumer (`sing-box check`, `nginx -t`) before reloading and keeps the
previous one when the check fails. `DOMAIN` in `.env` seeds a fresh node
only; afterwards the domain changes through `raw change-domain`.

`raw info` reports a **transport revision**: the fingerprint of the paths a
client's links were built from. If it changes, every issued subscription has to
be re-issued.

## Telegram bridge

The node runs a Telegram proxy and a tdesktop-web-proxy bridge behind it. The
bridge page is served at `/?bridge=<capability>`, where the capability is
HMAC-SHA256 over the domain keyed with the node's `webproxy_secret`, so the
page is unreachable without the secret and nginx never logs that request. The
optional FakeTLS branch (`raw set-mtproto-tls`) is sing-box's `mtproxy` inbound
with its own `mtproto_raw_secret`, so the two links never share a key. `raw info`
prints both links.

## Ops notes

- The container healthcheck curls the daemon's own `/healthz` (every managed
  process up); it does not touch the public 443 listener.
- `deployments/install.sh` is the one-command bootstrap the README deploy
  section pipes curl into.
- `deployments/host.sh` sets up the host side (ufw, Hysteria2 port hopping,
  fail2ban, time sync); `install.sh` runs it, and it is safe to re-run.
- `deployments/fail2ban` bans on the first 404 from a known probe path. The
  package default sshd jail covers SSH brute force; `host.sh` pins its port
  when SSH runs off 22.
- `deployments/logrotate.conf` rotates the nginx and process logs (daily, 14 kept, compressed).
- Back up `/opt/uncut/data` (SQLite) and `/opt/uncut/install` (the host path of
  the `/opt/sing-box` volume: keys, certs, generated configs). Losing
  `/opt/uncut/install/certs` means re-issuing every certificate.

## Development

```bash
gofmt -l ./cmd ./internal
go build ./... && go vet ./...
deadcode ./...
golangci-lint run ./...
```

This repository carries **no unit tests**, deliberately. Verify a config change
by rendering it and reading it; the running node is checked with
`curl 127.0.0.1:8088/healthz` (aggregate process status) and the daemon logs.
