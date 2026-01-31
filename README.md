# Uncut Core

Raw proxy server manager.

Built on sing-box extended.
Server disguises as a CloudFront backend. Deep traffic masking (AWS/Google/Sentry signatures) available for specific protocols. Includes SSL automation and hardening (UFW/Fail2ban).

### Screenshots

<img width="1044" height="418" alt="image" src="https://github.com/user-attachments/assets/1582eda2-84bd-4e73-9e68-993be86bdb84" />

### Requirements

- Ubuntu 20.04+ / Debian 11+
- Root access
- Subdomain
- SNI address

### Install

```bash
curl -fsSL https://raw.githubusercontent.com/rawizhere/uncut-core/main/install.sh | bash
```

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
├── data/        # Binaries
├── raw          # Entry point
└── install.sh   # Installer
```


