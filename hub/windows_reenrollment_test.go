package main

import (
	"crypto/sha256"
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

// setMachineOS updates an existing machine row's os column.
func (s *Server) setMachineOS(t *testing.T, id, osName string) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE machines SET os = ? WHERE id = ?`, osName, id); err != nil {
		t.Fatalf("set machine os=%q: %v", osName, err)
	}
}

// seedWindowsMachine seeds a fresh machine row with os='windows'.
func (s *Server) seedWindowsMachine(t *testing.T, id string) {
	t.Helper()
	s.seedTestMachine(t, id)
	s.setMachineOS(t, id, "windows")
}

func windowsReenrollmentPath(machineID string) string {
	return "/api/machines/" + machineID + "/windows-re-enrollment"
}

func windowsReenrollmentRequest(e http.Handler, machineID, authToken string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, windowsReenrollmentPath(machineID), nil)
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// TestTokensMigrationAddsNullableTargetMachineIDWithoutAlteringExisting locks
// in that the schema change is additive: a token inserted the old way (no
// target_machine_id) reads back with an unbound (NULL) target, and its
// existing validate/consume behavior is untouched.
func TestTokensMigrationAddsNullableTargetMachineIDWithoutAlteringExisting(t *testing.T) {
	_, s := setupTestServer(t)

	rawToken := "pre-migration-shape-token"
	h := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(h[:])
	expiresAt := time.Now().Add(15 * time.Minute).Format(time.RFC3339)

	// Insert exactly the way pre-existing code paths do: no target_machine_id.
	if _, err := s.db.Exec(`INSERT INTO tokens (token_hash, expires_at, used) VALUES (?, ?, FALSE)`, tokenHash, expiresAt); err != nil {
		t.Fatalf("insert legacy-shaped token: %v", err)
	}

	gotHash, target, err := s.validateAgentToken(rawToken)
	if err != nil {
		t.Fatalf("expected valid unbound token, got error: %v", err)
	}
	if gotHash != tokenHash {
		t.Fatalf("hash mismatch: got %s want %s", gotHash, tokenHash)
	}
	if target.Valid {
		t.Fatalf("expected NULL target_machine_id for a legacy-shaped token, got %q", target.String)
	}
}

// TestOrdinaryTokensRemainUnboundAndPreserveExistingBehavior locks in that a
// token minted by the ordinary POST /api/tokens flow has no target, so all
// pre-existing generic-enrollment behavior (including the takeover refusal
// covered by TestInstallTokenCannotTakeOverEnrolledMachine) is unaffected.
func TestOrdinaryTokensRemainUnboundAndPreserveExistingBehavior(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	adminToken := loginAndGetToken(t, e)
	t.Setenv("PUBLIC_URL", "https://hub.example")

	req := httptest.NewRequest(http.MethodPost, "/api/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create token: status %d: %s", rec.Code, rec.Body.String())
	}
	var resp installTokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	_, target, err := s.validateAgentToken(resp.Token)
	if err != nil {
		t.Fatalf("validate ordinary token: %v", err)
	}
	if target.Valid {
		t.Fatalf("ordinary Add Machine token must be unbound, got target %q", target.String)
	}
}

// TestWindowsReenrollmentAuthorization locks in the RBAC/existence gate:
// 401 unauthenticated, 403 viewer/operator, 404 missing machine, 404
// non-Windows machine, 200 fleet.admin against a real Windows machine.
func TestWindowsReenrollmentAuthorization(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://hub.example")
	s.seedWindowsMachine(t, "win-authz")
	if _, err := s.db.Exec(`INSERT INTO agent_credentials (machine_id, secret_hash) VALUES (?, ?)`, "win-authz", "authz-secret-hash"); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	s.seedTestMachine(t, "linux-authz") // os left unset -> normalizes to "linux"

	s.seedTestUser(t, "wre-viewer", "viewerpass123", "1234", RoleViewer, true, true)
	s.seedTestUser(t, "wre-operator", "operatorpass123", "1234", RoleOperator, true, true)
	viewerToken := loginAndGetTokenForCredentials(t, e, "wre-viewer", "viewerpass123")
	operatorToken := loginAndGetTokenForCredentials(t, e, "wre-operator", "operatorpass123")
	adminToken := loginAndGetToken(t, e)

	for _, tc := range []struct {
		name       string
		machineID  string
		token      string
		wantStatus int
	}{
		{name: "unauthenticated", machineID: "win-authz", wantStatus: http.StatusUnauthorized},
		{name: "viewer", machineID: "win-authz", token: viewerToken, wantStatus: http.StatusForbidden},
		{name: "operator", machineID: "win-authz", token: operatorToken, wantStatus: http.StatusForbidden},
		{name: "missing machine", machineID: "no-such-machine", token: adminToken, wantStatus: http.StatusNotFound},
		{name: "non-Windows machine", machineID: "linux-authz", token: adminToken, wantStatus: http.StatusNotFound},
		{name: "fleet.admin on Windows machine", machineID: "win-authz", token: adminToken, wantStatus: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := windowsReenrollmentRequest(e, tc.machineID, tc.token)
			if rec.Code != tc.wantStatus {
				t.Fatalf("expected %d, got %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestWindowsReenrollmentRequiresActiveCredential locks in that the endpoint
// refuses to mint a re-enrollment token for a Windows machine with no active
// credential — there is nothing for it to stage a replacement against — and
// that the refusal inserts no token row.
func TestWindowsReenrollmentRequiresActiveCredential(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	s.seedWindowsMachine(t, "win-no-credential")
	adminToken := loginAndGetToken(t, e)
	t.Setenv("PUBLIC_URL", "https://hub.example")

	rec := windowsReenrollmentRequest(e, "win-no-credential", adminToken)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a machine with no active credential, got %d: %s", rec.Code, rec.Body.String())
	}

	var tokenCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tokens`).Scan(&tokenCount); err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if tokenCount != 0 {
		t.Fatalf("expected no token minted on refusal, got %d", tokenCount)
	}
}

// TestWindowsReenrollmentRequiresPublicURLAndMutatesNothingOnFailure locks in
// that a missing PUBLIC_URL refuses before any write: no token row, and (when
// a credential already exists) that credential is untouched.
func TestWindowsReenrollmentRequiresPublicURLAndMutatesNothingOnFailure(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	s.seedWindowsMachine(t, "win-nopublicurl")
	if _, err := s.db.Exec(`INSERT INTO agent_credentials (machine_id, secret_hash) VALUES (?, ?)`, "win-nopublicurl", "existing-hash"); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	adminToken := loginAndGetToken(t, e)
	t.Setenv("PUBLIC_URL", "")

	rec := windowsReenrollmentRequest(e, "win-nopublicurl", adminToken)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when PUBLIC_URL unset, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "PUBLIC_URL") {
		t.Fatalf("expected error to mention PUBLIC_URL, got %s", rec.Body.String())
	}

	var tokenCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tokens`).Scan(&tokenCount); err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if tokenCount != 0 {
		t.Fatalf("expected no token minted on refusal, got %d", tokenCount)
	}
	var secretHash string
	if err := s.db.QueryRow(`SELECT secret_hash FROM agent_credentials WHERE machine_id = ?`, "win-nopublicurl").Scan(&secretHash); err != nil {
		t.Fatalf("read credential: %v", err)
	}
	if secretHash != "existing-hash" {
		t.Fatalf("credential was mutated by a refused request: %q", secretHash)
	}
}

// TestWindowsReenrollmentInsertsTargetedTokenWithoutTouchingLiveState locks in
// that preparing the command is inert: the token is minted target-bound and
// unused, but no credential is deleted and no live socket is closed.
func TestWindowsReenrollmentInsertsTargetedTokenWithoutTouchingLiveState(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	t.Setenv("PUBLIC_URL", "https://hub.example")

	s.seedWindowsMachine(t, "win-inert")
	firstToken := s.seedTokenValue(t, "win-inert-initial-token")
	oldSecret := s.enrollAndCaptureSecret(t, server, firstToken, "win-inert")
	if oldSecret == "" {
		t.Fatal("initial enrollment did not yield a secret")
	}
	// enrollAndCaptureSecret's metrics fixture reports os="linux/amd64" and
	// upsertMachine writes that back — restore os=windows so the machine
	// still qualifies for the endpoint, matching what a real Windows agent's
	// own metrics frames would report.
	s.setMachineOS(t, "win-inert", "windows")

	adminToken := loginAndGetToken(t, e)
	rec := windowsReenrollmentRequest(e, "win-inert", adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("prepare re-enrollment: status %d: %s", rec.Code, rec.Body.String())
	}
	var resp windowsReenrollmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.MachineID != "win-inert" || resp.Token == "" || resp.WindowsCommand == "" || resp.ExpiresAt == "" {
		t.Fatalf("incomplete response: %+v", resp)
	}

	// Token is target-bound and unused.
	h := sha256.Sum256([]byte(resp.Token))
	tokenHash := hex.EncodeToString(h[:])
	var used bool
	var targetMachineID string
	if err := s.db.QueryRow(`SELECT used, target_machine_id FROM tokens WHERE token_hash = ?`, tokenHash).Scan(&used, &targetMachineID); err != nil {
		t.Fatalf("read minted token: %v", err)
	}
	if used {
		t.Fatal("token was consumed merely by preparing the command")
	}
	if targetMachineID != "win-inert" {
		t.Fatalf("token target=%q, want win-inert", targetMachineID)
	}

	// Existing credential untouched.
	var secretHash string
	if err := s.db.QueryRow(`SELECT secret_hash FROM agent_credentials WHERE machine_id = ?`, "win-inert").Scan(&secretHash); err != nil {
		t.Fatalf("credential deleted merely by preparing the command: %v", err)
	}

	// No socket close: the still-enrolled agent's secret keeps authenticating.
	authHeader := http.Header{}
	authHeader.Set("Authorization", "Bearer "+oldSecret)
	liveConn, liveResp, err := dialWSWithHeader(t, server, "/ws/agent", authHeader)
	if err != nil {
		status := 0
		if liveResp != nil {
			status = liveResp.StatusCode
		}
		t.Fatalf("preparing the command disconnected/invalidated the live agent: status=%d err=%v", status, err)
	}
	liveConn.Close()
}

// TestWindowsReenrollmentCommandUsesPublicURLAndForceReenroll locks in that
// the returned command is generated exclusively from PUBLIC_URL (never a
// request Host header) and invokes install.ps1 with -ForceReenroll, while the
// ordinary POST /api/tokens Windows command omits the switch entirely.
func TestWindowsReenrollmentCommandUsesPublicURLAndForceReenroll(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	s.seedWindowsMachine(t, "win-cmd")
	if _, err := s.db.Exec(`INSERT INTO agent_credentials (machine_id, secret_hash) VALUES (?, ?)`, "win-cmd", "cmd-secret-hash"); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	adminToken := loginAndGetToken(t, e)
	t.Setenv("PUBLIC_URL", "https://hub.public.example")

	req := httptest.NewRequest(http.MethodPost, windowsReenrollmentPath("win-cmd"), nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Host = "evil.example"
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("prepare re-enrollment: status %d: %s", rec.Code, rec.Body.String())
	}
	var resp windowsReenrollmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if strings.Contains(resp.WindowsCommand, "evil.example") {
		t.Fatalf("command used the request Host instead of PUBLIC_URL: %q", resp.WindowsCommand)
	}
	if !strings.Contains(resp.WindowsCommand, "hub.public.example") {
		t.Fatalf("command did not use PUBLIC_URL: %q", resp.WindowsCommand)
	}
	if !strings.Contains(resp.WindowsCommand, "-ForceReenroll") {
		t.Fatalf("re-enrollment command missing -ForceReenroll: %q", resp.WindowsCommand)
	}

	// Ordinary Add Machine command must NOT carry the switch.
	ordReq := httptest.NewRequest(http.MethodPost, "/api/tokens", nil)
	ordReq.Header.Set("Authorization", "Bearer "+adminToken)
	ordRec := httptest.NewRecorder()
	e.ServeHTTP(ordRec, ordReq)
	if ordRec.Code != http.StatusOK {
		t.Fatalf("create ordinary token: status %d: %s", ordRec.Code, ordRec.Body.String())
	}
	var ordResp installTokenResponse
	if err := json.Unmarshal(ordRec.Body.Bytes(), &ordResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if strings.Contains(ordResp.WindowsCommand, "-ForceReenroll") {
		t.Fatalf("ordinary Add Machine Windows command must not contain -ForceReenroll: %q", ordResp.WindowsCommand)
	}
}

// TestTargetedTokenRejectedByWrongMachineAndLeftUnused locks in the early
// enrollment guard: a token minted for machine A must be rejected when
// presented by machine B, whether or not B already has a credential, and the
// token must remain usable afterward (not silently burned by the attempt).
func TestTargetedTokenRejectedByWrongMachineAndLeftUnused(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	t.Setenv("PUBLIC_URL", "https://hub.example")

	s.seedWindowsMachine(t, "win-target-a")
	firstToken := s.seedTokenValue(t, "win-target-a-initial")
	s.enrollAndCaptureSecret(t, server, firstToken, "win-target-a")
	s.setMachineOS(t, "win-target-a", "windows") // restore os=windows clobbered by the metrics fixture

	adminToken := loginAndGetToken(t, e)
	rec := windowsReenrollmentRequest(e, "win-target-a", adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("prepare re-enrollment: status %d: %s", rec.Code, rec.Body.String())
	}
	var resp windowsReenrollmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// A different, uninvolved machine tries to use A's targeted token.
	conn, err := wsDialAgent(t, server, "token="+resp.Token)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	sendMetricsMsg(t, conn, "win-target-b")

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("expected a rejection frame, got read error: %v", err)
	}
	if strings.Contains(string(msg), "agent_secret") {
		t.Fatalf("wrong-machine use of a targeted token was accepted: %s", string(msg))
	}
	if !strings.Contains(string(msg), "different machine") {
		t.Fatalf("expected a bound-to-a-different-machine rejection, got %s", string(msg))
	}

	// Token must remain unused.
	h := sha256.Sum256([]byte(resp.Token))
	tokenHash := hex.EncodeToString(h[:])
	var used bool
	if err := s.db.QueryRow(`SELECT used FROM tokens WHERE token_hash = ?`, tokenHash).Scan(&used); err != nil {
		t.Fatalf("read token: %v", err)
	}
	if used {
		t.Fatal("targeted token was consumed by a wrong-machine rejection")
	}

	// And win-target-b must not have gained a credential.
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_credentials WHERE machine_id = ?`, "win-target-b").Scan(&count); err != nil {
		t.Fatalf("count credentials: %v", err)
	}
	if count != 0 {
		t.Fatalf("wrong-machine targeted-token use created a credential: count=%d", count)
	}
}

// TestGenericTokenStillCannotReplaceEnrolledMachineCredential re-confirms,
// after the target-binding change, that an ordinary unbound token still
// cannot silently replace an already-enrolled machine's credential — this is
// the same invariant as TestInstallTokenCannotTakeOverEnrolledMachine,
// exercised again here alongside the new targeted-token paths so a future
// change to the branch logic that weakens one can't slip past only testing
// the other.
func TestGenericTokenStillCannotReplaceEnrolledMachineCredential(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()

	s.seedWindowsMachine(t, "win-generic-guard")
	firstToken := s.seedTokenValue(t, "win-generic-guard-initial")
	before := s.enrollAndCaptureSecret(t, server, firstToken, "win-generic-guard")

	genericToken := s.seedTokenValue(t, "win-generic-guard-attacker")
	conn, err := wsDialAgent(t, server, "token="+genericToken)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	sendMetricsMsg(t, conn, "win-generic-guard")

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("expected rejection, got read error: %v", err)
	}
	if strings.Contains(string(msg), "agent_secret") {
		t.Fatal("generic token replaced an enrolled machine's credential")
	}

	var after string
	if err := s.db.QueryRow(`SELECT secret_hash FROM agent_credentials WHERE machine_id = ?`, "win-generic-guard").Scan(&after); err != nil {
		t.Fatalf("read credential: %v", err)
	}
	h := sha256.Sum256([]byte(before))
	beforeHash := hex.EncodeToString(h[:])
	if after != beforeHash {
		t.Fatal("credential was replaced despite generic-token guard")
	}
}

// TestTargetedTokenConsumedOnceAndCannotBeReplayed covers the token-lifecycle
// half of the positive path — the full credential-handoff protocol (staging,
// the old secret remaining valid, and enrollment_committed promotion) is
// covered by windows_reenrollment_pending_test.go.
func TestTargetedTokenConsumedOnceAndCannotBeReplayed(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	t.Setenv("PUBLIC_URL", "https://hub.example")

	s.seedWindowsMachine(t, "win-target-once")
	firstToken := s.seedTokenValue(t, "win-target-once-initial")
	s.enrollAndCaptureSecret(t, server, firstToken, "win-target-once")
	s.setMachineOS(t, "win-target-once", "windows") // restore os=windows clobbered by the metrics fixture

	adminToken := loginAndGetToken(t, e)
	rec := windowsReenrollmentRequest(e, "win-target-once", adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("prepare re-enrollment: status %d: %s", rec.Code, rec.Body.String())
	}
	var resp windowsReenrollmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	conn, err := wsDialAgent(t, server, "token="+resp.Token)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	sendMetricsMsg(t, conn, "win-target-once")
	readEnrolledSecret(t, conn)

	h := sha256.Sum256([]byte(resp.Token))
	tokenHash := hex.EncodeToString(h[:])
	var used bool
	if err := s.db.QueryRow(`SELECT used FROM tokens WHERE token_hash = ?`, tokenHash).Scan(&used); err != nil {
		t.Fatalf("read token: %v", err)
	}
	if !used {
		t.Fatal("targeted token was not marked used after staging")
	}

	// Cannot be replayed. A consumed token has neither a valid secret nor a
	// valid token behind it, so handleAgentWS's pre-upgrade guard rejects the
	// handshake outright (401) rather than upgrading and rejecting on the
	// first frame.
	if conn2, resp2, err := dialWSWithHeader(t, server, "/ws/agent?token="+resp.Token, http.Header{}); err == nil {
		conn2.Close()
		t.Fatal("consumed targeted token was replayed successfully (handshake upgraded)")
	} else if resp2 == nil || resp2.StatusCode != http.StatusUnauthorized {
		status := 0
		if resp2 != nil {
			status = resp2.StatusCode
		}
		t.Fatalf("expected 401 rejecting the replayed token, got status=%d err=%v", status, err)
	}
}

// readEnrolledSecret drains frames on conn until it sees an "enrolled" frame
// and returns its agent_secret.
func readEnrolledSecret(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read enrolled frame: %v", err)
		}
		var frame struct {
			Type        string `json:"type"`
			AgentSecret string `json:"agent_secret"`
		}
		if json.Unmarshal(msg, &frame) == nil && frame.Type == "enrolled" && frame.AgentSecret != "" {
			return frame.AgentSecret
		}
	}
}

// TestConsumeTokenAndStoreCredentialEnforcesTargetInsideTransaction defeats a
// validate/consume race: two goroutines race to consume the same targeted
// token for its correct machine_id. Exactly one must win; the DB-level
// UPDATE ... WHERE used = FALSE guard (re-checked inside the same
// transaction as the target lookup) must make the second a clean failure,
// not a lost update.
func TestConsumeTokenAndStoreCredentialEnforcesTargetInsideTransaction(t *testing.T) {
	_, s := setupTestServer(t)
	s.seedWindowsMachine(t, "win-race")

	rawToken := "win-race-targeted-token"
	h := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(h[:])
	expiresAt := time.Now().Add(15 * time.Minute).Format(time.RFC3339)
	if _, err := s.db.Exec(
		`INSERT INTO tokens (token_hash, expires_at, used, target_machine_id) VALUES (?, ?, FALSE, ?)`,
		tokenHash, expiresAt, "win-race",
	); err != nil {
		t.Fatalf("seed targeted token: %v", err)
	}

	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = s.consumeTokenAndStoreCredential(tokenHash, "win-race", "secret-hash-race")
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
		t.Fatalf("expected exactly 1 successful consumption of a raced token, got %d", successes)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_credentials WHERE machine_id = ?`, "win-race").Scan(&count); err != nil {
		t.Fatalf("count credentials: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 credential row after a raced consume, got %d", count)
	}
}

// TestTokenTransactionsRecheckExpiryAndTakeoverGuard covers the rechecks
// that run inside the consuming transactions rather than only at initial
// validation: an expired token is refused by both the fresh-enrollment
// commit and targeted staging, and a fresh commit refuses a machine that
// gained a credential meanwhile. In every case the token stays unused.
func TestTokenTransactionsRecheckExpiryAndTakeoverGuard(t *testing.T) {
	_, s := setupTestServer(t)
	expired := time.Now().Add(-time.Minute).Format(time.RFC3339)

	freshHash := hashOf("expired-fresh-token")
	if _, err := s.db.Exec(`INSERT INTO tokens (token_hash, expires_at, used) VALUES (?, ?, FALSE)`, freshHash, expired); err != nil {
		t.Fatal(err)
	}
	if err := s.consumeTokenAndStoreCredential(freshHash, "expired-fresh", "h1"); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("fresh commit with expired token: err=%v, want expiry refusal", err)
	}
	if s.machineHasCredential("expired-fresh") || tokenUsed(t, s, freshHash) {
		t.Fatal("expired fresh token consumed or credential stored")
	}

	seedMachineCredential(t, s, "expired-targeted", "active-hash")
	targetedHash := hashOf("expired-targeted-token")
	if _, err := s.db.Exec(`INSERT INTO tokens (token_hash, expires_at, used, target_machine_id) VALUES (?, ?, FALSE, ?)`, targetedHash, expired, "expired-targeted"); err != nil {
		t.Fatal(err)
	}
	if err := s.stageTargetedCredential(targetedHash, "expired-targeted", "h2"); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("staging with expired token: err=%v, want expiry refusal", err)
	}
	if tokenUsed(t, s, targetedHash) {
		t.Fatal("expired targeted token consumed")
	}

	takeoverHash := hashOf("takeover-token")
	if _, err := s.db.Exec(`INSERT INTO tokens (token_hash, expires_at, used) VALUES (?, ?, FALSE)`, takeoverHash, time.Now().Add(time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if err := s.consumeTokenAndStoreCredential(takeoverHash, "expired-targeted", "h3"); err == nil || !strings.Contains(err.Error(), "already has a durable credential") {
		t.Fatalf("fresh commit against an enrolled machine: err=%v, want takeover refusal", err)
	}
	if got := activeSecretHash(t, s, "expired-targeted"); got != "active-hash" || tokenUsed(t, s, takeoverHash) {
		t.Fatalf("takeover attempt changed state: active=%q used=%v", got, tokenUsed(t, s, takeoverHash))
	}
}

// TestWindowsReenrollmentRouteHasFleetAdminScope mirrors
// TestCredentialRevokeRouteHasFleetAdminScope: the new route must be present
// in routeScopeRequirements with fleet.admin, and the generic startup audit
// (auditRBACRouteCoverage) must pass with it registered.
func TestWindowsReenrollmentRouteHasFleetAdminScope(t *testing.T) {
	key := routeScopeKey(http.MethodPost, "/api/machines/:id/windows-re-enrollment")
	if got := routeScopeRequirements[key]; got != scopeFleetAdmin {
		t.Fatalf("windows-re-enrollment route scope=%q, want %q", got, scopeFleetAdmin)
	}

	echoServer, _ := setupTestServer(t)
	if err := auditRBACRouteCoverage(echoServer, routeScopeRequirements); err != nil {
		t.Fatalf("RBAC route audit: %v", err)
	}
}
