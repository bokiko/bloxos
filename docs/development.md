# Development guide

Building and running BloxOS from source for local development. For production
use, prefer the [supported Compose deployment](../docker/README.md) and the
published images; this guide is the from-source path for working on the code.

Contribution policy, PR gates, and merge rules live in
[CONTRIBUTING.md](../CONTRIBUTING.md). Repo conventions and the agent
compatibility contract live in [AGENTS.md](../AGENTS.md).

## Components

| Component | Language | Runs as | Default address |
|---|---|---|---|
| Hub | Go | binary (systemd in prod) | `127.0.0.1:4000` |
| Agent | Go | systemd (Linux) / SCM (Windows) | outbound WebSocket only |
| Dashboard | Next.js | local service behind Caddy in prod | `127.0.0.1:3000` |

The dashboard never talks to agents directly — it always goes through the hub
(WebSocket agent↔hub, SSE hub↔browser). For the component map see
[architecture.md](architecture.md).

This from-source path targets **Linux on amd64**. The supported Compose build
packages both Linux architectures and the Windows agent.

## Quick loop

Use a disposable Linux host with Go 1.26.8, Node.js 22 and pnpm 10, matching
the current CI toolchain. Clone the repository first:

```sh
git clone https://github.com/bokiko/bloxos.git
cd bloxos
```

Use three shells, each starting at the repository root. Do not run a development
agent on a machine with an existing production agent, its credentials, or its
release-floor state. Read [the development safety notes](#disposable-agents-only-for-development-builds)
before enrolling it.

The hub refuses to start unless an
origin policy is explicit, and the dashboard on `:3000` is cross-origin to the
hub on `:4000`, so `NEXT_PUBLIC_HUB_URL` is **required** in dev.

```sh
# 1. Hub — needs an origin policy (PUBLIC_URL and/or ALLOWED_ORIGINS)
cd hub
PUBLIC_URL=http://localhost:4000 \
ALLOWED_ORIGINS=http://localhost:3000 \
HUB_LISTEN=127.0.0.1:4000 \
go run .
# First run writes a setup token to ~/.bloxos/setup-token. Keep this running.

# 2. Dashboard — must be told the hub origin (cross-origin in dev)
cd dashboard
pnpm install
NEXT_PUBLIC_HUB_URL=http://localhost:4000 pnpm dev
# Open http://localhost:3000, enter the setup token, create the first admin.

# 3. Agent — connects to a running hub. The --hub flag takes the FULL
#    WebSocket path including /ws/agent (unlike the BLOXOS_HUB env var, which
#    takes the base URL and has /ws/agent appended for you).
cd agent
go run . --hub ws://localhost:4000/ws/agent --token '<install-token>'
```

Get `<install-token>` from the dashboard: **Add Machine → Linux** generates a
one-line command carrying the one-time token. On loopback you can run that
command directly, or pass the token to `go run .` as above. The agent uses the
token once, stores a durable machine secret, and then appears in the fleet.

Installing a hub and enrolling a machine are different operations — an
enrollment command never installs another hub.

## Building binaries

```sh
mkdir -p bin
(cd hub   && go build -o ../bin/bloxos-hub .)
(cd agent && go build -o ../bin/bloxos-agent .)
```

For the hub's download endpoint to serve the agent you just built, install it
on a root-owned path and point the hub at it:

```sh
sudo install -d -o root -g root -m 0755 /usr/local/lib/bloxos/linux
sudo install -o root -g root -m 0755 bin/bloxos-agent \
  /usr/local/lib/bloxos/linux/bloxos-agent
# Then start the hub with BLOXOS_AGENT_BINARY pointing at that path.
```

The served agent path (and its complete ancestor chain) must be root-owned and
not group/other-writable; the hub refuses to serve a binary it cannot trust.

Windows agent artifact (cross-compiled from Linux):

```sh
(cd agent && GOOS=windows GOARCH=amd64 go build -o ../bin/bloxos-agent.exe .)
```

Native development that serves Windows agents must set
`BLOXOS_AGENT_BINARY_WINDOWS` to a trusted root-owned artifact path. For
Windows enrollment use the generated command in **Add Machine → Windows**.

## Disposable agents only for development builds

Any agent you build locally is **disposable** — run it only against a
throwaway hub and a throwaway machine identity. Never point a locally built
agent at a production hub or reuse a production machine's stored secret or
release floor. Two reasons, both from the update-safety contract in
[AGENTS.md](../AGENTS.md):

- **Changed agent bytes need a release bump.** Any change that alters the agent
  binary requires bumping the source-controlled release number and marker in
  `agent/release.go`. A build with changed bytes but the *same* release number
  is not a valid release.
- **The release floor rejects same-number/different-SHA.** Protocol-2 agents
  persist a signed release-number + SHA floor before replacing their
  executable. They accept a given release number only for the *identical* bytes
  and reject older numbers outright. A rebuilt dev binary
  may have a different SHA, so publishing changed bytes at an existing number
  is refused, and reusing a production
  machine's persisted floor with a dev binary can wedge that machine's updates.
  Never delete or reset the floor to work around this on a real machine — use a
  fresh, disposable machine instead.

Keep first enrollment on loopback in dev. For a LAN deployment, terminate TLS
in front of the hub, set `PUBLIC_URL` to that trusted HTTPS origin, set
`ALLOWED_ORIGINS` to the dashboard origin, and keep `BLOXOS_AGENT_BINARY` on an
explicit absolute path. The supported Compose path supplies the private-CA
fingerprint and verified leaf-key pin in generated onboarding commands; do not
replace it with an unverified download-and-run command.

## Environment reference

`hub` and `agent` read their configuration from environment variables. See
[.env.example](../.env.example) for common settings and
[configuration.md](configuration.md) for links to specialized settings; the
most relevant for local development:

| Variable | Component | Purpose |
|---|---|---|
| `PUBLIC_URL` | Hub | Browser-facing hub URL (required unless `ALLOWED_ORIGINS` is set); also drives generated commands and update-transport policy. |
| `ALLOWED_ORIGINS` | Hub | Comma-separated browser origins permitted by CORS (required unless `PUBLIC_URL` is set). |
| `HUB_LISTEN` | Hub | Listen address; defaults to `127.0.0.1:4000`. |
| `BLOXOS_AGENT_BINARY` | Hub | Absolute Linux amd64 agent binary to serve; ELF-verified, served only for its actual architecture. |
| `BLOXOS_AGENT_BINARY_ARM64` | Hub | Absolute Linux arm64 agent binary. |
| `BLOXOS_AGENT_BINARY_WINDOWS` | Hub | Absolute Windows agent binary to serve. |
| `BLOXOS_JWT_SECRET` | Hub | JWT secret (≥32 bytes); generated and persisted if unset. |
| `BLOXOS_SETUP_TOKEN` | Hub | Fixed first-boot setup token; generated and persisted if unset. |
| `NEXT_PUBLIC_HUB_URL` | Dashboard | Hub origin used by the browser when the dashboard and hub are not same-origin — **required in dev** (dashboard `:3000`, hub `:4000`). |
| `BLOXOS_HUB` | Agent | Hub **base** WebSocket URL; the agent appends `/ws/agent`. (The `--hub` flag, by contrast, expects the full path including `/ws/agent`.) |
| `BLOXOS_TOKEN` | Agent | One-time enrollment token. |
| `BLOXOS_SECRET` | Agent | Durable machine credential, normally managed by enrollment. |
| `BLOXOS_TERMINAL_USER` | Agent | Existing non-root Linux account for terminal sessions; if missing or root, terminals are refused (never run as root). |
| `BLOXOS_TLS_INSECURE` | Agent | TLS bypass, only in an agent built with `-tags insecure`; development only. |

## Running the checks

Run the same checks CI runs before pushing — see
[CONTRIBUTING.md](../CONTRIBUTING.md#pull-request-gates) for the full command
list, timeouts, and which jobs are blocking on `main`.

## Production builds and sample services

```sh
(cd hub       && go build -o ../bin/bloxos-hub .)
(cd agent     && go build -o ../bin/bloxos-agent .)
(cd dashboard && pnpm install --frozen-lockfile && pnpm build)
```

The sample units in [scripts/systemd](../scripts/systemd) use `<user>` and
`/opt/bloxos` placeholders. Replace `<user>`, install the hub and dashboard
artifacts there, keep the served agent binary on the root-owned path above,
then:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now bloxos-hub bloxos-dashboard bloxos-agent
```

For the supported, reproducible multi-architecture build (both Linux arches
plus the Windows agent embedded in the hub image), use the Compose path in
[docker/README.md](../docker/README.md) rather than hand-installed binaries.
