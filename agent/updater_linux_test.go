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

// TestVerifyDownloadedELFArch checks the self-updater's defense-in-depth ELF
// gate: this build's own architecture is accepted, the other architecture and
// malformed/truncated/32-bit binaries are refused. Arch-agnostic so it holds
// on both amd64 and arm64 CI runners.
func TestVerifyDownloadedELFArch(t *testing.T) {
	write := func(b []byte) string {
		p := filepath.Join(t.TempDir(), "bin")
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	header := func(machine uint16) []byte {
		b := make([]byte, 64)
		b[0], b[1], b[2], b[3] = 0x7f, 'E', 'L', 'F'
		b[4], b[5], b[6] = 2, 1, 1
		b[18], b[19] = byte(machine), byte(machine>>8)
		return b
	}
	self, ok := elfMachineForGOARCH[runtime.GOARCH]
	if !ok {
		t.Skipf("no ELF machine mapping for %s", runtime.GOARCH)
	}
	other := uint16(0x3e)
	if runtime.GOARCH == "amd64" {
		other = 0xb7
	}
	if err := verifyDownloadedELFArch(write(header(self))); err != nil {
		t.Fatalf("own architecture rejected: %v", err)
	}
	if err := verifyDownloadedELFArch(write(header(other))); err == nil {
		t.Fatal("a different architecture's binary was accepted")
	}
	if err := verifyDownloadedELFArch(write([]byte("#!/bin/sh\necho not an elf binary\n"))); err == nil {
		t.Fatal("non-ELF content accepted")
	}
	if err := verifyDownloadedELFArch(write([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1})); err == nil {
		t.Fatal("truncated header accepted")
	}
	b32 := header(self)
	b32[4] = 1 // ELFCLASS32
	if err := verifyDownloadedELFArch(write(b32)); err == nil {
		t.Fatal("32-bit ELF accepted")
	}
}
