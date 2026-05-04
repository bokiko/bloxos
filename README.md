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
- **Dashboard** is Next.js, served by the hub or hosted separately. The dashboard never talks to agents directly; it always goes through the hub.
- **API-polled targets** (Synology, Proxmox, anything without a native agent) are scraped by the hub on a schedule and surfaced as machines in the same dashboard.

### Why this shape

A central hub means agents don't need inbound network access — they punch out to the hub from wherever they live. Works through NATs, behind home routers, across Tailscale, across UniFi VLANs. The hub is the only thing that needs a stable address.

WebSocket from agents to hub means real-time, bidirectional. The hub can push commands (`refresh_metrics`, `run_command`, `open_terminal`) without polling. Agents can stream metrics without scrape intervals.

SSE from hub to browser means the dashboard updates live without WebSocket complexity in the frontend. SSE survives proxies and corporate firewalls better than WebSocket. Reconnect is automatic.

---

## Quick start

> **Status:** BloxOS is currently being prepared for public release. The instructions below are the target shape. The full installer is in active development — see [Project status](#project-status) below.

### 1. Install the hub

On any Linux machine you want as the central server, build and run from source:

```bash
git clone https://github.com/bokiko/bloxos.git
cd bloxos/hub && go build -o bloxos-hub .
./bloxos-hub
```

This starts the hub on port `:4000`. On first run it creates a setup token in `~/.bloxos/setup-token`; visit the dashboard to complete admin setup.

A one-line installer is on the v1.0 roadmap (see [Project status](#project-status)).

### 2. Open the dashboard

Browse to `http://<your-hub-ip>:8080` and log in with the credentials printed in step 1. Change the admin password immediately. Set a custom logo if you want one.

### 3. Add your first machine

Click **Add Machine** in the dashboard. Pick **Linux** or **Windows**. Copy the displayed install command. Run it on the target machine. The agent installs, registers, and shows up in the dashboard within seconds.

For Linux:
```bash
curl -fsSL https://<your-hub>/install/agent?token=<one-time-token> | sudo bash
```

For Windows (PowerShell as Administrator):
```powershell
iwr -useb https://<your-hub>/install/agent.ps1?token=<one-time-token> | iex
```

### 4. That's it

The agent self-updates from now on. Add more machines the same way.

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
| Deployment | Single Go binary for the hub, single binary for each agent, static Next.js bundle served by the hub |

No Docker required. No Kubernetes. No Redis. No Postgres. No external services. Three binaries and a SQLite file.

---

## Project status

BloxOS is **pre-1.0** and currently powering the author's homelab fleet of ~10 machines across Proxmox, Synology, Windows, Linux, and Mac. It's stable enough to be the only dashboard I look at, but not yet documented enough for a stranger to install without reading source.

**What's solid:**
- Hub, Linux agent, Windows agent — all three run continuously on my fleet
- Auto-update with rollback — battle-tested through several agent updates
- Hardware inventory, metrics, terminal, RBAC, themes, branding — all working
- ~20,000 lines of dashboard code, ~5,000 lines of Go across hub and agents

**What's not done:**
- Public installer scripts (the `curl | bash` flows above)
- Mobile-responsive layout
- Historical metrics retention beyond the live ring buffer
- Custom alert rules UI (alerts exist; the rule editor isn't shipped)
- Audit log
- Backup/restore tooling beyond "copy the SQLite file"

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
