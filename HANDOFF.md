# BloxOS Handoff Ledger

## State
- Done:
  - [x] Phase 1-5: All features (hub, agent, dashboard, GPU, terminal, alerts, charts, auth)
  - [x] UI Redesign: shadcn/ui + Framer Motion + Geist fonts
  - [x] Codex Security Audit: 3 rounds, 6/8 fixed, 2 accepted
  - [x] Hardening Do Now: creds rotated, DB 0600, token logging stopped, single-use tokens, Caddy TLS, config model, rate limiting
  - [x] Hardening Do Next Sprint (all 6 items, 2026-04-22):
    - [x] #8 Migration versioning (hub/migrations.go, schema_version table, 4 migrations)
    - [x] #9 Smoke tests (33 tests in hub/main_test.go)
    - [x] #10 Backend-enforce password/PIN rotation (credentialRotationMiddleware)
    - [x] #11 First-boot setup flow (POST /api/setup, bootstrap token, no default creds)
    - [x] #12 Enrollment redesign (durable agent secrets, SHA-256 hashed, revocable)
    - [x] #13 Terminal privilege tightening (30min timeout, max 3 sessions, audit logging)
- Now: Code committed and pushed. Service restart needed to activate. Tests green.
- Remaining:
  - [ ] Split hub/main.go into packages (do at ~4000 LOC or second contributor)
  - [ ] Separate terminal-gateway service
  - [ ] Full test suite beyond smoke tests
  - [ ] Dashboard setup page (/setup) -- frontend for first-boot flow

## Credentials
All credentials are in memory/credentials.md on the Mac Mini.
NEVER put secrets in this file or any committed file.

## Deployment Notes
The hardening sprint code is committed and pushed but NOT yet deployed.
To activate on the hub VM:

1. SSH to hub (creds in memory/credentials.md)
2. Stop services: `sudo systemctl stop bloxos-hub bloxos-agent`
3. Rebuild hub: `cd ~/bloxos/hub && /usr/local/go/bin/go build -o bloxos-hub .`
4. Copy hub binary: `sudo cp ~/bloxos/hub/bloxos-hub /usr/local/bin/bloxos-hub`
5. Rebuild agent: `cd ~/bloxos/agent && /usr/local/go/bin/go build -o bloxos-agent .`
6. Copy agent binary: `sudo cp ~/bloxos/agent/bloxos-agent /usr/local/bin/bloxos-agent`
7. Restart: `sudo systemctl start bloxos-hub bloxos-agent`
8. On first start: schema migrations run automatically (version 0 to 4)
9. Existing admin user will hit rotation enforcement -- change password and PIN via API

### First-boot setup (new deployments only)
- Hub writes setup token to `~/.bloxos/setup-token` (0600) if no users exist
- Or set `BLOXOS_SETUP_TOKEN` env var
- `GET /api/setup/status` returns `{"needs_setup": true/false}`
- `POST /api/setup` with `{"setup_token":"...","username":"...","password":"...","pin":"..."}` creates admin
- Dashboard needs a setup page (not built yet -- API-only for now)

### Agent enrollment (existing agents)
- Existing agents continue working via machine_id recognition (backward compatible)
- New agents receive a durable secret on enrollment (stored at /etc/bloxos/agent-secret)
- To re-enroll: delete machine from DB, generate new install token, re-run install script

## Known Issues
- After rebuilding agent, MUST copy binary to /usr/local/bin/bloxos-agent on hub
- Self-signed TLS: agents need InsecureSkipVerify for Caddy's cert
- Dashboard setup page not built yet -- first-boot setup is API-only
- Credentials were previously committed to HANDOFF.md and must be rotated

## Current Fleet
- bloxOs (192.168.16.113) -- hub VM, online
- ic-brain (192.168.16.78) -- InContext Research, online

## Architecture
LAN -> Caddy (:443 TLS) -> Hub (127.0.0.1:4000) + Dashboard (127.0.0.1:3000)
Agents connect via WSS through Caddy to /ws/agent

## Key URLs
- Dashboard: https://192.168.16.113
- Repo: https://github.com/bokiko/bloxos (PRIVATE)
- Branch: main
- Hardening plan: /Users/bokiko/.claude/plans/calm-baking-eclipse.md

## Test Commands
- Run tests: `cd ~/bloxos/hub && /usr/local/go/bin/go test -v -count=1 ./...`
- Build hub: `cd ~/bloxos/hub && /usr/local/go/bin/go build -o bloxos-hub .`
- Build agent: `cd ~/bloxos/agent && /usr/local/go/bin/go build -o bloxos-agent .`

## Tech Stack
Go 1.25, Echo, gorilla/websocket, gopsutil, modernc.org/sqlite, creack/pty, golang-jwt, bcrypt
Next.js 16, React 19, Tailwind v4, shadcn/ui, Framer Motion, Recharts, xterm.js
Caddy (internal TLS), SQLite WAL (0600), Ubuntu 22.04
