package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// enrollAndCaptureSecret enrolls a fresh agent for machineID via a valid token
// and returns the durable secret. It drains any agent_version frames the hub
// sends on registration until it sees the "enrolled" frame.
func (s *Server) enrollAndCaptureSecret(t *testing.T, server *httptest.Server, token, machineID string) string {
	t.Helper()
	conn, err := wsDialAgent(t, server, "token="+token)
	if err != nil {
		t.Fatalf("enroll dial: %v", err)
	}
	sendMetricsMsg(t, conn, machineID)

	var secret string
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for secret == "" {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read on enroll conn: %v", err)
		}
		var probe struct {
			Type        string `json:"type"`
			AgentSecret string `json:"agent_secret"`
		}
		if err := json.Unmarshal(msg, &probe); err != nil {
			continue
		}
		if probe.Type == "enrolled" {
			secret = probe.AgentSecret
		}
	}
	conn.Close()
	s.waitAgentDrain(t, machineID, 2*time.Second)
	return secret
}

// TestAuthedAgentCannotWriteForeignMachineID locks in the identity-binding fix:
// once a WebSocket is authenticated as machine A (via durable secret), any
// later frame that claims a different machine_id must be ignored, for every
// message type. Without the guard, one compromised fleet member can overwrite
// another machine's row/metrics/inventory and suppress its alerts.
func TestAuthedAgentCannotWriteForeignMachineID(t *testing.T) {
	e, s := setupTestServer(t)
	token := s.seedValidToken(t)
	server := httptest.NewServer(e)
	defer server.Close()

	secret := s.enrollAndCaptureSecret(t, server, token, "machine-A")

	// Reconnect authenticated as machine-A.
	conn, err := wsDialAgent(t, server, "secret="+secret)
	if err != nil {
		t.Fatalf("reconnect as A: %v", err)
	}
	defer conn.Close()

	writeJSONFrame := func(v map[string]interface{}) {
		data, _ := json.Marshal(v)
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			t.Fatalf("write frame: %v", err)
		}
	}

	// Forge one frame per sink we changed, each claiming a foreign machine-B.
	writeJSONFrame(map[string]interface{}{
		"type": "metrics", "machine_id": "machine-B",
		"hostname": "attacker", "ip": "10.9.9.9", "os": "linux",
		"cpu_percent": 1.0, "ram_used_bytes": 1, "ram_total_bytes": 2,
		"disk_used_bytes": 1, "disk_total_bytes": 2,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
	writeJSONFrame(map[string]interface{}{
		"type": "hardware_info", "machine_id": "machine-B", "cpu": "forged",
	})
	writeJSONFrame(map[string]interface{}{
		"type": "services", "machine_id": "machine-B",
		"services": []map[string]interface{}{
			{"name": "sshd", "status": "running", "description": "forged"},
		},
	})
	writeJSONFrame(map[string]interface{}{
		"type": "containers", "machine_id": "machine-B",
		"containers": []map[string]interface{}{
			{"id": "deadbeef", "name": "forged", "status": "up", "image": "evil:latest"},
		},
	})

	// Send a legitimate metrics frame for machine-A with a sentinel hostname.
	// Frames on one connection are processed in order, so once the sentinel is
	// visible the forged frames have already been handled (and rejected).
	writeJSONFrame(map[string]interface{}{
		"type": "metrics", "machine_id": "machine-A",
		"hostname": "sentinel-host", "ip": "10.0.0.1", "os": "linux",
		"cpu_percent": 2.0, "ram_used_bytes": 1, "ram_total_bytes": 2,
		"disk_used_bytes": 1, "disk_total_bytes": 2,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})

	// Wait until the sentinel A-write lands.
	deadline := time.Now().Add(3 * time.Second)
	for {
		var hostname string
		_ = s.db.QueryRow(`SELECT hostname FROM machines WHERE id = ?`, "machine-A").Scan(&hostname)
		if hostname == "sentinel-host" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sentinel A-write never landed (hostname=%q)", hostname)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// machine-B must not exist and must have no metrics.
	var machineCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM machines WHERE id = ?`, "machine-B").Scan(&machineCount); err != nil {
		t.Fatalf("count machine-B: %v", err)
	}
	if machineCount != 0 {
		t.Fatalf("machine-B row was created by a foreign-id frame (count=%d)", machineCount)
	}
	var metricCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM metrics WHERE machine_id = ?`, "machine-B").Scan(&metricCount); err != nil {
		t.Fatalf("count machine-B metrics: %v", err)
	}
	if metricCount != 0 {
		t.Fatalf("machine-B metrics were written by a foreign-id frame (count=%d)", metricCount)
	}

	// Every sink we changed must be locked down, not just metrics.
	for _, tbl := range []string{"services", "containers"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM `+tbl+` WHERE machine_id = ?`, "machine-B").Scan(&n); err != nil {
			t.Fatalf("count machine-B %s: %v", tbl, err)
		}
		if n != 0 {
			t.Fatalf("machine-B %s rows were written by a foreign-id frame (count=%d)", tbl, n)
		}
	}
}

// TestAgentWSInvalidTokenRejectedBeforeUpgrade locks in the pre-upgrade
// rejection: a caller with no durable secret and an invalid/expired/used token
// must be refused at the HTTP layer (401) rather than being upgraded and then
// held for the auth window. No token is seeded, so any token is invalid.
func TestAgentWSInvalidTokenRejectedBeforeUpgrade(t *testing.T) {
	e, _ := setupTestServer(t)
	server := httptest.NewServer(e)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/agent?token=bogus-invalid-token"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		conn.Close()
		t.Fatal("expected upgrade to be rejected for invalid token with no secret")
	}
	if resp == nil {
		t.Fatalf("expected an HTTP response (401) before upgrade, got none (err=%v)", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected HTTP 401 before upgrade, got %d (err=%v)", resp.StatusCode, err)
	}
}

// TestAgentWSIdleUpgradeIsDropped locks in the invalid-upgrade/idle fix: a
// client that completes the WebSocket upgrade but never sends its first
// authenticating frame must be dropped after the auth window rather than
// holding a socket open indefinitely.
func TestAgentWSIdleUpgradeIsDropped(t *testing.T) {
	e, s := setupTestServer(t)
	token := s.seedValidToken(t)

	prev := agentAuthWindow
	agentAuthWindow = 300 * time.Millisecond
	t.Cleanup(func() { agentAuthWindow = prev })

	server := httptest.NewServer(e)
	defer server.Close()

	// Upgrade succeeds (valid token) but we deliberately send no frame.
	conn, err := wsDialAgent(t, server, "token="+token)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Give the client a generous read deadline. If the hub enforces the auth
	// window it will close the socket at ~300ms; if it does not, the read
	// blocks until this client-side deadline (~3s). Distinguish by elapsed time.
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	start := time.Now()
	_, _, readErr := conn.ReadMessage()
	elapsed := time.Since(start)

	if readErr == nil {
		t.Fatal("expected the hub to close the idle socket, but a frame was read")
	}
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("hub did not drop idle socket within the auth window; read returned after %v (client-side timeout, not a server close)", elapsed)
	}
}

// TestHandleCloseTerminalReleasesWaiter locks in the terminal-close lifecycle
// fix: the close endpoint must route through cleanupTerminalSession (or
// otherwise close session.Done) so a goroutine parked on the session's Done
// channel is released. The old handler deleted the map entry and closed the
// sockets but never closed Done, leaking the parked handleTerminalWS goroutine.
func TestHandleCloseTerminalReleasesWaiter(t *testing.T) {
	e, s := setupTestServer(t)

	sid := "term-close-release"
	session := &TerminalSession{
		ID:           sid,
		MachineID:    "machine-x",
		UserID:       "test-admin-id",
		CreatedAt:    time.Now(),
		LastActivity: time.Now(),
		Done:         make(chan struct{}),
	}
	termSessionsMu.Lock()
	termSessions[sid] = session
	termSessionsMu.Unlock()

	released := make(chan struct{})
	go func() {
		<-waitForSessionEnd(sid)
		close(released)
	}()
	// Let the waiter park.
	time.Sleep(50 * time.Millisecond)

	req := httptest.NewRequest(http.MethodDelete, "/api/terminal/"+sid, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("session_id")
	c.SetParamValues(sid)
	if err := s.handleCloseTerminal(c); err != nil {
		t.Fatalf("handleCloseTerminal: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from close, got %d", rec.Code)
	}

	select {
	case <-released:
		// good — Done was closed.
	case <-time.After(2 * time.Second):
		t.Fatal("waiter parked on session.Done was not released by handleCloseTerminal (goroutine would leak)")
	}
}
