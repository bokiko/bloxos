package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
)

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// startWorker runs the journal worker for ph and makes the test join it on
// cleanup, so nothing else ever touches the journal concurrently.
func startWorker(t *testing.T, ph *powerHistory) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ph.cancel = cancel
	go ph.journalWorker(ctx)
	t.Cleanup(ph.cancelForTest)
}

// cancelForTest stops the worker started by startWorker and joins it.
func (ph *powerHistory) cancelForTest() {
	if ph.cancel != nil {
		ph.cancel()
		<-ph.workerDone
	}
}

// openWorkerJournal opens a journal whose lifetime is owned by a worker.
func openWorkerJournal(t *testing.T, dir string, c *jclock) *powerJournal {
	t.Helper()
	j, err := openPowerJournalWith(dir, c.now)
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	return j
}

func TestPowerHistoryWorkerJournalsAndPublishesSnapshot(t *testing.T) {
	c := &jclock{t: time.Unix(1_700_000_000, 0)}
	j := openWorkerJournal(t, t.TempDir(), c)
	ph := newPowerHistory(j)
	startWorker(t, ph)

	if _, ok := ph.nextBatch(); ok {
		t.Fatal("empty journal must offer nothing")
	}
	for i := 1; i <= 3; i++ {
		ph.enqueue(bucketEnding(c.t.UnixMilli() + int64(i)*30000))
	}
	waitFor(t, "3 durable records", func() bool {
		b, ok := ph.nextBatch()
		return ok && len(b.Buckets) == 3
	})
	b, _ := ph.nextBatch()
	if b.Type != powerhistory.BatchType || b.StreamID != j.streamID || b.RetainedFrom != 1 || b.Degraded {
		t.Fatalf("batch header: %+v", b)
	}
	if !eqSeqs(seqs(b.Buckets), []uint64{1, 2, 3}) {
		t.Fatalf("batch seqs: %v", seqs(b.Buckets))
	}

	// ACK via the read-loop entry point; worker applies it and republishes.
	raw, _ := json.Marshal(powerhistory.Ack{Type: powerhistory.AckType, StreamID: j.streamID, Through: 2})
	powerInst.Store(ph)
	t.Cleanup(func() { powerInst.Store(nil) })
	handlePowerHistoryAck(raw)
	waitFor(t, "ack applied", func() bool {
		b, ok := ph.nextBatch()
		return ok && len(b.Buckets) == 1 && b.Buckets[0].Seq == 3
	})

	// Future and foreign ACKs are ignored without disturbing the cursor.
	for _, a := range []powerhistory.Ack{
		{Type: powerhistory.AckType, StreamID: j.streamID, Through: 99},
		{Type: powerhistory.AckType, StreamID: "ffffffffffffffffffffffffffffffff", Through: 3},
	} {
		raw, _ := json.Marshal(a)
		handlePowerHistoryAck(raw)
	}
	time.Sleep(20 * time.Millisecond)
	if b, ok := ph.nextBatch(); !ok || len(b.Buckets) != 1 || b.Buckets[0].Seq != 3 {
		t.Fatalf("invalid ACKs must not move the cursor: %+v", b)
	}
}

func TestPowerHistoryAckMailboxCoalescesToNewest(t *testing.T) {
	c := &jclock{t: time.Unix(1_700_000_000, 0)}
	j := openTestJournal(t, t.TempDir(), c) // no worker: test owns it
	ph := newPowerHistory(j)
	// No worker running: offers must never block and only the newest wins.
	for i := uint64(1); i <= 1000; i++ {
		ph.offerAck(powerhistory.Ack{StreamID: j.streamID, Through: i})
	}
	ph.offerAck(powerhistory.Ack{StreamID: j.streamID, Through: 5}) // older: ignored
	ph.ackMu.Lock()
	got := ph.ackNext.Through
	ph.ackMu.Unlock()
	if got != 1000 {
		t.Fatalf("mailbox holds %d, want newest 1000", got)
	}
	select {
	case <-ph.ackSig:
	default:
		t.Fatal("signal must be pending for the worker")
	}
}

func TestPowerHistoryQueueOverflowDropsWithGapNotBlock(t *testing.T) {
	c := &jclock{t: time.Unix(1_700_000_000, 0)}
	j := openWorkerJournal(t, t.TempDir(), c)
	ph := newPowerHistory(j)
	// Worker not running: fill the queue and overflow it. enqueue must
	// return immediately every time.
	done := make(chan struct{})
	go func() {
		// Ascending ends, all in the recent past (inside retention and not
		// in the future-skew window).
		for i := 0; i < powerQueueDepth+5; i++ {
			ph.enqueue(bucketEnding(c.t.UnixMilli() - int64(powerQueueDepth+20-i)*30000))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("enqueue blocked on a stalled journal worker")
	}
	if !ph.degraded.Load() || !ph.dropped.Load() {
		t.Fatal("overflow must mark degraded and a pending gap")
	}
	startWorker(t, ph)
	waitFor(t, "queue drained", func() bool {
		b, ok := ph.nextBatch()
		return ok && len(b.Buckets) == 32
	})
	b, _ := ph.nextBatch()
	if b.Buckets[0].GapBefore {
		t.Error("buckets queued before the drop must not be flagged")
	}
	if b.Degraded {
		t.Error("degraded must clear once appends succeed again")
	}
	// The bucket enqueued after the loss is the one that declares it.
	ph.enqueue(bucketEnding(c.t.UnixMilli() - 5*30000))
	waitFor(t, "post-drop bucket", func() bool {
		return len(ph.snap.Load().records) == powerQueueDepth+1
	})
	recs := ph.snap.Load().records
	if !recs[len(recs)-1].GapBefore {
		t.Error("first bucket after a queue drop must declare GapBefore")
	}
	for _, r := range recs[:len(recs)-1] {
		if r.GapBefore {
			t.Errorf("seq %d wrongly flagged", r.Seq)
		}
	}
}

func TestPowerHistoryNextBatchNeverTouchesDisk(t *testing.T) {
	c := &jclock{t: time.Unix(1_700_000_000, 0)}
	dir := t.TempDir()
	j := openWorkerJournal(t, dir, c)
	ph := newPowerHistory(j)
	ctx, cancel := context.WithCancel(context.Background())
	go ph.journalWorker(ctx)
	ph.enqueue(bucketEnding(c.t.UnixMilli() + 30000))
	waitFor(t, "record", func() bool { _, ok := ph.nextBatch(); return ok })
	// Stop the worker (it closes the journal) and make the directory
	// disappear; batches still come from the published snapshot.
	cancel()
	<-ph.workerDone
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for i := 0; i < 1000; i++ {
		if _, ok := ph.nextBatch(); !ok {
			t.Fatal("snapshot lost")
		}
	}
	if time.Since(start) > time.Second {
		t.Fatal("nextBatch is not cheap")
	}
}

func TestPowerHistorySendThrottleIsMonotonicAndIndependentOfSnapshot(t *testing.T) {
	c := &jclock{t: time.Unix(1_700_000_000, 0)}
	j := openTestJournal(t, t.TempDir(), c)
	ph := newPowerHistory(j)
	base := time.Now()
	if !ph.allowSend(base) {
		t.Fatal("first send must be allowed")
	}
	// Reconnect + manual refresh inside the window: refused.
	for _, d := range []time.Duration{0, time.Second, 10 * time.Second, powerSendMinInterval - time.Millisecond} {
		if ph.allowSend(base.Add(d)) {
			t.Fatalf("send allowed %s after the previous one", d)
		}
	}
	if !ph.allowSend(base.Add(powerSendMinInterval)) {
		t.Fatal("send must be allowed once the interval has elapsed")
	}
	if ph.allowSend(base.Add(powerSendMinInterval + time.Second)) {
		t.Fatal("the slot just claimed must throttle again")
	}
	// Throttle does not depend on journal state: nothing pending here, and
	// nextBatch is untouched by the throttle.
	if _, ok := ph.nextBatch(); ok {
		t.Fatal("nextBatch must stay pure")
	}
}
