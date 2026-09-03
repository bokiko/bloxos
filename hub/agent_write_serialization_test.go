package main

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeWSWriter stands in for a live *websocket.Conn so the serialization
// property of writeLockedTo can be proven deterministically, without a real
// network connection and without relying on timing against the actual
// gorilla/websocket internals. It tracks the peak number of calls that were
// simultaneously inside WriteMessage and records every payload it received.
type fakeWSWriter struct {
	active    int32
	maxActive int32

	mu       sync.Mutex
	received [][]byte
}

func (f *fakeWSWriter) WriteMessage(messageType int, data []byte) error {
	cur := atomic.AddInt32(&f.active, 1)
	for {
		old := atomic.LoadInt32(&f.maxActive)
		if cur <= old {
			break
		}
		if atomic.CompareAndSwapInt32(&f.maxActive, old, cur) {
			break
		}
	}
	// Simulate a write slow enough that two unserialized callers would
	// almost certainly overlap if the mutex weren't actually serializing
	// them — a mutex bug that only shows up under real network latency
	// would otherwise be invisible in a fast in-memory test.
	time.Sleep(2 * time.Millisecond)

	cp := append([]byte(nil), data...)
	f.mu.Lock()
	f.received = append(f.received, cp)
	f.mu.Unlock()

	atomic.AddInt32(&f.active, -1)
	return nil
}

// TestConnectedAgentWritesAreSerializedAgainstConcurrentWriters proves
// writeLockedTo allows at most one concurrent call into the underlying
// writer, and that every message arrives intact and undamaged — the
// property the missing lock on the enrollment_confirmed sends violated.
// Fifty goroutines hammer the same mutex/writer pair concurrently; the fake
// writer's artificial delay means any missing serialization would show up
// as maxActive > 1 essentially every run, not as a rare flake.
func TestConnectedAgentWritesAreSerializedAgainstConcurrentWriters(t *testing.T) {
	var mu sync.Mutex
	fw := &fakeWSWriter{}

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload, _ := json.Marshal(map[string]string{"type": fmt.Sprintf("msg-%d", i)})
			if err := writeLockedTo(&mu, fw, websocket.TextMessage, payload); err != nil {
				t.Errorf("write %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if got := atomic.LoadInt32(&fw.maxActive); got != 1 {
		t.Fatalf("max concurrent writers = %d, want exactly 1", got)
	}
	if len(fw.received) != n {
		t.Fatalf("received %d messages, want %d", len(fw.received), n)
	}
	seen := map[string]bool{}
	for _, msg := range fw.received {
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(msg, &envelope); err != nil {
			t.Fatalf("corrupted/torn message: %s", msg)
		}
		if seen[envelope.Type] {
			t.Fatalf("duplicate message observed (implies a write was replayed or torn): %s", envelope.Type)
		}
		seen[envelope.Type] = true
	}
}

// TestConnectedAgentConfirmationRacesAnnouncePattern mirrors the exact
// real-world shape: one goroutine playing the role of
// announceVersionToAgent (which already correctly locks WriteMu) and
// another concurrently sending enrollment_confirmed through
// ConnectedAgent.writeLocked — the two writers that motivated this fix.
// Both must complete, both messages must arrive intact, and at most one may
// be inside the writer at a time.
func TestConnectedAgentConfirmationRacesAnnouncePattern(t *testing.T) {
	agent := &ConnectedAgent{}
	fw := &fakeWSWriter{}
	// ConnectedAgent.Conn is a concrete *websocket.Conn, so writeLocked
	// itself can't be pointed at the fake writer; exercise the same
	// underlying primitive (writeLockedTo against agent.WriteMu) directly,
	// which is exactly what writeLocked delegates to.

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		announceMsg, _ := json.Marshal(map[string]string{"type": "agent_version"})
		if err := writeLockedTo(&agent.WriteMu, fw, websocket.TextMessage, announceMsg); err != nil {
			t.Errorf("announce write: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		confirmMsg, _ := json.Marshal(map[string]string{"type": "enrollment_confirmed"})
		if err := writeLockedTo(&agent.WriteMu, fw, websocket.TextMessage, confirmMsg); err != nil {
			t.Errorf("confirm write: %v", err)
		}
	}()
	wg.Wait()

	if got := atomic.LoadInt32(&fw.maxActive); got != 1 {
		t.Fatalf("max concurrent writers = %d, want exactly 1 (announce and confirm overlapped)", got)
	}
	if len(fw.received) != 2 {
		t.Fatalf("received %d messages, want 2", len(fw.received))
	}
	gotTypes := map[string]bool{}
	for _, msg := range fw.received {
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(msg, &envelope); err != nil {
			t.Fatalf("corrupted message: %s", msg)
		}
		gotTypes[envelope.Type] = true
	}
	if !gotTypes["agent_version"] || !gotTypes["enrollment_confirmed"] {
		t.Fatalf("expected both agent_version and enrollment_confirmed, got %v", gotTypes)
	}
}
