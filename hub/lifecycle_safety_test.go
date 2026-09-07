package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

// lifecycleDeadline bounds every wait in this file; assertions are
// ordering-based, the deadline is only a hang guard.
const lifecycleDeadline = 5 * time.Second

func metricsFrame(machineID, hostname string) map[string]interface{} {
	return map[string]interface{}{
		"type": "metrics", "machine_id": machineID,
		"hostname": hostname, "ip": "10.0.0.1", "os": "linux",
		"cpu_percent": 1.0, "ram_used_bytes": 1, "ram_total_bytes": 2,
		"disk_used_bytes": 1, "disk_total_bytes": 2,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
}

func waitHostname(t *testing.T, s *Server, machineID, want string) {
	t.Helper()
	deadline := time.Now().Add(lifecycleDeadline)
	for {
		var got string
		_ = s.db.QueryRow(`SELECT hostname FROM machines WHERE id = ?`, machineID).Scan(&got)
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("machine %s hostname never became %q (got %q)", machineID, want, got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func countRows(t *testing.T, s *Server, table, machineID string) int {
	t.Helper()
	col := "machine_id"
	if table == "machines" {
		col = "id"
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+col+` = ?`, machineID).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// subscribeSSE registers a raw broadcast subscriber and returns it plus a
// cleanup. It sees exactly what handleSSE would forward.
func subscribeBroadcast(t *testing.T) (<-chan []byte, func()) {
	t.Helper()
	ch := make(chan []byte, 64)
	sseClientsMu.Lock()
	sseClients[ch] = struct{}{}
	sseClientsMu.Unlock()
	return ch, func() {
		sseClientsMu.Lock()
		delete(sseClients, ch)
		sseClientsMu.Unlock()
	}
}

// TestDeleteConnectedMachineClosesSocketAndDoesNotResurrect: deleting a
// machine whose agent is connected must close that socket, drop its rollout
// bookkeeping, emit machine_removed, and leave nothing for a late frame on
// the old socket to recreate.
func TestDeleteConnectedMachineClosesSocketAndDoesNotResurrect(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAndGetToken(t, e)

	conn := s.connectEnrolledAgent(t, server, "machine-A")
	defer conn.Close()
	readAISessionsConfig(t, conn)
	writeFrame(t, conn, metricsFrame("machine-A", "before-delete"))
	waitHostname(t, s, "machine-A", "before-delete")

	// Rollout bookkeeping that must not outlive the machine.
	agentRunningVersionsMu.Lock()
	agentRunningVersions["machine-A"] = agentVersionInfo{MachineID: "machine-A", RunningSHA: "deadbeef", OS: "linux", ReportedAt: time.Now()}
	agentRunningVersionsMu.Unlock()
	pendingReconnectsMu.Lock()
	pendingReconnects["machine-A"] = time.Now()
	pendingReconnectsMu.Unlock()

	events, unsubscribe := subscribeBroadcast(t)
	defer unsubscribe()

	req := httptest.NewRequest(http.MethodDelete, "/api/machines/machine-A", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}

	// The delete returned only after the handler finished, so the socket is
	// closed from the hub side and the registry entry is gone.
	_ = conn.SetReadDeadline(time.Now().Add(lifecycleDeadline))
	expectConnectionClosed(t, conn, "agent socket must be closed by machine deletion")
	if agentMapHasEntry(s, "machine-A") {
		t.Fatal("registry still holds the deleted machine")
	}
	agentRunningVersionsMu.RLock()
	_, versionKept := agentRunningVersions["machine-A"]
	agentRunningVersionsMu.RUnlock()
	pendingReconnectsMu.Lock()
	_, reconnectKept := pendingReconnects["machine-A"]
	pendingReconnectsMu.Unlock()
	if versionKept || reconnectKept {
		t.Fatalf("rollout state leaked: version=%v reconnect=%v", versionKept, reconnectKept)
	}

	// A late frame on the dead socket must not bring the machine back.
	_ = conn.WriteJSON(metricsFrame("machine-A", "after-delete"))
	time.Sleep(200 * time.Millisecond)
	for _, table := range []string{"machines", "metrics", "agent_credentials"} {
		if n := countRows(t, s, table, "machine-A"); n != 0 {
			t.Fatalf("%s still has %d rows for the deleted machine", table, n)
		}
	}

	// Dashboards are told explicitly.
	deadline := time.After(lifecycleDeadline)
	for {
		select {
		case ev := <-events:
			if strings.HasPrefix(string(ev), "event: machine_removed\n") {
				var payload struct {
					MachineID string `json:"machine_id"`
				}
				data := strings.TrimPrefix(strings.SplitN(string(ev), "\n", 2)[1], "data: ")
				if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &payload); err != nil || payload.MachineID != "machine-A" {
					t.Fatalf("bad machine_removed payload: %q", ev)
				}
				return
			}
		case <-deadline:
			t.Fatal("machine_removed event never broadcast")
		}
	}
}

// TestDeleteMachineNotConnectedStillWorks keeps the ordinary path honest:
// no live connection, plain row removal, event still emitted.
func TestDeleteMachineNotConnectedStillWorks(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	s.seedTestMachine(t, "machine-idle")
	adminToken := loginAndGetToken(t, e)
	events, unsubscribe := subscribeBroadcast(t)
	defer unsubscribe()

	req := httptest.NewRequest(http.MethodDelete, "/api/machines/machine-idle", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if n := countRows(t, s, "machines", "machine-idle"); n != 0 {
		t.Fatalf("row survived delete")
	}
	select {
	case ev := <-events:
		if !strings.Contains(string(ev), `"machine_id":"machine-idle"`) {
			t.Fatalf("unexpected event %q", ev)
		}
	case <-time.After(lifecycleDeadline):
		t.Fatal("no machine_removed event")
	}
}

// TestPreIdentityFramesCannotWriteForeignMachine: a socket holding a valid
// install token has no identity until its first metrics frame. Inventory
// frames sent before that must not write anything for the machine they
// claim, while the real first enrollment (metrics, then hardware) still works.
func TestPreIdentityFramesCannotWriteForeignMachine(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := s.seedValidToken(t)
	server := httptest.NewServer(e)
	defer server.Close()
	s.seedTestMachine(t, "machine-victim")

	conn, err := wsDialAgent(t, server, "token="+token)
	if err != nil {
		t.Fatalf("dial with token: %v", err)
	}
	defer conn.Close()

	// Forged inventory before any metrics frame: no identity yet.
	writeFrame(t, conn, map[string]interface{}{"type": "hardware_info", "machine_id": "machine-victim", "cpu": "forged"})
	writeFrame(t, conn, map[string]interface{}{"type": "services", "machine_id": "machine-victim",
		"services": []map[string]interface{}{{"name": "sshd", "status": "running", "description": "forged"}}})
	writeFrame(t, conn, map[string]interface{}{"type": "containers", "machine_id": "machine-victim",
		"containers": []map[string]interface{}{{"id": "deadbeef", "name": "forged", "status": "up", "image": "evil:latest"}}})
	writeFrame(t, conn, map[string]interface{}{"type": "hardware_info", "machine_id": "machine-phantom", "cpu": "forged"})

	// Genuine first enrollment: metrics establishes identity, hub replies enrolled.
	writeFrame(t, conn, metricsFrame("machine-new", "fresh-host"))
	_ = conn.SetReadDeadline(time.Now().Add(lifecycleDeadline))
	for {
		var frame struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for enrolled frame: %v", err)
		}
		_ = json.Unmarshal(raw, &frame)
		if frame.Type == "error" {
			t.Fatalf("enrollment rejected: %s", frame.Message)
		}
		if frame.Type == "enrolled" {
			break
		}
	}
	// Hardware after metrics, before commit, is the agent's real order. It
	// must not be written under the uncommitted identity, but it must not
	// be lost either: it is held and applied at the commit point. Services
	// before commit are simply dropped (the agent resends them every tick).
	writeFrame(t, conn, map[string]interface{}{"type": "hardware_info", "machine_id": "machine-new", "cpu": "real-cpu"})
	writeFrame(t, conn, map[string]interface{}{"type": "services", "machine_id": "machine-new",
		"services": []map[string]interface{}{{"name": "early", "status": "running", "description": "pre-commit"}}})
	writeFrame(t, conn, metricsFrame("machine-new", "fresh-host-2"))
	waitHostname(t, s, "machine-new", "fresh-host-2")

	var hwBeforeCommit string
	_ = s.db.QueryRow(`SELECT COALESCE(hardware_info,'') FROM machines WHERE id = ?`, "machine-new").Scan(&hwBeforeCommit)
	if hwBeforeCommit != "" {
		t.Fatalf("hardware_info written before enrollment committed: %q", hwBeforeCommit)
	}
	if n := countRows(t, s, "services", "machine-new"); n != 0 {
		t.Fatalf("services written before enrollment committed: %d rows", n)
	}

	// Commit: identity becomes registered, held hardware is applied.
	commitEnrollment(t, conn)
	writeFrame(t, conn, map[string]interface{}{"type": "services", "machine_id": "machine-new",
		"services": []map[string]interface{}{{"name": "late", "status": "running", "description": "post-commit"}}})
	writeFrame(t, conn, metricsFrame("machine-new", "fresh-host-3"))
	waitHostname(t, s, "machine-new", "fresh-host-3")

	var hw string
	_ = s.db.QueryRow(`SELECT COALESCE(hardware_info,'') FROM machines WHERE id = ?`, "machine-new").Scan(&hw)
	if !strings.Contains(hw, "real-cpu") {
		t.Fatalf("first-enrollment hardware_info not applied at commit: %q", hw)
	}
	var svcName string
	_ = s.db.QueryRow(`SELECT name FROM services WHERE machine_id = ?`, "machine-new").Scan(&svcName)
	if svcName != "late" {
		t.Fatalf("post-commit services not stored (got %q)", svcName)
	}
	var victimHW string
	_ = s.db.QueryRow(`SELECT COALESCE(hardware_info,'') FROM machines WHERE id = ?`, "machine-victim").Scan(&victimHW)
	if victimHW != "" {
		t.Fatalf("pre-identity frame rewrote another machine's hardware: %q", victimHW)
	}
	for _, table := range []string{"services", "containers"} {
		if n := countRows(t, s, table, "machine-victim"); n != 0 {
			t.Fatalf("pre-identity frame wrote %d %s rows for another machine", n, table)
		}
	}
	if n := countRows(t, s, "machines", "machine-phantom"); n != 0 {
		t.Fatal("pre-identity hardware_info created a phantom machine")
	}
}

// TestTerminalRelayConcurrentTeardownDoesNotPanic hammers the relay's three
// exit paths at once. Before the shared once-close, two of them could both
// see the done channel open and the second close panicked the process.
func TestTerminalRelayConcurrentTeardownDoesNotPanic(t *testing.T) {
	_, s := setupTestServer(t)

	serverConns := make(chan *websocket.Conn, 2)
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverConns <- c
	}))
	defer ws.Close()
	dial := func() *websocket.Conn {
		t.Helper()
		c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ws.URL, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	for i := 0; i < 40; i++ {
		agentClient, browserClient := dial(), dial()
		agentServer, browserServer := <-serverConns, <-serverConns
		id := "sess-" + time.Now().Format("150405.000000") + "-" + string(rune('a'+i%26))
		session := &TerminalSession{
			ID: id, MachineID: "m", AgentWS: agentServer, BrowserWS: browserServer,
			CreatedAt: time.Now(), LastActivity: time.Now(), Done: make(chan struct{}),
		}
		termSessionsMu.Lock()
		termSessions[id] = session
		termSessionsMu.Unlock()

		relayDone := make(chan struct{})
		go func() { s.terminalRelay(id, session); close(relayDone) }()

		// All three exit triggers at once: explicit cleanup closes both
		// server sockets; both clients close too.
		var wg sync.WaitGroup
		wg.Add(3)
		go func() { defer wg.Done(); s.cleanupTerminalSession(id) }()
		go func() { defer wg.Done(); agentClient.Close() }()
		go func() { defer wg.Done(); browserClient.Close() }()
		wg.Wait()
		select {
		case <-relayDone:
		case <-time.After(lifecycleDeadline):
			t.Fatalf("iteration %d: relay did not end", i)
		}
	}
}

func openEventStream(t *testing.T, server *httptest.Server, token string) (*http.Response, *bufio.Reader) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/events?token="+token, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status %d", resp.StatusCode)
	}
	r := bufio.NewReader(resp.Body)
	// Snapshot arrives first; consume its event line so we know we are live.
	line, err := r.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "event: snapshot") {
		t.Fatalf("expected snapshot first, got %q err=%v", line, err)
	}
	return resp, r
}

// streamEndsWithin reads until EOF and fails if it takes longer than max.
func streamEndsWithin(t *testing.T, r *bufio.Reader, max time.Duration, what string) {
	t.Helper()
	ended := make(chan struct{})
	go func() {
		for {
			if _, err := r.ReadString('\n'); err != nil {
				close(ended)
				return
			}
		}
	}()
	select {
	case <-ended:
	case <-time.After(max):
		t.Fatalf("event stream kept flowing after %s", what)
	}
}

// TestSSEStreamEndsWhenUserDeleted: an open stream must stop once its user
// no longer exists, without waiting for the client to hang up.
func TestSSEStreamEndsWhenUserDeleted(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	old := sseRevalidateInterval
	sseRevalidateInterval = 50 * time.Millisecond
	defer func() { sseRevalidateInterval = old }()

	adminToken := loginAndGetToken(t, e)
	resp, r := openEventStream(t, server, adminToken)
	defer resp.Body.Close()

	// Remove the user out from under the stream.
	if _, err := s.db.Exec(`DELETE FROM users`); err != nil {
		t.Fatal(err)
	}
	streamEndsWithin(t, r, lifecycleDeadline, "user deletion")
}

// TestSSEStreamEndsWhenTokenExpires: the token's own deadline is enforced
// on the stream, not only at the handshake.
func TestSSEStreamEndsWhenTokenExpires(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	old := sseRevalidateInterval
	sseRevalidateInterval = 50 * time.Millisecond
	defer func() { sseRevalidateInterval = old }()

	var userID string
	if err := s.db.QueryRow(`SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	claims := jwt.MapClaims{
		"user_id": userID, "username": "admin", "type": "sse",
		"exp": time.Now().Add(400*time.Millisecond).Unix() + 1,
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(jwtSecret)
	if err != nil {
		t.Fatal(err)
	}
	resp, r := openEventStream(t, server, token)
	defer resp.Body.Close()
	streamEndsWithin(t, r, lifecycleDeadline, "token expiry")
}

// TestDeleteWaitsForHandshakeInProgress pins the interleaving root asked
// for: an agent reconnect that has already validated its credential is paused
// just before registration (auth read lock held). A concurrent delete must
// not complete until that handshake finishes, and once it does the delete
// must close the new socket and leave no row for it to recreate.
func TestDeleteWaitsForHandshakeInProgress(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAndGetToken(t, e)

	secret := s.enrollAgentSecret(t, server, "machine-A")
	s.waitAgentDrain(t, "machine-A", 2*time.Second)

	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce sync.Once
	agentHandshakeTestHook = func(machineID string) {
		if machineID != "machine-A" {
			return
		}
		enteredOnce.Do(func() { close(entered) })
		<-release
	}
	defer func() { agentHandshakeTestHook = nil }()

	conns := make(chan *websocket.Conn, 1)
	go func() {
		c, err := wsDialAgent(t, server, "secret="+secret)
		if err != nil {
			t.Errorf("reconnect dial: %v", err)
			return
		}
		conns <- c
	}()
	select {
	case <-entered:
	case <-time.After(lifecycleDeadline):
		t.Fatal("handshake never reached the pre-registration point")
	}

	deleteCode := make(chan int, 1)
	go func() {
		req := httptest.NewRequest(http.MethodDelete, "/api/machines/machine-A", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		deleteCode <- rec.Code
	}()
	select {
	case code := <-deleteCode:
		t.Fatalf("delete completed (%d) while a validated handshake still held the auth lock", code)
	case <-time.After(300 * time.Millisecond):
	}

	close(release)
	var code int
	select {
	case code = <-deleteCode:
	case <-time.After(lifecycleDeadline):
		t.Fatal("delete never completed after the handshake was released")
	}
	if code != http.StatusOK {
		t.Fatalf("delete after handshake: %d", code)
	}

	var conn *websocket.Conn
	select {
	case conn = <-conns:
	case <-time.After(lifecycleDeadline):
		t.Fatal("reconnect never returned a connection")
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(lifecycleDeadline))
	expectConnectionClosed(t, conn, "socket registered during the paused handshake must be closed by the delete")
	_ = conn.WriteJSON(metricsFrame("machine-A", "after-delete"))
	time.Sleep(200 * time.Millisecond)
	for _, table := range []string{"machines", "agent_credentials"} {
		if n := countRows(t, s, table, "machine-A"); n != 0 {
			t.Fatalf("%s still has %d rows after delete", table, n)
		}
	}
	if agentMapHasEntry(s, "machine-A") {
		t.Fatal("registry still holds the deleted machine")
	}
}

// holdFirstIngest installs an ingest hook that parks the first frame for
// machineID inside the ingestion barrier until release is closed. Later
// frames pass straight through.
func holdFirstIngest(t *testing.T, machineID string) (entered <-chan struct{}, release chan struct{}) {
	t.Helper()
	enteredCh := make(chan struct{})
	release = make(chan struct{})
	var once sync.Once
	agentIngestTestHook = func(id string) {
		if id != machineID {
			return
		}
		first := false
		once.Do(func() { first = true; close(enteredCh) })
		if first {
			<-release
		}
	}
	t.Cleanup(func() { agentIngestTestHook = nil })
	return enteredCh, release
}

func deleteMachineAsync(e *echo.Echo, adminToken, machineID string) <-chan int {
	code := make(chan int, 1)
	go func() {
		req := httptest.NewRequest(http.MethodDelete, "/api/machines/"+machineID, nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		code <- rec.Code
	}()
	return code
}

// TestDeleteWaitsForFrameMidWrite: a frame already inside the ingestion
// barrier finishes before the delete proceeds, and nothing after the delete
// can write. There is no timeout path, so there is nothing to retry around.
func TestDeleteWaitsForFrameMidWrite(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAndGetToken(t, e)

	conn := s.connectEnrolledAgent(t, server, "machine-A")
	defer conn.Close()
	readAISessionsConfig(t, conn)
	writeFrame(t, conn, metricsFrame("machine-A", "settled"))
	waitHostname(t, s, "machine-A", "settled")

	entered, release := holdFirstIngest(t, "machine-A")
	writeFrame(t, conn, metricsFrame("machine-A", "mid-write"))
	select {
	case <-entered:
	case <-time.After(lifecycleDeadline):
		t.Fatal("frame never entered the ingestion barrier")
	}

	deleteCode := deleteMachineAsync(e, adminToken, "machine-A")
	select {
	case code := <-deleteCode:
		t.Fatalf("delete completed (%d) while a frame was inside the ingestion barrier", code)
	case <-time.After(300 * time.Millisecond):
	}

	close(release)
	select {
	case code := <-deleteCode:
		if code != http.StatusOK {
			t.Fatalf("delete after the frame finished: %d", code)
		}
	case <-time.After(lifecycleDeadline):
		t.Fatal("delete never completed after the frame was released")
	}
	_ = conn.SetReadDeadline(time.Now().Add(lifecycleDeadline))
	expectConnectionClosed(t, conn, "socket must be closed by the delete")
	_ = conn.WriteJSON(metricsFrame("machine-A", "after-delete"))
	time.Sleep(200 * time.Millisecond)
	for _, table := range []string{"machines", "metrics", "agent_credentials"} {
		if n := countRows(t, s, table, "machine-A"); n != 0 {
			t.Fatalf("%s still has %d rows after delete", table, n)
		}
	}
}

// TestDisplacedSocketMidWriteCannotResurrect: an older socket that a
// reconnect has already displaced can still be inside a frame. Its write
// must be refused once it re-checks registration under the barrier, and a
// delete racing that frame must wait for it, then remove everything.
func TestDisplacedSocketMidWriteCannotResurrect(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAndGetToken(t, e)

	secret := s.enrollAgentSecret(t, server, "machine-A")
	older, err := wsDialAgent(t, server, "secret="+secret)
	if err != nil {
		t.Fatal(err)
	}
	defer older.Close()
	readAISessionsConfig(t, older)
	writeFrame(t, older, metricsFrame("machine-A", "settled"))
	waitHostname(t, s, "machine-A", "settled")

	// Park the older socket inside the barrier on its next frame.
	entered, release := holdFirstIngest(t, "machine-A")
	writeFrame(t, older, metricsFrame("machine-A", "stale-write"))
	select {
	case <-entered:
	case <-time.After(lifecycleDeadline):
		t.Fatal("older socket never entered the ingestion barrier")
	}

	// A reconnect displaces it while it is parked.
	newer, err := wsDialAgent(t, server, "secret="+secret)
	if err != nil {
		t.Fatal(err)
	}
	defer newer.Close()
	waitForCondition(t, lifecycleDeadline, func() bool {
		current := s.registeredAgent("machine-A")
		return current != nil && s.isRegisteredConnection("machine-A", current)
	})

	// Delete must block behind the parked frame, then win.
	deleteCode := deleteMachineAsync(e, adminToken, "machine-A")
	select {
	case code := <-deleteCode:
		t.Fatalf("delete completed (%d) while the displaced socket was inside the barrier", code)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	select {
	case code := <-deleteCode:
		if code != http.StatusOK {
			t.Fatalf("delete: %d", code)
		}
	case <-time.After(lifecycleDeadline):
		t.Fatal("delete never completed")
	}

	// The displaced frame was refused (hostname never became stale-write is
	// unobservable after delete), so assert the strong property: no rows.
	time.Sleep(200 * time.Millisecond)
	for _, table := range []string{"machines", "metrics", "agent_credentials"} {
		if n := countRows(t, s, table, "machine-A"); n != 0 {
			t.Fatalf("%s still has %d rows after delete", table, n)
		}
	}
	_ = newer.SetReadDeadline(time.Now().Add(lifecycleDeadline))
	expectConnectionClosed(t, newer, "registered socket must be closed by the delete")
	if agentMapHasEntry(s, "machine-A") {
		t.Fatal("registry still holds the deleted machine")
	}
	// The displaced frame's latency write lives inside the barrier too, so
	// the cache cleared by the delete is not repopulated afterwards.
	machineLatencyMu.RLock()
	_, latencyKept := machineLatency["machine-A"]
	machineLatencyMu.RUnlock()
	if latencyKept {
		t.Fatal("latency cache repopulated by a displaced frame after delete")
	}
}

// TestSSEStreamEndsAtExactExpiry: the token deadline is enforced by its own
// timer, independent of the periodic revocation check.
func TestSSEStreamEndsAtExactExpiry(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	old := sseRevalidateInterval
	sseRevalidateInterval = 10 * time.Second // periodic check must not be what ends it
	defer func() { sseRevalidateInterval = old }()

	var userID string
	if err := s.db.QueryRow(`SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(1500 * time.Millisecond).Unix()
	claims := jwt.MapClaims{"user_id": userID, "username": "admin", "type": "sse", "exp": exp}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(jwtSecret)
	if err != nil {
		t.Fatal(err)
	}
	resp, r := openEventStream(t, server, token)
	defer resp.Body.Close()
	start := time.Now()
	streamEndsWithin(t, r, 4*time.Second, "exact token expiry")
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("stream ended too late for an exact expiry timer: %s", elapsed)
	}
}

// TestDeleteRemovalEventIsLast: a frame parked inside the barrier finishes,
// its broadcast goes out, and only then does machine_removed follow; nothing
// for the machine is broadcast after it.
func TestDeleteRemovalEventIsLast(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAndGetToken(t, e)

	conn := s.connectEnrolledAgent(t, server, "machine-A")
	defer conn.Close()
	readAISessionsConfig(t, conn)
	writeFrame(t, conn, metricsFrame("machine-A", "settled"))
	waitHostname(t, s, "machine-A", "settled")

	events, unsubscribe := subscribeBroadcast(t)
	defer unsubscribe()

	entered, release := holdFirstIngest(t, "machine-A")
	writeFrame(t, conn, metricsFrame("machine-A", "parked"))
	select {
	case <-entered:
	case <-time.After(lifecycleDeadline):
		t.Fatal("frame never entered the barrier")
	}
	deleteCode := deleteMachineAsync(e, adminToken, "machine-A")
	select {
	case code := <-deleteCode:
		t.Fatalf("delete completed (%d) while a frame was parked", code)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	select {
	case code := <-deleteCode:
		if code != http.StatusOK {
			t.Fatalf("delete: %d", code)
		}
	case <-time.After(lifecycleDeadline):
		t.Fatal("delete never completed")
	}
	// Late frame on the dead socket: must produce no event.
	_ = conn.WriteJSON(metricsFrame("machine-A", "after-delete"))

	var order []string
	deadline := time.After(lifecycleDeadline)
collect:
	for {
		select {
		case ev := <-events:
			text := string(ev)
			switch {
			case strings.HasPrefix(text, "event: machine_removed"):
				order = append(order, "removed")
				break collect
			case strings.Contains(text, `"parked"`):
				order = append(order, "parked-metrics")
			case strings.Contains(text, `"after-delete"`):
				order = append(order, "late-metrics")
			}
		case <-deadline:
			t.Fatalf("machine_removed never arrived; saw %v", order)
		}
	}
	if len(order) != 2 || order[0] != "parked-metrics" || order[1] != "removed" {
		t.Fatalf("event order = %v, want [parked-metrics removed]", order)
	}
	select {
	case ev := <-events:
		if strings.Contains(string(ev), "machine-A") {
			t.Fatalf("event for the deleted machine after machine_removed: %q", ev)
		}
	case <-time.After(300 * time.Millisecond):
	}
}

// TestAnnounceCannotRearmReconnectAfterDelete: the update announce runs on
// its own goroutine; its reconnect expectation must arm only while the
// agent still owns the registry entry, so a delete cannot be followed by a
// late arm and a phantom rollout failure.
func TestAnnounceCannotRearmReconnectAfterDelete(t *testing.T) {
	_, s := setupTestServer(t)
	agent := &ConnectedAgent{MachineID: "machine-A"}
	s.agentsMu.Lock()
	s.agents["machine-A"] = agent
	s.agentsMu.Unlock()
	defer func() {
		pendingReconnectsMu.Lock()
		delete(pendingReconnects, "machine-A")
		pendingReconnectsMu.Unlock()
	}()

	if !s.armReconnectIfRegistered("machine-A", agent) {
		t.Fatal("registered agent must be able to arm its reconnect expectation")
	}
	pendingReconnectsMu.Lock()
	_, armed := pendingReconnects["machine-A"]
	pendingReconnectsMu.Unlock()
	if !armed {
		t.Fatal("expectation not armed for registered agent")
	}

	// What delete does: take the entry, clear the expectation.
	s.agentsMu.Lock()
	delete(s.agents, "machine-A")
	s.agentsMu.Unlock()
	clearReconnectExpectation("machine-A")

	if s.armReconnectIfRegistered("machine-A", agent) {
		t.Fatal("late announce armed a reconnect expectation for a deleted machine")
	}
	pendingReconnectsMu.Lock()
	_, armed = pendingReconnects["machine-A"]
	pendingReconnectsMu.Unlock()
	if armed {
		t.Fatal("reconnect expectation re-armed after delete")
	}
}

// TestMachineRemovedKicksOverflowedStream: a subscriber whose queue is full
// cannot silently lose machine_removed. Data events stay lossy for it; the
// control event ends the stream so the client re-snapshots.
func TestMachineRemovedKicksOverflowedStream(t *testing.T) {
	ch := make(chan []byte, 64)
	kick := &sseKick{ch: make(chan struct{})}
	sseClientsMu.Lock()
	sseClients[ch] = struct{}{}
	sseKicks[ch] = kick
	sseClientsMu.Unlock()
	defer func() {
		sseClientsMu.Lock()
		delete(sseClients, ch)
		delete(sseKicks, ch)
		sseClientsMu.Unlock()
	}()

	for i := 0; i < 64; i++ {
		broadcastSSE([]byte("event: metrics\ndata: {}\n\n"))
	}
	if len(ch) != 64 {
		t.Fatalf("queue not full: %d", len(ch))
	}
	// A dropped data event must not kick.
	broadcastSSE([]byte("event: metrics\ndata: {}\n\n"))
	select {
	case <-kick.ch:
		t.Fatal("data overflow kicked the stream")
	default:
	}
	// A dropped control event must.
	broadcastSSEControl(machineRemovedEvent("machine-A"))
	select {
	case <-kick.ch:
	case <-time.After(lifecycleDeadline):
		t.Fatal("machine_removed overflow did not kick the stream")
	}
	// A stream with room receives the control event normally, no kick.
	room := make(chan []byte, 1)
	roomKick := &sseKick{ch: make(chan struct{})}
	sseClientsMu.Lock()
	sseClients[room] = struct{}{}
	sseKicks[room] = roomKick
	sseClientsMu.Unlock()
	defer func() {
		sseClientsMu.Lock()
		delete(sseClients, room)
		delete(sseKicks, room)
		sseClientsMu.Unlock()
	}()
	broadcastSSEControl(machineRemovedEvent("machine-B"))
	select {
	case ev := <-room:
		if !strings.Contains(string(ev), `"machine-B"`) {
			t.Fatalf("unexpected event %q", ev)
		}
	default:
		t.Fatal("control event not delivered to a stream with room")
	}
	select {
	case <-roomKick.ch:
		t.Fatal("stream with room was kicked")
	default:
	}
}

// TestSSEStreamEndsOnControlOverflow drives the real handler: a client that
// never reads sees its stream ended once a control event overflows.
func TestSSEStreamEndsOnControlOverflow(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()
	adminToken := loginAndGetToken(t, e)
	resp, r := openEventStream(t, server, adminToken)
	defer resp.Body.Close()

	// Find the handler's channel and fill it without the client reading.
	var target chan []byte
	waitForCondition(t, lifecycleDeadline, func() bool {
		sseClientsMu.RLock()
		defer sseClientsMu.RUnlock()
		for ch := range sseClients {
			if sseKicks[ch] != nil {
				target = ch
				return true
			}
		}
		return false
	})
	// Large frames fill the unread TCP buffers quickly; once the handler's
	// writes block, its queue fills and stays full.
	filler := []byte("event: metrics\ndata: {\"pad\":\"" + strings.Repeat("x", 32*1024) + "\"}\n\n")
	fillDeadline := time.Now().Add(lifecycleDeadline)
	for len(target) < cap(target) {
		broadcastSSE(filler)
		if time.Now().After(fillDeadline) {
			t.Fatalf("could not fill the stream queue (%d/%d)", len(target), cap(target))
		}
		if len(target) < cap(target) {
			time.Sleep(time.Millisecond)
		}
	}
	broadcastSSEControl(machineRemovedEvent("machine-A"))
	streamEndsWithin(t, r, lifecycleDeadline, "control-event overflow")
}

// TestFreshEnrollmentVersionReportHeldUntilCommit: an aborted enrollment
// leaves no /api/versions entry; a committed one keeps its initial report.
func TestFreshEnrollmentVersionReportHeldUntilCommit(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	server := httptest.NewServer(e)
	defer server.Close()

	enrollTo := func(machineID string, commit bool) {
		t.Helper()
		if _, err := s.db.Exec(`DELETE FROM tokens`); err != nil {
			t.Fatal(err)
		}
		token := s.seedValidToken(t)
		conn, err := wsDialAgent(t, server, "token="+token)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		writeFrame(t, conn, metricsFrame(machineID, "host"))
		_ = conn.SetReadDeadline(time.Now().Add(lifecycleDeadline))
		for {
			var frame struct {
				Type string `json:"type"`
			}
			_, raw, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("waiting for enrolled: %v", err)
			}
			_ = json.Unmarshal(raw, &frame)
			if frame.Type == "enrolled" {
				break
			}
		}
		writeFrame(t, conn, map[string]interface{}{"type": "agent_running_version", "machine_id": machineID, "sha256": "abc123", "os": "linux"})
		if commit {
			commitEnrollment(t, conn)
		}
		// Order the assertion behind the frames above.
		writeFrame(t, conn, metricsFrame(machineID, "host-2"))
		waitHostname(t, s, machineID, "host-2")
	}

	enrollTo("machine-abort", false)
	agentRunningVersionsMu.RLock()
	_, phantom := agentRunningVersions["machine-abort"]
	agentRunningVersionsMu.RUnlock()
	if phantom {
		t.Fatal("aborted enrollment left a version entry")
	}

	enrollTo("machine-commit", true)
	agentRunningVersionsMu.RLock()
	info, kept := agentRunningVersions["machine-commit"]
	agentRunningVersionsMu.RUnlock()
	if !kept || info.RunningSHA != "abc123" {
		t.Fatalf("committed enrollment lost its initial version report: kept=%v info=%+v", kept, info)
	}
}

// Keep echo imported for route-level helpers used above.
var _ = echo.New
