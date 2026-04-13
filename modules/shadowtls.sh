#!/bin/bash

# Configuration generators for ShadowTLS
generate_shadowtls_inbound() {
    local domain=$(get_setting "domain")
    local sni=$(get_setting "sni" "$domain")
    local password=$(get_setting "shadowtls_password" "$(openssl rand -hex 16)")
    
    # Save password if not set
    if [[ -z "$(get_setting "shadowtls_password")" ]]; then
        set_setting "shadowtls_password" "$password"
    fi

    cat <<EOF
{
  "type": "shadowtls",
  "tag": "shadowtls-in",
  "listen": "0.0.0.0",
  "listen_port": 8444,
  "detour": "vless-reality",
  "version": 3,
  "users": [
    {
      "password": "$password"
    }
  ],
  "handshake": {
    "server": "$sni",
    "server_port": 443
  },
  "strict_mode": true
}
EOF
}

add_shadowtls_protocol() {
    print_info "Adding ShadowTLS v3 protocol..."
    
    local password=$(openssl rand -hex 16)
    set_setting "shadowtls_password" "$password"
    
    # Ensure Reality is also active as we detour to it
    if ! protocol_exists "vless-reality"; then
        add_protocol_logic "vless-reality"
    fi
    
    add_protocol_to_settings "shadowtls"
    rebuild_config
}

generate_shadowtls_link() {
    local name=$1
    local password=$(get_setting "shadowtls_password")
    local domain=$(get_setting "domain")
    local sni=$(get_setting "sni" "$domain")
    local country=$(get_setting "country")
    
    echo "shadowtls://${password}@${domain}:8444?sni=${sni}&version=3#${name}-shadowtls-${country}"
}
