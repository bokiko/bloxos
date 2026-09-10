package main

import (
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
)

// logPowerEstimate keeps every line about the model under one grep-able
// prefix, so an operator reading a machine's logs can see in one search
// whether it is measuring or modelling and why.
func logPowerEstimate(format string, args ...any) {
	log.Printf("power-history: estimate: "+format, args...)
}

// Last-resort whole-platform power ESTIMATION.
//
// Everything else in this package measures. This file does not: it models a
// machine's platform power from CPU utilisation, for hosts that expose no
// power counter at all — an RK3588-class ARM SoC (regulator voltages, no
// current sense), a guest with no host MSRs, a Windows box with no signed
// ring-0 RAPL driver. Those machines are otherwise simply dark.
//
// The rules that keep this honest, in order of importance:
//
//  1. It is STRICTLY LAST. pickSystemSource only reaches the estimator after
//     every measured system backend has returned nil, and detectPowerSources
//     additionally withholds it whenever ANY power counter on the machine
//     works — see estimationAllowed.
//  2. It carries its own source id, powerhistory.SourceEstimateUtil, on every
//     sample. A consumer that does not check the label sees a number that
//     looks measured; a consumer that does cannot be misled.
//  3. It never blends. A modelled sample and a measured sample never enter
//     the same window (the accumulator already drops a domain whose samples
//     came from two backends), and no measured value is ever adjusted by it.
//  4. Without a defensible envelope it reports nothing. An unknown machine
//     stays dark rather than shipping a number nobody can justify.
//
// The model:
//
//	P(u) = P_idle + (P_max − P_idle) × u        u = CPU utilisation in [0,1]
//
// f(u) is LINEAR, deliberately. Dynamic CMOS power is proportional to the
// activity factor at a fixed operating point, and utilisation is the cheapest
// available proxy for activity. The real curve is not linear — DVFS raises
// voltage with frequency, so power climbs faster than utilisation in the
// lower half of the range, which is why the Fan/Weber/Barroso datacentre
// model uses a concave P_idle + (P_max−P_idle)(2u − u^r). That refinement
// buys a few percent, and it buys it only once P_idle and P_max are known to
// better than a few percent. Ours are not: the platform envelope is the
// dominant error term by an order of magnitude (see powerPlatformProfiles).
// Spending a fitted exponent on a number whose endpoints carry ±40% would be
// false precision. Linear is monotonic, exact at both anchors, and explains
// itself in one line.
//
// CPU frequency is deliberately NOT folded in. Scaling by f/f_max would need
// the voltage curve to mean anything, and on the machines this exists for
// (ARM SoCs, guests) cpufreq is either absent or reports the governor's
// request rather than the silicon's state.

const (
	// powerEstimateEnvDisable hard-disables estimation on this host.
	powerEstimateEnvDisable = "BLOXOS_POWER_ESTIMATE"
	// powerEstimateEnvWatts overrides the platform envelope with a measured
	// one, as "<idle>:<max>" in watts at the wall — e.g. "3.4:14.5".
	powerEstimateEnvWatts = "BLOXOS_POWER_ESTIMATE_WATTS"

	// powerEstimateMinEnvelope is the narrowest envelope worth modelling. A
	// platform whose idle and max are within a watt of each other carries no
	// utilisation signal at all, so the model would report a constant.
	powerEstimateMinEnvelope = 1.0
	// powerEstimateMaxWatts bounds an operator-supplied envelope. Above this
	// the units are wrong (milliwatts typed as watts, a rack total, a typo).
	powerEstimateMaxWatts = 5000.0
	// powerEstimateStaleFactor bounds the gap between two utilisation reads.
	// Beyond it the interval spans a suspend or a starved goroutine and the
	// busy fraction over it is not this instant's utilisation.
	powerEstimateStaleFactor = 5
)

// powerProfile is a platform's power envelope in watts at the wall, plus
// where the two numbers came from. basis is for the startup log and for
// operators arguing with the output; it is not on the wire.
type powerProfile struct {
	idleWatts float64
	maxWatts  float64
	basis     string
}

func (p powerProfile) valid() bool {
	return p.idleWatts >= 0 &&
		p.maxWatts <= powerEstimateMaxWatts &&
		p.maxWatts-p.idleWatts >= powerEstimateMinEnvelope
}

// watts evaluates the model. Utilisation outside [0,1] is clamped rather than
// extrapolated: a busy fraction above 1 is a clock artefact, not 110% load.
func (p powerProfile) watts(util float64) float64 {
	if math.IsNaN(util) {
		util = 0
	}
	util = math.Min(math.Max(util, 0), 1)
	return p.idleWatts + (p.maxWatts-p.idleWatts)*util
}

// powerIdentity is what the agent can cheaply learn about the machine it is
// running on, and all the estimator is allowed to key on.
type powerIdentity struct {
	// dtCompatible is the device-tree "compatible" list, most specific first
	// — e.g. ["xunlong,orangepi-5", "rockchip,rk3588s"]. It is the ONLY
	// reliable board identity on ARM: /proc/cpuinfo there names a core
	// (Cortex-A55), not a machine, and on a big.LITTLE SoC it names whichever
	// core the reader happened to look at.
	dtCompatible []string
	// dmiProduct is the x86 equivalent, from /sys/class/dmi/id/product_name.
	dmiProduct string
	// cpuModel is the CPU model string, lowercased and whitespace-collapsed.
	cpuModel string
	// cores is the logical CPU count the kernel reports.
	cores int
	// arch is runtime.GOARCH.
	arch string
	// virtualized is true when the host is a guest.
	virtualized bool
}

// powerPlatformProfile is one row of the built-in table.
//
// Every row states a WHOLE-PLATFORM envelope, not a CPU TDP. That is the
// quantity the model needs and the quantity an operator can check with a
// plug-in meter, and it is the reason the table is keyed on the board where a
// board identity exists. A TDP is a thermal design limit for one component;
// turning it into platform watts needs a PSU efficiency, a board, drives and
// fans that the CPU model does not determine. The N100 rows below exist
// precisely because that gap is measurable: reviewers put the same N100 in
// different boxes and measured idle floors five watts apart.
//
// cores, when non-zero, is the core count of the platform the row describes.
// A machine reporting a different count is not that platform — it is a
// different SKU, or a slice of one — and the row is refused rather than
// stretched.
type powerPlatformProfile struct {
	// keys are matched against powerIdentity.matchKeys, first hit wins.
	keys  []string
	cores int
	idle  float64
	max   float64
	basis string
}

// powerPlatformProfiles is deliberately small. It is not the mechanism — the
// operator override is (see powerEstimateEnvWatts). Every row cites where its
// numbers came from, and every row's spread is stated, because the spread is
// the honest headline: these are envelopes for a CLASS of machine, and a
// specific machine sits somewhere inside one.
var powerPlatformProfiles = []powerPlatformProfile{
	// Rockchip RK3588 / RK3588S boards (Orange Pi 5, Rock 5B, and the many
	// clones). 4× Cortex-A76 + 4× Cortex-A55; Linux reports eight cores.
	// Measured whole-board: Orange Pi 5 ~3.3 W idle / ~7.3 W all-core;
	// Orange Pi 5 Max ~4.2 W idle / ~18.7 W under full CPU load. The load
	// figure spans 7–19 W across boards depending on RAM size, NVMe and how
	// hard the A76 cluster is clocked, so this row is worth roughly ±40% at
	// the top of its range and an operator who cares should measure once and
	// set the override.
	{
		keys:  []string{"dt:rockchip,rk3588", "dt:rockchip,rk3588s"},
		cores: 8,
		idle:  3.5,
		max:   12.0,
		basis: "RK3588 board measurements (7-19 W all-core across boards)",
	},
	// Raspberry Pi 5 (BCM2712). ~3.0 W idle headless, ~8.8 W with all four
	// cores loaded.
	{
		keys:  []string{"dt:raspberrypi,5-model-b", "dt:brcm,bcm2712"},
		cores: 4,
		idle:  3.0,
		max:   8.8,
		basis: "Raspberry Pi 5 board measurements",
	},
	// Raspberry Pi 4 Model B (BCM2711). ~2.6 W idle with Ethernet up,
	// ~6.4 W under a four-core stress test.
	{
		keys:  []string{"dt:raspberrypi,4-model-b", "dt:brcm,bcm2711"},
		cores: 4,
		idle:  2.6,
		max:   6.4,
		basis: "Raspberry Pi 4B board measurements",
	},
	// Intel N100 mini PCs — the one x86 class where a whole-box envelope is
	// defensible, because the box IS the board plus one drive. Reviewed
	// units idle between ~4 W and ~12 W and peak between ~20 W and ~29 W;
	// the five-watt idle spread between two boxes holding the same chip is
	// exactly why this is an envelope and not a specification.
	{
		keys:  []string{"cpu:intel n100", "cpu:intel processor n100"},
		cores: 4,
		idle:  6.0,
		max:   22.0,
		basis: "N100 mini-PC wall measurements (idle 4-12 W, load 20-29 W)",
	},
}

// matchKeys renders the identity as lookup keys, most specific first.
func (id powerIdentity) matchKeys() []string {
	keys := make([]string, 0, len(id.dtCompatible)+2)
	for _, c := range id.dtCompatible {
		if c = strings.ToLower(strings.TrimSpace(c)); c != "" {
			keys = append(keys, "dt:"+c)
		}
	}
	if id.cpuModel != "" {
		keys = append(keys, "cpu:"+id.cpuModel)
	}
	return keys
}

// lookupPlatformProfile finds the table row for this machine, if any.
func lookupPlatformProfile(id powerIdentity) (powerProfile, bool) {
	for _, key := range id.matchKeys() {
		for _, row := range powerPlatformProfiles {
			if !rowHasKey(row, key) {
				continue
			}
			if row.cores != 0 && id.cores != 0 && row.cores != id.cores {
				// Right silicon, wrong machine: a cut-down SKU, or a guest
				// holding a slice of one. Its platform is not this row's.
				continue
			}
			return powerProfile{
				idleWatts: row.idle,
				maxWatts:  row.max,
				basis:     "table " + key + " (" + row.basis + ")",
			}, true
		}
	}
	return powerProfile{}, false
}

func rowHasKey(row powerPlatformProfile, key string) bool {
	for _, k := range row.keys {
		if k == key {
			return true
		}
	}
	return false
}

// --- per-core fallback ---

const (
	// powerSoCBaseWatts is the non-CPU floor of a single-board computer: the
	// PMIC, DRAM in self-refresh, the PHY and one storage device.
	powerSoCBaseWatts = 1.6
	// powerSoCIdleCoreWatts is a clock-gated core's residual draw.
	powerSoCIdleCoreWatts = 0.15
	// powerSoCBusyCoreWatts is a loaded 64-bit ARM core's draw, averaged over
	// a big.LITTLE mix: a Cortex-A55 sits near 0.4 W and an A76 near 1.8 W at
	// the clocks these boards ship, so a homogeneous 1.1 W/core lands inside
	// a factor of ~2 either way for any plausible cluster arrangement.
	powerSoCBusyCoreWatts = 1.1
	// powerSoCMaxCores bounds the class this heuristic claims to cover. A
	// 64-core ARM server is not a single-board computer and its platform
	// envelope has nothing to do with these constants.
	powerSoCMaxCores = 16
	powerSoCMinCores = 2
)

// perCoreSoCProfile is the fallback for an arm64 board the table does not
// know. It exists because that class is narrow and bounded — SoC, RAM and
// storage on one board, whole-board envelopes empirically between 2 W and
// 20 W — so a per-core figure cannot be wrong by more than roughly a factor
// of two, and being wrong by a factor of two about a 10 W board is a
// different kind of claim from being wrong by a factor of two about a
// workstation.
//
// There is NO x86 equivalent, on purpose. An unknown x86 machine has an
// unknown PSU, an unknown number of drives and fans, and possibly a discrete
// GPU drawing more than everything else combined. No per-core constant
// survives that, so an unknown x86 machine reports nothing.
func perCoreSoCProfile(id powerIdentity) (powerProfile, bool) {
	if id.arch != "arm64" || id.virtualized {
		return powerProfile{}, false
	}
	if id.cores < powerSoCMinCores || id.cores > powerSoCMaxCores {
		return powerProfile{}, false
	}
	// A board with no device tree is not a board this heuristic understands.
	// Requiring the node also keeps the fallback off ARM servers running
	// ACPI and off emulated targets.
	if len(id.dtCompatible) == 0 {
		return powerProfile{}, false
	}
	n := float64(id.cores)
	idle := powerSoCBaseWatts + powerSoCIdleCoreWatts*n
	return powerProfile{
		idleWatts: idle,
		maxWatts:  idle + powerSoCBusyCoreWatts*n,
		basis:     fmt.Sprintf("arm64 SoC per-core heuristic (%d cores, no table entry)", id.cores),
	}, true
}

// --- operator override ---

// parsePowerEnvelopeOverride reads "<idle>:<max>" watts. It is the mechanism
// the table is only a convenience for: an operator with a plug-in meter can
// pin any machine's envelope exactly, and no table will ever cover every SoC.
//
// A malformed value is a hard error rather than a silent fallback to the
// table — an operator who typed an envelope wants that envelope, and quietly
// substituting a different one would be the worst of both.
func parsePowerEnvelopeOverride(raw string) (powerProfile, error) {
	raw = strings.TrimSpace(raw)
	idleStr, maxStr, ok := strings.Cut(raw, ":")
	if !ok {
		return powerProfile{}, fmt.Errorf("want \"<idle>:<max>\" watts, got %q", raw)
	}
	idle, err := strconv.ParseFloat(strings.TrimSpace(idleStr), 64)
	if err != nil {
		return powerProfile{}, fmt.Errorf("idle watts %q: %w", idleStr, err)
	}
	maxW, err := strconv.ParseFloat(strings.TrimSpace(maxStr), 64)
	if err != nil {
		return powerProfile{}, fmt.Errorf("max watts %q: %w", maxStr, err)
	}
	p := powerProfile{idleWatts: idle, maxWatts: maxW, basis: "operator override " + powerEstimateEnvWatts}
	if math.IsNaN(idle) || math.IsInf(idle, 0) || math.IsNaN(maxW) || math.IsInf(maxW, 0) || !p.valid() {
		return powerProfile{}, fmt.Errorf("envelope %g:%g rejected: need 0 <= idle, max <= %g, and max-idle >= %g",
			idle, maxW, powerEstimateMaxWatts, powerEstimateMinEnvelope)
	}
	return p, nil
}

// resolvePowerProfile applies the whole precedence chain: operator override,
// then the table, then the arm64 SoC heuristic, then nothing.
func resolvePowerProfile(id powerIdentity, getenv func(string) string) (powerProfile, bool) {
	if v := strings.TrimSpace(getenv(powerEstimateEnvWatts)); v != "" {
		p, err := parsePowerEnvelopeOverride(v)
		if err != nil {
			logPowerEstimate("%s=%q rejected, not estimating: %v", powerEstimateEnvWatts, v, err)
			return powerProfile{}, false
		}
		return p, true
	}
	if p, ok := lookupPlatformProfile(id); ok && p.valid() {
		return p, true
	}
	if p, ok := perCoreSoCProfile(id); ok && p.valid() {
		return p, true
	}
	return powerProfile{}, false
}

// powerEstimateDisabled reports the operator opt-out. Estimation is the one
// backend that produces a number nobody measured, so it gets its own switch
// separate from BLOXOS_POWER_HISTORY: an operator can keep every real counter
// and refuse the model.
func powerEstimateDisabled(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv(powerEstimateEnvDisable))) {
	case "0", "off", "false", "no":
		return true
	}
	return false
}

// --- the sampler ---

// utilSource yields cumulative busy and total CPU time. Two reads make a
// utilisation; one read makes nothing.
type utilSource interface {
	// read returns busy and total cumulative CPU time in the same arbitrary
	// unit, or false when utilisation cannot be determined right now.
	read() (busy, total float64, ok bool)
}

// estimateSampler is the powerSource that models platform watts. It primes on
// its first sample exactly as an energy counter does, because a utilisation
// over an unknown interval is not a utilisation.
type estimateSampler struct {
	profile   powerProfile
	util      utilSource
	lastBusy  float64
	lastTotal float64
	lastAt    time.Time
	primed    bool
	interval  time.Duration
}

func newEstimateSampler(p powerProfile, u utilSource, interval time.Duration) *estimateSampler {
	return &estimateSampler{profile: p, util: u, interval: interval}
}

func (e *estimateSampler) source() string { return powerhistory.SourceEstimateUtil }

// sample models watts for the interval since the previous successful read.
//
// Every failure path reports unavailable and re-primes. There is no stale
// value, no carried-forward utilisation and no zero: a modelled backend that
// invented continuity where it had none would be indistinguishable from a
// broken counter, which is the one thing the whole feature is built to avoid.
func (e *estimateSampler) sample(now time.Time) (float64, bool) {
	busy, total, ok := e.util.read()
	if !ok {
		e.primed = false
		return 0, false
	}
	prevBusy, prevTotal, prevAt, primed := e.lastBusy, e.lastTotal, e.lastAt, e.primed
	e.lastBusy, e.lastTotal, e.lastAt, e.primed = busy, total, now, true
	if !primed {
		return 0, false
	}
	if dt := now.Sub(prevAt); dt <= 0 || dt > time.Duration(powerEstimateStaleFactor)*e.interval {
		return 0, false
	}
	dTotal := total - prevTotal
	dBusy := busy - prevBusy
	// Counters only advance. Anything else is a CPU hotplug, a reset or a
	// short read: no utilisation for this tick.
	if dTotal <= 0 || dBusy < 0 || dBusy > dTotal {
		return 0, false
	}
	w := e.profile.watts(dBusy / dTotal)
	if w < powerSystemMinWatts || w > powerRateMaxWatts {
		return 0, false
	}
	return w, true
}
