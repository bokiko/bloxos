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
	r.set("intel-rapl:0", 25_000) // wrapped: 950000 → 1000000 → 25000 = +75 mJ
	r.set("intel-rapl:1", 175_000)
	w, ok = s.sample(base.Add(2 * time.Second))
	if !ok || w < 0.0999 || w > 0.1001 {
		t.Fatalf("wrap: ok=%v w=%v", ok, w)
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
