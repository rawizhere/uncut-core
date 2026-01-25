#!/bin/bash

# Configuration generators for Inbounds
generate_vless_ws_inbound() {
    local users=$(jq -c '[.[] | {uuid: .uuid}]' "$CLIENTS_FILE")
    local theme_data=$(get_theme_data)
    local paths_str=$(echo "$theme_data" | awk -F'|' '{print $1}' | cut -d':' -f2)
    local primary_path_raw=$(echo "$paths_str" | cut -d',' -f1)
    local salted_path=$(get_salted_path "$primary_path_raw")

    cat <<EOF
{
  "type": "vless",
  "tag": "vless-ws",
  "listen": "127.0.0.1",
  "listen_port": 10001,
  "users": $users,
  "transport": {
    "type": "ws",
    "path": "$salted_path",
    "max_early_data": 0,
    "early_data_header_name": "Sec-WebSocket-Protocol"
  }
}
EOF
}

generate_xhttp_stealth_inbound() {
    local users=$(jq -c '[.[] | {uuid: .uuid}]' "$CLIENTS_FILE")
    local theme_data=$(get_theme_data)
    local paths_str=$(echo "$theme_data" | awk -F'|' '{print $1}' | cut -d':' -f2)
    local mode=$(echo "$theme_data" | awk -F'|' '{print $3}' | cut -d':' -f2)
    
    local primary_path_raw=$(echo "$paths_str" | cut -d',' -f2)
    local salted_path=$(get_salted_path "$primary_path_raw")

    cat <<EOF
{
  "type": "vless",
  "tag": "xhttp-stealth",
  "listen": "127.0.0.1",
  "listen_port": 10002,
  "users": $users,
  "transport": {
    "type": "xhttp",
    "path": "$salted_path",
    "mode": "auto"
  }
}
EOF
}

generate_vless_reality_inbound() {
    local sni=$(get_setting "sni")
    local private_key=$(get_setting "reality_private_key")
    local short_id=$(get_setting "reality_short_id")
    
    # Get client list
    local users=$(jq -c '[.[] | {uuid: .uuid, flow: "xtls-rprx-vision"}]' "$CLIENTS_FILE")
    
    cat <<EOF
{
  "type": "vless",
  "tag": "vless-reality",
  "listen": "0.0.0.0",
  "listen_port": 2083,
  "users": $users,
  "tls": {
    "enabled": true,
    "server_name": "$sni",
    "reality": {
      "enabled": true,
      "handshake": {
        "server": "$sni",
        "server_port": 443
      },
      "private_key": "$private_key",
      "short_id": ["$short_id"],
      "max_time_difference": "5m"
    }
  }
}
EOF
}

generate_hysteria2_inbound() {
    local domain=$(get_setting "domain")
    local obfs_password=$(get_setting "hysteria_obfs_password")
    
    # Get client list (password is used for hysteria2)
    local users=$(jq -c '[.[] | {password: (.password // .uuid)}]' "$CLIENTS_FILE")
    
    cat <<EOF
{
  "type": "hysteria2",
  "tag": "hysteria2",
  "listen": "0.0.0.0",
  "listen_port": 8443,
  "users": $users,
  "obfs": {
    "type": "salamander",
    "password": "$obfs_password"
  },
  "tls": {
    "enabled": true,
    "alpn": ["h3"],
    "certificate_path": "$INSTALL_DIR/certs/certificates/$domain.crt",
    "key_path": "$INSTALL_DIR/certs/certificates/$domain.key"
  }
}
EOF
}

generate_xhttp_inbound() {
    local domain=$(get_setting "domain")
    
    local users=$(jq -c '[.[] | {uuid: .uuid}]' "$CLIENTS_FILE")
    
    cat <<EOF
{
  "type": "vless",
  "tag": "xhttp",
  "listen": "0.0.0.0",
  "listen_port": 2053,
  "users": $users,
  "transport": {
    "type": "xhttp",
    "path": "/",
    "mode": "auto"
  },
  "tls": {
    "enabled": true,
    "alpn": ["h2", "http/1.1"],
    "certificate_path": "$INSTALL_DIR/certs/certificates/$domain.crt",
    "key_path": "$INSTALL_DIR/certs/certificates/$domain.key"
  }
}
EOF
}

generate_xhttp_reality_inbound() {
    local sni=$(get_setting "sni")
    local private_key=$(get_setting "reality_private_key")
    local short_id=$(get_setting "reality_short_id")
    
    local users=$(jq -c '[.[] | {uuid: .uuid}]' "$CLIENTS_FILE")
    
    cat <<EOF
{
  "type": "vless",
  "tag": "xhttp-reality",
  "listen": "0.0.0.0",
  "listen_port": 8443,
  "users": $users,
  "transport": {
    "type": "xhttp",
    "path": "/",
    "mode": "auto"
  },
  "tls": {
    "enabled": true,
    "server_name": "$sni",
    "reality": {
      "enabled": true,
      "handshake": {
        "server": "$sni",
        "server_port": 443
      },
      "private_key": "$private_key",
      "short_id": ["$short_id"],
      "max_time_difference": "5m"
    }
  }
}
EOF
}

generate_tuic_inbound() {
    local domain=$(get_setting "domain")
    
    # For TUIC we use uuid as uuid and password as password
    local users=$(jq -c '[.[] | {uuid: .uuid, password: (.password // .uuid), name: .name}]' "$CLIENTS_FILE")
    
    cat <<EOF
{
  "type": "tuic",
  "tag": "tuic",
  "listen": "0.0.0.0",
  "listen_port": 8550,
  "users": $users,
  "congestion_control": "bbr",
  "auth_timeout": "3s",
  "zero_rtt_handshake": false,
  "heartbeat": "10s",
  "tls": {
    "enabled": true,
    "alpn": ["h3"],
    "certificate_path": "$INSTALL_DIR/certs/certificates/$domain.crt",
    "key_path": "$INSTALL_DIR/certs/certificates/$domain.key"
  }
}
EOF
}

generate_http_inbound() {
    # For HTTP we use name as username and password as password
    local users=$(jq -c '[.[] | {username: .name, password: (.password // .uuid)}]' "$CLIENTS_FILE")
    
    cat <<EOF
{
  "type": "http",
  "tag": "http",
  "listen": "0.0.0.0",
  "listen_port": 52143,
  "users": $users
}
EOF
}

generate_socks_inbound() {
    # For SOCKS we use name as username and password as password
    local users=$(jq -c '[.[] | {username: .name, password: (.password // .uuid)}]' "$CLIENTS_FILE")
    
    cat <<EOF
{
  "type": "socks",
  "tag": "socks",
  "listen": "0.0.0.0",
  "listen_port": 52144,
  "users": $users
}
EOF
}

add_protocol_logic() {
    local protocol=$1
    local needs_reality=false
    local needs_hysteria=false
    local needs_tls=false

    [[ "$protocol" == "vless-reality" || "$protocol" == "xhttp-reality" ]] && needs_reality=true
    [[ "$protocol" == "hysteria2" ]] && needs_hysteria=true
    [[ "$protocol" == "hysteria2" || "$protocol" == "xhttp" || "$protocol" == "tuic" ]] && needs_tls=true

    # Check and generate necessary data
    if [[ "$needs_reality" == true ]]; then
        # Always generate new Reality keys
        print_info "Generating Reality keys..."
        local keys_output=$("$INSTALL_DIR/sing-box" generate reality-keypair)
        local private_key=$(echo "$keys_output" | grep "PrivateKey:" | awk '{print $2}')
        local public_key=$(echo "$keys_output" | grep "PublicKey:" | awk '{print $2}')
        set_setting "reality_private_key" "$private_key"
        set_setting "reality_public_key" "$public_key"
        
        # Always generate new short_id
        local short_id=$(generate_short_id)
        set_setting "reality_short_id" "$short_id"
    fi
    
    if [[ "$needs_hysteria" == true ]]; then
        local obfs_password=$(generate_obfs_password)
        set_setting "hysteria_obfs_password" "$obfs_password"
    fi
    
    # Ensure certificates exist for TLS protocols
    if [[ "$needs_tls" == true ]]; then
        print_info "Checking certificates for $protocol..."
        # Calls installed acme module function
        if command -v install_acme_sh &>/dev/null; then
            install_acme_sh
        else
            # fallback or error implies source order issues
            print_error "ACME module not loaded"
        fi
    fi
    
    # If vless-ws or xhttp-stealth, update Nginx config
    if [[ "$protocol" == "vless-ws" || "$protocol" == "xhttp-stealth" ]]; then
        print_info "Updating Nginx configuration for Stealth Mode..."
        local domain=$(get_setting "domain")
        if [[ -n "$domain" ]]; then
            setup_nginx_cdn "$domain"
        fi
    fi

    add_protocol_to_settings "$protocol"
}

add_protocol() {
    echo ""
    echo "=== Add Protocol ==="
    echo ""
    
    echo "Select protocol:"
    echo "1) VLESS + Reality      (TCP :2083)"
    echo "2) Hysteria2            (UDP :8443)"
    echo "3) XHTTP                (TCP :2053)"
    echo "4) XHTTP + Reality      (TCP :8443)"
    echo "5) TUIC v5              (UDP :8550)"
    echo "6) VLESS + WS (Nginx)   (TCP :443)"
    echo "7) XHTTP Stealth (Nginx)(TCP :443)"
    echo "8) HTTP                 (TCP :52143)"
    echo "9) SOCKS5               (TCP :52144)"
    echo "10) Create all protocols"
    echo "0) Back"
    echo ""
    
    read -p "Your choice: " choice
    
    if [[ "$choice" == "0" ]]; then
        return
    fi

    if [[ "$choice" == "10" ]]; then
        print_info "Adding all protocols..."
        local all_protos=("vless-reality" "hysteria2" "xhttp" "xhttp-reality" "tuic" "vless-ws" "xhttp-stealth" "http" "socks")
        for p in "${all_protos[@]}"; do
            if ! protocol_exists "$p"; then
                add_protocol_logic "$p"
            fi
        done
        rebuild_config
        systemctl restart sing-box
        print_success "All available protocols added"
        return
    fi

    local protocol=""
    case "$choice" in
        1) protocol="vless-reality" ;;
        2) protocol="hysteria2" ;;
        3) protocol="xhttp" ;;
        4) protocol="xhttp-reality" ;;
        5) protocol="tuic" ;;
        6) protocol="vless-ws" ;;
        7) protocol="xhttp-stealth" ;;
        8) protocol="http" ;;
        9) protocol="socks" ;;
        *)
            print_error "Invalid choice"
            return
            ;;
    esac
    
    if protocol_exists "$protocol"; then
        print_error "Protocol is already added"
        return
    fi
    
    add_protocol_logic "$protocol"
    
    # Update config.json
    rebuild_config
    
    # Restart service
    print_info "Restarting service..."
    systemctl restart sing-box
    sleep 2
    
    if systemctl is-active --quiet sing-box; then
        print_success "Protocol '$protocol' added"
    else
        print_error "Launch error! Port might be occupied. Check logs."
    fi
    echo ""
}

remove_protocol() {
    echo ""
    echo "=== Remove Protocol ==="
    echo ""
    
    local protocols=($(get_protocols))
    
    if [[ ${#protocols[@]} -eq 0 ]]; then
        print_warning "Protocols not configured"
        return
    fi
    
    echo "Active protocols:"
    for i in "${!protocols[@]}"; do
        echo "$((i+1))) ${protocols[$i]}"
    done
    echo "99) Remove all protocols"
    echo "0) Back"
    echo ""
    
    read -p "Your choice: " choice
    
    if [[ "$choice" == "0" ]]; then
        return
    fi
    
    if [[ "$choice" == "99" ]]; then
        read -p "Remove ALL protocols? y/n: " confirm
        if [[ "$confirm" != "y" ]]; then
            print_info "Cancelled"
            return
        fi
        for p in "${protocols[@]}"; do
            remove_protocol_from_settings "$p"
        done
        rebuild_config
        systemctl restart sing-box
        print_success "All protocols removed"
        return
    fi

    if ! [[ "$choice" =~ ^[0-9]+$ ]]; then
        print_error "Invalid choice"
        return
    fi
    
    local index=$((choice - 1))
    if [[ $index -lt 0 ]] || [[ $index -ge ${#protocols[@]} ]]; then
        print_error "Invalid choice"
        return
    fi
    
    local protocol="${protocols[$index]}"
    
    read -p "Remove protocol '$protocol'? y/n: " confirm
    if [[ "$confirm" != "y" ]]; then
        print_info "Cancelled"
        return
    fi
    
    remove_protocol_from_settings "$protocol"
    
    rebuild_config
    
    print_info "Restarting service..."
    systemctl restart sing-box
    sleep 2
    
    if systemctl is-active --quiet sing-box; then
        print_success "Protocol '$protocol' removed"
    else
        print_error "Launch error! Check logs."
    fi
    echo ""
}

list_protocols() {
    echo ""
    echo "=== Protocol List ==="
    echo ""
    
    local protocols=($(get_protocols))
    
    if [[ ${#protocols[@]} -eq 0 ]]; then
        print_warning "Protocols not configured"
    else
        echo "Active protocols:"
        for protocol in "${protocols[@]}"; do
            case "$protocol" in
                "vless-reality")
                    echo "  • VLESS + Reality (:2083)"
                    ;;
                "hysteria2")
                    echo "  • Hysteria2 (:8443 UDP)"
                    ;;
                "xhttp")
                    echo "  • XHTTP (:2053)"
                    ;;
                "xhttp-reality")
                    echo "  • XHTTP + Reality (:8443)"
                    ;;
                "tuic")
                    echo "  • TUIC v5 (:8550 UDP)"
                    ;;
                "vless-ws")
                    echo "  • VLESS + WebSocket (:443 via Nginx)"
                    ;;
                "xhttp-stealth")
                    echo "  • XHTTP Stealth (Nginx) (:443)"
                    ;;
                "http")
                    echo "  • HTTP (:52143)"
                    ;;
                "socks")
                    echo "  • SOCKS5 (:52144)"
                    ;;
            esac
        done
    fi
    echo ""
}

change_sni() {
    echo ""
    echo "=== Change SNI ==="
    echo ""
    
    local current_sni=$(get_setting "sni")
    if [[ -n "$current_sni" ]]; then
        echo "Current SNI: $current_sni"
    else
        echo "SNI not set"
    fi
    echo "Recommended SNI list:"
    echo "https://github.com/YukiKras/vless-wizard/blob/main/sni.txt"
    echo ""
    read -p "Enter new SNI: " new_sni
    
    set_setting "sni" "$new_sni"
    rebuild_config
    
    print_info "Restarting service..."
    systemctl restart sing-box
    sleep 2
    
    if systemctl is-active --quiet sing-box; then
        print_success "SNI changed. Client links updated."
    else
        print_error "Launch error! Check logs."
    fi
    echo ""
}
