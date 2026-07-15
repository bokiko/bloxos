//go:build !insecure

package main

import "testing"

// TestBuildAgentTLSConfigDefaultVerifies proves that a default/release build
// never disables TLS verification, even if BLOXOS_TLS_INSECURE=1 is set — the
// insecure bypass must not exist outside an explicit `-tags insecure` build.
func TestBuildAgentTLSConfigDefaultVerifies(t *testing.T) {
	t.Setenv("BLOXOS_TLS_INSECURE", "1")

	cfg, err := buildAgentTLSConfig("hub.example")
	if err != nil {
		t.Fatalf("buildAgentTLSConfig: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected a non-nil tls.Config")
	}
	if cfg.InsecureSkipVerify {
		t.Fatal("default build must not set InsecureSkipVerify even with BLOXOS_TLS_INSECURE=1")
	}
}
