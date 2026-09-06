package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// seedTokenValue inserts a valid (unused, unexpired) install token with the
// given raw value and returns it. Complements seedValidToken, which always
// inserts the same fixed token. Populates mint-time binding columns if
// PUBLIC_URL is set, so the token can be used with GET /join if needed.
func (s *Server) seedTokenValue(t *testing.T, raw string) string {
	t.Helper()
	h := sha256.Sum256([]byte(raw))
	tokenHash := hex.EncodeToString(h[:])
	expiresAt := time.Now().Add(1 * time.Hour).Format("2006-01-02 15:04:05")
	// Populate mint-time binding columns so join links work, mirroring
	// production: store the config binding only, never the script (which would
	// embed the raw token). Use the current PUBLIC_URL and CA state. If
	// PUBLIC_URL is not set, leave the columns NULL so the token is usable for
	// WebSocket enrollment but not for /join (which requires PUBLIC_URL).
	httpBase, _ := publicAndWebsocketBase()
	var mintHTTPBase, mintCASHA256 interface{}
	if httpBase != "" {
		_, caSHA256 := bootstrapCAFor(httpBase)
		mintHTTPBase = httpBase
		mintCASHA256 = caSHA256
	}
	if _, err := s.db.Exec(`INSERT INTO tokens (token_hash, expires_at, used, mint_time_http_base, mint_time_ca_sha256) VALUES (?, ?, FALSE, ?, ?)`,
		tokenHash, expiresAt, mintHTTPBase, mintCASHA256); err != nil {
		t.Fatalf("seed token %q: %v", raw, err)
	}
	return raw
}

// TestInstallTokenCannotTakeOverEnrolledMachine locks in item 1: a fresh valid
// install token must not be able to claim a machine_id that already has a
// durable credential. Re-enrollment requires an explicit revoke first.
func TestInstallTokenCannotTakeOverEnrolledMachine(t *testing.T) {
	e, s := setupTestServer(t)
	server := httptest.NewServer(e)
	defer server.Close()

	// Enroll machine-A and capture its durable secret.
	token1 := s.seedValidToken(t)
	secret := s.enrollAndCaptureSecret(t, server, token1, "machine-A")
	if secret == "" {
		t.Fatal("enrollment did not yield a secret")
	}

	var before string
	if err := s.db.QueryRow(`SELECT secret_hash FROM agent_credentials WHERE machine_id = ?`, "machine-A").Scan(&before); err != nil {
		t.Fatalf("read machine-A credential: %v", err)
	}

	// Attacker holds a fresh, valid token and tries to claim machine-A.
	s.seedTokenValue(t, "attacker-token-xyz")
	conn, err := wsDialAgent(t, server, "token=attacker-token-xyz")
	if err != nil {
		t.Fatalf("attacker dial: %v", err)
	}
	defer conn.Close()
	sendMetricsMsg(t, conn, "machine-A")

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("expected a rejection frame, got read error: %v", err)
	}
	if strings.Contains(string(msg), "agent_secret") {
		t.Fatalf("takeover succeeded — hub issued a new secret: %s", string(msg))
	}
	if !strings.Contains(string(msg), "revoke") {
		t.Fatalf("expected a revoke-required rejection, got %s", string(msg))
	}

	// machine-A's credential must be unchanged, and unique.
	var after string
	if err := s.db.QueryRow(`SELECT secret_hash FROM agent_credentials WHERE machine_id = ?`, "machine-A").Scan(&after); err != nil {
		t.Fatalf("read machine-A credential after: %v", err)
	}
	if after != before {
		t.Fatal("machine-A credential was replaced by a takeover enrollment")
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_credentials WHERE machine_id = ?`, "machine-A").Scan(&n); err != nil {
		t.Fatalf("count machine-A credentials: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 credential for machine-A, got %d", n)
	}

	// The attacker token must NOT be consumed, so a legitimate re-enroll can
	// still use it after an explicit revoke.
	h := sha256.Sum256([]byte("attacker-token-xyz"))
	var used bool
	if err := s.db.QueryRow(`SELECT used FROM tokens WHERE token_hash = ?`, hex.EncodeToString(h[:])).Scan(&used); err != nil {
		t.Fatalf("read attacker token: %v", err)
	}
	if used {
		t.Fatal("attacker token was consumed by a rejected takeover attempt")
	}
}

// TestCreateTokenRequiresPublicURL locks in item 2: with PUBLIC_URL unset,
// /api/tokens must refuse rather than emit a Host-header-derived install
// command, and must not mint a token.
func TestCreateTokenRequiresPublicURL(t *testing.T) {
	e, s := setupTestServer(t)
	token := loginAndGetToken(t, e)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "")

	req := httptest.NewRequest(http.MethodPost, "/api/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when PUBLIC_URL unset, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "PUBLIC_URL") {
		t.Fatalf("expected error to mention PUBLIC_URL, got %s", rec.Body.String())
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tokens`).Scan(&n); err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected no token minted on refusal, got %d", n)
	}
}

// TestCreateTokenUsesPublicURL locks in item 2's happy path: with PUBLIC_URL
// set, the generated command is derived from PUBLIC_URL.
func TestCreateTokenUsesPublicURL(t *testing.T) {
	e, s := setupTestServer(t)
	token := loginAndGetToken(t, e)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://hub.public.example")

	req := httptest.NewRequest(http.MethodPost, "/api/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	// A hostile Host header must not influence the emitted command.
	req.Host = "attacker.evil.example"
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	command, _ := resp["command"].(string)
	if !strings.Contains(command, "hub.public.example") {
		t.Fatalf("command should use PUBLIC_URL host, got %q", command)
	}
	if strings.Contains(command, "attacker.evil.example") {
		t.Fatalf("command leaked the request Host header: %q", command)
	}
}

// TestInstallScriptPinsAgentBinaryHash locks in item 3 (Linux): the generated
// install.sh must verify the downloaded agent binary's SHA-256 against the hash
// of the binary the hub actually serves.
func TestInstallScriptPinsAgentBinaryHash(t *testing.T) {
	e, _ := setupTestServer(t)

	dir := t.TempDir()
	bin := filepath.Join(dir, "bloxos-agent")
	content := []byte("fake-linux-agent-binary-v1")
	if err := os.WriteFile(bin, content, 0o755); err != nil {
		t.Fatalf("write agent binary: %v", err)
	}
	t.Setenv("BLOXOS_AGENT_BINARY", bin)

	withAgentBinaryState(t)
	useTestResolvedBinary(t, "linux", bin)

	sum := sha256.Sum256(content)
	want := hex.EncodeToString(sum[:])

	req := httptest.NewRequest(http.MethodGet, "/install.sh", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, want) {
		t.Fatalf("install.sh does not pin the expected agent SHA %s", want)
	}
	if !strings.Contains(body, "fingerprint mismatch") {
		t.Fatalf("install.sh missing the hash-verification failure path")
	}
}

// The generated Linux installer must not preserve the unprivileged
// downloader's ownership on a binary that systemd later executes as root.
func TestInstallScriptInstallsAgentBinaryAsRoot(t *testing.T) {
	e, _ := setupTestServer(t)
	useGeneratedTestBinary(t, "linux")

	req := httptest.NewRequest(http.MethodGet, "/install.sh", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}

	body := rec.Body.String()
	const install = "sudo install -o root -g root -m 0755 /tmp/bloxos-agent /usr/local/bin/bloxos-agent"
	if !strings.Contains(body, install) {
		t.Fatal("install.sh does not install the agent root-owned with mode 0755")
	}
	if strings.Contains(body, "sudo mv /tmp/bloxos-agent /usr/local/bin/bloxos-agent") {
		t.Fatal("install.sh still preserves the downloaded agent's unprivileged ownership")
	}
	if !strings.Contains(body, "rm -f /tmp/bloxos-agent") {
		t.Fatal("install.sh leaves the downloaded temporary agent behind")
	}
}

// TestWindowsInstallScriptPinsHashAndVerifiesBinaryDownload locks in item 3
// (Windows): install.ps1 must pin the agent .exe SHA-256 and must download the
// binary with TLS verification (via the bootstrapped CA), not curl -k. The
// initial CA fetch may remain insecure.
func TestWindowsInstallScriptPinsHashAndVerifiesBinaryDownload(t *testing.T) {
	e, _ := setupTestServer(t)

	dir := t.TempDir()
	bin := filepath.Join(dir, "bloxos-agent.exe")
	content := []byte("fake-windows-agent-binary-v1")
	if err := os.WriteFile(bin, content, 0o755); err != nil {
		t.Fatalf("write agent binary: %v", err)
	}
	t.Setenv("BLOXOS_AGENT_BINARY_WINDOWS", bin)

	withAgentBinaryState(t)
	useTestResolvedBinary(t, "windows", bin)

	sum := sha256.Sum256(content)
	want := hex.EncodeToString(sum[:])

	req := httptest.NewRequest(http.MethodGet, "/install.ps1", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, want) {
		t.Fatalf("install.ps1 does not pin the expected agent SHA %s", want)
	}
	if strings.Contains(body, "curl.exe -ksfL -o $AgentExe") {
		t.Fatal("install.ps1 still downloads the agent binary with insecure curl -k")
	}
	if !strings.Contains(body, "$CurlTlsArgs = @('--cacert', $CaPath)") {
		t.Fatal("install.ps1 agent download should verify TLS via the fingerprint-pinned CA")
	}
	if strings.Contains(body, "curl.exe -ksfL -o $CaPath") {
		t.Fatal("install.ps1 must not bootstrap or replace CA trust; the authenticated paste block owns that step")
	}
}

func tokenUsed(t *testing.T, s *Server, tokenHash string) bool {
	t.Helper()
	var used bool
	if err := s.db.QueryRow(`SELECT used FROM tokens WHERE token_hash = ?`, tokenHash).Scan(&used); err != nil {
		t.Fatalf("read token: %v", err)
	}
	return used
}

// TestFreshEnrollmentCommitsOnlyAfterAgentCommitted locks in review
// r3791514735 on #169: a fresh enrollment must not consume the token or
// store a credential when "enrolled" is sent. If the agent then fails to
// save the secret and drops the connection, the same token must still work;
// only enrollment_committed atomically consumes it and activates the secret,
// after which the hash-bound confirmation is sent.
func TestFreshEnrollmentCommitsOnlyAfterAgentCommitted(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	token := s.seedValidToken(t)
	tokenHash := hashOf(token)
	machineID := "fresh-commit-machine"

	conn1, err := wsDialAgent(t, server, "token="+token)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	sendMetricsMsg(t, conn1, machineID)
	conn1.SetReadDeadline(time.Now().Add(5 * time.Second))
	readEnrolledSecret(t, conn1)
	if tokenUsed(t, s, tokenHash) {
		t.Fatal("token consumed before the agent committed the secret")
	}
	if s.machineHasCredential(machineID) {
		t.Fatal("credential stored before the agent committed the secret")
	}
	if agentMapHasEntry(s, machineID) {
		t.Fatal("fresh socket registered as a live agent before commit")
	}

	// The agent's local save fails: it drops the connection without committing.
	conn1.Close()
	s.waitAgentDrain(t, machineID, 2*time.Second)

	conn2, err := wsDialAgent(t, server, "token="+token)
	if err != nil {
		t.Fatalf("retry dial with the same token: %v", err)
	}
	defer conn2.Close()
	sendMetricsMsg(t, conn2, machineID)
	conn2.SetReadDeadline(time.Now().Add(5 * time.Second))
	secret := readEnrolledSecret(t, conn2)
	if err := conn2.WriteMessage(websocket.TextMessage, []byte(`{"type":"enrollment_committed"}`)); err != nil {
		t.Fatalf("send enrollment_committed: %v", err)
	}
	if got := readEnrollmentConfirmedHash(t, conn2); got != hashOf(secret) {
		t.Fatalf("confirmation hash %q, want hash of the issued secret %q", got, hashOf(secret))
	}
	if !tokenUsed(t, s, tokenHash) {
		t.Fatal("token not consumed by the committed enrollment")
	}
	if got := activeSecretHash(t, s, machineID); got != hashOf(secret) {
		t.Fatalf("active secret hash %q, want %q", got, hashOf(secret))
	}
	if id, _, err := s.validateAgentSecret(secret); err != nil || id != machineID {
		t.Fatalf("committed secret does not authenticate: id=%q err=%v", id, err)
	}
	if !agentMapHasEntry(s, machineID) {
		t.Fatal("committed enrollment did not register the socket as a live agent")
	}
}

// TestFreshEnrollmentExpiredTokenAtCommitYieldsNoCredential locks in review
// r3791514737: expiry is re-checked at the commit point. A token that expires
// between "enrolled" and enrollment_committed produces no credential, no
// confirmation, and a closed connection.
func TestFreshEnrollmentExpiredTokenAtCommitYieldsNoCredential(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	token := s.seedValidToken(t)
	machineID := "fresh-expired-machine"

	conn, err := wsDialAgent(t, server, "token="+token)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	sendMetricsMsg(t, conn, machineID)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	readEnrolledSecret(t, conn)
	if agentMapHasEntry(s, machineID) {
		t.Fatal("fresh socket registered as a live agent before commit")
	}

	if _, err := s.db.Exec(`UPDATE tokens SET expires_at = ? WHERE token_hash = ?`,
		"2000-01-01 00:00:00", hashOf(token)); err != nil {
		t.Fatalf("expire token: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"enrollment_committed"}`)); err != nil {
		t.Fatalf("send enrollment_committed: %v", err)
	}
	// The hub must send no confirmation and close the socket itself; a
	// client-side read timeout means it was left open.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, msg, err := conn.ReadMessage()
		if err == nil {
			if strings.Contains(string(msg), "enrollment_confirmed") {
				t.Fatalf("confirmation sent for a token that expired before commit: %s", msg)
			}
			continue
		}
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			t.Fatal("connection left open after an expired commit")
		}
		break
	}
	if s.machineHasCredential(machineID) {
		t.Fatal("credential stored from a token that expired before commit")
	}
	if tokenUsed(t, s, hashOf(token)) {
		t.Fatal("expired token was consumed")
	}
	if agentMapHasEntry(s, machineID) {
		t.Fatal("failed fresh commit left the socket registered as a live agent")
	}
	waitForCondition(t, 5*time.Second, func() bool { return machineStatus(t, s, machineID) == "offline" })
}

// TestFreshCommitIsSerializedWithRevocation answers the Codex P1 on #172: a
// revocation cannot interleave with a fresh enrollment's commit. The fresh
// socket holds the auth read lock from upgrade until after the commit
// transaction and registration, and revocation needs the writer lock. So a
// revoke issued while the enrollment is staged but uncommitted blocks until
// the commit has landed, then finds the registered socket and deletes the
// just-committed credential; the secret ends revoked, not resurrected.
func TestFreshCommitIsSerializedWithRevocation(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	token := s.seedValidToken(t)
	adminToken := loginAndGetToken(t, e)
	machineID := "revoke-machine" // matches testCredentialRevokePath

	conn, err := wsDialAgent(t, server, "token="+token)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	sendMetricsMsg(t, conn, machineID)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	secret := readEnrolledSecret(t, conn)

	// Revocation arrives while the enrollment is staged but uncommitted.
	revoked := make(chan *httptest.ResponseRecorder, 1)
	go func() { revoked <- revokeCredentialRequest(e, adminToken) }()

	select {
	case rec := <-revoked:
		t.Fatalf("revocation ran (status %d) while the fresh enrollment was still uncommitted", rec.Code)
	case <-time.After(300 * time.Millisecond):
	}
	if s.machineHasCredential(machineID) || tokenUsed(t, s, hashOf(token)) {
		t.Fatal("state mutated before enrollment_committed")
	}

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"enrollment_committed"}`)); err != nil {
		t.Fatalf("send enrollment_committed: %v", err)
	}

	// Only after the commit does the writer get through, against the
	// now-registered socket and the now-stored credential. The confirmation
	// frame is not required here: once the read lock is released the
	// waiting revoke may close the socket before the confirmation is
	// delivered, which is a legitimate ordering.
	var rec *httptest.ResponseRecorder
	select {
	case rec = <-revoked:
	case <-time.After(5 * time.Second):
		t.Fatal("revocation never ran after the enrollment committed")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke status=%d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		CredentialExisted bool `json:"credential_existed"`
		ConnectionClosed  bool `json:"connection_closed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode revoke response: %v", err)
	}
	if !resp.CredentialExisted || !resp.ConnectionClosed {
		t.Fatalf("revocation did not run after the commit against the registered socket: %+v", resp)
	}
	if !tokenUsed(t, s, hashOf(token)) {
		t.Fatal("commit did not consume the token")
	}
	if s.machineHasCredential(machineID) {
		t.Fatal("credential resurrected after revocation")
	}
	if _, _, err := s.validateAgentSecret(secret); err == nil {
		t.Fatal("revoked secret still authenticates")
	}
	// The hub must close the socket. A confirmation may or may not have
	// been delivered before the close; if it was, it must name the
	// committed secret.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		_, msg, err := conn.ReadMessage()
		if err == nil {
			var frame struct {
				Type         string `json:"type"`
				SecretSHA256 string `json:"secret_sha256"`
			}
			if json.Unmarshal(msg, &frame) == nil && frame.Type == "enrollment_confirmed" && frame.SecretSHA256 != hashOf(secret) {
				t.Fatalf("confirmation hash %q, want %q", frame.SecretSHA256, hashOf(secret))
			}
			continue
		}
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			t.Fatal("revocation left the registered socket open")
		}
		break
	}
}
