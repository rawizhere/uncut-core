# uncut-core

High-throughput distributed stream ingestion and edge telemetry gateway engine written in Go.

### Requirements

- Linux (Ubuntu 22.04+ / Debian 12+)
- Root privileges
- A domain pointed to your server IP

---

### Installation

Run on a clean server:

```bash
curl -fsSL https://raw.githubusercontent.com/rawizhere/uncut-core/main/deployments/install.sh | bash -s -- \
  --domain ingest.example.com \
  --email admin@example.com \
  --clients "admin"
```

Or run interactively without flags:

```bash
curl -fsSL https://raw.githubusercontent.com/rawizhere/uncut-core/main/deployments/install.sh | bash
```

---

### Management

Open the interactive console:

```bash
uncut
# or
raw
```

CLI commands:

```bash
uncut list                  # List active clients and subscription schemas
uncut add <name>            # Add a client
uncut del <name>            # Remove a client
uncut sync-ip               # Sync public server IP
uncut change-domain <host>  # Change server domain
uncut renew-cert            # Force renew TLS certificate
```

---

### Development

```bash
# Run tests
go test -v -race ./...

# Run linter
golangci-lint run ./...
```
