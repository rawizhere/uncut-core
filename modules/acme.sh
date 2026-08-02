#!/bin/bash

is_cert_valid() {
    local cert_file=$1
    if [[ ! -f "$cert_file" ]]; then
        return 1
    fi
    
    # Reject self-signed certificates
    local issuer_raw=$(openssl x509 -in "$cert_file" -noout -issuer 2>/dev/null | cut -d= -f2-)
    local subject_raw=$(openssl x509 -in "$cert_file" -noout -subject 2>/dev/null | cut -d= -f2-)
    
    if [[ -z "$issuer_raw" || "$issuer_raw" == "$subject_raw" ]]; then
        return 1
    fi

    # Ensure certificate is issued by a recognized CA (e.g. Let's Encrypt / ZeroSSL)
    if ! openssl x509 -in "$cert_file" -noout -issuer 2>/dev/null | grep -qE "Let's Encrypt|ZeroSSL|Encryption|Authority|Trust|R3|R10|R11"; then
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
    
    mkdir -p "$INSTALL_DIR/certs/certificates"

    if [[ "$force_issue" == "false" ]] && is_cert_valid "$cert_crt"; then
        print_info "Certificates already exist and are valid. Skipping issuance."
        print_success "Using existing certificates: $cert_crt"
        return
    fi

    # Remove invalid/self-signed cert if present
    if ! is_cert_valid "$cert_crt"; then
        rm -f "$cert_crt" "$cert_key"
    fi
    
    print_info "Installing acme.sh..."
    
    if [[ ! -d "/root/.acme.sh" ]]; then
        if ! curl https://get.acme.sh | sh -s email="$email" >/dev/null 2>&1; then
            print_error "Failed to install acme.sh"
        fi
    fi
    
    # Ensure acme.sh is using Let's Encrypt
    /root/.acme.sh/acme.sh --set-default-ca --server letsencrypt >/dev/null 2>&1

    # Issue certificate
    print_info "Issuing SSL certificate via Let's Encrypt (Webroot)..."
    
    mkdir -p "/var/www/html"

    if /root/.acme.sh/acme.sh --issue --webroot /var/www/html -d "$domain" --server letsencrypt --force; then
        print_success "Certificate issued"
        
        # Install certificate
        print_info "Installing certificate..."
        
        /root/.acme.sh/acme.sh --install-cert -d "$domain" \
            --key-file       "$cert_key" \
            --fullchain-file "$cert_crt" \
            --reloadcmd     "systemctl restart nginx sing-box >/dev/null 2>&1 || true"
            
        /root/.acme.sh/acme.sh --install-cronjob >/dev/null 2>&1 || true

        chmod 644 "$cert_crt"
        chmod 600 "$cert_key"
        
        print_success "Let's Encrypt certificate installed successfully"
    else
        print_error "Failed to issue Let's Encrypt certificate for $domain"
        return 1
    fi

    # Final check
    if [[ ! -f "$cert_crt" ]]; then
        print_error "Critical: Certificate file not found along path: $cert_crt"
        return 1
    fi
}
