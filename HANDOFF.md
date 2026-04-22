# BloxOS Handoff Ledger

## State
- Done:
  - [x] Phase 1-5: All features (hub, agent, dashboard, GPU, terminal, alerts, charts, auth)
  - [x] UI Redesign: shadcn/ui + Framer Motion + Geist fonts
  - [x] Codex Security Audit: 3 rounds, 6/8 fixed, 2 accepted
  - [x] Hardening Do Now: creds rotated, DB 0600, token logging stopped, single-use tokens, Caddy TLS, config model, rate limiting
  - [x] Hardening Do Next Sprint (all 6 items complete, 2026-04-22):
    - [x] #8 Migration versioning (hub/migrations.go — schema_version table, 4 migrations)
    - [x] #9 Smoke tests (33 tests in hub/main_test.go, all passing)
    - [x] #10 Backend-enforce password/PIN rotation (credentialRotationMiddleware, allowlist-based)
    - [x] #11 First-boot setup flow (POST /api/setup, bootstrap token, no default creds)
    - [x] #12 Enrollment redesign (durable agent secrets, SHA-256 hashed, revocable)
    - [x] #13 Terminal privilege tightening (30min inactivity timeout, max 3 sessions, audit logging)
- Now: Stable. All hardening complete. Service restart needed to activate changes.
- Remaining (deferred items from hardening plan):
  - [ ] Split hub/main.go into packages (do at ~4000 LOC or second contributor)
  - [ ] Separate terminal-gateway service
  - [ ] Full test suite beyond smoke tests
  - [ ] Dashboard setup page (/setup) — frontend for first-boot flow

## Deployment Notes (IMPORTANT)
The hardening sprint code is committed and pushed but NOT deployed. To activate:
1. Stop services: 
2. Rebuild hub: 
3. Copy hub binary: 
4. Rebuild agent: 
5. Copy agent binary: 
6. Restart: 
7. On first start with new code: schema migrations run automatically (0→4)
8. Existing admin user works but will hit rotation enforcement — change password and PIN via API

### First-boot setup (new deployments only)
- Hub writes setup token to  (0600) if no users exist
-  returns 
-  with  creates admin
- Dashboard needs a setup page at  (not built yet)

### Agent enrollment (re-enrollment for existing agents)
- Existing agents continue working via machine_id recognition (backward compatible)
- New agents get a durable secret on enrollment (stored at /etc/bloxos/agent-secret)
- To re-enroll existing agents: delete from machines table, generate new install token

## Known Issues
- **Old agent binary:** After rebuilding agent, MUST copy to /usr/local/bin/bloxos-agent on hub
- **Self-signed TLS:** Agents need InsecureSkipVerify for Caddy's self-signed cert
- **IC-Brain sudo:** requires password (123Kdd) piped via 
- **Dashboard setup page:** Not built yet — first-boot setup works via API only

## Current Fleet
- **bloxOs** (192.168.16.113) — hub VM, online
- **ic-brain** (192.168.16.78) — InContext Research, online

## Credentials (rotated 2026-04-22)
- **Dashboard:** https://192.168.16.113 — admin / Bl0x0s!Fleet#2026
- **Terminal PIN:** 8371
- **JWT secret:** ~/.bloxos/jwt-secret (auto-generated)
- **VM SSH:** bokiko / 123Kdd

## Architecture


## Key URLs
- Dashboard: https://192.168.16.113
- Repo: https://github.com/bokiko/bloxos (PRIVATE)
- Branch: main
- Hardening plan: /Users/bokiko/.claude/plans/calm-baking-eclipse.md

## Tech Stack
Go 1.25, Echo, gorilla/websocket, gopsutil, modernc.org/sqlite, creack/pty, golang-jwt, bcrypt
Next.js 16, React 19, Tailwind v4, shadcn/ui, Framer Motion, Recharts, xterm.js
Caddy (internal TLS), SQLite WAL (0600), Ubuntu 22.04
