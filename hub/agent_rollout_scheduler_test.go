package main

import (
	"testing"
	"time"
)

// A stuck socket must not stop the fleet.
//
// The scheduler is a single goroutine, and writeLockedTo takes an agent's write
// lock with no deadline. If admission waited on that lock, one amd64 machine
// wedged mid-write would freeze admission and expiry for arm64 and Windows
// too — every platform stalled behind one bad connection, which is precisely
// the blast radius staging exists to bound.
func TestABlockedWriterDoesNotStallOtherPlatforms(t *testing.T) {
	_, s := setupTestServer(t)

	// An agent whose write lock is held and never released, as a socket
	// blocked in a write would be.
	stuck := &ConnectedAgent{MachineID: "stuck-amd64"}
	stuck.WriteMu.Lock()
	t.Cleanup(stuck.WriteMu.Unlock)
	s.agentsMu.Lock()
	s.agents["stuck-amd64"] = stuck
	s.agentsMu.Unlock()

	// One pass must COMPLETE despite it. A pass that blocks never returns, so
	// the bound here is the test's own timeout on the channel below.
	done := make(chan struct{})
	go func() {
		s.runRolloutPass()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a single blocked writer froze the scheduler; no other platform could be admitted or expired")
	}

	// And the blocked machine holds no reservation: skipping a busy writer
	// must not leave capacity consumed by a send that never happened.
	var slots int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_rollout_slot WHERE machine_id = ?`,
		"stuck-amd64").Scan(&slots); err != nil {
		t.Fatalf("count slots: %v", err)
	}
	if slots != 0 {
		t.Fatalf("a skipped busy writer left %d reservation(s) holding capacity", slots)
	}
}

// Resume is all-or-nothing: telling an operator "resumed" while the failed
// attempts stay terminal means the halt looks cleared and nothing moves.
func TestResumeIsAtomic(t *testing.T) {
	_, s := setupTestServer(t)

	// A platform with something to reset; otherwise resume has no work and
	// nothing to fail at, and the test would pass without testing anything.
	if _, _, err := s.rollout.reserve("linux/amd64", "m1",
		"aaaa000000000000000000000000000000000000000000000000000000000001", 8); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := s.setOperatorRolloutPause(true); err != nil {
		t.Fatalf("pause: %v", err)
	}
	// Drop the table the slot reset needs, so the transaction must fail.
	if _, err := s.db.Exec(`DROP TABLE agent_rollout_slot`); err != nil {
		t.Fatalf("drop: %v", err)
	}

	if err := s.resumeRolloutAtomically(); err == nil {
		t.Fatal("resume must fail when its slot resets cannot be applied")
	}
	// The pause must still be set: a partial resume is not a resume.
	paused, _ := s.operatorRolloutPause()
	if !paused {
		t.Fatal("a failed resume cleared the operator pause anyway; the operator would be " +
			"told updates resumed while every failed attempt stayed terminal")
	}
}

// A permanently busy socket must cost a recovered reservation NOTHING.
//
// Reserving and only then discovering the writer is busy spends the automatic
// resend budget without transmitting a byte, so a machine whose socket stays
// wedged would exhaust recovery and halt its platform having never been sent
// anything. The skip has to happen before any slot, counter or deadline is
// touched.
func TestABusyWriterNeverSpendsARecoveredReservation(t *testing.T) {
	_, s := setupTestServer(t)
	const machineID = "busy-writer"
	sha := "aaaa000000000000000000000000000000000000000000000000000000000001"

	agent := &ConnectedAgent{MachineID: machineID}
	s.agentsMu.Lock()
	s.agents[machineID] = agent
	s.agentsMu.Unlock()

	// A reservation that was never offered: the ambiguous crash state.
	if _, _, err := s.rollout.reserve("linux/amd64", machineID, sha, 8); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	var state0 string
	var attempt0, resends0 int
	var deadline0 int64
	read := func() (string, int, int, int64) {
		var state string
		var attempt, resends int
		var deadline int64
		if err := s.db.QueryRow(`SELECT state, attempt, resend_count, deadline_unix_ms
			FROM agent_rollout_slot WHERE machine_id = ?`, machineID).
			Scan(&state, &attempt, &resends, &deadline); err != nil {
			t.Fatalf("read slot: %v", err)
		}
		return state, attempt, resends, deadline
	}
	state0, attempt0, resends0, deadline0 = read()

	// Hold the writer across more passes than the automatic budget allows.
	agent.WriteMu.Lock()
	for i := 0; i < rolloutMaxResends+3; i++ {
		s.runRolloutPass()
	}
	agent.WriteMu.Unlock()

	state1, attempt1, resends1, deadline1 := read()
	if state1 != state0 || attempt1 != attempt0 || resends1 != resends0 || deadline1 != deadline0 {
		t.Fatalf("a busy writer consumed recovery state: state %s->%s attempt %d->%d "+
			"resends %d->%d deadline %d->%d",
			state0, state1, attempt0, attempt1, resends0, resends1, deadline0, deadline1)
	}

	st, err := s.rollout.platformState("linux/amd64")
	if err != nil {
		t.Fatalf("platform state: %v", err)
	}
	if st != nil && st.Status == rolloutHalted {
		t.Fatalf("a busy writer halted the platform without anything being sent: %s", st.HaltReason)
	}
}

// A pass queued behind a machine deletion must not insert an orphan slot.
func TestAQueuedPassCannotCreateSlotsForADeletedMachine(t *testing.T) {
	_, s := setupTestServer(t)
	const machineID = "deleted-machine"
	agent := &ConnectedAgent{MachineID: machineID}

	// Never registered — exactly the state after a delete has taken the
	// registry entry, which is what the ingestion barrier checks.
	s.announceVersionToAgent(machineID, agent)

	var slots int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_rollout_slot WHERE machine_id = ?`,
		machineID).Scan(&slots); err != nil {
		t.Fatalf("count: %v", err)
	}
	if slots != 0 {
		t.Fatalf("a pass created %d slot(s) for a machine the registry does not hold", slots)
	}
}
