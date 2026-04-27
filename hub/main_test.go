package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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
	pinHash, err := bcrypt.GenerateFromPassword([]byte("1234"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash pin: %v", err)
	}
	id := "test-admin-id"
	_, err = db.Exec(`INSERT INTO users (id, username, password_hash, terminal_pin_hash) VALUES (?, ?, ?, ?)`,
		id, "admin", string(hash), string(pinHash))
	if err != nil {
		t.Fatalf("seed test admin: %v", err)
	}
}

func seedTestUser(t *testing.T, username, password, pin string, role UserRole, passwordChanged, pinChanged bool) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	pinHash, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash pin: %v", err)
	}
	id := "test-user-" + username
	_, err = db.Exec(
		`INSERT INTO users (id, username, password_hash, terminal_pin_hash, password_changed, pin_changed, role) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, username, string(hash), string(pinHash), passwordChanged, pinChanged, role,
	)
	if err != nil {
		t.Fatalf("seed test user: %v", err)
	}
	return id
}

func seedTestMachine(t *testing.T, id string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO machines (id, hostname, status) VALUES (?, ?, 'online')`, id, "machine-"+id)
	if err != nil {
		t.Fatalf("seed test machine: %v", err)
	}
}

func generateTestCertPEM(t *testing.T) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "bloxos-test-ca",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create test cert: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
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
	// SQLite ":memory:" gives each *connection* its own database. database/sql's
	// pool can spin up extra connections under concurrent load (e.g. a
	// handleAgentWS goroutine racing the test's own queries), and those new
	// connections see an empty schema — manifests as "no such table:
	// agent_credentials" flakes. Pin to one connection so all queries hit the
	// same in-memory DB.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	// Phase 11 — match production: enable SQLite foreign-key enforcement so
	// ON DELETE CASCADE behaves the same in tests as in the live binary.
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("enable foreign_keys: %v", err)
	}

	if err := runMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	seedTestAdmin(t)
	jwtSecret = []byte("test-secret-key-for-smoke-tests")
	rateLimiter = NewRateLimiter()

	e := echo.New()
	e.HideBanner = true
	registerRoutes(e)

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
	// SQLite ":memory:" gives each *connection* its own database. database/sql's
	// pool can spin up extra connections under concurrent load (e.g. a
	// handleAgentWS goroutine racing the test's own queries), and those new
	// connections see an empty schema — manifests as "no such table:
	// agent_credentials" flakes. Pin to one connection so all queries hit the
	// same in-memory DB.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	// Phase 11 — match production: enable SQLite foreign-key enforcement so
	// ON DELETE CASCADE behaves the same in tests as in the live binary.
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatalf("enable foreign_keys: %v", err)
	}

	if err := runMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	jwtSecret = []byte("test-secret-key-for-smoke-tests")
	rateLimiter = NewRateLimiter()
	setupTokenValue = "test-setup-token-abc123"

	e := echo.New()
	e.HideBanner = true
	registerRoutes(e)

	return e
}

// loginAndGetToken performs a login with the default admin credentials and
// returns the JWT token string.
func loginAndGetToken(t *testing.T, e *echo.Echo) string {
	t.Helper()

	return loginAndGetTokenForCredentials(t, e, "admin", "bloxos")
}

func loginAndGetTokenForCredentials(t *testing.T, e *echo.Echo, username, password string) string {
	t.Helper()

	body := fmt.Sprintf(`{"username":%q,"password":%q}`, username, password)
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

func TestAdminCanCreateAndListUsers(t *testing.T) {
	e := setupTestServer(t)
	markCredentialsRotated(t)
	adminToken := loginAndGetToken(t, e)

	createBody := `{"username":"alice","password":"alicepass123","pin":"1234","role":"operator"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(createBody))
	createReq.Header.Set("Authorization", "Bearer "+adminToken)
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	e.ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 on create, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var created userRecord
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	if created.Role != string(RoleOperator) {
		t.Fatalf("expected role operator, got %q", created.Role)
	}
	if created.PasswordChanged || created.PINChanged {
		t.Fatal("new admin-created user should require credential rotation")
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	listReq.Header.Set("Authorization", "Bearer "+adminToken)
	listRec := httptest.NewRecorder()
	e.ServeHTTP(listRec, listReq)

	if listRec.Code != http.StatusOK {
		t.Fatalf("expected 200 on list, got %d: %s", listRec.Code, listRec.Body.String())
	}
	var users []userRecord
	if err := json.Unmarshal(listRec.Body.Bytes(), &users); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users (admin + alice), got %d", len(users))
	}
}

func TestCreateUserRejectsDuplicateUsername(t *testing.T) {
	e := setupTestServer(t)
	markCredentialsRotated(t)
	adminToken := loginAndGetToken(t, e)

	body := `{"username":"admin","password":"anotherpass123","pin":"1234","role":"viewer"}`
	req := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate username, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateUserRejectsInvalidRole(t *testing.T) {
	e := setupTestServer(t)
	markCredentialsRotated(t)
	adminToken := loginAndGetToken(t, e)

	body := `{"username":"bob","password":"bobpass123","pin":"1234","role":"superuser"}`
	req := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid role, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestOperatorCannotManageUsers(t *testing.T) {
	e := setupTestServer(t)
	markCredentialsRotated(t)
	seedTestUser(t, "op1", "operatorpass123", "1234", RoleOperator, true, true)
	opToken := loginAndGetTokenForCredentials(t, e, "op1", "operatorpass123")

	body := `{"username":"charlie","password":"charliepass123","pin":"1234","role":"viewer"}`
	req := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+opToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for operator creating user, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminCanPromoteAndDemoteOthers(t *testing.T) {
	e := setupTestServer(t)
	markCredentialsRotated(t)
	seedTestUser(t, "admin2", "admin2pass123", "1234", RoleAdmin, true, true)
	targetID := seedTestUser(t, "target", "targetpass123", "1234", RoleViewer, true, true)
	adminToken := loginAndGetToken(t, e)

	body := `{"role":"operator"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/users/"+targetID, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on role patch, got %d: %s", rec.Code, rec.Body.String())
	}
	var role string
	if err := db.QueryRow(`SELECT role FROM users WHERE id = ?`, targetID).Scan(&role); err != nil {
		t.Fatalf("fetch target role: %v", err)
	}
	if role != string(RoleOperator) {
		t.Fatalf("expected role operator in db, got %q", role)
	}
}

func TestAdminCannotChangeOwnRole(t *testing.T) {
	e := setupTestServer(t)
	markCredentialsRotated(t)
	seedTestUser(t, "admin2", "admin2pass123", "1234", RoleAdmin, true, true)
	adminToken := loginAndGetToken(t, e)

	body := `{"role":"operator"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/users/test-admin-id", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for self role change, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminCanDemoteAnotherAdmin(t *testing.T) {
	e := setupTestServer(t)
	markCredentialsRotated(t)
	otherID := seedTestUser(t, "other", "otherpass123", "1234", RoleAdmin, true, true)
	adminToken := loginAndGetToken(t, e)

	body := `{"role":"operator"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/users/"+otherID, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected demote of other admin to succeed, got %d: %s", rec.Code, rec.Body.String())
	}
	var role string
	if err := db.QueryRow(`SELECT role FROM users WHERE id = ?`, otherID).Scan(&role); err != nil {
		t.Fatalf("fetch role: %v", err)
	}
	if role != string(RoleOperator) {
		t.Fatalf("expected role operator, got %q", role)
	}
}

func TestAdminCanDeleteOtherUser(t *testing.T) {
	e := setupTestServer(t)
	markCredentialsRotated(t)
	targetID := seedTestUser(t, "victim", "victimpass123", "1234", RoleViewer, true, true)
	adminToken := loginAndGetToken(t, e)

	req := httptest.NewRequest(http.MethodDelete, "/api/users/"+targetID, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on delete, got %d: %s", rec.Code, rec.Body.String())
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE id = ?`, targetID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected target to be deleted, found %d rows", count)
	}
}

func TestAdminCannotDeleteSelf(t *testing.T) {
	e := setupTestServer(t)
	markCredentialsRotated(t)
	adminToken := loginAndGetToken(t, e)

	req := httptest.NewRequest(http.MethodDelete, "/api/users/test-admin-id", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on self delete, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminCanDeleteAnotherAdmin(t *testing.T) {
	e := setupTestServer(t)
	markCredentialsRotated(t)
	otherID := seedTestUser(t, "other", "otherpass123", "1234", RoleAdmin, true, true)
	adminToken := loginAndGetToken(t, e)

	req := httptest.NewRequest(http.MethodDelete, "/api/users/"+otherID, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on delete other admin, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRBACRouteAuditPassesForProductionRoutes(t *testing.T) {
	e := echo.New()
	e.HideBanner = true
	registerRoutes(e)

	if err := auditRBACRouteCoverage(e, routeScopeRequirements); err != nil {
		t.Fatalf("production route set failed RBAC audit: %v", err)
	}
}

func TestRBACRouteAuditDetectsMissingMapping(t *testing.T) {
	e := echo.New()
	e.HideBanner = true
	registerRoutes(e)
	// Add an extra protected route that has no scope mapping.
	api := e.Group("", jwtMiddleware, credentialRotationMiddleware, permissionMiddleware)
	api.GET("/api/ghost", func(c echo.Context) error { return nil })

	err := auditRBACRouteCoverage(e, routeScopeRequirements)
	if err == nil {
		t.Fatal("expected audit to fail for unmapped protected route, got nil")
	}
	if !strings.Contains(err.Error(), "missing scope mapping") || !strings.Contains(err.Error(), "/api/ghost") {
		t.Fatalf("expected error to name the unmapped route, got: %v", err)
	}
}

func TestRBACRouteAuditDetectsOrphanMapping(t *testing.T) {
	e := echo.New()
	e.HideBanner = true
	registerRoutes(e)

	// Clone production requirements and add an entry for a route that isn't registered.
	requirements := make(map[string]string, len(routeScopeRequirements)+1)
	for k, v := range routeScopeRequirements {
		requirements[k] = v
	}
	requirements[routeScopeKey(http.MethodGet, "/api/nonexistent")] = scopeFleetRead

	err := auditRBACRouteCoverage(e, requirements)
	if err == nil {
		t.Fatal("expected audit to fail for orphan scope mapping, got nil")
	}
	if !strings.Contains(err.Error(), "orphan scope mapping") || !strings.Contains(err.Error(), "/api/nonexistent") {
		t.Fatalf("expected error to name the orphan route, got: %v", err)
	}
}

func TestLoginIncludesRoleAndScopes(t *testing.T) {
	e := setupTestServer(t)
	markCredentialsRotated(t)
	seedTestUser(t, "viewer1", "viewerpass123", "1234", RoleViewer, true, true)

	body := `{"username":"viewer1","password":"viewerpass123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Role   string   `json:"role"`
		Scopes []string `json:"scopes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal login response: %v", err)
	}
	if resp.Role != string(RoleViewer) {
		t.Fatalf("expected role %q, got %q", RoleViewer, resp.Role)
	}
	if !slices.Contains(resp.Scopes, scopeFleetRead) {
		t.Fatalf("expected scopes to include %q, got %v", scopeFleetRead, resp.Scopes)
	}
	if slices.Contains(resp.Scopes, scopeFleetControl) {
		t.Fatalf("expected viewer scopes to exclude %q, got %v", scopeFleetControl, resp.Scopes)
	}
}

func TestViewerCanReadMachinesButCannotCreateInstallToken(t *testing.T) {
	e := setupTestServer(t)
	markCredentialsRotated(t)
	seedTestUser(t, "viewer2", "viewerpass123", "1234", RoleViewer, true, true)
	viewerToken := loginAndGetTokenForCredentials(t, e, "viewer2", "viewerpass123")

	readReq := httptest.NewRequest(http.MethodGet, "/api/machines", nil)
	readReq.Header.Set("Authorization", "Bearer "+viewerToken)
	readRec := httptest.NewRecorder()
	e.ServeHTTP(readRec, readReq)

	if readRec.Code != http.StatusOK {
		t.Fatalf("expected viewer machine read to succeed, got %d: %s", readRec.Code, readRec.Body.String())
	}

	writeReq := httptest.NewRequest(http.MethodPost, "/api/tokens", nil)
	writeReq.Header.Set("Authorization", "Bearer "+viewerToken)
	writeRec := httptest.NewRecorder()
	e.ServeHTTP(writeRec, writeReq)

	if writeRec.Code != http.StatusForbidden {
		t.Fatalf("expected viewer install token create to be forbidden, got %d: %s", writeRec.Code, writeRec.Body.String())
	}
	if !strings.Contains(writeRec.Body.String(), scopeTokensAdmin) {
		t.Fatalf("expected forbidden response to mention scope %q, got %s", scopeTokensAdmin, writeRec.Body.String())
	}
}

func TestOperatorCanUpdateTagsButCannotDeleteMachine(t *testing.T) {
	e := setupTestServer(t)
	markCredentialsRotated(t)
	seedTestUser(t, "operator1", "operatorpass123", "1234", RoleOperator, true, true)
	seedTestMachine(t, "machine-1")
	operatorToken := loginAndGetTokenForCredentials(t, e, "operator1", "operatorpass123")

	updateReq := httptest.NewRequest(http.MethodPut, "/api/machines/machine-1/tags", strings.NewReader(`{"tags":["gpu","lab"]}`))
	updateReq.Header.Set("Authorization", "Bearer "+operatorToken)
	updateReq.Header.Set("Content-Type", "application/json")
	updateRec := httptest.NewRecorder()
	e.ServeHTTP(updateRec, updateReq)

	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected operator tag update to succeed, got %d: %s", updateRec.Code, updateRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/machines/machine-1", nil)
	deleteReq.Header.Set("Authorization", "Bearer "+operatorToken)
	deleteRec := httptest.NewRecorder()
	e.ServeHTTP(deleteRec, deleteReq)

	if deleteRec.Code != http.StatusForbidden {
		t.Fatalf("expected operator delete to be forbidden, got %d: %s", deleteRec.Code, deleteRec.Body.String())
	}
	if !strings.Contains(deleteRec.Body.String(), scopeFleetAdmin) {
		t.Fatalf("expected forbidden response to mention scope %q, got %s", scopeFleetAdmin, deleteRec.Body.String())
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

func TestCreateTokenIncludesCABootstrapForHTTPS(t *testing.T) {
	e := setupTestServer(t)
	token := loginAndGetToken(t, e)
	markCredentialsRotated(t)

	caDir := t.TempDir()
	caPath := filepath.Join(caDir, "root.crt")
	caPEM := []byte("-----BEGIN CERTIFICATE-----\nZmFrZS1ibG94b3MtY2E=\n-----END CERTIFICATE-----\n")
	if err := os.WriteFile(caPath, caPEM, 0600); err != nil {
		t.Fatalf("write CA cert: %v", err)
	}

	t.Setenv("PUBLIC_URL", "https://bloxos.example")
	t.Setenv("BLOXOS_CA_CERT", caPath)

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

	command, _ := resp["command"].(string)
	if !strings.Contains(command, "BLOXOS_CA_URL=https://bloxos.example/download/ca.crt") {
		t.Fatalf("expected command to include CA URL, got %q", command)
	}
	if !strings.Contains(command, "BLOXOS_CA_SHA256=") {
		t.Fatalf("expected command to include CA SHA256, got %q", command)
	}
	if !strings.Contains(command, "curl -fsSLk https://bloxos.example/install.sh | bash") {
		t.Fatalf("expected HTTPS bootstrap command with curl -k, got %q", command)
	}
	if resp["ca_url"] != "https://bloxos.example/download/ca.crt" {
		t.Fatalf("unexpected ca_url: %v", resp["ca_url"])
	}
	if _, ok := resp["ca_sha256"].(string); !ok {
		t.Fatalf("response missing ca_sha256")
	}
}

func TestDownloadCACert(t *testing.T) {
	e := setupTestServer(t)

	caDir := t.TempDir()
	caPath := filepath.Join(caDir, "root.crt")
	caPEM := []byte("-----BEGIN CERTIFICATE-----\nZmFrZS1ibG94b3MtY2E=\n-----END CERTIFICATE-----\n")
	if err := os.WriteFile(caPath, caPEM, 0600); err != nil {
		t.Fatalf("write CA cert: %v", err)
	}
	t.Setenv("BLOXOS_CA_CERT", caPath)

	req := httptest.NewRequest(http.MethodGet, "/download/ca.crt", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != string(caPEM) {
		t.Fatalf("unexpected CA body: %q", rec.Body.String())
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

// TestAgentVersionAnnouncedOnReconnect locks in Phase 8's auto-update path:
// when an agent reconnects via durable secret, the hub MUST send an
// agent_version frame so an out-of-date binary can self-update. The original
// Phase 8 wired the announce only into the new-enrolment branch, leaving
// every existing fleet member stuck on the old binary across hub redeploys.
func TestAgentVersionAnnouncedOnReconnect(t *testing.T) {
	e := setupTestServer(t)
	token := seedValidToken(t)

	const stagedSHA = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	hubAgentBinaryMu.Lock()
	prevSHA := hubAgentBinarySHA
	hubAgentBinarySHA = stagedSHA
	hubAgentBinaryMu.Unlock()
	t.Cleanup(func() {
		hubAgentBinaryMu.Lock()
		hubAgentBinarySHA = prevSHA
		hubAgentBinaryMu.Unlock()
	})

	server := httptest.NewServer(e)
	defer server.Close()

	// Step 1: enrol via token, capture the durable secret. The enrolment
	// path also calls registerAgentConnection so an agent_version frame may
	// arrive on conn1 too — drain everything until we see the enrolled msg.
	conn1, err := wsDialAgent(t, server, "token="+token)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	sendMetricsMsg(t, conn1, "test-machine-version-announce")

	var secret string
	conn1.SetReadDeadline(time.Now().Add(5 * time.Second))
	for secret == "" {
		_, msg, err := conn1.ReadMessage()
		if err != nil {
			t.Fatalf("read on enrol conn: %v", err)
		}
		var probe struct {
			Type        string `json:"type"`
			AgentSecret string `json:"agent_secret"`
		}
		if err := json.Unmarshal(msg, &probe); err != nil {
			continue
		}
		if probe.Type == "enrolled" {
			secret = probe.AgentSecret
		}
	}
	if secret == "" {
		t.Fatal("enrolment did not yield a secret")
	}
	conn1.Close()
	waitAgentDrain(t, "test-machine-version-announce", 2*time.Second)

	// Step 2: reconnect via secret. The fix under test: the hub MUST send
	// an agent_version frame off the back of registerAgentConnection.
	conn2, err := wsDialAgent(t, server, "secret="+secret)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	defer conn2.Close()

	conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn2.ReadMessage()
	if err != nil {
		t.Fatalf("expected agent_version frame on reconnect, read err: %v", err)
	}
	var versionMsg struct {
		Type   string `json:"type"`
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(msg, &versionMsg); err != nil {
		t.Fatalf("parse version frame: %v (raw=%q)", err, string(msg))
	}
	if versionMsg.Type != "agent_version" {
		t.Fatalf("expected type=agent_version, got %q (raw=%q)", versionMsg.Type, string(msg))
	}
	if versionMsg.SHA256 != stagedSHA {
		t.Fatalf("expected sha256=%s, got %s", stagedSHA, versionMsg.SHA256)
	}
}

// TestAnnouncedSHAIsPerPlatform locks in Phase 9's per-OS SHA routing:
// a Windows agent must receive the Windows binary SHA, not the Linux
// SHA. Half-implementing this is the failure mode where Windows agents
// see the Linux SHA, mistake it for "out of date", download the Linux
// binary on Windows, fail, and update-loop forever.
func TestAnnouncedSHAIsPerPlatform(t *testing.T) {
	const linuxSHA = "1111111111111111111111111111111111111111111111111111111111111111"
	const windowsSHA = "2222222222222222222222222222222222222222222222222222222222222222"

	hubAgentBinaryMu.Lock()
	prevLinux := hubAgentBinarySHA
	prevWindows := hubWindowsAgentBinarySHA
	hubAgentBinarySHA = linuxSHA
	hubWindowsAgentBinarySHA = windowsSHA
	hubAgentBinaryMu.Unlock()
	t.Cleanup(func() {
		hubAgentBinaryMu.Lock()
		hubAgentBinarySHA = prevLinux
		hubWindowsAgentBinarySHA = prevWindows
		hubAgentBinaryMu.Unlock()
	})

	if got := announcedSHAFor("linux"); got != linuxSHA {
		t.Fatalf("announcedSHAFor(linux) = %s, want %s", got, linuxSHA)
	}
	if got := announcedSHAFor("windows"); got != windowsSHA {
		t.Fatalf("announcedSHAFor(windows) = %s, want %s", got, windowsSHA)
	}
	// Empty / unknown OS should fall back to the Linux SHA so legacy
	// agents stay coherent.
	if got := announcedSHAFor(""); got != linuxSHA {
		t.Fatalf("announcedSHAFor(\"\") = %s, want %s (fallback)", got, linuxSHA)
	}
}

// TestRecordAgentRunningVersionTracksOS verifies that when an agent
// reports its running version with an `os` field, the per-machine
// record is keyed against that OS so subsequent announces route to the
// right binary.
func TestRecordAgentRunningVersionTracksOS(t *testing.T) {
	// Use the test setup so the in-memory DB exists for lookupVersionHostname.
	_ = setupTestServer(t)

	const linuxSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const windowsSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	hubAgentBinaryMu.Lock()
	prevLinux := hubAgentBinarySHA
	prevWindows := hubWindowsAgentBinarySHA
	hubAgentBinarySHA = linuxSHA
	hubWindowsAgentBinarySHA = windowsSHA
	hubAgentBinaryMu.Unlock()
	t.Cleanup(func() {
		hubAgentBinaryMu.Lock()
		hubAgentBinarySHA = prevLinux
		hubWindowsAgentBinarySHA = prevWindows
		hubAgentBinaryMu.Unlock()
	})

	// Cleanup test entries from the in-memory map.
	t.Cleanup(func() {
		agentRunningVersionsMu.Lock()
		delete(agentRunningVersions, "test-machine-windows")
		delete(agentRunningVersions, "test-machine-linux")
		agentRunningVersionsMu.Unlock()
	})

	// A Windows agent reporting the Windows SHA is up-to-date.
	recordAgentRunningVersion("test-machine-windows", windowsSHA, "windows")
	agentRunningVersionsMu.RLock()
	winInfo := agentRunningVersions["test-machine-windows"]
	agentRunningVersionsMu.RUnlock()
	if winInfo.OS != "windows" {
		t.Fatalf("windows agent OS = %q, want windows", winInfo.OS)
	}
	if winInfo.UpdatePending {
		t.Fatalf("windows agent reporting matching SHA must not be UpdatePending")
	}

	// A Windows agent reporting the LINUX SHA must be flagged as pending —
	// otherwise the symptom (perpetual update loop) cannot be detected.
	recordAgentRunningVersion("test-machine-windows", linuxSHA, "windows")
	agentRunningVersionsMu.RLock()
	winInfo = agentRunningVersions["test-machine-windows"]
	agentRunningVersionsMu.RUnlock()
	if !winInfo.UpdatePending {
		t.Fatalf("windows agent reporting linux SHA must be UpdatePending")
	}

	// And vice versa for a Linux agent on the linux SHA.
	recordAgentRunningVersion("test-machine-linux", linuxSHA, "linux")
	agentRunningVersionsMu.RLock()
	linInfo := agentRunningVersions["test-machine-linux"]
	agentRunningVersionsMu.RUnlock()
	if linInfo.UpdatePending {
		t.Fatalf("linux agent reporting matching SHA must not be UpdatePending")
	}
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

func TestKnownMachineReconnectWithoutCredentialRejected(t *testing.T) {
	e := setupTestServer(t)

	if _, err := db.Exec(`INSERT INTO machines (id, hostname, status) VALUES (?, ?, ?)`,
		"known-machine-without-secret", "known-host", "online"); err != nil {
		t.Fatalf("seed known machine: %v", err)
	}

	server := httptest.NewServer(e)
	defer server.Close()

	conn, err := wsDialAgent(t, server, "token=definitely-invalid")
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	sendMetricsMsg(t, conn, "known-machine-without-secret")

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("expected rejection message, got read error: %v", err)
	}
	if !strings.Contains(string(msg), "durable credential required") {
		t.Fatalf("expected durable credential rejection, got %s", string(msg))
	}
}

func TestTerminalSessionValidPINForSetupUser(t *testing.T) {
	e := setupEmptyTestServer(t)

	setupBody := `{"setup_token":"test-setup-token-abc123","username":"myadmin","password":"securepass123","pin":"5678"}`
	setupReq := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(setupBody))
	setupReq.Header.Set("Content-Type", "application/json")
	setupRec := httptest.NewRecorder()
	e.ServeHTTP(setupRec, setupReq)
	if setupRec.Code != http.StatusOK {
		t.Fatalf("setup failed: %d: %s", setupRec.Code, setupRec.Body.String())
	}

	loginBody := `{"username":"myadmin","password":"securepass123"}`
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	e.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login failed: %d: %s", loginRec.Code, loginRec.Body.String())
	}
	var loginResp map[string]any
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	token, _ := loginResp["token"].(string)
	if token == "" {
		t.Fatal("login response missing token")
	}

	body := `{"pin":"5678"}`
	req := httptest.NewRequest(http.MethodPost, "/api/machines/nonexistent-machine/terminal", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code == http.StatusForbidden {
		t.Fatalf("valid setup-user PIN was rejected: %s", rec.Body.String())
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after PIN validation succeeds, got %d: %s", rec.Code, rec.Body.String())
	}
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
			UserID:       "test-admin-id",
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

// --- API Machine Tests ---

func TestCreateAPIMachine(t *testing.T) {
	e := setupTestServer(t)
	markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	// Register API machine routes.
	api := e.Group("", jwtMiddleware, credentialRotationMiddleware)
	api.GET("/api/api-machines", handleListAPIMachines)
	api.POST("/api/api-machines", handleCreateAPIMachine)
	api.PATCH("/api/api-machines/:id", handleUpdateAPIMachine)
	api.DELETE("/api/api-machines/:id", handleDeleteAPIMachine)
	api.POST("/api/api-machines/:id/poll", handleForceAPIPoll)

	body := `{"name":"Test Proxmox","adapter_type":"proxmox","base_url":"https://192.168.3.2:8006","auth_config":{"token_id":"root@pam!monitor","token_secret":"xxx"},"poll_interval_secs":120}`
	req := httptest.NewRequest(http.MethodPost, "/api/api-machines", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp["name"] != "Test Proxmox" {
		t.Errorf("expected name 'Test Proxmox', got %v", resp["name"])
	}
	if resp["adapter_type"] != "proxmox" {
		t.Errorf("expected adapter_type 'proxmox', got %v", resp["adapter_type"])
	}
	if resp["id"] == nil || resp["id"] == "" {
		t.Error("expected non-empty id")
	}
	pollInterval, _ := resp["poll_interval_secs"].(float64)
	if pollInterval != 120 {
		t.Errorf("expected poll_interval_secs 120, got %v", pollInterval)
	}
	if tlsCfg, ok := resp["tls_config"].(map[string]interface{}); !ok {
		t.Fatalf("expected tls_config object, got %T", resp["tls_config"])
	} else {
		if tlsCfg["mode"] != "system" {
			t.Errorf("expected tls_config.mode 'system', got %v", tlsCfg["mode"])
		}
		if tlsCfg["has_custom_ca"] != false {
			t.Errorf("expected has_custom_ca false, got %v", tlsCfg["has_custom_ca"])
		}
	}

	// Stop any pollers started during the test.
	stopAllAPIPollers()
	t.Log("create api machine: OK")
}

func TestCreateAPIMachineWithCustomCATrust(t *testing.T) {
	e := setupTestServer(t)
	markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	api := e.Group("", jwtMiddleware, credentialRotationMiddleware)
	api.POST("/api/api-machines", handleCreateAPIMachine)

	body := fmt.Sprintf(`{"name":"Secure Synology","adapter_type":"synology","base_url":"https://192.168.16.10:5001","auth_config":{"username":"u","password":"p"},"tls_config":{"mode":"custom_ca","ca_cert_pem":%q},"poll_interval_secs":60}`, generateTestCertPEM(t))
	req := httptest.NewRequest(http.MethodPost, "/api/api-machines", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	tlsCfg, ok := resp["tls_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected tls_config object, got %T", resp["tls_config"])
	}
	if tlsCfg["mode"] != "custom_ca" {
		t.Errorf("expected tls_config.mode 'custom_ca', got %v", tlsCfg["mode"])
	}
	if tlsCfg["has_custom_ca"] != true {
		t.Errorf("expected has_custom_ca true, got %v", tlsCfg["has_custom_ca"])
	}

	stopAllAPIPollers()
}

func TestCreateAPIMachineInvalidAdapter(t *testing.T) {
	e := setupTestServer(t)
	markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	api := e.Group("", jwtMiddleware, credentialRotationMiddleware)
	api.POST("/api/api-machines", handleCreateAPIMachine)

	body := `{"name":"Bad","adapter_type":"unknown","base_url":"http://x","auth_config":{}}`
	req := httptest.NewRequest(http.MethodPost, "/api/api-machines", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	t.Log("invalid adapter rejected: OK")
}

func TestCreateAPIMachineInvalidTLSConfig(t *testing.T) {
	e := setupTestServer(t)
	markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	api := e.Group("", jwtMiddleware, credentialRotationMiddleware)
	api.POST("/api/api-machines", handleCreateAPIMachine)

	body := `{"name":"Bad TLS","adapter_type":"synology","base_url":"https://192.168.1.1:5001","auth_config":{"username":"u","password":"p"},"tls_config":{"mode":"custom_ca","ca_cert_pem":"not a cert"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/api-machines", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "tls_config.ca_cert_pem") {
		t.Fatalf("expected tls_config validation error, got %s", rec.Body.String())
	}
}

func TestListAPIMachines(t *testing.T) {
	e := setupTestServer(t)
	markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	api := e.Group("", jwtMiddleware, credentialRotationMiddleware)
	api.GET("/api/api-machines", handleListAPIMachines)
	api.POST("/api/api-machines", handleCreateAPIMachine)

	// Create two machines.
	for _, name := range []string{"Machine A", "Machine B"} {
		body := fmt.Sprintf(`{"name":"%s","adapter_type":"synology","base_url":"http://192.168.1.1:5000","auth_config":{"username":"u","password":"p"}}`, name)
		req := httptest.NewRequest(http.MethodPost, "/api/api-machines", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: expected 201, got %d", name, rec.Code)
		}
	}

	// List.
	req := httptest.NewRequest(http.MethodGet, "/api/api-machines", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var machines []APIMachine
	if err := json.Unmarshal(rec.Body.Bytes(), &machines); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(machines) != 2 {
		t.Fatalf("expected 2 machines, got %d", len(machines))
	}
	for _, machine := range machines {
		var tlsCfg map[string]interface{}
		if err := json.Unmarshal(machine.TLSConfig, &tlsCfg); err != nil {
			t.Fatalf("unmarshal tls_config: %v", err)
		}
		if tlsCfg["mode"] != "system" {
			t.Fatalf("expected redacted tls_config.mode system, got %v", tlsCfg["mode"])
		}
	}

	stopAllAPIPollers()
	t.Logf("list api machines: got %d", len(machines))
}

func TestDeleteAPIMachine(t *testing.T) {
	e := setupTestServer(t)
	markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	api := e.Group("", jwtMiddleware, credentialRotationMiddleware)
	api.GET("/api/api-machines", handleListAPIMachines)
	api.POST("/api/api-machines", handleCreateAPIMachine)
	api.DELETE("/api/api-machines/:id", handleDeleteAPIMachine)

	// Create a machine.
	body := `{"name":"ToDelete","adapter_type":"proxmox","base_url":"https://x:8006","auth_config":{"token_id":"a","token_secret":"b"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/api-machines", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d", rec.Code)
	}

	var createResp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &createResp)
	id := createResp["id"].(string)

	// Delete it.
	req = httptest.NewRequest(http.MethodDelete, "/api/api-machines/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify it's gone.
	req = httptest.NewRequest(http.MethodGet, "/api/api-machines", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	var machines []APIMachine
	json.Unmarshal(rec.Body.Bytes(), &machines)
	if len(machines) != 0 {
		t.Fatalf("expected 0 machines after delete, got %d", len(machines))
	}

	stopAllAPIPollers()
	t.Log("delete api machine: OK")
}

func TestUpdateAPIMachine(t *testing.T) {
	e := setupTestServer(t)
	markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	api := e.Group("", jwtMiddleware, credentialRotationMiddleware)
	api.POST("/api/api-machines", handleCreateAPIMachine)
	api.PATCH("/api/api-machines/:id", handleUpdateAPIMachine)

	createBody := `{"name":"Main","adapter_type":"proxmox","base_url":"https://192.168.3.2:8006","auth_config":{"token_id":"root@pam!monitor","token_secret":"xxx"},"tls_config":{"mode":"insecure"},"poll_interval_secs":120}`
	req := httptest.NewRequest(http.MethodPost, "/api/api-machines", strings.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var createResp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	id := createResp["id"].(string)

	updateBody := fmt.Sprintf(`{"name":"Main Updated","poll_interval_secs":300,"tls_config":{"mode":"custom_ca","ca_cert_pem":%q}}`, generateTestCertPEM(t))
	req = httptest.NewRequest(http.MethodPatch, "/api/api-machines/"+id, strings.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("update: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal update: %v", err)
	}
	if resp["name"] != "Main Updated" {
		t.Fatalf("expected updated name, got %v", resp["name"])
	}
	tlsCfg, ok := resp["tls_config"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected tls_config object, got %T", resp["tls_config"])
	}
	if tlsCfg["mode"] != "custom_ca" || tlsCfg["has_custom_ca"] != true {
		t.Fatalf("unexpected tls_config response: %v", tlsCfg)
	}

	var storedName, storedTLS string
	var storedInterval int
	if err := db.QueryRow("SELECT name, tls_config, poll_interval_secs FROM api_machines WHERE id = ?", id).Scan(&storedName, &storedTLS, &storedInterval); err != nil {
		t.Fatalf("query updated machine: %v", err)
	}
	if storedName != "Main Updated" {
		t.Fatalf("expected stored updated name, got %s", storedName)
	}
	if storedInterval != 300 {
		t.Fatalf("expected stored poll interval 300, got %d", storedInterval)
	}
	if !strings.Contains(storedTLS, `"mode":"custom_ca"`) {
		t.Fatalf("expected stored tls_config to be custom_ca, got %s", storedTLS)
	}

	stopAllAPIPollers()
}

func TestUpdateAPIMachineRequiresAuthConfigOnAdapterChange(t *testing.T) {
	e := setupTestServer(t)
	markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	api := e.Group("", jwtMiddleware, credentialRotationMiddleware)
	api.POST("/api/api-machines", handleCreateAPIMachine)
	api.PATCH("/api/api-machines/:id", handleUpdateAPIMachine)

	createBody := `{"name":"Dasman","adapter_type":"synology","base_url":"http://192.168.16.234:5000","auth_config":{"username":"u","password":"p"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/api-machines", strings.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var createResp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	id := createResp["id"].(string)

	updateBody := `{"adapter_type":"proxmox"}`
	req = httptest.NewRequest(http.MethodPatch, "/api/api-machines/"+id, strings.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "auth_config is required when adapter_type changes") {
		t.Fatalf("unexpected error: %s", rec.Body.String())
	}

	stopAllAPIPollers()
}

func TestDeleteAPIMachineNotFound(t *testing.T) {
	e := setupTestServer(t)
	markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	api := e.Group("", jwtMiddleware, credentialRotationMiddleware)
	api.DELETE("/api/api-machines/:id", handleDeleteAPIMachine)

	req := httptest.NewRequest(http.MethodDelete, "/api/api-machines/nonexistent-id", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	t.Log("delete nonexistent: 404 OK")
}

func TestValidateBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid http", "http://192.168.16.234:5000", false},
		{"valid https", "https://192.168.7.99:8006", false},
		{"empty", "", true},
		{"no scheme", "192.168.1.1:8006", true},
		{"ftp scheme", "ftp://192.168.1.1", true},
		{"localhost", "http://localhost:8080", true},
		{"127.0.0.1", "http://127.0.0.1:8080", true},
		{"ipv6 loopback", "http://[::1]:8080", true},
		{"0.0.0.0", "http://0.0.0.0:8080", true},
		{"cloud metadata", "http://169.254.169.254/latest/meta-data/", true},
		{"link-local", "http://169.254.1.1/foo", true},
		{"valid internal ip", "http://192.168.0.199:5000", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBaseURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateBaseURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

// TestHardwareInfoUpsertPreCreatesRow locks in the defense added in
// fix/hardware-info-first-connect-race: the hardware_info handler must
// be able to persist a snapshot even when the machines row doesn't exist
// yet, and a subsequent upsertMachine (from the first metric) must NOT
// clobber the snapshot.
//
// Without this, agents lose their hardware snapshot on every fresh
// enrollment because the agent could send hardware_info before the
// metric that creates the machines row.
func TestHardwareInfoUpsertPreCreatesRow(t *testing.T) {
	setupTestServer(t)

	machineID := "test-hw-race-machine-id"
	hardwareJSON := `{"type":"hardware_info","machine_id":"` + machineID + `","cpu_model":"Test CPU","cpu_cores":4,"ram_total_bytes":8589934592}`

	// Step 1: simulate hardware_info arriving BEFORE any metric.
	// This is the exact UPSERT used in hub/agentws.go.
	if _, err := db.Exec(`
		INSERT INTO machines (id, hostname, status, hardware_info)
		VALUES (?, '', 'offline', ?)
		ON CONFLICT(id) DO UPDATE SET hardware_info = excluded.hardware_info
	`, machineID, hardwareJSON); err != nil {
		t.Fatalf("hardware_info upsert (pre-metric) failed: %v", err)
	}

	// Assert the row exists with hardware_info populated and hostname=''.
	var hostname string
	var stored sql.NullString
	if err := db.QueryRow(`SELECT hostname, hardware_info FROM machines WHERE id = ?`, machineID).Scan(&hostname, &stored); err != nil {
		t.Fatalf("row not present after pre-metric hardware_info: %v", err)
	}
	if !stored.Valid || stored.String != hardwareJSON {
		t.Fatalf("hardware_info not stored: got %q", stored.String)
	}
	if hostname != "" {
		t.Fatalf("expected placeholder hostname '', got %q", hostname)
	}

	// Step 2: simulate the first metric arriving (upsertMachine semantics).
	upsertMachine(AgentMetrics{
		MachineID: machineID,
		Hostname:  "test-host",
		IP:        "10.0.0.42",
		OS:        "ubuntu 24.04 (x86_64)",
	})

	// Assert hostname/ip/os are populated AND hardware_info survived.
	var hostname2, ip, osStr string
	var hwAfter sql.NullString
	if err := db.QueryRow(`SELECT hostname, COALESCE(ip,''), COALESCE(os,''), hardware_info FROM machines WHERE id = ?`, machineID).Scan(&hostname2, &ip, &osStr, &hwAfter); err != nil {
		t.Fatalf("query after metric failed: %v", err)
	}
	if hostname2 != "test-host" || ip != "10.0.0.42" || osStr != "ubuntu 24.04 (x86_64)" {
		t.Fatalf("metric did not populate fields: hostname=%q ip=%q os=%q", hostname2, ip, osStr)
	}
	if !hwAfter.Valid || hwAfter.String != hardwareJSON {
		t.Fatalf("upsertMachine clobbered hardware_info: got %q", hwAfter.String)
	}
}
