#!/bin/bash

show_menu() {
    clear
    echo "=== Uncut Core ==="
    
    if is_installed; then
        # Service status
        local sb_status="${RED}Stopped${NC}"
        systemctl is-active --quiet sing-box && sb_status="${GREEN}Running${NC}"
        
        local nx_status="${RED}Stopped${NC}"
        systemctl is-active --quiet nginx && nx_status="${GREEN}Running${NC}"
        
        # System status
        local cpu_usage=$(top -bn1 | grep "Cpu(s)" | sed "s/.*, *\([0-9.]*\)%* id.*/\1/" | awk '{print 100 - $1"%"}')
        local ram_usage=$(free -m | awk '/Mem:/ { print $3"MB / "$2"MB" }')
        local uptime_val=$(uptime -p | sed 's/up //')
        
        # Network/Config
        local server_ip=$(curl -s --connect-timeout 2 https://api.ipify.org 2>/dev/null || echo "Unknown")
        local domain=$(get_setting "domain")
        local protocol_count=$(get_protocols | wc -l)
        local client_count=$(jq 'length' "$CLIENTS_FILE" 2>/dev/null || echo "0")
        local auto_update=$(get_setting "auto_update" "false")
        
        local ssl_info="${RED}Not found${NC}"
        if [[ -n "$domain" ]]; then
            local cert_file="$INSTALL_DIR/certs/certificates/$domain.crt"
            if [[ -f "$cert_file" ]]; then
                local expiry_date=$(openssl x509 -in "$cert_file" -noout -enddate | cut -d= -f2)
                if is_cert_valid "$cert_file"; then
                    ssl_info="${GREEN}${expiry_date}${NC}"
                else
                    ssl_info="${RED}${expiry_date} (Expired/Renew Soon)${NC}"
                fi
            fi
        fi
        
        echo -e "Status: Sing-box: $sb_status | Nginx: $nx_status"
        echo -e "System: CPU: $cpu_usage | RAM: $ram_usage | Uptime: $uptime_val"
        echo -e "Server: IP: $server_ip | Domain: ${domain:-None} | SSL: $ssl_info"
        echo -e "Config: Protocols: $protocol_count | Clients: $client_count | Auto-update: $auto_update"
    else
        echo -e "Status: ${YELLOW}Not installed${NC}"
    fi
     
    echo "------------------------------------------------------------"
    echo -e " ${YELLOW}[CLIENTS]${NC}              ${YELLOW}[PROTOCOLS]${NC}            ${YELLOW}[SYSTEM]${NC}"
    echo " 1) Add Client          6) Add Protocol        11) System Health"
    echo " 2) Edit Client         7) Remove Protocol     12) Restart Services"
    echo " 3) Remove Client       8) List Protocols      13) Service Logs"
    echo " 4) List Clients        9) Change SNI          14) Renew SSL Cert"
    echo " 5) Get Links & QR     10) Rotate Mask Salt    15) MTProto Proxy"
    echo ""
    echo -e " ${YELLOW}[ENGINE & MAINTENANCE]${NC}"
    echo " 16) Update Core       17) Toggle Auto-Update  18) Change Engine Version"
    echo " 19) Install / Fix     20) Full Uninstall"
    echo ""
    echo " 0) Exit"
    echo "------------------------------------------------------------"
}

# Masking preset is now just rotate_masking_salt
# since AWS CloudFront is the exclusive theme
rotate_masking_salt() {
    local new_salt=$(openssl rand -hex 4)
    set_setting "protocol_salt" "$new_salt"
    print_success "Salt rotated: $new_salt"
    apply_masking_changes
}

apply_masking_changes() {
    print_info "Applying masking configuration..."
    # Update Nginx
    local domain=$(get_setting "domain")
    if [[ -n "$domain" ]]; then
        export NX_FAST_RECONFIG="true"
        setup_nginx_cdn "$domain"
    fi
    # Update Sing-box
    if command -v rebuild_config &>/dev/null; then
        rebuild_config
    fi
    # Restart services
    systemctl restart nginx sing-box
    print_success "Masking updated. Subscription links changed!"
}

toggle_auto_update() {
    local current=$(get_setting "auto_update" "false")
    if [[ "$current" == "true" ]]; then
        set_setting "auto_update" "false"
        print_success "Auto-update disabled"
    else
        set_setting "auto_update" "true"
        print_success "Auto-update enabled"
    fi
}
