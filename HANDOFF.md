# BloxOS Handoff Ledger

## State
- Now: [→] Phase 1: Foundation — repo cleanup, Go hub + agent + Next.js grid
- Remaining:
  - [ ] Phase 2: GPU + Services — go-nvml, systemd, Docker
  - [ ] Phase 3: Terminal — xterm.js + creack/pty + re-auth
  - [ ] Phase 4: Alerts + UX — Telegram, search/filter, tags, list view
  - [ ] Phase 5: Hardening — charts, retention, auth, auto-update

## Current Branch
`rewrite/bloxos-v2`

## Working Set
- Hub: `hub/main.go` — Echo server + WebSocket endpoint
- Agent: `agent/main.go` — gopsutil metrics + WebSocket client
- Dashboard: `dashboard/` — Next.js 15 (not scaffolded yet)

## Open Questions
- UNCONFIRMED: go-nvml on consumer GeForce cards — test in Phase 2
- DECIDED: Echo over Fiber for Go API framework
- DECIDED: go-nvml over nvidia-smi parsing for GPU metrics
- DECIDED: Separate WebSocket for terminal (not multiplexed with metrics)

## Quick Reference
- Hub: `cd hub && go run .`
- Agent: `cd agent && go run . --hub ws://localhost:4000/ws/agent --token test-token`
- Dashboard: `cd dashboard && pnpm dev`
- VM: 192.168.16.113 (BloxOS hub, VLAN 16)
