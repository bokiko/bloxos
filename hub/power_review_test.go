package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
	"github.com/labstack/echo/v4"
)

func reviewPowerHistory(t *testing.T, s *Server, query string) powerhistory.History {
	t.Helper()
	recorder := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/api/machines/power-review/power/history"+query, nil), recorder)
	c.SetParamNames("id")
	c.SetParamValues("power-review")
	if err := s.handlePowerHistory(c); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("history status %d: %s", recorder.Code, recorder.Body.String())
	}
	var history powerhistory.History
	if err := json.Unmarshal(recorder.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	return history
}

func TestReviewPowerEmptyDeltaPreservesGapMetadataAndCursor(t *testing.T) {
	_, s := setupTestServer(t)
	s.seedTestMachine(t, "power-review")
	for _, seq := range []uint64{1, 3} {
		batch := powerhistory.Batch{Type: powerhistory.BatchType, StreamID: "review", RetainedFrom: seq,
			Buckets: []powerhistory.Bucket{reviewPowerBucket(seq)}}
		if _, err := s.commitPowerBatch("power-review", &batch, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	initial := reviewPowerHistory(t, s, "")
	if len(initial.Gaps) != 1 {
		t.Fatalf("initial gap missing: %+v", initial)
	}
	delta := reviewPowerHistory(t, s, fmt.Sprintf("?after=%d", initial.Cursor))
	if len(delta.Points) != 0 || len(delta.Gaps) != 1 {
		t.Fatalf("empty delta discarded current gap metadata: %+v", delta)
	}
	if _, err := s.prunePowerHistory(time.Now().Add(25 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	afterPrune := reviewPowerHistory(t, s, fmt.Sprintf("?after=%d", initial.Cursor))
	if afterPrune.Cursor < initial.Cursor {
		t.Fatalf("retention reset cursor: %d -> %d", initial.Cursor, afterPrune.Cursor)
	}
}

func TestReviewPowerDefaultCoversFullDay(t *testing.T) {
	if powerHistoryDefaultMaxRecords < powerhistory.RetentionSeconds/powerhistory.WindowSeconds {
		t.Fatal("default history cap cannot hold one normal day of 30-second windows")
	}
}

func TestReviewPowerInitialHistoryIncludesAllRestartStreams(t *testing.T) {
	_, s := setupTestServer(t)
	s.seedTestMachine(t, "power-review")
	for i := 0; i < 12; i++ {
		batch := powerhistory.Batch{Type: powerhistory.BatchType, StreamID: fmt.Sprintf("restart-%02d", i),
			RetainedFrom: 1, Buckets: []powerhistory.Bucket{reviewPowerBucket(1)}}
		if _, err := s.commitPowerBatch("power-review", &batch, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if history := reviewPowerHistory(t, s, ""); len(history.Points) != 12 {
		t.Fatalf("restart streams hidden: %d points", len(history.Points))
	}
}

func TestReviewPowerGapWithoutSurvivingRecordsIsVisible(t *testing.T) {
	_, s := setupTestServer(t)
	s.seedTestMachine(t, "power-review")
	batch := powerhistory.Batch{Type: powerhistory.BatchType, StreamID: "review", RetainedFrom: 5, Buckets: []powerhistory.Bucket{}}
	if through, err := s.commitPowerBatch("power-review", &batch, time.Now()); err != nil || through != 4 {
		t.Fatalf("gap: %d %v", through, err)
	}
	history := reviewPowerHistory(t, s, "")
	if len(history.Points) != 0 || len(history.Gaps) != 1 || history.Gaps[0].Through != 4 {
		t.Fatalf("loss hidden without surviving points: %+v", history)
	}
}

func TestReviewPowerReplayNormalizesStoredJSONButRejectsChangedData(t *testing.T) {
	_, s := setupTestServer(t)
	s.seedTestMachine(t, "power-review")
	batch := powerhistory.Batch{Type: powerhistory.BatchType, StreamID: "review", RetainedFrom: 1,
		Buckets: []powerhistory.Bucket{reviewPowerBucket(1)}}
	if _, err := s.commitPowerBatch("power-review", &batch, time.Now()); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(batch.Buckets[0])
	if err != nil {
		t.Fatal(err)
	}
	var reordered map[string]any
	if err := json.Unmarshal(raw, &reordered); err != nil {
		t.Fatal(err)
	}
	reordered["gap_before"] = false // explicit default versus an omitted optional field
	reordered["future_optional"] = nil
	raw, err = json.Marshal(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE power_history_records SET payload = ? WHERE machine_id = ?`, string(raw), "power-review"); err != nil {
		t.Fatal(err)
	}
	if through, err := s.commitPowerBatch("power-review", &batch, time.Now()); err != nil || through != 1 {
		t.Fatalf("equivalent replay rejected: %d %v", through, err)
	}
	changed := 101.0
	batch.Buckets[0].GPUs[0].MeanWatts = &changed
	if _, err := s.commitPowerBatch("power-review", &batch, time.Now()); !errors.Is(err, errPowerConflict) {
		t.Fatalf("changed data must still conflict: %v", err)
	}
}

func TestReviewPowerClockRejectionIsVisibleAndClearsOnRecovery(t *testing.T) {
	e, s := setupTestServer(t)
	server := httptest.NewServer(e)
	defer server.Close()
	conn := s.connectEnrolledAgent(t, server, "power-review")
	defer conn.Close()
	readAISessionsConfig(t, conn)
	bucket := reviewPowerBucket(1)
	bucket.StartUnixMS += (48 * time.Hour).Milliseconds()
	bucket.EndUnixMS += (48 * time.Hour).Milliseconds()
	batch := powerhistory.Batch{Type: powerhistory.BatchType, StreamID: "review", RetainedFrom: 1,
		Buckets: []powerhistory.Bucket{bucket}}
	writeFrame(t, conn, batch)
	s.sendSentinelMetrics(t, conn, "power-review", "after-clock-rejection")
	history := reviewPowerHistory(t, s, "")
	if history.Problem != "clock_skew" || !history.Degraded || len(history.Points) != 0 {
		t.Fatalf("missing rejection diagnostic: %+v", history)
	}
	if s.powerHistoryProblem("another-machine") != "" {
		t.Fatal("diagnostic escaped machine binding")
	}
	batch.Buckets[0] = reviewPowerBucket(1)
	writeFrame(t, conn, batch)
	expectPowerAck(t, conn, "review", 1)
	history = reviewPowerHistory(t, s, "")
	if history.Problem != "" || history.Degraded || len(history.Points) != 1 {
		t.Fatalf("diagnostic did not recover: %+v", history)
	}
}

// Independent integration-review regressions, separate from implementation tests.
func reviewPowerBucket(seq uint64) powerhistory.Bucket {
	mean, peak := 100.0, 150.0
	now := time.Now().Add(-time.Minute)
	st := powerhistory.Stats{MeanWatts: &mean, PeakWatts: &peak, Samples: 30}
	return powerhistory.Bucket{Seq: seq, StartUnixMS: now.Add(-30 * time.Second).UnixMilli(),
		EndUnixMS: now.UnixMilli(), ExpectedSamples: 30,
		GPUs: []powerhistory.Sensor{{ID: "GPU-review", Stats: st}}, GPUTotal: &st}
}

func TestReviewPowerOutOfOrderRecovery(t *testing.T) {
	_, s := setupTestServer(t)
	s.seedTestMachine(t, "power-review")
	b := powerhistory.Batch{Type: powerhistory.BatchType, StreamID: "review", RetainedFrom: 1,
		Buckets: []powerhistory.Bucket{reviewPowerBucket(3)}}
	through, err := s.commitPowerBatch("power-review", &b, time.Now())
	if err != nil || through != 0 {
		t.Fatalf("future record: through=%d err=%v", through, err)
	}
	b.Buckets = []powerhistory.Bucket{reviewPowerBucket(1), reviewPowerBucket(2)}
	through, err = s.commitPowerBatch("power-review", &b, time.Now())
	if err != nil || through != 3 {
		t.Fatalf("fill hole: through=%d err=%v", through, err)
	}
}

func TestReviewPowerHugeRetentionGapIsBounded(t *testing.T) {
	_, s := setupTestServer(t)
	s.seedTestMachine(t, "power-review")
	batch := powerhistory.Batch{Type: powerhistory.BatchType, StreamID: "review", RetainedFrom: powerhistory.MaxSeq,
		Buckets: []powerhistory.Bucket{reviewPowerBucket(powerhistory.MaxSeq)}}
	start := time.Now()
	through, err := s.commitPowerBatch("power-review", &batch, start)
	if err != nil || through != powerhistory.MaxSeq {
		t.Fatalf("huge gap: through=%d err=%v", through, err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("gap processing must scale with stored rows, not the numeric sequence range")
	}
}

func TestReviewPowerGapUnionKeepsBothEnds(t *testing.T) {
	_, s := setupTestServer(t)
	s.seedTestMachine(t, "power-review")
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, interval := range [][2]uint64{{10, 20}, {15, 25}, {8, 9}, {26, 30}} {
		if err := declarePowerGap(tx, "power-review", "review", interval[0], interval[1]); err != nil {
			t.Fatal(err)
		}
	}
	var from, through, count int
	if err := tx.QueryRow(`SELECT MIN(from_seq), MAX(through_seq), COUNT(*) FROM power_history_gaps WHERE machine_id = ?`,
		"power-review").Scan(&from, &through, &count); err != nil {
		t.Fatal(err)
	}
	if from != 8 || through != 30 || count != 1 {
		t.Fatalf("gap union=%d..%d (%d rows)", from, through, count)
	}
}

func TestReviewPowerRejectsInconsistentStatistics(t *testing.T) {
	for name, change := range map[string]func(*powerhistory.Bucket){
		"missing mean":          func(b *powerhistory.Bucket) { b.GPUs[0].MeanWatts = nil },
		"missing peak":          func(b *powerhistory.Bucket) { b.GPUs[0].PeakWatts = nil },
		"too many samples":      func(b *powerhistory.Bucket) { b.GPUs[0].Samples = 31 },
		"mean above peak":       func(b *powerhistory.Bucket) { v := 200.0; b.GPUs[0].MeanWatts = &v },
		"total without devices": func(b *powerhistory.Bucket) { b.GPUs = nil },
		"empty duration":        func(b *powerhistory.Bucket) { b.EndUnixMS = b.StartUnixMS },
		"zero expected samples": func(b *powerhistory.Bucket) { b.ExpectedSamples = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			bucket := reviewPowerBucket(1)
			change(&bucket)
			batch := powerhistory.Batch{Type: powerhistory.BatchType, StreamID: "review", RetainedFrom: 1,
				Buckets: []powerhistory.Bucket{bucket}}
			if err := validatePowerBatch(&batch, time.Now()); err == nil {
				t.Fatal("inconsistent statistics accepted")
			}
		})
	}
}

// A changing GPU set must omit its aggregate before journaling. Keep hub
// validation strict, and prove an omitted aggregate preserves the rest of the
// batch, advances the ACK and does not block the next stable window.
func TestReviewPowerMembershipChangeBatchRecovery(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	conn := s.connectEnrolledAgent(t, server, "power-review")
	defer conn.Close()
	readAISessionsConfig(t, conn)

	changed := reviewPowerBucket(2)
	partial := changed.GPUs[0]
	partial.ID = "GPU-added"
	partial.Samples = 15
	changed.GPUs = append(changed.GPUs, partial)
	cpu := changed.GPUs[0].Stats
	changed.CPU = &cpu
	batch := powerhistory.Batch{Type: powerhistory.BatchType, StreamID: "membership", RetainedFrom: 1,
		Buckets: []powerhistory.Bucket{reviewPowerBucket(1), changed, reviewPowerBucket(3)}}
	writeFrame(t, conn, batch)
	// Sentinel processing establishes that the preceding frame was handled,
	// without poisoning this websocket's read state with a timeout.
	s.sendSentinelMetrics(t, conn, "power-review", "after-membership-rejection")
	if n := powerRowCount(t, s, "power-review"); n != 0 {
		t.Fatalf("invalid middle bucket partially committed: %d rows", n)
	}
	if acked, maxSeen := powerStreamStateOf(t, s, "power-review", "membership"); acked != 0 || maxSeen != 0 {
		t.Fatalf("invalid batch advanced stream: ack=%d max=%d", acked, maxSeen)
	}
	if problem := s.powerHistoryProblem("power-review"); problem != "rejected_data" {
		t.Fatalf("missing rejection diagnostic: %q", problem)
	}

	// Model the corrected agent's wire output, not a hub-side sanitization.
	batch.Buckets[1].GPUTotal = nil
	writeFrame(t, conn, batch)
	expectPowerAck(t, conn, "membership", 3)
	// An invalid batch must not have left an earlier ACK queued above.
	writeFrame(t, conn, powerhistory.Batch{Type: powerhistory.BatchType, StreamID: "membership", RetainedFrom: 4,
		Buckets: []powerhistory.Bucket{reviewPowerBucket(4)}})
	expectPowerAck(t, conn, "membership", 4)
	history := reviewPowerHistory(t, s, "")
	if history.Problem != "" || history.Degraded || len(history.Points) != 4 {
		t.Fatalf("history did not recover: %+v", history)
	}
	for _, point := range history.Points {
		if point.Seq != 2 {
			if point.GPUTotal == nil {
				t.Fatalf("stable window lost total: %+v", point)
			}
			continue
		}
		if point.GPUTotal != nil || len(point.GPUs) != 2 || point.GPUs[1].Samples != 15 || point.CPU == nil || point.CPU.Samples != 30 {
			t.Fatalf("changed window lost component data or retained invalid total: %+v", point)
		}
	}
}
