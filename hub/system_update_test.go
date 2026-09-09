package main

// Tests for the unified updater hub API and maintenance gate
// (hub/system_update.go). The mailbox is a temp dir per test; nothing touches
// a real worker, docker socket or systemd.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func setupUpdaterDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"inbox", "outbox"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("BLOXOS_UPDATER_DIR", dir)
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func writeCapabilities(t *testing.T, dir, mode string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "outbox", "capabilities.json"),
		`{"enabled":true,"mode":"`+mode+`"}`)
}

func updaterRequest(t *testing.T, e *echo.Echo, method, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *strings.Reader
	if body != "" {
		r = strings.NewReader(body)
	} else {
		r = strings.NewReader("")
	}
	req := httptest.NewRequest(method, "/api/system/update", r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestSystemUpdateGetUnconfigured(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("BLOXOS_UPDATER_DIR", "")
	adminToken := loginAndGetToken(t, e)
	rec := updaterRequest(t, e, http.MethodGet, adminToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["available"] != false {
		t.Fatalf("available should be false: %v", resp)
	}
	if resp["cli_command"] != "sudo bloxos-update init" {
		t.Fatalf("cli_command: %v", resp["cli_command"])
	}
	if resp["current_version"] == nil {
		t.Fatal("current_version missing")
	}
}

func TestSystemUpdateRejectsTrailingAndOversizedJSON(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)
	dir := setupUpdaterDir(t)
	writeCapabilities(t, dir, "compose")
	for _, body := range []string{
		`{"target_version":"latest"} {"command":"anything"}`,
		`{"target_version":"` + strings.Repeat("a", 2048) + `"}`,
	} {
		rec := updaterRequest(t, e, http.MethodPost, token, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected rejection, got %d", rec.Code)
		}
		if _, err := os.Stat(filepath.Join(dir, "inbox", "request.json")); !os.IsNotExist(err) {
			t.Fatal("invalid request was published")
		}
	}
}

func TestSystemUpdateGetConfiguredModesAndStatus(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	adminToken := loginAndGetToken(t, e)
	for _, mode := range []string{"native", "compose"} {
		dir := setupUpdaterDir(t)
		writeCapabilities(t, dir, mode)
		writeFile(t, filepath.Join(dir, "outbox", "status.json"),
			`{"request_id":"req-1","state":"staging","version":"v1.2.3","message":"ok","updated_at":"2026-09-09T00:00:00Z"}`)
		rec := updaterRequest(t, e, http.MethodGet, adminToken, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", mode, rec.Code, rec.Body)
		}
		var resp map[string]any
		json.Unmarshal(rec.Body.Bytes(), &resp)
		if resp["available"] != true || resp["mode"] != mode {
			t.Fatalf("%s: %+v", mode, resp)
		}
		st := resp["status"].(map[string]any)
		if st["state"] != "staging" || st["request_id"] != "req-1" {
			t.Fatalf("%s: status %+v", mode, st)
		}
	}
}

func TestSystemUpdateStatusSanitized(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	adminToken := loginAndGetToken(t, e)
	dir := setupUpdaterDir(t)
	writeCapabilities(t, dir, "native")
	writeFile(t, filepath.Join(dir, "outbox", "status.json"),
		`{"request_id":"r","state":"installing","version":"v1","message":"line1\u001b[31m escapes\u0000control","updated_at":"t"}`)
	rec := updaterRequest(t, e, http.MethodGet, adminToken, "")
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	msg := resp["status"].(map[string]any)["message"].(string)
	if strings.ContainsAny(msg, "\x1b\x00") {
		t.Fatalf("control characters leaked: %q", msg)
	}
	// malformed state is rejected, never passed through
	writeFile(t, filepath.Join(dir, "outbox", "status.json"),
		`{"request_id":"r","state":"teleporting","version":"v1","message":"x","updated_at":"t"}`)
	rec = updaterRequest(t, e, http.MethodGet, adminToken, "")
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["status"] != nil {
		t.Fatalf("invalid state passed through: %v", resp["status"])
	}
	if resp["reason"] != "updater status invalid" {
		t.Fatalf("reason: %v", resp["reason"])
	}
}

func TestSystemUpdatePostValidationAndWrite(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	adminToken := loginAndGetToken(t, e)

	// Not configured: 503.
	t.Setenv("BLOXOS_UPDATER_DIR", "")
	rec := updaterRequest(t, e, http.MethodPost, adminToken, `{"target_version":"latest"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured POST: %d %s", rec.Code, rec.Body)
	}

	dir := setupUpdaterDir(t)
	writeCapabilities(t, dir, "native")

	// Wrong versions / shapes: 400.
	for _, body := range []string{
		`{"target_version":"v1.2.2"}`,
		`{"target_version":"latest","extra":1}`,
		`{}`, `not-json`,
	} {
		rec = updaterRequest(t, e, http.MethodPost, adminToken, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: want 400, got %d", body, rec.Code)
		}
	}

	// Happy path: 202, exclusive request written with a UUID.
	rec = updaterRequest(t, e, http.MethodPost, adminToken, `{"target_version":"latest"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST: %d %s", rec.Code, rec.Body)
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	requestID, _ := resp["request_id"].(string)
	if requestID == "" {
		t.Fatalf("no request_id: %v", resp)
	}
	data, err := os.ReadFile(filepath.Join(dir, "inbox", "request.json"))
	if err != nil {
		t.Fatalf("request not written: %v", err)
	}
	var written map[string]string
	json.Unmarshal(data, &written)
	if written["request_id"] != requestID || written["target_version"] != "latest" {
		t.Fatalf("written request: %v", written)
	}

	// Second POST while a request exists: 409, file untouched.
	rec = updaterRequest(t, e, http.MethodPost, adminToken, `{"target_version":"latest"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second POST: want 409, got %d", rec.Code)
	}
}

func TestSystemUpdatePostRejectsActiveStatus(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	adminToken := loginAndGetToken(t, e)
	dir := setupUpdaterDir(t)
	writeCapabilities(t, dir, "compose")
	writeFile(t, filepath.Join(dir, "outbox", "status.json"),
		`{"request_id":"r","state":"installing","version":"v1","message":"x","updated_at":"t"}`)
	rec := updaterRequest(t, e, http.MethodPost, adminToken, `{"target_version":"latest"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("active status: want 409, got %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "inbox", "request.json")); !os.IsNotExist(err) {
		t.Fatal("request written despite active update")
	}
}

func TestSystemUpdatePostSymlinkNeverFollowed(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	adminToken := loginAndGetToken(t, e)
	dir := setupUpdaterDir(t)
	writeCapabilities(t, dir, "native")
	target := filepath.Join(t.TempDir(), "victim")
	if err := os.Symlink(target, filepath.Join(dir, "inbox", "request.json")); err != nil {
		t.Fatal(err)
	}
	rec := updaterRequest(t, e, http.MethodPost, adminToken, `{"target_version":"latest"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("symlinked request path: want 409, got %d", rec.Code)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("symlink target was written")
	}
}

func TestSystemUpdateRBAC(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	s.seedTestUser(t, "viewer1", "viewerpass123", "1234", RoleViewer, true, true)
	viewerToken := loginAndGetTokenForCredentials(t, e, "viewer1", "viewerpass123")
	dir := setupUpdaterDir(t)
	writeCapabilities(t, dir, "native")
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		body := ""
		if method == http.MethodPost {
			body = `{"target_version":"latest"}`
		}
		rec := updaterRequest(t, e, method, viewerToken, body)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("viewer %s: want 403, got %d", method, rec.Code)
		}
	}
	rec := updaterRequest(t, e, http.MethodGet, "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: want 401, got %d", rec.Code)
	}
}

func TestMaintenanceMiddlewareBlocksApplicationTraffic(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	adminToken := loginAndGetToken(t, e)
	s.seedTestMachine(t, "m1")
	dir := setupUpdaterDir(t)
	writeCapabilities(t, dir, "native")

	get := func(path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec
	}

	// No marker: normal traffic.
	if rec := get("/api/machines", adminToken); rec.Code != http.StatusOK {
		t.Fatalf("baseline /api/machines: %d", rec.Code)
	}

	writeFile(t, filepath.Join(dir, "outbox", "maintenance"), "")
	blocked := []string{"/api/machines", "/api/tokens", "/api/auth/login", "/join/x", "/ws/agent", "/api/auth/setup", "/api/setup/status"}
	for _, path := range blocked {
		rec := get(path, adminToken)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s during maintenance: want 503, got %d", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), dir) {
			t.Fatalf("%s leaked mailbox path in response", path)
		}
	}
	// Allowlist passes during maintenance.
	for path, want := range map[string]int{
		"/health":            http.StatusOK,
		"/api/build-info":    http.StatusOK,
		"/api/system/update": http.StatusOK,
	} {
		if rec := get(path, adminToken); rec.Code != want {
			t.Fatalf("%s during maintenance: want %d, got %d", path, want, rec.Code)
		}
	}
}

func TestMaintenanceMarkerErrorsFailClosed(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	adminToken := loginAndGetToken(t, e)
	s.seedTestMachine(t, "m1")
	dir := setupUpdaterDir(t)
	writeCapabilities(t, dir, "native")
	// A directory named "maintenance" makes os.Stat succeed... use a broken
	// parent instead: outbox replaced by a regular file so Stat errors are
	// non-NotExist (ENOTDIR) -> fail closed.
	if err := os.RemoveAll(filepath.Join(dir, "outbox")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "outbox"), "")
	req := httptest.NewRequest(http.MethodGet, "/api/machines", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("marker stat error must fail closed: want 503, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), dir) {
		t.Fatal("fail-closed response leaked a path")
	}
}
