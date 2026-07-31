package main

import (
	"strings"
	"testing"
)

// TestAgentDownloadURLRefusesPlaintext is the regression test for the
// self-update trust hole: the download scheme used to be "wss -> https,
// anything else -> plain http", so an http-served hub (which is what
// hub/agentws.go hands every agent when PUBLIC_URL is http://) meant the
// binary AND the SHA authorising it both arrived unauthenticated, on a
// public unauthenticated route, into a service running as root.
func TestAgentDownloadURLRefusesPlaintext(t *testing.T) {
	rejected := []string{
		"ws://hub.example.com:4000/ws/agent",
		"ws://192.168.16.234:4000/ws/agent",
		"http://hub.example.com/ws/agent",
		"WS://hub.example.com:4000/ws/agent",
		// Not loopback, despite the name.
		"ws://localhost.attacker.example:4000/ws/agent",
		// 127.0.0.1 in the userinfo, real host elsewhere.
		"ws://127.0.0.1@hub.example.com:4000/ws/agent",
	}
	for _, raw := range rejected {
		got, err := agentDownloadURL(raw, "")
		if err == nil {
			t.Errorf("agentDownloadURL(%q) = %q, want refusal", raw, got)
			continue
		}
		if !strings.Contains(err.Error(), "refusing to self-update") {
			t.Errorf("agentDownloadURL(%q) error = %v, want a refusal message", raw, err)
		}
	}
}

func TestAgentDownloadURLAcceptsTLS(t *testing.T) {
	cases := map[string]string{
		"wss://hub.example.com:4000/ws/agent":   "https://hub.example.com:4000/download/agent",
		"wss://hub.example.com/ws/agent":        "https://hub.example.com/download/agent",
		"https://hub.example.com/ws/agent":      "https://hub.example.com/download/agent",
		"WSS://Hub.Example.com:4000/ws/agent":   "https://Hub.Example.com:4000/download/agent",
		"  wss://hub.example.com:4000/ws/agent": "https://hub.example.com:4000/download/agent",
	}
	for raw, want := range cases {
		got, err := agentDownloadURL(raw, "")
		if err != nil {
			t.Errorf("agentDownloadURL(%q) unexpected error: %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("agentDownloadURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

// Loopback is exempt from the plaintext refusal: the hub is on this machine,
// so there is no network path for anyone to sit on. The shipped dev default
// is ws://localhost:4000, and single-box installs are a real deployment.
func TestAgentDownloadURLAllowsLoopbackPlaintext(t *testing.T) {
	cases := map[string]string{
		"ws://localhost:4000/ws/agent": "http://localhost:4000/download/agent",
		"ws://127.0.0.1:4000/ws/agent": "http://127.0.0.1:4000/download/agent",
		"ws://127.0.0.53/ws/agent":     "http://127.0.0.53/download/agent",
		"ws://[::1]:4000/ws/agent":     "http://[::1]:4000/download/agent",
		"http://localhost/ws/agent":    "http://localhost/download/agent",
	}
	for raw, want := range cases {
		got, err := agentDownloadURL(raw, "")
		if err != nil {
			t.Errorf("agentDownloadURL(%q) unexpected error: %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("agentDownloadURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestAgentDownloadURLAppendsOSQuery(t *testing.T) {
	got, err := agentDownloadURL("wss://hub.example.com:4000/ws/agent", "windows")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://hub.example.com:4000/download/agent?os=windows"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestAgentDownloadURLRejectsMalformed(t *testing.T) {
	for _, raw := range []string{
		"",
		"hub.example.com:4000",        // no scheme -> parsed as scheme "hub.example.com"
		"wss://",                      // no host
		"file:///etc/passwd",          // unsupported scheme
		"ftp://hub.example.com/x",     // unsupported scheme
		"://hub.example.com/ws/agent", // unparseable
	} {
		if got, err := agentDownloadURL(raw, ""); err == nil {
			t.Errorf("agentDownloadURL(%q) = %q, want error", raw, got)
		}
	}
}

func TestIsLoopbackHost(t *testing.T) {
	loopback := []string{"localhost", "LocalHost:4000", "127.0.0.1", "127.0.0.1:4000",
		"127.1.2.3", "[::1]", "[::1]:4000"}
	for _, h := range loopback {
		if !isLoopbackHost(h) {
			t.Errorf("isLoopbackHost(%q) = false, want true", h)
		}
	}
	notLoopback := []string{"hub.example.com", "hub.example.com:4000", "192.168.16.234",
		"192.168.16.234:4000", "0.0.0.0", "[2001:db8::1]:4000", "localhost.attacker.example"}
	for _, h := range notLoopback {
		if isLoopbackHost(h) {
			t.Errorf("isLoopbackHost(%q) = true, want false", h)
		}
	}
}
