package main

import (
	"testing"
	"time"
)

// TestBackoffAfterConn covers the reconnect-backoff reset rule: a connection
// that stayed up beyond stableConnThreshold is treated as healthy and resets
// the backoff to its base delay, so a later brief blip does not inherit a long
// backoff accumulated earlier in the agent's life.
func TestBackoffAfterConn(t *testing.T) {
	base := time.Second
	cases := []struct {
		name    string
		current time.Duration
		uptime  time.Duration
		want    time.Duration
	}{
		{"stable connection resets to base", 60 * time.Second, stableConnThreshold + time.Second, base},
		{"brief connection keeps current backoff", 60 * time.Second, time.Second, 60 * time.Second},
		{"exactly at threshold keeps current backoff", 30 * time.Second, stableConnThreshold, 30 * time.Second},
		{"already at base stays at base", base, time.Millisecond, base},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := backoffAfterConn(tc.current, base, tc.uptime); got != tc.want {
				t.Fatalf("backoffAfterConn(%v, %v, %v) = %v, want %v",
					tc.current, base, tc.uptime, got, tc.want)
			}
		})
	}
}
