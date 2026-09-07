//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestUpdateHelperTimeoutIsInertWithoutConsole records the evidence behind
// the helper rewrite: `timeout /t` needs a console for its keyboard wait,
// and a process started without one (the shape the SCM gives the helper)
// gets "Input redirection is not supported" and returns at once. A helper
// relying on it never actually waited for the agent to exit.
func TestUpdateHelperTimeoutIsInertWithoutConsole(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/C", "timeout /t 2 /nobreak > nul")
	cmd.Stdin = nil // NUL, not a console
	started := time.Now()
	err := cmd.Run()
	elapsed := time.Since(started)
	if elapsed >= 1500*time.Millisecond {
		t.Fatalf("timeout /t 2 waited %s without a console (err=%v); the old helper delay was real after all", elapsed, err)
	}
	t.Logf("timeout /t 2 without a console returned after %s (err=%v)", elapsed, err)
}

// TestUpdateHelperRetriesWhileTargetLocked runs the generated helper against
// a target held open with no sharing, the way a running executable is held.
// While the lock is held the helper must keep retrying and leave the marker
// alone; once released it replaces the binary, removes the marker and the
// staged copy, logs the outcome and deletes itself. The service name does
// not exist here (no administrator rights needed), so the STOPPED and
// RUNNING waits end at once and the failed start is logged; the SCM-level
// flow is covered by TestSCMPendingUpdateHelperReplacesServiceBinary.
func TestUpdateHelperRetriesWhileTargetLocked(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "agent.exe")
	newBin := target + ".new"
	marker := target + ".pending"
	for path, content := range map[string]string{target: "old-binary", newBin: "new-binary", marker: "{}"} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	helperPath := filepath.Join(dir, "helper.bat")
	if err := os.WriteFile(helperPath, []byte(buildHelperBatch(target, newBin, marker, "BloxOSE2Test-no-such-service")), 0o755); err != nil {
		t.Fatal(err)
	}

	// Hold the target open with share mode 0, like the running agent.
	h, err := windows.CreateFile(windows.StringToUTF16Ptr(target), windows.GENERIC_READ, 0, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("lock target: %v", err)
	}
	locked := true
	defer func() {
		if locked {
			windows.CloseHandle(h)
		}
	}()

	cmd := exec.Command("cmd.exe", "/C", helperPath)
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	time.Sleep(3 * time.Second)
	select {
	case err := <-done:
		t.Fatalf("helper exited (%v) while the binary was still locked", err)
	default:
	}
	// The target cannot be opened by anyone else while the handle is held
	// (that is the point of the lock), so check the move has not happened
	// through the files around it: the staged copy and marker still exist.
	for _, still := range []string{marker, newBin} {
		if _, err := os.Stat(still); err != nil {
			t.Fatalf("%s gone while the binary was still locked: %v", filepath.Base(still), err)
		}
	}

	windows.CloseHandle(h)
	locked = false
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("helper reported success although the service does not exist")
		}
	case <-time.After(45 * time.Second):
		cmd.Process.Kill()
		t.Fatal("helper did not finish within 45 s of the lock being released")
	}

	if got, _ := os.ReadFile(target); string(got) != "new-binary" {
		t.Fatalf("target not replaced after unlock: %q", got)
	}
	for _, gone := range []string{marker, newBin, helperPath} {
		if _, err := os.Stat(gone); err == nil {
			t.Errorf("%s still exists after the helper finished", filepath.Base(gone))
		}
	}
	logText, err := os.ReadFile(target + ".update-helper.log")
	if err != nil {
		t.Fatalf("helper log missing: %v", err)
	}
	for _, want := range []string{"replaced binary after", "not RUNNING after start"} {
		if !strings.Contains(string(logText), want) {
			t.Fatalf("helper log lacks %q:\n%s", want, logText)
		}
	}
	t.Logf("helper log: %s", strings.TrimSpace(string(logText)))
}
