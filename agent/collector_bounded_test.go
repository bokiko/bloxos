package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestMain lets this test binary double as a synthetic collector: when
// BLOXOS_TEST_COLLECTOR is set it behaves like the child under test instead
// of running tests. Portable to the Windows runner, unlike shelling out to
// sleep.
func TestMain(m *testing.M) {
	switch os.Getenv("BLOXOS_TEST_COLLECTOR") {
	case "hang":
		// Emulate nvidia-smi stuck in D state: never exits on its own.
		time.Sleep(time.Hour)
		os.Exit(0)
	case "ok":
		os.Stdout.WriteString("collector-output\n")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func selfAsCollector(t *testing.T, mode string) []string {
	t.Helper()
	t.Setenv("BLOXOS_TEST_COLLECTOR", mode)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return []string{exe, "-test.run=NoSuchTest"}
}

// TestRunCollectorTimesOutHungChild: a collector that never exits must
// return an error within the bound, not stall the caller.
func TestRunCollectorTimesOutHungChild(t *testing.T) {
	old := collectorTimeout
	collectorTimeout = 300 * time.Millisecond
	defer func() { collectorTimeout = old }()

	argv := selfAsCollector(t, "hang")
	start := time.Now()
	out, err := runCollector(argv...)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("hung collector returned no error (out=%q)", out)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("hung collector held the caller for %s", elapsed)
	}
}

// TestRunCollectorReturnsOutput: a healthy collector's stdout is returned.
func TestRunCollectorReturnsOutput(t *testing.T) {
	argv := selfAsCollector(t, "ok")
	out, err := runCollector(argv...)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "collector-output" {
		t.Fatalf("unexpected output %q", out)
	}
}

// TestGPUCollectorReportsUnavailableNotZeroOnHang: when nvidia-smi hangs,
// collectGPUMetrics reports no GPUs (unavailable), never zeroed readings.
func TestGPUCollectorReportsUnavailableNotZeroOnHang(t *testing.T) {
	old := collectorTimeout
	collectorTimeout = 300 * time.Millisecond
	defer func() { collectorTimeout = old }()
	argv := selfAsCollector(t, "hang")
	oldResolve := resolveNvidiaSmi
	resolveNvidiaSmi = func() string { return argv[0] }
	defer func() { resolveNvidiaSmi = oldResolve }()

	gpus := collectGPUMetrics()
	if gpus != nil {
		t.Fatalf("hung nvidia-smi produced readings: %+v", gpus)
	}
}

// keep exec imported for platforms where the helper is unused
var _ = exec.Command
