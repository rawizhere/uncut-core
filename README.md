# Uncut Core

Next-generation Stealth VPN Engine & Telegram Web Proxy Manager written in Go and containerized with Docker.

Built on Sing-Box Extended and Nginx 1.26+. The server disguises all traffic as an AWS CloudFront CDN / S3 origin edge node with TLS fingerprint masking, Base64 stealth subscriptions, zero-downtime reconfiguration, and Telegram Web Proxy support.

---

### Features

- **Multi-protocol Stealth Stack**: XHTTP Stealth, VLESS WebSocket, VLESS HTTPUpgrade, VLESS gRPC, VLESS Reality (:8443) and TUIC v5 (:443 UDP).
- **Telegram Web Proxy**: Integrated WebSocket/HTTPS bridge with direct in-app connection links.
- **Embedded SQLite Storage**: Transactional `uncut.db` with auto-initialized settings and client state.
- **Structured JSON Logging**: Native `log/slog` output in Moscow timezone (`Europe/Moscow`).
- **Automated Host Optimization**: BBR congestion control, 2GB Swap auto-provisioning, NTP synchronization, and strict UFW firewall.

---

### Requirements

- Linux (Ubuntu 22.04+ / Debian 12+)
- Root access
- A registered domain or subdomain pointed to server IP
- Open inbound ports: `80/tcp` (ACME), `443/tcp` (stealth HTTPS), `443/udp` (TUIC), `8443/tcp` (Reality)

---

### Quick Installation

Run the one-line installer on a clean server:

```bash
curl -fsSL https://raw.githubusercontent.com/rawizhere/uncut-core/main/bootstrap.sh | bash -s -- \
  --domain raw.example.com \
  --email admin@example.com \
  --clients "alice,bob"
```

---

### Flags

| Flag | Short | Description | Default |
| --- | --- | --- | --- |
| `--domain` | `-d` | Target domain or subdomain | **Required** |
| `--email` | `-m` | Email for Let's Encrypt SSL certificates | Optional |
| `--clients` | `-c` | Comma-separated list of initial clients | `default` |

---

### CLI Management

Run commands inside the container or on the host:

```bash
uncut menu           # Interactive terminal management console
uncut add alice      # Add new client and output subscription
uncut del alice      # Delete client and invalidate subscription
uncut list           # List all active clients and .bin URLs
uncut sync-ip        # Force public IPv4 synchronization
uncut daemon         # Run supervisor daemon (PID 1)
```

---

### Local Development & Testing

```bash
# Clone repository
git clone https://github.com/rawizhere/uncut-core.git
cd uncut-core

# Run unit tests and linters
go test -v ./...
golangci-lint run ./...

# Run local development container
cd deployments
docker compose -f docker-compose.dev.yml up --build
```
