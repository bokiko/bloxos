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
replaced. At boot, native installations hold the public proxy behind a recovery
gate. Compose updates temporarily disable automatic restarts on the selected
BloxOS containers and restore their original policies after recovery. Neither
path reopens a legacy server before the restore is durably committed.

Backups remain under `/var/lib/bloxos-updater/state`; they are private to root
and are not automatically deleted. Monitor free disk space and retain backups
according to your policy. These local recovery snapshots do not replace your
independent [backups](backup-restore.md).

## Check progress or diagnose a failure

```sh
sudo bloxos-update status
sudo journalctl -u bloxos-updater --no-pager -n 80
sudo journalctl -u bloxos-updater-recovery --no-pager -n 80
```

If preparation fails, the existing installation stays in place. If rollback
cannot finish, maintenance stays enabled: do not remove the maintenance marker
or delete updater state to force the website online. Preserve the logs and
backup and correct the host problem. Retry journal-driven recovery with:

```sh
sudo systemctl restart bloxos-updater-recovery.service bloxos-updater.service
```

The recovery gate fails closed if its configuration is missing or invalid, or
an existing journal is corrupt; do not delete these files to bypass it. A successfully completed update
normally has no active transaction journal—only retained backups.

Compose setup saves a root-private, resolved configuration and a separate
updater override. Do not run a different Compose project or omit that override
to manage an updater-controlled installation. Configuration changes and unusual
layouts require an operator to reconcile the saved deployment configuration;
the dashboard deliberately cannot submit paths, shell commands or image URLs.

## Server updates versus agent updates

**A server update now carries the agent payloads too.** Both server archives
ship all three agent binaries beside the hub executable, and the updater
transports them like any other file in the release — there is no separate agent
step and nothing to stage by hand. Compose images carry the same bundle.

This is a change. The updater used to replace only the hub and dashboard, so a
freshly updated hub kept offering whatever agent files were already on disk;
machines reported "matches offered build" and were correct, because the offer
itself was stale.

### Three reasons the fleet can still sit on old agents

They look identical on the Versions page until you read the right field, and
the remedies are completely different.

| What you see | What it means | Remedy |
| --- | --- | --- |
| **Delivery: System paths** or **Operator-managed** | The hub is not managing agents. Either it is a source build with no bundle, or `BLOXOS_AGENT_DELIVERY=external` is set, or an operator pin such as `BLOXOS_AGENT_BINARY` takes precedence. Upgrading the hub will not change the offer. | Remove the pin, or follow the [manual staging guide](native-agent-upgrades.md). |
| **Signing: Disabled**, or a blocked-rollout reason | New agent bytes are present and the hub cannot authorise them. An offline install holds no private key, and no signature has been staged for these exact bytes. | Sign those bytes offline and place the signature by content address — see [native agent delivery](native-agent-upgrades.md#signatures). Nothing is wrong with the binaries. |
| **Delivery: Broken** | The bundle failed validation. A packaged hub in this state refuses to start at all, so you will normally see a failed update and a rollback, not a running hub. | Read the hub's startup log; it names the payload and the reason. |

The first is a configuration choice, the second is withheld authorisation, and
the third is a bad artifact. Only the second leaves a healthy hub deliberately
holding an update back.

### Hub rollback and agent rollback are different things

They share a word and nothing else.

**Hub rollback** is the updater's. If a candidate release fails its readiness
check — including a bad or missing agent bundle, which is refused before the
database is even opened — the updater restores the previous release directory
and restarts the old hub. It is automatic, local to the server, and affects no
machine in the fleet.

**Agent rollback protection** is each agent's own. A protocol-2 agent keeps a
durable floor of the release number and SHA it is running, and refuses any
offer that is older, or that carries the same release number with different
bytes. Nothing on the hub can lower that floor, which is deliberate: it is what
stops a compromised or confused hub from pushing an old agent back onto the
fleet. Older protocol-1 agents do not enforce it.

The practical consequence: rolling the hub back does NOT roll the fleet's
agents back. Machines that already took an update keep the newer agent, and a
rolled-back hub offering older bytes will simply be refused by them. Plan an
agent change as a forward-only step.

Offline-signing installations retain their installation-specific signing
process. See [update signing](offline-update-signing.md).
