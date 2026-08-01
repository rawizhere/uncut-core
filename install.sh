#!/bin/bash

# Uncut Core Installer
# Interactive: curl -fsSL https://raw.githubusercontent.com/rawizhere/uncut-core/main/install.sh | bash
# Unattended:  curl -fsSL https://raw.githubusercontent.com/rawizhere/uncut-core/main/install.sh | bash -s -- --domain node1.domain.com --email admin@domain.com --clients "alice,bob"

export RED='\033[0;31m'
export GREEN='\033[0;32m'
export YELLOW='\033[1;33m'
export NC='\033[0m'

if [[ $EUID -ne 0 ]]; then
    echo -e "${RED}Error: Must be run as root${NC}"
    exit 1
fi

# Parse CLI arguments
export UNATTENDED=false
while [[ $# -gt 0 ]]; do
    case $1 in
        --domain) export DOMAIN="$2"; shift 2 ;;
        --email) export EMAIL="$2"; shift 2 ;;
        --sni) export SNI="$2"; shift 2 ;;
        --country) export COUNTRY="$2"; shift 2 ;;
        --protocols) export PROTOCOLS="$2"; shift 2 ;;
        --clients) export CLIENTS="$2"; shift 2 ;;
        --ssh-port) export SSH_PORT="$2"; shift 2 ;;
        --auto) export UNATTENDED=true; shift ;;
        *) shift ;;
    esac
done

if [[ -n "$DOMAIN" && -n "$EMAIL" ]]; then
    export UNATTENDED=true
fi

echo -e "${GREEN}Installing Uncut Core...${NC}"

INSTALL_DIR="/opt/sing-box"
mkdir -p "$INSTALL_DIR"

if command -v git &> /dev/null; then
    if [[ -d "$INSTALL_DIR/.git" ]]; then
        echo "Updating existing installation via Git..."
        cd "$INSTALL_DIR"
        git remote set-url origin https://github.com/rawizhere/uncut-core.git
        git fetch --all >/dev/null 2>&1
        git reset --hard origin/main >/dev/null 2>&1
    else
        echo "Cloning repository via Git..."
        git clone -q https://github.com/rawizhere/uncut-core.git "$INSTALL_DIR"
    fi
else
    echo "Git not found, downloading release tarball..."
    tmp_tar=$(mktemp)
    if curl -sL https://github.com/rawizhere/uncut-core/archive/refs/heads/main.tar.gz -o "$tmp_tar"; then
        tar -xzf "$tmp_tar" -C "$INSTALL_DIR" --strip-components=1 --overwrite
        rm -f "$tmp_tar"
    else
        echo -e "${RED}Failed to download repository tarball. Installing git...${NC}"
        apt-get update -qq && apt-get install -y git -qq
        git clone -q https://github.com/rawizhere/uncut-core.git "$INSTALL_DIR"
    fi
fi

# Permissions
chmod +x "$INSTALL_DIR/raw"
chmod +x "$INSTALL_DIR/core/"*.sh
chmod +x "$INSTALL_DIR/modules/"*.sh

# Symlink
ln -sf "$INSTALL_DIR/raw" /usr/local/bin/raw

# Setup Noise Generator Service
echo "Setting up Noise Generator service..."
cat > /etc/systemd/system/uncut-noise.service <<EOF
[Unit]
Description=Uncut Core Traffic Blending Noise Generator
After=network.target

[Service]
Type=oneshot
ExecStart=/bin/bash $INSTALL_DIR/modules/noise.sh
User=root
EOF

cat > /etc/systemd/system/uncut-noise.timer <<EOF
[Unit]
Description=Run Uncut Core Noise Generator periodically

[Timer]
OnBootSec=1m
OnUnitActiveSec=5m
RandomizedDelaySec=2m
Persistent=true

[Install]
WantedBy=timers.target
EOF

systemctl daemon-reload
systemctl enable --now uncut-noise.timer >/dev/null 2>&1 || true

echo -e "${GREEN}Installation files updated!${NC}"

if [[ "$UNATTENDED" == "true" ]]; then
    echo -e "${YELLOW}Starting unattended installation...${NC}"
    raw --auto
else
    echo "Run 'raw' to start interactive menu."
    # Auto-start with terminal connection to avoid infinite loop when piped
    exec raw </dev/tty
fi
