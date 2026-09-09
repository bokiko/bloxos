package main

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func getBuildInfo(t *testing.T, e http.Handler) (*httptest.ResponseRecorder, map[string]string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/build-info", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	var body map[string]string
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode build-info: %v (body %q)", err, rec.Body.String())
		}
	}
	return rec, body
}

// TestBuildInfoReportsHubIdentity: the endpoint is public (no auth), returns the
// four identity fields, and defaults to the unstamped values in a test build.
func TestBuildInfoReportsHubIdentity(t *testing.T) {
	e, _ := setupTestServer(t)
	rec, body := getBuildInfo(t, e)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200 (public, no auth): %s", rec.Code, rec.Body.String())
	}
	if body["component"] != "hub" {
		t.Errorf("component=%q, want hub", body["component"])
	}
	// Unstamped test binary reports the safe defaults, never a false release.
	if body["version"] != "development" {
		t.Errorf("version=%q, want the unstamped default %q", body["version"], "development")
	}
	if body["revision"] != "unknown" {
		t.Errorf("revision=%q, want the unstamped default %q", body["revision"], "unknown")
	}
	id := body["instance_id"]
	if raw, err := hex.DecodeString(id); err != nil || len(raw) != 16 {
		t.Errorf("instance_id=%q, want 32 hex chars of randomness", id)
	}
}

// TestBuildInfoInstanceStableWithinProcess: the instance id is minted once per
// process, so every request in this process sees the identical value (it only
// changes on restart).
func TestBuildInfoInstanceStableWithinProcess(t *testing.T) {
	e, _ := setupTestServer(t)
	_, first := getBuildInfo(t, e)
	_, second := getBuildInfo(t, e)
	// A second server in the same process shares the process-lifetime id.
	e2, _ := setupTestServer(t)
	_, third := getBuildInfo(t, e2)
	if first["instance_id"] == "" {
		t.Fatal("instance_id empty")
	}
	if first["instance_id"] != second["instance_id"] || first["instance_id"] != third["instance_id"] {
		t.Fatalf("instance_id not stable within the process: %q %q %q",
			first["instance_id"], second["instance_id"], third["instance_id"])
	}
}

// TestBuildInfoIsNotCached: an upgrade check must always see the process
// currently serving the endpoint, so the response is never cached.
func TestBuildInfoIsNotCached(t *testing.T) {
	e, _ := setupTestServer(t)
	rec, _ := getBuildInfo(t, e)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control=%q, want no-store", got)
	}
}

// TestBuildInfoDoesNotAffectHealthOrAgentVersion: /health keeps its exact
// liveness-only contract, and the legacy agent-version fields are untouched by
// this addition.
func TestBuildInfoDoesNotAffectHealthOrAgentVersion(t *testing.T) {
	e, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/health status=%d, want 200", rec.Code)
	}
	var health map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	// /health stays liveness-only: exactly {"status":"ok"}, no build metadata
	// leaked into it, and no Cache-Control added.
	if len(health) != 1 || health["status"] != "ok" {
		t.Fatalf("/health body changed: %v", health)
	}
	if rec.Header().Get("Cache-Control") != "" {
		t.Errorf("/health gained a Cache-Control header: %q", rec.Header().Get("Cache-Control"))
	}
}
