package main

import (
	"testing"
)

// TestHandleAISessionsConfigGatesScanning checks the wiring between the
// hub's config frame and the scanner gate. A nil connection is fine: the
// gate transition to "newly enabled" spawns sendAISessions, which we avoid
// by applying enabled=true before the handler so nothing writes.
func TestHandleAISessionsConfigGatesScanning(t *testing.T) {
	t.Setenv("BLOXOS_AI_SESSIONS", "")
	aiGate.Reset()
	if aiGate.Allowed("") {
		t.Fatal("gate must start closed")
	}
	handleAISessionsConfig(nil, nil, "m", []byte(`{"type":"ai_sessions_config","enabled":false,"rev":2}`))
	if aiGate.Allowed("") {
		t.Fatal("hub disable must close the gate")
	}
	// Reordered delivery: a stale rev 1 enable after the rev 2 disable.
	handleAISessionsConfig(nil, nil, "m", []byte(`{"type":"ai_sessions_config","enabled":true,"rev":1}`))
	if aiGate.Allowed("") {
		t.Fatal("stale lower-revision enable must be ignored")
	}
	aiGate.Apply(true, 3) // pre-open so the handler below does not spawn a report on a nil conn
	handleAISessionsConfig(nil, nil, "m", []byte(`{"type":"ai_sessions_config","enabled":true,"rev":3}`))
	if !aiGate.Allowed("") {
		t.Fatal("hub enable must open the gate")
	}
	if aiGate.Allowed("0") {
		t.Fatal("local opt-out must override hub enable")
	}
	handleAISessionsConfig(nil, nil, "m", []byte(`not json`))
	if !aiGate.Allowed("") {
		t.Fatal("malformed frame must not change the gate")
	}
	aiGate.Reset()
}
