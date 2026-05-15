# BloxOS Architecture

BloxOS has three runtime components and one local database.

```text
Linux/Windows agents
        |
        | WebSocket: /ws/agent
        v
Hub, Go + SQLite, 127.0.0.1:4000
        |
        | REST + SSE + terminal WebSocket
        v
Dashboard, Next.js, 127.0.0.1:3000
```

In production, Caddy normally fronts both local services:

- `/api/*` -> hub
- `/ws/*` -> hub
- `/install.sh`, `/install.ps1` -> hub-generated installers
- `/download/*` -> hub agent binaries and CA certificate
- everything else -> dashboard

## Hub

The hub owns the database and all control-plane APIs.

Important responsibilities:

- first-boot setup and JWT auth
- scope-based RBAC
- machine registry and latest metrics reads
- WebSocket control channel for agents
- SSE stream for dashboard updates
- terminal relay between browser and Linux agents
- install token creation and durable agent secret enrollment
- agent binary download and rollout version announcements
- API-polled machines such as Proxmox and Synology
- branding, preferences, pinned machines, saved filters, users, and alerts

SQLite migrations live in `hub/migrations.go`. The hub explicitly enables
foreign keys, but not every machine-keyed table has `ON DELETE CASCADE`, so some
cleanup still happens in handlers.

## Agent

Agents are native Go binaries.

Linux agents collect metrics and hardware inventory, support PTY-backed terminal
sessions, run under systemd, and store their durable secret at
`/etc/bloxos/agent-secret` when running as root.

Windows agents collect metrics and hardware inventory through Windows APIs, run
under the Service Control Manager, and use the same hub WebSocket protocol.
Terminal sessions are currently Linux-only.

On first enrollment, the agent connects with a one-time install token. The hub
exchanges that token for a durable per-machine secret. Later reconnects prefer
the durable secret.

## Dashboard

The dashboard is a Next.js app. It does not talk to agents directly.

It uses:

- REST for reads and operator actions
- SSE from `/api/events` for live fleet updates
- WebSocket only for browser-side terminal relay
- hub APIs for branding, theme, preferences, users, inventory, alerts, and
  version status

With `NEXT_PUBLIC_HUB_URL` unset, the dashboard uses same-origin API calls,
which is the intended Caddy-backed production mode.

## Update Flow

The hub hashes the configured agent binaries and announces the expected SHA to
connected agents. Agents compare that SHA with their running binary, download
`/download/agent` when needed, verify the SHA, replace the executable, and let
systemd or SCM restart them.

The hub tracks reported running versions so the dashboard can show rollout
state and pending updates.

## Deployment Assets

Sample deployment files live under `scripts/`:

- `scripts/caddy/Caddyfile`
- `scripts/systemd/bloxos-hub.service`
- `scripts/systemd/bloxos-dashboard.service`
- `scripts/systemd/bloxos-agent.service`

These are examples for the author's LAN deployment, not a polished installer.
Adjust paths, users, hostnames, and binary locations for your environment.
