# AGENTS.md

Operator and navigation guide for `uncut-core`. Local fleet notes (if present
under `docs/`, gitignored): read `docs/fleet.md` before touching live nodes.

## What this is

One Go binary (`raw`) renders nginx, sing-box, and tproxy configs from a local
SQLite store, serves subscriptions and a TUI, and ships in one container.

## Codebase & host layout

- `cmd/uncut/main.go` defines CLI commands and the `breaking()` `--yes` wrapper.
- `internal/config/` defines protocol sets (`DefaultProtocols`), `LoopbackPorts`, and settings.
- `internal/db/` holds the SQLite store and numbered schema migrations.
- `internal/core/` manages client lifecycles, AmneziaWG key and IP allocation, and subscription generation.
- `internal/setup/` handles bootstrap, settings loading, `RebuildAll`, and daemon reload triggers.
- `internal/nodeops/` runs the daemon loop, `raw doctor`, and command handlers.
- `internal/nginx/` and `internal/singbox/` render service configs.
- `internal/supervisor/` manages child processes (`sing-box`, `nginx`, `mtproto-proxy`, `tproxy-server`, `coredns`).
- `internal/transport/` and `internal/links/` generate random paths and Telegram proxy links.
- `internal/tui/` implements the interactive `raw menu`.
- `deployments/` contains `Dockerfile`, `docker-compose.yml`, `install.sh`, `host.sh`, `start.sh`, `logrotate.conf`, and `fail2ban/`.
- Live node paths: compose dir `/opt/uncut/uncut-core/deployments` (`.env`), volumes `/opt/uncut/data` (`uncut.db`, `logs/`, backups) and `/opt/uncut/install` (`config.json`, `certs/`, optional `bin/sing-box`), and container nginx stream `/etc/nginx/stream-conf.d/uncut-stream.conf`.

## Local checks before push

```sh
gofmt -l ./cmd ./internal
go mod tidy -diff
go build ./... && go vet ./...
golangci-lint run ./...
sh -n deployments/*.sh
```

This repository intentionally has no unit tests.

## Deploying a node on a server

Ask the operator first. Do not start without answers:

1. SSH access: user, host, port.
2. Domain that already has a DNS-only A record pointing to that server.
   A Cloudflare-proxied record breaks the transports.
3. Email for Let's Encrypt.
4. Node tag, a short label appended to client links (default: de).
5. Fresh server, or a previous release on it (clean it first, see below).
6. MTProto FakeTLS mask domain, if wanted; pick a well-known non-Russian site.

Then run these steps in order:

    # 1. Clean any previous release on the host:
    docker rm -f uncut-node 2>/dev/null || true
    docker image prune -af
    rm -rf /opt/uncut                 # irreversible: removes all client state
    systemctl stop fail2ban 2>/dev/null || true
    rm -f /etc/fail2ban/jail.d/uncut-* /etc/fail2ban/filter.d/uncut-*

    # 2. Install (runs deployments/host.sh: ufw, port hopping, fail2ban, time sync):
    curl -fsSL https://raw.githubusercontent.com/rawizhere/uncut-core/main/deployments/install.sh \
      | sh -s -- --domain <domain> --email <email> --tag <tag> \
                 --image ghcr.io/rawizhere/uncut-core:latest

    # 3. Verify, stopping at the first failure:
    docker ps --format '{{.Names}} {{.Status}}'   # uncut-node, healthy
    docker exec uncut-node raw doctor             # all PASS
    ufw status                                    # ssh, 80/tcp, 443/tcp, 443/udp, AWGPORT/udp, HOP/udp
    fail2ban-client status                        # sshd + uncut-dos + uncut-honeypot

`raw doctor` (`--json` for a machine-readable list) checks the daemon, configs,
DNS (an AAAA record fails because the node is IPv4-only), ports, certificate,
transports, the Telegram bridge, subscriptions, sysctls, and swap. Host-level
`ufw` and `fail2ban` live outside the container, hence those last two checks.

    # 4. Add clients and report URLs to the operator:
    docker exec uncut-node raw add <name>
    docker exec uncut-node raw list --json        # subscription URLs
    docker exec uncut-node raw info               # doh, tg web proxy, mtproto

## Node operations (quick reference)

- Update: run `docker compose pull && docker compose up -d` from
  `/opt/uncut/uncut-core/deployments` on the node. State lives in bind-backed
  volumes, so recreation is safe.
- Container image ships `sing-box-extended` built with `patches/sing-vmess-muxfix.patch`.
  `raw singbox-version <ver>` or TUI installs an unpatched upstream binary to
  `/opt/sing-box/bin/sing-box`, which shadows the image binary. Run
  `raw singbox-version --reset` (or "Use image version" in TUI) to return to the
  patched image binary.
- Protocols: `raw set-protocols --protocols ...` (and TUI `6. Inbound Protocols`)
  toggles active server transports (for example, turning off `vless-ws` and
  `vless-grpc` without code changes).
- Client toggle: `raw disable <name>` and `raw enable <name>` (and TUI
  `Client Details`) turn a client off in `sing-box` and empty its subscription
  while preserving its UUID, AWG keys, and subscription URL.
- Backups: `raw backup` writes `backup_<timestamp>.db` in `--data-dir`;
  `raw restore <file> --yes` restores it and rebuilds configs.
- Change node tag: set a non-empty `TAG=` in `deployments/.env` and recreate the
  container (empty `TAG=` is ignored by setup).
- `DOMAIN=` in `.env` only seeds a fresh node; change domains via
  `raw change-domain --yes`, then update `DOMAIN=` to match.
- Commands update SQLite and ask the daemon to reload; when the daemon is down,
  they report it and changes apply on its next start.
- Re-running `install.sh` rewrites `.env` from flags. Never use it to update.
- Host layer (ufw, `$HOP/udp` redirect onto 443, `AWGPORT/udp`, fail2ban, time
  sync) lives in `deployments/host.sh`. It is idempotent and resets ufw to the
  node rule set. Re-run it after changing ports:
  `curl -fsSL https://raw.githubusercontent.com/rawizhere/uncut-core/main/deployments/host.sh | HOP=<from:to> AWGPORT=<port> sh`.
  Never install `iptables-persistent`, because `apt` removes `ufw` for it.

## Breaking operations

These overwrite or invalidate issued client state and links, and refuse to run
without `--yes`. Run them only on explicit operator request:

- `raw rotate-paths` regenerates all transport paths and invalidates every issued link.
- `raw change-domain` sets a new domain and requests a fresh certificate.
- `raw set-sni` sets a new Reality SNI (`--host` or arg).
- `raw set-mtproto-tls` changes or clears the MTProto FakeTLS mask (`--domain`).
- `raw set-obfs` changes or clears Hysteria2 salamander obfs (`--clear`).
- `raw set-hop-ports` sets a new Hysteria2 UDP hop range (`from:to`); issued `hy2://` links keep the old `mport=` range.
- `raw set-awg-port` sets a new AmneziaWG UDP port and invalidates issued `wireguard://` links.
- `raw del`, `raw rotate-sub` affect one client.
- `raw restore <file>` replaces SQLite settings and clients from a backup.
