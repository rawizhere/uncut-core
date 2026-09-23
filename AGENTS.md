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

    # 1. Firewall: SSH rule first, or you can lock yourself out.
    ufw allow <ssh-port>/tcp && ufw allow 80/tcp && \
      ufw allow 443/tcp && ufw allow 443/udp && ufw --force enable
    # port hopping for Hysteria2 (also handled by install.sh on fresh deploys)
    ufw allow 20000:30000/udp

    # 2. Previous release on the box? Remove all of it:
    docker compose down || docker rm -f uncut-node
    docker image prune -af
    rm -rf /opt/uncut                 # irreversible: all client state
    systemctl stop fail2ban; rm -f /etc/fail2ban/jail.d/uncut-* /etc/fail2ban/filter.d/uncut-*
    grep stream-conf.d /etc/nginx/nginx.conf   # host nginx must be unpatched

    # 3. Install:
    curl -fsSL https://raw.githubusercontent.com/rawizhere/uncut-core/main/deployments/install.sh \
      | sh -s -- --domain <domain> --email <email> --tag <tag> \
                 --image ghcr.io/rawizhere/uncut-core:latest

    # 4. Verify, stop at the first failure:
    docker ps --format '{{.Names}} {{.Status}}'   # uncut-node, healthy
    docker exec uncut-node raw doctor             # all PASS
    systemctl restart fail2ban                    # jails need the node's log files first
    fail2ban-client status                        # sshd + uncut-dos + uncut-honeypot

`raw doctor` covers dns, ports, certificate, transports and subscriptions.
It cannot see host-level ufw and fail2ban — those two are steps 1 and 4.

    # 5. Clients and the report to the operator:
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
- Hysteria2 port hopping: install.sh sets up the 20000:30000/udp ufw rule and
  the DNAT onto 443 (netfilter-persistent persists it). On hosts installed
  before hopping existed, add both by hand; `mport` in client links only works
  with the DNAT in place.

## Breaking operations

These kill issued client links. Only on explicit operator request:

- `raw rotate-paths` — all transports, every link dies.
- `raw change-domain` — new domain plus a fresh certificate.
- `raw del`, `raw rotate-sub` — one client.
- Reality SNI change, MTProto FakeTLS mask change.
