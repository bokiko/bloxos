#!/usr/bin/env bash
# Consistent, offline backup of the shipped Compose deployment.
# Run from docker/; extra arguments are Compose global options (e.g. -p NAME).
set -euo pipefail
umask 077

if [[ $# -lt 1 ]]; then
  echo "Usage: bash ../scripts/backup-compose.sh NEW_BACKUP_DIRECTORY [compose options...]" >&2
  exit 2
fi
destination=$1
shift
compose() { docker compose "$@"; }
compose_options=("$@")

# The destination's parent must not let another OS user replace the newly
# created private directory with a symlink. Same-user hostile processes are
# outside this local backup boundary (they can already read its private keys).
python3 -c 'import os,stat,sys; p=os.path.dirname(os.path.abspath(sys.argv[1])); s=os.stat(p); sys.exit(0 if s.st_uid == os.getuid() and not s.st_mode & 0o022 else "Backup parent must be owned by this user and not group/world writable")' "$destination"
# No overwrite, including an existing empty directory or symlink.
mkdir -m 700 -- "$destination"
destination=$(cd "$destination" && pwd)
hub=$(compose "${compose_options[@]}" ps -a -q hub)
caddy=$(compose "${compose_options[@]}" ps -a -q caddy)
if [[ -z "$hub" || -z "$caddy" || "$hub" == *$'\n'* || "$caddy" == *$'\n'* ]]; then
  echo "Expected exactly one existing hub and Caddy container; backup incomplete." >&2
  exit 1
fi

# Docker container names are exclusive on the daemon, including across
# separate clients/OS users. This stopped, network-less lock container never
# executes the hub. Acquire BEFORE observing service running state: otherwise
# overlapping backups can restart the other's supposedly stopped database.
lock=$(docker create --name "bloxos-backup-lock-$hub" --network none --entrypoint /bin/true "$(docker inspect -f '{{.Image}}' "$hub")")
restart=()
resume() {
  result=$?
  trap - EXIT
  if [[ ${#restart[@]} -gt 0 ]]; then
    if ! docker start "${restart[@]}"; then
      echo "Could not restart the original services. Run docker compose start and inspect health." >&2
      result=1
    fi
  fi
  if ! docker rm -v "$lock" > /dev/null; then
    echo "Could not remove backup lock container $lock; inspect it before another backup." >&2
    result=1
  fi
  [[ $result -eq 0 ]] || echo "Backup failed or services need attention; inspect $destination." >&2
  exit "$result"
}
trap resume EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
# Save rendered configuration privately: it can contain credentials. This is
# reference material, not an automatically executable restore configuration.
compose "${compose_options[@]}" config > "$destination/compose-config.yaml"
docker inspect "$hub" "$caddy" > "$destination/containers.json"
for service in hub caddy; do
  container=$hub
  [[ "$service" == caddy ]] && container=$caddy
  if [[ $(docker inspect -f '{{.State.Running}}' "$container") == true ]]; then
    restart+=("$container")
  fi
done
compose "${compose_options[@]}" stop hub caddy
for container in "$hub" "$caddy"; do
  [[ $(docker inspect -f '{{.State.Running}}' "$container") == false ]]
done

# Copy complete stopped directories, not just the main SQLite file. This
# preserves any WAL and the file-backed JWT, setup and update-signing identity.
# Docker transports the archives even when the daemon is on another host.
docker cp "$hub:/data/." - > "$destination/hub-data.tar"
docker cp "$caddy:/data/." - > "$destination/caddy-data.tar"
docker cp "$caddy:/config/." - > "$destination/caddy-config.tar"
for archive in hub-data caddy-data caddy-config; do
  tar -tf "$destination/$archive.tar" > /dev/null
done
(cd "$destination" &&
  shasum -a 256 hub-data.tar caddy-data.tar caddy-config.tar compose-config.yaml containers.json > SHA256SUMS.pending &&
  python3 -c 'from pathlib import Path; p=Path("SHA256SUMS.pending"); lines=p.read_text().splitlines(); expected=["hub-data.tar","caddy-data.tar","caddy-config.tar","compose-config.yaml","containers.json"]; assert len(lines)==5 and all(len(line.split())==2 and len(line.split()[0])==64 and all(c in "0123456789abcdef" for c in line.split()[0]) and line.split()[1]==name for line,name in zip(lines,expected))' &&
  shasum -a 256 -c SHA256SUMS.pending > /dev/null &&
  mv SHA256SUMS.pending SHA256SUMS)
echo "Backup complete: $destination (contains private keys; keep it private and encrypted off-host)."
echo "Also retain your deployment .env, override files, and any externally mounted keys; see docs/backup-restore.md."
