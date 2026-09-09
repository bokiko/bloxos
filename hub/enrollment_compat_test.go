package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bokiko/bloxos/proto/updatesigning"
)

// The enrollment compatibility gate refuses to serve a fresh install
// (?enrollment=1) an agent binary whose enrollment-handshake support cannot be
// verified — a markerless (release 0) binary — while leaving the flagless
// self-update download completely unchanged. These tests pin that contract.

// writeMarkedAgentBinary writes a fake binary carrying release marker `release`
// and returns its path. A marked binary is, by construction, built after the
// enrollment_committed handshake shipped, so the gate must accept it.
func writeMarkedAgentBinary(t *testing.T, release uint64) string {
	t.Helper()
	marker, err := updatesigning.ReleaseMarker(release)
	if err != nil {
		t.Fatalf("build release marker: %v", err)
	}
	path := filepath.Join(t.TempDir(), "bloxos-agent")
	body := []byte("marked agent prefix\x00" + marker + "\x00marked agent suffix")
	if err := os.WriteFile(path, body, 0o755); err != nil {
		t.Fatalf("write marked binary: %v", err)
	}
	return path
}

func downloadAgent(t *testing.T, e http.Handler, query string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/download/agent"+query, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// TestEnrollmentDownloadRejectsMarkerlessBinary: a fresh install must not
// receive a markerless binary — the hub cannot verify it speaks the enrollment
// handshake — so the request is refused with an actionable 503 and the binary
// bytes are never served.
func TestEnrollmentDownloadRejectsMarkerlessBinary(t *testing.T) {
	e, _ := setupTestServer(t)
	withAgentBinaryState(t)
	// useGeneratedTestBinaryForArch writes plain bytes: no release marker.
	markerless := useGeneratedTestBinaryForArch(t, "linux", "amd64")
	recomputeAgentBinarySHA()

	code, body := downloadAgent(t, e, "?os=linux&arch=amd64&enrollment=1")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%q, want 503 for a markerless enrollment download", code, body)
	}
	// Actionable, honest body: unverifiable (not "predates"), points at the
	// staging procedure, and reassures the update path is unaffected.
	for _, want := range []string{"release marker", "cannot verify", "docs/native-agent-upgrades.md", "update normally"} {
		if !strings.Contains(body, want) {
			t.Errorf("503 body missing %q: %q", want, body)
		}
	}
	// The refusal must be a JSON error, never the binary bytes.
	if raw, _ := os.ReadFile(markerless); strings.Contains(body, string(raw)) {
		t.Fatal("markerless binary bytes were served despite the gate")
	}
}

// TestUpdateDownloadServesMarkerlessBinaryUnchanged: the same markerless binary
// on the flagless self-update path still downloads with 200 — the gate never
// touches updates, so no-marker fixtures and the migration hop are preserved.
func TestUpdateDownloadServesMarkerlessBinaryUnchanged(t *testing.T) {
	e, _ := setupTestServer(t)
	withAgentBinaryState(t)
	path := useGeneratedTestBinaryForArch(t, "linux", "amd64")
	recomputeAgentBinarySHA()

	code, body := downloadAgent(t, e, "?os=linux&arch=amd64")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%q, want 200 on the flagless update path", code, body)
	}
	want, _ := os.ReadFile(path)
	if body != string(want) {
		t.Fatal("update download did not serve the exact binary bytes")
	}
}

// TestEnrollmentDownloadAcceptsReleasedBinaryAllArch: a release-marked binary is
// accepted for enrollment on every platform the installer targets.
func TestEnrollmentDownloadAcceptsReleasedBinaryAllArch(t *testing.T) {
	e, _ := setupTestServer(t)
	withAgentBinaryState(t)
	for _, tc := range []struct{ os, arch, query string }{
		{"linux", "amd64", "?os=linux&arch=amd64&enrollment=1"},
		{"linux", "arm64", "?os=linux&arch=arm64&enrollment=1"},
		{"windows", "amd64", "?os=windows&enrollment=1"},
	} {
		path := writeMarkedAgentBinary(t, 7)
		useTestResolvedBinaryForArch(t, tc.os, tc.arch, path)
		recomputeAgentBinarySHA()

		code, body := downloadAgent(t, e, tc.query)
		if code != http.StatusOK {
			t.Fatalf("%s/%s: status=%d body=%q, want 200 for a release-marked enrollment download", tc.os, tc.arch, code, body)
		}
		want, _ := os.ReadFile(path)
		if body != string(want) {
			t.Fatalf("%s/%s: enrollment download did not serve the exact binary bytes", tc.os, tc.arch)
		}
	}
}

// TestEnrollmentDownloadMissingBinaryUnchanged: an architecture the hub has no
// binary for stays a 404 (its own honest message), with or without the flag —
// the gate is not confused with the absent-binary case.
func TestEnrollmentDownloadMissingBinaryUnchanged(t *testing.T) {
	e, _ := setupTestServer(t)
	withAgentBinaryState(t)
	// Serve only linux/amd64; arm64 has no binary.
	useTestResolvedBinaryForArch(t, "linux", "amd64", writeMarkedAgentBinary(t, 7))
	recomputeAgentBinarySHA()

	for _, q := range []string{"?os=linux&arch=arm64", "?os=linux&arch=arm64&enrollment=1"} {
		code, body := downloadAgent(t, e, q)
		if code != http.StatusNotFound {
			t.Fatalf("query %q: status=%d body=%q, want 404 for an absent binary (not the enrollment 503)", q, code, body)
		}
	}
}

// TestEnrollmentDownloadDefaultArchGated: no ?arch means amd64, and the
// enrollment flag still gates that default the installer's one-liner uses.
func TestEnrollmentDownloadDefaultArchGated(t *testing.T) {
	e, _ := setupTestServer(t)
	withAgentBinaryState(t)
	useGeneratedTestBinaryForArch(t, "linux", "amd64") // markerless default
	recomputeAgentBinarySHA()

	if code, body := downloadAgent(t, e, "?os=linux&enrollment=1"); code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%q, want 503 for the default-arch markerless enrollment download", code, body)
	}
}
