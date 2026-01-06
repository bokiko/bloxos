<p align="center">
  <img src="docs/assets/logo.png" alt="BloxOS Logo" width="200"/>
</p>

<h1 align="center">BloxOS</h1>

<p align="center">
  <strong>Open-source, self-hosted mining rig management system</strong>
</p>

<p align="center">
  <a href="#features">Features</a> •
  <a href="#system-requirements">Requirements</a> •
  <a href="#installation">Installation</a> •
  <a href="#dashboard-pages">Dashboard</a> •
  <a href="#supported-hardware">Hardware</a> •
  <a href="#contributing">Contributing</a>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License"/>
  <img src="https://img.shields.io/badge/platform-Windows%20%7C%20macOS%20%7C%20Linux-lightgrey.svg" alt="Platform"/>
  <img src="https://img.shields.io/badge/PRs-welcome-brightgreen.svg" alt="PRs Welcome"/>
  <img src="https://img.shields.io/badge/status-alpha-orange.svg" alt="Status: Alpha"/>
</p>

---

## Table of Contents

- [Overview](#overview)
- [Features](#features)
- [System Requirements](#system-requirements)
  - [Server Requirements](#server-requirements)
  - [Mining Rig Requirements](#mining-rig-requirements)
- [Installation](#installation)
  - [Windows Installation](#windows-installation)
  - [macOS Installation](#macos-installation)
  - [Linux Installation](#linux-installation)
  - [Raspberry Pi Installation](#raspberry-pi-installation)
  - [Docker Installation (All Platforms)](#docker-installation-all-platforms)
- [Quick Start Guide](#quick-start-guide)
- [Dashboard Pages](#dashboard-pages)
  - [Dashboard (Home)](#1-dashboard-home)
  - [Rigs](#2-rigs)
  - [Rig Detail](#3-rig-detail)
  - [Wallets](#4-wallets)
  - [Pools](#5-pools)
  - [Flight Sheets](#6-flight-sheets)
  - [OC Profiles](#7-oc-profiles)
  - [Rig Groups](#8-rig-groups)
  - [Alerts](#9-alerts)
  - [Users](#10-users)
  - [Settings](#11-settings)
- [Mining Support](#mining-support)
  - [GPU Mining](#gpu-mining)
  - [CPU Mining](#cpu-mining)
- [Supported Hardware](#supported-hardware)
- [Tech Stack](#tech-stack)
- [Project Structure](#project-structure)
- [Development](#development)
- [API Reference](#api-reference)
- [Security](#security)
- [Troubleshooting](#troubleshooting)
- [Contributing](#contributing)
- [License](#license)
- [Acknowledgments](#acknowledgments)

---

## Current Status

> **Note:** BloxOS is currently in **alpha** development. The core features are implemented but the system is not yet production-ready.

### Implemented Features

| Feature | Status | Notes |
|---------|--------|-------|
| API Server | Done | Fastify with full route coverage |
| Dashboard UI | Done | Next.js 15 with all pages |
| Authentication | Done | JWT + refresh tokens, strong passwords |
| Rig Management | Done | CRUD, groups, monitoring |
| SSH Integration | Done | Command execution, system info |
| Flight Sheets | Done | Wallet + Pool + Miner config |
| OC Profiles | Done | NVIDIA/AMD overclock settings |
| Alerts System | Done | Temperature, offline, hashrate alerts |
| Multi-user | Done | Roles: Admin, User, Monitor |
| Security Hardening | Done | Rate limiting, CSRF, RBAC |
| Agent (Go) | Partial | Skeleton implemented |
| WebSocket Updates | Partial | Basic implementation |
| Docker Deployment | Partial | Compose file ready |

### What's Working Now

- Full API with 20+ route files
- Dashboard with 12 pages
- User registration/login with secure auth
- Add rigs via SSH auto-discovery
- Assign flight sheets and OC profiles
- Execute SSH commands on rigs
- View GPU/CPU stats
- Alert configuration per rig
- Bulk actions on multiple rigs

---

## Overview

BloxOS is a **free, open-source alternative to HiveOS** for managing cryptocurrency mining rigs. Run it on your own server (even a Raspberry Pi!) and take full control of your mining operation.

**Why BloxOS?**
- **No monthly fees** - Self-hosted, completely free
- **Privacy first** - Your data stays on your server
- **Full control** - Customize everything to your needs
- **Lightweight** - Runs on minimal hardware including Raspberry Pi

---

## Features

| Category | Features |
|----------|----------|
| **Monitoring** | Real-time GPU/CPU stats, temperatures, hashrates, power consumption |
| **Control** | Start/stop miners, reboot rigs, execute SSH commands, web terminal |
| **Configuration** | Flight sheets, overclock profiles, wallet management, pool management |
| **Organization** | Rig groups, bulk actions, multi-user support with roles |
| **Alerts** | Temperature alerts, offline detection, hashrate drop notifications |
| **Mining** | GPU mining (NVIDIA/AMD), CPU mining support, multiple miner software |

---

## System Requirements

### Server Requirements

The server runs the BloxOS dashboard and API. This is where you access the web interface.

| Spec | Minimum | Recommended |
|------|---------|-------------|
| **CPU** | 2 cores | 4+ cores |
| **RAM** | 2 GB | 4+ GB |
| **Storage** | 10 GB | 20+ GB SSD |
| **OS** | Any (see below) | Ubuntu 22.04+ LTS |
| **Network** | 100 Mbps | 1 Gbps |

**Supported Server Platforms:**
- Windows 10/11
- macOS 12+ (Intel/Apple Silicon)
- Linux (Ubuntu, Debian, Fedora, Arch, etc.)
- Raspberry Pi 4/5 (4GB+ RAM recommended)

### Mining Rig Requirements

Each mining rig needs the BloxOS agent installed.

| Spec | Minimum |
|------|---------|
| **OS** | Linux (Ubuntu 20.04+, HiveOS, etc.) |
| **RAM** | 4 GB |
| **Network** | Stable connection to server |
| **GPU Driver** | NVIDIA 470+ / AMD AMDGPU-PRO |

---

## Installation

### Windows Installation

**Prerequisites:**
1. Install [Docker Desktop](https://www.docker.com/products/docker-desktop/)
2. Install [Git](https://git-scm.com/download/win)

**Steps:**

```powershell
# 1. Open PowerShell as Administrator

# 2. Clone the repository
git clone https://github.com/bokiko/bloxos.git
cd bloxos

# 3. Copy environment file
copy .env.example .env

# 4. Start with Docker
docker compose up -d

# 5. Open browser
start http://localhost:3000
```

---

### macOS Installation

**Prerequisites:**
1. Install [Docker Desktop](https://www.docker.com/products/docker-desktop/)
2. Install [Homebrew](https://brew.sh/) (optional but recommended)

**Steps:**

```bash
# 1. Open Terminal

# 2. Install Git (if not installed)
xcode-select --install

# 3. Clone the repository
git clone https://github.com/bokiko/bloxos.git
cd bloxos

# 4. Copy environment file
cp .env.example .env

# 5. Start with Docker
docker compose up -d

# 6. Open browser
open http://localhost:3000
```

---

### Linux Installation

**Prerequisites:**
```bash
# Ubuntu/Debian
sudo apt update
sudo apt install -y git curl docker.io docker-compose

# Start Docker
sudo systemctl start docker
sudo systemctl enable docker

# Add your user to docker group (logout/login after)
sudo usermod -aG docker $USER
```

**Steps:**

```bash
# 1. Clone the repository
git clone https://github.com/bokiko/bloxos.git
cd bloxos

# 2. Copy environment file
cp .env.example .env

# 3. Start with Docker
docker compose up -d

# 4. Open browser
# Navigate to http://YOUR_SERVER_IP:3000
```

---

### Raspberry Pi Installation

BloxOS runs great on Raspberry Pi 4/5 with 4GB+ RAM!

**Prerequisites:**
- Raspberry Pi 4 or 5 (4GB+ RAM)
- Raspberry Pi OS (64-bit) or Ubuntu Server
- MicroSD card (32GB+ recommended) or SSD

**Steps:**

```bash
# 1. Update system
sudo apt update && sudo apt upgrade -y

# 2. Install Docker
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER

# 3. Logout and login again, then:
git clone https://github.com/bokiko/bloxos.git
cd bloxos

# 4. Copy environment file
cp .env.example .env

# 5. Start with Docker (uses ARM-compatible images)
docker compose -f docker/docker-compose.yml up -d

# 6. Access from any device on your network
# http://RASPBERRY_PI_IP:3000
```

**Raspberry Pi Tips:**
- Use an SSD instead of SD card for better performance
- Ensure adequate cooling (fan/heatsink)
- Use a quality 5V/3A+ power supply
- Consider using Pi 5 for best performance

---

### Docker Installation (All Platforms)

The easiest way to run BloxOS on any platform.

**One-Command Install:**

```bash
# Clone and start
git clone https://github.com/bokiko/bloxos.git && cd bloxos && docker compose up -d
```

**What this does:**
1. Pulls PostgreSQL and Redis containers
2. Builds and starts the API server
3. Builds and starts the Dashboard
4. Everything runs on `http://localhost:3000`

**Docker Commands:**

```bash
# Start services
docker compose up -d

# Stop services
docker compose down

# View logs
docker compose logs -f

# Restart services
docker compose restart

# Update to latest version
git pull && docker compose up -d --build
```

---

## Quick Start Guide

### First Time Setup

1. **Open Dashboard** - Navigate to `http://YOUR_SERVER:3000`
2. **Create Admin Account** - You'll be redirected to `/setup` to create your admin account
3. **Add Your First Rig:**
   - Go to **Rigs** > **Add Rig**
   - Enter rig name, hostname/IP, SSH credentials
   - Click **Add Rig**

4. **Configure Mining:**
   - Add a **Wallet** (your crypto wallet address)
   - Add a **Pool** (mining pool URL)
   - Create a **Flight Sheet** (combines wallet + pool + miner)
   - Assign flight sheet to your rig

5. **Start Mining:**
   - Go to rig detail page
   - Click **Start Miner**

---

## Dashboard Pages

### 1. Dashboard (Home)
**Path:** `/`

| Feature | Description |
|---------|-------------|
| Overview Stats | Total rigs, online/offline count, total hashrate, power consumption |
| Rig List | Quick view of all rigs with status indicators |
| Recent Alerts | Latest alerts and notifications |
| Quick Actions | Fast access to common tasks |

---

### 2. Rigs
**Path:** `/rigs`

| Feature | Description |
|---------|-------------|
| Rig List | All rigs with status, hashrate, temperature, power |
| Filters | Filter by status (online/offline) or group |
| Bulk Select | Select multiple rigs for bulk actions |
| Bulk Actions | Start/stop miners, apply OC, reboot, assign flight sheets |
| Auto-Refresh | Live updates every 30 seconds |
| Add Rig | Button to add new mining rig |

---

### 3. Rig Detail
**Path:** `/rigs/[id]`

| Feature | Description |
|---------|-------------|
| Status Header | Rig name, status, IP, OS info, group tags |
| Stats Cards | GPU count, hashrate, temperature, last seen |
| Power Breakdown | GPU power, CPU power, total consumption |
| Mining Control | Assign flight sheet, start/stop miner |
| OC Control | Assign OC profile, apply/reset overclock |
| GPU Details | Per-GPU stats (temp, fan, power, clocks, hashrate) |
| CPU Details | CPU model, cores, temperature, usage, hashrate |
| System Info | Detailed hardware info (click "Load Details") |
| Alert Settings | Configure temperature/offline/hashrate alerts |
| SSH Terminal | Execute commands directly on the rig |
| Web Terminal | Full interactive terminal (xterm.js) |

---

### 4. Wallets
**Path:** `/wallets`

| Feature | Description |
|---------|-------------|
| Wallet List | All configured cryptocurrency wallets |
| Add Wallet | Name, coin type, wallet address |
| Edit/Delete | Modify or remove wallets |
| Coin Support | BTC, ETH, ETC, RVN, ERGO, KAS, and more |

---

### 5. Pools
**Path:** `/pools`

| Feature | Description |
|---------|-------------|
| Pool List | All configured mining pools |
| Add Pool | Name, coin, URL, username template |
| Pool Templates | Common pools pre-configured |
| Edit/Delete | Modify or remove pools |

---

### 6. Flight Sheets
**Path:** `/flight-sheets`

| Feature | Description |
|---------|-------------|
| Flight Sheet List | All mining configurations |
| Create Flight Sheet | Combine wallet + pool + miner + settings |
| Miner Selection | Choose miner software (T-Rex, lolMiner, etc.) |
| Extra Config | Additional miner arguments |
| Edit/Delete | Modify or remove flight sheets |

---

### 7. OC Profiles
**Path:** `/oc-profiles`

| Feature | Description |
|---------|-------------|
| Profile List | All overclock profiles |
| Create Profile | Name, vendor (NVIDIA/AMD), settings |
| NVIDIA Settings | Power limit, core offset, memory offset, fan speed |
| AMD Settings | Core clock, memory clock, voltage, fan speed |
| Edit/Delete | Modify or remove profiles |

---

### 8. Rig Groups
**Path:** `/rig-groups`

| Feature | Description |
|---------|-------------|
| Group List | All rig groups with colors |
| Create Group | Name and color picker |
| Rig Count | Number of rigs in each group |
| Edit/Delete | Modify or remove groups |
| Filtering | Use groups to filter rigs on other pages |

---

### 9. Alerts
**Path:** `/alerts`

| Feature | Description |
|---------|-------------|
| Alert List | All alerts with severity and timestamp |
| Filter | Show all or unread only |
| Mark as Read | Individual or mark all as read |
| Dismiss | Remove alerts from list |
| Alert Types | Temperature, offline, hashrate drop, errors |

---

### 10. Users
**Path:** `/users` (Admin only)

| Feature | Description |
|---------|-------------|
| User List | All users with roles |
| Add User | Create new user accounts |
| Roles | ADMIN (full access), USER (standard), MONITOR (read-only) |
| Edit/Delete | Modify or remove users |

---

### 11. Settings
**Path:** `/settings`

| Feature | Description |
|---------|-------------|
| Profile | Update display name and email |
| Password | Change account password |
| Preferences | UI settings (coming soon) |

---

## Mining Support

### GPU Mining

BloxOS supports both NVIDIA and AMD GPUs for cryptocurrency mining.

| Feature | NVIDIA | AMD |
|---------|--------|-----|
| Temperature Monitoring | Yes | Yes |
| Fan Speed Control | Yes | Yes |
| Power Limit | Yes | Yes |
| Core Clock Offset | Yes | Yes (absolute) |
| Memory Clock Offset | Yes | Yes (absolute) |
| Hashrate Monitoring | Yes | Yes |

**Supported Miner Software:**
- T-Rex (NVIDIA)
- TeamRedMiner (AMD)
- lolMiner (NVIDIA/AMD)
- NBMiner (NVIDIA/AMD)
- PhoenixMiner (NVIDIA/AMD)
- GMiner (NVIDIA/AMD)

### CPU Mining

BloxOS includes full support for CPU mining!

| Feature | Description |
|---------|-------------|
| CPU Monitoring | Temperature, usage, frequency, power draw |
| CPU Hashrate | Track CPU mining performance |
| Enable/Disable | Toggle CPU mining per rig |
| Supported Miners | XMRig, CPUMiner |

**CPU Mining Use Cases:**
- Monero (XMR) mining
- Raptoreum (RTM) mining  
- Verus (VRSC) mining
- Other CPU-mineable coins

**Enabling CPU Mining:**
1. Go to rig detail page
2. In "Mining Monitoring" section, click **CPU On**
3. Create a flight sheet with CPU miner (XMRig)
4. Assign to rig and start mining

---

## Supported Hardware

### GPUs

| Vendor | Series | Support Level |
|--------|--------|---------------|
| **NVIDIA** | GTX 1000 series | Full |
| **NVIDIA** | GTX 1600 series | Full |
| **NVIDIA** | RTX 2000 series | Full |
| **NVIDIA** | RTX 3000 series | Full |
| **NVIDIA** | RTX 4000 series | Full |
| **AMD** | RX 400/500 series | Full |
| **AMD** | RX Vega series | Full |
| **AMD** | RX 5000 series | Full |
| **AMD** | RX 6000 series | Full |
| **AMD** | RX 7000 series | Full |

### CPUs

| Vendor | Support |
|--------|---------|
| Intel | Full (Core, Xeon) |
| AMD | Full (Ryzen, EPYC, Threadripper) |

### Server Hardware

| Platform | Support |
|----------|---------|
| x86_64 (Intel/AMD) | Full |
| ARM64 (Raspberry Pi 4/5) | Full |
| Apple Silicon (M1/M2/M3) | Full (via Docker) |

---

## Tech Stack

| Component | Technology |
|-----------|------------|
| **Dashboard** | Next.js 15, React 18, TailwindCSS |
| **API Server** | Node.js, Fastify, WebSocket |
| **Database** | PostgreSQL 16 |
| **Cache** | Redis |
| **ORM** | Prisma |
| **Agent** | Go 1.22 |
| **Monorepo** | Turborepo, pnpm |
| **Containerization** | Docker, Docker Compose |

---

## Security

BloxOS has been designed with security in mind. The following security measures are implemented:

### Authentication & Authorization

| Feature | Implementation |
|---------|----------------|
| Password Requirements | 12+ chars, uppercase, lowercase, numbers, special chars |
| Password Hashing | bcrypt with 12 rounds |
| JWT Tokens | 4-hour access tokens + 7-day refresh tokens |
| Session Management | Token blacklisting, secure logout |
| Rate Limiting | Per-IP limits on all endpoints, stricter on auth |
| CSRF Protection | Double-submit cookie pattern |
| RBAC | Farm-based access control (users only see their rigs) |

### Data Protection

| Feature | Implementation |
|---------|----------------|
| SSH Credentials | AES-256-GCM encryption with PBKDF2 key derivation |
| Command Injection | Whitelist-based command validation |
| Input Validation | Zod schemas on all API inputs |
| SQL Injection | Prisma ORM with parameterized queries |

### Security Headers

| Header | Value |
|--------|-------|
| Helmet | Full security headers enabled |
| CORS | Configurable origins (strict in production) |
| Cookies | SameSite=strict, HttpOnly, Secure |

### Operational Security

| Feature | Implementation |
|---------|----------------|
| Secret Validation | Fails fast if secrets missing in production |
| Audit Logging | All sensitive operations logged |
| Request Tracing | X-Request-ID on all requests |
| Error Handling | Sanitized error messages in production |

### Environment Variables (Required in Production)

```bash
JWT_SECRET=<random-64-char-string>
ENCRYPTION_KEY=<random-64-char-string>
COOKIE_SECRET=<random-32-char-string>
CORS_ORIGINS=https://yourdomain.com
```

Generate secure secrets:
```bash
openssl rand -base64 48
```

---

## Project Structure

```
bloxos/
├── apps/
│   ├── api/                    # Fastify API server
│   │   ├── src/
│   │   │   ├── routes/         # API endpoints (20+ files)
│   │   │   │   ├── auth.ts     # Authentication
│   │   │   │   ├── rigs.ts     # Rig management
│   │   │   │   ├── ssh.ts      # SSH commands
│   │   │   │   ├── miners.ts   # Miner control
│   │   │   │   └── ...
│   │   │   ├── services/       # Business logic
│   │   │   │   ├── auth-service.ts
│   │   │   │   ├── ssh-manager.ts
│   │   │   │   ├── miner-control.ts
│   │   │   │   ├── oc-service.ts
│   │   │   │   └── ...
│   │   │   ├── middleware/     # Auth, CSRF, RBAC
│   │   │   │   ├── auth.ts
│   │   │   │   ├── authorization.ts
│   │   │   │   └── csrf.ts
│   │   │   └── utils/          # Helpers
│   │   │       ├── security.ts
│   │   │       └── encryption.ts
│   │   └── package.json
│   ├── dashboard/              # Next.js web UI
│   │   ├── src/
│   │   │   ├── app/            # Pages (App Router)
│   │   │   │   ├── rigs/       # Rig pages
│   │   │   │   ├── wallets/
│   │   │   │   ├── pools/
│   │   │   │   ├── flight-sheets/
│   │   │   │   ├── oc-profiles/
│   │   │   │   ├── alerts/
│   │   │   │   ├── users/
│   │   │   │   └── settings/
│   │   │   ├── components/
│   │   │   └── context/
│   │   └── package.json
│   └── agent/                  # Go agent for rigs
│       ├── cmd/agent/
│       └── internal/
│           ├── api/
│           ├── collector/
│           └── config/
├── packages/
│   └── database/               # Prisma schema & client
│       └── prisma/
│           └── schema.prisma   # 20+ models
├── docker/
│   └── docker-compose.yml
├── thoughts/                   # Continuity ledgers
│   └── ledgers/
├── .env.example
├── AGENTS.md                   # AI agent guidelines
└── turbo.json
```

---

## Development

### Prerequisites

- Node.js 22+
- pnpm 9+
- Go 1.22+ (for agent)
- Docker & Docker Compose

### Setup

```bash
# Install dependencies
pnpm install

# Start databases
docker compose up -d postgres redis

# Push database schema
pnpm db:push

# Start development servers
pnpm dev
```

### Commands

| Command | Description |
|---------|-------------|
| `pnpm dev` | Start all apps in development mode |
| `pnpm build` | Build all apps for production |
| `pnpm lint` | Run linting |
| `pnpm test` | Run tests |
| `pnpm db:push` | Push Prisma schema to database |
| `pnpm db:studio` | Open Prisma Studio |
| `pnpm db:migrate` | Create migration |

### Building the Agent

```bash
cd apps/agent

# Build for Linux (amd64)
GOOS=linux GOARCH=amd64 go build -o bloxos-agent-linux-amd64 ./cmd/agent

# Build for Linux (arm64)
GOOS=linux GOARCH=arm64 go build -o bloxos-agent-linux-arm64 ./cmd/agent
```

---

## API Reference

### Authentication

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/auth/register` | POST | Register new user |
| `/api/auth/login` | POST | Login and get JWT |
| `/api/auth/logout` | POST | Logout |
| `/api/auth/me` | GET | Get current user |

### Rigs

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/rigs` | GET | List all rigs |
| `/api/rigs` | POST | Create new rig |
| `/api/rigs/:id` | GET | Get rig details |
| `/api/rigs/:id` | PATCH | Update rig |
| `/api/rigs/:id` | DELETE | Delete rig |
| `/api/rigs/:id/miner/start` | POST | Start miner |
| `/api/rigs/:id/miner/stop` | POST | Stop miner |

### Resources

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/wallets` | GET/POST | Manage wallets |
| `/api/pools` | GET/POST | Manage pools |
| `/api/flight-sheets` | GET/POST | Manage flight sheets |
| `/api/oc-profiles` | GET/POST | Manage OC profiles |
| `/api/rig-groups` | GET/POST | Manage rig groups |
| `/api/alerts` | GET | List alerts |

### SSH Operations

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/ssh/test` | POST | Test SSH connection |
| `/api/ssh/add-rig` | POST | Add rig via SSH discovery |
| `/api/ssh/rig/:id/exec` | POST | Execute command on rig |
| `/api/ssh/rig/:id/system-info` | GET | Get detailed system info |

### Security

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/csrf-token` | GET | Get CSRF token |
| `/api/auth/refresh` | POST | Refresh access token |

**Note:** All state-changing requests require `X-CSRF-Token` header.

---

## Troubleshooting

### Common Issues

<details>
<summary><strong>Cannot connect to rig via SSH</strong></summary>

1. Verify rig is online and reachable: `ping RIG_IP`
2. Check SSH credentials are correct
3. Ensure SSH is enabled on the rig: `sudo systemctl status ssh`
4. Check firewall allows port 22

</details>

<details>
<summary><strong>Dashboard shows "Authentication required"</strong></summary>

1. Clear browser cookies
2. Try incognito/private browsing mode
3. Check API server is running: `docker compose logs api`

</details>

<details>
<summary><strong>GPU not detected</strong></summary>

1. Verify GPU drivers installed: `nvidia-smi` or `rocm-smi`
2. Check NVIDIA driver version 470+
3. Restart the agent on the rig

</details>

<details>
<summary><strong>Miner won't start</strong></summary>

1. Ensure flight sheet is assigned to rig
2. Check miner binary exists on rig
3. View rig events for error messages
4. Check SSH terminal for miner output

</details>

### Getting Help

- Check [GitHub Issues](https://github.com/bokiko/bloxos/issues)
- Review logs: `docker compose logs -f`
- Enable debug mode in `.env`

---

## Contributing

Contributions are welcome! Please read our contributing guidelines.

### How to Contribute

1. **Fork** the repository
2. **Create** your feature branch
   ```bash
   git checkout -b feature/amazing-feature
   ```
3. **Commit** your changes
   ```bash
   git commit -m 'feat: add amazing feature'
   ```
4. **Push** to the branch
   ```bash
   git push origin feature/amazing-feature
   ```
5. **Open** a Pull Request

### Commit Convention

We use [Conventional Commits](https://www.conventionalcommits.org/):

- `feat:` - New feature
- `fix:` - Bug fix
- `docs:` - Documentation
- `refactor:` - Code refactoring
- `test:` - Adding tests
- `chore:` - Maintenance

---

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

---

## Acknowledgments

- Inspired by [HiveOS](https://hiveos.farm)
- Built with the amazing open-source community
- Thanks to all contributors!

---

<p align="center">
  <strong>BloxOS</strong> - Take control of your mining operation.
</p>

<p align="center">
  <a href="https://github.com/bokiko/bloxos">GitHub</a> •
  <a href="https://github.com/bokiko/bloxos/issues">Issues</a> •
  <a href="https://github.com/bokiko/bloxos/discussions">Discussions</a>
</p>
