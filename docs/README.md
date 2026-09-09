# BloxOS documentation

Start with the [project overview](../README.md) or choose the task you need.

## Install and operate

| Task | Guide |
| --- | --- |
| Install the hub, trust HTTPS, add machines | [Docker Compose deployment](../docker/README.md) |
| Upgrade an existing installation | [Update instructions](../README.md#update-an-existing-installation) and [release notes](https://github.com/bokiko/bloxos/releases) |
| Back up or restore your hub and its identity | [Backup and restore](backup-restore.md) |
| Configure the hub and agents | [Configuration reference](configuration.md) and [environment template](../.env.example) |
| Diagnose a refused agent update or recover an agent | [Agent update recovery](agent-update-recovery.md) |
| Read the Versions page labels (served builds, rollout states) | [Agent versions and rollout](versions.md) |
| Operate without a hub-held signing key | [Offline update signing](offline-update-signing.md) |

## Explore the app

| Topic | Guide |
| --- | --- |
| See the three live dashboards | [Screenshot gallery](screenshots/README.md) |
| Choose a layout and colors | [Design guide](themes/README.md) |
| Keep machines in your preferred positions | [Machine arrangement](machine-order.md) |
| Understand component power, retention and missing sensors | [Power history](power-history.md) |
| Understand alert timing, acknowledgment and recovery | [Alert lifecycle](alerts.md) |
| Understand AI monitoring privacy and limitations | [AI Sessions overview](../README.md#ai-activity-without-reading-the-conversation) |

## Develop and contribute

- [Development setup](development.md)
- [Architecture](architecture.md)
- [Dashboard development](../dashboard/README.md)
- [Contribution guide and checks](../CONTRIBUTING.md)
- [Engineering contracts](../AGENTS.md)
- [Security reporting](../SECURITY.md)

## Repository map

| Directory | Purpose |
| --- | --- |
| `hub/` | Go API, SQLite state, enrollment and fleet coordination |
| `agent/` | Native Linux and Windows services |
| `proto/` | Shared protocol contracts |
| `dashboard/` | Next.js application; canonical logo SVGs in `public/brand/` |
| `docker/` | Supported Compose deployment and proxy configuration |
| `scripts/` | Installation, backups, smoke tests and sample services |
| `docs/` | User guides, engineering references and screenshots |
| `.github/` | CI workflows and contribution templates |

The [future ideas document](../BLOXOS_FUTURE.md) is exploratory. Use release notes
for shipped changes and the issue tracker for active work.
