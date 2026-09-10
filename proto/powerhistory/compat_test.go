package powerhistory

import (
	"encoding/json"
	"strings"
	"testing"
)

// legacyBucket is the Bucket as it was before the scalar domains and their
// source labels existed. It stands in for a hub or agent build that has not
// been updated, which is not hypothetical: fleet agents whose signing key is
// gone keep sending exactly this shape indefinitely.
type legacyBucket struct {
	Seq             uint64   `json:"seq"`
	StartUnixMS     int64    `json:"start_unix_ms"`
	EndUnixMS       int64    `json:"end_unix_ms"`
	ExpectedSamples int      `json:"expected_samples"`
	GPUs            []Sensor `json:"gpus"`
	GPUTotal        *Stats   `json:"gpu_total,omitempty"`
	CPU             *Stats   `json:"cpu,omitempty"`
	GapBefore       bool     `json:"gap_before,omitempty"`
}

func sampleStats(mean, peak float64) *Stats {
	return &Stats{MeanWatts: &mean, PeakWatts: &peak, Samples: WindowSeconds}
}

// Old agent → new hub: every field an old bucket carries survives, and the
// domains it never knew about decode as unavailable rather than zero.
func TestOldBucketDecodesOnNewHub(t *testing.T) {
	old := legacyBucket{
		Seq: 7, StartUnixMS: 1788739200000, EndUnixMS: 1788739230000, ExpectedSamples: 30,
		GPUs:     []Sensor{{ID: "gpu-0", Stats: *sampleStats(100, 120)}},
		GPUTotal: sampleStats(100, 120),
		CPU:      sampleStats(65, 80),
	}
	raw, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	var got Bucket
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Seq != 7 || got.CPU == nil || *got.CPU.MeanWatts != 65 || len(got.GPUs) != 1 {
		t.Fatalf("old fields lost: %+v", got)
	}
	if got.System != nil || got.DRAM != nil || got.Sources != nil {
		t.Fatalf("absent domains must be unavailable, not zero: %+v", got)
	}
	if got.SourceFor(DomainCPU) != "" {
		t.Fatal("an unlabelled reading must not acquire an invented label")
	}
}

// New agent → old hub: the additive fields are ignored, and everything the
// old hub does understand is unchanged. Critically, an old hub normalizes a
// stored payload through ITS OWN schema on both sides of a replay
// comparison, so dropping the new fields is symmetric and a retransmission
// is still recognised as identical rather than as a data conflict.
func TestNewBucketDecodesOnOldHub(t *testing.T) {
	fresh := Bucket{
		Seq: 7, StartUnixMS: 1788739200000, EndUnixMS: 1788739230000, ExpectedSamples: 30,
		GPUs:     []Sensor{{ID: "gpu-0", Stats: *sampleStats(100, 120)}},
		GPUTotal: sampleStats(100, 120),
		CPU:      sampleStats(65, 80),
		System:   sampleStats(210, 240),
		DRAM:     sampleStats(7.5, 9),
		Sources: []DomainSource{
			{Domain: DomainSystem, Source: SourceRAPLPsys},
			{Domain: DomainCPU, Source: SourceRAPLPackage},
			{Domain: DomainDRAM, Source: SourceRAPLDRAM},
		},
	}
	raw, err := json.Marshal(fresh)
	if err != nil {
		t.Fatal(err)
	}
	var old legacyBucket
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatalf("an old hub must still decode a new bucket: %v", err)
	}
	if old.Seq != 7 || old.CPU == nil || *old.CPU.MeanWatts != 65 || old.GPUTotal == nil {
		t.Fatalf("old hub lost a field it understands: %+v", old)
	}

	first, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	var again legacyBucket
	if err := json.Unmarshal(raw, &again); err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(again)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("replay comparison is not stable on an old hub:\n%s\n%s", first, second)
	}
	if strings.Contains(string(first), "system") || strings.Contains(string(first), "sources") {
		t.Fatalf("old schema must simply not carry the new fields: %s", first)
	}
}

// The new fields are omitted entirely when unavailable, so a machine with no
// whole-system counter costs nothing on the wire and cannot be misread as
// having reported zero watts.
func TestUnavailableDomainsAreOmittedFromTheWire(t *testing.T) {
	raw, err := json.Marshal(Bucket{Seq: 1, CPU: sampleStats(65, 80)})
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{`"system"`, `"dram"`, `"sources"`} {
		if strings.Contains(string(raw), absent) {
			t.Fatalf("%s must be omitted when unavailable: %s", absent, raw)
		}
	}
	zero := 0.0
	raw, err = json.Marshal(Bucket{Seq: 1, System: &Stats{MeanWatts: &zero, PeakWatts: &zero, Samples: 30}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"system":{"mean_watts":0`) {
		t.Fatalf("a measured zero must stay distinguishable from unavailable: %s", raw)
	}
}
