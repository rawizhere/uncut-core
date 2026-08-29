# MTProxy C builder on Debian (glibc)
FROM debian:bookworm-slim AS mtproxy-builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    build-essential \
    libssl-dev \
    zlib1g-dev \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

RUN git clone --depth 1 https://github.com/TelegramMessenger/MTProxy.git /tmp/MTProxy \
    && cd /tmp/MTProxy \
    && make \
    && mkdir -p /build \
    && cp objs/bin/mtproto-proxy /build/mtproto-proxy

# Go builder
FROM golang:1.27-alpine AS go-builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -trimpath -o uncut ./cmd/uncut

# Build tproxy-server
RUN git clone --depth 1 https://github.com/telegramdesktop/tproxy-server.git /tmp/tproxy-server \
    && cd /tmp/tproxy-server \
    && CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -trimpath -o /build/tproxy-server ./cmd/tproxy-server

# Final Runtime Image based on Debian Slim
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    tzdata \
    curl \
    nginx \
    jq \
    bind9-dnsutils \
    socat \
    iproute2 \
    openssl \
    zlib1g \
    logrotate \
    cron \
    procps \
    tar \
    gzip \
    && rm -rf /var/lib/apt/lists/*

# Download official Telegram proxy secrets and configurations
RUN mkdir -p /etc/mtproxy \
    && curl -fsSL https://core.telegram.org/getProxySecret -o /etc/mtproxy/proxy-secret \
    && curl -fsSL https://core.telegram.org/getProxyConfig -o /etc/mtproxy/proxy-multi.conf

# Install Sing-Box Extended release
RUN EXT_TAG=$(curl -fsSL https://api.github.com/repos/shtorm-7/sing-box-extended/releases/latest | jq -r .tag_name) \
    && ARCH=$(dpkg --print-architecture) \
    && curl -fsSL "https://github.com/shtorm-7/sing-box-extended/releases/download/${EXT_TAG}/sing-box-${EXT_TAG#v}-linux-${ARCH}.tar.gz" -o /tmp/sing-box.tar.gz \
    && tar -xzf /tmp/sing-box.tar.gz -C /tmp \
    && mv /tmp/sing-box-*/sing-box /usr/local/bin/sing-box \
    && chmod +x /usr/local/bin/sing-box \
    && rm -rf /tmp/sing-box*

WORKDIR /app

COPY --from=go-builder /build/uncut /usr/local/bin/uncut
COPY --from=go-builder /build/tproxy-server /usr/local/bin/tproxy-server
COPY --from=mtproxy-builder /build/mtproto-proxy /usr/local/bin/mtproto-proxy
COPY deployments/start.sh /app/start.sh
COPY deployments/logrotate.conf /etc/logrotate.d/uncut-nginx

RUN chmod +x /app/start.sh /usr/local/bin/uncut /usr/local/bin/tproxy-server /usr/local/bin/mtproto-proxy \
    && rm -f /etc/nginx/sites-enabled/* /etc/nginx/sites-available/* /etc/nginx/conf.d/* /etc/nginx/http.d/* \
    && mkdir -p /opt/uncut/data/logs /opt/sing-box /etc/nginx/sites-available /etc/nginx/sites-enabled /var/www/cdn /etc/nginx/snippets /etc/mtproxy /srv/tproxy-site /var/www/html

WORKDIR /app

ENV DATA_DIR=/opt/uncut/data
ENV INSTALL_DIR=/opt/sing-box

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD curl -fsSL http://127.0.0.1:8088/healthz || exit 1

ENTRYPOINT ["/app/start.sh"]
CMD ["daemon"]
