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
	"errors"
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
func (s *Server) seedTestAdmin(t *testing.T) {
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
	_, err = s.db.Exec(`INSERT INTO users (id, username, password_hash, terminal_pin_hash) VALUES (?, ?, ?, ?)`,
		id, "admin", string(hash), string(pinHash))
	if err != nil {
		t.Fatalf("seed test admin: %v", err)
	}
}

func (s *Server) seedTestUser(t *testing.T, username, password, pin string, role UserRole, passwordChanged, pinChanged bool) string {
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
	_, err = s.db.Exec(
		`INSERT INTO users (id, username, password_hash, terminal_pin_hash, password_changed, pin_changed, role) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, username, string(hash), string(pinHash), passwordChanged, pinChanged, role,
	)
	if err != nil {
		t.Fatalf("seed test user: %v", err)
	}
	return id
}

func (s *Server) seedTestMachine(t *testing.T, id string) {
	t.Helper()
	_, err := s.db.Exec(`INSERT INTO machines (id, hostname, status) VALUES (?, ?, 'online')`, id, "machine-"+id)
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
// an Echo instance with all routes registered plus the isolated *Server
// backing it.
func setupTestServer(t *testing.T) (*echo.Echo, *Server) {
	t.Helper()

	// Default the homelab opt-in to ON for the test suite. Bloxos is a
	// homelab tool — the realistic deployment shape has the fleet on
	// RFC 1918 ranges (192.168.x or 10.x), so existing tests that
	// create API machines pointed at 192.168 URLs reflect production.
	// TestValidateBaseURL overrides this per sub-case via t.Setenv
	// when it specifically tests the default-block path.
	t.Setenv("BLOXOS_ALLOW_PRIVATE_TARGETS", "1")

	// Agent version announcements are signed; without signing material the
	// hub correctly refuses to announce anything. main() sets this up via
	// initUpdateSigning(); tests get a process-lifetime in-memory key.
	ensureTestUpdateSigningKey(t)

	// Drain stale goroutines from prior tests that may still reference
	// the global terminal session map.
	termSessionsMu.Lock()
	termSessions = make(map[string]*TerminalSession)
	termSessionsMu.Unlock()
	time.Sleep(100 * time.Millisecond)

	db, err := sql.Open("sqlite", ":memory:")
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

	s := newServer(db)
	s.seedTestAdmin(t)
	jwtSecret = []byte("test-secret-key-for-smoke-tests")
	rateLimiter = NewRateLimiter()

	e := echo.New()
	e.HideBanner = true
	s.registerRoutes(e)

	return e, s
}

// setupEmptyTestServer creates a server with NO users (for testing setup flow).
func setupEmptyTestServer(t *testing.T) (*echo.Echo, *Server) {
	t.Helper()

	termSessionsMu.Lock()
	termSessions = make(map[string]*TerminalSession)
	termSessionsMu.Unlock()
	time.Sleep(100 * time.Millisecond)

	db, err := sql.Open("sqlite", ":memory:")
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

	s := newServer(db)

	e := echo.New()
	e.HideBanner = true
	s.registerRoutes(e)

	return e, s
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
func (s *Server) markCredentialsRotated(t *testing.T) {
	t.Helper()
	_, err := s.db.Exec(`UPDATE users SET password_changed = TRUE, pin_changed = TRUE WHERE username = 'admin'`)
	if err != nil {
		t.Fatalf("mark credentials rotated: %v", err)
	}
}

func TestAdminCanCreateAndListUsers(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
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
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
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
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
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
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	s.seedTestUser(t, "op1", "operatorpass123", "1234", RoleOperator, true, true)
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
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	s.seedTestUser(t, "admin2", "admin2pass123", "1234", RoleAdmin, true, true)
	targetID := s.seedTestUser(t, "target", "targetpass123", "1234", RoleViewer, true, true)
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
	if err := s.db.QueryRow(`SELECT role FROM users WHERE id = ?`, targetID).Scan(&role); err != nil {
		t.Fatalf("fetch target role: %v", err)
	}
	if role != string(RoleOperator) {
		t.Fatalf("expected role operator in db, got %q", role)
	}
}

func TestAdminCannotChangeOwnRole(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	s.seedTestUser(t, "admin2", "admin2pass123", "1234", RoleAdmin, true, true)
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
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	otherID := s.seedTestUser(t, "other", "otherpass123", "1234", RoleAdmin, true, true)
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
	if err := s.db.QueryRow(`SELECT role FROM users WHERE id = ?`, otherID).Scan(&role); err != nil {
		t.Fatalf("fetch role: %v", err)
	}
	if role != string(RoleOperator) {
		t.Fatalf("expected role operator, got %q", role)
	}
}

func TestAdminCanDeleteOtherUser(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	targetID := s.seedTestUser(t, "victim", "victimpass123", "1234", RoleViewer, true, true)
	adminToken := loginAndGetToken(t, e)

	req := httptest.NewRequest(http.MethodDelete, "/api/users/"+targetID, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on delete, got %d: %s", rec.Code, rec.Body.String())
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE id = ?`, targetID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected target to be deleted, found %d rows", count)
	}
}

func TestAdminCannotDeleteSelf(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
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
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	otherID := s.seedTestUser(t, "other", "otherpass123", "1234", RoleAdmin, true, true)
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
	s := newServer(nil)
	e := echo.New()
	e.HideBanner = true
	s.registerRoutes(e)

	if err := auditRBACRouteCoverage(e, routeScopeRequirements); err != nil {
		t.Fatalf("production route set failed RBAC audit: %v", err)
	}
}

func TestRBACRouteAuditDetectsMissingMapping(t *testing.T) {
	s := newServer(nil)
	e := echo.New()
	e.HideBanner = true
	s.registerRoutes(e)
	// Add an extra protected route that has no scope mapping.
	api := e.Group("", jwtMiddleware, s.credentialRotationMiddleware, s.permissionMiddleware)
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
	s := newServer(nil)
	e := echo.New()
	e.HideBanner = true
	s.registerRoutes(e)

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
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	s.seedTestUser(t, "viewer1", "viewerpass123", "1234", RoleViewer, true, true)

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
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	s.seedTestUser(t, "viewer2", "viewerpass123", "1234", RoleViewer, true, true)
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
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	s.seedTestUser(t, "operator1", "operatorpass123", "1234", RoleOperator, true, true)
	s.seedTestMachine(t, "machine-1")
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

func TestDeleteMachineRemovesAgentCredential(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	s.seedTestMachine(t, "machine-delete-credential")
	adminToken := loginAndGetToken(t, e)

	if _, err := s.db.Exec(`INSERT INTO agent_credentials (machine_id, secret_hash) VALUES (?, ?)`,
		"machine-delete-credential", "test-secret-hash"); err != nil {
		t.Fatalf("seed agent credential: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/machines/machine-delete-credential", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected delete to succeed, got %d: %s", rec.Code, rec.Body.String())
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_credentials WHERE machine_id = ?`, "machine-delete-credential").Scan(&count); err != nil {
		t.Fatalf("query agent credential count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected agent credential to be deleted, got %d row(s)", count)
	}
}

// --- Tests ---

// 1. Login with valid credentials -> 200 + JWT
func TestLoginValidCredentials(t *testing.T) {
	e, _ := setupTestServer(t)

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
	e, _ := setupTestServer(t)

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
	e, _ := setupTestServer(t)

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
	e, _ := setupTestServer(t)

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
	e, _ := setupTestServer(t)
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
	e, _ := setupTestServer(t)
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
	e, s := setupTestServer(t)
	token := loginAndGetToken(t, e)
	s.markCredentialsRotated(t)
	// Install-command generation now requires PUBLIC_URL (Host-header fallback
	// removed) — set it so the happy path still yields a command.
	t.Setenv("PUBLIC_URL", "https://hub.example")

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
	e, s := setupTestServer(t)
	token := loginAndGetToken(t, e)
	s.markCredentialsRotated(t)

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
	e, _ := setupTestServer(t)

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
	_, s := setupTestServer(t)

	rawToken := "test-token-valid-enrollment"
	h := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(h[:])
	expiresAt := time.Now().Add(15 * time.Minute).Format(time.RFC3339)

	_, err := s.db.Exec(`INSERT INTO tokens (token_hash, expires_at, used) VALUES (?, ?, FALSE)`,
		tokenHash, expiresAt)
	if err != nil {
		t.Fatalf("insert token: %v", err)
	}

	gotHash, valErr := s.validateAgentToken(rawToken)
	if valErr != nil {
		t.Fatalf("expected valid token, got error: %v", valErr)
	}
	if gotHash != tokenHash {
		t.Errorf("expected hash %s, got %s", tokenHash, gotHash)
	}
}

// 8. Agent enrollment with used token -> rejected
func TestValidateAgentTokenUsed(t *testing.T) {
	_, s := setupTestServer(t)

	rawToken := "test-token-used-12345"
	h := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(h[:])
	expiresAt := time.Now().Add(15 * time.Minute).Format(time.RFC3339)

	_, err := s.db.Exec(`INSERT INTO tokens (token_hash, expires_at, used) VALUES (?, ?, TRUE)`,
		tokenHash, expiresAt)
	if err != nil {
		t.Fatalf("insert token: %v", err)
	}

	_, valErr := s.validateAgentToken(rawToken)
	if valErr == nil {
		t.Fatal("expected error for used token, got nil")
	}
	if !strings.Contains(valErr.Error(), "already used") {
		t.Errorf("expected 'already used' error, got: %v", valErr)
	}
}

// 9. Agent enrollment with expired token -> rejected
func TestValidateAgentTokenExpired(t *testing.T) {
	_, s := setupTestServer(t)

	rawToken := "test-token-expired-12345"
	h := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(h[:])
	expiresAt := time.Now().Add(-1 * time.Hour).Format(time.RFC3339)

	_, err := s.db.Exec(`INSERT INTO tokens (token_hash, expires_at, used) VALUES (?, ?, FALSE)`,
		tokenHash, expiresAt)
	if err != nil {
		t.Fatalf("insert token: %v", err)
	}

	_, valErr := s.validateAgentToken(rawToken)
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
	e, s := setupTestServer(t)
	token := loginAndGetToken(t, e)
	s.markCredentialsRotated(t)

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
	e, s := setupTestServer(t)
	token := loginAndGetToken(t, e)
	s.markCredentialsRotated(t)

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
	e, _ := setupTestServer(t)

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
	e, _ := setupTestServer(t)

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
	e, _ := setupTestServer(t)
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
	e, s := setupTestServer(t)
	token := loginAndGetToken(t, e)
	s.markCredentialsRotated(t)

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
	e, _ := setupTestServer(t)
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
	e, s := setupTestServer(t)
	token := loginAndGetToken(t, e)

	// Only mark password as changed
	_, err := s.db.Exec(`UPDATE users SET password_changed = TRUE WHERE username = 'admin'`)
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
	e, _ := setupEmptyTestServer(t)

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
	e, _ := setupTestServer(t)

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
	e, s := setupEmptyTestServer(t)

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
	err := s.db.QueryRow(`SELECT password_changed, pin_changed FROM users WHERE username = 'myadmin'`).Scan(&passwordChanged, &pinChanged)
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
	e, _ := setupEmptyTestServer(t)

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
	e, _ := setupTestServer(t)

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
	e, _ := setupEmptyTestServer(t)

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
	e, _ := setupEmptyTestServer(t)

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
	e, _ := setupEmptyTestServer(t)

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
	e, _ := setupEmptyTestServer(t)

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
func (s *Server) waitAgentDrain(t *testing.T, machineID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.agentsMu.RLock()
		_, exists := s.agents[machineID]
		s.agentsMu.RUnlock()
		if !exists {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Not fatal — the goroutine may not have registered the machine yet
	t.Logf("waitAgentDrain: %s still in agents map after %v (may not have registered)", machineID, timeout)
}

func (s *Server) seedValidToken(t *testing.T) string {
	t.Helper()
	rawToken := "test-enrollment-token-abc123"
	h := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(h[:])
	expiresAt := time.Now().Add(1 * time.Hour).Format("2006-01-02 15:04:05")
	_, err := s.db.Exec(`INSERT INTO tokens (token_hash, expires_at, used) VALUES (?, ?, FALSE)`, tokenHash, expiresAt)
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
	e, s := setupTestServer(t)
	token := s.seedValidToken(t)

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
	s.waitAgentDrain(t, "test-machine-enroll-001", 2*time.Second)
	server.Close()

	// Verify token was consumed.
	h := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(h[:])
	var used bool
	err = s.db.QueryRow(`SELECT used FROM tokens WHERE token_hash = ?`, tokenHash).Scan(&used)
	if err != nil {
		t.Fatalf("query token: %v", err)
	}
	if !used {
		t.Fatal("token should be marked as used after enrollment")
	}

	// Verify credential was stored in DB.
	var storedMachineID string
	err = s.db.QueryRow(`SELECT machine_id FROM agent_credentials LIMIT 1`).Scan(&storedMachineID)
	if err != nil {
		t.Fatalf("query agent_credentials: %v", err)
	}
	if storedMachineID != "test-machine-enroll-001" {
		t.Fatalf("expected machine_id=test-machine-enroll-001, got %s", storedMachineID)
	}

	t.Logf("enrollment successful: secret=%s... machine=%s", enrolled.AgentSecret[:8], storedMachineID)
}

func TestAgentReconnectWithSecret(t *testing.T) {
	e, s := setupTestServer(t)
	token := s.seedValidToken(t)

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
	s.waitAgentDrain(t, "test-machine-reconnect-001", 2*time.Second)
	server.Close()
}

// TestAgentVersionAnnouncedOnReconnect locks in Phase 8's auto-update path:
// when an agent reconnects via durable secret, the hub MUST send an
// agent_version frame so an out-of-date binary can self-update. The original
// Phase 8 wired the announce only into the new-enrolment branch, leaving
// every existing fleet member stuck on the old binary across hub redeploys.
func TestAgentVersionAnnouncedOnReconnect(t *testing.T) {
	e, s := setupTestServer(t)
	token := s.seedValidToken(t)

	// The hub no longer announces to an agent it has heard nothing from —
	// at WS-upgrade time it knows neither the agent's capability nor its
	// transport, and guessing is what arms reconnect timers for updates
	// that get refused. Real agents always send agent_running_version on
	// connect (reportAgentVersion), so this test does too; without it the
	// simulated agent is less faithful than the thing it stands in for.
	t.Setenv("PUBLIC_URL", "https://hub.example.com")

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
	s.waitAgentDrain(t, "test-machine-version-announce", 2*time.Second)

	// Step 2: reconnect via secret. The fix under test: the hub MUST send
	// an agent_version frame off the back of registerAgentConnection.
	conn2, err := wsDialAgent(t, server, "secret="+secret)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	defer conn2.Close()

	running, _ := json.Marshal(map[string]interface{}{
		"type":                "agent_running_version",
		"sha256":              "0000000000000000000000000000000000000000000000000000000000000000",
		"os":                  "linux",
		"update_protocol":     1,
		"update_transport_ok": true,
		"update_key_pinned":   true,
	})
	if err := conn2.WriteMessage(websocket.TextMessage, running); err != nil {
		t.Fatalf("write agent_running_version: %v", err)
	}

	conn2.SetReadDeadline(time.Now().Add(3 * time.Second))
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
	// Empty / unknown OS must NOT announce a SHA — the legacy "fall back
	// to Linux SHA" behavior caused perpetual update loops on Windows
	// agents whose OS hadn't been learned yet at WS-upgrade time.
	// recordAgentRunningVersion now triggers an announce after the OS is
	// learned from the first agent_running_version message, so brand-new
	// agents still get auto-update — just deferred from WS-upgrade time
	// to first-message-arrival time.
	if got := announcedSHAFor(""); got != "" {
		t.Fatalf("announcedSHAFor(\"\") = %q, want %q (no announce until OS is known)", got, "")
	}
	if got := announcedSHAFor("unknown"); got != "" {
		t.Fatalf("announcedSHAFor(unknown) = %q, want %q", got, "")
	}
}

// TestRecordAgentRunningVersionTracksOS verifies that when an agent
// reports its running version with an `os` field, the per-machine
// record is keyed against that OS so subsequent announces route to the
// right binary.
func TestRecordAgentRunningVersionTracksOS(t *testing.T) {
	// Use the test setup so the in-memory DB exists for lookupVersionHostname.
	_, s := setupTestServer(t)

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
	s.recordAgentRunningVersion("test-machine-windows", agentVersionReport{RunningSHA: windowsSHA, OS: "windows", UpdateProtocol: minSignatureCapableProtocol, TransportOK: true})
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
	s.recordAgentRunningVersion("test-machine-windows", agentVersionReport{RunningSHA: linuxSHA, OS: "windows", UpdateProtocol: minSignatureCapableProtocol, TransportOK: true})
	agentRunningVersionsMu.RLock()
	winInfo = agentRunningVersions["test-machine-windows"]
	agentRunningVersionsMu.RUnlock()
	if !winInfo.UpdatePending {
		t.Fatalf("windows agent reporting linux SHA must be UpdatePending")
	}

	// And vice versa for a Linux agent on the linux SHA.
	s.recordAgentRunningVersion("test-machine-linux", agentVersionReport{RunningSHA: linuxSHA, OS: "linux", UpdateProtocol: minSignatureCapableProtocol, TransportOK: true})
	agentRunningVersionsMu.RLock()
	linInfo := agentRunningVersions["test-machine-linux"]
	agentRunningVersionsMu.RUnlock()
	if linInfo.UpdatePending {
		t.Fatalf("linux agent reporting matching SHA must not be UpdatePending")
	}
}

// TestLookupAgentOSNormalisesHardwareStrings locks in the auto-update
// rollout fix: machines.os holds human-readable values such as
// "ubuntu 24.04 (x86_64)" or "Microsoft Windows 10 Pro 22H2 (x86_64)",
// not legacy "linux/amd64". lookupAgentOS must collapse both into the
// "linux"/"windows" family announcedSHAFor expects, otherwise the
// post-Phase-9 unknown-OS protection silently suppresses every
// announce and the entire fleet stops auto-updating.
func TestLookupAgentOSNormalisesHardwareStrings(t *testing.T) {
	_, s := setupTestServer(t)

	t.Cleanup(func() {
		_, _ = s.db.Exec(`DELETE FROM machines WHERE id LIKE 'lookup-os-test-%'`)
	})

	cases := []struct {
		id     string
		stored string
		want   string
	}{
		{"lookup-os-test-ubuntu", "ubuntu 24.04 (x86_64)", "linux"},
		{"lookup-os-test-debian", "debian 12 (aarch64)", "linux"},
		{"lookup-os-test-fedora", "Fedora 41 (x86_64)", "linux"},
		{"lookup-os-test-arch", "Arch Linux (x86_64)", "linux"},
		{"lookup-os-test-alpine", "Alpine 3.21 (x86_64)", "linux"},
		{"lookup-os-test-rpi", "raspbian 12", "linux"},
		{"lookup-os-test-w10", "Microsoft Windows 10 Pro 22H2 (x86_64)", "windows"},
		{"lookup-os-test-w11", "Microsoft Windows 11 Pro 25H2 (x86_64)", "windows"},
		{"lookup-os-test-legacy-l", "linux/amd64", "linux"},
		{"lookup-os-test-legacy-w", "windows/amd64", "windows"},
		{"lookup-os-test-empty", "", ""},
	}
	for _, tc := range cases {
		_, err := s.db.Exec(
			`INSERT OR REPLACE INTO machines (id, hostname, os, status) VALUES (?, ?, ?, 'offline')`,
			tc.id, tc.id, tc.stored,
		)
		if err != nil {
			t.Fatalf("seed %s: %v", tc.id, err)
		}
		got := s.lookupAgentOS(tc.id)
		if got != tc.want {
			t.Errorf("lookupAgentOS(stored=%q) = %q, want %q", tc.stored, got, tc.want)
		}
	}
}

func TestAgentReconnectWithInvalidSecret(t *testing.T) {
	e, _ := setupTestServer(t)

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
	e, s := setupTestServer(t)
	token := s.seedValidToken(t)

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
	s.waitAgentDrain(t, "test-machine-revoke-001", 2*time.Second)

	// Step 2: Revoke the credential.
	if err := s.revokeAgentCredential("test-machine-revoke-001"); err != nil {
		t.Fatalf("revoke credential: %v", err)
	}

	// Verify it was deleted.
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM agent_credentials WHERE machine_id = ?`, "test-machine-revoke-001").Scan(&count)
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
	e, s := setupTestServer(t)

	if _, err := s.db.Exec(`INSERT INTO machines (id, hostname, status) VALUES (?, ?, ?)`,
		"known-machine-without-secret", "known-host", "online"); err != nil {
		t.Fatalf("seed known machine: %v", err)
	}

	server := httptest.NewServer(e)
	defer server.Close()

	// A known machine presenting an invalid token and no durable secret is now
	// rejected before the WebSocket upgrade (no valid credential of any kind),
	// rather than being upgraded to receive a post-upgrade rejection message.
	conn, err := wsDialAgent(t, server, "token=definitely-invalid")
	if err == nil {
		conn.Close()
		t.Fatal("expected upgrade to be rejected for invalid token with no secret")
	}
}

func TestTerminalSessionValidPINForSetupUser(t *testing.T) {
	e, _ := setupEmptyTestServer(t)

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
	e, s := setupTestServer(t)
	token := loginAndGetToken(t, e)
	s.markCredentialsRotated(t)

	// Seed a fake agent so handleStartTerminal doesn't return 404.
	machineID := "test-machine-max-sessions"
	s.db.Exec(`INSERT INTO machines (id, hostname, status) VALUES (?, ?, ?)`, machineID, "test-host", "online")
	s.agentsMu.Lock()
	s.agents[machineID] = &ConnectedAgent{MachineID: machineID}
	s.agentsMu.Unlock()
	defer func() {
		s.agentsMu.Lock()
		delete(s.agents, machineID)
		s.agentsMu.Unlock()
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
	e, s := setupTestServer(t)
	token := loginAndGetToken(t, e)
	s.markCredentialsRotated(t)

	machineID := "test-machine-audit"
	s.db.Exec(`INSERT INTO machines (id, hostname, status) VALUES (?, ?, ?)`, machineID, "audit-host", "online")

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

	s.agentsMu.Lock()
	s.agents[machineID] = &ConnectedAgent{MachineID: machineID, Conn: agentConn}
	s.agentsMu.Unlock()
	defer func() {
		s.agentsMu.Lock()
		delete(s.agents, machineID)
		s.agentsMu.Unlock()
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
	err = s.db.QueryRow(`SELECT source_ip, user_id FROM terminal_sessions WHERE machine_id = ?`, machineID).Scan(&sourceIP, &userID)
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
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	// Register API machine routes.
	api := e.Group("", jwtMiddleware, s.credentialRotationMiddleware)
	api.GET("/api/api-machines", s.handleListAPIMachines)
	api.POST("/api/api-machines", s.handleCreateAPIMachine)
	api.PATCH("/api/api-machines/:id", s.handleUpdateAPIMachine)
	api.DELETE("/api/api-machines/:id", s.handleDeleteAPIMachine)
	api.POST("/api/api-machines/:id/poll", s.handleForceAPIPoll)

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
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	api := e.Group("", jwtMiddleware, s.credentialRotationMiddleware)
	api.POST("/api/api-machines", s.handleCreateAPIMachine)

	body := fmt.Sprintf(`{"name":"Secure Synology","adapter_type":"synology","base_url":"https://192.168.1.52:5001","auth_config":{"username":"u","password":"p"},"tls_config":{"mode":"custom_ca","ca_cert_pem":%q},"poll_interval_secs":60}`, generateTestCertPEM(t))
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
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	api := e.Group("", jwtMiddleware, s.credentialRotationMiddleware)
	api.POST("/api/api-machines", s.handleCreateAPIMachine)

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
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	api := e.Group("", jwtMiddleware, s.credentialRotationMiddleware)
	api.POST("/api/api-machines", s.handleCreateAPIMachine)

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
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	api := e.Group("", jwtMiddleware, s.credentialRotationMiddleware)
	api.GET("/api/api-machines", s.handleListAPIMachines)
	api.POST("/api/api-machines", s.handleCreateAPIMachine)

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
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	api := e.Group("", jwtMiddleware, s.credentialRotationMiddleware)
	api.GET("/api/api-machines", s.handleListAPIMachines)
	api.POST("/api/api-machines", s.handleCreateAPIMachine)
	api.DELETE("/api/api-machines/:id", s.handleDeleteAPIMachine)

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
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	api := e.Group("", jwtMiddleware, s.credentialRotationMiddleware)
	api.POST("/api/api-machines", s.handleCreateAPIMachine)
	api.PATCH("/api/api-machines/:id", s.handleUpdateAPIMachine)

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
	if err := s.db.QueryRow("SELECT name, tls_config, poll_interval_secs FROM api_machines WHERE id = ?", id).Scan(&storedName, &storedTLS, &storedInterval); err != nil {
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
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	api := e.Group("", jwtMiddleware, s.credentialRotationMiddleware)
	api.POST("/api/api-machines", s.handleCreateAPIMachine)
	api.PATCH("/api/api-machines/:id", s.handleUpdateAPIMachine)

	createBody := `{"name":"MyNAS","adapter_type":"synology","base_url":"http://192.168.1.50:5000","auth_config":{"username":"u","password":"p"}}`
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
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)

	api := e.Group("", jwtMiddleware, s.credentialRotationMiddleware)
	api.DELETE("/api/api-machines/:id", s.handleDeleteAPIMachine)

	req := httptest.NewRequest(http.MethodDelete, "/api/api-machines/nonexistent-id", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	t.Log("delete nonexistent: 404 OK")
}

// TestValidateBaseURL covers the SSRF guard. Pre-B6, RFC 1918 ranges
// were silently allowed even without an opt-in — the only explicit
// blocks were localhost / 0.0.0.0 / 169.254.x. After B6, 10.0.0.0/8,
// 172.16.0.0/12, and 192.168.0.0/16 are blocked unless
// BLOXOS_ALLOW_PRIVATE_TARGETS=1 is set in the hub's environment.
//
// Tests run with the homelab opt-in cleared by default; the homelab
// sub-tests explicitly enable it via t.Setenv. Public IPs are
// allowed in both modes.
func TestValidateBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		homelab bool
		wantErr bool
	}{
		// Format / scheme errors — independent of homelab mode.
		{"empty", "", false, true},
		{"no scheme", "192.168.1.1:8006", false, true},
		{"ftp scheme", "ftp://192.168.1.1", false, true},

		// Localhost / metadata blocks — never bypassable, homelab=true
		// is set on these to prove the env var doesn't unlock them.
		{"localhost", "http://localhost:8080", true, true},
		{"127.0.0.1", "http://127.0.0.1:8080", true, true},
		{"ipv6 loopback", "http://[::1]:8080", true, true},
		{"0.0.0.0", "http://0.0.0.0:8080", true, true},
		{"cloud metadata", "http://169.254.169.254/latest/meta-data/", true, true},
		{"link-local", "http://169.254.1.1/foo", true, true},

		// RFC 1918 — blocked by default (B6 fix).
		{"10/8 default blocked", "http://10.20.30.40:8006", false, true},
		{"172.16/12 default blocked", "http://172.20.5.10:8080", false, true},
		{"192.168/16 default blocked", "http://192.168.1.50:5000", false, true},

		// RFC 1918 — allowed when homelab opt-in is set.
		{"10/8 homelab allowed", "http://10.20.30.40:8006", true, false},
		{"172.16/12 homelab allowed", "http://172.20.5.10:8080", true, false},
		{"192.168/16 homelab allowed", "http://192.168.1.50:5000", true, false},

		// 172.x outside 16-31 (RFC 1918 is 172.16/12) is public.
		{"172.32 is public", "http://172.32.0.1:8080", false, false},

		// Public IPs always allowed.
		{"public ip allowed default", "http://8.8.8.8:443", false, false},
		{"public hostname allowed default", "https://example.com", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.homelab {
				t.Setenv("BLOXOS_ALLOW_PRIVATE_TARGETS", "1")
			} else {
				t.Setenv("BLOXOS_ALLOW_PRIVATE_TARGETS", "")
			}
			err := validateBaseURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateBaseURL(%q) homelab=%v error = %v, wantErr %v",
					tt.url, tt.homelab, err, tt.wantErr)
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
	_, s := setupTestServer(t)

	machineID := "test-hw-race-machine-id"
	hardwareJSON := `{"type":"hardware_info","machine_id":"` + machineID + `","cpu_model":"Test CPU","cpu_cores":4,"ram_total_bytes":8589934592}`

	// Step 1: simulate hardware_info arriving BEFORE any metric.
	// This is the exact UPSERT used in hub/agentws.go.
	if _, err := s.db.Exec(`
		INSERT INTO machines (id, hostname, status, hardware_info)
		VALUES (?, '', 'offline', ?)
		ON CONFLICT(id) DO UPDATE SET hardware_info = excluded.hardware_info
	`, machineID, hardwareJSON); err != nil {
		t.Fatalf("hardware_info upsert (pre-metric) failed: %v", err)
	}

	// Assert the row exists with hardware_info populated and hostname=''.
	var hostname string
	var stored sql.NullString
	if err := s.db.QueryRow(`SELECT hostname, hardware_info FROM machines WHERE id = ?`, machineID).Scan(&hostname, &stored); err != nil {
		t.Fatalf("row not present after pre-metric hardware_info: %v", err)
	}
	if !stored.Valid || stored.String != hardwareJSON {
		t.Fatalf("hardware_info not stored: got %q", stored.String)
	}
	if hostname != "" {
		t.Fatalf("expected placeholder hostname '', got %q", hostname)
	}

	// Step 2: simulate the first metric arriving (upsertMachine semantics).
	s.upsertMachine(AgentMetrics{
		MachineID: machineID,
		Hostname:  "test-host",
		IP:        "10.0.0.42",
		OS:        "ubuntu 24.04 (x86_64)",
	})

	// Assert hostname/ip/os are populated AND hardware_info survived.
	var hostname2, ip, osStr string
	var hwAfter sql.NullString
	if err := s.db.QueryRow(`SELECT hostname, COALESCE(ip,''), COALESCE(os,''), hardware_info FROM machines WHERE id = ?`, machineID).Scan(&hostname2, &ip, &osStr, &hwAfter); err != nil {
		t.Fatalf("query after metric failed: %v", err)
	}
	if hostname2 != "test-host" || ip != "10.0.0.42" || osStr != "ubuntu 24.04 (x86_64)" {
		t.Fatalf("metric did not populate fields: hostname=%q ip=%q os=%q", hostname2, ip, osStr)
	}
	if !hwAfter.Valid || hwAfter.String != hardwareJSON {
		t.Fatalf("upsertMachine clobbered hardware_info: got %q", hwAfter.String)
	}
}

// TestResolveCORSOrigins locks in B7: the prior CORS setup fell back
// to AllowOrigins=[]string{"*"} when neither ALLOWED_ORIGINS nor
// PUBLIC_URL was set. Combined with JWT-in-localStorage, that means
// any malicious page could read authenticated responses if the user
// happened to have a hub session in another tab. The fix is to
// REFUSE TO START in that configuration — operators must opt in to
// origin policy explicitly.
func TestResolveCORSOrigins(t *testing.T) {
	t.Run("explicit_ALLOWED_ORIGINS_wins", func(t *testing.T) {
		t.Setenv("ALLOWED_ORIGINS", "https://app.example.com,https://staging.example.com")
		t.Setenv("PUBLIC_URL", "https://ignored.example.com")
		got, err := resolveCORSOrigins()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"https://app.example.com", "https://staging.example.com"}
		if !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("ALLOWED_ORIGINS_trims_whitespace", func(t *testing.T) {
		t.Setenv("ALLOWED_ORIGINS", "  https://a.example.com ,https://b.example.com  ")
		t.Setenv("PUBLIC_URL", "")
		got, err := resolveCORSOrigins()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"https://a.example.com", "https://b.example.com"}
		if !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("falls_back_to_PUBLIC_URL", func(t *testing.T) {
		t.Setenv("ALLOWED_ORIGINS", "")
		t.Setenv("PUBLIC_URL", "https://192.168.1.100")
		got, err := resolveCORSOrigins()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !slices.Equal(got, []string{"https://192.168.1.100"}) {
			t.Errorf("got %v, want [https://192.168.1.100]", got)
		}
	})

	t.Run("both_unset_returns_error", func(t *testing.T) {
		t.Setenv("ALLOWED_ORIGINS", "")
		t.Setenv("PUBLIC_URL", "")
		got, err := resolveCORSOrigins()
		if err == nil {
			t.Fatalf("expected error for missing CORS config, got origins=%v", got)
		}
		if got != nil {
			t.Errorf("expected nil origins on error, got %v", got)
		}
		// Error message must NOT silently fall back to "*" — that's
		// exactly the regression the test guards.
		if !strings.Contains(err.Error(), "PUBLIC_URL") || !strings.Contains(err.Error(), "ALLOWED_ORIGINS") {
			t.Errorf("error %q should mention both env vars so the operator knows the fix", err)
		}
	})

	t.Run("ALLOWED_ORIGINS_only_whitespace_falls_through_to_PUBLIC_URL", func(t *testing.T) {
		t.Setenv("ALLOWED_ORIGINS", "  , , ")
		t.Setenv("PUBLIC_URL", "https://example.com")
		got, err := resolveCORSOrigins()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !slices.Equal(got, []string{"https://example.com"}) {
			t.Errorf("got %v, want [https://example.com]", got)
		}
	})
}

// TestBuildTelegramPayloadIsPlainText locks in B5: the alert
// notification path used parse_mode="HTML" and substituted
// agent-reported strings (m.hostname, rule.Name) into the message
// body unescaped. An agent that reported its hostname as something
// containing `<` or `&` would either get rendered weirdly by Telegram
// or cause the API to reject the message as malformed HTML — either
// way, the alert silently fails. A malicious agent hostname could
// also smuggle markup through to operators reading the alerts.
//
// Fix: drop parse_mode entirely. Telegram's default is plain text.
// Future "I want bold formatting" callers would explicitly opt in
// via a separate path.
func TestBuildTelegramPayloadIsPlainText(t *testing.T) {
	payload, err := buildTelegramPayload("chat-id-123", "alert with <script>tag")
	if err != nil {
		t.Fatalf("build payload: %v", err)
	}
	var parsed map[string]string
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if _, ok := parsed["parse_mode"]; ok {
		t.Errorf("payload still contains parse_mode=%q — this is the regression we just fixed; Telegram will parse user-supplied strings as HTML",
			parsed["parse_mode"])
	}
	if got := parsed["chat_id"]; got != "chat-id-123" {
		t.Errorf("chat_id = %q, want chat-id-123", got)
	}
	// Text is preserved verbatim — no escaping applied here. Plain-text
	// mode means Telegram doesn't interpret it, so we don't need to.
	if got := parsed["text"]; got != "alert with <script>tag" {
		t.Errorf("text = %q, want verbatim original", got)
	}
}

// TestBulkCommandRejectsOversize locks in B3 (size cap): the bulk
// command endpoint had no upper bound on machine_ids. A request with
// 10000 IDs would spawn 10000 goroutines instantly, each holding a
// pendingCmds entry + a goroutine + (per WriteMessage) a per-agent
// write-mutex contention. Easy DoS surface for any authenticated
// operator. The fix caps the request size at maxBulkCommandTargets
// and returns 400 above that.
func TestBulkCommandRejectsOversize(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	adminToken := loginAndGetToken(t, e)

	ids := make([]string, maxBulkCommandTargets+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("machine-%d", i)
	}
	bodyBytes, _ := json.Marshal(map[string]interface{}{
		"machine_ids": ids,
		"type":        "restart_service",
		"target":      "nginx",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/bulk/command", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for %d targets (limit=%d), got %d: %s",
			len(ids), maxBulkCommandTargets, rec.Code, rec.Body.String())
	}
}

// TestBulkCommandAcceptsAtLimit confirms the cap is exactly
// maxBulkCommandTargets — exactly that many IDs must be accepted.
// Without this, the off-by-one between < and <= silently bites only
// at the boundary. (None of the IDs are real, so the response will
// still be 200 with N rows containing "agent not connected" — the
// per-machine error is fine here, we only assert the request itself
// wasn't bounced at the size check.)
func TestBulkCommandAcceptsAtLimit(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	adminToken := loginAndGetToken(t, e)

	ids := make([]string, maxBulkCommandTargets)
	for i := range ids {
		ids[i] = fmt.Sprintf("machine-%d", i)
	}
	bodyBytes, _ := json.Marshal(map[string]interface{}{
		"machine_ids": ids,
		"type":        "restart_service",
		"target":      "nginx",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/bulk/command", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code == http.StatusBadRequest {
		t.Fatalf("expected acceptance at exactly the limit (%d), got 400: %s",
			maxBulkCommandTargets, rec.Body.String())
	}
}

// TestAgentWSRateLimited locks in B2: /ws/agent had no rate limit
// before this fix. A flood of WebSocket upgrade requests could exhaust
// the hub's file descriptors and goroutine count — the agent endpoint
// was the only request handler in the hub without a rateLimiter.Allow
// gate. Modeled on TestLoginRateLimiting.
//
// The handler now rejects the (N+1)th request from the same IP within
// a one-minute window with HTTP 429. Real agents reconnect at most a
// few times per minute even on flaky networks, so wsAgentRateLimit=30
// per minute leaves significant headroom while shutting down a flood.
func TestAgentWSRateLimited(t *testing.T) {
	e, _ := setupTestServer(t)

	const ip = "10.99.0.1:55555"
	var lastCode int
	for i := 0; i < wsAgentRateLimit+1; i++ {
		req := httptest.NewRequest(http.MethodGet, "/ws/agent?secret=test", nil)
		req.RemoteAddr = ip
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		lastCode = rec.Code
	}
	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on request %d (limit=%d), got %d",
			wsAgentRateLimit+1, wsAgentRateLimit, lastCode)
	}
}

// TestAgentWSRateLimitPerIP confirms the limit is per-source-IP, not
// global. Fleet-wide reconnect after a hub restart must not be blocked
// — each agent has its own IP so each gets its own bucket.
func TestAgentWSRateLimitPerIP(t *testing.T) {
	e, _ := setupTestServer(t)

	for i := 0; i < wsAgentRateLimit+1; i++ {
		req := httptest.NewRequest(http.MethodGet, "/ws/agent?secret=test", nil)
		req.RemoteAddr = "10.99.0.1:1111"
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
	}

	req := httptest.NewRequest(http.MethodGet, "/ws/agent?secret=test", nil)
	req.RemoteAddr = "10.99.0.2:2222"
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code == http.StatusTooManyRequests {
		t.Fatalf("rate limit leaked across IPs — IP-B should not be limited by IP-A's bucket")
	}
}

// TestRunWithRecoverCatchesPanic locks in B1: every long-running hub
// goroutine (alertEvalLoop, cleanupLoop, versionRefreshLoop,
// reconnectMonitorLoop, terminalRelay) was previously a `go funcName()`
// spawn with no panic recovery. A panic inside any of them silently
// killed that subsystem until the next hub restart — alerts stop
// firing, cleanup stops running, agent SHAs go stale, terminals
// stop relaying. Net effect: invisible degradation.
//
// runWithRecover is the inner seam that wraps the panicking call and
// reports back whether a panic occurred. Tested directly; the outer
// goSafelyForever (which uses a sleep + for-loop to restart) is a
// thin wrapper reviewed by inspection.
func TestRunWithRecoverCatchesPanic(t *testing.T) {
	cases := []struct {
		name      string
		fn        func()
		wantPanic bool
	}{
		{
			name:      "no_panic_returns_false",
			fn:        func() {},
			wantPanic: false,
		},
		{
			name:      "string_panic_returns_true",
			fn:        func() { panic("boom") },
			wantPanic: true,
		},
		{
			name:      "error_panic_returns_true",
			fn:        func() { panic(errors.New("typed")) },
			wantPanic: true,
		},
		{
			name:      "nil_panic_returns_true",
			fn:        func() { panic(nil) },
			wantPanic: true,
		},
		{
			name:      "runtime_panic_returns_true",
			fn:        func() { var p *int; _ = *p }, // nil-deref
			wantPanic: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("runWithRecover let a panic propagate to the caller: %v", r)
				}
			}()
			got := runWithRecover("test/"+tc.name, tc.fn)
			if got != tc.wantPanic {
				t.Errorf("runWithRecover panicked=%v, want %v", got, tc.wantPanic)
			}
		})
	}
}

// TestExtractAPIMachineIP locks in C5: the IP for an API-polled
// machine must be derived from the baseURL field (which is always a
// well-formed URL the user typed when registering the machine), NOT
// from the display-name field. The pre-fix code at hub/main.go:2208
// did `strings.TrimPrefix(strings.TrimPrefix(name, "https://"), "http://")`
// against the wrong variable — for typical display names like
// "dasman-syn" or "Synology NAS" this produced garbage IP values like
// "dasman-syn" in the machines table.
//
// When the adapter has already discovered the IP via its API
// (result.IP), prefer that — it's the canonical answer. Fall back to
// parsing baseURL only when the adapter didn't report one.
func TestExtractAPIMachineIP(t *testing.T) {
	cases := []struct {
		name     string
		baseURL  string
		resultIP string
		want     string
	}{
		{
			name:     "adapter_provided_ip_wins",
			baseURL:  "https://192.168.1.51:5001",
			resultIP: "10.0.0.1",
			want:     "10.0.0.1",
		},
		{
			name:    "extract_ip_from_https_url_with_port",
			baseURL: "https://192.168.1.51:5001",
			want:    "192.168.1.51",
		},
		{
			name:    "extract_ip_from_https_url_no_port",
			baseURL: "https://192.168.1.51",
			want:    "192.168.1.51",
		},
		{
			name:    "extract_hostname_from_url",
			baseURL: "https://syn.local:5001",
			want:    "syn.local",
		},
		{
			name:    "extract_from_http_url",
			baseURL: "http://10.20.30.40:8006",
			want:    "10.20.30.40",
		},
		{
			name:    "ipv6_url",
			baseURL: "https://[2001:db8::1]:5001",
			want:    "2001:db8::1",
		},
		{
			name:    "garbage_baseurl_returns_empty",
			baseURL: "not-a-valid-url",
			want:    "",
		},
		{
			name:    "empty_inputs_return_empty",
			baseURL: "",
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractAPIMachineIP(tc.baseURL, tc.resultIP)
			if got != tc.want {
				t.Errorf("extractAPIMachineIP(%q, %q) = %q, want %q",
					tc.baseURL, tc.resultIP, got, tc.want)
			}
		})
	}
}

// TestUnregisterAgentConnectionDeletesOwn verifies the happy path:
// when a connection cleanly exits and no reconnect has happened,
// unregisterAgentConnection removes the entry. Without this baseline,
// the identity-check logic in the next test could be vacuous.
func TestUnregisterAgentConnectionDeletesOwn(t *testing.T) {
	_, s := setupTestServer(t)

	mid := "test-c2-clean-exit"
	conn := &ConnectedAgent{}

	t.Cleanup(func() {
		s.agentsMu.Lock()
		delete(s.agents, mid)
		s.agentsMu.Unlock()
	})

	s.agentsMu.Lock()
	s.agents[mid] = conn
	s.agentsMu.Unlock()

	s.unregisterAgentConnection(mid, conn)

	s.agentsMu.RLock()
	_, exists := s.agents[mid]
	s.agentsMu.RUnlock()
	if exists {
		t.Errorf("agents[%q] should have been deleted by its owning connection", mid)
	}
}

// TestUnregisterAgentConnectionLeavesNewerEntry locks in the C2 fix:
// when a fast-reconnect has installed a new ConnectedAgent at the same
// machine_id BEFORE the prior handler's defer fires, the prior defer
// must NOT delete the new entry. Pre-fix code at hub/agentws.go:450-454
// did `delete(agents, machineID)` unconditionally, causing a real
// production false-offline on flaky-network agents.
//
// Deterministic — no sleeps, no goroutines. Simulates the race by
// sequentially registering A → registering B (overwrite) → running
// A's cleanup → asserting B is still there.
func TestUnregisterAgentConnectionLeavesNewerEntry(t *testing.T) {
	_, s := setupTestServer(t)

	mid := "test-c2-reconnect-race"
	connA := &ConnectedAgent{}
	connB := &ConnectedAgent{}

	t.Cleanup(func() {
		s.agentsMu.Lock()
		delete(s.agents, mid)
		s.agentsMu.Unlock()
	})

	s.agentsMu.Lock()
	s.agents[mid] = connA
	s.agentsMu.Unlock()

	// Simulate fast reconnect — B installs itself before A's cleanup runs.
	s.agentsMu.Lock()
	s.agents[mid] = connB
	s.agentsMu.Unlock()

	// A's defer fires. Must not touch B.
	s.unregisterAgentConnection(mid, connA)

	s.agentsMu.RLock()
	got, exists := s.agents[mid]
	s.agentsMu.RUnlock()
	if !exists {
		t.Fatalf("agents[%q] was deleted; expected B's entry to remain", mid)
	}
	if got != connB {
		t.Errorf("agents[%q] = %p, want %p (connB)", mid, got, connB)
	}
}

// TestUnregisterAgentConnectionEmptyMachineIDNoOp guards the call site:
// handleAgentWS may call cleanup with machineID="" if auth never
// succeeded. Helper must no-op rather than delete the "" key.
func TestUnregisterAgentConnectionEmptyMachineIDNoOp(t *testing.T) {
	_, s := setupTestServer(t)

	sentinel := &ConnectedAgent{}
	s.agentsMu.Lock()
	s.agents[""] = sentinel
	s.agentsMu.Unlock()
	t.Cleanup(func() {
		s.agentsMu.Lock()
		delete(s.agents, "")
		s.agentsMu.Unlock()
	})

	s.unregisterAgentConnection("", &ConnectedAgent{})

	s.agentsMu.RLock()
	got, exists := s.agents[""]
	s.agentsMu.RUnlock()
	if !exists || got != sentinel {
		t.Errorf("empty-machineID call must be a no-op; got=%p exists=%v want=%p", got, exists, sentinel)
	}
}

// TestUnregisterAgentConnectionMarksOfflineOnlyForOwn validates that
// markOffline (status='offline' in DB) is also gated by the identity
// check. Without it, a fast-reconnect briefly flips the dashboard to
// offline before B's metrics flip it back — visible flicker on the UI.
func TestUnregisterAgentConnectionMarksOfflineOnlyForOwn(t *testing.T) {
	_, s := setupTestServer(t)

	mid := "test-c2-offline-gating"
	connA := &ConnectedAgent{}
	connB := &ConnectedAgent{}

	t.Cleanup(func() {
		_, _ = s.db.Exec(`DELETE FROM machines WHERE id = ?`, mid)
		s.agentsMu.Lock()
		delete(s.agents, mid)
		s.agentsMu.Unlock()
	})

	if _, err := s.db.Exec(
		`INSERT INTO machines (id, hostname, status) VALUES (?, ?, 'online')`,
		mid, "c2-offline-host",
	); err != nil {
		t.Fatalf("seed machine: %v", err)
	}

	// B has reconnected after A's drop.
	s.agentsMu.Lock()
	s.agents[mid] = connB
	s.agentsMu.Unlock()

	// A's cleanup runs. Must NOT flip status to offline because B is alive.
	s.unregisterAgentConnection(mid, connA)

	var status string
	if err := s.db.QueryRow(
		`SELECT status FROM machines WHERE id = ?`, mid,
	).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "online" {
		t.Errorf("status flipped to %q despite B being live; want 'online'", status)
	}
}

// TestAPIMachineErrorUpsert locks in the SQL contract for the API-poll
// error path. Pre-fix bug: the literal `error` keyword in
// VALUES (?, ?, error, ?, ...) was unquoted, so SQLite parsed it as a
// column reference and the INSERT crashed at runtime — silently, because
// the caller (startAPIPoller's goroutine in hub/main.go) discarded the
// error. Result: API-polled machines (Synology / Proxmox / etc.) never
// transitioned to `status='error'` in the dashboard when their poll
// failed. The fix quotes the literal as 'error'.
//
// This test calls the extracted markAPIMachineError helper directly so
// failure propagates as a return value, not a swallowed log line.
func TestAPIMachineErrorUpsert(t *testing.T) {
	_, s := setupTestServer(t)

	machineID := "api-c1-test"
	displayName := "test-synology"
	adapter := "synology"

	t.Cleanup(func() {
		_, _ = s.db.Exec(`DELETE FROM machines WHERE id = ?`, machineID)
	})

	// First call: INSERT path. Must not error and must write status='error'.
	if err := s.markAPIMachineError(machineID, displayName, adapter); err != nil {
		t.Fatalf("markAPIMachineError (insert): %v", err)
	}

	var hostname, status, tags string
	if err := s.db.QueryRow(
		`SELECT hostname, status, COALESCE(tags, '') FROM machines WHERE id = ?`,
		machineID,
	).Scan(&hostname, &status, &tags); err != nil {
		t.Fatalf("read after insert: %v", err)
	}
	if status != "error" {
		t.Errorf("status after insert = %q, want %q", status, "error")
	}
	if hostname != displayName {
		t.Errorf("hostname after insert = %q, want %q", hostname, displayName)
	}
	if tags != adapter {
		t.Errorf("tags after insert = %q, want %q", tags, adapter)
	}

	// Second call: ON CONFLICT UPDATE path. Must not error and must keep
	// status='error'. (Also validates the ON CONFLICT branch itself.)
	if err := s.markAPIMachineError(machineID, displayName, adapter); err != nil {
		t.Fatalf("markAPIMachineError (upsert): %v", err)
	}

	var statusAfterUpsert string
	if err := s.db.QueryRow(
		`SELECT status FROM machines WHERE id = ?`, machineID,
	).Scan(&statusAfterUpsert); err != nil {
		t.Fatalf("read after upsert: %v", err)
	}
	if statusAfterUpsert != "error" {
		t.Errorf("status after upsert = %q, want %q", statusAfterUpsert, "error")
	}
}
