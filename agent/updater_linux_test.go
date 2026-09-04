//go:build linux

package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestDownloadAgentBinaryRequestsOwnArch pins the self-updater's side of
// per-architecture delivery: the download must name this binary's OS and
// GOARCH, so a hub that serves several architectures hands back the build
// for this CPU rather than its default. Without it, an arm64 agent on a
// hub whose default is amd64 downloads bytes its CPU cannot execute.
func TestDownloadAgentBinaryRequestsOwnArch(t *testing.T) {
	var got url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("agent-bytes"))
	}))
	defer server.Close()

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	prev := hubURL
	t.Cleanup(func() { hubURL = prev })
	// Loopback plaintext is the one transport the gate permits.
	hubURL = "ws://" + u.Host + "/ws/agent"

	dest := filepath.Join(t.TempDir(), "bloxos-agent.new")
	if err := downloadAgentBinary(dest); err != nil {
		t.Fatalf("downloadAgentBinary: %v", err)
	}
	if got == nil {
		t.Fatal("the hub was never asked for the binary")
	}
	if got.Get("os") != runtime.GOOS || got.Get("arch") != runtime.GOARCH {
		t.Fatalf("download query = %q, want os=%s&arch=%s", got.Encode(), runtime.GOOS, runtime.GOARCH)
	}
	if body, err := os.ReadFile(dest); err != nil || string(body) != "agent-bytes" {
		t.Fatalf("downloaded body = %q (err=%v)", body, err)
	}
}
