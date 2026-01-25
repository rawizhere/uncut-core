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
    set_setting "auto_update" "false"
    
    # Generate unique salt for paths
    local salt=$(openssl rand -hex 4)
    set_setting "protocol_salt" "$salt"
    
    # Set default theme (Theme C: CDN Sync / AWS S3)
    set_setting "masking_theme" "cdn_sync"
    
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

# Define Masking Presets
get_theme_data() {
    local theme=$(get_setting "masking_theme" "analytics")
    case "$theme" in
        "analytics")
            echo "paths:/p/track/event,/v1/collect,/metrics/v2/dispatch|headers:X-Provider:Google-Analytics,Access-Control-Allow-Origin:*|mode:request_response"
            ;;
        "infrastructure")
            echo "paths:/api/v1/report/crash,/sys/logs/bulk,/telemetry/agent/sync|headers:X-Sentry-Auth:redacted,X-Datadog-Trace-Id:redacted|mode:request_response"
            ;;
        "cdn_sync")
            echo "paths:/storage/v2/sync,/media/origin/push,/cdn/worker/runtime|headers:X-Amz-Cf-Id:redacted,X-Edge-Origin-Shield:active|mode:streaming"
            ;;
        "security")
            echo "paths:/security/report/csp,/.well-known/security/audit,/policy/v1/check|headers:X-Security-Audit:compliant|mode:auto"
            ;;
        *)
            # Fallback to analytics
            echo "paths:/p/track/event,/v1/collect,/metrics/v2/dispatch|headers:X-Provider:Google-Analytics,Access-Control-Allow-Origin:*|mode:request_response"
            ;;
    esac
}

# Get value from settings.json
get_setting() {
    local key=$1
    local default=$2
    local value=$(jq -r ".$key // empty" "$SETTINGS_FILE" 2>/dev/null)
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

# Add protocol to settings.json
add_protocol_to_settings() {
    local protocol=$1
    local tmp=$(mktemp)
    jq --arg proto "$protocol" '.protocols += [$proto] | .protocols |= unique' "$SETTINGS_FILE" > "$tmp"
    mv "$tmp" "$SETTINGS_FILE"
}

# Remove protocol from settings.json
remove_protocol_from_settings() {
    local protocol=$1
    local tmp=$(mktemp)
    jq --arg proto "$protocol" '.protocols -= [$proto]' "$SETTINGS_FILE" > "$tmp"
    mv "$tmp" "$SETTINGS_FILE"
}

# Get protocol list
get_protocols() {
    jq -r '.protocols[]' "$SETTINGS_FILE" 2>/dev/null
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
