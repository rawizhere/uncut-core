#!/bin/bash

# Uncut Core Installer
# curl -fsSL https://raw.githubusercontent.com/rawizhere/uncut-core/main/install.sh | bash

export RED='\033[0;31m'
export GREEN='\033[0;32m'
export NC='\033[0m'

if [[ $EUID -ne 0 ]]; then
    echo -e "${RED}Error: Must be run as root${NC}"
    exit 1
fi

echo -e "${GREEN}Installing Uncut Core...${NC}"

# Install git if missing
if ! command -v git &> /dev/null; then
    apt-get update -qq
    apt-get install -y git -qq
fi

INSTALL_DIR="/opt/sing-box"

if [[ -d "$INSTALL_DIR" ]]; then
    echo "Updating existing installation and migrating repo..."
    cd "$INSTALL_DIR"
    git remote set-url origin https://github.com/rawizhere/uncut-core.git
    git fetch --all >/dev/null 2>&1
    git reset --hard origin/main >/dev/null 2>&1
    # Cleanup old binary if exists
    [[ -f "proxiii" ]] && rm "proxiii"
else
    echo "Cloning repository..."
    git clone -q https://github.com/rawizhere/uncut-core.git "$INSTALL_DIR"
fi

# Permissions
chmod +x "$INSTALL_DIR/raw"
chmod +x "$INSTALL_DIR/core/"*.sh
chmod +x "$INSTALL_DIR/modules/"*.sh

# Symlink
ln -sf "$INSTALL_DIR/raw" /usr/local/bin/raw

echo -e "${GREEN}Installation complete!${NC}"
echo "Run 'raw' to start."

# Auto-start with terminal connection to avoid infinite loop when piped
exec raw </dev/tty
