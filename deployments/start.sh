#!/bin/sh
set -e

mkdir -p /opt/uncut/data/logs/nginx
mkdir -p /var/www/cdn/subs
mkdir -p /opt/sing-box/certs/certificates
mkdir -p /var/www/html/.well-known/acme-challenge

# Setup daily logrotate cron job
if [ -f /etc/logrotate.d/uncut-nginx ]; then
    cron || true
fi

exec /usr/local/bin/raw --data-dir=/opt/uncut/data --install-dir=/opt/sing-box "$@"
