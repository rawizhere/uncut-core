#!/bin/bash

# Colors
export RED='\033[0;31m'
export GREEN='\033[0;32m'
export YELLOW='\033[0;33m'
export BLUE='\033[0;34m'
export NC='\033[0m'

# Logging functions
print_success() {
    echo -e "${GREEN}✓ $1${NC}"
}

print_error() {
    echo -e "${RED}✗ $1${NC}"
}

print_warning() {
    echo -e "${YELLOW}! $1${NC}"
}

print_info() {
    echo -e "${BLUE}ℹ $1${NC}"
}

# System checks
# Check if run as root
check_root() {
    if [[ $EUID -ne 0 ]]; then
        print_error "Script must be run as root"
        exit 1
    fi
}

check_os() {
    if [[ ! -f /etc/debian_version ]]; then
        print_error "Only Ubuntu/Debian is supported"
        exit 1
    fi
}

check_internet() {
    if ! curl -s --connect-timeout 3 https://1.1.1.1 >/dev/null 2>&1; then
        print_error "No internet connection"
        exit 1
    fi
}

# Check if sing-box is installed
is_installed() {
    # Check if binary exists and nginx is present
    [[ -f "$INSTALL_DIR/sing-box" ]] && command -v nginx >/dev/null 2>&1
}

# Check for script updates
check_script_updates() {
    # Check if auto_update is enabled
    local auto_update=$(get_setting "auto_update" "false")
    [[ "$auto_update" != "true" ]] && return
    
    print_info "Checking for script updates..."
    
    cd "$SCRIPT_DIR" || return
    git fetch origin main >/dev/null 2>&1
    
    local local_hash=$(git rev-parse HEAD)
    local remote_hash=$(git rev-parse origin/main)
    
    if [[ "$local_hash" != "$remote_hash" ]]; then
        print_warning "New version of Uncut Core is available!"
        read -p "Update now? y/n: " perform_update
        if [[ "$perform_update" == "y" ]]; then
            print_info "Updating script..."
            if git pull origin main; then
                print_success "Script updated. Please restart the manager."
                exit 0
            else
                print_error "Update failed"
            fi
        fi
    fi
}
