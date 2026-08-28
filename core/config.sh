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

VALID_PROTOCOLS="xhttp-stealth vless-ws vless-httpupgrade vless-grpc vless-reality tuic"

migrate_legacy_protocols() {
    if [[ ! -f "$SETTINGS_FILE" ]]; then
        return 0
    fi

    local changed=0
    local valid_json=$(printf '%s\n' $VALID_PROTOCOLS | jq -R . | jq -s -c .)

    local tmp=$(mktemp)
    if jq --argjson valid "$valid_json"         '.protocols = ((.protocols // []) | map(select(. as $p | $valid | index($p))))'         "$SETTINGS_FILE" > "$tmp" 2>/dev/null; then
        if ! cmp -s "$SETTINGS_FILE" "$tmp"; then
            mv "$tmp" "$SETTINGS_FILE"
            changed=1
        else
            rm -f "$tmp"
        fi
    else
        rm -f "$tmp"
    fi

    if [[ -f "$CLIENTS_FILE" ]]; then
        local ctmp=$(mktemp)
        if jq --argjson valid "$valid_json"             '[.[] | .protocols = ((.protocols // []) | map(select(. as $p | $valid | index($p)))) | select((.protocols | length) > 0)]'             "$CLIENTS_FILE" > "$ctmp" 2>/dev/null; then
            if ! cmp -s "$CLIENTS_FILE" "$ctmp"; then
                mv "$ctmp" "$CLIENTS_FILE"
                changed=1
            else
                rm -f "$ctmp"
            fi
        else
            rm -f "$ctmp"
        fi
    fi

    if [[ "$changed" == "1" ]]; then
        print_warning "Removed legacy protocols from settings/clients. Rebuilding..."
        rebuild_config 2>/dev/null || true
        regenerate_all_subscriptions 2>/dev/null || true
    fi
}


backup_core() {
    local backup_dir="$INSTALL_DIR/backups"
    mkdir -p "$backup_dir"
    local stamp=$(date +%Y%m%d_%H%M%S)
    local target="$backup_dir/uncut_backup_${stamp}.tar.gz"

    local files=("$SETTINGS_FILE" "$CLIENTS_FILE" "$CONFIG_FILE")
    local exists=0
    for f in "${files[@]}"; do
        [[ -f "$f" ]] && exists=1
    done
    [[ -f /etc/nginx/nginx.conf ]] && [[ -d /etc/nginx/sites-enabled ]] && exists=1

    if [[ "$exists" == "0" ]]; then
        return 0
    fi

    tar -czf "$target" -C /         "${INSTALL_DIR#/}/settings.json"         "${INSTALL_DIR#/}/clients.json"         "${INSTALL_DIR#/}/config.json"         "etc/nginx" 2>/dev/null

    ls -1t "$backup_dir"/uncut_backup_*.tar.gz 2>/dev/null | tail -n +15 | xargs -r rm -f
    print_success "Core + Nginx config backed up ($(basename "$target"))"
}

install_backup_cron() {
    local cron_line="0 4 * * * ${SCRIPT_DIR}/raw --backup"
    if ! crontab -l 2>/dev/null | grep -qF -- "$cron_line"; then
        ( crontab -l 2>/dev/null; echo "$cron_line" ) | crontab -
        print_success "Daily backup cron installed (04:00)"
    else
        print_info "Backup cron already present"
    fi
}

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
    
    echo "/${base}/${salt}"
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
    # Path 1: XHTTP Stealth, Path 2: VLESS WS, Path 3: VLESS HTTPUpgrade, Path 4: VLESS gRPC
    echo "paths:/assets/js,/assets/css,/assets/img,/assets/fonts|headers:X-Amz-Cf-Id:redacted,X-Edge-Origin-Shield:active|mode:streaming|fallback:aws.amazon.com"
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
    
    mkdir -p "$(dirname "$SETTINGS_FILE")"
    if [[ ! -f "$SETTINGS_FILE" ]] || [[ ! -s "$SETTINGS_FILE" ]] || ! jq empty "$SETTINGS_FILE" >/dev/null 2>&1; then
        echo '{}' > "$SETTINGS_FILE"
    fi

    local tmp=$(mktemp)
    if jq --arg key "$key" --arg val "$value" '.[$key] = $val' "$SETTINGS_FILE" > "$tmp" 2>/dev/null; then
        mv "$tmp" "$SETTINGS_FILE"
    else
        rm -f "$tmp"
    fi
}

# Add protocol to settings.json
add_protocol_to_settings() {
    local protocol=$1
    if [[ ! -f "$SETTINGS_FILE" ]] || [[ ! -s "$SETTINGS_FILE" ]] || ! jq empty "$SETTINGS_FILE" >/dev/null 2>&1; then
        echo '{"protocols": []}' > "$SETTINGS_FILE"
    fi
    local tmp=$(mktemp)
    if jq --arg proto "$protocol" '.protocols = ((.protocols // []) + ($proto | split(" ")) | flatten | map(select(. != "")) | unique)' "$SETTINGS_FILE" > "$tmp" 2>/dev/null; then
        mv "$tmp" "$SETTINGS_FILE"
    else
        rm -f "$tmp"
    fi
}

# Remove protocol from settings.json
remove_protocol_from_settings() {
    local protocol=$1
    if [[ ! -f "$SETTINGS_FILE" ]] || [[ ! -s "$SETTINGS_FILE" ]] || ! jq empty "$SETTINGS_FILE" >/dev/null 2>&1; then
        return 0
    fi
    local tmp=$(mktemp)
    if jq --arg proto "$protocol" '.protocols |= (map(select(. != $proto)))' "$SETTINGS_FILE" > "$tmp" 2>/dev/null; then
        mv "$tmp" "$SETTINGS_FILE"
    else
        rm -f "$tmp"
    fi
    
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
