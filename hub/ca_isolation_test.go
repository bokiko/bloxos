package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// failingSystemTrustProbe is the default system-trust verifier setupTestServer
// installs, so a test never silently dials the network during the
// auto-discovered-CA fallback. Tests that exercise the fallback replace it.
func failingSystemTrustProbe(context.Context, *url.URL) error {
	return errors.New("system-trust probe not configured for this test")
}

// isolatedCACandidates is the bootstrap-CA search path setupTestServer installs
// on every test server. It honors an explicitly-set BLOXOS_CA_CERT — the many
// private-CA tests depend on that env var to make a hub private — but drops the
// ambient host paths (~/.local/share/caddy, /var/lib/caddy, /root) that the
// production bootstrapCACertCandidates also reads. Those ambient paths are what
// leaked a developer's or CI runner's real Caddy root into a public test hub's
// classification, mis-classifying it private-CA and failing the public-hub
// tests depending on where they ran. An *auto-discovered* ambient CA is now
// exercised deliberately via injectAmbientCACandidate, never by accident.
func isolatedCACandidates() []string {
	if env := os.Getenv("BLOXOS_CA_CERT"); env != "" {
		return []string{env}
	}
	return nil
}

// injectAmbientCACandidate makes s behave as if the host had an auto-discovered
// Caddy root on disk (e.g. ~/.local/share/caddy/.../root.crt) — the exact
// production condition that used to mis-classify a public hub. It writes a
// real, parseable CA certificate and points s.caCertCandidates at it *without*
// setting BLOXOS_CA_CERT, so the candidate is ambient (auto-discovered), not
// operator-configured, and classification actually parses it. Returns the path.
func injectAmbientCACandidate(t *testing.T, s *Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ambient-caddy-root.crt")
	if err := os.WriteFile(path, []byte(generateTestCertPEM(t)), 0o600); err != nil {
		t.Fatalf("write ambient CA: %v", err)
	}
	s.caCertCandidates = func() []string { return []string{path} }
	return path
}

// TestAmbientCACandidateDoesNotMakeAPublicHubPrivate asserts the FIX. A
// genuinely public HTTPS hub with an unrelated, auto-discovered Caddy root on
// disk (the reported production condition) must still be classified public: the
// leaf does not chain to that stray CA, but it verifies under the hub's OS
// trust store, so the mint carries no CA material and no pin. This is the
// shared root cause of the three reported failures
// (TestPublicTrustedHTTPSAndHTTPCommandsDoNotFetchLocalCA, the "publicly
// trusted https" join case, and TestJoinRejectsMissingBinding's empty-CA drift)
// — all now host-independent.
//
// The request Host ("evil.example") never influences the decision; only the
// host CA state and the endpoint's real chain do.
func TestAmbientCACandidateDoesNotMakeAPublicHubPrivate(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://public.example")

	// Baseline: isolated, no candidate → public.
	base := mintJoinToken(t, e, loginAndGetToken(t, e), "evil.example")
	if base.CASHA256 != "" || base.JoinPin != "" || base.CAURL != "" {
		t.Fatalf("isolated public hub already carries CA material: %+v", base)
	}

	// A real but unrelated ambient Caddy root leaks into discovery. The live
	// leaf does not chain to it (resolver reports a chain mismatch)...
	injectAmbientCACandidate(t, s)
	withJoinPinResolver(t, func(context.Context, *url.URL, []byte) (string, error) {
		return "", fmt.Errorf("leaf issued by a different authority: %w", errJoinCAChainMismatch)
	})
	// ...but the hub's OS trust store verifies it, so the hub is public.
	s.systemTrustProbe = func(context.Context, *url.URL) error { return nil }

	got := mintJoinToken(t, e, loginAndGetToken(t, e), "evil.example")
	if got.CASHA256 != "" || got.JoinPin != "" || got.CAURL != "" {
		t.Fatalf("ambient stray CA wrongly made a public hub private: %+v", got)
	}
	if strings.Contains(got.Command, "-k") || strings.Contains(got.Command, "--pinnedpubkey") {
		t.Fatalf("public command gained private-CA flags: %q", got.Command)
	}
}

// TestAmbientCACandidateNeitherVerifiesRefuses: when the live leaf verifies
// against neither the auto-discovered CA nor the hub's OS trust store, the hub
// refuses to mint (503) rather than emit an unverifiable command — and no token
// is created.
func TestAmbientCACandidateNeitherVerifiesRefuses(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://public.example")
	injectAmbientCACandidate(t, s)
	withJoinPinResolver(t, func(context.Context, *url.URL, []byte) (string, error) {
		return "", fmt.Errorf("chain mismatch: %w", errJoinCAChainMismatch)
	})
	// systemTrustProbe stays the failing default installed by setupTestServer.

	before := countTokens(t, s)
	req := httptest.NewRequest(http.MethodPost, "/api/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+loginAndGetToken(t, e))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%q, want 503", rec.Code, rec.Body.String())
	}
	if after := countTokens(t, s); after != before {
		t.Fatalf("a token was minted on refusal: before=%d after=%d", before, after)
	}
}

// TestAmbientCACandidateNetworkFailureRefuses: a genuine chain mismatch tries
// system trust, but an *unreachable* endpoint (the resolver reports a network
// failure, not a mismatch) must be refused outright — never a silent downgrade
// to system trust or a public command.
func TestAmbientCACandidateNetworkFailureRefuses(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://public.example")
	injectAmbientCACandidate(t, s)
	withJoinPinResolver(t, func(context.Context, *url.URL, []byte) (string, error) {
		return "", errors.New("dial tcp 10.0.0.1:443: connect: connection refused")
	})
	// If the network failure were wrongly treated as a mismatch, this passing
	// probe would misclassify the hub public. It must never be consulted.
	s.systemTrustProbe = func(context.Context, *url.URL) error {
		t.Fatal("system trust must not be consulted on a network failure")
		return nil
	}

	req := httptest.NewRequest(http.MethodPost, "/api/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+loginAndGetToken(t, e))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%q, want 503 on network failure", rec.Code, rec.Body.String())
	}
}

// countTokens returns how many token rows exist, for no-orphan assertions.
func countTokens(t *testing.T, s *Server) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tokens`).Scan(&n); err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	return n
}

// TestPublicHubClassificationIsHostIndependent is the isolation proof: with the
// default setupTestServer seam a public HTTPS hub is classified public — empty
// CA, no pin, plain verified curl, join code served — regardless of the CA
// roots on the machine running the suite. This is what makes the three reported
// public-hub tests deterministic. The pin resolver must never run: reaching it
// would mean the hub decided it was behind a private CA.
func TestPublicHubClassificationIsHostIndependent(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://public.example")
	withJoinPinResolver(t, func(context.Context, *url.URL, []byte) (string, error) {
		t.Fatal("pin resolver ran for a public hub — classification leaked a CA")
		return "", nil
	})

	got := mintJoinToken(t, e, loginAndGetToken(t, e), "evil.example")
	if got.CASHA256 != "" || got.CAURL != "" || got.JoinPin != "" {
		t.Fatalf("public hub carries CA material: %+v", got)
	}
	if strings.Contains(got.Command, " -k") || strings.Contains(got.Command, "--pinnedpubkey") {
		t.Fatalf("public command has private-CA flags: %q", got.Command)
	}
	if strings.Contains(got.AdvancedCommand, "/download/ca.crt") {
		t.Fatalf("public advanced command fetches local CA: %q", got.AdvancedCommand)
	}
	// The public (empty-CA) binding is a valid binding: the code serves.
	if rec := getJoin(t, e, got.Token, ""); rec.Code != http.StatusOK {
		t.Fatalf("empty-CA join: status=%d body=%q, want 200", rec.Code, rec.Body.String())
	}
}

// TestJoinBindingStatesUnderIsolation locks the binding contract the isolation
// must preserve: a real public mint stores an empty-string CA binding that is a
// valid, served binding (200), while a never-recorded (NULL) binding is refused
// with the same opaque 404 as any other unusable code.
func TestJoinBindingStatesUnderIsolation(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://public.example")

	// Real public mint → empty-string CA binding → usable (200).
	got := mintJoinToken(t, e, loginAndGetToken(t, e), "evil.example")
	var caBinding sql.NullString
	if err := s.db.QueryRow(`SELECT mint_time_ca_sha256 FROM tokens WHERE token_hash = ?`, hashOf(got.Token)).Scan(&caBinding); err != nil {
		t.Fatalf("read binding: %v", err)
	}
	if !caBinding.Valid || caBinding.String != "" {
		t.Fatalf("public mint CA binding = %+v, want a valid empty string", caBinding)
	}
	if rec := getJoin(t, e, got.Token, ""); rec.Code != http.StatusOK {
		t.Fatalf("empty-CA code: status=%d, want 200", rec.Code)
	}

	// A never-bound (NULL CA) row is refused, even though it is unexpired.
	h := sha256.Sum256([]byte("never-bound-null-ca"))
	if _, err := s.db.Exec(
		`INSERT INTO tokens (token_hash, expires_at, mint_time_http_base, mint_time_ca_sha256) VALUES (?, ?, ?, ?)`,
		hexOf(h[:]), time.Now().Add(time.Hour).Format("2006-01-02 15:04:05"), "https://public.example", nil); err != nil {
		t.Fatalf("seed NULL-CA row: %v", err)
	}
	if rec := getJoin(t, e, "never-bound-null-ca", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("NULL-CA code: status=%d, want 404", rec.Code)
	}
}

// TestClassifyBootstrapCALiveTLS exercises classifyBootstrapCA end-to-end
// against real TLS listeners with the REAL pin resolver (not the stub), so the
// network-failure-vs-chain-mismatch distinction is proven against actual
// crypto/tls errors rather than injected sentinels.
func TestClassifyBootstrapCALiveTLS(t *testing.T) {
	// A real hub endpoint: a self-signed leaf for 127.0.0.1 and the CA to
	// trust it with. PUBLIC_URL points straight at it, so the default dial
	// target reaches it and SNI/verification use 127.0.0.1.
	srv, caPEM := selfSignedServer(t, 24*time.Hour)
	leaf := srv.Certificate()
	httpBase := srv.URL // https://127.0.0.1:<port>
	publicURL, err := url.Parse(httpBase)
	if err != nil {
		t.Fatal(err)
	}

	newServerRealResolver := func(t *testing.T) *Server {
		_, s := setupTestServer(t)
		// Use the real TLS-dialling resolver for these cases.
		withJoinPinResolver(t, pinPresentedLeafSPKI)
		return s
	}

	t.Run("auto candidate that the leaf chains to is private", func(t *testing.T) {
		s := newServerRealResolver(t)
		s.caCertCandidates = func() []string { return []string{writeCAFile(t, caPEM)} }
		dec, err := s.classifyBootstrapCA(context.Background(), httpBase, publicURL)
		if err != nil {
			t.Fatalf("classify: %v", err)
		}
		if dec.caSHA256 == "" || dec.joinPin != spkiPinOf(leaf) {
			t.Fatalf("expected private with the leaf pinned, got %+v", dec)
		}
	})

	t.Run("auto candidate mismatch but OS trust verifies is public", func(t *testing.T) {
		s := newServerRealResolver(t)
		_, otherCA := selfSignedServer(t, 24*time.Hour) // unrelated CA
		s.caCertCandidates = func() []string { return []string{writeCAFile(t, otherCA)} }
		s.systemTrustProbe = func(context.Context, *url.URL) error { return nil }
		dec, err := s.classifyBootstrapCA(context.Background(), httpBase, publicURL)
		if err != nil {
			t.Fatalf("classify: %v", err)
		}
		if dec != (bootstrapCADecision{}) {
			t.Fatalf("expected public (empty decision), got %+v", dec)
		}
	})

	t.Run("auto candidate mismatch and OS trust also fails refuses", func(t *testing.T) {
		s := newServerRealResolver(t)
		_, otherCA := selfSignedServer(t, 24*time.Hour)
		s.caCertCandidates = func() []string { return []string{writeCAFile(t, otherCA)} }
		s.systemTrustProbe = func(context.Context, *url.URL) error {
			return errors.New("not in the OS trust store")
		}
		if _, err := s.classifyBootstrapCA(context.Background(), httpBase, publicURL); err == nil {
			t.Fatal("expected refusal when neither the CA nor OS trust verifies")
		}
	})

	t.Run("network failure refuses without consulting OS trust", func(t *testing.T) {
		s := newServerRealResolver(t)
		s.caCertCandidates = func() []string { return []string{writeCAFile(t, caPEM)} }
		s.systemTrustProbe = func(context.Context, *url.URL) error {
			t.Fatal("OS trust must not be consulted on a network failure")
			return nil
		}
		// A closed port: the handshake never completes, so this is a network
		// failure, not a chain mismatch.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		closedAddr := ln.Addr().String()
		ln.Close()
		deadURL, _ := url.Parse("https://" + closedAddr)
		if _, err := s.classifyBootstrapCA(context.Background(), "https://"+closedAddr, deadURL); err == nil {
			t.Fatal("expected refusal on an unreachable endpoint")
		}
	})

	t.Run("explicit CA that the leaf does not chain to fails closed", func(t *testing.T) {
		s := newServerRealResolver(t)
		_, otherCA := selfSignedServer(t, 24*time.Hour)
		t.Setenv("BLOXOS_CA_CERT", writeCAFile(t, otherCA))
		s.systemTrustProbe = func(context.Context, *url.URL) error {
			t.Fatal("an explicit CA is authoritative; OS trust must not be consulted")
			return nil
		}
		if _, err := s.classifyBootstrapCA(context.Background(), httpBase, publicURL); err == nil {
			t.Fatal("expected fail-closed for an explicit CA the leaf does not chain to")
		}
	})
}

// writeCAFile writes pem to a temp file and returns its path.
func writeCAFile(t *testing.T, pem []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(path, pem, 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}
	return path
}

// TestPrivateJoinGETDoesNotRePin is root's GET regression guard: a private join
// link, once minted, must keep serving without re-running the pin resolver at
// GET time. curl already authenticated the mint-time leaf via the pinned
// command before the GET reaches the hub, so re-pinning here would wrongly
// 404 a valid link when the leaf is near expiry or the hub endpoint has a
// transient outage. GET recomputes the CA binding from the selected CA file
// only, never a TLS probe.
func TestPrivateJoinGETDoesNotRePin(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://hub.internal:8443")
	t.Setenv("BLOXOS_CA_CERT", testCAFile(t))

	// Mint a private link with a working resolver.
	got := mintJoinToken(t, e, loginAndGetToken(t, e), "client.example")
	if got.CASHA256 == "" || got.JoinPin == "" {
		t.Fatalf("expected a private mint, got %+v", got)
	}

	// After mint, the resolver both fails AND would fail t.Fatal if called at
	// GET time. The link must still serve.
	withJoinPinResolver(t, func(context.Context, *url.URL, []byte) (string, error) {
		t.Fatal("GET must not invoke the pin resolver for an already-minted private link")
		return "", errJoinCAChainMismatch
	})
	// Any TLS probe at GET is likewise forbidden for a private binding.
	s.systemTrustProbe = func(context.Context, *url.URL) error {
		t.Fatal("GET must not probe system trust for a private binding")
		return nil
	}

	rec := getJoin(t, e, got.Token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("private GET status=%d body=%q, want 200 (no re-pin)", rec.Code, rec.Body.String())
	}
	if want := "#!/bin/bash\n" + got.AdvancedCommand + "\n"; rec.Body.String() != want {
		t.Fatalf("served script is not the minted advanced command")
	}
}
