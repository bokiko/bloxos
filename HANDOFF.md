# BloxOS Handoff Ledger

## State
- Done:
  - [x] Phase 1-5: All features (hub, agent, dashboard, GPU, terminal, alerts, charts, auth)
  - [x] UI Redesign: shadcn/ui + Framer Motion + Geist fonts
  - [x] Codex Security Audit: 3 rounds, 6/8 fixed, 2 accepted
  - [x] Hardening Do Now: creds rotated, DB 0600, token logging stopped, single-use tokens, Caddy TLS, config model, rate limiting
- Now: Stable. Deploying agents to fleet machines.
- Remaining (Do Next Sprint — plan at /Users/bokiko/.claude/plans/calm-baking-eclipse.md):
  - [ ] Migration versioning (before schema changes)
  - [ ] Smoke tests (auth, enrollment, terminal)
  - [ ] Backend-enforce password/PIN rotation
  - [ ] First-boot setup flow (no default creds)
  - [ ] Enrollment redesign (bootstrap → durable hashed agent credential)
  - [ ] Terminal privilege tightening

## Known Issues (2026-04-22)
- **Old agent binary problem:** The install script downloads the agent binary from the hub at `/usr/local/bin/bloxos-agent`. After rebuilding the agent, you MUST run `sudo cp ~/bloxos/agent/bloxos-agent /usr/local/bin/bloxos-agent` on the hub VM, or the install script serves the stale binary.
- **Self-signed TLS:** Agents need `InsecureSkipVerify: true` for Caddy's self-signed cert. This is in the current agent code. Install script uses `curl -k`.
- **IC-Brain sudo:** requires password (123Kdd) piped via `echo 123Kdd | sudo -S`

## Current Fleet
- **bloxOs** (192.168.16.113) — hub VM itself, online
- **ic-brain** (192.168.16.78) — InContext Research, online

## Credentials (rotated 2026-04-22)
- **Dashboard:** https://192.168.16.113 — admin / Bl0x0s!Fleet#2026
- **Terminal PIN:** 8371
- **JWT secret:** ~/.bloxos/jwt-secret (auto-generated)
- **VM SSH:** bokiko / 123Kdd

## Architecture
```
LAN → Caddy (:443 TLS) → Hub (127.0.0.1:4000) + Dashboard (127.0.0.1:3000)
                           ↑
                     Agents (WSS through Caddy)
```

## Service Management
```bash
# All 4 services (auto-start on boot)
systemctl is-active bloxos-hub bloxos-agent bloxos-dashboard caddy

# Restart
sudo systemctl restart bloxos-hub

# Logs
journalctl -u bloxos-hub -f

# Add a machine (from dashboard or CLI)
# 1. Generate token: POST /api/tokens (needs JWT auth)
# 2. On target: export BLOXOS_HUB=wss://192.168.16.113 BLOXOS_TOKEN=<token>; curl -sk https://192.168.16.113/install.sh | sudo -E bash
# 3. IMPORTANT: ensure /usr/local/bin/bloxos-agent on hub VM is the latest build
```

## Key URLs
- Dashboard: https://192.168.16.113
- Hub health: https://192.168.16.113/health
- Repo: https://github.com/bokiko/bloxos (PRIVATE)
- Branch: main

## Working Set
- hub/main.go — ~2700 lines
- agent/main.go — ~800 lines
- dashboard/ — Next.js 16, shadcn/ui, dark mode
- scripts/caddy/Caddyfile — reverse proxy
- Hardening plan: /Users/bokiko/.claude/plans/calm-baking-eclipse.md

## Tech Stack
Go 1.25, Echo, gorilla/websocket, gopsutil, modernc.org/sqlite, creack/pty, golang-jwt, bcrypt
Next.js 16, React 19, Tailwind v4, shadcn/ui, Framer Motion, Recharts, xterm.js
Caddy (internal TLS), SQLite WAL (0600), Ubuntu 22.04
