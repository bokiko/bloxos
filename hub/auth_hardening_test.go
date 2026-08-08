package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
)

func signHS256(t *testing.T, claims jwt.MapClaims, secret []byte) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := tok.SignedString(secret)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return s
}

func ctxWithBearer(e *echo.Echo, token string) echo.Context {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec)
}

// TestExtractUserIDValidToken: a properly signed, unexpired token yields its
// user_id.
func TestExtractUserIDValidToken(t *testing.T) {
	e, _ := setupTestServer(t)
	tok := signHS256(t, jwt.MapClaims{"user_id": "user-123", "exp": time.Now().Add(time.Hour).Unix()}, jwtSecret)
	if got := extractUserIDFromRequest(ctxWithBearer(e, tok)); got != "user-123" {
		t.Fatalf("extractUserIDFromRequest = %q, want user-123", got)
	}
}

// TestExtractUserIDRejectsBadSignature: a token signed with the wrong key must
// not be trusted.
func TestExtractUserIDRejectsBadSignature(t *testing.T) {
	e, _ := setupTestServer(t)
	tok := signHS256(t, jwt.MapClaims{"user_id": "user-123", "exp": time.Now().Add(time.Hour).Unix()}, []byte("a-totally-different-signing-key-32b!"))
	if got := extractUserIDFromRequest(ctxWithBearer(e, tok)); got != "" {
		t.Fatalf("expected empty userID for bad signature, got %q", got)
	}
}

// TestExtractUserIDRejectsNoneAlg: an alg=none token must be rejected (the
// keyfunc must require HMAC).
func TestExtractUserIDRejectsNoneAlg(t *testing.T) {
	e, _ := setupTestServer(t)
	tok, err := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"user_id": "user-123", "exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none-alg token: %v", err)
	}
	if got := extractUserIDFromRequest(ctxWithBearer(e, tok)); got != "" {
		t.Fatalf("expected empty userID for alg=none token, got %q", got)
	}
}

// TestExtractUserIDRejectsExpired: an expired token must not be trusted.
func TestExtractUserIDRejectsExpired(t *testing.T) {
	e, _ := setupTestServer(t)
	tok := signHS256(t, jwt.MapClaims{"user_id": "user-123", "exp": time.Now().Add(-time.Hour).Unix()}, jwtSecret)
	if got := extractUserIDFromRequest(ctxWithBearer(e, tok)); got != "" {
		t.Fatalf("expected empty userID for expired token, got %q", got)
	}
}

// TestLoginUnknownUsernameRunsDummyCompare: an unknown username still returns
// 401 and pays a bcrypt comparison (dummy hash), so its response time is
// comparable to a real user's — closing the username-enumeration timing oracle.
// Before the fix the unknown-username path returned in well under a millisecond.
func TestLoginUnknownUsernameRunsDummyCompare(t *testing.T) {
	e, _ := setupTestServer(t)

	body := `{"username":"definitely-not-a-real-user","password":"whatever-password"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	start := time.Now()
	e.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unknown username, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid credentials") {
		t.Fatalf("expected 'invalid credentials', got %s", rec.Body.String())
	}
	// bcrypt at DefaultCost reliably takes tens of milliseconds; a no-bcrypt
	// fast path is sub-millisecond. Generous 15ms threshold avoids CI flake.
	if elapsed < 15*time.Millisecond {
		t.Fatalf("unknown-username login returned in %v; dummy bcrypt compare appears to be skipped", elapsed)
	}
}

// TestValidateEnvJWTSecret: the env-provided JWT secret must meet the same
// minimum length as the file-backed secret.
func TestValidateEnvJWTSecret(t *testing.T) {
	if _, err := validateEnvJWTSecret(strings.Repeat("a", 32)); err != nil {
		t.Fatalf("32-byte secret should be accepted: %v", err)
	}
	if _, err := validateEnvJWTSecret(strings.Repeat("a", 64)); err != nil {
		t.Fatalf("64-byte secret should be accepted: %v", err)
	}
	if _, err := validateEnvJWTSecret(strings.Repeat("a", 31)); err == nil {
		t.Fatal("31-byte secret should be rejected")
	}
	if _, err := validateEnvJWTSecret(""); err == nil {
		t.Fatal("empty secret should be rejected")
	}
}
