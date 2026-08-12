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
Metrics stream over WebSocket from agent to hub, then push to your browser via SSE. CPU, RAM, GPU usage, GPU power, thermals, network throughput, disk I/O — all updating multiple times per second. No 15-second scrape intervals. No "did it just freeze or is it just slow." When a value changes, you see it change.

### Real hardware inventory
Every agent collects DMI data, RAM modules (manufacturer, speed, slot, ECC status), GPU devices (model, VRAM, driver), PCI bus, network adapters, disks, BIOS, and motherboard. The fleet-wide `/inventory` page is sortable, filterable, groupable, and exports to CSV / JSON / Markdown. The first time you use it to find "every machine with less than 32GB RAM" in three seconds, you'll understand why it's there.

### Web terminal that actually works
xterm.js, theme-aware, stable 360px pane. Hit a machine, get a real shell. No SSH key juggling, no port forwarding, no "what was the IP again." The terminal session belongs to the operator's auth context, not the machine's.

### Two operating systems, one dashboard
The Linux agent and the Windows agent speak the same WebSocket protocol to the same hub. The Windows agent registers itself as a Windows Service via SCM, collects hardware via WMI, and supports the same auto-update flow. From the dashboard, a Windows machine and a Linux machine look and behave identically.

### Auto-update with rollback
The hub announces the agent's current SHA. Agents compare against their own binary. If different, they download the new version, verify the SHA, save the previous binary as `.prev`, atomically rename, and exit — systemd or SCM restarts them on the new version. If anything goes wrong, a recovery service swaps the previous binary back. A circuit breaker pauses rollout after two failures in five minutes.

You push an update to the hub. The fleet self-heals to the new version. Or rolls back. Either way, you don't get paged.

### Multi-user with real permissions
Viewers, operators, admins. Every endpoint has a scope (`fleet.read`, `fleet.control`, `fleet.metadata`, `fleet.admin`, `branding.admin`, `users.admin`). Operators can run actions but not change roles. Admins can change branding. Viewers can look but not touch. JWT-based, bcrypt for passwords.

### Personalization that respects your eyes
Five themes — BloxOS (default), Solarized, Dracula, Nord, Tokyo Night — each in light and dark variants where appropriate. Per-user. Plus org-wide custom branding: upload your own logo, favicon, and login welcome message. Density toggle (comfortable / compact). Pinned machines. Saved filters. Default views per user.

### Cmd+K everywhere
Command palette opens on `Cmd+K` (or `Ctrl+K`). Jump to any machine by name, run any action, open any setting, search inventory, switch themes. The palette is how operators actually use the system once they know it exists.

### One-file database
SQLite. The whole system — users, machines, metrics history, inventory, branding, preferences, saved filters, notes — lives in `bloxos.db`. Backup is `cp bloxos.db backup.db`. Restore is the inverse. No replication setup, no migration nightmares, no "which Postgres version are we on."

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
| `BLOXOS_AGENT_BINARY` | Recommended | Absolute Linux agent binary served by the hub. |
| `BLOXOS_AGENT_BINARY_WINDOWS` | No | Absolute Windows agent binary served by the hub. |
| `BLOXOS_UPDATE_PUBKEY` | No | Base64 Ed25519 public key for detached-signature mode. |
| `BLOXOS_UPDATE_SIGNING_KEY` | No | Explicit online-signing private-key path. |
| `BLOXOS_ALLOW_PRIVATE_TARGETS` | No | Set to `1` to permit API pollers to target RFC1918 addresses. |
| `BLOXOS_TELEGRAM_TOKEN` | No | Telegram bot token; both Telegram values are needed. |
| `BLOXOS_TELEGRAM_CHAT_ID` | No | Telegram destination chat ID. |
| `BLOXOS_HUB` | Agent | Hub base WebSocket URL; the agent appends `/ws/agent`. |
| `BLOXOS_SECRET` | Agent | Durable machine credential, normally managed by enrollment. |
| `BLOXOS_TOKEN` | Agent enrollment | One-time enrollment token. |
| `BLOXOS_TERMINAL_USER` | No | Linux account used for terminal sessions. |
| `BLOXOS_UPDATE_PUBKEY_PATH` | No | Override for the agent's pinned update-key file. |
| `BLOXOS_TLS_INSECURE` | Development only | TLS bypass available only in an agent built with `-tags insecure`. |
| `ProgramFiles` | Windows-provided | Used to discover NVIDIA tooling; normally never overridden. |
| `NEXT_PUBLIC_HUB_URL` | Dashboard | Hub origin when dashboard and hub are not same-origin. |

## Quick start from source

> **Status:** BloxOS is pre-1.0. The polished public installer is still in
> progress. This path builds the hub and Linux agent from source and keeps the
> first enrollment on loopback.

### 1. Clone and build

```bash
git clone https://github.com/bokiko/bloxos.git
cd bloxos
mkdir -p bin
(cd hub && go build -o ../bin/bloxos-hub .)
(cd agent && go build -o ../bin/bloxos-agent .)
```

To build the Windows agent artifact:

```bash
(cd agent && GOOS=windows GOARCH=amd64 go build -o ../bin/bloxos-agent.exe .)
```

Windows enrollment is under active rework and is intentionally not documented
as a runnable installer flow yet.

### 2. Configure and run the hub

These exports set the origin policy required for startup. The explicit
agent-binary path also ensures that the download endpoint serves the artifact
you just built.

```bash
export PUBLIC_URL=http://localhost:4000
export ALLOWED_ORIGINS=http://localhost:3000
export HUB_LISTEN=127.0.0.1:4000
export BLOXOS_AGENT_BINARY="$PWD/bin/bloxos-agent"
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
on this same machine. The loopback quick start avoids a remote first-contact
trust decision while the public bootstrap flow is being reworked. The agent
uses its one-time token once, stores a durable machine secret, and then appears
in the fleet.

For a LAN deployment, terminate TLS in front of the hub, set `PUBLIC_URL` to
that trusted HTTPS origin, set `ALLOWED_ORIGINS` to the dashboard origin, and
keep `BLOXOS_AGENT_BINARY` on an explicit absolute path. A safe cold-start
bootstrap for a private or self-signed CA remains tracked in
[#150](https://github.com/bokiko/bloxos/issues/150).

### 5. Production builds and sample services

```bash
(cd hub && go build -o ../bin/bloxos-hub .)
(cd agent && go build -o ../bin/bloxos-agent .)
(cd dashboard && pnpm install --frozen-lockfile && pnpm build)
```

The sample units in [scripts/systemd](scripts/systemd) use `<user>` and
`/opt/bloxos` placeholders. Replace `<user>`, install the built artifacts,
then enable each installed unit:

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

No Docker required. No Kubernetes. No Redis. No Postgres. No external services. A hub binary, agent binaries, a dashboard service, and a SQLite file.

---

## Project status

BloxOS is **pre-1.0** and currently powering the author's homelab fleet of ~10 machines across Proxmox, Synology, Windows, Linux, and Mac. It's stable enough to be the only dashboard I look at, but not yet documented enough for a stranger to install without reading source.

**What's solid:**
- Hub, Linux agent, Windows agent — all three run continuously on my fleet
- Auto-update with rollback — battle-tested through several agent updates
- Hardware inventory, metrics, terminal, RBAC, themes, branding — all working
- ~20,000 lines of dashboard code, ~5,000 lines of Go across hub and agents

**What's not done:**
- Polished public one-line hub installer
- Mobile-responsive layout
- Historical metrics retention beyond the live ring buffer
- Custom alert rules UI (alerts exist; the rule editor isn't shipped)
- Audit log
- Backup/restore tooling beyond "copy the SQLite file"
- Docker packaging

**What's planned for v1.0:**
- Polished installer flow for hub and agents
- Documentation site
- Mobile responsive pass
- Custom alert rules editor
- Per-machine power tracking chart (GPU first, total system later)

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
