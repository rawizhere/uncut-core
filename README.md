# Uncut Core

Raw proxy server manager.

Built on sing-box extended.
Server seamlessly disguises as an AWS CloudFront backend/edge node. Features deep traffic masking, TLS fingerprinting protection, SSL automation, and server hardening (UFW/Fail2ban/Honey Pots).

### Screenshots

<img width="1044" height="418" alt="image" src="https://github.com/user-attachments/assets/1582eda2-84bd-4e73-9e68-993be86bdb84" />

### Requirements

- Ubuntu 20.04+ / Debian 11+
- Root access
- Subdomain
- SNI address

### Install

Interactive:

```bash
curl -fsSL https://raw.githubusercontent.com/rawizhere/uncut-core/main/install.sh | bash
```

Unattended:

```bash
curl -fsSL https://raw.githubusercontent.com/rawizhere/uncut-core/main/install.sh | bash -s -- --domain domain.com --email email@site.com --country DE --sni dl.google.com --ssh-port 1488 --clients "user1,user2"
```

### Flags

| Flag | Description | Default |
| --- | --- | --- |
| `--domain` | Target domain or subdomain | Required |
| `--email` | Email for ACME SSL certificates | Required |
| `--country` | Two-letter country code for node links | `US` |
| `--sni` | SNI domain address for TLS masking | `dl.google.com` |
| `--protocols` | Protocol selection | `default` |
| `--ssh-port` | Custom SSH listening port | `22` |
| `--clients` | Comma-separated list of client names | Optional |
| `--auto` | Run in non-interactive mode | `false` |

### Usage

```bash
raw
```

### Tree

```text
.
├── core/        # Logic
├── modules/     # Acme, Nginx, Engine
├── templates/   # Configs
├── raw          # Entry point
└── install.sh   # Installer
```



