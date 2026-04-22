# BloxOS Handoff Ledger

## State
- Done:
  - [x] Phase 1a: Foundation — repo cleanup, Go hub (Echo+WS+SQLite) + Go agent (gopsutil+WS) + Next.js dark grid
  - [x] Phase 1b: Live Pipeline — agent IP detection, CORS, SSE wiring, machine detail page
  - [x] Phase 1c: Services & Commands — ServicePanel, ContainerPanel, Toast, RebootModal, command relay
  - [x] Phase 1d: Polish + systemd services (so BloxOS survives reboot)
  - [x] Phase 2: GPU + Network — nvidia-smi GPU done, network/load metrics remaining
  - [x] Phase 3: Terminal — xterm.js + creack/pty + PIN gate + audit logging
  - [x] Phase 4: Alerts + UX — alert engine, Telegram push, search/filter, tags, list view, alert panel, add machine flow
- Now: [→] Phase 5: Hardening — metric charts, retention, dashboard auth, agent auto-update, Windows
- Remaining:
  - [ ] Phase 5: Hardening

## Current Branch
`rewrite/bloxos-v2`

## Immediate Next Steps
1. Set BLOXOS_TELEGRAM_TOKEN and BLOXOS_TELEGRAM_CHAT_ID env vars in bloxos-hub.service for Telegram notifications
2. Phase 5: Metric charts (recharts), retention policy, dashboard auth, agent auto-update
3. Test terminal on remote GPU machines (ai-03 with RTX 3090)
4. Make terminal PIN configurable (env var or settings)

## What Works (Verified 2026-04-22)
- Agent → Hub → SQLite → SSE → Dashboard: full pipeline working
- Fleet grid: live machine card with colored status border, "seen: Xs ago"
- Detail view: CPU/RAM/disk progress bars, services panel, containers panel
- Command execution: restart/stop/start services, restart containers, reboot — all via hub relay
- Toast notifications for command feedback
- GPU metrics: nvidia-smi XML parsing, per-GPU display (temp, util, VRAM, power, fan)
- **Terminal: xterm.js + PTY via creack/pty, PIN gate (hardcoded 1234), WebSocket relay, audit logging**
- **Alert Engine (Phase 4):**
  - 6 default alert rules: CPU>90%, RAM>95%, Disk>90%, GPU>80C (warn), GPU>90C (crit), Offline>120s (crit)
  - Evaluation loop every 30s — auto-creates and auto-resolves alerts
  - Telegram push notifications (new alert + resolved) — requires BLOXOS_TELEGRAM_TOKEN + BLOXOS_TELEGRAM_CHAT_ID env vars
  - REST API: GET /api/alerts, GET /api/alerts/active/count, POST /api/alerts/:id/acknowledge
  - GET /api/alert-rules, PUT /api/alert-rules/:id (enable/disable, change threshold)
  - SSE alert events for real-time dashboard updates
- **UX Polish (Phase 4):**
  - Search bar: filter machines by hostname/IP (live, case-insensitive)
  - Status filter dropdown: All / Online / Warning / Offline
  - Sort dropdown: Name (A-Z) / Status (worst first) / CPU% / GPU Temp
  - Grid/List view toggle — list view is a dense table with all key metrics
  - Machine tags: comma-separated, shown as pills on MachineCard, filterable in search bar
  - PUT /api/machines/:id/tags — set tags for a machine
  - Alert slide-out panel: lists active alerts, acknowledge individual or all, real-time via SSE
  - Add Machine modal: generates one-time install token, shows curl command
  - POST /api/tokens, GET /install.sh, GET /download/agent — one-line agent install flow
- Shell injection blocked (target validation regex)
- Demo mode fallback when hub offline

## Telegram Setup
To enable Telegram notifications, add env vars to the hub systemd service:
```bash
sudo systemctl edit bloxos-hub
# Add under [Service]:
# Environment="BLOXOS_TELEGRAM_TOKEN=your-bot-token"
# Environment="BLOXOS_TELEGRAM_CHAT_ID=your-chat-id"
sudo systemctl restart bloxos-hub
```

## How to Start (systemd -- auto-starts on boot)
```bash
sudo systemctl start bloxos-hub bloxos-agent bloxos-dashboard
sudo systemctl stop bloxos-hub bloxos-agent bloxos-dashboard
sudo systemctl restart bloxos-hub bloxos-agent bloxos-dashboard
systemctl status bloxos-hub bloxos-agent bloxos-dashboard
journalctl -u bloxos-hub -f
curl http://localhost:4000/health
curl http://localhost:4000/api/machines
curl http://localhost:4000/api/alert-rules
curl http://localhost:4000/api/alerts
# Dashboard at http://192.168.16.113:3000
```

## Working Set
- `hub/main.go` — Go Echo server, WebSocket agent handler, SSE broadcast, SQLite, command relay, terminal relay, alert engine, Telegram, install flow
- `agent/main.go` — Go agent, gopsutil metrics, WebSocket client, service/Docker discovery, command executor, PTY terminal
- `dashboard/` — Next.js 16, Tailwind v4, dark mode
  - `src/app/page.tsx` — Fleet grid + list view + search/filter/sort + alert panel + add machine
  - `src/app/machine/[id]/page.tsx` — Machine detail + terminal UI
  - `src/components/` — MachineCard, AlertPanel, AddMachineModal, ServicePanel, ContainerPanel, Toast, RebootModal, ProgressBar, StatusBadge, Terminal
  - `src/hooks/useFleetSSE.ts` — SSE with auto-reconnect + alert events
  - `src/lib/demo-data.ts` — Type definitions + demo data
- `docs/PLAN.md` — Full architecture plan

## Open Questions
- UNCONFIRMED: Terminal on remote machines (tested only on bloxOs local agent)
- TODO: Make terminal PIN configurable (env var or hub config)
- TODO: Full I/O audit logging for terminal sessions (Phase 5)
- TODO: Decide metric retention policy (7d granular + downsample, or flat 90d?)

## Key URLs
- Dashboard: http://192.168.16.113:3000
- Hub API: http://192.168.16.113:4000
- Hub health: http://192.168.16.113:4000/health
- Repo: https://github.com/bokiko/bloxos (PRIVATE)

## Tech Stack
- Hub + Agent: Go 1.25.0 (Echo, gorilla/websocket, gopsutil, modernc.org/sqlite, creack/pty)
- Dashboard: Next.js 16 + React 19 + Tailwind v4 + TypeScript + xterm.js 6
- DB: SQLite WAL mode
- VM: Ubuntu 22.04, 32GB RAM, 322GB disk, VLAN 16
