package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
// production condition that mis-classifies a public hub. It writes a temp cert
// and points s.caCertCandidates at it *without* setting BLOXOS_CA_CERT, so the
// candidate is ambient (auto-discovered), not operator-configured. The resolver
// is stubbed in setupTestServer, so the cert bytes need not be a real chain.
// Returns the candidate path.
func injectAmbientCACandidate(t *testing.T, s *Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ambient-caddy-root.crt")
	if err := os.WriteFile(path, []byte("ambient-host-caddy-root"), 0o600); err != nil {
		t.Fatalf("write ambient CA: %v", err)
	}
	s.caCertCandidates = func() []string { return []string{path} }
	return path
}

// TestAmbientCACandidateMisclassifiesPublicHub reproduces the reported failure
// deterministically. A genuinely public HTTPS hub (HTTPS PUBLIC_URL, no
// operator BLOXOS_CA_CERT) is classified public with the default isolation; the
// only change that flips it to a private-CA mint is an ambient, auto-discovered
// Caddy root leaking in from the host. This is the shared root cause of the
// three reported failures:
//   - TestPublicTrustedHTTPSAndHTTPCommandsDoNotFetchLocalCA (public mint gains
//     CA material), which set BLOXOS_CA_CERT to a missing path but could not
//     defeat the ambient host paths;
//   - the "publicly trusted https is plain verified TLS" join case (the pin
//     resolver runs and the command gains -k --pinnedpubkey); and
//   - TestJoinRejectsMissingBinding, where the drift check finds the ambient
//     CA so its empty-string ("empty-ca-ok") binding no longer matches and the
//     code 404s instead of serving.
// The seam also keeps other public-hub tests deterministic
// (e.g. TestJoinRebuildByteEquivalenceAcrossTransports).
//
// The request Host ("evil.example") is identical for both mints; only the host
// CA state differs. That pins the cause to ambient CA discovery, not the Host.
func TestAmbientCACandidateMisclassifiesPublicHub(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://public.example")

	// Baseline: isolated (as every test is by default) → public classification.
	base := mintJoinToken(t, e, loginAndGetToken(t, e), "evil.example")
	if base.CASHA256 != "" || base.JoinPin != "" || base.CAURL != "" {
		t.Fatalf("isolated public hub already carries CA material: %+v", base)
	}

	// Simulate the host's auto-discovered Caddy root leaking into discovery.
	injectAmbientCACandidate(t, s)
	leaked := mintJoinToken(t, e, loginAndGetToken(t, e), "evil.example")
	if leaked.CASHA256 == "" {
		t.Fatal("ambient CA candidate did not change classification — seam not wired")
	}
	if !strings.Contains(leaked.Command, "--pinnedpubkey sha256//") {
		t.Fatalf("private classification did not pin the leaf: %q", leaked.Command)
	}
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
