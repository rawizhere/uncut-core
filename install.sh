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
export UPDATE_ONLY=false
export PROTOCOLS="default"
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
        --update|-u|update) export UPDATE_ONLY=true; shift ;;
        *) shift ;;
    esac
done

if [[ -n "$DOMAIN" && -n "$EMAIL" ]]; then
    export UNATTENDED=true
fi

# Wait/clear apt locks if held by unattended-upgrades on fresh boot
if pgrep -f "unattended-upgr|dpkg|apt" >/dev/null 2>&1; then
    echo "Waiting for background package updates to complete..."
    timeout 60 bash -c 'while pgrep -f "unattended-upgr|dpkg|apt" >/dev/null 2>&1; do sleep 2; done' || true
fi

# Install core dependencies immediately before running any script logic
echo "Installing required system dependencies (jq, curl, tar, openssl)..."
apt-get update -qq >/dev/null 2>&1 || true
apt-get install -y jq curl tar openssl ca-certificates gawk gettext-base >/dev/null 2>&1 || true

INSTALL_DIR="/opt/sing-box"
mkdir -p "$INSTALL_DIR"

if command -v git &> /dev/null; then
    if [[ -d "$INSTALL_DIR/.git" ]]; then
        echo "Updating existing installation via Git..."
        cd "$INSTALL_DIR"
        git remote set-url origin https://github.com/rawizhere/uncut-core.git
        git fetch --all >/dev/null 2>&1
        git reset --hard origin/main >/dev/null 2>&1
    elif [[ -z "$(ls -A "$INSTALL_DIR" 2>/dev/null)" ]]; then
        echo "Cloning repository via Git..."
        git clone -q https://github.com/rawizhere/uncut-core.git "$INSTALL_DIR"
    else
        echo "Updating files via release tarball..."
        tmp_tar=$(mktemp)
        if curl -sL https://github.com/rawizhere/uncut-core/archive/refs/heads/main.tar.gz -o "$tmp_tar"; then
            tar -xzf "$tmp_tar" -C "$INSTALL_DIR" --strip-components=1 --overwrite
            rm -f "$tmp_tar"
        fi
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

echo -e "${GREEN}Installation files updated!${NC}"

if [[ "$UPDATE_ONLY" == "true" ]]; then
    echo -e "${YELLOW}Running core update...${NC}"
    raw update
elif [[ "$UNATTENDED" == "true" ]]; then
    echo -e "${YELLOW}Starting unattended installation...${NC}"
    raw --auto
else
    echo "Run 'raw' to start interactive menu."
    # Auto-start with terminal connection to avoid infinite loop when piped
    exec raw </dev/tty
fi
