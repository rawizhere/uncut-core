#!/bin/bash

# Link generation
generate_vless_reality_link() {
    local name=$1
    local uuid=$2
    local domain=$(get_setting "domain")
    local country=$(get_setting "country")
    local sni=$(get_setting "sni")
    local public_key=$(get_setting "reality_public_key")
    local short_id=$(get_setting "reality_short_id")
    
    echo "vless://${uuid}@${domain}:2083?type=tcp&encryption=none&security=reality&pbk=${public_key}&fp=chrome&sni=${sni}&sid=${short_id}&spx=%2F&flow=xtls-rprx-vision#${name}-vless-reality-${country}"
}

generate_vless_ws_link() {
    local name=$1
    local uuid=$2
    local domain=$(get_setting "domain")
    local country=$(get_setting "country")
    
    # Get current theme primary path
    local theme_data=$(get_theme_data)
    local paths_str=$(echo "$theme_data" | awk -F'|' '{print $1}' | cut -d':' -f2)
    local primary_path_raw=$(echo "$paths_str" | cut -d',' -f1)
    if [[ -z "$primary_path_raw" || "$primary_path_raw" == "null" ]]; then
        primary_path_raw="/chat"
    fi
    local salted_path=$(get_salted_path "$primary_path_raw")
    
    # URL encode path for link
    local encoded_path=$(echo -n "$salted_path" | python3 -c "import urllib.parse, sys; print(urllib.parse.quote(sys.stdin.read()), end='')")
    
    echo "vless://${uuid}@${domain}:443?type=ws&security=tls&path=${encoded_path}&encryption=none#${name}-vless-ws-${country}"
}

generate_xhttp_stealth_link() {
    local name=$1
    local uuid=$2
    local domain=$(get_setting "domain")
    local country=$(get_setting "country")
    
    local theme_data=$(get_theme_data)
    local paths_str=$(echo "$theme_data" | awk -F'|' '{print $1}' | cut -d':' -f2)
    # Use 2nd path for XHTTP Stealth (corresponds to odd index in nginx loop)
    local primary_path_raw=$(echo "$paths_str" | cut -d',' -f2)
    if [[ -z "$primary_path_raw" || "$primary_path_raw" == "null" ]]; then
        primary_path_raw="/chat"
    fi
    local salted_path=$(get_salted_path "$primary_path_raw")
    local encoded_path=$(echo -n "$salted_path" | python3 -c "import urllib.parse, sys; print(urllib.parse.quote(sys.stdin.read()), end='')")
    
    echo "vless://${uuid}@${domain}:443?type=xhttp&security=tls&path=${encoded_path}&encryption=none&mode=auto&host=${domain}#${name}-xhttp-stealth-${country}"
}

generate_hysteria2_link() {
    local name=$1
    local password=$2
    local domain=$(get_setting "domain")
    local country=$(get_setting "country")
    local obfs_password=$(get_setting "hysteria_obfs_password")
    
    echo "hysteria2://${password}@${domain}:8443?obfs=salamander&obfs-password=${obfs_password}&sni=${domain}#${name}-hysteria2-${country}"
}

generate_xhttp_link() {
    local name=$1
    local uuid=$2
    local domain=$(get_setting "domain")
    local country=$(get_setting "country")
    
    echo "vless://${uuid}@${domain}:2053?type=xhttp&security=tls&path=%2F&encryption=none&mode=auto&host=${domain}#${name}-xhttp-${country}"
}

generate_xhttp_reality_link() {
    local name=$1
    local uuid=$2
    local domain=$(get_setting "domain")
    local country=$(get_setting "country")
    local sni=$(get_setting "sni")
    local public_key=$(get_setting "reality_public_key")
    local short_id=$(get_setting "reality_short_id")
    
    echo "vless://${uuid}@${domain}:8443?type=xhttp&security=reality&pbk=${public_key}&fp=chrome&sni=${sni}&sid=${short_id}&spx=%2F&mode=auto&host=${sni}#${name}-xhttp-reality-${country}"
}

generate_tuic_link() {
    local name=$1
    local uuid=$2
    local password=$3
    local domain=$(get_setting "domain")
    local country=$(get_setting "country")
    
    # TUIC uses uuid and password
    echo "tuic://${uuid}:${password}@${domain}:8550?congestion_control=bbr&udp_relay_mode=native&alpn=h3&allow_insecure=0#${name}-tuic-${country}"
}

generate_http_link() {
    local name=$1
    local password=$2
    local domain=$(get_setting "domain")
    local country=$(get_setting "country")
    
    echo "http://${name}:${password}@${domain}:52143#${name}-http-${country}"
}

generate_socks_link() {
    local name=$1
    local password=$2
    local domain=$(get_setting "domain")
    local country=$(get_setting "country")
    
    echo "socks5://${name}:${password}@${domain}:52144#${name}-socks-${country}"
}

generate_subscription_file() {
    local name=$1
    local uuid=$2
    local password=$3
    local hash=$(generate_client_hash "$uuid")
    local domain=$(get_setting "domain")
    local sub_dir="/var/www/cdn/subs"
    
    mkdir -p "$sub_dir"
    
    # Collect all links
    local links=""
    while IFS= read -r protocol; do
        case "$protocol" in
            "vless-reality")
                links+=$(generate_vless_reality_link "$name" "$uuid")
                links+=$'\n'
                ;;
            "hysteria2")
                links+=$(generate_hysteria2_link "$name" "$password")
                links+=$'\n'
                ;;
            "xhttp")
                links+=$(generate_xhttp_link "$name" "$uuid")
                links+=$'\n'
                ;;
            "xhttp-reality")
                links+=$(generate_xhttp_reality_link "$name" "$uuid")
                links+=$'\n'
                ;;
            "tuic")
                links+=$(generate_tuic_link "$name" "$uuid" "$password")
                links+=$'\n'
                ;;
            "vless-ws")
                links+=$(generate_vless_ws_link "$name" "$uuid")
                links+=$'\n'
                ;;
            "xhttp-stealth")
                links+=$(generate_xhttp_stealth_link "$name" "$uuid")
                links+=$'\n'
                ;;
            "http")
                links+=$(generate_http_link "$name" "$password")
                links+=$'\n'
                ;;
            "socks")
                links+=$(generate_socks_link "$name" "$password")
                links+=$'\n'
                ;;
        esac
    done < <(get_protocols)
    
    # Base64 encode and save
    echo -n "$links" | base64 -w 0 > "$sub_dir/$hash"
}

regenerate_all_subscriptions() {
    local sub_dir="/var/www/cdn/subs"
    rm -rf "$sub_dir"/*
    
    if [[ -f "$CLIENTS_FILE" ]]; then
        while IFS= read -r client_json; do
            local name=$(echo "$client_json" | jq -r '.name')
            local uuid=$(echo "$client_json" | jq -r '.uuid')
            # fallback password to uuid 
            local password=$(echo "$client_json" | jq -r '.password // .uuid')
            generate_subscription_file "$name" "$uuid" "$password"
        done < <(jq -c '.[]' "$CLIENTS_FILE")
    fi
}

get_subscription_url() {
    local uuid=$1
    local domain=$(get_setting "domain")
    local hash=$(generate_client_hash "$uuid")
    echo "https://${domain}/${hash}"
}

rebuild_config() {
    print_info "Updating configuration..."
    
    # Backup current config
    local backup_file="${CONFIG_FILE}.backup"
    if [[ -f "$CONFIG_FILE" ]]; then
        cp "$CONFIG_FILE" "$backup_file"
    fi
    
    local inbounds="[]"
    
    # Add inbound for each active protocol
    while IFS= read -r protocol; do
        case "$protocol" in
            "vless-reality")
                local vless_inbound=$(generate_vless_reality_inbound)
                inbounds=$(echo "$inbounds" | jq --argjson inbound "$vless_inbound" '. += [$inbound]')
                ;;
            "hysteria2")
                local hysteria_inbound=$(generate_hysteria2_inbound)
                inbounds=$(echo "$inbounds" | jq --argjson inbound "$hysteria_inbound" '. += [$inbound]')
                ;;
            "xhttp")
                local xhttp_inbound=$(generate_xhttp_inbound)
                inbounds=$(echo "$inbounds" | jq --argjson inbound "$xhttp_inbound" '. += [$inbound]')
                ;;
            "xhttp-reality")
                local xhttp_reality_inbound=$(generate_xhttp_reality_inbound)
                inbounds=$(echo "$inbounds" | jq --argjson inbound "$xhttp_reality_inbound" '. += [$inbound]')
                ;;
            "tuic")
                local tuic_inbound=$(generate_tuic_inbound)
                inbounds=$(echo "$inbounds" | jq --argjson inbound "$tuic_inbound" '. += [$inbound]')
                ;;
            "vless-ws")
                local vless_ws_inbound=$(generate_vless_ws_inbound)
                inbounds=$(echo "$inbounds" | jq --argjson inbound "$vless_ws_inbound" '. += [$inbound]')
                ;;
            "xhttp-stealth")
                local xhttp_stealth_inbound=$(generate_xhttp_stealth_inbound)
                inbounds=$(echo "$inbounds" | jq --argjson inbound "$xhttp_stealth_inbound" '. += [$inbound]')
                ;;
            "http")
                local http_inbound=$(generate_http_inbound)
                inbounds=$(echo "$inbounds" | jq --argjson inbound "$http_inbound" '. += [$inbound]')
                ;;
            "socks")
                local socks_inbound=$(generate_socks_inbound)
                inbounds=$(echo "$inbounds" | jq --argjson inbound "$socks_inbound" '. += [$inbound]')
                ;;
        esac
    done < <(get_protocols)
    
    # Update config.json with error handling
    local tmp=$(mktemp)
    if ! jq --argjson inbounds "$inbounds" '.inbounds = $inbounds' "$CONFIG_FILE" > "$tmp" 2>/dev/null; then
        print_error "Failed to generate config, restoring backup"
        if [[ -f "$backup_file" ]]; then
            mv "$backup_file" "$CONFIG_FILE"
        fi
        rm -f "$tmp"
        return 1
    fi
    
    # Validate JSON
    if ! jq empty "$tmp" 2>/dev/null; then
        print_error "Generated config is invalid, restoring backup"
        if [[ -f "$backup_file" ]]; then
            mv "$backup_file" "$CONFIG_FILE"
        fi
        rm -f "$tmp"
        return 1
    fi
    
    mv "$tmp" "$CONFIG_FILE"
    rm -f "$backup_file"
    
    # Regenerate subscription files
    regenerate_all_subscriptions
    
    print_success "Configuration updated"
}

# Client helpers
validate_client_name() {
    local name=$1
    if [[ ! "$name" =~ ^[a-z0-9_-]+$ ]] || [[ -z "$name" ]]; then
        return 1
    fi
    return 0
}

client_exists() {
    local name=$1
    jq -e --arg name "$name" '.[] | select(.name == $name)' "$CLIENTS_FILE" >/dev/null 2>&1
}

add_client() {
    echo ""
    echo "=== Add Client ==="
    echo ""
    
    echo "Enter client name(s). For multiple clients, separate with commas."
    echo "Example: alice, bob, charlie"
    echo ""
    read -p "Client name(s): " input
    
    IFS=',' read -ra names <<< "$input"
    
    local valid_names=()
    local has_errors=false
    
    for raw_name in "${names[@]}"; do
        local name=$(echo "$raw_name" | tr -d ' ')
        
        if [[ -z "$name" ]]; then
            continue
        fi
        
        if ! validate_client_name "$name"; then
            print_error "Invalid name '$name' [only a-z, 0-9, -, _]"
            has_errors=true
            continue
        fi
        
        if client_exists "$name"; then
            print_error "Client '$name' already exists"
            has_errors=true
            continue
        fi
        
        valid_names+=("$name")
    done
    
    if [[ "$has_errors" == "true" && ${#valid_names[@]} -gt 0 ]]; then
        echo ""
        read -p "Continue with valid names only? y/n: " confirm
        if [[ "$confirm" != "y" ]]; then
            return
        fi
    fi
    
    if [[ ${#valid_names[@]} -eq 0 ]]; then
        print_error "No valid names to add"
        return
    fi
    
    local created_clients=()
    
    for name in "${valid_names[@]}"; do
        local uuid=$(generate_uuid)
        local password=$(generate_password)
        
        local tmp=$(mktemp)
        jq --arg name "$name" --arg uuid "$uuid" --arg password "$password" '. += [{name: $name, uuid: $uuid, password: $password}]' "$CLIENTS_FILE" > "$tmp"
        mv "$tmp" "$CLIENTS_FILE"
        
        generate_subscription_file "$name" "$uuid" "$password"
        
        created_clients+=("$name:$uuid:$password")
        print_success "Client '$name' created"
    done
    
    rebuild_config
    
    print_info "Restarting service..."
    systemctl restart sing-box
    sleep 2
    
    if systemctl is-active --quiet sing-box; then
        echo ""
        print_success "All clients added successfully"
        echo ""
        
        for client_data in "${created_clients[@]}"; do
            IFS=':' read -r name uuid password <<< "$client_data"
            show_client_links_internal "$name" "$uuid" "$password"
            echo ""
        done
    else
        print_error "Launch error! Check logs."
    fi
}

remove_client() {
    echo ""
    echo "=== Remove Client ==="
    echo ""
    
    local client_count=$(jq 'length' "$CLIENTS_FILE")
    if [[ "$client_count" -eq 0 ]]; then
        print_warning "No clients"
        return
    fi
    
    echo "Clients:"
    local clients=($(jq -r '.[] | .name' "$CLIENTS_FILE"))
    for i in "${!clients[@]}"; do
        echo "$((i+1))) ${clients[$i]}"
    done
    echo "99) Remove all clients"
    echo "0) Back"
    echo ""
    
    read -p "Your choice: " choice
    
    if [[ "$choice" == "0" ]]; then
        return
    fi
    
    if [[ "$choice" == "99" ]]; then
        read -p "Are you sure you want to remove ALL clients? y/n: " confirm
        if [[ "$confirm" != "y" ]]; then
            return
        fi
        echo "[]" > "$CLIENTS_FILE"
        print_success "All clients removed"
    else
        if ! [[ "$choice" =~ ^[0-9]+$ ]]; then
            print_error "Invalid choice"
            return
        fi
        
        local index=$((choice - 1))
        if [[ $index -lt 0 ]] || [[ $index -ge ${#clients[@]} ]]; then
            print_error "Invalid choice"
            return
        fi
        
        local name="${clients[$index]}"
        
        read -p "Remove client '$name'? y/n: " confirm
        if [[ "$confirm" != "y" ]]; then
            return
        fi
        
        local tmp=$(mktemp)
        jq --arg name "$name" 'del(.[] | select(.name == $name))' "$CLIENTS_FILE" > "$tmp"
        mv "$tmp" "$CLIENTS_FILE"
        print_success "Client '$name' removed"
    fi
    
    rebuild_config
    
    print_info "Restarting service..."
    systemctl restart sing-box
    sleep 2
    
    if systemctl is-active --quiet sing-box; then
        print_success "Client removed"
    else
        print_error "Launch error! Check logs."
    fi
    echo ""
}

show_client_links_internal() {
    local name=$1
    local uuid=$2
    local password=$3
    
    echo "Links for $name:"
    echo ""
    
    while IFS= read -r protocol; do
        case "$protocol" in
            "vless-reality")
                echo "VLESS Reality:"
                generate_vless_reality_link "$name" "$uuid"
                echo ""
                ;;
            "hysteria2")
                echo "Hysteria2:"
                generate_hysteria2_link "$name" "$password"
                echo ""
                ;;
            "xhttp")
                echo "XHTTP:"
                generate_xhttp_link "$name" "$uuid"
                echo ""
                ;;
            "xhttp-reality")
                echo "XHTTP Reality:"
                generate_xhttp_reality_link "$name" "$uuid"
                echo ""
                ;;
            "tuic")
                echo "TUIC v5:"
                generate_tuic_link "$name" "$uuid" "$password"
                echo ""
                ;;
            "vless-ws")
                echo "VLESS WebSocket (Nginx):"
                generate_vless_ws_link "$name" "$uuid"
                echo ""
                ;;
            "xhttp-stealth")
                echo "XHTTP Stealth (Nginx):"
                generate_xhttp_stealth_link "$name" "$uuid"
                echo ""
                ;;
            "http")
                echo "HTTP (Proxy):"
                generate_http_link "$name" "$password"
                echo ""
                ;;
            "socks")
                echo "SOCKS5 (Proxy):"
                generate_socks_link "$name" "$password"
                echo ""
                ;;
        esac
    done < <(get_protocols)
    
    local sub_url=$(get_subscription_url "$uuid")
    echo "---"
    echo "Subscription URL:"
    echo "$sub_url"
}

show_client_links() {
    echo ""
    echo "=== Client Links ==="
    echo ""
    
    local client_count=$(jq 'length' "$CLIENTS_FILE")
    if [[ "$client_count" -eq 0 ]]; then
        print_warning "No clients"
        return
    fi
    
    echo "Clients:"
    local clients=($(jq -r '.[] | .name' "$CLIENTS_FILE"))
    for i in "${!clients[@]}"; do
        echo "$((i+1))) ${clients[$i]}"
    done
    echo "0) Back"
    echo ""
    
    read -p "Your choice: " choice
    
    if [[ "$choice" == "0" ]]; then
        return
    fi
    
    if ! [[ "$choice" =~ ^[0-9]+$ ]]; then
        print_error "Invalid choice"
        return
    fi
    
    local index=$((choice - 1))
    if [[ $index -lt 0 ]] || [[ $index -ge ${#clients[@]} ]]; then
        print_error "Invalid choice"
        return
    fi
    
    local name="${clients[$index]}"
    local uuid=$(jq -r --arg name "$name" '.[] | select(.name == $name) | .uuid' "$CLIENTS_FILE")
    local password=$(jq -r --arg name "$name" '.[] | select(.name == $name) | .password // .uuid' "$CLIENTS_FILE")
    
    echo ""
    show_client_links_internal "$name" "$uuid" "$password"
    echo ""
}

list_clients() {
    echo ""
    echo "=== Client List ==="
    echo ""
    
    local client_count=$(jq 'length' "$CLIENTS_FILE")
    
    if [[ "$client_count" -eq 0 ]]; then
        print_warning "No clients"
    else
        echo "Clients ($client_count):"
        jq -r '.[] | .name' "$CLIENTS_FILE" | nl
    fi
    
    echo ""
}
