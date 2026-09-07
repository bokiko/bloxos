package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

func TestCommandMayDisconnectAgent(t *testing.T) {
	for _, tc := range []struct {
		os, command, target string
		want                bool
	}{
		{"linux", "restart_service", "bloxos-agent", true},
		{"linux", "stop_service", "bloxos-agent.service", true},
		{"windows", "restart_service", "BloxOSAgent", true},
		{"windows", "stop_service", "bloxosagent", true},
		{"linux", "reboot", "", true},
		{"windows", "shutdown", "", true},
		{"linux", "restart_service", "nginx", false},
		{"linux", "start_service", "bloxos-agent", false},
		{"windows", "restart_service", "bloxos-agent", false},
		{"linux", "restart_service", "BloxOSAgent", false},
		{"", "restart_service", "bloxos-agent", false},
		{"", "reboot", "", false},
	} {
		if got := commandMayDisconnectAgent(tc.os, tc.command, tc.target); got != tc.want {
			t.Errorf("%+v: got %v", tc, got)
		}
	}
}

// The mock consumes commands, but deliberately cannot acknowledge its own
// restart. Other commands return the supplied response via the normal waiter.
func commandFeedbackAgent(t *testing.T, s *Server, machineID string, success bool) (*websocket.Conn, <-chan CommandToAgent) {
	t.Helper()
	received := make(chan CommandToAgent, 8)
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
			received <- cmd
			if commandMayDisconnectAgent("linux", cmd.Type, cmd.Target) && cmd.Type != "reboot" {
				continue
			}
			pendingCmdsMu.Lock()
			ch := pendingCmds[cmd.ID]
			pendingCmdsMu.Unlock()
			if ch != nil {
				resp := CommandResponse{ID: cmd.ID, Success: success, Output: "agent reply"}
				if !success {
					resp.Error = "denied"
				}
				ch <- resp
			}
		}
	}))
	t.Cleanup(ws.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ws.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	s.agentsMu.Lock()
	s.agents[machineID] = &ConnectedAgent{MachineID: machineID, Conn: conn}
	s.agentsMu.Unlock()
	s.seedTestMachine(t, machineID)
	if _, err := s.db.Exec("UPDATE machines SET os = 'linux/amd64' WHERE id = ?", machineID); err != nil {
		t.Fatal(err)
	}
	return conn, received
}

func TestCommandFeedback(t *testing.T) {
	_, s := setupTestServer(t)
	conn, received := commandFeedbackAgent(t, s, "feedback", false)
	e := echo.New()
	e.POST("/machines/:id/command", s.handleCommand)
	e.POST("/bulk", s.handleBulkCommand)
	request := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		w := httptest.NewRecorder()
		start := time.Now()
		e.ServeHTTP(w, r)
		if time.Since(start) > disconnectCommandGrace+time.Second {
			t.Fatal("command waited for a response from a restarting agent")
		}
		return w
	}

	w := request("/machines/feedback/command", `{"type":"restart_service","target":"bloxos-agent"}`)
	var resp CommandResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusAccepted || !resp.Accepted || resp.Success || resp.ID == "" {
		t.Fatalf("self restart must be sent, not completed: %d %s", w.Code, w.Body)
	}
	select {
	case cmd := <-received:
		if cmd.ID != resp.ID {
			t.Fatal("accepted ID differs from sent command")
		}
	case <-time.After(time.Second):
		t.Fatal("accepted without sending")
	}
	pendingCmdsMu.Lock()
	_, leaked := pendingCmds[resp.ID]
	pendingCmdsMu.Unlock()
	if leaked {
		t.Fatal("accepted command leaked a waiter")
	}

	w = request("/machines/feedback/command", `{"type":"reboot","target":""}`)
	resp = CommandResponse{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || resp.Accepted || resp.Error != "denied" {
		t.Fatalf("immediate reboot failure must not be hidden: %d %s", w.Code, w.Body)
	}

	w = request("/machines/feedback/command", `{"type":"restart_service","target":"nginx"}`)
	resp = CommandResponse{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || resp.Accepted || resp.Success || resp.Error != "denied" {
		t.Fatalf("ordinary failure must remain a failure: %d %s", w.Code, w.Body)
	}

	w = request("/bulk", `{"machine_ids":["feedback","offline"],"type":"restart_service","target":"bloxos-agent"}`)
	var bulk struct {
		Results []struct {
			MachineID string `json:"machine_id"`
			Accepted  bool   `json:"accepted"`
			Success   bool   `json:"success"`
			Error     string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &bulk); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || len(bulk.Results) != 2 {
		t.Fatalf("bad bulk: %s", w.Body)
	}
	for _, result := range bulk.Results {
		if result.MachineID == "feedback" && (!result.Accepted || result.Success) {
			t.Fatalf("bad acceptance: %+v", result)
		}
		if result.MachineID == "offline" && (result.Accepted || result.Error == "") {
			t.Fatalf("bad offline result: %+v", result)
		}
	}

	conn.Close()
	w = request("/machines/feedback/command", `{"type":"restart_service","target":"bloxos-agent"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("failed socket write cannot be accepted: %d", w.Code)
	}
}
