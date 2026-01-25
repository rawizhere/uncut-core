#!/bin/bash

# Domain validation
validate_domain() {
    local domain=$1
    if [[ ! "$domain" =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]*\.[a-zA-Z]{2,}$ ]]; then
        return 1
    fi
    return 0
}

# Check if domain points to this server
check_domain_dns() {
    local domain=$1
    local server_ip=$(curl -s --connect-timeout 3 https://api.ipify.org 2>/dev/null || curl -s --connect-timeout 3 https://ifconfig.me 2>/dev/null)
    local domain_ip=$(dig +short "$domain" 2>/dev/null | tail -n1)
    
    if [[ -z "$server_ip" ]]; then
        print_warning "Could not determine server IP"
        return 0  # Allow to proceed
    fi
    
    if [[ -z "$domain_ip" ]]; then
        print_error "Domain '$domain' does not resolve to any IP"
        return 1
    fi
    
    if [[ "$server_ip" != "$domain_ip" ]]; then
        print_warning "Domain '$domain' resolves to $domain_ip, but server IP is $server_ip"
        read -p "Continue anyway? y/n: " confirm
        if [[ "$confirm" != "y" ]]; then
            return 1
        fi
    fi
    
    return 0
}

# Check if required ports are available
check_ports_available() {
    local ports_in_use=""
    
    # Check port 80
    if ss -tlnp 2>/dev/null | grep -q ":80 " || netstat -tlnp 2>/dev/null | grep -q ":80 "; then
        local proc80=$(ss -tlnp 2>/dev/null | grep ":80 " | awk '{print $NF}' | head -n1)
        ports_in_use+="  - Port 80 is in use by: $proc80\n"
    fi
    
    # Check port 443
    if ss -tlnp 2>/dev/null | grep -q ":443 " || netstat -tlnp 2>/dev/null | grep -q ":443 "; then
        local proc443=$(ss -tlnp 2>/dev/null | grep ":443 " | awk '{print $NF}' | head -n1)
        ports_in_use+="  - Port 443 is in use by: $proc443\n"
    fi
    
    if [[ -n "$ports_in_use" ]]; then
        print_warning "Some ports are already in use:"
        echo -e "$ports_in_use"
        read -p "Continue anyway? y/n: " confirm
        if [[ "$confirm" != "y" ]]; then
            return 1
        fi
    fi
    
    return 0
}

# Enable BBR
enable_bbr() {
    print_info "Enabling BBR..."
    if ! grep -q "net.core.default_qdisc=fq" /etc/sysctl.conf; then
        echo "net.core.default_qdisc=fq" >> /etc/sysctl.conf
    fi
    if ! grep -q "net.ipv4.tcp_congestion_control=bbr" /etc/sysctl.conf; then
        echo "net.ipv4.tcp_congestion_control=bbr" >> /etc/sysctl.conf
    fi
    sysctl -p >/dev/null 2>&1
    print_success "BBR enabled"
}

# Configure UFW firewall
setup_firewall() {
    print_info "Configuring UFW firewall..."
    
    # Install UFW if not installed
    if ! command -v ufw &> /dev/null; then
        apt-get install -y ufw > /dev/null 2>&1
    fi
    
    # Ask about SSH port
    local ssh_port=22
    echo ""
    read -p "Enter SSH port [22]: " custom_ssh_port
    if [[ -n "$custom_ssh_port" ]]; then
        if [[ "$custom_ssh_port" =~ ^[0-9]+$ ]] && [[ "$custom_ssh_port" -ge 1 ]] && [[ "$custom_ssh_port" -le 65535 ]]; then
            ssh_port=$custom_ssh_port
            if [[ "$ssh_port" != "22" ]]; then
                print_info "Configuring SSH to listen on port $ssh_port..."
                
                # Backup config
                cp /etc/ssh/sshd_config /etc/ssh/sshd_config.bak
                
                # Update config
                if grep -q "^Port " /etc/ssh/sshd_config; then
                    sed -i "s/^Port .*/Port $ssh_port/" /etc/ssh/sshd_config
                elif grep -q "^#Port " /etc/ssh/sshd_config; then
                    sed -i "s/^#Port .*/Port $ssh_port/" /etc/ssh/sshd_config
                else
                    echo "Port $ssh_port" >> /etc/ssh/sshd_config
                fi
                
                # Check for socket activation (common on Ubuntu 22.04+)
                if systemctl is-active --quiet ssh.socket; then
                    print_info "Disabling ssh.socket to enforce sshd_config port..."
                    systemctl stop ssh.socket
                    systemctl disable ssh.socket
                fi
                
                # Restart SSH
                if systemctl restart ssh >/dev/null 2>&1 || systemctl restart sshd >/dev/null 2>&1; then
                    print_success "SSH configuration updated and service restarted"
                else
                    print_error "Failed to restart SSH service. Check config manually."
                fi
            fi
        else
            print_warning "Invalid port, using 22"
        fi
    fi
    
    # Disable IPv6 in UFW (as requested)
    sed -i 's/IPV6=yes/IPV6=no/g' /etc/default/ufw

    
    # Reset rules
    ufw --force reset > /dev/null 2>&1
    
    # Allow SSH (critical!)
    ufw allow ${ssh_port}/tcp comment 'SSH' > /dev/null 2>&1
    
    # Allow proxy ports
    ufw allow 80/tcp comment 'HTTP/ACME' > /dev/null 2>&1
    ufw allow 443/tcp comment 'HTTPS/CDN' > /dev/null 2>&1
    ufw allow 2083/tcp comment 'VLESS Reality' > /dev/null 2>&1
    ufw allow 2053/tcp comment 'XHTTP' > /dev/null 2>&1
    ufw allow 8443/tcp comment 'XHTTP Reality' > /dev/null 2>&1
    ufw allow 8443/udp comment 'Hysteria2' > /dev/null 2>&1
    ufw allow 8550/udp comment 'TUIC' > /dev/null 2>&1
    ufw allow 52143/tcp comment 'HTTP Proxy' > /dev/null 2>&1
    ufw allow 52144/tcp comment 'SOCKS Proxy' > /dev/null 2>&1
    
    # Enable firewall
    ufw --force enable > /dev/null 2>&1
    
    print_success "Firewall configured"
    echo "Open ports: ${ssh_port}(SSH), 80, 443, 2053, 2083, 8443(TCP/UDP), 8550(UDP), 52143, 52144"
    
    # Install and configure Fail2ban for SSH protection
    setup_fail2ban
}

# Setup Fail2ban for SSH protection
setup_fail2ban() {
    print_info "Configuring Fail2ban for SSH protection..."
    
    # Install fail2ban if not installed
    if ! command -v fail2ban-client &> /dev/null; then
        apt-get install -y fail2ban > /dev/null 2>&1
    fi
    
    # Create SSH jail configuration
    local template_file="$SCRIPT_DIR/templates/fail2ban.conf.template"
    if [[ -f "$template_file" ]]; then
        envsubst < "$template_file" > /etc/fail2ban/jail.d/uncut-core-ssh.conf
    else
        print_warning "Template not found at $template_file. Using fallback."
        cat > /etc/fail2ban/jail.d/uncut-core-ssh.conf <<EOF
[sshd]
enabled = true
port = ssh
filter = sshd
logpath = /var/log/auth.log
maxretry = 5
bantime = 3600
findtime = 600
EOF
    fi

    # Create Nginx scraping protection
    print_info "Adding Nginx scraping protection..."
    local n_filter_tmpl="$SCRIPT_DIR/templates/fail2ban_nginx_filter.conf.template"
    local n_jail_tmpl="$SCRIPT_DIR/templates/fail2ban_nginx_jail.conf.template"

    if [[ -f "$n_filter_tmpl" ]] && [[ -f "$n_jail_tmpl" ]]; then
        cat "$n_filter_tmpl" > /etc/fail2ban/filter.d/nginx-forbidden.conf
        cat "$n_jail_tmpl" > /etc/fail2ban/jail.d/uncut-core-nginx.conf
        print_success "Nginx protection added (10 attempts/min -> 1h ban)"
    fi

    
    # Restart fail2ban
    systemctl enable fail2ban > /dev/null 2>&1
    systemctl restart fail2ban > /dev/null 2>&1
    
    print_success "Fail2ban configured (SSH: 5 attempts, Nginx: 10 attempts)"
}
