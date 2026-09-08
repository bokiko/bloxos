//go:build linux

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// runToError runs a shell fragment and returns the resulting error, so the
// tests exercise real *exec.ExitError values rather than hand-built ones.
func runToError(t *testing.T, script string) error {
	t.Helper()
	return exec.Command("sh", "-c", script).Run()
}

// TestErrKilledBySignal distinguishes a teardown signal from an ordinary
// failure: SIGTERM/SIGKILL qualify (a successful self-restart kills the
// child), while a non-zero exit code, another signal, and a nil error do not.
func TestErrKilledBySignal(t *testing.T) {
	if err := runToError(t, "kill -TERM $$"); !errKilledBySignal(err) {
		t.Errorf("SIGTERM death: errKilledBySignal(%v) = false, want true", err)
	}
	if err := runToError(t, "kill -KILL $$"); !errKilledBySignal(err) {
		t.Errorf("SIGKILL death: errKilledBySignal(%v) = false, want true", err)
	}
	if err := runToError(t, "exit 1"); errKilledBySignal(err) {
		t.Errorf("exit 1: errKilledBySignal(%v) = true, want false", err)
	}
	if err := runToError(t, "exit 5"); errKilledBySignal(err) {
		t.Errorf("exit 5: errKilledBySignal(%v) = true, want false", err)
	}
	// A different signal (SIGINT) is not a systemd teardown; report it.
	if err := runToError(t, "kill -INT $$"); errKilledBySignal(err) {
		t.Errorf("SIGINT death: errKilledBySignal(%v) = true, want false", err)
	}
	if errKilledBySignal(nil) {
		t.Error("nil error: errKilledBySignal(nil) = true, want false")
	}
}

// hubForCommand starts a fake hub socket, returns the connected agent side
// and a channel that receives the next message the agent writes.
func hubForCommand(t *testing.T) (*websocket.Conn, *sync.Mutex, chan []byte) {
	t.Helper()
	got := make(chan []byte, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		if _, msg, err := ws.ReadMessage(); err == nil {
			got <- msg
		}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn, &sync.Mutex{}, got
}

// reportCommandOutcome runs the exact production decision
// (shouldSuppressTeardownReply) against real *exec.ExitError values, then
// writes (or suppresses) the reply the way handleCommand does. Only the write
// wrapper is test-local; the branch under test is the shipped one.
func reportCommandOutcome(conn *websocket.Conn, mu *sync.Mutex, cmd Command, ctxErr error, runErr error) {
	if shouldSuppressTeardownReply(cmd.Type, cmd.Target, ctxErr, runErr) {
		return
	}
	resp := CommandResponse{Type: "command_response", ID: cmd.ID, Success: runErr == nil}
	if runErr != nil {
		resp.Error = runErr.Error()
	}
	writeJSON(conn, mu, resp)
}

// TestSelfRestartSignalDeathSuppressesFailureReply: a self-restart whose
// child dies by SIGTERM sends no command_response, so the hub's disconnect
// grace yields the truthful 202 instead of a 200 failure.
func TestSelfRestartSignalDeathSuppressesFailureReply(t *testing.T) {
	conn, mu, got := hubForCommand(t)
	err := runToError(t, "kill -TERM $$")
	reportCommandOutcome(conn, mu, Command{Type: "restart_service", Target: "bloxos-agent", ID: "cmd-1"}, nil, err)
	select {
	case msg := <-got:
		t.Fatalf("a reply was sent for a self-restart signal death: %s", msg)
	case <-time.After(500 * time.Millisecond):
	}
}

// TestSelfRestartOrdinaryFailureIsReported: a self-restart that fails with a
// status code (unit unknown, permission denied) still reports success=false,
// so a genuine failure is never hidden behind the 202.
func TestSelfRestartOrdinaryFailureIsReported(t *testing.T) {
	conn, mu, got := hubForCommand(t)
	err := runToError(t, "exit 5")
	reportCommandOutcome(conn, mu, Command{Type: "restart_service", Target: "bloxos-agent", ID: "cmd-2"}, nil, err)
	select {
	case msg := <-got:
		var resp CommandResponse
		if e := json.Unmarshal(msg, &resp); e != nil {
			t.Fatalf("bad reply %q: %v", msg, e)
		}
		if resp.Success || resp.Error == "" {
			t.Fatalf("ordinary failure not reported: %+v", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no reply sent for an ordinary self-restart failure")
	}
}

// TestOtherServiceSignalDeathIsReported: a signal death restarting a
// *different* service is a real failure and must be reported, not suppressed.
func TestOtherServiceSignalDeathIsReported(t *testing.T) {
	conn, mu, got := hubForCommand(t)
	err := runToError(t, "kill -TERM $$")
	reportCommandOutcome(conn, mu, Command{Type: "restart_service", Target: "nginx", ID: "cmd-3"}, nil, err)
	select {
	case msg := <-got:
		var resp CommandResponse
		if e := json.Unmarshal(msg, &resp); e != nil {
			t.Fatalf("bad reply %q: %v", msg, e)
		}
		if resp.Success {
			t.Fatalf("other-service signal death reported as success: %+v", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no reply sent for another service's failure")
	}
}

// TestSelfRestartTimeoutIsReported: when the deadline fired (ctx error set),
// even a signal death is surfaced as an error, never suppressed.
func TestSelfRestartTimeoutIsReported(t *testing.T) {
	conn, mu, got := hubForCommand(t)
	err := runToError(t, "kill -KILL $$")
	reportCommandOutcome(conn, mu, Command{Type: "reboot", ID: "cmd-4"}, context.DeadlineExceeded, err)
	select {
	case msg := <-got:
		var resp CommandResponse
		if e := json.Unmarshal(msg, &resp); e != nil {
			t.Fatalf("bad reply %q: %v", msg, e)
		}
		if resp.Success {
			t.Fatalf("timed-out command reported as success: %+v", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no reply sent for a timed-out command")
	}
}
