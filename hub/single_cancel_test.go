package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

// serveSingleAsync issues a single-machine command under the given context
// and returns a channel that yields the recorder once the handler returned.
func serveSingleAsync(t *testing.T, s *Server, ctx context.Context, machineID, body string) <-chan *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	e.POST("/machines/:id/command", s.handleCommand)
	r := httptest.NewRequest(http.MethodPost, "/machines/"+machineID+"/command", strings.NewReader(body)).WithContext(ctx)
	r.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		done <- w
	}()
	return done
}

func awaitSingle(t *testing.T, done <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case w := <-done:
		return w
	case <-time.After(bulkTestDeadline):
		t.Fatal("command handler did not return after cancellation")
		return nil
	}
}

func assertNoLeakedSingleWaiters(t *testing.T) {
	t.Helper()
	pendingCmdsMu.Lock()
	defer pendingCmdsMu.Unlock()
	for id := range pendingCmds {
		if strings.HasPrefix(id, "cmd-") {
			t.Fatalf("command waiter %s leaked after handler returned", id)
		}
	}
}

// TestCommandAlreadyCanceledIsNeverSent: a request whose context is already
// done when it reaches the handler must not register a waiter or write a
// reboot to the agent.
func TestCommandAlreadyCanceledIsNeverSent(t *testing.T) {
	_, s := setupTestServer(t)
	ids := []string{"single-precanceled"}
	agents := newSilentBulkAgents(t, s, ids)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := awaitSingle(t, serveSingleAsync(t, s, ctx, ids[0], `{"type":"reboot","target":""}`))
	if w.Code == http.StatusOK || w.Code == http.StatusAccepted {
		t.Fatalf("canceled request must not report success or acceptance: %d %s", w.Code, w.Body)
	}
	agents.assertNothingElseSent(t, s, ids)
	assertNoLeakedSingleWaiters(t)
}

// TestCommandCancelWhileWriteLockHeld pins the agent's write lock so the
// handler has registered its waiter and is blocked on WriteMu when the
// request is canceled. Releasing the lock must not let the reboot escape.
func TestCommandCancelWhileWriteLockHeld(t *testing.T) {
	_, s := setupTestServer(t)
	ids := []string{"single-locked"}
	agents := newSilentBulkAgents(t, s, ids)
	s.agentsMu.RLock()
	agent := s.agents[ids[0]]
	s.agentsMu.RUnlock()

	agent.WriteMu.Lock()
	var unlockOnce sync.Once
	unlock := func() { unlockOnce.Do(agent.WriteMu.Unlock) }
	t.Cleanup(unlock)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := serveSingleAsync(t, s, ctx, ids[0], `{"type":"reboot","target":""}`)

	// Wait for the handler to pass the pre-registration recheck; with the
	// write lock held, its next observable step is blocking on WriteMu.
	deadline := time.Now().Add(bulkTestDeadline)
	for {
		pendingCmdsMu.Lock()
		registered := false
		for id := range pendingCmds {
			if strings.HasPrefix(id, "cmd-") {
				registered = true
			}
		}
		pendingCmdsMu.Unlock()
		if registered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("handler never registered its waiter")
		}
		time.Sleep(time.Millisecond)
	}

	cancel()
	unlock()
	w := awaitSingle(t, done)
	if w.Code == http.StatusOK || w.Code == http.StatusAccepted {
		t.Fatalf("command written after cancellation, or misreported: %d %s", w.Code, w.Body)
	}
	agents.assertNothingElseSent(t, s, ids)
	assertNoLeakedSingleWaiters(t)
}
