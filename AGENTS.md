# AGENTS.md

Facts an agent needs before deploying or reconfiguring a node. The README
explains the project; this file explains installation and configuration.

## What this is

One Go binary (`raw`) renders nginx, sing-box and tproxy configs from a local
SQLite store, runs subscriptions and a TUI, and ships in one container.

## Deploying a node on a server

Ask the operator first. Do not start without answers:

1. SSH access: user, host, port.
2. Domain that already has an A record to that server, DNS-only.
   A Cloudflare-proxied record breaks the transports.
3. Email for Let's Encrypt.
4. Node tag — a short label appended to client links (default: de).
5. Fresh server, or a previous release on it (then clean it first, see below).
6. MTProto FakeTLS mask domain, if wanted; pick a well-known non-Russian site.

Then, in order:

    # 1. Previous release on the box? Remove all of it:
    docker compose down || docker rm -f uncut-node
    docker image prune -af
    rm -rf /opt/uncut                 # irreversible: all client state
    systemctl stop fail2ban; rm -f /etc/fail2ban/jail.d/uncut-* /etc/fail2ban/filter.d/uncut-*
    grep stream-conf.d /etc/nginx/nginx.conf   # host nginx must be unpatched

    # 2. Install (runs deployments/host.sh: ufw, port hopping, fail2ban, time sync):
    curl -fsSL https://raw.githubusercontent.com/rawizhere/uncut-core/main/deployments/install.sh \
      | sh -s -- --domain <domain> --email <email> --tag <tag> \
                 --image ghcr.io/rawizhere/uncut-core:latest

    # 3. Verify, stop at the first failure:
    docker ps --format '{{.Names}} {{.Status}}'   # uncut-node, healthy
    docker exec uncut-node raw doctor             # all PASS
    ufw status                                    # ssh, 80/tcp, 443/tcp, 443/udp, 20000:30000/udp
    fail2ban-client status                        # sshd + uncut-dos + uncut-honeypot

`raw doctor` covers dns, ports, certificate, transports and subscriptions.
It cannot see host-level ufw and fail2ban, hence the last two checks.

    # 4. Clients and the report to the operator:
    docker exec uncut-node raw add -n <name>
    docker exec uncut-node raw list --json        # subscription URLs
    docker exec uncut-node raw info               # doh, tg web proxy, mtproto

## Node operations (quick reference)

- Update: `docker compose pull && docker compose up -d` from `deployments/`
  on the node. State lives in bind-backed volumes; recreation is safe, the
  installed sing-box version survives.
- Change node tag: edit `TAG=` in `deployments/.env`, recreate the container.
  Subscriptions regenerate on boot.
- Re-running install.sh rewrites `.env` from flags — never use it to update.
- fail2ban runs on the host, not in the container.
- Host layer (ufw, the 20000:30000/udp redirect onto 443 for Hysteria2 port
  hopping, fail2ban, time sync) lives in `deployments/host.sh`. It is
  idempotent and resets ufw to the node rule set; re-run it on a live node
  after changing it:
  `curl -fsSL https://raw.githubusercontent.com/rawizhere/uncut-core/main/deployments/host.sh | sh`.
  Never install iptables-persistent: apt removes ufw for it.

## Breaking operations

These kill issued client links. Only on explicit operator request:

- `raw rotate-paths` — all transports, every link dies.
- `raw change-domain` — new domain plus a fresh certificate.
- `raw del`, `raw rotate-sub` — one client.
- Reality SNI change, MTProto FakeTLS mask change.
