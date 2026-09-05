package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bokiko/bloxos/proto/aisessions"
	"github.com/gorilla/websocket"
)

// aiSessionsGet performs GET /api/ai-sessions with the given JWT.
func aiSessionsGet(t *testing.T, e http.Handler, token string) (int, aiSessionsResponse, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/ai-sessions", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	var resp aiSessionsResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode ai-sessions response: %v: %s", err, rec.Body.String())
		}
	}
	return rec.Code, resp, rec.Body.String()
}

func aiSessionsPatch(t *testing.T, e http.Handler, token, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/api/ai-sessions/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func writeFrame(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

// sendSentinelMetrics sends a metrics frame with a distinctive hostname and
// waits until the hub has persisted it. Frames on one socket are processed
// in order, so once the sentinel is visible every earlier frame was handled.
func (s *Server) sendSentinelMetrics(t *testing.T, conn *websocket.Conn, machineID, hostname string) {
	t.Helper()
	writeFrame(t, conn, map[string]any{
		"type": "metrics", "machine_id": machineID, "hostname": hostname, "ip": "10.0.0.1", "os": "linux",
		"cpu_percent": 1.0, "ram_used_bytes": 1, "ram_total_bytes": 2, "disk_used_bytes": 1, "disk_total_bytes": 2,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var got string
		if s.db.QueryRow(`SELECT hostname FROM machines WHERE id = ?`, machineID).Scan(&got) == nil && got == hostname {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("hub never persisted sentinel metrics for %s", machineID)
}

func aiSessionsFrame(machineID string, sessions ...map[string]any) map[string]any {
	if sessions == nil {
		sessions = []map[string]any{}
	}
	return map[string]any{"type": aisessions.MessageType, "machine_id": machineID, "schema": 1, "sessions": sessions}
}

func validSession(id, tool string) map[string]any {
	return map[string]any{
		"id": id, "tool": tool, "started_at": "2026-09-05T10:00:00Z",
		"project":  map[string]any{"value": "bloxos", "source": "cwd", "confidence": "exact"},
		"model":    map[string]any{"value": "claude-opus-5", "source": "argv_flag", "confidence": "exact"},
		"activity": map[string]any{"value": "active", "source": "cpu_time", "confidence": "inferred"},
	}
}

// enrollAgentSecret enrolls machineID through a fresh install token and
// returns its durable secret.
func (s *Server) enrollAgentSecret(t *testing.T, server *httptest.Server, machineID string) string {
	t.Helper()
	if _, err := s.db.Exec(`DELETE FROM tokens`); err != nil {
		t.Fatal(err)
	}
	token := s.seedValidToken(t)
	return s.enrollAndCaptureSecret(t, server, token, machineID)
}

// connectEnrolledAgent enrolls machineID and returns a live, registered
// socket authenticated with its durable secret.
func (s *Server) connectEnrolledAgent(t *testing.T, server *httptest.Server, machineID string) *websocket.Conn {
	t.Helper()
	secret := s.enrollAgentSecret(t, server, machineID)
	conn, err := wsDialAgent(t, server, "secret="+secret)
	if err != nil {
		t.Fatalf("reconnect %s: %v", machineID, err)
	}
	return conn
}

func TestAISessionsIngestAndRead(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAndGetToken(t, e)

	// Default: enabled, nothing reporting.
	code, resp, body := aiSessionsGet(t, e, adminToken)
	if code != http.StatusOK || !resp.Enabled || len(resp.Machines) != 0 {
		t.Fatalf("initial read: %d %s", code, body)
	}
	if !strings.Contains(body, `"machines":[]`) {
		t.Fatalf("empty machines must serialize as []: %s", body)
	}

	conn := s.connectEnrolledAgent(t, server, "machine-A")
	defer conn.Close()
	writeFrame(t, conn, aiSessionsFrame("machine-A", validSession("aaaa0001", "claude"), validSession("aaaa0002", "codex")))
	s.sendSentinelMetrics(t, conn, "machine-A", "host-a")

	code, resp, body = aiSessionsGet(t, e, adminToken)
	if code != http.StatusOK || len(resp.Machines) != 1 {
		t.Fatalf("read after ingest: %d %s", code, body)
	}
	m := resp.Machines[0]
	if m.MachineID != "machine-A" || m.Hostname != "host-a" || len(m.Sessions) != 2 {
		t.Fatalf("unexpected machine view: %+v", m)
	}
	if m.Sessions[0].Tool != "claude" || m.Sessions[0].Project.Value != "bloxos" || m.Sessions[0].Model.Confidence != "exact" || m.Sessions[0].Activity.Value != "active" {
		t.Fatalf("session attrs not preserved: %+v", m.Sessions[0])
	}
	if resp.StaleAfterSeconds != int(aiSessionsStaleAfter/time.Second) {
		t.Fatalf("stale_after_seconds = %d", resp.StaleAfterSeconds)
	}

	// A later report replaces, never accumulates: an empty list means no sessions.
	writeFrame(t, conn, aiSessionsFrame("machine-A"))
	s.sendSentinelMetrics(t, conn, "machine-A", "host-a2")
	_, resp, _ = aiSessionsGet(t, e, adminToken)
	if len(resp.Machines) != 1 || len(resp.Machines[0].Sessions) != 0 {
		t.Fatalf("empty report should clear sessions but keep the machine: %+v", resp.Machines)
	}

	// Disconnect: live sessions only, so the machine disappears.
	conn.Close()
	s.waitAgentDrain(t, "machine-A", 2*time.Second)
	_, resp, _ = aiSessionsGet(t, e, adminToken)
	if len(resp.Machines) != 0 {
		t.Fatalf("disconnected machine still listed: %+v", resp.Machines)
	}
}

// TestAISessionsPrivacyRegressionHub proves that anything outside the
// contract sent by an agent — planted prompts, secrets, full paths, argv,
// env — never reaches the read API, even inside otherwise-valid sessions.
func TestAISessionsPrivacyRegressionHub(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAndGetToken(t, e)

	const (
		// Real API keys carry characters outside the model-id charset
		// (here "/" and "+"), which is what the hub can enforce. An
		// identifier-shaped string planted in the model field is
		// indistinguishable from a model id and passes by design — see
		// TestAISessionsModelFieldIsShapeCheckedOnly.
		secret   = "sk-ant-api03-PLANTED/SECRET+VALUE"
		prompt   = "please rotate the prod password hunter2"
		fullPath = "/home/alice/work/secret-project"
		fullArgv = "claude --dangerously-skip-permissions -p"
	)
	conn := s.connectEnrolledAgent(t, server, "machine-P")
	defer conn.Close()

	frame := aiSessionsFrame("machine-P",
		map[string]any{
			"id": "abcd1234", "tool": "claude",
			"pid": 4242, "cwd": fullPath, "argv": []string{"claude", "-p", prompt}, "cmdline": fullArgv,
			"env": map[string]string{"ANTHROPIC_API_KEY": secret}, "username": "alice", "prompt": prompt,
			"transcript": "user: " + prompt,
			"project":    map[string]any{"value": fullPath, "source": "cwd", "confidence": "exact", "path": fullPath},
			"model":      map[string]any{"value": secret, "source": "argv_flag", "confidence": "exact"},
			"activity":   map[string]any{"value": "typing: " + prompt, "source": "cpu_time", "confidence": "inferred"},
		},
		// Attribute values that are individually acceptable but overclaim confidence
		// or cite a source the contract does not allow.
		map[string]any{
			"id": "abcd5678", "tool": "codex",
			"project":  map[string]any{"value": "alice", "source": "transcript", "confidence": "exact"},
			"model":    map[string]any{"value": "gpt-5-codex", "source": "env", "confidence": "exact"},
			"activity": map[string]any{"value": "active", "source": "cpu_time", "confidence": "exact"},
		},
		map[string]any{"id": "abcd9999", "tool": "cursor", "prompt": prompt},
	)
	frame["prompt"] = prompt
	frame["env"] = map[string]string{"SECRET": secret}
	writeFrame(t, conn, frame)
	s.sendSentinelMetrics(t, conn, "machine-P", "host-p")

	code, resp, body := aiSessionsGet(t, e, adminToken)
	if code != http.StatusOK {
		t.Fatalf("read: %d %s", code, body)
	}
	for _, forbidden := range []string{
		secret, "PLANTED", prompt, "hunter2", fullPath, "/home/", "alice", fullArgv,
		"dangerously", "transcript", "typing", "4242", `"pid"`, `"argv"`, `"cwd":`, `"env"`, `"username"`, `"cmdline"`, `"path"`, "cursor",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("read API leaks %q:\n%s", forbidden, body)
		}
	}
	if len(resp.Machines) != 1 || len(resp.Machines[0].Sessions) != 2 {
		t.Fatalf("expected the two whitelisted sessions to survive: %+v", resp.Machines)
	}
	for _, sess := range resp.Machines[0].Sessions {
		if sess.Project != aisessions.Unknown() || sess.Model != aisessions.Unknown() || sess.Activity != aisessions.Unknown() {
			t.Errorf("attrs outside the contract must collapse to unknown: %+v", sess)
		}
	}
}

// TestAISessionsModelFieldIsShapeCheckedOnly documents a residual risk
// rather than hiding it: the model attribute is a free identifier (letters,
// digits, ".", "_", ":", "-", at most 64 chars) because model ids are not
// enumerable. A compromised agent can therefore push any identifier-shaped
// string there. Values with path separators, spaces or other characters
// are rejected, which is what keeps paths, prompts and typical secrets out.
func TestAISessionsModelFieldIsShapeCheckedOnly(t *testing.T) {
	_, s := setupTestServer(t)
	agent := &ConnectedAgent{}
	s.agentsMu.Lock()
	s.agents["m-shape"] = agent
	s.agentsMu.Unlock()
	t.Cleanup(func() {
		s.agentsMu.Lock()
		delete(s.agents, "m-shape")
		s.agentsMu.Unlock()
	})
	frame := func(model string) []byte {
		raw, _ := json.Marshal(aiSessionsFrame("m-shape", map[string]any{
			"id": "01", "tool": "claude",
			"model": map[string]any{"value": model, "source": "argv_flag", "confidence": "exact"},
		}))
		return raw
	}
	s.ingestAISessions("m-shape", agent, frame("identifier-shaped-but-not-a-model-123"))
	if got := s.aiSessions.live()["m-shape"].Sessions[0].Model.Value; got != "identifier-shaped-but-not-a-model-123" {
		t.Fatalf("identifier-shaped value should pass shape check, got %q", got)
	}
	for _, bad := range []string{"/etc/passwd", "opus sonnet", "sk-ant/key+", "a=b", strings.Repeat("x", 65), "$HOME"} {
		s.ingestAISessions("m-shape", agent, frame(bad))
		if got := s.aiSessions.live()["m-shape"].Sessions[0].Model; got != aisessions.Unknown() {
			t.Errorf("model %q should be rejected, got %+v", bad, got)
		}
	}
}

// TestAISessionsBoundToAuthenticatedMachine covers spoofing: a socket
// authenticated as A cannot plant sessions on B, and a frame's own
// machine_id (even empty) never overrides the authenticated identity.
func TestAISessionsBoundToAuthenticatedMachine(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAndGetToken(t, e)

	connB := s.connectEnrolledAgent(t, server, "machine-B")
	defer connB.Close()
	s.sendSentinelMetrics(t, connB, "machine-B", "host-b")

	connA := s.connectEnrolledAgent(t, server, "machine-A")
	defer connA.Close()

	// Cross-machine claim: dropped entirely.
	writeFrame(t, connA, aiSessionsFrame("machine-B", validSession("b0b0b0b0", "codex")))
	// Empty machine_id: bound to A, not rejected and not orphaned.
	writeFrame(t, connA, aiSessionsFrame("", validSession("a0a0a0a0", "claude")))
	s.sendSentinelMetrics(t, connA, "machine-A", "host-a")

	_, resp, body := aiSessionsGet(t, e, adminToken)
	byID := map[string]aiSessionsMachineView{}
	for _, m := range resp.Machines {
		byID[m.MachineID] = m
	}
	if b, ok := byID["machine-B"]; ok && len(b.Sessions) != 0 {
		t.Fatalf("spoofed sessions landed on machine-B: %s", body)
	}
	a, ok := byID["machine-A"]
	if !ok || len(a.Sessions) != 1 || a.Sessions[0].ID != "a0a0a0a0" {
		t.Fatalf("frame with empty machine_id should bind to the authenticated machine: %s", body)
	}
	if strings.Contains(body, "b0b0b0b0") {
		t.Fatalf("cross-machine session leaked somewhere: %s", body)
	}
}

// TestAISessionsRequireRegisteredConnection: a token-enrolling socket that
// has sent metrics (so machineID is known) but has not committed enrollment
// is not registered and must not be able to publish sessions.
func TestAISessionsRequireRegisteredConnection(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAndGetToken(t, e)

	token := s.seedValidToken(t)
	conn, err := wsDialAgent(t, server, "token="+token)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	sendMetricsMsg(t, conn, "machine-U")
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	readFrameType(t, conn, "enrolled") // secret issued, NOT committed → not registered
	writeFrame(t, conn, aiSessionsFrame("machine-U", validSession("u0u0u0u0", "kimi")))
	s.sendSentinelMetrics(t, conn, "machine-U", "host-u")

	_, _, body := aiSessionsGet(t, e, adminToken)
	if strings.Contains(body, "machine-U") || strings.Contains(body, "u0u0u0u0") {
		t.Fatalf("uncommitted enrollment published sessions: %s", body)
	}

	// Direct unit check of the guard against a socket that is not the registered one.
	s.ingestAISessions("machine-U", &ConnectedAgent{}, []byte(`{"type":"ai_sessions","sessions":[{"id":"ff","tool":"claude"}]}`))
	if live := s.aiSessions.live(); len(live) != 0 {
		t.Fatalf("unregistered agent wrote a snapshot: %+v", live)
	}
	s.ingestAISessions("", &ConnectedAgent{}, []byte(`{"type":"ai_sessions","sessions":[{"id":"ff","tool":"claude"}]}`))
	if live := s.aiSessions.live(); len(live) != 0 {
		t.Fatalf("empty machine id wrote a snapshot: %+v", live)
	}
}

func TestAISessionsStaleSnapshotExpires(t *testing.T) {
	_, s := setupTestServer(t)
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	s.aiSessions.now = func() time.Time { return now }

	s.aiSessions.put("fresh", []aisessions.Session{{ID: "01", Tool: "claude"}})
	now = now.Add(aiSessionsStaleAfter - time.Second)
	s.aiSessions.put("recent", []aisessions.Session{{ID: "02", Tool: "codex"}})

	live := s.aiSessions.live()
	if len(live) != 2 {
		t.Fatalf("both snapshots should be live: %v", live)
	}
	now = now.Add(2 * time.Second) // fresh is now > stale window old; recent is 2s old
	live = s.aiSessions.live()
	if _, ok := live["fresh"]; ok {
		t.Fatalf("stale snapshot still served: %v", live)
	}
	if _, ok := live["recent"]; !ok {
		t.Fatalf("recent snapshot dropped: %v", live)
	}
	// Eviction is real, not just filtered from one read.
	s.aiSessions.mu.RLock()
	_, stillStored := s.aiSessions.byMachine["fresh"]
	s.aiSessions.mu.RUnlock()
	if stillStored {
		t.Fatal("stale snapshot was filtered but not evicted")
	}
	// A fresh report resurrects the machine.
	s.aiSessions.put("fresh", nil)
	if _, ok := s.aiSessions.live()["fresh"]; !ok {
		t.Fatal("new report after expiry not served")
	}
}

func TestAISessionsRBAC(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	s.seedTestUser(t, "viewer-ai", "viewerpass123", "1234", RoleViewer, true, true)
	s.seedTestUser(t, "operator-ai", "operatorpass123", "1234", RoleOperator, true, true)
	viewer := loginAndGetTokenForCredentials(t, e, "viewer-ai", "viewerpass123")
	operator := loginAndGetTokenForCredentials(t, e, "operator-ai", "operatorpass123")
	admin := loginAndGetToken(t, e)

	if code, _, body := aiSessionsGet(t, e, ""); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated read: %d %s", code, body)
	}
	if code, body := aiSessionsPatch(t, e, "", `{"enabled":false}`); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated patch: %d %s", code, body)
	}
	for name, tok := range map[string]string{"viewer": viewer, "operator": operator, "admin": admin} {
		if code, _, body := aiSessionsGet(t, e, tok); code != http.StatusOK {
			t.Fatalf("%s read: %d %s", name, code, body)
		}
	}
	for name, tok := range map[string]string{"viewer": viewer, "operator": operator} {
		code, body := aiSessionsPatch(t, e, tok, `{"enabled":false}`)
		if code != http.StatusForbidden || !strings.Contains(body, scopeFleetAdmin) {
			t.Fatalf("%s patch should be forbidden citing %s: %d %s", name, scopeFleetAdmin, code, body)
		}
	}
	if code, body := aiSessionsPatch(t, e, admin, `{"enabled":false}`); code != http.StatusOK || !strings.Contains(body, `"enabled":false`) {
		t.Fatalf("admin patch: %d %s", code, body)
	}
	if code, body := aiSessionsPatch(t, e, admin, `{}`); code != http.StatusBadRequest {
		t.Fatalf("patch without enabled should be 400: %d %s", code, body)
	}
	if code, body := aiSessionsPatch(t, e, admin, `{"enabled":"yes"}`); code != http.StatusBadRequest {
		t.Fatalf("patch with non-boolean should be 400: %d %s", code, body)
	}
}

func TestAISessionsAdminSwitchDisablesAndClears(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	admin := loginAndGetToken(t, e)

	conn := s.connectEnrolledAgent(t, server, "machine-S")
	defer conn.Close()
	writeFrame(t, conn, aiSessionsFrame("machine-S", validSession("50505050", "claude")))
	s.sendSentinelMetrics(t, conn, "machine-S", "host-s")
	if _, resp, _ := aiSessionsGet(t, e, admin); len(resp.Machines) != 1 {
		t.Fatalf("precondition: expected one machine, got %+v", resp.Machines)
	}

	// Disable: existing snapshot gone at once, persisted across a "restart"
	// of the in-memory view, and new reports are discarded.
	if code, body := aiSessionsPatch(t, e, admin, `{"enabled":false}`); code != http.StatusOK {
		t.Fatalf("disable: %d %s", code, body)
	}
	if live := s.aiSessions.live(); len(live) != 0 {
		t.Fatalf("disable must clear snapshots immediately: %+v", live)
	}
	if s.aiSessionsEnabled() {
		t.Fatal("setting not persisted")
	}
	writeFrame(t, conn, aiSessionsFrame("machine-S", validSession("50505051", "codex")))
	s.sendSentinelMetrics(t, conn, "machine-S", "host-s2")
	code, resp, body := aiSessionsGet(t, e, admin)
	if code != http.StatusOK || resp.Enabled || len(resp.Machines) != 0 {
		t.Fatalf("disabled read: %d %s", code, body)
	}
	if live := s.aiSessions.live(); len(live) != 0 {
		t.Fatalf("report accepted while disabled: %+v", live)
	}

	// Re-enable: the next report shows up again.
	if code, body := aiSessionsPatch(t, e, admin, `{"enabled":true}`); code != http.StatusOK {
		t.Fatalf("enable: %d %s", code, body)
	}
	writeFrame(t, conn, aiSessionsFrame("machine-S", validSession("50505052", "kimi")))
	s.sendSentinelMetrics(t, conn, "machine-S", "host-s3")
	_, resp, body = aiSessionsGet(t, e, admin)
	if !resp.Enabled || len(resp.Machines) != 1 || resp.Machines[0].Sessions[0].ID != "50505052" {
		t.Fatalf("re-enabled read: %s", body)
	}
}

func TestAISessionsSettingDefaultsEnabledWithoutRow(t *testing.T) {
	_, s := setupTestServer(t)
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM hub_settings`).Scan(&n); err != nil {
		t.Fatalf("hub_settings table missing: %v", err)
	}
	if n != 0 || !s.aiSessionsEnabled() {
		t.Fatalf("fresh install must be enabled with no rows (rows=%d)", n)
	}
}

// TestAISessionsProtocolCompatibility: frames from older agents (which
// never send ai_sessions) keep working unchanged; frames from newer agents
// with a higher schema and unknown fields are accepted with the known
// fields only; a malformed frame is ignored without disturbing the socket.
func TestAISessionsProtocolCompatibility(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	admin := loginAndGetToken(t, e)

	conn := s.connectEnrolledAgent(t, server, "machine-C")
	defer conn.Close()

	// Old-agent traffic: services/containers frames as before.
	writeFrame(t, conn, map[string]any{"type": "services", "machine_id": "machine-C",
		"services": []map[string]any{{"name": "sshd", "status": "running", "description": "ok"}}})
	// Malformed ai_sessions payload (sessions is not an array).
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"ai_sessions","machine_id":"machine-C","sessions":"nope"}`)); err != nil {
		t.Fatal(err)
	}
	// Future-schema frame with unknown fields at every level.
	writeFrame(t, conn, map[string]any{
		"type": "ai_sessions", "machine_id": "machine-C", "schema": 7, "future_top": true,
		"sessions": []map[string]any{{
			"id": "c0c0c0c0", "tool": "claude", "future_field": "x",
			"project":  map[string]any{"value": "bloxos", "source": "cwd", "confidence": "exact", "future": 1},
			"model":    map[string]any{"value": "", "source": "none", "confidence": "unknown"},
			"activity": map[string]any{"value": "idle", "source": "cpu_time", "confidence": "inferred"},
			"tokens":   map[string]any{"in": 10},
		}},
	})
	s.sendSentinelMetrics(t, conn, "machine-C", "host-c")

	_, resp, body := aiSessionsGet(t, e, admin)
	if len(resp.Machines) != 1 || len(resp.Machines[0].Sessions) != 1 {
		t.Fatalf("future-schema frame not accepted: %s", body)
	}
	if strings.Contains(body, "future") || strings.Contains(body, "tokens") {
		t.Fatalf("unknown fields leaked through: %s", body)
	}
	sess := resp.Machines[0].Sessions[0]
	if sess.Project.Value != "bloxos" || sess.Activity.Value != "idle" || sess.Model != aisessions.Unknown() {
		t.Fatalf("known fields mangled: %+v", sess)
	}
	// The socket is still healthy and the old-agent frame was stored.
	var svc int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM services WHERE machine_id = 'machine-C'`).Scan(&svc); err != nil || svc != 1 {
		t.Fatalf("services frame lost: n=%d err=%v", svc, err)
	}
}

// TestAISessionsConcurrentIngestReadToggle exercises the store under the race
// detector: many machines reporting while readers list and an admin toggles.
func TestAISessionsConcurrentIngestReadToggle(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	admin := loginAndGetToken(t, e)

	const machines = 8
	agents := make([]*ConnectedAgent, machines)
	for i := range agents {
		agents[i] = &ConnectedAgent{}
		s.agentsMu.Lock()
		s.agents[fmt.Sprintf("m%d", i)] = agents[i]
		s.agentsMu.Unlock()
	}
	t.Cleanup(func() {
		s.agentsMu.Lock()
		for i := range agents {
			delete(s.agents, fmt.Sprintf("m%d", i))
		}
		s.agentsMu.Unlock()
	})

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < machines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("m%d", i)
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				default:
				}
				raw, _ := json.Marshal(aiSessionsFrame(id, validSession(fmt.Sprintf("%08x", n), "claude")))
				s.ingestAISessions(id, agents[i], raw)
				if n%7 == 0 {
					s.aiSessions.remove(id)
				}
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < 20; n++ {
			if code, body := aiSessionsPatch(t, e, admin, fmt.Sprintf(`{"enabled":%v}`, n%2 == 0)); code != http.StatusOK {
				t.Errorf("toggle: %d %s", code, body)
			}
		}
	}()
	for n := 0; n < 50; n++ {
		if code, _, body := aiSessionsGet(t, e, admin); code != http.StatusOK {
			t.Errorf("read: %d %s", code, body)
		}
	}
	close(stop)
	wg.Wait()
	// Leave the switch on for a final consistency check.
	if code, _ := aiSessionsPatch(t, e, admin, `{"enabled":true}`); code != http.StatusOK {
		t.Fatal("final enable failed")
	}
	if code, _, _ := aiSessionsGet(t, e, admin); code != http.StatusOK {
		t.Fatal("final read failed")
	}
}

func TestAISessionsDisplacedSocketCannotReport(t *testing.T) {
	_, s := setupTestServer(t)
	old := &ConnectedAgent{}
	fresh := &ConnectedAgent{}
	s.agentsMu.Lock()
	s.agents["m-d"] = fresh
	s.agentsMu.Unlock()
	t.Cleanup(func() {
		s.agentsMu.Lock()
		delete(s.agents, "m-d")
		s.agentsMu.Unlock()
	})
	raw, _ := json.Marshal(aiSessionsFrame("m-d", validSession("d0d0d0d0", "claude")))
	s.ingestAISessions("m-d", old, raw)
	if len(s.aiSessions.live()) != 0 {
		t.Fatal("displaced socket wrote a snapshot")
	}
	s.ingestAISessions("m-d", fresh, raw)
	if len(s.aiSessions.live()) != 1 {
		t.Fatal("registered socket could not write a snapshot")
	}
	// Unregistering the displaced socket must not wipe the fresh one's snapshot.
	s.unregisterAgentConnection("m-d", old)
	if len(s.aiSessions.live()) != 1 {
		t.Fatal("unregister of a displaced socket removed the live snapshot")
	}
	s.unregisterAgentConnection("m-d", fresh)
	if len(s.aiSessions.live()) != 0 {
		t.Fatal("unregister of the registered socket left its snapshot behind")
	}
}

func TestDeleteMachineRemovesAISessions(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	admin := loginAndGetToken(t, e)
	s.seedTestMachine(t, "m-del")
	s.aiSessions.put("m-del", []aisessions.Session{{ID: "01", Tool: "claude"}})

	req := httptest.NewRequest(http.MethodDelete, "/api/machines/m-del", nil)
	req.Header.Set("Authorization", "Bearer "+admin)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if len(s.aiSessions.live()) != 0 {
		t.Fatal("deleted machine still has an AI sessions snapshot")
	}
}

// readAISessionsConfig drains frames until an ai_sessions_config arrives and
// returns its enabled flag.
func readAISessionsConfig(t *testing.T, conn *websocket.Conn) bool {
	t.Helper()
	enabled, _ := readAISessionsConfigRev(t, conn)
	return enabled
}

// readAISessionsConfigRev is readAISessionsConfig returning the revision too.
func readAISessionsConfigRev(t *testing.T, conn *websocket.Conn) (bool, uint64) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for %s: %v", aiSessionsConfigType, err)
		}
		var frame struct {
			Type    string  `json:"type"`
			Enabled *bool   `json:"enabled"`
			Rev     *uint64 `json:"rev"`
		}
		if json.Unmarshal(msg, &frame) == nil && frame.Type == aiSessionsConfigType {
			if frame.Enabled == nil || frame.Rev == nil || *frame.Rev == 0 {
				t.Fatalf("config frame must carry enabled and a non-zero rev: %s", msg)
			}
			return *frame.Enabled, *frame.Rev
		}
	}
}

// TestAISessionsConfigRevisionAdvancesAndIsOrderable: each toggle advances
// the revision and frames carry it, so an agent can discard a registration
// frame that lands after a newer toggle broadcast.
func TestAISessionsConfigRevisionAdvancesAndIsOrderable(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	admin := loginAndGetToken(t, e)

	conn := s.connectEnrolledAgent(t, server, "machine-R")
	defer conn.Close()
	enabled, rev1 := readAISessionsConfigRev(t, conn)
	if !enabled {
		t.Fatal("default must be enabled")
	}
	if code, _ := aiSessionsPatch(t, e, admin, `{"enabled":false}`); code != http.StatusOK {
		t.Fatal("disable failed")
	}
	enabled, rev2 := readAISessionsConfigRev(t, conn)
	if enabled || rev2 <= rev1 {
		t.Fatalf("disable must advance rev: got enabled=%v rev %d after %d", enabled, rev2, rev1)
	}
	if code, _ := aiSessionsPatch(t, e, admin, `{"enabled":true}`); code != http.StatusOK {
		t.Fatal("enable failed")
	}
	enabled, rev3 := readAISessionsConfigRev(t, conn)
	if !enabled || rev3 <= rev2 {
		t.Fatalf("enable must advance rev: got enabled=%v rev %d after %d", enabled, rev3, rev2)
	}
	if got := string(aiSessionsConfigFrame(false, rev2)); !strings.Contains(got, fmt.Sprintf(`"rev":%d`, rev2)) {
		t.Fatalf("frame must embed rev: %s", got)
	}
}

// TestAISessionsConfigSnapshotConsistentUnderConcurrentToggle: sends racing
// with toggles always observe a consistent (enabled, rev) pair — one rev
// never reports two different states — and revs are monotonic.
func TestAISessionsConfigSnapshotConsistentUnderConcurrentToggle(t *testing.T) {
	_, s := setupTestServer(t)
	// Load the persisted default (rev 1, enabled) before any toggle so the
	// revision→state mapping below is deterministic.
	if enabled, rev := s.aiSessionsConfigSnapshot(); !enabled || rev != 1 {
		t.Fatalf("initial snapshot enabled=%v rev=%d, want true/1", enabled, rev)
	}
	var mu sync.Mutex
	seen := map[uint64]bool{}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var last uint64
			for {
				select {
				case <-stop:
					return
				default:
				}
				enabled, rev := s.aiSessionsConfigSnapshot()
				if rev < last {
					t.Errorf("revision went backwards: %d after %d", rev, last)
				}
				last = rev
				mu.Lock()
				if prev, ok := seen[rev]; ok && prev != enabled {
					t.Errorf("rev %d observed as both %v and %v", rev, prev, enabled)
				}
				seen[rev] = enabled
				mu.Unlock()
			}
		}()
	}
	for n := 0; n < 40; n++ {
		if err := s.setAISessionsEnabled(n%2 == 1); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	enabled, rev := s.aiSessionsConfigSnapshot()
	if rev != 41 || !enabled { // rev 1 initial + 40 toggles; the last (n=39) enabled
		t.Fatalf("final snapshot enabled=%v rev=%d, want true/41", enabled, rev)
	}
	// Toggle n lands at rev n+2 and sets enabled = (n odd), so odd revs
	// (including the initial rev 1) are enabled and even revs are disabled.
	for r, en := range seen {
		want := r%2 == 1
		if en != want {
			t.Errorf("rev %d observed enabled=%v, want %v", r, en, want)
		}
	}
}

// TestAISessionsConfigSentOnRegistrationAndBroadcast covers the runtime
// signal: default-on after registration, a disable pushed to connected
// agents, and a reconnect receiving the current (disabled) state.
func TestAISessionsConfigSentOnRegistrationAndBroadcast(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	admin := loginAndGetToken(t, e)

	secret := s.enrollAgentSecret(t, server, "machine-G")
	conn, err := wsDialAgent(t, server, "secret="+secret)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if !readAISessionsConfig(t, conn) {
		t.Fatal("default state after registration must be enabled")
	}

	if code, body := aiSessionsPatch(t, e, admin, `{"enabled":false}`); code != http.StatusOK {
		t.Fatalf("disable: %d %s", code, body)
	}
	if readAISessionsConfig(t, conn) {
		t.Fatal("connected agent must receive the disable")
	}

	// Reconnect the same machine while disabled: the fresh socket hears
	// "disabled" up front, before any scheduled scan could run.
	conn.Close()
	s.waitAgentDrain(t, "machine-G", 2*time.Second)
	conn2, err := wsDialAgent(t, server, "secret="+secret)
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()
	if readAISessionsConfig(t, conn2) {
		t.Fatal("agent reconnecting while disabled must be told disabled")
	}

	if code, body := aiSessionsPatch(t, e, admin, `{"enabled":true}`); code != http.StatusOK {
		t.Fatalf("enable: %d %s", code, body)
	}
	if !readAISessionsConfig(t, conn2) {
		t.Fatal("connected agent must receive the re-enable")
	}
}

// TestAISessionsConfigIgnoredByOldAgent simulates a pre-feature agent: it
// treats the config frame as a command, finds no id, and ignores it. The
// hub must keep serving that socket normally and must not expect a reply.
func TestAISessionsConfigIgnoredByOldAgent(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	admin := loginAndGetToken(t, e)

	conn := s.connectEnrolledAgent(t, server, "machine-O")
	defer conn.Close()
	readAISessionsConfig(t, conn) // arrives; an old agent would drop it
	if code, _ := aiSessionsPatch(t, e, admin, `{"enabled":false}`); code != http.StatusOK {
		t.Fatal("disable failed")
	}
	readAISessionsConfig(t, conn)
	// An old agent keeps sending its usual traffic and never ai_sessions.
	s.sendSentinelMetrics(t, conn, "machine-O", "host-o")
	var status string
	if err := s.db.QueryRow(`SELECT status FROM machines WHERE id = 'machine-O'`).Scan(&status); err != nil || status != "online" {
		t.Fatalf("old agent socket disturbed: status=%q err=%v", status, err)
	}
	if !s.isRegisteredConnection("machine-O", s.registeredAgent("machine-O")) {
		t.Fatal("old agent lost its registration")
	}
	if _, resp, _ := aiSessionsGet(t, e, admin); len(resp.Machines) != 0 {
		t.Fatalf("old agent should have no snapshot: %+v", resp.Machines)
	}
}

func (s *Server) registeredAgent(machineID string) *ConnectedAgent {
	s.agentsMu.RLock()
	defer s.agentsMu.RUnlock()
	return s.agents[machineID]
}

// TestAISessionsConcurrentTogglesKeepDBAndCacheConsistent: many admin
// toggles racing each other must leave the persisted value equal to the
// cached value, because both are updated under one lock in one order.
func TestAISessionsConcurrentTogglesKeepDBAndCacheConsistent(t *testing.T) {
	_, s := setupTestServer(t)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for n := 0; n < 25; n++ {
				if err := s.setAISessionsEnabled((i+n)%2 == 0); err != nil {
					t.Errorf("toggle: %v", err)
				}
			}
		}(i)
	}
	wg.Wait()
	cached, rev := s.aiSessionsConfigSnapshot()
	if rev != 1+16*25 {
		t.Fatalf("rev = %d, want %d", rev, 1+16*25)
	}
	if persisted := s.loadAISessionsEnabled(); persisted != cached {
		t.Fatalf("persisted=%v cached=%v after concurrent toggles", persisted, cached)
	}
}

// subscribeSSE registers a test channel with the global SSE client set and
// returns it plus a cleanup that removes it.
func subscribeSSE(t *testing.T) chan []byte {
	t.Helper()
	ch := make(chan []byte, 64)
	sseClientsMu.Lock()
	sseClients[ch] = struct{}{}
	sseClientsMu.Unlock()
	t.Cleanup(func() {
		sseClientsMu.Lock()
		delete(sseClients, ch)
		sseClientsMu.Unlock()
	})
	return ch
}

// nextSSEEvent waits for the next frame of the named event and returns its
// decoded data. Frames for other events are skipped.
func nextSSEEvent(t *testing.T, ch chan []byte, event string) (string, map[string]any) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case frame := <-ch:
			text := string(frame)
			if !strings.HasPrefix(text, "event: "+event+"\n") {
				continue
			}
			payload := strings.TrimSuffix(strings.TrimPrefix(text, "event: "+event+"\ndata: "), "\n\n")
			var decoded map[string]any
			if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
				t.Fatalf("decode %s payload %q: %v", event, payload, err)
			}
			return payload, decoded
		case <-deadline:
			t.Fatalf("no %s SSE event within 5s", event)
		}
	}
}

// TestAISessionsSSEEventShapeAndPrivacy: an accepted report is broadcast as
// one ai_sessions event carrying exactly the GET view shape, sanitized.
func TestAISessionsSSEEventShapeAndPrivacy(t *testing.T) {
	_, s := setupTestServer(t)
	s.seedTestMachine(t, "m-sse")
	agent := &ConnectedAgent{}
	s.agentsMu.Lock()
	s.agents["m-sse"] = agent
	s.agentsMu.Unlock()
	t.Cleanup(func() {
		s.agentsMu.Lock()
		delete(s.agents, "m-sse")
		s.agentsMu.Unlock()
	})
	ch := subscribeSSE(t)

	const secret = "sk-ant/PLANTED+SECRET"
	frame := aiSessionsFrame("m-sse",
		validSession("5e5e0001", "claude"),
		map[string]any{"id": "5e5e0002", "tool": "codex", "prompt": secret, "cwd": "/home/alice/x",
			"model": map[string]any{"value": secret, "source": "argv_flag", "confidence": "exact"}},
		map[string]any{"id": "5e5e0003", "tool": "cursor"},
	)
	frame["env"] = map[string]string{"KEY": secret}
	raw, _ := json.Marshal(frame)
	s.ingestAISessions("m-sse", agent, raw)

	payload, decoded := nextSSEEvent(t, ch, "ai_sessions")
	for _, forbidden := range []string{secret, "PLANTED", "/home/", "alice", `"prompt"`, `"cwd":`, `"env"`, "cursor"} {
		if strings.Contains(payload, forbidden) {
			t.Errorf("SSE payload leaks %q: %s", forbidden, payload)
		}
	}
	wantKeys := map[string]bool{"machine_id": true, "hostname": true, "received_at": true, "sessions": true}
	for k := range decoded {
		if !wantKeys[k] {
			t.Errorf("unexpected top-level key %q in ai_sessions event", k)
		}
	}
	if decoded["machine_id"] != "m-sse" || decoded["hostname"] != "machine-m-sse" {
		t.Fatalf("machine identity wrong: %s", payload)
	}
	sessions := decoded["sessions"].([]any)
	if len(sessions) != 2 {
		t.Fatalf("expected 2 whitelisted sessions, got %d: %s", len(sessions), payload)
	}
	for _, raw := range sessions {
		sess := raw.(map[string]any)
		for k := range sess {
			switch k {
			case "id", "tool", "started_at", "project", "model", "activity":
			default:
				t.Errorf("unexpected session key %q", k)
			}
		}
	}
	second := sessions[1].(map[string]any)
	if second["model"].(map[string]any)["value"] != "" {
		t.Errorf("secret-shaped model value must be dropped: %v", second["model"])
	}
	if _, err := time.Parse(time.RFC3339, decoded["received_at"].(string)); err != nil {
		t.Errorf("received_at not RFC3339: %v", decoded["received_at"])
	}
}

// TestAISessionsSSERemovalAndConfigBroadcasts: unregistering the reporting
// socket announces removal once; deleting a machine does too; toggling the
// switch announces the new state and disable clears everything.
func TestAISessionsSSERemovalAndConfigBroadcasts(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	admin := loginAndGetToken(t, e)
	ch := subscribeSSE(t)

	agent := &ConnectedAgent{}
	s.agentsMu.Lock()
	s.agents["m-rm"] = agent
	s.agentsMu.Unlock()
	raw, _ := json.Marshal(aiSessionsFrame("m-rm", validSession("0a0a0a0a", "kimi")))
	s.ingestAISessions("m-rm", agent, raw)
	nextSSEEvent(t, ch, "ai_sessions")

	// A displaced (non-registered) socket unregistering must not announce.
	s.unregisterAgentConnection("m-rm", &ConnectedAgent{})
	// The registered socket going away does.
	s.unregisterAgentConnection("m-rm", agent)
	_, removed := nextSSEEvent(t, ch, "ai_sessions_removed")
	if removed["machine_id"] != "m-rm" {
		t.Fatalf("removal event for wrong machine: %v", removed)
	}
	// Nothing left to remove: no second announcement.
	s.removeAISessions("m-rm")
	select {
	case frame := <-ch:
		if strings.HasPrefix(string(frame), "event: ai_sessions_removed") {
			t.Fatalf("duplicate removal announced: %s", frame)
		}
	case <-time.After(100 * time.Millisecond):
	}

	// Machine delete path.
	s.seedTestMachine(t, "m-del2")
	s.aiSessions.put("m-del2", []aisessions.Session{{ID: "01", Tool: "claude"}})
	req := httptest.NewRequest(http.MethodDelete, "/api/machines/m-del2", nil)
	req.Header.Set("Authorization", "Bearer "+admin)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if _, removed := nextSSEEvent(t, ch, "ai_sessions_removed"); removed["machine_id"] != "m-del2" {
		t.Fatalf("delete removal event wrong: %v", removed)
	}

	// Admin switch.
	if code, body := aiSessionsPatch(t, e, admin, `{"enabled":false}`); code != http.StatusOK {
		t.Fatalf("disable: %d %s", code, body)
	}
	if _, cfg := nextSSEEvent(t, ch, "ai_sessions_config"); cfg["enabled"] != false {
		t.Fatalf("config event should say disabled: %v", cfg)
	}
	if code, body := aiSessionsPatch(t, e, admin, `{"enabled":true}`); code != http.StatusOK {
		t.Fatalf("enable: %d %s", code, body)
	}
	if _, cfg := nextSSEEvent(t, ch, "ai_sessions_config"); cfg["enabled"] != true {
		t.Fatalf("config event should say enabled: %v", cfg)
	}
}

func TestAISessionsReadIncludesHubClock(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	admin := loginAndGetToken(t, e)
	fixed := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	s.aiSessions.now = func() time.Time { return fixed }
	_, _, body := aiSessionsGet(t, e, admin)
	if !strings.Contains(body, `"now":"2026-09-05T12:00:00Z"`) {
		t.Fatalf("response must carry the hub clock: %s", body)
	}
}
