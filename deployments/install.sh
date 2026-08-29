#!/usr/bin/env bash
set -e

# ==============================================================================
# Uncut Core 3.0 One-Line Installer (Docker-based)
# ==============================================================================

# Ensure script is run as root
if [ "$(id -u)" -ne 0 ]; then
    echo "This script must be run as root. Use 'sudo bash install.sh'" >&2
    exit 1
fi

DOMAIN=""
EMAIL=""
REGION="eu-1"
API_VERSION="2.4.1"
CLIENTS="admin"
ZEROSSL_KID=""
ZEROSSL_HMAC=""

while [ $# -gt 0 ]; do
    case "$1" in
        -d|--domain)
            DOMAIN="$2"
            shift 2
            ;;
        -m|--email)
            EMAIL="$2"
            shift 2
            ;;
        -r|--region)
            REGION="$2"
            shift 2
            ;;
        -v|--api-version)
            API_VERSION="$2"
            shift 2
            ;;
        -c|--clients)
            CLIENTS="$2"
            shift 2
            ;;
        --zerossl-kid)
            ZEROSSL_KID="$2"
            shift 2
            ;;
        --zerossl-hmac)
            ZEROSSL_HMAC="$2"
            shift 2
            ;;
        -h|--help)
            echo "Usage: install.sh [--domain example.com] [--email admin@example.com] [--region eu-1] [--api-version 2.4.1] [--clients user1,user2] [--zerossl-kid <kid>] [--zerossl-hmac <hmac>]"
            exit 0
            ;;
        *)
            shift
            ;;
    esac
done

INSTALL_DIR="/opt/uncut"
DEPLOY_DIR="$INSTALL_DIR/deployments"

echo "=== Installing Dependencies (Docker & Compose) ==="
if ! command -v docker >/dev/null 2>&1; then
    curl -fsSL https://get.docker.com | sh
fi

mkdir -p "$DEPLOY_DIR" "$INSTALL_DIR/data" "$INSTALL_DIR/install"

echo "=== Configuring Host Firewall ==="
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
    echo "Configuring and enabling UFW..."
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
    echo "Configuring iptables fallback rules..."
    iptables -C INPUT -p tcp --dport 2398 ! -s 127.0.0.1 -j DROP >/dev/null 2>&1 || iptables -I INPUT -p tcp --dport 2398 ! -s 127.0.0.1 -j DROP >/dev/null 2>&1 || true
    iptables -C INPUT -p tcp --dport 8888 ! -s 127.0.0.1 -j DROP >/dev/null 2>&1 || iptables -I INPUT -p tcp --dport 8888 ! -s 127.0.0.1 -j DROP >/dev/null 2>&1 || true
fi

# Interactive prompt if flags were omitted
if [ -z "$DOMAIN" ] && [ ! -f "$DEPLOY_DIR/.env" ]; then
    echo ""
    read -rp "Enter your server domain (e.g. ingest-eu-1.example.com): " DOMAIN
    read -rp "Enter admin email (e.g. admin@example.com): " EMAIL
fi

# Write or update .env if domain supplied
if [ -n "$DOMAIN" ]; then
    if [ -f "$DEPLOY_DIR/.env" ]; then
        OLD_EMAIL=$(grep -E '^EMAIL=' "$DEPLOY_DIR/.env" | cut -d= -f2-)
        OLD_KID=$(grep -E '^ZEROSSL_EAB_KID=' "$DEPLOY_DIR/.env" | cut -d= -f2-)
        OLD_HMAC=$(grep -E '^ZEROSSL_EAB_HMAC=' "$DEPLOY_DIR/.env" | cut -d= -f2-)
        [ -z "$EMAIL" ] && EMAIL="$OLD_EMAIL"
        [ -z "$ZEROSSL_KID" ] && ZEROSSL_KID="$OLD_KID"
        [ -z "$ZEROSSL_HMAC" ] && ZEROSSL_HMAC="$OLD_HMAC"
    fi
    cat > "$DEPLOY_DIR/.env" << ENV_EOF
DOMAIN=${DOMAIN}
EMAIL=${EMAIL:-admin@${DOMAIN}}
REGION=${REGION}
API_VERSION=${API_VERSION}
CLIENTS=${CLIENTS}
ZEROSSL_EAB_KID=${ZEROSSL_KID}
ZEROSSL_EAB_HMAC=${ZEROSSL_HMAC}
ENV_EOF
fi

# Download or create docker-compose.yml
cat > "$DEPLOY_DIR/docker-compose.yml" << 'COMPOSE_EOF'
services:
  uncut:
    image: ghcr.io/rawizhere/uncut-core:latest
    container_name: uncut-core
    restart: unless-stopped
    network_mode: host
    env_file:
      - .env
    environment:
      - DATA_DIR=/opt/uncut/data
      - INSTALL_DIR=/opt/sing-box
      - TZ=Europe/Moscow
    volumes:
      - uncut_data:/opt/uncut/data
      - uncut_install:/opt/sing-box
      - /etc/localtime:/etc/localtime:ro
      - /etc/timezone:/etc/timezone:ro
    logging:
      driver: "json-file"
      options:
        max-size: "10m"
        max-file: "3"

volumes:
  uncut_data:
    driver: local
    driver_opts:
      type: none
      o: bind
      device: /opt/uncut/data
  uncut_install:
    driver: local
    driver_opts:
      type: none
      o: bind
      device: /opt/uncut/install
COMPOSE_EOF

# Setup host wrapper commands 'raw' and 'uncut'
for cmd in raw uncut; do
    cat > "/usr/local/bin/$cmd" << 'WRAPPER_EOF'
#!/bin/sh
if [ -t 0 ]; then
    exec docker exec -it uncut-core uncut "$@"
else
    exec docker exec -i uncut-core uncut "$@"
fi
WRAPPER_EOF
    chmod +x "/usr/local/bin/$cmd"
done

echo "=== Wrapper commands 'raw' and 'uncut' installed ==="

if [ -f "$DEPLOY_DIR/.env" ]; then
    echo "=== Starting Uncut Core Daemon ==="
    cd "$DEPLOY_DIR" && docker compose pull && docker compose up -d --force-recreate
    echo ""
    echo "=== Waiting for services to initialize... ==="
    sleep 3
    echo ""
    uncut list || true
    echo ""
    echo "=== Active Port Security Check ==="
    ss -tlnp 2>/dev/null | grep -E ':(80|443|8443|2398|8888)' || true
    echo ""
    echo "=== Installation Complete! ==="
    echo "Run 'uncut' or 'raw' anytime to open the management console."
else
    echo "=== Please configure $DEPLOY_DIR/.env and run 'cd $DEPLOY_DIR && docker compose up -d' ==="
fi
