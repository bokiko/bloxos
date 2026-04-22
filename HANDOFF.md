# BloxOS Handoff Ledger

## State
- Done:
  - [x] Phase 1: Foundation — Go hub (Echo+WS+SQLite) + Go agent (gopsutil+WS) + Next.js dark grid
  - [x] Phase 2: GPU + Services — nvidia-smi, systemd, Docker discovery, command relay
  - [x] Phase 3: Terminal — xterm.js + creack/pty + PTY relay + PIN gate
  - [x] Phase 4: Alerts + UX — alert engine, Telegram ready, search/filter/sort, tags, list view, one-line install
  - [x] Phase 5: Hardening — metric charts, JWT auth, retention cleanup, latency, bulk actions
  - [x] UI Redesign — shadcn/ui + Framer Motion + Geist fonts
  - [x] Codex Security Audit (3 rounds) — 6/8 fixed, 2 accepted
  - [x] Hardening (Do Now) — creds rotated, DB perms, token logging, single-use tokens, Caddy TLS, config, rate limiting
- Now: Stable. All "Do Now" hardening complete.
- Remaining (Do Next Sprint):
  - [ ] Migration versioning (do FIRST before schema changes)
  - [ ] Smoke tests (auth, enrollment, terminal — ~10-15 tests)
  - [ ] Backend-enforce password/PIN rotation (middleware, not just UI modal)
  - [ ] First-boot setup flow (no default creds, bootstrap proof required)
  - [ ] Enrollment redesign (bootstrap token → hashed durable agent credential)
  - [ ] Terminal privilege tightening (30min timeout, max 3 sessions, metadata audit)

## Current Branch
`main`

## Credentials (rotated 2026-04-22)
- Admin password: `Bl0x0s!Fleet#2026`
- Terminal PIN: `8371`
- First-run token: `~/.bloxos/first-run-token` (if fresh DB)
- JWT secret: `~/.bloxos/jwt-secret` (auto-generated)

## Security Posture (2026-04-22, Codex-audited)
- Install tokens: single-use (consumed on enrollment)
- Known agents: reconnect without re-validating token
- Terminal PIN: server-side enforced (bcrypt)
- Terminal WebSocket: browser=JWT, agent=terminal_token
- Caddy TLS: reverse proxy on :443, hub/dashboard localhost only
- Config: PUBLIC_URL, HUB_LISTEN, ALLOWED_ORIGINS via env
- Rate limiting: login 5/min, terminal 5/min, token 3/min, enrollment 10/min
- Trusted proxy: real IP from X-Forwarded-For (127.0.0.1 only)
- DB: 0600 permissions
- Tokens: never logged, written to file only
- Accepted: default creds with forced change UI, scoped SSE tokens in URL

## Architecture
```
LAN → Caddy (:443 TLS) → Hub (127.0.0.1:4000) + Dashboard (127.0.0.1:3000)
                           ↑
                     Agents (WSS through Caddy)
```

## Service Management (systemd, auto-start on boot)
```bash
systemctl is-active bloxos-hub bloxos-agent bloxos-dashboard caddy
sudo systemctl restart bloxos-hub
journalctl -u bloxos-hub -f
```

## Key URLs
- Dashboard: https://192.168.16.113
- Hub health: https://192.168.16.113/health
- Hub direct (localhost): http://127.0.0.1:4000
- Repo: https://github.com/bokiko/bloxos (PRIVATE)

## Working Set
- `hub/main.go` — ~2700 lines. API, WS, SSE, auth, alerts, rate limiting, config
- `agent/main.go` — ~800 lines. Metrics, services, Docker, GPU, PTY, commands
- `dashboard/` — Next.js 15, shadcn/ui, Framer Motion, Geist, dark mode
- `scripts/caddy/Caddyfile` — Caddy reverse proxy
- `scripts/systemd/` — systemd unit files
- Hardening plan: see /Users/bokiko/.claude/plans/calm-baking-eclipse.md

## Quick Reference
- Hub: `cd hub && go run .`
- Agent: `cd agent && go run . --hub ws://localhost:4000/ws/agent --token <token>`
- Dashboard: `cd dashboard && pnpm dev`

## Tech Stack
- Go 1.25, Echo, gorilla/websocket, gopsutil, modernc.org/sqlite, creack/pty, golang-jwt, bcrypt
- Next.js 16, React 19, Tailwind v4, shadcn/ui, Framer Motion, Recharts, xterm.js
- Caddy (internal TLS), SQLite WAL (0600), Ubuntu 22.04
