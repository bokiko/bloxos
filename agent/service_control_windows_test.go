//go:build windows

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

/* ============================================================================
 * Real service control manager tests
 *
 * Opt-in with BLOXOS_SCM_TEST=1 (the CI windows job sets it). They install
 * this test binary as a uniquely named service whose stop handler is slow or
 * stuck, so the code under test is exercised against the real SCM rather
 * than a fake. With the gate set, a non-elevated process fails rather than
 * skips, so a misconfigured runner cannot pass silently.
 * ============================================================================ */

const (
	testServiceSlowStop  = 4 * time.Second
	testServiceStuckStop = 5 * time.Minute
)

// testServiceModes is called from TestMain. When the SCM started this binary
// as a test service, or the code under test spawned it as the restart
// helper, run that role instead of the tests.
func testServiceModes() bool {
	var mode, oncePath, helperTarget string
	for _, a := range os.Args[1:] {
		switch {
		case strings.HasPrefix(a, "-bloxos-test-service="):
			mode = strings.TrimPrefix(a, "-bloxos-test-service=")
		case strings.HasPrefix(a, "-bloxos-test-once="):
			oncePath = strings.TrimPrefix(a, "-bloxos-test-once=")
		case strings.HasPrefix(a, "-bloxos-test-restart-helper="):
			helperTarget = strings.TrimPrefix(a, "-bloxos-test-restart-helper=")
		}
	}
	switch {
	case helperTarget != "":
		if err := runRestartServiceHelper(helperTarget); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	case mode != "":
		// The service type is own-process, so the SCM ignores this name.
		_ = svc.Run("BloxOSE2Test", &testServiceHandler{mode: mode, oncePath: oncePath})
		os.Exit(0)
	}
	return false
}

// testServiceHandler is the SCM handler for the test service. Its stop takes
// testServiceSlowStop ("slow") or effectively never completes ("stuck"). In
// "self-restart" mode it spawns the restart helper against its own service
// name once (guarded by oncePath so the restarted instance does not loop).
type testServiceHandler struct {
	mode     string
	oncePath string
}

func (h *testServiceHandler) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}
	if h.mode == "pending-update" && len(args) > 0 {
		// The production sequence from applyPendingUpdate, minus the
		// signature checks: while START_PENDING, write the helper beside the
		// executable, spawn it detached and exit. The restarted (replaced)
		// binary finds no marker and runs as an ordinary service.
		exe, _ := os.Executable()
		marker := exe + ".pending"
		if _, err := os.Stat(marker); err == nil {
			helperPath := filepath.Join(filepath.Dir(exe), "bloxos-agent-update-helper.bat")
			helper := buildHelperBatch(exe, exe+".new", marker, args[0])
			if err := os.WriteFile(helperPath, []byte(helper), 0o755); err == nil {
				if err := spawnDetachedHelper(helperPath); err == nil {
					os.Exit(0)
				}
			}
		}
	}
	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	if h.mode == "self-restart" && len(args) > 0 {
		if f, err := os.OpenFile(h.oncePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); err == nil {
			f.Close()
			exe, _ := os.Executable()
			_ = spawnDetachedProcess(exe, "-bloxos-test-restart-helper="+args[0])
		}
	}
	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			changes <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			changes <- svc.Status{State: svc.StopPending}
			switch h.mode {
			case "slow":
				time.Sleep(testServiceSlowStop)
			case "stuck":
				time.Sleep(testServiceStuckStop)
			}
			return false, 0
		}
	}
	return false, 0
}

func requireSCM(t *testing.T) *mgr.Mgr {
	t.Helper()
	if os.Getenv("BLOXOS_SCM_TEST") == "" {
		t.Skip("set BLOXOS_SCM_TEST=1 to run against the real service control manager")
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Fatal("BLOXOS_SCM_TEST is set but this process is not elevated; the SCM tests need administrator rights")
	}
	m, err := mgr.Connect()
	if err != nil {
		t.Fatalf("connect to SCM: %v", err)
	}
	t.Cleanup(func() { m.Disconnect() })
	return m
}

// installTestService registers this binary as a manual-start service with a
// unique name and removes it again (killing a stuck process) on cleanup.
func installTestService(t *testing.T, m *mgr.Mgr, mode string, extraArgs ...string) (string, *mgr.Service) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("BloxOSE2Test-%s-%d-%d", mode, os.Getpid(), time.Now().UnixNano()%1_000_000)
	args := append([]string{"-bloxos-test-service=" + mode}, extraArgs...)
	s, err := m.CreateService(name, exe, mgr.Config{DisplayName: name, StartType: mgr.StartManual}, args...)
	if err != nil {
		t.Fatalf("create service %s: %v", name, err)
	}
	t.Cleanup(func() {
		defer s.Close()
		if st, err := s.Query(); err == nil && st.State != svc.Stopped {
			_, _ = s.Control(svc.Stop)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			if waitForState(ctx, s, name, svc.Stopped) != nil && st.ProcessId != 0 {
				// A stuck stop handler is part of the test; do not wait for it.
				if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, st.ProcessId); err == nil {
					_ = windows.TerminateProcess(h, 1)
					windows.CloseHandle(h)
				}
			}
			cancel()
		}
		if err := s.Delete(); err != nil {
			t.Logf("cleanup: delete %s: %v", name, err)
		}
	})
	return name, s
}

func startAndWaitRunning(t *testing.T, s *mgr.Service, name string) uint32 {
	t.Helper()
	if err := s.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := waitForState(ctx, s, name, svc.Running); err != nil {
		t.Fatal(err)
	}
	st, err := s.Query()
	if err != nil || st.ProcessId == 0 {
		t.Fatalf("query %s: pid=%d err=%v", name, st.ProcessId, err)
	}
	return st.ProcessId
}

// TestSCMRestartWaitsForSlowStop: restart_service on a service whose stop
// takes four seconds must wait for STOPPED before starting, and end with the
// service RUNNING under a new process.
func TestSCMRestartWaitsForSlowStop(t *testing.T) {
	m := requireSCM(t)
	name, s := installTestService(t, m, "slow")
	oldPID := startAndWaitRunning(t, s, name)

	ctx, cancel := context.WithTimeout(context.Background(), agentCommandTimeout)
	defer cancel()
	started := time.Now()
	out, err := controlService(ctx, "restart_service", name)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("restart failed after %s: %v\n%s", elapsed, err, out)
	}
	if elapsed < testServiceSlowStop-500*time.Millisecond {
		t.Fatalf("restart returned after %s, before the slow stop could have completed (%s)", elapsed, testServiceSlowStop)
	}
	st, err := s.Query()
	if err != nil {
		t.Fatal(err)
	}
	if st.State != svc.Running {
		t.Fatalf("service is %s after restart, want running\n%s", serviceStateName(st.State), out)
	}
	if st.ProcessId == 0 || st.ProcessId == oldPID {
		t.Fatalf("service pid %d after restart (was %d); expected a new process\n%s", st.ProcessId, oldPID, out)
	}
	for _, want := range []string{"stop requested", "stopped", "start requested", "running"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// TestSCMRestartBoundedWhenStopHangs: a service whose stop never completes
// must not hold the command past its context; the error names the state.
func TestSCMRestartBoundedWhenStopHangs(t *testing.T) {
	m := requireSCM(t)
	name, s := installTestService(t, m, "stuck")
	startAndWaitRunning(t, s, name)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := time.Now()
	_, err := controlService(ctx, "restart_service", name)
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("restart of a stuck service reported success")
	}
	if !strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), "stop-pending") {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("command held for %s past a 3 s bound", elapsed)
	}
}

// TestSCMStopAndStartSeparately: stop_service waits for STOPPED and
// start_service waits for RUNNING; repeating either is reported, not failed.
func TestSCMStopAndStartSeparately(t *testing.T) {
	m := requireSCM(t)
	name, s := installTestService(t, m, "slow")
	startAndWaitRunning(t, s, name)
	ctx, cancel := context.WithTimeout(context.Background(), agentCommandTimeout)
	defer cancel()

	if out, err := controlService(ctx, "stop_service", name); err != nil {
		t.Fatalf("stop: %v\n%s", err, out)
	}
	if st, _ := s.Query(); st.State != svc.Stopped {
		t.Fatalf("after stop_service state is %s", serviceStateName(st.State))
	}
	if out, err := controlService(ctx, "stop_service", name); err != nil || !strings.Contains(string(out), "already stopped") {
		t.Fatalf("second stop: err=%v out=%s", err, out)
	}
	if out, err := controlService(ctx, "start_service", name); err != nil {
		t.Fatalf("start: %v\n%s", err, out)
	}
	if st, _ := s.Query(); st.State != svc.Running {
		t.Fatalf("after start_service state is %s", serviceStateName(st.State))
	}
	if out, err := controlService(ctx, "start_service", name); err != nil || !strings.Contains(string(out), "already running") {
		t.Fatalf("second start: err=%v out=%s", err, out)
	}
}

// TestSCMUnknownServiceIsAnError: a missing service is reported, not hung.
func TestSCMUnknownServiceIsAnError(t *testing.T) {
	requireSCM(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := controlService(ctx, "restart_service", "BloxOSE2Test-does-not-exist")
	if err == nil || !strings.Contains(err.Error(), "open service") {
		t.Fatalf("expected open-service error, got %v", err)
	}
}

// TestSCMSelfRestartViaDetachedHelper: a service that restarts itself through
// the detached helper (the path BloxOSAgent uses for restart_service on its
// own name) ends up RUNNING under a new process, with the helper's log
// recording the stop/start sequence.
func TestSCMSelfRestartViaDetachedHelper(t *testing.T) {
	m := requireSCM(t)
	once := t.TempDir() + `\once`
	name, s := installTestService(t, m, "self-restart", "-bloxos-test-once="+once)
	oldPID := startAndWaitRunning(t, s, name)

	deadline := time.Now().Add(60 * time.Second)
	for {
		st, err := s.Query()
		if err != nil {
			t.Fatal(err)
		}
		if st.State == svc.Running && st.ProcessId != 0 && st.ProcessId != oldPID {
			break
		}
		if time.Now().After(deadline) {
			logText, _ := os.ReadFile(restartHelperLogPath())
			t.Fatalf("service not restarted by helper within 60 s: state=%s pid=%d (was %d)\nhelper log:\n%s",
				serviceStateName(st.State), st.ProcessId, oldPID, logText)
		}
		time.Sleep(scmPollInterval)
	}
	// The helper writes its final line after its own RUNNING poll, which
	// can lag the pid change observed above by a poll interval.
	logDeadline := time.Now().Add(15 * time.Second)
	for {
		logText, _ := os.ReadFile(restartHelperLogPath())
		if strings.Contains(string(logText), name+" restarted") {
			t.Logf("helper log:\n%s", logText)
			return
		}
		if time.Now().After(logDeadline) {
			t.Fatalf("helper log does not record the restart:\n%s", logText)
		}
		time.Sleep(scmPollInterval)
	}
}

// copyBinary writes src's bytes plus extra to dst.
func copyBinary(t *testing.T, src, dst string, extra []byte) int64 {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	n, err := io.Copy(out, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := out.Write(extra); err != nil {
		t.Fatal(err)
	}
	return n + int64(len(extra))
}

// TestSCMPendingUpdateHelperReplacesServiceBinary runs the pending-update
// helper the way production does: the service process writes the helper
// while START_PENDING, spawns it detached and exits. Under the real SCM the
// helper must wait for STOPPED, replace the (until then locked) service
// binary with the staged copy, remove the marker and staged file, start the
// service and see it RUNNING, logging each step.
func TestSCMPendingUpdateHelperReplacesServiceBinary(t *testing.T) {
	m := requireSCM(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "bloxos-e2-svc.exe")
	copyBinary(t, self, target, nil)
	// The staged binary is this test binary with trailing bytes, so the
	// replacement is visible by size while remaining runnable.
	newSize := copyBinary(t, self, target+".new", []byte("\n#bloxos-e2-staged\n"))
	if err := os.WriteFile(target+".pending", []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	name := fmt.Sprintf("BloxOSE2Test-update-%d-%d", os.Getpid(), time.Now().UnixNano()%1_000_000)
	s, err := m.CreateService(name, target, mgr.Config{DisplayName: name, StartType: mgr.StartManual}, "-bloxos-test-service=pending-update")
	if err != nil {
		t.Fatalf("create service %s: %v", name, err)
	}
	t.Cleanup(func() {
		defer s.Close()
		if st, err := s.Query(); err == nil && st.State != svc.Stopped {
			_, _ = s.Control(svc.Stop)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = waitForState(ctx, s, name, svc.Stopped)
			cancel()
		}
		if err := s.Delete(); err != nil {
			t.Logf("cleanup: delete %s: %v", name, err)
		}
	})

	// The first instance exits while START_PENDING, so do not wait for
	// RUNNING here; the helper is what brings the service up.
	if err := s.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	logPath := target + ".update-helper.log"
	deadline := time.Now().Add(120 * time.Second)
	for {
		st, err := s.Query()
		if err != nil {
			t.Fatal(err)
		}
		info, statErr := os.Stat(target)
		logText, _ := os.ReadFile(logPath)
		replaced := statErr == nil && info.Size() == newSize
		if st.State == svc.Running && st.ProcessId != 0 && replaced && strings.Contains(string(logText), name+" running") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("helper did not complete within 120 s: state=%s pid=%d replaced=%v\nhelper log:\n%s",
				serviceStateName(st.State), st.ProcessId, replaced, logText)
		}
		time.Sleep(scmPollInterval)
	}
	logText, _ := os.ReadFile(logPath)
	t.Logf("helper log:\n%s", logText)
	for _, want := range []string{"replaced binary after", name + " running"} {
		if !strings.Contains(string(logText), want) {
			t.Errorf("helper log lacks %q", want)
		}
	}
	if strings.Contains(string(logText), "did not report STOPPED") {
		t.Errorf("helper gave up waiting for STOPPED")
	}
	for _, gone := range []string{target + ".pending", target + ".new", filepath.Join(dir, "bloxos-agent-update-helper.bat")} {
		if _, err := os.Stat(gone); err == nil {
			t.Errorf("%s still exists after the helper finished", filepath.Base(gone))
		}
	}
}
