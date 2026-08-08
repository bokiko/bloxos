package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantErr  bool
		wantText string
	}{
		{"empty", "", true, "password must be at least 8 characters"},
		{"seven_chars", "1234567", true, "password must be at least 8 characters"},
		{"eight_chars", "12345678", false, ""},
		{"nine_chars", "123456789", false, ""},
		{"long", strings.Repeat("a", 200), false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePassword(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if err.Error() != tc.wantText {
					t.Fatalf("error text: got %q want %q", err.Error(), tc.wantText)
				}
			} else if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

// Regression: handleChangePassword used to enforce 6 chars while every other
// endpoint enforced 8. A 7-char password should now be rejected by all four
// password-accepting endpoints with the same error message.
func TestChangePasswordRejectsSevenCharPassword(t *testing.T) {
	e, _ := setupTestServer(t)
	token := loginAndGetToken(t, e)

	body := `{"current_password":"bloxos","new_password":"7chars7"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/change-password", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "at least 8 characters") {
		t.Fatalf("expected unified policy error, got %s", rec.Body.String())
	}
}

func TestAdminCreateUserRejectsSevenCharPassword(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	adminToken := loginAndGetToken(t, e)

	body := `{"username":"shorty","password":"7chars7","pin":"1234","role":"viewer"}`
	req := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "at least 8 characters") {
		t.Fatalf("expected unified policy error, got %s", rec.Body.String())
	}
}
