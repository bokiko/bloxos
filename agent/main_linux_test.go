//go:build linux

// First-ever test file in agent/. Establishes the test pattern for the
// package. Future Linux-only agent tests anchor here. PTY-touching tests
// must be gated behind BLOXOS_PTY_TEST=1 to keep CI runs portable
// across runner environments that lack /dev/ptmx.

package main

import (
	"errors"
	"os/exec"
	"os/user"
	"sync"
	"testing"
)

// fakeWaitable counts the number of times Wait() is invoked. Used by
// the waitOnce coordinator tests to prove the underlying Wait is called
// exactly once regardless of how many goroutines race for it.
type fakeWaitable struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (f *fakeWaitable) Wait() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.err
}

func (f *fakeWaitable) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// TestWaitCoordinatorCallsUnderlyingWaitOnce locks in the C3 fix:
// in agent/main_linux.go, both the cleanup defer and the waitCh
// goroutine called bashCmd.Wait() / bashCmd.Process.Wait() on the
// same command. Per stdlib contract, Wait may only be called once
// per *exec.Cmd; the second call's behaviour is undefined.
//
// Fix introduces a waitOnce coordinator: many callers can ask for
// the result, only the first triggers the underlying Wait. This test
// runs five concurrent Wait calls against a fake and asserts the fake
// observes exactly one call. Pure unit test — no PTY, no /dev/ptmx,
// runs in any CI.
func TestWaitCoordinatorCallsUnderlyingWaitOnce(t *testing.T) {
	const goroutines = 5
	wantErr := errors.New("test-exit-status")
	fake := &fakeWaitable{err: wantErr}
	waiter := newWaitOnce(fake)

	var wg sync.WaitGroup
	results := make([]error, goroutines)
	start := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			<-start // synchronise so all goroutines call Wait() simultaneously
			results[i] = waiter.Wait()
		}()
	}
	close(start)
	wg.Wait()

	if got := fake.callCount(); got != 1 {
		t.Errorf("underlying Wait was called %d times, want 1", got)
	}
	for i, r := range results {
		if !errors.Is(r, wantErr) {
			t.Errorf("results[%d] = %v, want %v", i, r, wantErr)
		}
	}

	// Subsequent calls must also return the cached error without
	// calling the underlying Wait again.
	if got := waiter.Wait(); !errors.Is(got, wantErr) {
		t.Errorf("post-completion Wait() = %v, want %v", got, wantErr)
	}
	if got := fake.callCount(); got != 1 {
		t.Errorf("post-completion call count = %d, want 1", got)
	}
}

// TestWaitCoordinatorPropagatesNilError covers the happy-path case
// where the underlying command exited cleanly (Wait returned nil).
// All callers must see nil; the cached-result path must not synthesise
// a fake "already finished" error.
func TestWaitCoordinatorPropagatesNilError(t *testing.T) {
	fake := &fakeWaitable{err: nil}
	waiter := newWaitOnce(fake)

	if err := waiter.Wait(); err != nil {
		t.Errorf("first Wait() = %v, want nil", err)
	}
	if err := waiter.Wait(); err != nil {
		t.Errorf("second Wait() = %v, want nil", err)
	}
	if got := fake.callCount(); got != 1 {
		t.Errorf("call count = %d, want 1", got)
	}
}

// TestApplyTerminalCredentials_NonNumericUIDFailsClosed locks in C4:
// when the resolved terminal user has a UID string that cannot be
// parsed as an integer, applyTerminalCredentials must return an error
// rather than silently defaulting to UID 0 (root). The pre-fix code
// at agent/main_linux.go:139-140 used `uid, _ := strconv.ParseUint(...)`
// which made parse failure a privilege-escalation vector by malformed
// /etc/passwd content.
func TestApplyTerminalCredentials_NonNumericUIDFailsClosed(t *testing.T) {
	bashCmd := exec.Command("/bin/true")
	termUser := &user.User{
		Uid:      "not-a-number",
		Gid:      "1000",
		Username: "nope",
		HomeDir:  "/home/nope",
	}

	err := applyTerminalCredentials(bashCmd, termUser)
	if err == nil {
		t.Fatal("expected error for non-numeric UID, got nil")
	}
	// SysProcAttr must NOT have been set — the whole point is that we
	// abort BEFORE configuring the process to run as anything.
	if bashCmd.SysProcAttr != nil {
		t.Errorf("SysProcAttr was set despite parse failure: %+v", bashCmd.SysProcAttr)
	}
}

// TestApplyTerminalCredentials_NonNumericGIDFailsClosed mirrors the
// UID test for GID. A valid UID + invalid GID must still abort —
// otherwise a malformed group entry would put us in root's gid (0).
func TestApplyTerminalCredentials_NonNumericGIDFailsClosed(t *testing.T) {
	bashCmd := exec.Command("/bin/true")
	termUser := &user.User{
		Uid:      "1000",
		Gid:      "junk",
		Username: "nope",
		HomeDir:  "/home/nope",
	}

	err := applyTerminalCredentials(bashCmd, termUser)
	if err == nil {
		t.Fatal("expected error for non-numeric GID, got nil")
	}
	if bashCmd.SysProcAttr != nil {
		t.Errorf("SysProcAttr was set despite GID parse failure: %+v", bashCmd.SysProcAttr)
	}
}

// TestApplyTerminalCredentials_ValidUser is the happy-path baseline.
// Without it the fail-closed tests could pass by always returning an
// error, even on valid input.
func TestApplyTerminalCredentials_ValidUser(t *testing.T) {
	bashCmd := exec.Command("/bin/true")
	termUser := &user.User{
		Uid:      "1000",
		Gid:      "1000",
		Username: "ok",
		HomeDir:  "/home/ok",
	}

	if err := applyTerminalCredentials(bashCmd, termUser); err != nil {
		t.Fatalf("unexpected error for valid user: %v", err)
	}
	if bashCmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr was not set for a valid user")
	}
	if bashCmd.SysProcAttr.Credential == nil {
		t.Fatal("Credential was not set for a valid user")
	}
	if got := bashCmd.SysProcAttr.Credential.Uid; got != 1000 {
		t.Errorf("Credential.Uid = %d, want 1000", got)
	}
	if got := bashCmd.SysProcAttr.Credential.Gid; got != 1000 {
		t.Errorf("Credential.Gid = %d, want 1000", got)
	}
	if bashCmd.Dir != "/home/ok" {
		t.Errorf("Dir = %q, want /home/ok", bashCmd.Dir)
	}
}

// TestApplyTerminalCredentials_NilUserFallback covers the existing
// pre-Phase-A behaviour: if no non-root user can be resolved, we fall
// back to running as the current user (which on the agent host means
// root). The helper must accept this without error — it's the right
// behaviour for a misconfigured single-user host where no
// BLOXOS_TERMINAL_USER, "bokiko", or other candidate exists. SysProcAttr
// stays nil so the spawned process inherits the agent's identity.
func TestApplyTerminalCredentials_NilUserFallback(t *testing.T) {
	bashCmd := exec.Command("/bin/true")

	if err := applyTerminalCredentials(bashCmd, nil); err != nil {
		t.Fatalf("nil user must not error (it's the documented fallback): %v", err)
	}
	if bashCmd.SysProcAttr != nil {
		t.Errorf("SysProcAttr was set for nil user: %+v", bashCmd.SysProcAttr)
	}
}

// TestApplyTerminalCredentials_RootUserFallback covers the same
// fallback path when the resolved user happens to be root (UID 0).
// We deliberately skip the credential setup in that case rather than
// setting Credential{Uid:0, Gid:0} — both produce the same effect but
// the no-SysProcAttr form is what the existing code does and what
// callers test against.
func TestApplyTerminalCredentials_RootUserFallback(t *testing.T) {
	bashCmd := exec.Command("/bin/true")
	termUser := &user.User{
		Uid:      "0",
		Gid:      "0",
		Username: "root",
		HomeDir:  "/root",
	}

	if err := applyTerminalCredentials(bashCmd, termUser); err != nil {
		t.Fatalf("root user must not error (existing fallback): %v", err)
	}
	if bashCmd.SysProcAttr != nil {
		t.Errorf("SysProcAttr was set for root fallback: %+v", bashCmd.SysProcAttr)
	}
}
