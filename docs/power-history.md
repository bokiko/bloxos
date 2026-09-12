# Component power history

Power history supplements the existing instantaneous GPU readings. It does not
measure wall power and must not be described as total machine electricity use.
CPU readings, when available, describe supported CPU package sensors only.

Where a machine exposes a genuine whole-system counter, that reading is carried
separately as the `system` domain and labelled with the backend behind it. It is
still not a wall measurement: a RAPL platform zone covers the board the SoC can
see.

**A machine that measures nothing reports nothing.** There is no modelled
fallback and no code that could produce one: a number derived from a
utilisation curve is not a measurement however carefully it is labelled. ARM
SoCs such as the RK3588 expose regulator voltages with no current sense, so
watts are not derivable from them, and none are invented.

## Domains and backends

Buckets carry four independent measurements. They are **disjoint scopes, never
summands**: on hardware that can measure it, `system` already contains `cpu`,
`dram` and the GPUs, so consumers must never add domains together or derive one
from another. Each is sampled by its own backend on its own schedule, so even
their sample counts need not agree.

| Domain | Backends, in preference order | Path or command | Units |
| --- | --- | --- | --- |
| `system` | `rapl-psys` | `/sys/class/powercap/intel-rapl:<n>` named `psys` | µJ counter |
| | `ipmi-dcmi` | `ipmitool dcmi power reading`, **only while the BMC reports the reading state as `activated`** | watts |
| `cpu` | `rapl-package` | top-level `intel-rapl:<n>` zones named `package-*`, summed | µJ counter |
| `dram` | `rapl-dram` | `intel-rapl:<n>:<m>` sub-zones named `dram`, summed | µJ counter |
| GPUs | NVIDIA streaming query | `nvidia-smi --query-gpu=...` | watts |
| | DRM hwmon | `/sys/class/drm/card*/device/hwmon/hwmon*/power1_average` | µW |

Detection runs once at startup and logs what it found. The rules that keep the
numbers honest:

- **psys is preferred over the package sum for `system`, never added to it.**
  The package zones keep answering for `cpu` in their own field. Nothing here
  or downstream sums two domains into one number.
- A psys zone that a vendor exposes but never advances is rejected at startup,
  so it cannot shadow a BMC that does work.
- **Only two backends measure whole-platform power.** A battery and a generic
  hwmon chip were removed because neither establishes SCOPE. A pack's discharge
  is the machine's draw only while the machine runs on that pack alone; on AC,
  or with one pack charging while another discharges, the packs carry part of
  the load and nothing available says which part. A chip name says that
  something measures, never what: a shunt reports whatever rail it is wired
  across, and ACPI's `power_meter` is no better — the kernel's own documentation
  provides `power*_is_battery` and the `measures/` symlinks precisely because
  the chip alone does not say. Rows already recorded from either still decode;
  they are simply not added into a system total.
- **Every RAPL counter in a group must advance on its own account.** A frozen
  zone makes its group unavailable, never smaller: summing first let one busy
  socket certify a frozen sibling, and an entirely frozen group reported 0.0 W
  as if it were a measurement.
- **RAPL inside a VM guest is withheld entirely** — `cpu` and `dram` as well as
  `system`. A package counter measures physical silicon the guest shares with
  tenants it cannot see. A virtualization *host* keeps all three.
- `ipmitool` is executed only when a BMC character device exists, and then at
  most once every five seconds; a slow BMC lowers that window's sample count
  rather than delaying any other domain.
- Every measured domain is emitted together with a `sources` entry naming its
  backend. An unlabelled `cpu` reading comes from an agent predating labelling
  and means the RAPL package sum, which is what those agents measured.
- If a domain's samples inside one window came from **more than one backend**
  (a laptop unplugged mid-window), the domain is omitted for that window rather
  than averaged across measurement methods. The other domains are unaffected
  and the next stable window reports normally. This is the rule GPU totals
  already apply to a changing device set.
- **A backward step on a RAPL counter is unavailable, not a wrap.** A rollover
  and a counter reset (suspend/resume, a driver reload) are indistinguishable
  from two reads, and assuming a wrap invents up to a whole
  `max_energy_range_uj` of energy at a rate the plausibility ceiling would
  accept. One missed sample at rollover is the cheaper error; the next interval
  recovers on its own. An implausible interval or an out-of-range value
  re-primes instead of reporting a wrong number.
- **Every backend above reads a counter.** Windows RAPL needs a signed ring-0
  driver and is unavailable inside a VM regardless, so the scalar domains stay
  absent there. Windows NVIDIA GPU power is reported and is unaffected by any
  of this.

NVIDIA cards are excluded from the DRM hwmon scan: `nvidia-smi` already streams
them, and a second identifier for the same device would inflate the sensor list.
On a machine with both an NVIDIA card and an AMD one, the two producers report
different device sets, so the combined GPU total is omitted for those windows —
no single simultaneous observation of all GPUs exists. Per-GPU readings are
unaffected.

Sensor and backend identifiers are provenance strings only. They never reach a
path, a query or a machine identity, which is taken from the authenticated
connection.

## Data flow

The agent samples power separately from the expensive hardware, services and
container scans. Every 30 seconds it forms a window of per-GPU average watts,
highest observed sample, valid sample count and expected sample count. A combined
GPU peak is computed from complete simultaneous observations, never by summing
each device's independently observed maximum. Missing sensors are unavailable,
not zero. The existing `power_watts` instantaneous field keeps its meaning.

If GPU membership changes inside a window, its combined GPU total is omitted
rather than mixing different device sets. Per-GPU readings and scalar-domain
statistics are retained, and combined totals resume in the next stable window.
The same rule covers a scalar domain whose backend changed inside the window.
This keeps the recorded window valid for replay without relaxing hub validation.
A window in which everything observed was discarded emits no bucket at all and
is declared as a gap, rather than claiming coverage it does not have.

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

There are 2,880 completed 30-second windows per day. A worst-case JSON record —
two GPU sensors, GPU-total statistics and all three labelled scalar domains —
measures 835 bytes including its newline: about 2.4 MB for a full day. The same
fixture with 16 GPUs is 2,585 bytes, about 7.4 MB per day. Most machines send
less, because a domain with no counter is omitted from the wire entirely rather
than sent as a zero. Actual sizes vary with sensor identifiers
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

GPU history uses NVIDIA's streaming query and, for non-NVIDIA cards, DRM hwmon
power channels. The scalar domains use the backends tabulated above and exclude
overlapping subdomains. On Windows every scalar domain is unavailable; NVIDIA
GPU power is reported there and is unaffected. Set `BLOXOS_POWER_HISTORY=0` on
an agent before startup to disable the feature.

`BLOXOS_POWER_ESTIMATE_WATTS` and `BLOXOS_POWER_ESTIMATE` are gone along with
the model they configured. A machine with no counter reports nothing, and no
environment variable changes that.

## Estimated power — REMOVED

Nothing generates an estimate any more. The rule is plainer than the model it
replaces: **never estimate — a machine with no counter reports nothing.**

A modelled number is not a measurement, and labelling it carefully does not
change what it is. Every consumer that sees one has to decide what to do with
it, and a fleet total is exactly where that decision gets made badly — an
estimate summed beside counters produces a figure that reads like electricity
use and is not.

What remains is DECODING, deliberately:

- `estimate-util` is still a valid `sources` value on the wire, and
  `powerhistory.IsEstimatedSource` / `(*Bucket).Estimated(domain)` still read it
  back. Rows written before this change are still on disk in agent journals and
  in the hub's history; they replay with their label intact, and there is a test
  that proves it.
- Agents older than this change still report modelled values, and the hub still
  presents them as modelled — separately from measured, never summed into it.

There is deliberately no `estimated` boolean on the wire. The hub stores a
bucket by decoding and re-encoding it under its own schema, so a field an older
hub does not know is silently dropped — and a dropped `estimated` reads as
`false`, meaning "measured". That fails open on the one property that must
never be wrong. The `sources` label cannot vanish that way.

## Protocol compatibility

`system`, `dram` and `sources` were added to `powerhistory.Bucket` as optional
fields. There is no version negotiation for these frames; compatibility is a
property of the encoding, in both directions:

- **Old agent → new hub.** An old bucket omits the new fields, so they decode as
  nil — unavailable, not zero — and validation never requires them. An
  unlabelled `cpu` reading is accepted exactly as before and is never given an
  invented label. Fleet agents that cannot be updated keep reporting forever.
- **New agent → old hub.** An old hub ignores fields it does not know, stores
  everything it does understand, and normalizes both sides of a replay
  comparison through its own schema — so dropping the new fields is symmetric
  and a retransmission is still recognised as identical rather than as a data
  conflict. The cost is that the new domains are not persisted by that hub.
- **Newer agent → this hub.** A `sources` entry naming a domain this hub does
  not interpret is bounds-checked and stored rather than rejected, so a hub
  upgrade is never a precondition for an agent upgrade.

`estimate-util` needed no protocol change at all: it is a new **value** for the
existing `sources` field, not a new field. A hub predating estimation
bounds-checks the label, stores it and returns it unchanged, so an estimate
stays identifiable as an estimate across a version skew in either direction.
That placement was deliberate — the label is the only thing separating a
modelled number from a measured one, so it was put somewhere no schema mismatch
can drop it.

The corollary is a real risk, not a theoretical one: a consumer that renders
`system` **without** consulting `sources` will present an estimate as though a
counter produced it. Reading the label is therefore mandatory, not advisory, for
every consumer in this repository.

The hub validates the new fields with the same rigour as the old ones: finite
watts within range, mean not above peak, statistics present exactly when samples
are, samples not above the window's expected count, bounded and unique domain
labels, and no label for a domain that carries no statistics. It deliberately
enforces **no arithmetic relation between domains** — not even "system exceeds
cpu" — because they come from different backends on different schedules.

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

### Real GPU canary

An isolated Linux canary passed on two RTX 3090 GPUs using release-2 agent and
hub binaries after integrating PR #180. It used real NVIDIA readings, no host
credentials or published ports, and disconnected container networking during
the test. The installed fleet agent remained running with the same PID and
start timestamp.

Two initial windows each contained all 30 samples. One idle window measured
33.883 W and 37.153 W, correctly combining to 71.036 W. During the hub outage,
two additional windows remained unacknowledged on disk. After restarting the
canary agent and recovering the hub, both replayed unchanged. The test finished
with five distinct records, ACK 66, one declared reservation gap, and three
non-overlapping delta records. The disposable canary container was removed;
logs and API responses were retained outside the repository.

To opt into the two-GPU mode on a disposable Linux Docker host with NVIDIA
container-runtime support:

```sh
PHSMOKE_REAL_GPU=1 bash scripts/smoke/power-history.sh
```

This was an idle GPU pipeline/recovery check, not a loaded-machine experiment,
wall-power calibration or 24-hour soak. CPU sensors were unavailable in the
restricted container and correctly absent. Real CPU package readings and
Windows driver streaming remain unverified; cross-compilation is not a
substitute for those hardware checks. Fleet rollout remains a separate step.

The subsequent GPU-membership correction advances the agent release marker to 3.
The hardware canary above tested release 2; it is not a hardware test of this
later correction. Membership transitions have separate accumulator and hub
regression coverage.

### Broadened backend coverage

The source-agnostic backend layer (`system`/`dram` domains, RAPL, IPMI/DCMI and
DRM GPU hwmon) has unit coverage against fake sysfs trees and a fake BMC —
including the deactivated-BMC case, per-counter RAPL progress, backward counter
steps, unreadable zone names, and the VM guest gate — plus hub validation and
protocol-compatibility tests in both directions. It has **not** been run against real hardware for any of the new
backends: no psys zone, BMC or AMD card has been read on a physical machine
yet, and no reading has been compared against a wall meter. Treat every new
backend as unverified until a canary says otherwise.

### Legacy rows

Removing a backend from the agent does not remove the rows it already wrote.
Journals on real machines still hold them, and they replay after an upgrade.

- A modelled (`estimate-util`) row replays with its label intact and is still
  presented as modelled, separately from measured. There is a journal test for
  exactly this.
- A `battery` or `hwmon:*` reading in the `system` domain is a real measurement
  of something, but not demonstrably of that machine. It is excluded from
  system totals — current and historical — and counted with the machines whose
  source or scope is unverified, so the omission is visible rather than silent.
- An all-zero RAPL window (mean **and** peak exactly zero) is excluded the same
  way. This is not a claim that the row was fabricated: samples and expected
  cannot separate a frozen counter from a window of genuinely zero energy, and
  that inability is the reason to withhold rather than to sum. A positive peak
  with a mean that rounds to zero is not this case — the counter moved. GPU
  zeros and active BMC zeros are real readings and survive.

The stored rows are never rewritten. All of this is a read-time judgement about
what may be added up.

The agent suite is cross-compiled for `linux/arm64` and run on Linux, because
the agent does not build on macOS.

No reading from any backend has been compared against a wall meter. That
comparison is still the only thing that would turn these numbers from
plausible into verified, and it has not been done.
