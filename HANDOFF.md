# BloxOS Handoff Ledger

## State
- Done:
  - [x] Feature baseline: hub, agent, dashboard, GPU metrics, alerts, auth, terminal, API-polled machines
  - [x] UI refresh: shadcn/ui + Framer Motion + Geist fonts
  - [x] Security hardening phase:
    - [x] DB permissions `0600`
    - [x] bootstrap secrets removed from logs
    - [x] single-use install tokens
    - [x] credential rotation enforcement
    - [x] first-boot setup backend
    - [x] durable agent secrets (hashed, revocable)
    - [x] terminal session limits and audit metadata
  - [x] Trust hardening:
    - [x] agent TLS verification enabled by default
    - [x] installer CA bootstrap with fingerprint verification
    - [x] terminal browser token scoped instead of full JWT in URL
    - [x] API-machine TLS trust moved from global env override to per-machine config
  - [x] Product gaps closed:
    - [x] API machine edit flow (`PATCH /api/api-machines/:id`)
    - [x] in-process poller restart after API machine update
    - [x] GitHub Actions CI for hub tests, agent tests, dashboard lint/build
- In progress:
  - [ ] PR `#21` / branch `feat/setup-ui`: frontend `/setup` page for first-boot flow
    - current branch head: `3fcb836`
    - latest fixes included:
      - blocking error state when setup status cannot be fetched
      - explicit message when setup succeeds but auto-login fails
      - login page skips setup probe when already authenticated
      - setup retry button retriggers the setup-status fetch correctly
      - RBAC/action-scope foundation (`admin` / `operator` / `viewer`) with server-side route checks
      - startup route-audit guard so protected `/api/*` routes cannot drift from the RBAC scope map
- Remaining:
  - [ ] merge PR `#21`
  - [ ] deploy `/setup` UI after merge
  - [ ] move Proxmox API machine from temporary `insecure` mode to `custom_ca`
  - [ ] split `hub/main.go` into packages when the repo stabilizes further

## Credentials
All live credentials are stored outside git.
Never put raw secrets, tokens, passwords, PINs, or SSH credentials in this file or any committed file.

## Current Deployment
- Supported deployment path: `Caddy + systemd`
- Topology:
  - Caddy terminates HTTPS on `:443`
  - hub listens on `127.0.0.1:4000`
  - dashboard listens on `127.0.0.1:3000`
  - agents connect through Caddy over `wss://.../ws/agent`
- CI is active in GitHub Actions for:
  - `go test -count=1 ./...` in `hub`
  - `go test -count=1 ./...` in `agent`
  - `pnpm lint` in `dashboard`
  - `pnpm build` in `dashboard`

## First-Boot Setup
- Backend setup flow already exists and is the only supported bootstrap path for new deployments.
- Status endpoint: `GET /api/setup/status`
- Setup endpoint: `POST /api/setup`
- Setup token sources:
  - `~/.bloxos/setup-token` (owner-readable only) when no users exist
  - or `BLOXOS_SETUP_TOKEN`
- PR `#21` adds the frontend `/setup` page on top of this backend flow.

## Agent Enrollment
- Legacy reconnect fallback has been removed.
- Agents must authenticate with durable secrets on reconnect.
- Install tokens are single-use and mint durable agent credentials.
- Durable agent secret is stored locally on the machine and hashed in the hub DB.
- Re-enrollment path for an older or broken agent:
  1. generate a fresh install token
  2. reinstall or re-run the agent bootstrap flow
  3. verify the new durable secret is present locally

## API Machines
- TLS trust is now per machine:
  - `system`
  - `custom_ca`
  - temporary `insecure`
- Existing self-signed HTTPS endpoints must be configured explicitly.
- Current follow-up still needed:
  - move the Proxmox API machine from temporary `insecure` mode to `custom_ca`
- API machines can now be edited from the dashboard; direct SQLite edits should no longer be necessary.

## Known Issues
- PR `#21` is still open and not merged into `main` yet.
- `/setup` UI exists on the feature branch only until PR `#21` lands.
- Proxmox API polling still uses explicit per-machine `insecure` TLS until its CA PEM is configured.
- RBAC is currently backend-enforced; dashboard role-aware affordances are still minimal.

## Current Fleet
- Last verified rollout state:
  - 3 enrolled agents online
  - 2 API machines polling
- Recheck live fleet state from the dashboard or hub API before any ops work.

## Key URLs
- Dashboard: `https://192.168.16.113`
- Repo: `https://github.com/bokiko/bloxos`
- Active feature branch: `feat/setup-ui`
- Open setup PR: `#21`

## Useful Commands
- Hub tests: `cd ~/bloxos/hub && /usr/local/go/bin/go test -count=1 ./...`
- Agent tests: `cd ~/bloxos/agent && /usr/local/go/bin/go test -count=1 ./...`
- Dashboard lint: `cd ~/bloxos/dashboard && pnpm lint`
- Dashboard build: `cd ~/bloxos/dashboard && pnpm build`
- Hub build: `cd ~/bloxos/hub && /usr/local/go/bin/go build -o bloxos-hub .`
- Agent build: `cd ~/bloxos/agent && /usr/local/go/bin/go build -o bloxos-agent .`

## Tech Stack
- Backend: Go 1.25, Echo, gorilla/websocket, modernc.org/sqlite, JWT, bcrypt
- Frontend: Next.js 16, React 19, Tailwind v4, shadcn/ui, Framer Motion, Recharts, xterm.js
- Infra: Caddy, systemd, SQLite WAL
