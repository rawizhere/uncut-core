#!/bin/bash

install_acme_sh() {
    local email=$(get_setting "email")
    local domain=$(get_setting "domain")
    
    if [[ -z "$domain" ]]; then
        print_error "Domain not found in settings"
        return 1
    fi

    local cert_crt="$INSTALL_DIR/certs/certificates/$domain.crt"
    local cert_key="$INSTALL_DIR/certs/certificates/$domain.key"
    
    # Check if certificates already exist
    if [[ -f "$cert_crt" ]] && [[ -f "$cert_key" ]]; then
        print_info "Certificates already exist. Skipping issuance."
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
    print_info "Issuing SSL certificate..."
    
    # Stop Nginx to release port 80 only if it exists and is running
    if systemctl is-active --quiet nginx; then
        systemctl stop nginx
    fi
    
    mkdir -p "$INSTALL_DIR/certs/certificates"

    if /root/.acme.sh/acme.sh --issue --standalone -d "$domain" --force; then
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
