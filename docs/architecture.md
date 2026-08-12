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

### Server struct

A `Server` struct in `hub/server.go` owns the database handle and the connected
agent registry (`db`, `agents`, `agentsMu`), and most handlers are methods on
`*Server`. It also tracks the goroutines it spawns, so a server can be shut down
without racing work still in flight — which is what makes the hub test suite
race-clean.

Ownership is deliberately partial. Most other mutable state — SSE clients,
pending commands, terminal sessions, the JWT secret, the rate limiter, API
pollers, and the rollout/version caches — is still package-level. Moving the
rest is planned work, not a description of today's code; see `BLOXOS_FUTURE.md`.

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
connected agents, together with an Ed25519 signature over
`bloxos-agent-update:v1:<os>:<sha>`. The signature comes from either a detached
`<binary>.sig` produced offline or a hub-held signing key.

Protocol-v1 agents accept an update only when the signature verifies against
their pinned key and the transport is permitted. The pinned key lives at
`/etc/bloxos/agent-update.pub` on Linux and beside the executable on Windows.
Protocol-0 agents cannot verify signatures and are permitted one migration hop
only when `PUBLIC_URL` is TLS or loopback. Afterward, the hub withholds further
updates until the agent's key is pinned through a trusted provisioning path.

`announceDecision` is shared by the announcement path and versions API. It
fails closed for protocol-v1 agents when no valid signature is available, the
transport is plaintext, or the update key is unpinned. Withheld agents do not
create reconnect expectations. A circuit breaker pauses rollout after repeated
genuine failures.

Linux verifies the update, replaces its executable atomically, and relies on
systemd plus an `OnFailure` recovery unit to restore `.prev` after repeated
startup failure.

Windows stages the download as `<exe>.new` and writes `<exe>.pending` with the
expected `sha256` and `signature`. `performUpdateWindows` exits with code `1`
for SCM to restart the service. Before the swap, `applyPendingUpdate` parses the
marker, hashes `.new`, compares the SHA, and verifies the signature against the
pinned key. Validation failure removes both staged files. On success it spawns
a helper that attempts `move /Y` before deleting the marker; marker deletion is
unconditional, not evidence that the move succeeded. Windows attempts to
snapshot `.prev` but has no automatic rollback, so recovery remains manual.

The hub tracks reported running versions so the dashboard can show rollout
state and pending updates.

Signatures still carry no downgrade or monotonicity protection, so a previously
valid `(os, sha)` pair remains valid indefinitely ([#145]).

### Resolving the served binary (Linux)

For Linux agents, `agentBinaryPathFor` resolves the served binary from an
ordered candidate list: `$BLOXOS_AGENT_BINARY`, then `./agent/bloxos-agent`
relative to the hub's working directory, then `/usr/local/bin/bloxos-agent`.
The relative candidate does not resolve when the hub's `WorkingDirectory` is
the `hub/` subdirectory, and the fallback is silent. Set
`BLOXOS_AGENT_BINARY` explicitly in deployments to avoid serving a stale binary
([#149]). Windows resolution uses its own environment variable and fallbacks.

[#145]: https://github.com/bokiko/bloxos/issues/145
[#149]: https://github.com/bokiko/bloxos/issues/149

## Deployment Assets

Sample deployment files live under `scripts/`:

- `scripts/caddy/Caddyfile`
- `scripts/systemd/bloxos-hub.service`
- `scripts/systemd/bloxos-dashboard.service`
- `scripts/systemd/bloxos-agent.service`

These are examples for the author's LAN deployment, not a polished installer.
Adjust paths, users, hostnames, and binary locations for your environment.
