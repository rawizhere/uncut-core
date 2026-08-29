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
CLIENTS="admin"
ZEROSSL_KID=""
ZEROSSL_HMAC=""

while [ $# -gt 0 ]; do
    case "$1" in
        --domain)
            DOMAIN="$2"
            shift 2
            ;;
        --email)
            EMAIL="$2"
            shift 2
            ;;
        --clients)
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
            echo "Usage: install.sh [--domain example.com] [--email admin@example.com] [--clients user1,user2] [--zerossl-kid <kid>] [--zerossl-hmac <hmac>]"
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

# Interactive prompt if flags were omitted
if [ -z "$DOMAIN" ] && [ ! -f "$DEPLOY_DIR/.env" ]; then
    echo ""
    read -rp "Enter your server domain (e.g. vpn.example.com): " DOMAIN
    read -rp "Enter admin email (e.g. admin@example.com): " EMAIL
fi

# Write .env if supplied
if [ -n "$DOMAIN" ]; then
    cat > "$DEPLOY_DIR/.env" << ENV_EOF
DOMAIN=${DOMAIN}
EMAIL=${EMAIL:-admin@${DOMAIN}}
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
    echo "=== Installation Complete! ==="
    echo "Run 'uncut' or 'raw' anytime to open the management console."
else
    echo "=== Please configure $DEPLOY_DIR/.env and run 'cd $DEPLOY_DIR && docker compose up -d' ==="
fi
