package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testCredentialRevokePath = "/api/machines/revoke-machine/credential"

func seedMachineCredential(t *testing.T, s *Server, machineID, secretHash string) {
	t.Helper()
	s.seedTestMachine(t, machineID)
	if _, err := s.db.Exec(
		`INSERT INTO agent_credentials (machine_id, secret_hash) VALUES (?, ?)`,
		machineID, secretHash,
	); err != nil {
		t.Fatalf("seed agent credential: %v", err)
	}
}

func revokeCredentialRequest(e http.Handler, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, testCredentialRevokePath, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestRevokeAgentCredentialAuthorization(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	seedMachineCredential(t, s, "revoke-machine", "original-secret-hash")

	s.seedTestUser(t, "revoke-viewer", "viewerpass123", "1234", RoleViewer, true, true)
	s.seedTestUser(t, "revoke-operator", "operatorpass123", "1234", RoleOperator, true, true)

	viewerToken := loginAndGetTokenForCredentials(t, e, "revoke-viewer", "viewerpass123")
	operatorToken := loginAndGetTokenForCredentials(t, e, "revoke-operator", "operatorpass123")
	adminToken := loginAndGetToken(t, e)

	for _, tc := range []struct {
		name       string
		token      string
		wantStatus int
	}{
		{name: "unauthenticated", wantStatus: http.StatusUnauthorized},
		{name: "viewer", token: viewerToken, wantStatus: http.StatusForbidden},
		{name: "operator", token: operatorToken, wantStatus: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := revokeCredentialRequest(e, tc.token)
			if rec.Code != tc.wantStatus {
				t.Fatalf("expected %d, got %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
			}
		})
	}

	rec := revokeCredentialRequest(e, adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected admin revoke 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var result struct {
		MachineID         string `json:"machine_id"`
		CredentialExisted bool   `json:"credential_existed"`
		ConnectionClosed  bool   `json:"connection_closed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode revoke response: %v", err)
	}
	if result.MachineID != "revoke-machine" || !result.CredentialExisted || result.ConnectionClosed {
		t.Fatalf("unexpected revoke response: %+v", result)
	}
}

func TestRevokeAgentCredentialPreservesMachineAndHistoryAndIsIdempotent(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	seedMachineCredential(t, s, "revoke-machine", "original-secret-hash")
	if _, err := s.db.Exec(
		`INSERT INTO metrics (machine_id, cpu_percent, timestamp) VALUES (?, ?, CURRENT_TIMESTAMP)`,
		"revoke-machine", 42.5,
	); err != nil {
		t.Fatalf("seed metric: %v", err)
	}
	adminToken := loginAndGetToken(t, e)

	first := revokeCredentialRequest(e, adminToken)
	if first.Code != http.StatusOK {
		t.Fatalf("first revoke: status %d: %s", first.Code, first.Body.String())
	}
	second := revokeCredentialRequest(e, adminToken)
	if second.Code != http.StatusOK {
		t.Fatalf("second revoke: status %d: %s", second.Code, second.Body.String())
	}

	var secondResult struct {
		CredentialExisted bool `json:"credential_existed"`
		ConnectionClosed  bool `json:"connection_closed"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondResult); err != nil {
		t.Fatalf("decode second revoke: %v", err)
	}
	if secondResult.CredentialExisted || secondResult.ConnectionClosed {
		t.Fatalf("repeat revoke should be an idempotent no-op: %+v", secondResult)
	}

	for name, query := range map[string]string{
		"credential": `SELECT COUNT(*) FROM agent_credentials WHERE machine_id = ?`,
		"machine":    `SELECT COUNT(*) FROM machines WHERE id = ?`,
		"metrics":    `SELECT COUNT(*) FROM metrics WHERE machine_id = ?`,
	} {
		var count int
		if err := s.db.QueryRow(query, "revoke-machine").Scan(&count); err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		want := 1
		if name == "credential" {
			want = 0
		}
		if count != want {
			t.Fatalf("%s count=%d, want %d", name, count, want)
		}
	}
}

func TestRevokeAgentCredentialClosesLiveConnection(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()

	token := s.seedValidToken(t)
	secret := s.enrollAndCaptureSecret(t, server, token, "revoke-machine")
	conn, err := wsDialAgent(t, server, "secret="+secret)
	if err != nil {
		t.Fatalf("reconnect with durable secret: %v", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for {
		s.agentsMu.RLock()
		_, connected := s.agents["revoke-machine"]
		s.agentsMu.RUnlock()
		if connected {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("agent did not register before revoke")
		}
		time.Sleep(10 * time.Millisecond)
	}

	rec := revokeCredentialRequest(e, loginAndGetToken(t, e))
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke: status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"connection_closed":true`) {
		t.Fatalf("response did not report live connection closure: %s", rec.Body.String())
	}

	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
	s.waitAgentDrain(t, "revoke-machine", 2*time.Second)
}

func TestRevokedSecretWithFreshTokenReenrollsSameMachine(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()

	firstToken := s.seedValidToken(t)
	oldSecret := s.enrollAndCaptureSecret(t, server, firstToken, "revoke-machine")
	adminToken := loginAndGetToken(t, e)
	if rec := revokeCredentialRequest(e, adminToken); rec.Code != http.StatusOK {
		t.Fatalf("revoke: status %d: %s", rec.Code, rec.Body.String())
	}

	if _, resp, err := dialWSWithHeader(t, server, "/ws/agent", http.Header{
		"Authorization": []string{"Bearer " + oldSecret},
	}); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		if err == nil {
			t.Fatal("revoked secret was accepted")
		}
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("revoked secret status=%d, want 401 (err=%v)", status, err)
	}

	freshToken := s.seedTokenValue(t, "fresh-reenrollment-token")
	h := http.Header{}
	h.Set("Authorization", "Bearer "+oldSecret)
	h.Set(agentEnrollTokenHeader, freshToken)
	conn, resp, err := dialWSWithHeader(t, server, "/ws/agent", h)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("old-secret + fresh-token dial failed: %v (status=%d)", err, status)
	}
	defer conn.Close()

	tokenSum := sha256.Sum256([]byte(freshToken))
	tokenHash := hex.EncodeToString(tokenSum[:])
	var usedBefore bool
	if err := s.db.QueryRow(`SELECT used FROM tokens WHERE token_hash = ?`, tokenHash).Scan(&usedBefore); err != nil {
		t.Fatalf("read token before enrollment frame: %v", err)
	}
	if usedBefore {
		t.Fatal("fresh token was consumed before successful enrollment")
	}

	sendMetricsMsg(t, conn, "revoke-machine")
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var newSecret string
	for newSecret == "" {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read re-enrollment frame: %v", err)
		}
		var frame struct {
			Type        string `json:"type"`
			AgentSecret string `json:"agent_secret"`
		}
		if json.Unmarshal(msg, &frame) == nil && frame.Type == "enrolled" {
			newSecret = frame.AgentSecret
		}
	}
	if newSecret == oldSecret {
		t.Fatal("re-enrollment reused the revoked secret")
	}
	commitEnrollment(t, conn)

	var usedAfter bool
	if err := s.db.QueryRow(`SELECT used FROM tokens WHERE token_hash = ?`, tokenHash).Scan(&usedAfter); err != nil {
		t.Fatalf("read token after enrollment: %v", err)
	}
	if !usedAfter {
		t.Fatal("fresh token was not consumed by successful re-enrollment")
	}

	var credentialCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_credentials WHERE machine_id = ?`, "revoke-machine").Scan(&credentialCount); err != nil {
		t.Fatalf("count replacement credentials: %v", err)
	}
	if credentialCount != 1 {
		t.Fatalf("credential count=%d, want 1", credentialCount)
	}
	var machineCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM machines WHERE id = ?`, "revoke-machine").Scan(&machineCount); err != nil {
		t.Fatalf("count machine rows: %v", err)
	}
	if machineCount != 1 {
		t.Fatalf("machine count=%d, want 1", machineCount)
	}
}

func TestCredentialRevokeRouteHasFleetAdminScope(t *testing.T) {
	key := routeScopeKey(http.MethodDelete, "/api/machines/:id/credential")
	if got := routeScopeRequirements[key]; got != scopeFleetAdmin {
		t.Fatalf("revoke route scope=%q, want %q", got, scopeFleetAdmin)
	}

	echoServer, _ := setupTestServer(t)
	if err := auditRBACRouteCoverage(echoServer, routeScopeRequirements); err != nil {
		t.Fatalf("RBAC route audit: %v", err)
	}
}
