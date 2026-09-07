package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
)

// jclock is a settable wall clock for the journal's age bound.
type jclock struct{ t time.Time }

func (c *jclock) now() time.Time { return c.t }

func openTestJournal(t *testing.T, dir string, c *jclock) *powerJournal {
	t.Helper()
	j, err := openPowerJournalWith(dir, c.now)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	t.Cleanup(j.close)
	return j
}

func bucketEnding(endMS int64) powerhistory.Bucket {
	m, p := 100.0, 120.0
	return powerhistory.Bucket{
		StartUnixMS: endMS - 30000, EndUnixMS: endMS, ExpectedSamples: 30,
		GPUs: []powerhistory.Sensor{{ID: "GPU-a", Stats: powerhistory.Stats{MeanWatts: &m, PeakWatts: &p, Samples: 30}}},
	}
}

func appendN(t *testing.T, j *powerJournal, c *jclock, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		c.t = c.t.Add(30 * time.Second)
		if err := j.append(bucketEnding(c.t.UnixMilli())); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
}

func seqs(bs []powerhistory.Bucket) []uint64 {
	out := make([]uint64, len(bs))
	for i, b := range bs {
		out[i] = b.Seq
	}
	return out
}

func eqSeqs(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func segFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "seg-") {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestJournalIdentityPersistsAndSequenceNeverReused(t *testing.T) {
	dir := t.TempDir()
	c := &jclock{t: time.Unix(1_700_000_000, 0)}
	j := openTestJournal(t, dir, c)
	id := j.streamID
	if !isPowerStreamID(id) || j.nextSeq != 1 || j.reserved != 0 {
		t.Fatalf("fresh journal: id=%q next=%d reserved=%d", id, j.nextSeq, j.reserved)
	}
	appendN(t, j, c, 20)
	if j.records[0].Seq != 1 || j.reserved < 20 {
		t.Fatalf("new stream must start at seq 1 and reserve ahead: first=%d reserved=%d", j.records[0].Seq, j.reserved)
	}
	reserved := j.reserved
	j.close()

	// Restart: identity kept, records replayable, next seq skips past the
	// durable reservation so a torn tail can never reuse a committed seq.
	j2 := openTestJournal(t, dir, c)
	if j2.streamID != id {
		t.Fatalf("stream id changed across restart: %s -> %s", id, j2.streamID)
	}
	if got := seqs(j2.records); len(got) != 20 || got[0] != 1 || got[19] != 20 {
		t.Fatalf("records after restart: %v", got)
	}
	if j2.reserved != reserved || j2.nextSeq != reserved+1 {
		t.Fatalf("nextSeq must resume at reservation+1 (%d), got %d (reserved %d)", reserved+1, j2.nextSeq, j2.reserved)
	}
	skip := reserved + 1 // first seq after the restart hole

	// Simulate the hub's contiguous ACK progression across the hole.
	snap := j2.snapshot()
	b, ok := snap.pendingBatch(32, 1<<20)
	if !ok || b.RetainedFrom != 1 || !eqSeqs(seqs(b.Buckets), seqs(j2.records)) {
		t.Fatalf("first replay batch: ok=%v %+v", ok, b)
	}
	if adv, err := j2.ack(id, 20); err != nil || !adv {
		t.Fatalf("ack 20: adv=%v err=%v", adv, err)
	}
	// Nothing pending yet: header-only batch declares 21..reserved lost.
	b, ok = j2.snapshot().pendingBatch(32, 1<<20)
	if !ok || len(b.Buckets) != 0 || b.RetainedFrom != skip {
		t.Fatalf("gap declaration batch: ok=%v %+v", ok, b)
	}
	if adv, err := j2.ack(id, skip-1); err != nil || !adv {
		t.Fatalf("ack across declared gap: adv=%v err=%v", adv, err)
	}
	if _, ok := j2.snapshot().pendingBatch(32, 1<<20); ok {
		t.Fatal("nothing should be pending after the gap is acknowledged")
	}
	appendN(t, j2, c, 1)
	b, ok = j2.snapshot().pendingBatch(32, 1<<20)
	if !ok || b.RetainedFrom != skip || len(b.Buckets) != 1 || b.Buckets[0].Seq != skip {
		t.Fatalf("post-gap batch: %+v", b)
	}
	// Acknowledged records stay on disk (24 h retention) — the hole is not
	// a reason to drop them.
	if len(j2.records) != 21 {
		t.Fatalf("acked records must remain retained locally, have %d", len(j2.records))
	}
}

func TestJournalBatchIsContiguousRunAndRetainedFromIsOldestPending(t *testing.T) {
	dir := t.TempDir()
	c := &jclock{t: time.Unix(1_700_000_000, 0)}
	j := openTestJournal(t, dir, c)
	appendN(t, j, c, 5)
	j.close()
	j = openTestJournal(t, dir, c) // hole 6..reserved
	skip := j.nextSeq
	appendN(t, j, c, 3) // skip, skip+1, skip+2
	if _, err := j.ack(j.streamID, 2); err != nil {
		t.Fatal(err)
	}
	b, ok := j.snapshot().pendingBatch(32, 1<<20)
	if !ok || b.RetainedFrom != 3 || !eqSeqs(seqs(b.Buckets), []uint64{3, 4, 5}) {
		t.Fatalf("batch must stop at the hole: %+v", b)
	}
	if _, err := j.ack(j.streamID, 5); err != nil {
		t.Fatal(err)
	}
	b, _ = j.snapshot().pendingBatch(32, 1<<20)
	if b.RetainedFrom != skip || !eqSeqs(seqs(b.Buckets), []uint64{skip, skip + 1, skip + 2}) {
		t.Fatalf("after ack the next run is offered with the gap declared: %+v", b)
	}
	// Size bound halves the run rather than exceeding the frame cap.
	small, _ := j.snapshot().pendingBatch(32, 400)
	if len(small.Buckets) >= 3 {
		t.Fatalf("byte cap not applied: %d buckets", len(small.Buckets))
	}
}

func TestJournalAckValidation(t *testing.T) {
	dir := t.TempDir()
	c := &jclock{t: time.Unix(1_700_000_000, 0)}
	j := openTestJournal(t, dir, c)
	appendN(t, j, c, 3)
	if _, err := j.ack("00000000000000000000000000000000", 1); err == nil {
		t.Error("foreign stream ACK must be rejected")
	}
	if _, err := j.ack(j.streamID, 3); err != nil {
		t.Errorf("ack of last durable seq must succeed: %v", err)
	}
	if _, err := j.ack(j.streamID, 4); err == nil {
		t.Error("ACK beyond last durable seq must be rejected")
	}
	if adv, err := j.ack(j.streamID, 2); err != nil || adv {
		t.Errorf("replayed/older ACK must be a no-op: adv=%v err=%v", adv, err)
	}
	j.close()
	j2 := openTestJournal(t, dir, c)
	if j2.acked != 3 {
		t.Errorf("ACK cursor must persist across restart, got %d", j2.acked)
	}
	// After a restart the reservation hole (4..reserved) is declared by a
	// header-only batch so the hub can ACK across it; no buckets travel.
	b, ok := j2.snapshot().pendingBatch(32, 1<<20)
	if !ok || len(b.Buckets) != 0 || b.RetainedFrom != j2.nextSeq {
		t.Errorf("restart must declare the reservation hole: ok=%v %+v", ok, b)
	}
}

func TestJournalTornTailIsTruncatedAndAppendContinues(t *testing.T) {
	dir := t.TempDir()
	c := &jclock{t: time.Unix(1_700_000_000, 0)}
	j := openTestJournal(t, dir, c)
	appendN(t, j, c, 5)
	j.close()
	files := segFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("want one segment, got %v", files)
	}
	p := filepath.Join(dir, files[0])
	data, _ := os.ReadFile(p)
	if err := os.WriteFile(p, data[:len(data)-7], 0o644); err != nil {
		t.Fatal(err)
	}
	j2 := openTestJournal(t, dir, c)
	if got := seqs(j2.records); !eqSeqs(got, []uint64{1, 2, 3, 4}) {
		t.Fatalf("torn record must be dropped: %v", got)
	}
	if st, _ := os.Stat(p); st.Size() != int64(len(data)-len(data[strings.LastIndex(string(data[:len(data)-1]), "\n")+1:])) {
		t.Errorf("file must be truncated to the last good line, size=%d", st.Size())
	}
	skip := j2.reserved + 1
	appendN(t, j2, c, 1)
	if got := j2.records[len(j2.records)-1].Seq; got != skip || got <= 5 {
		t.Errorf("post-recovery seq must come from the reservation (%d), got %d", skip, got)
	}
	j2.close()
	j3 := openTestJournal(t, dir, c)
	if got := seqs(j3.records); !eqSeqs(got, []uint64{1, 2, 3, 4, skip}) {
		t.Fatalf("third open: %v", got)
	}
}

func TestJournalCorruptMiddleSegmentDropsLaterSegments(t *testing.T) {
	dir := t.TempDir()
	c := &jclock{t: time.Unix(1_700_000_000, 0)}
	j := openTestJournal(t, dir, c)
	j.segRecords = 2
	appendN(t, j, c, 6) // segs: [1,2] [3,4] [5,6]
	j.close()
	files := segFiles(t, dir)
	if len(files) != 3 {
		t.Fatalf("want 3 segments, got %v", files)
	}
	if err := os.WriteFile(filepath.Join(dir, files[1]), []byte("{garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	j2 := openTestJournal(t, dir, c)
	j2.segRecords = 2
	if got := seqs(j2.records); !eqSeqs(got, []uint64{1, 2}) {
		t.Fatalf("records after corrupt middle segment: %v", got)
	}
	if left := segFiles(t, dir); len(left) != 1 {
		t.Fatalf("later segments must be removed, left %v", left)
	}
}

func TestJournalMissingStateRotatesIdentity(t *testing.T) {
	dir := t.TempDir()
	c := &jclock{t: time.Unix(1_700_000_000, 0)}
	j := openTestJournal(t, dir, c)
	id := j.streamID
	appendN(t, j, c, 3)
	j.close()
	if err := os.Remove(filepath.Join(dir, "state-"+id+".json")); err != nil {
		t.Fatal(err)
	}
	j2 := openTestJournal(t, dir, c)
	if j2.streamID == id {
		t.Fatal("identity without its state file must rotate (fail closed)")
	}
	if len(j2.records) != 0 || j2.nextSeq != 1 {
		t.Fatalf("rotated stream must start empty: %d records next=%d", len(j2.records), j2.nextSeq)
	}
	for _, f := range segFiles(t, dir) {
		if strings.Contains(f, id) {
			t.Errorf("orphan segment of old stream left behind: %s", f)
		}
	}
}

func TestJournalCountBoundCompactsSegments(t *testing.T) {
	dir := t.TempDir()
	c := &jclock{t: time.Unix(1_700_000_000, 0)}
	j := openTestJournal(t, dir, c)
	j.maxRecords, j.segRecords = 10, 4
	appendN(t, j, c, 20)
	// Strict RAM bound; on disk, fully-dropped segments are deleted and the
	// boundary segment is compacted to exactly the retained records.
	if got := seqs(j.records); len(got) != 10 || got[0] != 11 {
		t.Fatalf("retained after count bound: %v", got)
	}
	files := segFiles(t, dir)
	if len(files) != 3 { // [9-12]→[11,12], [13-16], [17-20]
		t.Fatalf("segments on disk: %v", files)
	}
	if n := segLines(t, filepath.Join(dir, files[0])); n != 2 {
		t.Fatalf("boundary segment must be compacted to 2 lines, has %d", n)
	}
	b, _ := j.snapshot().pendingBatch(32, 1<<20)
	if b.RetainedFrom != 11 {
		t.Errorf("retention gap must be declared: RetainedFrom=%d", b.RetainedFrom)
	}
	j.close()
	j2 := openTestJournal(t, dir, c)
	if got := seqs(j2.records); len(got) != 10 || got[0] != 11 {
		t.Fatalf("after reopen: %v", got)
	}
}

func TestJournalByteBoundIsStrict(t *testing.T) {
	dir := t.TempDir()
	c := &jclock{t: time.Unix(1_700_000_000, 0)}
	j := openTestJournal(t, dir, c)
	line, _ := json.Marshal(bucketEnding(0))
	j.segRecords = 2
	j.maxBytes = int64(len(line)+20) * 5
	for i := 0; i < 40; i++ {
		appendN(t, j, c, 1)
		var total int64
		for _, f := range segFiles(t, dir) {
			st, _ := os.Stat(filepath.Join(dir, f))
			total += st.Size()
		}
		if total > j.maxBytes {
			t.Fatalf("after %d appends disk holds %d > cap %d", i+1, total, j.maxBytes)
		}
	}
}

func TestJournalAgeBoundAcrossAllRecordsAndOnDisk(t *testing.T) {
	dir := t.TempDir()
	c := &jclock{t: time.Unix(1_700_000_000, 0)}
	j := openTestJournal(t, dir, c)
	j.segRecords = 3
	appendN(t, j, c, 6) // 6 records over 3 min
	// Clock regression: a record whose end time is far in the past lands in
	// the interior of the sequence.
	old := bucketEnding(c.t.Add(-48 * time.Hour).UnixMilli())
	if err := j.append(old); err != nil {
		t.Fatal(err)
	}
	appendN(t, j, c, 2)
	for _, r := range j.records {
		if r.Seq == 7 {
			t.Fatal("expired interior record must not be retained in RAM")
		}
	}
	b, _ := j.snapshot().pendingBatch(32, 1<<20)
	for _, r := range b.Buckets {
		if r.Seq == 7 {
			t.Fatal("expired interior record must not be uploaded")
		}
	}
	// Advance past retention for the first two segments: they must be gone
	// from disk even though the last one is still live.
	c.t = c.t.Add(24*time.Hour + time.Minute)
	appendN(t, j, c, 1)
	if got := seqs(j.records); len(got) != 1 {
		t.Fatalf("only the fresh record may remain: %v", got)
	}
	if files := segFiles(t, dir); len(files) != 1 {
		t.Fatalf("expired segments must be deleted: %v", files)
	}
	// Retention gap is explicit.
	b, _ = j.snapshot().pendingBatch(32, 1<<20)
	if b.RetainedFrom != 10 {
		t.Errorf("RetainedFrom=%d", b.RetainedFrom)
	}
}

func TestJournalDiskFailureIsRetriedNotPermanent(t *testing.T) {
	dir := t.TempDir()
	c := &jclock{t: time.Unix(1_700_000_000, 0)}
	j := openTestJournal(t, dir, c)
	appendN(t, j, c, 2)
	// Storage vanishes: close the handle, make the live segment "full" so
	// the next append must create a new segment, and point the directory at
	// a path that does not exist.
	real := j.dir
	j.close()
	j.segRecords = 2
	j.dir = filepath.Join(dir, "gone")
	for i := 0; i < 3; i++ {
		if err := j.append(bucketEnding(1)); err == nil {
			t.Fatal("append must fail while the journal directory is unavailable")
		}
	}
	if j.nextSeq != 3 {
		t.Fatalf("a failed append must not consume a sequence, nextSeq=%d", j.nextSeq)
	}
	if len(j.segs) != 1 {
		t.Fatalf("failed opens must not register phantom segments: %d", len(j.segs))
	}
	// Storage returns: the next append creates the new segment and continues.
	j.dir = real
	appendN(t, j, c, 1)
	if got := seqs(j.records); !eqSeqs(got, []uint64{1, 2, 3}) {
		t.Fatalf("after recovery: %v", got)
	}
	j.close()
	j2 := openTestJournal(t, dir, c)
	if got := seqs(j2.records); !eqSeqs(got, []uint64{1, 2, 3}) {
		t.Fatalf("durable after recovery: %v", got)
	}
}

func TestJournalRefusesOversizedRecordAndUnenforceableRetention(t *testing.T) {
	dir := t.TempDir()
	c := &jclock{t: time.Unix(1_700_000_000, 0)}
	j := openTestJournal(t, dir, c)
	big := bucketEnding(1)
	big.GPUs[0].ID = strings.Repeat("x", powerJournalMaxLine)
	if err := j.append(big); !errors.Is(err, errPowerLineTooLong) {
		t.Fatalf("oversized line must be refused, got %v", err)
	}
	if j.nextSeq != 1 {
		t.Fatal("refused record must not consume a sequence")
	}

	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions; cannot force an unlink failure")
	}
	j.segRecords, j.maxRecords = 2, 2
	appendN(t, j, c, 2)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	// Next append must roll to a new segment (needs create → fails) or, if
	// it can proceed, retention must be enforced; either way no growth
	// beyond the caps and no permanent wedge.
	err := j.append(bucketEnding(c.t.UnixMilli()))
	if err == nil && len(segFiles(t, dir)) > 2 {
		t.Fatal("journal grew past its bound while retention could not be enforced")
	}
	os.Chmod(dir, 0o755)
	appendN(t, j, c, 2)
	if len(j.records) > 2 {
		t.Fatalf("RAM bound violated: %d", len(j.records))
	}
}

func TestJournalRecoveryIsBounded(t *testing.T) {
	dir := t.TempDir()
	c := &jclock{t: time.Unix(1_700_000_000, 0)}
	j := openTestJournal(t, dir, c)
	j.segRecords = 2
	appendN(t, j, c, 4)
	j.close()
	files := segFiles(t, dir)
	// Inflate the first segment far past its record cap.
	p := filepath.Join(dir, files[0])
	data, _ := os.ReadFile(p)
	var blob []byte
	for i := 0; i < 50; i++ {
		blob = append(blob, data...)
	}
	if err := os.WriteFile(p, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	j2, err := openPowerJournalWith(dir, c.now)
	if err != nil {
		t.Fatal(err)
	}
	defer j2.close()
	// Duplicate seqs are not ascending → treated as corrupt after the valid
	// prefix; later segments dropped; nothing unbounded loaded.
	if len(j2.records) > powerJournalSegRec {
		t.Fatalf("recovery loaded %d records from an oversized segment", len(j2.records))
	}
	if st, _ := os.Stat(p); st.Size() >= int64(len(blob)) {
		t.Error("oversized segment must be truncated back to its valid prefix")
	}
}

func segLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Count(string(data), "\n")
}

func TestJournalExpiredInteriorRecordIsCompactedOnDisk(t *testing.T) {
	dir := t.TempDir()
	c := &jclock{t: time.Unix(1_700_000_000, 0)}
	j := openTestJournal(t, dir, c)
	j.segRecords = 3
	appendN(t, j, c, 3) // seg A: 1,2,3
	appendN(t, j, c, 1) // seg B: 4
	old := bucketEnding(c.t.Add(-48 * time.Hour).UnixMilli())
	if err := j.append(old); err != nil { // seg B: 5 (expired interior) → compacted out at once
		t.Fatal(err)
	}
	appendN(t, j, c, 2) // seg B: 6, 7 (compaction freed the slot)
	files := segFiles(t, dir)
	if len(files) != 2 {
		t.Fatalf("segments: %v", files)
	}
	if n := segLines(t, filepath.Join(dir, files[1])); n != 3 {
		t.Fatalf("interior expired record must be compacted away on disk; seg B has %d lines", n)
	}
	if got := seqs(j.records); !eqSeqs(got, []uint64{1, 2, 3, 4, 6, 7}) {
		t.Fatalf("RAM: %v", got)
	}
	j.close()
	j2 := openTestJournal(t, dir, c)
	if got := seqs(j2.records); !eqSeqs(got, []uint64{1, 2, 3, 4, 6, 7}) {
		t.Fatalf("after reopen: %v", got)
	}
	// Horizon crossing compacts the LIVE segment too: 1..6 expire, 7 stays,
	// and the next append lands in the compacted live segment.
	c.t = c.t.Add(24*time.Hour - 30*time.Second)
	appendN(t, j2, c, 1) // seq resumes past the reservation after the reopen
	got := seqs(j2.records)
	if len(got) == 0 || got[0] != 7 || got[len(got)-1] != j2.nextSeq-1 {
		t.Fatalf("horizon crossing: %v (next %d)", got, j2.nextSeq)
	}
	for _, r := range j2.records {
		if r.EndUnixMS < c.t.Add(-24*time.Hour).UnixMilli() {
			t.Fatalf("expired record retained: seq %d", r.Seq)
		}
	}
	left := segFiles(t, dir)
	if len(left) != 1 {
		t.Fatalf("seg A must be deleted and seg B compacted+live: %v", left)
	}
	if n := segLines(t, filepath.Join(dir, left[0])); n != len(j2.records) {
		t.Fatalf("live segment lines %d != retained records %d", n, len(j2.records))
	}
}

func TestJournalIdleAllExpiredIsPurgedByRetentionTick(t *testing.T) {
	dir := t.TempDir()
	c := &jclock{t: time.Unix(1_700_000_000, 0)}
	j := openTestJournal(t, dir, c)
	j.segRecords = 2
	appendN(t, j, c, 5) // 3 segments incl. live
	if _, err := j.ack(j.streamID, 5); err != nil {
		t.Fatal(err)
	}
	// Sensors stop, everything is acknowledged, a day passes.
	c.t = c.t.Add(25 * time.Hour)
	if err := j.enforce(); err != nil {
		t.Fatal(err)
	}
	if len(j.records) != 0 || len(segFiles(t, dir)) != 0 || len(j.segs) != 0 {
		t.Fatalf("idle expiry must purge every segment including the live one: records=%d files=%v segs=%d",
			len(j.records), segFiles(t, dir), len(j.segs))
	}
	// And the journal keeps working afterwards.
	appendN(t, j, c, 1)
	if got := seqs(j.records); len(got) != 1 || got[0] != 6 {
		t.Fatalf("append after purge: %v", got)
	}
}

func TestPowerHistoryWorkerTickEnforcesRetentionWithoutSamples(t *testing.T) {
	prev := powerRetentionTick
	powerRetentionTick = 10 * time.Millisecond
	t.Cleanup(func() { powerRetentionTick = prev })
	dir := t.TempDir()
	c := &jclock{t: time.Unix(1_700_000_000, 0)}
	j := openWorkerJournal(t, dir, c)
	appendN(t, j, c, 3)
	var mu sync.Mutex
	now := c.t
	j.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	ph := newPowerHistory(j)
	startWorker(t, ph)
	mu.Lock()
	now = now.Add(25 * time.Hour)
	mu.Unlock()
	waitFor(t, "retention tick purge", func() bool { return len(ph.snap.Load().records) == 0 })
	// Join the worker before inspecting the directory.
	ph.cancelForTest()
	if files := segFiles(t, dir); len(files) != 0 {
		t.Fatalf("segments left on disk after idle expiry: %v", files)
	}
}

func TestJournalFutureStampedBucketIsDroppedNotReplayed(t *testing.T) {
	dir := t.TempDir()
	c := &jclock{t: time.Unix(1_700_000_000, 0)}
	j := openTestJournal(t, dir, c)
	// Clock jumps 48 h ahead; a bucket is stamped there.
	c.t = c.t.Add(48 * time.Hour)
	appendN(t, j, c, 1) // seq 1, end ≈ now+48h (by the wrong clock it is "now")
	// Clock corrects; a normal bucket follows.
	c.t = c.t.Add(-48 * time.Hour)
	appendN(t, j, c, 1) // seq 2
	b, ok := j.snapshot().pendingBatch(32, 1<<20)
	if !ok || b.RetainedFrom != 2 || !eqSeqs(seqs(b.Buckets), []uint64{2}) {
		t.Fatalf("future bucket must be declared lost and seq 2 offered: ok=%v %+v", ok, b)
	}
	if got := seqs(j.records); !eqSeqs(got, []uint64{2}) {
		t.Fatalf("future bucket must be purged from RAM: %v", got)
	}
	if n := segLines(t, filepath.Join(dir, segFiles(t, dir)[0])); n != 1 {
		t.Fatalf("future bucket must be compacted off disk, %d lines", n)
	}
	// Small allowed skew is not touched.
	c.t = c.t.Add(-10 * time.Minute)
	if got, _ := j.snapshot().pendingBatch(32, 1<<20); len(got.Buckets) != 1 {
		t.Fatalf("a bucket within the allowed skew must remain replayable: %+v", got)
	}
}
