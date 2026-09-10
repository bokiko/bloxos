package powerhistory

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestRepresentativeStorageAndBatchSize(t *testing.T) {
	// Measure encoded data rather than assuming that a day of history is KBs.
	mean, peak := 237.1234567890123, 308.9876543210987
	stats := Stats{MeanWatts: &mean, PeakWatts: &peak, Samples: 30}
	for _, sensors := range []int{2, MaxSensors} {
		t.Run(fmt.Sprintf("%d_GPUs", sensors), func(t *testing.T) {
			// The fullest bucket the agent can produce: every scalar domain
			// measured and labelled, so the budget covers the worst case.
			bucket := Bucket{Seq: 2880, StartUnixMS: 1788739200000, EndUnixMS: 1788739230000,
				ExpectedSamples: 30, GPUTotal: &stats, CPU: &stats, System: &stats, DRAM: &stats,
				Sources: []DomainSource{
					{Domain: DomainSystem, Source: SourceHwmonPrefix + "power_meter"},
					{Domain: DomainCPU, Source: SourceRAPLPackage},
					{Domain: DomainDRAM, Source: SourceRAPLDRAM},
				}}
			for i := 0; i < sensors; i++ {
				bucket.GPUs = append(bucket.GPUs, Sensor{ID: fmt.Sprintf("GPU-%036d", i), Stats: stats})
			}
			record, err := json.Marshal(bucket)
			if err != nil {
				t.Fatal(err)
			}
			batch := Batch{Type: BatchType, StreamID: strings.Repeat("a", 32), RetainedFrom: 1}
			for i := 0; i < MaxBatchRecords; i++ {
				batch.Buckets = append(batch.Buckets, bucket)
			}
			encoded, err := json.Marshal(batch)
			if err != nil {
				t.Fatal(err)
			}
			if len(encoded) > MaxFrameBytes {
				t.Fatalf("representative full batch exceeds frame cap: %d", len(encoded))
			}
			t.Logf("JSON record+newline=%d B; 24h journal=%d B; 32-record batch=%d B (excludes transport/filesystem overhead)",
				len(record)+1, (len(record)+1)*(RetentionSeconds/WindowSeconds), len(encoded))
		})
	}
}

func TestUnavailableAndZeroRemainDistinct(t *testing.T) {
	zero := 0.0
	for _, tc := range []struct {
		stats Stats
		want  string
	}{
		{Stats{}, `"mean_watts":null`},
		{Stats{MeanWatts: &zero, PeakWatts: &zero, Samples: 30}, `"mean_watts":0`},
	} {
		b, err := json.Marshal(tc.stats)
		if err != nil || !strings.Contains(string(b), tc.want) {
			t.Fatalf("marshal=%s err=%v want %s", b, err, tc.want)
		}
	}
}

func TestBatchIdentityIsConnectionScoped(t *testing.T) {
	b, err := json.Marshal(Batch{Type: BatchType, StreamID: "stream", RetainedFrom: 1, Buckets: []Bucket{{Seq: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "machine_id") {
		t.Fatal("batch must not select authenticated machine identity")
	}
	var decoded Batch
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Buckets[0].Seq != 1 || decoded.StreamID != "stream" {
		t.Fatalf("bad roundtrip: %+v", decoded)
	}
}

func TestHistoryUsesSeparateSampledValues(t *testing.T) {
	mean, peak := 237.0, 308.0
	h := History{Points: []Point{{StreamID: "s", Bucket: Bucket{Seq: 1, GPUTotal: &Stats{MeanWatts: &mean, PeakWatts: &peak, Samples: 30}}}}, Gaps: []Gap{}}
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"power_watts"`) {
		t.Fatal("history must not reinterpret legacy instantaneous power")
	}
	var decoded History
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if *decoded.Points[0].GPUTotal.PeakWatts != peak {
		t.Fatal("lost sampled peak")
	}
}
