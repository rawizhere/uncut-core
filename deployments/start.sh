#!/bin/sh
set -e

mkdir -p /opt/uncut/data/logs/nginx
mkdir -p /var/www/cdn/subs
mkdir -p /opt/sing-box/certs/certificates
mkdir -p /var/www/html/.well-known/acme-challenge

# Generate dummy CDN assets if missing
if [ ! -f /var/www/cdn/index.html ]; then
    echo '<!DOCTYPE html><html><head><title>CloudFront CDN Origin</title></head><body><h3>Distribution Active</h3></body></html>' > /var/www/cdn/index.html
fi

# Setup daily logrotate cron job
if [ -f /etc/logrotate.d/uncut-nginx ]; then
    if command -v cron >/dev/null 2>&1; then
        cron || true
    elif command -v crond >/dev/null 2>&1; then
        crond -b -l 8 || true
    fi
fi

exec /usr/local/bin/uncut --data-dir=/opt/uncut/data --install-dir=/opt/sing-box "$@"
