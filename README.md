# uncut-core

Stealth VPN node in one container: a relay core behind nginx on a single
host, several transports sharing TCP 443, a Telegram proxy bridge, client
subscriptions and self-renewing TLS.

Handing this repository to an agent? Point it at AGENTS.md first.

The node is the whole program. There is no control plane — state lives in one
SQLite file on the box and everything is driven from `raw` on that box.

## How it fits together

The firewall opens `80`, `443/tcp` and `443/udp` (plus SSH); everything
else below is loopback-only:

| Port | Owner | What |
|------|-------|------|
| 80/tcp | nginx | ACME HTTP-01 challenge; everything else `301` to https |
| 443/tcp | nginx `stream` | split by TLS SNI |
| 443/udp | sing-box | UDP relay |
| 8443/tcp | sing-box | TLS relay inbound; reachable only through the 443 SNI split |

nginx has to sit in front because sing-box cannot demultiplex one TCP port
between several inbounds. The splitter routes the relay SNI to
`127.0.0.1:8443` and everything else to `127.0.0.1:8442`, where the node's own
domain is served and any other SNI is rejected with `ssl_reject_handshake on;`.

The relay inbounds behind nginx are loopback-only: 10001–10004
(transports) and 8443 (TLS relay); CoreDNS (DoH) 3053.

The node also serves a **private DoH endpoint** at
`https://<domain><transport-random-path>/dns-query` (printed by `raw info`,
same random secret family as the subscription URLs). It is the operator's
reserve resolver: TLS terminates on the already-exposed 443, wrong paths are
404 (banned by fail2ban), and the upstream chain round-robins plain 53
foreign resolvers, so a wholesale ban of popular public DoH does not kill
it as long as plain DNS reaches them.

The node publishes nothing: no landing page, no health endpoint, no API
document; an unknown path gets nginx's stock 404.

## Deploy

One command deploys the whole node — installs docker if missing, fetches the
repository, writes `.env` from the flags, starts compose and waits for the
process probe:

```bash
curl -fsSL https://raw.githubusercontent.com/rawizhere/uncut-core/main/deployments/install.sh \
  | sudo sh -s -- --domain node.example.com --email admin@example.com
# optional: --tag de --tz Europe/Moscow
```

Manual path, when you want to read every step:

```bash
cp deployments/.env.example deployments/.env
$EDITOR deployments/.env          # DOMAIN and EMAIL are the only required values
docker compose -f deployments/docker-compose.yml up -d
```

`DOMAIN` must already have an A record pointing at the host, DNS-only — a
Cloudflare-proxied record breaks the transports. Ports 80, 443/tcp and
443/udp have to be reachable from the internet for the certificate and the
transports.

On first boot the node generates its relay keys, relay credentials, random
transport paths and proxy secrets, and seeds a client named `admin` (set
`CLIENTS` to a comma-separated list to seed different names).

## Certificates

Issuance runs in the daemon and renews 30 days before expiry. Let's Encrypt is
the only CA; the challenge is HTTP-01, so port 80 must stay open. On a first
boot the nginx render leaves the ssl server out until the first certificate
lands (HTTP-01 needs nginx up; the ssl server needs the cert) — the TLS relay
needs no certificate and works meanwhile. To bring your own certificate instead — a
wildcard, or a private CA — install it directly:

```bash
raw set-cert --cert-base64 "$(base64 -w0 fullchain.pem)" \
               --key-base64  "$(base64 -w0 privkey.pem)"
raw renew-cert        # force a renewal now
```

## Commands

```bash
raw menu              # interactive administration menu
raw daemon            # the supervisor: sing-box, nginx, tproxy
raw add -n phone      # add a client, prints its subscription URL and a QR
raw list              # clients with subscription URLs
raw del -n phone      # remove a client and its subscription file
raw rotate-sub phone  # new subscription token for one client; the old URL dies
raw info              # version, tag, DoH endpoint, transport revision, cert expiry
raw doctor            # health checks: dns, ports, cert, transports, subscriptions
raw rotate-paths      # re-generate random paths; re-issue subscriptions
raw change-domain node2.example.com   # new domain + a fresh certificate
raw sync-ip [--force <ip>]            # pin the detected public IP
```

Every command takes `--data-dir` and `--install-dir`; `info`, `list` and
`add` also take `--json`.

`raw info` reports a **transport revision**: the fingerprint of the paths a
client's links were built from. If it changes, every issued subscription has to
be re-issued.

## Telegram bridge

The node runs a Telegram proxy and a tdesktop-web-proxy bridge behind it. The
bridge page is served at `/?bridge=<capability>`, where the capability is
HMAC-SHA256 over the domain keyed with the node's proxy raw secret — so the
page is unreachable without the secret. `raw menu` prints the link.

## Ops notes

- The container healthcheck curls the loopback bridge and the daemon's own
  `/healthz`; it does not touch the public 443 listener.
- `deployments/install.sh` is the one-command bootstrap the README deploy
  section pipes curl into.
- `deployments/fail2ban` bans on the first 404 from a known probe path;
  `install.sh` installs and enables it on the host (apt systems). The package
  default sshd jail covers SSH brute force; `install.sh` pins its port when
  SSH runs off 22.
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
