package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// testJoinPin is what the stubbed resolver returns: a syntactically valid
// base64 SHA-256 that no real server presents.
const testJoinPin = "dGVzdC1qb2luLXBpbi1ub3QtYS1yZWFsLWtleS0wMDA="

// stubJoinPinResolver replaces the TLS-dialling pin resolver for the life of
// a test so that no test minting a token behind a private CA reaches the
// network. setupTestServer installs it; tests that need a failing or
// recording resolver call withJoinPinResolver on top.
func stubJoinPinResolver(t *testing.T) {
	t.Helper()
	withJoinPinResolver(t, func(context.Context, *url.URL, []byte) (string, error) {
		return testJoinPin, nil
	})
}

func withJoinPinResolver(t *testing.T, fn joinPinResolver) {
	t.Helper()
	previous := resolveJoinPin
	resolveJoinPin = fn
	t.Cleanup(func() { resolveJoinPin = previous })
}

// mintJoinToken creates a token through the real handler and returns the
// typed response. host is the request Host header, which must never leak
// into anything the hub generates.
func mintJoinToken(t *testing.T, e interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, jwt, host string) installerTokenResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/tokens", nil)
	req.Host = host
	req.Header.Set("Authorization", "Bearer "+jwt)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create token status=%d: %s", rec.Code, rec.Body.String())
	}
	var got installerTokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	return got
}

func getJoin(t *testing.T, e interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, code, host string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/join/"+code, nil)
	if host != "" {
		req.Host = host
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// assertJoinResponseHeaders pins the headers every /join answer carries,
// success or not: never cached, never sniffed, plain text.
func assertJoinResponseHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", got)
	}
}

// captureHubLog routes the standard logger into a buffer for the test.
func captureHubLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &buf
}

// TestJoinServesTheAdvancedCommandForAFreshToken is the product contract:
// the short command fetches join_url, and what join_url serves is exactly
// the verbose bootstrap /api/tokens also returns, addressed by PUBLIC_URL,
// carrying the token and the CA bootstrap.
func TestJoinServesTheAdvancedCommandForAFreshToken(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://hub.public.example")
	t.Setenv("BLOXOS_CA_CERT", testCAFile(t))
	got := mintJoinToken(t, e, loginAndGetToken(t, e), "evil.example")

	if got.JoinURL != "https://hub.public.example/join/"+got.Token {
		t.Fatalf("join_url = %q", got.JoinURL)
	}
	rec := getJoin(t, e, got.Token, "attacker.evil.example")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET join status=%d: %s", rec.Code, rec.Body.String())
	}
	assertJoinResponseHeaders(t, rec)
	script := rec.Body.String()
	if want := "#!/bin/bash\n" + got.AdvancedCommand + "\n"; script != want {
		t.Fatalf("join script is not the advanced command:\n--- got\n%s\n--- want\n%s", script, want)
	}
	for _, needle := range []string{
		"HUB_HTTP='https://hub.public.example'",
		"HUB_WS='wss://hub.public.example'",
		"TOKEN='" + got.Token + "'",
		"CA_URL='https://hub.public.example/download/ca.crt'",
		"CA_SHA256='" + got.CASHA256 + "'",
		`curl --proto '=https' --tlsv1.2 -fsSL "${CA_ARGS[@]}" "$HUB_HTTP/install.sh"`,
		`bash "$INSTALLER"`,
	} {
		if !strings.Contains(script, needle) {
			t.Errorf("join script lacks %q", needle)
		}
	}
	if strings.Contains(script, "evil.example") {
		t.Fatal("join script leaked a request Host header")
	}

	if bash, err := exec.LookPath("bash"); err == nil {
		path := filepath.Join(t.TempDir(), "join.sh")
		if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(bash, "-n", path).CombinedOutput(); err != nil {
			t.Fatalf("bash -n join script: %v\n%s", err, out)
		}
	}
}

// TestJoinCommandShapes pins the three commands /api/tokens can return and
// that none of them is an unauthenticated network pipe.
func TestJoinCommandShapes(t *testing.T) {
	t.Run("private CA pins the presented key", func(t *testing.T) {
		e, s := setupTestServer(t)
		s.markCredentialsRotated(t)
		t.Setenv("PUBLIC_URL", "https://hub.lan:8443")
		t.Setenv("BLOXOS_CA_CERT", testCAFile(t))
		var sawURL string
		var sawPEM []byte
		withJoinPinResolver(t, func(_ context.Context, u *url.URL, caPEM []byte) (string, error) {
			sawURL, sawPEM = u.String(), caPEM
			return testJoinPin, nil
		})
		got := mintJoinToken(t, e, loginAndGetToken(t, e), "evil.example")
		if sawURL != "https://hub.lan:8443" || string(sawPEM) != "test-private-ca" {
			t.Fatalf("resolver saw url=%q pem=%q; must be the explicit PUBLIC_URL and the configured CA", sawURL, sawPEM)
		}
		want := `bash -c 's=$(curl -fsSk --pinnedpubkey sha256//` + testJoinPin + ` https://hub.lan:8443/join/` + got.Token + `) && bash -c "$s"'`
		if got.Command != want {
			t.Fatalf("command = %q\nwant      %q", got.Command, want)
		}
		if got.JoinPin != testJoinPin {
			t.Fatalf("join_pin = %q", got.JoinPin)
		}
		if got.AdvancedCommand != buildLinuxInstallCommand("https://hub.lan:8443", "wss://hub.lan:8443", got.Token, got.CAURL, got.CASHA256) {
			t.Fatal("advanced_command is not the verbose bootstrap")
		}
		if got.WindowsCommand != buildWindowsInstallCommand("https://hub.lan:8443", "wss://hub.lan:8443", got.Token, got.CAURL, got.CASHA256, false) {
			t.Fatal("windows_command changed; it must stay the full verified paste block")
		}
	})

	t.Run("publicly trusted https is plain verified TLS", func(t *testing.T) {
		e, s := setupTestServer(t)
		s.markCredentialsRotated(t)
		t.Setenv("PUBLIC_URL", "https://hub.example.com")
		t.Setenv("BLOXOS_CA_CERT", filepath.Join(t.TempDir(), "missing.crt"))
		withJoinPinResolver(t, func(context.Context, *url.URL, []byte) (string, error) {
			t.Fatal("pin resolver must not run without a private CA")
			return "", nil
		})
		got := mintJoinToken(t, e, loginAndGetToken(t, e), "evil.example")
		want := `bash -c 's=$(curl -fsS https://hub.example.com/join/` + got.Token + `) && bash -c "$s"'`
		if got.Command != want {
			t.Fatalf("command = %q\nwant      %q", got.Command, want)
		}
		if got.JoinPin != "" {
			t.Fatalf("join_pin = %q, want empty", got.JoinPin)
		}
	})

	t.Run("http keeps the existing plaintext policy without new flags", func(t *testing.T) {
		e, s := setupTestServer(t)
		s.markCredentialsRotated(t)
		t.Setenv("PUBLIC_URL", "http://127.0.0.1:4000")
		t.Setenv("BLOXOS_CA_CERT", filepath.Join(t.TempDir(), "missing.crt"))
		got := mintJoinToken(t, e, loginAndGetToken(t, e), "evil.example")
		want := `bash -c 's=$(curl -fsS http://127.0.0.1:4000/join/` + got.Token + `) && bash -c "$s"'`
		if got.Command != want {
			t.Fatalf("command = %q\nwant      %q", got.Command, want)
		}
		if !strings.Contains(got.AdvancedCommand, "unencrypted") {
			t.Fatal("the served bootstrap must still print the plaintext warning")
		}
	})

	t.Run("no trust-all pipe in any command", func(t *testing.T) {
		for _, tc := range []struct{ url, ca string }{
			{"https://hub.lan", testCAFile(t)},
			{"https://hub.example.com", filepath.Join(t.TempDir(), "missing.crt")},
			{"http://127.0.0.1:4000", filepath.Join(t.TempDir(), "missing.crt")},
		} {
			e, s := setupTestServer(t)
			s.markCredentialsRotated(t)
			t.Setenv("PUBLIC_URL", tc.url)
			t.Setenv("BLOXOS_CA_CERT", tc.ca)
			got := mintJoinToken(t, e, loginAndGetToken(t, e), "evil.example")
			for name, command := range map[string]string{"command": got.Command, "advanced_command": got.AdvancedCommand} {
				if strings.Contains(command, "| bash") || strings.Contains(command, "| sh") || strings.Contains(command, "|bash") {
					t.Errorf("%s for %s pipes the network into a shell: %q", name, tc.url, command)
				}
			}
			insecure := strings.Contains(got.Command, " -k") || strings.Contains(got.Command, "-fsSk") || strings.Contains(got.Command, "--insecure")
			if insecure && !strings.Contains(got.Command, "--pinnedpubkey sha256//") {
				t.Errorf("command for %s disables verification without pinning: %q", tc.url, got.Command)
			}
			if strings.HasPrefix(tc.url, "http://") && insecure {
				t.Errorf("http command for %s gained TLS flags: %q", tc.url, got.Command)
			}
			if !strings.HasPrefix(got.Command, `bash -c 's=$(curl -fsS`) || !strings.HasSuffix(got.Command, `) && bash -c "$s"'`) {
				t.Errorf("command for %s is not the download-then-run form: %q", tc.url, got.Command)
			}
		}
	})
}

// TestJoinRefusesToMintWithoutATrustworthyPin: behind a private CA, a pin
// the hub cannot verify means no token at all, not a weaker command.
func TestJoinRefusesToMintWithoutATrustworthyPin(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://hub.lan")
	t.Setenv("BLOXOS_CA_CERT", testCAFile(t))
	withJoinPinResolver(t, func(context.Context, *url.URL, []byte) (string, error) {
		return "", errors.New("TLS handshake with hub.lan:443 using the configured CA failed: x509: certificate signed by unknown authority")
	})
	var before int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tokens`).Scan(&before); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+loginAndGetToken(t, e))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s, want 503", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unknown authority") || !strings.Contains(rec.Body.String(), "BLOXOS_CA_CERT") {
		t.Fatalf("error does not explain the failure: %s", rec.Body.String())
	}
	var after int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tokens`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("a token was inserted (%d -> %d) although no command could be produced", before, after)
	}
}

// TestJoinUnusableCodesAreIndistinguishable: unknown, expired, consumed and
// Windows-bound codes get byte-identical 404s, and none of them reaches the
// hub log.
func TestJoinUnusableCodesAreIndistinguishable(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://hub.example")
	logBuf := captureHubLog(t)

	expired := "expired-code-3c0a4e7f"
	h := sha256.Sum256([]byte(expired))
	if _, err := s.db.Exec(`INSERT INTO tokens (token_hash, expires_at, used) VALUES (?, ?, FALSE)`,
		hexOf(h[:]), time.Now().UTC().Add(-time.Minute).Format("2006-01-02 15:04:05")); err != nil {
		t.Fatal(err)
	}
	used := s.seedTokenValue(t, "used-code-9b1d2e6a")
	if _, err := s.db.Exec(`UPDATE tokens SET used = TRUE WHERE token_hash = ?`, hashOf(used)); err != nil {
		t.Fatal(err)
	}
	windowsBound := "windows-code-5e7f8a9b"
	hw := sha256.Sum256([]byte(windowsBound))
	if _, err := s.db.Exec(`INSERT INTO tokens (token_hash, expires_at, target_machine_id) VALUES (?, ?, ?)`,
		hexOf(hw[:]), time.Now().UTC().Add(time.Hour).Format("2006-01-02 15:04:05"), "win-machine"); err != nil {
		t.Fatal(err)
	}

	codes := map[string]string{
		"unknown":       "never-minted-code-1a2b3c4d",
		"expired":       expired,
		"used":          used,
		"windows-bound": windowsBound,
		"empty-ish":     "%20",
		"oversized":     strings.Repeat("x", 300),
	}
	var reference *httptest.ResponseRecorder
	for name, code := range codes {
		rec := getJoin(t, e, code, "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s code: status=%d body=%q, want 404", name, rec.Code, rec.Body.String())
		}
		assertJoinResponseHeaders(t, rec)
		if rec.Body.String() != joinUnavailableBody {
			t.Fatalf("%s code: body=%q, want the shared unavailable text", name, rec.Body.String())
		}
		if reference == nil {
			reference = rec
			continue
		}
		if rec.Body.String() != reference.Body.String() || rec.Header().Get("Content-Type") != reference.Header().Get("Content-Type") {
			t.Fatalf("%s code is distinguishable from the first unusable code", name)
		}
	}
	// A usable code is the only thing that changes the answer.
	live := s.seedTokenValue(t, "live-code-7d8e9f0a")
	if rec := getJoin(t, e, live, ""); rec.Code != http.StatusOK {
		t.Fatalf("live code status=%d", rec.Code)
	}
	for name, code := range codes {
		if name == "oversized" || name == "empty-ish" {
			continue
		}
		if strings.Contains(logBuf.String(), code) {
			t.Fatalf("hub log contains the %s join code", name)
		}
	}
	if strings.Contains(logBuf.String(), live) {
		t.Fatal("hub log contains a live join code")
	}
}

// TestJoinIsRetryableUntilTheDurableCommit drives the real enrollment
// handshake: the link keeps working through the download, through a first
// connection that dies before committing, and goes dead the moment
// enrollment_committed stores the credential.
func TestJoinIsRetryableUntilTheDurableCommit(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://hub.example")
	server := httptest.NewServer(e)
	defer server.Close()
	got := mintJoinToken(t, e, loginAndGetToken(t, e), "evil.example")
	token := got.Token
	machineID := "join-retry-machine"

	for i := 0; i < 3; i++ {
		if rec := getJoin(t, e, token, ""); rec.Code != http.StatusOK {
			t.Fatalf("GET %d before enrollment: status=%d", i, rec.Code)
		}
	}
	if tokenUsed(t, s, hashOf(token)) {
		t.Fatal("GET /join consumed the token")
	}

	conn1, err := wsDialAgent(t, server, "token="+token)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	sendMetricsMsg(t, conn1, machineID)
	readEnrolledSecret(t, conn1)
	// The agent received its secret but has not committed: the link must
	// still work, because this install can still fail and be re-run.
	if rec := getJoin(t, e, token, ""); rec.Code != http.StatusOK {
		t.Fatalf("GET after enrolled-but-uncommitted: status=%d", rec.Code)
	}
	conn1.Close()
	s.waitAgentDrain(t, machineID, 2*time.Second)
	if rec := getJoin(t, e, token, ""); rec.Code != http.StatusOK {
		t.Fatalf("GET after a dropped connection: status=%d", rec.Code)
	}

	conn2, err := wsDialAgent(t, server, "token="+token)
	if err != nil {
		t.Fatalf("retry dial: %v", err)
	}
	defer conn2.Close()
	sendMetricsMsg(t, conn2, machineID)
	secret := readEnrolledSecret(t, conn2)
	if err := conn2.WriteMessage(websocket.TextMessage, []byte(`{"type":"enrollment_committed"}`)); err != nil {
		t.Fatalf("send enrollment_committed: %v", err)
	}
	if h := readEnrollmentConfirmedHash(t, conn2); h != hashOf(secret) {
		t.Fatalf("confirmation hash %q, want %q", h, hashOf(secret))
	}
	if !tokenUsed(t, s, hashOf(token)) {
		t.Fatal("commit did not consume the token")
	}
	rec := getJoin(t, e, token, "")
	if rec.Code != http.StatusNotFound || rec.Body.String() != joinUnavailableBody {
		t.Fatalf("GET after the durable commit: status=%d body=%q", rec.Code, rec.Body.String())
	}
}

// TestJoinConcurrentReadsAcrossTheCommitBoundary hammers /join from many
// goroutines while the token is consumed. Every answer must be one of the
// two legal ones, nothing after the commit may succeed, and the race
// detector must stay quiet.
func TestJoinConcurrentReadsAcrossTheCommitBoundary(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://hub.example")
	got := mintJoinToken(t, e, loginAndGetToken(t, e), "evil.example")
	token := got.Token

	const readers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	var mu sync.Mutex
	var bad []string
	var committedAtNanos atomic.Int64 // 0 until the durable commit returns
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 40; i++ {
				at := time.Now().UnixNano()
				rec := getJoin(t, e, token, "")
				switch {
				case rec.Code == http.StatusOK && strings.HasPrefix(rec.Body.String(), "#!/bin/bash\n"):
					if committed := committedAtNanos.Load(); committed != 0 && at > committed {
						mu.Lock()
						bad = append(bad, "200 after the durable commit")
						mu.Unlock()
					}
				case rec.Code == http.StatusNotFound && rec.Body.String() == joinUnavailableBody:
				default:
					mu.Lock()
					bad = append(bad, rec.Body.String())
					mu.Unlock()
				}
			}
		}()
	}
	close(start)
	time.Sleep(20 * time.Millisecond)
	if err := s.consumeTokenAndStoreCredential(hashOf(token), "join-race-machine", hashOf("secret")); err != nil {
		t.Fatalf("commit: %v", err)
	}
	committedAtNanos.Store(time.Now().UnixNano())
	wg.Wait()
	if len(bad) > 0 {
		t.Fatalf("illegal /join answers: %q", bad)
	}
	if rec := getJoin(t, e, token, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("after commit: status=%d", rec.Code)
	}
}

// TestJoinMethodAndAuthorityHandling: only GET is served, /join needs no
// session, and the script's authority comes from PUBLIC_URL alone.
func TestJoinMethodAndAuthorityHandling(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://hub.example")
	code := s.seedTokenValue(t, "method-code-2b3c4d5e")

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req := httptest.NewRequest(method, "/join/"+code, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		// Echo answers an unregistered method with 405, or with 401 when
		// the protected group's catch-all sees it first; either way it is
		// not served.
		if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusUnauthorized {
			t.Errorf("%s /join: status=%d, want 405 or 401", method, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "HUB_HTTP=") {
			t.Errorf("%s /join served the script", method)
		}
	}
	rec := getJoin(t, e, code, "attacker.evil.example:8443")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "HUB_HTTP='https://hub.example'") || strings.Contains(rec.Body.String(), "attacker") {
		t.Fatalf("script authority is not PUBLIC_URL: %s", rec.Body.String())
	}
	// The token row is untouched by any of the above.
	if tokenUsed(t, s, hashOf(code)) {
		t.Fatal("token was consumed by a read")
	}
}

// TestJoinRequiresPublicURLAtMintAndAtServe: no Host-derived authority in
// either direction.
func TestJoinRequiresPublicURLAtMintAndAtServe(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	code := s.seedTokenValue(t, "nopublic-code-6f7a8b9c")
	t.Setenv("PUBLIC_URL", "")
	rec := getJoin(t, e, code, "hub.example")
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "hub.example") {
		t.Fatalf("serving without PUBLIC_URL: status=%d body=%q", rec.Code, rec.Body.String())
	}
	for _, bad := range []string{"ftp://hub.example", "https://user:pw@hub.example", "https://hub.example/?x=1", "hub.example"} {
		t.Setenv("PUBLIC_URL", bad)
		req := httptest.NewRequest(http.MethodPost, "/api/tokens", nil)
		req.Header.Set("Authorization", "Bearer "+loginAndGetToken(t, e))
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("PUBLIC_URL=%q: status=%d, want 400", bad, rec.Code)
		}
		rateLimiter = NewRateLimiter()
	}
}

// TestJoinCommandQuotesUnsafeShellWords: anything outside the bare-word set
// is double-quoted inside the single-quoted wrapper so the pasted line
// cannot glob or split, and a PUBLIC_URL that cannot be made safe is
// refused at mint.
func TestJoinCommandQuotesUnsafeShellWords(t *testing.T) {
	if got := buildLinuxJoinCommand("https://[fd00::1]:8443/join/abc", "AbC+/="); got != `bash -c 's=$(curl -fsSk --pinnedpubkey sha256//AbC+/= "https://[fd00::1]:8443/join/abc") && bash -c "$s"'` {
		t.Fatalf("IPv6 join URL: %q", got)
	}
	// A pin is only meaningful over https.
	if got := buildLinuxJoinCommand("http://127.0.0.1:4000/join/abc", "AbC="); got != `bash -c 's=$(curl -fsS http://127.0.0.1:4000/join/abc) && bash -c "$s"'` {
		t.Fatalf("http with a pin: %q", got)
	}
	for _, bad := range []string{"https://hub.example/a b", "https://hub.example/it's", "https://hub.example/$(x)", "https://hub.example/a\"b"} {
		if _, err := parsePublicURL(bad); err == nil {
			t.Errorf("parsePublicURL(%q) accepted a shell-unsafe URL", bad)
		}
	}
	for _, ok := range []string{"https://hub.example", "https://[fd00::1]:8443", "https://hub.example/bloxos", "http://10.0.0.5:4000"} {
		if _, err := parsePublicURL(ok); err != nil {
			t.Errorf("parsePublicURL(%q): %v", ok, err)
		}
	}
}

// TestJoinCommandExitSemantics runs the generated command with the real
// bash and curl against local servers. A failed fetch — expired link, key
// pin mismatch — must exit non-zero and execute nothing; a successful fetch
// must run the script and return its exit status.
func TestJoinCommandExitSemantics(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not available")
	}
	marker := filepath.Join(t.TempDir(), "ran")
	script := "#!/bin/bash\nprintf ran > " + marker + "\nexit 7\n"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if r.URL.Path == "/join/live" {
			_, _ = w.Write([]byte(script))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(joinUnavailableBody))
	})
	run := func(t *testing.T, command string) (int, string) {
		t.Helper()
		_ = os.Remove(marker)
		cmd := exec.Command(bash, "-c", command)
		cmd.Env = append(os.Environ(), "CURL_HOME="+t.TempDir())
		out, err := cmd.CombinedOutput()
		code := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else if err != nil {
			t.Fatalf("run: %v", err)
		}
		return code, string(out)
	}
	ran := func() bool { _, err := os.Stat(marker); return err == nil }

	plain := httptest.NewServer(handler)
	defer plain.Close()
	t.Run("live link runs the script and returns its status", func(t *testing.T) {
		code, out := run(t, buildLinuxJoinCommand(plain.URL+"/join/live", ""))
		if code != 7 || !ran() {
			t.Fatalf("exit=%d ran=%v out=%q", code, ran(), out)
		}
	})
	t.Run("dead link exits non-zero and runs nothing", func(t *testing.T) {
		code, out := run(t, buildLinuxJoinCommand(plain.URL+"/join/gone", ""))
		if code == 0 || ran() {
			t.Fatalf("exit=%d ran=%v out=%q", code, ran(), out)
		}
		if !strings.Contains(out, "404") {
			t.Fatalf("curl did not say why: %q", out)
		}
	})
	t.Run("unreachable hub exits non-zero and runs nothing", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		ln.Close()
		code, out := run(t, buildLinuxJoinCommand("http://"+addr+"/join/live", ""))
		if code == 0 || ran() {
			t.Fatalf("exit=%d ran=%v out=%q", code, ran(), out)
		}
	})

	tlsSrv := httptest.NewTLSServer(handler)
	defer tlsSrv.Close()
	t.Run("matching pin runs the script over a private CA", func(t *testing.T) {
		code, out := run(t, buildLinuxJoinCommand(tlsSrv.URL+"/join/live", spkiPinOf(tlsSrv.Certificate())))
		if code != 7 || !ran() {
			t.Fatalf("exit=%d ran=%v out=%q", code, ran(), out)
		}
	})
	t.Run("pin mismatch exits non-zero and runs nothing", func(t *testing.T) {
		wrong := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
		code, out := run(t, buildLinuxJoinCommand(tlsSrv.URL+"/join/live", wrong))
		if code == 0 || ran() {
			t.Fatalf("exit=%d ran=%v out=%q", code, ran(), out)
		}
		if !strings.Contains(out, "pinned public key") {
			t.Fatalf("curl did not name the pin failure: %q", out)
		}
	})
	t.Run("no pin over a private CA is refused by curl itself", func(t *testing.T) {
		code, _ := run(t, buildLinuxJoinCommand(tlsSrv.URL+"/join/live", ""))
		if code == 0 || ran() {
			t.Fatalf("unverifiable chain was accepted: exit=%d ran=%v", code, ran())
		}
	})
}

// TestRedactJoinCodes: the request logger's ${uri} must never carry a code.
func TestRedactJoinCodes(t *testing.T) {
	for in, want := range map[string]string{
		"2026-09-06T00:00:00Z GET /join/0f9d2c1e-aaaa-bbbb-cccc-123456789abc 200 1ms\n": "2026-09-06T00:00:00Z GET /join/[REDACTED] 200 1ms\n",
		"GET /join/abc?x=1 404":       "GET /join/[REDACTED]?x=1 404",
		"GET /join/ 404":              "GET /join/ 404",
		"GET /join/a/b 404 /join/c\n": "GET /join/[REDACTED]/b 404 /join/[REDACTED]\n",
		"GET /install.sh 200":         "GET /install.sh 200",
	} {
		if got := redactJoinCodes(in); got != want {
			t.Errorf("redactJoinCodes(%q) = %q, want %q", in, got, want)
		}
	}
	var out bytes.Buffer
	w := &tokenRedactingWriter{w: &out}
	if _, err := w.Write([]byte("GET /join/secret-code 200 token=abc\n")); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "GET /join/[REDACTED] 200 token=[REDACTED]\n" {
		t.Fatalf("writer output %q", got)
	}
	// Regression: the writer used to restart its search from the beginning
	// after every replacement and never returned for any line carrying a
	// value, spinning the request goroutine forever.
	out.Reset()
	line := "GET /ws/agent?token=abc&secret=def&x=1 101 token=ghi\n"
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = w.Write([]byte(line))
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("tokenRedactingWriter.Write did not return")
	}
	if got := out.String(); got != "GET /ws/agent?token=[REDACTED]&secret=[REDACTED]&x=1 101 token=[REDACTED]\n" {
		t.Fatalf("writer output %q", got)
	}
}

// --- the real resolver, against local TLS listeners only ---

func spkiPinOf(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func pemOf(cert *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
}

// selfSignedServer starts a TLS listener with a fresh self-signed CA leaf for
// 127.0.0.1 valid for the given duration, and returns the server plus the
// PEM to trust it with.
func selfSignedServer(t *testing.T, validFor time.Duration) (*httptest.Server, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "bloxos-join-test"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(validFor),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the pin resolver sent an HTTP request: %s %s", r.Method, r.URL)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, pemOf(cert)
}

func TestPinPresentedLeafSPKI(t *testing.T) {
	srv, caPEM := selfSignedServer(t, 24*time.Hour)
	u, _ := url.Parse(srv.URL)
	leaf := srv.Certificate()

	t.Run("returns the SPKI pin of the verified leaf without an HTTP request", func(t *testing.T) {
		got, err := pinPresentedLeafSPKI(context.Background(), u, caPEM)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got != spkiPinOf(leaf) {
			t.Fatalf("pin = %q, want %q", got, spkiPinOf(leaf))
		}
		if _, err := base64.StdEncoding.DecodeString(got); err != nil {
			t.Fatalf("pin is not base64: %v", err)
		}
	})

	t.Run("fails closed when the chain does not verify against the configured CA", func(t *testing.T) {
		other, _ := selfSignedServer(t, 24*time.Hour)
		if _, err := pinPresentedLeafSPKI(context.Background(), u, pemOf(other.Certificate())); err == nil {
			t.Fatal("expected verification failure against the wrong CA")
		} else if !strings.Contains(err.Error(), "using the configured CA failed") {
			t.Fatalf("error does not say what failed: %v", err)
		}
		if _, err := pinPresentedLeafSPKI(context.Background(), u, []byte("not a pem")); err == nil || !strings.Contains(err.Error(), "no PEM certificate") {
			t.Fatalf("unparseable CA: %v", err)
		}
	})

	t.Run("rejects non-https and hostless URLs before dialling", func(t *testing.T) {
		for _, raw := range []string{"http://127.0.0.1:1", "https://", "ftp://127.0.0.1"} {
			pu, _ := url.Parse(raw)
			if _, err := pinPresentedLeafSPKI(context.Background(), pu, caPEM); err == nil {
				t.Errorf("%q: expected an error", raw)
			}
		}
	})

	t.Run("refuses a leaf that expires before the command would", func(t *testing.T) {
		short, shortPEM := selfSignedServer(t, installTokenTTL/2)
		su, _ := url.Parse(short.URL)
		_, err := pinPresentedLeafSPKI(context.Background(), su, shortPEM)
		if err == nil || !strings.Contains(err.Error(), "expires at") {
			t.Fatalf("expected an expiry error, got %v", err)
		}
	})

	t.Run("is bounded by the context deadline against a silent listener", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				defer c.Close() // hold it open, never handshake
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		su, _ := url.Parse("https://" + ln.Addr().String())
		started := time.Now()
		if _, err := pinPresentedLeafSPKI(ctx, su, caPEM); err == nil {
			t.Fatal("expected a timeout")
		}
		if elapsed := time.Since(started); elapsed > 3*time.Second {
			t.Fatalf("resolver took %s; must honour the deadline", elapsed)
		}
	})
}

// TestPinDialAddress pins the dial-target override: default is PUBLIC_URL's
// host:port, BLOXOS_PIN_DIAL_ADDR replaces only that, and a malformed value
// fails closed rather than being ignored.
func TestPinDialAddress(t *testing.T) {
	mustURL := func(raw string) *url.URL {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	t.Run("defaults to PUBLIC_URL host:port", func(t *testing.T) {
		t.Setenv(pinDialAddrEnv, "")
		if got, err := pinDialAddress(mustURL("https://hub.example")); err != nil || got != "hub.example:443" {
			t.Fatalf("got %q err %v", got, err)
		}
		if got, _ := pinDialAddress(mustURL("https://hub.example:8443")); got != "hub.example:8443" {
			t.Fatalf("explicit port: %q", got)
		}
	})
	t.Run("override replaces only the dial target", func(t *testing.T) {
		t.Setenv(pinDialAddrEnv, "caddy:443")
		if got, err := pinDialAddress(mustURL("https://127.0.0.1")); err != nil || got != "caddy:443" {
			t.Fatalf("got %q err %v", got, err)
		}
	})
	t.Run("rejects a malformed override", func(t *testing.T) {
		for _, bad := range []string{"https://caddy:443", "caddy", "caddy/", ":443", "caddy:"} {
			t.Setenv(pinDialAddrEnv, bad)
			if _, err := pinDialAddress(mustURL("https://hub.example")); err == nil {
				t.Errorf("override %q accepted", bad)
			}
		}
	})
}

// TestPinDialOverrideConnectsElsewhereVerifiesPublicURLHost is the Compose
// case: the connection goes to an address other than PUBLIC_URL, but the
// certificate is still verified against PUBLIC_URL's hostname and the
// configured CA. Proves a wrong dial target cannot downgrade trust.
func TestPinDialOverrideConnectsElsewhereVerifiesPublicURLHost(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	// Cert valid for the DNS name "hub.internal", served on a 127.0.0.1 port.
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "hub.internal"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"hub.internal"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Errorf("the pin resolver sent an HTTP request")
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	srv.StartTLS()
	defer srv.Close()

	pu, err := url.Parse("https://hub.internal") // does not resolve; only the override is reachable
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(pinDialAddrEnv, strings.TrimPrefix(srv.URL, "https://"))
	got, err := pinPresentedLeafSPKI(context.Background(), pu, pemOf(cert))
	if err != nil {
		t.Fatalf("resolve with dial override: %v", err)
	}
	if got != spkiPinOf(cert) {
		t.Fatalf("pin = %q, want %q", got, spkiPinOf(cert))
	}

	// The same leaf under a PUBLIC_URL host it is not valid for must fail,
	// even though the dial still reaches it — verification is not bypassed.
	wrong, _ := url.Parse("https://other.invalid")
	if _, err := pinPresentedLeafSPKI(context.Background(), wrong, pemOf(cert)); err == nil {
		t.Fatal("expected verification failure for a host the cert does not cover")
	}
}

// TestJoinRebuildByteEquivalenceAcrossTransports: the rebuilt /join script is
// byte-identical to the minted advanced_command for a publicly trusted HTTPS
// hub (no CA binding) and for a plain-HTTP hub, not just the private-CA case.
func TestJoinRebuildByteEquivalenceAcrossTransports(t *testing.T) {
	for _, tc := range []struct {
		name, publicURL string
		setCA           bool
	}{
		{"public HTTPS, no CA", "https://public.example.com", false},
		{"plain HTTP", "http://10.0.0.5:4000", false},
		{"private CA", "https://hub.internal:8443", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, s := setupTestServer(t)
			s.markCredentialsRotated(t)
			t.Setenv("PUBLIC_URL", tc.publicURL)
			if tc.setCA {
				t.Setenv("BLOXOS_CA_CERT", testCAFile(t))
			} else {
				t.Setenv("BLOXOS_CA_CERT", filepath.Join(t.TempDir(), "absent.crt"))
			}
			got := mintJoinToken(t, e, loginAndGetToken(t, e), "client.example")
			rec := getJoin(t, e, got.Token, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("GET status=%d body=%q", rec.Code, rec.Body.String())
			}
			if want := "#!/bin/bash\n" + got.AdvancedCommand + "\n"; rec.Body.String() != want {
				t.Fatalf("rebuilt script != minted advanced_command:\n--- got\n%s\n--- want\n%s", rec.Body.String(), want)
			}
			// The token must not be at rest, whatever the transport.
			var mintScript sql.NullString
			if err := s.db.QueryRow(`SELECT mint_time_script FROM tokens WHERE token_hash = ?`, hashOf(got.Token)).Scan(&mintScript); err != nil {
				t.Fatalf("read row: %v", err)
			}
			if mintScript.Valid && mintScript.String != "" {
				t.Fatalf("mint_time_script populated for %s", tc.name)
			}
		})
	}
}

// TestJoinRejectsMissingBinding: a row whose binding columns are NULL (a
// pre-binding token, or a partial/missing binding) is not served, even though
// it is unexpired and unconsumed. An empty-string CA binding is still valid.
func TestJoinRejectsMissingBinding(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://hub.example.com")

	insert := func(raw string, httpBase, caSHA any) {
		h := sha256.Sum256([]byte(raw))
		if _, err := s.db.Exec(
			`INSERT INTO tokens (token_hash, expires_at, mint_time_http_base, mint_time_ca_sha256) VALUES (?, ?, ?, ?)`,
			hexOf(h[:]), time.Now().Add(time.Hour).Format("2006-01-02 15:04:05"), httpBase, caSHA); err != nil {
			t.Fatalf("seed %s: %v", raw, err)
		}
	}
	insert("both-null", nil, nil)
	insert("http-base-null", nil, "")
	insert("ca-null", "https://hub.example.com", nil)
	insert("empty-ca-ok", "https://hub.example.com", "") // valid: public TLS/HTTP binding

	for _, code := range []string{"both-null", "http-base-null", "ca-null"} {
		if rec := getJoin(t, e, code, ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status=%d, want 404 for a missing binding", code, rec.Code)
		}
	}
	if rec := getJoin(t, e, "empty-ca-ok", ""); rec.Code != http.StatusOK {
		t.Errorf("empty-ca-ok: status=%d, want 200 (empty CA binding is valid)", rec.Code)
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2], out[i*2+1] = digits[v>>4], digits[v&0x0f]
	}
	return string(out)
}

// TestJoinRejectsAfterPublicURLChange: a join link is bound to the PUBLIC_URL
// and CA state at mint time, so changing PUBLIC_URL after mint rejects the
// join with the same opaque 404 as any other unusable code.
func TestJoinRejectsAfterPublicURLChange(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://old.example.com")
	t.Setenv("BLOXOS_CA_CERT", testCAFile(t))

	// Mint with old.example.com
	got := mintJoinToken(t, e, loginAndGetToken(t, e), "client.example")
	code := got.Token

	// Verify it works with the mint-time config
	rec := getJoin(t, e, code, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET with mint-time config: status=%d", rec.Code)
	}
	mintTimeScript := rec.Body.String()
	if !strings.Contains(mintTimeScript, "HUB_HTTP='https://old.example.com'") {
		t.Fatal("mint-time script does not contain the mint-time URL")
	}

	// Change PUBLIC_URL
	t.Setenv("PUBLIC_URL", "https://new.example.com")

	// Join must reject with 404
	rec = getJoin(t, e, code, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET after PUBLIC_URL change: status=%d body=%q, want 404", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != joinUnavailableBody {
		t.Fatalf("body = %q, want the standard unavailable text", rec.Body.String())
	}
	assertJoinResponseHeaders(t, rec)

	// The token is NOT consumed by the drift rejection
	if tokenUsed(t, s, hashOf(code)) {
		t.Fatal("drift rejection consumed the token")
	}
}

// TestJoinRejectsAfterCACertChange: changing the bootstrap CA cert after mint
// rejects the join, even when PUBLIC_URL stays the same.
func TestJoinRejectsAfterCACertChange(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://hub.internal:8443")
	oldCA := testCAFile(t)
	t.Setenv("BLOXOS_CA_CERT", oldCA)

	// Mint with the first CA
	got := mintJoinToken(t, e, loginAndGetToken(t, e), "client.example")
	code := got.Token
	oldCASHA := got.CASHA256

	// Verify it works with the mint-time config
	rec := getJoin(t, e, code, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET with mint-time config: status=%d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "CA_SHA256='"+oldCASHA+"'") {
		t.Fatal("mint-time script does not contain the mint-time CA SHA")
	}

	// Replace the CA file with different content (simulating cert rotation)
	newCA := filepath.Join(t.TempDir(), "new-ca.crt")
	if err := os.WriteFile(newCA, []byte("new-test-private-ca-cert"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BLOXOS_CA_CERT", newCA)

	// Join must reject with 404
	rec = getJoin(t, e, code, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET after CA change: status=%d body=%q, want 404", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != joinUnavailableBody {
		t.Fatalf("body = %q, want the standard unavailable text", rec.Body.String())
	}
	assertJoinResponseHeaders(t, rec)

	// The token is NOT consumed by the drift rejection
	if tokenUsed(t, s, hashOf(code)) {
		t.Fatal("drift rejection consumed the token")
	}
}

// TestJoinRebuildsScriptAndStoresNoRawToken is the token-at-rest regression:
// the served join script is rebuilt byte-for-byte from the stored config
// binding plus the request token, and the raw install token appears in no
// column of the tokens row. Only its SHA-256 (token_hash) is stored.
func TestJoinRebuildsScriptAndStoresNoRawToken(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://mint.example.com")
	t.Setenv("BLOXOS_CA_CERT", testCAFile(t))

	got := mintJoinToken(t, e, loginAndGetToken(t, e), "client.example")
	code := got.Token
	wantScript := "#!/bin/bash\n" + got.AdvancedCommand + "\n"

	// The rebuilt script is byte-identical to what was minted, and carries the
	// token from the request path.
	rec := getJoin(t, e, code, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status=%d", rec.Code)
	}
	if rec.Body.String() != wantScript {
		t.Fatalf("rebuilt script does not match mint-time advanced_command:\n--- got\n%s\n--- want\n%s",
			rec.Body.String(), wantScript)
	}
	if !strings.Contains(rec.Body.String(), "TOKEN='"+code+"'") {
		t.Fatal("rebuilt script does not carry the request token")
	}

	// Every stored column of the row must be free of the raw token. token_hash
	// is its SHA-256; the binding columns are config only; mint_time_script
	// must not be written.
	var tokenHash string
	var mintScript, mintHTTP, mintCA sql.NullString
	if err := s.db.QueryRow(
		`SELECT token_hash, mint_time_script, mint_time_http_base, mint_time_ca_sha256 FROM tokens WHERE token_hash = ?`,
		hashOf(code)).Scan(&tokenHash, &mintScript, &mintHTTP, &mintCA); err != nil {
		t.Fatalf("read token row: %v", err)
	}
	for name, val := range map[string]string{
		"token_hash":          tokenHash,
		"mint_time_script":    mintScript.String,
		"mint_time_http_base": mintHTTP.String,
		"mint_time_ca_sha256": mintCA.String,
	} {
		if strings.Contains(val, code) {
			t.Fatalf("raw install token is persisted in column %s: %q", name, val)
		}
	}
	if mintScript.Valid && mintScript.String != "" {
		t.Fatalf("mint_time_script is populated (%q); the script must not be stored", mintScript.String)
	}
	if !mintHTTP.Valid || mintHTTP.String != "https://mint.example.com" {
		t.Fatalf("mint_time_http_base = %q, want https://mint.example.com", mintHTTP.String)
	}
	if !mintCA.Valid || mintCA.String != got.CASHA256 {
		t.Fatalf("mint_time_ca_sha256 = %q, want %q", mintCA.String, got.CASHA256)
	}
}

// TestJoinScriptScrubMigrationClearsPersistedToken locks in the upgrade path:
// a snapshot row that still holds a token-bearing mint_time_script (written by
// the interim build) is cleared by the migration, while its config binding and
// usability are preserved so the token keeps working.
func TestJoinScriptScrubMigrationClearsPersistedToken(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := runMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	// Simulate an interim-build row: mint_time_script embeds the raw token.
	raw := "legacy-snapshot-token-abcdef"
	h := sha256.Sum256([]byte(raw))
	tokenHash := hexOf(h[:])
	legacyScript := "#!/bin/bash\nTOKEN='" + raw + "'\n"
	if _, err := db.Exec(
		`INSERT INTO tokens (token_hash, expires_at, mint_time_script, mint_time_http_base, mint_time_ca_sha256) VALUES (?, ?, ?, ?, ?)`,
		tokenHash, time.Now().Add(time.Hour).Format("2006-01-02 15:04:05"), legacyScript, "https://legacy.example.com", "deadbeef"); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	// Re-apply only the final (scrub) migration by rewinding the version.
	if _, err := db.Exec(`UPDATE schema_version SET version = ?`, len(migrations)-1); err != nil {
		t.Fatalf("rewind schema_version: %v", err)
	}
	if err := runMigrations(db); err != nil {
		t.Fatalf("re-run migrations: %v", err)
	}

	var mintScript, mintHTTP, mintCA sql.NullString
	if err := db.QueryRow(
		`SELECT mint_time_script, mint_time_http_base, mint_time_ca_sha256 FROM tokens WHERE token_hash = ?`,
		tokenHash).Scan(&mintScript, &mintHTTP, &mintCA); err != nil {
		t.Fatalf("read scrubbed row: %v", err)
	}
	if mintScript.Valid && mintScript.String != "" {
		t.Fatalf("mint_time_script not scrubbed: %q", mintScript.String)
	}
	// The config binding survives, so the token remains usable and rebuildable.
	if mintHTTP.String != "https://legacy.example.com" || mintCA.String != "deadbeef" {
		t.Fatalf("binding columns altered by scrub: http=%q ca=%q", mintHTTP.String, mintCA.String)
	}
	s := &Server{db: db}
	usable, info := s.joinCodeUsable(raw)
	if !usable {
		t.Fatal("migration stranded an unexpired token with a complete binding")
	}
	want := linuxJoinScript("https://legacy.example.com", "wss://legacy.example.com", raw,
		"https://legacy.example.com/download/ca.crt", "deadbeef")
	if got := rebuildLinuxJoinScript(info.MintTimeHTTPBase, info.MintTimeCASHA256, raw); got != want {
		t.Fatal("migrated token no longer rebuilds the original bootstrap")
	}
}
