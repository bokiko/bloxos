//go:build linux

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestStartTerminalRefusalIsReportedToHub: when no usable terminal user
// exists, the agent answers the start_terminal command with an error the
// hub can show the operator, instead of only logging and leaving the
// browser on a blank terminal.
func TestStartTerminalRefusalIsReportedToHub(t *testing.T) {
	received := make(chan []byte, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		_, msg, err := ws.ReadMessage()
		if err == nil {
			received <- msg
		}
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	oldHub := hubURL
	hubURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/agent"
	defer func() { hubURL = oldHub }()
	t.Setenv("BLOXOS_TERMINAL_USER", "bloxos-ghost-user-e2")

	raw := []byte(`{"type":"start_terminal","id":"term-abcdef12","session_id":"abcdef12-session","terminal_token":"tok"}`)
	var mu sync.Mutex
	handleStartTerminal(conn, &mu, Command{Type: "start_terminal", ID: "term-abcdef12"}, raw)

	select {
	case msg := <-received:
		var resp CommandResponse
		if err := json.Unmarshal(msg, &resp); err != nil {
			t.Fatalf("bad response %q: %v", msg, err)
		}
		if resp.Type != "command_response" || resp.ID != "term-abcdef12" || resp.Success {
			t.Fatalf("unexpected response: %+v", resp)
		}
		if !strings.Contains(resp.Error, "bloxos-ghost-user-e2") {
			t.Fatalf("error does not name the configured account: %q", resp.Error)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hub never received the refusal")
	}
}
