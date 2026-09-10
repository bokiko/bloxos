package powerhistory

import (
	"encoding/json"
	"strings"
	"testing"
)

// A modelled value is distinguishable from a measured one by exactly one
// thing: its Sources label. That is the whole contract, so it is tested as
// such rather than through any convenience field.
func TestEstimateIsIdentifiableOnlyByItsSourceLabel(t *testing.T) {
	estimated := Bucket{
		Seq: 1, StartUnixMS: 1788739200000, EndUnixMS: 1788739230000, ExpectedSamples: 30,
		System:  sampleStats(7.5, 11.2),
		Sources: []DomainSource{{Domain: DomainSystem, Source: SourceEstimateUtil}},
	}
	measured := Bucket{
		Seq: 2, StartUnixMS: 1788739200000, EndUnixMS: 1788739230000, ExpectedSamples: 30,
		System:  sampleStats(7.5, 11.2),
		Sources: []DomainSource{{Domain: DomainSystem, Source: SourceRAPLPsys}},
	}
	if !estimated.Estimated(DomainSystem) {
		t.Fatal("a bucket labelled estimate-util must report its system domain as modelled")
	}
	if measured.Estimated(DomainSystem) {
		t.Fatal("a psys reading must never report as modelled")
	}
	// The two buckets differ ONLY in the label. Strip it and they are byte
	// identical — which is exactly why the label is mandatory rather than
	// advisory, and why this test would be vacuous without checking it.
	estimated.Sources, measured.Sources = nil, nil
	estimated.Seq = measured.Seq
	withoutLabel, err := json.Marshal(estimated)
	if err != nil {
		t.Fatal(err)
	}
	measuredRaw, err := json.Marshal(measured)
	if err != nil {
		t.Fatal(err)
	}
	if string(withoutLabel) != string(measuredRaw) {
		t.Fatalf("stripped of labels these must be identical, otherwise this test is not testing what it claims:\n%s\n%s",
			withoutLabel, measuredRaw)
	}
}

func TestEstimatedSourceRecognitionIsCentralised(t *testing.T) {
	if !IsEstimatedSource(SourceEstimateUtil) {
		t.Fatal("estimate-util must be recognised as a model")
	}
	// Every backend that reads a counter must be recognised as measured. A
	// new modelled backend added without teaching IsEstimatedSource about it
	// would be silently presented as a measurement.
	for _, measured := range []string{
		SourceRAPLPsys, SourceRAPLPackage, SourceRAPLDRAM,
		SourceBattery, SourceIPMIDCMI, SourceHwmonPrefix + "power_meter",
		"", "estimate", "estimate-util-x", "rapl-psys ",
	} {
		if IsEstimatedSource(measured) {
			t.Fatalf("%q must not be treated as a model", measured)
		}
	}
	// An unlabelled domain predates labelling, and those agents only ever
	// measured.
	if (&Bucket{CPU: sampleStats(65, 80)}).Estimated(DomainCPU) {
		t.Fatal("an unlabelled domain must not be reported as modelled")
	}
}

// The estimate label rides the existing Sources array, so it needs no new
// field and reaches an unmodified 3a hub intact. This matters more than it
// looks: a hub stores a bucket by decoding and re-encoding it under its own
// schema, so a boolean flag a hub did not know about would be dropped — and
// a dropped "estimated" reads as "measured".
func TestEstimateLabelSurvivesAHubThatPredatesIt(t *testing.T) {
	// A 3a-era hub: it knows Sources, but nothing about estimation.
	type sourceAwareBucket struct {
		Seq             uint64         `json:"seq"`
		StartUnixMS     int64          `json:"start_unix_ms"`
		EndUnixMS       int64          `json:"end_unix_ms"`
		ExpectedSamples int            `json:"expected_samples"`
		GPUs            []Sensor       `json:"gpus"`
		System          *Stats         `json:"system,omitempty"`
		CPU             *Stats         `json:"cpu,omitempty"`
		Sources         []DomainSource `json:"sources,omitempty"`
	}
	raw, err := json.Marshal(Bucket{
		Seq: 9, StartUnixMS: 1788739200000, EndUnixMS: 1788739230000, ExpectedSamples: 30,
		System:  sampleStats(7.5, 11.2),
		Sources: []DomainSource{{Domain: DomainSystem, Source: SourceEstimateUtil}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var old sourceAwareBucket
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatal(err)
	}
	stored, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored), SourceEstimateUtil) {
		t.Fatalf("a hub that predates estimation must still store the label that says this was modelled: %s", stored)
	}
	// And it round-trips back to a consumer that does understand it.
	var back Bucket
	if err := json.Unmarshal(stored, &back); err != nil {
		t.Fatal(err)
	}
	if !back.Estimated(DomainSystem) {
		t.Fatal("the label must survive a round trip through an older hub")
	}
}

// A modelled backend is bounded by the same label rules as any other, so it
// costs nothing new on the wire and fits the existing limits.
func TestEstimateSourceFitsTheLabelBudget(t *testing.T) {
	if len(SourceEstimateUtil) > MaxSourceLen {
		t.Fatalf("source id %q exceeds MaxSourceLen %d", SourceEstimateUtil, MaxSourceLen)
	}
	raw, err := json.Marshal(Bucket{Seq: 1, CPU: sampleStats(65, 80)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "estimated") {
		t.Fatalf("no estimation field may appear on a measured bucket: %s", raw)
	}
}
