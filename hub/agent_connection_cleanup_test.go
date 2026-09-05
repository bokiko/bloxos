package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// ===== BLOCKER 1 (round 4): ghost connections from post-registration early
// returns. Before the fix, handleAgentWS only called unregisterAgentConnection
// from the ReadMessage-error branch; every other post-registration return
// (a failed confirm write, a promotion that errors/mismatches/expires, an
// enrollment_committed with no matching staged state) left s.agents[machineID]
// pointing at a dead connection and could leave the machine stuck 'online'.
// The fix is a single unconditional `defer` right after the ConnectedAgent is
// created, which Go guarantees runs on every return from handleAgentWS —
// these tests exercise the deterministically-reachable early-return branches
// end-to-end through the real handler (no forced network-level write
// failures, which would be inherently flaky over a real socket; the
// unconditional defer covers the write-failure branches by construction,
// not by branch-by-branch simulation).

func machineStatus(t *testing.T, s *Server, machineID string) string {
	t.Helper()
	var status string
	if err := s.db.QueryRow(`SELECT status FROM machines WHERE id = ?`, machineID).Scan(&status); err != nil {
		t.Fatalf("read status for %s: %v", machineID, err)
	}
	return status
}

// expectConnectionClosed reads until the hub closes the socket. The only
// frame tolerated on the way is the post-registration ai_sessions_config
// notice; anything else means the hub kept talking instead of closing.
func expectConnectionClosed(t *testing.T, conn *websocket.Conn, msg string) {
	t.Helper()
	for {
		_, frame, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(frame, &envelope) == nil && envelope.Type == aiSessionsConfigType {
			continue
		}
		t.Fatalf("%s (got frame %s)", msg, frame)
	}
}

func agentMapHasEntry(s *Server, machineID string) bool {
	s.agentsMu.RLock()
	defer s.agentsMu.RUnlock()
	_, ok := s.agents[machineID]
	return ok
}

// waitForAgentMapAbsence polls until s.agents[machineID] is empty or the
// timeout passes, then fails the test if it never cleared. Registration and
// cleanup both happen inside the WS handler's own goroutine, asynchronously
// with respect to the test closing its client connection.
func waitForAgentMapAbsence(t *testing.T, s *Server, machineID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !agentMapHasEntry(s, machineID) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("agents map still holds an entry for %s after %v — ghost connection", machineID, timeout)
}

// TestEnrollmentCommittedWithNoStagedStateUnregisters covers the
// "enrollment_committed arrives without matching staged state" early return:
// an ordinary reconnect (registered pre-loop via the durable-secret path,
// never entered any enrollment/staging flow) that then sends a spurious
// enrollment_committed must have the hub close the connection AND remove it
// from the agents map — not just close the socket while leaving a ghost
// registry entry.
func TestEnrollmentCommittedWithNoStagedStateUnregisters(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	rawToken := s.seedValidToken(t)

	machineID := "no-staged-state-cleanup"
	secret := s.enrollAndCaptureSecret(t, server, rawToken, machineID)

	h := http.Header{}
	h.Set("Authorization", "Bearer "+secret)
	conn, _, err := dialWSWithHeader(t, server, "/ws/agent", h)
	if err != nil {
		t.Fatalf("reconnect with durable secret: %v", err)
	}
	defer conn.Close()
	sendMetricsMsg(t, conn, machineID)

	// Registration happens pre-loop for an ordinary secret reconnect —
	// confirm it actually registered before provoking the early return.
	waitForCondition(t, 5*time.Second, func() bool { return agentMapHasEntry(s, machineID) })

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"enrollment_committed"}`)); err != nil {
		t.Fatalf("send spurious enrollment_committed: %v", err)
	}

	// The hub must close the connection (no matching staged state).
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	expectConnectionClosed(t, conn, "expected the connection to be closed after enrollment_committed with no staged state")
	waitForAgentMapAbsence(t, s, machineID, 5*time.Second)
}

// TestPromotionMismatchUnregisters extends the existing sabotage scenario
// (TestHubPromotionFailureSendsNoConfirmationAndClosesConnection) with the
// map-cleanup assertion: a staged pending value that no longer matches at
// enrollment_committed time must both close the connection and remove it
// from the agents map.
func TestPromotionMismatchUnregisters(t *testing.T) {
	server, s, _, resp := prepareTargetedReenrollment(t, "promo-mismatch-cleanup")
	conn, _ := dialAndStage(t, server, "promo-mismatch-cleanup", resp.Token)
	defer conn.Close()

	waitForCondition(t, 5*time.Second, func() bool { return agentMapHasEntry(s, "promo-mismatch-cleanup") })

	if _, err := s.db.Exec(`UPDATE agent_credentials SET pending_secret_hash = 'someone-elses-value' WHERE machine_id = ?`, "promo-mismatch-cleanup"); err != nil {
		t.Fatalf("sabotage pending: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"enrollment_committed"}`)); err != nil {
		t.Fatalf("send enrollment_committed: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	expectConnectionClosed(t, conn, "expected the connection to be closed after a mismatched promotion")
	waitForAgentMapAbsence(t, s, "promo-mismatch-cleanup", 5*time.Second)
}

// TestPromotionExpiredDuringCommitUnregisters covers the "pending promotion
// expires" early return: the pending window lapses on the very connection
// that staged it, and only then does enrollment_committed arrive.
func TestPromotionExpiredDuringCommitUnregisters(t *testing.T) {
	machineID := "promo-expired-cleanup"
	server, s, _, resp := prepareTargetedReenrollment(t, machineID)
	conn, _ := dialAndStage(t, server, machineID, resp.Token)
	defer conn.Close()

	waitForCondition(t, 5*time.Second, func() bool { return agentMapHasEntry(s, machineID) })

	if _, err := s.db.Exec(
		`UPDATE agent_credentials SET pending_expires_at = ? WHERE machine_id = ?`,
		time.Now().Add(-time.Minute).Format(time.RFC3339), machineID,
	); err != nil {
		t.Fatalf("force pending expiry: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"enrollment_committed"}`)); err != nil {
		t.Fatalf("send enrollment_committed: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	expectConnectionClosed(t, conn, "expected the connection to be closed after committing an expired pending credential")
	waitForAgentMapAbsence(t, s, machineID, 5*time.Second)
}

// TestReplacementClosesDisplacedConnectionAndKeepsNewer is the end-to-end
// proof for overlapping reconnects (review r3779926380 on #167). Two real
// connections authenticate with the same durable secret for the same
// machine_id. Registering B must (1) make the hub close the displaced A,
// so a later revocation cannot miss a still-authenticated old socket, and
// (2) A's handler exit must find it is no longer the registered connection
// and leave B registered, the machine online, and B usable.
func TestReplacementClosesDisplacedConnectionAndKeepsNewer(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	rawToken := s.seedValidToken(t)

	machineID := "dual-conn-replace"
	secret := s.enrollAndCaptureSecret(t, server, rawToken, machineID)

	dial := func() *websocket.Conn {
		h := http.Header{}
		h.Set("Authorization", "Bearer "+secret)
		conn, _, err := dialWSWithHeader(t, server, "/ws/agent", h)
		if err != nil {
			t.Fatalf("reconnect with durable secret: %v", err)
		}
		return conn
	}

	connA := dial()
	defer connA.Close()
	sendMetricsMsg(t, connA, machineID)
	waitForCondition(t, 5*time.Second, func() bool { return machineStatus(t, s, machineID) == "online" })

	connB := dial()
	defer connB.Close()
	sendMetricsMsg(t, connB, machineID)

	// (1) The hub must close A on its own. Drain any hub frames on A and
	// distinguish a server-side close from this client's own read timeout.
	connA.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, _, err := connA.ReadMessage()
		if err == nil {
			continue
		}
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			t.Fatal("displaced connection A was left open after B registered; revocation would miss it")
		}
		break
	}

	// (2) A's handler exit must not disturb B's live registration.
	time.Sleep(400 * time.Millisecond)
	if status := machineStatus(t, s, machineID); status != "online" {
		t.Fatalf("machine flipped to %q after the displaced connection exited; want unchanged 'online' (B is still live)", status)
	}
	if !agentMapHasEntry(s, machineID) {
		t.Fatal("agents map entry removed by the displaced connection's exit — B's live registration was evicted")
	}
	if err := connB.WriteMessage(websocket.PingMessage, nil); err != nil {
		t.Fatalf("B's connection is no longer usable after A's exit: %v", err)
	}
}

// TestPreRegistrationRejectionLeavesNoAgentMapEntry covers "pre-registration
// rejection remains a no-op": a connection that never authenticates (no
// valid secret, no valid token) never reaches the WebSocket upgrade at all,
// so it can never appear in — and can never wrongly disappear from — the
// agents map.
func TestPreRegistrationRejectionLeavesNoAgentMapEntry(t *testing.T) {
	e, s := setupTestServer(t)
	server := httptest.NewServer(e)
	defer server.Close()

	machineID := "pre-registration-rejected"
	if _, err := wsDialAgent(t, server, "token=not-a-real-token"); err == nil {
		t.Fatal("expected the upgrade to be rejected for an invalid token")
	}

	if agentMapHasEntry(s, machineID) {
		t.Fatal("rejected pre-auth connection somehow left an agents map entry")
	}
}

// waitForCondition polls cond until it returns true or timeout elapses.
func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}
