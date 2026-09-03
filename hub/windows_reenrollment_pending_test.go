package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// prepareTargetedReenrollment seeds a Windows machine with an active
// credential, mints a re-enrollment token for it via the real endpoint, and
// returns the server, the old secret, and the parsed token response.
func prepareTargetedReenrollment(t *testing.T, machineID string) (*httptest.Server, *Server, string, windowsReenrollmentResponse) {
	t.Helper()
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	t.Cleanup(server.Close)
	t.Setenv("PUBLIC_URL", "https://hub.example")

	s.seedWindowsMachine(t, machineID)
	firstToken := s.seedTokenValue(t, machineID+"-initial")
	oldSecret := s.enrollAndCaptureSecret(t, server, firstToken, machineID)
	s.setMachineOS(t, machineID, "windows") // restore os=windows clobbered by the metrics fixture

	adminToken := loginAndGetToken(t, e)
	rec := windowsReenrollmentRequest(e, machineID, adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("prepare re-enrollment: status %d: %s", rec.Code, rec.Body.String())
	}
	var resp windowsReenrollmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return server, s, oldSecret, resp
}

func dialAndStage(t *testing.T, server *httptest.Server, machineID, token string) (*websocket.Conn, string) {
	t.Helper()
	conn, err := wsDialAgent(t, server, "token="+token)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	sendMetricsMsg(t, conn, machineID)
	newSecret := readEnrolledSecret(t, conn)
	return conn, newSecret
}

func activeSecretHash(t *testing.T, s *Server, machineID string) string {
	t.Helper()
	var hash string
	if err := s.db.QueryRow(`SELECT secret_hash FROM agent_credentials WHERE machine_id = ?`, machineID).Scan(&hash); err != nil {
		t.Fatalf("read active secret_hash: %v", err)
	}
	return hash
}

func pendingState(t *testing.T, s *Server, machineID string) (hash string, expiresAt string) {
	t.Helper()
	var h, exp sql.NullString
	if err := s.db.QueryRow(`SELECT pending_secret_hash, pending_expires_at FROM agent_credentials WHERE machine_id = ?`, machineID).Scan(&h, &exp); err != nil {
		t.Fatalf("read pending state: %v", err)
	}
	return h.String, exp.String
}

func hashOf(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

func secretAuthenticates(t *testing.T, server *httptest.Server, secret string) bool {
	t.Helper()
	h := http.Header{}
	h.Set("Authorization", "Bearer "+secret)
	conn, resp, err := dialWSWithHeader(t, server, "/ws/agent", h)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return false
		}
		t.Fatalf("dial: %v (status=%v)", err, respStatus(resp))
		return false
	}
	conn.Close()
	return true
}

func respStatus(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

// TestTargetedReenrollmentStagesPendingKeepingOldActive is the core positive
// path for BLOCKER 1: staging a targeted re-enrollment must NOT touch the
// active credential. The old secret is proven still valid by actually
// authenticating with it against the running hub, not merely by reading a
// row count.
func TestTargetedReenrollmentStagesPendingKeepingOldActive(t *testing.T) {
	server, s, oldSecret, resp := prepareTargetedReenrollment(t, "win-stage")
	oldActiveHash := activeSecretHash(t, s, "win-stage")

	conn, newSecret := dialAndStage(t, server, "win-stage", resp.Token)
	defer conn.Close()
	if newSecret == oldSecret {
		t.Fatal("staged secret reused the old secret")
	}

	// Active credential is untouched.
	if got := activeSecretHash(t, s, "win-stage"); got != oldActiveHash {
		t.Fatalf("active secret_hash changed merely by staging: got %q, want unchanged %q", got, oldActiveHash)
	}
	// Pending reflects the new secret.
	pendingHash, pendingExp := pendingState(t, s, "win-stage")
	if pendingHash != hashOf(newSecret) {
		t.Fatalf("pending_secret_hash=%q, want hash of the staged secret", pendingHash)
	}
	if pendingExp == "" {
		t.Fatal("pending_expires_at not set")
	}

	// The old secret still actually authenticates against the running hub.
	if !secretAuthenticates(t, server, oldSecret) {
		t.Fatal("old secret no longer authenticates immediately after staging — this is exactly the split-brain risk being fixed")
	}
}

// TestDualCredentialConnectionRoutesToStagingNotOrdinaryReconnect covers the
// actual real-world shape of a -ForceReenroll restart: the agent's old
// secret file is still on disk (no longer moved aside per blocker 1's fix)
// AND BLOXOS_TOKEN is freshly set, so the very first connection attempt
// presents BOTH credentials together in one handshake — exactly what
// agent/main.go's runAgent does whenever both agentSecret and token are
// non-empty. This must be detected and routed to staging, not silently
// fast-pathed as an ordinary secret reconnect that never looks at the token.
func TestDualCredentialConnectionRoutesToStagingNotOrdinaryReconnect(t *testing.T) {
	server, s, oldSecret, resp := prepareTargetedReenrollment(t, "win-dual")
	oldActiveHash := activeSecretHash(t, s, "win-dual")

	h := http.Header{}
	h.Set("Authorization", "Bearer "+oldSecret)
	h.Set(agentEnrollTokenHeader, resp.Token)
	conn, _, err := dialWSWithHeader(t, server, "/ws/agent", h)
	if err != nil {
		t.Fatalf("dual-credential dial: %v", err)
	}
	defer conn.Close()
	sendMetricsMsg(t, conn, "win-dual")
	newSecret := readEnrolledSecret(t, conn)

	if newSecret == oldSecret {
		t.Fatal("dual-credential connection did not stage a new secret")
	}
	// Proves staging (not ordinary-reconnect fast path, which would never
	// have consumed the token or produced an "enrolled" frame at all): active
	// unchanged, pending set to the new secret.
	if got := activeSecretHash(t, s, "win-dual"); got != oldActiveHash {
		t.Fatalf("active secret_hash changed on a dual-credential connection before any commit: got %q, want unchanged %q", got, oldActiveHash)
	}
	pendingHash, _ := pendingState(t, s, "win-dual")
	if pendingHash != hashOf(newSecret) {
		t.Fatalf("pending_secret_hash=%q, want hash of the newly staged secret", pendingHash)
	}
	// The token was actually consumed — proof the token was looked at at all,
	// not ignored the way a generic/irrelevant token riding along would be.
	h2 := sha256.Sum256([]byte(resp.Token))
	var used bool
	if err := s.db.QueryRow(`SELECT used FROM tokens WHERE token_hash = ?`, hex.EncodeToString(h2[:])).Scan(&used); err != nil {
		t.Fatalf("read token: %v", err)
	}
	if !used {
		t.Fatal("targeted token was not consumed on the dual-credential connection")
	}
}

// TestDualCredentialWithGenericTokenIsOrdinaryReconnectTokenIgnored: when the
// secret is valid but the accompanying token is generic (unbound) rather
// than targeted at this machine, it must never override the valid
// credential — this is just an ordinary reconnect, and the token is left
// completely unconsumed.
func TestDualCredentialWithGenericTokenIsOrdinaryReconnectTokenIgnored(t *testing.T) {
	server, s, oldSecret, _ := prepareTargetedReenrollment(t, "win-dual-generic")
	genericToken := s.seedTokenValue(t, "win-dual-generic-irrelevant-token")

	h := http.Header{}
	h.Set("Authorization", "Bearer "+oldSecret)
	h.Set(agentEnrollTokenHeader, genericToken)
	conn, _, err := dialWSWithHeader(t, server, "/ws/agent", h)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	sendMetricsMsg(t, conn, "win-dual-generic")

	// No "enrolled" frame should ever arrive for an ordinary reconnect. Give
	// it a short bounded window, then confirm nothing changed.
	conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, msg, err := conn.ReadMessage(); err == nil && strings.Contains(string(msg), "enrolled") {
		t.Fatalf("ordinary reconnect with a generic token unexpectedly staged/enrolled: %s", msg)
	}

	h2 := sha256.Sum256([]byte(genericToken))
	var used bool
	if err := s.db.QueryRow(`SELECT used FROM tokens WHERE token_hash = ?`, hex.EncodeToString(h2[:])).Scan(&used); err != nil {
		t.Fatalf("read token: %v", err)
	}
	if used {
		t.Fatal("generic token riding alongside a valid secret was consumed")
	}
	pendingHash, _ := pendingState(t, s, "win-dual-generic")
	if pendingHash != "" {
		t.Fatal("pending state was set despite the token being generic, not targeted")
	}
}

// TestNoAcknowledgementOldSecretStillAuthenticates: if the agent never sends
// enrollment_committed at all (frame lost, agent crashed after receiving the
// pending secret but before/instead of acking), the old secret must remain
// the one that authenticates — proving the fix actually closes blocker 1's
// failure window rather than just moving it.
func TestNoAcknowledgementOldSecretStillAuthenticates(t *testing.T) {
	server, s, oldSecret, resp := prepareTargetedReenrollment(t, "win-noack")
	conn, _ := dialAndStage(t, server, "win-noack", resp.Token)
	conn.Close() // simulate the agent vanishing before ever acking

	if !secretAuthenticates(t, server, oldSecret) {
		t.Fatal("old secret was invalidated despite no enrollment_committed ever being sent")
	}
	// And nothing was promoted.
	pendingHash, _ := pendingState(t, s, "win-noack")
	if pendingHash == "" {
		t.Fatal("pending state was cleared without ever being acknowledged")
	}
}

// TestEnrollmentCommittedPromotesPendingAndInvalidatesOld: once the agent
// sends enrollment_committed on the connection that staged it, the pending
// secret becomes active and the old one is invalidated — this is the ONLY
// point at which the old credential should stop working.
func TestEnrollmentCommittedPromotesPendingAndInvalidatesOld(t *testing.T) {
	server, s, oldSecret, resp := prepareTargetedReenrollment(t, "win-commit")
	conn, newSecret := dialAndStage(t, server, "win-commit", resp.Token)
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"enrollment_committed"}`)); err != nil {
		t.Fatalf("send enrollment_committed: %v", err)
	}
	waitForPromotion(t, s, "win-commit", hashOf(newSecret))

	if got := activeSecretHash(t, s, "win-commit"); got != hashOf(newSecret) {
		t.Fatalf("active secret_hash=%q, want the promoted new secret's hash", got)
	}
	pendingHash, pendingExp := pendingState(t, s, "win-commit")
	if pendingHash != "" || pendingExp != "" {
		t.Fatalf("pending state not cleared after promotion: hash=%q expires=%q", pendingHash, pendingExp)
	}

	if secretAuthenticates(t, server, oldSecret) {
		t.Fatal("old secret still authenticates after successful promotion")
	}
	if !secretAuthenticates(t, server, newSecret) {
		t.Fatal("new secret does not authenticate after promotion")
	}
}

// waitForPromotion polls until machine_id's active secret_hash equals want or
// a short deadline passes — promotion happens in the WS read-loop goroutine,
// asynchronously with respect to the test's WriteMessage call.
func waitForPromotion(t *testing.T, s *Server, machineID, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if activeSecretHash(t, s, machineID) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("promotion did not complete within timeout")
}

// TestAcknowledgementLostReconnectWithPendingSecretPromotesIt covers the
// recovery path: the agent durably saved its new secret and reconnects with
// it (proving durability), but the hub never received an
// enrollment_committed (connection dropped right after storage, or a service
// restart happened between save and ack). validateAgentSecret must recognize
// and promote the pending secret on this reconnect.
func TestAcknowledgementLostReconnectWithPendingSecretPromotesIt(t *testing.T) {
	server, s, oldSecret, resp := prepareTargetedReenrollment(t, "win-recover")
	conn, newSecret := dialAndStage(t, server, "win-recover", resp.Token)
	conn.Close() // ack lost

	// Fresh connection, reconnecting with the durably-saved pending secret —
	// no token this time, exactly like a real agent restart.
	h := http.Header{}
	h.Set("Authorization", "Bearer "+newSecret)
	conn2, _, err := dialWSWithHeader(t, server, "/ws/agent", h)
	if err != nil {
		t.Fatalf("reconnect with pending secret: %v", err)
	}
	defer conn2.Close()

	waitForPromotion(t, s, "win-recover", hashOf(newSecret))
	pendingHash, _ := pendingState(t, s, "win-recover")
	if pendingHash != "" {
		t.Fatal("pending state not cleared after recovery promotion")
	}
	if secretAuthenticates(t, server, oldSecret) {
		t.Fatal("old secret still authenticates after recovery promotion")
	}
}

// TestExpiredPendingSecretRejectedOldActiveRemainsValid: a pending secret
// that's aged past its bounded window must not authenticate, and the old
// active secret must remain valid throughout the abandoned attempt.
func TestExpiredPendingSecretRejectedOldActiveRemainsValid(t *testing.T) {
	server, s, oldSecret, resp := prepareTargetedReenrollment(t, "win-expired")
	conn, newSecret := dialAndStage(t, server, "win-expired", resp.Token)
	conn.Close()

	if _, err := s.db.Exec(
		`UPDATE agent_credentials SET pending_expires_at = ? WHERE machine_id = ?`,
		time.Now().Add(-time.Minute).Format(time.RFC3339), "win-expired",
	); err != nil {
		t.Fatalf("force pending expiry: %v", err)
	}

	if secretAuthenticates(t, server, newSecret) {
		t.Fatal("expired pending secret was accepted")
	}
	if !secretAuthenticates(t, server, oldSecret) {
		t.Fatal("old active secret stopped working while the pending attempt merely expired")
	}
}

// TestConcurrentTargetedConsumptionsProduceAtMostOnePendingCredential races
// two goroutines staging the SAME targeted token for the same machine —
// exactly one may succeed, and the resulting pending state must reflect a
// single, internally consistent outcome (not a partially-applied mix).
func TestConcurrentTargetedConsumptionsProduceAtMostOnePendingCredential(t *testing.T) {
	_, s := setupTestServer(t)
	s.seedWindowsMachine(t, "win-stage-race")
	if _, err := s.db.Exec(`INSERT INTO agent_credentials (machine_id, secret_hash) VALUES (?, ?)`, "win-stage-race", "race-active-hash"); err != nil {
		t.Fatalf("seed credential: %v", err)
	}

	rawToken := "win-stage-race-token"
	tokenHash := hashOf(rawToken)
	if _, err := s.db.Exec(
		`INSERT INTO tokens (token_hash, expires_at, used, target_machine_id) VALUES (?, ?, FALSE, ?)`,
		tokenHash, time.Now().Add(15*time.Minute).Format(time.RFC3339), "win-stage-race",
	); err != nil {
		t.Fatalf("seed targeted token: %v", err)
	}

	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = s.stageTargetedCredential(tokenHash, "win-stage-race", "pending-hash-"+string(rune('A'+idx)))
		}(i)
	}
	wg.Wait()

	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly 1 successful staging of a raced token, got %d", successes)
	}
	if got := activeSecretHash(t, s, "win-stage-race"); got != "race-active-hash" {
		t.Fatalf("active credential was touched by racing staging attempts: %q", got)
	}
}
