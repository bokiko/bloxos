package main

// Concurrency regression for the power-history commit path (issue #130
// finding 2): an independent writer holding the database write lock must
// make commitPowerBatch WAIT, not fail — and the returned ACK-through value
// must become observable only after the committing transaction completes.
// The deferred-txlock variant documents the old failure for contrast.
//
// These tests MUST use a file-backed database: modernc.org/sqlite gives
// each pooled connection its own :memory: database, which is why the other
// test helpers pin SetMaxOpenConns(1). Here the point is precisely multiple
// live connections over one file, so a real file in t.TempDir() is used.

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
	"modernc.org/sqlite"
)

// deferredPowerDSN is the pre-fix DSN: identical pragmas, default (deferred)
// transaction locking. Kept local to the contrast test; production uses
// databaseDSN.
func deferredPowerDSN(path string) string {
	return path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
}

func openFilePowerServer(t *testing.T, dsn string) *Server {
	t.Helper()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open %q: %v", dsn, err)
	}
	t.Cleanup(func() { db.Close() })
	if err := runMigrations(db); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO machines (id, hostname, status) VALUES ('m1', 'host-m1', 'online')`); err != nil {
		t.Fatalf("seed machine: %v", err)
	}
	return &Server{db: db}
}

func filePowerBatch(seq uint64) powerhistory.Batch {
	mean, peak := 100.0, 150.0
	now := time.Now()
	st := powerhistory.Stats{MeanWatts: &mean, PeakWatts: &peak, Samples: 30}
	return powerhistory.Batch{
		Type: powerhistory.BatchType, StreamID: "file-wal", RetainedFrom: seq,
		Buckets: []powerhistory.Bucket{{
			Seq: seq, StartUnixMS: now.Add(-30 * time.Second).UnixMilli(), EndUnixMS: now.UnixMilli(),
			ExpectedSamples: 30,
			GPUs:            []powerhistory.Sensor{{ID: "GPU-0", Stats: st}},
			GPUTotal:        &st,
		}},
	}
}

// holdWriteLock starts a transaction and performs a real write so it holds
// the database write lock regardless of the DSN's txlock mode (immediate or
// deferred). Caller must commit/rollback to release; a cleanup rollback
// keeps a t.Fatal from wedging the pool.
func holdWriteLock(t *testing.T, db *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin blocker: %v", err)
	}
	t.Cleanup(func() { tx.Rollback() })
	if _, err := tx.Exec(`UPDATE machines SET hostname = hostname WHERE id = 'm1'`); err != nil {
		t.Fatalf("blocker write: %v", err)
	}
	return tx
}

type commitResult struct {
	through uint64
	err     error
}

// commitAsync runs commitPowerBatch in the background and fails the test if
// it returns before the write lock is released — the ACK-through value must
// never be observable while an independent writer holds the lock.
func commitAsync(t *testing.T, s *Server, b powerhistory.Batch) chan commitResult {
	t.Helper()
	done := make(chan commitResult, 1)
	go func() {
		through, err := s.commitPowerBatch("m1", &b, time.Now())
		done <- commitResult{through, err}
	}()
	return done
}

func assertBlocked(t *testing.T, done chan commitResult) {
	t.Helper()
	select {
	case r := <-done:
		t.Fatalf("commitPowerBatch returned early while a writer held the lock (through=%d err=%v)", r.through, r.err)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestPowerHistoryCommitImmediateWaitsBehindWriter is the production-helper
// regression: with _txlock=immediate, the commit waits for the independent
// writer, succeeds after release, and a byte-identical replay adds nothing.
func TestPowerHistoryCommitImmediateWaitsBehindWriter(t *testing.T) {
	s := openFilePowerServer(t, databaseDSN(filepath.Join(t.TempDir(), "hub.db")))
	batch := filePowerBatch(1)

	blocker := holdWriteLock(t, s.db)
	done := commitAsync(t, s, batch)
	assertBlocked(t, done)

	if err := blocker.Commit(); err != nil {
		t.Fatalf("blocker commit: %v", err)
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("commitPowerBatch failed behind a writer: %v", r.err)
		}
		if r.through != 1 {
			t.Fatalf("through = %d, want 1", r.through)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("commitPowerBatch never finished after the writer released the lock")
	}

	if n := powerRowCount(t, s, "m1"); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}

	// Replay of the byte-identical batch (lost-ACK scenario): absorbed, no
	// duplicate row, same through.
	through, err := s.commitPowerBatch("m1", &batch, time.Now())
	if err != nil {
		t.Fatalf("replay commit: %v", err)
	}
	if through != 1 {
		t.Fatalf("replay through = %d, want 1", through)
	}
	if n := powerRowCount(t, s, "m1"); n != 1 {
		t.Fatalf("replay produced duplicates: rows = %d", n)
	}
}

// TestPowerHistoryCommitDeferredTxlockFailsBehindCommittingWriter documents
// the pre-fix failure for contrast: under deferred locking the batch
// transaction reads first, then its write upgrade hits the held lock and
// fails IMMEDIATELY with SQLITE_BUSY — SQLite does not invoke the busy
// handler for the read→write upgrade, so busy_timeout(5000) never gets a
// chance to wait. Nothing is persisted; the agent's next retry replays it.
func TestPowerHistoryCommitDeferredTxlockFailsBehindCommittingWriter(t *testing.T) {
	s := openFilePowerServer(t, deferredPowerDSN(filepath.Join(t.TempDir(), "hub.db")))

	blocker := holdWriteLock(t, s.db)
	start := time.Now()
	through, err := s.commitPowerBatch("m1", ptrBatch(filePowerBatch(1)), time.Now())
	if err == nil {
		t.Fatal("deferred txlock unexpectedly survived a held write lock")
	}
	// The failure must be exactly SQLITE_BUSY (base code 5), not some
	// unrelated error the test would otherwise launder.
	var sqErr *sqlite.Error
	if !errors.As(err, &sqErr) || sqErr.Code() != 5 {
		t.Fatalf("expected SQLITE_BUSY (5), got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("deferred failure should be immediate, took %s", elapsed)
	}
	if through != 0 {
		t.Fatalf("failed commit reported through=%d", through)
	}

	// Atomicity: the failed commit left no partial state.
	if n := powerRowCount(t, s, "m1"); n != 0 {
		t.Fatalf("failed deferred commit leaked rows: %d", n)
	}
	if acked, maxSeen := powerStreamStateOf(t, s, "m1", "file-wal"); acked != 0 || maxSeen != 0 {
		t.Fatalf("failed deferred commit wrote stream state: ack=%d max=%d", acked, maxSeen)
	}

	if err := blocker.Commit(); err != nil {
		t.Fatalf("blocker commit: %v", err)
	}
	// Once the writer releases, the same batch (the agent's replay) commits.
	if through, err := s.commitPowerBatch("m1", ptrBatch(filePowerBatch(1)), time.Now()); err != nil || through != 1 {
		t.Fatalf("replay after release: through=%d err=%v", through, err)
	}
}

func ptrBatch(b powerhistory.Batch) *powerhistory.Batch { return &b }
