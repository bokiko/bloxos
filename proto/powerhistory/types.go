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
	// All protocol counters are exactly representable by dashboard JavaScript.
	MaxSeq = uint64(1<<53 - 1)
)

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
	// GapBefore marks local collection/journal loss before this window.
	GapBefore bool `json:"gap_before,omitempty"`
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
