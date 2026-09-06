# Containers

The hub side of a deployment runs as containers: Caddy for TLS, the hub API,
and the dashboard. Agents stay native services on every managed machine;
they are not containerized.

## Compose: the supported hub deployment

One required setting, three commands, from a clone of this repository:

```bash
cd docker
cp .env.example .env      # set HUB_HOST to this machine's hostname or IP
docker compose up -d --build
```

Published images are an alternative to building: every `v*` tag publishes
`ghcr.io/bokiko/bloxos-hub` and `ghcr.io/bokiko/bloxos-dashboard` for
amd64 and arm64, tagged with the version and `latest`. To use them, replace
the last command with `docker compose pull && docker compose up -d`; set
`BLOXOS_VERSION` in `.env` to pin a version instead of `latest`.

Then read the first-boot setup token and open the dashboard:

```bash
docker compose exec hub cat /data/.bloxos/setup-token
```

Open `https://<HUB_HOST>`, accept the browser warning for the internal CA
(or install its root certificate, see below), enter the token, and create
the admin account. Use **Add Machine** to enroll agents; the generated
command carries the CA fingerprint, so agents verify the hub without any
manual trust step.

What the stack contains:

- `caddy` serves `https://HUB_HOST` with its internal CA and forwards API,
  WebSocket, installer, and download paths to the hub and everything else to
  the dashboard. It runs unprivileged as the hub's uid so the hub can read
  the CA it writes.
- `hub` derives `PUBLIC_URL` from `HUB_HOST` and reads Caddy's root
  certificate read-only from the `caddy-data` volume to pin it into install
  commands.
- `dashboard` is served same-origin behind Caddy.
- `caddy-init` runs once to hand the Caddy volumes to the unprivileged uid.

`HUB_HOST` is a bare hostname or IP address, no scheme, no port; the stack
publishes 80 and 443. IP addresses work because Caddy is given a default
SNI for clients that connect without one.

Client addresses: Caddy forwards the client address to the hub and the hub
accepts it from peers on the private Compose network, so per-address rate
limits and the terminal audit see clients, not Caddy. The hub's port is not
published, so only the stack's own containers can reach it. On rootless
Docker the address Caddy sees is the port forwarder's, so all external
clients share one address there; rootful Docker passes the real address.

Operations:

- Upgrade: `git pull` then `docker compose up -d --build`, or with published
  images `docker compose pull && docker compose up -d`. The hub and the
  agents it serves come from the same commit; connected agents that have
  pinned the update key self-update.
- Back up the `bloxos-data` volume (database, secrets, setup token,
  update-signing key) and the `caddy-data` volume (the CA). Losing the
  signing key strands agent self-update; losing the CA invalidates the
  certificate every enrolled agent pinned, and each must be re-enrolled.
- Root certificate for browsers:
  `docker compose exec caddy cat /data/caddy/pki/authorities/local/root.crt`.
- Logs: `docker compose logs -f hub`.

## Smoke test

`scripts/smoke/compose-enroll.sh` is the end-to-end gate for this stack. On
a disposable Linux host with Docker, systemd, and passwordless sudo it
brings the stack up, completes setup through the API, mints an install
token, runs the generated Linux command on that same host so it enrolls as
an agent, asserts the machine is online with the CA pinned, then removes
the agent and the stack. CI runs it on pull requests that touch the
container files and before publishing images on a tag. Locally:

```bash
SMOKE_CONFIRM_DISPOSABLE=1 HUB_HOST=<this host's IP> scripts/smoke/compose-enroll.sh
```

## Images

| Image | Dockerfile | Contents | Port |
|---|---|---|---|
| hub | `Dockerfile.hub` | hub binary plus the agent binaries the hub serves | 4000 |
| dashboard | `Dockerfile.dashboard` | Next.js standalone server | 3000 |

Both build from the repository root (the hub and agent modules share
`proto/` through a local replace directive) and run as unprivileged users.
The sections below run them individually, without Caddy; published images
are not part of this step.

### Build

```bash
docker build -f Dockerfile.hub -t bloxos-hub .
docker build -f Dockerfile.dashboard -t bloxos-dashboard .
```

The hub image builds the hub and both agents from the same commit with
`CGO_ENABLED=0`, `-trimpath`, no VCS stamping, and stripped binaries, so a
rebuild of the same commit yields the same served agent hash and does not
trigger a fleet rollout.

### Architecture

Every hub image, whatever architecture it runs on, carries Linux agents for
both amd64 and arm64 (cross-compiled in the build stage, no emulation) and the
Windows amd64 agent, so one hub serves a mixed fleet. The hub picks the Linux
build from the `arch` the installer and the agent's self-updater request
(`/download/agent?os=linux&arch=amd64|arm64`), answers 404 with a message
naming the architecture when it has no binary for it, and the installer
refuses to install an ELF whose `e_machine` does not match the host CPU. An
agent reports its architecture on connect, and the hub announces only that
architecture's SHA to it. A request without `arch`, or an agent that reports
none, gets the amd64 build, as before.

### Run the hub alone

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
`/usr/local/lib/bloxos/linux/amd64/bloxos-agent`,
`/usr/local/lib/bloxos/linux/arm64/bloxos-agent` and
`/usr/local/lib/bloxos/windows/bloxos-agent.exe`, which are the resolver's
default paths, so no `BLOXOS_AGENT_BINARY*` setting is needed. The Compose file sets `BLOXOS_PIN_DIAL_ADDR=caddy:443` so the hub can read the TLS certificate it pins into one-line join commands: PUBLIC_URL routes to Caddy from outside, but to the hub's own loopback from inside its container, so the pin handshake is aimed at the Caddy service while SNI and verification still use PUBLIC_URL. For a private
CA, mount the CA certificate and point `BLOXOS_CA_CERT` at it. Back up the
`/data` volume; losing the update-signing key strands agent self-update.

### Run the dashboard alone

```bash
docker run -d --name bloxos-dashboard -p 3000:3000 bloxos-dashboard
```

The dashboard is built with an empty `NEXT_PUBLIC_HUB_URL`, so it expects
to be served from the same origin as the hub API, which a reverse proxy in
front of both provides. It runs as the `node` user.

Both images carry a healthcheck against `/health` (hub) and `/login`
(dashboard).
