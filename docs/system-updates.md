# Update BloxOS

For an installation with the host updater configured:

```sh
sudo bloxos-update update
```

Or sign in as an administrator and open **Settings → Updates → Update BloxOS**.
Both start the same host worker. Closing your browser or SSH connection does
not cancel an update. The worker selects the latest stable GitHub release;
prereleases are never selected automatically.

## Enable updates on an older installation

The dashboard cannot grant itself root access. An administrator must install
the worker once on the **server hosting BloxOS**, not on each managed machine.
Run this from the existing BloxOS checkout (or its `docker` directory):

```sh
updater=$(mktemp) && curl --proto '=https' --tlsv1.2 -fL https://github.com/bokiko/bloxos/releases/latest/download/bloxos-update -o "$updater" && sudo python3 "$updater" update
```

This downloads the official release helper over verified HTTPS. Review the
downloaded file before running it if your operating policy requires that.
Setup displays the detected installation and asks for confirmation. It installs
`bloxos-update` and its systemd worker, then updates that installation. Later,
use the short command above or the dashboard button. Python 3.10 or newer is
required. An existing native deployment also needs its supported Node runtime.

The button updates the hub and dashboard. To refresh the separately installed
host helper itself when release notes call for it, rerun the bootstrap command;
it preserves the existing deployment configuration and refuses to replace a
worker that is currently running a transaction.

The worker supports the standard Linux/systemd native hub and dashboard behind
Caddy, or the standard local Docker Compose stack with its named data volumes.
It verifies which installation serves the configured public HTTPS address.
It refuses unsupported or ambiguous layouts instead of starting a second stack
or switching your proxy. Remote/rootless Docker daemons and custom proxies need an
operator-managed upgrade; see [upgrade verification](verified-upgrades.md).

## What happens during an update

1. Resolve the release and stage both components before downtime. Compose uses
   immutable image digests; native deployments use checksum-verified server
   archives extracted from those published images.
2. Stop public traffic and both components, then back up deployment data.
3. Install the candidate pair and start it in maintenance mode.
4. Verify both components directly and through the public URL, including their
   release, source revision and running-instance identity.
5. Accept the update, or restore the previous pair and coherent data snapshot.

Maintenance briefly makes the application unavailable. Databases, authentication
identity and Caddy CA are preserved; normal updates do not require enrolling
your fleet again. Native source checkouts and untracked user files are not
replaced. An interrupted transaction is recovered by the host worker at boot.

Backups remain under `/var/lib/bloxos-updater/state`; they are private to root
and are not automatically deleted. Monitor free disk space and retain backups
according to your policy. These local recovery snapshots do not replace your
independent [backups](backup-restore.md).

## Check progress or diagnose a failure

```sh
sudo bloxos-update status
sudo journalctl -u bloxos-updater --no-pager -n 80
```

If preparation fails, the existing installation stays in place. If rollback
cannot finish, maintenance stays enabled: do not remove the maintenance marker
or delete updater state to force the website online. Preserve the logs and
backup, correct the host problem, and restart `bloxos-updater.service` to retry
journal-driven recovery.

Compose setup saves a root-private, resolved configuration and a separate
updater override. Do not run a different Compose project or omit that override
to manage an updater-controlled installation. Configuration changes and unusual
layouts require an operator to reconcile the saved deployment configuration;
the dashboard deliberately cannot submit paths, shell commands or image URLs.

## Server updates versus agent updates

The native server updater replaces the hub and dashboard, not separately
installed agent payloads. Use the [native agent guide](native-agent-upgrades.md)
for those files. Compose images include agent payloads, so a new hub image can
announce eligible signed agent updates. Offline-signing installations retain
their installation-specific signing process. See [update signing](offline-update-signing.md).
