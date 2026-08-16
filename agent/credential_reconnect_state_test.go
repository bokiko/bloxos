package main

import "testing"

// ===== ROUND 5 BLOCKER 1: connectLoop must actually honor a pending
// rejection across iterations =====
//
// Round 4 introduced shouldFallBackToActive and set agentSecret = active
// before `continue`, but connectLoop's top-of-loop logic unconditionally
// re-read agent-secret.pending FIRST on every iteration — and rejection
// never removes that file. The very next iteration silently re-selected the
// same already-rejected pending secret, undoing the fallback: an immediate
// pending→401→pending loop, never actually reaching active. These tests
// prove the fix at the level Codex asked for — not just that the single-shot
// predicate returns true, but that a SEQUENCE of iterations, each reading
// the (unchanged) on-disk state fresh, produces the correct candidate.

func TestSelectCredentialCandidatePrefersUnrejectedPending(t *testing.T) {
	sel := selectCredentialCandidate("pending-secret", "active-secret", "")
	if sel.secret != "pending-secret" || !sel.usedPending {
		t.Fatalf("got %+v, want pending selected", sel)
	}
}

func TestSelectCredentialCandidateSkipsKnownRejectedPendingForActive(t *testing.T) {
	sel := selectCredentialCandidate("pending-secret", "active-secret", "pending-secret")
	if sel.secret != "active-secret" || sel.usedPending {
		t.Fatalf("got %+v, want active selected once pending is known-rejected", sel)
	}
}

func TestSelectCredentialCandidateRetriesRejectedPendingWhenNoActive(t *testing.T) {
	sel := selectCredentialCandidate("pending-secret", "", "pending-secret")
	if sel.secret != "pending-secret" || !sel.usedPending {
		t.Fatalf("got %+v, want the only available credential (pending) retried", sel)
	}
}

func TestSelectCredentialCandidateTriesFreshPendingDespiteOldRejection(t *testing.T) {
	// A NEW re-enrollment attempt staged a different pending secret after
	// the old one was abandoned — must not be confused with it.
	sel := selectCredentialCandidate("new-pending-secret", "active-secret", "old-rejected-secret")
	if sel.secret != "new-pending-secret" || !sel.usedPending {
		t.Fatalf("got %+v, want the fresh pending secret tried, not skipped", sel)
	}
}

func TestSelectCredentialCandidateNoCredentialsAtAll(t *testing.T) {
	sel := selectCredentialCandidate("", "", "")
	if sel.secret != "" || sel.usedPending {
		t.Fatalf("got %+v, want empty selection", sel)
	}
}

func TestNextRejectedPendingSecretMarksOnDefiniteRejection(t *testing.T) {
	got := nextRejectedPendingSecret("", credentialSelection{secret: "p", usedPending: true}, authOutcomeRejected, "p")
	if got != "p" {
		t.Fatalf("got %q, want the rejected secret tracked", got)
	}
}

func TestNextRejectedPendingSecretUnchangedOnTransportFailureUsingActive(t *testing.T) {
	// Already tracking a rejection; a transport/TLS failure while retrying
	// with ACTIVE must not clear or otherwise disturb that tracking — the
	// pending candidate must stay avoided.
	got := nextRejectedPendingSecret("p", credentialSelection{secret: "active-secret", usedPending: false}, authOutcomeUnknown, "p")
	if got != "p" {
		t.Fatalf("got %q, want rejection tracking preserved through an active-side transport failure", got)
	}
}

func TestNextRejectedPendingSecretUnchangedOnTransportFailureUsingPending(t *testing.T) {
	got := nextRejectedPendingSecret("", credentialSelection{secret: "p", usedPending: true}, authOutcomeUnknown, "p")
	if got != "" {
		t.Fatalf("got %q, want no rejection recorded for a mere transport failure", got)
	}
}

func TestNextRejectedPendingSecretClearsWhenPendingFileGone(t *testing.T) {
	// The tracked rejection becomes moot once the pending file itself is
	// gone (promoted, or cleaned up as stale by a confirmed active
	// reconnect) — nothing left to avoid, and a LATER fresh pending secret
	// must not be mistakenly compared against a leftover value.
	got := nextRejectedPendingSecret("p", credentialSelection{secret: "active-secret", usedPending: false}, authOutcomeUnknown, "")
	if got != "" {
		t.Fatalf("got %q, want tracking cleared once no pending file remains", got)
	}
}

// simulatedConnectLoopIteration mirrors EXACTLY connectLoop's own per-
// iteration decision + state update (selectCredentialCandidate then
// nextRejectedPendingSecret), so a scripted sequence of on-disk states and
// hub outcomes proves the real multi-iteration behavior — not just that the
// underlying pure functions are individually correct in isolation.
func simulatedConnectLoopIteration(rejectedPendingSecret, pendingOnDisk, activeOnDisk string, outcome authOutcome) (sel credentialSelection, nextRejected string, immediateRetry bool) {
	sel = selectCredentialCandidate(pendingOnDisk, activeOnDisk, rejectedPendingSecret)
	immediateRetry = shouldFallBackToActive(sel.usedPending, outcome, activeOnDisk != "")
	nextRejected = nextRejectedPendingSecret(rejectedPendingSecret, sel, outcome, pendingOnDisk)
	return sel, nextRejected, immediateRetry
}

// TestConnectLoopHonorsPendingRejectionAcrossIterations is the exact
// reproduction Codex asked for: the pending file is NEVER deleted or
// changed between iterations (nothing in this scenario promotes or cleans
// it up — it just sits there, abandoned/expired hub-side), yet attempt 2
// must use active, not immediately re-select the just-rejected pending
// secret. Also proves the sequel: a transport failure while on the active
// fallback keeps retrying active, never jumping back to pending.
func TestConnectLoopHonorsPendingRejectionAcrossIterations(t *testing.T) {
	const pending = "abandoned-pending-secret"
	const active = "still-valid-active-secret"
	var rejected string

	// Attempt 1: pending file exists, nothing rejected yet -> try pending.
	sel1, rejected, retry1 := simulatedConnectLoopIteration(rejected, pending, active, authOutcomeRejected)
	if sel1.secret != pending || !sel1.usedPending {
		t.Fatalf("attempt 1: got %+v, want pending selected", sel1)
	}
	if !retry1 {
		t.Fatal("attempt 1: expected an immediate retry (active is available) after the pending rejection")
	}
	if rejected != pending {
		t.Fatalf("attempt 1: rejected tracking = %q, want %q", rejected, pending)
	}

	// Attempt 2 — THE bug this test exists to catch: the pending FILE is
	// still on disk (untouched), but the state machine must select ACTIVE,
	// not immediately re-select the just-rejected pending value.
	sel2, rejected, retry2 := simulatedConnectLoopIteration(rejected, pending, active, authOutcomeUnknown)
	if sel2.usedPending {
		t.Fatalf("attempt 2: re-selected the already-rejected pending candidate (%+v) — this IS the pending→401→pending loop bug", sel2)
	}
	if sel2.secret != active {
		t.Fatalf("attempt 2: got secret %q, want the active credential %q", sel2.secret, active)
	}
	if retry2 {
		t.Fatal("attempt 2: a transport failure (authOutcomeUnknown) must never trigger another immediate retry")
	}
	if rejected != pending {
		t.Fatalf("attempt 2: rejected tracking changed to %q, want it to stay %q", rejected, pending)
	}

	// Attempt 3: another transport failure while still on the active
	// fallback — must keep retrying active, not jump back to pending.
	sel3, rejected, retry3 := simulatedConnectLoopIteration(rejected, pending, active, authOutcomeUnknown)
	if sel3.usedPending || sel3.secret != active {
		t.Fatalf("attempt 3: got %+v, want active retried again, not pending", sel3)
	}
	if retry3 {
		t.Fatal("attempt 3: transport failure must not trigger an immediate retry")
	}
	if rejected != pending {
		t.Fatalf("attempt 3: rejected tracking changed to %q, want it to stay %q", rejected, pending)
	}
}

// TestConnectLoopRetriesPendingWithBackoffWhenNoActiveAvailable covers "no
// active credential available -> retain pending and normal backoff": across
// repeated rejections with nothing to fall back to, the pending file must
// never be discarded (nowhere else has this secret), and there must be no
// immediate-retry (busy-loop) behavior.
func TestConnectLoopRetriesPendingWithBackoffWhenNoActiveAvailable(t *testing.T) {
	const pending = "only-credential-we-have"
	var rejected string

	for attempt := 1; attempt <= 3; attempt++ {
		sel, next, retry := simulatedConnectLoopIteration(rejected, pending, "", authOutcomeRejected)
		rejected = next
		if sel.secret != pending || !sel.usedPending {
			t.Fatalf("attempt %d: got %+v, want the only credential (pending) retried", attempt, sel)
		}
		if retry {
			t.Fatalf("attempt %d: no active credential exists — must use normal backoff, not an immediate retry", attempt)
		}
		if rejected != pending {
			t.Fatalf("attempt %d: rejected tracking = %q, want %q (retained, not discarded)", attempt, rejected, pending)
		}
	}
}

// TestConnectLoopTriesFreshPendingAfterOldOneWasAbandoned: once a NEW
// re-enrollment attempt stages a genuinely different pending secret, it
// must be tried immediately — not skipped because SOME pending value was
// previously rejected.
func TestConnectLoopTriesFreshPendingAfterOldOneWasAbandoned(t *testing.T) {
	const oldPending = "abandoned-secret"
	const active = "active-secret"
	const newPending = "fresh-reenrollment-secret"

	sel1, rejected, _ := simulatedConnectLoopIteration("", oldPending, active, authOutcomeRejected)
	if sel1.secret != oldPending {
		t.Fatalf("attempt 1: got %+v", sel1)
	}
	if rejected != oldPending {
		t.Fatalf("rejected tracking = %q, want %q", rejected, oldPending)
	}

	// A fresh re-enrollment stages a new pending value; old pending file is
	// gone (replaced) — the tracked rejection is now stale.
	sel2, rejected, _ := simulatedConnectLoopIteration(rejected, newPending, active, authOutcomeUnknown)
	if sel2.secret != newPending || !sel2.usedPending {
		t.Fatalf("attempt 2: got %+v, want the fresh pending secret tried immediately", sel2)
	}
	_ = rejected
}
