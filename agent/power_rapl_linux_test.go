//go:build linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
)

// fakeRAPL lays out a powercap tree on disk (for discovery, which uses
// os.ReadDir) and answers reads from an in-memory map so counters can be
// advanced or made unreadable per test.
type fakeRAPL struct {
	root  string
	files map[string]string
	fail  map[string]bool
}

func newFakeRAPL(t *testing.T) *fakeRAPL {
	t.Helper()
	return &fakeRAPL{root: t.TempDir(), files: map[string]string{}, fail: map[string]bool{}}
}

func (r *fakeRAPL) zone(name, zoneName string, energy, maxRange uint64) {
	dir := filepath.Join(r.root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	r.files[filepath.Join(dir, "name")] = zoneName + "\n"
	r.files[filepath.Join(dir, "energy_uj")] = strconv.FormatUint(energy, 10) + "\n"
	if maxRange > 0 {
		r.files[filepath.Join(dir, "max_energy_range_uj")] = strconv.FormatUint(maxRange, 10) + "\n"
	}
}

func (r *fakeRAPL) set(name string, energy uint64) {
	r.files[filepath.Join(r.root, name, "energy_uj")] = strconv.FormatUint(energy, 10) + "\n"
}

func (r *fakeRAPL) read(p string) ([]byte, error) {
	if r.fail[p] {
		return nil, errors.New("permission denied")
	}
	v, ok := r.files[p]
	if !ok {
		return nil, os.ErrNotExist
	}
	return []byte(v), nil
}

func TestRAPLDiscoverySplitsDomainsWithoutOverlap(t *testing.T) {
	r := newFakeRAPL(t)
	r.zone("intel-rapl:0", "package-0", 1000, 1<<40)
	r.zone("intel-rapl:1", "package-1", 2000, 1<<40)
	r.zone("intel-rapl:0:0", "core", 500, 1<<40) // inside package-0
	r.zone("intel-rapl:0:1", "dram", 500, 1<<40)
	r.zone("intel-rapl:1:1", "dram", 700, 1<<40)
	r.zone("intel-rapl-mmio:0", "package-0", 1000, 0) // MMIO mirror of package-0
	r.zone("intel-rapl:2", "psys", 9000, 1<<40)
	psys, pkg, dram := discoverRAPL(r.root, r.read)
	if psys == nil || len(psys.zones) != 1 || psys.source() != powerhistory.SourceRAPLPsys {
		t.Fatalf("psys: %+v", psys)
	}
	if pkg == nil || len(pkg.zones) != 2 || pkg.source() != powerhistory.SourceRAPLPackage {
		t.Fatalf("packages: %+v", pkg)
	}
	if dram == nil || len(dram.zones) != 2 || dram.source() != powerhistory.SourceRAPLDRAM {
		t.Fatalf("dram: %+v", dram)
	}
	// The cpu group must hold neither psys, nor the core sub-zone, nor the
	// MMIO mirror of a package it already counts.
	for _, z := range pkg.zones {
		if base := filepath.Base(filepath.Dir(z.energyPath)); base != "intel-rapl:0" && base != "intel-rapl:1" {
			t.Fatalf("package group contains %s", base)
		}
	}
}

func TestRAPLUnreadableZoneDisablesOnlyItsDomain(t *testing.T) {
	r := newFakeRAPL(t)
	r.zone("intel-rapl:0", "package-0", 1000, 1<<40)
	r.zone("intel-rapl:1", "package-1", 2000, 1<<40)
	r.zone("intel-rapl:2", "psys", 9000, 1<<40)
	r.fail[filepath.Join(r.root, "intel-rapl:1", "energy_uj")] = true
	psys, pkg, _ := discoverRAPL(r.root, r.read)
	if pkg != nil {
		t.Fatalf("a partially readable package set must not become a cpu total: %+v", pkg)
	}
	if psys == nil {
		t.Fatal("an unreadable package must not take psys down with it")
	}

	if p, k, d := discoverRAPL(filepath.Join(r.root, "missing"), r.read); p != nil || k != nil || d != nil {
		t.Fatal("no powercap tree must mean unavailable")
	}
	// The interface value must be a true nil when unavailable.
	var iface powerSource
	if _, k, _ := discoverRAPL(filepath.Join(r.root, "missing"), r.read); k != nil {
		iface = k
	}
	if iface != nil {
		t.Fatal("typed nil leaked into the interface")
	}
}

func TestRAPLSampleRatesAndWraps(t *testing.T) {
	r := newFakeRAPL(t)
	const maxRange = 1_000_000 // 1 J range to make wrap easy
	r.zone("intel-rapl:0", "package-0", 900_000, maxRange)
	r.zone("intel-rapl:1", "package-1", 100_000, maxRange)
	_, s, _ := discoverRAPL(r.root, r.read)
	if s == nil {
		t.Fatal("discovery failed")
	}
	base := time.Now()
	if _, ok := s.sample(base); ok {
		t.Fatal("first sample has no rate")
	}
	// +50 mJ on each package over 1 s → 0.1 W total; package-0 wraps.
	r.set("intel-rapl:0", 950_000)
	r.set("intel-rapl:1", 150_000)
	w, ok := s.sample(base.Add(time.Second))
	if !ok || w < 0.0999 || w > 0.1001 {
		t.Fatalf("rate: ok=%v w=%v", ok, w)
	}
	// A BACKWARD STEP IS UNAVAILABLE, even though this one looks exactly like
	// a wrap. A rollover and a counter reset (suspend/resume, a driver reload)
	// are indistinguishable from two reads, and assuming wrap invents up to a
	// whole max_energy_range_uj of energy that may never have been consumed —
	// at a rate the aggregate ceiling would happily accept. One missed sample
	// at rollover is the cheaper error.
	r.set("intel-rapl:0", 25_000) // 950000 → wrapped, or reset; nothing can tell
	r.set("intel-rapl:1", 175_000)
	if w, ok := s.sample(base.Add(2 * time.Second)); ok {
		t.Fatalf("a backward counter step must not be guessed as a wrap: %v W", w)
	}
	// And it recovers on its own: the next interval measures from the new
	// baseline with no intervention.
	r.set("intel-rapl:0", 75_000)
	r.set("intel-rapl:1", 225_000)
	w, ok = s.sample(base.Add(3 * time.Second))
	if !ok || w < 0.0999 || w > 0.1001 {
		t.Fatalf("recovery after a backward step: ok=%v w=%v", ok, w)
	}
	// Implausible interval (long stall) re-primes instead of reporting.
	r.set("intel-rapl:0", 30_000)
	if _, ok := s.sample(base.Add(60 * time.Second)); ok {
		t.Fatal("stale interval must not produce a value")
	}
	// A read error re-primes; the following sample is again rate-less.
	r.fail[filepath.Join(r.root, "intel-rapl:1", "energy_uj")] = true
	if _, ok := s.sample(base.Add(61 * time.Second)); ok {
		t.Fatal("read error must not produce a value")
	}
	delete(r.fail, filepath.Join(r.root, "intel-rapl:1", "energy_uj"))
	if _, ok := s.sample(base.Add(62 * time.Second)); ok {
		t.Fatal("sample right after a read error must only prime")
	}
	// Both counters advance: EVERY participating zone has to move, so a
	// resuming sample cannot be produced by one busy zone alone.
	r.set("intel-rapl:0", 80_000)
	r.set("intel-rapl:1", 275_000)
	if _, ok := s.sample(base.Add(63 * time.Second)); !ok {
		t.Fatal("rate must resume after re-priming")
	}
}

// A zone that vanishes mid-run (module unloaded, counter removed) degrades to
// unavailable and recovers if it comes back; it never emits a partial sum.
func TestRAPLZoneVanishingMidRunDegradesToUnavailable(t *testing.T) {
	r := newFakeRAPL(t)
	r.zone("intel-rapl:0", "package-0", 0, 1<<40)
	r.zone("intel-rapl:1", "package-1", 0, 1<<40)
	_, s, _ := discoverRAPL(r.root, r.read)
	base := time.Now()
	s.sample(base)
	r.set("intel-rapl:0", 1_000_000)
	r.set("intel-rapl:1", 1_000_000)
	if _, ok := s.sample(base.Add(time.Second)); !ok {
		t.Fatal("baseline rate expected")
	}
	gone := filepath.Join(r.root, "intel-rapl:1", "energy_uj")
	delete(r.files, gone)
	for i := 2; i < 5; i++ {
		if w, ok := s.sample(base.Add(time.Duration(i) * time.Second)); ok {
			t.Fatalf("vanished zone must not yield %v W", w)
		}
	}
	r.files[gone] = "5000000\n"
	s.sample(base.Add(5 * time.Second)) // re-prime
	r.set("intel-rapl:0", 3_000_000)
	r.set("intel-rapl:1", 6_000_000)
	if _, ok := s.sample(base.Add(6 * time.Second)); !ok {
		t.Fatal("backend must recover once the zone returns")
	}
}

// A malformed counter value is unreadable data, not a zero-watt reading.
func TestRAPLMalformedValueIsUnavailable(t *testing.T) {
	r := newFakeRAPL(t)
	r.zone("intel-rapl:0", "package-0", 0, 1<<40)
	r.files[filepath.Join(r.root, "intel-rapl:0", "energy_uj")] = "not-a-number\n"
	if _, pkg, _ := discoverRAPL(r.root, r.read); pkg != nil {
		t.Fatal("a malformed counter must not become a backend")
	}
}

// psys is preferred for `system` while the packages go on answering for
// `cpu`. The two are reported side by side and never added.
func TestPsysPreferredForSystemAndPackagesStillReportCPU(t *testing.T) {
	r := newFakeRAPL(t)
	r.zone("intel-rapl:0", "package-0", 0, 1<<40)
	r.zone("intel-rapl:1", "psys", 0, 1<<40)
	env := newFakePowerEnv(t)
	env.raplRoot = r.root
	env.read = r.read
	// The platform zone advances across the detection probe, as a wired-up
	// psys does; a zone that did not would be rejected (see the stuck-psys
	// case in power_sources_linux_test.go).
	tick := env.sleep
	env.sleep = func(d time.Duration) {
		r.set("intel-rapl:1", 1_000_000)
		tick(d)
	}
	set := detectPowerSources(env)
	if set.system == nil || set.system.source() != powerhistory.SourceRAPLPsys {
		t.Fatalf("system backend: %+v", set.system)
	}
	if set.cpu == nil || set.cpu.source() != powerhistory.SourceRAPLPackage {
		t.Fatalf("cpu backend: %+v", set.cpu)
	}

	// 10 W platform, 4 W package over one second.
	base := time.Now()
	set.system.sample(base) // re-anchor both counters on the test clock
	set.cpu.sample(base)
	r.set("intel-rapl:1", 11_000_000)
	r.set("intel-rapl:0", 4_000_000)
	sysW, sysOK := set.system.sample(base.Add(time.Second))
	cpuW, cpuOK := set.cpu.sample(base.Add(time.Second))
	if !sysOK || !cpuOK {
		t.Fatalf("both domains must sample: system=%v cpu=%v", sysOK, cpuOK)
	}
	if sysW < 9.99 || sysW > 10.01 {
		t.Fatalf("system watts %v: psys must be reported as measured, never summed with the package", sysW)
	}
	if cpuW < 3.99 || cpuW > 4.01 {
		t.Fatalf("cpu watts %v", cpuW)
	}
}

// EVERY participating counter has to advance, on its own account.
//
// Summing first and judging the total let one busy socket certify a frozen
// sibling: the sum stayed plausible and shipped as the whole domain, which is
// an undercount wearing a measurement's label.
func TestAFrozenZoneIsNotCertifiedByABusySibling(t *testing.T) {
	r := newFakeRAPL(t)
	r.zone("intel-rapl:0", "package-0", 1_000_000, 1<<40)
	r.zone("intel-rapl:1", "package-1", 2_000_000, 1<<40)
	_, pkg, _ := discoverRAPL(r.root, r.read)
	if pkg == nil {
		t.Fatal("discovery failed")
	}
	base := time.Now()
	if _, ok := pkg.sample(base); ok {
		t.Fatal("first sample primes")
	}

	// CONTROL: with BOTH advancing, this fixture does produce a rate — so the
	// assertion below is about the frozen zone and not about a sampler that
	// never reports anything.
	r.set("intel-rapl:0", 11_000_000)
	r.set("intel-rapl:1", 12_000_000)
	if w, ok := pkg.sample(base.Add(time.Second)); !ok || w < 19.99 || w > 20.01 {
		t.Fatalf("control: two advancing zones must yield 20 W, got ok=%v w=%v", ok, w)
	}

	// Package 1 stops. The sum is still large and still plausible.
	r.set("intel-rapl:0", 21_000_000)
	if w, ok := pkg.sample(base.Add(2 * time.Second)); ok {
		t.Fatalf("a frozen zone was certified by its busy sibling: %v W", w)
	}
}

// With minWatts 0, an entirely frozen group produced 0.0 W and PASSED — a
// running CPU reported as drawing nothing, which is worse than silence
// because it is indistinguishable from a real idle measurement.
func TestAnEntirelyFrozenGroupIsUnavailableNotZeroWatts(t *testing.T) {
	for _, domain := range []struct {
		name  string
		build func(r *fakeRAPL) *raplSampler
	}{
		{"package", func(r *fakeRAPL) *raplSampler { _, p, _ := discoverRAPL(r.root, r.read); return p }},
		{"dram", func(r *fakeRAPL) *raplSampler { _, _, d := discoverRAPL(r.root, r.read); return d }},
	} {
		t.Run(domain.name, func(t *testing.T) {
			r := newFakeRAPL(t)
			r.zone("intel-rapl:0", "package-0", 5_000_000, 1<<40)
			r.zone("intel-rapl:0:0", "dram", 3_000_000, 1<<40)
			s := domain.build(r)
			if s == nil {
				t.Fatal("discovery failed")
			}
			if s.minWatts != 0 {
				t.Fatalf("this test is about the minWatts==0 domains; %s has %v", domain.name, s.minWatts)
			}
			base := time.Now()
			s.sample(base)
			if w, ok := s.sample(base.Add(time.Second)); ok {
				t.Fatalf("a frozen %s group reported %v W as a measurement", domain.name, w)
			}
		})
	}
}

// A counter outside its own declared range is not arithmetic to perform.
func TestARangeViolationIsRefusedBeforeSubtraction(t *testing.T) {
	r := newFakeRAPL(t)
	const maxRange = 1_000_000
	r.zone("intel-rapl:0", "package-0", 500_000, maxRange)
	_, pkg, _ := discoverRAPL(r.root, r.read)
	base := time.Now()
	pkg.sample(base)
	r.set("intel-rapl:0", maxRange+1)
	if w, ok := pkg.sample(base.Add(time.Second)); ok {
		t.Fatalf("a value above max_energy_range_uj was used: %v W", w)
	}
	// Recovers from a sane pair afterwards.
	r.set("intel-rapl:0", 600_000)
	pkg.sample(base.Add(2 * time.Second))
	r.set("intel-rapl:0", 700_000)
	if _, ok := pkg.sample(base.Add(3 * time.Second)); !ok {
		t.Fatal("the sampler must recover once the counter is sane again")
	}
}

// A zone whose NAME cannot be read is not a zone that is absent: it matched
// the powercap naming, so it exists, and only its domain is unknown.
//
// Skipping it was how a partial sum became a domain total — on a two-socket
// board where package-1's name is unreadable, the CPU domain reported one
// socket as if it were the machine.
func TestAnUnreadableZoneNameWithholdsTheDomainsItCouldBelongTo(t *testing.T) {
	build := func(t *testing.T, hide string) (psys, pkg, dram *raplSampler) {
		r := newFakeRAPL(t)
		r.zone("intel-rapl:0", "package-0", 1_000_000, 1<<40)
		r.zone("intel-rapl:1", "package-1", 2_000_000, 1<<40)
		r.zone("intel-rapl:2", "psys", 3_000_000, 1<<40)
		r.zone("intel-rapl:0:0", "dram", 4_000_000, 1<<40)
		// Named sub-zones that are legitimately not dram. These must NOT be
		// mistaken for missing dram contributors.
		r.zone("intel-rapl:0:1", "core", 5_000_000, 1<<40)
		r.zone("intel-rapl:1:0", "uncore", 6_000_000, 1<<40)
		if hide != "" {
			r.fail[filepath.Join(r.root, hide, "name")] = true
		}
		return discoverRAPL(r.root, r.read)
	}

	// CONTROL: with every name readable, all three domains exist — so the
	// withholding below is caused by the unreadable name and nothing else.
	psys, pkg, dram := build(t, "")
	if psys == nil || pkg == nil || dram == nil {
		t.Fatalf("control: all three domains must exist: psys=%v pkg=%v dram=%v", psys, pkg, dram)
	}

	// A TOP-LEVEL zone could be a package or psys, so it compromises both.
	psys, pkg, dram = build(t, "intel-rapl:1")
	if pkg != nil {
		t.Fatal("the cpu domain reported a partial package sum")
	}
	if psys != nil {
		t.Fatal("the system domain was reported while a sibling zone was unidentifiable")
	}
	if dram == nil {
		t.Fatal("dram is unaffected by an unreadable TOP-LEVEL name and must survive")
	}

	// A SUB-ZONE can only have been dram, so only dram is compromised.
	psys, pkg, dram = build(t, "intel-rapl:0:0")
	if dram != nil {
		t.Fatal("the dram domain reported a sum with an unidentifiable sub-zone present")
	}
	if pkg == nil || psys == nil {
		t.Fatalf("cpu and system are unaffected by an unreadable SUB-ZONE name: pkg=%v psys=%v", pkg, psys)
	}
}
