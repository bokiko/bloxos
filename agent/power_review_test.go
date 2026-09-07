package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
)

func TestReviewEmptyPowerHistoryDoesNotConsumeSendSlot(t *testing.T) {
	j, err := openPowerJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer j.close()
	ph := newPowerHistory(j)
	previous := powerInst.Swap(ph)
	defer powerInst.Store(previous)
	// An empty history never reaches the socket; a refresh here must not
	// postpone the first real completed window.
	sendPowerHistory(nil, &sync.Mutex{})
	if !ph.allowSend(time.Now()) {
		t.Fatal("empty refresh consumed the upload slot")
	}
}

func TestReviewPowerConstantFractionMeetsWireContract(t *testing.T) {
	for _, watts := range []float64{0.1, 33.83, 37.74, 237.13} {
		var acc statsAcc
		for i := 0; i < 30; i++ {
			acc.add(watts)
		}
		st := acc.stats()
		if *st.MeanWatts > *st.PeakWatts {
			t.Fatalf("constant %g produced mean %.17g > peak %.17g", watts, *st.MeanWatts, *st.PeakWatts)
		}
	}
}

// Run under -race: filesystem ownership, snapshot reads and ACK coalescing must
// remain independent even while retention prunes previously published records.
func TestReviewPowerConcurrentSnapshotAndRetention(t *testing.T) {
	j, err := openPowerJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	j.maxRecords, j.segRecords = 4, 2
	targetSeq := j.nextSeq + 20
	ph := newPowerHistory(j)
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	workers.Add(1)
	go func() { defer workers.Done(); ph.journalWorker(ctx) }()
	defer func() { cancel(); workers.Wait() }()
	readerCtx, stopReader := context.WithCancel(ctx)
	var readers sync.WaitGroup
	for i := 0; i < 3; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for readerCtx.Err() == nil {
				if batch, ok := ph.nextBatch(); ok && len(batch.Buckets) > 0 {
					ph.offerAck(powerhistory.Ack{Type: powerhistory.AckType, StreamID: batch.StreamID,
						Through: batch.Buckets[len(batch.Buckets)-1].Seq})
				}
				time.Sleep(time.Millisecond)
			}
		}()
	}
	defer func() { stopReader(); readers.Wait() }()
	for i := 0; i < 20; i++ {
		end := time.Now()
		watts := 100.0 + float64(i)
		st := powerhistory.Stats{MeanWatts: &watts, PeakWatts: &watts, Samples: 30}
		ph.enqueue(powerhistory.Bucket{StartUnixMS: end.Add(-30 * time.Second).UnixMilli(), EndUnixMS: end.UnixMilli(),
			ExpectedSamples: 30, GPUs: []powerhistory.Sensor{{ID: "GPU-review", Stats: st}}, GPUTotal: &st})
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if snap := ph.snap.Load(); snap != nil && snap.nextSeq >= targetSeq {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("journal worker did not persist the queued windows")
}
