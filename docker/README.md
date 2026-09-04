# Container images

Two images cover the hub side of a deployment. Agents stay native
services on every managed machine; they are not containerized.

| Image | Dockerfile | Contents | Port |
|---|---|---|---|
| hub | `Dockerfile.hub` | hub binary plus the agent binaries the hub serves | 4000 |
| dashboard | `Dockerfile.dashboard` | Next.js standalone server | 3000 |

Both build from the repository root (the hub and agent modules share
`proto/` through a local replace directive) and run as unprivileged users.
Compose and published images are not part of this step.

## Build

```bash
docker build -f Dockerfile.hub -t bloxos-hub .
docker build -f Dockerfile.dashboard -t bloxos-dashboard .
```

The hub image builds the hub and both agents from the same commit with
`CGO_ENABLED=0`, `-trimpath`, no VCS stamping, and stripped binaries, so a
rebuild of the same commit yields the same served agent hash and does not
trigger a fleet rollout.

## Architecture

The served Linux agent matches the image's own architecture: an arm64 image
serves arm64 Linux agents, an amd64 image serves amd64 Linux agents. The
served Windows agent is always amd64. One image therefore serves one Linux
agent architecture; a fleet that mixes amd64 and arm64 Linux machines needs
per-architecture resolution in the hub, which is not implemented yet.

## Run the hub

The hub refuses to start without an origin policy, so `PUBLIC_URL` (or
`ALLOWED_ORIGINS`) is required. All state lives in one volume mounted at
`/data`: `bloxos.db`, and `.bloxos/` holding the setup token, JWT secret and
update-signing key.

```bash
docker run -d --name bloxos-hub -p 4000:4000 \
  -e PUBLIC_URL=http://127.0.0.1:4000 \
  -v bloxos-data:/data \
  bloxos-hub
docker exec bloxos-hub cat /data/.bloxos/setup-token
```

Inside the image the hub listens on `0.0.0.0:4000` and runs as uid 65532.
The agent binaries are root-owned and read-only at
`/usr/local/lib/bloxos/linux/bloxos-agent` and
`/usr/local/lib/bloxos/windows/bloxos-agent.exe`, which are the resolver's
default paths, so no `BLOXOS_AGENT_BINARY*` setting is needed. For a private
CA, mount the CA certificate and point `BLOXOS_CA_CERT` at it. Back up the
`/data` volume; losing the update-signing key strands agent self-update.

## Run the dashboard

```bash
docker run -d --name bloxos-dashboard -p 3000:3000 bloxos-dashboard
```

The dashboard is built with an empty `NEXT_PUBLIC_HUB_URL`, so it expects
to be served from the same origin as the hub API, which a reverse proxy in
front of both provides. It runs as the `node` user.

Both images carry a healthcheck against `/health` (hub) and `/login`
(dashboard).
