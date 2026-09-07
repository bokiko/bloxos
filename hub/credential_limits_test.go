package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCredentialChangeAttemptsAreAccountLimited(t *testing.T) {
	for _, tc := range []struct{ endpoint, wrong, correct string }{
		{"change-pin", `{"current_pin":"wrong","new_pin":"9999"}`, `{"current_pin":"1234","new_pin":"9999"}`},
		{"change-password", `{"current_password":"wrong","new_password":"newpassword123"}`, `{"current_password":"bloxos","new_password":"newpassword123"}`},
	} {
		t.Run(tc.endpoint, func(t *testing.T) {
			e, _ := setupTestServer(t)
			token := loginAndGetToken(t, e)
			for attempt := 0; attempt < 7; attempt++ {
				body := tc.wrong
				if attempt == 6 {
					body = tc.correct
				}
				req := httptest.NewRequest(http.MethodPost, "/api/auth/"+tc.endpoint, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer "+token)
				// Changing source IP cannot bypass an authenticated account budget.
				req.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", attempt+1)
				rec := httptest.NewRecorder()
				e.ServeHTTP(rec, req)
				want := http.StatusForbidden
				if attempt >= 5 {
					want = http.StatusTooManyRequests
				}
				if rec.Code != want {
					t.Fatalf("attempt %d: status %d, want %d", attempt+1, rec.Code, want)
				}
				if attempt >= 5 && rec.Header().Get("Retry-After") != "60" {
					t.Fatal("missing retry guidance")
				}
			}
		})
	}
}
