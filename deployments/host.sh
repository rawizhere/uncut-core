#!/bin/sh
# Host layer of a node (time sync, ufw, Hysteria2 port hopping, fail2ban); idempotent, re-run on a live node: curl -fsSL https://raw.githubusercontent.com/rawizhere/uncut-core/main/deployments/host.sh | sh
set -eu

REF=${REF:-main}
RAW=https://raw.githubusercontent.com/rawizhere/uncut-core/$REF/deployments
HOP=20000:30000
LOGS=/opt/uncut/data/logs/nginx

if [ "$(id -u)" != 0 ]; then
	echo "run as root" >&2
	exit 1
fi

SSH_PORT=$(sshd -T 2>/dev/null | awk '/^port /{print $2; exit}')
if [ -z "$SSH_PORT" ]; then
	echo "cannot read the sshd port, refusing to touch the firewall" >&2
	exit 1
fi

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
# iptables-persistent conflicts with ufw and apt silently removes one for the other, so ufw owns the filter rules and the hop redirect has its own unit.
apt-get install -y -qq ufw fail2ban >/dev/null
apt-get purge -y -qq iptables-persistent netfilter-persistent >/dev/null 2>&1 || true

# A clock that drifts or jumps wedges sing-box timers and the node serves dead transports while every process looks alive.
if ! systemctl is-active --quiet chrony && ! systemctl is-active --quiet systemd-timesyncd; then
	apt-get install -y -qq chrony >/dev/null 2>&1 || true
	systemctl enable --now chrony 2>/dev/null || systemctl enable --now systemd-timesyncd 2>/dev/null || true
fi

# MTProto proxy opens thousands of short-lived connections to Telegram middle proxies;
# the default table (often 7-16k) fills up and the kernel drops packets at random.
echo "==> conntrack"
sysctl -w net.netfilter.nf_conntrack_max=65536 >/dev/null
if [ -f /sys/module/nf_conntrack/parameters/hashsize ]; then
	echo 16384 > /sys/module/nf_conntrack/parameters/hashsize
fi
sysctl -w net.ipv4.tcp_tw_reuse=1 >/dev/null
printf 'net.netfilter.nf_conntrack_max = 65536\nnet.ipv4.tcp_tw_reuse = 1\n' > /etc/sysctl.d/99-uncut-conntrack.conf

echo "==> firewall"
ufw --force reset >/dev/null
rm -f /etc/ufw/*.rules.[0-9]*
ufw default deny incoming >/dev/null
ufw default allow outgoing >/dev/null
for rule in "$SSH_PORT/tcp" 80/tcp 443/tcp 443/udp "$HOP/udp"; do
	ufw allow "$rule" >/dev/null
done
ufw --force enable >/dev/null
# On a host where this run just installed ufw, enable sometimes does not take; retry once and fail loudly rather than leave the node unprotected.
if ! ufw status | grep -q "Status: active"; then
	ufw disable >/dev/null
	ufw --force enable >/dev/null
fi
ufw status | grep -q "Status: active" || { echo "ufw did not activate" >&2; exit 1; }

cat > /etc/systemd/system/uncut-hop.service <<EOF
[Unit]
Description=Hysteria2 port hopping: UDP $HOP onto 443

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c 'iptables -t nat -C PREROUTING -p udp --dport $HOP -j REDIRECT --to-ports 443 2>/dev/null || iptables -t nat -A PREROUTING -p udp --dport $HOP -j REDIRECT --to-ports 443'

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable uncut-hop.service >/dev/null 2>&1
systemctl restart uncut-hop.service

echo "==> fail2ban"
mkdir -p /etc/fail2ban/filter.d /etc/fail2ban/jail.d "$LOGS"
curl -fsSL "$RAW/fail2ban/filter.d/uncut-honeypot.conf" -o /etc/fail2ban/filter.d/uncut-honeypot.conf
curl -fsSL "$RAW/fail2ban/jail.d/uncut-nginx.local" -o /etc/fail2ban/jail.d/uncut-nginx.local
# fail2ban refuses to start when a jail's log file is missing, which is the case until the node's nginx first runs.
touch "$LOGS/access.log" "$LOGS/error.log"
# The package sshd jail bans port 22 only; pin the real port when SSH moved off 22.
if [ "$SSH_PORT" != 22 ]; then
	printf '[sshd]\nport = %s\n' "$SSH_PORT" > /etc/fail2ban/jail.d/sshd-port.local
else
	rm -f /etc/fail2ban/jail.d/sshd-port.local
fi
systemctl enable fail2ban >/dev/null 2>&1
systemctl restart fail2ban

echo "host layer applied: ufw allows $SSH_PORT/tcp 80/tcp 443/tcp 443/udp $HOP/udp"
