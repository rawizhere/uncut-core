#!/bin/bash

is_cert_valid() {
    local cert_file=$1
    if [[ ! -f "$cert_file" ]]; then
        return 1
    fi
    
    local expiry_date=$(openssl x509 -in "$cert_file" -noout -enddate | cut -d= -f2)
    local expiry_epoch=$(date -d "$expiry_date" +%s 2>/dev/null)
    
    if [[ -z "$expiry_epoch" ]]; then
        return 1
    fi
    
    local now_epoch=$(date +%s)
    local min_validity=$(( 30 * 86400 ))
    
    if (( expiry_epoch - now_epoch < min_validity )); then
        return 1
    fi
    return 0
}

install_acme_sh() {
    local force_issue="false"
    [[ "$1" == "--force" ]] && force_issue="true"
    
    local email=$(get_setting "email")
    local domain=$(get_setting "domain")
    
    if [[ -z "$domain" ]]; then
        print_error "Domain not found in settings"
        return 1
    fi

    local cert_crt="$INSTALL_DIR/certs/certificates/$domain.crt"
    local cert_key="$INSTALL_DIR/certs/certificates/$domain.key"
    
    if [[ "$force_issue" == "false" ]] && is_cert_valid "$cert_crt"; then
        print_info "Certificates already exist and are valid. Skipping issuance."
        print_success "Using existing certificates: $cert_crt"
        return
    fi
    
    print_info "Installing acme.sh..."
    
    if [[ ! -d "/root/.acme.sh" ]]; then
        if ! curl https://get.acme.sh | sh -s email="$email" >/dev/null 2>&1; then
            print_error "Failed to install acme.sh"
        fi
    fi
    
    # Issue certificate
    print_info "Issuing SSL certificate via Webroot (Zero Downtime)..."
    
    mkdir -p "$INSTALL_DIR/certs/certificates"
    mkdir -p "/var/www/html"

    if /root/.acme.sh/acme.sh --issue --webroot /var/www/html -d "$domain" --force; then
        print_success "Certificate issued"
        
        # Install certificate
        print_info "Installing certificate..."
        
        /root/.acme.sh/acme.sh --install-cert -d "$domain" \
            --key-file       "$INSTALL_DIR/certs/certificates/$domain.key" \
            --fullchain-file "$INSTALL_DIR/certs/certificates/$domain.crt" \
            --reloadcmd     "if systemctl is-active --quiet nginx; then systemctl restart nginx; fi"
            
        chmod 644 "$INSTALL_DIR/certs/certificates/$domain.crt"
        chmod 600 "$INSTALL_DIR/certs/certificates/$domain.key"
        
        print_success "Certificate installed"
    else
        print_error "Failed to issue certificate"
        
        # Fallback to self-signed
        print_warning "Falling back to self-signed certificate..."
        
        if openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
            -keyout "$INSTALL_DIR/certs/certificates/$domain.key" \
            -out "$INSTALL_DIR/certs/certificates/$domain.crt" \
            -subj "/CN=$domain"; then
            
            chmod 644 "$INSTALL_DIR/certs/certificates/$domain.crt"
            chmod 600 "$INSTALL_DIR/certs/certificates/$domain.key"
            
            print_success "Self-signed certificate created"
        else
            print_error "Failed to generate self-signed certificate"
        fi
    fi
    
    # Final check
    if [[ ! -f "$cert_crt" ]]; then
        print_error "Critical: Certificate file not found along path: $cert_crt"
        return 1
    fi
}
