# BloxOS Handoff Ledger

## State

- Done:
  - [x] Feature baseline: hub, agent, dashboard, GPU metrics, alerts, auth, terminal, API-polled machines
  - [x] UI refresh: shadcn/ui + Framer Motion + Geist fonts
  - [x] Security hardening:
    - [x] DB permissions `0600`
    - [x] bootstrap secrets removed from logs
    - [x] single-use install tokens
    - [x] credential rotation enforcement
    - [x] first-boot setup backend
    - [x] durable agent secrets (hashed, revocable)
    - [x] terminal session limits + audit metadata
  - [x] Trust hardening:
    - [x] agent TLS verification on by default
    - [x] installer CA bootstrap with fingerprint verification
    - [x] terminal browser token scoped instead of full JWT in URL
    - [x] API-machine TLS trust per-machine instead of a hub-wide env override
  - [x] Product gaps closed:
    - [x] API machine edit flow (`PATCH /api/api-machines/:id`)
    - [x] in-process poller restart after API-machine update
    - [x] GitHub Actions CI for hub tests, agent tests, dashboard lint/build
  - [x] First-boot UI (PR #21):
    - [x] dedicated `/setup` page
    - [x] login auto-redirects to `/setup` when initialization is pending
    - [x] auto-login + handoff to dashboard after successful setup
    - [x] blocking error/retry state when setup status fetch fails
    - [x] explicit message when setup succeeds but auto-login fails
    - [x] login skips setup probe when already authenticated
  - [x] RBAC foundation (merged with PR #21):
    - [x] `admin` / `operator` / `viewer` roles + scopes
    - [x] server-side enforcement via `permissionMiddleware`
    - [x] login response returns role + scopes
    - [x] startup route-audit guard so protected `/api/*` routes can never drift from the RBAC scope map
  - [x] UI polish pass (PRs #22-#25):
    - [x] Phase 1: design tokens + motion vars + scale conventions in `globals.css`
    - [x] Phase 2: card + list correctness — empty GPU rows removed on CPU-only machines, list view matches card data, `auto-rows-fr` keeps card heights equal across a row
    - [x] Phase 3: machine detail page restructured into shadcn Tabs (Overview / Services / Containers / Metrics / Terminal) with `?tab=X` deep-linking and `keepMounted` on Terminal so the PTY survives tab switches
    - [x] Phase 4: canonical `:focus-visible` outline ring, inline SSE-disconnect banner, smallest-text bump
  - [x] Bug fix (PR #26): `localeCompare` crash from machines with null hostname
  - [x] Hardware specs (PR #27):
    - [x] agent emits a one-time `hardware_info` snapshot on connect (CPU model + cores, RAM, kernel, virtualization, disks with type/size, NICs with speed, GPU model)
    - [x] hub stores raw JSON in new `machines.hardware_info` column (migration v8)
    - [x] dashboard renders a Hardware panel in the detail Overview tab
- In progress:
  - [ ] User management dashboard UI — backend already on branch `feat/user-management` (commit `4e2f09e`, unmerged): `POST/GET/PATCH/DELETE /api/users` behind `users.admin` scope. Frontend `/users` page + admin-only nav still TODO.
- Remaining:
  - [ ] move Proxmox API machine from temporary `insecure` mode to `custom_ca`
  - [ ] split `hub/main.go` into packages when the repo stabilizes further
  - [ ] cross-test contamination flake on `TestAgentReconnectWithSecret` (passes 3/3 in isolation, fails ~half the full-suite runs — global `db` var + leaked goroutines)

## Credentials
All live credentials live outside git in `~/.bloxos/`-style paths or operator memory. **Never** put raw secrets, tokens, passwords, PINs, or SSH credentials in this file or any committed file.

## Current Deployment
- Path: `Caddy + systemd` on the BloxOS hub VM (`192.168.16.113`).
- Topology:
  - Caddy terminates HTTPS on `:443`
  - hub listens on `127.0.0.1:4000`
  - dashboard listens on `127.0.0.1:3000`
  - agents connect through Caddy over `wss://.../ws/agent`
- The single canonical clone on the hub VM is `~/bloxos` (which the systemd units point at). The duplicate `~/projects/bloxos` clone has been removed.
- CI runs in GitHub Actions on every push:
  - `go test -count=1 ./...` in `hub`
  - `go test -count=1 ./...` in `agent`
  - `pnpm lint` in `dashboard`
  - `pnpm build` in `dashboard`

## First-Boot Setup
- Backend: `GET /api/setup/status`, `POST /api/setup`.
- Token sources: `~/.bloxos/setup-token` (owner-readable only) when no users exist, or `BLOXOS_SETUP_TOKEN` env.
- UI: `/setup` page (PR #21) walks the operator through token → admin username → password → terminal PIN, then auto-logs in.

## Agent Enrollment
- Legacy reconnect fallback removed; agents must authenticate with durable secrets.
- Install tokens are single-use and mint durable agent credentials.
- Durable secret stored locally on the machine, hashed in the hub DB.
- Re-enrollment for a broken agent: generate fresh install token → reinstall/re-bootstrap → verify new durable secret on disk.

## API Machines
- TLS trust is per machine: `system`, `custom_ca`, or temporary `insecure`.
- Self-signed HTTPS endpoints must be configured explicitly.
- Outstanding follow-up: convert the Proxmox API machine from `insecure` to `custom_ca`.

## RBAC
- Roles: `admin`, `operator`, `viewer`.
- Login response returns `role` + `scopes` for client-side gating.
- Server enforces every protected route via `permissionMiddleware` against `routeScopeRequirements`.
- A boot-time `auditRBACRouteCoverage` check fails server startup if any registered `/api/*` route lacks a scope mapping (or vice-versa).
- Dashboard role-aware affordances still minimal — backend will refuse insufficient-scope writes with 403.

## Hardware Info
- Agent collects a static spec snapshot on every connect: CPU (model/cores/threads/freq), RAM total, kernel, virtualization, boot time, disks (`/sys/block`: device/model/size/type), NICs (`/sys/class/net`: name/IPv4/MAC/speed), GPU model names.
- Hub stores the raw JSON in `machines.hardware_info` (migration v8). `GET /api/machines/:id` returns it as a decoded object.
- Dashboard detail Overview tab shows it in a Hardware panel; absent fields are hidden.

## Known Issues
- Proxmox API polling still uses `insecure` until its CA PEM is configured.
- Dashboard role-aware affordances are minimal (server enforces, UI doesn't yet hide).
- Pre-existing flaky test `TestAgentReconnectWithSecret` on full-suite runs.

## Current Fleet
- Three enrolled agents online: `BloxOs` (hub self), `ai-04`, `ic-brain` — all on the hardware-aware build.
- Two API machines polling: `Dasman` (Synology DSM), `Dell` (Proxmox VE — still in `insecure` mode).
- Recheck live state from the dashboard or hub API before any ops work.

## Key URLs
- Dashboard: `https://192.168.16.113`
- Repo: `https://github.com/bokiko/bloxos`
- Open backend branch (unmerged): `feat/user-management` at `4e2f09e`

## Useful Commands
- Hub tests: `cd ~/bloxos/hub && /usr/local/go/bin/go test -count=1 ./...`
- Agent tests: `cd ~/bloxos/agent && /usr/local/go/bin/go test -count=1 ./...`
- Dashboard lint: `cd ~/bloxos/dashboard && pnpm lint`
- Dashboard build: `cd ~/bloxos/dashboard && pnpm build`
- Hub build: `cd ~/bloxos/hub && /usr/local/go/bin/go build -o bloxos-hub .`
- Agent build: `cd ~/bloxos/agent && /usr/local/go/bin/go build -o bloxos-agent .`
- Restart all on hub VM: `sudo systemctl restart bloxos-hub bloxos-dashboard bloxos-agent`

## Tech Stack
- Backend: Go 1.25, Echo, gorilla/websocket, modernc.org/sqlite, JWT, bcrypt
- Frontend: Next.js 16, React 19, Tailwind v4, shadcn/ui (Base UI primitives), Framer Motion, Recharts, xterm.js
- Infra: Caddy, systemd, SQLite WAL
