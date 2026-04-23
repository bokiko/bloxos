<p align="center">
  <img src="https://capsule-render.vercel.app/api?type=waving&color=gradient&customColorList=14,20,24&height=200&section=header&text=BloxOS&fontSize=60&fontColor=ffffff&animation=fadeIn&fontAlignY=38&desc=Fleet%20Management%20Dashboard%20for%20AI%20Machines&descAlignY=55&descAlign=50" />
</p>

<p align="center">
  <a href="https://github.com/bokiko/bloxos"><img src="https://img.shields.io/badge/GitHub-BloxOS-181717?style=for-the-badge&logo=github" alt="GitHub"></a>
  <a href="https://x.com/bokiko"><img src="https://img.shields.io/badge/X-@bokiko-000000?style=for-the-badge&logo=x&logoColor=white" alt="X"></a>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.24+-2ecc71?style=flat-square&logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/badge/Next.js-15-2ecc71?style=flat-square&logo=next.js&logoColor=white" alt="Next.js">
  <img src="https://img.shields.io/badge/SQLite-WAL-2ecc71?style=flat-square&logo=sqlite&logoColor=white" alt="SQLite">
  <img src="https://img.shields.io/badge/NVIDIA-GPU_Monitoring-2ecc71?style=flat-square&logo=nvidia&logoColor=white" alt="NVIDIA">
  <img src="https://img.shields.io/badge/xterm.js-Terminal-2ecc71?style=flat-square&logo=windowsterminal&logoColor=white" alt="xterm.js">
  <img src="https://img.shields.io/badge/version-1.0.0-2ecc71?style=flat-square" alt="Version">
  <img src="https://img.shields.io/github/last-commit/bokiko/bloxos?style=flat-square&color=2ecc71" alt="Last Commit">
</p>

<p align="center">
  <img src="https://readme-typing-svg.demolab.com?font=Fira+Code&size=18&pause=1000&color=2ecc71&center=true&vCenter=true&width=600&lines=Monitor+GPU+health+across+your+fleet;Restart+services+without+SSH;Built-in+web+terminal+in+your+browser;Dark+mode+%7C+Real-time+%7C+Self-hosted" alt="Typing SVG" />
</p>

## Contents

- [Why BloxOS?](#why-bloxos)
- [Quick Start](#quick-start)
- [Features](#features)
- [Architecture](#architecture)
- [Tech Stack](#tech-stack)
- [Project Structure](#project-structure)
- [Systemd Services](#systemd-services)
- [Continuity](#continuity)
- [License](#license)

---

## Why BloxOS?

If you run a homelab with multiple Ubuntu/Debian machines doing AI inference, LLM serving, or GPU workloads — you know the pain:

1. SSH into each box to check if things are healthy
2. Discover a service crashed 6 hours ago because nobody was watching
3. Restart Ollama on machine #7, then realize machine #3 also needs it
4. Check GPU temps one by one with `nvidia-smi`
5. Lose track of which machine runs what

**BloxOS replaces all of that with one dashboard.** A lightweight Go agent runs on each machine, reports metrics over WebSocket, and lets you control everything from a single dark-mode web UI — including opening a terminal right in your browser.

```
Open dashboard  →  See all machines  →  Click one  →  Check GPU / services  →  Restart Ollama  →  Open terminal  →  Done
```

No cloud. No subscription. No agents phoning home to someone else's server. Everything runs on your network.

---

## Quick Start

### Hub + Dashboard (on your server)

```bash
# Build
cd hub && go build -o bloxos-hub .
cd ../dashboard && pnpm install && pnpm build

# Run
./hub/bloxos-hub &                            # API on :4000
cd dashboard && npx next start -H 0.0.0.0 &   # Dashboard on :3000
```

### Agent (on each machine)

```bash
# One-line install (generate token from dashboard)
curl -sL https://<hub>/install.sh | BLOXOS_HUB=wss://<hub> BLOXOS_TOKEN=<token> bash

# Or manual
cd agent && go build -o bloxos-agent .
./bloxos-agent --hub wss://<hub>/ws/agent --token <token>
```

### First Boot Setup

1. Start the hub.
2. Read the one-time setup token from `~/.bloxos/setup-token`, or set `BLOXOS_SETUP_TOKEN`.
3. `POST /api/setup` with your setup token, admin username, password, and terminal PIN.
4. Log in with the credentials you just created.

---

## Features

<table>
<tr>
<td width="50%">

### Fleet Dashboard
HiveOS-style grid of machine cards with colored status borders (green / amber / red). Real-time CPU, RAM, disk, GPU metrics. Grid and list view toggle for large fleets.

### GPU Monitoring
nvidia-smi integration reporting temperature, utilization, VRAM, power draw, and fan speed. Tested on RTX 3090, 3080, 3070 Ti. Graceful fallback when no GPU present.

### Service & Container Control
Discover and display systemd services and Docker containers. Restart, stop, or start any service or container with one click from the detail view.

### Built-in Web Terminal
xterm.js terminal embedded in the dashboard. PTY relay via WebSocket through the hub. PIN-gated access with session audit metadata. No SSH client needed.

</td>
<td width="50%">

### Alert Engine
6 default rules (CPU >90%, RAM >95%, disk >90%, GPU >80C, GPU >90C, machine offline). Auto-resolve when conditions clear. Telegram push notifications. Slide-out alert panel with badge counter.

### Metric Charts
Sparkline CPU trends on machine cards. Full area charts on detail page for CPU, RAM, GPU temp, and VRAM. Time range selector: 30m, 1h, 6h, 24h, 7d.

### Bulk Actions
Multi-select machines in list view. Restart a service or reboot across the entire selection with one click.

### One-Line Agent Install
Generate a time-limited token from the dashboard. Run one curl command on the target machine. Agent downloads, installs as systemd service, and starts reporting.

### Supported Deploy Path
The supported checked-in deployment path is `Caddy + systemd` on a single host. The Docker Compose files are not currently maintained as a supported runtime path.

</td>
</tr>
</table>

### Additional

- **Search / Filter / Sort** — Filter by hostname, IP, status, or tags. Sort by name, status, CPU, or GPU temp. All client-side, instant results.
- **Machine Tags** — Group machines with custom labels. Filter the fleet by tag.
- **JWT Authentication** — Login-protected dashboard. 24-hour token expiry. Re-auth gate for destructive actions.
- **Metric Retention** — Automatic cleanup: 7 days for metrics, 30 days for alerts. Prevents unbounded DB growth.
- **Network Latency** — Agent-to-hub round-trip displayed on each card and detail view.

---

## Architecture

```
┌──────────────────────────────────────────────────────────┐
│  BloxOS Hub                                               │
│  ┌────────────┐  ┌───────────┐  ┌─────────────────────┐  │
│  │ Dashboard  │  │  Hub API  │  │  Terminal Relay      │  │
│  │ Next.js 15 │  │  Go/Echo  │  │  WebSocket <-> PTY  │  │
│  │ :3000      │  │  :4000    │  │                     │  │
│  └────────────┘  └─────┬─────┘  └──────────┬──────────┘  │
│                        │ SQLite (WAL)       │             │
└────────────────────────┼────────────────────┘             │
                         │                                  │
         ┌───────────────┴───────────────┐                  │
         │  Agent (per machine, ~15MB)   │                  │
         │  gopsutil + nvidia-smi + pty  │<--- WebSocket ---┘
         │  Connects OUTBOUND to hub     │
         └───────────────────────────────┘
```

**Key design decisions:**
- Agents connect **outbound** to the hub — no ports opened on target machines
- Separate WebSocket for terminal I/O (not multiplexed with metrics)
- SSE for dashboard updates (30s fleet view, 5-10s detail view)
- 15-second keepalive pings prevent browser disconnects
- nvidia-smi XML parsing works on all GeForce consumer GPUs
- Agent runs as non-root with sudo whitelist for specific commands

---

## Tech Stack

| Component | Technology |
|-----------|-----------|
| **Hub API** | Go 1.24, Echo, gorilla/websocket, SQLite (WAL mode) |
| **Agent** | Go, gopsutil, nvidia-smi XML parsing, creack/pty |
| **Dashboard** | Next.js 15, React, Tailwind CSS, Recharts, xterm.js |
| **Auth** | JWT (HS256), bcrypt password hashing |
| **Alerts** | Configurable rule engine + Telegram Bot API |
| **Database** | SQLite with WAL mode (single file, zero config) |

---

## Project Structure

```
bloxos/
├── hub/           Go API server + WebSocket relay + alert engine
├── agent/         Go agent binary (metrics, services, Docker, PTY)
├── dashboard/     Next.js 15 frontend (dark mode, Tailwind)
├── scripts/       Install scripts + systemd service files
├── proto/         Shared protocol definitions
├── docs/          Architecture plan
├── HANDOFF.md     Continuity ledger for AI-assisted development
└── CLAUDE.md      Repo-level AI coding instructions
```

---

## Systemd Services

```bash
# Status
sudo systemctl status bloxos-hub bloxos-agent bloxos-dashboard

# Restart
sudo systemctl restart bloxos-hub

# Logs
journalctl -u bloxos-hub -f
journalctl -u bloxos-agent -f
journalctl -u bloxos-dashboard -f
```

Service files are in `scripts/systemd/` and can be copied to `/etc/systemd/system/`.

---

## Continuity

This project uses a **handoff ledger** (`HANDOFF.md`) in the repo root. Any Claude Code session on any machine can read it on start and know what is done, in progress, and next. See `CLAUDE.md` for repo conventions.

---

## License

Private.

<img src="https://capsule-render.vercel.app/api?type=waving&color=gradient&customColorList=14,20,24&height=100&section=footer" />
