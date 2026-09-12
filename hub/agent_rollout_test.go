package main

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// rolloutFixture is a controller over a real migrated database with an
// injected clock. Time is never real here: a dwell that depended on wall-clock
// sleep would make the suite slow and flaky, and would test the scheduler
// rather than the rule.
type rolloutFixture struct {
	c   *rolloutController
	db  *sql.DB
	now time.Time
	t   *testing.T

	// registry mirrors the hub's: which connection currently owns a machine.
	// Tests drive it explicitly, because the ownership boundary IS the
	// behaviour under test in several of them.
	registryMu sync.Mutex
	registry   map[string]*ConnectedAgent
}

func (f *rolloutFixture) register(machineID string, c *ConnectedAgent) {
	f.registryMu.Lock()
	defer f.registryMu.Unlock()
	if f.registry == nil {
		f.registry = map[string]*ConnectedAgent{}
	}
	f.registry[machineID] = c
}

// currentConn answers "who owns this machine now". A machine with no recorded
// registration defaults to whatever connection was last seen writing evidence,
// which keeps the simpler unit tests from having to model the registry.
func (f *rolloutFixture) currentConn(machineID string) *ConnectedAgent {
	f.registryMu.Lock()
	defer f.registryMu.Unlock()
	return f.registry[machineID]
}

// newRolloutDB is an in-memory database with migrations applied. MaxOpenConns
// stays at 1, matching the other suites: ":memory:" gives each connection its
// OWN database, so a pool would hand different statements different schemas.
func newRolloutDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open rollout test db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := runMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return db
}

func newRolloutFixture(t *testing.T) *rolloutFixture {
	t.Helper()
	db := newRolloutDB(t)
	f := &rolloutFixture{db: db, now: time.Unix(1_700_000_000, 0).UTC(), t: t}
	c, err := newRolloutController(db, func() time.Time { return f.now })
	if err != nil {
		t.Fatalf("controller: %v", err)
	}
	c.attachRegistry(f.currentConn)
	f.c = c
	return f
}

func (f *rolloutFixture) advance(d time.Duration) { f.now = f.now.Add(d) }

// restart rebuilds the controller over the SAME database, which is what a hub
// restart looks like: durable rows survive, in-memory evidence does not.
func (f *rolloutFixture) restart() {
	f.t.Helper()
	c, err := newRolloutController(f.db, func() time.Time { return f.now })
	if err != nil {
		f.t.Fatalf("restart: %v", err)
	}
	c.attachRegistry(f.currentConn)
	f.c = c
}

func (f *rolloutFixture) slot(platform, machineID string) (state string, attempt int, deadline int64) {
	state, attempt, _, deadline = f.slotFull(platform, machineID)
	return
}

func (f *rolloutFixture) slotFull(platform, machineID string) (state string, attempt, resends int, deadline int64) {
	f.t.Helper()
	err := f.db.QueryRow(`SELECT state, attempt, resend_count, deadline_unix_ms FROM agent_rollout_slot
		WHERE platform = ? AND machine_id = ? ORDER BY generation DESC LIMIT 1`,
		platform, machineID).Scan(&state, &attempt, &resends, &deadline)
	if err != nil {
		f.t.Fatalf("slot %s/%s: %v", platform, machineID, err)
	}
	return
}

// prove drives a machine all the way to validated on one connection.
func (f *rolloutFixture) prove(platform, machineID, candidate string, conn *ConnectedAgent) {
	f.t.Helper()
	st, err := f.c.platformState(platform)
	if err != nil || st == nil {
		f.t.Fatalf("platform state: %v", err)
	}
	f.register(machineID, conn)
	f.c.observeCandidateReport(platform, machineID, conn, st.Generation, candidate, candidate)
	// Metrics every 30s, inside the allowed gap, spanning the whole dwell.
	// The dwell is measured BETWEEN metrics frames, so the loop has to run
	// until the last frame is at least a dwell after the first.
	f.advance(30 * time.Second)
	f.c.observeTelemetry(platform, machineID, conn)
	for span := time.Duration(0); span < rolloutDwell; span += 30 * time.Second {
		f.advance(30 * time.Second)
		f.c.observeTelemetry(platform, machineID, conn)
	}
}

const testPlatform = "linux/amd64"
const candidateA = "aaaa000000000000000000000000000000000000000000000000000000000001"
const candidateB = "bbbb000000000000000000000000000000000000000000000000000000000002"

func conn(id string) *ConnectedAgent { return &ConnectedAgent{MachineID: id} }

// ---------------------------------------------------------------- reservation

func TestCanaryAdmitsExactlyOneMachine(t *testing.T) {
	f := newRolloutFixture(t)
	first, _, err := f.c.reserve(testPlatform, "m1", candidateA, 8)
	if err != nil || first == nil {
		t.Fatalf("first machine must be admitted as the canary: %v", err)
	}
	second, _, err := f.c.reserve(testPlatform, "m2", candidateA, 8)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if second != nil {
		t.Fatal("a second machine must wait: the canary stage admits exactly one")
	}
}

// The case a unique key on (machine, candidate) does NOT cover: it deduplicates
// repeat calls for one machine, and would have let two DIFFERENT machines both
// insert for the final slot.
//
// This one runs on a FILE database with the production DSN and a real
// connection pool. The in-memory fixture pins MaxOpenConns to 1, so its
// transactions serialise in the pool before SQLite is ever involved — the test
// would pass without _txlock=immediate doing anything, and would deadlock
// rather than race. The claim under test is about SQLite's write lock, so the
// test has to reach it.
func TestTwoDistinctMachinesRaceForTheFinalSlot(t *testing.T) {
	db, err := sql.Open("sqlite", databaseDSN(filepath.Join(t.TempDir(), "rollout.db")))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(4) // a real pool: the goroutines must actually overlap
	if err := runMigrations(db); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	f := &rolloutFixture{db: db, now: time.Unix(1_700_000_000, 0).UTC(), t: t}
	controller, err := newRolloutController(db, func() time.Time { return f.now })
	if err != nil {
		t.Fatalf("controller: %v", err)
	}
	controller.attachRegistry(f.currentConn)
	f.c = controller
	// Take the canary and validate it so the stage advances to a batch of two.
	canary, _, _ := f.c.reserve(testPlatform, "canary", candidateA, 8)
	if canary == nil {
		t.Fatal("canary must reserve")
	}
	c0 := conn("canary")
	f.prove(testPlatform, "canary", candidateA, c0)
	if err := f.c.tick(testPlatform); err != nil {
		t.Fatalf("tick: %v", err)
	}

	// Fill one of the two batch slots, leaving exactly one.
	if r, _, _ := f.c.reserve(testPlatform, "filler", candidateA, 8); r == nil {
		t.Fatal("the first batch slot must be available")
	}

	// Two distinct machines race for the last one.
	var wg sync.WaitGroup
	results := make([]*rolloutReservation, 2)
	for i, id := range []string{"racer-a", "racer-b"} {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			r, _, err := f.c.reserve(testPlatform, id, candidateA, 8)
			if err != nil {
				t.Errorf("reserve %s: %v", id, err)
			}
			results[i] = r
		}(i, id)
	}
	wg.Wait()

	won := 0
	for _, r := range results {
		if r != nil {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("exactly one machine may take the final slot, %d did", won)
	}
}

func TestARepeatedTriggerForOneMachineNeitherResendsNorExtends(t *testing.T) {
	f := newRolloutFixture(t)
	r, _, _ := f.c.reserve(testPlatform, "m1", candidateA, 8)
	if r == nil {
		t.Fatal("reserve")
	}
	if err := f.c.markOffered(r); err != nil {
		t.Fatalf("markOffered: %v", err)
	}
	_, _, deadlineBefore := f.slot(testPlatform, "m1")

	f.advance(time.Minute)
	again, _, err := f.c.reserve(testPlatform, "m1", candidateA, 8)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if again != nil {
		t.Fatal("an offered slot must not be re-reserved by a duplicate trigger")
	}
	state, attempt, deadlineAfter := f.slot(testPlatform, "m1")
	if state != rolloutOffered || attempt != 1 || deadlineAfter != deadlineBefore {
		t.Fatalf("duplicate trigger changed the slot: state=%s attempt=%d deadline moved=%v",
			state, attempt, deadlineAfter != deadlineBefore)
	}
}

// ------------------------------------------------------------------- recovery

// Crash after reserving, before the socket write: the agent was never offered
// anything, so a bounded resend must be allowed — within the ORIGINAL window.
func TestRecoveryResendPreservesTheAttemptWindow(t *testing.T) {
	f := newRolloutFixture(t)
	r, _, _ := f.c.reserve(testPlatform, "m1", candidateA, 8)
	if r == nil {
		t.Fatal("reserve")
	}
	_, _, deadlineBefore := f.slot(testPlatform, "m1")

	f.advance(2 * time.Minute) // crash, restart, machine reconnects
	f.restart()

	// The baseline is the deadline AFTER the restart grace has been applied.
	// Comparing against the pre-restart value left room for the bug: the old
	// code set a fresh now+10m window, which at t=2m lands at t=12m and sits
	// comfortably inside "original plus five minutes of grace". The resend
	// must not move the deadline AT ALL.
	_, _, deadlineAfterRestart := f.slot(testPlatform, "m1")
	if deadlineAfterRestart < deadlineBefore {
		t.Fatal("the restart grace shortened the attempt window")
	}

	again, _, err := f.c.reserve(testPlatform, "m1", candidateA, 8)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if again == nil || !again.Resend {
		t.Fatal("a reservation that never reached a socket must be resendable")
	}
	_, attempt, resends, deadlineAfterResend := f.slotFull(testPlatform, "m1")
	// An automatic resend spends the RESEND budget, not an operator attempt:
	// the two are separate so that exhausting recovery cannot make Resume inert.
	if resends != 1 {
		t.Fatalf("resend must consume the automatic resend budget, got %d", resends)
	}
	if attempt != 1 {
		t.Fatalf("automatic recovery must not consume an operator attempt, got %d", attempt)
	}
	if deadlineAfterResend != deadlineAfterRestart {
		t.Fatalf("the resend granted itself a new window: %d -> %d (%v added)",
			deadlineAfterRestart, deadlineAfterResend,
			time.Duration(deadlineAfterResend-deadlineAfterRestart)*time.Millisecond)
	}
}

func TestRestartGraceIsGrantedOnceNotPerRestart(t *testing.T) {
	f := newRolloutFixture(t)
	if r, _, _ := f.c.reserve(testPlatform, "m1", candidateA, 8); r == nil {
		t.Fatal("reserve")
	}
	_, _, original := f.slot(testPlatform, "m1")

	f.restart()
	_, _, afterFirst := f.slot(testPlatform, "m1")
	for i := 0; i < 5; i++ {
		f.advance(10 * time.Second)
		f.restart()
	}
	_, _, afterMany := f.slot(testPlatform, "m1")

	if afterFirst < original {
		t.Fatal("the grace shortened the attempt window")
	}
	if afterMany != afterFirst {
		t.Fatalf("a crash loop kept extending the deadline: %d -> %d", afterFirst, afterMany)
	}
}

// Exhausting the AUTOMATIC resend budget must not make the operator's Resume
// inert: bounding both with one counter cleared the halt and moved nothing.
func TestResumeWorksAfterTheAutomaticResendBudgetIsSpent(t *testing.T) {
	f := newRolloutFixture(t)
	if r, _, _ := f.c.reserve(testPlatform, "m1", candidateA, 8); r == nil {
		t.Fatal("reserve")
	}
	for i := 0; i < rolloutMaxResends+2; i++ {
		if _, _, err := f.c.reserve(testPlatform, "m1", candidateA, 8); err != nil {
			t.Fatalf("reserve: %v", err)
		}
	}
	if state, _, _ := f.slot(testPlatform, "m1"); state != rolloutFailed {
		t.Fatalf("the automatic budget should be spent, state=%s", state)
	}

	if err := f.c.resume(testPlatform); err != nil {
		t.Fatalf("resume: %v", err)
	}
	// Not merely status=active: the machine must actually become sendable.
	r, why, err := f.c.reserve(testPlatform, "m1", candidateA, 8)
	if err != nil {
		t.Fatalf("reserve after resume: %v", err)
	}
	if r == nil {
		t.Fatalf("resume left the machine unsendable (%q); the halt cleared but nothing moved", why)
	}
}

func TestExhaustedResendAttemptsHaltDurably(t *testing.T) {
	f := newRolloutFixture(t)
	if r, _, _ := f.c.reserve(testPlatform, "m1", candidateA, 8); r == nil {
		t.Fatal("reserve")
	}
	for i := 0; i < rolloutMaxResends+2; i++ {
		if _, _, err := f.c.reserve(testPlatform, "m1", candidateA, 8); err != nil {
			t.Fatalf("reserve: %v", err)
		}
	}
	// Reload from the database: a halt that lives only in the aborted
	// transaction is not a halt.
	f.restart()
	st, err := f.c.platformState(testPlatform)
	if err != nil || st == nil {
		t.Fatalf("platform state: %v", err)
	}
	if st.Status != rolloutHalted {
		t.Fatalf("exhausting attempts must halt the platform durably, status=%s", st.Status)
	}
	if st.HaltReason == "" {
		t.Fatal("a halt must carry a reason an operator can read")
	}
}

// ------------------------------------------------------------------- evidence

func TestDwellRequiresContinuousTelemetryOnOneConnection(t *testing.T) {
	f := newRolloutFixture(t)
	if r, _, _ := f.c.reserve(testPlatform, "m1", candidateA, 8); r == nil {
		t.Fatal("reserve")
	}
	st, _ := f.c.platformState(testPlatform)
	c1 := conn("m1")
	f.register("m1", c1)

	f.c.observeCandidateReport(testPlatform, "m1", c1, st.Generation, candidateA, candidateA)
	// One report, then silence past the allowed gap, then a frame at the far
	// end of the dwell. The endpoints look right; the middle proves nothing.
	f.advance(rolloutDwell + time.Second)
	f.c.observeTelemetry(testPlatform, "m1", c1)
	if ok, why := f.c.dwellSatisfied(testPlatform, "m1", st.Generation, candidateA); ok {
		t.Fatal("a gap in the middle of the dwell must restart it, not be ignored")
	} else if why == "" {
		t.Fatal("a refusal must say why")
	}

	// Continuous frames do satisfy it.
	f.prove(testPlatform, "m1", candidateA, c1)
	if ok, why := f.c.dwellSatisfied(testPlatform, "m1", st.Generation, candidateA); !ok {
		t.Fatalf("continuous telemetry across the dwell must satisfy it: %s", why)
	}
}

// A version report says which bytes are running. It says nothing about the
// machine being monitored, so repeating it must never satisfy a dwell.
func TestVersionReportsAloneNeverValidate(t *testing.T) {
	f := newRolloutFixture(t)
	if r, _, _ := f.c.reserve(testPlatform, "m1", candidateA, 8); r == nil {
		t.Fatal("reserve")
	}
	st, _ := f.c.platformState(testPlatform)
	c1 := conn("m1")
	f.register("m1", c1)

	for i := 0; i < 10; i++ {
		f.c.observeCandidateReport(testPlatform, "m1", c1, st.Generation, candidateA, candidateA)
		f.advance(20 * time.Second)
	}
	if ok, why := f.c.dwellSatisfied(testPlatform, "m1", st.Generation, candidateA); ok {
		t.Fatal("repeated version reports with no metrics satisfied the dwell")
	} else if why == "" {
		t.Fatal("a refusal must say why")
	}
}

// The dwell endpoint must be an observation, not a timer expiring.
func TestTheDwellEndpointMustBeARealMetricsFrame(t *testing.T) {
	f := newRolloutFixture(t)
	if r, _, _ := f.c.reserve(testPlatform, "m1", candidateA, 8); r == nil {
		t.Fatal("reserve")
	}
	st, _ := f.c.platformState(testPlatform)
	c1 := conn("m1")
	f.register("m1", c1)

	f.c.observeCandidateReport(testPlatform, "m1", c1, st.Generation, candidateA, candidateA)
	f.advance(30 * time.Second)
	f.c.observeTelemetry(testPlatform, "m1", c1) // dwell starts here

	// The clock passes the dwell, but no further frame has arrived.
	f.advance(rolloutDwell + time.Second)
	if ok, _ := f.c.dwellSatisfied(testPlatform, "m1", st.Generation, candidateA); ok {
		t.Fatal("the dwell completed on a timer, with no metrics at its endpoint")
	}
}

// One connection proving candidate A must not hand that dwell to candidate B.
func TestEvidenceForOneCandidateDoesNotValidateAnother(t *testing.T) {
	f := newRolloutFixture(t)
	if r, _, _ := f.c.reserve(testPlatform, "m1", candidateA, 8); r == nil {
		t.Fatal("reserve")
	}
	st, _ := f.c.platformState(testPlatform)
	c1 := conn("m1")
	f.register("m1", c1)
	f.prove(testPlatform, "m1", candidateA, c1)
	if ok, _ := f.c.dwellSatisfied(testPlatform, "m1", st.Generation, candidateA); !ok {
		t.Fatal("control: A must be satisfied")
	}

	// The candidate changes. The machine is still running A, on the same
	// socket, and nothing about B has been observed.
	if _, _, err := f.c.reserve(testPlatform, "m2", candidateB, 9); err != nil {
		t.Fatalf("reserve B: %v", err)
	}
	stB, _ := f.c.platformState(testPlatform)
	if stB.Generation == st.Generation {
		t.Fatal("a changed candidate must start a new generation")
	}
	if ok, _ := f.c.dwellSatisfied(testPlatform, "m1", stB.Generation, candidateB); ok {
		t.Fatal("A's dwell was accepted as proof for B")
	}
}

func TestRestartClearsInFlightEvidenceButKeepsValidatedProgress(t *testing.T) {
	f := newRolloutFixture(t)
	if r, _, _ := f.c.reserve(testPlatform, "m1", candidateA, 8); r == nil {
		t.Fatal("reserve")
	}
	st, _ := f.c.platformState(testPlatform)
	c1 := conn("m1")
	f.register("m1", c1)
	f.prove(testPlatform, "m1", candidateA, c1)
	if err := f.c.tick(testPlatform); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if state, _, _ := f.slot(testPlatform, "m1"); state != rolloutHealthy {
		t.Fatalf("the canary should be validated, got %s", state)
	}

	// A second machine is mid-dwell when the hub restarts.
	if r, _, _ := f.c.reserve(testPlatform, "m2", candidateA, 8); r == nil {
		t.Fatal("the batch stage must admit m2")
	}
	c2 := conn("m2")
	f.register("m2", c2)
	f.c.observeCandidateReport(testPlatform, "m2", c2, st.Generation, candidateA, candidateA)
	f.advance(30 * time.Second)
	f.c.observeTelemetry(testPlatform, "m2", c2)

	f.restart()

	// Validated progress is durable...
	if state, _, _ := f.slot(testPlatform, "m1"); state != rolloutHealthy {
		t.Fatalf("validated progress must survive a restart, got %s", state)
	}
	// ...and in-flight health evidence is not: downtime cannot accrue as dwell.
	f.advance(rolloutDwell)
	if ok, _ := f.c.dwellSatisfied(testPlatform, "m2", st.Generation, candidateA); ok {
		t.Fatal("a dwell in progress at restart was allowed to complete across the gap")
	}
}

// -------------------------------------------------------------- progression

// Before any eligible machine connects there are no slots at all. Advancing on
// "nothing outstanding" would run the platform up to batch size before its
// first machine ever arrived, and the canary would never happen.
func TestAnEmptyPlatformNeverAdvancesPastTheCanary(t *testing.T) {
	f := newRolloutFixture(t)
	if _, _, err := f.c.reserve(testPlatform, "m1", candidateA, 8); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	// Drop the slot so the platform exists with no targets at all.
	if _, err := f.db.Exec(`DELETE FROM agent_rollout_slot`); err != nil {
		t.Fatalf("clear: %v", err)
	}
	for i := 0; i < 10; i++ {
		if err := f.c.tick(testPlatform); err != nil {
			t.Fatalf("tick: %v", err)
		}
		f.advance(time.Minute)
	}
	st, _ := f.c.platformState(testPlatform)
	if st.Stage != 0 {
		t.Fatalf("an empty platform advanced to stage %d; the first machine would skip the canary", st.Stage)
	}

	// The first machine to arrive is still a lone canary.
	if r, _, _ := f.c.reserve(testPlatform, "late", candidateA, 8); r == nil {
		t.Fatal("the late machine must reserve")
	}
	if r, _, _ := f.c.reserve(testPlatform, "late2", candidateA, 8); r != nil {
		t.Fatal("the second late machine must wait: the canary is still one machine")
	}
}

// A reservation admitted after tick's preliminary look must not be stepped
// over. Comparing on the stage does not catch this: the new reservation does
// not change the stage, so a read-then-write advance still fires and leaves a
// machine holding a slot in a stage the platform has already left.
func TestAReservationArrivingDuringATickIsNotStrandedByAnAdvance(t *testing.T) {
	f := newRolloutFixture(t)
	// Validate the canary so the platform is eligible to advance.
	if r, _, _ := f.c.reserve(testPlatform, "canary", candidateA, 8); r == nil {
		t.Fatal("canary")
	}
	f.prove(testPlatform, "canary", candidateA, conn("canary"))
	if err := f.c.tick(testPlatform); err != nil {
		t.Fatalf("tick: %v", err)
	}
	before, _ := f.c.platformState(testPlatform)

	// A machine is admitted into the CURRENT stage, exactly as one would be
	// between a count and an update.
	if r, _, _ := f.c.reserve(testPlatform, "arriving", candidateA, 8); r == nil {
		t.Fatal("the new stage must admit a machine")
	}
	during, _ := f.c.platformState(testPlatform)
	if during.Stage != before.Stage {
		t.Fatalf("a reservation must not itself move the stage: %d -> %d", before.Stage, during.Stage)
	}

	if err := f.c.tick(testPlatform); err != nil {
		t.Fatalf("tick: %v", err)
	}
	after, _ := f.c.platformState(testPlatform)
	if after.Stage != during.Stage {
		t.Fatalf("the stage advanced past outstanding work: %d -> %d", during.Stage, after.Stage)
	}
}

// A stage in which every candidate was withheld has nothing outstanding and
// has proven nothing. Advancing on "nothing in flight" alone would walk the
// platform up through the batch sizes without ever testing the build.
func TestAStageOfWithheldMachinesDoesNotAdvance(t *testing.T) {
	f := newRolloutFixture(t)
	if err := f.c.withhold(testPlatform, "m1", candidateA, 8, "no pinned update key"); err != nil {
		t.Fatalf("withhold: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := f.c.tick(testPlatform); err != nil {
			t.Fatalf("tick: %v", err)
		}
		f.advance(time.Minute)
	}
	st, _ := f.c.platformState(testPlatform)
	if st.Stage != 0 {
		t.Fatalf("a stage that proved nothing advanced to %d", st.Stage)
	}
}

// The interleaving that the old design could not survive: a report from a
// socket that was STILL the owner when its ownership was checked, and had lost
// the machine by the time it wrote.
//
// The previous code checked ownership outside the evidence lock and then
// replaced the machine's single record under it, so that report destroyed the
// winner's proof — and the check could not prevent it, because the check had
// already passed. A test that simply sends a late frame after the registry
// changed does not reach this: the early check rejects it. This one holds the
// old writer between its check and its write.
func TestAReportInFlightAcrossAReplacementCannotDiscardFreshProof(t *testing.T) {
	f := newRolloutFixture(t)
	if r, _, _ := f.c.reserve(testPlatform, "m1", candidateA, 8); r == nil {
		t.Fatal("reserve")
	}
	st, _ := f.c.platformState(testPlatform)

	old := conn("m1")
	fresh := conn("m1")
	f.register("m1", old)

	// The old connection begins reporting; hold it before it writes.
	released := make(chan struct{})
	wrote := make(chan struct{})
	go func() {
		<-released
		// Both ingestion paths, because both are writers.
		f.c.observeCandidateReport(testPlatform, "m1", old, st.Generation, candidateA, candidateA)
		f.c.observeTelemetry(testPlatform, "m1", old)
		close(wrote)
	}()

	// While it is held, the machine is taken over and the new connection
	// completes a full dwell.
	f.register("m1", fresh)
	f.prove(testPlatform, "m1", candidateA, fresh)
	if ok, why := f.c.dwellSatisfied(testPlatform, "m1", st.Generation, candidateA); !ok {
		t.Fatalf("control: the current connection must be validated first: %s", why)
	}

	close(released)
	<-wrote

	if ok, why := f.c.dwellSatisfied(testPlatform, "m1", st.Generation, candidateA); !ok {
		t.Fatalf("a report in flight across the replacement destroyed valid proof: %s", why)
	}
}

// The displaced socket's own record must also never be READ as the machine's
// health once the registry has moved on.
func TestADisplacedSocketsProofIsNeverRead(t *testing.T) {
	f := newRolloutFixture(t)
	if r, _, _ := f.c.reserve(testPlatform, "m1", candidateA, 8); r == nil {
		t.Fatal("reserve")
	}
	st, _ := f.c.platformState(testPlatform)

	old := conn("m1")
	f.register("m1", old)
	f.prove(testPlatform, "m1", candidateA, old)
	if ok, _ := f.c.dwellSatisfied(testPlatform, "m1", st.Generation, candidateA); !ok {
		t.Fatal("control: the first connection must be validated")
	}

	// The machine is taken over by a socket that has proven nothing.
	fresh := conn("m1")
	f.register("m1", fresh)
	if ok, why := f.c.dwellSatisfied(testPlatform, "m1", st.Generation, candidateA); ok {
		t.Fatal("the displaced socket's completed dwell was read as the machine's health")
	} else if why == "" {
		t.Fatal("a refusal must say why")
	}
}

func TestProgressionNeedsNoOperatorAction(t *testing.T) {
	f := newRolloutFixture(t)
	machines := []string{"m1", "m2", "m3", "m4", "m5"}
	validated := map[string]bool{}

	for round := 0; round < 12 && len(validated) < len(machines); round++ {
		for _, id := range machines {
			if validated[id] {
				continue
			}
			if r, _, err := f.c.reserve(testPlatform, id, candidateA, 8); err != nil {
				t.Fatalf("reserve: %v", err)
			} else if r != nil {
				_ = f.c.markOffered(r)
			}
		}
		for _, id := range machines {
			if validated[id] {
				continue
			}
			var state string
			err := f.db.QueryRow(`SELECT state FROM agent_rollout_slot WHERE machine_id = ?`, id).Scan(&state)
			if err == sql.ErrNoRows {
				continue
			} else if err != nil {
				t.Fatalf("state: %v", err)
			}
			if state == rolloutOffered {
				f.prove(testPlatform, id, candidateA, conn(id))
			}
		}
		if err := f.c.tick(testPlatform); err != nil {
			t.Fatalf("tick: %v", err)
		}
		for _, id := range machines {
			var state string
			if err := f.db.QueryRow(`SELECT state FROM agent_rollout_slot WHERE machine_id = ?`, id).
				Scan(&state); err == nil && state == rolloutHealthy {
				validated[id] = true
			}
		}
	}

	if len(validated) != len(machines) {
		t.Fatalf("only %d of %d machines progressed without operator action: %v",
			len(validated), len(machines), validated)
	}
	st, _ := f.c.platformState(testPlatform)
	if st.Status == rolloutHalted {
		t.Fatalf("a healthy rollout halted: %s", st.HaltReason)
	}
	if st.Stage < 2 {
		t.Fatalf("five machines at one canary plus batches of two should reach stage >= 2, got %d", st.Stage)
	}
}

// "Complete" means caught up now, not closed forever.
func TestALateMachineReopensACompletedPlatform(t *testing.T) {
	f := newRolloutFixture(t)
	r, _, _ := f.c.reserve(testPlatform, "m1", candidateA, 8)
	if r == nil {
		t.Fatal("reserve")
	}
	f.prove(testPlatform, "m1", candidateA, conn("m1"))
	if err := f.c.tick(testPlatform); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if err := f.c.markComplete(testPlatform); err != nil {
		t.Fatalf("markComplete: %v", err)
	}

	late, _, err := f.c.reserve(testPlatform, "late", candidateA, 8)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if late == nil {
		t.Fatal("a machine that was offline for the whole rollout must still be offered the update")
	}
	if late.Stage == 0 {
		t.Fatal("a late arrival must not be run through the canary again")
	}
	st, _ := f.c.platformState(testPlatform)
	if st.Status != rolloutActive {
		t.Fatalf("the platform should have reopened, status=%s", st.Status)
	}
	// The machine that already passed is not re-validated.
	if again, _, _ := f.c.reserve(testPlatform, "m1", candidateA, 8); again != nil {
		t.Fatal("a validated machine must not be reserved again")
	}
}

// --------------------------------------------------------- withheld / resume

func TestAWithheldMachineDoesNotHaltAndResumesAutomatically(t *testing.T) {
	f := newRolloutFixture(t)
	if err := f.c.withhold(testPlatform, "m1", candidateA, 8, "no pinned update key"); err != nil {
		t.Fatalf("withhold: %v", err)
	}
	st, _ := f.c.platformState(testPlatform)
	if st.Status != rolloutActive {
		t.Fatalf("an ineligible machine is not a failure; status=%s", st.Status)
	}
	status, err := f.c.status(testPlatform)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.Withheld != 1 || status.Failed != 0 {
		t.Fatalf("expected one withheld and no failures, got %+v", status)
	}
	if status.WithheldReason["m1"] == "" {
		t.Fatal("a withheld machine must carry its reason")
	}

	// Eligibility is fixed: the machine must be picked up with no operator step.
	r, _, err := f.c.reserve(testPlatform, "m1", candidateA, 8)
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if r == nil {
		t.Fatal("a machine whose eligibility recovered must be reserved without operator action")
	}
}

func TestResumeRetriesFailedAttempts(t *testing.T) {
	f := newRolloutFixture(t)
	if r, _, _ := f.c.reserve(testPlatform, "m1", candidateA, 8); r == nil {
		t.Fatal("reserve")
	}
	f.advance(rolloutAttemptTimeout + rolloutRestartGrace + time.Minute)
	if err := f.c.tick(testPlatform); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if state, _, _ := f.slot(testPlatform, "m1"); state != rolloutFailed {
		t.Fatalf("an expired attempt must fail, got %s", state)
	}
	st, _ := f.c.platformState(testPlatform)
	if st.Status != rolloutHalted {
		t.Fatal("a failed attempt must halt the platform")
	}

	if err := f.c.resume(testPlatform); err != nil {
		t.Fatalf("resume: %v", err)
	}
	state, attempt, _ := f.slot(testPlatform, "m1")
	if state != rolloutReserved {
		t.Fatalf("resume must give the failed slot a new attempt, state=%s", state)
	}
	if attempt < 2 {
		t.Fatalf("resume must increment the attempt, got %d", attempt)
	}
	st, _ = f.c.platformState(testPlatform)
	if st.Status != rolloutActive {
		t.Fatalf("resume must clear the halt, status=%s", st.Status)
	}
}

func TestStatusCountsRunningCandidatesNotOnlyValidatedOnes(t *testing.T) {
	f := newRolloutFixture(t)
	r, _, _ := f.c.reserve(testPlatform, "m1", candidateA, 8)
	if r == nil {
		t.Fatal("reserve")
	}
	if _, err := f.db.Exec(`UPDATE agent_rollout_slot SET state = ? WHERE machine_id = ?`,
		rolloutObserved, "m1"); err != nil {
		t.Fatalf("observe: %v", err)
	}
	status, err := f.c.status(testPlatform)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	// The machine IS running the candidate; only its dwell is unfinished.
	// Reporting it as not updated reads as a stalled rollout.
	if status.Updated != 1 {
		t.Fatalf("a machine running the candidate must count as updated, got %d", status.Updated)
	}
	if status.Validated != 0 {
		t.Fatalf("it is not validated yet, got %d", status.Validated)
	}
}

func TestPlatformsProgressIndependently(t *testing.T) {
	f := newRolloutFixture(t)
	other := "linux/arm64"
	if r, _, _ := f.c.reserve(testPlatform, "amd", candidateA, 8); r == nil {
		t.Fatal("amd64 canary")
	}
	if r, _, _ := f.c.reserve(other, "arm", candidateB, 8); r == nil {
		t.Fatal("arm64 canary")
	}

	// Fail amd64 outright.
	f.advance(rolloutAttemptTimeout + rolloutRestartGrace + time.Minute)
	if err := f.c.tick(testPlatform); err != nil {
		t.Fatalf("tick: %v", err)
	}
	amd, _ := f.c.platformState(testPlatform)
	if amd.Status != rolloutHalted {
		t.Fatal("amd64 should have halted")
	}
	arm, _ := f.c.platformState(other)
	if arm.Status != rolloutActive {
		t.Fatalf("one platform halting must not halt another, arm64 status=%s", arm.Status)
	}
}
