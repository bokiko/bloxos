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

// dialWSWithHeader dials an arbitrary hub WebSocket path with custom handshake
// headers. A successful dial (101 Switching Protocols) means the hub accepted
// the credentials before upgrade; a rejected dial returns an error plus the
// HTTP response.
func dialWSWithHeader(t *testing.T, server *httptest.Server, path string, h http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + path
	return websocket.DefaultDialer.Dial(wsURL, h)
}

// TestAgentWSAuthViaSecretHeader proves /ws/agent authenticates a durable
// secret presented in the Authorization header, with no query credentials.
func TestAgentWSAuthViaSecretHeader(t *testing.T) {
	e := setupTestServer(t)
	server := httptest.NewServer(e)
	defer server.Close()

	token := seedValidToken(t)
	secret := enrollAndCaptureSecret(t, server, token, "machine-hdr-secret")
	if secret == "" {
		t.Fatal("enrollment did not yield a secret")
	}

	h := http.Header{}
	h.Set("Authorization", "Bearer "+secret)
	conn, resp, err := dialWSWithHeader(t, server, "/ws/agent", h)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial with Authorization header failed: %v (status=%d)", err, status)
	}
	defer conn.Close()

	// A secret-authenticated connection is registered at upgrade time.
	deadline := time.Now().Add(2 * time.Second)
	for {
		agentsMu.RLock()
		_, ok := agents["machine-hdr-secret"]
		agentsMu.RUnlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("agent authenticated via secret header was never registered")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAgentWSEnrollViaTokenHeader proves /ws/agent performs token enrollment
// when the install token is presented in the X-Bloxos-Enroll-Token header.
func TestAgentWSEnrollViaTokenHeader(t *testing.T) {
	e := setupTestServer(t)
	server := httptest.NewServer(e)
	defer server.Close()

	token := seedValidToken(t)
	h := http.Header{}
	h.Set("X-Bloxos-Enroll-Token", token)
	conn, resp, err := dialWSWithHeader(t, server, "/ws/agent", h)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial with enroll-token header failed: %v (status=%d)", err, status)
	}
	defer conn.Close()

	sendMetricsMsg(t, conn, "machine-hdr-enroll")

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var secret string
	for secret == "" {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read enrolled frame: %v", err)
		}
		var p struct {
			Type        string `json:"type"`
			AgentSecret string `json:"agent_secret"`
		}
		if err := json.Unmarshal(msg, &p); err != nil {
			continue
		}
		if p.Type == "enrolled" {
			secret = p.AgentSecret
		}
	}
	if secret == "" {
		t.Fatal("header-based enrollment did not issue a secret")
	}
}

// TestAgentWSSecretQueryFallback proves the deprecated query-param credential
// path still authenticates, so agents predating the header change keep working.
func TestAgentWSSecretQueryFallback(t *testing.T) {
	e := setupTestServer(t)
	server := httptest.NewServer(e)
	defer server.Close()

	token := seedValidToken(t)
	secret := enrollAndCaptureSecret(t, server, token, "machine-qp-secret")

	conn, resp, err := dialWSWithHeader(t, server, "/ws/agent?secret="+secret, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("query-param secret fallback failed: %v (status=%d)", err, status)
	}
	defer conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for {
		agentsMu.RLock()
		_, ok := agents["machine-qp-secret"]
		agentsMu.RUnlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("agent authenticated via query-param secret was never registered")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestTerminalAgentTokenViaHeader proves /ws/terminal role=agent accepts the
// terminal token in the X-Bloxos-Terminal-Token header.
func TestTerminalAgentTokenViaHeader(t *testing.T) {
	e := setupTestServer(t)
	server := httptest.NewServer(e)
	defer server.Close()

	sid := "term-hdr-session"
	now := time.Now()
	termSessionsMu.Lock()
	termSessions[sid] = &TerminalSession{
		ID:            sid,
		MachineID:     "machine-term",
		TerminalToken: "tok-header-abc",
		CreatedAt:     now,
		LastActivity:  now,
		Done:          make(chan struct{}),
	}
	termSessionsMu.Unlock()
	t.Cleanup(func() { cleanupTerminalSession(sid) })

	h := http.Header{}
	h.Set("X-Bloxos-Terminal-Token", "tok-header-abc")
	conn, resp, err := dialWSWithHeader(t, server, "/ws/terminal/"+sid+"?role=agent", h)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("terminal agent dial with token header failed: %v (status=%d)", err, status)
	}
	conn.Close()
}

// TestTerminalAgentTokenQueryFallback proves the deprecated query-param
// terminal_token still authenticates the agent role.
func TestTerminalAgentTokenQueryFallback(t *testing.T) {
	e := setupTestServer(t)
	server := httptest.NewServer(e)
	defer server.Close()

	sid := "term-qp-session"
	now := time.Now()
	termSessionsMu.Lock()
	termSessions[sid] = &TerminalSession{
		ID:            sid,
		MachineID:     "machine-term",
		TerminalToken: "tok-qp-abc",
		CreatedAt:     now,
		LastActivity:  now,
		Done:          make(chan struct{}),
	}
	termSessionsMu.Unlock()
	t.Cleanup(func() { cleanupTerminalSession(sid) })

	conn, resp, err := dialWSWithHeader(t, server, "/ws/terminal/"+sid+"?role=agent&terminal_token=tok-qp-abc", nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("terminal agent query-param fallback failed: %v (status=%d)", err, status)
	}
	conn.Close()
}

// TestTerminalAgentInvalidTokenHeaderRejected proves a wrong header token is
// rejected before upgrade.
func TestTerminalAgentInvalidTokenHeaderRejected(t *testing.T) {
	e := setupTestServer(t)
	server := httptest.NewServer(e)
	defer server.Close()

	sid := "term-bad-session"
	now := time.Now()
	termSessionsMu.Lock()
	termSessions[sid] = &TerminalSession{
		ID:            sid,
		TerminalToken: "tok-real",
		CreatedAt:     now,
		LastActivity:  now,
		Done:          make(chan struct{}),
	}
	termSessionsMu.Unlock()
	t.Cleanup(func() { cleanupTerminalSession(sid) })

	h := http.Header{}
	h.Set("X-Bloxos-Terminal-Token", "tok-wrong")
	conn, resp, err := dialWSWithHeader(t, server, "/ws/terminal/"+sid+"?role=agent", h)
	if err == nil {
		conn.Close()
		t.Fatal("expected rejection for wrong terminal token, dial succeeded")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		got := 0
		if resp != nil {
			got = resp.StatusCode
		}
		t.Fatalf("expected 401 for wrong terminal token, got %d", got)
	}
}
