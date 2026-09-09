<div align="center">

<img src="dashboard/public/brand/bloxos-mark.svg" width="80" height="80" alt="BloxOS logo">

# BloxOS

**Your machines. One clear view.**

Self-hosted fleet management for Linux servers, Windows workstations, and AI machines.<br>
See what is running, understand your hardware, and manage your fleet from one dashboard.

[![Release](https://img.shields.io/github/v/release/bokiko/bloxos)](https://github.com/bokiko/bloxos/releases/latest)
[![CI](https://github.com/bokiko/bloxos/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/bokiko/bloxos/actions/workflows/ci.yml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

[Get started](#get-started) · [Dashboard gallery](docs/screenshots/README.md) · [Documentation](docs/README.md) · [Releases](https://github.com/bokiko/bloxos/releases) · [Report a bug](https://github.com/bokiko/bloxos/issues/new/choose)

</div>

![Operations Wall showing connected Linux and Windows demo machines, fleet resource usage, and machine controls](https://cdn.jsdelivr.net/gh/bokiko/bloxos@bca13c456ecc1e5bd89282f96486a43b602bc4f9/docs/screenshots/operations-wall.png)

*Actual v1.1.0 dashboard with synthetic demo machines. Missing GPU sensors display N/A; no private fleet data is shown.*

## One place to look. One place to act.

BloxOS brings the everyday work of running a homelab or small AI fleet together.
Host it on your own hardware, connect your machines, and open a browser.
No Kubernetes, Redis, or external database required.

| See your fleet | Operate it | Make it yours |
| --- | --- | --- |
| Live CPU, RAM, disk, GPU and freshness indicators | Linux web terminals with non-root shells and re-authentication | Three live layouts, each with Original, Bright and Dark colors |
| Hardware inventory with search, filters and exports | Machine actions, service and container controls where supported | Classic dashboard with eight palettes |
| Supported AI-tool session metadata across machines | Viewer, operator and admin permissions | Per-user preferences, pins and saved filters |
| 24-hour component power history with averages and sampled peaks | Native Linux and Windows agents with signed updates | Instance logo, favicon and welcome-message branding |

### Three designs. The same working app.

**Operations Wall** gives you an open fleet overview. **Grove Workspace** adds a
sidebar and a separate context column. **Precision Console** puts the machine
table first.

Choose **Account menu → Design**, then a color. Navigation continues through
machine details, inventory, AI Sessions, versions and settings. Existing
accounts keep Classic until they choose another design.

| Grove Workspace · Original | Precision Console · Bright |
| --- | --- |
| [![Grove Workspace dashboard with synthetic demo machines](https://cdn.jsdelivr.net/gh/bokiko/bloxos@bca13c456ecc1e5bd89282f96486a43b602bc4f9/docs/screenshots/grove-workspace.png)](https://cdn.jsdelivr.net/gh/bokiko/bloxos@bca13c456ecc1e5bd89282f96486a43b602bc4f9/docs/screenshots/grove-workspace.png) | [![Precision Console dashboard with synthetic demo machines](https://cdn.jsdelivr.net/gh/bokiko/bloxos@bca13c456ecc1e5bd89282f96486a43b602bc4f9/docs/screenshots/precision-console.png)](https://cdn.jsdelivr.net/gh/bokiko/bloxos@bca13c456ecc1e5bd89282f96486a43b602bc4f9/docs/screenshots/precision-console.png) |

[See all three dashboards →](docs/screenshots/README.md) · [Design and color guide →](docs/themes/README.md)

### Machines stay where you put them

Name sorting no longer moves machines when connectivity changes. Choose
**Arrange machines**, drag or use the arrows, then **Save order**. Your **My
order** is saved per account across grid/list and all four layouts. New machines
appear after your saved set. [Machine arrangement guide →](docs/machine-order.md)

### AI activity, without reading the conversation

AI Sessions reports supported running tools—Claude Code, Codex and Kimi—with
project basenames, explicitly detected model information, and inferred activity
states. Detection has limits; it is not a universal list of every local model.

It is **read-only metadata monitoring**: no prompts, responses, transcripts,
terminal output or full project paths. Administrators can disable it fleet-wide;
a machine can opt out with `BLOXOS_AI_SESSIONS=0`.

### Power readings you can interpret

Component sensors are sampled locally every second, collected into 30-second
averages and sampled peaks, and retained in a rolling 24-hour history.
Unavailable readings stay unavailable, and incomplete totals are labelled.

**Component power is not wall power.** CPU and GPU readings do not include every
part of a machine or power-supply losses. [How power history works →](docs/power-history.md)

### Know what your agents are running

The **Versions** page shows the agent builds your hub offers for Linux x86-64,
Linux ARM64 and Windows x86-64, including missing platform binaries. It
distinguishes numbered releases, legacy unnumbered builds and unknown versions.
**Matches offered build** means a machine matches its hub's offered file—not
necessarily the newest BloxOS release. Older hubs without enough information
show an unknown status instead of guessing. [Version labels explained →](docs/versions.md)

An operator's **Pause rollout** is saved across hub restarts and changes to
served agent files in v1.2.1 and later. It stops new update announcements; it
cannot cancel updates already announced. Downgrading to an older hub loses
enforcement of that saved pause. [Rollout control →](docs/versions.md#rollout-control)

## Get started

You need a Docker host with **Docker Compose v2**, Git, and a hostname or IP
address reachable by your browser and managed machines. The packaged hub and
dashboard support **Linux amd64 and arm64**. Ports **80 and 443** must be available.

### 1. Download BloxOS

```sh
git clone --branch v1.2.1 --depth 1 https://github.com/bokiko/bloxos.git
cd bloxos/docker
cp .env.example .env
```

### 2. Set your address

Open `.env` in a text editor. Set `HUB_HOST` to your Docker host's reachable
hostname or IP—without `https://`, a path, or a port—and add the version:

```dotenv
HUB_HOST=192.168.1.50
BLOXOS_VERSION=1.2.1
```

Replace the example IP with your own address. Do not use `localhost` if other
machines need to reach this hub.

### 3. Start it

```sh
docker compose pull
docker compose up -d --no-build
docker compose exec hub cat /data/.bloxos/setup-token
```

Open **`https://<HUB_HOST>`**, enter the setup token, and create your admin
account. There is no shared default username or password.

The default stack uses a private certificate authority. Your browser will need
to trust its root certificate; follow the [browser trust instructions](docker/README.md#browser-trust).
The generated agent command already includes the required verification.

Using a publicly trusted certificate, such as Let's Encrypt, instead of the
default private CA? Leave `BLOXOS_CA_CERT` unset in the hub configuration.
An invalid explicit CA setting stops install-command generation with an error;
private-CA installations should keep their correct CA configuration.
[TLS configuration guidance →](docs/configuration.md#tls-trust-for-onboarding)

[Full installation guide and troubleshooting →](docker/README.md)

## Add your first machine

1. In the dashboard, choose **Add Machine**.
2. Select **Linux** or **Windows**.
3. Copy the generated command and run it on that machine.

Linux onboarding is **one copy-and-paste line**. Windows uses the generated
PowerShell command. The installer sets up the native service; the machine then
appears in your fleet. Enrollment links expire after 15 minutes—generate a new
one if needed, and do not share them publicly.

Linux agents support amd64 and arm64 with systemd; the packaged Windows agent
is amd64. Installation needs administrative privileges. The Linux service runs
as root and the Windows service as LocalSystem; Linux terminal sessions run as
a configured **non-root** user.

This command enrolls a machine into an existing hub. It does not install a
second hub. A public one-line *hub* installer is not shipped.

## Update an existing installation

**Back up first.** Use the [backup and restore guide](docs/backup-restore.md),
which preserves the database, secrets, signing identity and Caddy CA. Never
use `docker compose down -v` to update.

In your existing Compose directory, set `BLOXOS_VERSION=1.2.1` in your existing
`.env`, then run these with the same project name and any existing overrides:

```sh
docker compose pull hub dashboard
docker compose up -d --no-build hub dashboard
```

Refresh your browser when the services are healthy. Keep your existing volumes
and keys; normal upgrades do not require enrolling every machine again.

For new machines, generate a **fresh** Add Machine command after upgrading.
Since v1.2.0, new commands use `/api/join/`, so older proxies that already forward
`/api/*` need no route edit. Previously copied `/join/` commands may still fail
or have expired.
See the [Docker upgrade guide](docker/README.md#upgrades) for details.

The hub serves agent updates too: eligible older agents can update and restart
after a hub upgrade. Legacy agents may need update-key pinning; offline-signing
installations have a separate procedure. Very old or customized deployments
should compare their Compose configuration before updating.

On a native installation, replacing the hub executable does **not** replace
separate agent files. Use the [native agent check-and-stage guide](docs/native-agent-upgrades.md)
to prepare the published payloads without starting a fleet rollout.

[Release notes](https://github.com/bokiko/bloxos/releases/tag/v1.2.1) ·
[Update signing](docs/offline-update-signing.md) ·
[Agent recovery](docs/agent-update-recovery.md)

## How it fits together

```text
Linux / Windows agents ── outbound WebSocket ── Go hub + SQLite
                                                    │
                                           Caddy HTTPS proxy
                                                    │
                                            Browser dashboard
```

Agents initiate the connection; managed machines need no inbound agent port.
The browser uses the hub API and an SSE stream. The Compose stack packages
Caddy, the hub, and the Next.js dashboard; agents remain native services.
API-polled integrations are also supported where an adapter is available.

[Architecture](docs/architecture.md) · [Configuration reference](docs/configuration.md) · [Source development](docs/development.md)

## Know the boundaries

- Web terminals are **Linux-only**. Only session metadata is audited, not terminal content.
- Hardware and sensor coverage depends on the OS, device and available drivers.
- AI Sessions is live metadata, not conversation playback, remote AI control, or session history.
- Power history covers 24 hours of component readings, not long-term observability or a wall-power meter.
- Signed updates and protocol-2 rollback protection are shipped; recovery differs between Linux and Windows.
- A full product-wide audit log, custom alert-rule editor, and public one-line hub installer are not shipped.

For current priorities, use the [issue tracker](https://github.com/bokiko/bloxos/issues).
[BLOXOS_FUTURE.md](BLOXOS_FUTURE.md) is a collection of longer-term ideas, not a
list of available features or a release commitment.

## Documentation and contributing

Start with the [documentation index](docs/README.md) for installation,
configuration, backups, power history, alerts, designs, and development.

Bug reports and focused pull requests are welcome. For larger changes, open an
issue describing the problem first. Read [CONTRIBUTING.md](CONTRIBUTING.md) and
the [Code of Conduct](CODE_OF_CONDUCT.md). Report vulnerabilities privately
using [SECURITY.md](SECURITY.md), not public issues.

## License and credits

[Apache License 2.0](LICENSE). Built by [Bokiko](https://bokiko.io).

This fleet-management project is unrelated to BotBlox's similarly named
Ethernet-switch firmware.
