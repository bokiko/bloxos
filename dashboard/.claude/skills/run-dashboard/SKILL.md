---
name: run-dashboard
description: Build and run the BloxOS dashboard (Next.js) against a local hub backend, then drive it with a headless-browser script to take screenshots and smoke-test UI primitives (dropdowns, modals, command palette). Use when asked to run, start, launch, or screenshot the dashboard, or to smoke-test it after a dependency bump (especially @base-ui/react) or a UI change.
---

The dashboard is a Next.js app that talks to the Go hub over HTTP + a
WebSocket, and has no meaningful standalone mode — driving it means
building and running the hub too. An agent drives it via
`.claude/skills/run-dashboard/driver.mjs`, a Playwright script with its
own isolated dependency install (does not touch `dashboard/`'s own
`package.json`/`pnpm-lock.yaml`).

All paths below are relative to `dashboard/` (this skill's directory
is `dashboard/.claude/skills/run-dashboard/`).

## Prerequisites

Go and Node/pnpm are already project requirements (see repo root
`CONTRIBUTING.md`). Playwright's Chromium needs its OS deps once:

```bash
cd .claude/skills/run-dashboard
npm install   # installs Playwright into this skill dir only
npx playwright install --with-deps chromium
```

No extra `apt-get` was needed beyond what `playwright install --with-deps`
pulls in itself (fontconfig/freetype/font packages — already present in
this container).

## Build

```bash
mkdir -p /tmp/bloxos-dashboard-smoke/{home,hub-run}
chmod 0700 /tmp/bloxos-dashboard-smoke/home
( cd ../hub && go build -o /tmp/bloxos-dashboard-smoke/hub-run/bloxos-hub . )
```

The hub binary is built to a scratch dir on purpose — see Gotchas
(relative-path SQLite file).

## Run (agent path)

Three steps: launch the hub, launch the dashboard dev server, run the
driver. Each is a background process; the driver assumes both are
already up.

```bash
# 1. Hub — from a scratch working directory, NOT hub/ (see Gotchas)
cd /tmp/bloxos-dashboard-smoke/hub-run
HOME=/tmp/bloxos-dashboard-smoke/home \
BLOXOS_SETUP_TOKEN=smoketest-setup-token-12345 \
ALLOWED_ORIGINS=http://localhost:3000 \
nohup ./bloxos-hub > hub.log 2>&1 &
disown
timeout 15 bash -c 'until curl -sf -o /dev/null -w "%{http_code}" \
  http://127.0.0.1:4000/api/setup -X OPTIONS -H "Origin: http://localhost:3000" \
  2>/dev/null | grep -q 204; do sleep 0.5; done'

# 2. Dashboard dev server — from dashboard/
NEXT_PUBLIC_HUB_URL=http://localhost:4000 nohup pnpm dev \
  > /tmp/bloxos-dashboard-smoke/dashboard.log 2>&1 &
disown
timeout 30 bash -c 'until curl -sf http://localhost:3000 >/dev/null; do sleep 1; done'

# 3. Drive it
cd .claude/skills/run-dashboard
node driver.mjs --shots-dir /tmp/bloxos-dashboard-smoke/screenshots
```

The driver: completes first-boot `/setup` (creates an admin user, falls
back to a manual `/login` fill if setup doesn't auto-sign-in), then on
the fleet page opens the status-filter **dropdown** and selects an item,
opens the **Add Machine modal** and closes it, and opens the **command
palette** (⌘K) — the three most common `@base-ui/react`-backed surfaces.
It asserts zero browser console errors and zero page errors across the
whole run, not just that screenshots exist. Exits non-zero (with a
`FAIL:` line) if either check fails.

Screenshots land in `/tmp/bloxos-dashboard-smoke/screenshots/`,
numbered in the order they were taken (`01-setup-page.png` …
`09-command-palette-open.png`). **Look at them** — a blank or
half-rendered frame is a failure even if the driver's own DOM
assertions pass.

Teardown:

```bash
lsof -ti:3000 -sTCP:LISTEN | xargs -r kill -9
lsof -ti:4000 -sTCP:LISTEN | xargs -r kill -9
```

(`-9` because the wrapper processes don't reliably forward a plain
`kill` to the child — see Gotchas.)

## Run (human path)

```bash
( cd ../hub && HOME=/tmp/bloxos-dashboard-smoke/home go run . ) # separate terminal
NEXT_PUBLIC_HUB_URL=http://localhost:4000 pnpm dev         # from dashboard/
```

Open `http://localhost:3000/setup`, paste whatever setup token the hub
printed to its own stdout (or set `BLOXOS_SETUP_TOKEN` before starting
it, as above, to skip hunting for it in the logs).

## Test

```bash
pnpm lint && pnpm build
```

Both must pass with zero errors/warnings for a change to be mergeable
(`Dashboard Lint + Build` is a required CI check on `main`). This is
static verification only — it does not replace actually running the
app; a change can lint- and build-clean while still being visually
broken (a bad Tailwind class, a `@base-ui/react` API drift) or throwing
at runtime.

---

## Gotchas

- **Hub writes `bloxos.db` as a relative path.** Running it from
  `hub/` creates a stray SQLite file in the repo checkout. Always run
  the built binary from a scratch directory.
- **Hub secrets follow `HOME`.** The hub reads or creates its JWT secret,
  update-signing key, setup token, and first-run token under `~/.bloxos`.
  Always point `HOME` at the smoke directory, or a sandbox run can read or
  overwrite the operator's real local credentials even though its database is
  in scratch.
- **Hub fails closed on CORS by design.** With neither `ALLOWED_ORIGINS`
  nor `PUBLIC_URL` set, it refuses to start at all rather than fall
  back to a wildcard origin (`refusing to start with wildcard origin`).
  This is intentional hardening, not a bug — always set
  `ALLOWED_ORIGINS=http://localhost:3000` for local dev against the
  Next.js dev server.
- **Dashboard defaults to same-origin API calls.** Without
  `NEXT_PUBLIC_HUB_URL`, `getHubWsBaseUrl()` in `src/lib/session.ts`
  falls back to `window.location.origin` — silently wrong once the
  dashboard (port 3000) and hub (port 4000) are on different ports.
  Always set it explicitly.
- **`pnpm dev &`'s `$!` is the wrapper, not the server.** Killing that
  PID leaves the actual `next-server` process running and the port
  still bound. Kill by port (`lsof -ti:3000 -sTCP:LISTEN`) instead of
  trusting `$!`. Verified in this container: `kill $!` left `next-server`
  alive; `lsof -ti:3000 | xargs kill -9` was needed.
- **Ambiguous Playwright locators silently hit the wrong element.**
  `text=Live` on the fleet page matches both the dropdown's menu item
  *and* the "live" text in the ALERTS summary card, and Playwright's
  auto-retry means the failure mode is a 30s timeout fighting an inert
  overlay (`data-base-ui-inert`), not an immediate clear error. Scope
  menu clicks to `[role="menuitem"]:has-text(...)`, not bare text
  matches, on any page with repeated status words.
- **A dismissed/duplicate-key lockfile is not a dropdown/modal
  problem.** If `Dashboard Lint + Build` fails on `pnpm install`
  (`ERR_PNPM_BROKEN_LOCKFILE`), that's unrelated to anything this
  driver tests — it's usually a `git merge`-produced duplicate YAML key
  in `pnpm-lock.yaml`. Don't debug it by re-running this skill; fix the
  lockfile first (see recent history around PR #140 for the exact
  fix pattern: remove the duplicate block, don't blindly
  `pnpm install --lockfile-only`, which can silently upgrade an
  unrelated package past what was actually reviewed).

## Troubleshooting

- **`refusing to start with wildcard origin`** (hub log, exits
  immediately): `ALLOWED_ORIGINS` (or `PUBLIC_URL`) wasn't set. Add
  `ALLOWED_ORIGINS=http://localhost:3000`.
- **Driver hangs / times out on `input[placeholder="Paste the
  one-time setup token"]`**: the dashboard is pointed at the wrong hub,
  or the hub isn't actually reachable yet. Confirm
  `NEXT_PUBLIC_HUB_URL=http://localhost:4000` was set *before* `pnpm dev`
  started (Next.js inlines `NEXT_PUBLIC_*` at build/start time — setting
  it after the server is already running has no effect).
- **`page.click` times out with `<div role="presentation"
  data-base-ui-inert=""> ... subtree intercepts pointer events`**: an
  ambiguous locator resolved to an element behind the dropdown/dialog's
  own overlay, not a real regression. Make the locator more specific
  (see Gotchas).
- **Setup form submits but the driver ends up back on `/setup`**: the
  setup token didn't match. `BLOXOS_SETUP_TOKEN` must be set on the hub
  process *before* it boots (it only reads the token at first-boot when
  the users table is empty) and must exactly match what the driver
  fills in (`smoketest-setup-token-12345` by default — override both
  sides together, not just one).
