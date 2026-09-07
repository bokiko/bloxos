package main

import (
	"context"
	"os"
	"strings"
	"sync"
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
		// A child that never exits on its own. It is killable, so this covers the
		// kill path only; a D-state child that ignores SIGKILL is covered by
		// TestRunBoundedReturnsWhenWaitCannotAndDoesNotStack.
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
	return []string{exe, "-test.run=NoSuchTest_" + mode}
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
	// This child is killable, so the background wait returns and the gate
	// clears; later tests must not see it as still outstanding.
	waitUntilCleared(t, 5*time.Second, func() bool {
		collectorInflightMu.Lock()
		defer collectorInflightMu.Unlock()
		return !collectorInflight[strings.Join(argv, " ")]
	})
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

// TestRunBoundedReturnsWhenWaitCannotAndDoesNotStack: a function that never
// returns (emulating a child in uninterruptible sleep whose Wait cannot
// complete) must not hold the caller past the bound, and a second call for
// the same command must be refused rather than starting another copy.
// Once the blocked wait finally returns, the gate clears.
func TestRunBoundedReturnsWhenWaitCannotAndDoesNotStack(t *testing.T) {
	release := make(chan struct{})
	var starts int32
	var mu sync.Mutex
	blocked := func(ctx context.Context) ([]byte, error) {
		mu.Lock()
		starts++
		mu.Unlock()
		<-release // ignores ctx entirely, like a D-state child
		return []byte("late"), nil
	}

	start := time.Now()
	_, err := runBounded("blocked-collector", 200*time.Millisecond, blocked)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("caller held for %s", time.Since(start))
	}

	// Repeated ticks must not stack another process.
	if _, err := runBounded("blocked-collector", 200*time.Millisecond, blocked); err != errCollectorBusy {
		t.Fatalf("second invocation should be refused as busy, got %v", err)
	}
	mu.Lock()
	n := starts
	mu.Unlock()
	if n != 1 {
		t.Fatalf("blocked collector started %d times, want 1", n)
	}

	// When the wait finally returns, the gate clears and the next tick runs.
	close(release)
	waitUntilCleared(t, 2*time.Second, func() bool {
		collectorInflightMu.Lock()
		defer collectorInflightMu.Unlock()
		return !collectorInflight["blocked-collector"]
	})
	out, err := runBounded("blocked-collector", time.Second, func(context.Context) ([]byte, error) { return []byte("fresh"), nil })
	if err != nil || string(out) != "fresh" {
		t.Fatalf("gate did not clear: out=%q err=%v", out, err)
	}
}

// TestRunBoundedFastSuccessNeverReportsTimeout: a function that returns
// promptly must be reported as a success every time, and the very next call
// for the same key must run rather than be refused as busy. The pinned
// subtest holds the caller until the deadline has fired, which is exactly
// the state the old worker-side cancel() produced after publishing a
// result: both select arms ready, and roughly half the calls "timed out".
func TestRunBoundedFastSuccessNeverReportsTimeout(t *testing.T) {
	quick := func(context.Context) ([]byte, error) { return []byte("fast"), nil }
	run := func(t *testing.T, iterations int, timeout time.Duration) {
		for i := 0; i < iterations; i++ {
			out, err := runBounded("fast-collector", timeout, quick)
			if err != nil {
				t.Fatalf("iteration %d: fast collector reported %v", i, err)
			}
			if string(out) != "fast" {
				t.Fatalf("iteration %d: unexpected output %q", i, out)
			}
		}
	}
	t.Run("free-running", func(t *testing.T) { run(t, 500, 50*time.Millisecond) })
	t.Run("result-and-deadline-both-ready", func(t *testing.T) {
		runBoundedBeforeWaitHook = func(ctx context.Context) { <-ctx.Done() }
		defer func() { runBoundedBeforeWaitHook = nil }()
		run(t, 50, 20*time.Millisecond)
	})
}

func waitUntilCleared(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met before deadline")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
