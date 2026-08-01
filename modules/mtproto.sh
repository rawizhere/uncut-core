#!/bin/bash

export MTG_INSTALL_DIR="/opt/mtg"
export MTG_SERVICE_FILE="/etc/systemd/system/mtg.service"

install_mtg() {
    print_info "Detecting architecture for mtg..."
    local arch=$(uname -m)
    local mtg_arch=""
    case $arch in
        x86_64) mtg_arch="amd64" ;;
        aarch64) mtg_arch="arm64" ;;
        *)
            print_error "Unsupported architecture: $arch"
            return
            ;;
    esac

    print_info "Fetching latest mtg version..."
    local release_info=$(curl -s https://api.github.com/repos/9seconds/mtg/releases/latest)
    local version=$(echo "$release_info" | jq -r .tag_name)
    
    if [[ -z "$version" ]] || [[ "$version" == "null" ]]; then
        print_error "Failed to fetch mtg version"
        return
    fi
    print_success "Version: $version"

    local file_name="mtg-${version#v}-linux-${mtg_arch}"
    local url="https://github.com/9seconds/mtg/releases/download/${version}/${file_name}.tar.gz"
    
    print_info "Downloading mtg..."
    local tmp_dir=$(mktemp -d)
    
    if ! curl -sL "$url" -o "$tmp_dir/mtg.tar.gz"; then
        print_error "Failed to download mtg"
        rm -rf "$tmp_dir"
        return
    fi
    
    print_info "Extracting..."
    tar -xzf "$tmp_dir/mtg.tar.gz" -C "$tmp_dir" 2>/dev/null || {
        print_error "Extraction error"
        rm -rf "$tmp_dir"
        return
    }
    
    mkdir -p "$MTG_INSTALL_DIR"
    mv "$tmp_dir/$file_name/mtg" "$MTG_INSTALL_DIR/mtg"
    chmod +x "$MTG_INSTALL_DIR/mtg"
    rm -rf "$tmp_dir"
    
    local domain=$(get_setting "domain")
    local cert_file="$INSTALL_DIR/certs/certificates/$domain.crt"
    local fallback_target="127.0.0.1:443"
    
    if [[ -z "$domain" ]] || ! is_cert_valid "$cert_file"; then
        print_warning "SSL certificate for domain is missing or self-signed. Using aws.amazon.com fallback for MTProto TLS."
        domain="aws.amazon.com"
        fallback_target="aws.amazon.com:443"
    fi
    
    print_info "Generating TLS secret for $domain..."
    local secret=$("$MTG_INSTALL_DIR/mtg" generate-secret "$domain" 2>/dev/null)
    
    if [[ -z "$secret" ]]; then
        print_error "Failed to generate mtg secret"
        return
    fi
    
    print_info "Generating config for mtg..."
    cat > "$MTG_INSTALL_DIR/config.toml" <<EOF
secret = "$secret"
bind-to = "0.0.0.0:4430"
fallback = "$fallback_target"
prefer-ip-version = "ipv4"
EOF

    set_setting "mtg_secret" "$secret"
    set_setting "mtg_port" "4430"
    
    print_info "Creating mtg systemd service..."
    cat > "$MTG_SERVICE_FILE" <<EOF
[Unit]
Description=mtg - MTProto Proxy
Documentation=https://github.com/9seconds/mtg
After=network.target

[Service]
Type=simple
ExecStart=$MTG_INSTALL_DIR/mtg run $MTG_INSTALL_DIR/config.toml
Restart=always
RestartSec=3
DynamicUser=true
AmbientCapabilities=CAP_NET_BIND_SERVICE
StandardOutput=syslog
StandardError=syslog
SyslogIdentifier=mtg

[Install]
WantedBy=multi-user.target
EOF

    # Open firewall port
    if command -v ufw >/dev/null 2>&1; then
        print_info "Opening port 4430 in UFW..."
        ufw allow 4430/tcp >/dev/null 2>&1
    fi

    systemctl daemon-reload
    systemctl enable mtg >/dev/null 2>&1
    systemctl start mtg
    
    if systemctl is-active --quiet mtg; then
        print_success "MTProto proxy installed and started successfully!"
        show_mtg_status_and_links
    else
        print_error "mtg service failed to start. Check 'journalctl -u mtg'."
    fi
}

reinstall_mtg() {
    print_info "Reinstalling MTProto proxy..."
    uninstall_mtg
    install_mtg
}

uninstall_mtg() {
    print_info "Stopping and disabling mtg service..."
    pkill -9 -f "$MTG_INSTALL_DIR/mtg" >/dev/null 2>&1 || true
    timeout 5 systemctl stop mtg >/dev/null 2>&1 || true
    systemctl disable mtg >/dev/null 2>&1 || true
    
    print_info "Removing files..."
    rm -f "$MTG_SERVICE_FILE"
    rm -rf "$MTG_INSTALL_DIR"
    
    # Close firewall port
    if command -v ufw >/dev/null 2>&1; then
        print_info "Closing port 4430 in UFW..."
        ufw delete allow 4430/tcp >/dev/null 2>&1
    fi
    
    # Remove from settings
    local tmp=$(mktemp)
    jq 'del(.mtg_secret, .mtg_port)' "$SETTINGS_FILE" > "$tmp" 2>/dev/null && mv "$tmp" "$SETTINGS_FILE"
    
    systemctl daemon-reload
    print_success "MTProto proxy uninstalled"
}

show_mtg_status_and_links() {
    if ! [[ -f "$MTG_INSTALL_DIR/mtg" ]]; then
        print_error "MTProto Proxy is not installed"
        return
    fi
    
    if systemctl is-active --quiet mtg; then
        print_success "MTProto Proxy (mtg): active"
    else
        print_error "MTProto Proxy (mtg): inactive"
    fi
    
    local secret=$(get_setting "mtg_secret")
    local port=$(get_setting "mtg_port" "4430")
    local domain=$(get_setting "domain")
    local server_ip=$(curl -s --connect-timeout 2 https://api.ipify.org 2>/dev/null || curl -s --connect-timeout 2 https://ifconfig.me 2>/dev/null)
    
    if [[ -n "$secret" && -n "$port" ]]; then
        echo ""
        echo "=== MTProto Proxy Links ==="
        if [[ -n "$domain" ]]; then
            echo "Domain link:"
            echo "tg://proxy?server=${domain}&port=${port}&secret=${secret}"
            echo ""
        fi
        if [[ -n "$server_ip" ]]; then
            echo "IP link:"
            echo "tg://proxy?server=${server_ip}&port=${port}&secret=${secret}"
            echo ""
        fi
        echo "==========================="
        echo ""
    fi
}

mtproto_menu() {
    while true; do
        echo ""
        echo "=== MTProto Proxy Management ==="
        echo ""
        
        if [[ -f "$MTG_INSTALL_DIR/mtg" ]]; then
            echo "1) Show Status & Link"
            echo "2) Reinstall MTProto Proxy"
            echo "3) Uninstall MTProto Proxy"
        else
            echo "1) Install MTProto Proxy"
        fi
        
        echo "0) Back"
        echo ""
        
        read -p "Your choice: " choice
        
        case "$choice" in
            1)
                if [[ -f "$MTG_INSTALL_DIR/mtg" ]]; then
                    show_mtg_status_and_links
                else
                    install_mtg
                fi
                ;;
            2)
                if [[ -f "$MTG_INSTALL_DIR/mtg" ]]; then
                    reinstall_mtg
                else
                    print_error "Invalid choice"
                fi
                ;;
            3)
                if [[ -f "$MTG_INSTALL_DIR/mtg" ]]; then
                    read -p "Are you sure you want to uninstall MTProto proxy? y/n: " confirm
                    if [[ "$confirm" == "y" ]]; then
                        uninstall_mtg
                    fi
                else
                    print_error "Invalid choice"
                fi
                ;;
            0) return ;;
            *) print_error "Invalid choice" ;;
        esac
        
        echo ""
        read -p "Press Enter to continue..."
    done
}

reconfig_mtg_domain() {
    if [[ -f "$MTG_INSTALL_DIR/mtg" ]]; then
        local domain=$(get_setting "domain")
        local cert_file="$INSTALL_DIR/certs/certificates/$domain.crt"
        local fallback_target="127.0.0.1:443"
        
        if [[ -z "$domain" ]] || ! is_cert_valid "$cert_file"; then
            domain="aws.amazon.com"
            fallback_target="aws.amazon.com:443"
        fi

        print_info "Syncing MTProto secret with domain: $domain..."
        local new_secret=$("$MTG_INSTALL_DIR/mtg" generate-secret "$domain" 2>/dev/null)
        if [[ -n "$new_secret" ]]; then
            cat > "$MTG_INSTALL_DIR/config.toml" <<EOF
secret = "$new_secret"
bind-to = "0.0.0.0:4430"
fallback = "$fallback_target"
prefer-ip-version = "ipv4"
EOF
            set_setting "mtg_secret" "$new_secret"
            pkill -9 -f "$MTG_INSTALL_DIR/mtg" >/dev/null 2>&1 || true
            systemctl restart mtg >/dev/null 2>&1 || true
            print_success "MTProto domain secret updated"
        fi
    fi
}
