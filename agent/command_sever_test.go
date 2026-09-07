package main

import "testing"

// TestCommandSeversOwnConnection is the truth table for which commands are
// expected to tear the agent down. Only these, together with a signal death,
// suppress the failure reply (see handleCommand). Restarting a *different*
// service, or any container command, must not qualify — a signal death there
// is a real failure the operator needs to see.
func TestCommandSeversOwnConnection(t *testing.T) {
	cases := []struct {
		cmdType string
		target  string
		want    bool
	}{
		{"reboot", "", true},
		{"shutdown", "", true},
		{"restart_service", "bloxos-agent", true},
		{"restart_service", "bloxos-agent.service", true},
		{"stop_service", "bloxos-agent", true},
		{"stop_service", "bloxos-agent.service", true},
		{"restart_service", "nginx", false},
		{"stop_service", "docker", false},
		{"start_service", "bloxos-agent", false},
		{"restart_container", "bloxos-agent", false},
		{"refresh_metrics", "", false},
	}
	for _, c := range cases {
		if got := commandSeversOwnConnection(c.cmdType, c.target); got != c.want {
			t.Errorf("commandSeversOwnConnection(%q, %q) = %v, want %v", c.cmdType, c.target, got, c.want)
		}
	}
}
