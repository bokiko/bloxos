# Backup and restore

## Compose (supported deployment)

Use a brief maintenance window. The hub uses SQLite WAL: copying only
`bloxos.db` while it is running can miss committed data. The database alone is
also not the hub's identity. Keep the **whole hub data directory**, Caddy data
(including the original CA private key), and Caddy configuration together.

From the deployment's `docker/` directory, with the same environment and
Compose options you normally use:

```bash
bash ../scripts/backup-compose.sh /absolute/path/to/new-backup-directory
# For a named project/override, append your usual global options:
# bash ../scripts/backup-compose.sh /absolute/path/to/new-backup-directory -p my-bloxos -f compose.yaml -f override.yaml
```

Requires Docker Compose, Bash, Python3, tar and `shasum`. The destination must
not exist; its parent must be owned by the invoking OS user and not writable
by group/others (use a private backup directory, not `/tmp` directly).
The script stops hub and Caddy, copies their complete state, then
restarts only services that were running before the backup. Agents reconnect
when the hub returns; the dashboard can temporarily show disconnected data.
No volume, database, release floor, or key is deleted. A failure leaves the
partial directory for inspection; a complete backup has an atomically finalized
`SHA256SUMS` with all five entries verified. `.pending` is not complete.

Overlapping backups of the same hub are refused using an exclusive, stopped
Docker lock container; it never runs code and has no network. Normal exit
removes it. A killed client/daemon failure can leave this lock behind: inspect
the `bloxos-backup-lock-<hub-container-id>` container and verify no backup is
still active before removing that exact lock container with `docker rm -v`.
Do not remove the hub container or replace the stack during a backup.

This procedure assumes the shipped `/data` and `/config` mounts have no other
writers. Do not run another hub against the same database. Prevent another
operator/orchestrator from restarting the stack during the maintenance window.
Custom external signing-key mounts, environment-supplied JWT secrets, external
databases, and custom proxy deployments require their corresponding state too.
Separately save `.env`, override files and externally mounted keys in your
encrypted backup store. The rendered configuration and container metadata in
the backup are private reference material; they may contain credentials.

Copy the backup to encrypted off-host storage. Checksums detect accidental
damage, not a malicious replacement; restore only your own trusted archives.
Test restoration periodically. Losing the update-signing key or CA can strand
enrolled agents; never generate replacements as a shortcut during recovery.

## Restore into a clean, isolated Compose project

Do not extract over an existing deployment. Keep the original volumes and
backup unchanged until the restored app has been validated. On a replacement
host, install the same tested BloxOS revision/images and recover your original
`.env`, overrides and external secrets. Do not upgrade during disaster recovery.
Keep the replacement isolated from enrolled machines until validation is done:
starting a hub with newer served agent bytes can initiate fleet updates.

Verify the archives first:

```bash
cd /absolute/path/to/backup-directory
shasum -a 256 -c SHA256SUMS
```

In the recovered checkout's `docker/` directory, use a **new, unused project
name**, without overrides that reuse existing/external volume names. Set
`HUB_HOST` to the original hostname and arrange isolated DNS/routing for the
test. Build or pull the original images before continuing. `create` must not
be replaced by `up`: nothing may write to the volumes before restoration.

```bash
export COMPOSE_PROJECT_NAME=bloxos-restore-test
docker compose create
hub=$(docker compose ps -a -q hub)
caddy=$(docker compose ps -a -q caddy)
# Verify both containers are stopped and inspect their Mounts. All three
# named volumes must be newly created for this project, not production volumes.
docker inspect "$hub" "$caddy"
```

Only after verifying these exact targets, restore with ownership preserved:

```bash
backup=/absolute/path/to/backup-directory
docker cp -a - "$hub:/data" < "$backup/hub-data.tar"
docker cp -a - "$caddy:/data" < "$backup/caddy-data.tar"
docker cp -a - "$caddy:/config" < "$backup/caddy-config.tar"
docker compose up -d
docker compose ps
```

Verify login with an existing account (not first-boot setup), expected machine
rows/history/settings, and the original CA certificate and update public key.
Use an isolated test agent to verify reconnect/update trust. Do not share a
live agent's credentials with a second agent or reset any agent's signed
release floor. Keep the old deployment stopped when switching the original
hostname to its replacement; do not run two hubs with cloned identity against
the fleet. Resume live traffic only after the checks pass.

If an extraction fails, keep the target stopped. Do not start a partially
restored stack or retry over partially populated volumes; use another fresh
project and retain the failed target for inspection.

## Native deployment

Stop the hub service and any other database writers. Back up its whole working
data directory (including any `bloxos.db-wal`/`bloxos.db-shm` files), the hub
service account's `.bloxos` directory, environment/service configuration,
external signing keys, branding storage and reverse-proxy CA/configuration.
Paths differ from Compose: inspect your service unit and configuration.
Preserve ownership and permissions. Restart the original service afterward.
Restore only while the replacement is stopped, on the same tested build.
Never delete WAL files to make a backup or force a database to open.
