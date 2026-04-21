# BloxOS Handoff Ledger

## State
- Done:
  - [x] Phase 1a: Foundation — repo cleanup, Go hub (Echo+WS+SQLite) + Go agent (gopsutil+WS) + Next.js dark grid
  - [x] Phase 1b: Live Pipeline — agent IP detection, CORS, SSE wiring, machine detail page
  - [x] Phase 1c: Services & Commands — ServicePanel, ContainerPanel, Toast, RebootModal, command relay
- Now: [→] Phase 1d: Polish + systemd services (so BloxOS survives reboot)
- Remaining:
  - [ ] Phase 2: GPU + Network — go-nvml, network/load metrics
  - [ ] Phase 3: Terminal — xterm.js + creack/pty + re-auth gate
  - [ ] Phase 4: Alerts + UX — Telegram push, search/filter, tags/groups, list view
  - [ ] Phase 5: Hardening — metric charts, retention, dashboard auth, agent auto-update, Windows

## Current Branch
`rewrite/bloxos-v2`

## Immediate Next Steps
1. Create systemd services for hub, agent, dashboard (so they auto-start on boot)
2. Phase 2: GPU metrics via go-nvml (test on a machine with NVIDIA GPU)
3. Phase 3: Terminal (xterm.js + creack/pty)

## What Works (Verified 2026-04-21)
- Agent → Hub → SQLite → SSE → Dashboard: full pipeline working
- Fleet grid: live machine card with colored status border, "seen: Xs ago"
- Detail view: CPU/RAM/disk progress bars, services panel (7 services), containers panel (8 containers)
- Command execution: restart/stop/start services, restart containers, reboot — all via hub relay
- Toast notifications for command feedback
- Shell injection blocked (target validation regex)
- Demo mode fallback when hub offline
- REST: /health, /api/machines, /api/machines/:id, /api/machines/:id/services, /api/machines/:id/containers
- POST /api/machines/:id/command — synchronous relay with 10s timeout

## How to Start (no systemd yet — manual)
```bash
# SSH into VM
ssh bokiko@192.168.16.113  # password: see credentials.md

# Start hub
cd ~/bloxos/hub && nohup ./bloxos-hub > /tmp/bloxos-hub.log 2>&1 &

# Start agent
cd ~/bloxos/agent && nohup ./bloxos-agent --hub ws://localhost:4000/ws/agent --token test-token > /tmp/bloxos-agent.log 2>&1 &

# Start dashboard
cd ~/bloxos/dashboard && nohup npx next dev -H 0.0.0.0 -p 3000 > /tmp/bloxos-dashboard.log 2>&1 &

# Verify
curl http://localhost:4000/health       # → {"status":"ok"}
curl http://localhost:4000/api/machines  # → machine list
# Dashboard at http://192.168.16.113:3000
```

## Working Set
- `hub/main.go` — Go Echo server, WebSocket agent handler, SSE broadcast, SQLite, command relay
- `agent/main.go` — Go agent, gopsutil metrics, WebSocket client, service/Docker discovery, command executor
- `dashboard/` — Next.js 15, Tailwind v4, dark mode
  - `src/app/page.tsx` — Fleet grid
  - `src/app/machine/[id]/page.tsx` — Machine detail
  - `src/components/` — MachineCard, ServicePanel, ContainerPanel, Toast, RebootModal, ProgressBar, StatusBadge
  - `src/hooks/useFleetSSE.ts` — SSE with auto-reconnect
- `docs/PLAN.md` — Full architecture plan

## Open Questions
- UNCONFIRMED: go-nvml on consumer GeForce (RTX 3080/3090) — test in Phase 2
- TODO: systemd units for hub/agent/dashboard (survive reboot)
- TODO: Decide metric retention policy (7d granular + downsample, or flat 90d?)

## Key URLs
- Dashboard: http://192.168.16.113:3000
- Hub API: http://192.168.16.113:4000
- Hub health: http://192.168.16.113:4000/health
- Repo: https://github.com/bokiko/bloxos (PRIVATE)
- Plan: /Users/bokiko/.claude/plans/calm-baking-eclipse.md (Mac) or docs/PLAN.md (repo)

## Tech Stack
- Hub + Agent: Go 1.24.3 (Echo, gorilla/websocket, gopsutil, modernc.org/sqlite)
- Dashboard: Next.js 15 + React + Tailwind v4 + TypeScript
- DB: SQLite WAL mode
- VM: Ubuntu 22.04, 32GB RAM, 322GB disk, VLAN 16
