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

## Rules
- Agent runs as non-root user with sudo whitelist
- Terminal sessions require re-auth (PIN/password gate)
- All terminal I/O logged for audit
- Credentials NEVER committed — use env vars or local config
- Dark mode is the default UI theme
- Caddy TLS uses RSA-2048 keys, not ECDSA. Do not change without testing on stock Windows PowerShell 5.1.

## Audit conventions
Every code change has two failure surfaces — audit both:
- **Forward audit**: what could break *because of* the change?
- **Backward audit (stale-preamble pass)**: what was relying on the old behavior and is now stranded? Anytime the calling shell, parent context, or invocation model changes (e.g. `iwr | iex` → `powershell.exe -File`, parent-shell env vars → child-process env), code that depended on the old context becomes suspect and needs explicit re-evaluation.
- **Incidental-deletion audit**: when a cleanup step wipes a directory, registry key, or shared resource, enumerate every consumer that reads from it — not just the producer that wrote the thing you're targeting. Wiping `.bloxos/` to remove a stale `agent-secret` may also delete a `ca.crt` that TLS validation silently depends on.
