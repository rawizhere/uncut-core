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
        
        echo -e "Status: Sing-box: $sb_status | Nginx: $nx_status"
        echo -e "System: CPU: $cpu_usage | RAM: $ram_usage | Uptime: $uptime_val"
        echo -e "Server: IP: $server_ip | Domain: ${domain:-None}"
        echo -e "Config: Protocols: $protocol_count | Clients: $client_count | Auto-update: $auto_update"
    else
        echo -e "Status: ${YELLOW}Not installed${NC}"
    fi
     
    echo "------------------------------------------------------------"
    echo " 1) Install           2) Update           3) Uninstall"
    echo ""
    echo " --- CLIENTS ---      --- PROTOCOLS ---   --- SYSTEM ---"
    echo " 4) Add Client       8) Add Proto        12) Status"
    echo " 5) Remove Client    9) Remove Proto     13) Restart"
    echo " 6) List Clients    10) List Protos      14) Stop"
    echo " 7) Get Links       11) Change SNI       15) Logs             16) Toggle Auto-update"
    echo " 17) Masking Settings"
    echo ""
    echo " 0) Exit"
    echo "------------------------------------------------------------"
}

masking_settings_menu() {
    while true; do
        echo ""
        echo "=== Masking Settings (v10) ==="
        echo "Current Theme: $(get_setting "masking_theme" "analytics")"
        echo "Current Salt:  $(get_setting "protocol_salt")"
        echo ""
        echo "1) Theme: Analytics (Google-like)"
        echo "2) Theme: Infrastructure (Sentry/Datadog-like)"
        echo "3) Theme: CDN Sync (AWS-like)"
        echo "4) Theme: Security (Enterprise-like)"
        echo "5) Rotate Global Salt (Affects Nginx regex)"
        echo "0) Back"
        echo ""
        read -p "Your choice: " mchoice
        
        case "$mchoice" in
            1) set_setting "masking_theme" "analytics"; apply_masking_changes ;;
            2) set_setting "masking_theme" "infrastructure"; apply_masking_changes ;;
            3) set_setting "masking_theme" "cdn_sync"; apply_masking_changes ;;
            4) set_setting "masking_theme" "security"; apply_masking_changes ;;
            5) rotate_masking_salt ;;
            0) break ;;
            *) print_error "Invalid choice" ;;
        esac
    done
}

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
