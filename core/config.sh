#!/bin/bash

# Constants
export SCRIPT_VERSION="10.0.0"
export INSTALL_DIR="/opt/sing-box"
export CONFIG_FILE="$INSTALL_DIR/config.json"
export SETTINGS_FILE="$INSTALL_DIR/settings.json"
export CLIENTS_FILE="$INSTALL_DIR/clients.json"
export LOG_FILE="$INSTALL_DIR/sing-box.log"
export SERVICE_FILE="/etc/systemd/system/sing-box.service"
export LOGROTATE_FILE="/etc/logrotate.d/sing-box"

# Initialize settings
init_settings() {
    mkdir -p "$INSTALL_DIR"
    if [[ ! -f "$SETTINGS_FILE" ]]; then
        echo '{"protocols": []}' > "$SETTINGS_FILE"
    fi
    if [[ ! -f "$CLIENTS_FILE" ]]; then
        echo '[]' > "$CLIENTS_FILE"
    fi
    
    # Core system settings
    set_setting "install_date" "$(date +%Y-%m-%d)"
    [[ -z $(get_setting "protocol_salt") ]] && set_setting "protocol_salt" "$(openssl rand -hex 4)"
    set_setting "masking_theme" "cdn_sync" # Always CloudFront/AWS
    [[ -z $(get_setting "auto_update") ]] && set_setting "auto_update" "false"
    
    local current_sni=$(get_setting "sni")
    if [[ -z "$current_sni" || "$current_sni" == "www.microsoft.com" ]]; then
        set_setting "sni" "dl.google.com"
    fi
    
    # DPI Flags
    [[ -z $(get_setting "dpi_fragment_enabled") ]] && set_setting "dpi_fragment_enabled" "true"
    [[ -z $(get_setting "dpi_hello_padding_enabled") ]] && set_setting "dpi_hello_padding_enabled" "true"
    
    # Protocol Keys Initialization
    [[ -z $(get_setting "sudoku_key") ]] && set_setting "sudoku_key" "$(openssl rand -hex 16)"
    [[ -z $(get_setting "snell_psk") ]] && set_setting "snell_psk" "$(openssl rand -hex 16)"
    [[ -z $(get_setting "reality_short_id") ]] && set_setting "reality_short_id" "$(generate_short_id)"
    
    if [[ -z $(get_setting "reality_private_key") || -z $(get_setting "reality_public_key") ]]; then
        if [[ -f "$INSTALL_DIR/sing-box" ]]; then
            local keys_output=$("$INSTALL_DIR/sing-box" generate reality-keypair 2>/dev/null)
            local priv=$(echo "$keys_output" | grep "PrivateKey:" | awk '{print $2}')
            local pub=$(echo "$keys_output" | grep "PublicKey:" | awk '{print $2}')
            if [[ -n "$priv" && -n "$pub" ]]; then
                set_setting "reality_private_key" "$priv"
                set_setting "reality_public_key" "$pub"
            fi
        fi
    fi

    set_setting "traffic_shaping_level" "high"
    set_setting "shadow_tls_enabled" "false"
    
    print_success "Settings initialized"
}

# Get salted path based on base path
get_salted_path() {
    local base=$1
    local salt=$(get_setting "protocol_salt")
    # If no salt (upgraded from old version), generate one
    if [[ -z "$salt" ]]; then
        salt=$(openssl rand -hex 4)
        set_setting "protocol_salt" "$salt"
    fi
    
    # Remove leading/trailing slashes for consistency
    base="${base#/}"
    base="${base%/}"
    
    echo "/${base}/${salt}/stream"
}

# Helper for DPI parameters in links
get_dpi_link_params() {
    local params=""
    
    local fragment=$(get_setting "dpi_fragment_enabled" "false")
    if [[ "$fragment" == "true" ]]; then
        # Default fragmentation for Nekobox/v2rayN compatible parameters
        params+="&fragment=10-500,0-20"
    fi
    
    local padding=$(get_setting "dpi_hello_padding_enabled" "false")
    if [[ "$padding" == "true" ]]; then
        # Hello Padding (randomize handshake length)
        params+="&padding=900-1200"
    fi
    
    echo "$params"
}

# Define Masking Presets
get_theme_data() {
    # Exclusive Theme: CDN Sync (AWS CloudFront/S3 style)
    echo "paths:/storage/v2/sync,/media/origin/push,/cdn/worker/runtime|headers:X-Amz-Cf-Id:redacted,X-Edge-Origin-Shield:active|mode:streaming|fallback:aws.amazon.com"
}

# Get value from settings.json
get_setting() {
    local key=$1
    local default=$2
    local value=$(jq -r ".$key // empty" "$SETTINGS_FILE" 2>/dev/null)
    if [[ "$key" == "sni" && ("$value" == "www.microsoft.com" || -z "$value" || "$value" == "null") ]]; then
        if [[ -n "$SNI" ]]; then
            value="$SNI"
        else
            value="dl.google.com"
        fi
        set_setting "sni" "$value"
    fi
    if [[ -z "$value" || "$value" == "null" ]]; then
        echo "$default"
    else
        echo "$value"
    fi
}

# Set value in settings.json
set_setting() {
    local key=$1
    local value=$2
    local tmp=$(mktemp)
    jq --arg key "$key" --arg val "$value" '.[$key] = $val' "$SETTINGS_FILE" > "$tmp"
    mv "$tmp" "$SETTINGS_FILE"
}

# Add protocol to settings.json and update existing clients
add_protocol_to_settings() {
    local protocol=$1
    local tmp=$(mktemp)
    # Ensure protocol is split by space if accidentally passed as one string
    jq --arg proto "$protocol" '.protocols += ($proto | split(" ")) | .protocols |= (flatten | map(select(. != "")) | unique)' "$SETTINGS_FILE" > "$tmp"
    mv "$tmp" "$SETTINGS_FILE"
    
    # Also add new protocol to existing clients
    if [[ -f "$CLIENTS_FILE" ]]; then
        local ctmp=$(mktemp)
        jq --arg proto "$protocol" 'map(.protocols += ($proto | split(" ")) | .protocols |= (flatten | map(select(. != "")) | unique))' "$CLIENTS_FILE" > "$ctmp"
        mv "$ctmp" "$CLIENTS_FILE"
    fi
}

# Remove protocol from settings.json
remove_protocol_from_settings() {
    local protocol=$1
    local tmp=$(mktemp)
    # Remove from settings
    jq --arg proto "$protocol" '.protocols -= [$proto]' "$SETTINGS_FILE" > "$tmp"
    mv "$tmp" "$SETTINGS_FILE"
    
    # Remove from all clients
    if [[ -f "$CLIENTS_FILE" ]]; then
        local ctmp=$(mktemp)
        jq --arg proto "$protocol" 'map(.protocols -= [$proto])' "$CLIENTS_FILE" > "$ctmp"
        mv "$ctmp" "$CLIENTS_FILE"
    fi
}

# Get protocol list
get_protocols() {
    jq -r '.protocols? // [] | map(split(" ")) | flatten | map(select(. != "")) | .[]' "$SETTINGS_FILE" 2>/dev/null | sort -u
}

# Check if protocol exists
protocol_exists() {
    local protocol=$1
    jq -e --arg proto "$protocol" '.protocols[] | select(. == $proto)' "$SETTINGS_FILE" >/dev/null 2>&1
}

# Crypto helpers
generate_uuid() {
    cat /proc/sys/kernel/random/uuid
}

generate_password() {
    openssl rand -hex 5
}

generate_obfs_password() {
    openssl rand -hex 16
}

generate_short_id() {
    openssl rand -hex 3
}

generate_client_hash() {
    local uuid=$1
    local salt="uncut-core-sub-salt-v1"
    echo -n "${uuid}${salt}" | md5sum | awk '{print $1}'
}
