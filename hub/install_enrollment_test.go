package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedTokenValue inserts a valid (unused, unexpired) install token with the
// given raw value and returns it. Complements seedValidToken, which always
// inserts the same fixed token.
func (s *Server) seedTokenValue(t *testing.T, raw string) string {
	t.Helper()
	h := sha256.Sum256([]byte(raw))
	tokenHash := hex.EncodeToString(h[:])
	expiresAt := time.Now().Add(1 * time.Hour).Format("2006-01-02 15:04:05")
	if _, err := s.db.Exec(`INSERT INTO tokens (token_hash, expires_at, used) VALUES (?, ?, FALSE)`, tokenHash, expiresAt); err != nil {
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

	hubAgentBinaryMu.Lock()
	prevSHA, prevMtime := hubAgentBinarySHA, hubAgentBinaryMtime
	hubAgentBinarySHA = ""
	hubAgentBinaryMtime = time.Time{}
	hubAgentBinaryMu.Unlock()
	t.Cleanup(func() {
		hubAgentBinaryMu.Lock()
		hubAgentBinarySHA, hubAgentBinaryMtime = prevSHA, prevMtime
		hubAgentBinaryMu.Unlock()
	})

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

	hubAgentBinaryMu.Lock()
	prevSHA, prevMtime := hubWindowsAgentBinarySHA, hubWindowsAgentBinaryMtime
	hubWindowsAgentBinarySHA = ""
	hubWindowsAgentBinaryMtime = time.Time{}
	hubAgentBinaryMu.Unlock()
	t.Cleanup(func() {
		hubAgentBinaryMu.Lock()
		hubWindowsAgentBinarySHA, hubWindowsAgentBinaryMtime = prevSHA, prevMtime
		hubAgentBinaryMu.Unlock()
	})

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
	if !strings.Contains(body, "--cacert $CaPath") {
		t.Fatal("install.ps1 agent download should verify TLS via the bootstrapped CA (--cacert)")
	}
	if !strings.Contains(body, "curl.exe -ksfL -o $CaPath") {
		t.Fatal("expected the initial CA bootstrap fetch to remain (curl -k to $CaPath)")
	}
}
