#!/bin/sh
# Uncut Core node bootstrap: one command deploys the whole node. curl -fsSL https://raw.githubusercontent.com/rawizhere/uncut-core/main/deployments/install.sh \ | sh -s -- --domain node-eu-1.example.com --email admin@example.com Flags: --domain FQDN        required: the node's public domain (DNS A record must point here) --email ADDR         required: ACME account email --tag TAG            optional: label tail on client links (default: de) --tz ZONE            optional: timezone (default: Europe/Moscow) --ref GITREF         optional: branch/tag to fetch (default: main) --image IMAGE        optional: pull a prebuilt image instead of building --dir DIR            optional: install root (default: /opt/uncut)

set -eu

DIR=/opt/uncut
REF=main
IMAGE=
DOMAIN=
EMAIL=
TAG=de
TZ=Europe/Moscow

usage() {
	echo "usage: install.sh --domain FQDN --email ADDR [--tag TAG] [--tz ZONE] [--ref REF] [--image IMAGE] [--dir DIR]" >&2
	exit 2
}

while [ $# -gt 0 ]; do
	case "$1" in
	--domain) DOMAIN=$2; shift 2 ;;
	--email) EMAIL=$2; shift 2 ;;
	--tag) TAG=$2; shift 2 ;;
	--tz) TZ=$2; shift 2 ;;
	--ref) REF=$2; shift 2 ;;
	--image) IMAGE=$2; shift 2 ;;
	--dir) DIR=$2; shift 2 ;;
	--help) usage ;;
	*) usage ;;
	esac
done

[ -n "$DOMAIN" ] || usage
[ -n "$EMAIL" ] || usage

if [ "$(id -u)" != 0 ]; then
	echo "run as root (or pipe to: sudo sh)" >&2
	exit 1
fi

# Docker: install when missing.
if ! command -v docker >/dev/null 2>&1; then
	echo "==> installing docker"
	curl -fsSL https://get.docker.com | sh
fi
if ! docker compose version >/dev/null 2>&1; then
	echo "docker compose plugin missing" >&2
	exit 1
fi

# Hysteria2 port hopping: a UDP range DNATs onto 443; the range is dead weight until sing-box answers there, so it is safe to set up early.
if command -v ufw >/dev/null 2>&1 && ufw status | grep -q "Status: active"; then
	ufw allow 20000:30000/udp >/dev/null
fi
if ! iptables -t nat -C PREROUTING -p udp --dport 20000:30000 -j REDIRECT --to-ports 443 2>/dev/null; then
	iptables -t nat -A PREROUTING -p udp --dport 20000:30000 -j REDIRECT --to-ports 443
fi
if ! command -v netfilter-persistent >/dev/null 2>&1; then
	DEBIAN_FRONTEND=noninteractive apt-get install -y -qq iptables-persistent >/dev/null
fi
netfilter-persistent save >/dev/null

# The compose bind volumes need the device dirs before the first up.
mkdir -p "$DIR/data" "$DIR/install"

# Fetch the repository that carries the compose file and the build context.
SRC="$DIR/uncut-core"
if [ -d "$SRC/.git" ]; then
	git -C "$SRC" fetch --depth 1 origin "$REF"
	git -C "$SRC" checkout --detach FETCH_HEAD
else
	git clone --depth 1 --branch "$REF" https://github.com/rawizhere/uncut-core "$SRC"
fi

# fail2ban on the host: honeypot bans (one probe 404 -> 24h) and nginx rate-limit bans. apt-based systems only; jails live in deployments/fail2ban.
if command -v apt-get >/dev/null 2>&1; then
	echo "==> installing fail2ban"
	apt-get update -qq
	DEBIAN_FRONTEND=noninteractive apt-get install -y -qq fail2ban
	mkdir -p /etc/fail2ban/filter.d /etc/fail2ban/jail.d "$DIR/data/logs/nginx"
	cp "$SRC/deployments/fail2ban/filter.d/uncut-honeypot.conf" /etc/fail2ban/filter.d/
	cp "$SRC/deployments/fail2ban/jail.d/uncut-nginx.local" /etc/fail2ban/jail.d/
	# The package default enables the sshd jail — but it bans port 22 only. Pin the real port when SSH moved off 22, or the brute-force ban hits nothing.
	SSH_PORT=$(sshd -T 2>/dev/null | awk '/^port /{print $2; exit}')
	if [ -z "$SSH_PORT" ]; then
		SSH_PORT=$(awk '/^Port /{print $2; exit}' /etc/ssh/sshd_config /etc/ssh/sshd_config.d/*.conf 2>/dev/null)
	fi
	if [ -n "$SSH_PORT" ] && [ "$SSH_PORT" != "22" ]; then
		printf '[sshd]\nport = %s\n' "$SSH_PORT" > /etc/fail2ban/jail.d/sshd-port.local
	fi
	systemctl enable --now fail2ban 2>/dev/null || service fail2ban restart 2>/dev/null || true
else
	echo "fail2ban: apt-get not found, skipping (jails are in deployments/fail2ban)"
fi

# Best-effort sanity: the domain should resolve to this host's public address.
PUBIP=$(curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null || true)
DOMIP=$(getent hosts "$DOMAIN" 2>/dev/null | awk '{print $1; exit}' || true)
if [ -n "$PUBIP" ] && [ -n "$DOMIP" ] && [ "$PUBIP" != "$DOMIP" ]; then
	echo "WARNING: $DOMAIN resolves to $DOMIP but this host's address is $PUBIP" >&2
fi

# .env from flags.
ENVFILE="$SRC/deployments/.env"
{
	echo "DOMAIN=$DOMAIN"
	echo "EMAIL=$EMAIL"
	echo "TAG=$TAG"
	echo "TZ=$TZ"
	echo "LOG_LEVEL=info"
} > "$ENVFILE"

cd "$SRC/deployments"

echo "==> building and starting the node"
if [ -n "$IMAGE" ]; then
	UNCUT_IMAGE=$IMAGE docker compose up -d
else
	docker compose up -d --build
fi

echo "==> waiting for the node to come up"
i=0
while [ $i -lt 120 ]; do
	if curl -fsS --max-time 3 http://127.0.0.1:8088/healthz >/dev/null 2>&1; then
		break
	fi
	i=$((i + 1))
	sleep 1
done
if [ $i -ge 120 ]; then
	echo "node did not answer /healthz in 120s; logs:" >&2
	docker compose logs --tail 50 uncut >&2 || true
	exit 1
fi

echo "node is up."

# The node just created the nginx log files fail2ban jails watch: a first-start fail2ban came up before they existed and silently skipped the uncut jails.
if systemctl is-active --quiet fail2ban 2>/dev/null; then
	systemctl restart fail2ban
fi

echo "next steps:"
echo "  docker exec uncut-node raw info                 # revision, cert expiry, proxy links"
echo "  docker exec uncut-node raw add -n <name>        # add a client, prints links and a QR"
