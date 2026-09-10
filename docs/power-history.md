# Component power history

Power history supplements the existing instantaneous GPU readings. It does not
measure wall power and must not be described as total machine electricity use.
CPU readings, when available, describe supported CPU package sensors only.

Where a machine exposes a genuine whole-system counter, that reading is carried
separately as the `system` domain and labelled with the backend behind it. It is
still not a wall measurement: a RAPL platform zone covers the board the SoC can
see, and a discharging battery excludes the charger losses that are not
occurring while it discharges.

A machine that measures **nothing at all** may instead report a **modelled
estimate** for `system`, labelled `estimate-util`. An estimate is not a
measurement and is never blended with one — see
[Estimated power](#estimated-power). Where no counter exists and the machine
cannot be identified confidently enough to model, it still reports nothing.

## Domains and backends

Buckets carry four independent measurements. They are **disjoint scopes, never
summands**: on hardware that can measure it, `system` already contains `cpu`,
`dram` and the GPUs, so consumers must never add domains together or derive one
from another. Each is sampled by its own backend on its own schedule, so even
their sample counts need not agree.

| Domain | Backends, in preference order | Path or command | Units |
| --- | --- | --- | --- |
| `system` | `rapl-psys` | `/sys/class/powercap/intel-rapl:<n>` named `psys` | µJ counter |
| | `battery` | `/sys/class/power_supply/*` with `type=Battery` and `scope` unset or `System` | µW, else µA × µV |
| | `ipmi-dcmi` | `ipmitool dcmi power reading` | watts |
| | `hwmon:<chip>` | `/sys/class/hwmon/hwmon<n>/power1_average` or `power1_input` | µW |
| | `estimate-util` **— NOT A MEASUREMENT** | modelled from `/proc/stat`; engages only when nothing is measured | watts (modelled) |
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
  so it cannot shadow a battery or BMC that does work.
- A battery counts only while it is **discharging**. On AC it measures charge
  current, which is not system power, so the domain reports nothing until the
  machine is unplugged again. Multiple discharging packs are summed; a pack that
  disappears makes the backend unavailable rather than silently halving the
  measured draw.
- The generic hwmon list is deliberately short (`power_meter` and the INA2xx
  shunt monitors). A GPU or CPU chip's power channel is a component sensor and
  is never promoted to whole-system power.
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
- RAPL counters wrap at `max_energy_range_uj`; wrap is corrected, and an
  implausible interval or result re-primes instead of reporting a wrong number.
- **Every backend above except `estimate-util` reads a counter, and a measured
  value is never mixed with a modelled one.** ARM SoCs such as the RK3588 expose
  regulator voltages with no current, so watts are not derivable from them and
  none are invented; what those machines may get instead is the clearly labelled
  utilisation model described in [Estimated power](#estimated-power), and only
  when they measure nothing at all. Windows RAPL needs a signed ring-0 driver
  and is unavailable inside a VM regardless, so the scalar domains stay absent
  there and nothing is estimated on Windows.
- The mixed-backend rule below covers estimation without amendment: a window
  whose `system` samples came from both a counter and the model is dropped, not
  averaged. In practice it cannot arise, because the model is only ever attached
  to a machine that has no counter to switch back to.

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
overlapping subdomains. On Windows every scalar domain is unavailable and no
estimate is offered: the model is Linux-only, because it needs `/proc/stat` and
the device tree. Set `BLOXOS_POWER_HISTORY=0` on an agent before startup to
disable the feature.

Two variables control estimation, read once at startup and echoed in the
agent's log line along with the resolved envelope and where it came from:

| Variable | Effect |
| --- | --- |
| `BLOXOS_POWER_ESTIMATE_WATTS="<idle>:<max>"` | Whole-platform envelope in watts. Beats the built-in table and the heuristic, and enables estimation on machines nothing else covers. **A malformed value disables estimation rather than falling back** — an operator who typed an envelope wants that envelope, not a guess. |
| `BLOXOS_POWER_ESTIMATE=off` (also `0`, `false`, `no`) | Refuses the model outright. Every real counter keeps working. |

**Calibrate with a wall meter.** Measure the machine idle and under sustained
load, then set `BLOXOS_POWER_ESTIMATE_WATTS` to those two numbers. That single
step takes the machine from roughly ±40% to the accuracy of the meter, and it
is the only path to an estimate worth trusting.

## Estimated power

A machine that exposes no power counter at all — most ARM SoCs, virtual
machines, Windows hosts — can report a **modelled** `system` value instead of
nothing. It is labelled `estimate-util` in `sources`, and
`powerhistory.IsEstimatedSource` / `(*Bucket).Estimated(domain)` read that back
in one call. **Consumers must present it as an estimate and must never sum it
with measured values into a single unqualified figure.**

There is deliberately no `estimated` boolean on the wire. The hub stores a
bucket by decoding and re-encoding it under its own schema, so a field an older
hub does not know is silently dropped — and a dropped `estimated` reads as
`false`, meaning "measured". That fails open on the one property that must never
be wrong. The `sources` label cannot vanish that way.

### The model

    P = P_idle + (P_max - P_idle) * u

`u` is the CPU busy fraction from `/proc/stat`, clamped to `[0,1]` rather than
extrapolated. Busy excludes `idle`, `iowait` **and** `steal`: an `iowait` CPU is
halted and drawing idle power, and `steal` is time the hypervisor gave to
someone else, during which this guest consumed nothing.

The relationship is linear on purpose. The true curve is concave — DVFS raises
voltage with frequency — but that refinement buys a few percent while the
endpoints themselves carry ±40%. A fitted exponent would be false precision.
Frequency is not folded in: scaling by `f/f_max` needs a voltage curve to mean
anything, and on the hardware this targets `cpufreq` is either absent or reports
the governor's request rather than silicon state.

The built-in table holds **whole-platform envelopes**, not CPU TDPs. A TDP is a
thermal limit for one component; converting it to platform watts requires a PSU
efficiency, board, drives and fans that the CPU model does not determine. The
same chip in two different boxes can idle five watts apart. An envelope is also
what an operator can verify with a plug meter, and it is the same shape as the
override, so calibration is one variable.

Rows are keyed on **device-tree `compatible` first**, CPU model second, and a
row carrying a core count is refused when the machine reports a different one —
a 4-vCPU guest does not inherit an 8-core board's envelope.

The table is deliberately small. It is a convenience; the override is the
mechanism, because no table will ever cover every SoC. What ships today:

| Match | Cores | Envelope | Where the numbers come from |
| --- | --- | --- | --- |
| `rockchip,rk3588`, `rockchip,rk3588s` | 8 | 3.5-12.0 W | RK3588 board measurements, all-core draw spanning 7-19 W across boards |
| `raspberrypi,5-model-b`, `brcm,bcm2712` | 4 | 3.0-8.8 W | Raspberry Pi 5 board measurements |
| `raspberrypi,4-model-b`, `brcm,bcm2711` | 4 | 2.6-6.4 W | Raspberry Pi 4B board measurements |
| CPU model `intel n100` | 4 | 6.0-22.0 W | N100 mini-PC wall measurements, idle 4-12 W and load 20-29 W |

The spread in that last column is the honest headline: these are envelopes for a
*class* of machine, and any specific machine sits somewhere inside one. Each row
carries its citation in `agent/power_estimate.go`. Adding a row is a small
change backed by published measurements; calibrating one machine needs no code
change at all.

> **CPU model strings are unreliable on big.LITTLE hardware.** Reading core 0's
> model and multiplying by the total core count reports, for example, "8x
> Cortex-A55" for an RK3588 that is really 4x Cortex-A76 + 4x Cortex-A55. On ARM,
> `/sys/firmware/devicetree/base/compatible` is the real board identity.

An unknown x86 machine reports **nothing**: its PSU, drives and any discrete GPU
are unknown and can draw more than everything else combined. An unknown ARM
board with a device tree, 2-16 cores and no virtualisation falls back to a
bounded per-core heuristic, because that class cannot be wrong by more than
roughly 2x.

### It engages only when nothing is measured

The gate is structural, not a matter of ordering: the estimator is not a
candidate in the `system` backend chain and cannot be reached from it. It is
attached separately, and only when `system`, `cpu`, `dram` and GPU are all
unavailable, `nvidia-smi` included.

The reason is not merely tidiness. A wrong envelope can place an estimated
`system` **below** a measured `cpu` — visibly impossible, and it discredits
every other number the agent reports. With a measured 300 W GPU the same
contradiction is far larger, because a CPU-utilisation model knows nothing about
the card.

Two limitations follow, and neither is papered over:

- A discrete GPU with **no** power counter is invisible to the gate and absent
  from the model, so such a machine would be badly under-reported.
- Detection runs once at startup. A counter that appears later — a battery
  plugged in mid-run — does not displace an already-attached estimate until the
  agent restarts. This is inherited behaviour for every backend, not new here.

### Accuracy, stated plainly

**Trend-grade, not billing-grade.** Good enough for "is this machine busy and
roughly how costly"; useless for billing, capacity sign-off or comparing
hardware.

| Basis | Expected error |
| --- | --- |
| Table hit, CPU-dominated workload | ±30-40% |
| Per-core ARM heuristic | within roughly 2x |
| Operator override from a wall meter | ±5-10%, i.e. the meter's accuracy |

Worst cases, in order:

1. **Any accelerator the CPU counter cannot see** — an integrated Mali GPU, an
   NPU, an uncounted discrete card.
2. **I/O- or memory-bound work**, where utilisation is high and power is not.
   Excluding `iowait` helps; it does not fix a memory-bound spin.
3. **Thermal throttling**, which lowers real power while utilisation stays
   pinned at 100%. The model keeps reporting `P_max`, so the error grows the
   longer the load runs.
4. **A board configured differently from its table row** — NVMe, extra RAM and
   2.5 GbE all add watts the envelope did not assume.

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

The source-agnostic backend layer (`system`/`dram` domains, battery, IPMI/DCMI,
generic hwmon and DRM GPU hwmon) has unit coverage against fake sysfs trees and
a fake BMC, plus hub validation and protocol-compatibility tests in both
directions. It has **not** been run against real hardware for any of the new
backends: no psys zone, battery pack, BMC, INA shunt or AMD card has been read
on a physical machine yet, and no reading has been compared against a wall
meter. Treat every new backend as unverified until a canary says otherwise.

### Estimated system power

The utilisation model has unit coverage for the formula at 0%, 50% and 100% and
its monotonicity; the envelope table's hits, misses and core-count refusal; the
operator override and its refusal of a malformed value; `/proc/stat` parsing
including the `iowait`, `steal` and guest-column rules; the "engages only when
nothing is measured" gate exercised against each measured backend in turn and
against `nvidia-smi`; and the source label on every emitted bucket. Protocol
tests cover the label surviving a round trip through a hub that predates
estimation. The agent suite was cross-compiled for `linux/arm64` and run on
Linux, because the agent does not build on macOS.

It has **not** been compared against a wall meter on any machine, and no
estimating machine has been run in the fleet. Every number it produces today
rests on published board measurements rather than on a reading taken from this
hardware — the RK3588 row in particular is drawn from boards whose measured
all-core draw spans 7 W to 19 W, which is where its stated ±30-40% comes from.

The first machine to run it should be metered under real load and pinned with
`BLOXOS_POWER_ESTIMATE_WATTS`, not trusted as shipped. Until then, treat an
`estimate-util` reading as an indication that a machine is busy, and not as a
figure about how much electricity it used.
