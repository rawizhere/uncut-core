# Uncut Core

Raw proxy server manager.

Built on sing-box extended.
Server disguises as a CloudFront backend. Deep traffic masking (AWS/Google/Sentry signatures) available for specific protocols. Includes SSL automation and hardening (UFW/Fail2ban).

### Install

```bash
curl -fsSL https://raw.githubusercontent.com/tempizhere/uncut-core/main/install.sh | bash
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
