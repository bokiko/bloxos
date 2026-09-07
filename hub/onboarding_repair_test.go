package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// legacyTokenRow inserts a token the way hubs before this change did: a Go
// time.Time handed straight to the driver, which stores it with the local
// offset and zone name. This is the exact on-disk form an upgraded hub
// finds, so the cleanup must be judged against it, not a hand-written string.
func legacyTokenRow(t *testing.T, s *Server, hash string, expiry time.Time) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO tokens (token_hash, expires_at) VALUES (?, ?)`, hash, expiry); err != nil {
		t.Fatal(err)
	}
}

func tokenExists(t *testing.T, s *Server, hash string) bool {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tokens WHERE token_hash = ?`, hash).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

// TestTokenCleanupJudgesLegacyOffsetRowsByInstant: a fresh token stored by
// an older hub west of UTC must survive cleanup, an expired one stored east
// of UTC must be removed, and the new UTC text form behaves the same. The
// old SQL text comparison got both legacy cases wrong.
func TestTokenCleanupJudgesLegacyOffsetRowsByInstant(t *testing.T) {
	_, s := setupTestServer(t)
	if _, err := s.db.Exec(`DELETE FROM tokens`); err != nil {
		t.Fatal(err)
	}
	// Real zone abbreviations as a hub's local clock would render them, plus
	// the numeric form Go uses for zones without a name (e.g. "+03").
	west := time.FixedZone("EDT", -4*3600)
	east := time.FixedZone("+03", 3*3600)
	unnamed := time.FixedZone("", -4*3600)
	now := time.Now()
	legacyTokenRow(t, s, "legacy-west-fresh", now.In(west).Add(15*time.Minute))
	legacyTokenRow(t, s, "legacy-east-expired", now.In(east).Add(-time.Hour))
	legacyTokenRow(t, s, "legacy-west-expired", now.In(west).Add(-time.Hour))
	legacyTokenRow(t, s, "legacy-unnamed-fresh", now.In(unnamed).Add(15*time.Minute))
	if _, err := s.db.Exec(`INSERT INTO tokens (token_hash, expires_at) VALUES (?, ?)`, "utc-fresh", tokenExpiryText(now.Add(15*time.Minute))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO tokens (token_hash, expires_at) VALUES (?, ?)`, "utc-expired", tokenExpiryText(now.Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}

	// Every stored form must still validate the way validateAgentToken does.
	for _, hash := range []string{"legacy-west-fresh", "legacy-unnamed-fresh", "utc-fresh"} {
		var stored string
		if err := s.db.QueryRow(`SELECT expires_at FROM tokens WHERE token_hash = ?`, hash).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if err := checkTokenExpiry(stored); err != nil {
			t.Fatalf("%s (%q) must validate as unexpired: %v", hash, stored, err)
		}
	}

	deleted, err := s.deleteExpiredTokens(now)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 {
		t.Fatalf("deleted %d rows, want 3", deleted)
	}
	for hash, want := range map[string]bool{
		"legacy-west-fresh": true, "legacy-unnamed-fresh": true, "utc-fresh": true,
		"legacy-east-expired": false, "legacy-west-expired": false, "utc-expired": false,
	} {
		if got := tokenExists(t, s, hash); got != want {
			t.Fatalf("token %s present=%v, want %v", hash, got, want)
		}
	}
}

// TestMintedTokensStoreUTCText: new rows carry the UTC RFC3339 form, which
// both the Go parser and SQLite's date functions read.
func TestMintedTokensStoreUTCText(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "http://127.0.0.1:4000")
	adminToken := loginAndGetToken(t, e)
	req := httptest.NewRequest(http.MethodPost, "/api/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mint: %d %s", rec.Code, rec.Body.String())
	}
	var stored string
	if err := s.db.QueryRow(`SELECT expires_at FROM tokens ORDER BY rowid DESC LIMIT 1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(stored, "Z") || strings.Contains(stored, " ") {
		t.Fatalf("expires_at not stored as UTC RFC3339 text: %q", stored)
	}
	var sqliteReadable bool
	if err := s.db.QueryRow(`SELECT julianday(expires_at) IS NOT NULL FROM tokens ORDER BY rowid DESC LIMIT 1`).Scan(&sqliteReadable); err != nil || !sqliteReadable {
		t.Fatalf("SQLite cannot read the stored expiry (%v)", err)
	}
}

func postSetup(e http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func userCount(t *testing.T, s *Server) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestSetupTokenSurvivesValidationAndStorageFailures: a rejected password
// and then a storage failure must both leave the same token usable, and the
// eventual success creates exactly one admin.
func TestSetupTokenSurvivesValidationAndStorageFailures(t *testing.T) {
	e, s := setupEmptyTestServer(t)
	const token = "test-setup-token-abc123"

	if rec := postSetup(e, `{"setup_token":"`+token+`","username":"admin","password":"short","pin":"5678"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("short password: %d %s", rec.Code, rec.Body.String())
	}

	setupStorageTestHook = func() error { return errors.New("disk full") }
	rec := postSetup(e, `{"setup_token":"`+token+`","username":"admin","password":"securepass123","pin":"5678"}`)
	setupStorageTestHook = nil
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("storage failure: %d %s", rec.Code, rec.Body.String())
	}
	if n := userCount(t, s); n != 0 {
		t.Fatalf("user created despite storage failure: %d", n)
	}

	rec = postSetup(e, `{"setup_token":"`+token+`","username":"admin","password":"securepass123","pin":"5678"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("retry with the same token must succeed: %d %s", rec.Code, rec.Body.String())
	}
	if n := userCount(t, s); n != 1 {
		t.Fatalf("expected exactly one admin, got %d", n)
	}
	if rec := postSetup(e, `{"setup_token":"`+token+`","username":"admin2","password":"securepass123","pin":"5678"}`); rec.Code == http.StatusOK {
		t.Fatal("token reusable after successful setup")
	}
}

// TestSetupConcurrentRequestsCreateOneAdmin: many valid requests racing on
// the same token produce exactly one admin and exactly one success.
func TestSetupConcurrentRequestsCreateOneAdmin(t *testing.T) {
	e, s := setupEmptyTestServer(t)
	const token = "test-setup-token-abc123"
	const n = 8
	codes := make(chan int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := `{"setup_token":"` + token + `","username":"admin","password":"securepass123","pin":"5678"}`
			codes <- postSetup(e, body).Code
		}(i)
	}
	wg.Wait()
	close(codes)
	ok := 0
	for code := range codes {
		if code == http.StatusOK {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("expected exactly one successful setup, got %d", ok)
	}
	if got := userCount(t, s); got != 1 {
		t.Fatalf("expected exactly one admin row, got %d", got)
	}
}

// TestPublicURLTrailingSlashProducesCleanCommands: a PUBLIC_URL configured
// with a trailing slash must not leak "//" into the paste block, the join
// link or the mint-time binding, and the drift check must still pass.
func TestPublicURLTrailingSlashProducesCleanCommands(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "http://127.0.0.1:4000/")
	adminToken := loginAndGetToken(t, e)

	req := httptest.NewRequest(http.MethodPost, "/api/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mint: %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Command         string `json:"command"`
		AdvancedCommand string `json:"advanced_command"`
		WindowsCommand  string `json:"windows_command"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for name, cmd := range map[string]string{"command": got.Command, "advanced_command": got.AdvancedCommand, "windows_command": got.WindowsCommand} {
		if strings.Contains(cmd, "4000//") || strings.Contains(cmd, "4000/'") || strings.Contains(cmd, `4000/"`) {
			t.Fatalf("%s carries the trailing slash into a path: %s", name, cmd)
		}
	}
	if !strings.Contains(got.AdvancedCommand, "HUB_HTTP='http://127.0.0.1:4000'") {
		t.Fatalf("advanced command HUB_HTTP not normalized: %s", got.AdvancedCommand)
	}
	var mintBase string
	if err := s.db.QueryRow(`SELECT mint_time_http_base FROM tokens ORDER BY rowid DESC LIMIT 1`).Scan(&mintBase); err != nil {
		t.Fatal(err)
	}
	if mintBase != "http://127.0.0.1:4000" {
		t.Fatalf("mint-time binding not normalized: %q", mintBase)
	}
	// Join link works and rebuilds the same script (drift check passes).
	if !strings.HasPrefix(got.Command, "bash <(curl") && !strings.Contains(got.Command, "/join/") {
		t.Fatalf("unexpected short command shape: %s", got.Command)
	}
}

// TestJoinDriftCheckToleratesLegacyTrailingSlashBinding: a token minted by
// an older hub with the trailing slash stored in its binding still serves
// after upgrade, because the check compares origins.
func TestJoinDriftCheckToleratesLegacyTrailingSlashBinding(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "http://127.0.0.1:4000")
	adminToken := loginAndGetToken(t, e)
	req := httptest.NewRequest(http.MethodPost, "/api/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mint: %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	// Simulate the legacy binding spelling.
	if _, err := s.db.Exec(`UPDATE tokens SET mint_time_http_base = mint_time_http_base || '/'`); err != nil {
		t.Fatal(err)
	}
	joinPath := got.Command[strings.Index(got.Command, "/join/"):]
	joinPath = strings.Fields(joinPath)[0]
	joinPath = strings.Trim(joinPath, `"')`)
	jreq := httptest.NewRequest(http.MethodGet, joinPath, nil)
	jrec := httptest.NewRecorder()
	e.ServeHTTP(jrec, jreq)
	if jrec.Code != http.StatusOK {
		t.Fatalf("legacy-binding join rejected: %d %s", jrec.Code, jrec.Body.String())
	}
	body := jrec.Body.String()
	if strings.Contains(body, "4000//") || !strings.Contains(body, "HUB_HTTP='http://127.0.0.1:4000'") {
		t.Fatalf("served script rebuilt from the raw legacy binding: %s", body)
	}
}

// TestInstallScriptsRunAsRootWithoutSudo: both generated scripts define the
// SUDO shim before their first privileged step and never call sudo directly.
func TestInstallScriptsRunAsRootWithoutSudo(t *testing.T) {
	paste := buildLinuxInstallCommand("https://hub.example", "wss://hub.example", "tok", "https://hub.example/ca.crt", "abc")
	install := fetchLinuxInstallScript(t)
	for name, script := range map[string]string{"paste block": paste, "install.sh": install} {
		shim := `if [[ $(id -u) -eq 0 ]]; then SUDO=""; else SUDO=sudo; fi`
		shimAt := strings.Index(script, shim)
		if shimAt < 0 {
			t.Fatalf("%s lacks the SUDO shim", name)
		}
		for _, line := range strings.Split(script, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "sudo ") || strings.Contains(line, " sudo ") || strings.Contains(line, "|sudo ") {
				if strings.Contains(line, "SUDO=sudo") {
					continue
				}
				t.Fatalf("%s still calls sudo directly: %q", name, line)
			}
		}
		if first := strings.Index(script, "$SUDO "); first >= 0 && first < shimAt {
			t.Fatalf("%s uses $SUDO before defining it", name)
		}
	}
}
