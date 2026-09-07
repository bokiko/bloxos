package main

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestDeleteDoesNotWaitForBlockedPowerACK(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	token := loginAndGetToken(t, e)
	conn := s.connectEnrolledAgent(t, server, "machine-A")
	defer conn.Close()
	readAISessionsConfig(t, conn)
	s.sendSentinelMetrics(t, conn, "machine-A", "blocked-ack")
	s.agentsMu.RLock()
	agent := s.agents["machine-A"]
	s.agentsMu.RUnlock()
	if agent == nil {
		t.Fatal("agent not registered")
	}
	// Deterministic backpressure: the ACK cannot acquire the writer while
	// this lock is held. The old ingest callback held the global barrier
	// across this wait, so even deletion could not close the socket.
	agent.WriteMu.Lock()
	release := sync.OnceFunc(agent.WriteMu.Unlock)
	defer release()
	start, end := nowWindowMS()
	writeFrame(t, conn, powerBatch("stream-1", 1, powerBucket(1, start, end, 100, 200, 30, 30)))
	deadline := time.Now().Add(lifecycleDeadline)
	for powerRowCount(t, s, "machine-A") != 1 {
		if time.Now().After(deadline) {
			t.Fatal("power batch never committed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/machines/machine-A", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { e.ServeHTTP(rec, req); close(done) }()
	select {
	case <-done:
		if rec.Code != http.StatusOK {
			t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
		}
	case <-time.After(lifecycleDeadline):
		release()
		<-done
		t.Fatal("delete waited for the power ACK writer")
	}
	if countRows(t, s, "machines", "machine-A") != 0 {
		t.Fatal("machine survived delete")
	}
}
