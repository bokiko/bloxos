<div align="center">

# BloxOS

**The operator console your homelab actually deserves.**

Real-time fleet management for self-hosted infrastructure — Linux servers, Windows workstations, Proxmox VMs, NAS units, mining rigs. One dashboard, live metrics, web terminals, hardware inventory, native Windows + Linux agents, auto-update, multi-user RBAC.

[![License](https://img.shields.io/badge/license-Apache_2.0-blue.svg)](LICENSE)
[![Made with Go](https://img.shields.io/badge/agent-Go-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Made with Next.js](https://img.shields.io/badge/dashboard-Next.js-000000?logo=next.js&logoColor=white)](https://nextjs.org)
[![SQLite](https://img.shields.io/badge/storage-SQLite-003B57?logo=sqlite&logoColor=white)](https://sqlite.org)

[Report a bug](https://github.com/bokiko/bloxos/issues) · [Author](https://bokiko.io)

</div>

---

## Why BloxOS exists

I run a homelab. Multiple Proxmox boxes, a Synology NAS, a Windows workstation, a Mac Studio doing AI work, a couple of mining rigs, plus VMs spread across all of it. The existing options to manage that fleet are all wrong for me:

- **Datadog / New Relic** — built for SaaS companies, priced like SaaS companies, send my home network telemetry to a third party.
- **Grafana + Prometheus + node_exporter** — three services to maintain just to see if a box is alive. Charts are great. Operating the fleet is not what they do.
- **Cockpit / Webmin** — per-machine dashboards, no fleet view, no Windows story.
- **Proxmox UI** — only sees Proxmox.

BloxOS is what I wanted instead: **one dashboard that treats my whole fleet as one thing**, runs entirely on my hardware, holds zero of my data on someone else's servers, and is fast enough that it feels alive instead of feeling like a monitoring tool.

If you've ever opened five browser tabs to check on five machines, this is for you.

---

## What you actually get

### Live, not polled
Metrics stream over WebSocket from agent to hub, then push to your browser via SSE. The normal agent snapshot cadence is 30 seconds, with on-demand refresh. Freshness indicators distinguish current readings from stale data. Power history samples component sensors locally every second and uploads 30-second averages and sampled peaks; this is not a wall-power meter.

### Real hardware inventory
Every agent collects DMI data, RAM modules (manufacturer, speed, slot, ECC status), GPU devices (model, VRAM, driver), PCI bus, network adapters, disks, BIOS, and motherboard. The fleet-wide `/inventory` page is sortable, filterable, groupable, and exports to CSV / JSON / Markdown. The first time you use it to find "every machine with less than 32GB RAM" in three seconds, you'll understand why it's there.

### Web terminal that actually works
xterm.js, theme-aware, stable 360px pane. Hit a machine, get a real shell. No SSH key juggling, no port forwarding, no "what was the IP again." The terminal session belongs to the operator's auth context, not the machine's.

### Two operating systems, one dashboard
The Linux agent and the Windows agent speak the same WebSocket protocol to the same hub. The Windows agent registers itself as a Windows Service via SCM, collects hardware via WMI, and supports signed auto-update. Web terminal access is Linux-only; Windows enrollment and re-enrollment use generated PowerShell commands.

### Signed auto-update and recovery
Protocol-v1 agents accept an update only when its Ed25519 release signature verifies against their pinned key and the transport is permitted. Protocol-0 agents require a deliberately limited migration hop to reach protocol v1, because signature verification cannot be retrofitted into an already-running binary; the hub permits that hop only over TLS or loopback. After migration, the agent is withheld until its update key is pinned through a trusted provisioning path.

The signature covers `bloxos-agent-update:v1:<os>:<sha>`. It may come from a detached `<binary>.sig` produced offline, in which case the hub holds no private key, or from a hub-held signing key. Protocol-v1 updates fail closed when the signature is missing or invalid, the transport is plaintext, or the agent has no pinned key.

Both platforms use a `.prev` file for recovery, but their behavior differs:

- **Linux** verifies the update, replaces the executable atomically, and exits for systemd to restart it. An `OnFailure` recovery unit can automatically restore `.prev` after repeated startup failure.
- **Windows** attempts to snapshot `.prev`, downloads to `<exe>.new`, and writes `<exe>.pending` with the expected `sha256` and `signature`. On the SCM restart, `applyPendingUpdate` hashes `.new`, compares the SHA, and verifies the signature against the pinned key before spawning the swap helper. `performUpdateWindows` exits with code `1` to trigger that SCM restart. The helper attempts `move /Y` before deleting the marker, but marker deletion is unconditional; Windows has no automatic rollback, so restoring `.prev` remains manual.

A circuit breaker pauses fleet rollout after two failures in five minutes. Protocol-2 agents persist a signed release-number/SHA floor before replacing their executable: older builds are rejected, and the same release number is accepted only for identical bytes. Protocol-1 agents do not enforce this floor. Never delete the floor during reinstall, key rotation, or recovery; see [AGENTS.md](AGENTS.md) for the compatibility contract.

You push an update to the hub and the fleet moves to the new version on its own. On Linux, a bad build can also recover automatically.

### Multi-user with real permissions
Viewers, operators, admins. Every endpoint has a scope (`fleet.read`, `fleet.control`, `fleet.metadata`, `fleet.admin`, `branding.admin`, `users.admin`). Operators can run actions but not change roles. Admins can change branding. Viewers can look but not touch. JWT-based, bcrypt for passwords.

### Personalization that respects your eyes
Five themes — BloxOS (default), Solarized, Dracula, Nord, Tokyo Night — each in light and dark variants where appropriate. Per-user. Plus org-wide custom branding: upload your own logo, favicon, and login welcome message. Density toggle (comfortable / compact). Pinned machines. Saved filters. Default views per user.

### Cmd+K everywhere
Command palette opens on `Cmd+K` (or `Ctrl+K`). Jump to any machine by name, run any action, open any setting, search inventory, switch themes. The palette is how operators actually use the system once they know it exists.

### One-file database
SQLite keeps users, machines, history, inventory, branding, preferences and notes together. The hub uses WAL, so a live copy of `bloxos.db` alone is **not a safe backup**. Preserve the database and the hub's signing/JWT identity plus the proxy CA using the [backup and restore procedure](docs/backup-restore.md).

### Built for operators, not viewers
- Notes per machine (markdown-ish, URL auto-link)
- Persistent last-known state in localStorage so the dashboard hydrates instantly even before WebSocket reconnects
- Live freshness timer per card so you always know how stale the data is
- Per-card refresh button + global refresh button
- Skeleton loading states everywhere — no flashes of blank UI
- `prefers-reduced-motion` honored throughout

---

## Architecture at a glance

```
┌─────────────────┐         ┌─────────────────┐
│   Linux Agent   │◀──WS───▶│                 │
└─────────────────┘         │                 │
                            │       Hub       │◀──SSE──▶  Dashboard (browser)
┌─────────────────┐         │   (Go + SQLite) │
│  Windows Agent  │◀──WS───▶│                 │◀──REST──▶  CLI / scripts
└─────────────────┘         │                 │
                            └─────────────────┘
┌─────────────────┐                  ▲
│   API-polled    │──────────────────┘
│  (Synology, etc)│
└─────────────────┘
```

- **Hub** is a single Go binary. Holds the SQLite database. Speaks WebSocket to agents, SSE to dashboards, REST to CLIs and scripts.
- **Agents** are single Go binaries. Linux runs under systemd; Windows runs under SCM. They open one outbound WebSocket to the hub — no inbound ports needed on agent machines.
- **Dashboard** is Next.js. In production it normally runs as its own local service on `127.0.0.1:3000` behind Caddy; in development it runs with `pnpm dev`. The dashboard never talks to agents directly; it always goes through the hub.
- **API-polled targets** (Synology, Proxmox, anything without a native agent) are scraped by the hub on a schedule and surfaced as machines in the same dashboard.

For a deeper component map, see [docs/architecture.md](docs/architecture.md).

### Why this shape

A central hub means agents don't need inbound network access — they punch out to the hub from wherever they live. Works through NATs, behind home routers, across Tailscale, across UniFi VLANs. The hub is the only thing that needs a stable address.

WebSocket from agents to hub means real-time, bidirectional. The hub can push commands (`refresh_metrics`, `run_command`, `open_terminal`) without polling. Agents can stream metrics without scrape intervals.

SSE from hub to browser means the dashboard updates live without WebSocket complexity in the frontend. SSE survives proxies and corporate firewalls better than WebSocket. Reconnect is automatic.

---

## Configuration

The hub refuses to start unless an origin policy is explicit. Set
`PUBLIC_URL`, `ALLOWED_ORIGINS`, or both before the first boot. Copy
[.env.example](.env.example) for a commented reference covering every
environment variable read by the Go hub and agent.

| Variable | Required | Purpose |
|---|---:|---|
| `PUBLIC_URL` | **Yes, unless `ALLOWED_ORIGINS` is set** | Browser-facing hub URL; also drives generated commands and update transport policy. |
| `ALLOWED_ORIGINS` | **Yes, unless `PUBLIC_URL` is set** | Comma-separated browser origins permitted by CORS. |
| `HUB_LISTEN` | No | Hub listen address; defaults to `127.0.0.1:4000`. |
| `BLOXOS_JWT_SECRET` | No | JWT secret, at least 32 bytes; otherwise generated and persisted. |
| `BLOXOS_SETUP_TOKEN` | No | Fixed first-boot setup token; otherwise generated and persisted. |
| `BLOXOS_CA_CERT` | No | Additional CA certificate used by installers and agents. |
| `BLOXOS_AGENT_BINARY` | No | Absolute Linux **amd64** agent binary; blank uses the built-in defaults (`/usr/local/lib/bloxos/linux/amd64/bloxos-agent`, then the legacy `/usr/local/lib/bloxos/linux/bloxos-agent`, then a hub sibling). Served only for the architecture its ELF actually is. |
| `BLOXOS_AGENT_BINARY_ARM64` | No | Absolute Linux arm64 agent binary; blank uses `/usr/local/lib/bloxos/linux/arm64/bloxos-agent`. ELF-verified like all Linux paths, so no request is served another CPU's binary. |
| `BLOXOS_AGENT_BINARY_WINDOWS` | No | Absolute Windows agent binary served by the hub. |
| `BLOXOS_UPDATE_PUBKEY` | No | Base64 Ed25519 public key for detached-signature mode. |
| `BLOXOS_UPDATE_SIGNING_KEY` | No | Explicit online-signing private-key path. |
| `BLOXOS_ALLOW_PRIVATE_TARGETS` | No | Set to `1` to permit API pollers to target RFC1918 addresses. |
| `BLOXOS_TELEGRAM_TOKEN` | No | Telegram bot token; both Telegram values are needed. |
| `BLOXOS_TELEGRAM_CHAT_ID` | No | Telegram destination chat ID. |
| `BLOXOS_HUB` | Agent | Hub base WebSocket URL; the agent appends `/ws/agent`. |
| `BLOXOS_SECRET` | Agent | Durable machine credential, normally managed by enrollment. |
| `BLOXOS_TOKEN` | Agent enrollment | One-time enrollment token. |
| `BLOXOS_TERMINAL_USER` | No | Existing non-root Linux account used for terminal sessions. Unset, the agent tries `bokiko`, `ubuntu`, `admin`; if none exists, or the named account is missing or root, terminals are refused (never run as root). |
| `BLOXOS_UPDATE_PUBKEY_PATH` | No | Override for the agent's pinned update-key file. |
| `BLOXOS_TLS_INSECURE` | Development only | TLS bypass available only in an agent built with `-tags insecure`. |
| `ProgramFiles` | Windows-provided | Used to discover NVIDIA tooling; normally never overridden. |
| `NEXT_PUBLIC_HUB_URL` | Dashboard | Hub origin when dashboard and hub are not same-origin. |

## Quick start: Compose hub

Use the [supported Compose deployment](docker/README.md) on a Docker host:

```bash
git clone https://github.com/bokiko/bloxos.git
cd bloxos/docker
cp .env.example .env
# Edit .env: set HUB_HOST to this machine's hostname or IP (no scheme/port).
docker compose up -d --build
docker compose exec hub cat /data/.bloxos/setup-token
```

Open `https://<HUB_HOST>` and use the setup token to create your account. The
stack uses an internal CA; the container guide explains browser trust. Choose
**Add Machine** and copy the generated Linux one-line command or Windows
PowerShell command to that machine. Installing a hub and enrolling a machine
are different operations; an enrollment command does not install another hub.

Build from your chosen tested revision for current source. A green main build
does not mean that the same revision is published as `latest` or deployed on
your host. When using published images, pin an available version and verify
its revision; do not assume an older release has the current source features.
Back up before upgrading. The hub also serves agent updates, so upgrading it
can update enrolled machines, not just the dashboard.

## Native development from source

> This alternative is for Linux development on amd64. The supported Compose
> build packages both Linux architectures and Windows. A one-line public hub
> installer is separate work; machine onboarding already ships.

### 1. Clone and build

```bash
git clone https://github.com/bokiko/bloxos.git
cd bloxos
mkdir -p bin
(cd hub && go build -o ../bin/bloxos-hub .)
(cd agent && go build -o ../bin/bloxos-agent .)
sudo install -d -o root -g root -m 0755 /usr/local/lib/bloxos/linux
sudo install -o root -g root -m 0755 bin/bloxos-agent \
  /usr/local/lib/bloxos/linux/bloxos-agent
```

To build the Windows agent artifact:

```bash
(cd agent && GOOS=windows GOARCH=amd64 go build -o ../bin/bloxos-agent.exe .)
```

For Windows enrollment, use the generated command in **Add Machine → Windows**.
The Compose image already includes the Windows artifact. Native development
must set `BLOXOS_AGENT_BINARY_WINDOWS` to a trusted root-owned artifact path.

### 2. Configure and run the hub

These exports set the origin policy required for startup. The explicit
agent-binary path also ensures that the download endpoint serves the artifact
you just built.

```bash
export PUBLIC_URL=http://localhost:4000
export ALLOWED_ORIGINS=http://localhost:3000
export HUB_LISTEN=127.0.0.1:4000
export BLOXOS_AGENT_BINARY=/usr/local/lib/bloxos/linux/bloxos-agent
./bin/bloxos-hub
```

On first run the hub creates a setup token in `~/.bloxos/setup-token`.
Keep this shell running.

### 3. Run the dashboard

In another shell:

```bash
cd bloxos/dashboard
pnpm install
NEXT_PUBLIC_HUB_URL=http://localhost:4000 pnpm dev
```

Open `http://localhost:3000`, enter the setup token, and create the first
admin account.

### 4. Enroll the local Linux agent

In the dashboard, choose **Add Machine → Linux** and run the generated command
on this same machine. This development path keeps first enrollment on loopback. The agent
uses its one-time token once, stores a durable machine secret, and then appears
in the fleet.

For a LAN deployment, terminate TLS in front of the hub, set `PUBLIC_URL` to
that trusted HTTPS origin, set `ALLOWED_ORIGINS` to the dashboard origin, and
keep `BLOXOS_AGENT_BINARY` on an explicit absolute path. The supported Compose
path supplies the private-CA fingerprint and verified leaf-key pin in generated
onboarding commands; do not replace this with an unverified download-and-run command.

### 5. Production builds and sample services

```bash
(cd hub && go build -o ../bin/bloxos-hub .)
(cd agent && go build -o ../bin/bloxos-agent .)
(cd dashboard && pnpm install --frozen-lockfile && pnpm build)
```

The sample units in [scripts/systemd](scripts/systemd) use `<user>` and
`/opt/bloxos` placeholders. Replace `<user>` and install the hub and dashboard
artifacts there. Keep the served agent binary on the root-owned path shown
above (or another absolute path whose complete ancestor chain is root-owned and
not group/other-writable), then enable each installed unit:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now bloxos-hub bloxos-dashboard bloxos-agent
```

---

## Tech stack

| Layer | Stack |
|---|---|
| Hub | Go, SQLite, WebSocket, SSE, JWT, bcrypt |
| Linux agent | Go, systemd, `/sys/class/thermal`, `/proc`, `dmidecode`, `lspci`, `nvidia-smi` |
| Windows agent | Go, Windows Service Manager, WMI (`Win32_*`, `MSAcpi_ThermalZoneTemperature`), `nvidia-smi.exe` |
| Dashboard | Next.js 16 (App Router), React, TypeScript, Tailwind CSS v4, lucide-react, cmdk, recharts, xterm.js |
| Auth | JWT (HS256), bcrypt password hashing, scope-based RBAC |
| Real-time | WebSocket (agent ↔ hub), SSE (hub ↔ browser) |
| Deployment | Single Go binary for the hub, single binary for each agent, Next.js dashboard behind Caddy/systemd |

Compose is the supported packaged hub deployment; native services remain an alternative. No Kubernetes, Redis or Postgres is required.

---

## Project status

BloxOS is **pre-1.0** and currently powering the author's homelab fleet of ~10 machines across Proxmox, Synology, Windows, Linux, and Mac. It's stable enough to be the only dashboard I look at, but not yet documented enough for a stranger to install without reading source.

**What's solid:**
- Hub, Linux agent, Windows agent — all three run continuously on my fleet
- Signed auto-update with monotonic protection on protocol-2 agents; recovery differs by OS
- Hardware inventory, metrics, terminal, RBAC, themes, branding — all working
- Metadata-only AI session monitoring and 24-hour component power history

**What's not done:**
- Polished public one-line hub installer
- Mobile-responsive layout
- Historical metrics retention beyond the live ring buffer
- Custom alert rules UI (alerts exist; the rule editor isn't shipped)
- Audit log
- Broader disaster-recovery automation (a consistent backup helper and clean restore procedure ship)

**What's planned for v1.0:**
- Polished installer flow for hub and agents
- Documentation site
- Mobile responsive pass
- Custom alert rules editor
- Longer-term history beyond the shipped 24-hour component power chart

See [the roadmap](#roadmap) for what's coming after v1.0.

---

## Roadmap

Beyond v1.0:

- **Audit log** — every privileged action recorded with operator + timestamp
- **Fleet-wide full-text search** — across machine names, notes, tags, hardware
- **Historical metrics** — opt-in long-term storage with downsampling
- **Custom dashboard layouts** — drag-to-arrange machine cards, save layouts per user
- **Alert delivery** — webhooks (Discord, Slack, Telegram, generic)
- **API tokens** — long-lived tokens for automation scripts, with scope restrictions
- **Plugin system for API-polled machines** — first-class Synology, Proxmox, UniFi, TrueNAS support

If something on this list matters more to you than the others, [open an issue](https://github.com/bokiko/bloxos/issues) and tell me — operator pull is how priorities move.

---

## Comparison

|  | BloxOS | Datadog | Grafana stack | Cockpit |
|---|---|---|---|---|
| Self-hosted | ✅ | ❌ | ✅ | ✅ |
| Single binary install | ✅ | n/a | ❌ | ✅ |
| Linux + Windows agents | ✅ | ✅ | partial | ❌ |
| Live web terminal | ✅ | ❌ | ❌ | ✅ |
| Hardware inventory | ✅ | ❌ | ❌ | partial |
| Multi-user RBAC | ✅ | ✅ | ✅ | ❌ |
| Auto-updating agents | ✅ | ✅ | ❌ | ❌ |
| Fleet view | ✅ | ✅ | ✅ | ❌ |
| No subscription | ✅ | ❌ | ✅ | ✅ |

BloxOS isn't trying to compete with Datadog on enterprise observability or with Grafana on time-series visualization. It's competing for the **single operator running their own infrastructure** who wants one tool that does the operator-facing job well.

---

## Naming note

There is a separate, unrelated project also called BloxOS by [BotBlox](https://github.com/botblox/bloxos-releases) — embedded Linux for industrial Ethernet switches. Different audience entirely (hardware firmware vs. fleet management). If you're looking for switch firmware, that's not this. If you're looking for the homelab dashboard, you're in the right place.

---

## Contributing

The project is open source under the Apache 2.0 license. Issues, ideas, and pull requests are welcome.

If you're building something on BloxOS or want a feature added, the fastest path is to open an issue describing the use case before opening a PR — that way we can talk about the shape of the change before code happens.

---

## License

[Apache License 2.0](LICENSE) — permissive, includes an explicit patent grant, requires preserving the `NOTICE` file when redistributing. Use it commercially or personally; modify it, fork it, ship it inside another product. Just don't sue contributors over patents and don't strip the attribution.

---

## Author

Built by [Bokiko](https://bokiko.io) — infrastructure between hardware and intelligence.

- 🌐 [bokiko.io](https://bokiko.io)
- 🐦 [@Bokiko](https://x.com/Bokiko)
- ✍️ [Medium](https://medium.com/@bokiko)
- 📧 Open an [issue](https://github.com/bokiko/bloxos/issues) for project-specific contact

If BloxOS makes your homelab quieter to operate, ⭐ the repo. That's the only marketing this project will ever do.
