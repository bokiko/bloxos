#!/usr/bin/env bash
# Release builder only. Extract canonical bytes; never rebuild or start a hub.
set -euo pipefail
if [[ $# != 4 ]]; then
  echo "usage: export-agent-bundle.sh IMAGE@sha256:DIGEST SOURCE_SHA vX.Y.Z NEW_OUTPUT_DIR" >&2
  exit 2
fi
image_ref=$1
source_sha=$2
release_tag=$3
output_dir=$4
[[ "$image_ref" =~ @sha256:[0-9a-f]{64}$ ]] || { echo "image must be digest-pinned" >&2; exit 2; }
[[ "$source_sha" =~ ^[0-9a-f]{40}$ ]] || { echo "source must be full commit SHA" >&2; exit 2; }
[[ ! -e "$output_dir" && ! -L "$output_dir" ]] || { echo "output already exists; refusing overwrite" >&2; exit 2; }
script_dir=$(cd -- "$(dirname -- "$0")" && pwd)
python3 "$script_dir/agent_bundle.py" validate-tag --version "$release_tag" >/dev/null
docker pull --platform linux/amd64 "$image_ref"
actual_source=$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$image_ref")
[[ "$actual_source" == "$source_sha" ]] || { echo "image source revision mismatch" >&2; exit 1; }
container_id=$(docker create --platform linux/amd64 --network none "$image_ref")
[[ "$container_id" =~ ^[0-9a-f]{64}$ ]] || { echo "invalid export container ID" >&2; exit 1; }
trap 'docker rm -v "$container_id" >/dev/null' EXIT
mkdir -m 0700 -- "$output_dir"
# Copy from the image's own managed bundle, under the canonical names. The
# legacy /usr/local/lib/bloxos paths are hard links to these same inodes, so
# either source yields identical bytes; taking them from here means the release
# catalog describes the very files the containerised hub serves, under the very
# names it serves them by.
docker cp "$container_id:/usr/local/bin/agents/bloxos-agent-linux-amd64" "$output_dir/bloxos-agent-linux-amd64"
docker cp "$container_id:/usr/local/bin/agents/bloxos-agent-linux-arm64" "$output_dir/bloxos-agent-linux-arm64"
docker cp "$container_id:/usr/local/bin/agents/bloxos-agent-windows-amd64.exe" "$output_dir/bloxos-agent-windows-amd64.exe"
manifest_sha=$(python3 "$script_dir/agent_bundle.py" manifest --bundle "$output_dir" \
  --source "$source_sha" --version "$release_tag" --image-digest "${image_ref##*@}")
printf '%s  agent-manifest.json\n' "$manifest_sha" > "$output_dir/agent-manifest.sha256"
python3 "$script_dir/agent_bundle.py" check --bundle "$output_dir" --manifest-sha256 "$manifest_sha"
