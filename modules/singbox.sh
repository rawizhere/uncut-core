#!/bin/bash

# Download sing-box
download_singbox() {
    print_info "Detecting architecture..."
    local arch=$(uname -m)
    case $arch in
        x86_64) arch="amd64" ;;
        aarch64) arch="arm64" ;;
        *)
            print_error "Unsupported architecture: $arch"
            exit 1
            ;;
    esac
    print_success "Architecture: $arch"

    print_info "Fetching latest sing-box extended version..."
    local release_info=$(curl -s https://api.github.com/repos/shtorm-7/sing-box-extended/releases/latest)
    local version=$(echo "$release_info" | jq -r .tag_name)
    
    if [[ -z "$version" ]] || [[ "$version" == "null" ]]; then
        print_error "Failed to fetch sing-box version"
        exit 1
    fi
    print_success "Version: $version"

    # Search for download file
    print_info "Searching for download file..."
    local asset_name=$(echo "$release_info" | jq -r ".assets[] | select(.name | contains(\"linux-${arch}\")) | .name" | head -n1)
    
    if [[ -z "$asset_name" ]]; then
        print_error "No file found for architecture $arch"
        exit 1
    fi
    
    local url="https://github.com/shtorm-7/sing-box-extended/releases/download/${version}/${asset_name}"
    print_info "File: $asset_name"
    
    print_info "Downloading sing-box..."
    local tmp_dir=$(mktemp -d)
    
    if ! curl -sL "$url" -o "$tmp_dir/sing-box-archive"; then
        print_error "Failed to download sing-box"
        rm -rf "$tmp_dir"
        exit 1
    fi
    
    print_info "Extracting..."
    
    # Determine archive type and extract accordingly
    if [[ "$asset_name" == *.tar.gz ]]; then
        tar -xzf "$tmp_dir/sing-box-archive" -C "$tmp_dir" 2>/dev/null || {
            print_error "tar.gz extraction error"
            rm -rf "$tmp_dir"
            exit 1
        }
    elif [[ "$asset_name" == *.zip ]]; then
        unzip -q "$tmp_dir/sing-box-archive" -d "$tmp_dir" 2>/dev/null || {
            print_error "zip extraction error"
            rm -rf "$tmp_dir"
            exit 1
        }
    else
        # Direct binary handling
        mkdir -p "$INSTALL_DIR"
        mv "$tmp_dir/sing-box-archive" "$INSTALL_DIR/sing-box"
        chmod +x "$INSTALL_DIR/sing-box"
        rm -rf "$tmp_dir"
        print_success "Sing-box installed"
        return
    fi
    
    # Search for sing-box binary in extracted files
    mkdir -p "$INSTALL_DIR"
    local binary_path=$(find "$tmp_dir" -name "sing-box" -type f | head -n1)
    
    if [[ -z "$binary_path" ]]; then
        print_error "sing-box binary not found in archive"
        rm -rf "$tmp_dir"
        exit 1
    fi
    
    mv "$binary_path" "$INSTALL_DIR/sing-box"
    chmod +x "$INSTALL_DIR/sing-box"
    
    rm -rf "$tmp_dir"
    print_success "Sing-box installed"
}

create_initial_configs() {
    print_info "Creating configuration files..."
    
    # Create basic config.json
    local template_file="$SCRIPT_DIR/templates/config.json.template"
    if [[ -f "$template_file" ]]; then
        cp "$template_file" "$CONFIG_FILE"
    else
        cat > "$CONFIG_FILE" <<EOF
{
  "log": {
    "level": "info",
    "timestamp": true
  },
  "inbounds": [],
  "outbounds": [
    {
      "type": "direct",
      "tag": "direct"
    }
  ]
}
EOF
    fi

    # Create empty clients.json
    echo "[]" > "$CLIENTS_FILE"
    
    print_success "Configuration files created"
}

create_systemd_service() {
    print_info "Creating systemd service..."
    
    local template_file="$SCRIPT_DIR/templates/systemd.service.template"
    if [[ -f "$template_file" ]]; then
        envsubst '$INSTALL_DIR $CONFIG_FILE $LOG_FILE' < "$template_file" > "$SERVICE_FILE"
    else
        print_warning "Template not found at $template_file. Using fallback."
        cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=sing-box service
After=network.target

[Service]
Type=simple
ExecStart=$INSTALL_DIR/sing-box run -c $CONFIG_FILE
ExecReload=/bin/kill -HUP \$MAINPID
Restart=on-failure
RestartSec=5
StandardOutput=append:$LOG_FILE
StandardError=append:$LOG_FILE

[Install]
WantedBy=multi-user.target
EOF
    fi

    systemctl daemon-reload
    systemctl enable sing-box >/dev/null 2>&1
    print_success "Systemd service created"
}

setup_logrotate() {
    print_info "Configuring logrotate..."
    local template_file="$SCRIPT_DIR/templates/logrotate.conf.template"
    if [[ -f "$template_file" ]]; then
        envsubst '$LOG_FILE' < "$template_file" > "$LOGROTATE_FILE"
    else
        cat > "$LOGROTATE_FILE" <<EOF
${LOG_FILE} {
    daily
    rotate 3
    compress
    missingok
    notifempty
    copytruncate
}
EOF
    fi
    print_success "Logrotate configured"
}

install_dependencies() {
    print_info "Installing dependencies..."
    apt-get update -qq
    # Install all required packages including dnsutils for dig command
    if ! apt-get install -y curl jq openssl socat nginx gettext-base dnsutils; then
        print_error "Failed to install dependencies. Please check your internet connection or apt-get logs."
        exit 1
    fi
    # Verify critical components
    if ! command -v nginx >/dev/null 2>&1; then
        print_error "Nginx installation verification failed. Please install nginx manually."
        exit 1
    fi
    print_success "Dependencies installed"
}

install_singbox() {
    echo ""
    echo "=== Uncut Core Installation ==="
    echo ""
    
    if [[ -f "$INSTALL_DIR/sing-box" ]] && command -v nginx >/dev/null 2>&1; then
        print_error "Sing-box is already installed"
        return
    fi
    
    # Request data
    while true; do
        read -p "Enter domain: " domain
        if validate_domain "$domain"; then
            # Check DNS
            if check_domain_dns "$domain"; then
                break
            fi
        else
            print_error "Invalid domain"
        fi
    done
    
    while true; do
        read -p "Enter email: " email
        if [[ "$email" =~ ^[^@]+@[^@]+$ ]]; then
            break
        else
            print_error "Invalid email"
        fi
    done
    
    read -p "Enter country code: " country
    
    # SNI with default value
    local default_sni="www.microsoft.com"
    echo "Recommended SNI list:"
    echo "https://github.com/YukiKras/vless-wizard/blob/main/sni.txt"
    read -p "Enter SNI for Reality [$default_sni]: " sni
    sni=${sni:-$default_sni}
    
    echo ""
    
    # Pre-flight checks
    print_info "Running pre-flight checks..."
    if ! check_ports_available; then
        print_error "Installation cancelled due to port conflicts"
        return
    fi
    print_success "Pre-flight checks passed"
    
    # Start installation log
    local install_log="$INSTALL_DIR/install.log"
    mkdir -p "$INSTALL_DIR"
    echo "=== Uncut Core Installation Log ===" > "$install_log"
    echo "Date: $(date)" >> "$install_log"
    echo "Domain: $domain" >> "$install_log"
    echo "" >> "$install_log"
    
    # Installation
    install_dependencies 2>&1 | tee -a "$install_log"
    download_singbox 2>&1 | tee -a "$install_log"
    mkdir -p "$INSTALL_DIR/certs"
    enable_bbr 2>&1 | tee -a "$install_log"
    create_systemd_service 2>&1 | tee -a "$install_log"
    create_initial_configs 2>&1 | tee -a "$install_log"
    setup_logrotate 2>&1 | tee -a "$install_log"
    setup_firewall 2>&1 | tee -a "$install_log"
    
    # Create settings.json first as issue_certificates needs it
    cat > "$SETTINGS_FILE" <<EOF
{
  "domain": "$domain",
  "email": "$email",
  "country": "$country",
  "sni": "$sni",
  "reality_private_key": "",
  "reality_public_key": "",
  "reality_short_id": "",
  "hysteria_obfs_password": "$(openssl rand -hex 16)",
  "protocol_salt": "$(openssl rand -hex 4)",
  "masking_theme": "cdn_sync",
  "auto_update": "false",
  "protocols": [
    "hysteria2",
    "xhttp-stealth"
  ]
}
EOF
    
    # Nginx Setup from module
    setup_nginx_cdn "$domain" 2>&1 | tee -a "$install_log"
    
    echo ""
    print_success "Sing-box installed"
    
    print_success "BBR enabled"
    print_success "Autostart configured"
    echo ""
    echo "Domain: $domain"
    echo "Country: $country"
    echo "Email: $email"
    echo ""
    print_success "Certificates generated (Real or Self-signed fallback)"
    echo ""
    
    # Add default protocols automatically for a better "out-of-the-box" experience
    print_info "Adding default secure protocols (Hysteria2 & XHTTP-Stealth)..."
    
    # Check if they exist before adding (using protocol_exists from config.sh)
    if ! protocol_exists "hysteria2"; then
        add_protocol_logic "hysteria2"
    fi
    
    if ! protocol_exists "xhttp-stealth"; then
        add_protocol_logic "xhttp-stealth"
    fi
    
    rebuild_config
    
    if ! systemctl restart sing-box; then
        print_error "Failed to restart sing-box"
    fi
    
    if ! systemctl restart nginx; then
        print_error "Failed to restart nginx"
    else
        print_success "Nginx configured and running"
    fi
    
    print_success "Installation complete with Hysteria2 & XHTTP-Stealth protocols!"
    echo ""
    read -p "Do you want to add more protocols now? y/n: " add_now
    if [[ "$add_now" == "y" ]]; then
        add_protocol
    fi
    echo ""
}

update_singbox() {
    echo ""
    echo "=== Update Sing-box ==="
    echo ""
    
    # Check current version
    local current_version=$("$INSTALL_DIR/sing-box" version 2>/dev/null | head -n1 | awk '{print $3}')
    print_info "Current version: ${current_version:-unknown}"
    
    # Fetch latest version
    print_info "Checking for updates..."
    local release_info=$(curl -s https://api.github.com/repos/shtorm-7/sing-box-extended/releases/latest)
    local latest_version=$(echo "$release_info" | jq -r .tag_name)
    
    if [[ -z "$latest_version" ]] || [[ "$latest_version" == "null" ]]; then
        print_error "Failed to check latest version"
        return
    fi
    
    print_info "Latest version: $latest_version"
    
    if [[ "$current_version" == "$latest_version" ]]; then
        print_success "You already have the latest version!"
        read -p "Force reinstall anyway? y/n: " force
        if [[ "$force" != "y" ]]; then
            return
        fi
    fi
    
    echo ""
    echo "Update will include:"
    echo "  - sing-box binary: $current_version -> $latest_version"
    echo "  - Nginx packages"
    echo "  - Configurations will NOT be affected"
    echo ""
    read -p "Continue? y/n: " confirm
    if [[ "$confirm" != "y" ]]; then
        print_info "Cancelled"
        return
    fi
    
    # System update
    print_info "Updating packages..."
    apt-get update > /dev/null 2>&1
    apt-get upgrade -y nginx > /dev/null 2>&1
    
    # sing-box update
    if [[ "$current_version" != "$latest_version" || "$force" == "y" ]]; then
        print_info "Downloading sing-box $latest_version..."
        download_singbox
    else
        print_info "Sing-box binary is up to date, skipping download."
    fi
    
    print_info "Restarting services..."
    
    # Re-apply Nginx config to ensure latest changes
    local domain=$(get_setting "domain")
    if [[ -n "$domain" ]]; then
        setup_nginx_cdn "$domain"
        
        # Regenerate subscriptions (in case hash format changed)
        regenerate_all_subscriptions
    fi
    
    print_info "Restarting Nginx..."
    if systemctl restart nginx; then
        print_success "Nginx restarted"
    else
        print_error "Nginx restart failed! Check: nginx -t"
    fi
    systemctl restart sing-box
    sleep 2
    
    if systemctl is-active --quiet sing-box; then
        print_success "Update completed"
        echo "Sing-box: $("$INSTALL_DIR"/sing-box version | head -n1)"
    else
        print_error "sing-box launch error! Check logs."
    fi
    echo ""
}

uninstall_singbox() {
    echo ""
    echo "=== Uninstall Uncut Core ==="
    echo ""
    
    echo "WARNING: The following will be removed:"
    echo "  - Sing-box"
    echo "  - Nginx"
    echo "  - All configurations"
    echo "  - All logs"
    echo ""
    echo "1) Standard uninstall (preserve certificates)"
    echo "2) Full uninstall (remove everything including certificates)"
    echo "0) Cancel"
    echo ""
    read -p "Your choice: " uninstall_choice
    
    case "$uninstall_choice" in
        1)
            local remove_certs=false
            ;;
        2)
            local remove_certs=true
            print_warning "This will also remove SSL certificates!"
            read -p "Are you absolutely sure? y/n: " confirm
            if [[ "$confirm" != "y" ]]; then
                print_info "Cancelled"
                return
            fi
            ;;
        *)
            print_info "Cancelled"
            return
            ;;
    esac
    
    # Stop services
    print_info "Stopping services..."
    systemctl stop sing-box 2>/dev/null || true
    systemctl disable sing-box 2>/dev/null || true
    systemctl stop nginx 2>/dev/null || true
    systemctl disable nginx 2>/dev/null || true
    
    # Remove sing-box
    print_info "Removing Sing-box binary and configs..."
    rm -f "$SERVICE_FILE"
    rm -f "$CONFIG_FILE"
    rm -f "$SETTINGS_FILE"
    rm -f "$CLIENTS_FILE"
    rm -f "$LOG_FILE"
    rm -f "$INSTALL_DIR/sing-box"
    rm -f "$INSTALL_DIR/install.log"
    rm -f "$LOGROTATE_FILE"
    
    # Handle certificates
    if [[ "$remove_certs" == "true" ]]; then
        print_info "Removing certificates..."
        rm -rf "$INSTALL_DIR/certs"
        rmdir "$INSTALL_DIR" 2>/dev/null || true
    else
        print_info "Certificates preserved in $INSTALL_DIR/certs"
    fi
    
    # Remove Nginx and configs
    print_info "Removing Nginx..."
    apt-get remove --purge -y nginx nginx-common 2>/dev/null || true
    rm -rf /etc/nginx
    rm -rf /var/www/cdn
    
    # Remove UFW rules
    print_info "Cleaning firewall..."
    ufw --force disable 2>/dev/null || true
    ufw --force reset 2>/dev/null || true
    
    systemctl daemon-reload
    
    print_success "All components removed"
    echo ""
}

show_status() {
    echo ""
    echo "=== Status ==="
    echo ""
    
    if ! [[ -f "$INSTALL_DIR/sing-box" ]] || ! command -v nginx >/dev/null 2>&1; then
        print_error "Sing-box is not installed"
        return
    fi
    
    if systemctl is-active --quiet sing-box; then
        print_success "Sing-box: active"
    else
        print_error "Sing-box: inactive"
    fi
    
    local uptime=$(systemctl show sing-box --property=ActiveEnterTimestamp --value)
    if [[ -n "$uptime" ]]; then
        echo "Uptime: $(systemctl show sing-box --property=ActiveEnterTimestamp --value | xargs -I {} date -d {} +"%Y-%m-%d %H:%M:%S")"
    fi
    
    echo ""
    local domain=$(get_setting "domain")
    local country=$(get_setting "country")
    echo "Domain: $domain"
    echo "Country: $country"
    
    echo ""
    local protocol_count=$(get_protocols | wc -l)
    local client_count=$(jq 'length' "$CLIENTS_FILE")
    echo "Protocols: $protocol_count"
    echo "Clients: $client_count"
    
    echo ""
}

restart_service() {
    echo ""
    echo "=== Restart ==="
    echo ""
    
    print_info "Restarting service..."
    systemctl restart sing-box
    sleep 2
    
    if systemctl is-active --quiet sing-box; then
        print_success "Sing-box restarted"
    else
        print_error "Launch error! Check logs."
    fi
    
    echo ""
}

stop_service() {
    echo ""
    echo "=== Stop ==="
    echo ""
    
    print_info "Stopping service..."
    systemctl stop sing-box
    sleep 1
    
    if ! systemctl is-active --quiet sing-box; then
        print_success "Sing-box stopped"
    else
        print_error "Failed to stop service"
    fi
    
    echo ""
}

show_logs() {
    echo ""
    echo "=== Logs ==="
    echo ""
    
    echo "1) Last 20 lines"
    echo "2) Live logs (Ctrl+C to exit)"
    echo "0) Back"
    echo ""
    read -p "Your choice: " choice
    
    case "$choice" in
        1)
            if [[ -f "$LOG_FILE" ]]; then
                echo ""
                tail -n 20 "$LOG_FILE"
            else
                print_warning "Log file not found"
            fi
            ;;
        2)
            echo ""
            print_info "Press Ctrl+C to exit live logs..."
            echo ""
            journalctl -fu sing-box 2>/dev/null || tail -f "$LOG_FILE" 2>/dev/null || print_error "Cannot access logs"
            ;;
        0)
            return
            ;;
        *)
            print_error "Invalid choice"
            ;;
    esac
    
    echo ""
}

