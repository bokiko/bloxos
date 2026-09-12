package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

/* ============================================================================
 * Staged agent rollout
 *
 * One canary per platform, then fixed batches of two, advancing on their own
 * with no operator action. A platform that fails an attempt halts and waits
 * for a person.
 *
 * Two rules shape everything here.
 *
 * HEALTH IS NOT A MATCHING SHA. A machine reporting the candidate proves the
 * bytes arrived, not that they work: an agent can report its version and die
 * seconds later, and the report survives its socket. So a slot becomes healthy
 * only after the candidate is reported ON THE CURRENT CONNECTION and that same
 * connection then stays up, delivering hub-received telemetry, for the dwell.
 * Any disconnect, stale telemetry or hub restart resets the dwell to nothing.
 *
 * DURABILITY IS FOR DECISIONS, NOT FOR EVIDENCE. Reservations, stage and
 * completed outcomes are persisted, because losing them re-sends updates and
 * loses progress. Dwell evidence is never persisted, because it belongs to a
 * live connection: writing it down would let a restart resurrect proof about a
 * socket that no longer exists. The controller therefore starts with no
 * evidence at all, which is the correct post-restart state by construction
 * rather than by remembering to clear it.
 * ============================================================================ */

const (
	// rolloutDwell is how long one connection must stay up, delivering
	// telemetry, after reporting the candidate.
	rolloutDwell = 60 * time.Second
	// rolloutTelemetryGap bounds the silence allowed BETWEEN frames during a
	// dwell, and must be shorter than the dwell. At 90s against a 60s dwell a
	// single report followed by silence would satisfy health at 60s having
	// proven nothing after the first instant. Agents report about every 30s,
	// so 45s tolerates one missed frame and no more.
	rolloutTelemetryGap = 45 * time.Second
	// Capacity: one canary, then fixed batches of two. Deliberately flat —
	// an exponential ladder reaches the whole fleet in a handful of stages,
	// which is the blast radius this exists to bound.
	rolloutCanaryCapacity = 1
	rolloutBatchCapacity  = 2
	// rolloutAttemptTimeout bounds one attempt: reserve, offer, and prove
	// health. Generous, because an agent restarts and reconnects inside it.
	rolloutAttemptTimeout = 10 * time.Minute
	// rolloutMaxResends bounds AUTOMATIC crash-recovery sends within one
	// attempt. Recovery is at-least-once and must not become unbounded replay.
	//
	// It deliberately does not bound operator retries. Using one counter for
	// both meant that once automatic recovery had spent it, Resume cleared the
	// halt and then found every slot permanently unretryable — the halt looked
	// resolved and nothing moved.
	rolloutMaxResends = 3
	// rolloutRestartGrace is added once to every live deadline when the
	// controller is built, so hub downtime is not charged to an attempt.
	rolloutRestartGrace = 5 * time.Minute
	// rolloutTickInterval is how often the single scheduler evaluates dwell
	// and advancement. Short relative to the dwell, so a validated machine
	// does not wait long to be promoted.
	rolloutTickInterval = 10 * time.Second
)

// Slot states.
const (
	// rolloutReserved holds capacity; nothing has been written to a socket.
	rolloutReserved = "reserved"
	// rolloutOffered means the announcement write returned successfully.
	rolloutOffered = "offered"
	// rolloutObserved means the machine is running the candidate, established
	// from a hub-received report rather than from an announcement we made.
	rolloutObserved = "observed"
	// rolloutHealthy is terminal success and persists as historical progress.
	rolloutHealthy = "healthy"
	// rolloutFailed is terminal until an operator resume creates a new attempt.
	rolloutFailed = "failed"
	// rolloutWithheld is an eligibility refusal — no signature, no pinned key,
	// an unusable transport, a floor mismatch. NOT a failure: it does not halt
	// the platform, does not consume capacity, and is re-evaluated every tick.
	rolloutWithheld = "withheld"
)

// Platform statuses.
// Platform statuses. Only two are persisted, deliberately.
//
// There is no durable "complete". Whether the fleet is caught up is a
// statement about machines that happen to be connected right now, so writing
// it down created a state that had to be UNWOUND when a late machine appeared
// — and that reopen transition was a hole: a first stage in which every
// machine was withheld had nothing outstanding, completed having proven
// nothing, and then reopened at batch size, putting two machines on an
// untested build with no canary ever having passed.
//
// The stage is the only authority on canary-versus-batch. A late machine
// enters whatever stage the platform is actually in: stage 0 until a canary
// validates, the batch stage afterwards. Nothing can bypass that, because
// there is no transition to bypass it with. "Caught up" is derived for display
// and never stored.
const (
	rolloutActive = "active"
	rolloutHalted = "halted"
)

// rolloutEvidence is dwell evidence for one machine. Memory only.
type rolloutEvidence struct {
	// conn identifies the connection this evidence belongs to. The pointer IS
	// the identity: a replaced socket is a different pointer, so evidence
	// cannot survive a reconnect. agentRunningVersions is keyed by machine and
	// does survive one, which is exactly why it cannot establish health alone.
	conn *ConnectedAgent
	// generation and candidate name what this evidence is evidence FOR.
	// Without them, one connection that proved itself on candidate A would
	// hand that same dwell to candidate B's slot the instant B was reserved —
	// the machine is still running A, and nothing would have checked.
	generation int64
	candidate  string
	// sawCandidate is whether that exact SHA was reported on THIS connection.
	// A report from a previous socket does not carry over.
	sawCandidate bool
	// dwellStart is when continuous METRICS coverage began, by hub receipt
	// time. Zero until the first metrics frame after the candidate report.
	dwellStart time.Time
	// lastMetrics is the hub receipt time of the last METRICS frame.
	//
	// Deliberately separate from version reports. A version report says which
	// bytes are running; it says nothing about whether the machine is being
	// monitored, and an agent that re-reports its version on a timer while
	// reporting nothing else would otherwise satisfy a dwell without a single
	// metrics frame. Hub receipt time throughout: an agent's own timestamp is
	// its claim about itself, and a stalled one keeps asserting freshness.
	lastMetrics time.Time
}

type rolloutController struct {
	db  *sql.DB
	now func() time.Time

	mu sync.Mutex
	// evidence is keyed platform + "\x00" + machineID, then by the CONNECTION
	// that produced it. Per-connection keys are what make writes safe without
	// an ownership check: a socket can only write into its own record, so no
	// interleaving lets one clobber another's. Empty at construction.
	evidence map[string]map[*ConnectedAgent]*rolloutEvidence
	// sending claims a (platform, generation, machine, attempt) for one
	// goroutine, so concurrent recovery triggers cannot both resend.
	sending map[string]bool

	// current returns the connection the registry currently holds for a
	// machine. It is called ONLY from dwellSatisfied, and never while c.mu is
	// held, so the evidence lock and the registry lock have exactly one
	// ordering and cannot deadlock against each other.
	current func(machineID string) *ConnectedAgent
	// currentMu guards `current` alone, so reading it does not require c.mu
	// and cannot be part of any ordering with the registry.
	currentMu sync.RWMutex
}

// attachRegistry points the controller at the live agent registry.
func (c *rolloutController) attachRegistry(current func(string) *ConnectedAgent) {
	c.currentMu.Lock()
	defer c.currentMu.Unlock()
	c.current = current
}

// currentConnection asks the registry who owns a machine right now.
func (c *rolloutController) currentConnection(machineID string) *ConnectedAgent {
	c.currentMu.RLock()
	lookup := c.current
	c.currentMu.RUnlock()
	if lookup == nil {
		return nil
	}
	return lookup(machineID)
}

func rolloutKey(platform, machineID string) string {
	return platform + "\x00" + machineID
}

func rolloutCapacity(stage int) int {
	if stage <= 0 {
		return rolloutCanaryCapacity
	}
	return rolloutBatchCapacity
}

// newRolloutController builds the controller and reconciles what a restart
// invalidated.
//
// It starts with no dwell evidence, which is the whole point: every in-flight
// health claim dies with the process that observed it. Durable reservations
// survive, so nothing is needlessly re-sent, and live deadlines get one grace
// extension so hub downtime is not charged against an attempt.
func newRolloutController(db *sql.DB, now func() time.Time) (*rolloutController, error) {
	if now == nil {
		now = time.Now
	}
	c := &rolloutController{db: db, now: now,
		evidence: map[string]map[*ConnectedAgent]*rolloutEvidence{}}
	if err := c.reconcileAfterRestart(); err != nil {
		return nil, err
	}
	return c, nil
}

// reconcileAfterRestart grants each live attempt ONE deadline extension.
//
// Bounded per attempt, durably. Extending on every construction would hand a
// crash loop unlimited time: a hub restarting every thirty seconds would push
// deadlines forward forever and an attempt that can never succeed would never
// be declared failed either. grace_used is cleared only when a new attempt
// begins, so a subsequent restart cannot re-grant it.
func (c *rolloutController) reconcileAfterRestart() error {
	// max(existing, now+grace): a blanket assignment would SHORTEN a
	// ten-minute attempt with nine minutes left to a five-minute one, so a
	// restart could fail an attempt sooner than no restart at all.
	grace := c.now().Add(rolloutRestartGrace).UnixMilli()
	_, err := c.db.Exec(`UPDATE agent_rollout_slot
		SET deadline_unix_ms = MAX(deadline_unix_ms, ?), grace_used = 1
		WHERE state IN (?, ?, ?) AND grace_used = 0`,
		grace, rolloutReserved, rolloutOffered, rolloutObserved)
	return err
}

// rolloutPlatformState is one platform's durable row.
type rolloutPlatformState struct {
	Platform   string
	Generation int64
	Candidate  string
	Release    uint64
	Stage      int
	Status     string
	HaltReason string
}

// loadPlatform reads a platform's row inside tx, creating or re-generating it
// for the current candidate.
//
// A changed candidate starts a NEW GENERATION rather than editing the row in
// place. Keying slots by SHA alone is not enough: rolling out B and then back
// to A would find A's old rows and treat its earlier stages as already done.
// A generation only ever moves forward, so no earlier stage can be revived.
func (c *rolloutController) loadPlatform(tx *sql.Tx, platform, candidate string, release uint64) (rolloutPlatformState, error) {
	var st rolloutPlatformState
	st.Platform = platform
	row := tx.QueryRow(`SELECT generation, candidate_sha, candidate_release, stage, status, halt_reason
		FROM agent_rollout_platform WHERE platform = ?`, platform)
	err := row.Scan(&st.Generation, &st.Candidate, &st.Release, &st.Stage, &st.Status, &st.HaltReason)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		st = rolloutPlatformState{Platform: platform, Generation: 1, Candidate: candidate,
			Release: release, Stage: 0, Status: rolloutActive}
		if _, err := tx.Exec(`INSERT INTO agent_rollout_platform
			(platform, generation, candidate_sha, candidate_release, stage, status, halt_reason, updated_unix_ms)
			VALUES (?, ?, ?, ?, 0, ?, '', ?)`,
			platform, st.Generation, candidate, release, rolloutActive, c.now().UnixMilli()); err != nil {
			return st, err
		}
		return st, nil
	case err != nil:
		return st, err
	}

	if st.Candidate != candidate {
		st.Generation++
		st.Candidate = candidate
		st.Release = release
		st.Stage = 0
		st.Status = rolloutActive
		st.HaltReason = ""
		if _, err := tx.Exec(`UPDATE agent_rollout_platform
			SET generation = ?, candidate_sha = ?, candidate_release = ?, stage = 0,
			    status = ?, halt_reason = '', updated_unix_ms = ?
			WHERE platform = ?`,
			st.Generation, candidate, release, rolloutActive, c.now().UnixMilli(), platform); err != nil {
			return st, err
		}
	}
	return st, nil
}

// rolloutReservation is what a caller needs to carry a send through.
type rolloutReservation struct {
	Platform   string
	Generation int64
	MachineID  string
	Stage      int
	Attempt    int
	// Resend is true when this reservation recovers an attempt whose socket
	// write may or may not have happened. The caller must treat delivery as
	// at-least-once.
	Resend bool
}

// rolloutUnconfirmedReason is what an expired RESERVED slot is told to say.
//
// Not "never accepted the update offer". Reserved is the explicitly AMBIGUOUS
// state: the socket write may have landed and the process died before the
// record of it, so the hub does not know whether the machine ever saw the
// offer. Blaming the machine for refusing something it may have received —
// and may be running — is a claim the hub cannot support, and it is the first
// thing an operator reads when a platform halts.
const rolloutUnconfirmedReason = "update was not confirmed within the attempt window"

// reserve takes one slot for a machine, or reports why it did not.
//
// The capacity check, the stage read and the insert are ONE transaction, and
// the DSN sets _txlock=immediate so it holds SQLite's write lock from BEGIN.
// That is what bounds concurrency between DIFFERENT machines: a unique key on
// (machine, candidate) only ever deduplicates repeat calls for the SAME
// machine, and two distinct machines racing for the final slot would both have
// inserted. Here one transaction observes the other's row and backs off.
func (c *rolloutController) reserve(platform, machineID, candidate string, release uint64) (*rolloutReservation, string, error) {
	if candidate == "" {
		return nil, "no candidate binary", nil
	}
	tx, err := c.db.Begin()
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = tx.Rollback() }()

	st, err := c.loadPlatform(tx, platform, candidate, release)
	if err != nil {
		return nil, "", err
	}
	// Commit before every early return. loadPlatform may have inserted the
	// platform row or bumped its generation, and the deferred Rollback would
	// silently undo that — leaving a candidate change to be re-detected and
	// re-bumped forever without a reservation ever being made. The same shape
	// made the exhausted-attempt branch below roll its own halt back.
	stop := func(reason string) (*rolloutReservation, string, error) {
		if err := tx.Commit(); err != nil {
			return nil, "", err
		}
		return nil, reason, nil
	}

	if st.Status == rolloutHalted {
		return stop("rollout halted: " + st.HaltReason)
	}
	now := c.now()

	// An existing slot for this machine in this generation decides the answer.
	var state string
	var attempt, resends int
	var offered sql.NullInt64
	var deadline int64
	err = tx.QueryRow(`SELECT state, attempt, resend_count, offered_unix_ms, deadline_unix_ms
		FROM agent_rollout_slot WHERE platform = ? AND generation = ? AND machine_id = ?`,
		platform, st.Generation, machineID).Scan(&state, &attempt, &resends, &offered, &deadline)
	switch {
	case err == nil:
		switch state {
		case rolloutHealthy:
			return stop("") // already done; a duplicate trigger changes nothing
		case rolloutFailed:
			return stop("attempt failed; operator resume required")
		case rolloutOffered, rolloutObserved:
			// Already announced and awaiting proof. A duplicate trigger must
			// not resend or extend the deadline.
			return stop("")
		case rolloutWithheld:
			// Eligibility refusals are re-evaluated by the caller each tick;
			// clearing the row lets a now-eligible machine be reserved.
			if _, err := tx.Exec(`DELETE FROM agent_rollout_slot
				WHERE platform = ? AND generation = ? AND machine_id = ?`,
				platform, st.Generation, machineID); err != nil {
				return nil, "", err
			}
		case rolloutReserved:
			// Reserved but never marked offered. Either the process died
			// before the write, or after it and before the mark — the durable
			// state is identical and the hub cannot tell them apart. Recovery
			// is a bounded resend under the SAME slot identity; delivery is
			// at-least-once and is not claimed otherwise.
			// Expiry first: resending into an attempt that has already run
			// out of time would restart the clock by the back door.
			if now.UnixMilli() > deadline {
				if err := c.failSlotTx(tx, st, machineID, rolloutUnconfirmedReason); err != nil {
					return nil, "", err
				}
				return stop("attempt window expired")
			}
			if resends >= rolloutMaxResends {
				if err := c.failSlotTx(tx, st, machineID, "exhausted automatic resend budget"); err != nil {
					return nil, "", err
				}
				return stop("exhausted automatic resend budget")
			}
			// The DEADLINE AND GRACE ARE PRESERVED. An automatic resend is
			// recovery within one attempt, not a new one: refreshing either
			// would let a crash loop buy unlimited time, which is what the
			// per-attempt bound exists to prevent. A new window is the
			// operator's to grant, through resume().
			if _, err := tx.Exec(`UPDATE agent_rollout_slot SET resend_count = resend_count + 1
				WHERE platform = ? AND generation = ? AND machine_id = ?`,
				platform, st.Generation, machineID); err != nil {
				return nil, "", err
			}
			if err := tx.Commit(); err != nil {
				return nil, "", err
			}
			return &rolloutReservation{Platform: platform, Generation: st.Generation,
				MachineID: machineID, Stage: st.Stage, Attempt: attempt, Resend: true}, "", nil
		}
	case !errors.Is(err, sql.ErrNoRows):
		return nil, "", err
	}

	// Capacity is OUTSTANDING UNVALIDATED work, not a lifetime row count.
	// Healthy, failed and withheld rows accumulate for the life of a
	// generation and must never shrink the batch.
	var outstanding int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM agent_rollout_slot
		WHERE platform = ? AND generation = ? AND state IN (?, ?, ?)`,
		platform, st.Generation, rolloutReserved, rolloutOffered, rolloutObserved).Scan(&outstanding); err != nil {
		return nil, "", err
	}
	if outstanding >= rolloutCapacity(st.Stage) {
		return stop("") // the batch is full; this machine waits its turn
	}

	if _, err := tx.Exec(`INSERT INTO agent_rollout_slot
		(platform, generation, machine_id, stage, attempt, state, reserved_unix_ms, offered_unix_ms, deadline_unix_ms, reason)
		VALUES (?, ?, ?, ?, 1, ?, ?, NULL, ?, '')`,
		platform, st.Generation, machineID, st.Stage, rolloutReserved,
		now.UnixMilli(), now.Add(rolloutAttemptTimeout).UnixMilli()); err != nil {
		return nil, "", err
	}
	if err := tx.Commit(); err != nil {
		return nil, "", err
	}
	return &rolloutReservation{Platform: platform, Generation: st.Generation,
		MachineID: machineID, Stage: st.Stage, Attempt: 1}, "", nil
}

// releaseBeforeSend gives a reservation back without recording an attempt.
//
// Used when the send boundary finds the rollout paused or the candidate
// changed. The row is DELETED rather than marked: capacity must not be held by
// something that was never offered, and the machine must stay eligible once
// the operator resumes.
//
// The delete is conditioned on generation, attempt AND still being reserved,
// so it can only ever remove the row this caller created. A blind delete would
// race another path that has already offered or observed the slot and would
// throw away a real attempt — or worse, real progress.
func (c *rolloutController) releaseBeforeSend(r *rolloutReservation) error {
	_, err := c.db.Exec(`DELETE FROM agent_rollout_slot
		WHERE platform = ? AND generation = ? AND machine_id = ?
		  AND attempt = ? AND state = ?`,
		r.Platform, r.Generation, r.MachineID, r.Attempt, rolloutReserved)
	return err
}

// forgetEvidence drops a machine's in-memory dwell evidence.
//
// Durable slots are NOT touched here: they are deleted inside the machine's
// own delete transaction, so a rollback keeps them.
func (c *rolloutController) forgetEvidence(machineID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.evidence {
		if strings.HasSuffix(key, "\x00"+machineID) {
			delete(c.evidence, key)
		}
	}
}

// observeRunningCandidate records that a machine is running the candidate.
//
// reserved or offered only. Terminal states are left alone: a healthy slot has
// already been validated, and a failed one is the operator's to retry — a
// report arriving afterwards must not quietly reopen either.
//
// This is also how the ambiguous crash window resolves. A process that died
// between the socket write and the mark leaves a slot that says "reserved"
// whether or not the agent ever received the offer; when the machine comes
// back running the candidate, that question is answered by the machine rather
// than by bookkeeping the hub lost.
func (c *rolloutController) observeRunningCandidate(platform, machineID string, generation int64) error {
	_, err := c.db.Exec(`UPDATE agent_rollout_slot SET state = ?, ran_candidate = 1
		WHERE platform = ? AND generation = ? AND machine_id = ? AND state IN (?, ?)`,
		rolloutObserved, platform, generation, machineID, rolloutReserved, rolloutOffered)
	return err
}

// markOffered records that the announcement write returned.
func (c *rolloutController) markOffered(r *rolloutReservation) error {
	_, err := c.db.Exec(`UPDATE agent_rollout_slot
		SET state = ?, offered_unix_ms = ?
		WHERE platform = ? AND generation = ? AND machine_id = ? AND attempt = ? AND state = ?`,
		rolloutOffered, c.now().UnixMilli(),
		r.Platform, r.Generation, r.MachineID, r.Attempt, rolloutReserved)
	return err
}

// withhold records an eligibility refusal, visibly and without halting.
//
// It will NOT convert an existing reserved, offered or observed slot. Freshly
// ineligible machines are checked before they ever reserve, so a row in one of
// those states is an attempt already under way — quite possibly one whose
// write landed and whose mark was lost. Turning it into a withheld row would
// release capacity for an offer that may be live, and let another machine
// become canary alongside it. Those attempts are resolved by evidence or by
// their own timeout, never by a later refusal.
func (c *rolloutController) withhold(platform, machineID, candidate string, release uint64, reason string) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	st, err := c.loadPlatform(tx, platform, candidate, release)
	if err != nil {
		return err
	}
	now := c.now().UnixMilli()
	if _, err := tx.Exec(`INSERT INTO agent_rollout_slot
		(platform, generation, machine_id, stage, attempt, state, reserved_unix_ms, offered_unix_ms, deadline_unix_ms, reason)
		VALUES (?, ?, ?, ?, 0, ?, ?, NULL, ?, ?)
		ON CONFLICT (platform, generation, machine_id) DO UPDATE SET
			state = excluded.state, reason = excluded.reason
		WHERE agent_rollout_slot.state = ?`,
		platform, st.Generation, machineID, st.Stage, rolloutWithheld, now, now, reason,
		rolloutWithheld); err != nil {
		return err
	}
	return tx.Commit()
}

func (c *rolloutController) failSlotTx(tx *sql.Tx, st rolloutPlatformState, machineID, reason string) error {
	if _, err := tx.Exec(`UPDATE agent_rollout_slot SET state = ?, reason = ?
		WHERE platform = ? AND generation = ? AND machine_id = ?`,
		rolloutFailed, reason, st.Platform, st.Generation, machineID); err != nil {
		return err
	}
	// One failed attempt halts the platform. There is no separate failure
	// counter: exposure is already bounded to a canary or a pair, so a second
	// data point would cost another machine to learn the same thing.
	_, err := tx.Exec(`UPDATE agent_rollout_platform
		SET status = ?, halt_reason = ?, updated_unix_ms = ?
		WHERE platform = ? AND generation = ?`,
		rolloutHalted, fmt.Sprintf("%s: %s", machineID, reason), c.now().UnixMilli(),
		st.Platform, st.Generation)
	return err
}

/* ----------------------------------------------------------------------------
 * Health evidence — bound to a connection, never to a machine
 *
 * agentRunningVersions is keyed by machine ID and survives a socket
 * replacement, so a machine that reported the candidate, dropped, and
 * reconnected would still "match" — and a brand-new socket would inherit proof
 * it never produced. That map stays for the UI and for compatibility, but it
 * cannot establish health on its own.
 *
 * Everything below is keyed on the *ConnectedAgent pointer. A replaced socket
 * is a different pointer, so evidence cannot carry across a reconnect, and the
 * registry is consulted so a displaced old socket cannot supply evidence for a
 * machine it no longer owns.
 * -------------------------------------------------------------------------- */

// observeCandidateReport records that a machine reported a SHA on THIS
// connection.
//
// No ownership check, and none is needed: evidence is filed under the
// connection that produced it, so a socket can only ever write into its own
// record. A displaced one writes into a record nobody reads.
//
// The previous shape checked ownership OUTSIDE the lock and then replaced the
// machine's single record under it. A report from a socket that was still the
// owner when it was checked, and had lost the machine by the time it took the
// lock, would overwrite the winner's proof — and nothing about the check
// prevented it, because the check had already passed.
func (c *rolloutController) observeCandidateReport(platform, machineID string, conn *ConnectedAgent,
	generation int64, running, candidate string) {
	if conn == nil || candidate == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ev := c.evidenceFor(platform, machineID, conn)
	if ev.generation != generation || !strings.EqualFold(ev.candidate, candidate) {
		// Different bytes or a different generation: this connection has
		// proven nothing about them yet.
		ev.generation = generation
		ev.candidate = strings.ToLower(candidate)
		ev.sawCandidate = false
		ev.dwellStart = time.Time{}
	}
	if !strings.EqualFold(running, candidate) {
		// Reporting something else ends this connection's dwell: the machine
		// is not on the candidate now, whatever it said before.
		ev.sawCandidate = false
		ev.dwellStart = time.Time{}
		return
	}
	// A version report establishes WHICH BYTES are running and nothing more.
	// It does not advance the dwell, because being told a version is not the
	// same as observing a machine work.
	ev.sawCandidate = true
}

// noteMetrics records a metrics frame and restarts the dwell if the connection
// went quiet for longer than the allowed gap.
//
// The gap is measured BEFORE lastMetrics is overwritten. Overwriting first
// would let a single post-silence frame launder the silence: the connection
// would look freshly alive, the dwell start would be untouched, and a machine
// that went quiet for the whole window would be declared healthy on the
// strength of two endpoints with nothing between them.
func (e *rolloutEvidence) noteMetrics(now time.Time) {
	switch {
	case !e.sawCandidate:
		// Nothing to accrue toward yet.
		e.lastMetrics = now
		e.dwellStart = time.Time{}
		return
	case e.dwellStart.IsZero():
		e.dwellStart = now
	case !e.lastMetrics.IsZero() && now.Sub(e.lastMetrics) > rolloutTelemetryGap:
		e.dwellStart = now
	}
	e.lastMetrics = now
}

// observeTelemetry records a hub-received metrics frame on this connection.
//
// Hub receipt time, never an agent timestamp: a stalled agent can keep
// asserting its own freshness, and a wrong clock makes the claim meaningless
// either way.
func (c *rolloutController) observeTelemetry(platform, machineID string, conn *ConnectedAgent) {
	if conn == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evidenceFor(platform, machineID, conn).noteMetrics(c.now())
}

// evidenceFor returns this connection's own record, creating it if needed.
// Caller holds c.mu.
//
// Records are NOT pruned. They live exactly as long as their handler:
// unregisterAgentConnection drops each connection's record as it exits, and a
// displaced socket is closed, so it exits too. A size backstop was worse than
// nothing here — its eviction order used a timestamp that only advances on a
// write, while an agent reports its version once on connect, so the record
// holding a hard-won sawCandidate looked stale precisely because nothing had
// needed to touch it. Churn on other sockets would then discard the live
// connection's proof and the dwell could never complete until it reconnected.
func (c *rolloutController) evidenceFor(platform, machineID string, conn *ConnectedAgent) *rolloutEvidence {
	key := rolloutKey(platform, machineID)
	byConn := c.evidence[key]
	if byConn == nil {
		byConn = map[*ConnectedAgent]*rolloutEvidence{}
		c.evidence[key] = byConn
	}
	ev := byConn[conn]
	if ev == nil {
		ev = &rolloutEvidence{conn: conn}
		byConn[conn] = ev
	}
	return ev
}

// forgetConnection drops a connection's evidence when its socket goes away.
func (c *rolloutController) forgetConnection(platform, machineID string, conn *ConnectedAgent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := rolloutKey(platform, machineID)
	byConn := c.evidence[key]
	if byConn == nil {
		return
	}
	if conn == nil {
		delete(c.evidence, key)
		return
	}
	delete(byConn, conn)
	if len(byConn) == 0 {
		delete(c.evidence, key)
	}
}

// dwellSatisfied reports whether the machine has proven itself on the
// connection the registry currently owns for it.
//
// The registry is consulted ONCE, here, and outside c.mu — so no path ever
// holds the evidence lock while reaching into the agent registry, and the lock
// order between them cannot invert. Reads select the current owner's record;
// writes only ever touch their own. Neither can clobber the other.
func (c *rolloutController) dwellSatisfied(platform, machineID string,
	generation int64, candidate string) (bool, string) {
	current := c.currentConnection(machineID)
	if current == nil {
		return false, "machine is not connected"
	}

	c.mu.Lock()
	var ev *rolloutEvidence
	if byConn := c.evidence[rolloutKey(platform, machineID)]; byConn != nil {
		ev = byConn[current]
	}
	var sawCandidate bool
	var evGeneration int64
	var evCandidate string
	var dwellStart, lastMetrics time.Time
	if ev != nil {
		sawCandidate, dwellStart, lastMetrics = ev.sawCandidate, ev.dwellStart, ev.lastMetrics
		evGeneration, evCandidate = ev.generation, ev.candidate
	}
	c.mu.Unlock()

	if ev == nil {
		return false, "no evidence from the current connection"
	}
	if evGeneration != generation || !strings.EqualFold(evCandidate, candidate) {
		return false, "evidence is for a different candidate"
	}
	if !sawCandidate {
		return false, "candidate not reported on this connection"
	}
	if dwellStart.IsZero() {
		return false, "no metrics observed since the candidate was reported"
	}
	now := c.now()
	if now.Sub(lastMetrics) > rolloutTelemetryGap {
		return false, "metrics stale on this connection"
	}
	// The dwell is measured between METRICS FRAMES, not against the clock.
	// Comparing now against the start would complete a dwell during silence,
	// so the endpoint has to be an observation rather than a timer expiring.
	if lastMetrics.Sub(dwellStart) < rolloutDwell {
		return false, "dwell in progress"
	}
	return true, ""
}

/* ----------------------------------------------------------------------------
 * Send ownership
 *
 * Durable state alone does not prevent two goroutines from both re-sending a
 * recovered attempt: they can read the same row before either writes. An
 * in-process claim per (platform, generation, machine, attempt) makes a send
 * single-owner within this hub, on top of the durable state that makes it
 * single-owner across restarts.
 * -------------------------------------------------------------------------- */

func (c *rolloutController) claimSend(r *rolloutReservation) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sending == nil {
		c.sending = map[string]bool{}
	}
	key := fmt.Sprintf("%s\x00%d\x00%s\x00%d", r.Platform, r.Generation, r.MachineID, r.Attempt)
	if c.sending[key] {
		return false
	}
	c.sending[key] = true
	return true
}

func (c *rolloutController) releaseSend(r *rolloutReservation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.sending, fmt.Sprintf("%s\x00%d\x00%s\x00%d", r.Platform, r.Generation, r.MachineID, r.Attempt))
}

/* ----------------------------------------------------------------------------
 * Progression
 *
 * The loop owns advancement. Triggers (a connect, a version report, a resume)
 * only wake it; none of them decides anything, which is what keeps racing
 * triggers from each advancing a stage or resending an attempt.
 * -------------------------------------------------------------------------- */

// tick promotes validated slots, advances stages and expires attempts for one
// platform. It takes no lock across socket I/O and performs none.
func (c *rolloutController) tick(platform string) error {
	st, err := c.platformState(platform)
	if err != nil || st == nil || st.Status != rolloutActive {
		return err
	}

	rows, err := c.db.Query(`SELECT machine_id, state, deadline_unix_ms
		FROM agent_rollout_slot
		WHERE platform = ? AND generation = ? AND state IN (?, ?, ?)`,
		platform, st.Generation, rolloutReserved, rolloutOffered, rolloutObserved)
	if err != nil {
		return err
	}
	type live struct {
		machineID string
		state     string
		deadline  int64
	}
	var outstanding []live
	for rows.Next() {
		var l live
		if err := rows.Scan(&l.machineID, &l.state, &l.deadline); err != nil {
			rows.Close()
			return err
		}
		outstanding = append(outstanding, l)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	now := c.now()
	for _, l := range outstanding {
		if ok, _ := c.dwellSatisfied(platform, l.machineID, st.Generation, st.Candidate); ok {
			if _, err := c.db.Exec(`UPDATE agent_rollout_slot SET state = ?, reason = ''
				WHERE platform = ? AND generation = ? AND machine_id = ?`,
				rolloutHealthy, platform, st.Generation, l.machineID); err != nil {
				return err
			}
			continue
		}
		if now.UnixMilli() > l.deadline {
			reason := "did not prove a healthy connection within the attempt window"
			if l.state == rolloutReserved {
				reason = rolloutUnconfirmedReason
			}
			tx, err := c.db.Begin()
			if err != nil {
				return err
			}
			if err := c.failSlotTx(tx, *st, l.machineID, reason); err != nil {
				_ = tx.Rollback()
				return err
			}
			if err := tx.Commit(); err != nil {
				return err
			}
			return nil // halted; nothing further advances this platform
		}
	}

	// Advancement is ONE statement, with both conditions evaluated inside it.
	//
	// Counting first and then updating does not work, and a compare-on-stage
	// does not rescue it: a reservation admitted between the count and the
	// write does not change the stage, so the update still fires and the stage
	// advances past work that was just accepted. The subqueries below are
	// evaluated under the same write lock as the update itself, so no
	// reservation can slip between them.
	//
	// Two conditions, both necessary. NOT EXISTS outstanding is "this stage
	// has nothing left in flight". EXISTS healthy is "this stage actually
	// proved something" — without it, a platform with no slots at all would
	// advance on every tick and be at batch size before its first machine ever
	// connected, and a stage whose every candidate was WITHHELD would advance
	// having learned nothing about the build.
	result, err := c.db.Exec(`UPDATE agent_rollout_platform
		SET stage = stage + 1, updated_unix_ms = ?
		WHERE platform = ? AND generation = ? AND status = ? AND stage = ?
		  AND NOT EXISTS (
		        SELECT 1 FROM agent_rollout_slot s
		        WHERE s.platform = agent_rollout_platform.platform
		          AND s.generation = agent_rollout_platform.generation
		          AND s.stage = agent_rollout_platform.stage
		          AND s.state IN (?, ?, ?))
		  AND EXISTS (
		        SELECT 1 FROM agent_rollout_slot s
		        WHERE s.platform = agent_rollout_platform.platform
		          AND s.generation = agent_rollout_platform.generation
		          AND s.stage = agent_rollout_platform.stage
		          AND s.state = ?)`,
		now.UnixMilli(), platform, st.Generation, rolloutActive, st.Stage,
		rolloutReserved, rolloutOffered, rolloutObserved, rolloutHealthy)
	if err != nil {
		return err
	}
	_, _ = result.RowsAffected()
	return nil
}

func (c *rolloutController) platformState(platform string) (*rolloutPlatformState, error) {
	var st rolloutPlatformState
	st.Platform = platform
	err := c.db.QueryRow(`SELECT generation, candidate_sha, candidate_release, stage, status, halt_reason
		FROM agent_rollout_platform WHERE platform = ?`, platform).
		Scan(&st.Generation, &st.Candidate, &st.Release, &st.Stage, &st.Status, &st.HaltReason)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// rolloutStatus is what an operator is shown.
type rolloutStatus struct {
	Platform   string `json:"platform"`
	Generation int64  `json:"generation"`
	Candidate  string `json:"candidate_sha"`
	Stage      int    `json:"stage"`
	// Status is the DURABLE state: active or halted. Active means the
	// automatic policy is enabled, not that something is being sent right now.
	Status     string `json:"status"`
	HaltReason string `json:"halt_reason,omitempty"`
	// Summary is derived for display and never stored. Whether a fleet is
	// caught up is a statement about machines that happen to be connected, so
	// persisting it created a state that had to be unwound when a late machine
	// appeared — and that reopen path could skip the canary entirely.
	Summary string `json:"summary"`
	// Updated counts machines this generation has SEEN running the candidate,
	// whatever happened to their validation afterwards. Deriving it from the
	// current state instead meant a machine whose dwell failed stopped being
	// counted as updated the moment it failed — leaving "0 updated, 1 failed"
	// for a machine demonstrably on the new build.
	//
	// It is HISTORICAL and scoped to THIS generation's slots: ran_candidate is
	// a latch, so a machine counted here may since have been rolled back,
	// disconnected, or failed. It says "observed on this build in this
	// rollout", never "is on this build now". Machines that were already
	// current, and so never needed a slot, are not represented here at all;
	// MachinesTotal on the fleet endpoints is the denominator, not this.
	Updated int `json:"updated"`
	// Validated is the subset that has completed its dwell.
	Validated int `json:"validated"`
	// Validating is running the candidate with its dwell still accruing.
	//
	// It has to be reported separately, because it is neither pending nor
	// validated: without it a canary mid-dwell left every count at zero and
	// the summary read "waiting for an eligible machine" while a machine was
	// in fact actively proving the build.
	Validating int `json:"validating"`
	// Pending is reserved or offered: an attempt exists and has not resolved.
	//
	// It is shown as "pending attempts", not "offered" and not "pending
	// offers". A reservation is taken before the write to the socket, so it
	// includes attempts never sent — and resume can open a fresh validation
	// attempt for a machine ALREADY running the candidate, which correctly
	// receives no announcement at all. Either word would assert a send, or a
	// rejection, that the hub cannot know about. The JSON field name stays
	// `pending` for compatibility; only the wording changed.
	Pending int `json:"pending"`
	// Withheld machines are ineligible, with reasons. Not failures.
	Withheld       int               `json:"withheld"`
	WithheldReason map[string]string `json:"withheld_reasons,omitempty"`
	Failed         int               `json:"failed"`
	FailedReason   map[string]string `json:"failed_reasons,omitempty"`
}

func (c *rolloutController) status(platform string) (*rolloutStatus, error) {
	st, err := c.platformState(platform)
	if err != nil || st == nil {
		return nil, err
	}
	out := &rolloutStatus{Platform: platform, Generation: st.Generation, Candidate: st.Candidate,
		Stage: st.Stage, Status: st.Status, HaltReason: st.HaltReason,
		WithheldReason: map[string]string{}, FailedReason: map[string]string{}}

	rows, err := c.db.Query(`SELECT machine_id, state, reason, ran_candidate FROM agent_rollout_slot
		WHERE platform = ? AND generation = ?`, platform, st.Generation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var machineID, state, reason string
		var ranCandidate int
		if err := rows.Scan(&machineID, &state, &reason, &ranCandidate); err != nil {
			return nil, err
		}
		if ranCandidate == 1 || state == rolloutHealthy {
			out.Updated++
		}
		switch state {
		case rolloutHealthy:
			out.Validated++
		case rolloutObserved:
			// Running the candidate, dwell still accruing.
			out.Validating++
		case rolloutReserved, rolloutOffered:
			out.Pending++
		case rolloutWithheld:
			out.Withheld++
			out.WithheldReason[machineID] = reason
		case rolloutFailed:
			out.Failed++
			out.FailedReason[machineID] = reason
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out.Summary = rolloutSummary(out)
	return out, nil
}

// rolloutSummary describes a platform in words an operator can act on.
func rolloutSummary(st *rolloutStatus) string {
	switch {
	case st.Status == rolloutHalted:
		return "halted — needs an operator: " + st.HaltReason
	case st.Pending > 0 || st.Validating > 0:
		// Validating machines are in flight too. Omitting them read as
		// "waiting for an eligible machine" for the whole of a canary dwell,
		// which is the one window where the rollout is most busy.
		return fmt.Sprintf("updating: %d pending attempts, %d validating, %d validated",
			st.Pending, st.Validating, st.Validated)
	case st.Validated == 0 && st.Withheld > 0:
		return fmt.Sprintf("waiting for an eligible machine: %d withheld", st.Withheld)
	case st.Validated == 0:
		return "waiting for an eligible machine"
	case st.Failed > 0:
		return fmt.Sprintf("%d validated, %d failed", st.Validated, st.Failed)
	default:
		// This counts SLOTS, and slots exist only for machines this
		// generation reserved one for. A machine that never connected, or was
		// already current, has no slot at all — so this cannot say the
		// connected fleet is caught up, and it must not try. It says only
		// that nothing tracked is still outstanding.
		return fmt.Sprintf("no outstanding tracked attempts: %d validated", st.Validated)
	}
}

// resume clears a halt and gives every failed slot a NEW attempt.
//
// Without the new attempt this is inert: failed rows are terminal, so an
// operator would clear the halt and watch nothing happen. A fresh attempt
// number also resets the restart grace, so recovery gets its own bounded
// window rather than inheriting an exhausted one.
func (c *rolloutController) resume(platform string) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := c.resumeTx(tx, platform); err != nil {
		return err
	}
	return tx.Commit()
}

// resumeTx is resume inside a caller's transaction, so an operator resume can
// clear the pause and retry the failed attempts as one atomic change.
func (c *rolloutController) resumeTx(tx *sql.Tx, platform string) error {
	// Read the platform row through THIS transaction. Calling platformState
	// here used c.db while the transaction already held the only pooled
	// connection, and resume deadlocked outright.
	var generation int64
	if err := tx.QueryRow(`SELECT generation FROM agent_rollout_platform WHERE platform = ?`,
		platform).Scan(&generation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil // nothing rolled out on this platform yet
		}
		return err
	}
	now := c.now()
	// Every failed slot, with NO attempt cap. An operator asking again is a
	// decision, not a retry budget; bounding it by the automatic resend
	// counter is what made Resume clear the halt and then move nothing.
	if _, err := tx.Exec(`UPDATE agent_rollout_slot
		SET state = ?, attempt = attempt + 1, resend_count = 0, grace_used = 0,
		    offered_unix_ms = NULL, deadline_unix_ms = ?, reason = ''
		WHERE platform = ? AND generation = ? AND state = ?`,
		rolloutReserved, now.Add(rolloutAttemptTimeout).UnixMilli(),
		platform, generation, rolloutFailed); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE agent_rollout_platform
		SET status = ?, halt_reason = '', updated_unix_ms = ?
		WHERE platform = ? AND generation = ?`,
		rolloutActive, now.UnixMilli(), platform, generation); err != nil {
		return err
	}
	return nil
}
