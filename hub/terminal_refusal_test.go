package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Agent refusals of start_terminal (no non-root terminal user, credential
// setup, PTY) arrive as a command_response carrying the start_terminal
// command id, which has no pending waiter. These tests prove the reason
// reaches the browser and the session closes, whichever side arrives first.

func seedRefusalSession(t *testing.T, s *Server, sid, machineID string) *TerminalSession {
	t.Helper()
	now := time.Now()
	session := &TerminalSession{
		ID:            sid,
		MachineID:     machineID,
		TerminalToken: "tok-" + sid,
		UserID:        "user-refusal",
		CreatedAt:     now,
		LastActivity:  now,
		Done:          make(chan struct{}),
	}
	termSessionsMu.Lock()
	termSessions[sid] = session
	termSessionsMu.Unlock()
	t.Cleanup(func() { s.cleanupTerminalSession(sid) })
	return session
}

func dialRefusalBrowser(t *testing.T, server *httptest.Server, sid string) *websocket.Conn {
	t.Helper()
	tok, err := generateTerminalBrowserToken(sid, "user-refusal")
	if err != nil {
		t.Fatal(err)
	}
	conn, resp, err := dialWSWithHeader(t, server, "/ws/terminal/"+sid+"?role=browser&browser_token="+tok, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("browser dial: %v (status=%d)", err, status)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func expectRefusalThenClose(t *testing.T, conn *websocket.Conn, reason string) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	msgType, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("browser did not receive the refusal: %v", err)
	}
	if msgType != websocket.TextMessage || !strings.Contains(string(msg), reason) {
		t.Fatalf("browser got type=%d %q, want the refusal text containing %q", msgType, msg, reason)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("browser socket still open after the refusal")
	}
}

func sessionExists(sid string) bool {
	termSessionsMu.RLock()
	defer termSessionsMu.RUnlock()
	_, ok := termSessions[sid]
	return ok
}

// TestTerminalRefusalReachesConnectedBrowser: browser is already waiting
// when the agent's refusal arrives.
func TestTerminalRefusalReachesConnectedBrowser(t *testing.T) {
	e, s := setupTestServer(t)
	server := httptest.NewServer(e)
	defer server.Close()
	sid := "refusal-browser-first-session"
	seedRefusalSession(t, s, sid, "machine-refusal")
	conn := dialRefusalBrowser(t, server, sid)

	reason := "no non-root terminal user found; set BLOXOS_TERMINAL_USER to an existing non-root account"
	resp := CommandResponse{Type: "command_response", ID: terminalCommandIDPrefix + sid[:terminalCommandIDChars], Error: reason}
	if !s.failTerminalSessionFromAgent("machine-refusal", resp) {
		t.Fatal("refusal was not routed to the session")
	}
	expectRefusalThenClose(t, conn, reason)
	if sessionExists(sid) {
		t.Fatal("session still registered after refusal")
	}
}

// TestTerminalRefusalBeforeBrowserConnects: the agent answers within
// milliseconds of the start request, usually before the browser has opened
// its socket; the browser must still be told on connect.
func TestTerminalRefusalBeforeBrowserConnects(t *testing.T) {
	e, s := setupTestServer(t)
	server := httptest.NewServer(e)
	defer server.Close()
	sid := "refusal-agent-first-session"
	seedRefusalSession(t, s, sid, "machine-refusal")

	reason := `BLOXOS_TERMINAL_USER="ghost": user: unknown user ghost`
	resp := CommandResponse{Type: "command_response", ID: terminalCommandIDPrefix + sid[:terminalCommandIDChars], Error: reason}
	if !s.failTerminalSessionFromAgent("machine-refusal", resp) {
		t.Fatal("refusal was not routed to the session")
	}
	if !sessionExists(sid) {
		t.Fatal("session dropped before the browser could be told")
	}
	conn := dialRefusalBrowser(t, server, sid)
	expectRefusalThenClose(t, conn, reason)
	if sessionExists(sid) {
		t.Fatal("session still registered after refusal")
	}
}

// TestTerminalRefusalIgnoredWhenNotOwnerOrAlreadyRelaying: only the
// session's machine may fail it, and a session whose agent has connected
// belongs to the relay.
func TestTerminalRefusalIgnoredWhenNotOwnerOrAlreadyRelaying(t *testing.T) {
	_, s := setupTestServer(t)
	sid := "refusal-guard-session-1"
	session := seedRefusalSession(t, s, sid, "machine-refusal")
	resp := CommandResponse{Type: "command_response", ID: terminalCommandIDPrefix + sid[:terminalCommandIDChars], Error: "nope"}

	if s.failTerminalSessionFromAgent("machine-other", resp) {
		t.Fatal("another machine failed the session")
	}
	if s.failTerminalSessionFromAgent("machine-refusal", CommandResponse{ID: resp.ID}) {
		t.Fatal("a success response failed the session")
	}
	session.mu.Lock()
	session.AgentWS = &websocket.Conn{}
	session.mu.Unlock()
	if s.failTerminalSessionFromAgent("machine-refusal", resp) {
		t.Fatal("a session with a connected agent was failed")
	}
	session.mu.Lock()
	session.AgentWS = nil
	reasonSet := session.FailReason
	session.mu.Unlock()
	if reasonSet != "" {
		t.Fatalf("FailReason set by an ignored response: %q", reasonSet)
	}
	if !sessionExists(sid) {
		t.Fatal("session removed by an ignored response")
	}
}
