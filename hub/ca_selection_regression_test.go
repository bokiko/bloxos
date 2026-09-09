package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestBootstrapExplicitCAFailuresNeverFallback(t *testing.T) {
	valid := writeCAFile(t, []byte(generateTestCertPEM(t)))
	bad := filepath.Join(t.TempDir(), "malformed.crt")
	if err := os.WriteFile(bad, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("https://hub.example")
	for name, path := range map[string]string{"missing": filepath.Join(t.TempDir(), "missing.crt"), "malformed": bad, "directory": t.TempDir()} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("BLOXOS_CA_CERT", path)
			s := &Server{caCertCandidates: func() []string { return []string{valid} }}
			withJoinPinResolver(t, func(context.Context, *url.URL, []byte) (string, error) {
				t.Fatal("invalid explicit CA reached pin resolver")
				return "", nil
			})
			s.systemTrustProbe = func(context.Context, *url.URL) error {
				t.Fatal("invalid explicit CA fell back to OS trust")
				return nil
			}
			if _, err := s.classifyBootstrapCA(context.Background(), u.String(), u); err == nil || !strings.Contains(err.Error(), "BLOXOS_CA_CERT") {
				t.Fatalf("expected actionable explicit-CA error, got %v", err)
			}
			r := httptest.NewRecorder()
			if err := s.handleDownloadCACert(echo.New().NewContext(httptest.NewRequest("GET", "/download/ca.crt", nil), r)); err != nil {
				t.Fatal(err)
			}
			if r.Code != http.StatusInternalServerError {
				t.Fatalf("download fell back: %d %s", r.Code, r.Body.String())
			}
		})
	}
}

func TestBootstrapSkippedCandidateDownloadMatchesBinding(t *testing.T) {
	t.Setenv("BLOXOS_CA_CERT", "")
	validPEM := []byte(generateTestCertPEM(t))
	valid := writeCAFile(t, validPEM)
	bad := filepath.Join(t.TempDir(), "stray.crt")
	if err := os.WriteFile(bad, []byte("unrelated non-certificate file"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Server{caCertCandidates: func() []string { return []string{bad, valid} }}
	withJoinPinResolver(t, func(_ context.Context, _ *url.URL, pem []byte) (string, error) {
		if string(pem) != string(validPEM) {
			t.Fatal("mint did not select the valid candidate")
		}
		return testJoinPin, nil
	})
	u, _ := url.Parse("https://hub.internal")
	decision, err := s.classifyBootstrapCA(context.Background(), u.String(), u)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRecorder()
	if err := s.handleDownloadCACert(echo.New().NewContext(httptest.NewRequest("GET", "/download/ca.crt", nil), r)); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(r.Body.Bytes())
	if r.Code != 200 || hex.EncodeToString(sum[:]) != decision.caSHA256 {
		t.Fatalf("download bytes differ from mint binding: %d", r.Code)
	}
}

func TestBootstrapCAFailureLeavesBothMintEndpointsUnchanged(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	auth := loginAndGetToken(t, e)
	t.Setenv("PUBLIC_URL", "https://hub.example")
	t.Setenv("BLOXOS_CA_CERT", filepath.Join(t.TempDir(), "missing.crt"))
	s.seedWindowsMachine(t, "win-ca-failure")
	if _, err := s.db.Exec(`INSERT INTO agent_credentials(machine_id, secret_hash) VALUES ('win-ca-failure', 'existing-secret-hash')`); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tokens`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/tokens", windowsReenrollmentPath("win-ca-failure")} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Authorization", "Bearer "+auth)
		r := httptest.NewRecorder()
		e.ServeHTTP(r, req)
		if r.Code != 503 {
			t.Fatalf("%s returned %d: %s", path, r.Code, r.Body.String())
		}
		var after int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM tokens`).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if after != before {
			t.Fatalf("%s left an orphan token", path)
		}
	}
	var unchanged int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_credentials WHERE machine_id = 'win-ca-failure' AND secret_hash = 'existing-secret-hash' AND pending_secret_hash IS NULL`).Scan(&unchanged); err != nil {
		t.Fatal(err)
	}
	if unchanged != 1 {
		t.Fatal("failed mint changed the active or pending credential")
	}
}

func TestBootstrapPinRefreshedPerMint(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	auth := loginAndGetToken(t, e)
	t.Setenv("PUBLIC_URL", "https://hub.internal")
	t.Setenv("BLOXOS_CA_CERT", "")
	ca := testCAFile(t)
	s.caCertCandidates = func() []string { return []string{ca} }
	calls := 0
	withJoinPinResolver(t, func(context.Context, *url.URL, []byte) (string, error) {
		calls++
		pin := make([]byte, 32)
		pin[0] = byte(calls) // same CA/origin, newly presented leaf key
		return base64.StdEncoding.EncodeToString(pin), nil
	})
	s.systemTrustProbe = func(context.Context, *url.URL) error {
		t.Fatal("candidate that verifies must win even if OS store also trusts it")
		return nil
	}
	first := mintJoinToken(t, e, auth, "client.example")
	second := mintJoinToken(t, e, auth, "client.example")
	if calls != 2 || first.JoinPin == second.JoinPin || first.CASHA256 != second.CASHA256 {
		t.Fatalf("pin was cached across mints or probed twice within one mint: calls=%d", calls)
	}
}
