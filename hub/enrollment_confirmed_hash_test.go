package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// ===== BLOCKER 2 (round 4): every enrollment_confirmed names the exact
// credential it confirms via secret_sha256. Before this fix the agent had
// no way to bind a confirmation to a specific local file — it trusted
// whichever file it had just written, which is exactly the split-brain
// blocker 2 closes. These tests lock in that ALL FOUR confirmation sites in
// handleAgentWS include the correct hash, not just that a frame arrives.

// readEnrollmentConfirmedHash drains conn until an "enrollment_confirmed"
// frame arrives and returns its secret_sha256 field.
func readEnrollmentConfirmedHash(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for enrollment_confirmed: %v", err)
		}
		var frame struct {
			Type         string `json:"type"`
			SecretSHA256 string `json:"secret_sha256"`
		}
		if json.Unmarshal(msg, &frame) == nil && frame.Type == "enrollment_confirmed" {
			return frame.SecretSHA256
		}
	}
}

// TestFreshEnrollmentConfirmationCarriesActiveSecretHash covers "fresh
// enrollment confirmation carries the hash stored active": a brand new
// machine's confirmation must name the exact secret consumeTokenAndStoreCredential
// wrote active — hashOf the plaintext the agent received AND the DB's
// active secret_hash must both equal it.
func TestFreshEnrollmentConfirmationCarriesActiveSecretHash(t *testing.T) {
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
	sendMetricsMsg(t, conn, "fresh-hash-machine")
	newSecret := readEnrolledSecret(t, conn)

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"enrollment_committed"}`)); err != nil {
		t.Fatalf("send enrollment_committed: %v", err)
	}
	gotHash := readEnrollmentConfirmedHash(t, conn)

	want := hashOf(newSecret)
	if gotHash != want {
		t.Fatalf("secret_sha256=%q, want hash of the issued secret %q", gotHash, want)
	}
	if got := activeSecretHash(t, s, "fresh-hash-machine"); got != want {
		t.Fatalf("active secret_hash=%q, want %q — confirmation hash must match what's actually active", got, want)
	}
}

// TestStagedPromotionConfirmationCarriesPromotedSecretHash covers "staged
// promotion confirmation carries the promoted hash".
func TestStagedPromotionConfirmationCarriesPromotedSecretHash(t *testing.T) {
	server, s, _, resp := prepareTargetedReenrollment(t, "staged-hash-machine")
	conn, newSecret := dialAndStage(t, server, "staged-hash-machine", resp.Token)
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"enrollment_committed"}`)); err != nil {
		t.Fatalf("send enrollment_committed: %v", err)
	}
	gotHash := readEnrollmentConfirmedHash(t, conn)

	want := hashOf(newSecret)
	if gotHash != want {
		t.Fatalf("secret_sha256=%q, want hash of the promoted secret %q", gotHash, want)
	}
	waitForPromotion(t, s, "staged-hash-machine", want)
}

// TestPendingSecretReconnectConfirmationCarriesPromotedHash covers
// "pending-secret reconnect confirmation carries the hash that was
// promoted/validated" — the lost-acknowledgement recovery path.
func TestPendingSecretReconnectConfirmationCarriesPromotedHash(t *testing.T) {
	server, _, _, resp := prepareTargetedReenrollment(t, "pending-reconnect-hash-machine")
	conn, newSecret := dialAndStage(t, server, "pending-reconnect-hash-machine", resp.Token)
	conn.Close() // ack lost

	h := http.Header{}
	h.Set("Authorization", "Bearer "+newSecret)
	conn2, _, err := dialWSWithHeader(t, server, "/ws/agent", h)
	if err != nil {
		t.Fatalf("reconnect with pending secret: %v", err)
	}
	defer conn2.Close()

	gotHash := readEnrollmentConfirmedHash(t, conn2)
	want := hashOf(newSecret)
	if gotHash != want {
		t.Fatalf("secret_sha256=%q, want the promoted secret's hash %q", gotHash, want)
	}
}

// TestLingeringTokenActiveReconnectConfirmationCarriesActiveHash covers
// "active-secret reconnect with a lingering token carries the active hash it
// actually authenticated".
func TestLingeringTokenActiveReconnectConfirmationCarriesActiveHash(t *testing.T) {
	server, s, _, resp := prepareTargetedReenrollment(t, "lingering-token-hash-machine")
	conn, newSecret := dialAndStage(t, server, "lingering-token-hash-machine", resp.Token)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"enrollment_committed"}`)); err != nil {
		t.Fatalf("send enrollment_committed: %v", err)
	}
	waitForPromotion(t, s, "lingering-token-hash-machine", hashOf(newSecret))
	conn.Close()

	h := http.Header{}
	h.Set("Authorization", "Bearer "+newSecret)
	h.Set(agentEnrollTokenHeader, resp.Token)
	conn2, _, err := dialWSWithHeader(t, server, "/ws/agent", h)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	defer conn2.Close()

	gotHash := readEnrollmentConfirmedHash(t, conn2)
	want := hashOf(newSecret)
	if gotHash != want {
		t.Fatalf("secret_sha256=%q, want the active secret's hash %q (the one this reconnect actually authenticated with)", gotHash, want)
	}
}

// TestStaleAcknowledgementCannotEmitConfirmationForNewerPendingHash: once a
// connection's staged pending value no longer matches what it originally
// staged (sabotaged here to stand in for a genuine concurrent re-stage —
// see TestStaleAcknowledgementCannotClobberNewerReStagedPending for the
// direct CAS-level proof of that race), committing on that connection must
// close it with NO confirmation frame at all — never a confirmation naming
// some other hash, which would wrongly tell the agent it may trust a secret
// it never actually received.
func TestStaleAcknowledgementCannotEmitConfirmationForNewerPendingHash(t *testing.T) {
	server, s, _, resp := prepareTargetedReenrollment(t, "stale-ack-hash-machine")
	connA, _ := dialAndStage(t, server, "stale-ack-hash-machine", resp.Token)
	defer connA.Close()

	if _, err := s.db.Exec(`UPDATE agent_credentials SET pending_secret_hash = 'someone-elses-newer-hash' WHERE machine_id = ?`, "stale-ack-hash-machine"); err != nil {
		t.Fatalf("sabotage pending: %v", err)
	}

	if err := connA.WriteMessage(websocket.TextMessage, []byte(`{"type":"enrollment_committed"}`)); err != nil {
		t.Fatalf("send stale enrollment_committed: %v", err)
	}
	connA.SetReadDeadline(time.Now().Add(2 * time.Second))
	assertNoFrameType(t, connA, "enrollment_confirmed")
}
