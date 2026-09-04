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
issues a durable per-machine secret in an `enrolled` frame but writes nothing
yet: the agent stages the secret to disk and replies `enrollment_committed`,
and only then does the hub consume the token, store the credential, register
the connection, and answer with a hash-bound `enrollment_confirmed`. Until that
commit the socket has no live registry entry, receives no routed commands, has
not completed authentication, and stays inside the fixed authentication window
rather than the idle deadline. The first metrics frame may already have
created or updated the machine row as online; if the enrollment never commits,
that row is marked offline when the socket disconnects. An agent that fails to
save and drops the connection can retry with the same token. Later reconnects
prefer the durable secret.

The hub and the agent binaries it serves are a coupled deployment: build and
deploy them from the same commit. Agents built from source since commit
06a0b60 send `enrollment_committed`. An agent binary from before that commit
saves the secret and drops its token on `enrolled` without ever committing, so
under this contract it never receives a credential and cannot complete a fresh
enrollment; already-enrolled agents reconnecting with a stored secret are
unaffected.

Rerunning the Windows paste command on a healthy machine intentionally
redownloads the agent, reinstalls the service, and restarts it, the same as the
Linux installer. The installer cannot tell whether the local secret is still
valid at the hub, and a rerun with a fresh token is how an operator repairs a
machine whose credential was revoked; a brief restart on a healthy box is
expected, not a fault.

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

### Resolving served agent binaries

A single resolver supplies the path used to hash, find the detached `.sig`, and
serve `/download/agent`. Linux and Windows resolve independently. An explicit
`BLOXOS_AGENT_BINARY` or `BLOXOS_AGENT_BINARY_WINDOWS` is authoritative: it
must be absolute and trusted, and a missing or rejected configured path fails
closed without falling through. Without an override, each platform checks its
root-owned system default under `/usr/local/lib/bloxos/`, then a sibling of the
running hub executable. Resolution never depends on the process working
directory.

The resolver canonicalizes the path and requires the binary plus every ancestor
to be root-owned and not group/other-writable. The API and Versions dashboard
show the selected path, source, SHA, mtime, or platform-specific resolution
error. A failure clears only that platform's advertised SHA, so one missing
artifact cannot leave a stale digest or disable the other platform.

Deploying this resolver requires a coupled live migration, not a hub-only
upgrade. Before restarting the updated hub, place the currently served Linux
binary and its valid detached signature in a root-owned directory, and update
`BLOXOS_AGENT_BINARY` to that path. Preserve the prior binary, signature, and
service configuration for rollback. The migration and restart are operational
changes and are intentionally separate from the code change.

[#145]: https://github.com/bokiko/bloxos/issues/145

## Deployment Assets

Sample deployment files live under `scripts/`:

- `scripts/caddy/Caddyfile`
- `scripts/systemd/bloxos-hub.service`
- `scripts/systemd/bloxos-dashboard.service`
- `scripts/systemd/bloxos-agent.service`

These are examples for the author's LAN deployment, not a polished installer.
Adjust paths, users, hostnames, and binary locations for your environment.
