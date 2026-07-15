//go:build insecure

package main

import "testing"

// TestBuildAgentTLSConfigInsecureBypass runs only under `-tags insecure`. It
// proves the dev build honors BLOXOS_TLS_INSECURE=1 (skip verification) and
// still verifies when the env is unset.
func TestBuildAgentTLSConfigInsecureBypass(t *testing.T) {
	t.Setenv("BLOXOS_TLS_INSECURE", "1")
	cfg, err := buildAgentTLSConfig("hub.example")
	if err != nil {
		t.Fatalf("buildAgentTLSConfig: %v", err)
	}
	if !cfg.InsecureSkipVerify {
		t.Fatal("insecure build with BLOXOS_TLS_INSECURE=1 must set InsecureSkipVerify")
	}

	t.Setenv("BLOXOS_TLS_INSECURE", "")
	cfg2, err := buildAgentTLSConfig("hub.example")
	if err != nil {
		t.Fatalf("buildAgentTLSConfig (no env): %v", err)
	}
	if cfg2.InsecureSkipVerify {
		t.Fatal("insecure build without BLOXOS_TLS_INSECURE must still verify")
	}
}
