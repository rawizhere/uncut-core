# MTProxy C builder on Debian (glibc)
FROM debian:bookworm-slim AS mtproxy-builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    build-essential \
    libssl-dev \
    zlib1g-dev \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Pinned: the plain backend tproxy relays to; a floating HEAD would change the
# middle-proxy protocol under the bridge. FakeTLS lives in sing-box's mtproxy inbound.
ARG MTProxy_COMMIT=f36d8af769ffaeac36978d38c2c0f6d1104c2137
RUN git clone https://github.com/TelegramMessenger/MTProxy.git /tmp/MTProxy \
    && cd /tmp/MTProxy \
    && git checkout ${MTProxy_COMMIT} \
    && make \
    && mkdir -p /build \
    && cp objs/bin/mtproto-proxy /build/mtproto-proxy

# Go builder (pinned: a floating golang tag silently moves the toolchain)
FROM golang:1.27-alpine AS go-builder

RUN apk add --no-cache git ca-certificates tzdata

# VERSION arrives through the build args (compose pins it to the same number
# as internal/config); a plain `go build` falls back to that constant, so the binary never reports a drifting dev value.
ARG VERSION=dev

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux \
    go build -ldflags="-s -w -X github.com/rawizhere/uncut-core/internal/config.Version=${VERSION}" \
    -trimpath -o raw ./cmd/uncut

# Build tproxy-server. Pinned to a commit: the bridge protocol it speaks is
# only defined by its source, and an unreviewed upstream change would break clients with no error anywhere.
ARG TPROXY_SERVER_COMMIT=52a5feb7fac38f68da5afef9cedd9b3bfc8473ca
RUN git clone https://github.com/telegramdesktop/tproxy-server.git /tmp/tproxy-server \
    && cd /tmp/tproxy-server \
    && git checkout "${TPROXY_SERVER_COMMIT}" \
    && CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -trimpath -o /build/tproxy-server ./cmd/tproxy-server

# Final Runtime Image based on Debian Slim
FROM debian:bookworm-slim

# nginx: mainline from nginx.org - Debian bookworm ships 1.22, and the https
# server needs `http2 on;` (>=1.25.1). No QUIC: UDP 443 belongs to Hysteria2.
# apt reads the armored .asc key directly, so gnupg is not needed. procps: pkill
# reloads sing-box; curl: healthcheck; cron + logrotate: nginx log rotation.
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    tzdata \
    curl \
    zlib1g \
    logrotate \
    cron \
    procps \
    && curl -fsSL https://nginx.org/keys/nginx_signing.key -o /etc/apt/keyrings/nginx.asc \
    && echo "deb [signed-by=/etc/apt/keyrings/nginx.asc] https://nginx.org/packages/mainline/debian bookworm nginx" > /etc/apt/sources.list.d/nginx.list \
    && apt-get update && apt-get install -y --no-install-recommends nginx \
    && rm -rf /var/lib/apt/lists/*

# SNI splitter wiring: TCP 443 must be owned by the stream module so reality
# and the https server can share the port. The stream include lives at MAIN context (conf.d is http-only), so nginx.conf itself is patched: load the dynamic stream module when the package ships one, and include the node's stream-conf.d directory right before the http block.
RUN if [ -f /usr/lib/nginx/modules/ngx_stream_module.so ]; then \
        sed -i '1i load_module /usr/lib/nginx/modules/ngx_stream_module.so;' /etc/nginx/nginx.conf; \
    fi \
    && sed -i '/^http {/i include /etc/nginx/stream-conf.d/*.conf;\n' /etc/nginx/nginx.conf \
    && mkdir -p /etc/nginx/stream-conf.d

# Download official Telegram proxy secrets and configurations
RUN mkdir -p /etc/mtproxy \
    && curl -fsSL https://core.telegram.org/getProxySecret -o /etc/mtproxy/proxy-secret \
    && curl -fsSL https://core.telegram.org/getProxyConfig -o /etc/mtproxy/proxy-multi.conf

# Install Sing-Box Extended release. Pinned on purpose: the fleet runs one
# version and nodes are upgraded by hand, never by rebuilding against latest.
ARG SINGBOX_EXT_VERSION=1.14.3-extended-2.7.3
RUN ARCH=$(dpkg --print-architecture) \
    && curl -fsSL "https://github.com/shtorm-7/sing-box-extended/releases/download/v${SINGBOX_EXT_VERSION}/sing-box-${SINGBOX_EXT_VERSION}-linux-${ARCH}.tar.gz" -o /tmp/sing-box.tar.gz \
    && tar -xzf /tmp/sing-box.tar.gz -C /tmp \
    && mv /tmp/sing-box-*/sing-box /usr/local/bin/sing-box \
    && chmod +x /usr/local/bin/sing-box \
    && rm -rf /tmp/sing-box*

# Install CoreDNS for the private DoH endpoint (loopback only; nginx in
# front). Pinned on purpose, same discipline as the sing-box pin.
ARG COREDNS_VERSION=1.14.7
RUN ARCH=$(dpkg --print-architecture) \
    && curl -fsSL "https://github.com/coredns/coredns/releases/download/v${COREDNS_VERSION}/coredns_${COREDNS_VERSION}_linux_${ARCH}.tgz" -o /tmp/coredns.tgz \
    && tar -xzf /tmp/coredns.tgz -C /usr/local/bin coredns \
    && chmod +x /usr/local/bin/coredns \
    && rm -f /tmp/coredns.tgz

WORKDIR /app

COPY --from=go-builder /build/raw /usr/local/bin/raw
COPY --from=go-builder /build/tproxy-server /usr/local/bin/tproxy-server
COPY --from=mtproxy-builder /build/mtproto-proxy /usr/local/bin/mtproto-proxy
COPY deployments/start.sh /app/start.sh
COPY deployments/logrotate.conf /etc/logrotate.d/uncut-nginx
COPY deployments/Corefile /etc/coredns/Corefile

RUN chmod +x /app/start.sh /usr/local/bin/raw /usr/local/bin/tproxy-server /usr/local/bin/mtproto-proxy \
    && rm -f /etc/nginx/sites-enabled/* /etc/nginx/sites-available/* /etc/nginx/conf.d/* /etc/nginx/http.d/* \
    && mkdir -p /opt/uncut/data/logs /opt/sing-box /etc/nginx/conf.d /var/www/cdn /etc/nginx/snippets /etc/mtproxy /srv/tproxy-site /var/www/html \
    && mkdir -p /etc/coredns

WORKDIR /app

# /healthz alone decides: a fallback to the tproxy page would report healthy with sing-box down. The start period covers the first certificate.
HEALTHCHECK --interval=15s --timeout=3s --start-period=60s --retries=3 \
  CMD curl -fsS --max-time 2 http://127.0.0.1:8088/healthz || exit 1

ENTRYPOINT ["/app/start.sh"]
CMD ["daemon"]
