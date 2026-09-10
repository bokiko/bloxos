//go:build linux

package main

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
)

// newFakeEstimateEnv is an estimator surface for a machine that measures
// nothing and is not identifiable — the deliberate default, so every test
// says out loud which piece it is turning on.
func newFakeEstimateEnv() powerEstimateEnv {
	return powerEstimateEnv{
		read:          func(string) ([]byte, error) { return nil, errors.New("absent") },
		getenv:        func(string) string { return "" },
		identity:      func(func(string) ([]byte, error)) powerIdentity { return powerIdentity{arch: "arm64"} },
		util:          func() utilSource { return utilRamp(0.5) },
		nvidiaPresent: func() bool { return false },
	}
}

func withIdentity(env powerEstimateEnv, id powerIdentity) powerEstimateEnv {
	env.identity = func(func(string) ([]byte, error)) powerIdentity { return id }
	return env
}

// orangePi5 is the identity an RK3588 board actually presents to Linux.
func orangePi5() powerIdentity {
	return powerIdentity{
		dtCompatible: []string{"xunlong,orangepi-5", "rockchip,rk3588s"},
		cpuModel:     "cortex-a55",
		cores:        8,
		arch:         "arm64",
	}
}

// --- /proc/stat utilisation ---

func TestProcStatUtilisationExcludesIdleIowaitAndSteal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stat")
	u := &procStatUtil{readFile: os.ReadFile, path: path}

	// user nice system idle iowait irq softirq steal guest guest_nice
	write := func(line string) {
		if err := os.WriteFile(path, []byte(line+"\ncpu0 1 2 3 4 5 6 7 8 0 0\nintr 999\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Busy = user+nice+system+irq+softirq = 100+0+50+10+5 = 165.
	// Not busy = idle 500 + iowait 200 + steal 35 = 735. Total 900.
	write("cpu 100 0 50 500 200 10 5 35 0 0")
	busy, total, ok := u.read()
	if !ok || busy != 165 || total != 900 {
		t.Fatalf("busy=%v total=%v ok=%v; iowait is a halted CPU and steal is another guest's time — neither is this machine burning watts", busy, total, ok)
	}

	// guest/guest_nice are already counted inside user/nice; adding them
	// again would double-count a virtualised workload.
	write("cpu 100 0 50 500 200 10 5 35 90 10")
	if _, total, _ := u.read(); total != 900 {
		t.Fatalf("total %v; guest columns must not be added again", total)
	}

	// Malformed or absent files are unavailable, never zero utilisation.
	for _, bad := range []string{"cpu", "cpu 1 2 3", "notcpu 1 2 3 4 5", "cpu 1 2 x 4 5"} {
		write(bad)
		if _, _, ok := u.read(); ok {
			t.Fatalf("%q must not parse as a utilisation", bad)
		}
	}
	u.path = filepath.Join(dir, "does-not-exist")
	if _, _, ok := u.read(); ok {
		t.Fatal("a missing /proc/stat must be unavailable")
	}
}

func TestProcStatDrivesTheModelEndToEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stat")
	u := &procStatUtil{readFile: os.ReadFile, path: path}
	s := newEstimateSampler(powerProfile{idleWatts: 3.5, maxWatts: 12.0}, u, powerSampleInterval)

	base := time.Now()
	// A boot-fresh /proc/stat whose counters have not moved yet carries no
	// interval, so it primes and reports nothing.
	if err := os.WriteFile(path, []byte("cpu 0 0 0 100 0 0 0 0 0 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.sample(base); ok {
		t.Fatal("the first read primes the counters")
	}
	// A second of pure idle time.
	if err := os.WriteFile(path, []byte("cpu 0 0 0 200 0 0 0 0 0 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w, ok := s.sample(base.Add(powerSampleInterval)); !ok || math.Abs(w-3.5) > 1e-9 {
		t.Fatalf("idle: ok=%v w=%v, want 3.5 W", ok, w)
	}
	// A second of pure user time.
	if err := os.WriteFile(path, []byte("cpu 100 0 0 200 0 0 0 0 0 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w, ok := s.sample(base.Add(2 * powerSampleInterval)); !ok || math.Abs(w-12.0) > 1e-9 {
		t.Fatalf("saturated: ok=%v w=%v, want 12 W", ok, w)
	}
}

// --- identity ---

func TestDeviceTreeIsTheBoardIdentityOnARM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compatible")
	// The device tree stores NUL-separated strings, most specific first.
	if err := os.WriteFile(path, []byte("xunlong,orangepi-5\x00rockchip,rk3588s\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	read := func(p string) ([]byte, error) {
		if p == deviceTreeCompatible {
			return os.ReadFile(path)
		}
		return nil, errors.New("absent")
	}
	got := readDeviceTreeCompatible(read)
	want := []string{"xunlong,orangepi-5", "rockchip,rk3588s"}
	if len(got) != len(want) {
		t.Fatalf("compatible %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("compatible %v, want %v (board before SoC)", got, want)
		}
	}
	if readDeviceTreeCompatible(func(string) ([]byte, error) { return nil, errors.New("absent") }) != nil {
		t.Fatal("a machine with no device tree must report no compatible strings")
	}
}

func TestCPUModelNormalisationSurvivesVendorDecoration(t *testing.T) {
	for raw, want := range map[string]string{
		"Intel(R) N100":     "intel n100",
		"  Intel®  N100  ":  "intel n100",
		"AMD Ryzen 9 5950X": "amd ryzen 9 5950x",
		"Cortex-A55":        "cortex-a55",
		"":                  "",
	} {
		if got := normalizePowerCPUModel(raw); got != want {
			t.Fatalf("normalize(%q) = %q, want %q", raw, got, want)
		}
	}
}

// --- ordering: a measured counter always wins ---

func TestEstimateIsWithheldWheneverAnythingIsMeasured(t *testing.T) {
	measured := &stubPowerSource{id: "rapl-package"}
	for _, tc := range []struct {
		name string
		set  powerSourceSet
	}{
		{"system measured", powerSourceSet{system: measured}},
		{"cpu measured", powerSourceSet{cpu: measured}},
		{"dram measured", powerSourceSet{dram: measured}},
		{"gpu measured", powerSourceSet{gpu: &stubGPUPoller{}}},
	} {
		env := withIdentity(newFakeEstimateEnv(), orangePi5())
		got := attachPowerEstimate(tc.set, env)
		if got.system != tc.set.system {
			t.Fatalf("%s: the estimator replaced or added a system backend (%v); a machine with a real counter must never carry a modelled platform figure beside it",
				tc.name, got.system)
		}
	}
	// nvidia-smi streaming GPU power counts as measuring, even though it is
	// discovered outside the source set.
	env := withIdentity(newFakeEstimateEnv(), orangePi5())
	env.nvidiaPresent = func() bool { return true }
	if got := attachPowerEstimate(powerSourceSet{}, env); got.system != nil {
		t.Fatalf("nvidia-smi present: got system backend %q; a CPU-driven model cannot stand beside a measured 300 W card", got.system.source())
	}
}

func TestEstimateEngagesOnlyWhenNothingIsMeasured(t *testing.T) {
	env := withIdentity(newFakeEstimateEnv(), orangePi5())
	set := attachPowerEstimate(powerSourceSet{}, env)
	if set.system == nil {
		t.Fatal("a machine measuring nothing, with a known board, must get the modelled backend")
	}
	if set.system.source() != powerhistory.SourceEstimateUtil {
		t.Fatalf("source %q, want %q", set.system.source(), powerhistory.SourceEstimateUtil)
	}
	if set.cpu != nil || set.dram != nil {
		t.Fatal("the estimator answers for the system domain only; cpu and dram stay absent")
	}
	if !strings.Contains(set.describe(), powerhistory.SourceEstimateUtil) {
		t.Fatalf("startup description %q must name the modelled backend", set.describe())
	}
}

func TestUnidentifiableMachineGetsNoEstimate(t *testing.T) {
	// The default fake identity is an arm64 machine with no device tree and
	// no CPU string: nothing to anchor an envelope on.
	if set := attachPowerEstimate(powerSourceSet{}, newFakeEstimateEnv()); set.system != nil {
		t.Fatalf("got system backend %q; an unjustifiable estimate is worse than nothing", set.system.source())
	}
}

func TestOperatorOptOutAndOverrideReachTheSourceSet(t *testing.T) {
	off := withIdentity(newFakeEstimateEnv(), orangePi5())
	off.getenv = func(k string) string {
		if k == powerEstimateEnvDisable {
			return "off"
		}
		return ""
	}
	if set := attachPowerEstimate(powerSourceSet{}, off); set.system != nil {
		t.Fatalf("%s=off must leave the machine dark, got %q", powerEstimateEnvDisable, set.system.source())
	}

	// An override enables estimation on a machine nothing else covers, and
	// the resulting sampler uses that envelope.
	over := newFakeEstimateEnv() // unidentifiable machine
	over.getenv = func(k string) string {
		if k == powerEstimateEnvWatts {
			return "5:45"
		}
		return ""
	}
	over.util = func() utilSource { return utilRamp(1.0) }
	set := attachPowerEstimate(powerSourceSet{}, over)
	if set.system == nil {
		t.Fatal("the override must enable estimation on an otherwise unidentifiable machine")
	}
	base := time.Now()
	set.system.sample(base) // prime
	if w, ok := set.system.sample(base.Add(powerSampleInterval)); !ok || math.Abs(w-45) > 1e-9 {
		t.Fatalf("saturated with a 5:45 override: ok=%v w=%v, want 45 W", ok, w)
	}
}

// --- stubs ---

type stubPowerSource struct{ id string }

func (s *stubPowerSource) source() string                   { return s.id }
func (s *stubPowerSource) sample(time.Time) (float64, bool) { return 42, true }

type stubGPUPoller struct{}

func (stubGPUPoller) poll(now time.Time) powerGPUTick {
	w := 300.0
	return powerGPUTick{at: now, readings: []powerReading{{id: "gpu0", watts: &w}}, complete: true}
}
