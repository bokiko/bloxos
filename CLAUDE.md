# BloxOS

Fleet management dashboard for AI machines. Go hub + agent, Next.js dashboard.

## Architecture
- `hub/` — Go API server (Echo + gorilla/websocket), port 4000
- `agent/` — Go agent binary (gopsutil, go-nvml, creack/pty)
- `dashboard/` — Next.js 15 frontend (React, Tailwind, Recharts, xterm.js)
- `proto/` — Shared protocol definitions
- `scripts/` — Install/deploy scripts

## Development
- Hub: `cd hub && go run .`
- Agent: `cd agent && go run . --hub ws://localhost:4000/ws/agent --token <token>`
- Dashboard: `cd dashboard && pnpm dev`

## Continuity
Read `HANDOFF.md` at the start of every session. It tracks what is done, in progress, and next.
Update it before any commit that changes project state.

## Rules
- Agent runs as non-root user with sudo whitelist
- Terminal sessions require re-auth (PIN/password gate)
- All terminal I/O logged for audit
- Credentials NEVER committed — use env vars or local config
- Dark mode is the default UI theme
