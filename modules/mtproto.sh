# Install MTProxy and tproxy-server
install_telegram_proxies() {
    print_info "Checking Telegram Proxy components..."
    
    # Install MTProxy backend if missing
    if [[ ! -x "/opt/MTProxy/objs/bin/mtproto-proxy" || ! -f "/etc/mtproxy/proxy-secret" ]]; then
        print_info "Building official Telegram MTProxy backend..."
        export DEBIAN_FRONTEND=noninteractive
        apt-get update -qq >/dev/null 2>&1 || true
        apt-get install -y -qq --no-install-recommends ca-certificates curl build-essential libssl-dev util-linux zlib1g-dev >/dev/null 2>&1 || true
        
        if ! id mtproxy >/dev/null 2>&1; then
            useradd --system --home /nonexistent --shell /usr/sbin/nologin mtproxy >/dev/null 2>&1 || true
        fi
        
        local tmp_build=$(mktemp -d /tmp/mtproxy-build.XXXXXX)
        local commit="f36d8af769ffaeac36978d38c2c0f6d1104c2137"
        curl -sSL "https://github.com/TelegramMessenger/MTProxy/archive/${commit}.tar.gz" -o "$tmp_build/mtproxy.tar.gz"
        mkdir -p "$tmp_build/MTProxy"
        tar -C "$tmp_build/MTProxy" --strip-components=1 -xzf "$tmp_build/mtproxy.tar.gz" 2>/dev/null || true
        make -C "$tmp_build/MTProxy" -j"$(nproc)" >/dev/null 2>&1 || true
        
        if [[ -x "$tmp_build/MTProxy/objs/bin/mtproto-proxy" ]]; then
            mkdir -p /opt/MTProxy/objs/bin
            cp "$tmp_build/MTProxy/objs/bin/mtproto-proxy" /opt/MTProxy/objs/bin/mtproto-proxy
            chmod +x /opt/MTProxy/objs/bin/mtproto-proxy
            rm -rf "$tmp_build"
            print_success "MTProxy backend compiled successfully"
        fi
        
        mkdir -p /etc/mtproxy
        curl -sSL "https://core.telegram.org/getProxySecret" -o /etc/mtproxy/proxy-secret
        curl -sSL "https://core.telegram.org/getProxyConfig" -o /etc/mtproxy/proxy-multi.conf
        chmod 0640 /etc/mtproxy/proxy-secret /etc/mtproxy/proxy-multi.conf
    fi

    # Install tproxy-server if missing
    if [[ ! -x "$INSTALL_DIR/tproxy-server" ]]; then
        print_info "Setting up Telegram WEB Proxy relay..."
        if ! command -v go >/dev/null 2>&1; then
            apt-get install -y -qq golang-go >/dev/null 2>&1 || true
        fi
        
        local tmp_tproxy=$(mktemp -d /tmp/tproxy-build.XXXXXX)
        if git clone --depth 1 https://github.com/telegramdesktop/tproxy-server.git "$tmp_tproxy" >/dev/null 2>&1; then
            (cd "$tmp_tproxy" && go build -o "$INSTALL_DIR/tproxy-server" ./cmd/tproxy-server >/dev/null 2>&1)
            chmod +x "$INSTALL_DIR/tproxy-server" 2>/dev/null || true
            rm -rf "$tmp_tproxy"
            print_success "Telegram WEB Proxy relay installed successfully"
        fi
    fi
}

# Generate Telegram MTProto secret
generate_mtproto_secret() {
    local domain=$(get_setting "domain")
    local raw_hex=$(openssl rand -hex 16)
    local hex_domain=$(python3 -c "import sys; print('$domain'.encode().hex(), end='')" 2>/dev/null || xxd -p <<< "$domain" | tr -d '\n')
    echo "ee${raw_hex}${hex_domain}"
}

setup_mtproto_service() {
    local secret=$1
    local domain=$(get_setting "domain")
    
    # Extract clean 32-char hex secret
    local clean_hex_secret="${secret#ee}"
    clean_hex_secret="${clean_hex_secret:0:32}"
    if [[ -z "$clean_hex_secret" || ${#clean_hex_secret} -lt 32 ]]; then
        clean_hex_secret=$(openssl rand -hex 16)
    fi

    # Configure MTProxy backend
    mkdir -p /etc/mtproxy
    echo "MTPROXY_SECRET=$clean_hex_secret" > /etc/mtproxy/mtproxy.env
    echo "MTPROXY_WORKERS=1" >> /etc/mtproxy/mtproxy.env
    echo "MTPROXY_MAX_CONNECTIONS=4096" >> /etc/mtproxy/mtproxy.env

    cat > /etc/systemd/system/mtproxy.service <<EOF
[Unit]
Description=Official Telegram MTProto Proxy Backend
After=network.target

[Service]
Type=simple
EnvironmentFile=/etc/mtproxy/mtproxy.env
ExecStart=/opt/MTProxy/objs/bin/mtproto-proxy -u nobody -p 8888 -H 2398 -S \${MTPROXY_SECRET} --aes-pwd /etc/mtproxy/proxy-secret /etc/mtproxy/proxy-multi.conf -M \${MTPROXY_WORKERS} -C \${MTPROXY_MAX_CONNECTIONS}
Restart=on-failure
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

    # Configure tproxy-server
    mkdir -p "$INSTALL_DIR/tproxy"
    mkdir -p /srv/tproxy-site
    if [[ ! -f /srv/tproxy-site/index.html ]]; then
        echo '<!DOCTYPE html><html><head><title>CloudFront Edge Origin</title></head><body><h3>Origin Server Active</h3></body></html>' > /srv/tproxy-site/index.html
    fi

    # Create profiles.json
    cat > "$INSTALL_DIR/tproxy/profiles.json" <<EOF
{
  "profiles": [
    {
      "name": "default",
      "secret": "$clean_hex_secret",
      "backend": "127.0.0.1:2398",
      "carrier_mode": "https"
    }
  ]
}
EOF
    chmod 0600 "$INSTALL_DIR/tproxy/profiles.json"

    # Create config.json
    cat > "$INSTALL_DIR/tproxy/config.json" <<EOF
{
  "public_hostname": "$domain",
  "listen": "127.0.0.1:8080",
  "admin_listen": "127.0.0.1:8081",
  "public_dir": "/srv/tproxy-site",
  "profiles_file": "$INSTALL_DIR/tproxy/profiles.json",
  "enable_pprof": false
}
EOF

    cat > /etc/systemd/system/tproxy-server.service <<EOF
[Unit]
Description=Telegram WEB Proxy Service (tproxy-server)
After=network.target mtproxy.service

[Service]
Type=simple
ExecStart=$INSTALL_DIR/tproxy-server -config $INSTALL_DIR/tproxy/config.json
Restart=on-failure
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    systemctl enable mtproxy tproxy-server >/dev/null 2>&1
    systemctl restart mtproxy >/dev/null 2>&1
    systemctl restart tproxy-server >/dev/null 2>&1
}

get_mtproto_link() {
    local domain=$(get_setting "domain")
    local secret=$(get_setting "mtproto_secret")
    local clean_hex="${secret#ee}"
    clean_hex="${clean_hex:0:32}"
    if [[ -n "$domain" && -n "$secret" ]]; then
        echo "tg://proxy?server=${domain}&port=443&secret=${secret}"
    fi
}

get_web_proxy_secret() {
    local secret=$(get_setting "mtproto_secret")
    local clean_hex="${secret#ee}"
    clean_hex="${clean_hex:0:32}"
    echo "$clean_hex"
}

mtproto_menu() {
    echo ""
    echo "=== MTProto Proxy (Telegram 1-Click) ==="
    echo ""
    
    local enabled=$(get_setting "mtproto_enabled" "false")
    local secret=$(get_setting "mtproto_secret")
    local domain=$(get_setting "domain")
    
    if [[ "$enabled" == "true" ]]; then
        echo -e "Status: ${GREEN}Enabled (Nginx :443 Hub)${NC}"
        echo "1-Click Telegram Link (MTPROTO):"
        echo -e "${YELLOW}$(get_mtproto_link)${NC}"
        echo ""
        echo "Telegram WEB Proxy Settings (for Edit proxy -> WEB):"
        echo "  Web proxy hostname: ${YELLOW}${domain}${NC}"
        echo "  Secret:             ${YELLOW}$(get_web_proxy_secret)${NC}"
        echo ""
        if command -v qrencode >/dev/null 2>&1; then
            qrencode -t ANSIUTF8 "$(get_mtproto_link)" 2>/dev/null || true
        fi
        echo ""
        echo "1) Disable Telegram Proxies"
        echo "2) Rotate Secret"
        echo "0) Back"
        echo ""
        read -p "Your choice: " choice
        case "$choice" in
            1)
                systemctl stop mtproxy tproxy-server >/dev/null 2>&1 || true
                systemctl disable mtproxy tproxy-server >/dev/null 2>&1 || true
                set_setting "mtproto_enabled" "false"
                # Re-apply Nginx config
                setup_nginx_cdn "$domain"
                print_success "MTProto disabled"
                ;;
            2)
                local new_secret=$(generate_mtproto_secret)
                set_setting "mtproto_secret" "$new_secret"
                setup_mtproto_service "$new_secret"
                print_success "Secret rotated"
                echo -e "New Link: ${YELLOW}$(get_mtproto_link)${NC}"
                ;;
            0) return ;;
        esac
    else
        echo -e "Status: ${RED}Disabled${NC}"
        echo ""
        echo "Enable MTProto Telegram Proxy (routed via Nginx :443)?"
        echo "1) Enable MTProto"
        echo "0) Back"
        echo ""
        read -p "Your choice: " choice
        if [[ "$choice" == "1" ]]; then
            install_telegram_proxies || return 1
            if [[ -z "$secret" ]]; then
                secret=$(generate_mtproto_secret)
                set_setting "mtproto_secret" "$secret"
            fi
            set_setting "mtproto_enabled" "true"
            setup_mtproto_service "$secret"
            
            # Re-apply Nginx CDN config to add location for MTProto / tg-ws if needed
            setup_nginx_cdn "$domain"
            
            print_success "Telegram Web Proxy & MTProto enabled and running behind Nginx :443"
            echo -e "Telegram Link: ${YELLOW}$(get_mtproto_link)${NC}"
        fi
    fi
}
