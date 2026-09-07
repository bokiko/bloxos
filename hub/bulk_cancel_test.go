package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

// bulkTestDeadline bounds every wait in this file. It is a hang guard, not
// a timing assumption: the assertions themselves are ordering-based.
const bulkTestDeadline = 10 * time.Second

// bulkSentinelType is a message the test writes through an agent's socket
// after the handler has returned. WebSocket frames are ordered per
// connection, so once the sentinel arrives at the mock agent, any command
// the hub had written to that socket earlier has already been counted.
const bulkSentinelType = "test-sentinel"

// silentBulkAgents stands up mock agents that record every command they
// receive and never reply, so in-flight bulk targets stay parked in their
// wait until the request is canceled. All agents share one WebSocket
// server; the hub side keeps a distinct connection per machine.
type silentBulkAgents struct {
	commands  chan CommandToAgent
	sentinels chan CommandToAgent
}

func newSilentBulkAgents(t *testing.T, s *Server, machineIDs []string) *silentBulkAgents {
	t.Helper()
	m := &silentBulkAgents{
		commands:  make(chan CommandToAgent, len(machineIDs)*2),
		sentinels: make(chan CommandToAgent, len(machineIDs)*2),
	}
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var cmd CommandToAgent
			if err := conn.ReadJSON(&cmd); err != nil {
				return
			}
			if cmd.Type == bulkSentinelType {
				m.sentinels <- cmd
				continue
			}
			m.commands <- cmd
		}
	}))
	t.Cleanup(ws.Close)
	for _, id := range machineIDs {
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ws.URL, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		s.agentsMu.Lock()
		s.agents[id] = &ConnectedAgent{MachineID: id, Conn: conn}
		s.agentsMu.Unlock()
		s.seedTestMachine(t, id)
	}
	return m
}

// receiveCommands drains exactly n commands from the mock agents, failing
// on the shared deadline instead of hanging.
func (m *silentBulkAgents) receiveCommands(t *testing.T, n int) []CommandToAgent {
	t.Helper()
	deadline := time.After(bulkTestDeadline)
	got := make([]CommandToAgent, 0, n)
	for len(got) < n {
		select {
		case cmd := <-m.commands:
			got = append(got, cmd)
		case <-deadline:
			t.Fatalf("received %d of %d expected commands before deadline", len(got), n)
		}
	}
	return got
}

// assertNothingElseSent writes a sentinel through each listed agent socket
// and waits for all of them to arrive; any command the hub wrote to those
// sockets earlier would already be in m.commands by then.
func (m *silentBulkAgents) assertNothingElseSent(t *testing.T, s *Server, machineIDs []string) {
	t.Helper()
	sentinel, _ := json.Marshal(CommandToAgent{Type: bulkSentinelType, ID: "sentinel"})
	for _, id := range machineIDs {
		s.agentsMu.RLock()
		agent := s.agents[id]
		s.agentsMu.RUnlock()
		if agent == nil {
			t.Fatalf("agent %s missing", id)
		}
		if err := agent.writeLocked(websocket.TextMessage, sentinel); err != nil {
			t.Fatalf("sentinel write to %s: %v", id, err)
		}
	}
	deadline := time.After(bulkTestDeadline)
	for i := 0; i < len(machineIDs); i++ {
		select {
		case <-m.sentinels:
		case <-deadline:
			t.Fatalf("only %d of %d sentinels arrived before deadline", i, len(machineIDs))
		}
	}
	select {
	case cmd := <-m.commands:
		t.Fatalf("command %s was sent after cancellation", cmd.ID)
	default:
	}
}

type bulkCancelResult struct {
	MachineID string `json:"machine_id"`
	Accepted  bool   `json:"accepted"`
	Success   bool   `json:"success"`
	Error     string `json:"error"`
}

// serveBulkAsync issues the bulk request under a cancelable context and
// returns the cancel func plus a channel that yields the recorder once the
// handler has returned.
func serveBulkAsync(t *testing.T, s *Server, body string) (context.CancelFunc, <-chan *httptest.ResponseRecorder) {
	t.Helper()
	e := echo.New()
	e.POST("/bulk", s.handleBulkCommand)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := httptest.NewRequest(http.MethodPost, "/bulk", strings.NewReader(body)).WithContext(ctx)
	r.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		done <- w
	}()
	return cancel, done
}

func awaitBulk(t *testing.T, done <-chan *httptest.ResponseRecorder) []bulkCancelResult {
	t.Helper()
	select {
	case w := <-done:
		var resp struct {
			Results []bulkCancelResult `json:"results"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("bad bulk response %d: %s", w.Code, w.Body)
		}
		return resp.Results
	case <-time.After(bulkTestDeadline):
		t.Fatal("bulk handler did not return after cancellation")
		return nil
	}
}

func assertNoLeakedBulkWaiters(t *testing.T) {
	t.Helper()
	pendingCmdsMu.Lock()
	defer pendingCmdsMu.Unlock()
	for id := range pendingCmds {
		if strings.HasPrefix(id, "bulk-") {
			t.Fatalf("bulk waiter %s leaked after handler returned", id)
		}
	}
}

// TestBulkCommandCancelStopsUnsentTargets fans out to more targets than the
// worker bound, lets the first wave land on the agents, then cancels the
// request while those workers are still waiting for replies. The targets
// that had not yet been dispatched must never receive the command, and the
// results must say which commands were on the wire and which were not.
func TestBulkCommandCancelStopsUnsentTargets(t *testing.T) {
	_, s := setupTestServer(t)
	const total = bulkCommandConcurrency + 5
	ids := make([]string, total)
	for i := range ids {
		ids[i] = "bulk-cancel-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	agents := newSilentBulkAgents(t, s, ids)

	idsJSON, _ := json.Marshal(ids)
	cancel, done := serveBulkAsync(t, s, `{"machine_ids":`+string(idsJSON)+`,"type":"restart_service","target":"nginx"}`)

	// The first wave is exactly one worker per slot; the loop is now blocked
	// acquiring a slot for target 21, and every worker is parked waiting.
	firstWave := agents.receiveCommands(t, bulkCommandConcurrency)
	seen := map[string]bool{}
	for _, cmd := range firstWave {
		if cmd.Type != "restart_service" || cmd.Target != "nginx" || !strings.HasPrefix(cmd.ID, "bulk-") {
			t.Fatalf("unexpected command %+v", cmd)
		}
		if seen[cmd.ID] {
			t.Fatalf("duplicate command id %s", cmd.ID)
		}
		seen[cmd.ID] = true
	}

	cancel()
	results := awaitBulk(t, done)

	if len(results) != total {
		t.Fatalf("expected one result per target (%d), got %d", total, len(results))
	}
	var sent, unsent int
	byMachine := map[string]bulkCancelResult{}
	for _, r := range results {
		if _, dup := byMachine[r.MachineID]; dup {
			t.Fatalf("duplicate result for %s", r.MachineID)
		}
		byMachine[r.MachineID] = r
		if r.Accepted || r.Success {
			t.Fatalf("cancellation must not report success or acceptance: %+v", r)
		}
		switch r.Error {
		case bulkCanceledSentError:
			sent++
		case bulkCanceledUnsentError:
			unsent++
		default:
			t.Fatalf("unexpected result: %+v", r)
		}
	}
	if sent != bulkCommandConcurrency || unsent != total-bulkCommandConcurrency {
		t.Fatalf("sent=%d unsent=%d, want %d/%d", sent, unsent, bulkCommandConcurrency, total-bulkCommandConcurrency)
	}
	for _, id := range ids {
		if _, ok := byMachine[id]; !ok {
			t.Fatalf("missing result for %s", id)
		}
	}

	agents.assertNothingElseSent(t, s, ids)
	assertNoLeakedBulkWaiters(t)
}

// TestBulkCommandCancelWhileWriteLockHeld pins the agent's write lock so the
// worker has already registered its waiter and is blocked on WriteMu when
// the request is canceled. Releasing the lock afterwards must not let the
// command escape: the recheck after acquiring the lock has to win.
func TestBulkCommandCancelWhileWriteLockHeld(t *testing.T) {
	_, s := setupTestServer(t)
	ids := []string{"bulk-locked"}
	agents := newSilentBulkAgents(t, s, ids)
	s.agentsMu.RLock()
	agent := s.agents[ids[0]]
	s.agentsMu.RUnlock()

	agent.WriteMu.Lock()
	var unlockOnce sync.Once
	unlock := func() { unlockOnce.Do(agent.WriteMu.Unlock) }
	t.Cleanup(unlock)

	cancel, done := serveBulkAsync(t, s, `{"machine_ids":["bulk-locked"],"type":"reboot","target":""}`)

	// Wait for the worker to pass the pre-registration recheck; with the
	// write lock held, its next observable step is blocking on WriteMu.
	deadline := time.Now().Add(bulkTestDeadline)
	for {
		pendingCmdsMu.Lock()
		registered := false
		for id := range pendingCmds {
			if strings.HasPrefix(id, "bulk-") {
				registered = true
			}
		}
		pendingCmdsMu.Unlock()
		if registered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker never registered its waiter")
		}
		time.Sleep(time.Millisecond)
	}

	cancel()
	unlock()
	results := awaitBulk(t, done)

	if len(results) != 1 || results[0].MachineID != ids[0] {
		t.Fatalf("unexpected results: %+v", results)
	}
	if r := results[0]; r.Error != bulkCanceledUnsentError || r.Accepted || r.Success {
		t.Fatalf("command written after cancellation, or misreported: %+v", r)
	}
	agents.assertNothingElseSent(t, s, ids)
	assertNoLeakedBulkWaiters(t)
}

// TestBulkCommandUncanceledStillReportsNormally guards the existing paths:
// a self-terminating command that gets no reply is still accepted after the
// grace, and an ordinary command that replies keeps its reply.
func TestBulkCommandUncanceledStillReportsNormally(t *testing.T) {
	_, s := setupTestServer(t)
	_, received := commandFeedbackAgent(t, s, "bulk-normal", true)

	_, done := serveBulkAsync(t, s, `{"machine_ids":["bulk-normal","bulk-offline"],"type":"restart_service","target":"bloxos-agent"}`)
	results := awaitBulk(t, done)
	if len(results) != 2 {
		t.Fatalf("bad results: %+v", results)
	}
	for _, r := range results {
		switch r.MachineID {
		case "bulk-normal":
			if !r.Accepted || r.Success || r.Error != "" {
				t.Fatalf("self restart must be accepted: %+v", r)
			}
		case "bulk-offline":
			if r.Accepted || r.Error != "agent not connected" {
				t.Fatalf("offline target: %+v", r)
			}
		default:
			t.Fatalf("unexpected machine: %+v", r)
		}
	}
	select {
	case <-received:
	case <-time.After(bulkTestDeadline):
		t.Fatal("accepted without sending")
	}

	_, done = serveBulkAsync(t, s, `{"machine_ids":["bulk-normal"],"type":"restart_service","target":"nginx"}`)
	results = awaitBulk(t, done)
	if len(results) != 1 || !results[0].Success || results[0].Accepted || results[0].Error != "" {
		t.Fatalf("ordinary reply must be preserved: %+v", results)
	}
	assertNoLeakedBulkWaiters(t)
}
