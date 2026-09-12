package main

// Fleet power history — GET /api/fleet/power/history.
//
// The per-machine endpoint (power_history.go) answers "what did THIS machine
// draw". This one answers "what is the fleet drawing", which is a harder
// question to answer honestly, because the fleet is never fully instrumented:
//
//   - Only some machines have a whole-platform counter at all (RAPL psys,
//     battery discharge, BMC DCMI, a board-level hwmon shunt). The rest report
//     component domains, or nothing.
//   - A machine with no counter may report a MODELLED figure instead, labelled
//     with an estimator backend. An estimate is not a measurement and is never
//     added to one here: measured and estimated are two parallel series all the
//     way out to the JSON, so a caller physically cannot render one merged
//     number without doing the addition itself and owning it.
//   - Machines join, leave, go offline mid-window, and declare collection gaps.
//
// So this endpoint reports a series AND its coverage, and every total it emits
// is qualified by how many machines stand behind it. `Complete` is the
// fleet-scale equivalent of the dashboard's `gpuPowerComplete`: true only when
// every machine in the fleet contributed to every bucket. Anything else is a
// partial sum and the response says so.
//
// WHAT IS NOT HERE, DELIBERATELY:
//   - No fleet PEAK. Peaks are sampled maxima on independent, unsynchronised
//     schedules; adding them produces a number no instant ever measured. The
//     per-machine chart already carries peak where peak is meaningful.
//   - No cross-domain sum. system, cpu, dram and gpu are disjoint scopes (see
//     proto/powerhistory), so each is aggregated on its own and they are
//     returned side by side, never added.
//   - No interpolation across gaps. Unobserved time contributes zero energy.
//
// ENERGY IS AN EXTRAPOLATION, NOT A FLOOR. This comment previously claimed
// every energy figure was a lower bound. It is not, and the claim is withdrawn.
// A window's mean is the mean of the samples that SUCCEEDED in it — statsAcc
// averages over n successful reads (agent/power_history.go) — and this file
// then weights that mean by the window's whole span. One successful 300 W read
// in a 30 s window contributes 9000 W*s as if all 30 seconds had been observed.
// The unread seconds could have drawn far less, so the result is neither an
// upper nor a lower bound: it is a model built from sampled power.
//
// Samples and expected samples are therefore carried through to the response
// so a client can disclose how much of each window was actually read, and no
// consumer may describe these figures as "at least" or as measured energy.

import (
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
	"github.com/labstack/echo/v4"
)

// fleetPowerDomainGPU aggregates Bucket.GPUTotal — the agent's own complete
// simultaneous GPU observation. It is not one of the protocol's scalar
// domains, so it carries no source label; GPU power is always measured.
const fleetPowerDomainGPU = "gpu"

// fleetPowerDomains is the response's fixed domain order. Every domain is
// always present, empty or not, so a client's shape never depends on what the
// fleet happened to report.
var fleetPowerDomains = []string{
	powerhistory.DomainSystem,
	powerhistory.DomainCPU,
	powerhistory.DomainDRAM,
	fleetPowerDomainGPU,
}

const (
	fleetPowerKindMeasured  = "measured"
	fleetPowerKindEstimated = "estimated"
	// fleetPowerKindUnknown is a reading whose backend this hub does not
	// recognise. It is NOT measured and NOT modelled: it is unclassified, and
	// it is kept out of both totals rather than being folded into either.
	fleetPowerKindUnknown = "unknown"
)

// fleetPowerMeasuredSources is the set of backend identifiers this hub knows
// to be MEASUREMENTS. Classification fails closed: a label that is neither in
// this set, nor the hwmon prefix, nor a known modelled source, is reported as
// fleetPowerKindUnknown.
//
// Why fail closed. The previous rule was "estimated if IsEstimatedSource, else
// measured", which silently promoted every unrecognised label to measured. A
// future modelled backend reaching an older hub would therefore be presented as
// a counter reading — the exact failure the protocol's no-boolean rule exists to
// prevent. Unknown provenance must read as unknown, never as measurement.
var fleetPowerMeasuredSources = map[string]bool{
	powerhistory.SourceRAPLPsys:    true,
	powerhistory.SourceRAPLPackage: true,
	powerhistory.SourceRAPLDRAM:    true,
	powerhistory.SourceBattery:     true,
	powerhistory.SourceIPMIDCMI:    true,
}

// fleetPowerClassify maps a domain's recorded backend label to a kind.
//
// The unlabelled case is preserved deliberately as MEASURED: the only agents
// that emit scalar statistics without a source label predate source labelling
// entirely, and what those builds measured was RAPL. Reclassifying them as
// unknown would blank working history across every fleet still running them.
// An estimator has always labelled itself, so nothing modelled can arrive
// unlabelled.
func fleetPowerClassify(domain, source string) string {
	// GPU totals carry no source label by construction (see
	// fleetPowerDomainGPU): the agent only emits one from a complete
	// simultaneous observation of real devices.
	if domain == fleetPowerDomainGPU {
		return fleetPowerKindMeasured
	}
	if source == "" {
		return fleetPowerKindMeasured // pre-labelling agent; RAPL by definition
	}
	if powerhistory.IsEstimatedSource(source) {
		return fleetPowerKindEstimated
	}
	if fleetPowerMeasuredSources[source] {
		return fleetPowerKindMeasured
	}
	// hwmon backends name the chip they found, so the identifier is open-ended
	// by design and cannot be enumerated here. This classifies the reading as a
	// MEASUREMENT only; it says nothing about what the sensor is wired across.
	// A shunt monitor reports whatever rail it sits on, so scope belongs to the
	// agent's domain assignment, never to this label.
	if strings.HasPrefix(source, powerhistory.SourceHwmonPrefix) {
		return fleetPowerKindMeasured
	}
	return fleetPowerKindUnknown
}

// Modelled-ness is still owned by powerhistory.IsEstimatedSource: this file
// never pattern-matches its own way to "that looks like an estimate".
//
// What changed is the DEFAULT. The rule used to be "estimated if
// IsEstimatedSource, else measured", which quietly promoted every label this
// hub did not recognise — including a modelled backend added after this build —
// to measured. Classification now fails closed in fleetPowerClassify: known
// measured backends and the open-ended hwmon prefix are measured, the known
// modelled source is modelled, and anything else is UNKNOWN and kept out of
// both totals.
//
// The one deliberate exception is an UNLABELLED reading, which stays measured:
// the only agents emitting unlabelled statistics predate source labelling
// entirely, and what they had was RAPL. An estimator has always labelled
// itself, so nothing modelled can arrive unlabelled.

// fleetPowerSourceUnlabelled stands in for a scalar-domain reading that named
// no backend, so "which sources are behind this number" is answerable even for
// a fleet still running pre-labelling agents.
const fleetPowerSourceUnlabelled = "unlabelled"

type fleetPowerSpec struct {
	window time.Duration
	bucket time.Duration
}

// fleetPowerPeriods mirrors the ?period vocabulary of the metrics-history
// endpoint, minus 7d: power history is retained for 24 hours, so a 7-day
// window could only ever be one day of data drawn across seven days of axis.
// The bucket width rises with the window so the point count stays bounded
// (30–96 points) whatever the fleet size.
var fleetPowerPeriods = map[string]fleetPowerSpec{
	"30m": {30 * time.Minute, time.Minute},
	"1h":  {time.Hour, time.Minute},
	"6h":  {6 * time.Hour, 5 * time.Minute},
	"24h": {24 * time.Hour, 15 * time.Minute},
}

const fleetPowerDefaultPeriod = "6h"

const (
	// fleetPowerDefaultMaxRecords bounds the rows read for one request. Each
	// row's payload is JSON-decoded, so this is the real cost knob: a 24h
	// window is 2880 30-second buckets per machine, i.e. ~17k rows for six
	// machines and ~288k for a hundred. Truncation drops the OLDEST rows and
	// is reported, never silently absorbed.
	fleetPowerDefaultMaxRecords = 20000
	fleetPowerMaxRecordsCap     = 60000
	// fleetPowerMaxSilentListed bounds the silent-machine id list so a large
	// under-instrumented fleet cannot inflate the response. The COUNT is
	// always exact; only the list is capped.
	fleetPowerMaxSilentListed = 100
	// fleetPowerMaxSourcesListed bounds the per-domain backend list.
	fleetPowerMaxSourcesListed = 16
	// fleetPowerFutureSkewToleranceMS is how far ahead of the hub clock an agent
	// window may end and still be accepted as a real observation time.
	//
	// It is deliberately SMALL — timestamp resolution and transit jitter, not a
	// clock-drift allowance. An earlier draft used two minutes, which meant a
	// window stamped 119 seconds in the future became "the newest observation"
	// and any ordinary age check then read it as current. A reading cannot be
	// newer than now; anything meaningfully ahead of the hub clock is skew of
	// unknown size, and unknown age must never qualify as a current reading.
	//
	// Skew does not discard data: such a reading still contributes its power to
	// history. What it loses is the right to set freshness.
	fleetPowerFutureSkewToleranceMS = 2_000
)

// --- response ---

// FleetPowerBucket is one time bucket. Watts are nil when nothing reported —
// nil is unavailable, zero is a real reading, exactly as in the protocol.
// Measured and estimated are never added together here.
type FleetPowerBucket struct {
	StartUnixMS int64 `json:"start_unix_ms"`
	EndUnixMS   int64 `json:"end_unix_ms"`
	// MeasuredWatts is the SUM OF REPORTED SAMPLE MEANS over machines with a
	// real counter: each machine's mean over the windows it reported inside
	// this bucket, added together. It is NOT a fleet mean over a common interval — the
	// contributing machines' windows need not overlap, so a machine reporting
	// only in the first half of a bucket and another only in the second half
	// still sum. Present it with its contributor count, never as "the fleet
	// drew N watts at this moment".
	MeasuredWatts    *float64 `json:"measured_watts"`
	MeasuredMachines int      `json:"measured_machines"`
	// MeasuredWindowSeconds is the summed span of the windows behind
	// MeasuredWatts. Compare against BucketSeconds × MeasuredMachines to see
	// what fraction of the bucket those machines actually reported over.
	MeasuredWindowSeconds float64 `json:"measured_window_seconds"`
	// EstimatedWatts is the same sum over machines reporting a MODELLED
	// figure. It is a separate series and must be labelled as an estimate
	// wherever it is shown.
	EstimatedWatts    *float64 `json:"estimated_watts"`
	EstimatedMachines int      `json:"estimated_machines"`
	// EstimatedWindowSeconds mirrors MeasuredWindowSeconds for the modelled
	// series.
	EstimatedWindowSeconds float64 `json:"estimated_window_seconds"`
	// UnknownMachines counts contributors in this bucket whose backend this
	// hub could not classify. Their watts are deliberately NOT summed into
	// either series; the count exists so the omission is visible rather than
	// silent.
	UnknownMachines int `json:"unknown_machines"`
	// Gap marks that a contributing machine declared local collection loss
	// immediately before a window landing in this bucket. Missing data is not
	// zero power.
	Gap bool `json:"gap"`
}

// FleetPowerKind is the window-scale roll-up for one domain and one kind.
type FleetPowerKind struct {
	// Machines contributed at least one reading anywhere in the window.
	Machines int `json:"machines"`
	// EnergyKWh EXTRAPOLATES each window's sampled mean across that window's
	// whole span. It is NOT a floor and NOT measured energy: a window whose
	// reads mostly failed still contributes its full span. Present it as
	// modelled from sampled power, with SampleCount/ExpectedSampleCount
	// disclosing how much was actually read.
	EnergyKWh float64 `json:"energy_kwh"`
	// ObservedMachineSeconds is DEPRECATED and was misnamed: it is the sum of
	// reporting-window SPANS, not time actually observed. A 30 s window built
	// from one successful read still counts 30. Retained unchanged for
	// existing clients; use ReportingWindowSeconds and the sample counts.
	ObservedMachineSeconds float64 `json:"observed_machine_seconds"`
	// ReportingWindowSeconds is the honest name for the value above: summed
	// spans of the windows that contributed.
	ReportingWindowSeconds float64 `json:"reporting_window_seconds"`
	// SampleCount is the number of individual sensor reads that SUCCEEDED
	// across those windows; ExpectedSampleCount is how many the agent expected
	// to take.
	//
	// Read them as evidence WEIGHT, not as a fraction of time observed. A low
	// ratio does not locate a gap and a high one does not prove continuity:
	// backends sample at deliberately different cadences, and IPMI samples
	// slowly by design, so a small count can be a fully healthy backend. The
	// counts must never be multiplied into a duration.
	SampleCount         int64 `json:"sample_count"`
	ExpectedSampleCount int64 `json:"expected_sample_count"`
	// LatestObservationEndUnixMS is the end of the most recent contributing
	// AGENT window, not a chart bin edge. Freshness must be judged from this.
	// Zero when nothing contributed.
	LatestObservationEndUnixMS int64 `json:"latest_observation_end_unix_ms"`
	// Sources are the backend ids behind this number, sorted.
	Sources []string `json:"sources"`
}

type FleetPowerDomain struct {
	Domain    string             `json:"domain"`
	Buckets   []FleetPowerBucket `json:"buckets"`
	Measured  FleetPowerKind     `json:"measured"`
	Estimated FleetPowerKind     `json:"estimated"`
	// Unknown holds contributors whose backend this hub cannot classify. They
	// are reported separately and are never folded into Measured or Estimated.
	Unknown FleetPowerKind `json:"unknown"`
	// ReportingMachines is the union of measured, estimated AND unknown
	// machines: a machine reporting in several kinds or buckets is counted
	// once. It answers "how many machines are behind this domain at all",
	// which is why unclassified contributors are included here even though
	// their watts are excluded from both series.
	ReportingMachines int `json:"reporting_machines"`
	// AllMachinesContributed means every known machine contributed at least one
	// reading to every bucket in this window. It is a PRESENCE statement and
	// nothing more: it says each machine was heard from, not that the bucket
	// was measured throughout. Use it to distinguish "some machines dropped
	// out" from "everyone reported"; never as measurement coverage.
	AllMachinesContributed bool `json:"all_machines_contributed"`
	// Complete is always FALSE and is retained only so existing clients keep
	// parsing. Stored readings cannot establish complete measurement of a
	// bucket: window spans are not enforced non-overlapping at ingest, a window
	// is binned by its END so it can credit time spent in the previous bucket,
	// and a window's mean comes only from the reads that succeeded within it.
	// Anything needing "did everyone report" should read
	// AllMachinesContributed instead.
	Complete bool `json:"complete"`
}

type FleetPowerCoverage struct {
	// MachinesTotal is every machine the fleet contains, including ones that
	// cannot report power. It is the honest denominator for "N of M".
	MachinesTotal int `json:"machines_total"`
	// MachinesReporting contributed a reading in ANY domain in this window.
	MachinesReporting int      `json:"machines_reporting"`
	MachinesSilent    int      `json:"machines_silent"`
	SilentMachineIDs  []string `json:"silent_machine_ids"`
	// MachinesAPIPolled are API-polled machines. They have no agent and so can
	// never report power; naming them explains part of the shortfall instead
	// of leaving the reader to wonder which machines are broken.
	MachinesAPIPolled int `json:"machines_api_polled"`
	// DegradedMachines reported reduced recording coverage in the last 24h.
	DegradedMachines int `json:"degraded_machines"`
	// GapsDeclared is the number of machine/stream ranges declared permanently
	// lost in the last 24h. It is seq-scoped, not time-scoped, so it is a
	// warning rather than a position on the axis.
	GapsDeclared int `json:"gaps_declared"`
	// GapBuckets counts buckets in this window carrying a declared gap.
	GapBuckets int `json:"gap_buckets"`
	// MachinesSkewed counts machines whose newest window ended implausibly far
	// ahead of the hub clock. Their power still contributes; what is withheld
	// is their claim to be the freshest observation, because a skewed clock
	// would otherwise make stale fleet data read as current.
	MachinesSkewed int `json:"machines_skewed"`
	// Truncated means the record cap was hit and the OLDEST part of the window
	// was not read. WindowStartUnixMS below is then later than requested.
	Truncated bool `json:"truncated"`
}

type FleetPowerHistory struct {
	// Period is the RESOLVED period, which may differ from an unrecognised
	// request. Clients label the axis from this, never from what they asked.
	Period        string `json:"period"`
	BucketSeconds int    `json:"bucket_seconds"`
	// StartUnixMS/EndUnixMS bound the buckets actually returned.
	StartUnixMS     int64              `json:"start_unix_ms"`
	EndUnixMS       int64              `json:"end_unix_ms"`
	GeneratedUnixMS int64              `json:"generated_unix_ms"`
	Domains         []FleetPowerDomain `json:"domains"`
	Coverage        FleetPowerCoverage `json:"coverage"`
}

// --- aggregation (pure — no I/O, unit-tested directly) ---

// fleetPowerRecord is one stored 30-second bucket with the machine that
// produced it. Times come from the indexed columns, statistics from the
// payload, exactly as the row was written.
type fleetPowerRecord struct {
	MachineID string
	StartMS   int64
	EndMS     int64
	Expected  int
	Bucket    powerhistory.Bucket
}

// fleetPowerInputs is everything the aggregation needs that is not a reading.
type fleetPowerInputs struct {
	Period       string
	Spec         fleetPowerSpec
	StartMS      int64
	BucketCount  int
	Now          int64
	MachineIDs   []string
	APIPolled    int
	Degraded     int
	GapsDeclared int
	Truncated    bool
	OldestReadMS int64 // 0 when nothing was truncated
}

type fleetPowerCell struct {
	wattSeconds float64
	seconds     float64
	samples     int64
	expected    int64
}

type fleetPowerCellKey struct {
	domain  string
	kind    string
	bucket  int
	machine string
}

type fleetPowerSeriesKey struct {
	domain string
	kind   string
}

// aggregateFleetPower turns stored buckets into the response. It never
// fabricates: a bucket no machine covered stays nil, an estimate never lands
// in the measured column, and a partial bucket reports how partial it is.
func aggregateFleetPower(records []fleetPowerRecord, in fleetPowerInputs) FleetPowerHistory {
	bucketMS := in.Spec.bucket.Milliseconds()
	endMS := in.StartMS + int64(in.BucketCount)*bucketMS

	cells := map[fleetPowerCellKey]*fleetPowerCell{}
	seriesMachines := map[fleetPowerSeriesKey]map[string]bool{}
	seriesSources := map[fleetPowerSeriesKey]map[string]bool{}
	seriesEnergy := map[fleetPowerSeriesKey]float64{}
	seriesObserved := map[fleetPowerSeriesKey]float64{}
	seriesSamples := map[fleetPowerSeriesKey]int64{}
	seriesExpected := map[fleetPowerSeriesKey]int64{}
	seriesLatestEnd := map[fleetPowerSeriesKey]int64{}
	skewed := map[string]bool{}
	gapBuckets := map[string]map[int]bool{} // domain -> bucket index
	reporting := map[string]bool{}

	for i := range records {
		rec := &records[i]
		if rec.EndMS <= in.StartMS || rec.EndMS > endMS {
			continue
		}
		// A reading covers the half-open interval [StartMS, EndMS), so the
		// bucket it belongs to is the one holding the instant just BEFORE its
		// end. Mapping EndMS directly pushed a window ending exactly on a
		// bucket edge into the NEXT bucket, even though none of its time was
		// spent there: a 30 s window covering [30s, 60s) of a 60 s bucket was
		// attributed to the following minute. That both misplaced the power and
		// made full temporal coverage of a bucket impossible to express.
		idx := int((rec.EndMS - 1 - in.StartMS) / bucketMS)
		if idx < 0 || idx >= in.BucketCount {
			continue
		}
		// Window duration is the reading's own window, validated at ingest to
		// be positive and at most an hour. It is what weights the mean and
		// what turns watts into energy.
		windowSeconds := float64(rec.EndMS-rec.StartMS) / 1000
		if windowSeconds <= 0 {
			continue
		}

		for _, domain := range fleetPowerDomains {
			stats, source, maxWatts := fleetPowerDomainStats(&rec.Bucket, domain)
			if stats == nil {
				continue
			}
			// Stored rows were validated at ingest, but a row is re-checked
			// against the same bounds rather than trusted: a corrupt or
			// hand-edited payload must not become a fleet total.
			if err := validPowerStats(stats, rec.Expected, maxWatts); err != nil {
				continue
			}
			if stats.Samples <= 0 || stats.MeanWatts == nil {
				continue
			}
			mean := *stats.MeanWatts
			if math.IsNaN(mean) || math.IsInf(mean, 0) {
				continue
			}

			kind := fleetPowerClassify(domain, source)
			series := fleetPowerSeriesKey{domain: domain, kind: kind}

			cellKey := fleetPowerCellKey{domain: domain, kind: kind, bucket: idx, machine: rec.MachineID}
			cell := cells[cellKey]
			if cell == nil {
				cell = &fleetPowerCell{}
				cells[cellKey] = cell
			}
			cell.wattSeconds += mean * windowSeconds
			cell.seconds += windowSeconds
			cell.samples += int64(stats.Samples)
			cell.expected += int64(rec.Expected)

			// Freshness is judged from the agent's own window end, never from
			// a chart bin edge (a bin end can sit up to a bucket width in the
			// future). A window ending implausibly far ahead of the hub clock
			// is clock skew: it must not count as the newest observation, or a
			// skewed agent would make the whole fleet look freshly reported.
			if rec.EndMS <= in.Now+fleetPowerFutureSkewToleranceMS {
				if rec.EndMS > seriesLatestEnd[series] {
					seriesLatestEnd[series] = rec.EndMS
				}
			} else {
				skewed[rec.MachineID] = true
			}

			if seriesMachines[series] == nil {
				seriesMachines[series] = map[string]bool{}
			}
			seriesMachines[series][rec.MachineID] = true
			if seriesSources[series] == nil {
				seriesSources[series] = map[string]bool{}
			}
			seriesSources[series][fleetPowerSourceLabel(domain, source)] = true
			seriesEnergy[series] += mean * windowSeconds / 3_600_000 // W·s -> kWh
			seriesObserved[series] += windowSeconds
			seriesSamples[series] += int64(stats.Samples)
			seriesExpected[series] += int64(rec.Expected)
			reporting[rec.MachineID] = true

			if rec.Bucket.GapBefore {
				if gapBuckets[domain] == nil {
					gapBuckets[domain] = map[int]bool{}
				}
				gapBuckets[domain][idx] = true
			}
		}
	}

	total := len(in.MachineIDs)
	domains := make([]FleetPowerDomain, 0, len(fleetPowerDomains))
	gapBucketIndices := map[int]bool{}

	for _, domain := range fleetPowerDomains {
		buckets := make([]FleetPowerBucket, in.BucketCount)
		complete := total > 0 && in.BucketCount > 0
		for idx := 0; idx < in.BucketCount; idx++ {
			b := FleetPowerBucket{
				StartUnixMS: in.StartMS + int64(idx)*bucketMS,
				EndUnixMS:   in.StartMS + int64(idx+1)*bucketMS,
				Gap:         gapBuckets[domain][idx],
			}
			b.MeasuredWatts, b.MeasuredMachines, b.MeasuredWindowSeconds = fleetPowerBucketSum(cells, domain, fleetPowerKindMeasured, idx)
			b.EstimatedWatts, b.EstimatedMachines, b.EstimatedWindowSeconds = fleetPowerBucketSum(cells, domain, fleetPowerKindEstimated, idx)
			_, b.UnknownMachines, _ = fleetPowerBucketSum(cells, domain, fleetPowerKindUnknown, idx)
			// Presence is tracked, but it is NOT sufficient for Complete; see
			// the assignment after this loop. A machine reporting both a
			// measured and an estimated figure would be double-counted by a
			// naive sum of the two counts, so presence is judged on the union
			// of machines.
			if fleetPowerBucketMachineCount(cells, domain, idx) < total {
				complete = false
			}
			if b.Gap {
				gapBucketIndices[idx] = true
			}
			buckets[idx] = b
		}

		measuredKey := fleetPowerSeriesKey{domain: domain, kind: fleetPowerKindMeasured}
		estimatedKey := fleetPowerSeriesKey{domain: domain, kind: fleetPowerKindEstimated}
		unknownKey := fleetPowerSeriesKey{domain: domain, kind: fleetPowerKindUnknown}

		buildKind := func(key fleetPowerSeriesKey) FleetPowerKind {
			return FleetPowerKind{
				Machines:                   len(seriesMachines[key]),
				EnergyKWh:                  seriesEnergy[key],
				ObservedMachineSeconds:     seriesObserved[key],
				ReportingWindowSeconds:     seriesObserved[key],
				SampleCount:                seriesSamples[key],
				ExpectedSampleCount:        seriesExpected[key],
				LatestObservationEndUnixMS: seriesLatestEnd[key],
				Sources:                    sortedCapped(seriesSources[key], fleetPowerMaxSourcesListed),
			}
		}

		union := map[string]bool{}
		for id := range seriesMachines[measuredKey] {
			union[id] = true
		}
		for id := range seriesMachines[estimatedKey] {
			union[id] = true
		}
		for id := range seriesMachines[unknownKey] {
			union[id] = true
		}

		domains = append(domains, FleetPowerDomain{
			Domain:            domain,
			Buckets:           buckets,
			Measured:          buildKind(measuredKey),
			Estimated:         buildKind(estimatedKey),
			Unknown:           buildKind(unknownKey),
			ReportingMachines: len(union),
			// Complete is reported FALSE unconditionally, and that is a
			// deliberate, conservative choice rather than an oversight.
			//
			// What the stored data can support is "every machine contributed
			// something to every bucket" (computed above as `complete`). What
			// Complete was being read as is "this bucket is a full measurement
			// of the fleet over its whole span", and nothing here can establish
			// that:
			//
			//   - Summed window spans are not a coverage measure. Ingest
			//     validates each span as positive and bounded; it does NOT
			//     enforce that a machine's windows are non-overlapping across
			//     streams, so spans can double-count.
			//   - A window is binned by its END, so a [45s,75s) record credits
			//     all 30 of its seconds to the bucket it ends in, including
			//     time spent in the previous one.
			//   - A window's mean comes from the reads that SUCCEEDED in it, so
			//     even exact span coverage is not continuous measurement.
			//
			// Sample counts cannot rescue it either: backends sample at
			// deliberately different cadences (IPMI slowly by design), so a low
			// ratio is not evidence of a gap. Until observations carry their own
			// measured duration, "complete" is not a claim this data can make.
			AllMachinesContributed: complete && len(union) == total,
			Complete:               false,
		})
	}

	var silent []string
	for _, id := range in.MachineIDs {
		if !reporting[id] {
			silent = append(silent, id)
		}
	}
	sort.Strings(silent)
	listed := silent
	if len(listed) > fleetPowerMaxSilentListed {
		listed = listed[:fleetPowerMaxSilentListed]
	}
	if listed == nil {
		listed = []string{}
	}

	// A truncated read did not reach the requested start, so the window the
	// response describes is the one that was actually read.
	startMS := in.StartMS
	if in.Truncated && in.OldestReadMS > startMS {
		startMS = in.OldestReadMS
	}

	return FleetPowerHistory{
		Period:          in.Period,
		BucketSeconds:   int(in.Spec.bucket / time.Second),
		StartUnixMS:     startMS,
		EndUnixMS:       endMS,
		GeneratedUnixMS: in.Now,
		Domains:         domains,
		Coverage: FleetPowerCoverage{
			MachinesTotal:     total,
			MachinesReporting: len(reporting),
			MachinesSilent:    len(silent),
			SilentMachineIDs:  listed,
			MachinesAPIPolled: in.APIPolled,
			DegradedMachines:  in.Degraded,
			GapsDeclared:      in.GapsDeclared,
			GapBuckets:        len(gapBucketIndices),
			MachinesSkewed:    len(skewed),
			Truncated:         in.Truncated,
		},
	}
}

// fleetPowerDomainStats returns one domain's statistics, the backend label
// recorded for it, and the sanity cap that applies to it.
func fleetPowerDomainStats(bk *powerhistory.Bucket, domain string) (*powerhistory.Stats, string, float64) {
	switch domain {
	case powerhistory.DomainSystem:
		return bk.System, bk.SourceFor(domain), powerMaxWattsDomain
	case powerhistory.DomainCPU:
		return bk.CPU, bk.SourceFor(domain), powerMaxWattsDomain
	case powerhistory.DomainDRAM:
		return bk.DRAM, bk.SourceFor(domain), powerMaxWattsDomain
	case fleetPowerDomainGPU:
		// GPUTotal is the agent's own complete simultaneous observation, never
		// a sum of independent per-device readings assembled here.
		return bk.GPUTotal, "", powerMaxWattsGPUTotal
	}
	return nil, "", 0
}

func fleetPowerSourceLabel(domain, source string) string {
	if source != "" {
		return source
	}
	if domain == fleetPowerDomainGPU {
		return fleetPowerDomainGPU
	}
	return fleetPowerSourceUnlabelled
}

// fleetPowerBucketSum adds up each contributing machine's time-weighted mean for
// one bucket, and reports how many machines contributed and the summed span of
// the windows behind them.
//
// The result is a SUM OF REPORTED SAMPLE MEANS, not a fleet mean over a shared
// interval. An earlier comment here justified the addition on the grounds that
// "mean power over a common interval is additive". That is true, and it is not
// what this computes: contributors' windows need not share an interval at all.
// Two machines each reporting 100 W over opposite halves of a bucket produce
// 200 W, which the fleet never drew at any instant.
//
// Summed peaks would be worse still, which is why no peak is emitted.
//
// windowSeconds is returned so a consumer can see the spans involved. It is a
// sum of raw spans, NOT a deduplicated measure of time covered: ingest does not
// enforce that a machine's windows are non-overlapping across streams, and a
// window is binned by its end, so its span may include time spent in the
// previous bucket. Do not render it as a percentage of the bucket observed.
func fleetPowerBucketSum(cells map[fleetPowerCellKey]*fleetPowerCell, domain, kind string, idx int) (*float64, int, float64) {
	var sum, windowSeconds float64
	machines := 0
	for key, cell := range cells {
		if key.domain != domain || key.kind != kind || key.bucket != idx || cell.seconds <= 0 {
			continue
		}
		sum += cell.wattSeconds / cell.seconds
		windowSeconds += cell.seconds
		machines++
	}
	if machines == 0 {
		return nil, 0, 0
	}
	return &sum, machines, windowSeconds
}

// fleetPowerBucketMachineCount counts DISTINCT machines behind a bucket across
// both kinds.
func fleetPowerBucketMachineCount(cells map[fleetPowerCellKey]*fleetPowerCell, domain string, idx int) int {
	seen := map[string]bool{}
	for key, cell := range cells {
		if key.domain != domain || key.bucket != idx || cell.seconds <= 0 {
			continue
		}
		seen[key.machine] = true
	}
	return len(seen)
}

func sortedCapped(set map[string]bool, limit int) []string {
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// --- handler ---

// resolveFleetPowerPeriod maps ?period onto a supported window, falling back to
// the default for anything unrecognised. The RESOLVED name is echoed in the
// response so a client cannot label an axis with a period it did not get.
func resolveFleetPowerPeriod(raw string) (string, fleetPowerSpec) {
	if spec, ok := fleetPowerPeriods[raw]; ok {
		return raw, spec
	}
	return fleetPowerDefaultPeriod, fleetPowerPeriods[fleetPowerDefaultPeriod]
}

// handleFleetPowerHistory serves GET /api/fleet/power/history.
//
// Scope is fleet.read, the same as the per-machine power endpoint: this is the
// same telemetry, aggregated. There is no per-user machine visibility in this
// product — fleet.read means the whole fleet — so "machines the caller may
// see" and "every machine" are the same set, and the denominator is honest.
func (s *Server) handleFleetPowerHistory(c echo.Context) error {
	period, spec := resolveFleetPowerPeriod(c.QueryParam("period"))
	maxRecords := clampQueryInt(c, "max_records", fleetPowerDefaultMaxRecords, 1, fleetPowerMaxRecordsCap)

	now := time.Now()
	bucketMS := spec.bucket.Milliseconds()
	bucketCount := int(spec.window / spec.bucket)
	// The window ends at the end of the bucket the clock is currently in, so
	// the newest reading always has a bucket to land in.
	endMS := (now.UnixMilli()/bucketMS + 1) * bucketMS
	startMS := endMS - int64(bucketCount)*bucketMS

	machineIDs, err := s.fleetPowerMachineIDs()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to query machines"})
	}

	// Newest-first with a cap: if the cap bites, what is dropped is the oldest
	// end of the window, never the current readings.
	//
	// The predicate is (startMS, endMS] and MUST match the interval rule the
	// aggregator applies. A reading covers [start, end), so one ending exactly
	// at endMS lies inside the requested window and one ending exactly at
	// startMS lies entirely before it. The previous [startMS, endMS) predicate
	// fetched the reading that ended before the window and dropped the most
	// recently completed one, leaving the newest bucket permanently a reading
	// short. Changing the aggregator alone would not have fixed that: rows the
	// query never returns cannot be re-binned.
	rows, err := s.db.Query(`SELECT machine_id, start_unix_ms, end_unix_ms, expected_samples, payload
		FROM power_history_records
		WHERE end_unix_ms > ? AND end_unix_ms <= ?
		ORDER BY end_unix_ms DESC LIMIT ?`, startMS, endMS, maxRecords)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to query power history"})
	}
	records := make([]fleetPowerRecord, 0, 256)
	oldestReadMS := int64(0)
	for rows.Next() {
		var rec fleetPowerRecord
		var payload string
		if err := rows.Scan(&rec.MachineID, &rec.StartMS, &rec.EndMS, &rec.Expected, &payload); err != nil {
			rows.Close()
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to read power history"})
		}
		// A single corrupt payload is skipped rather than failing the fleet
		// view: one bad row must not black out every other machine's power.
		if err := json.Unmarshal([]byte(payload), &rec.Bucket); err != nil {
			continue
		}
		if oldestReadMS == 0 || rec.EndMS < oldestReadMS {
			oldestReadMS = rec.EndMS
		}
		records = append(records, rec)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to read power history"})
	}

	degraded, gaps, err := s.fleetPowerHealth()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to query power history health"})
	}
	apiPolled, err := s.fleetPowerAPIPolledCount()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to query machines"})
	}

	return c.JSON(http.StatusOK, aggregateFleetPower(records, fleetPowerInputs{
		Period:       period,
		Spec:         spec,
		StartMS:      startMS,
		BucketCount:  bucketCount,
		Now:          now.UnixMilli(),
		MachineIDs:   machineIDs,
		APIPolled:    apiPolled,
		Degraded:     degraded,
		GapsDeclared: gaps,
		Truncated:    len(records) == maxRecords,
		OldestReadMS: oldestReadMS,
	}))
}

func (s *Server) fleetPowerMachineIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM machines ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// fleetPowerAPIPolledCount counts machines backed by an API adapter rather
// than an agent. storeAPIPollResult derives their machine id as "api-"+<api
// machine id>, so the join is exact rather than a prefix guess.
func (s *Server) fleetPowerAPIPolledCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM machines m
		WHERE EXISTS (SELECT 1 FROM api_machines a WHERE m.id = 'api-' || a.id)`).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return n, err
}

// fleetPowerHealth returns how many machines reported degraded recording and
// how many gap ranges were declared, both within the retention window.
func (s *Server) fleetPowerHealth() (degraded, gaps int, err error) {
	if err = s.db.QueryRow(`SELECT COUNT(DISTINCT machine_id) FROM power_history_stream_state
		WHERE degraded = TRUE AND updated_at >= datetime('now', '-24 hours')`).Scan(&degraded); err != nil {
		return 0, 0, err
	}
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM power_history_gaps
		WHERE recorded_at >= datetime('now', '-24 hours')`).Scan(&gaps); err != nil {
		return 0, 0, err
	}
	return degraded, gaps, nil
}
