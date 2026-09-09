package main

// Add Machine must not hand the operator a command that is certain to fail on
// the target machine. The download path refuses a markerless agent for a fresh
// install (it cannot prove the binary speaks the enrollment handshake), so the
// mint path applies the same condition first.
//
// The classification and the refusal decision are exercised directly: the
// resolver requires a root-owned binary, which a unit test cannot produce, so
// the state lookup is injected per call rather than through a shared package
// variable that parallel tests could race on.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func stateFor(states map[string]agentBinaryState) func(agentPlatform) agentBinaryState {
	return func(platform agentPlatform) agentBinaryState { return states[platform.String()] }
}

// A served binary with a usable release marker.
func servedRelease(release uint64) agentBinaryState {
	return agentBinaryState{Path: "/usr/local/lib/bloxos/linux/bloxos-agent",
		Source: "environment:BLOXOS_AGENT_BINARY", SHA: "abc123", Release: release}
}

func TestEnrollmentReadinessClassification(t *testing.T) {
	// The exact field condition: a binary IS served, it simply cannot prove
	// enrollment support. This is what stranded two machines.
	t.Run("markerless is blocking, not merely missing", func(t *testing.T) {
		servable, markerless := enrollmentReadinessFrom(stateFor(map[string]agentBinaryState{
			"linux/amd64": servedRelease(0),
		}))
		if len(servable) != 0 {
			t.Fatalf("markerless binary must not be servable, got %v", servable)
		}
		if len(markerless) != 1 || !strings.Contains(markerless[0], "linux/amd64") {
			t.Fatalf("want linux/amd64 reported markerless, got %v", markerless)
		}
		if !strings.Contains(markerless[0], "BLOXOS_AGENT_BINARY") {
			t.Fatalf("the report must name the source so an operator can find it, got %v", markerless)
		}
	})

	t.Run("a release-marked binary is servable", func(t *testing.T) {
		servable, markerless := enrollmentReadinessFrom(stateFor(map[string]agentBinaryState{
			"linux/amd64": servedRelease(minEnrollmentAgentRelease),
		}))
		if len(markerless) != 0 {
			t.Fatalf("release-marked binary must not be blocking, got %v", markerless)
		}
		if len(servable) != 1 || servable[0] != "linux/amd64" {
			t.Fatalf("want linux/amd64 servable, got %v", servable)
		}
	})

	// An unresolvable binary is the download path's 404 to explain, not a
	// reason to stop minting: widening the refusal would block Add Machine on
	// hubs that work today, including every hub with no arm64 payload staged.
	t.Run("an unresolvable binary is neither servable nor blocking", func(t *testing.T) {
		servable, markerless := enrollmentReadinessFrom(stateFor(map[string]agentBinaryState{
			"linux/amd64": {Error: "no trusted binary is available"},
		}))
		if len(servable) != 0 || len(markerless) != 0 {
			t.Fatalf("unresolvable must be reported neither way, got servable=%v markerless=%v",
				servable, markerless)
		}
	})

	t.Run("windows is not consulted for a Linux join command", func(t *testing.T) {
		servable, markerless := enrollmentReadinessFrom(stateFor(map[string]agentBinaryState{
			"windows/amd64": servedRelease(0),
		}))
		if len(servable) != 0 || len(markerless) != 0 {
			t.Fatalf("windows must not affect Linux readiness, got servable=%v markerless=%v",
				servable, markerless)
		}
	})
}

func TestEnrollmentRefusalDecision(t *testing.T) {
	markerless := []string{"linux/amd64 (source environment:BLOXOS_AGENT_BINARY)"}

	t.Run("refuses when nothing servable and something markerless", func(t *testing.T) {
		reason := enrollmentRefusal(nil, markerless)
		if reason == "" {
			t.Fatal("a hub serving only a markerless agent must refuse to mint")
		}
		// The message has to be actionable on its own: what is wrong, where,
		// what to do, and that the existing fleet is not affected.
		for _, want := range []string{"release marker", "linux/amd64",
			"native-agent-upgrades", "Already-enrolled agents are unaffected"} {
			if !strings.Contains(reason, want) {
				t.Fatalf("refusal must mention %q, got %q", want, reason)
			}
		}
	})

	t.Run("proceeds when any architecture is servable", func(t *testing.T) {
		// A fleet of amd64 machines is legitimately unaffected by a missing or
		// markerless arm64 payload, so one good architecture is enough.
		if reason := enrollmentRefusal([]string{"linux/amd64"}, markerless); reason != "" {
			t.Fatalf("one servable arch must allow minting, got %q", reason)
		}
	})

	t.Run("proceeds when nothing is served at all", func(t *testing.T) {
		if reason := enrollmentRefusal(nil, nil); reason != "" {
			t.Fatalf("an unresolvable binary is the download path's 404, got %q", reason)
		}
	})
}

// Non-regression: the default test environment resolves no agent binary at all,
// which must keep minting exactly as it did before this gate existed.
func TestCreateTokenStillMintsWhenNoAgentBinaryResolves(t *testing.T) {
	e, s := setupTestServer(t)
	token := loginAndGetToken(t, e)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", "https://hub.public.example")

	req := httptest.NewRequest(http.MethodPost, "/api/tokens", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 when no binary resolves, got %d: %s", rec.Code, rec.Body.String())
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tokens`).Scan(&n); err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected exactly one token minted, got %d", n)
	}
}
