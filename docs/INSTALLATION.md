# BloxOS Installation Guide

> Complete guide for installing BloxOS server and agent

---

## Table of Contents

1. [Requirements](#requirements)
2. [Quick Start (Docker)](#quick-start-docker)
3. [Manual Installation](#manual-installation)
4. [Agent Installation](#agent-installation)
5. [Configuration](#configuration)
6. [Troubleshooting](#troubleshooting)

---

## Requirements

### Server Requirements

- **OS:** Ubuntu 22.04/24.04 LTS, Debian 12+, or any Linux with Docker
- **CPU:** 2+ cores recommended
- **RAM:** 2GB minimum, 4GB recommended
- **Storage:** 20GB+ for database and logs
- **Network:** Open ports 3000 (dashboard), 3001 (API)

### Agent Requirements (Mining Rigs)

- **OS:** Ubuntu 20.04+, HiveOS, or any Linux
- **GPU:** NVIDIA (with nvidia-smi) or AMD (with rocm-smi)
- **Network:** Outbound access to server on port 3001

---

## Quick Start (Docker)

The fastest way to get BloxOS running.

### 1. Clone the Repository

```bash
git clone https://github.com/bokiko/bloxos.git
cd bloxos
```

### 2. Configure Environment

```bash
cp .env.example .env
```

Edit `.env` and set required values:

```bash
# Required - Generate secure secrets
JWT_SECRET=$(openssl rand -base64 32)
COOKIE_SECRET=$(openssl rand -base64 32)
ENCRYPTION_KEY=$(openssl rand -hex 32)

# Database
DATABASE_URL="postgresql://bloxos:bloxos_secure_password@postgres:5432/bloxos"

# Optional - For production
NODE_ENV=production
FORCE_HTTPS=true
CORS_ORIGINS=https://your-domain.com
```

### 3. Start Services

```bash
# Production mode
docker compose -f docker/docker-compose.yml -f docker/docker-compose.prod.yml up -d

# Check status
docker compose -f docker/docker-compose.yml ps
```

### 4. Access Dashboard

Open `http://your-server:3000` in your browser.

First user to register becomes admin.

---

## Manual Installation

For development or custom deployments.

### 1. Install Prerequisites

```bash
# Node.js 22+
curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -
sudo apt-get install -y nodejs

# pnpm
npm install -g pnpm

# PostgreSQL 16
sudo apt-get install -y postgresql-16

# Redis (optional, for session storage)
sudo apt-get install -y redis-server
```

### 2. Clone and Install

```bash
git clone https://github.com/bokiko/bloxos.git
cd bloxos
pnpm install
```

### 3. Configure Database

```bash
# Create database and user
sudo -u postgres psql << EOF
CREATE USER bloxos WITH PASSWORD 'your_secure_password';
CREATE DATABASE bloxos OWNER bloxos;
GRANT ALL PRIVILEGES ON DATABASE bloxos TO bloxos;
EOF
```

### 4. Configure Environment

```bash
cp .env.example .env
# Edit .env with your settings
```

### 5. Push Database Schema

```bash
pnpm db:push
```

### 6. Build and Start

```bash
# Build all packages
pnpm build

# Start in production mode
NODE_ENV=production pnpm start

# Or for development
pnpm dev
```

---

## Agent Installation

Install the agent on each mining rig.

### One-Line Installer (Recommended)

```bash
curl -sSL https://raw.githubusercontent.com/bokiko/bloxos/main/apps/agent/install.sh | sudo bash -s -- \
  --server ws://your-server:3001 \
  --token YOUR_RIG_TOKEN
```

### Manual Installation

#### 1. Download Agent

```bash
# Download latest release
wget https://github.com/bokiko/bloxos/releases/latest/download/bloxos-agent-linux-amd64 -O bloxos-agent
chmod +x bloxos-agent
sudo mv bloxos-agent /usr/local/bin/
```

#### 2. Create Configuration

```bash
sudo mkdir -p /etc/bloxos
sudo tee /etc/bloxos/agent.conf << EOF
SERVER_URL=ws://your-server:3001
RIG_TOKEN=your_rig_token_here
STATS_INTERVAL=30
HEARTBEAT_INTERVAL=10
EOF
```

#### 3. Create Systemd Service

```bash
sudo tee /etc/systemd/system/bloxos-agent.service << EOF
[Unit]
Description=BloxOS Mining Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=/etc/bloxos/agent.conf
ExecStart=/usr/local/bin/bloxos-agent --server \${SERVER_URL} --token \${RIG_TOKEN}
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable bloxos-agent
sudo systemctl start bloxos-agent
```

#### 4. Verify Installation

```bash
# Check status
sudo systemctl status bloxos-agent

# View logs
sudo journalctl -u bloxos-agent -f
```

### Getting a Rig Token

1. Log in to the BloxOS dashboard
2. Go to **Rigs** > **Add Rig**
3. Enter rig name and optional details
4. Copy the generated token
5. Use token in agent installation

---

## Configuration

### Environment Variables

See [ENV.md](./ENV.md) for complete environment variable reference.

#### Required Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection string | `postgresql://user:pass@host:5432/db` |
| `JWT_SECRET` | Secret for signing JWTs (32+ chars) | `openssl rand -base64 32` |
| `COOKIE_SECRET` | Secret for cookies (32+ chars) | `openssl rand -base64 32` |
| `ENCRYPTION_KEY` | Key for encrypting SSH credentials | `openssl rand -hex 32` |

#### Optional Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `API_PORT` | `3001` | API server port |
| `NODE_ENV` | `development` | Environment mode |
| `FORCE_HTTPS` | `false` | Redirect HTTP to HTTPS |
| `CORS_ORIGINS` | `*` (dev) | Allowed CORS origins |
| `REDIS_URL` | none | Redis URL for sessions |

### Reverse Proxy (nginx)

For production, use nginx as reverse proxy with SSL:

```nginx
server {
    listen 80;
    server_name bloxos.example.com;
    return 301 https://$server_name$request_uri;
}

server {
    listen 443 ssl http2;
    server_name bloxos.example.com;

    ssl_certificate /etc/letsencrypt/live/bloxos.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/bloxos.example.com/privkey.pem;

    # Dashboard
    location / {
        proxy_pass http://localhost:3000;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection 'upgrade';
        proxy_set_header Host $host;
        proxy_cache_bypass $http_upgrade;
    }

    # API
    location /api {
        proxy_pass http://localhost:3001;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection 'upgrade';
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

---

## Troubleshooting

### Server Issues

#### Database connection failed

```bash
# Check PostgreSQL is running
sudo systemctl status postgresql

# Test connection
psql -h localhost -U bloxos -d bloxos
```

#### Port already in use

```bash
# Find process using port
sudo lsof -i :3001

# Kill process or change port in .env
```

### Agent Issues

#### Agent won't connect

1. Check server URL is correct (include `ws://` or `wss://`)
2. Verify rig token is valid
3. Check firewall allows outbound to server port
4. View agent logs: `journalctl -u bloxos-agent -f`

#### GPU not detected

```bash
# NVIDIA - verify nvidia-smi works
nvidia-smi

# AMD - verify rocm-smi works
rocm-smi

# Check agent has permission to run these
```

#### Miner not detected

1. Ensure miner is running before agent starts
2. Check miner API is enabled (port 4067 for T-Rex, etc.)
3. Restart agent after starting miner

### Common Errors

| Error | Solution |
|-------|----------|
| `ECONNREFUSED` | Server not running or wrong port |
| `Invalid token` | Token doesn't match database |
| `Authentication timeout` | Network latency, increase timeout |
| `Permission denied` | Run agent with sudo or fix permissions |

---

## Updating

### Server Update

```bash
cd bloxos
git pull
pnpm install
pnpm build
pnpm db:push  # Apply any schema changes

# Restart services
docker compose -f docker/docker-compose.yml restart
# or
sudo systemctl restart bloxos-api bloxos-dashboard
```

### Agent Update

```bash
# Using installer
curl -sSL https://raw.githubusercontent.com/bokiko/bloxos/main/apps/agent/install.sh | sudo bash -s -- --update

# Manual
wget https://github.com/bokiko/bloxos/releases/latest/download/bloxos-agent-linux-amd64 -O /tmp/bloxos-agent
sudo mv /tmp/bloxos-agent /usr/local/bin/bloxos-agent
sudo chmod +x /usr/local/bin/bloxos-agent
sudo systemctl restart bloxos-agent
```

---

## Uninstalling

### Server

```bash
# Docker
docker compose -f docker/docker-compose.yml down -v

# Manual
sudo systemctl stop bloxos-api bloxos-dashboard
sudo rm -rf /opt/bloxos
sudo -u postgres psql -c "DROP DATABASE bloxos;"
```

### Agent

```bash
# Using installer
curl -sSL https://raw.githubusercontent.com/bokiko/bloxos/main/apps/agent/install.sh | sudo bash -s -- --uninstall

# Manual
sudo systemctl stop bloxos-agent
sudo systemctl disable bloxos-agent
sudo rm /etc/systemd/system/bloxos-agent.service
sudo rm /usr/local/bin/bloxos-agent
sudo rm -rf /etc/bloxos
sudo systemctl daemon-reload
```

---

*Last Updated: January 2026*
