# BloxOS Handoff Ledger

## State
- Done:
  - [x] Phase 1a: Foundation -- repo cleanup, Go hub + agent + Next.js grid
  - [x] Phase 1b: Live Pipeline -- agent IP, CORS, SSE wiring, machine detail page
- Now: Phase 1c: Polish -- verify demo/live mode toggle, UX tweaks, mobile responsive
- Remaining:
  - [ ] Phase 2: GPU + Services -- go-nvml, systemd, Docker, network/load metrics
  - [ ] Phase 3: Terminal -- xterm.js + creack/pty + re-auth
  - [ ] Phase 4: Alerts + UX -- Telegram, search/filter, tags, list view
  - [ ] Phase 5: Hardening -- charts, retention, auth, auto-update

## Current Branch
rewrite/bloxos-v2

## What Works (End-to-End Verified)
- Agent collects CPU/RAM/disk + local IP, sends to hub every 30s via WebSocket
- Hub stores in SQLite, broadcasts to SSE clients with enriched snapshot
- Dashboard connects to hub SSE, shows live machine card replacing demo data
- Machine detail page (/machine/[id]) shows system/GPU panels with live updates
- Demo mode fallback when hub is offline (with badge)
- GET /api/machines/:id returns machine info + latest metrics
- CORS configured for dashboard at :3000

## Working Set
- Hub: hub/main.go -- Echo server + WebSocket + SSE broadcast + machine detail API
- Agent: agent/main.go -- gopsutil metrics + WebSocket client + IP detection
- Dashboard: dashboard/ -- Next.js 15 + Tailwind v4, dark theme
  - src/app/page.tsx -- Fleet grid with SSE + demo fallback
  - src/app/machine/[id]/page.tsx -- Machine detail view (system + GPU + placeholders)
  - src/components/MachineCard.tsx -- Card with Link wrapper for navigation
  - src/components/ProgressBar.tsx -- Color-coded horizontal progress bar
  - src/components/StatusBadge.tsx -- Online/Warning/Offline badge
  - src/hooks/useFleetSSE.ts -- SSE hook with auto-reconnect
  - src/lib/demo-data.ts -- 6 fake machines for UI dev
- Dev: http://192.168.16.113:3000 (dashboard), :4000 (hub)

## Open Questions
- UNCONFIRMED: go-nvml on consumer GeForce cards -- test in Phase 2
- DECIDED: Echo over Fiber for Go API framework
- DECIDED: go-nvml over nvidia-smi parsing for GPU metrics
- DECIDED: Separate WebSocket for terminal (not multiplexed with metrics)
- DECIDED: Tailwind v4 CSS-based config (@theme inline), no tailwind.config.ts

## Quick Reference
- Hub: cd hub && go run .
- Agent: cd agent && go run . --hub ws://localhost:4000/ws/agent --token test-token
- Dashboard: cd dashboard && npx next dev -p 3000 -H 0.0.0.0
- VM: 192.168.16.113 (BloxOS hub, VLAN 16)
