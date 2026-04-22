package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

// seedTestAdmin creates a test admin user directly in the DB (bypasses setup flow).
// Uses the legacy default credentials (admin/bloxos, PIN 1234) for test compatibility.
func seedTestAdmin(t *testing.T) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("bloxos"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	id := "test-admin-id"
	_, err = db.Exec(`INSERT INTO users (id, username, password_hash) VALUES (?, ?, ?)`,
		id, "admin", string(hash))
	if err != nil {
		t.Fatalf("seed test admin: %v", err)
	}
}

// setupTestServer creates a fresh in-memory DB, seeds a test admin user,
// sets a deterministic JWT secret, resets the rate limiter, and returns
// an Echo instance with all routes registered.
func setupTestServer(t *testing.T) *echo.Echo {
	t.Helper()

	// Drain stale goroutines from prior tests that may still reference
	// the old db via the global agents map or markOffline calls.
	agentsMu.Lock()
	agents = make(map[string]*ConnectedAgent)
	agentsMu.Unlock()
	termSessionsMu.Lock()
	termSessions = make(map[string]*TerminalSession)
	termSessionsMu.Unlock()
	time.Sleep(100 * time.Millisecond)

	var err error
	db, err = sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := runMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	seedTestAdmin(t)
	jwtSecret = []byte("test-secret-key-for-smoke-tests")
	rateLimiter = NewRateLimiter()

	e := echo.New()
	e.HideBanner = true

	// Public endpoints.
	e.GET("/health", handleHealth)
	e.GET("/ws/agent", handleAgentWS)
	e.POST("/api/auth/login", handleLogin)
	e.GET("/api/setup/status", handleSetupStatus)
	e.POST("/api/setup", handleSetup)

	// Protected endpoints.
	api := e.Group("", jwtMiddleware, credentialRotationMiddleware)
	api.GET("/api/machines", handleListMachines)
	api.GET("/api/machines/:id", handleGetMachine)
	api.GET("/api/machines/:id/services", handleGetServices)
	api.GET("/api/machines/:id/containers", handleGetContainers)
	api.POST("/api/machines/:id/command", handleCommand)
	api.PUT("/api/machines/:id/tags", handleSetTags)
	api.GET("/api/machines/:id/metrics/history", handleMetricsHistory)
	api.DELETE("/api/machines/:id", handleDeleteMachine)

	api.POST("/api/machines/:id/terminal", handleStartTerminal)
	api.DELETE("/api/machines/:id/terminal/:session_id", handleCloseTerminal)
	e.GET("/ws/terminal/:session_id", handleTerminalWS)

	api.GET("/api/alerts", handleListAlerts)
	api.GET("/api/alerts/active/count", handleAlertCount)
	api.POST("/api/alerts/:id/acknowledge", handleAcknowledgeAlert)
	api.GET("/api/alert-rules", handleListAlertRules)
	api.PUT("/api/alert-rules/:id", handleUpdateAlertRule)

	api.POST("/api/auth/change-password", handleChangePassword)
	api.POST("/api/auth/change-pin", handleChangePIN)
	api.POST("/api/auth/sse-token", handleSSEToken)

	api.POST("/api/tokens", handleCreateToken)
	api.POST("/api/bulk/command", handleBulkCommand)

	return e
}

// setupEmptyTestServer creates a server with NO users (for testing setup flow).
func setupEmptyTestServer(t *testing.T) *echo.Echo {
	t.Helper()

	agentsMu.Lock()
	agents = make(map[string]*ConnectedAgent)
	agentsMu.Unlock()
	termSessionsMu.Lock()
	termSessions = make(map[string]*TerminalSession)
	termSessionsMu.Unlock()
	time.Sleep(100 * time.Millisecond)

	var err error
	db, err = sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := runMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	jwtSecret = []byte("test-secret-key-for-smoke-tests")
	rateLimiter = NewRateLimiter()
	setupTokenValue = "test-setup-token-abc123"

	e := echo.New()
	e.HideBanner = true

	e.GET("/health", handleHealth)
	e.POST("/api/auth/login", handleLogin)
	e.GET("/api/setup/status", handleSetupStatus)
	e.POST("/api/setup", handleSetup)

	api := e.Group("", jwtMiddleware, credentialRotationMiddleware)
	api.GET("/api/machines", handleListMachines)

	return e
}

// loginAndGetToken performs a login with the default admin credentials and
// returns the JWT token string.
func loginAndGetToken(t *testing.T, e *echo.Echo) string {
	t.Helper()

	body := `{"username":"admin","password":"bloxos"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("login failed: status %d, body %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal login response: %v", err)
	}
	token, ok := resp["token"].(string)
	if !ok || token == "" {
		t.Fatalf("login response missing token: %v", resp)
	}
	return token
}

// markCredentialsRotated sets password_changed and pin_changed to TRUE for the
// default admin so that existing tests pass through the rotation middleware.
func markCredentialsRotated(t *testing.T) {
	t.Helper()
	_, err := db.Exec(`UPDATE users SET password_changed = TRUE, pin_changed = TRUE WHERE username = 'admin'`)
	if err != nil {
		t.Fatalf("mark credentials rotated: %v", err)
	}
}

// --- Tests ---

// 1. Login with valid credentials -> 200 + JWT
func TestLoginValidCredentials(t *testing.T) {
	e := setupTestServer(t)

	body := `{"username":"admin","password":"bloxos"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if tok, ok := resp["token"].(string); !ok || tok == "" {
		t.Error("response missing 'token' field")
	}
	if _, ok := resp["expires_in"]; !ok {
		t.Error("response missing 'expires_in' field")
	}
	if _, ok := resp["password_change_required"]; !ok {
		t.Error("response missing 'password_change_required' field")
	}
}

// 2. Login with invalid credentials -> 401
func TestLoginInvalidCredentials(t *testing.T) {
	e := setupTestServer(t)

	body := `{"username":"admin","password":"wrongpassword"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

// 2b. Login with nonexistent username -> 401
func TestLoginInvalidUsername(t *testing.T) {
	e := setupTestServer(t)

	body := `{"username":"nonexistent","password":"bloxos"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

// 3. Login rate limiting -> 6th attempt -> 429
func TestLoginRateLimiting(t *testing.T) {
	e := setupTestServer(t)

	body := `{"username":"admin","password":"wrongpassword"}`

	// The rate limiter allows 5 per minute for login.
	// Fire 6 requests - the 6th should be 429.
	var lastCode int
	for i := 0; i < 6; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "10.0.0.1:12345"
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		lastCode = rec.Code
	}

	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on 6th request, got %d", lastCode)
	}
}

// 4. Password change with valid JWT -> 200
func TestChangePasswordWithValidJWT(t *testing.T) {
	e := setupTestServer(t)
	token := loginAndGetToken(t, e)

	body := `{"current_password":"bloxos","new_password":"newpassword123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/change-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify old password no longer works.
	loginBody := `{"username":"admin","password":"bloxos"}`
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(loginBody))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Error("old password should no longer work after change")
	}

	// Verify new password works.
	loginBody2 := `{"username":"admin","password":"newpassword123"}`
	req3 := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(loginBody2))
	req3.Header.Set("Content-Type", "application/json")
	rec3 := httptest.NewRecorder()
	e.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Error("new password should work after change")
	}
}

// 5. PIN change with valid JWT -> 200
func TestChangePINWithValidJWT(t *testing.T) {
	e := setupTestServer(t)
	token := loginAndGetToken(t, e)

	// Default PIN is "1234" - change it.
	body := `{"current_pin":"1234","new_pin":"9999"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/change-pin", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// 6. Token creation (install token) with valid JWT -> 200 + token returned
func TestCreateTokenWithValidJWT(t *testing.T) {
	e := setupTestServer(t)
	token := loginAndGetToken(t, e)
	markCredentialsRotated(t)

	req := httptest.NewRequest(http.MethodPost, "/api/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := resp["token"].(string); !ok {
		t.Error("response missing 'token' field")
	}
	if _, ok := resp["expires_at"].(string); !ok {
		t.Error("response missing 'expires_at' field")
	}
	if _, ok := resp["command"].(string); !ok {
		t.Error("response missing 'command' field")
	}
}

// 7. Agent enrollment with valid token - tested via validateAgentToken
func TestValidateAgentTokenValid(t *testing.T) {
	_ = setupTestServer(t)

	rawToken := "test-token-valid-enrollment"
	h := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(h[:])
	expiresAt := time.Now().Add(15 * time.Minute).Format(time.RFC3339)

	_, err := db.Exec(`INSERT INTO tokens (token_hash, expires_at, used) VALUES (?, ?, FALSE)`,
		tokenHash, expiresAt)
	if err != nil {
		t.Fatalf("insert token: %v", err)
	}

	gotHash, valErr := validateAgentToken(rawToken)
	if valErr != nil {
		t.Fatalf("expected valid token, got error: %v", valErr)
	}
	if gotHash != tokenHash {
		t.Errorf("expected hash %s, got %s", tokenHash, gotHash)
	}
}

// 8. Agent enrollment with used token -> rejected
func TestValidateAgentTokenUsed(t *testing.T) {
	_ = setupTestServer(t)

	rawToken := "test-token-used-12345"
	h := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(h[:])
	expiresAt := time.Now().Add(15 * time.Minute).Format(time.RFC3339)

	_, err := db.Exec(`INSERT INTO tokens (token_hash, expires_at, used) VALUES (?, ?, TRUE)`,
		tokenHash, expiresAt)
	if err != nil {
		t.Fatalf("insert token: %v", err)
	}

	_, valErr := validateAgentToken(rawToken)
	if valErr == nil {
		t.Fatal("expected error for used token, got nil")
	}
	if !strings.Contains(valErr.Error(), "already used") {
		t.Errorf("expected 'already used' error, got: %v", valErr)
	}
}

// 9. Agent enrollment with expired token -> rejected
func TestValidateAgentTokenExpired(t *testing.T) {
	_ = setupTestServer(t)

	rawToken := "test-token-expired-12345"
	h := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(h[:])
	expiresAt := time.Now().Add(-1 * time.Hour).Format(time.RFC3339)

	_, err := db.Exec(`INSERT INTO tokens (token_hash, expires_at, used) VALUES (?, ?, FALSE)`,
		tokenHash, expiresAt)
	if err != nil {
		t.Fatalf("insert token: %v", err)
	}

	_, valErr := validateAgentToken(rawToken)
	if valErr == nil {
		t.Fatal("expected error for expired token, got nil")
	}
	if !strings.Contains(valErr.Error(), "expired") {
		t.Errorf("expected 'expired' error, got: %v", valErr)
	}
}

// 10. Terminal session with valid PIN -> not rejected on PIN
// (Without a real WebSocket agent, the handler returns 404 "agent not connected"
// after PIN validation succeeds. We verify it does NOT return 403.)
func TestTerminalSessionValidPIN(t *testing.T) {
	e := setupTestServer(t)
	token := loginAndGetToken(t, e)
	markCredentialsRotated(t)

	body := fmt.Sprintf(`{"pin":"1234"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/machines/nonexistent-machine/terminal", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	// With no agent connected, we expect 404 ("agent not connected").
	// If we got 403, the PIN was rejected - that would be a failure.
	if rec.Code == http.StatusForbidden {
		t.Fatalf("valid PIN was rejected: %s", rec.Body.String())
	}
	if rec.Code != http.StatusNotFound {
		t.Logf("unexpected status %d (expected 404 for missing agent): %s", rec.Code, rec.Body.String())
	}
}

// 11. Terminal session with invalid PIN -> 403
func TestTerminalSessionInvalidPIN(t *testing.T) {
	e := setupTestServer(t)
	token := loginAndGetToken(t, e)
	markCredentialsRotated(t)

	body := `{"pin":"0000"}`
	req := httptest.NewRequest(http.MethodPost, "/api/machines/some-machine/terminal", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for invalid PIN, got %d: %s", rec.Code, rec.Body.String())
	}
}

// 12. Unauthenticated request to protected endpoint -> 401
func TestUnauthenticatedProtectedEndpoint(t *testing.T) {
	e := setupTestServer(t)

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/machines"},
		{http.MethodPost, "/api/tokens"},
		{http.MethodPost, "/api/auth/change-password"},
		{http.MethodPost, "/api/auth/change-pin"},
		{http.MethodGet, "/api/alerts"},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("expected 401 for unauthenticated %s %s, got %d", ep.method, ep.path, rec.Code)
			}
		})
	}
}

// Bonus: Health endpoint is public (no auth required)
func TestHealthEndpointPublic(t *testing.T) {
	e := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// --- Credential Rotation Enforcement Tests (Finding #10) ---

// 13. Default admin (password_changed=false, pin_changed=false) gets 403 on protected endpoint
func TestRotationEnforcement_DefaultAdminBlocked(t *testing.T) {
	e := setupTestServer(t)
	token := loginAndGetToken(t, e)

	req := httptest.NewRequest(http.MethodGet, "/api/machines", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for unrotated credentials, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["error"] != "credentials_not_rotated" {
		t.Errorf("expected error 'credentials_not_rotated', got %v", resp["error"])
	}
	if resp["password_changed"] != false {
		t.Errorf("expected password_changed=false, got %v", resp["password_changed"])
	}
	if resp["pin_changed"] != false {
		t.Errorf("expected pin_changed=false, got %v", resp["pin_changed"])
	}
}

// 14. After changing password AND pin, protected endpoints return 200
func TestRotationEnforcement_FullyRotatedAllowed(t *testing.T) {
	e := setupTestServer(t)
	token := loginAndGetToken(t, e)
	markCredentialsRotated(t)

	req := httptest.NewRequest(http.MethodGet, "/api/machines", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for rotated credentials, got %d: %s", rec.Code, rec.Body.String())
	}
}

// 15. Allowlisted endpoints work even when rotation is incomplete
func TestRotationEnforcement_AllowlistBypass(t *testing.T) {
	e := setupTestServer(t)
	token := loginAndGetToken(t, e)

	// change-password should work (allowlisted)
	body := `{"current_password":"bloxos","new_password":"newpassword123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/change-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for allowlisted change-password, got %d: %s", rec.Code, rec.Body.String())
	}

	// change-pin should work (allowlisted)
	body2 := `{"current_pin":"1234","new_pin":"5678"}`
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/change-pin", strings.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+token)
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)

	// change-password changed the password, need to re-login for a valid token
	// but the PIN change uses verifyTerminalPIN which doesn't check JWT user
	// The token is still valid (JWT hasn't expired), so this should work
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 for allowlisted change-pin, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

// 16. Partial rotation (only password changed, not PIN) still blocks
func TestRotationEnforcement_PartialRotationBlocked(t *testing.T) {
	e := setupTestServer(t)
	token := loginAndGetToken(t, e)

	// Only mark password as changed
	_, err := db.Exec(`UPDATE users SET password_changed = TRUE WHERE username = 'admin'`)
	if err != nil {
		t.Fatalf("update password_changed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/machines", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for partial rotation (pin not changed), got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["password_changed"] != true {
		t.Errorf("expected password_changed=true, got %v", resp["password_changed"])
	}
	if resp["pin_changed"] != false {
		t.Errorf("expected pin_changed=false, got %v", resp["pin_changed"])
	}
}

// --- First-Boot Setup Tests (Finding #11) ---

// 17. Setup status returns needs_setup=true when no users exist
func TestSetupStatusNeedsSetup(t *testing.T) {
	e := setupEmptyTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["needs_setup"] != true {
		t.Errorf("expected needs_setup=true, got %v", resp["needs_setup"])
	}
}

// 18. Setup status returns needs_setup=false when users exist
func TestSetupStatusAlreadySetup(t *testing.T) {
	e := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["needs_setup"] != false {
		t.Errorf("expected needs_setup=false, got %v", resp["needs_setup"])
	}
}

// 19. Setup with valid token creates admin user
func TestSetupWithValidToken(t *testing.T) {
	e := setupEmptyTestServer(t)

	body := `{"setup_token":"test-setup-token-abc123","username":"myadmin","password":"securepass123","pin":"5678"}`
	req := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["message"] != "Setup complete" {
		t.Errorf("expected message 'Setup complete', got %v", resp["message"])
	}
	if resp["username"] != "myadmin" {
		t.Errorf("expected username 'myadmin', got %v", resp["username"])
	}

	// Verify the user was created with correct flags.
	var passwordChanged, pinChanged bool
	err := db.QueryRow(`SELECT password_changed, pin_changed FROM users WHERE username = 'myadmin'`).Scan(&passwordChanged, &pinChanged)
	if err != nil {
		t.Fatalf("query user: %v", err)
	}
	if !passwordChanged {
		t.Error("expected password_changed=true")
	}
	if !pinChanged {
		t.Error("expected pin_changed=true")
	}

	// Verify login works with new credentials.
	loginBody := `{"username":"myadmin","password":"securepass123"}`
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(loginBody))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("login with setup credentials failed: %d: %s", rec2.Code, rec2.Body.String())
	}

	// Verify setup status now says false.
	req3 := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	rec3 := httptest.NewRecorder()
	e.ServeHTTP(rec3, req3)
	var statusResp map[string]interface{}
	json.Unmarshal(rec3.Body.Bytes(), &statusResp)
	if statusResp["needs_setup"] != false {
		t.Error("expected needs_setup=false after setup")
	}
}

// 20. Setup with invalid token returns 403
func TestSetupWithInvalidToken(t *testing.T) {
	e := setupEmptyTestServer(t)

	body := `{"setup_token":"wrong-token","username":"admin","password":"securepass123","pin":"5678"}`
	req := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

// 21. Setup after admin already exists returns 404
func TestSetupAfterAlreadySetup(t *testing.T) {
	e := setupTestServer(t)

	body := `{"setup_token":"anything","username":"admin2","password":"securepass123","pin":"5678"}`
	req := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// 22. Setup rate limiting - 6th attempt returns 429
func TestSetupRateLimiting(t *testing.T) {
	e := setupEmptyTestServer(t)

	body := `{"setup_token":"wrong-token","username":"admin","password":"securepass123","pin":"5678"}`

	var lastCode int
	for i := 0; i < 6; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "10.0.0.99:12345"
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		lastCode = rec.Code
	}

	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on 6th request, got %d", lastCode)
	}
}

// 23. Setup validation - password too short
func TestSetupValidation_ShortPassword(t *testing.T) {
	e := setupEmptyTestServer(t)

	body := `{"setup_token":"test-setup-token-abc123","username":"admin","password":"short","pin":"5678"}`
	req := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for short password, got %d: %s", rec.Code, rec.Body.String())
	}
}

// 24. Setup validation - invalid PIN (non-numeric)
func TestSetupValidation_InvalidPIN(t *testing.T) {
	e := setupEmptyTestServer(t)

	body := `{"setup_token":"test-setup-token-abc123","username":"admin","password":"securepass123","pin":"abcd"}`
	req := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-numeric PIN, got %d: %s", rec.Code, rec.Body.String())
	}
}

// 25. Setup validation - empty username
func TestSetupValidation_EmptyUsername(t *testing.T) {
	e := setupEmptyTestServer(t)

	body := `{"setup_token":"test-setup-token-abc123","username":"","password":"securepass123","pin":"5678"}`
	req := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty username, got %d: %s", rec.Code, rec.Body.String())
	}
}


// --- Enrollment Tests ---

// wsDialAgent is a helper that upgrades to a WebSocket using the test server's agent endpoint.
func wsDialAgent(t *testing.T, server *httptest.Server, queryParams string) (*websocket.Conn, error) {
	t.Helper()
	// Convert http:// to ws://
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/agent?" + queryParams
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	return conn, err
}

// seedValidToken inserts a valid token into the DB and returns the raw token string.

// waitAgentDrain waits until the handleAgentWS goroutine has fully exited for
// the given machine_id. This prevents race conditions where the background
// goroutine calls markOffline on a stale or closed db.
func waitAgentDrain(t *testing.T, machineID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		agentsMu.RLock()
		_, exists := agents[machineID]
		agentsMu.RUnlock()
		if !exists {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Not fatal — the goroutine may not have registered the machine yet
	t.Logf("waitAgentDrain: %s still in agents map after %v (may not have registered)", machineID, timeout)
}

func seedValidToken(t *testing.T) string {
	t.Helper()
	rawToken := "test-enrollment-token-abc123"
	h := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(h[:])
	expiresAt := time.Now().Add(1 * time.Hour).Format("2006-01-02 15:04:05")
	_, err := db.Exec(`INSERT INTO tokens (token_hash, expires_at, used) VALUES (?, ?, FALSE)`, tokenHash, expiresAt)
	if err != nil {
		t.Fatalf("seed valid token: %v", err)
	}
	return rawToken
}

// sendMetricsMsg sends a metrics message with the given machine_id and reads back any response.
func sendMetricsMsg(t *testing.T, conn *websocket.Conn, machineID string) {
	t.Helper()
	metrics := map[string]interface{}{
		"type":             "metrics",
		"machine_id":       machineID,
		"hostname":         "test-host",
		"ip":               "10.0.0.1",
		"os":               "linux/amd64",
		"cpu_percent":      25.0,
		"ram_used_bytes":   1073741824,
		"ram_total_bytes":  4294967296,
		"disk_used_bytes":  10737418240,
		"disk_total_bytes": 107374182400,
		"timestamp":        time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(metrics)
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("write metrics: %v", err)
	}
}

func TestAgentEnrollmentWithToken(t *testing.T) {
	e := setupTestServer(t)
	token := seedValidToken(t)

	// Start real HTTP server for WebSocket upgrade.
	server := httptest.NewServer(e)

	// Connect with valid token.
	conn, err := wsDialAgent(t, server, "token="+token)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}

	// Send first metrics to trigger enrollment.
	sendMetricsMsg(t, conn, "test-machine-enroll-001")

	// Read the enrollment response.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read enrolled message: %v", err)
	}

	var enrolled struct {
		Type        string `json:"type"`
		AgentSecret string `json:"agent_secret"`
	}
	if err := json.Unmarshal(msg, &enrolled); err != nil {
		t.Fatalf("unmarshal enrolled: %v", err)
	}
	if enrolled.Type != "enrolled" {
		t.Fatalf("expected type=enrolled, got %s (msg: %s)", enrolled.Type, string(msg))
	}
	if enrolled.AgentSecret == "" {
		t.Fatal("enrolled message missing agent_secret")
	}
	if len(enrolled.AgentSecret) != 64 { // 32 bytes hex-encoded
		t.Fatalf("expected 64-char hex secret, got %d chars", len(enrolled.AgentSecret))
	}

	// Close connection and wait for handler goroutine to finish.
	conn.Close()
	waitAgentDrain(t, "test-machine-enroll-001", 2*time.Second)
	server.Close()

	// Verify token was consumed.
	h := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(h[:])
	var used bool
	err = db.QueryRow(`SELECT used FROM tokens WHERE token_hash = ?`, tokenHash).Scan(&used)
	if err != nil {
		t.Fatalf("query token: %v", err)
	}
	if !used {
		t.Fatal("token should be marked as used after enrollment")
	}

	// Verify credential was stored in DB.
	var storedMachineID string
	err = db.QueryRow(`SELECT machine_id FROM agent_credentials LIMIT 1`).Scan(&storedMachineID)
	if err != nil {
		t.Fatalf("query agent_credentials: %v", err)
	}
	if storedMachineID != "test-machine-enroll-001" {
		t.Fatalf("expected machine_id=test-machine-enroll-001, got %s", storedMachineID)
	}

	t.Logf("enrollment successful: secret=%s... machine=%s", enrolled.AgentSecret[:8], storedMachineID)
}

func TestAgentReconnectWithSecret(t *testing.T) {
	e := setupTestServer(t)
	token := seedValidToken(t)

	server := httptest.NewServer(e)

	// Step 1: Enroll with token to get a secret.
	conn1, err := wsDialAgent(t, server, "token="+token)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}

	sendMetricsMsg(t, conn1, "test-machine-reconnect-001")

	conn1.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn1.ReadMessage()
	if err != nil {
		t.Fatalf("read enrolled message: %v", err)
	}

	var enrolled struct {
		Type        string `json:"type"`
		AgentSecret string `json:"agent_secret"`
	}
	json.Unmarshal(msg, &enrolled)
	if enrolled.AgentSecret == "" {
		t.Fatal("no secret received during enrollment")
	}
	conn1.Close()

	// Step 2: Reconnect using the secret.
	conn2, err := wsDialAgent(t, server, "secret="+enrolled.AgentSecret)
	if err != nil {
		t.Fatalf("reconnect with secret failed: %v", err)
	}

	// Send metrics to confirm connection works.
	sendMetricsMsg(t, conn2, "test-machine-reconnect-001")

	// The connection should stay open (no error message).
	// Try reading - we should NOT get an error/disconnect.
	// Send another metrics and verify no error.
	sendMetricsMsg(t, conn2, "test-machine-reconnect-001")

	t.Log("reconnection with secret successful")

	conn2.Close()
	waitAgentDrain(t, "test-machine-reconnect-001", 2*time.Second)
	server.Close()
}

func TestAgentReconnectWithInvalidSecret(t *testing.T) {
	e := setupTestServer(t)

	server := httptest.NewServer(e)

	// Try connecting with a random invalid secret.
	_, err := wsDialAgent(t, server, "secret=0000111122223333444455556666777788889999aaaabbbbccccddddeeeeffff")
	if err == nil {
		t.Fatal("expected connection to be rejected with invalid secret")
	}
	// The hub should return 401 before upgrading to WebSocket.
	t.Logf("correctly rejected invalid secret: %v", err)

	server.Close()
}

func TestAgentSecretRevocation(t *testing.T) {
	e := setupTestServer(t)
	token := seedValidToken(t)

	server := httptest.NewServer(e)

	// Step 1: Enroll to get a secret.
	conn1, err := wsDialAgent(t, server, "token="+token)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}

	sendMetricsMsg(t, conn1, "test-machine-revoke-001")

	conn1.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn1.ReadMessage()
	if err != nil {
		t.Fatalf("read enrolled message: %v", err)
	}

	var enrolled struct {
		Type        string `json:"type"`
		AgentSecret string `json:"agent_secret"`
	}
	json.Unmarshal(msg, &enrolled)
	conn1.Close()
	waitAgentDrain(t, "test-machine-revoke-001", 2*time.Second)

	// Step 2: Revoke the credential.
	if err := revokeAgentCredential("test-machine-revoke-001"); err != nil {
		t.Fatalf("revoke credential: %v", err)
	}

	// Verify it was deleted.
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM agent_credentials WHERE machine_id = ?`, "test-machine-revoke-001").Scan(&count)
	if count != 0 {
		t.Fatal("credential should be deleted after revocation")
	}

	// Step 3: Try reconnecting with the revoked secret.
	_, err = wsDialAgent(t, server, "secret="+enrolled.AgentSecret)
	if err == nil {
		t.Fatal("expected connection to be rejected after revocation")
	}
	t.Logf("correctly rejected revoked secret: %v", err)

	server.Close()
}

// TestTerminalMaxConcurrentSessions verifies that the 4th terminal session is rejected.
func TestTerminalMaxConcurrentSessions(t *testing.T) {
	e := setupTestServer(t)
	token := loginAndGetToken(t, e)
	markCredentialsRotated(t)

	// Seed a fake agent so handleStartTerminal doesn't return 404.
	machineID := "test-machine-max-sessions"
	db.Exec(`INSERT INTO machines (id, hostname, status) VALUES (?, ?, ?)`, machineID, "test-host", "online")
	agentsMu.Lock()
	agents[machineID] = &ConnectedAgent{MachineID: machineID}
	agentsMu.Unlock()
	defer func() {
		agentsMu.Lock()
		delete(agents, machineID)
		agentsMu.Unlock()
	}()

	// Pre-populate 3 terminal sessions in the in-memory map.
	termSessionsMu.Lock()
	for i := 0; i < 3; i++ {
		sid := fmt.Sprintf("fake-session-%d", i)
		termSessions[sid] = &TerminalSession{
			ID:           sid,
			MachineID:    "some-machine",
			CreatedAt:    time.Now(),
			LastActivity: time.Now(),
		}
	}
	termSessionsMu.Unlock()
	defer func() {
		termSessionsMu.Lock()
		for i := 0; i < 3; i++ {
			delete(termSessions, fmt.Sprintf("fake-session-%d", i))
		}
		termSessionsMu.Unlock()
	}()

	// Attempt to create a 4th session — should be rejected with 429.
	body := `{"pin":"1234"}`
	req := httptest.NewRequest(http.MethodPost, "/api/machines/"+machineID+"/terminal", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for max concurrent sessions, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["error"] != "max concurrent terminal sessions reached (3)" {
		t.Errorf("unexpected error message: %s", resp["error"])
	}
}


// TestTerminalAuditFields verifies source_ip and user_id are stored in the terminal_sessions table.
func TestTerminalAuditFields(t *testing.T) {
	e := setupTestServer(t)
	token := loginAndGetToken(t, e)
	markCredentialsRotated(t)

	machineID := "test-machine-audit"
	db.Exec(`INSERT INTO machines (id, hostname, status) VALUES (?, ?, ?)`, machineID, "audit-host", "online")

	// Create a dummy WebSocket server so the agent has a real Conn.
	dummyWS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				break
			}
		}
	}))
	defer dummyWS.Close()

	wsURL := "ws" + dummyWS.URL[4:]
	agentConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial dummy ws: %v", err)
	}
	defer agentConn.Close()

	agentsMu.Lock()
	agents[machineID] = &ConnectedAgent{MachineID: machineID, Conn: agentConn}
	agentsMu.Unlock()
	defer func() {
		agentsMu.Lock()
		delete(agents, machineID)
		agentsMu.Unlock()
	}()

	body := `{"pin":"1234"}`
	req := httptest.NewRequest(http.MethodPost, "/api/machines/"+machineID+"/terminal", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Forwarded-For", "10.20.30.40")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var sourceIP, userID sql.NullString
	err = db.QueryRow(`SELECT source_ip, user_id FROM terminal_sessions WHERE machine_id = ?`, machineID).Scan(&sourceIP, &userID)
	if err != nil {
		t.Fatalf("query terminal_sessions: %v", err)
	}

	if !sourceIP.Valid || sourceIP.String == "" {
		t.Error("source_ip should be populated in terminal_sessions")
	}
	if !userID.Valid || userID.String == "" {
		t.Error("user_id should be populated in terminal_sessions")
	}
	if userID.Valid && userID.String != "test-admin-id" {
		t.Errorf("expected user_id 'test-admin-id', got '%s'", userID.String)
	}
	t.Logf("audit fields: source_ip=%s user_id=%s", sourceIP.String, userID.String)

	// Clean up any terminal sessions created.
	termSessionsMu.Lock()
	for k := range termSessions {
		delete(termSessions, k)
	}
	termSessionsMu.Unlock()
}
