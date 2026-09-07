package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
)

// powerJournal is a bounded, append-only log of completed buckets. It is
// owned by exactly one goroutine (the journal worker); everything else reads
// the immutable powerSnapshot it publishes.
//
// Files, all inside dir and scoped by stream id:
//
//	stream.json                       current identity — the ONE commit point
//	state-<id>.json                   {acked, reserved}: durable ACK cursor and
//	                                  sequence high-water reservation
//	seg-<id>-<firstseq>.ndjson        record segments, one Bucket per line
//
// Durability rules:
//   - a record is visible to upload only after its line is fsync'd;
//   - a sequence number is only assigned below a reservation that was
//     fsync'd first, so a torn or lost tail after a crash can never hand a
//     hub-committed sequence to different data (the next restart skips
//     ahead to the reservation and flags GapBefore);
//   - identity rotation writes the new state file, then commits stream.json
//     with a single durable rename; a crash on either side leaves a
//     consistent (old or new) world. Any inconsistency found on open
//     (identity without its state file) rotates: fail closed, never reuse.
//
// Bounds (hard, enforced on every append):
//   - retained records ≤ powerJournalMaxRec (24 h of 30 s buckets) and none
//     older than the retention window, checked across ALL records (a clock
//     regression can leave expired records in the interior);
//   - on disk, every segment (powerJournalSegRec = 10 records = 5 min, the
//     live one included) is reconciled against the retained set on every
//     append and on a periodic worker tick, so expiry happens even when no
//     samples arrive. A segment whose records are all gone is deleted; one
//     that is partly gone (horizon crossing, or an expired interior record
//     after a clock regression) is compacted in place via temp file +
//     durable rename. Disk therefore never holds an expired record past the
//     next enforcement and no valid data is ever trimmed early;
//   - on-disk bytes ≤ powerJournalMaxBytes by deleting whole oldest
//     segments (their still-valid records are dropped and the loss is
//     declared through RetainedFrom); a record line above
//     powerJournalMaxLine is refused;
//   - recovery decodes at most the same bounds (records, bytes, segments);
//   - when ANY delete or compaction fails, appends fail (degraded) until
//     enforcement succeeds, rather than letting the directory grow.
type powerJournal struct {
	dir      string
	streamID string

	segs     []*powerSegment
	cur      *os.File // append handle for the last segment; nil = reopen needed
	records  []powerhistory.Bucket
	acked    uint64
	reserved uint64
	nextSeq  uint64
	overCap  bool // last enforcement failed while over a hard cap

	maxRecords int
	maxBytes   int64
	segRecords int
	retention  time.Duration
	now        func() time.Time
}

type powerSegment struct {
	path     string
	firstSeq uint64
	lastSeq  uint64
	count    int
	bytes    int64
}

const (
	powerStreamFile      = "stream.json"
	powerJournalMaxBytes = 32 << 20
	powerJournalMaxLine  = 64 << 10
	powerJournalMaxRec   = powerhistory.RetentionSeconds / powerhistory.WindowSeconds
	powerJournalSegRec   = 10 // five minutes per segment; small so compaction rewrites stay cheap
	powerJournalReserve  = 64 // sequence numbers reserved per state write
)

type powerStreamMeta struct {
	StreamID  string `json:"stream_id"`
	CreatedMS int64  `json:"created_unix_ms"`
}

type powerState struct {
	Acked    uint64 `json:"acked"`
	Reserved uint64 `json:"reserved"`
}

var errPowerLineTooLong = errors.New("record exceeds line limit")

func openPowerJournal(dir string) (*powerJournal, error) {
	return openPowerJournalWith(dir, time.Now)
}

func openPowerJournalWith(dir string, now func() time.Time) (*powerJournal, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	j := &powerJournal{
		dir:        dir,
		maxRecords: powerJournalMaxRec,
		maxBytes:   powerJournalMaxBytes,
		segRecords: powerJournalSegRec,
		retention:  powerhistory.RetentionSeconds * time.Second,
		now:        now,
	}
	if err := j.loadIdentity(); err != nil {
		return nil, err
	}
	if err := j.recover(); err != nil {
		return nil, err
	}
	j.removeOrphans()
	last := max(j.acked, j.reserved)
	if n := len(j.records); n > 0 && j.records[n-1].Seq > last {
		last = j.records[n-1].Seq
	}
	j.nextSeq = last + 1
	if j.nextSeq >= powerMaxSeq {
		if err := j.rotateStream("sequence space exhausted"); err != nil {
			return nil, err
		}
	}
	if err := j.enforce(); err != nil {
		return nil, err
	}
	return j, nil
}

func (j *powerJournal) statePath() string {
	return filepath.Join(j.dir, "state-"+j.streamID+".json")
}

func (j *powerJournal) segPath(firstSeq uint64) string {
	return filepath.Join(j.dir, fmt.Sprintf("seg-%s-%020d.ndjson", j.streamID, firstSeq))
}

// loadIdentity reads stream.json and its state file, rotating on anything
// inconsistent. Identity without state means either a crash mid-rotation
// (impossible by construction) or external tampering; either way the safe
// answer is a new stream.
func (j *powerJournal) loadIdentity() error {
	data, err := os.ReadFile(filepath.Join(j.dir, powerStreamFile))
	if err == nil {
		var m powerStreamMeta
		if json.Unmarshal(data, &m) == nil && isPowerStreamID(m.StreamID) {
			j.streamID = m.StreamID
			st, err := os.ReadFile(j.statePath())
			var s powerState
			if err == nil && json.Unmarshal(st, &s) == nil {
				j.acked, j.reserved = s.Acked, s.Reserved
				return nil
			}
			return j.rotateStream("state file missing or unreadable for stream " + j.streamID)
		}
		log.Printf("power-history: %s unreadable, starting a new stream", powerStreamFile)
	}
	return j.rotateStream("no stream identity")
}

func isPowerStreamID(s string) bool {
	if len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// rotateStream creates a fresh identity. Order: new state file first (an
// orphan if we crash), then stream.json via one durable rename, which is
// the commit. Old files are orphans afterwards and removed best-effort.
func (j *powerJournal) rotateStream(why string) error {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Errorf("stream id: %w", err)
	}
	id := hex.EncodeToString(b[:])
	if j.cur != nil {
		j.cur.Close()
		j.cur = nil
	}
	old := j.streamID
	j.streamID = id
	// Reserved 0: no sequence has been handed out under this identity, so
	// the first append reserves and starts at 1. A restart later resumes
	// at reserved+1 (never below), whatever the journal tail looks like.
	st, _ := json.Marshal(powerState{Acked: 0, Reserved: 0})
	if err := writePowerFileAtomic(j.statePath(), st); err != nil {
		j.streamID = old
		return fmt.Errorf("write state: %w", err)
	}
	meta, _ := json.Marshal(powerStreamMeta{StreamID: id, CreatedMS: j.now().UnixMilli()})
	if err := writePowerFileAtomic(filepath.Join(j.dir, powerStreamFile), meta); err != nil {
		j.streamID = old
		return fmt.Errorf("write stream identity: %w", err)
	}
	log.Printf("power-history: new stream %s (%s)", id, why)
	j.segs = nil
	j.records = nil
	j.acked = 0
	j.reserved = 0
	j.nextSeq = 1
	j.overCap = false
	j.removeOrphans()
	return nil
}

// removeOrphans deletes segment/state files that belong to other streams.
func (j *powerJournal) removeOrphans() {
	entries, err := os.ReadDir(j.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() {
			continue
		}
		ours := n == powerStreamFile ||
			n == "state-"+j.streamID+".json" ||
			strings.HasPrefix(n, "seg-"+j.streamID+"-")
		known := strings.HasPrefix(n, "state-") || strings.HasPrefix(n, "seg-") ||
			(strings.HasPrefix(n, ".") && strings.HasSuffix(n, ".tmp"))
		if !ours && known {
			_ = os.Remove(filepath.Join(j.dir, n))
		}
	}
}

// recover loads this stream's segments in sequence order, keeping the
// longest valid strictly-ascending prefix. A torn/corrupt line truncates
// its segment there and drops every later segment.
func (j *powerJournal) recover() error {
	entries, err := os.ReadDir(j.dir)
	if err != nil {
		return fmt.Errorf("read %s: %w", j.dir, err)
	}
	prefix := "seg-" + j.streamID + "-"
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, prefix) || !strings.HasSuffix(n, ".ndjson") {
			continue
		}
		fs, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(n, prefix), ".ndjson"), 10, 64)
		if err != nil {
			continue
		}
		j.segs = append(j.segs, &powerSegment{path: filepath.Join(j.dir, n), firstSeq: fs})
	}
	sort.Slice(j.segs, func(a, b int) bool { return j.segs[a].firstSeq < j.segs[b].firstSeq })
	// Never load more files than retention could have produced; surplus
	// oldest segments are already outside every bound.
	if maxSegs := j.maxRecords/j.segRecords + 2; len(j.segs) > maxSegs {
		for _, s := range j.segs[:len(j.segs)-maxSegs] {
			_ = os.Remove(s.path)
		}
		j.segs = j.segs[len(j.segs)-maxSegs:]
	}

	var lastSeq uint64
	var totalBytes int64
	for i, seg := range j.segs {
		good, count, recs, clean := j.readSegment(seg.path, lastSeq, j.maxBytes-totalBytes)
		totalBytes += good
		if len(recs) > 0 {
			lastSeq = recs[len(recs)-1].Seq
			seg.lastSeq = lastSeq
		}
		j.records = append(j.records, recs...)
		seg.count, seg.bytes = count, good
		if clean {
			continue
		}
		log.Printf("power-history: %s truncated at byte %d (torn or corrupt record)", filepath.Base(seg.path), good)
		if err := os.Truncate(seg.path, good); err != nil {
			return fmt.Errorf("truncate %s: %w", seg.path, err)
		}
		for _, later := range j.segs[i+1:] {
			_ = os.Remove(later.path)
		}
		j.segs = j.segs[:i+1]
		break
	}
	// Drop empty segments (a truncated-to-zero tail) so firstSeq stays honest.
	kept := j.segs[:0]
	for _, seg := range j.segs {
		if seg.count == 0 {
			_ = os.Remove(seg.path)
			continue
		}
		kept = append(kept, seg)
	}
	j.segs = kept
	return nil
}

// readSegment parses one segment, returning the valid byte prefix, record
// count, records, and whether the whole file was clean. Decoding stops (and
// the rest is treated as corrupt) at the segment record cap, at the line
// cap, or once byteBudget is exhausted, so an oversized file is never
// loaded whole.
func (j *powerJournal) readSegment(path string, lastSeq uint64, byteBudget int64) (int64, int, []powerhistory.Bucket, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, nil, false
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, powerJournalMaxLine+1)
	var good int64
	var recs []powerhistory.Bucket
	for {
		if len(recs) >= j.segRecords || good >= byteBudget {
			// Anything beyond the cap: is there more? then it's not clean.
			_, peekErr := r.Peek(1)
			return good, len(recs), recs, peekErr != nil
		}
		line, err := r.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) || int64(len(line)) > byteBudget-good {
			return good, len(recs), recs, false
		}
		if err != nil { // io.EOF: any partial trailing bytes are torn
			return good, len(recs), recs, len(line) == 0
		}
		var b powerhistory.Bucket
		if json.Unmarshal(bytes.TrimSpace(line), &b) != nil || b.Seq <= lastSeq {
			return good, len(recs), recs, false
		}
		lastSeq = b.Seq
		recs = append(recs, b)
		good += int64(len(line))
	}
}

// ensureOpen (re)opens the append handle for the current segment, starting a
// new segment when there is none or the last one is full. Safe to call after
// any failure: it never leaves the journal permanently closed, and a segment
// is only registered once its file is actually open.
func (j *powerJournal) ensureOpen() error {
	if j.cur != nil {
		return nil
	}
	var seg *powerSegment
	if n := len(j.segs); n > 0 && j.segs[n-1].count < j.segRecords {
		seg = j.segs[n-1]
	}
	fresh := seg == nil
	if fresh {
		seg = &powerSegment{path: j.segPath(j.nextSeq), firstSeq: j.nextSeq}
		// Create the empty segment through the atomic writer so the new
		// directory entry itself is durable (fsync of the parent on POSIX,
		// write-through move on Windows) before any record lands in it. A
		// path with firstSeq == nextSeq can never be a confirmed segment
		// (recovery loaded everything below nextSeq), so replacing whatever
		// sits there is safe; confirmed segments are never truncated here.
		if err := writePowerFileAtomic(seg.path, nil); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(seg.path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	// A failed earlier write may have left a partial line on an existing
	// segment; cut back to the known-good byte count so it stays parseable.
	if st, err := f.Stat(); err == nil && st.Size() != seg.bytes {
		if err := f.Truncate(seg.bytes); err != nil {
			f.Close()
			return err
		}
	}
	if fresh {
		j.segs = append(j.segs, seg)
	}
	j.cur = f
	return nil
}

func (j *powerJournal) writeState(acked, reserved uint64) error {
	data, _ := json.Marshal(powerState{Acked: acked, Reserved: reserved})
	if err := writePowerFileAtomic(j.statePath(), data); err != nil {
		return fmt.Errorf("persist state: %w", err)
	}
	j.acked, j.reserved = acked, reserved
	return nil
}

// append assigns the next sequence, writes and fsyncs the record, then makes
// it retained. Every failure path leaves the journal ready to retry.
func (j *powerJournal) append(b powerhistory.Bucket) error {
	if j.overCap {
		if err := j.enforce(); err != nil {
			return fmt.Errorf("retention not enforceable, refusing new history: %w", err)
		}
	}
	if j.nextSeq >= powerMaxSeq {
		if err := j.rotateStream("sequence space exhausted"); err != nil {
			return err
		}
	}
	if j.nextSeq > j.reserved {
		if err := j.writeState(j.acked, j.nextSeq+powerJournalReserve); err != nil {
			return err
		}
	}
	b.Seq = j.nextSeq
	line, err := json.Marshal(b)
	if err != nil {
		return err
	}
	if len(line) > powerJournalMaxLine {
		return errPowerLineTooLong
	}
	line = append(line, '\n')
	if len(j.segs) > 0 && j.segs[len(j.segs)-1].count >= j.segRecords && j.cur != nil {
		j.cur.Close()
		j.cur = nil
	}
	if err := j.ensureOpen(); err != nil {
		return err
	}
	if _, err := j.cur.Write(line); err != nil {
		j.cur.Close()
		j.cur = nil
		return err
	}
	if err := j.cur.Sync(); err != nil {
		j.cur.Close()
		j.cur = nil
		return err
	}
	seg := j.segs[len(j.segs)-1]
	seg.count++
	seg.bytes += int64(len(line))
	seg.lastSeq = b.Seq
	j.nextSeq++
	j.records = append(j.records, b)
	return j.enforce()
}

// enforce applies the hard bounds. In RAM: age filter across all records,
// then the newest maxRecords. On disk: every segment is reconciled with RAM
// membership — deleted when nothing in it is retained, compacted when only
// part of it is — and then whole oldest segments go while total bytes
// exceed the cap. Any failure sets overCap, which makes appends refuse
// until a later enforcement succeeds.
func (j *powerJournal) enforce() error {
	now := j.now()
	cut := now.Add(-j.retention).UnixMilli()
	horizon := now.Add(powerhistory.MaxFutureSkewSeconds * time.Second).UnixMilli()
	expired := 0
	for _, r := range j.records {
		if !powerRecordLive(r, cut, horizon) {
			expired++
		}
	}
	if expired > 0 {
		// Fresh slice: the published snapshot aliases the old backing
		// array and must never observe in-place compaction.
		kept := make([]powerhistory.Bucket, 0, len(j.records)-expired)
		for _, r := range j.records {
			if powerRecordLive(r, cut, horizon) {
				kept = append(kept, r)
			}
		}
		j.records = kept
	}
	if n := len(j.records); n > j.maxRecords {
		j.records = j.records[n-j.maxRecords:]
	}

	var firstErr error
	var total int64
	live := (*powerSegment)(nil)
	if n := len(j.segs); n > 0 {
		live = j.segs[n-1]
	}
	kept := make([]*powerSegment, 0, len(j.segs))
	ri := 0
	for _, s := range j.segs {
		for ri < len(j.records) && j.records[ri].Seq < s.firstSeq {
			ri++
		}
		start := ri
		for ri < len(j.records) && j.records[ri].Seq <= s.lastSeq {
			ri++
		}
		inSeg := j.records[start:ri]
		if len(inSeg) == s.count || firstErr != nil {
			kept = append(kept, s)
			total += s.bytes
			continue
		}
		if s == live && j.cur != nil {
			j.cur.Close()
			j.cur = nil
		}
		if len(inSeg) == 0 {
			if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				firstErr = err
				kept = append(kept, s)
				total += s.bytes
			}
			continue
		}
		if err := j.compactSegment(s, inSeg); err != nil {
			firstErr = err
		}
		kept = append(kept, s)
		total += s.bytes
	}
	j.segs = kept

	for firstErr == nil && total > j.maxBytes && len(j.segs) > 0 {
		s := j.segs[0]
		if s == live && j.cur != nil {
			j.cur.Close()
			j.cur = nil
		}
		if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			firstErr = err
			break
		}
		total -= s.bytes
		i := 0
		for i < len(j.records) && j.records[i].Seq <= s.lastSeq {
			i++
		}
		j.records = j.records[i:]
		j.segs = j.segs[1:]
	}

	if cap(j.records) > 2*j.maxRecords+2*j.segRecords {
		fresh := make([]powerhistory.Bucket, len(j.records))
		copy(fresh, j.records)
		j.records = fresh
	}
	if firstErr != nil {
		j.overCap = true
		return firstErr
	}
	j.overCap = false
	return nil
}

// powerRecordLive reports whether a record is inside the retention window
// and not implausibly far in the future. A bucket stamped while the wall
// clock was wrong (jumped ahead, later corrected) would otherwise sit at the
// head of the replay queue and be rejected by the hub's skew check for as
// long as it stayed "in the future", blocking every valid bucket behind it.
// Such records are treated as lost: dropped, compacted away, and declared
// through RetainedFrom like any other retention loss.
func powerRecordLive(r powerhistory.Bucket, cut, horizon int64) bool {
	return r.EndUnixMS >= cut && r.EndUnixMS <= horizon
}

// compactSegment rewrites s so it holds exactly recs (temp file + durable
// rename; the caller has closed the live handle if s is live).
func (j *powerJournal) compactSegment(s *powerSegment, recs []powerhistory.Bucket) error {
	var buf bytes.Buffer
	for _, b := range recs {
		line, err := json.Marshal(b)
		if err != nil {
			return err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	if err := writePowerFileAtomic(s.path, buf.Bytes()); err != nil {
		return err
	}
	s.count = len(recs)
	s.bytes = int64(buf.Len())
	s.lastSeq = recs[len(recs)-1].Seq
	return nil
}

// ack advances the cursor for a matching stream. through must not exceed
// the last durable seq; anything at or below the current cursor is a
// harmless replay. The cursor is persisted before the call returns.
func (j *powerJournal) ack(streamID string, through uint64) (bool, error) {
	if streamID != j.streamID {
		return false, fmt.Errorf("stream mismatch (%s != %s)", streamID, j.streamID)
	}
	if through >= j.nextSeq {
		return false, fmt.Errorf("through %d beyond last durable seq %d", through, j.nextSeq-1)
	}
	if through <= j.acked {
		return false, nil
	}
	if err := j.writeState(through, j.reserved); err != nil {
		return false, err
	}
	return true, nil
}

func (j *powerJournal) close() {
	if j.cur != nil {
		j.cur.Close()
		j.cur = nil
	}
}

// powerSnapshot is the immutable view published to the send path. records
// aliases the journal's slice: the worker only ever appends past this
// length or replaces the slice, never mutates elements within it.
type powerSnapshot struct {
	streamID  string
	acked     uint64
	nextSeq   uint64
	records   []powerhistory.Bucket
	retention time.Duration
	now       func() time.Time
}

func (j *powerJournal) snapshot() *powerSnapshot {
	return &powerSnapshot{
		streamID:  j.streamID,
		acked:     j.acked,
		nextSeq:   j.nextSeq,
		records:   j.records[:len(j.records):len(j.records)],
		retention: j.retention,
		now:       j.now,
	}
}

// pendingBatch returns the next replayable buckets: the oldest contiguous
// run of unacknowledged records, bounded by count and encoded size, with the
// age and future-skew bounds applied at read time so a stale snapshot never
// exposes expired or clock-poisoned records. RetainedFrom is the oldest replayable pending seq (nextSeq when
// nothing is pending) — NOT the oldest record on disk, since acknowledged
// history is kept locally for the full retention window. A RetainedFrom
// above acked+1 explicitly declares every sequence in between as lost
// (retention trim, torn journal, or a post-restart reservation skip); the
// hub commits that gap and ACKs across it. ok is false when there is
// nothing to send and no gap to declare.
func (s *powerSnapshot) pendingBatch(maxRecords, maxBytes int) (*powerhistory.Batch, bool) {
	now := s.now()
	cut := now.Add(-s.retention).UnixMilli()
	horizon := now.Add(powerhistory.MaxFutureSkewSeconds * time.Second).UnixMilli()
	var pending []powerhistory.Bucket
	for _, r := range s.records {
		if r.Seq > s.acked && powerRecordLive(r, cut, horizon) {
			pending = append(pending, r)
		}
	}
	// Only a contiguous run is replayable in one batch; anything after a
	// sequence hole waits for the ACK that lets the next batch declare it.
	end := 0
	for end < len(pending) && end < maxRecords {
		if end > 0 && pending[end].Seq != pending[end-1].Seq+1 {
			break
		}
		end++
	}
	pending = pending[:end]
	retainedFrom := s.nextSeq
	if len(pending) > 0 {
		retainedFrom = pending[0].Seq
	}
	gap := retainedFrom > s.acked+1
	if len(pending) == 0 && !gap {
		return nil, false
	}
	for {
		batch := &powerhistory.Batch{
			Type:         powerhistory.BatchType,
			StreamID:     s.streamID,
			RetainedFrom: retainedFrom,
			Buckets:      pending,
		}
		if batch.Buckets == nil {
			batch.Buckets = []powerhistory.Bucket{}
		}
		enc, err := json.Marshal(batch)
		if err != nil {
			return nil, false
		}
		if len(enc) <= maxBytes || len(pending) <= 1 {
			return batch, true
		}
		pending = pending[:len(pending)/2]
	}
}

// writePowerFileAtomic writes data to a same-directory temp file, fsyncs it
// and durably renames it over path.
func writePowerFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { tmp.Close(); os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := durableRename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
