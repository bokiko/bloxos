# Component power history

Power history supplements the existing instantaneous GPU readings. It does not
measure wall power and must not be described as total machine electricity use.
CPU readings, when available, describe supported CPU package sensors only.

## Data flow

The agent samples power separately from the expensive hardware, services and
container scans. Every 30 seconds it forms a window of per-GPU average watts,
highest observed sample, valid sample count and expected sample count. A combined
GPU peak is computed from complete simultaneous observations, never by summing
each device's independently observed maximum. Missing sensors are unavailable,
not zero. The existing `power_watts` instantaneous field keeps its meaning.

Completed windows are saved locally before becoming eligible for upload. The
local journal is bounded by 24 hours and a byte limit. The network sends bounded
batches of new or unacknowledged windows, not the whole journal on every tick.
The hub identifies the machine from its authenticated connection, deduplicates
by machine/stream/sequence, and acknowledges only after committing the contiguous
prefix. A lost acknowledgement causes a harmless retry. Sequence identity does
not depend on the wall clock.

The dashboard's Metrics tab displays the last 24 hours, per GPU, all GPUs
together, or available CPU packages. It labels averages and sampled peaks,
displays sample age and coverage, and leaves missing observations blank. A
one-second sampled peak is not a guaranteed electrical transient maximum.

## Storage and traffic budget

There are 2,880 completed 30-second windows per day. A representative JSON
record with two GPU sensors, GPU-total statistics and CPU statistics measures
524 bytes including its newline: about 1.5 MB for a full day. The same fixture
with 16 GPUs is about 6.5 MB per day. Actual sizes vary with sensor identifiers
and numeric precision; these are encoded-data measurements, not filesystem or
network-overhead estimates. The reproducible size check is
`cd proto && go test -v ./powerhistory -run TestRepresentativeStorageAndBatchSize`.

Steady-state upload sends only the newest completed window each 30-second
cycle. Reconnect replay is capped at 32 records per cycle, so a full-day backlog
takes roughly 47 minutes to clear while new windows continue arriving. The
send throttle also covers manual refresh and reconnect: its minimum interval
is 25 seconds, allowing scheduling jitter around the normal 30-second tick. The
dashboard fetches a bounded history view when its Metrics tab is mounted, then
asks for newly ingested records using a cursor. Because the cursor follows hub
ingestion order, older samples uploaded after a disconnection still arrive in
the response; the graph keeps only samples within the last 24 hours. It does
not request one-second raw samples from each machine.

The Linux journal lives in `/etc/bloxos/power-history` for the root service;
otherwise it is alongside the agent's existing credential state. It uses small
10-record segments (five minutes each), plus stream/acknowledgement state. The
retained journal data is capped at 32 MiB and 2,880 records. Metadata, filesystem
allocation and temporary compaction files add overhead. Partially expired
segments are safely compacted, and a one-minute maintenance tick ages out data
even when sensors stop reporting. An unfinished 30-second window stays in
memory; completed windows are flushed to disk before upload.

GPU history currently uses NVIDIA's streaming query. CPU history uses readable
Linux RAPL package counters and excludes overlapping subdomains. Windows CPU
package power is unavailable; no CPU estimate is invented. Set
`BLOXOS_POWER_HISTORY=0` on an agent before startup to disable the feature.

## Failure boundaries

- The unfinished window can be lost on an abrupt restart or power loss. There
  is no zero-loss guarantee.
- Disk failures must not stop the normal metrics/agent connection. Bounded
  queues may drop history under sustained storage failure; that is reported
  as degraded coverage or a gap, not invented zero-watt readings.
- Retaining only a day means a long disconnection can permanently lose the
  oldest unacknowledged windows. The protocol declares this retention gap.
- The hub retains sequence high-water state independently from chart rows, so
  deleting old chart history does not make an old retransmission new again.
- Older agents continue sending instantaneous metrics. They do not gain sampled
  history retroactively. Older hubs may ignore the additive history frames.
- Rejected batches are not silently acknowledged. The dashboard exposes a
  bounded diagnostic for clock skew, conflicting replay, invalid data or storage
  failure on the current agent connection. It clears after a successful batch.
  Stored JSON is normalized through the current schema before comparing retries;
  genuinely different data for an existing sequence remains a conflict.

Do not remove the CA, enrollment secret, or pinned update public key while
maintaining or repairing power history. New telemetry does not require rerunning
installation, changing credentials, or adding a dashboard onboarding step.

## Review and rollout status

This feature is undergoing PR review. It has not been deployed to the fleet.
The checks below distinguish synthetic pipeline validation from hardware testing.

Validated locally:

- Full hub tests, full hub race tests and `go vet`.
- Full Linux agent race tests, including an unprivileged journal failure test.
- Linux amd64/arm64 and Windows amd64 agent builds; Windows test compilation.
- Shared protocol race tests.
- Dashboard tests (17), lint and production build.
- Read-only NVIDIA streaming-query check on the dual-GPU ASUS machine.
- Linux agent vet and tests with the optional `insecure` build tag.
- Isolated real-binary pipeline smoke using synthetic NVIDIA readings:
  enrollment, two full 30-second windows, valid clock-step partial windows,
  durable ACK, offline recording, agent restart during the outage, hub recovery,
  unchanged replayed data, declared restart gap and non-overlapping API deltas.
- Smoke-script syntax, ShellCheck, host-execution refusal and invalid-prefix
  refusal. The shared protocol vet/race job is now included in CI; its commands
  passed locally; consult the PR checks for GitHub Actions results.

The user reviewed and approved the local sample-data preview. This is visual
acceptance, not a live hardware accuracy check.

The isolated smoke committed four initial readings, recorded two more while the
hub was down without advancing the ACK, then recovered both unchanged after an
agent restart. It finished with seven distinct records, one declared reservation
gap, agent ACK 66 and a delta of three new records. These are test-fixture
results, not live-machine measurements. Its disposable containers and build
volume were removed; existing fleet containers were unchanged.

Run the repeatable smoke on a disposable Docker container from a Linux Docker
host (it does not install an agent on that host):

```sh
bash scripts/smoke/power-history.sh
```

The smoke does not simulate an ACK lost after hub commit; duplicate/retry and
commit-failure behavior also has separate unit/WebSocket integration coverage.
It does not establish a 24-hour resource soak or physical sensor accuracy.

Still needed before fleet rollout: a real-hardware canary. Real CPU package
readings and Windows driver streaming have not been runtime-verified.
Cross-compilation is not a substitute for those hardware checks.
