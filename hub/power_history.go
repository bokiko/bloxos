package main

// Power history (hub side) — durable 30s power buckets reported by agents
// over the authenticated WebSocket. The hub keys everything by the MACHINE
// THE SOCKET AUTHENTICATED AS, never by anything in the frame: a compromised
// agent cannot plant history on another machine. Batches are validated whole
// before any mutation, deduplicated by (machine, stream, seq), and
// acknowledged only after the transaction committing the contiguous prefix
// (including explicitly declared retention gaps) has committed. A lost ACK
// therefore causes a harmless replay, never a duplicate row or a false
// "new data" signal: sequence high-water lives in stream state, not in the
// chart rows, so pruning old rows never resurrects an old transmission.
//
// Legacy gpu_metrics is untouched: power history is a separate table with
// its own retention, and old agents simply never send these frames.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

// Validation bounds. The contract caps batch shape (records/sensors/bytes);
// these are the hub-side sanity bounds for the values inside a bucket.
const (
	powerMaxIDLen           = 128
	powerMaxWattsPerSensor  = 100000.0  // a single sensor reporting >100kW is bogus
	powerMaxWattsGPUTotal   = 1000000.0 // 16 bogus sensors summed still cap here
	powerMaxWattsDomain     = 100000.0  // a scalar domain (system/cpu/dram) is one counter
	powerMaxSamples         = 1000000
	powerMaxExpectedSamples = 86400 // a day of 1Hz samples
	powerMaxWindowDuration  = time.Hour
	powerMinStartUnixMS     = 1000000000000 // 2001-09-09; rejects garbage epoch values
	powerMaxFutureSkew      = powerhistory.MaxFutureSkewSeconds * time.Second
	powerMaxPastAge         = 48 * time.Hour // retention (24h) plus replay slack
)

var powerIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// powerSourceRe additionally allows ':', which separates a hwmon backend
// from its chip name ("hwmon:power_meter"). Backend labels are display and
// provenance strings only: they never reach a path, a query or an identity.
var powerSourceRe = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

// powerScalarDomains are the domains this hub understands. A source label
// naming one of them must be backed by statistics in the matching field.
// Labels for domains it does NOT know are bounds-checked and stored as-is
// rather than rejected, so a newer agent is never hard-blocked by an older
// hub over a field that hub does not interpret.
var powerScalarDomains = map[string]bool{
	powerhistory.DomainSystem: true,
	powerhistory.DomainCPU:    true,
	powerhistory.DomainDRAM:   true,
}

var (
	errPowerClockSkew = errors.New("machine clock is too far ahead of hub")
	errPowerConflict  = errors.New("conflicting power-history replay")
)

const (
	powerIssueNone uint32 = iota
	powerIssueInvalid
	powerIssueClock
	powerIssueStorage
	powerIssueConflict
)

func (s *Server) powerHistoryProblem(machineID string) string {
	s.agentsMu.RLock()
	agent := s.agents[machineID]
	s.agentsMu.RUnlock()
	if agent == nil {
		return ""
	}
	switch agent.powerIssue.Load() {
	case powerIssueInvalid:
		return "rejected_data"
	case powerIssueClock:
		return "clock_skew"
	case powerIssueStorage:
		return "storage_error"
	case powerIssueConflict:
		return "conflicting_replay"
	default:
		return ""
	}
}

// --- validation (pure, no I/O — runs before any mutation) ---

func validPowerID(id string) bool {
	return len(id) > 0 && len(id) <= powerMaxIDLen && powerIDRe.MatchString(id)
}

func validPowerStats(st *powerhistory.Stats, expected int, maxWatts float64) error {
	if st.Samples < 0 || st.Samples > powerMaxSamples {
		return fmt.Errorf("samples %d out of range", st.Samples)
	}
	// Stats are meaningful only over observed samples: mean AND peak are
	// present exactly when samples > 0, never alongside zero samples.
	if st.Samples == 0 && (st.MeanWatts != nil || st.PeakWatts != nil) {
		return fmt.Errorf("stats present with zero samples")
	}
	if st.Samples > 0 && (st.MeanWatts == nil || st.PeakWatts == nil) {
		return fmt.Errorf("samples %d without both mean and peak", st.Samples)
	}
	if st.Samples > expected {
		return fmt.Errorf("samples %d exceed expected %d", st.Samples, expected)
	}
	for name, w := range map[string]*float64{"mean_watts": st.MeanWatts, "peak_watts": st.PeakWatts} {
		if w == nil {
			continue
		}
		if math.IsNaN(*w) || math.IsInf(*w, 0) {
			return fmt.Errorf("%s not finite", name)
		}
		if *w < 0 || *w > maxWatts {
			return fmt.Errorf("%s %f out of range", name, *w)
		}
	}
	if st.MeanWatts != nil && st.PeakWatts != nil && *st.PeakWatts < *st.MeanWatts {
		return fmt.Errorf("peak %f below mean %f", *st.PeakWatts, *st.MeanWatts)
	}
	return nil
}

// validatePowerBatch rejects the whole batch unless every field is within
// contract and sanity bounds. Nothing may be written for a half-valid batch.
//
// The measurement-AGE check is deliberately NOT here: it runs in
// commitPowerBatch only for genuinely new seqs, after dedupe. A duplicate
// replay of already-committed data must never be rejected for age — the
// agent would retry the same unacked journal entry forever. Agents prune
// aged entries from the journal before upload instead.
func validatePowerBatch(b *powerhistory.Batch, now time.Time) error {
	if b.Type != powerhistory.BatchType {
		return fmt.Errorf("type %q", b.Type)
	}
	if !validPowerID(b.StreamID) {
		return fmt.Errorf("invalid stream_id %q", b.StreamID)
	}
	// 0 means "no retention declared", which is only valid once buckets
	// exist; a declaration (including the zero-bucket gap-only batch an
	// agent sends after retention eats its last unsent record) names the
	// oldest retained seq, which starts at 1.
	if b.RetainedFrom < 1 || b.RetainedFrom > powerhistory.MaxSeq {
		return fmt.Errorf("retained_from %d out of range", b.RetainedFrom)
	}
	if len(b.Buckets) > powerhistory.MaxBatchRecords {
		return fmt.Errorf("bucket count %d out of range", len(b.Buckets))
	}
	futureLimit := now.Add(powerMaxFutureSkew).UnixMilli()
	var prevSeq uint64
	for i := range b.Buckets {
		bk := &b.Buckets[i]
		if bk.Seq == 0 || bk.Seq > powerhistory.MaxSeq {
			return fmt.Errorf("bucket %d: seq %d out of range", i, bk.Seq)
		}
		if i > 0 && bk.Seq <= prevSeq {
			return fmt.Errorf("bucket %d: seq %d not strictly increasing", i, bk.Seq)
		}
		prevSeq = bk.Seq
		if bk.Seq < b.RetainedFrom {
			return fmt.Errorf("bucket %d: seq %d below retained_from %d", i, bk.Seq, b.RetainedFrom)
		}
		if bk.StartUnixMS > futureLimit || bk.EndUnixMS > futureLimit {
			return fmt.Errorf("%w: bucket %d", errPowerClockSkew, i)
		}
		if bk.StartUnixMS < powerMinStartUnixMS {
			return fmt.Errorf("bucket %d: start_unix_ms %d out of range", i, bk.StartUnixMS)
		}
		if bk.EndUnixMS <= bk.StartUnixMS || bk.EndUnixMS > futureLimit {
			return fmt.Errorf("bucket %d: end_unix_ms %d out of range", i, bk.EndUnixMS)
		}
		if time.Duration(bk.EndUnixMS-bk.StartUnixMS)*time.Millisecond > powerMaxWindowDuration {
			return fmt.Errorf("bucket %d: window duration out of range", i)
		}
		if bk.ExpectedSamples < 1 || bk.ExpectedSamples > powerMaxExpectedSamples {
			return fmt.Errorf("bucket %d: expected_samples %d out of range", i, bk.ExpectedSamples)
		}
		if len(bk.GPUs) > powerhistory.MaxSensors {
			return fmt.Errorf("bucket %d: %d GPUs exceeds max %d", i, len(bk.GPUs), powerhistory.MaxSensors)
		}
		seen := make(map[string]struct{}, len(bk.GPUs))
		minGPUSamples := 0
		for j := range bk.GPUs {
			g := &bk.GPUs[j]
			if !validPowerID(g.ID) {
				return fmt.Errorf("bucket %d gpu %d: invalid id %q", i, j, g.ID)
			}
			if _, dup := seen[g.ID]; dup {
				return fmt.Errorf("bucket %d: duplicate gpu id %q", i, g.ID)
			}
			seen[g.ID] = struct{}{}
			if err := validPowerStats(&g.Stats, bk.ExpectedSamples, powerMaxWattsPerSensor); err != nil {
				return fmt.Errorf("bucket %d gpu %q: %w", i, g.ID, err)
			}
			if j == 0 || g.Samples < minGPUSamples {
				minGPUSamples = g.Samples
			}
		}
		if bk.GPUTotal != nil {
			if err := validPowerStats(bk.GPUTotal, bk.ExpectedSamples, powerMaxWattsGPUTotal); err != nil {
				return fmt.Errorf("bucket %d gpu_total: %w", i, err)
			}
			// GPUTotal is computed from complete simultaneous observations:
			// it cannot exist without GPUs, and cannot cover more
			// observations than the least-observed GPU in the set.
			if bk.GPUTotal.Samples > 0 {
				if len(bk.GPUs) == 0 {
					return fmt.Errorf("bucket %d: gpu_total without gpus", i)
				}
				if bk.GPUTotal.Samples > minGPUSamples {
					return fmt.Errorf("bucket %d: gpu_total samples %d exceed least-observed gpu %d", i, bk.GPUTotal.Samples, minGPUSamples)
				}
			}
		}
		// Scalar domains. They are validated INDEPENDENTLY and never against
		// one another: system, cpu and dram come from different backends on
		// different sampling schedules, so "system must exceed cpu" is not a
		// property the hub may assume, let alone enforce. Nothing here sums
		// them either — system already contains cpu where both exist.
		for _, d := range []struct {
			name  string
			stats *powerhistory.Stats
		}{
			{powerhistory.DomainSystem, bk.System},
			{powerhistory.DomainCPU, bk.CPU},
			{powerhistory.DomainDRAM, bk.DRAM},
		} {
			if d.stats == nil {
				continue
			}
			if err := validPowerStats(d.stats, bk.ExpectedSamples, powerMaxWattsDomain); err != nil {
				return fmt.Errorf("bucket %d %s: %w", i, d.name, err)
			}
		}
		if err := validPowerSources(bk); err != nil {
			return fmt.Errorf("bucket %d: %w", i, err)
		}
	}
	return nil
}

// validPowerSources bounds the backend labels and holds them to their one
// promise: a label claims that a domain's statistics in THIS bucket came
// from that backend, so a label for a domain carrying no statistics is not a
// weaker claim, it is a false one.
//
// An absent Sources array is not an error. Agents predating source labelling
// send CPU statistics with no label at all, and those agents keep reporting
// indefinitely.
func validPowerSources(bk *powerhistory.Bucket) error {
	if len(bk.Sources) > powerhistory.MaxDomainSources {
		return fmt.Errorf("%d sources exceeds max %d", len(bk.Sources), powerhistory.MaxDomainSources)
	}
	present := map[string]*powerhistory.Stats{
		powerhistory.DomainSystem: bk.System,
		powerhistory.DomainCPU:    bk.CPU,
		powerhistory.DomainDRAM:   bk.DRAM,
	}
	seen := make(map[string]struct{}, len(bk.Sources))
	for j, src := range bk.Sources {
		if !validPowerLabel(src.Domain) {
			return fmt.Errorf("source %d: invalid domain %q", j, src.Domain)
		}
		if !validPowerLabel(src.Source) {
			return fmt.Errorf("source %d: invalid source %q", j, src.Source)
		}
		if _, dup := seen[src.Domain]; dup {
			return fmt.Errorf("duplicate source for domain %q", src.Domain)
		}
		seen[src.Domain] = struct{}{}
		if powerScalarDomains[src.Domain] && present[src.Domain] == nil {
			return fmt.Errorf("source %q labels domain %q with no statistics", src.Source, src.Domain)
		}
	}
	return nil
}

func validPowerLabel(s string) bool {
	return len(s) > 0 && len(s) <= powerhistory.MaxSourceLen && powerSourceRe.MatchString(s)
}

// --- ingest (agent WebSocket) ---

// ingestPowerHistory handles one power_history frame from an authenticated
// agent socket. It returns a non-nil error for every rejected or failed
// batch and only sends the ACK frame after the committing transaction has
// durably committed — an ACK implies durable contiguous prefix, so a lost
// ACK after commit is a harmless replay, while a commit failure must never
// be acknowledged.
func (s *Server) ingestPowerHistory(machineID string, agent *ConnectedAgent, raw []byte) error {
	ack, err := s.commitPowerHistoryFrame(machineID, agent, raw)
	if err != nil {
		return err
	}
	if ack != nil {
		if err := agent.writeLocked(websocket.TextMessage, ack); err != nil {
			return fmt.Errorf("ack write: %w", err)
		}
	}
	return nil
}

// commitPowerHistoryFrame validates and durably commits one batch and
// returns the ACK frame to send, or nil when nothing was committed. It
// performs no network write: the read loop runs it under the ingestion
// barrier and sends the ACK afterwards, so a socket whose peer is not
// reading can never hold the barrier and stall machine deletion.
func (s *Server) commitPowerHistoryFrame(machineID string, agent *ConnectedAgent, raw []byte) ([]byte, error) {
	if machineID == "" || !s.isRegisteredConnection(machineID, agent) {
		return nil, fmt.Errorf("unregistered connection")
	}
	// Diagnostic state belongs only to this registered connection. No invalid
	// frame mutates durable telemetry or advances an ACK. Never expose raw errors.
	issue := powerIssueInvalid
	defer func() { agent.powerIssue.Store(issue) }()
	if len(raw) > powerhistory.MaxFrameBytes {
		return nil, fmt.Errorf("frame %d bytes exceeds %d", len(raw), powerhistory.MaxFrameBytes)
	}
	var batch powerhistory.Batch
	if err := json.Unmarshal(raw, &batch); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if err := validatePowerBatch(&batch, time.Now()); err != nil {
		if errors.Is(err, errPowerClockSkew) {
			issue = powerIssueClock
		}
		return nil, err
	}
	issue = powerIssueStorage
	through, err := s.commitPowerBatch(machineID, &batch, time.Now())
	if err != nil {
		if errors.Is(err, errPowerConflict) {
			issue = powerIssueConflict
		}
		log.Printf("power history commit for %s stream %s: %v", machineID, batch.StreamID, err)
		return nil, err
	}
	issue = powerIssueNone
	agent.powerIssue.Store(issue) // clear before the successful ACK is observable
	ack, _ := json.Marshal(powerhistory.Ack{Type: powerhistory.AckType, StreamID: batch.StreamID, Through: through})
	return ack, nil
}

type powerStreamState struct {
	ackedThrough uint64
	maxSeenSeq   uint64
	retainedFrom uint64
}

// commitPowerBatch applies one validated batch in a single transaction:
// existence-based idempotent dedupe, conflict rejection, age enforcement
// for genuinely new data only, explicit gap declaration, and
// contiguous-prefix advancement. Returns the new acked-through sequence.
//
// An empty bucket list is valid: it is the gap-only batch an agent sends
// after retention eats its last unsent record — retention loss declared,
// nothing to insert. Dedupe is by RECORD EXISTENCE, not by max_seen_seq:
// an out-of-order arrival (3, then 1,2 to fill the hole) must insert 1 and
// 2 even though maxSeen already says 3.
func (s *Server) commitPowerBatch(machineID string, b *powerhistory.Batch, now time.Time) (uint64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	st := powerStreamState{}
	err = tx.QueryRow(`SELECT acked_through, max_seen_seq, retained_from
		FROM power_history_stream_state WHERE machine_id = ? AND stream_id = ?`,
		machineID, b.StreamID).Scan(&st.ackedThrough, &st.maxSeenSeq, &st.retainedFrom)
	if err == sql.ErrNoRows {
		err = nil
	} else if err != nil {
		return 0, err
	}

	// Load existing rows in the batch's seq range for dedupe/conflict checks.
	existing := map[uint64]string{}
	if len(b.Buckets) > 0 {
		minSeq, maxSeq := b.Buckets[0].Seq, b.Buckets[0].Seq
		for _, bk := range b.Buckets {
			if bk.Seq < minSeq {
				minSeq = bk.Seq
			}
			if bk.Seq > maxSeq {
				maxSeq = bk.Seq
			}
		}
		rows, err := tx.Query(`SELECT seq, payload FROM power_history_records
			WHERE machine_id = ? AND stream_id = ? AND seq BETWEEN ? AND ?`,
			machineID, b.StreamID, minSeq, maxSeq)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var seq uint64
			var payload string
			if err := rows.Scan(&seq, &payload); err != nil {
				rows.Close()
				return 0, err
			}
			existing[seq] = payload
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return 0, err
		}
	}

	pastLimit := now.Add(-powerMaxPastAge).UnixMilli()
	for i := range b.Buckets {
		bk := &b.Buckets[i]
		payload, err := json.Marshal(bk)
		if err != nil {
			return 0, err
		}
		if stored, ok := existing[bk.Seq]; ok {
			// Compare both sides using the current schema: field order, omitted
			// optional fields and additive schema changes are not data conflicts.
			var previous powerhistory.Bucket
			if err := json.Unmarshal([]byte(stored), &previous); err != nil {
				return 0, fmt.Errorf("seq %d: corrupt stored payload", bk.Seq)
			}
			canonical, err := json.Marshal(previous)
			if err != nil {
				return 0, err
			}
			if string(canonical) != string(payload) {
				return 0, fmt.Errorf("%w: seq %d", errPowerConflict, bk.Seq)
			}
			continue
		}
		if bk.Seq <= st.ackedThrough {
			// Acked but the row was pruned by retention: absorb silently —
			// the high-water in stream state still answers for this seq, so
			// an old retransmission never becomes a new row.
			continue
		}
		// Genuinely new (out-of-order arrivals included). Age is enforced
		// HERE, after dedupe, so an aged duplicate replay is absorbed above
		// instead of poisoning the agent's retry loop — the agent prunes aged
		// entries from its journal before upload; data this old is outside
		// retention and cannot be accepted as new.
		if bk.StartUnixMS < pastLimit {
			return 0, fmt.Errorf("seq %d: start older than retention window; prune before upload", bk.Seq)
		}
		if _, err := tx.Exec(`INSERT INTO power_history_records
			(machine_id, stream_id, seq, start_unix_ms, end_unix_ms, expected_samples, payload)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			machineID, b.StreamID, bk.Seq, bk.StartUnixMS, bk.EndUnixMS, bk.ExpectedSamples, string(payload)); err != nil {
			return 0, err
		}
		if bk.Seq > st.maxSeenSeq {
			st.maxSeenSeq = bk.Seq
		}
	}

	// Explicit retention-gap declaration. retained_from is the oldest seq
	// the agent still holds; advancing it past unacknowledged seqs declares
	// them permanently lost. Only that declared pruned prefix becomes gaps —
	// missing seqs the agent has NOT declared lost stay holes, never silent
	// gaps, and keep the un-acked prefix discontiguous.
	if b.RetainedFrom > st.retainedFrom {
		// Candidate lost seqs are (ackedThrough, retainedFrom). When the
		// prefix already covers them there is nothing to declare; the guard
		// also keeps ackedThrough+1 from wrapping at MaxSeq.
		if b.RetainedFrom > st.ackedThrough+1 {
			if err := declarePowerGapsForMissing(tx, machineID, b.StreamID, st.ackedThrough+1, b.RetainedFrom-1, b.Buckets); err != nil {
				return 0, err
			}
		}
		st.retainedFrom = b.RetainedFrom
	}

	// Advance the contiguous durable prefix: present records and declared
	// gaps both count; an undeclared hole stops the advance.
	through, err := powerAckedThrough(tx, machineID, b.StreamID, st.ackedThrough)
	if err != nil {
		return 0, err
	}
	st.ackedThrough = through

	degraded := 0
	if b.Degraded {
		degraded = 1
	}
	if _, err := tx.Exec(`INSERT INTO power_history_stream_state
		(machine_id, stream_id, acked_through, max_seen_seq, retained_from, degraded, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(machine_id, stream_id) DO UPDATE SET
			acked_through = excluded.acked_through,
			max_seen_seq = excluded.max_seen_seq,
			retained_from = excluded.retained_from,
			degraded = excluded.degraded,
			updated_at = CURRENT_TIMESTAMP`,
		machineID, b.StreamID, st.ackedThrough, st.maxSeenSeq, st.retainedFrom, degraded); err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return st.ackedThrough, nil
}

// declarePowerGapsForMissing declares one merged gap per contiguous run of
// seqs missing from [lo, hi]. Work is O(existing rows + batch buckets):
// present seqs are collected and sorted, then interval-subtracted from the
// range — the seq space itself (up to 2^53) is never walked, so a huge
// declared gap cannot stall the hub.
func declarePowerGapsForMissing(tx *sql.Tx, machineID, streamID string, lo, hi uint64, buckets []powerhistory.Bucket) error {
	presentSet := map[uint64]bool{}
	for _, bk := range buckets {
		if bk.Seq >= lo && bk.Seq <= hi {
			presentSet[bk.Seq] = true
		}
	}
	rows, err := tx.Query(`SELECT seq FROM power_history_records
		WHERE machine_id = ? AND stream_id = ? AND seq BETWEEN ? AND ?`,
		machineID, streamID, lo, hi)
	if err != nil {
		return err
	}
	for rows.Next() {
		var seq uint64
		if err := rows.Scan(&seq); err != nil {
			rows.Close()
			return err
		}
		presentSet[seq] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	present := make([]uint64, 0, len(presentSet))
	for seq := range presentSet {
		present = append(present, seq)
	}
	sort.Slice(present, func(i, j int) bool { return present[i] < present[j] })

	cursor := lo
	for _, seq := range present {
		if cursor > hi {
			break
		}
		if seq > cursor {
			if err := declarePowerGap(tx, machineID, streamID, cursor, seq-1); err != nil {
				return err
			}
		}
		if seq >= cursor {
			cursor = seq + 1 // present is sorted+unique; seq+1 cannot exceed hi+1 <= MaxSeq
		}
	}
	if cursor <= hi {
		return declarePowerGap(tx, machineID, streamID, cursor, hi)
	}
	return nil
}

// declarePowerGap records [fromSeq, throughSeq] as permanently lost for the
// stream, merging with overlapping or directly adjacent gap rows so the gap
// table stays one row per contiguous run.
func declarePowerGap(tx *sql.Tx, machineID, streamID string, fromSeq, throughSeq uint64) error {
	// Extend across neighbors that overlap or touch the new range. MIN/MAX
	// over an empty match still returns one row with NULL columns (not
	// ErrNoRows), so scan into nullable values.
	lo, hi := fromSeq, throughSeq
	if lo > 1 {
		lo--
	}
	if hi < powerhistory.MaxSeq {
		hi++
	}
	var mergedLo, mergedHi sql.NullInt64
	err := tx.QueryRow(`SELECT MIN(from_seq), MAX(through_seq) FROM power_history_gaps
		WHERE machine_id = ? AND stream_id = ? AND from_seq <= ? AND through_seq >= ?`,
		machineID, streamID, hi, lo).Scan(&mergedLo, &mergedHi)
	if err != nil {
		return err
	}
	mergedFrom, mergedThrough := fromSeq, throughSeq
	if mergedLo.Valid {
		mergedFrom, mergedThrough = uint64(mergedLo.Int64), uint64(mergedHi.Int64)
		if fromSeq < mergedFrom {
			mergedFrom = fromSeq
		}
		if throughSeq > mergedThrough {
			mergedThrough = throughSeq
		}
		if _, err := tx.Exec(`DELETE FROM power_history_gaps
			WHERE machine_id = ? AND stream_id = ? AND from_seq <= ? AND through_seq >= ?`,
			machineID, streamID, hi, lo); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO power_history_gaps (machine_id, stream_id, from_seq, through_seq)
		VALUES (?, ?, ?, ?)`, machineID, streamID, mergedFrom, mergedThrough)
	return err
}

// powerAckedThrough walks present records and declared gaps starting just
// after fromThrough and returns the last seq of the contiguous committed
// prefix.
func powerAckedThrough(tx *sql.Tx, machineID, streamID string, fromThrough uint64) (uint64, error) {
	if fromThrough >= powerhistory.MaxSeq {
		return fromThrough, nil
	}
	var present []uint64
	prows, err := tx.Query(`SELECT seq FROM power_history_records
		WHERE machine_id = ? AND stream_id = ? AND seq > ? ORDER BY seq`,
		machineID, streamID, fromThrough)
	if err != nil {
		return 0, err
	}
	for prows.Next() {
		var seq uint64
		if err := prows.Scan(&seq); err != nil {
			prows.Close()
			return 0, err
		}
		present = append(present, seq)
	}
	prows.Close()
	if err := prows.Err(); err != nil {
		return 0, err
	}

	type gapRange struct{ from, through uint64 }
	var gaps []gapRange
	grows, err := tx.Query(`SELECT from_seq, through_seq FROM power_history_gaps
		WHERE machine_id = ? AND stream_id = ? AND through_seq > ? ORDER BY from_seq`,
		machineID, streamID, fromThrough)
	if err != nil {
		return 0, err
	}
	for grows.Next() {
		var g gapRange
		if err := grows.Scan(&g.from, &g.through); err != nil {
			grows.Close()
			return 0, err
		}
		gaps = append(gaps, g)
	}
	grows.Close()
	if err := grows.Err(); err != nil {
		return 0, err
	}

	next := fromThrough + 1
	pi := 0
	for {
		advanced := false
		for _, g := range gaps {
			if g.from <= next && next <= g.through {
				next = g.through + 1
				advanced = true
			}
		}
		for pi < len(present) && present[pi] < next {
			pi++
		}
		for pi < len(present) && present[pi] == next {
			next++
			pi++
			advanced = true
		}
		if !advanced {
			break
		}
	}
	return next - 1, nil
}

// --- retention ---

// prunePowerHistory deletes chart rows outside the 24h retention window and
// stale gap rows. Stream STATE (high-water, retained_from) is deliberately
// untouched: retention pruning must never make an old retransmission look
// new again.
func (s *Server) prunePowerHistory(now time.Time) (int64, error) {
	cutoff := now.Add(-powerhistory.RetentionSeconds * time.Second).UnixMilli()
	res, err := s.db.Exec(`DELETE FROM power_history_records WHERE end_unix_ms < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	// Gaps explain holes in the visible window; a gap recorded before the
	// window started is noise. 48h keeps one full window of overlap.
	if _, err := s.db.Exec(`DELETE FROM power_history_gaps WHERE recorded_at < datetime('now', '-48 hours')`); err != nil {
		return n, err
	}
	return n, nil
}

// --- read API ---

const (
	// Default must hold one normal day of 30-second windows (2880) so an
	// untruncated initial read covers the full retention window.
	powerHistoryDefaultMaxRecords = 4096
	powerHistoryMaxRecordsCap     = 16384
)

func clampQueryInt(c echo.Context, name string, def, min, max int) int {
	v, err := strconv.Atoi(c.QueryParam(name))
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// powerHistoryHighWater returns the durable ingestion high-water for a
// machine: the larger of the machine's newest record id and the table's
// AUTOINCREMENT sequence value. The sequence survives row pruning (it lives
// in sqlite_sequence, never reused), so retention can never regress the
// cursor.
func (s *Server) powerHistoryHighWater(machineID string) (uint64, error) {
	var machineMax, globalSeq uint64
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM power_history_records WHERE machine_id = ?`, machineID).Scan(&machineMax); err != nil {
		return 0, err
	}
	switch err := s.db.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name = 'power_history_records'`).Scan(&globalSeq); err {
	case nil:
	case sql.ErrNoRows:
		globalSeq = 0
	default:
		return 0, err
	}
	if globalSeq > machineMax {
		return globalSeq, nil
	}
	return machineMax, nil
}

// handlePowerHistory serves GET /api/machines/:id/power/history — the
// authenticated dashboard view of component power history.
//
// Two modes, one contract:
//   - Initial: no ?after → the NEWEST buckets of the last 24h (bounded by
//     max_records), re-sorted chronologically per stream. All streams participate.
//   - Delta: ?after=N → buckets with an ingestion id > N, ordered by
//     arrival. The cursor is ingestion order, NOT measurement time, so a
//     late backfill (e.g. an agent reconnecting with unacked journal
//     buckets) still reaches a client that already fetched the window.
//
// Delta pages ordered by id with cursor = last returned id when truncated
// never skip records. Gaps and degraded are always the CURRENT machine
// state — an empty delta still reports them. Every response, including an
// empty one, carries cursor: the durable ingestion high-water (or the last
// returned id on a truncated page).
func (s *Server) handlePowerHistory(c echo.Context) error {
	machineID := c.Param("id")
	var exists int
	if err := s.db.QueryRow(`SELECT 1 FROM machines WHERE id = ?`, machineID).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "machine not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to query machine"})
	}

	maxRecords := clampQueryInt(c, "max_records", powerHistoryDefaultMaxRecords, 1, powerHistoryMaxRecordsCap)
	windowStart := time.Now().Add(-powerhistory.RetentionSeconds * time.Second).UnixMilli()

	after, delta, err := parseAfterCursor(c)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	// High-water is read before the rows query: rows landing in between get
	// an id greater than the cursor and are delivered on the next poll.
	highWater, err := s.powerHistoryHighWater(machineID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if highWater > powerhistory.MaxSeq {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "power history cursor exceeds supported range"})
	}
	hist := powerhistory.History{Points: []powerhistory.Point{}, Gaps: []powerhistory.Gap{}, Cursor: highWater}

	var rows *sql.Rows
	if delta {
		rows, err = s.db.Query(`SELECT id, stream_id, payload FROM power_history_records
			WHERE machine_id = ? AND id > ? AND id <= ?
			ORDER BY id ASC LIMIT ?`, machineID, after, highWater, maxRecords)
	} else {
		// Newest-first selection: if the cap truncates the window, the
		// dropped buckets are the oldest, never the current ones.
		rows, err = s.db.Query(`SELECT id, stream_id, payload FROM power_history_records
			WHERE machine_id = ? AND end_unix_ms >= ? AND id <= ?
			ORDER BY end_unix_ms DESC, id DESC LIMIT ?`, machineID, windowStart, highWater, maxRecords)
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	var lastID uint64
	for rows.Next() {
		var ingestID uint64
		var streamID, payload string
		if err := rows.Scan(&ingestID, &streamID, &payload); err != nil {
			rows.Close()
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		var bk powerhistory.Bucket
		if err := json.Unmarshal([]byte(payload), &bk); err != nil {
			rows.Close()
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "corrupt stored bucket"})
		}
		hist.Points = append(hist.Points, powerhistory.Point{StreamID: streamID, Bucket: bk})
		lastID = ingestID
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	// Truncated delta page: resume after the LAST RETURNED id so the
	// unreturned tail is not skipped. Complete or empty page: the durable
	// high-water.
	if delta && len(hist.Points) == maxRecords && lastID > 0 {
		hist.Cursor = lastID
	}

	if !delta {
		// Arrival order for deltas; chronological per stream for the
		// initial view (charts), which selected newest-first above.
		sort.SliceStable(hist.Points, func(i, j int) bool {
			if hist.Points[i].StreamID != hist.Points[j].StreamID {
				return hist.Points[i].StreamID < hist.Points[j].StreamID
			}
			return hist.Points[i].Seq < hist.Points[j].Seq
		})
	}

	return s.finishPowerHistory(c, machineID, hist)
}

// finishPowerHistory attaches current gap and degraded state. Both are
// machine state, not a diff of the returned points: a delta that returns
// zero new buckets must still report the machine's existing gaps and its
// degraded flag. Recently declared gaps remain visible even when all their
// records were lost, or the gap is before/after the surviving sequence range.
func (s *Server) finishPowerHistory(c echo.Context, machineID string, hist powerhistory.History) error {
	rows, err := s.db.Query(`SELECT stream_id, from_seq, through_seq FROM power_history_gaps
		WHERE machine_id = ? AND recorded_at >= datetime('now', '-24 hours')
		ORDER BY recorded_at DESC, stream_id, from_seq LIMIT ?`, machineID, powerHistoryDefaultMaxRecords)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	for rows.Next() {
		var gap powerhistory.Gap
		if err := rows.Scan(&gap.StreamID, &gap.From, &gap.Through); err != nil {
			rows.Close()
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		hist.Gaps = append(hist.Gaps, gap)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	// Degraded reflects the most recent batch state of any stream that
	// reported within the retention window; a stale flag ages out.
	var degradedCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM power_history_stream_state
		WHERE machine_id = ? AND degraded = TRUE AND updated_at >= datetime('now', '-24 hours')`,
		machineID).Scan(&degradedCount); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	hist.Problem = s.powerHistoryProblem(machineID)
	hist.Degraded = degradedCount > 0 || hist.Problem != ""

	return c.JSON(http.StatusOK, hist)
}

// parseAfterCursor extracts the optional ?after delta cursor. Absent means
// an initial full-window read; present must be a non-negative integer.
func parseAfterCursor(c echo.Context) (after uint64, delta bool, err error) {
	raw := c.QueryParam("after")
	if raw == "" {
		return 0, false, nil
	}
	v, perr := strconv.ParseUint(raw, 10, 64)
	if perr != nil || v > powerhistory.MaxSeq {
		return 0, false, fmt.Errorf("invalid after cursor %q", raw)
	}
	return v, true, nil
}
