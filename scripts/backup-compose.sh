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

# No overwrite, including an existing empty directory or symlink.
mkdir -m 700 -- "$destination"
destination=$(cd "$destination" && pwd)
hub=$(compose "${compose_options[@]}" ps -a -q hub)
caddy=$(compose "${compose_options[@]}" ps -a -q caddy)
if [[ -z "$hub" || -z "$caddy" || "$hub" == *$'\n'* || "$caddy" == *$'\n'* ]]; then
  echo "Expected exactly one existing hub and Caddy container; backup incomplete." >&2
  exit 1
fi

# Save rendered configuration privately: it can contain credentials. This is
# reference material, not an automatically executable restore configuration.
compose "${compose_options[@]}" config > "$destination/compose-config.yaml"
docker inspect "$hub" "$caddy" > "$destination/containers.json"
restart=()
for service in hub caddy; do
  container=$hub
  [[ "$service" == caddy ]] && container=$caddy
  if [[ $(docker inspect -f '{{.State.Running}}' "$container") == true ]]; then
    restart+=("$container")
  fi
done
resume() {
  result=$?
  trap - EXIT
  if [[ ${#restart[@]} -gt 0 ]]; then
    if ! docker start "${restart[@]}"; then
      echo "Could not restart the original services. Run docker compose start and inspect health." >&2
      result=1
    fi
  fi
  [[ $result -eq 0 ]] || echo "Backup failed or services need attention; inspect $destination." >&2
  exit "$result"
}
trap resume EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
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
(cd "$destination" && shasum -a 256 hub-data.tar caddy-data.tar caddy-config.tar compose-config.yaml containers.json > SHA256SUMS)
echo "Backup complete: $destination (contains private keys; keep it private and encrypted off-host)."
echo "Also retain your deployment .env, override files, and any externally mounted keys; see docs/backup-restore.md."
