//go:build linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
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

func TestRAPLDiscoverySelectsOnlyTopLevelPackages(t *testing.T) {
	r := newFakeRAPL(t)
	r.zone("intel-rapl:0", "package-0", 1000, 1<<40)
	r.zone("intel-rapl:1", "package-1", 2000, 1<<40)
	r.zone("intel-rapl:0:0", "core", 500, 1<<40)      // sub-zone: overlaps package
	r.zone("intel-rapl:0:1", "dram", 500, 1<<40)      // sub-zone
	r.zone("intel-rapl-mmio:0", "package-0", 1000, 0) // MMIO mirror of package-0
	r.zone("intel-rapl:2", "psys", 9000, 1<<40)       // platform: contains packages
	s := discoverRAPL(r.root, r.read)
	if s == nil || len(s.zones) != 2 {
		t.Fatalf("want exactly the two package zones, got %+v", s)
	}
}

func TestRAPLUnreadablePackageDisablesBackend(t *testing.T) {
	r := newFakeRAPL(t)
	r.zone("intel-rapl:0", "package-0", 1000, 1<<40)
	r.zone("intel-rapl:1", "package-1", 2000, 1<<40)
	r.fail[filepath.Join(r.root, "intel-rapl:1", "energy_uj")] = true
	if s := discoverRAPL(r.root, r.read); s != nil {
		t.Fatalf("a partially readable package set must not be reported as CPU total: %+v", s)
	}
	if s := discoverRAPL(filepath.Join(r.root, "missing"), r.read); s != nil {
		t.Fatal("no powercap tree must mean unavailable")
	}
	// The interface value must be a true nil when unavailable.
	var iface powerCPUSampler
	if s := discoverRAPL(filepath.Join(r.root, "missing"), r.read); s != nil {
		iface = s
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
	s := discoverRAPL(r.root, r.read)
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
