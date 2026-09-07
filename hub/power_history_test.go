package main

// End-to-end tests for hub-side power history: authenticated WS ingest,
// whole-batch validation, idempotent replay vs conflict rejection, explicit
// gap declaration, ACK-only-after-commit, retention pruning that preserves
// the sequence high-water and the AUTOINCREMENT delta cursor, and late
// backfill delivery by ingestion order.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
	"github.com/gorilla/websocket"
)

// --- helpers ---

func powerBucket(seq uint64, startMS, endMS int64, mean, peak float64, samples, expected int) map[string]any {
	return map[string]any{
		"seq":              seq,
		"start_unix_ms":    startMS,
		"end_unix_ms":      endMS,
		"expected_samples": expected,
		"gpus": []any{
			map[string]any{"id": "gpu-0", "mean_watts": mean, "peak_watts": peak, "samples": samples},
			map[string]any{"id": "gpu-1", "mean_watts": mean / 2, "peak_watts": peak / 2, "samples": samples},
		},
		"gpu_total": map[string]any{"mean_watts": mean * 1.5, "peak_watts": peak * 1.5, "samples": samples},
		"cpu":       map[string]any{"mean_watts": 65.0, "peak_watts": 80.0, "samples": samples},
	}
}

func powerBatch(streamID string, retainedFrom uint64, buckets ...map[string]any) map[string]any {
	return map[string]any{
		"type":          "power_history",
		"stream_id":     streamID,
		"retained_from": retainedFrom,
		"buckets":       buckets,
	}
}

func nowWindowMS() (int64, int64) {
	end := time.Now().UnixMilli()
	return end - 30_000, end
}

// readPowerAck reads the next frame with a deadline, returning an error on
// timeout (used both to receive ACKs and to prove none arrived).
func readPowerAck(t *testing.T, conn *websocket.Conn) (powerhistory.Ack, error) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		return powerhistory.Ack{}, err
	}
	var ack powerhistory.Ack
	if err := json.Unmarshal(msg, &ack); err != nil {
		t.Fatalf("unmarshal frame %q: %v", msg, err)
	}
	return ack, nil
}

func expectPowerAck(t *testing.T, conn *websocket.Conn, streamID string, through uint64) {
	t.Helper()
	ack, err := readPowerAck(t, conn)
	if err != nil {
		t.Fatalf("expected ack for %s, got read error: %v", streamID, err)
	}
	if ack.Type != powerhistory.AckType || ack.StreamID != streamID || ack.Through != through {
		t.Fatalf("unexpected ack: %+v (want stream=%s through=%d)", ack, streamID, through)
	}
}

func expectNoPowerAck(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Fatal("expected no frame, got one")
	}
}

func powerHistoryGet(t *testing.T, h http.Handler, token, machineID, query string) (int, powerhistory.History, string) {
	t.Helper()
	url := "/api/machines/" + machineID + "/power/history" + query
	req := httptest.NewRequest(http.MethodGet, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var hist powerhistory.History
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &hist); err != nil {
			t.Fatalf("unmarshal history: %v (%s)", err, rec.Body.String())
		}
	}
	return rec.Code, hist, rec.Body.String()
}

func powerRowCount(t *testing.T, s *Server, machineID string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM power_history_records WHERE machine_id = ?`, machineID).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

func powerStreamStateOf(t *testing.T, s *Server, machineID, streamID string) (acked, maxSeen uint64) {
	t.Helper()
	err := s.db.QueryRow(`SELECT acked_through, max_seen_seq FROM power_history_stream_state
		WHERE machine_id = ? AND stream_id = ?`, machineID, streamID).Scan(&acked, &maxSeen)
	if err == sql.ErrNoRows {
		return 0, 0
	}
	if err != nil {
		t.Fatalf("stream state: %v", err)
	}
	return acked, maxSeen
}

// --- tests ---

func TestPowerHistoryIngestAckAndRead(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAndGetToken(t, e)

	conn := s.connectEnrolledAgent(t, server, "machine-A")
	defer conn.Close()
	readAISessionsConfig(t, conn) // drain the registration-time config frame
	s.sendSentinelMetrics(t, conn, "machine-A", "host-a")

	start, end := nowWindowMS()
	writeFrame(t, conn, powerBatch("stream-1", 1, powerBucket(1, start, end, 100, 200, 30, 30)))
	expectPowerAck(t, conn, "stream-1", 1)

	if n := powerRowCount(t, s, "machine-A"); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}

	code, hist, body := powerHistoryGet(t, e, adminToken, "machine-A", "")
	if code != http.StatusOK {
		t.Fatalf("GET: %d %s", code, body)
	}
	if len(hist.Points) != 1 || len(hist.Gaps) != 0 || hist.Degraded {
		t.Fatalf("unexpected history: %+v", hist)
	}
	p := hist.Points[0]
	if p.StreamID != "stream-1" || p.Seq != 1 || p.ExpectedSamples != 30 {
		t.Fatalf("unexpected point: %+v", p)
	}
	if p.GPUTotal == nil || *p.GPUTotal.PeakWatts != 300 || p.CPU == nil || *p.CPU.MeanWatts != 65 {
		t.Fatalf("gpu_total/cpu not preserved: %+v", p)
	}
	if len(p.GPUs) != 2 || p.GPUs[0].ID != "gpu-0" || *p.GPUs[1].MeanWatts != 50 {
		t.Fatalf("per-GPU breakdown not preserved: %+v", p.GPUs)
	}
	if hist.Cursor == 0 {
		t.Fatal("cursor must be non-zero after ingest")
	}
	if !strings.Contains(body, `"points":[`) || !strings.Contains(body, `"gaps":[`) {
		t.Fatalf("empty-contract fields must serialize as []: %s", body)
	}
}

func TestPowerHistoryBoundToAuthenticatedConnection(t *testing.T) {
	e, s := setupTestServer(t)
	server := httptest.NewServer(e)
	defer server.Close()

	conn := s.connectEnrolledAgent(t, server, "machine-A")
	defer conn.Close()
	readAISessionsConfig(t, conn)

	// Spoof attempt: the envelope claims another machine. The identity
	// guard drops the frame before the switch — nothing is stored for
	// either machine. A sentinel-metrics barrier confirms the hub processed
	// everything before this point; a negative websocket read is NOT used
	// here because a gorilla read timeout permanently poisons the reader.
	start, end := nowWindowMS()
	spoofed := powerBatch("stream-1", 1, powerBucket(1, start, end, 100, 200, 30, 30))
	spoofed["machine_id"] = "machine-B"
	writeFrame(t, conn, spoofed)
	s.sendSentinelMetrics(t, conn, "machine-A", "host-a")
	if n := powerRowCount(t, s, "machine-A"); n != 0 {
		t.Fatalf("spoofed frame stored %d rows for machine-A", n)
	}
	if n := powerRowCount(t, s, "machine-B"); n != 0 {
		t.Fatalf("spoofed frame stored %d rows for machine-B", n)
	}

	// The same batch without the spoofed field is accepted and keyed to the
	// authenticated machine, not any field in the frame.
	writeFrame(t, conn, powerBatch("stream-1", 1, powerBucket(1, start, end, 100, 200, 30, 30)))
	expectPowerAck(t, conn, "stream-1", 1)
	if n := powerRowCount(t, s, "machine-A"); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

func TestPowerHistoryInvalidBatchesRejectedWhole(t *testing.T) {
	e, s := setupTestServer(t)
	server := httptest.NewServer(e)
	defer server.Close()

	conn := s.connectEnrolledAgent(t, server, "machine-A")
	defer conn.Close()
	readAISessionsConfig(t, conn)

	start, end := nowWindowMS()
	fresh := func() map[string]any { return powerBucket(1, start, end, 100, 200, 30, 30) }

	bad := map[string]func() map[string]any{
		"negative watts": func() map[string]any {
			b := fresh()
			b["gpus"] = []any{map[string]any{"id": "gpu-0", "mean_watts": -1, "peak_watts": 10, "samples": 30}}
			return b
		},
		"stats with zero samples": func() map[string]any {
			b := fresh()
			b["gpus"] = []any{map[string]any{"id": "gpu-0", "mean_watts": 10, "samples": 0}}
			return b
		},
		"negative samples": func() map[string]any {
			b := fresh()
			b["gpus"] = []any{map[string]any{"id": "gpu-0", "samples": -3}}
			return b
		},
		"duplicate gpu ids": func() map[string]any {
			b := fresh()
			b["gpus"] = []any{
				map[string]any{"id": "gpu-0", "samples": 1},
				map[string]any{"id": "gpu-0", "samples": 1},
			}
			return b
		},
		"end before start": func() map[string]any {
			b := fresh()
			b["start_unix_ms"], b["end_unix_ms"] = end, start
			return b
		},
		"window too long": func() map[string]any {
			b := fresh()
			b["end_unix_ms"] = start + int64(2*time.Hour/time.Millisecond)
			return b
		},
		"too old": func() map[string]any {
			b := fresh()
			old := time.Now().Add(-72 * time.Hour).UnixMilli()
			b["start_unix_ms"], b["end_unix_ms"] = old, old+30_000
			return b
		},
		"too far future (clock skew)": func() map[string]any {
			b := fresh()
			fut := time.Now().Add(2 * 24 * time.Hour).UnixMilli()
			b["start_unix_ms"], b["end_unix_ms"] = fut, fut+30_000
			return b
		},
		"seq zero": func() map[string]any {
			b := fresh()
			b["seq"] = uint64(0)
			return b
		},
		"seq above max": func() map[string]any {
			b := fresh()
			b["seq"] = uint64(1) << 53
			return b
		},
		"wrong type": func() map[string]any {
			b := fresh()
			b["expected_samples"] = "thirty"
			return b
		},
		"string watts": func() map[string]any {
			b := fresh()
			b["gpus"] = []any{map[string]any{"id": "gpu-0", "mean_watts": "NaN", "samples": 30}}
			return b
		},
		"oversized frame": func() map[string]any {
			b := fresh()
			b["gpus"] = []any{map[string]any{"id": "gpu-0", "mean_watts": 10, "samples": 30, "pad": strings.Repeat("x", powerhistory.MaxFrameBytes)}}
			return b
		},
	}

	for name, mk := range bad {
		t.Run(name, func(t *testing.T) {
			writeFrame(t, conn, powerBatch("stream-1", 1, mk()))
			expectNoPowerAck(t, conn)
			if n := powerRowCount(t, s, "machine-A"); n != 0 {
				t.Fatalf("invalid batch mutated storage: %d rows", n)
			}
		})
	}

	// Batch-level violations on a valid bucket.
	writeFrame(t, conn, map[string]any{
		"type": "power_history", "stream_id": "../evil", "retained_from": 1,
		"buckets": []any{fresh()},
	})
	expectNoPowerAck(t, conn)

	b1, b2 := fresh(), fresh()
	b2["seq"] = uint64(1) // duplicate/unsorted seq within batch
	writeFrame(t, conn, powerBatch("stream-1", 1, b1, b2))
	expectNoPowerAck(t, conn)

	// More buckets than the contract maximum.
	many := make([]map[string]any, 0, powerhistory.MaxBatchRecords+1)
	for seq := uint64(1); seq <= powerhistory.MaxBatchRecords+1; seq++ {
		b := fresh()
		b["seq"] = seq
		many = append(many, b)
	}
	writeFrame(t, conn, powerBatch("stream-1", 1, many...))
	expectNoPowerAck(t, conn)

	if n := powerRowCount(t, s, "machine-A"); n != 0 {
		t.Fatalf("storage mutated by invalid batches: %d rows", n)
	}
	if acked, _ := powerStreamStateOf(t, s, "machine-A", "stream-1"); acked != 0 {
		t.Fatalf("stream state mutated: acked=%d", acked)
	}
}

func TestPowerHistoryReplayIdempotentAndConflictRejected(t *testing.T) {
	e, s := setupTestServer(t)
	server := httptest.NewServer(e)
	defer server.Close()

	conn := s.connectEnrolledAgent(t, server, "machine-A")
	defer conn.Close()
	readAISessionsConfig(t, conn)

	start, end := nowWindowMS()
	b1 := powerBucket(1, start, end, 100, 200, 30, 30)
	b2 := powerBucket(2, start, end, 110, 210, 30, 30)
	writeFrame(t, conn, powerBatch("stream-1", 1, b1, b2))
	expectPowerAck(t, conn, "stream-1", 2)

	// Lost-ACK replay: identical payloads are absorbed, ack re-sent, no
	// duplicate rows.
	writeFrame(t, conn, powerBatch("stream-1", 1, b1, b2))
	expectPowerAck(t, conn, "stream-1", 2)
	if n := powerRowCount(t, s, "machine-A"); n != 2 {
		t.Fatalf("replay duplicated rows: %d", n)
	}

	// Conflicting payload for an already-acked seq: batch rejected whole.
	bad := powerBucket(1, start, end, 999, 999, 30, 30)
	writeFrame(t, conn, powerBatch("stream-1", 1, bad))
	expectNoPowerAck(t, conn)
	if n := powerRowCount(t, s, "machine-A"); n != 2 {
		t.Fatalf("conflicting batch mutated rows: %d", n)
	}
	if acked, maxSeen := powerStreamStateOf(t, s, "machine-A", "stream-1"); acked != 2 || maxSeen != 2 {
		t.Fatalf("conflicting batch mutated state: acked=%d max=%d", acked, maxSeen)
	}
}

func TestPowerHistoryGapDeclarationAdvancesAckThrough(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAndGetToken(t, e)

	conn := s.connectEnrolledAgent(t, server, "machine-A")
	defer conn.Close()
	readAISessionsConfig(t, conn)

	start, end := nowWindowMS()
	writeFrame(t, conn, powerBatch("stream-1", 1,
		powerBucket(1, start, end, 100, 200, 30, 30),
		powerBucket(2, start, end, 100, 200, 30, 30),
		powerBucket(3, start, end, 100, 200, 30, 30),
	))
	expectPowerAck(t, conn, "stream-1", 3)

	// Agent lost seq 4 (journal pruned) and declares retention from 5.
	// The gap becomes part of the contiguous prefix, so the hub acks
	// through 6 — but never creates silent holes on its own.
	writeFrame(t, conn, powerBatch("stream-1", 5,
		powerBucket(5, start, end, 100, 200, 30, 30),
		powerBucket(6, start, end, 100, 200, 30, 30),
	))
	expectPowerAck(t, conn, "stream-1", 6)

	_, hist, body := powerHistoryGet(t, e, adminToken, "machine-A", "")
	if len(hist.Gaps) != 1 || hist.Gaps[0].From != 4 || hist.Gaps[0].Through != 4 {
		t.Fatalf("expected gap [4,4], got %+v (%s)", hist.Gaps, body)
	}
}

func TestPowerHistoryNoSilentHoles(t *testing.T) {
	e, s := setupTestServer(t)
	server := httptest.NewServer(e)
	defer server.Close()

	conn := s.connectEnrolledAgent(t, server, "machine-A")
	defer conn.Close()
	readAISessionsConfig(t, conn)

	start, end := nowWindowMS()
	// Sparse new seqs with NO retention-loss declaration (retained_from 0):
	// accepted, but the un-acked prefix must stay discontiguous and no gap
	// may be invented.
	writeFrame(t, conn, powerBatch("stream-1", 1,
		powerBucket(10, start, end, 100, 200, 30, 30),
		powerBucket(11, start, end, 100, 200, 30, 30),
	))
	expectPowerAck(t, conn, "stream-1", 0)

	var gapCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM power_history_gaps WHERE machine_id = ?`, "machine-A").Scan(&gapCount); err != nil {
		t.Fatal(err)
	}
	if gapCount != 0 {
		t.Fatalf("silent gap created: %d", gapCount)
	}

	// Filling the hole makes the prefix contiguous through 11.
	filled := make([]map[string]any, 0, 9)
	for seq := uint64(1); seq <= 9; seq++ {
		filled = append(filled, powerBucket(seq, start, end, 100, 200, 30, 30))
	}
	writeFrame(t, conn, powerBatch("stream-1", 1, filled...))
	expectPowerAck(t, conn, "stream-1", 11)
}

func TestPowerHistoryNoAckWhenCommitFails(t *testing.T) {
	e, s := setupTestServer(t)
	server := httptest.NewServer(e)
	defer server.Close()

	conn := s.connectEnrolledAgent(t, server, "machine-A")
	defer conn.Close()
	readAISessionsConfig(t, conn)

	if err := s.db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	start, end := nowWindowMS()
	raw, _ := json.Marshal(powerBatch("stream-1", 1, powerBucket(1, start, end, 100, 200, 30, 30)))
	if err := s.ingestPowerHistory("machine-A", s.registeredAgent("machine-A"), raw); err == nil {
		t.Fatal("expected commit failure on closed db")
	}
	expectNoPowerAck(t, conn)
}

func TestPowerHistoryRetentionPrunePreservesHighWaterAndCursor(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAndGetToken(t, e)

	conn := s.connectEnrolledAgent(t, server, "machine-A")
	defer conn.Close()
	readAISessionsConfig(t, conn)
	s.sendSentinelMetrics(t, conn, "machine-A", "host-a")

	start, end := nowWindowMS()
	writeFrame(t, conn, powerBatch("stream-1", 1, powerBucket(1, start, end, 100, 200, 30, 30)))
	expectPowerAck(t, conn, "stream-1", 1)
	writeFrame(t, conn, powerBatch("stream-1", 1, powerBucket(2, start, end, 100, 200, 30, 30)))
	expectPowerAck(t, conn, "stream-1", 2)

	_, hist, _ := powerHistoryGet(t, e, adminToken, "machine-A", "")
	if len(hist.Points) != 2 || hist.Cursor == 0 {
		t.Fatalf("initial read: %+v", hist)
	}
	cursor := hist.Cursor

	// Age seq 1 past retention and prune. Stream high-water must survive.
	old := time.Now().Add(-25 * time.Hour).UnixMilli()
	if _, err := s.db.Exec(`UPDATE power_history_records SET start_unix_ms = ?, end_unix_ms = ? WHERE seq = 1`, old, old+30_000); err != nil {
		t.Fatal(err)
	}
	if _, err := s.prunePowerHistory(time.Now()); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n := powerRowCount(t, s, "machine-A"); n != 1 {
		t.Fatalf("prune kept %d rows, want 1", n)
	}
	if acked, maxSeen := powerStreamStateOf(t, s, "machine-A", "stream-1"); acked != 2 || maxSeen != 2 {
		t.Fatalf("prune reset high-water: acked=%d max=%d", acked, maxSeen)
	}

	// Replay of the pruned, acked seq: absorbed, not re-inserted.
	writeFrame(t, conn, powerBatch("stream-1", 1, powerBucket(1, start, end, 100, 200, 30, 30)))
	expectPowerAck(t, conn, "stream-1", 2)
	if n := powerRowCount(t, s, "machine-A"); n != 1 {
		t.Fatalf("pruned replay resurrected rows: %d", n)
	}

	// Delta from the pre-prune cursor: empty, cursor never regresses.
	code, delta, _ := powerHistoryGet(t, e, adminToken, "machine-A", fmt.Sprintf("?after=%d", cursor))
	if code != http.StatusOK || len(delta.Points) != 0 || delta.Cursor != cursor {
		t.Fatalf("delta after prune: code=%d %+v", code, delta)
	}

	// New ingest after prune gets a FRESH id (AUTOINCREMENT does not reuse)
	// and is delivered exactly once by the delta cursor.
	writeFrame(t, conn, powerBatch("stream-1", 1, powerBucket(3, start, end, 100, 200, 30, 30)))
	expectPowerAck(t, conn, "stream-1", 3)
	code, delta, _ = powerHistoryGet(t, e, adminToken, "machine-A", fmt.Sprintf("?after=%d", cursor))
	if code != http.StatusOK || len(delta.Points) != 1 || delta.Points[0].Seq != 3 || delta.Cursor <= cursor {
		t.Fatalf("delta after new ingest: code=%d %+v", code, delta)
	}
}

func TestPowerHistoryLateBackfillDeliveredByIngestionOrder(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAndGetToken(t, e)

	conn := s.connectEnrolledAgent(t, server, "machine-A")
	defer conn.Close()
	readAISessionsConfig(t, conn)
	s.sendSentinelMetrics(t, conn, "machine-A", "host-a")

	start, end := nowWindowMS()
	writeFrame(t, conn, powerBatch("stream-1", 1, powerBucket(1, start, end, 100, 200, 30, 30)))
	expectPowerAck(t, conn, "stream-1", 1)

	_, hist, _ := powerHistoryGet(t, e, adminToken, "machine-A", "")
	cursor := hist.Cursor

	// A 30-hour-old unacked journal bucket arrives late. It is outside the
	// 24h initial window, but the delta cursor is ingestion order, so it
	// still reaches the client.
	backStart := time.Now().Add(-30 * time.Hour).UnixMilli()
	writeFrame(t, conn, powerBatch("stream-1", 1, powerBucket(2, backStart, backStart+30_000, 100, 200, 30, 30)))
	expectPowerAck(t, conn, "stream-1", 2)

	_, initial, _ := powerHistoryGet(t, e, adminToken, "machine-A", "")
	if len(initial.Points) != 1 {
		t.Fatalf("initial window should exclude 20h-old bucket: %+v", initial.Points)
	}
	code, delta, _ := powerHistoryGet(t, e, adminToken, "machine-A", fmt.Sprintf("?after=%d", cursor))
	if code != http.StatusOK || len(delta.Points) != 1 || delta.Points[0].Seq != 2 {
		t.Fatalf("late backfill missing from delta: code=%d %+v", code, delta)
	}
	if delta.Points[0].StartUnixMS != backStart {
		t.Fatalf("backfill measurement time not preserved: %+v", delta.Points[0])
	}
}

func TestPowerHistoryDeltaPaginationCursor(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAndGetToken(t, e)

	conn := s.connectEnrolledAgent(t, server, "machine-A")
	defer conn.Close()
	readAISessionsConfig(t, conn)
	s.sendSentinelMetrics(t, conn, "machine-A", "host-a")

	start, end := nowWindowMS()
	buckets := make([]map[string]any, 0, 3)
	for seq := uint64(1); seq <= 3; seq++ {
		buckets = append(buckets, powerBucket(seq, start, end, 100, 200, 30, 30))
	}
	writeFrame(t, conn, powerBatch("stream-1", 1, buckets...))
	expectPowerAck(t, conn, "stream-1", 3)

	// Truncated delta page: cursor is the last RETURNED id, so the tail is
	// not skipped.
	_, page1, _ := powerHistoryGet(t, e, adminToken, "machine-A", "?after=0&max_records=1")
	if len(page1.Points) != 1 || page1.Cursor == 0 {
		t.Fatalf("page1: %+v", page1)
	}
	_, page2, _ := powerHistoryGet(t, e, adminToken, "machine-A", fmt.Sprintf("?after=%d&max_records=1", page1.Cursor))
	if len(page2.Points) != 1 || page2.Points[0].Seq != 2 {
		t.Fatalf("page2 skipped data: %+v", page2)
	}
	_, page3, _ := powerHistoryGet(t, e, adminToken, "machine-A", fmt.Sprintf("?after=%d&max_records=100", page2.Cursor))
	if len(page3.Points) != 1 || page3.Points[0].Seq != 3 {
		t.Fatalf("page3: %+v", page3)
	}
}

func TestPowerHistoryDegradedFlagTracksLatestBatch(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAndGetToken(t, e)

	conn := s.connectEnrolledAgent(t, server, "machine-A")
	defer conn.Close()
	readAISessionsConfig(t, conn)
	s.sendSentinelMetrics(t, conn, "machine-A", "host-a")

	start, end := nowWindowMS()
	degraded := powerBatch("stream-1", 1, powerBucket(1, start, end, 100, 200, 30, 30))
	degraded["degraded"] = true
	writeFrame(t, conn, degraded)
	expectPowerAck(t, conn, "stream-1", 1)

	_, hist, _ := powerHistoryGet(t, e, adminToken, "machine-A", "")
	if !hist.Degraded {
		t.Fatal("degraded batch must surface degraded=true")
	}

	writeFrame(t, conn, powerBatch("stream-1", 1, powerBucket(2, start, end, 100, 200, 30, 30)))
	expectPowerAck(t, conn, "stream-1", 2)
	_, hist, _ = powerHistoryGet(t, e, adminToken, "machine-A", "")
	if hist.Degraded {
		t.Fatal("degraded must clear on the next clean batch")
	}
}

func TestPowerHistoryReadAPIAuthAndBounds(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAndGetToken(t, e)
	s.seedTestUser(t, "viewer1", "viewerpass123", "1234", RoleViewer, true, true)
	viewerToken := loginAndGetTokenForCredentials(t, e, "viewer1", "viewerpass123")
	s.seedTestMachine(t, "machine-A")

	if code, _, _ := powerHistoryGet(t, e, "", "machine-A", ""); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d", code)
	}
	if code, hist, _ := powerHistoryGet(t, e, viewerToken, "machine-A", ""); code != http.StatusOK {
		t.Fatalf("viewer read: %d", code)
	} else if len(hist.Points) != 0 || len(hist.Gaps) != 0 || hist.Degraded || hist.Cursor != 0 {
		t.Fatalf("empty machine must return empty contract with cursor: %+v", hist)
	}
	if code, _, _ := powerHistoryGet(t, e, adminToken, "machine-404", ""); code != http.StatusNotFound {
		t.Fatalf("unknown machine: %d", code)
	}
	if code, _, body := powerHistoryGet(t, e, adminToken, "machine-A", "?after=abc"); code != http.StatusBadRequest {
		t.Fatalf("bad cursor: %d %s", code, body)
	}
}

func TestDeleteMachineRemovesPowerHistory(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAndGetToken(t, e)

	conn := s.connectEnrolledAgent(t, server, "machine-A")
	readAISessionsConfig(t, conn)

	start, end := nowWindowMS()
	writeFrame(t, conn, powerBatch("stream-1", 2, powerBucket(2, start, end, 100, 200, 30, 30)))
	expectPowerAck(t, conn, "stream-1", 2) // declares gap [1,1]
	conn.Close()
	s.waitAgentDrain(t, "machine-A", 2*time.Second)

	req := httptest.NewRequest(http.MethodDelete, "/api/machines/machine-A", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}

	for _, table := range []string{"power_history_records", "power_history_stream_state", "power_history_gaps"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE machine_id = ?`, "machine-A").Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Fatalf("%s still has %d rows after machine deletion", table, n)
		}
	}
}

func TestPowerHistoryGapOnlyBatch(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAndGetToken(t, e)

	conn := s.connectEnrolledAgent(t, server, "machine-A")
	defer conn.Close()
	readAISessionsConfig(t, conn)
	s.sendSentinelMetrics(t, conn, "machine-A", "host-a")

	start, end := nowWindowMS()
	writeFrame(t, conn, powerBatch("stream-1", 1,
		powerBucket(1, start, end, 100, 200, 30, 30),
		powerBucket(2, start, end, 100, 200, 30, 30),
	))
	expectPowerAck(t, conn, "stream-1", 2)

	// Retention ate the last unsent records: the agent sends a zero-bucket
	// batch declaring retention from 5. No panic on the empty bucket list,
	// and the ACK advances across the explicitly declared lost prefix.
	writeFrame(t, conn, map[string]any{
		"type": "power_history", "stream_id": "stream-1", "retained_from": 5,
		"buckets": []any{},
	})
	expectPowerAck(t, conn, "stream-1", 4)

	_, hist, body := powerHistoryGet(t, e, adminToken, "machine-A", "")
	if len(hist.Gaps) != 1 || hist.Gaps[0].From != 3 || hist.Gaps[0].Through != 4 {
		t.Fatalf("expected declared gap [3,4], got %+v (%s)", hist.Gaps, body)
	}

	// An empty batch with no retention declaration is meaningless and must
	// be rejected (retained_from >= 1 is required).
	writeFrame(t, conn, map[string]any{
		"type": "power_history", "stream_id": "stream-1", "retained_from": 0,
		"buckets": []any{},
	})
	expectNoPowerAck(t, conn)
}

func TestPowerHistoryAgedDuplicateReplayNotPoisoned(t *testing.T) {
	e, s := setupTestServer(t)
	server := httptest.NewServer(e)
	defer server.Close()

	conn := s.connectEnrolledAgent(t, server, "machine-A")
	defer conn.Close()
	readAISessionsConfig(t, conn)

	// Simulate data committed long ago (e.g. accepted near the age limit,
	// replayed after it): an ancient-but-committed seq in stream state with
	// its canonical row. A byte-identical replay must be absorbed by dedupe
	// BEFORE any age enforcement — otherwise the agent would retry the same
	// unacked journal entry forever.
	start, end := nowWindowMS()
	bucket := powerBucket(1, start, end, 100, 200, 30, 30)
	// Canonical payload must be the hub's own encoding (struct field order),
	// not a map-order marshal.
	raw, _ := json.Marshal(bucket)
	var pb powerhistory.Bucket
	if err := json.Unmarshal(raw, &pb); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(pb)
	if _, err := s.db.Exec(`INSERT INTO power_history_records
		(machine_id, stream_id, seq, start_unix_ms, end_unix_ms, expected_samples, payload)
		VALUES (?, ?, 1, ?, ?, 30, ?)`, "machine-A", "stream-1", start, end, string(payload)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO power_history_stream_state
		(machine_id, stream_id, acked_through, max_seen_seq, retained_from) VALUES (?, ?, 0, 1, 1)`,
		"machine-A", "stream-1"); err != nil {
		t.Fatal(err)
	}

	writeFrame(t, conn, powerBatch("stream-1", 1, bucket))
	expectPowerAck(t, conn, "stream-1", 1)
	if n := powerRowCount(t, s, "machine-A"); n != 1 {
		t.Fatalf("aged replay duplicated rows: %d", n)
	}

	// Mixed batch: the aged duplicate is absorbed and the fresh new bucket
	// still commits in the same transaction.
	writeFrame(t, conn, powerBatch("stream-1", 1, bucket, powerBucket(2, start, end, 110, 210, 30, 30)))
	expectPowerAck(t, conn, "stream-1", 2)
	if n := powerRowCount(t, s, "machine-A"); n != 2 {
		t.Fatalf("mixed batch rows: %d", n)
	}
}

func TestPowerHistoryOldAgentWithoutFramesUnaffected(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAndGetToken(t, e)

	// An old agent speaks only metrics — no power frames, no ACKs, and the
	// history endpoint serves an empty contract for it.
	conn := s.connectEnrolledAgent(t, server, "machine-old")
	defer conn.Close()
	readAISessionsConfig(t, conn)
	s.sendSentinelMetrics(t, conn, "machine-old", "host-old")

	code, hist, _ := powerHistoryGet(t, e, adminToken, "machine-old", "")
	if code != http.StatusOK || len(hist.Points) != 0 || hist.Cursor != 0 {
		t.Fatalf("old agent history: %d %+v", code, hist)
	}
	var hostname string
	if err := s.db.QueryRow(`SELECT hostname FROM machines WHERE id = 'machine-old'`).Scan(&hostname); err != nil || hostname != "host-old" {
		t.Fatalf("metrics flow broken: hostname=%q err=%v", hostname, err)
	}
}
