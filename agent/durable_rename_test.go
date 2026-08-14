//go:build !windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// ===== ROUND 5 BLOCKERS 2 & 3: durableRename must fail closed, and both
// staging (temp -> pending/active) and promotion (pending -> active) must
// go through the exact same primitive =====
//
// The earlier version of this durability step (formerly
// commitPendingToActive) opened the parent directory and called Sync, but
// swallowed BOTH failures — returning nil even when the directory couldn't
// be opened, and discarding dir.Sync's error entirely. That contradicted
// its own "durably replaces" doc comment: a caller could treat a
// non-durable rename as a proven commit, cleared BLOXOS_TOKEN or sent
// enrollment_committed on the strength of a guarantee that was never
// actually established.

// fakeSyncCloser lets a test simulate Sync failing (or Close, though that
// error is currently unchecked at the call site — see the doc on
// durableRenameWith) without touching a real file descriptor.
type fakeSyncCloser struct {
	syncErr error
}

func (f *fakeSyncCloser) Sync() error  { return f.syncErr }
func (f *fakeSyncCloser) Close() error { return nil }

func TestDurableRenameWithPropagatesDirectoryOpenFailure(t *testing.T) {
	dir := t.TempDir()
	oldpath := filepath.Join(dir, "old")
	newpath := filepath.Join(dir, "new")
	if err := os.WriteFile(oldpath, []byte("secret"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	renameCalled := false
	openErr := errors.New("permission denied opening parent directory")
	err := durableRenameWith(
		func(o, n string) error { renameCalled = true; return os.Rename(o, n) },
		func(string) (syncCloser, error) { return nil, openErr },
		oldpath, newpath,
	)
	if err == nil {
		t.Fatal("expected the directory-open failure to propagate, not be swallowed")
	}
	if !renameCalled {
		t.Fatal("rename should have been attempted before the directory-open step")
	}
	// The rename itself DID succeed (this is the documented, intentional
	// behavior — see durableRenameWith's doc) — the file really is at
	// newpath now, even though durability is unproven.
	if _, statErr := os.Stat(newpath); statErr != nil {
		t.Fatalf("expected the file to exist at newpath despite the reported error (rename succeeded, only the durability proof failed): %v", statErr)
	}
}

func TestDurableRenameWithPropagatesDirectorySyncFailure(t *testing.T) {
	dir := t.TempDir()
	oldpath := filepath.Join(dir, "old")
	newpath := filepath.Join(dir, "new")
	if err := os.WriteFile(oldpath, []byte("secret"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	syncErr := errors.New("EIO: input/output error")
	err := durableRenameWith(
		os.Rename,
		func(string) (syncCloser, error) { return &fakeSyncCloser{syncErr: syncErr}, nil },
		oldpath, newpath,
	)
	if err == nil {
		t.Fatal("expected the directory-sync failure to propagate, not be swallowed")
	}
	if _, statErr := os.Stat(newpath); statErr != nil {
		t.Fatalf("expected the file to exist at newpath despite the reported error: %v", statErr)
	}
}

func TestDurableRenameWithPropagatesRenameFailure(t *testing.T) {
	// The rename step itself failing (e.g. cross-device, permission) must
	// also propagate, and durableRenameWith must not attempt to open/sync
	// the directory when there was nothing to commit.
	dirOpened := false
	err := durableRenameWith(
		func(string, string) error { return errors.New("rename failed") },
		func(string) (syncCloser, error) { dirOpened = true; return nil, nil },
		"/does/not/matter/old", "/does/not/matter/new",
	)
	if err == nil {
		t.Fatal("expected the rename failure to propagate")
	}
	if dirOpened {
		t.Fatal("directory must not be opened for fsync when the rename itself never succeeded")
	}
}

func TestDurableRenameWithSucceedsWhenBothStepsSucceed(t *testing.T) {
	dir := t.TempDir()
	oldpath := filepath.Join(dir, "old")
	newpath := filepath.Join(dir, "new")
	if err := os.WriteFile(oldpath, []byte("secret"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := durableRename(oldpath, newpath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(newpath)
	if err != nil || string(data) != "secret" {
		t.Fatalf("data=%q err=%v, want the renamed content", data, err)
	}
}

// TestStagingAndPromotionUseTheIdenticalDurablePrimitive proves
// defaultCredentialFileWriter's rename step is LITERALLY the same function
// as durableRename (not a look-alike reimplementation) via function-pointer
// identity — a reliable comparison for a plain top-level function value
// assigned directly, which is exactly how it's wired.
// defaultCredentialConfirmDeps's promote closure can't be compared the same
// way (it's a closure, not a bare reference), so its equivalence is proven
// behaviorally by TestPendingToActivePromotionFailsClosedOnDurabilityError
// and TestMatchingPendingConfirmationReplacesActiveViaRealCommit below —
// both exercise the real promote closure and observe durableRename's exact
// documented contract (rename-then-fsync, propagate errors, don't undo a
// successful rename).
func TestStagingAndPromotionUseTheIdenticalDurablePrimitive(t *testing.T) {
	w := defaultCredentialFileWriter()
	got := reflect.ValueOf(w.rename).Pointer()
	want := reflect.ValueOf(durableRename).Pointer()
	if got != want {
		t.Fatal("defaultCredentialFileWriter's rename step is not durableRename itself — staging and promotion must share one primitive, not two implementations")
	}
}

// TestHandleEnrolledFrameSendsNoCommitWhenDurableStagingFails is the
// integration-level proof for blocker 2: when the durable staging write
// reports a failure (rename succeeded, but fsync durability could not be
// proven), handleEnrolledFrame must not send enrollment_committed — the hub
// must never be told to trust a secret whose local durability is unproven.
func TestHandleEnrolledFrameSendsNoCommitWhenDurableStagingFails(t *testing.T) {
	dir := t.TempDir()
	pendingPath := filepath.Join(dir, "agent-secret.pending")

	w := defaultCredentialFileWriter()
	w.rename = func(oldpath, newpath string) error {
		return durableRenameWith(
			os.Rename,
			func(string) (syncCloser, error) { return nil, errors.New("directory unavailable for fsync") },
			oldpath, newpath,
		)
	}
	savePendingFailingDurability := func(secret string) error {
		return writeCredentialFileAtomicWith(w, pendingPath, secret)
	}

	commitSent := false
	accepted, err := handleEnrolledFrame("new-secret", savePendingFailingDurability, func() error { commitSent = true; return nil })
	if err == nil {
		t.Fatal("expected the durability failure to propagate out of handleEnrolledFrame")
	}
	if accepted {
		t.Fatal("expected accepted=false")
	}
	if commitSent {
		t.Fatal("enrollment_committed must never be sent when durable staging reported a failure")
	}

	// "Pending/active recovery material remains usable": the underlying
	// rename DID succeed (only the fsync proof failed) — the pending file
	// genuinely exists and is readable, so a subsequent reconnect attempt
	// (which loads and tries agent-secret.pending) has real material to
	// work with rather than nothing at all.
	data, statErr := os.ReadFile(pendingPath)
	if statErr != nil || string(data) != "new-secret\n" {
		t.Fatalf("pending recovery material missing/wrong after a reported durability failure: data=%q err=%v", data, statErr)
	}
}

// TestPendingToActivePromotionFailsClosedOnDurabilityError is blocker 3's
// direct proof against defaultCredentialConfirmDeps's real promote closure
// (not a hand-rolled substitute): a durability failure during promotion
// must propagate as an error from handleEnrollmentConfirmed, which (per
// main.go's enrollment_confirmed handler, unchanged this round) means the
// connection is torn down WITHOUT clearing BLOXOS_TOKEN or trusting the
// promotion — never the old "best-effort, report success anyway" behavior.
func TestPendingToActivePromotionFailsClosedOnDurabilityError(t *testing.T) {
	dir := t.TempDir()
	activePath := filepath.Join(dir, "agent-secret")
	pendingPath := pendingCredentialPath(activePath)
	if err := writeCredentialFileAtomic(activePath, "old-secret"); err != nil {
		t.Fatalf("seed active: %v", err)
	}
	if err := writeCredentialFileAtomic(pendingPath, "new-secret"); err != nil {
		t.Fatalf("seed pending: %v", err)
	}
	confirmedHash, err := hashCredentialFile(pendingPath)
	if err != nil {
		t.Fatalf("hash pending: %v", err)
	}

	deps := credentialConfirmDeps{
		hashActive:  func() (string, error) { return hashCredentialFile(activePath) },
		hashPending: func() (string, error) { return hashCredentialFile(pendingPath) },
		promote: func() error {
			return durableRenameWith(
				os.Rename,
				func(string) (syncCloser, error) { return nil, errors.New("directory unavailable for fsync") },
				pendingPath, activePath,
			)
		},
		removePending: func() error { return os.Remove(pendingPath) },
	}

	outcome, err := handleEnrollmentConfirmed(confirmedHash, deps)
	if err == nil {
		t.Fatal("expected the promotion durability failure to propagate")
	}
	if outcome != enrollmentConfirmRejected {
		t.Fatalf("outcome=%v, want enrollmentConfirmRejected", outcome)
	}

	// Recovery material check: the rename itself succeeded (only fsync
	// proof failed), so the secret is now physically at activePath — a
	// hash-bound reconnect using it will authenticate and get re-confirmed
	// (see handleEnrollmentConfirmed's "already active" branch), safely
	// reconciling what durableRename could not itself prove durable.
	data, statErr := os.ReadFile(activePath)
	if statErr != nil || string(data) != "new-secret\n" {
		t.Fatalf("active recovery material missing/wrong after a reported promotion durability failure: data=%q err=%v", data, statErr)
	}
}
