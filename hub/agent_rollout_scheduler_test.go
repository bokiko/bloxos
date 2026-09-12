package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// eligibleAgent wires a machine that the announce path will actually act on:
// a served binary exists, the hub knows its OS and arch, and its reported
// protocol, transport, key and release floor all pass announceDecision.
//
// This matters more than it looks. Without it the announce path returns at
// sha=="" or an unknown OS LONG before it reserves a slot or reaches the
// ingestion barrier, so a test naming either of those would pass against code
// that had neither — which is exactly what the first versions of the tests
// below did.
func eligibleAgent(t *testing.T, s *Server, machineID, osName, arch string) (*ConnectedAgent, *websocket.Conn) {
	t.Helper()
	// A TLS deployment, so transport policy does not withhold. Protocol 1
	// rather than 2 deliberately: the protocol-2 anti-rollback gate requires a
	// served binary carrying a release number, and stagePendingUpdate writes
	// an unnumbered fixture. Using 2 here would withhold on
	// agent_release_missing and every test below would be asserting against a
	// machine the hub was refusing for an unrelated reason.
	t.Setenv("PUBLIC_URL", "https://hub.example.com")

	connections := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		if conn, err := upgrader.Upgrade(w, r, nil); err == nil {
			connections <- conn
		}
	}))
	t.Cleanup(server.Close)
	client, _, err := websocket.DefaultDialer.Dial("ws"+server.URL[len("http"):], nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	hubSide := <-connections
	t.Cleanup(func() { hubSide.Close() })

	agent := &ConnectedAgent{MachineID: machineID, Conn: hubSide}
	s.agentsMu.Lock()
	s.agents[machineID] = agent
	s.agentsMu.Unlock()
	t.Cleanup(func() {
		s.agentsMu.Lock()
		delete(s.agents, machineID)
		s.agentsMu.Unlock()
	})

	agentRunningVersionsMu.Lock()
	agentRunningVersions[machineID] = agentVersionInfo{
		MachineID: machineID, OS: osName, Arch: arch, ArchReported: true,
		RunningSHA: "0000000000000000000000000000000000000000000000000000000000000000",
		ReportedAt: time.Now(), UpdateProtocol: 1,
		UpdateTransportOK: true, UpdateKeyPinned: true,
	}
	agentRunningVersionsMu.Unlock()
	t.Cleanup(func() {
		agentRunningVersionsMu.Lock()
		delete(agentRunningVersions, machineID)
		agentRunningVersionsMu.Unlock()
	})
	return agent, client
}

// stagePlatformBinary serves a distinct fixture binary for one platform, so a
// machine on it has something to be offered.
func stagePlatformBinary(t *testing.T, osName, arch string) {
	t.Helper()
	name := "bloxos-agent-" + osName + "-" + arch
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("staged "+osName+"/"+arch+" agent binary"), 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	useTestResolvedBinaryForArch(t, osName, arch, path)
	recomputeBinaryForPlatform(agentPlatform{OS: normalizeAgentOS(osName), Arch: arch})
}

// readsAFrame reports whether the agent side received anything promptly.
func readsAFrame(t *testing.T, client *websocket.Conn) bool {
	t.Helper()
	_ = client.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, err := client.ReadMessage()
	return err == nil
}

// A stuck socket must not stop the fleet.
//
// The scheduler is a single goroutine, and writeLockedTo takes an agent's write
// lock with no deadline. If admission waited on that lock, one amd64 machine
// wedged mid-write would freeze admission and expiry for arm64 and Windows
// too — every platform stalled behind one bad connection, which is precisely
// the blast radius staging exists to bound.
func TestABlockedWriterDoesNotStallOtherPlatforms(t *testing.T) {
	_, s := setupTestServer(t)
	stagePendingUpdate(t)

	// A real amd64 machine whose write lock is held and never released, as a
	// socket blocked mid-write would be.
	stuck, _ := eligibleAgent(t, s, "stuck-amd64", "linux", archAMD64)
	stuck.WriteMu.Lock()
	t.Cleanup(stuck.WriteMu.Unlock)

	// Real machines on the OTHER platforms, each with its own served binary.
	// Without these the test could pass with nothing else in the registry at
	// all, proving only that an empty loop finishes — and without their
	// binaries the announce path would withhold on sha=="" long before
	// reaching anything this test names.
	stagePlatformBinary(t, "linux", archARM64)
	stagePlatformBinary(t, "windows", archAMD64)
	_, armClient := eligibleAgent(t, s, "arm-machine", "linux", archARM64)
	_, winClient := eligibleAgent(t, s, "win-machine", "windows", archAMD64)

	done := make(chan struct{})
	go func() {
		s.runRolloutPass()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a single blocked writer froze the scheduler; no other platform could be admitted")
	}

	// The other platforms must have actually PROGRESSED, not merely not
	// crashed: each is its own platform, so each gets its own canary.
	progressed := 0
	for name, client := range map[string]*websocket.Conn{"arm64": armClient, "windows": winClient} {
		if readsAFrame(t, client) {
			progressed++
		} else {
			t.Logf("%s received no announcement", name)
		}
	}
	if progressed != 2 {
		t.Fatalf("only %d of 2 other platforms were admitted behind a blocked amd64 writer", progressed)
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
	stagePendingUpdate(t)
	const machineID = "busy-writer"
	agent, client := eligibleAgent(t, s, machineID, "linux", archAMD64)

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

	// CONTROL, writable: one pass must actually admit and SEND. Without this
	// the held-lock phase below could pass on a machine the announce path was
	// declining for some unrelated reason, long before any slot existed.
	s.runRolloutPass()
	if !readsAFrame(t, client) {
		t.Fatal("control: an eligible machine must receive an announcement")
	}
	if state, _, _, _ := read(); state != rolloutOffered {
		t.Fatalf("control: the slot should be offered, got %s", state)
	}

	// Put it back into the ambiguous never-offered state a crash leaves.
	if _, err := s.db.Exec(`UPDATE agent_rollout_slot SET state = ?, offered_unix_ms = NULL
		WHERE machine_id = ?`, rolloutReserved, machineID); err != nil {
		t.Fatalf("reset to reserved: %v", err)
	}

	// CONTROL, writable again: recovery DOES spend the resend budget when it
	// can actually send, so the assertion below is about the busy path and not
	// about recovery being inert.
	before := func() int { _, _, r, _ := read(); return r }()
	s.runRolloutPass()
	if after := func() int { _, _, r, _ := read(); return r }(); after != before+1 {
		t.Fatalf("control: a writable resend must consume the budget, %d -> %d", before, after)
	}
	_ = readsAFrame(t, client)
	if _, err := s.db.Exec(`UPDATE agent_rollout_slot SET state = ?, offered_unix_ms = NULL
		WHERE machine_id = ?`, rolloutReserved, machineID); err != nil {
		t.Fatalf("reset to reserved: %v", err)
	}

	state0, attempt0, resends0, deadline0 := read()

	// Now hold the writer across more passes than the budget allows.
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

	// And once writable again, it sends.
	s.runRolloutPass()
	if !readsAFrame(t, client) {
		t.Fatal("the machine was never sent to after its writer freed up")
	}
}

// A pass queued behind a machine deletion must not insert an orphan slot.
func TestAQueuedPassCannotCreateSlotsForADeletedMachine(t *testing.T) {
	_, s := setupTestServer(t)
	stagePendingUpdate(t)
	const machineID = "deleted-machine"
	agent, client := eligibleAgent(t, s, machineID, "linux", archAMD64)

	// CONTROL: while registered, this exact machine is admitted and sent to.
	// Without it the assertion below would hold for a machine the announce
	// path was rejecting long before the ingestion barrier.
	s.announceVersionToAgent(machineID, agent)
	if !readsAFrame(t, client) {
		t.Fatal("control: a registered eligible machine must be sent to")
	}
	var created int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_rollout_slot WHERE machine_id = ?`,
		machineID).Scan(&created); err != nil {
		t.Fatalf("count: %v", err)
	}
	if created != 1 {
		t.Fatalf("control: the registered case must create a slot, got %d", created)
	}

	// Now what a delete does: take the registry entry and the durable rows.
	// The platform row goes too, so the assertion below can tell "reserve was
	// never reached" from "reserve ran and was cleaned up afterwards".
	s.agentsMu.Lock()
	delete(s.agents, machineID)
	s.agentsMu.Unlock()
	if _, err := s.db.Exec(`DELETE FROM agent_rollout_slot`); err != nil {
		t.Fatalf("delete slots: %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM agent_rollout_platform`); err != nil {
		t.Fatalf("delete platforms: %v", err)
	}

	// A pass queued behind that delete must insert nothing.
	s.announceVersionToAgent(machineID, agent)

	var slots int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_rollout_slot WHERE machine_id = ?`,
		machineID).Scan(&slots); err != nil {
		t.Fatalf("count slots: %v", err)
	}
	if slots != 0 {
		t.Fatalf("a pass created %d slot(s) for a machine the registry does not hold", slots)
	}

	// And the reservation must never have been ATTEMPTED. Checking only that
	// no slot survives cannot distinguish the barrier from the pre-send
	// registry check cleaning up after itself — and that distinction is the
	// whole point, because cleanup only happens if the process lives long
	// enough to reach it. reserve() creates the platform row as its first act,
	// so its absence proves reserve was never called.
	var platforms int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_rollout_platform`).Scan(&platforms); err != nil {
		t.Fatalf("count platforms: %v", err)
	}
	if platforms != 0 {
		t.Fatal("the reservation ran for a deleted machine and was merely cleaned up afterwards; " +
			"a crash between the two would have left an orphan slot holding capacity")
	}
}

// A machine that took the update and then failed validation is still ON the
// candidate. Deriving "updated" from the current state alone reported
// "0 updated, 1 failed" for a machine demonstrably running the new build.
func TestAFailedValidationStillCountsAsUpdated(t *testing.T) {
	f := newRolloutFixture(t)
	if r, _, _ := f.c.reserve(testPlatform, "m1", candidateA, 8); r == nil {
		t.Fatal("reserve")
	}
	st, _ := f.c.platformState(testPlatform)
	if err := f.c.observeRunningCandidate(testPlatform, "m1", st.Generation); err != nil {
		t.Fatalf("observe: %v", err)
	}

	status, err := f.c.status(testPlatform)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Updated != 1 || status.Validated != 0 {
		t.Fatalf("a machine running the candidate must be updated but not validated: %+v", status)
	}

	// Its dwell never completes and the attempt lapses.
	f.advance(rolloutAttemptTimeout + rolloutRestartGrace + time.Minute)
	if err := f.c.tick(testPlatform); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if state, _, _ := f.slot(testPlatform, "m1"); state != rolloutFailed {
		t.Fatalf("the attempt should have failed, got %s", state)
	}

	status, err = f.c.status(testPlatform)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Failed != 1 {
		t.Fatalf("the failure must be reported: %+v", status)
	}
	if status.Updated != 1 {
		t.Fatalf("the machine is still running the candidate; reporting %d updated alongside "+
			"%d failed tells an operator nothing shipped", status.Updated, status.Failed)
	}
}

// A report that lands just after the served binary changed must not mark the
// NEW generation's slot observed on the strength of the OLD bytes.
func TestAReportForTheOldCandidateDoesNotObserveTheNewOne(t *testing.T) {
	f := newRolloutFixture(t)
	if r, _, _ := f.c.reserve(testPlatform, "m1", candidateA, 8); r == nil {
		t.Fatal("reserve A")
	}
	// The candidate changes; a new generation begins.
	if _, _, err := f.c.reserve(testPlatform, "m2", candidateB, 9); err != nil {
		t.Fatalf("reserve B: %v", err)
	}
	stB, _ := f.c.platformState(testPlatform)
	if !strings.EqualFold(stB.Candidate, candidateB) {
		t.Fatalf("the platform should be on B, got %s", stB.Candidate)
	}

	// m1 holds no slot in the new generation, so an attempt to observe it
	// there must change nothing.
	if err := f.c.observeRunningCandidate(testPlatform, "m1", stB.Generation); err != nil {
		t.Fatalf("observe: %v", err)
	}
	status, err := f.c.status(testPlatform)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Updated != 0 {
		t.Fatalf("a machine running the OLD candidate was counted as updated for the new one: %+v", status)
	}
}

// A resume with no controller must fail, not clear the pause and report success.
func TestResumeWithoutAControllerFails(t *testing.T) {
	_, s := setupTestServer(t)
	if err := s.setOperatorRolloutPause(true); err != nil {
		t.Fatalf("pause: %v", err)
	}
	controller := s.rollout
	s.rollout = nil
	t.Cleanup(func() { s.rollout = controller })

	if err := s.resumeRolloutAtomically(); err == nil {
		t.Fatal("resume must fail when no controller can retry the failed attempts")
	}
	if paused, _ := s.operatorRolloutPause(); !paused {
		t.Fatal("a resume that could retry nothing cleared the pause anyway")
	}
}

// The production path, not just the controller: an all-withheld first stage
// followed by two machines becoming eligible must admit exactly ONE.
//
// The core test covers this through tick() alone. This one runs real passes,
// because the hole it guards lived in the scheduler's completion wiring rather
// than in the controller — a first stage with nothing outstanding completed
// having proven nothing, and the next arrivals were treated as post-canary
// work and both offered the untested build.
func TestAllWithheldThenEligibleStillAdmitsOnlyACanary(t *testing.T) {
	_, s := setupTestServer(t)
	stagePendingUpdate(t)

	// Two machines the announce path will refuse: protocol 1 with no usable
	// transport is a real eligibility refusal, not a contrivance.
	var clients []*websocket.Conn
	for _, id := range []string{"w1", "w2"} {
		_, client := eligibleAgent(t, s, id, "linux", archAMD64)
		agentRunningVersionsMu.Lock()
		v := agentRunningVersions[id]
		v.UpdateTransportOK = false
		agentRunningVersions[id] = v
		agentRunningVersionsMu.Unlock()
		clients = append(clients, client)
	}
	for i := 0; i < 3; i++ {
		s.runRolloutPass()
	}
	// Assert the negative from durable state, not from the socket: a read that
	// times out puts a gorilla connection into a permanent error state, so
	// probing for silence would break the later positive check on the same
	// client and the test would fail for a reason of its own making.
	var attempts int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_rollout_slot WHERE state IN (?, ?, ?)`,
		rolloutReserved, rolloutOffered, rolloutObserved).Scan(&attempts); err != nil {
		t.Fatalf("count: %v", err)
	}
	if attempts != 0 {
		t.Fatalf("%d withheld machine(s) were admitted", attempts)
	}

	// Both become eligible at once.
	agentRunningVersionsMu.Lock()
	for _, id := range []string{"w1", "w2"} {
		v := agentRunningVersions[id]
		v.UpdateTransportOK = true
		agentRunningVersions[id] = v
	}
	agentRunningVersionsMu.Unlock()

	s.runRolloutPass()

	offered := 0
	for _, client := range clients {
		if readsAFrame(t, client) {
			offered++
		}
	}
	if offered != 1 {
		rows, _ := s.db.Query(`SELECT machine_id, state, reason FROM agent_rollout_slot`)
		defer rows.Close()
		for rows.Next() {
			var id, state, reason string
			_ = rows.Scan(&id, &state, &reason)
			t.Logf("slot %s state=%s reason=%q", id, state, reason)
		}
		t.Fatalf("%d machines were offered the update with no canary ever validated; exactly one may be", offered)
	}
}

// The same shape with nobody connected at all: an empty first stage must not
// advance, so the first machine to appear is still a lone canary.
func TestAnEmptyFirstStageThenTwoLateMachinesAdmitsOnlyACanary(t *testing.T) {
	_, s := setupTestServer(t)
	stagePendingUpdate(t)

	for i := 0; i < 5; i++ {
		s.runRolloutPass() // nothing connected
	}

	_, first := eligibleAgent(t, s, "late-1", "linux", archAMD64)
	_, second := eligibleAgent(t, s, "late-2", "linux", archAMD64)
	s.runRolloutPass()

	offered := 0
	for _, client := range []*websocket.Conn{first, second} {
		if readsAFrame(t, client) {
			offered++
		}
	}
	if offered != 1 {
		t.Fatalf("%d machines were offered after an empty first stage; the canary must still be one", offered)
	}
}

// Disconnect must clear THAT connection's evidence, through the real
// lifecycle path rather than by calling the cleanup directly — which is how
// the cleanup came to have no callers at all while its comment claimed
// disconnect handled it.
func TestDisconnectClearsThatConnectionsEvidence(t *testing.T) {
	_, s := setupTestServer(t)
	stagePendingUpdate(t)
	const machineID = "disconnecting"
	agent, _ := eligibleAgent(t, s, machineID, "linux", archAMD64)
	platform := "linux/amd64"

	// Give it real evidence.
	s.runRolloutPass()
	st, err := s.rollout.platformState(platform)
	if err != nil || st == nil {
		t.Fatalf("platform state: %v", err)
	}
	s.rollout.observeCandidateReport(platform, machineID, agent, st.Generation,
		st.Candidate, st.Candidate)
	s.rollout.observeTelemetry(platform, machineID, agent)

	s.rollout.mu.Lock()
	before := len(s.rollout.evidence[rolloutKey(platform, machineID)])
	s.rollout.mu.Unlock()
	if before == 0 {
		t.Fatal("control: the connection must have evidence before it disconnects")
	}

	// The real path a closing socket takes.
	s.unregisterAgentConnection(machineID, agent)

	s.rollout.mu.Lock()
	after := len(s.rollout.evidence[rolloutKey(platform, machineID)])
	s.rollout.mu.Unlock()
	if after != 0 {
		t.Fatalf("evidence for a closed connection survived the disconnect (%d record(s))", after)
	}
}

// A displaced socket's late evidence must never cost the winner its proof,
// and each connection's record must go away with that connection.
func TestDisplacedSocketsCannotDisplaceTheWinnersProof(t *testing.T) {
	f := newRolloutFixture(t)
	const machineID = "m1"
	if r, _, _ := f.c.reserve(testPlatform, machineID, candidateA, 8); r == nil {
		t.Fatal("reserve")
	}
	st, _ := f.c.platformState(testPlatform)

	// The winner establishes real proof FIRST.
	winner := conn(machineID)
	f.register(machineID, winner)
	f.prove(testPlatform, machineID, candidateA, winner)
	if ok, why := f.c.dwellSatisfied(testPlatform, machineID, st.Generation, candidateA); !ok {
		t.Fatalf("control: the winner must be validated first: %s", why)
	}

	// Then more displaced sockets than any backstop would have kept deliver
	// late evidence. Each writes only into its own record.
	displaced := make([]*ConnectedAgent, 0, 8)
	for i := 0; i < 8; i++ {
		f.advance(time.Second)
		other := conn(machineID)
		displaced = append(displaced, other)
		f.c.observeCandidateReport(testPlatform, machineID, other, st.Generation, candidateA, candidateA)
		f.c.observeTelemetry(testPlatform, machineID, other)
	}
	if ok, why := f.c.dwellSatisfied(testPlatform, machineID, st.Generation, candidateA); !ok {
		t.Fatalf("late evidence from displaced sockets destroyed the winner's proof: %s", why)
	}

	// Each exiting connection removes only its own record.
	for _, other := range displaced {
		f.c.forgetConnection(testPlatform, machineID, other)
	}
	if ok, why := f.c.dwellSatisfied(testPlatform, machineID, st.Generation, candidateA); !ok {
		t.Fatalf("a displaced socket's cleanup took the winner's proof: %s", why)
	}

	// And when the winner itself exits, its record goes.
	f.c.forgetConnection(testPlatform, machineID, winner)
	if ok, _ := f.c.dwellSatisfied(testPlatform, machineID, st.Generation, candidateA); ok {
		t.Fatal("evidence survived the connection that produced it")
	}
}
