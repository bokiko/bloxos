package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// readFrameType drains conn until it sees a frame whose "type" field equals
// want, or the deadline set by the caller expires.
func readFrameType(t *testing.T, conn *websocket.Conn, want string) {
	t.Helper()
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for %q frame: %v", want, err)
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(msg, &envelope) == nil && envelope.Type == want {
			return
		}
	}
}

// assertNoFrameType fails if a frame of type unwanted arrives before the
// connection's already-set read deadline expires; a clean read timeout or
// disconnect is the expected/passing outcome.
func assertNoFrameType(t *testing.T, conn *websocket.Conn, unwanted string) {
	t.Helper()
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return // timeout or close — exactly what we want
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(msg, &envelope) == nil && envelope.Type == unwanted {
			t.Fatalf("unexpected %q frame: %s", unwanted, msg)
		}
	}
}

// ===== BLOCKER 1: deterministic (non-goroutine) proof of the real CAS =====

// TestStaleAcknowledgementCannotClobberNewerReStagedPending is the
// deterministic reproduction Codex asked for, not a goroutine race: stage A,
// capture what a connection holding A's ack would present, re-stage B
// (simulating a second re-enrollment attempt prepared before A's ack
// arrives), deliver the stale A acknowledgement, and prove active is
// unchanged and pending is still B. Then acknowledge B and prove only B
// promotes.
func TestStaleAcknowledgementCannotClobberNewerReStagedPending(t *testing.T) {
	_, s := setupTestServer(t)
	s.seedWindowsMachine(t, "win-stale-ack")
	if _, err := s.db.Exec(`INSERT INTO agent_credentials (machine_id, secret_hash) VALUES (?, ?)`, "win-stale-ack", "orig-active-hash"); err != nil {
		t.Fatalf("seed credential: %v", err)
	}

	// Stage A.
	tokenAHash := hashOf("stale-ack-token-a")
	if _, err := s.db.Exec(
		`INSERT INTO tokens (token_hash, expires_at, used, target_machine_id) VALUES (?, ?, FALSE, ?)`,
		tokenAHash, time.Now().Add(15*time.Minute).Format(time.RFC3339), "win-stale-ack",
	); err != nil {
		t.Fatalf("seed token A: %v", err)
	}
	if err := s.stageTargetedCredential(tokenAHash, "win-stale-ack", "pending-hash-A"); err != nil {
		t.Fatalf("stage A: %v", err)
	}

	// Re-stage B before A's acknowledgement is delivered — the exact
	// scenario a stale ack must not be allowed to clobber.
	tokenBHash := hashOf("stale-ack-token-b")
	if _, err := s.db.Exec(
		`INSERT INTO tokens (token_hash, expires_at, used, target_machine_id) VALUES (?, ?, FALSE, ?)`,
		tokenBHash, time.Now().Add(15*time.Minute).Format(time.RFC3339), "win-stale-ack",
	); err != nil {
		t.Fatalf("seed token B: %v", err)
	}
	if err := s.stageTargetedCredential(tokenBHash, "win-stale-ack", "pending-hash-B"); err != nil {
		t.Fatalf("stage B: %v", err)
	}

	// Deliver the stale A acknowledgement.
	promotedA, err := s.promotePendingCredentialCAS("win-stale-ack", "pending-hash-A")
	if err != nil {
		t.Fatalf("promote A: %v", err)
	}
	if promotedA {
		t.Fatal("stale acknowledgement A must not promote once B has been re-staged")
	}

	// Active untouched, pending is still B (not cleared, not A).
	if got := activeSecretHash(t, s, "win-stale-ack"); got != "orig-active-hash" {
		t.Fatalf("active secret_hash=%q after a stale ack, want unchanged %q", got, "orig-active-hash")
	}
	pendingHash, _ := pendingState(t, s, "win-stale-ack")
	if pendingHash != "pending-hash-B" {
		t.Fatalf("pending_secret_hash=%q after stale ack A, want B's hash still staged: %q", pendingHash, "pending-hash-B")
	}

	// Now acknowledge B — only B may promote.
	promotedB, err := s.promotePendingCredentialCAS("win-stale-ack", "pending-hash-B")
	if err != nil {
		t.Fatalf("promote B: %v", err)
	}
	if !promotedB {
		t.Fatal("B's acknowledgement should promote — it is the current pending value")
	}
	if got := activeSecretHash(t, s, "win-stale-ack"); got != "pending-hash-B" {
		t.Fatalf("active secret_hash=%q after promoting B, want %q", got, "pending-hash-B")
	}
}

// ===== BLOCKER 2: fail-closed on malformed/missing/exactly-expired expiry =====

func seedRawPending(t *testing.T, s *Server, machineID, hash string, expiresAt interface{}) {
	t.Helper()
	if _, err := s.db.Exec(
		`UPDATE agent_credentials SET pending_secret_hash = ?, pending_expires_at = ? WHERE machine_id = ?`,
		hash, expiresAt, machineID,
	); err != nil {
		t.Fatalf("seed raw pending state: %v", err)
	}
}

func TestPendingWithNullExpiryNeverPromotesOrAuthenticates(t *testing.T) {
	_, s := setupTestServer(t)
	s.seedWindowsMachine(t, "win-null-exp")
	if _, err := s.db.Exec(`INSERT INTO agent_credentials (machine_id, secret_hash) VALUES (?, ?)`, "win-null-exp", "active-hash"); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	seedRawPending(t, s, "win-null-exp", "pending-hash-null", nil)

	promoted, err := s.promotePendingCredentialCAS("win-null-exp", "pending-hash-null")
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted {
		t.Fatal("a NULL pending_expires_at must never promote")
	}
	if got := activeSecretHash(t, s, "win-null-exp"); got != "active-hash" {
		t.Fatalf("active secret_hash=%q, want unchanged", got)
	}
	pendingHash, _ := pendingState(t, s, "win-null-exp")
	if pendingHash != "" {
		t.Fatalf("malformed pending state was not cleared: hash=%q", pendingHash)
	}
}

func TestPendingWithMalformedExpiryNeverPromotesOrAuthenticates(t *testing.T) {
	_, s := setupTestServer(t)
	s.seedWindowsMachine(t, "win-bad-exp")
	if _, err := s.db.Exec(`INSERT INTO agent_credentials (machine_id, secret_hash) VALUES (?, ?)`, "win-bad-exp", "active-hash"); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	seedRawPending(t, s, "win-bad-exp", "pending-hash-bad", "not-a-valid-timestamp")

	promoted, err := s.promotePendingCredentialCAS("win-bad-exp", "pending-hash-bad")
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted {
		t.Fatal("an unparseable pending_expires_at must never promote")
	}
	if got := activeSecretHash(t, s, "win-bad-exp"); got != "active-hash" {
		t.Fatalf("active secret_hash=%q, want unchanged", got)
	}
	pendingHash, _ := pendingState(t, s, "win-bad-exp")
	if pendingHash != "" {
		t.Fatalf("malformed pending state was not cleared: hash=%q", pendingHash)
	}
}

func TestPendingExactlyAtExpiryNeverPromotesOldActiveRemainsValid(t *testing.T) {
	_, s := setupTestServer(t)
	s.seedWindowsMachine(t, "win-exact-exp")
	if _, err := s.db.Exec(`INSERT INTO agent_credentials (machine_id, secret_hash) VALUES (?, ?)`, "win-exact-exp", "active-hash"); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	// "Exactly" expired: the boundary itself and anything not strictly in the
	// future must reject. time.Now() formatted now will already be <= the
	// check's later time.Now() by the time promotePendingCredentialCAS runs.
	seedRawPending(t, s, "win-exact-exp", "pending-hash-exact", time.Now().Format(time.RFC3339))

	promoted, err := s.promotePendingCredentialCAS("win-exact-exp", "pending-hash-exact")
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted {
		t.Fatal("an expiry that is not strictly in the future must never promote")
	}
	if got := activeSecretHash(t, s, "win-exact-exp"); got != "active-hash" {
		t.Fatalf("active secret_hash=%q, want unchanged", got)
	}
}

func TestMalformedPendingDoesNotClobberConcurrentlyReStagedValue(t *testing.T) {
	// The malformed-expiry clear must itself be CAS-scoped: if a valid
	// re-stage happened between the read and the clear attempt, the clear
	// must not touch it.
	_, s := setupTestServer(t)
	s.seedWindowsMachine(t, "win-malformed-race")
	if _, err := s.db.Exec(`INSERT INTO agent_credentials (machine_id, secret_hash) VALUES (?, ?)`, "win-malformed-race", "active-hash"); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	// Attempt to promote a hash that was never actually staged (simulating
	// "read a malformed snapshot, but reality has since moved on").
	seedRawPending(t, s, "win-malformed-race", "real-pending-hash", time.Now().Add(time.Minute).Format(time.RFC3339))

	promoted, err := s.promotePendingCredentialCAS("win-malformed-race", "some-other-stale-hash")
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted {
		t.Fatal("a non-matching hash must never promote")
	}
	// The real, valid pending value must be untouched.
	pendingHash, pendingExp := pendingState(t, s, "win-malformed-race")
	if pendingHash != "real-pending-hash" || pendingExp == "" {
		t.Fatalf("valid pending state was clobbered by an unrelated promotion attempt: hash=%q expires=%q", pendingHash, pendingExp)
	}
}

// TestDBClockExpiryGuardDefeatsStaleGoSideCheck is the deterministic
// TOCTOU reproduction: it constructs exactly the race blocker 2 describes —
// the Go-side pre-filter says "not expired yet" (via an injected clock that
// lies about the current time), while the REAL database clock, which the
// promoting UPDATE's julianday predicate checks independently, has already
// moved past the deadline. If the UPDATE only re-checked the Go-computed
// verdict (or nothing at all), this would incorrectly promote; the DB-time
// predicate must reject it regardless of what the injected clock claimed.
func TestDBClockExpiryGuardDefeatsStaleGoSideCheck(t *testing.T) {
	_, s := setupTestServer(t)
	s.seedWindowsMachine(t, "win-toctou")
	if _, err := s.db.Exec(`INSERT INTO agent_credentials (machine_id, secret_hash) VALUES (?, ?)`, "win-toctou", "active-hash"); err != nil {
		t.Fatalf("seed credential: %v", err)
	}

	// A real, already-past expiry — the actual database clock (and an
	// honest Go clock) both agree this is expired right now.
	realExpiry := time.Now().Add(-2 * time.Second)
	seedRawPending(t, s, "win-toctou", "pending-hash-toctou", realExpiry.Format(time.RFC3339))

	// The injected clock claims "now" is well BEFORE that expiry — exactly
	// what a stale/delayed Go-side check would see if real time raced past
	// the deadline between the parse/compare and the UPDATE executing.
	lyingNow := func() time.Time { return realExpiry.Add(-time.Hour) }

	promoted, err := s.promotePendingCredentialCASAt("win-toctou", "pending-hash-toctou", lyingNow)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted {
		t.Fatal("promotion succeeded despite the real DB clock being past the recorded expiry — the Go-side pre-filter alone is not a sufficient guard")
	}
	if got := activeSecretHash(t, s, "win-toctou"); got != "active-hash" {
		t.Fatalf("active secret_hash=%q, want unchanged", got)
	}
}

// TestDBClockExpiryGuardAllowsGenuinelyFuturePending is the companion
// positive case: when both the Go-side pre-filter and the real DB clock
// agree the deadline is still ahead, promotion must succeed — the new
// predicate must not be so strict it rejects legitimate, unexpired pending
// credentials.
func TestDBClockExpiryGuardAllowsGenuinelyFuturePending(t *testing.T) {
	_, s := setupTestServer(t)
	s.seedWindowsMachine(t, "win-genuinely-future")
	if _, err := s.db.Exec(`INSERT INTO agent_credentials (machine_id, secret_hash) VALUES (?, ?)`, "win-genuinely-future", "active-hash"); err != nil {
		t.Fatalf("seed credential: %v", err)
	}

	futureExpiry := time.Now().Add(pendingCredentialWindow)
	seedRawPending(t, s, "win-genuinely-future", "pending-hash-future", futureExpiry.Format(time.RFC3339))

	promoted, err := s.promotePendingCredentialCAS("win-genuinely-future", "pending-hash-future")
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if !promoted {
		t.Fatal("a genuinely future, correctly matching pending credential must promote")
	}
	if got := activeSecretHash(t, s, "win-genuinely-future"); got != "pending-hash-future" {
		t.Fatalf("active secret_hash=%q, want promoted %q", got, "pending-hash-future")
	}
}

// TestJuliandayParsesStageTargetedCredentialsExactFormat locks in that the
// exact RFC3339 string stageTargetedCredential actually writes (not a
// hand-picked example) round-trips correctly through SQLite's julianday(),
// for both a future and a past instance of that same format.
func TestJuliandayParsesStageTargetedCredentialsExactFormat(t *testing.T) {
	_, s := setupTestServer(t)
	s.seedWindowsMachine(t, "win-julianday-format")
	if _, err := s.db.Exec(`INSERT INTO agent_credentials (machine_id, secret_hash) VALUES (?, ?)`, "win-julianday-format", "active-hash"); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	tokenHash := hashOf("julianday-format-token")
	if _, err := s.db.Exec(
		`INSERT INTO tokens (token_hash, expires_at, used, target_machine_id) VALUES (?, ?, FALSE, ?)`,
		tokenHash, time.Now().Add(15*time.Minute).Format(time.RFC3339), "win-julianday-format",
	); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	if err := s.stageTargetedCredential(tokenHash, "win-julianday-format", "pending-hash-format"); err != nil {
		t.Fatalf("stage: %v", err)
	}
	_, storedExpiry := pendingState(t, s, "win-julianday-format")
	if storedExpiry == "" {
		t.Fatal("stageTargetedCredential did not store an expiry")
	}

	var isFuture bool
	if err := s.db.QueryRow(`SELECT julianday(?) > julianday('now')`, storedExpiry).Scan(&isFuture); err != nil {
		t.Fatalf("julianday query on stageTargetedCredential's own format: %v", err)
	}
	if !isFuture {
		t.Fatalf("SQLite did not parse stageTargetedCredential's stored format %q as a future timestamp", storedExpiry)
	}

	// And the exact same value, once genuinely in the past, is correctly
	// recognized as such too.
	promoted, err := s.promotePendingCredentialCAS("win-julianday-format", "some-unrelated-hash")
	if err != nil {
		t.Fatalf("promote unrelated hash: %v", err)
	}
	if promoted {
		t.Fatal("an unrelated hash must never promote regardless of expiry")
	}
}

// ===== BLOCKER 3: enrollment_confirmed round-trip and lost-frame recovery =====

// TestSuccessfulStagedPromotionSendsConfirmationFrame proves the CAS
// promotion path sends an explicit "enrollment_confirmed" frame — not just
// that the DB ends up promoted (already covered by
// TestEnrollmentCommittedPromotesPendingAndInvalidatesOld).
func TestSuccessfulStagedPromotionSendsConfirmationFrame(t *testing.T) {
	server, _, _, resp := prepareTargetedReenrollment(t, "win-confirm-frame")
	conn, _ := dialAndStage(t, server, "win-confirm-frame", resp.Token)
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"enrollment_committed"}`)); err != nil {
		t.Fatalf("send enrollment_committed: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	readFrameType(t, conn, "enrollment_confirmed")
}

// TestFreshEnrollmentAlsoReceivesConfirmationBeforeCleanupIsExpected proves
// the fix isn't scoped to targeted re-enrollment only: an ordinary first
// enrollment (no pre-existing credential) that sends enrollment_committed
// also gets enrollment_confirmed back.
func TestFreshEnrollmentAlsoReceivesConfirmationFrame(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	rawToken := s.seedValidToken(t)

	conn, err := wsDialAgent(t, server, "token="+rawToken)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	sendMetricsMsg(t, conn, "fresh-confirm-machine")
	readEnrolledSecret(t, conn)

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"enrollment_committed"}`)); err != nil {
		t.Fatalf("send enrollment_committed: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	readFrameType(t, conn, "enrollment_confirmed")
}

// TestHubPromotionFailureSendsNoConfirmationAndClosesConnection: when the
// staged pending value no longer matches (already promoted/cleared by
// something else) at the time enrollment_committed arrives, the hub must
// send no confirmation and close the connection rather than leave the agent
// in an ambiguous "did it work?" state.
func TestHubPromotionFailureSendsNoConfirmationAndClosesConnection(t *testing.T) {
	server, s, _, resp := prepareTargetedReenrollment(t, "win-promo-fail")
	conn, _ := dialAndStage(t, server, "win-promo-fail", resp.Token)
	defer conn.Close()

	// Sabotage the staged pending value out from under this connection,
	// simulating "something else already consumed/cleared it".
	if _, err := s.db.Exec(`UPDATE agent_credentials SET pending_secret_hash = 'someone-elses-value' WHERE machine_id = ?`, "win-promo-fail"); err != nil {
		t.Fatalf("sabotage pending: %v", err)
	}

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"enrollment_committed"}`)); err != nil {
		t.Fatalf("send enrollment_committed: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	assertNoFrameType(t, conn, "enrollment_confirmed")

	// The connection itself must be closed (next read fails), not just silent.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("expected the connection to be closed after a failed promotion")
	}
}

// TestLostConfirmationReconnectWithPendingSecretYieldsConfirmation extends
// TestAcknowledgementLostReconnectWithPendingSecretPromotesIt with an
// explicit check that the recovery reconnect actually receives
// enrollment_confirmed once upgraded — proof the agent has a real signal to
// act on, not just that the DB quietly ended up correct.
func TestLostConfirmationReconnectWithPendingSecretYieldsConfirmation(t *testing.T) {
	server, _, _, resp := prepareTargetedReenrollment(t, "win-recover-confirm")
	conn, newSecret := dialAndStage(t, server, "win-recover-confirm", resp.Token)
	conn.Close() // ack lost

	h := http.Header{}
	h.Set("Authorization", "Bearer "+newSecret)
	conn2, _, err := dialWSWithHeader(t, server, "/ws/agent", h)
	if err != nil {
		t.Fatalf("reconnect with pending secret: %v", err)
	}
	defer conn2.Close()
	conn2.SetReadDeadline(time.Now().Add(5 * time.Second))
	readFrameType(t, conn2, "enrollment_confirmed")
}

// TestLingeringTokenOnActiveReconnectStillReceivesConfirmation covers "promotion
// succeeded but confirmation/upgrade was lost": a later reconnect using the
// now-fully-active secret, but still carrying a (now stale/consumed) token
// header from the original attempt, must still receive enrollment_confirmed
// so the agent can safely drop that token.
func TestLingeringTokenOnActiveReconnectStillReceivesConfirmation(t *testing.T) {
	server, s, _, resp := prepareTargetedReenrollment(t, "win-lingering-token")
	conn, newSecret := dialAndStage(t, server, "win-lingering-token", resp.Token)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"enrollment_committed"}`)); err != nil {
		t.Fatalf("send enrollment_committed: %v", err)
	}
	waitForPromotion(t, s, "win-lingering-token", hashOf(newSecret))
	conn.Close()

	// Reconnect with the now-active secret AND the original (already
	// consumed) token still present — exactly what a process restart between
	// promotion and the confirm frame arriving would look like.
	h := http.Header{}
	h.Set("Authorization", "Bearer "+newSecret)
	h.Set(agentEnrollTokenHeader, resp.Token)
	conn2, _, err := dialWSWithHeader(t, server, "/ws/agent", h)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	defer conn2.Close()
	conn2.SetReadDeadline(time.Now().Add(5 * time.Second))
	readFrameType(t, conn2, "enrollment_confirmed")
}
