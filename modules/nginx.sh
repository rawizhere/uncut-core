#!/bin/bash

# Generate placeholder images for CDN masking
generate_placeholder_images() {
    local cdn_dir="/var/www/cdn/images"
    local source_image="$SCRIPT_DIR/data/placeholder.png"

    print_info "Generating placeholder images for CDN..."
    
    # Create directory
    mkdir -p "$cdn_dir"

    # Copy placeholder images using the binary asset
    # Copy assets
    if [[ -f "$SCRIPT_DIR/data/placeholder.png" ]]; then
        cp "$SCRIPT_DIR/data/placeholder.png" "$cdn_dir/placeholder.png"
    fi
    
    if [[ -f "$SCRIPT_DIR/data/1.png" ]]; then
        cp "$SCRIPT_DIR/data/1.png" "$cdn_dir/1.png"
    fi
    
    if [[ -f "$SCRIPT_DIR/data/2.png" ]]; then
        cp "$SCRIPT_DIR/data/2.png" "$cdn_dir/2.png"
    fi
    
    if [[ -f "$SCRIPT_DIR/data/logo.png" ]]; then
        cp "$SCRIPT_DIR/data/logo.png" "$cdn_dir/logo.png"
    fi
    
    print_success "CDN images updated from data directory"

    # Fallback if critical placeholder is missing (though unlikely in this flow)
    if [[ ! -f "$cdn_dir/placeholder.png" ]]; then
        print_warning "Placeholder image source not found at $source_image. Using fallback."
        # Backup: Base64-encoded 1x1 transparent PNG
        local tiny_png="iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
        echo "$tiny_png" | base64 -d > "$cdn_dir/placeholder.png"
        echo "$tiny_png" | base64 -d > "$cdn_dir/1.png"
        echo "$tiny_png" | base64 -d > "$cdn_dir/2.png"
        echo "$tiny_png" | base64 -d > "$cdn_dir/logo.png"
    fi
}

# Function to generate dynamic location blocks for Nginx
generate_nginx_masking_block() {
    local theme_data=$(get_theme_data)
    
    # Parse theme data (Format: paths:p1,p2|headers:h1,h2|mode:m)
    local paths_str=$(echo "$theme_data" | awk -F'|' '{print $1}' | cut -d':' -f2)
    local headers_str=$(echo "$theme_data" | awk -F'|' '{print $2}' | cut -d':' -f2)
    
    IFS=',' read -ra paths <<< "$paths_str"
    IFS=',' read -ra extra_headers <<< "$headers_str"
    
    local blocks=""
    local i=0
    for p in "${paths[@]}"; do
        local salted_path=$(get_salted_path "$p")
        
        blocks+="    # Masked Location: $p\n"
        blocks+="    location ~* ^${salted_path} {\n"
        blocks+="        proxy_redirect off;\n"
        
        # Custom logic per protocol
        if (( i % 2 == 0 )); then
             # VLESS-WS (Even) -> Port 10001
             blocks+="        proxy_pass http://127.0.0.1:10001;\n"
             blocks+="        proxy_set_header Upgrade \$http_upgrade;\n"
             blocks+="        proxy_set_header Connection \"upgrade\";\n"
        else
             # XHTTP-Stealth (Odd) -> Port 10002
             # Needs NO buffering and NO Upgrade headers
             blocks+="        proxy_pass http://127.0.0.1:10002;\n"
             blocks+="        proxy_buffering off;\n"
             blocks+="        proxy_request_buffering off;\n"
             blocks+="        client_max_body_size 0;\n"
             blocks+="        proxy_read_timeout 1h;\n"
             blocks+="        proxy_send_timeout 1h;\n"
             blocks+="        proxy_set_header X-Forwarded-Proto \$scheme;\n"
        fi
        
        blocks+="        proxy_http_version 1.1;\n"
        i=$((i+1))
        blocks+="        proxy_set_header Host \$host;\n"
        blocks+="        proxy_set_header X-Real-IP \$remote_addr;\n"
        blocks+="        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;\n"
        
        # Add preset specific headers
        for h in "${extra_headers[@]}"; do
            [[ -z "$h" ]] && continue
            local h_name=$(echo "$h" | cut -d':' -f1)
            local h_val=$(echo "$h" | cut -d':' -f2-)
            blocks+="        add_header $h_name \"$h_val\" always;\n"
        done
        
        blocks+="    }\n\n"
    done
    
    # Also add standard CDN paths inside the server block
    blocks+="    # API endpoints (fake CDN behavior)\n"
    blocks+="    location /api/status {\n"
    blocks+="        include /etc/nginx/snippets/cdn_headers.conf;\n"
    blocks+="        default_type \"application/json\";\n"
    blocks+="        return 200 '{\"status\":\"ok\",\"node\":\"${SECOND_LEVEL}\",\"region\":\"${CF_POP_REGION}\"}';\n"
    blocks+="    }\n\n"
    
    blocks+="    # robots.txt (every CDN has this)\n"
    blocks+="    location = /robots.txt {\n"
    blocks+="        include /etc/nginx/snippets/cdn_headers.conf;\n"
    blocks+="        default_type \"text/plain\";\n"
    blocks+="        return 200 \"User-agent: *\\nDisallow: /\\n\";\n"
    blocks+="    }\n\n"
    
    blocks+="    # security.txt\n"
    blocks+="    location = /.well-known/security.txt {\n"
    blocks+="        include /etc/nginx/snippets/cdn_headers.conf;\n"
    blocks+="        default_type \"text/plain\";\n"
    blocks+="        return 200 \"Contact: security@cloudfront.net\\nExpires: 2027-12-31T23:59:59.000Z\\n\";\n"
    blocks+="    }\n\n"
    
    blocks+="    # Favicon\n"
    blocks+="    location = /favicon.ico {\n"
    blocks+="        include /etc/nginx/snippets/cdn_headers.conf;\n"
    blocks+="        add_header Content-Length \"0\" always;\n"
    blocks+="        add_header Cache-Control \"public, max-age=86400\" always;\n"
    blocks+="        return 204;\n"
    blocks+="    }\n"

    echo -e "$blocks"
}

# Configure Nginx for CDN masking
setup_nginx_cdn() {
    local domain=$1
    
    if [[ -z "$domain" ]]; then
        print_error "Domain not specified for Nginx configuration"
        return 1
    fi
    
    print_info "Configuring Nginx CDN masking..."
    
    # Quick reconfig mode (for theme changes)
    if [[ "$NX_FAST_RECONFIG" == "true" ]]; then
        local domain=$(get_setting "domain")
        [[ -z "$domain" ]] && return
        # Skip installation, go straight to config generation
    else
        # Check Nginx installation
        if ! command -v nginx &> /dev/null; then
            print_info "Installing Nginx..."
            apt-get update -qq
            apt-get install -y nginx gettext-base > /dev/null 2>&1
            print_success "Nginx installed"
        else
            print_info "Nginx is already installed"
        fi
    fi

    # Ensure certificates exist (either real or self-signed) before anything else
    # This calls the function from modules/acme.sh which must be sourced
    install_acme_sh
    
    # Generate images
    generate_placeholder_images
    
    # Create subscription directory
    mkdir -p /var/www/cdn/subs
    
    # Extract second-level domain
    local domain_parts=(${domain//./ })
    export SECOND_LEVEL="${domain_parts[-2]}"
    
    # Generate realistic identifiers
    local cf_edge_id="d$(openssl rand -hex 7)"  # d + 14 hex chars = 15 total
    local edge_node_id="$(openssl rand -hex 6)"
    local cf_pop_codes=("FRA50-C1" "IAD89-C2" "LHR61-C1" "NRT57-C2" "SIN52-C1" "SYD62-C2")
    local cf_pop="${cf_pop_codes[$RANDOM % ${#cf_pop_codes[@]}]}"
    
    # Create Nginx configuration
    print_info "Creating Nginx configuration..."
    
    # Export variables for envsubst
    export EDGE_NODE_ID="$edge_node_id"
    export CF_EDGE_ID="$cf_edge_id"
    export CF_POP="$cf_pop"
    export DOMAIN="$domain"
    export INSTALL_DIR="$INSTALL_DIR"
    export CF_POP_REGION="${cf_pop:0:3}"
    export HOST_ID=$(openssl rand -base64 20 | tr -d '\n')
    export XHTTP_LOCATION_BLOCKS=$(generate_nginx_masking_block)
    
    # Create headers snippet
    mkdir -p /etc/nginx/snippets
    local template_headers="$SCRIPT_DIR/templates/nginx_cdn_headers.conf.template"
    
    if [[ -f "$template_headers" ]]; then
        envsubst '$SECOND_LEVEL $EDGE_NODE_ID $CF_EDGE_ID $CF_POP $DOMAIN $INSTALL_DIR $CF_POP_REGION' < "$template_headers" > /etc/nginx/snippets/cdn_headers.conf
    else
        print_warning "Template headers not found. Using fallback."
        # Fallback heredoc
        cat > /etc/nginx/snippets/cdn_headers.conf <<END
    add_header X-CDN-Node "${SECOND_LEVEL}-edge-${EDGE_NODE_ID}" always;
    add_header X-Cache "HIT" always;
    add_header X-Request-ID \$request_id always;
    add_header Via "1.1 ${CF_EDGE_ID}.cloudfront.net (CloudFront)" always;
    add_header Server "CloudFront" always;
    add_header Accept-Ranges "bytes" always;
    add_header Vary "Accept-Encoding, Origin" always;
    add_header Age "\$cache_age" always;
    add_header X-Amz-Cf-Id "\$request_id==" always;
    add_header X-Amz-Cf-Pop "${CF_POP}" always;
    add_header ETag "\"\$request_id\"" always;
    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;
END
    fi

    # Write dynamic location blocks to a separate file to avoid envsubst issues
    echo -e "$XHTTP_LOCATION_BLOCKS" > "$INSTALL_DIR/nginx_locations.conf"

    # Create site config
    local template_site="$SCRIPT_DIR/templates/nginx_site.conf.template"
    if [[ -f "$template_site" ]]; then
        # Only replace DOMAIN, INSTALL_DIR, and HOST_ID. 
        envsubst '$DOMAIN $INSTALL_DIR $HOST_ID' < "$template_site" > /etc/nginx/sites-available/cdn
    else
        print_warning "Template site config not found. Using fallback."
        cat > /etc/nginx/sites-available/cdn <<EOF
# Map for pseudo-random Age header (based on msec)
map \$msec \$cache_age {
    ~0\$ "0";
    ~1\$ "42";
    ~2\$ "87";
    ~3\$ "156";
    ~4\$ "203";
    ~5\$ "0";
    ~6\$ "91";
    ~7\$ "134";
    ~8\$ "267";
    ~9\$ "15";
    default "0";
}

# HTTP server - serves images and proxies ACME challenges
server {
    listen 80;
    server_name $DOMAIN;

    # Redirect everything to HTTPS
    location / {
        return 301 https://\$host\$request_uri;
    }
}

# HTTPS server - CDN masking
server {
    listen 443 ssl http2;
    server_name $DOMAIN;
    server_tokens off;

    # SSL certificates (managed by sing-box ACME)
    ssl_certificate $INSTALL_DIR/certs/certificates/$DOMAIN.crt;
    ssl_certificate_key $INSTALL_DIR/certs/certificates/$DOMAIN.key;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;

    # CDN-like headers (base)
    include /etc/nginx/snippets/cdn_headers.conf;

    # CORS headers (common for CDN)
    add_header Access-Control-Allow-Origin "*" always;
    add_header Access-Control-Allow-Methods "GET, HEAD, OPTIONS" always;

    # Subscriptions
    location ~ "^/([a-f0-9]{32})$" {
        include /etc/nginx/snippets/cdn_headers.conf;
        alias /var/www/cdn/subs/\$1;
        default_type "application/octet-stream";
        add_header Content-Disposition "inline";
    }

    # Images (with cache variation)
    location /images/ {
        include /etc/nginx/snippets/cdn_headers.conf;
        root /var/www/cdn;
        expires 30d;
        add_header Cache-Control "public, immutable";
        # Randomize cache status for realism
        set \$cache_type "Hit from cloudfront";
        add_header X-Cache "\$cache_type" always;
        try_files \$uri =404;
    }

    # API endpoints (fake CDN behavior)
    location /api/status {
        include /etc/nginx/snippets/cdn_headers.conf;
        default_type "application/json";
        return 200 '{"status":"ok","node":"${SECOND_LEVEL}","region":"${CF_POP_REGION}"}';
    }

    # robots.txt (every CDN has this) - MUST be before location /
    location = /robots.txt {
        include /etc/nginx/snippets/cdn_headers.conf;
        default_type "text/plain";
        return 200 "User-agent: *\nDisallow: /\n";
    }

    # security.txt (responsible disclosure) - MUST be before location /
    location = /.well-known/security.txt {
        include /etc/nginx/snippets/cdn_headers.conf;
        default_type "text/plain";
        return 200 "Contact: security@cloudfront.net\nExpires: 2027-12-31T23:59:59.000Z\n";
    }

    # Favicon (with proper headers) - MUST be before location /
    location = /favicon.ico {
        include /etc/nginx/snippets/cdn_headers.conf;
        add_header Content-Length "0" always;
        add_header Cache-Control "public, max-age=86400" always;
        return 204;
    }

    # Everything else -> 403 (S3 XML style) - MUST be last
    location / {
        include /etc/nginx/snippets/cdn_headers.conf;
        add_header x-amz-request-id "\$request_id" always;
        default_type application/xml;
        return 403 '<?xml version="1.0" encoding="UTF-8"?>
<Error>
    <Code>AccessDenied</Code>
    <Message>Access Denied</Message>
    <RequestId>\$request_id</RequestId>
    <HostId>${HOST_ID}</HostId>
</Error>';
    }
}
EOF
    fi
    
    # Activate configuration
    ln -sf /etc/nginx/sites-available/cdn /etc/nginx/sites-enabled/cdn
    
    # Remove default configuration if exists (CRITICAL)
    rm -f /etc/nginx/sites-enabled/default
    rm -f /etc/nginx/sites-available/default
    
    # Hide nginx version (server_tokens off)
    if ! grep -q "server_tokens off" /etc/nginx/nginx.conf; then
        sed -i '/http {/a \    server_tokens off;' /etc/nginx/nginx.conf
    fi
    
    # Check configuration
    if nginx -t; then
        systemctl restart nginx
        systemctl enable nginx > /dev/null 2>&1
        print_success "Nginx configured and running"
    else
        print_error "Nginx configuration error"
        nginx -t
        return 1
    fi
}
