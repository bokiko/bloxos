//go:build linux

// First-ever test file in agent/. Establishes the test pattern for the
// package. Future Linux-only agent tests anchor here. PTY-touching tests
// must be gated behind BLOXOS_PTY_TEST=1 to keep CI runs portable
// across runner environments that lack /dev/ptmx.

package main

import (
	"errors"
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
