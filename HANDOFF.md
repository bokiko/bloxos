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
  - [x] User management UI (PR #28 backend, #29 dashboard): `/users` page + admin-only nav, role-aware affordances on the index card.
  - [x] API-polled machine detail-page NULL fix (PR #30): `COALESCE` GPU metric columns so Proxmox/Synology cards stop 500-ing.
  - [x] Proxmox API machine moved from `insecure` to `custom_ca` (leaf-pinned cert).
  - [x] Role-aware dashboard UI (PR #32): every admin button gated on `useAuth().hasScope(...)` — Add Machine, Add API, bulk-action bar, list-view selection, card delete/edit, detail-page Delete/Reboot/Terminal tab — backend route map is the source of truth.
  - [x] hub/main.go split (PRs #33–#39): the 4344-line monolith is now `main.go` (2236) + `auth.go` (593) + `alerts.go` (425) + `terminal.go` (470) + `agentws.go` (704). All still `package main` sharing the same globals — pure code moves, zero behavior change.
  - [x] Test flake (PR #40): `TestAgentReconnectWithSecret` / `TestAgentEnrollmentWithToken` fixed by `db.SetMaxOpenConns(1)` in test setup. Root cause was SQLite `:memory:` per-connection isolation (each pool connection got its own empty database, so a concurrent goroutine querying a freshly-opened second connection saw "no such table"), not the goroutine-leak this ledger previously hypothesised. Production unaffected — `bloxos.db` is a real file. 4/4 green CI runs post-fix vs ~50% flake pre-fix.
  - [x] Frontend Phase 1: dual-theme system + Cmd+K command palette
    - dual `:root` (light) + `.dark` token sets in `globals.css`; `--color-blox-*` re-aliased onto semantic tokens so every existing component becomes theme-aware with zero edits
    - `ThemeContext` with system-preference detection + `localStorage` persistence; inline bootstrap script in `layout.tsx` paints the correct class before React hydrates (no FOUC)
    - `ThemeToggle` in header dropdown (light / dark / system)
    - `CommandPalette` (cmdk-based) bound to ⌘K / Ctrl+K with navigate / machines / actions (RBAC-gated) / theme / account groups
    - lazy state initializers + event-handler-based clear avoid React 19 `set-state-in-effect` violations
  - [x] Frontend Phase 2: header refactor + fleet pulse strip
    - `FleetPulse` component below the header — 5-cell glanceable health: Fleet (online/warn/offline ratio bar), Avg CPU (excludes offline), Avg RAM, Max GPU (conditional), Alerts (clickable to open panel) + live SSE indicator
    - severity hint via 2px left accent only on warning/danger cells; healthy cells stay neutral
    - `Add Machine` promoted to filled-blue primary action; `Add API` demoted to ghost variant, hidden on mobile
    - `UserMenu` dropdown consolidates Users link + Logout; shows current role
    - removed standalone wifi icon, alert bell, and inline summary text (folded into FleetPulse)
  - [x] Hardware-info first-connect race fixed: agent now sends hardware snapshot AFTER the first metric (so the `machines` row exists when the hub stores it); hub handler upgraded from plain `UPDATE` to `INSERT … ON CONFLICT(id) DO UPDATE` as defense-in-depth. Diagnosed when AiFarm-01 (WSL2 agent) enrolled fresh and got `hardware_info=NULL` because hub-side `upsertMachine` only ran on first metric arrival, after the snapshot UPDATE had already affected 0 rows. New test `TestHardwareInfoUpsertPreCreatesRow` locks in the defense.
  - [x] Frontend Phase 3: MachineCard redesign
    - `ProgressBar` got an explicit `variant` API (neutral/active/warning/danger); 0% now renders neutral gray instead of green (fixes the misleading "just enrolled = healthy" appearance). Backwards-compatible with detail-page callers.
    - `Sparkline` reads `--accent` from the active theme (CSS-var-driven), so the line color follows light/dark; placeholder is a subtle bottom rule instead of a dashed centered line.
    - `MachineCard` rewritten with proper hierarchy: status accent stripe + pulsing dot + hostname dominant; CPU as big number + inline sparkline; compact RAM/Disk rows; single GPU line only when data exists. Three explicit states (live / awaiting first metrics / offline). Action buttons always visible at 40% opacity, brighten on hover. Adapter mention appears once (lavender uppercase in IP row), duplicate corner pill removed. `whileHover y:-2` spring replaces the old `scale: 1.02`.
    - New `MachineCardSkeleton` export; grid renders 4 shimmer placeholders during initial SSE fetch instead of an empty page.
  - [x] Frontend Phase 4: detail-page polish
    - New `HardwareCard` component (extracted from inline `HardwarePanel`): 4 sections (Compute / Memory & Storage / Network / Platform) with consistent uppercase labels, primary fields, sub-headers; disk type tags (NVME/SSD/HDD), NIC speeds, GPU sub-section in Compute. Honest empty states for missing disks/NICs.
    - Detail page header refactor: top sticky bar shrinks to h-12 (navigation + actions only); hostname becomes the genuine hero (text-xl/2xl semibold, status dot inline) in a new hero section with a `<dl>` of key facts (IP, OS, Latency, ID).
    - New `MetricsChartsSkeleton` for the metrics tab pre-data state — pseudo area-chart silhouettes shimmer in place of charts (theme-aware via `--accent`), pulsing "Collecting data" caption. Uses `useId()` for unique linearGradient ids.
    - Terminal pane refactor: dropped the macOS traffic-light dots; header matches HardwareCard rhythm (icon-tile + title + inline pulsing-dot status); stable 360px body height across all 5 states (no jolts during PIN flow).
    - Page transition tightened (`y: 8 → 0`, `0.25s ease-out`).
  - [x] Frontend Phase 5: final polish pass — **redesign complete**
    - Motion durations migrated to semantic tokens across non-shadcn components (`Toast`, `ProgressBar`, `FleetPulse`); Framer Motion `transition` props normalized to `0.25/0.3/0.4` per `fast/base/slow` buckets. Shadcn `ui/*` primitives intentionally left at vendor defaults to avoid update conflicts.
    - `prefers-reduced-motion` support: `@media` block in `globals.css` collapses transitions and freezes ambient pulses/shimmer when set; critical feedback (focus rings, hover colors) preserved.
    - `/login`, `/setup`, `/users` brought to parity: spinner+text in submit buttons during async, 3 shimmer table rows on `/users` initial fetch, page transitions tightened to `0.25–0.3s ease-out`, `…` ellipsis character throughout.
    - Focus ring layering documented in `globals.css`: shadcn 3px ring on primitives + canonical 2px outline as fallback, both resolving to `--accent`.
    - xterm.js light theme (Solarized Light derivative): `Terminal.tsx` reads `useTheme().resolvedTheme` and swaps palette in-place on theme change without disconnecting the WebSocket; detail-page wrapper bg switched to `var(--surface-base)` for seamless surface match.
    - `--color-blox-*` token aliases documented as **permanent** (no migration to semantic tokens necessary — shims work transparently).
  - [x] Phase 8: automatic agent self-update with rollback safety
    - Agent (`agent/updater.go`): on every connect, hub announces SHA-256 of the binary it's serving via an `agent_version` frame. If SHA differs from running binary, agent downloads `/download/agent` (TLS-pinned via reused `websocketDialerFor`), verifies SHA, atomically renames into place (works on running binary — kernel preserves the running inode), saves prior binary as `.prev`, writes `.bloxos-agent-updated-at` marker, `os.Exit(0)`. systemd's `Restart=always` brings the agent back on the new binary. No `systemctl restart` from inside the agent — avoids self-kill races.
    - Agent reports running SHA back to hub on connect via `agent_running_version` so the hub knows what's actually running.
    - Hub (`hub/agent_versions.go`): SHA cache (recomputed when binary mtime changes), per-machine version tracking, **circuit breaker** (2 failed reconnects within 5 min auto-pauses the rollout), reconnect-monitor goroutine watches for agents that don't come back within 90s of the announce. New endpoints: `GET /api/versions` (`fleet.read`), `POST /api/versions/pause` + `/resume` (`fleet.admin`).
    - Recovery infrastructure (written by `install.sh`): `bloxos-agent.service` gets `OnFailure=bloxos-agent-recover.service` + `StartLimitBurst=3`/`StartLimitInterval=60`. Recovery script restores `.prev` if `.bloxos-agent-updated-at` is recent (<10 min), saves the failed binary as `.failed.<ts>`, logs to `/var/log/bloxos-agent-rollback.log`, restarts the agent.
    - Dashboard: new `VersionsContext` polls `/api/versions` every 60s. `MachineCard` footer shows amber "update pending" badge when out-of-date. New `/versions` page with hub binary card, per-agent status table, and admin-only Pause/Resume rollout controls. Cmd+K palette gets an "Agent versions" entry.
    - Backwards-compatible: pre-Phase-8 agents on old binaries don't send `agent_running_version` (hub just won't show them on `/versions`) and ignore the `agent_version` frame they don't understand. Once they reinstall via the latest `install.sh`, they pick up the recovery infrastructure and the auto-update path takes over for all subsequent deploys.
    - **One final manual rollout** is required to install the recovery infrastructure (recovery unit + script + `OnFailure=`) on existing fleet machines; after that, every future agent deploy is zero-touch.
  - [x] Phase 7: persistent last-known state + live freshness timers + refresh endpoints
    - localStorage cache (`dashboard/src/lib/metrics-cache.ts`) — versioned schema, user-keyed (avoids cross-user leak on shared browsers), debounced writes (every ~2s max), 7-day TTL, quota-exceeded handling. Cache flushes on unmount, clears on logout.
    - `SSEContext` hydrates from cache **before** first paint — cards render instantly with last-known values on reload. "Awaiting first metrics…" now only fires for never-before-seen machines.
    - `LiveTimeSince` component in `MachineCard` — owns its own 1Hz interval so the footer "12s ago" advances live without parent re-render. Dropped the previous parent-level `forceTick` interval (cards now only re-render on actual prop change — perf win at 30+ machines).
    - Per-card `RefreshButton` — click triggers a `POST /api/machines/:id/refresh`; the hub forwards a `refresh_metrics` command to the agent over the existing WS, agent runs `sendAll()` immediately, fresh metrics arrive within ~1s via the existing SSE stream. Hidden on offline machines.
    - `GlobalRefreshButton` in fleet header — same flow but broadcast to every connected agent via `POST /api/refresh`. Both endpoints gated on `fleet.control` (viewer-role users see disabled state).
    - Agent (`agent/main.go`) — added `refresh_metrics` to `allowedCommands` with a special-case branch in `handleCommand` that spawns `sendAll(conn, mu, machineID)` in a goroutine, using captured closure state (no package-level `activeConn` ceremony). Backwards-compatible: agents still on the old binary will reject the command as unknown — UI silently no-ops, no error toast.
  - [x] Phase 6 Unit A: comprehensive hardware inventory collection (backend)
    - Expanded `HardwareInfo` JSON schema (additive, omitempty everywhere — older agents stay forward-compatible): DMI (system/board/BIOS/chassis), per-DIMM RAM modules via `dmidecode --type 17/16`, structured GPU devices via `lspci -mm`, full PCI device list, expanded disks (serial/firmware/interface), CPU sockets/cache (L1/L2/L3 from `/sys/devices/system/cpu`), CPU flags filtered to capability-relevant set (AVX/AVX-512/VMX/SVM/AES…), CPU family+model parsing for Ryzen/Xeon/Core/EPYC/Atom/Apple Silicon.
    - New aggregating endpoint `GET /api/inventory` (RBAC scope `fleet.read`): top-line totals (machine count, total RAM, total storage by type, total CPU cores, GPU count, unique CPU/GPU/motherboard counts), per-machine summary rows, CPU/memory/GPU groups, per-disk and per-NIC rows.
    - Pre-compiled regex package-level vars in agent (one-shot compile, not per-call). `inventoryMaxInt` / `appendUnique` / `dedupedGPUModels` helpers scoped to `inventory.go`.
    - Backwards-compatible: agents on the old binary continue working; new fields backfill as agents redeploy. Hub VM (virtualized) won't expose DMI — fields stay empty and the dashboard degrades gracefully. Bare-metal agents (`ai-04`, `ic-brain`) will populate them on next agent restart.
  - [x] Phase 6 Unit B: `/inventory` dashboard page — closes Phase 6
    - `InventoryProvider` (`dashboard/src/contexts/InventoryContext.tsx`) fetches `/api/inventory` on demand with idle/loading/ready/error states; resets cache on auth change.
    - New `/inventory` route with hero + 5-cell summary strip (Total RAM, Total Cores, Total Storage with NVMe/SSD/HDD breakdown, GPUs, Motherboards) + 6 tabbed views (Machines / CPU / Memory / Storage / GPU / Network) wired to URL via `?view=` for deep-linking.
    - Generic `<InventoryTable>` (sort any column, free-text filter, group-by any column, column-visibility toggle). State resets on view change via parent's `key={activeView}` remount — no view-watching effect needed (avoids React 19 `set-state-in-effect`).
    - `<InventoryExportMenu>` — download CSV / JSON / Markdown plus clipboard-copy Markdown. Filenames include view + ISO date.
    - Header link: `Boxes` icon between Theme and divider. Cmd+K palette: "Hardware inventory" entry under Navigate.
    - Honest empty rendering — fields not yet collected by an agent show `—`. Page works against current data and progressively shows more as Unit A's expanded collection rolls out fleet-wide.
    - Reused the Base UI error #31 fix pattern (wrap `DropdownMenuLabel` in `DropdownMenuGroup`) for the table toolbar dropdowns.
- Remaining:
  - [ ] Roll out new agent binary to `ai-04`, `ic-brain`, `AiFarm-01` (operator step — `curl /download/agent` + `systemctl restart bloxos-agent` on each), then auto-update takes over for all future deploys.

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
- Dashboard hides every admin affordance the role lacks (PR #32). Backend still refuses with 403 if a viewer hand-crafts a write request.

## Hardware Info
- Agent collects a static spec snapshot on every connect: CPU (model/cores/threads/freq), RAM total, kernel, virtualization, boot time, disks (`/sys/block`: device/model/size/type), NICs (`/sys/class/net`: name/IPv4/MAC/speed), GPU model names.
- Hub stores the raw JSON in `machines.hardware_info` (migration v8). `GET /api/machines/:id` returns it as a decoded object.
- Dashboard detail Overview tab shows it in a Hardware panel; absent fields are hidden.

## Known Issues
- (none — flake fixed 2026-04-26 in PR #40)

## hub/ file map (post-split)
- `main.go` — entrypoint, route registration, DB init, metrics ingest, machine REST handlers, SSE, bulk commands, API-machine pollers, rate limiter, log redactor.
- `auth.go` — first-boot setup, login, JWT middleware, credential rotation gate, password/PIN change, SSE token mint, JWT secret loader.
- `alerts.go` — `AlertRule`/`Alert` types, evaluation loop, SSE broadcast, Telegram, alert REST endpoints.
- `terminal.go` — `TerminalSession`, PIN-gated start, agent + browser WS upgrade, relay, cleanup, allowed-origins helper.
- `agentws.go` — `ConnectedAgent`, `handleAgentWS`, install token + script + agent download + CA download, durable agent secret lifecycle, first-run token bootstrap.
- `rbac.go`, `users.go`, `migrations.go` — unchanged, already separated.

## Current Fleet
- Three enrolled agents online: `BloxOs` (hub self), `ai-04`, `ic-brain` — all on the hardware-aware build.
- Two API machines polling: `Dasman` (Synology DSM), `Dell` (Proxmox VE — `custom_ca` since PR #30).
- Recheck live state from the dashboard or hub API before any ops work.

## Key URLs
- Dashboard: `https://192.168.16.113`
- Repo: `https://github.com/bokiko/bloxos`

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
