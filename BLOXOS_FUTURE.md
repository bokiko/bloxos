# BloxOS Future — Hub Architecture Refactor

**Status:** Draft for approval
**Date:** 2026-05-02
**Author:** brainstorm session (bokiko + Claude)
**Branch (proposed):** Each PR on its own `refactor/<name>` branch off `main`
**Scope budget:** ~1 working week, 12 PRs

---

## Goal

Convert the BloxOS hub from a 14-file `package main` codebase with ~12 mutable globals into 11 properly-bounded internal packages with dependency injection, repository-pattern storage, and structured logging. Zero behavior change for end users. Sets the foundation for product-depth (A) and automation (C) work to land cleanly.

## Non-Goals

- **No new features.** Anything that adds behavior is out of scope; if a PR is tempted, it's the wrong PR.
- **No frontend work.** The 7-context consolidation gets its own design later. This refactor is hub-only.
- **No agent code reorganization.** Agent's `package main` stays — its build-tag split (Linux/Windows) is the only structural cut it currently needs.
- **No DB schema changes.** Migrations stay where they are (logically); only the runner relocates.
- **No protocol changes.** Agent WS frames, install token shape, install scripts — all unchanged.
- **No HA/multi-instance work.** That's a future product call; this PR makes it *possible* but doesn't deliver it.

---

## Final package layout (11 packages + cmd)

```
hub/
  cmd/hub/main.go                  # ~50 LOC entrypoint: load config, build server, run
  internal/
    config/                        # typed Config struct, env loader, slog setup
      config.go
    store/                         # SQLite + repos + migrations
      store.go                     # Store wraps *sql.DB, exposes repo accessors
      machines.go                  # MachineRepo
      users.go                     # UserRepo
      alerts.go                    # AlertRepo + AlertHistoryRepo (room for A's history feature)
      branding.go                  # BrandingRepo
      preferences.go               # PreferencesRepo (incl. pinned, saved filters)
      inventory.go                 # raw row reads (aggregation lives in inventory/)
      versions.go                  # per-machine version tracking
      terminal.go                  # terminal session audit metadata
      migrations.go                # migration runner + embedded SQL
    sse/                           # Broadcaster (no domain knowledge)
      broadcaster.go
    auth/                          # JWT, login, RBAC, middleware, route audit
      jwt.go
      login.go
      rbac.go
      middleware.go
      audit.go                     # boot-time route-coverage check
    metrics/                       # ingest + history queries
      ingest.go
      history.go
    branding/                      # branding service (logo/favicon/title/welcome)
      service.go
    inventory/                     # aggregator (computes /api/inventory response)
      aggregator.go
    apipoll/                       # external machine pollers (Synology, Proxmox, ...)
      registry.go
      poller.go
      tls.go
    fleet/                         # agent registry + connection + enrolment + versions + agent WS
      manager.go                   # agents map + mutex + lookup
      connection.go                # ConnectedAgent
      enrol.go                     # install tokens, durable secrets, first-run bootstrap
      versions.go                  # SHA tracking, circuit breaker, announce-on-reconnect
      ws.go                        # handleAgentWS
    terminal/                      # PTY sessions
      session.go
      ws.go                        # browser-side WS upgrade + relay
    alerts/                        # rule engine + Telegram notifier
      engine.go
      telegram.go
    server/                        # Server struct + route wiring + HTTP handlers as methods
      server.go                    # New(*Config, *Store, ...) (*Server, error); Run()
      routes.go                    # all route registration in one place
      machines.go                  # handler methods
      users.go
      auth.go                      # handler shims around internal/auth
      alerts.go
      branding.go
      preferences.go
      inventory.go
      versions.go
      refresh.go
      terminal.go
      apimachines.go
      httperr.go                   # one error-shape helper
```

**Naming convention:** every internal package exports a single primary type matching the package noun (`store.Store`, `auth.Service`, `fleet.Manager`, `sse.Broadcaster`, `alerts.Engine`, `metrics.Service`, `apipoll.Registry`, `terminal.Manager`, `branding.Service`, `inventory.Aggregator`). Repos under `store` are `MachineRepo`, `UserRepo`, etc. — accessed via `s.Store.Machines()`, `s.Store.Users()`.

---

## PR sequence (12 PRs)

Each PR has: **Goal** (one line), **Mechanics** (concrete moves), **Verification** (the test bar to clear), **Out of scope** (drift guard).

### PR 0 — Introduce `Server` struct in `package main`
**Branch:** `refactor/00-server-struct`
**LOC touched:** ~3000 (every handler + every test)
**Risk:** Highest. Pure mechanical transformation, no extraction.

> ⚠️ **PARTIALLY SUPERSEDED by #148 — do not start this from scratch, and do
> not treat it as finished either.**
>
> #148 landed a deliberately **scoped subset** of this PR, sized to what
> issue #60 actually required rather than the full transformation below:
>
> - `Server` struct exists in `package main` (`hub/server.go`)
> - **only** `db`, `agents`, and `agentsMu` moved onto it
> - most handlers became methods on `*Server`
> - `goTracked` / `Shutdown` added, so a server waits for work it spawned
>
> **Still outstanding:** most package-level mutable state is untouched —
> `sseClients`, `pendingCmds`, `termSessions`, `jwtSecret`, `rateLimiter`,
> `setupTokenValue`, `machineLatency`, `apiPollers`, the telegram config, and
> the rollout/version caches all remain package-level. `newServer` and
> `registerRoutes` exist in narrower forms than described below, and no `Run`
> method exists.
>
> The remaining scope should be reassessed against the current code before any
> of it is implemented — the description below predates #148 and no longer
> describes the starting point.

**Goal:** Move every package-level mutable global into `*Server` fields. Convert every handler to a method on `*Server`. `main()` shrinks to: build config, build server, register routes, listen.

**Mechanics:**
- Define `type Server struct { db *sql.DB; agents map[string]*ConnectedAgent; agentsMu sync.RWMutex; sseClients ...; pendingCmds ...; termSessions ...; telegramToken string; telegramChatID string; machineLatency ...; jwtSecret []byte; rateLimiter *RateLimiter; apiPollers ...; setupTokenValue string; agentVersionInfo ...; ... }`
- `func newServer() (*Server, error)` does what `main()` currently does up to route registration
- `func (s *Server) registerRoutes(e *echo.Echo)` does route wiring
- `func (s *Server) Run() error` starts listening
- Every handler `handleX(c echo.Context) error` becomes `func (s *Server) handleX(c echo.Context) error`
- Every test in `main_test.go` updated to use `s := newTestServer(t)` (helper added) instead of touching globals
- All package-level `var` declarations of mutable state deleted
- Read-only vars (regexes, valid-value maps) **stay** package-level — they're constants, not state

**Verification:**
- `go build ./...` clean
- `go test -count=1 ./...` green (same test count as before; no tests added or removed)
- `go vet ./...` clean
- Manual smoke: hub starts, agent reconnects, dashboard loads, terminal opens, metrics flow

**Out of scope:** No package extraction. No file moves. No new files except `server.go` extracted from current `main.go`. No behavior change.

---

### PR 1 — `internal/config`
**Branch:** `refactor/01-config`
**LOC touched:** ~200
**Risk:** Low

**Goal:** All config (env vars, file paths, ports, CA path, JWT secret path, DB path, Telegram creds) flows through one typed struct loaded once.

**Mechanics:**
- `internal/config/config.go`: `type Config struct { ... }; func Load() (*Config, error)`
- `Server` gets `cfg *config.Config` field, populated in `newServer`
- Replace every `os.Getenv("BLOXOS_*")` site in hub code with `s.cfg.X`
- `slog` setup lives here: `cfg.Logger *slog.Logger`, JSON handler in production / text in dev
- `Server` gets `log *slog.Logger` field; replace top-priority `log.Printf` calls (errors, lifecycle) with structured logging. Remaining `log.Printf` calls migrate opportunistically in later PRs.

**Verification:**
- All existing env-var contracts preserved (verified by reading current code, listing every `os.Getenv`, mapping to `Config` field, listing in PR description)
- Tests pass with no env changes required
- New unit tests for `Load()`: defaults, override, missing-required-fail

**Out of scope:** No CLI flags (env-var-only stays the contract). No config file (.env loading stays a deployment concern, not a code concern).

---

### PR 2 — `internal/store`
**Branch:** `refactor/02-store`
**LOC touched:** ~1500
**Risk:** Medium (every SQL query relocates)

**Goal:** All `db.Query` / `db.Exec` calls move behind repo interfaces. `Server` no longer holds `*sql.DB` directly — only `*store.Store`.

**Mechanics:**
- `store.Store` wraps `*sql.DB`, exposes `Machines() MachineRepo`, `Users() UserRepo`, etc.
- Each repo is an interface + struct impl: `type MachineRepo interface { Get(ctx, id) (*Machine, error); List(ctx, filter) ([]*Machine, error); ... }; type machineRepo struct { db *sql.DB }`
- Migrations runner: `store.Migrate(ctx, db) error`. Migration SQL embedded via `//go:embed migrations/*.sql` (a small refactor of current in-memory slice — keeps current structure, just relocated).
- Every handler that today calls `s.db.Query(...)` becomes `s.store.Machines().List(ctx, ...)` etc.
- Test helper `newTestServer` builds `Store` with `:memory:` DB; **`SetMaxOpenConns(1)` workaround stays for now** (real fix comes when tests stop sharing one DB instance — that's PR 12)
- Per **migration strategy (x)**: storage-layer tests move into `internal/store/*_test.go` as unit tests using the repo interfaces. The integration tests in `main_test.go` that exercise storage indirectly via HTTP keep passing unchanged.

**Verification:**
- All SQL accounted for: PR description includes a checklist mapping every removed `db.X` call site to its new repo location
- Repo interfaces have unit tests covering happy path + at least one error path each
- Integration tests still green

**Out of scope:** No new tables. No schema changes. No new query patterns. Repo method signatures mirror current call patterns 1:1 (refactoring those is a later concern).

---

### PR 3 — `internal/sse`
**Branch:** `refactor/03-sse`
**LOC touched:** ~150
**Risk:** Low

**Goal:** SSE broadcasting becomes a small package consumed by anything that pushes events.

**Mechanics:**
- `sse.Broadcaster` owns `map[chan []byte]struct{}` + mutex + `Subscribe`/`Unsubscribe`/`Broadcast`
- `Server.sseClients` field replaced with `Server.sse *sse.Broadcaster`
- Handler that mints SSE clients (`/api/events`) gets `s.sse.Subscribe(...)` / defer `Unsubscribe`
- Every site that today writes to `sseClients` gets `s.sse.Broadcast(payload)`
- New unit tests: subscribe/broadcast/unsubscribe, slow-consumer dropping (verify current behavior, document it)

**Out of scope:** No event-shape changes. No routing/filtering of events (every subscriber still gets every event — current behavior preserved).

---

### PR 4 — `internal/auth`
**Branch:** `refactor/04-auth`
**LOC touched:** ~900
**Risk:** Medium (JWT + RBAC are security-critical)

**Goal:** All authentication + authorization logic in one package, exposing a small surface to `server`.

**Mechanics:**
- `auth.Service` owns: JWT secret, login flow (incl. credential rotation gate), password/PIN change, SSE token mint, first-boot setup token validation
- `auth.Middleware(scope string) echo.MiddlewareFunc` replaces `permissionMiddleware`
- `auth.RouteAudit(routes []RouteEntry) error` replaces `auditRBACRouteCoverage` — runs at boot, fails server start if mismatched
- RBAC scope map (`routeScopeRequirements`) stays in `auth.go` next to `Middleware`
- Tests for JWT round-trip, scope enforcement, route-audit failure modes move into `internal/auth/*_test.go`

**Verification:**
- Boot-time audit still catches a deliberately-misregistered route (test added)
- Token refresh / rotation flow unchanged for existing clients (manual smoke from dashboard)
- Existing auth integration tests in `main_test.go` still green

**Out of scope:** No new auth methods. No 2FA. No session DB. No per-user audit log changes (that's an A item).

---

### PR 5 — `internal/metrics`
**Branch:** `refactor/05-metrics`
**LOC touched:** ~400
**Risk:** Low

**Goal:** Metrics ingest from agent WS frames + history queries for the detail-page charts live in one place.

**Mechanics:**
- `metrics.Service` exposes `Ingest(ctx, machineID string, m Metrics) error` and `History(ctx, machineID string, since time.Time) ([]Point, error)`
- `Ingest` writes via `s.store.Metrics()` and broadcasts via `s.sse.Broadcast(...)`
- The `nullableFloat` helper for `cpu_temp_c` (and any future nullable metric) lives here
- Latency tracking (current `machineLatency` global) moves here too — it's a per-machine rolling metric
- Tests: ingest happy path, nullable handling, latency aggregation

**Out of scope:** No retention. No rollups. No new metric types. (All A territory.)

---

### PR 6 — `internal/branding`
**Branch:** `refactor/06-branding`
**LOC touched:** ~260
**Risk:** Low

**Goal:** Branding domain logic (PNG validation, SHA cache-busting, single-row config CRUD) in its own package.

**Mechanics:**
- `branding.Service` exposes `Get(ctx) (*Config, error)`, `Update(ctx, patch) error`, `SetLogo(ctx, png []byte) error`, etc.
- PNG magic-bytes + decode validation logic lives here
- SHA computation for cache-busting stays here
- `Server` handlers become thin shims over the service

**Out of scope:** No new branding fields. Welcome message stays at 500 chars.

---

### PR 7 — `internal/inventory`
**Branch:** `refactor/07-inventory`
**LOC touched:** ~430
**Risk:** Low

**Goal:** Inventory aggregator (the logic that produces `/api/inventory` from per-machine `hardware_info` JSON) lives separately from raw storage.

**Mechanics:**
- `inventory.Aggregator` exposes `Build(ctx) (*InventoryResponse, error)` — reads via `s.store.Inventory()`, computes totals + groupings + deduped lists
- All the helpers (`appendUnique`, `dedupedGPUModels`, `inventoryMaxInt`) move here
- Pre-compiled regex package-level vars stay (they're constants)

**Out of scope:** No new inventory dimensions. No caching of the aggregated response.

---

### PR 8 — `internal/apipoll`
**Branch:** `refactor/08-apipoll`
**LOC touched:** ~500
**Risk:** Low

**Goal:** Polled-machine registry + per-machine pollers + TLS-mode parsing in one package, ready for B-style ecosystem expansion later.

**Mechanics:**
- `apipoll.Registry` owns `map[string]*Poller` + mutex, exposes `Add`, `Update`, `Remove`, `Restart`
- `apipoll.Poller` interface — current Synology + Proxmox impls become first concrete types behind it
- TLS config parsing (`parseAPITLSConfig`, `validateAPITLSConfig`) lives here
- `Server` triggers `s.apiPollers.Restart(machineID)` after a `PATCH /api/api-machines/:id` instead of the current in-process callback dance

**Verification:**
- Existing pollers behaviorally unchanged (Proxmox custom_ca, Synology system trust)
- Add+Restart cycle exercised by test using a stub Poller

**Out of scope:** No new poller types (TrueNAS, OPNsense, Unifi etc. — that's B work).

---

### PR 9 — `internal/fleet`
**Branch:** `refactor/09-fleet`
**LOC touched:** ~1400
**Risk:** **High** — touches WebSocket protocol, durable enrolment, version handshake, refresh commands

**Goal:** Agent-side everything. The single biggest extraction.

**Mechanics:**
- `fleet.Manager` owns `agents` map + mutex + `pendingCmds` map + mutex
- `fleet.ConnectedAgent` is the per-agent struct
- `fleet.Enroller` handles install token mint, durable secret rotation, first-run bootstrap
- `fleet.VersionTracker` owns SHA cache + per-OS announce + circuit breaker + reconnect monitor goroutine
- `fleet.HandleAgentWS` is the WebSocket upgrade entry point — depends on `auth.Service` (token validation), `metrics.Service` (ingest), `sse.Broadcaster` (UI events), `store.Versions()` (record running SHA)
- The `registerAgentConnection` helper extracted in Phase 8 stays — moves into this package
- All the Phase 9 per-OS routing logic (`announcedSHAFor`, `lookupAgentOS`, etc.) moves here

**Verification:**
- Live agents must reconnect after restart with zero log noise
- Phase 8 announce-on-reconnect test (`TestAgentVersionAnnouncedOnReconnect`) passes from new location
- Phase 9 per-OS SHA tests pass from new location
- Full hub test suite green

**Out of scope:** No protocol changes. No new agent commands. No new enrolment paths.

---

### PR 10 — `internal/terminal`
**Branch:** `refactor/10-terminal`
**LOC touched:** ~470
**Risk:** Medium (WS upgrade + PTY relay)

**Goal:** PTY session lifecycle, PIN-gated start, audit metadata in one package.

**Mechanics:**
- `terminal.Manager` owns `termSessions` map + mutex + `Start`, `Stop`, `Get`
- `terminal.Session` is the per-session struct
- Browser-side WS upgrade + agent-side WS relay live here
- Allowed-origins helper moves here
- Depends on `auth.Service` (PIN re-auth) and `fleet.Manager` (agent connection lookup)

**Verification:**
- Cmd+K → Open Terminal still works on a live agent
- Session limits + cleanup unchanged

**Out of scope:** No new terminal features. Windows terminal still gated off via `platformSupportsTerminal()` in agent code (unchanged).

---

### PR 11 — `internal/alerts`
**Branch:** `refactor/11-alerts`
**LOC touched:** ~425
**Risk:** Low

**Goal:** Rule engine + Telegram notifier with explicit room for the alert-history work that A will land.

**Mechanics:**
- `alerts.Engine` owns the evaluation loop, depends on `metrics.Service` (read latest values), `store.Alerts()` (rules + future history), `sse.Broadcaster` (push to UI), `alerts.Notifier` interface
- `alerts.Telegram` is first concrete `Notifier`
- Alert REST endpoints become handler methods in `server/alerts.go` calling into `alerts.Engine`

**Out of scope:** No alert history (A). No acknowledgment / silencing (A). No new notifier types (A).

---

### PR 12 — Final landing in `internal/server`
**Branch:** `refactor/12-server-finalization`
**LOC touched:** ~300
**Risk:** Low

**Goal:** Move everything still in `package main` into `internal/server`. `cmd/hub/main.go` becomes a ~50-line entrypoint. Drop the `SetMaxOpenConns(1)` test workaround now that the test architecture supports per-test fresh stores.

**Mechanics:**
- `Server` struct relocates to `internal/server/server.go`
- `routes.go` consolidates all route registration in one auditable place (used by `auth.RouteAudit`)
- All remaining handler methods relocate as `internal/server/<domain>.go`
- `httperr.go` introduced: single helper for JSON error responses, mapping `store.ErrNotFound` → 404, validation errors → 400, scope failures → 403
- `cmd/hub/main.go`: load config, build store, run migrations, build server with all dependencies, listen
- `main_test.go` is deleted; all tests now live in `internal/<package>/*_test.go`
- Test helper `newTestServer(t)` lives in `internal/server/testing.go` and produces a fresh `Server` per test (each with its own `:memory:` DB, no shared state). `SetMaxOpenConns(1)` workaround removed.
- Add `hub/hub` (compiled binary) to `.gitignore`; remove from index in this PR

**Verification:**
- `go test -count=1 -race ./...` green (race detector is the real test that globals are gone)
- Per-test parallelism enabled (`t.Parallel()` works in at least one previously-flaky test)
- `cmd/hub/main.go` < 100 LOC
- `find internal -name "*.go" | xargs grep -l "^var [a-z]" | xargs grep -L "= regexp\." | xargs grep -l "= make("` returns zero results (no mutable global maps remaining)

**Out of scope:** No feature work. No new test cases beyond what verifies the structural changes.

---

## Test migration strategy: (x) — migrate as we go

- Each extraction PR moves the tests for that domain into the new package as unit tests using the new abstractions (real or mock, whichever is cheaper)
- Integration coverage in `main_test.go` shrinks PR-by-PR as the surface it tests gets re-tested at the unit level
- `main_test.go` is fully gone by PR 12
- After PR 12: `go test -race ./...` enables real per-test parallelism (today blocked by globals + `SetMaxOpenConns(1)`)

## Logging migration

- PR 1 introduces `*slog.Logger` on `Server`
- High-priority `log.Printf` calls (errors, lifecycle events, security events) get migrated **opportunistically** in the PR that touches them
- Low-priority `log.Printf` debug lines stay until explicitly cleaned up — this isn't a logging refactor, it's an architecture refactor
- Final cleanup of remaining `log.Printf` happens in a follow-up PR after PR 12, not bundled in

## Side issues addressed

- **`hub/hub` binary in git** — addressed in PR 12 (`.gitignore` + `git rm --cached`). Adding it to PR 0 would muddy that PR's "pure refactor" character.
- **Frontend context consolidation (7 contexts)** — explicitly deferred. Gets its own design after this refactor lands.
- **Agent code reorganization** — explicitly deferred.

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| PR 0 introduces a hidden bug because every handler signature changes at once | Manual smoke checklist in PR 0 description: hub boots, dashboard loads, agent reconnects, terminal opens, metrics stream, alert fires, Telegram delivers. CI must pass with the new test setup before merge. |
| PR 9 (fleet) breaks WS protocol invisibly | Stage on a non-prod hub instance with FAT-LOLO + ai-04 first; verify auto-update + version announce + refresh + terminal handoff before merging. |
| Stack of PRs gets stale because user picks up a feature mid-refactor | Each PR is independently mergeable. If A or C work has to land mid-stack, do it in the new structure where extractions are complete and old structure where they aren't. The goal is forward progress, not purity. |
| Hidden global I missed in PR 0 reappears as a nil-pointer at 2am | After PR 12, `go test -race ./...` is the proof. Until then, manual `grep "^var " hub/*.go` after each PR to confirm no new mutable package-level state was introduced. |
| Repository interfaces calcify too early — first feature in A discovers we picked the wrong abstraction | Repo interfaces in PR 2 mirror current call patterns 1:1, no speculative methods. First A feature is allowed to add methods or change shapes — accepted cost of discovering the right abstraction by use, not by guess. |

## Success criteria

- ✓ `cmd/hub/main.go` < 100 LOC
- ✓ Zero mutable package-level vars in `internal/`
- ✓ `go test -race ./...` green
- ✓ At least one previously-serial test runs under `t.Parallel()`
- ✓ `SetMaxOpenConns(1)` workaround removed
- ✓ End users notice nothing
- ✓ `hub/hub` binary out of git
- ✓ Foundation in place: A's alert-history can land as a new repo + service without touching globals; C's scheduler can be a new package consuming `fleet.Manager` + `store` without restructuring anything

## Open questions

- **UNCONFIRMED:** Should structured logging go full-bore in PR 1 (replace every `log.Printf`) or stay opportunistic? Current spec says opportunistic. Could be flipped if you want a clean `slog`-only codebase by PR 12.
- **UNCONFIRMED:** Should we add a `context.Context` propagation pass as part of this refactor? Current code uses `context.Background()` in many places. Spec assumes no — that's a separate concern. Could be folded in if you want.
- **UNCONFIRMED:** Branch naming and PR title prefix — proposing `refactor/NN-name` and conventional-commits `refactor: extract internal/X package` per the project's commit conventions. OK?

---

## Roadmap context (what comes after this refactor)

Per the 360 brainstorm session, the agreed product priority is **F → A → C**.

**F = this document.** Architecture refactor. Foundation work.

**A = Product depth (next).** Build on the new architecture:
- Alert history + acknowledgment + silencing
- Metrics retention + rollups (1m → 1h → 1d aggregation)
- Log aggregation across the fleet (tail journalctl from one place)
- GPU process visibility (nvidia-smi pmon)
- Power/cost view (watt-hours, $/kWh)

**C = Power-user automation (after A).** Programmability layer:
- Scheduled actions ("restart ollama nightly at 4am")
- Fleet-wide command runner with output collection
- Webhook triggers for external integrations
- Recipes (composable action templates)

Items explicitly deferred or paused:
- **B = Ecosystem breadth** (TrueNAS, OPNsense, Unifi, Home Assistant, smart-PDU/UPS pollers) — `apipoll.Poller` interface from PR 8 makes this easy when wanted
- **D = Reliability hardening** (HA hub, backup automation) — depends on F being done
- **E = OSS distribution** (docs, install UX, Docker compose, demo) — separate track
- **Frontend context consolidation** (7 → 3-4 contexts + TanStack Query) — own design after F
- **Agent code reorganization** — not currently needed
- **Unified Add Machine chooser** (per saved plan at `thoughts/shared/plans/2026-04-27-unified-add-machine-chooser.md` on hub VM) — paused per bokiko, "save it until I tell you when to implement it"
- **Live Windows enrolment verification** (Phase 9) — ops task, blocked on a live Windows host
- **Re-add Dasman/Dell API machines** — ops task, blocked on credential rotation
- **Decide on Ai-05 + dont-know offline WSL machines** — ops decision

---

*Authored 2026-05-02 from a `/brainstorm` session covering: 360 project review → priority pick (F→A→C) → architecture-debt scope (β) → package layout (B-modified) → migration strategy (ii one-package-at-a-time) → test strategy (x migrate-as-we-go).*
