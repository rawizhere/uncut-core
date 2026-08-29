#!/usr/bin/env bash
set -e

# Defaults
DOMAIN=""
EMAIL=""
REGION="eu-1"
API_VERSION="2.4.1"
CLIENTS="default"
UNCUT_REF="${UNCUT_REF:-main}"

while [[ $# -gt 0 ]]; do
    case $1 in
        -d|--domain) DOMAIN="$2"; shift 2 ;;
        -m|--email) EMAIL="$2"; shift 2 ;;
        -r|--region) REGION="$2"; shift 2 ;;
        -v|--api-version) API_VERSION="$2"; shift 2 ;;
        -c|--clients) CLIENTS="$2"; shift 2 ;;
        *) shift ;;
    esac
done

if [[ -z "$DOMAIN" ]]; then
    echo "Error: --domain is required"
    echo "Usage: curl -fsSL https://raw.githubusercontent.com/rawizhere/uncut-core/main/bootstrap.sh | bash -s -- -d <domain> [-m <email>] [-r <region>] [-v <version>] [-c <clients>]"
    exit 1
fi

echo "=== Step 1: System Optimization ==="
timedatectl set-timezone Europe/Moscow 2>/dev/null || true
timedatectl set-ntp on 2>/dev/null || true

# Enable BBR & FQ
if ! grep -q "net.core.default_qdisc=fq" /etc/sysctl.conf 2>/dev/null; then
    cat >> /etc/sysctl.conf << 'SYSCTL'
net.core.default_qdisc=fq
net.ipv4.tcp_congestion_control=bbr
net.ipv4.tcp_fastopen=3
net.core.rmem_max=67108864
net.core.wmem_max=67108864
net.ipv4.tcp_rmem=4096 87380 33554432
net.ipv4.tcp_wmem=4096 65536 33554432
SYSCTL
    sysctl -p >/dev/null 2>&1 || true
fi

# Enable 2GB Swap if missing
if [[ $(swapon --show | wc -l) -le 1 ]]; then
    fallocate -l 2G /swapfile 2>/dev/null || dd if=/dev/zero of=/swapfile bs=1M count=2048
    chmod 600 /swapfile
    mkswap /swapfile >/dev/null 2>&1
    swapon /swapfile >/dev/null 2>&1
    if ! grep -q "/swapfile" /etc/fstab; then
        echo "/swapfile none swap sw 0 0" >> /etc/fstab
    fi
fi

# Free conflicting ports
systemctl stop apache2 nginx 2>/dev/null || true

# Setup UFW Firewall
SSH_PORT="22"
if [ -f /etc/ssh/sshd_config ]; then
    DETECTED_PORT=$(grep -E '^\s*Port\s+[0-9]+' /etc/ssh/sshd_config 2>/dev/null | awk '{print $2}' | tail -n1)
    [ -n "$DETECTED_PORT" ] && SSH_PORT="$DETECTED_PORT"
fi

if ! command -v ufw >/dev/null 2>&1 && command -v apt-get >/dev/null 2>&1; then
    apt-get update -qq >/dev/null 2>&1 || true
    apt-get install -y -qq ufw >/dev/null 2>&1 || true
fi

if command -v ufw >/dev/null 2>&1; then
    ufw default deny incoming >/dev/null 2>&1 || true
    ufw default allow outgoing >/dev/null 2>&1 || true
    ufw allow "${SSH_PORT}/tcp" >/dev/null 2>&1 || true
    ufw allow 80/tcp >/dev/null 2>&1 || true
    ufw allow 443/tcp >/dev/null 2>&1 || true
    ufw allow 443/udp >/dev/null 2>&1 || true
    ufw allow 8443/tcp >/dev/null 2>&1 || true
    ufw deny 2398/tcp >/dev/null 2>&1 || true
    ufw deny 8888/tcp >/dev/null 2>&1 || true
    ufw --force enable >/dev/null 2>&1 || true
elif command -v iptables >/dev/null 2>&1; then
    iptables -C INPUT -p tcp --dport 2398 ! -s 127.0.0.1 -j DROP >/dev/null 2>&1 || iptables -I INPUT -p tcp --dport 2398 ! -s 127.0.0.1 -j DROP >/dev/null 2>&1 || true
    iptables -C INPUT -p tcp --dport 8888 ! -s 127.0.0.1 -j DROP >/dev/null 2>&1 || iptables -I INPUT -p tcp --dport 8888 ! -s 127.0.0.1 -j DROP >/dev/null 2>&1 || true
fi

# Setup Fail2ban on Host
if command -v apt-get >/dev/null 2>&1; then
    apt-get update -qq >/dev/null 2>&1 || true
    apt-get install -y -qq fail2ban curl tar >/dev/null 2>&1 || true
    mkdir -p /etc/fail2ban/filter.d /etc/fail2ban/jail.d /opt/uncut/data/logs/nginx
    touch /opt/uncut/data/logs/nginx/access.log /opt/uncut/data/logs/nginx/error.log
fi

echo "=== Step 2: Install Docker ==="
if ! command -v docker >/dev/null 2>&1; then
    curl -fsSL https://get.docker.com | sh
    systemctl enable docker
    systemctl start docker
fi

echo "=== Step 3: Configure and Deploy Uncut Core ==="
INSTALL_DIR="/opt/uncut"
mkdir -p "$INSTALL_DIR/data" "$INSTALL_DIR/install" "$INSTALL_DIR/deployments"

cat > "$INSTALL_DIR/deployments/.env" << ENVEOF
DOMAIN=${DOMAIN}
EMAIL=${EMAIL:-admin@${DOMAIN}}
REGION=${REGION}
API_VERSION=${API_VERSION}
ENVEOF

# Download deployment repository archive or fallback files
TMP_DIR=$(mktemp -d)
if curl -fsSL "https://github.com/rawizhere/uncut-core/archive/refs/heads/${UNCUT_REF}.tar.gz" -o "$TMP_DIR/repo.tar.gz" 2>/dev/null; then
    tar -xzf "$TMP_DIR/repo.tar.gz" -C "$TMP_DIR"
    EXTRACTED_DIR=$(find "$TMP_DIR" -maxdepth 1 -type d -name "uncut-core*" | head -n 1)
    if [[ -n "$EXTRACTED_DIR" ]]; then
        cp "$EXTRACTED_DIR/deployments/docker-compose.yml" "$INSTALL_DIR/deployments/docker-compose.yml"
        if [[ -d "$EXTRACTED_DIR/deployments/fail2ban" ]]; then
            cp -r "$EXTRACTED_DIR/deployments/fail2ban/filter.d/"* /etc/fail2ban/filter.d/ 2>/dev/null || true
            cp -r "$EXTRACTED_DIR/deployments/fail2ban/jail.d/"* /etc/fail2ban/jail.d/ 2>/dev/null || true
            systemctl restart fail2ban 2>/dev/null || true
        fi
    fi
else
    curl -fsSL "https://raw.githubusercontent.com/rawizhere/uncut-core/${UNCUT_REF}/deployments/docker-compose.yml" -o "$INSTALL_DIR/deployments/docker-compose.yml"
    curl -fsSL "https://raw.githubusercontent.com/rawizhere/uncut-core/${UNCUT_REF}/deployments/fail2ban/filter.d/uncut-honeypot.conf" -o /etc/fail2ban/filter.d/uncut-honeypot.conf 2>/dev/null || true
    curl -fsSL "https://raw.githubusercontent.com/rawizhere/uncut-core/${UNCUT_REF}/deployments/fail2ban/jail.d/uncut-nginx.local" -o /etc/fail2ban/jail.d/uncut-nginx.local 2>/dev/null || true
    systemctl restart fail2ban 2>/dev/null || true
fi
rm -rf "$TMP_DIR"

cd "$INSTALL_DIR/deployments"
docker compose pull || true
docker compose up -d

# Create initial clients
IFS=',' read -ra CLIENT_ARRAY <<< "$CLIENTS"
for client in "${CLIENT_ARRAY[@]}"; do
    client_clean=$(echo "$client" | xargs)
    if [[ -n "$client_clean" ]]; then
        docker exec uncut-core uncut add -n "$client_clean" || true
    fi
done

echo ""
echo "=== Uncut Core Deployment Successful ==="
docker exec uncut-core uncut list
