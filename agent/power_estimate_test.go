package main

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
)

// fakeUtil replays a scripted sequence of cumulative CPU-time readings, so a
// utilisation curve can be exercised without a real /proc/stat.
type fakeUtil struct {
	steps []fakeUtilStep
	i     int
}

type fakeUtilStep struct {
	busy, total float64
	ok          bool
}

func (f *fakeUtil) read() (float64, float64, bool) {
	if f.i >= len(f.steps) {
		return 0, 0, false
	}
	s := f.steps[f.i]
	f.i++
	return s.busy, s.total, s.ok
}

// utilRamp builds cumulative counters that advance by `ticks` of CPU time per
// read with the given busy fraction, which is how /proc/stat actually moves.
func utilRamp(fractions ...float64) *fakeUtil {
	f := &fakeUtil{}
	busy, total := 0.0, 0.0
	f.steps = append(f.steps, fakeUtilStep{busy, total, true}) // priming read
	for _, frac := range fractions {
		total += 100
		busy += 100 * frac
		f.steps = append(f.steps, fakeUtilStep{busy, total, true})
	}
	return f
}

func sampleAt(t *testing.T, s *estimateSampler, n int) []float64 {
	t.Helper()
	base := time.Now()
	out := make([]float64, 0, n)
	for i := 1; i <= n; i++ {
		w, ok := s.sample(base.Add(time.Duration(i) * powerSampleInterval))
		if !ok {
			out = append(out, math.NaN())
			continue
		}
		out = append(out, w)
	}
	return out
}

// --- the formula ---

func TestEstimateIsMonotonicAndAnchoredAtBothEnds(t *testing.T) {
	p := powerProfile{idleWatts: 3.5, maxWatts: 12.0}
	// 0%, 50% and 100% utilisation, in that order, on one sampler.
	s := newEstimateSampler(p, utilRamp(0, 0.5, 1.0), powerSampleInterval)
	got := sampleAt(t, s, 4)
	if !math.IsNaN(got[0]) {
		t.Fatalf("the first sample primes the counters and must report nothing, got %v W", got[0])
	}
	want := []float64{3.5, 7.75, 12.0}
	for i, w := range want {
		if math.Abs(got[i+1]-w) > 1e-9 {
			t.Fatalf("utilisation step %d: got %v W, want %v W", i, got[i+1], w)
		}
	}
	// Monotonic across the whole range, not just at the anchors.
	prev := math.Inf(-1)
	for u := 0.0; u <= 1.0001; u += 0.01 {
		w := p.watts(u)
		if w < prev {
			t.Fatalf("watts(%v)=%v is below watts of the previous step (%v): f must be monotonic", u, w, prev)
		}
		prev = w
	}
}

func TestEstimateClampsUtilisationRatherThanExtrapolating(t *testing.T) {
	p := powerProfile{idleWatts: 10, maxWatts: 50}
	if w := p.watts(1.4); w != 50 {
		t.Fatalf("a busy fraction above 1 is a clock artefact, not 140%% load: got %v W", w)
	}
	if w := p.watts(-0.2); w != 10 {
		t.Fatalf("negative utilisation must clamp to idle, got %v W", w)
	}
	if w := p.watts(math.NaN()); w != 10 {
		t.Fatalf("NaN utilisation must not produce NaN watts, got %v W", w)
	}
}

func TestEstimateNeverReportsStaleOrInventedSamples(t *testing.T) {
	p := powerProfile{idleWatts: 3, maxWatts: 12}
	base := time.Now()

	// A read failure re-primes: the next tick must also report nothing
	// rather than reusing the previous utilisation.
	s := newEstimateSampler(p, &fakeUtil{steps: []fakeUtilStep{
		{100, 100, true},
		{0, 0, false},
		{200, 200, true},
	}}, powerSampleInterval)
	for i, want := range []bool{false, false, false} {
		if _, ok := s.sample(base.Add(time.Duration(i+1) * powerSampleInterval)); ok != want {
			t.Fatalf("sample %d: ok=%v, want %v", i, ok, want)
		}
	}

	// A gap far longer than the sampling interval (suspend, starved
	// goroutine) is not this instant's utilisation.
	s = newEstimateSampler(p, utilRamp(1.0), powerSampleInterval)
	s.sample(base)
	if _, ok := s.sample(base.Add(powerEstimateStaleFactor*powerSampleInterval + time.Second)); ok {
		t.Fatal("a stale interval must report unavailable, not a value")
	}

	// Counters that go backwards (CPU hotplug, container cgroup swap) or
	// report more busy time than total time are nonsense, not a reading.
	for _, bad := range [][2]float64{{50, 50}, {250, 150}} {
		s = newEstimateSampler(p, &fakeUtil{steps: []fakeUtilStep{
			{100, 100, true}, {bad[0], bad[1], true},
		}}, powerSampleInterval)
		s.sample(base)
		if _, ok := s.sample(base.Add(powerSampleInterval)); ok {
			t.Fatalf("busy=%v total=%v after busy=100 total=100 must be unavailable", bad[0], bad[1])
		}
	}
}

func TestEstimateCarriesTheEstimateSourceLabel(t *testing.T) {
	s := newEstimateSampler(powerProfile{idleWatts: 3, maxWatts: 12}, utilRamp(0.5), powerSampleInterval)
	if s.source() != powerhistory.SourceEstimateUtil {
		t.Fatalf("source %q, want %q", s.source(), powerhistory.SourceEstimateUtil)
	}
	if !powerhistory.IsEstimatedSource(s.source()) {
		t.Fatal("the estimator's source id must be recognised as a model by the shared helper")
	}
}

// --- table ---

func TestTDPTableHitAndMiss(t *testing.T) {
	// Hit: the RK3588 board is identified by its device tree, not by
	// /proc/cpuinfo, which on that SoC names a Cortex-A55 core.
	p, ok := lookupPlatformProfile(powerIdentity{
		dtCompatible: []string{"xunlong,orangepi-5", "rockchip,rk3588s"},
		cpuModel:     "cortex-a55",
		cores:        8,
		arch:         "arm64",
	})
	if !ok {
		t.Fatal("an RK3588S board must match the table")
	}
	if p.idleWatts != 3.5 || p.maxWatts != 12.0 {
		t.Fatalf("RK3588 envelope %v..%v W", p.idleWatts, p.maxWatts)
	}
	if !strings.Contains(p.basis, "rockchip,rk3588s") {
		t.Fatalf("basis %q must name the key that matched", p.basis)
	}

	// Hit on an x86 CPU key.
	if p, ok := lookupPlatformProfile(powerIdentity{cpuModel: "intel n100", cores: 4, arch: "amd64"}); !ok ||
		p.idleWatts != 6.0 || p.maxWatts != 22.0 {
		t.Fatalf("N100 lookup: ok=%v profile=%+v", ok, p)
	}

	// Miss: no board, no known CPU.
	if _, ok := lookupPlatformProfile(powerIdentity{cpuModel: "some unknown cpu", cores: 16, arch: "amd64"}); ok {
		t.Fatal("an unknown CPU must not match the table")
	}
}

func TestTableRowRefusedWhenCoreCountDisagrees(t *testing.T) {
	// Four vCPUs carved out of an eight-core RK3588, or a cut-down SKU:
	// right silicon, different machine, so the row's envelope is not this
	// machine's envelope.
	if _, ok := lookupPlatformProfile(powerIdentity{
		dtCompatible: []string{"rockchip,rk3588"}, cores: 4, arch: "arm64",
	}); ok {
		t.Fatal("a 4-core machine must not inherit the 8-core RK3588 envelope")
	}
	if _, ok := lookupPlatformProfile(powerIdentity{cpuModel: "intel n100", cores: 2, arch: "amd64"}); ok {
		t.Fatal("a 2-vCPU guest must not inherit the 4-core N100 box envelope")
	}
}

// --- fallback and refusal ---

func TestUnknownMachineReportsUnavailableRatherThanGuessing(t *testing.T) {
	none := func(string) string { return "" }
	for _, tc := range []struct {
		name string
		id   powerIdentity
	}{
		{"unknown x86 desktop", powerIdentity{cpuModel: "unknown cpu", cores: 8, arch: "amd64"}},
		{"unknown x86 with no cpu string", powerIdentity{cores: 4, arch: "amd64"}},
		{"arm64 guest", powerIdentity{dtCompatible: []string{"linux,dummy-virt"}, cores: 8, arch: "arm64", virtualized: true}},
		{"arm64 with no device tree", powerIdentity{cores: 8, arch: "arm64"}},
		{"arm64 server far outside the SBC class", powerIdentity{dtCompatible: []string{"cavium,thunder"}, cores: 96, arch: "arm64"}},
		{"single core", powerIdentity{dtCompatible: []string{"acme,tiny"}, cores: 1, arch: "arm64"}},
	} {
		if p, ok := resolvePowerProfile(tc.id, none); ok {
			t.Fatalf("%s: produced envelope %+v; an unjustifiable estimate is worse than nothing", tc.name, p)
		}
	}
}

func TestPerCoreFallbackCoversUnknownARMBoards(t *testing.T) {
	p, ok := resolvePowerProfile(powerIdentity{
		dtCompatible: []string{"acme,unknown-sbc", "acme,soc9000"},
		cores:        4,
		arch:         "arm64",
	}, func(string) string { return "" })
	if !ok {
		t.Fatal("an arm64 board with a device tree must fall back to the per-core heuristic")
	}
	// 1.6 + 0.15*4 = 2.2 idle; + 1.1*4 = 6.6 at full load.
	if math.Abs(p.idleWatts-2.2) > 1e-9 || math.Abs(p.maxWatts-6.6) > 1e-9 {
		t.Fatalf("per-core envelope %v..%v W", p.idleWatts, p.maxWatts)
	}
	if !strings.Contains(p.basis, "heuristic") {
		t.Fatalf("basis %q must say it is a heuristic, not a measurement", p.basis)
	}
	// It must stay inside the envelope the class is known to occupy.
	if p.maxWatts > 20 || p.idleWatts < 1 {
		t.Fatalf("heuristic left the single-board class it claims: %+v", p)
	}
}

// --- operator override ---

func TestOperatorOverrideBeatsTableAndHeuristic(t *testing.T) {
	env := func(k string) string {
		if k == powerEstimateEnvWatts {
			return "4.25:31.5"
		}
		return ""
	}
	// An RK3588 that WOULD match the table.
	id := powerIdentity{dtCompatible: []string{"rockchip,rk3588"}, cores: 8, arch: "arm64"}
	p, ok := resolvePowerProfile(id, env)
	if !ok {
		t.Fatal("the override must produce a profile")
	}
	if p.idleWatts != 4.25 || p.maxWatts != 31.5 {
		t.Fatalf("override envelope %v..%v W, want 4.25..31.5", p.idleWatts, p.maxWatts)
	}
	if !strings.Contains(p.basis, powerEstimateEnvWatts) {
		t.Fatalf("basis %q must name the override", p.basis)
	}

	// It also enables estimation on a machine no table row and no heuristic
	// covers — which is the whole point of having it.
	unknown := powerIdentity{cpuModel: "some unlisted soc", cores: 6, arch: "amd64"}
	if p, ok := resolvePowerProfile(unknown, env); !ok || p.maxWatts != 31.5 {
		t.Fatalf("override on an unknown machine: ok=%v profile=%+v", ok, p)
	}
}

func TestMalformedOverrideRefusesToEstimateAtAll(t *testing.T) {
	for _, raw := range []string{
		"12",           // no separator
		"a:b",          // not numbers
		"12:",          // missing max
		"-5:20",        // negative idle
		"20:12",        // inverted
		"10:10.5",      // envelope too narrow to carry any signal
		"5:100000",     // max out of range
		"NaN:20",       // not finite
		"1:Inf",        // not finite
		"3.5:12:extra", // trailing junk parses as a bad max
		"  :  ",        // blank
	} {
		if p, err := parsePowerEnvelopeOverride(raw); err == nil {
			t.Fatalf("%q was accepted as envelope %+v", raw, p)
		}
	}
	// A rejected override does NOT fall back to the table: an operator who
	// typed an envelope wants that envelope, and quietly substituting a
	// different one is the worst of both.
	env := func(k string) string {
		if k == powerEstimateEnvWatts {
			return "not-an-envelope"
		}
		return ""
	}
	if _, ok := resolvePowerProfile(powerIdentity{dtCompatible: []string{"rockchip,rk3588"}, cores: 8, arch: "arm64"}, env); ok {
		t.Fatal("a malformed override must disable estimation, not silently fall back to the table")
	}
}

func TestOperatorCanRefuseEstimationEntirely(t *testing.T) {
	for _, v := range []string{"0", "off", "OFF", "false", "no", " off "} {
		if !powerEstimateDisabled(func(string) string { return v }) {
			t.Fatalf("%s=%q must disable estimation", powerEstimateEnvDisable, v)
		}
	}
	for _, v := range []string{"", "1", "on", "yes", "please"} {
		if powerEstimateDisabled(func(string) string { return v }) {
			t.Fatalf("%s=%q must not disable estimation", powerEstimateEnvDisable, v)
		}
	}
}

// --- the accumulator keeps the label attached ---

func TestEstimatedBucketIsLabelledAndDistinguishableFromMeasurement(t *testing.T) {
	acc := newPowerAccumulator(powerWindow, powerSampleInterval)
	base := time.Now()
	for i := 0; i < powerhistory.WindowSeconds; i++ {
		at := base.Add(time.Duration(i) * time.Second)
		acc.addDomainAt(powerhistory.DomainSystem, at, at.UnixMilli(), 7.5, powerhistory.SourceEstimateUtil)
	}
	end := base.Add(powerWindow)
	buckets := acc.tickAt(end, end.UnixMilli())
	if len(buckets) != 1 {
		t.Fatalf("want one bucket, got %d", len(buckets))
	}
	b := buckets[0]
	if b.System == nil {
		t.Fatal("the modelled system domain must be emitted")
	}
	if got := b.SourceFor(powerhistory.DomainSystem); got != powerhistory.SourceEstimateUtil {
		t.Fatalf("system source %q, want %q — an unlabelled estimate is indistinguishable from a measurement",
			got, powerhistory.SourceEstimateUtil)
	}
	if !b.Estimated(powerhistory.DomainSystem) {
		t.Fatal("Bucket.Estimated must report the modelled system domain")
	}
	if b.Estimated(powerhistory.DomainCPU) || b.Estimated(powerhistory.DomainDRAM) {
		t.Fatal("absent domains must not report as estimated")
	}
}

func TestModelledAndMeasuredSamplesNeverAverageTogether(t *testing.T) {
	acc := newPowerAccumulator(powerWindow, powerSampleInterval)
	base := time.Now()
	// A window that somehow saw both must emit NOTHING for that domain
	// rather than a mean spanning a counter and a model.
	for i := 0; i < powerhistory.WindowSeconds; i++ {
		at := base.Add(time.Duration(i) * time.Second)
		src := powerhistory.SourceEstimateUtil
		if i%2 == 0 {
			src = powerhistory.SourceRAPLPsys
		}
		acc.addDomainAt(powerhistory.DomainSystem, at, at.UnixMilli(), 7.5, src)
	}
	end := base.Add(powerWindow)
	for _, b := range acc.tickAt(end, end.UnixMilli()) {
		if b.System != nil {
			t.Fatalf("a window mixing a counter and a model must drop the domain, got %+v", *b.System)
		}
	}
}
