#!/usr/bin/env bash
# Power-history end-to-end smoke: the REAL agent binary and the REAL hub
# binary, built from this tree, running inside one disposable Docker
# container on its loopback. Exercises the whole pipeline:
#
#   process sampling (SYNTHETIC nvidia-smi) -> 30 s accumulator -> local
#   journal (fsync) -> WebSocket batch -> hub commit + ACK -> persisted ACK
#   cursor -> authenticated history API, then offline journaling, an agent
#   restart during the outage, and hub recovery with unchanged replayed data
#   and an explicitly declared sequence gap.
#
# By default this is NOT hardware verification: GPU readings come from a
# synthetic nvidia-smi fixture. PHSMOKE_REAL_GPU=1 instead checks two real
# NVIDIA GPUs through the container runtime; neither mode measures wall power.
# No host services or credentials are touched: no host
# credential dirs, no docker socket mount, no Compose stack, no published
# ports. The host-side footprint is one Docker build volume (removed unless
# KEEP=1), two containers removed on exit and, optionally, existing Go caches
# in. Every resource name carries a per-invocation unique suffix; a name
# collision aborts instead of reusing or deleting anything.
#
# Usage (any Linux host or VM with docker; rootless is fine, no sudo):
#   scripts/smoke/power-history.sh
# Environment:
#   PHSMOKE_PREFIX  name prefix ([a-z0-9][a-z0-9-]{0,23}, default phsmoke)
#   GO_IMAGE        golang image (default golang:1.25)
#   GOMOD_VOL / GOCACHE_VOL  optional named volumes with warm Go caches,
#                   mounted for the BUILD step only
#   KEEP=1          keep the build volume (the test container is always --rm)
#   PHSMOKE_TIMEOUT per-phase wait in seconds (default 180)
#   PHSMOKE_REAL_GPU=1 opt into a two-NVIDIA-GPU hardware canary instead of
#                   synthetic inputs; requires Docker GPU runtime support.
#
# Requires on the host: docker. Everything else runs in the container.
set -euo pipefail

P="${PHSMOKE_PREFIX:-phsmoke}"
GO_IMAGE="${GO_IMAGE:-golang:1.25}"
PHSMOKE_REAL_GPU="${PHSMOKE_REAL_GPU:-0}"
[[ "$PHSMOKE_REAL_GPU" == 0 || "$PHSMOKE_REAL_GPU" == 1 ]] || { echo "invalid GPU mode" >&2; exit 2; }
if ! [[ "$P" =~ ^[a-z0-9][a-z0-9-]{0,23}$ ]]; then
  echo "refusing: PHSMOKE_PREFIX must match [a-z0-9][a-z0-9-]{0,23}" >&2; exit 2
fi

# Cleanup-owned state is global so the EXIT trap can see it after outer()
# returns. Only resources this invocation actually created are ever removed,
# and containers are addressed by the ID docker returned, never by name.
CLEAN_TMP="" CLEAN_VOL="" CLEAN_BUILD_ID="" CLEAN_TEST_ID=""
cleanup() {
  [[ -n "$CLEAN_TEST_ID"  ]] && docker rm -f "$CLEAN_TEST_ID"  >/dev/null 2>&1 || true
  [[ -n "$CLEAN_BUILD_ID" ]] && docker rm -f "$CLEAN_BUILD_ID" >/dev/null 2>&1 || true
  if [[ -n "$CLEAN_VOL" ]]; then
    if [[ "${KEEP:-}" == "1" ]]; then echo "KEEP=1: build volume $CLEAN_VOL kept"
    else docker volume rm -f "$CLEAN_VOL" >/dev/null 2>&1 || true; fi
  fi
  [[ -n "$CLEAN_TMP" ]] && rmdir "$CLEAN_TMP" 2>/dev/null || true
}

# ---------------------------------------------------------------------------
# Outer driver (runs on the host / VM shell).
# ---------------------------------------------------------------------------
outer() {
  local repo run vbin testc buildc
  repo="$(cd "$(dirname "$0")/../.." && pwd)"
  trap cleanup EXIT
  # mktemp -d reserves the unique suffix for the life of this run.
  CLEAN_TMP="$(mktemp -d "${TMPDIR:-/tmp}/$P.XXXXXXXX")"
  run="$(basename "$CLEAN_TMP" | tr '[:upper:]' '[:lower:]' | tr -c '[:alnum:]\n' '-')"
  vbin="$run-bin" testc="$run-test" buildc="$run-build"
  echo "== power-history smoke (real_gpu=$PHSMOKE_REAL_GPU; real agent+hub binaries)"
  echo "   source: $repo (read-only)   run: $run   image: $GO_IMAGE"

  for n in "$testc" "$buildc"; do
    if docker ps -a --format '{{.Names}}' | grep -qx "$n"; then
      echo "refusing: container $n already exists" >&2; exit 2
    fi
  done
  if docker volume ls --format '{{.Name}}' | grep -qx "$vbin"; then
    echo "refusing: volume $vbin already exists" >&2; exit 2
  fi

  local cache_args=()
  for n in "${GOMOD_VOL:-}" "${GOCACHE_VOL:-}"; do
    [[ -z "$n" ]] && continue
    [[ "$n" =~ ^[a-zA-Z0-9][a-zA-Z0-9_.-]*$ ]] || { echo "refusing: caches must be named volumes" >&2; exit 2; }
    docker volume inspect "$n" >/dev/null || { echo "refusing: cache volume must already exist" >&2; exit 2; }
  done
  [[ -n "${GOMOD_VOL:-}" ]] && cache_args+=(-v "$GOMOD_VOL:/go/pkg/mod")
  [[ -n "${GOCACHE_VOL:-}" ]] && cache_args+=(-v "$GOCACHE_VOL:/root/.cache")

  echo "== build hub, agent and verifier into volume $vbin"
  CLEAN_VOL="$(docker volume create "$vbin")"
  CLEAN_BUILD_ID="$(docker create -i --name "$buildc" \
    -v "$repo:/src:ro" -v "$vbin:/out" "${cache_args[@]}" \
    -e PHSMOKE_CONTAINER=1 -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=0 \
    "$GO_IMAGE" bash -s -- build)"
  docker start -a -i "$CLEAN_BUILD_ID" < "$0"
  [[ "$(docker inspect -f '{{.State.ExitCode}}' "$CLEAN_BUILD_ID")" == 0 ]] || return 1
  docker rm -f "$CLEAN_BUILD_ID" >/dev/null; CLEAN_BUILD_ID=""

  echo "== run pipeline test in container $testc (loopback only, no ports, no host mounts)"
  local gpu_args=()
  [[ "$PHSMOKE_REAL_GPU" == 1 ]] && gpu_args+=(--gpus all -e NVIDIA_DRIVER_CAPABILITIES=utility)
  CLEAN_TEST_ID="$(docker create -i --name "$testc" \
    --network none "${gpu_args[@]}" \
    -v "$vbin:/opt/phsmoke:ro" \
    -e PHSMOKE_CONTAINER=1 -e PHSMOKE_REAL_GPU="$PHSMOKE_REAL_GPU" -e PHSMOKE_TIMEOUT="${PHSMOKE_TIMEOUT:-180}" \
    "$GO_IMAGE" bash -s -- inner)"
  docker start -a -i "$CLEAN_TEST_ID" < "$0"
  [[ "$(docker inspect -f '{{.State.ExitCode}}' "$CLEAN_TEST_ID")" == 0 ]]
}

# require_container refuses to run the build/inner steps anywhere but inside
# the disposable container this script launched: inner writes /etc/bloxos and
# runs a hub, which must never happen on a host.
require_container() {
  if [[ ! -f /.dockerenv || "${PHSMOKE_CONTAINER:-}" != "1" ]]; then
    echo "refusing: '$1' step must run inside the smoke container (via the outer driver)" >&2; exit 2
  fi
}

# ---------------------------------------------------------------------------
# Build step (inside the build container; /src ro, /out rw).
# ---------------------------------------------------------------------------
build() {
  require_container build
  # Inspect the mount flags; never write-probe the source tree.
  if ! awk '$5 == "/src" { print $6 }' /proc/self/mountinfo | grep -Eq '(^|,)ro(,|$)'; then
    echo "refusing: /src must be mounted read-only" >&2; exit 2
  fi
  set -x
  (cd /src/hub && go build -o /out/bloxos-hub .)
  (cd /src/agent && go build -o /out/bloxos-agent .)
  mkdir -p /tmp/verify && write_verifier > /tmp/verify/main.go
  (cd /tmp/verify && go build -o /out/phsmoke-verify main.go)
  set +x
  for f in /out/*; do printf '   %s %s bytes\n' "$f" "$(stat -c %s "$f")"; done
}

# ---------------------------------------------------------------------------
# Inner test (inside the test container).
# ---------------------------------------------------------------------------
inner() {
  require_container inner
  [[ -x /opt/phsmoke/phsmoke-verify && -x /opt/phsmoke/bloxos-hub && -x /opt/phsmoke/bloxos-agent ]] \
    || { echo "refusing: build volume not mounted at /opt/phsmoke" >&2; exit 2; }
  local W=/work T="${PHSMOKE_TIMEOUT:-180}"
  mkdir -p "$W/bin" "$W/hub" "$W/log" "$W/out"
  export PATH="$W/bin:/opt/phsmoke:$PATH"
  V=/opt/phsmoke/phsmoke-verify
  HUB=http://127.0.0.1:4000
  JWT=""
  HUB_PID="" AGENT_PID=""

  fail() { echo "SMOKE FAIL: $*" >&2; dump; exit 1; }
  dump() {
    { echo "---- hub.log (tail)"; tail -n 40 "$W/log/hub.log" 2>/dev/null || true
      echo "---- agent.log (tail)"; tail -n 60 "$W/log/agent.log" 2>/dev/null || true; } >&2
  }
  api() { # method path [json]
    local m=$1 p=$2 d=${3:-}
    curl -sS -f --max-time 20 -X "$m" "$HUB$p" -H 'Content-Type: application/json' \
      ${JWT:+-H "Authorization: Bearer $JWT"} ${d:+-d "$d"}
  }
  jstr() { sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p" | head -n1; }

  # SYNTHETIC nvidia-smi: answers only the agent's streaming power query.
  # The legacy `-x -q` XML dump exits non-zero (no GPUs for legacy metrics),
  # which is exactly what a machine without the XML path would look like.
  if [[ "$PHSMOKE_REAL_GPU" == 1 ]]; then
    PHSMOKE_GPU_IDS=$(nvidia-smi --query-gpu=uuid --format=csv,noheader | paste -sd, -)
    export PHSMOKE_GPU_IDS
    echo "== REAL GPU canary: driver-reported sensors $PHSMOKE_GPU_IDS (not wall power)"
  else
  cat > "$W/bin/nvidia-smi" <<'EOF'
#!/bin/sh
set -e
# SYNTHETIC nvidia-smi fixture for scripts/smoke/power-history.sh.
# Two fake GPUs, ~1 Hz, deterministic sawtooth so mean != peak.
case "$*" in
  *--query-gpu=count,index,uuid,power.draw*)
    i=0
    while :; do
      p0=$(( 30 + (i % 7) * 10 )); p1=$(( 40 + (i % 5) * 20 ))
      echo "2, 0, GPU-SYNTH-0, $p0.5"
      echo "2, 1, GPU-SYNTH-1, $p1.0"
      i=$((i+1)); sleep 1
    done ;;
  *) exit 1 ;;
esac
EOF
  chmod +x "$W/bin/nvidia-smi"
  echo "== fixture: $(command -v nvidia-smi) (SYNTHETIC)"
  fi

  start_hub() {
    (cd "$W/hub" && exec env HUB_LISTEN=127.0.0.1:4000 PUBLIC_URL=$HUB \
      BLOXOS_JWT_SECRET=phsmoke-jwt-secret-0123456789abcdef0123456789abcdef \
      /opt/phsmoke/bloxos-hub) >>"$W/log/hub.log" 2>&1 &
    HUB_PID=$!
    for _ in $(seq 1 60); do
      curl -sf --max-time 5 "$HUB/health" >/dev/null 2>&1 && return 0
      sleep 0.5
    done
    fail "hub did not become healthy"
  }
  start_agent() {
    BLOXOS_HUB=ws://127.0.0.1:4000 BLOXOS_TOKEN="$TOKEN" BLOXOS_AI_SESSIONS=0 \
      /opt/phsmoke/bloxos-agent >>"$W/log/agent.log" 2>&1 &
    AGENT_PID=$!
  }
  stop_pid() { kill -TERM "$1" 2>/dev/null || true; for _ in $(seq 1 50); do kill -0 "$1" 2>/dev/null || return 0; sleep 0.1; done; kill -KILL "$1" 2>/dev/null || true; }

  wait_ack() {
    local wanted=$1 deadline=$(( $(date +%s) + T )) current
    while :; do
      current=$(sed -n 's/.*"acked":\([0-9]*\).*/\1/p' /etc/bloxos/power-history/state-*.json)
      [[ "${current:-0}" -ge "$wanted" ]] && return 0
      [[ $(date +%s) -ge $deadline ]] && fail "ACK did not persist through $wanted"
      sleep 1
    done
  }

  # wait_points FILE MIN_POINTS MIN_MAXSEQ : poll history until thresholds.
  wait_points() {
    local file=$1 minp=$2 minseq=$3 deadline=$(( $(date +%s) + T )) summ=""
    while :; do
      api GET "/api/machines/$MID/power/history" > "$file" || true
      if summ=$($V summary "$minp" < "$file" 2>/dev/null); then
        local maxseq; maxseq=$(sed -n 's/.*MAXSEQ=\([0-9]*\).*/\1/p' <<<"$summ")
        if [[ "${maxseq:-0}" -ge "$minseq" ]]; then echo "$summ"; return 0; fi
      fi
      if [[ $(date +%s) -ge $deadline ]]; then
        { echo "last verifier output: $summ"; $V summary "$minp" < "$file" || true; } >&2
        fail "timed out waiting for $minp points / maxseq>=$minseq"
      fi
      sleep 5
    done
  }

  echo "== phase 0: hub up, real first-boot setup, login, mint install token"
  start_hub
  # Hub runs as the container's root with its default HOME; its
  # first-boot state stays inside the container.
  local st; st=$(cat "$HOME/.bloxos/setup-token")
  api POST /api/setup "{\"setup_token\":\"$st\",\"username\":\"phsmoke-admin\",\"password\":\"PhSmoke-Test-Passw0rd!2026\",\"pin\":\"123456\"}" >/dev/null || fail "setup failed"
  JWT=$(api POST /api/auth/login '{"username":"phsmoke-admin","password":"PhSmoke-Test-Passw0rd!2026"}' | jstr token)
  [[ -n "$JWT" ]] || fail "login returned no JWT"
  TOKEN=$(api POST /api/tokens '{}' | jstr token)
  [[ -n "$TOKEN" ]] || fail "token mint failed"
  echo "   setup+login+token OK"

  echo "== phase 1: agent enrolls over ws://loopback, samples GPUs (real_gpu=$PHSMOKE_REAL_GPU), journals, uploads"
  start_agent
  MID=""
  for _ in $(seq 1 60); do
    MID=$(api GET /api/machines | $V machine-id 2>/dev/null || true)
    [[ -n "$MID" ]] && break; sleep 1
  done
  [[ -n "$MID" ]] || fail "machine never registered"
  echo "   machine id: $MID"
  S1=$(wait_points "$W/out/h1.json" 2 2) || exit 1
  echo "   $S1"
  grep -q "power-history: starting stream=" "$W/log/agent.log" || fail "agent did not start power history"
  grep -q "nvidia-smi" "$W/log/agent.log" && echo "   agent log mentions nvidia-smi: $(grep -c nvidia-smi "$W/log/agent.log") line(s)"
  echo "   journal dir:"; find /etc/bloxos/power-history -maxdepth 1 -type f -printf '     %f %s bytes\n'
  local acked; acked=$(cat /etc/bloxos/power-history/state-*.json)
  echo "   persisted agent state: $acked"
  local maxseq1; maxseq1=$(sed -n 's/.*MAXSEQ=\([0-9]*\).*/\1/p' <<<"$S1")
  local ackn; ackn=$(sed -n 's/.*"acked":\([0-9]*\).*/\1/p' <<<"$acked")
  wait_ack "$maxseq1"
  ackn=$(sed -n 's/.*"acked":\([0-9]*\).*/\1/p' /etc/bloxos/power-history/state-*.json)
  [[ "${ackn:-0}" -ge "$maxseq1" ]] || fail "ACK cursor $ackn behind hub max seq $maxseq1"
  echo "   ACK persisted: acked=$ackn >= hub maxseq=$maxseq1"

  echo "== phase 2: hub DOWN -> agent journals unacknowledged buckets offline -> agent restarted"
  echo "            while hub is still down -> hub UP -> unacked journal replayed after reconnect"
  stop_pid "$HUB_PID"
  if curl -sf --max-time 2 "$HUB/health" >/dev/null 2>&1; then fail "hub still reachable after stop"; fi
  local acked_before; acked_before=$(sed -n 's/.*"acked":\([0-9]*\).*/\1/p' /etc/bloxos/power-history/state-*.json)
  local before_lines; before_lines=$(cat /etc/bloxos/power-history/seg-*.ndjson 2>/dev/null | wc -l)
  echo "   hub stopped; agent state acked=$acked_before journal_records=$before_lines; waiting for offline buckets..."
  local deadline=$(( $(date +%s) + T ))
  while :; do
    local unacked; unacked=$( { cat /etc/bloxos/power-history/seg-*.ndjson 2>/dev/null | grep -o '"seq":[0-9]*' || true; } | cut -d: -f2 | awk -v a="$acked_before" '$1>a' | wc -l)
    [[ "$unacked" -ge 2 ]] && break
    [[ $(date +%s) -ge $deadline ]] && fail "agent did not journal offline buckets"
    sleep 5
  done
  stop_pid "$AGENT_PID"
  cat /etc/bloxos/power-history/seg-*.ndjson > "$W/out/journal-offline.ndjson"
  local last_journaled; last_journaled=$(grep -o '"seq":[0-9]*' "$W/out/journal-offline.ndjson" | cut -d: -f2 | sort -n | tail -n1)
  local acked_mid; acked_mid=$(sed -n 's/.*"acked":\([0-9]*\).*/\1/p' /etc/bloxos/power-history/state-*.json)
  [[ "$acked_mid" == "$acked_before" ]] || fail "ACK cursor moved while hub was down ($acked_before -> $acked_mid)"
  echo "   offline journal: $unacked unacknowledged record(s), seqs up to $last_journaled, acked still $acked_before"
  start_agent
  sleep 3
  start_hub
  echo "   agent restarted (hub still down at the time), hub back up; waiting for replay + new buckets"
  S2=$(wait_points "$W/out/h2.json" 3 $((last_journaled + 1))) || exit 1
  echo "   $S2"
  $V compare "$W/out/h1.json" --gap-after "$last_journaled" < "$W/out/h2.json" || fail "phase 2 comparison"
  $V replayed "$W/out/journal-offline.ndjson" "$acked_before" < "$W/out/h2.json" || fail "unacked journal replay"
  local acked_after; acked_after=$(sed -n 's/.*"acked":\([0-9]*\).*/\1/p' /etc/bloxos/power-history/state-*.json)
  local maxseq2; maxseq2=$(sed -n 's/.*MAXSEQ=\([0-9]*\).*/\1/p' <<<"$S2")
  wait_ack "$maxseq2"
  acked_after=$(sed -n 's/.*"acked":\([0-9]*\).*/\1/p' /etc/bloxos/power-history/state-*.json)
  [[ "${acked_after:-0}" -ge "$maxseq2" ]] || fail "ACK cursor $acked_after behind hub max seq $maxseq2 after replay"
  echo "   ACK cursor after replay: $acked_after (hub max seq $maxseq2); acked-through lines in agent log: $(grep -c 'acked through' "$W/log/agent.log")"

  echo "== phase 3: delta cursor from phase 1 returns only later ingestion"
  local cursor1; cursor1=$(sed -n 's/.*CURSOR=\([0-9]*\).*/\1/p' <<<"$S1")
  api GET "/api/machines/$MID/power/history?after=$cursor1" > "$W/out/d3.json"
  $V disjoint "$W/out/h1.json" < "$W/out/d3.json" || fail "delta after cursor overlapped the earlier window"

  echo "== phase 4: resource check"
  echo "   agent RSS: $(awk '/VmRSS/{print $2" "$3}' "/proc/$AGENT_PID/status")   nvidia-smi streaming procs: $(pgrep -fc 'nvidia-smi --query-gpu' || true)"
  echo "   hub gaps/degraded: $(sed -n 's/.*GAPS=\([0-9]*\) DEGRADED=\([a-z]*\).*/gaps=\1 degraded=\2/p' <<<"$S2")"
  echo
  echo "SMOKE PASS (real_gpu=$PHSMOKE_REAL_GPU; real agent+hub binaries; loopback container)"
  echo "LIMITS: no wall-power accuracy or 24h soak claim; ACK-loss-without-restart relies on unit/WS tests."
  [[ "$PHSMOKE_REAL_GPU" == 1 ]] || echo "Synthetic fixture: NOT hardware verification; legacy XML path answered no GPU."
  stop_pid "$AGENT_PID"; stop_pid "$HUB_PID"
}

# ---------------------------------------------------------------------------
# Verifier source (Go, no dependencies). Modes:
#   machine-id                 print the first machine id from /api/machines
#   summary MIN                validate invariants, print POINTS= STREAMS= ...
#   compare PREV [--gap-after N]  PREV ⊆ current byte-identical, no dups,
#                              new points, and (optionally) a declared gap
#                              starting at N+1 on PREV's stream
#   disjoint FILE              no (stream,seq) of FILE appears in current
# ---------------------------------------------------------------------------
write_verifier() {
cat <<'EOF'
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type Stats struct {
	MeanWatts *float64 `json:"mean_watts"`
	PeakWatts *float64 `json:"peak_watts"`
	Samples   int      `json:"samples"`
}
type Sensor struct {
	ID string `json:"id"`
	Stats
}
type Bucket struct {
	Seq             uint64   `json:"seq"`
	StartUnixMS     int64    `json:"start_unix_ms"`
	EndUnixMS       int64    `json:"end_unix_ms"`
	ExpectedSamples int      `json:"expected_samples"`
	GPUs            []Sensor `json:"gpus"`
	GPUTotal        *Stats   `json:"gpu_total,omitempty"`
	CPU             *Stats   `json:"cpu,omitempty"`
	GapBefore       bool     `json:"gap_before,omitempty"`
}
type Point struct {
	StreamID string `json:"stream_id"`
	Bucket
}
type Gap struct {
	StreamID string `json:"stream_id"`
	From     uint64 `json:"from"`
	Through  uint64 `json:"through"`
}
type History struct {
	Points   []Point `json:"points"`
	Gaps     []Gap   `json:"gaps"`
	Cursor   uint64  `json:"cursor"`
	Degraded bool    `json:"degraded"`
}

func die(f string, a ...any) { fmt.Fprintf(os.Stderr, "verify: "+f+"\n", a...); os.Exit(1) }

func load(r io.Reader) History {
	var h History
	if err := json.NewDecoder(r).Decode(&h); err != nil {
		die("decode history: %v", err)
	}
	return h
}
func loadFile(p string) History {
	f, err := os.Open(p)
	if err != nil {
		die("%v", err)
	}
	defer f.Close()
	return load(f)
}
func key(p Point) string { return p.StreamID + ":" + strconv.FormatUint(p.Seq, 10) }
func canon(p Point) string { b, _ := json.Marshal(p.Bucket); return string(b) }

func checkStats(what string, s *Stats, max int) {
	if s.Samples < 1 || s.Samples > max {
		die("%s samples %d outside [1,%d]", what, s.Samples, max)
	}
	if s.MeanWatts == nil || s.PeakWatts == nil {
		die("%s missing mean/peak", what)
	}
	if *s.MeanWatts > *s.PeakWatts || *s.MeanWatts < 0 {
		die("%s mean %v > peak %v", what, *s.MeanWatts, *s.PeakWatts)
	}
}

func validate(h History, minPoints int) (maxSeq uint64, streams map[string]bool) {
	if len(h.Points) < minPoints {
		die("only %d points (< %d)", len(h.Points), minPoints)
	}
	seen := map[string]bool{}
	completeWindows := 0
	last := map[string]uint64{}
	streams = map[string]bool{}
	for _, p := range h.Points {
		k := key(p)
		if seen[k] {
			die("duplicate %s", k)
		}
		seen[k] = true
		streams[p.StreamID] = true
		if p.Seq <= last[p.StreamID] && last[p.StreamID] != 0 {
			die("seq not ascending in stream %s at %d", p.StreamID, p.Seq)
		}
		last[p.StreamID] = p.Seq
		if p.Seq > maxSeq {
			maxSeq = p.Seq
		}
		duration := p.EndUnixMS-p.StartUnixMS
		if duration <= 0 || duration > 30000 {
			die("seq %d invalid window %d ms", p.Seq, duration)
		}
		if duration == 30000 { completeWindows++ }
		if p.ExpectedSamples != 30 {
			die("seq %d expected_samples %d", p.Seq, p.ExpectedSamples)
		}
		expectedIDs := []string{"GPU-SYNTH-0", "GPU-SYNTH-1"}
		realGPU := os.Getenv("PHSMOKE_REAL_GPU") == "1"
		if realGPU { expectedIDs = strings.Split(os.Getenv("PHSMOKE_GPU_IDS"), ",") }
		if len(expectedIDs) != 2 || len(p.GPUs) != 2 || p.GPUs[0].ID != strings.TrimSpace(expectedIDs[0]) || p.GPUs[1].ID != strings.TrimSpace(expectedIDs[1]) {
			die("seq %d sensor IDs do not match the two expected GPUs", p.Seq)
		}
		minS := 1 << 30
		for _, g := range p.GPUs {
			checkStats("seq "+strconv.FormatUint(p.Seq, 10)+" gpu "+g.ID, &g.Stats, 30)
			if duration == 30000 && g.Samples < 20 {
				die("seq %d gpu %s only %d samples (fixture is ~1 Hz)", p.Seq, g.ID, g.Samples)
			}
			if g.Samples < minS {
				minS = g.Samples
			}
		}
		if p.GPUTotal == nil {
			die("seq %d has no gpu_total despite complete synthetic ticks", p.Seq)
		}
		checkStats("seq "+strconv.FormatUint(p.Seq, 10)+" gpu_total", p.GPUTotal, minS)
		// Fixture: GPU0 in [30,90]+0.5, GPU1 in [40,120]; total peak <= 210.5.
		if !realGPU && (*p.GPUTotal.PeakWatts > 210.5 || *p.GPUTotal.MeanWatts < 70) {
			die("seq %d gpu_total out of fixture range: %+v", p.Seq, *p.GPUTotal)
		}
		if p.CPU != nil {
			checkStats("seq "+strconv.FormatUint(p.Seq, 10)+" cpu", p.CPU, 30)
		}
	}
	if minPoints >= 2 && completeWindows < 2 {
		die("need at least two complete 30s windows, got %d (clock-step partial windows allowed)", completeWindows)
	}
	return maxSeq, streams
}

func main() {
	if len(os.Args) < 2 {
		die("mode required")
	}
	switch os.Args[1] {
	case "machine-id":
		var ms []struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(os.Stdin).Decode(&ms); err != nil || len(ms) == 0 {
			os.Exit(1)
		}
		fmt.Print(ms[0].ID)
	case "summary":
		min, _ := strconv.Atoi(os.Args[2])
		h := load(os.Stdin)
		maxSeq, streams := validate(h, min)
		fmt.Printf("POINTS=%d STREAMS=%d MAXSEQ=%d CURSOR=%d GAPS=%d DEGRADED=%v\n",
			len(h.Points), len(streams), maxSeq, h.Cursor, len(h.Gaps), h.Degraded)
	case "compare":
		prev := loadFile(os.Args[2])
		var gapAfter uint64
		if len(os.Args) >= 5 && os.Args[3] == "--gap-after" {
			gapAfter, _ = strconv.ParseUint(os.Args[4], 10, 64)
		}
		cur := load(os.Stdin)
		curMax, curStreams := validate(cur, len(prev.Points)+1)
		prevMax, prevStreams := validate(prev, 1)
		if len(curStreams) != 1 || len(prevStreams) != 1 {
			die("expected exactly one stream across restart, got prev=%d cur=%d", len(prevStreams), len(curStreams))
		}
		idx := map[string]string{}
		for _, p := range cur.Points {
			idx[key(p)] = canon(p)
		}
		for _, p := range prev.Points {
			c, ok := idx[key(p)]
			if !ok {
				die("earlier point %s vanished", key(p))
			}
			if c != canon(p) {
				die("earlier point %s changed after replay", key(p))
			}
		}
		if curMax <= prevMax {
			die("no new sequences after restart (prev max %d, cur max %d)", prevMax, curMax)
		}
		if gapAfter > 0 {
			found := false
			for _, g := range cur.Gaps {
				if g.From == gapAfter+1 && g.Through < curMax {
					found = true
				}
			}
			if !found {
				die("no declared gap starting at %d (restart reservation hole); gaps=%+v", gapAfter+1, cur.Gaps)
			}
			// Contract: the first bucket after the restart carries gap_before
			// (collection stopped while the process was down).
			var first *Point
			for i := range cur.Points {
				if cur.Points[i].Seq > gapAfter && (first == nil || cur.Points[i].Seq < first.Seq) {
					first = &cur.Points[i]
				}
			}
			if first == nil || !first.GapBefore {
				die("first post-restart bucket must carry gap_before: %+v", first)
			}
		}
		fmt.Printf("COMPARE OK: prev=%d points intact, cur=%d points, maxseq %d -> %d, gaps=%d\n",
			len(prev.Points), len(cur.Points), prevMax, curMax, len(cur.Gaps))
	case "replayed":
		// Every journal record above the pre-outage ACK cursor must be on the
		// hub byte-identical (canonical bucket JSON) after the reconnect.
		f, err := os.Open(os.Args[2])
		if err != nil {
			die("%v", err)
		}
		ackedBefore, _ := strconv.ParseUint(os.Args[3], 10, 64)
		cur := load(os.Stdin)
		idx := map[uint64]string{}
		for _, p := range cur.Points {
			idx[p.Seq] = canon(p)
		}
		dec := json.NewDecoder(f)
		n := 0
		for dec.More() {
			var b Bucket
			if err := dec.Decode(&b); err != nil {
				die("journal decode: %v", err)
			}
			if b.Seq <= ackedBefore {
				continue
			}
			jb, _ := json.Marshal(b)
			got, ok := idx[b.Seq]
			if !ok {
				die("journaled seq %d (unacked before outage) never reached the hub", b.Seq)
			}
			if got != string(jb) {
				die("journaled seq %d differs on hub:\n journal %s\n hub     %s", b.Seq, jb, got)
			}
			n++
		}
		if n < 2 {
			die("expected >=2 replayed unacked records, matched %d", n)
		}
		fmt.Printf("REPLAY OK: %d unacknowledged journal record(s) replayed byte-identical after reconnect\n", n)
	case "disjoint":
		other := loadFile(os.Args[2])
		cur := load(os.Stdin)
		validate(cur, 1)
		if cur.Cursor <= other.Cursor { die("delta cursor did not advance") }
		had := map[string]bool{}
		for _, p := range other.Points {
			had[key(p)] = true
		}
		for _, p := range cur.Points {
			if had[key(p)] {
				die("delta re-delivered %s", key(p))
			}
		}
		fmt.Printf("DELTA OK: %d new points, cursor %d, none overlapping\n", len(cur.Points), cur.Cursor)
	default:
		die("unknown mode %q", os.Args[1])
	}
}
EOF
}

case "${1:-}" in
  build) build ;;
  inner) inner ;;
  *) outer ;;
esac
