// Package powerhistory defines additive, replayable power telemetry. Values are
// component sensor readings, never whole-machine or wall power estimates.
package powerhistory

const (
	BatchType            = "power_history"
	AckType              = "power_history_ack"
	MaxBatchRecords      = 32
	MaxSensors           = 16
	MaxFrameBytes        = 256 * 1024
	RetentionSeconds     = 24 * 60 * 60
	WindowSeconds        = 30
	MaxFutureSkewSeconds = 15 * 60
	// MaxDomainSources bounds Bucket.Sources. Three scalar domains exist
	// today; the slack leaves room for an additive fourth without another
	// protocol change.
	MaxDomainSources = 8
	// MaxSourceLen bounds a single domain or backend identifier.
	MaxSourceLen = 64
	// All protocol counters are exactly representable by dashboard JavaScript.
	MaxSeq = uint64(1<<53 - 1)
)

// Measurement domains. They are DISJOINT SCOPES, never summands of one
// another: where the hardware can measure it, System already contains CPU,
// DRAM and the GPUs, so a consumer must never add domains together and must
// never derive one from another. Each is measured by its own backend on its
// own schedule, so even their sample counts need not agree.
const (
	// DomainSystem is whole-platform power — board input, not one component.
	// Present only where a genuine whole-system counter exists.
	DomainSystem = "system"
	// DomainCPU is CPU package power.
	DomainCPU = "cpu"
	// DomainDRAM is memory-controller power, reported separately from CPU
	// and never folded into it.
	DomainDRAM = "dram"
)

// Stable backend identifiers. Every reading names the backend that produced
// it, so a consumer can say WHAT was measured and HOW instead of presenting
// unlike measurements as one number.
const (
	SourceRAPLPsys    = "rapl-psys"    // powercap RAPL platform (psys) zone
	SourceRAPLPackage = "rapl-package" // sum of top-level RAPL package zones
	SourceRAPLDRAM    = "rapl-dram"    // sum of RAPL dram sub-zones
	SourceBattery     = "battery"      // power_supply class discharge power
	SourceIPMIDCMI    = "ipmi-dcmi"    // BMC DCMI instantaneous platform power
	// SourceHwmonPrefix prefixes a generic hwmon backend with its chip name,
	// e.g. "hwmon:power_meter".
	SourceHwmonPrefix = "hwmon:"
)

// DomainSource names the backend that produced one domain's statistics in
// this bucket. It is emitted only for a domain that actually carries samples
// here; a domain whose samples came from more than one backend inside the
// window is omitted entirely rather than averaged across methods.
type DomainSource struct {
	Domain string `json:"domain"`
	Source string `json:"source"`
}

// Stats describes observed samples in one window. Nil watts means unavailable;
// zero watts is a valid reading. Peak is a sampled maximum, not electrical peak.
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
	// GPUTotal is computed from complete simultaneous GPU samples, not from
	// summing independent per-device peaks. Nil means no complete observation.
	GPUTotal *Stats `json:"gpu_total,omitempty"`
	CPU      *Stats `json:"cpu,omitempty"`
	// System is whole-platform power for this window, measured by a
	// whole-system counter (RAPL psys, battery discharge, BMC DCMI, a
	// whole-board hwmon shunt). Nil means no such counter — never an
	// estimate, and never a sum of the component domains.
	System *Stats `json:"system,omitempty"`
	// DRAM is memory-controller power. Nil means unavailable. It is reported
	// beside CPU, never inside it.
	DRAM *Stats `json:"dram,omitempty"`
	// Sources names the backend behind each scalar domain present above.
	// Absent on buckets from agents predating source labelling: an unlabelled
	// CPU reading is a RAPL package sum, which is what those agents measured.
	Sources []DomainSource `json:"sources,omitempty"`
	// GapBefore marks local collection/journal loss before this window.
	GapBefore bool `json:"gap_before,omitempty"`
}

// SourceFor returns the backend identifier recorded for a domain, or "" when
// the bucket carries no label for it.
func (b *Bucket) SourceFor(domain string) string {
	for _, s := range b.Sources {
		if s.Domain == domain {
			return s.Source
		}
	}
	return ""
}

// RetainedFrom is the oldest sequence still available for unacknowledged replay.
// Acknowledged records may remain in the local journal without holding it back.
// Advancing it past unacknowledged data explicitly declares loss to the hub
// (retention, corrupt-tail recovery, or unused durable sequence reservations).
// Machine identity is taken from the authenticated connection, not this frame.
type Batch struct {
	Type         string   `json:"type"`
	StreamID     string   `json:"stream_id"`
	RetainedFrom uint64   `json:"retained_from"`
	Buckets      []Bucket `json:"buckets"`
	Degraded     bool     `json:"degraded,omitempty"`
}

// Through advances only after the hub transaction commits a contiguous prefix,
// including any explicitly declared retention gap. Lost ACKs are safe to replay.
type Ack struct {
	Type     string `json:"type"`
	StreamID string `json:"stream_id"`
	Through  uint64 `json:"through"`
}

// Point and History are the authenticated dashboard history response.
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
	Degraded bool    `json:"degraded"`
	// Cursor is the hub's durable ingestion high-water, not a sample timestamp.
	// GET ?after=cursor includes late backfill without replaying the entire day.
	Cursor uint64 `json:"cursor"`
	// Problem is a bounded diagnostic code from the currently connected agent.
	// It does not acknowledge or accept a rejected telemetry frame.
	Problem string `json:"problem,omitempty"`
}
