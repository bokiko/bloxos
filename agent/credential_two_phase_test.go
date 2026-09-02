package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// ===== BLOCKER 2 (round 4): real local two-phase credential handoff =====
//
// Before this fix, "enrolled" wrote straight to the active credential file:
// if the hub's later "enrollment_confirmed" was lost, delayed, or the
// promotion failed/expired hub-side, the local active file had already been
// overwritten while the hub still considered the OLD secret active — a
// split-brain the agent could not recover from on its own (the new local
// secret was unpromoted/expired, and the old, hub-valid secret was gone).
// These tests exercise the replacement design: "enrolled" writes only a
// PENDING file, and only a hash-bound "enrollment_confirmed" (proven via
// handleEnrollmentConfirmed) may ever promote it into the active file.

func TestPendingCredentialPathAppendsSuffix(t *testing.T) {
	got := pendingCredentialPath("/etc/bloxos/agent-secret")
	want := "/etc/bloxos/agent-secret.pending"
	if got != want {
		t.Fatalf("pendingCredentialPath = %q, want %q", got, want)
	}
}

func TestHashCredentialFileWithMissingFileReturnsEmpty(t *testing.T) {
	hash, err := hashCredentialFileWith(os.ReadFile, filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash != "" {
		t.Fatalf("hash=%q, want empty for a missing file", hash)
	}
}

func TestHashCredentialFileWithExistingFileReturnsHashOfTrimmedContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-secret")
	if err := writeCredentialFileAtomic(path, "the-secret"); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := hashCredentialFile(path)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	want := hashOfForTest("the-secret")
	if got != want {
		t.Fatalf("hash=%q, want %q", got, want)
	}
}

// hashOfForTest mirrors hashCredentialFile's own hashing (sha256 hex of the
// trimmed secret) so tests can compute an expected value without depending
// on any hub-side helper.
func hashOfForTest(secret string) string {
	h, err := hashCredentialFileWith(func(string) ([]byte, error) {
		return []byte(secret + "\n"), nil
	}, "unused")
	if err != nil {
		panic(err)
	}
	return h
}

func TestHashCredentialFileWithPropagatesReadErrorForExistingFile(t *testing.T) {
	sentinel := errors.New("permission denied")
	_, err := hashCredentialFileWith(func(string) ([]byte, error) {
		return nil, sentinel
	}, "some-path")
	if err == nil {
		t.Fatal("expected the read error to propagate, not be folded into \"absent\"")
	}
}

// spyConfirmDeps wraps credentialConfirmDeps with call-order tracking and
// configurable failures, so handleEnrollmentConfirmed's state machine can be
// tested without touching real disk except where a test explicitly wants to
// (see the real-file variants below).
type spyConfirmDeps struct {
	order []string

	activeHash, pendingHash string
	activeErr, pendingErr   error
	promoteErr, removeErr   error
}

func (s *spyConfirmDeps) deps() credentialConfirmDeps {
	return credentialConfirmDeps{
		hashActive:  func() (string, error) { s.order = append(s.order, "hashActive"); return s.activeHash, s.activeErr },
		hashPending: func() (string, error) { s.order = append(s.order, "hashPending"); return s.pendingHash, s.pendingErr },
		promote:     func() error { s.order = append(s.order, "promote"); return s.promoteErr },
		removePending: func() error {
			s.order = append(s.order, "removePending")
			return s.removeErr
		},
	}
}

func TestHandleEnrollmentConfirmedRejectsEmptyHash(t *testing.T) {
	s := &spyConfirmDeps{activeHash: "active-hash", pendingHash: "pending-hash"}
	outcome, err := handleEnrollmentConfirmed("", s.deps())
	if err == nil {
		t.Fatal("expected an error for an empty confirmed hash")
	}
	if outcome != enrollmentConfirmRejected {
		t.Fatalf("outcome=%v, want enrollmentConfirmRejected", outcome)
	}
	if len(s.order) != 0 {
		t.Fatalf("no filesystem operation should run for an empty hash, got %v", s.order)
	}
}

func TestHandleEnrollmentConfirmedPromotesMatchingPending(t *testing.T) {
	s := &spyConfirmDeps{activeHash: "old-active-hash", pendingHash: "new-hash"}
	outcome, err := handleEnrollmentConfirmed("new-hash", s.deps())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome != enrollmentConfirmPromoted {
		t.Fatalf("outcome=%v, want enrollmentConfirmPromoted", outcome)
	}
	// promote (the commit point / rename) must run, and — since a rename IS
	// the removal of the pending file — removePending must NOT be called
	// separately in this branch.
	found := false
	for _, op := range s.order {
		if op == "removePending" {
			t.Fatal("removePending must not be called on the promote path — the rename already consumes the pending file")
		}
		if op == "promote" {
			found = true
		}
	}
	if !found {
		t.Fatal("promote was never called")
	}
}

func TestHandleEnrollmentConfirmedPromotesEvenWithNoPriorActiveFile(t *testing.T) {
	// Covers "clean enrollment works when no active file exists": a brand
	// new machine has no local active file at all when its first
	// confirmation arrives — the pending-hash match alone must be enough.
	s := &spyConfirmDeps{activeHash: "", pendingHash: "fresh-hash"}
	outcome, err := handleEnrollmentConfirmed("fresh-hash", s.deps())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome != enrollmentConfirmPromoted {
		t.Fatalf("outcome=%v, want enrollmentConfirmPromoted", outcome)
	}
}

func TestHandleEnrollmentConfirmedAlreadyActiveClearsStalePending(t *testing.T) {
	s := &spyConfirmDeps{activeHash: "active-hash", pendingHash: "some-other-stale-hash"}
	outcome, err := handleEnrollmentConfirmed("active-hash", s.deps())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome != enrollmentConfirmAlreadyActive {
		t.Fatalf("outcome=%v, want enrollmentConfirmAlreadyActive", outcome)
	}
	sawRemove, sawPromote := false, false
	for _, op := range s.order {
		if op == "removePending" {
			sawRemove = true
		}
		if op == "promote" {
			sawPromote = true
		}
	}
	if !sawRemove {
		t.Fatal("a stale, non-matching pending file must be cleaned up once active is confirmed authoritative")
	}
	if sawPromote {
		t.Fatal("promote must never run when the confirmation already matches the active file")
	}
}

func TestHandleEnrollmentConfirmedAlreadyActiveNoOpWhenNoPendingFile(t *testing.T) {
	s := &spyConfirmDeps{activeHash: "active-hash", pendingHash: ""}
	outcome, err := handleEnrollmentConfirmed("active-hash", s.deps())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome != enrollmentConfirmAlreadyActive {
		t.Fatalf("outcome=%v, want enrollmentConfirmAlreadyActive", outcome)
	}
	for _, op := range s.order {
		if op == "removePending" {
			t.Fatal("removePending must not run when there is no pending file to remove")
		}
	}
}

func TestHandleEnrollmentConfirmedMismatchChangesNothing(t *testing.T) {
	s := &spyConfirmDeps{activeHash: "active-hash", pendingHash: "pending-hash"}
	outcome, err := handleEnrollmentConfirmed("some-unrelated-hash", s.deps())
	if err == nil {
		t.Fatal("expected an error when the confirmed hash matches neither file")
	}
	if outcome != enrollmentConfirmRejected {
		t.Fatalf("outcome=%v, want enrollmentConfirmRejected", outcome)
	}
	for _, op := range s.order {
		if op == "promote" || op == "removePending" {
			t.Fatalf("a mismatched confirmation must not mutate anything, but ran %q", op)
		}
	}
}

func TestHandleEnrollmentConfirmedPromotionFailurePreservesRecoveryMaterial(t *testing.T) {
	s := &spyConfirmDeps{activeHash: "old-active-hash", pendingHash: "new-hash", promoteErr: errors.New("rename failed: disk full")}
	outcome, err := handleEnrollmentConfirmed("new-hash", s.deps())
	if err == nil {
		t.Fatal("expected the promote error to propagate")
	}
	if outcome != enrollmentConfirmRejected {
		t.Fatalf("outcome=%v, want enrollmentConfirmRejected", outcome)
	}
	for _, op := range s.order {
		if op == "removePending" {
			t.Fatal("a failed promotion must not remove the pending file — it is the only remaining recovery material")
		}
	}
}

func TestHandleEnrollmentConfirmedPropagatesHashReadErrors(t *testing.T) {
	s := &spyConfirmDeps{pendingErr: errors.New("read failed")}
	if _, err := handleEnrollmentConfirmed("whatever", s.deps()); err == nil {
		t.Fatal("expected a pending-hash read error to propagate")
	}

	s2 := &spyConfirmDeps{pendingHash: "no-match", activeErr: errors.New("read failed")}
	if _, err := handleEnrollmentConfirmed("whatever", s2.deps()); err == nil {
		t.Fatal("expected an active-hash read error to propagate")
	}
}

// ===== Real-filesystem end-to-end: enrolled -> pending file only, then
// hash-bound confirmation -> real durable promote via durableRename.

func TestEnrolledFrameWritesPendingFileNeverActive(t *testing.T) {
	dir := t.TempDir()
	activePath := filepath.Join(dir, "agent-secret")
	pendingPath := pendingCredentialPath(activePath)

	savePending := func(secret string) error { return writeCredentialFileAtomic(pendingPath, secret) }
	committed := false
	accepted, err := handleEnrolledFrame("brand-new-secret", savePending, func() error { committed = true; return nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !accepted || !committed {
		t.Fatalf("accepted=%v committed=%v, want both true", accepted, committed)
	}

	if _, err := os.Stat(activePath); err == nil {
		t.Fatal("the active credential file must not exist merely from receiving \"enrolled\"")
	}
	data, err := os.ReadFile(pendingPath)
	if err != nil {
		t.Fatalf("pending file was not written: %v", err)
	}
	if got := string(data); got != "brand-new-secret\n" {
		t.Fatalf("pending file content=%q, want the staged secret", got)
	}
}

// TestFullTwoPhaseHandoffChainOnRealFiles is the end-to-end proof of "stage
// -> local pending write -> hash-bound confirm -> local active replacement"
// entirely from the agent's side of the protocol, using only real files (the
// hub's own half — the CAS promotion in promotePendingCredentialCAS — is
// proven independently by the hub's own test suite; what's proven here is
// that the agent's local file state machine, wired together exactly as
// main.go wires it, ends up correct).
func TestFullTwoPhaseHandoffChainOnRealFiles(t *testing.T) {
	dir := t.TempDir()
	activePath := filepath.Join(dir, "agent-secret")
	pendingPath := pendingCredentialPath(activePath)
	if err := writeCredentialFileAtomic(activePath, "old-secret"); err != nil {
		t.Fatalf("seed old active: %v", err)
	}

	// Step 1: "enrolled" arrives — staged to pending only.
	savePending := func(secret string) error { return writeCredentialFileAtomic(pendingPath, secret) }
	committedSent := false
	accepted, err := handleEnrolledFrame("new-secret", savePending, func() error { committedSent = true; return nil })
	if err != nil || !accepted || !committedSent {
		t.Fatalf("handleEnrolledFrame: accepted=%v committed=%v err=%v", accepted, committedSent, err)
	}
	if data, _ := os.ReadFile(activePath); string(data) != "old-secret\n" {
		t.Fatal("active file must still hold the OLD secret after staging alone")
	}

	// Step 2: hub confirms with the hash of the secret it promoted — which,
	// on the hub side, is exactly the hash of what was staged (same value
	// this agent just wrote to pendingPath).
	confirmedHash, err := hashCredentialFile(pendingPath)
	if err != nil {
		t.Fatalf("hash pending: %v", err)
	}
	deps := credentialConfirmDeps{
		hashActive:    func() (string, error) { return hashCredentialFile(activePath) },
		hashPending:   func() (string, error) { return hashCredentialFile(pendingPath) },
		promote:       func() error { return durableRename(pendingPath, activePath) },
		removePending: func() error { return os.Remove(pendingPath) },
	}
	outcome, err := handleEnrollmentConfirmed(confirmedHash, deps)
	if err != nil {
		t.Fatalf("handleEnrollmentConfirmed: %v", err)
	}
	if outcome != enrollmentConfirmPromoted {
		t.Fatalf("outcome=%v, want enrollmentConfirmPromoted", outcome)
	}

	// Step 3: local active replacement — the ONLY point at which it changed.
	activeData, err := os.ReadFile(activePath)
	if err != nil || string(activeData) != "new-secret\n" {
		t.Fatalf("active=%q err=%v, want the newly promoted secret", activeData, err)
	}
	if _, err := os.Stat(pendingPath); err == nil {
		t.Fatal("no .pending residue expected after a normal successful handoff")
	}
}

func TestFailedPendingWriteSendsNoCommit(t *testing.T) {
	sendAttempted := false
	savePending := func(string) error { return errors.New("disk full") }
	accepted, err := handleEnrolledFrame("secret", savePending, func() error { sendAttempted = true; return nil })
	if err == nil {
		t.Fatal("expected the save error to propagate")
	}
	if accepted {
		t.Fatal("expected accepted=false")
	}
	if sendAttempted {
		t.Fatal("enrollment_committed must never be sent when the pending write itself failed")
	}
}

func TestFailedCommitSendLeavesActiveAndPendingIntact(t *testing.T) {
	dir := t.TempDir()
	activePath := filepath.Join(dir, "agent-secret")
	pendingPath := pendingCredentialPath(activePath)
	if err := writeCredentialFileAtomic(activePath, "old-active-secret"); err != nil {
		t.Fatalf("seed active: %v", err)
	}

	savePending := func(secret string) error { return writeCredentialFileAtomic(pendingPath, secret) }
	accepted, err := handleEnrolledFrame("new-secret", savePending, func() error { return errors.New("connection reset") })
	if err == nil {
		t.Fatal("expected the send error to propagate")
	}
	if accepted {
		t.Fatal("expected accepted=false when the commit send fails")
	}

	activeData, aerr := os.ReadFile(activePath)
	if aerr != nil || string(activeData) != "old-active-secret\n" {
		t.Fatalf("active file changed despite a failed commit send: data=%q err=%v", activeData, aerr)
	}
	pendingData, perr := os.ReadFile(pendingPath)
	if perr != nil || string(pendingData) != "new-secret\n" {
		t.Fatalf("pending file was not left intact for a later retry: data=%q err=%v", pendingData, perr)
	}
}

func TestMatchingPendingConfirmationReplacesActiveViaRealCommit(t *testing.T) {
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
		hashActive:    func() (string, error) { return hashCredentialFile(activePath) },
		hashPending:   func() (string, error) { return hashCredentialFile(pendingPath) },
		promote:       func() error { return durableRename(pendingPath, activePath) },
		removePending: func() error { return os.Remove(pendingPath) },
	}

	outcome, err := handleEnrollmentConfirmed(confirmedHash, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome != enrollmentConfirmPromoted {
		t.Fatalf("outcome=%v, want enrollmentConfirmPromoted", outcome)
	}

	activeData, err := os.ReadFile(activePath)
	if err != nil {
		t.Fatalf("read active after promotion: %v", err)
	}
	if string(activeData) != "new-secret\n" {
		t.Fatalf("active content=%q, want the promoted secret", activeData)
	}
	if _, err := os.Stat(pendingPath); err == nil {
		t.Fatal("pending file must be gone after a real promote (the rename IS the removal)")
	}
}

func TestPendingToActiveReplacementFailurePreservesBothFilesOnRealDisk(t *testing.T) {
	dir := t.TempDir()
	activePath := filepath.Join(dir, "agent-secret")
	pendingPath := pendingCredentialPath(activePath)
	if err := writeCredentialFileAtomic(activePath, "old-secret"); err != nil {
		t.Fatalf("seed active: %v", err)
	}
	if err := writeCredentialFileAtomic(pendingPath, "new-secret"); err != nil {
		t.Fatalf("seed pending: %v", err)
	}
	confirmedHash, _ := hashCredentialFile(pendingPath)

	deps := credentialConfirmDeps{
		hashActive:  func() (string, error) { return hashCredentialFile(activePath) },
		hashPending: func() (string, error) { return hashCredentialFile(pendingPath) },
		// Simulate a commit-point failure without touching real disk.
		promote:       func() error { return errors.New("simulated durability failure") },
		removePending: func() error { return os.Remove(pendingPath) },
	}

	if _, err := handleEnrollmentConfirmed(confirmedHash, deps); err == nil {
		t.Fatal("expected the simulated promotion failure to propagate")
	}

	activeData, err := os.ReadFile(activePath)
	if err != nil || string(activeData) != "old-secret\n" {
		t.Fatalf("active file must be untouched on a failed promotion: data=%q err=%v", activeData, err)
	}
	pendingData, err := os.ReadFile(pendingPath)
	if err != nil || string(pendingData) != "new-secret\n" {
		t.Fatalf("pending file must survive a failed promotion for retry: data=%q err=%v", pendingData, err)
	}
}

// ===== Reconnect-recovery decision logic (pure, deterministic) =====

func TestShouldFallBackToActive(t *testing.T) {
	for _, tc := range []struct {
		name        string
		usedPending bool
		outcome     authOutcome
		haveActive  bool
		want        bool
	}{
		{"pending rejected, active available: fall back", true, authOutcomeRejected, true, true},
		{"pending rejected, no active to fall back to", true, authOutcomeRejected, false, false},
		{"pending attempt hit a transport/TLS failure: no fallback", true, authOutcomeUnknown, true, false},
		{"active credential itself was rejected: no fallback loop", false, authOutcomeRejected, true, false},
		{"active credential, transport failure: no fallback", false, authOutcomeUnknown, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldFallBackToActive(tc.usedPending, tc.outcome, tc.haveActive)
			if got != tc.want {
				t.Fatalf("shouldFallBackToActive(%v, %v, %v) = %v, want %v", tc.usedPending, tc.outcome, tc.haveActive, got, tc.want)
			}
		})
	}
}

func TestClassifyDialAuthOutcome(t *testing.T) {
	for _, tc := range []struct {
		name string
		resp *http.Response
		err  error
		want authOutcome
	}{
		{"401 with an error is a definitive rejection", &http.Response{StatusCode: http.StatusUnauthorized}, errors.New("bad handshake"), authOutcomeRejected},
		{"403 is not treated as a credential rejection", &http.Response{StatusCode: http.StatusForbidden}, errors.New("bad handshake"), authOutcomeUnknown},
		{"nil response (transport/TLS failure) is never a rejection", nil, errors.New("connection refused"), authOutcomeUnknown},
		{"no error at all is never a rejection regardless of resp", &http.Response{StatusCode: http.StatusUnauthorized}, nil, authOutcomeUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyDialAuthOutcome(tc.resp, tc.err)
			if got != tc.want {
				t.Fatalf("classifyDialAuthOutcome() = %v, want %v", got, tc.want)
			}
		})
	}
}
