package main

import (
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"
)

// getJoinAt performs GET on an arbitrary path (so tests can hit both the
// canonical /api/join/<code> and the legacy /join/<code>).
func getJoinAt(t *testing.T, e interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, path, host string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if host != "" {
		req.Host = host
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// TestJoinCanonicalAPIPathMatchesLegacy: /api/join/<code> and /join/<code>
// share one handler, so a fresh token serves the same bootstrap on both, and
// neither GET consumes the token — repeated fetches keep succeeding.
func TestJoinCanonicalAPIPathMatchesLegacy(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://hub.public.example")
	got := mintJoinToken(t, e, loginAndGetToken(t, e), "evil.example")

	// The canonical path serves the bootstrap with no auth header (public).
	api := getJoinAt(t, e, "/api/join/"+got.Token, "")
	if api.Code != http.StatusOK {
		t.Fatalf("/api/join: status=%d body=%q", api.Code, api.Body.String())
	}
	assertJoinResponseHeaders(t, api)

	legacy := getJoinAt(t, e, "/join/"+got.Token, "")
	if legacy.Code != http.StatusOK {
		t.Fatalf("/join: status=%d body=%q", legacy.Code, legacy.Body.String())
	}
	if api.Body.String() != legacy.Body.String() {
		t.Fatalf("canonical and legacy bodies differ")
	}

	// No-consume: a third fetch (canonical) still serves — only
	// enrollment_committed consumes the token.
	if again := getJoinAt(t, e, "/api/join/"+got.Token, ""); again.Code != http.StatusOK {
		t.Fatalf("repeated /api/join GET status=%d (should not consume)", again.Code)
	}

	// The served script must carry the mint-time pin/origin and never the
	// request Host, on the canonical path exactly as on the legacy one.
	body := api.Body.String()
	if !strings.Contains(body, "hub.public.example") {
		t.Fatalf("served script missing PUBLIC_URL origin")
	}
	if strings.Contains(body, "evil.example") {
		t.Fatalf("served script leaked request Host")
	}
}

// TestJoinMintedURLIsCanonical: newly minted links point at /api/join/ (what
// old proxies forward), never the bare /join.
func TestJoinMintedURLIsCanonical(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://hub.public.example")
	got := mintJoinToken(t, e, loginAndGetToken(t, e), "evil.example")

	if got.JoinURL != "https://hub.public.example/api/join/"+got.Token {
		t.Fatalf("join_url = %q, want canonical /api/join path", got.JoinURL)
	}
	if !strings.Contains(got.Command, got.JoinURL) {
		t.Fatalf("short command missing canonical join URL: %q", got.Command)
	}
}

// TestJoinCanonicalUnusableCodesOpaque: unknown/expired/consumed codes on the
// canonical path get the same opaque 404 as the legacy path.
func TestJoinCanonicalUnusableCodesOpaque(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://hub.example")

	expired := "expired-code-api-3c0a4e7f"
	h := sha256.Sum256([]byte(expired))
	if _, err := s.db.Exec(`INSERT INTO tokens (token_hash, expires_at, used) VALUES (?, ?, FALSE)`,
		hexOf(h[:]), time.Now().UTC().Add(-time.Minute).Format("2006-01-02 15:04:05")); err != nil {
		t.Fatal(err)
	}
	used := s.seedTokenValue(t, "used-code-api-9b1d2e6a")
	if _, err := s.db.Exec(`UPDATE tokens SET used = TRUE WHERE token_hash = ?`, hashOf(used)); err != nil {
		t.Fatal(err)
	}

	for name, code := range map[string]string{
		"unknown": "never-minted-code-api",
		"expired": expired,
		"used":    used,
	} {
		rec := getJoinAt(t, e, "/api/join/"+code, "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status=%d, want 404", name, rec.Code)
		}
		assertJoinResponseHeaders(t, rec)
		if rec.Body.String() != joinUnavailableBody {
			t.Fatalf("%s: body=%q, want shared unavailable text", name, rec.Body.String())
		}
	}
}

// TestRedactJoinCodesCoversAPIPath: the log redactor scrubs the code on the
// canonical /api/join/ path too, since it matches the "/join/" substring.
func TestRedactJoinCodesCoversAPIPath(t *testing.T) {
	cases := map[string]string{
		"GET /api/join/0f9d2c1e-secret 200 1ms\n": "GET /api/join/[REDACTED] 200 1ms\n",
		"GET /api/join/abc?x=1 404":               "GET /api/join/[REDACTED]?x=1 404",
		"GET /api/join/ 404":                      "GET /api/join/ 404",
	}
	for in, want := range cases {
		if got := redactJoinCodes(in); got != want {
			t.Errorf("redactJoinCodes(%q) = %q, want %q", in, got, want)
		}
	}
}

const syntheticNext404 = "<!DOCTYPE html><html><head><title>404: This page could not be found</title></head><body>404 | This page could not be found.</body></html>"

// TestJoinThroughLegacyProxy is the root-cause regression: a reverse proxy
// that forwards ONLY /api/*, /install.sh and /download/* to the hub — and
// answers a bare /join with a synthetic Next.js HTML 404, exactly like the old
// deployment that broke — must still onboard through the canonical URL.
//
// PUBLIC_URL is the proxy origin, so the minted link is what a real operator
// would paste. Fetching that link through the proxy reaches the hub and serves
// the bootstrap; the legacy /join path for the same token hits the SPA 404;
// and a repeated canonical fetch still succeeds (the token is not consumed).
func TestJoinThroughLegacyProxy(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)

	hub := httptest.NewServer(e)
	defer hub.Close()
	hubURL, err := url.Parse(hub.URL)
	if err != nil {
		t.Fatal(err)
	}
	rp := httputil.NewSingleHostReverseProxy(hubURL)

	mux := http.NewServeMux()
	forward := func(w http.ResponseWriter, r *http.Request) { rp.ServeHTTP(w, r) }
	// Legacy proxy contract: only these prefixes reach the hub.
	mux.HandleFunc("/api/", forward)
	mux.HandleFunc("/install.sh", forward)
	mux.HandleFunc("/download/", forward)
	// A bare /join is NOT forwarded — the SPA serves an HTML 404, as the old
	// deployment did.
	mux.HandleFunc("/join/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, syntheticNext404)
	})
	proxy := httptest.NewServer(mux)
	defer proxy.Close()

	// Mint with the proxy as the public origin, so the link is canonical.
	t.Setenv("PUBLIC_URL", proxy.URL)
	got := mintJoinToken(t, e, loginAndGetToken(t, e), "evil.example")
	if got.JoinURL != proxy.URL+"/api/join/"+got.Token {
		t.Fatalf("join_url = %q, want %s/api/join/%s", got.JoinURL, proxy.URL, got.Token)
	}
	if !strings.Contains(got.Command, got.JoinURL) {
		t.Fatalf("short command does not carry the canonical URL: %q", got.Command)
	}

	// The pasted link, through the proxy, reaches the hub and serves the
	// exact advanced bootstrap.
	wantBody := "#!/bin/bash\n" + got.AdvancedCommand + "\n"
	for i := 0; i < 2; i++ { // repeat proves the GET does not consume the token
		resp, err := http.Get(got.JoinURL)
		if err != nil {
			t.Fatalf("GET %s (attempt %d): %v", got.JoinURL, i+1, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("canonical via proxy (attempt %d): status=%d", i+1, resp.StatusCode)
		}
		if string(body) != wantBody {
			t.Fatalf("canonical body via proxy (attempt %d) does not match the advanced bootstrap", i+1)
		}
	}

	// The legacy path for the same token hits the SPA 404, never the hub.
	legacyResp, err := http.Get(proxy.URL + "/join/" + got.Token)
	if err != nil {
		t.Fatalf("GET legacy via proxy: %v", err)
	}
	legacyBody, _ := io.ReadAll(legacyResp.Body)
	legacyResp.Body.Close()
	if legacyResp.StatusCode != http.StatusNotFound {
		t.Fatalf("legacy via proxy: status=%d, want 404", legacyResp.StatusCode)
	}
	if !strings.Contains(string(legacyBody), "could not be found") {
		t.Fatalf("legacy via proxy should be the SPA HTML 404, got %q", legacyBody)
	}
}
