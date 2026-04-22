# BloxOS

Self-hosted fleet management dashboard for AI and compute machines. Monitor health, control services, and open terminals — all from one dark-mode web UI.

Built for homelabs running Ollama, LLM inference, multi-agent systems, and GPU workloads across multiple machines.

## Features

- **Fleet Dashboard** — HiveOS-style grid of machine cards with real-time CPU, RAM, disk, GPU metrics
- **GPU Monitoring** — Temperature, utilization, VRAM, power draw via nvidia-smi (tested on RTX 3090)
- **Service Control** — View and restart systemd services and Docker containers from the UI
- **Built-in Terminal** — xterm.js web terminal with PTY relay, PIN-gated for security
- **Alert Engine** — Configurable rules (CPU, RAM, disk, GPU temp, offline), auto-resolve, Telegram push
- **Search / Filter / Sort** — Filter by hostname, status, tags. Sort by name, status, CPU, GPU temp
- **Grid + List Views** — Card grid for overview, dense table for scanning 20+ machines
- **Machine Tags** — Group and filter machines by custom tags
- **Metric Charts** — Sparklines on cards, full area charts on detail page (30m to 7d)
- **Bulk Actions** — Restart services or reboot across multiple machines at once
- **One-Line Install** — Generate a token, run one curl command on the target machine
- **JWT Auth** — Login-protected dashboard with session management
- **Dark Mode** — Default. Near-black theme with green/amber/red status accents

## Architecture

```
┌──────────────────────────────────────────────────────────┐
│  BloxOS Hub                                               │
│  ┌────────────┐  ┌───────────┐  ┌─────────────────────┐  │
│  │ Dashboard  │  │  Hub API  │  │  Terminal Relay      │  │
│  │ Next.js 15 │  │  Go/Echo  │  │  WebSocket ↔ PTY    │  │
│  │ :3000      │  │  :4000    │  │                     │  │
│  └────────────┘  └─────┬─────┘  └──────────┬──────────┘  │
│                        │ SQLite (WAL)       │             │
└────────────────────────┼────────────────────┘             │
                         │                                  │
         ┌───────────────┴───────────────┐                  │
         │  Agent (per machine, ~15MB)   │                  │
         │  gopsutil + nvidia-smi + pty  │◄─── WebSocket ───┘
         │  Connects OUTBOUND to hub     │
         └───────────────────────────────┘
```

**Key design decisions:**
- Agents connect outbound — no ports opened on target machines
- Separate WebSocket for terminal I/O (not multiplexed with metrics)
- SSE for dashboard updates (30s fleet, 5-10s detail view)
- nvidia-smi XML parsing for GPU (works on all GeForce cards)
- Agent runs as non-root with sudo whitelist for commands

## Quick Start

### Hub + Dashboard (on your server)

```bash
# Build
cd hub && go build -o bloxos-hub .
cd ../dashboard && pnpm install && pnpm build

# Run
./hub/bloxos-hub &                        # API on :4000
cd dashboard && npx next start -p 3000 &  # Dashboard on :3000
```

### Agent (on each machine)

```bash
# Option 1: One-line install (generate token from dashboard)
curl -sL http://<hub-ip>:4000/install.sh | BLOXOS_HUB=ws://<hub-ip>:4000 BLOXOS_TOKEN=<token> bash

# Option 2: Manual
cd agent && go build -o bloxos-agent .
./bloxos-agent --hub ws://<hub-ip>:4000/ws/agent --token <token>
```

### Default Login

```
Username: admin
Password: bloxos
Terminal PIN: 1234
```

## Tech Stack

| Component | Technology |
|-----------|-----------|
| Dashboard | Next.js 15, React, Tailwind CSS, Recharts, xterm.js |
| Hub API | Go, Echo, gorilla/websocket, SQLite (WAL) |
| Agent | Go, gopsutil, nvidia-smi, creack/pty |
| Auth | JWT (HS256), bcrypt |
| Alerts | Configurable rules + Telegram Bot API |

## Project Structure

```
bloxos/
├── hub/           Go API server + WebSocket relay + alert engine
├── agent/         Go agent binary (metrics, services, Docker, PTY)
├── dashboard/     Next.js 15 frontend
├── scripts/       Install scripts, systemd service files
├── proto/         Shared protocol definitions
├── docs/          Architecture plan
├── HANDOFF.md     Continuity ledger (any Claude can resume from here)
└── CLAUDE.md      Repo-level AI instructions
```

## Systemd Services

```bash
# Manage services
sudo systemctl status bloxos-hub bloxos-agent bloxos-dashboard
sudo systemctl restart bloxos-hub

# Logs
journalctl -u bloxos-hub -f
journalctl -u bloxos-agent -f
```

Service files are in `scripts/systemd/`.

## Continuity

This project uses a **handoff ledger** (`HANDOFF.md`) in the repo root. Any Claude Code session on any machine can read it on start and know exactly what is done, in progress, and next. See `CLAUDE.md` for repo conventions.

## License

Private.
