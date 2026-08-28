#!/bin/bash

# Configuration generators for Inbounds
generate_vless_ws_inbound() {
    local users=$(jq -c '[.[] | select(.protocols == null or .protocols == [] or (.protocols[]? == "vless-ws")) | {uuid: .uuid}]' "$CLIENTS_FILE" 2>/dev/null || echo "[]")
    local theme_data=$(get_theme_data)
    local paths_str=$(echo "$theme_data" | awk -F'|' '{print $1}' | cut -d':' -f2)
    local primary_path_raw=$(echo "$paths_str" | cut -d',' -f2)
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
    local users=$(jq -c '[.[] | select(.protocols == null or .protocols == [] or (.protocols[]? == "xhttp-stealth")) | {uuid: .uuid}]' "$CLIENTS_FILE" 2>/dev/null || echo "[]")
    local theme_data=$(get_theme_data)
    local paths_str=$(echo "$theme_data" | awk -F'|' '{print $1}' | cut -d':' -f2)
    local mode=$(echo "$theme_data" | awk -F'|' '{print $3}' | cut -d':' -f2)
    
    local primary_path_raw=$(echo "$paths_str" | cut -d',' -f1)
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
    "mode": "stream-up",
    "x_padding_bytes": "100-2500",
    "no_sse_header": false,
    "sc_max_each_post_bytes": 1000000,
    "sc_max_buffered_posts": 30,
    "sc_stream_up_server_secs": "20-80"
  }
}
EOF
}

generate_vless_httpupgrade_inbound() {
    local users=$(jq -c '[.[] | select(.protocols == null or .protocols == [] or (.protocols[]? == "vless-httpupgrade")) | {uuid: .uuid}]' "$CLIENTS_FILE" 2>/dev/null || echo "[]")
    local theme_data=$(get_theme_data)
    local paths_str=$(echo "$theme_data" | awk -F'|' '{print $1}' | cut -d':' -f2)
    local primary_path_raw=$(echo "$paths_str" | cut -d',' -f3)
    local salted_path=$(get_salted_path "$primary_path_raw")

    cat <<EOF
{
  "type": "vless",
  "tag": "vless-httpupgrade",
  "listen": "127.0.0.1",
  "listen_port": 10004,
  "users": $users,
  "transport": {
    "type": "httpupgrade",
    "path": "$salted_path"
  }
}
EOF
}

generate_vless_grpc_inbound() {
    local users=$(jq -c '[.[] | select(.protocols == null or .protocols == [] or (.protocols[]? == "vless-grpc")) | {uuid: .uuid}]' "$CLIENTS_FILE" 2>/dev/null || echo "[]")
    local salt=$(get_setting "protocol_salt")
    local service_name="EdgeContent_${salt}"

    cat <<EOF
{
  "type": "vless",
  "tag": "vless-grpc",
  "listen": "127.0.0.1",
  "listen_port": 10003,
  "users": $users,
  "transport": {
    "type": "grpc",
    "service_name": "$service_name"
  }
}
EOF
}

generate_vless_reality_inbound() {
    local sni=$(get_setting "sni" "dl.google.com")
    local private_key=$(get_setting "reality_private_key")
    local short_id=$(get_setting "reality_short_id")
    
    if [[ -z "$private_key" || -z "$short_id" ]]; then
        if [[ -f "$INSTALL_DIR/sing-box" ]]; then
            local keys_output=$("$INSTALL_DIR/sing-box" generate reality-keypair 2>/dev/null)
            private_key=$(echo "$keys_output" | grep "PrivateKey:" | awk '{print $2}')
            local public_key=$(echo "$keys_output" | grep "PublicKey:" | awk '{print $2}')
            short_id=$(generate_short_id)
            set_setting "reality_private_key" "$private_key"
            set_setting "reality_public_key" "$public_key"
            set_setting "reality_short_id" "$short_id"
        fi
    fi
    
    local handshake_server=$(get_setting "reality_handshake_server" "$sni")
    local users=$(jq -c '[.[] | select(.protocols == null or .protocols == [] or (.protocols[]? == "vless-reality")) | {uuid: .uuid, flow: "xtls-rprx-vision"}]' "$CLIENTS_FILE" 2>/dev/null || echo "[]")
    
    cat <<EOF
{
  "type": "vless",
  "tag": "vless-reality",
  "listen": "0.0.0.0",
  "listen_port": 8443,
  "users": $users,
  "tls": {
    "enabled": true,
    "server_name": "$sni",
    "reality": {
      "enabled": true,
      "handshake": {
        "server": "$handshake_server",
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
    local users=$(jq -c '[.[] | select(.protocols == null or .protocols == [] or (.protocols[]? == "tuic")) | {uuid: .uuid, password: (.password // .uuid), name: .name}]' "$CLIENTS_FILE" 2>/dev/null || echo "[]")

    cat <<EOF
{
  "type": "tuic",
  "tag": "tuic",
  "listen": "0.0.0.0",
  "listen_port": 443,
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

add_protocol_logic() {
    local protocol=$1

    if [[ "$protocol" == "vless-reality" ]]; then
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

    add_protocol_to_settings "$protocol"
}
add_protocol() {
    echo ""
    echo "=== Add Protocol ==="
    echo ""

    echo "Select protocol:"
    echo "1) XHTTP Stealth (Nginx)  (TCP :443)"
    echo "2) VLESS + WS (Nginx)     (TCP :443)"
    echo "3) VLESS + HTTPUpgrade    (TCP :443)"
    echo "4) VLESS + gRPC (Nginx)   (TCP :443)"
    echo "5) TUIC v5               (UDP :443)"
    echo "6) VLESS + Reality       (TCP :8443)"
    echo "7) Create all protocols"
    echo "0) Back"
    echo ""

    read -p "Your choice: " choice

    if [[ "$choice" == "0" ]]; then
        return
    fi

    if [[ "$choice" == "7" ]]; then
        print_info "Adding all protocols..."
        local all_protos=("xhttp-stealth" "vless-ws" "vless-httpupgrade" "vless-grpc" "tuic" "vless-reality")
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
        1) protocol="xhttp-stealth" ;;
        2) protocol="vless-ws" ;;
        3) protocol="vless-httpupgrade" ;;
        4) protocol="vless-grpc" ;;
        5) protocol="tuic" ;;
        6) protocol="vless-reality" ;;
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

    local domain=$(get_setting "domain")
    if [[ -n "$domain" ]]; then
        setup_nginx_cdn "$domain"
    fi

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
        local protocol
        for protocol in "${protocols[@]}"; do
            case "$protocol" in
                "vless-reality")
                    echo "  • VLESS + Reality (:8443)"
                    ;;
                "tuic")
                    echo "  • TUIC v5 (:443 UDP)"
                    ;;
                "vless-ws")
                    echo "  • VLESS + WebSocket (:443 via Nginx)"
                    ;;
                "xhttp-stealth")
                    echo "  • XHTTP Stealth (:443 via Nginx)"
                    ;;
                "vless-httpupgrade")
                    echo "  • VLESS + HTTPUpgrade (:443 via Nginx)"
                    ;;
                "vless-grpc")
                    echo "  • VLESS + gRPC (:443 via Nginx)"
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
