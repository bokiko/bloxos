package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

func openPauseTestServer(t *testing.T, path string) *Server {
	t.Helper()
	db, err := sql.Open("sqlite", databaseDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := runMigrations(db); err != nil {
		t.Fatal(err)
	}
	return newServer(db)
}

func requestOperatorPause(t *testing.T, s *Server, pause bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/", nil), r)
	var err error
	if pause {
		err = s.handlePauseRollout(c)
	} else {
		err = s.handleResumeRollout(c)
	}
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func isolateAutomaticPause(t *testing.T) {
	t.Helper()
	rolloutPausedMu.Lock()
	old, reason := rolloutPaused, rolloutPauseReason
	rolloutPaused, rolloutPauseReason = false, ""
	rolloutPausedMu.Unlock()
	rolloutFailuresMu.Lock()
	failures := append([]rolloutFailure(nil), rolloutFailures...)
	rolloutFailures = nil
	rolloutFailuresMu.Unlock()
	t.Cleanup(func() {
		rolloutPausedMu.Lock()
		rolloutPaused, rolloutPauseReason = old, reason
		rolloutPausedMu.Unlock()
		rolloutFailuresMu.Lock()
		rolloutFailures = failures
		rolloutFailuresMu.Unlock()
	})
}

func TestOperatorPauseSurvivesDatabaseReopen(t *testing.T) {
	isolateAutomaticPause(t)
	path := filepath.Join(t.TempDir(), "hub.db")
	s := openPauseTestServer(t, path)
	if paused, _ := s.operatorRolloutPause(); paused {
		t.Fatal("new hub starts paused")
	}
	if r := requestOperatorPause(t, s, true); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := openPauseTestServer(t, path)
	if paused, _ := restarted.operatorRolloutPause(); !paused {
		t.Fatal("restart lost operator pause")
	}
	if r := requestOperatorPause(t, restarted, false); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if err := restarted.db.Close(); err != nil {
		t.Fatal(err)
	}
	again := openPauseTestServer(t, path)
	if paused, _ := again.operatorRolloutPause(); paused {
		t.Fatal("explicit resume was not persisted")
	}
}

func TestOperatorPauseWriteFailuresDoNotClaimSuccess(t *testing.T) {
	isolateAutomaticPause(t)
	s := openPauseTestServer(t, filepath.Join(t.TempDir(), "hub.db"))
	if _, err := s.db.Exec(`CREATE TRIGGER reject_pause BEFORE INSERT ON hub_settings BEGIN SELECT RAISE(ABORT, 'disk failure'); END`); err != nil {
		t.Fatal(err)
	}
	if r := requestOperatorPause(t, s, true); r.Code != http.StatusServiceUnavailable {
		t.Fatal(r.Code)
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_pause`); err != nil {
		t.Fatal(err)
	}
	if r := requestOperatorPause(t, s, true); r.Code != 200 {
		t.Fatal(r.Code)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_resume BEFORE UPDATE ON hub_settings BEGIN SELECT RAISE(ABORT, 'disk failure'); END`); err != nil {
		t.Fatal(err)
	}
	if r := requestOperatorPause(t, s, false); r.Code != http.StatusServiceUnavailable {
		t.Fatal(r.Code)
	}
	if paused, _ := s.operatorRolloutPause(); !paused {
		t.Fatal("failed resume cleared pause")
	}
}

func TestOperatorPauseWaitsForAnnouncementGate(t *testing.T) {
	s := openPauseTestServer(t, filepath.Join(t.TempDir(), "hub.db"))
	// Model an announcement already inside the send gate. A successful pause
	// must wait for it, then prevent subsequent readers from announcing.
	s.operatorRolloutMu.RLock()
	done := make(chan int, 1)
	go func() {
		r := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/", nil), r)
		if err := s.handlePauseRollout(c); err != nil {
			done <- 500
			return
		}
		done <- r.Code
	}()
	select {
	case <-done:
		s.operatorRolloutMu.RUnlock()
		t.Fatal("pause acknowledged before the active announcement gate drained")
	case <-time.After(50 * time.Millisecond):
	}
	s.operatorRolloutMu.RUnlock()
	select {
	case status := <-done:
		if status != 200 {
			t.Fatal(status)
		}
	case <-time.After(time.Second):
		t.Fatal("pause did not complete after announcement gate drained")
	}
	if paused, _ := s.operatorRolloutPause(); !paused {
		t.Fatal("pause was not saved before acknowledgment")
	}
}

func TestOperatorPauseUnreadableAndCorruptFailClosed(t *testing.T) {
	s := openPauseTestServer(t, filepath.Join(t.TempDir(), "hub.db"))
	if _, err := s.db.Exec(`INSERT INTO hub_settings(key, value) VALUES (?, 'broken')`, operatorRolloutPauseKey); err != nil {
		t.Fatal(err)
	}
	if paused, reason := s.operatorRolloutPause(); !paused || !strings.Contains(reason, "invalid") {
		t.Fatal(paused, reason)
	}
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	if paused, reason := s.operatorRolloutPause(); !paused || !strings.Contains(reason, "unreadable") {
		t.Fatal(paused, reason)
	}
	// This must return at the durable pause gate, before using an agent socket.
	s.announceVersionToAgent("no-connection", nil)
}

func TestOperatorPauseSurvivesSHAChangeAndSuppressesAnnouncements(t *testing.T) {
	isolateAutomaticPause(t)
	_, s := setupTestServer(t)
	t.Setenv("PUBLIC_URL", "https://hub.example.com")
	s.seedTestMachine(t, "pause-regression")
	agentRunningVersionsMu.Lock()
	previous, existed := agentRunningVersions["pause-regression"]
	agentRunningVersions["pause-regression"] = agentVersionInfo{MachineID: "pause-regression", OS: "linux", Arch: "amd64", RunningSHA: "older", UpdateProtocol: 1, UpdateKeyPinned: true, UpdateTransportOK: true}
	agentRunningVersionsMu.Unlock()
	t.Cleanup(func() {
		agentRunningVersionsMu.Lock()
		defer agentRunningVersionsMu.Unlock()
		if existed {
			agentRunningVersions["pause-regression"] = previous
		} else {
			delete(agentRunningVersions, "pause-regression")
		}
	})
	if _, err := s.db.Exec(`UPDATE machines SET os = 'linux/amd64' WHERE id = 'pause-regression'`); err != nil {
		t.Fatal(err)
	}
	binary := useGeneratedTestBinary(t, "linux")
	recomputeBinaryFor("linux")
	before := currentAgentBinaryState("linux").SHA
	if r := requestOperatorPause(t, s, true); r.Code != 200 {
		t.Fatal(r.Code)
	}
	rolloutPausedMu.Lock()
	rolloutPaused, rolloutPauseReason = true, "automatic breaker"
	rolloutPausedMu.Unlock()
	if err := os.WriteFile(binary, []byte("changed binary fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	recomputeBinaryFor("linux")
	if currentAgentBinaryState("linux").SHA == before {
		t.Fatal("test did not change served SHA")
	}
	rolloutPausedMu.RLock()
	autoPaused := rolloutPaused
	rolloutPausedMu.RUnlock()
	if autoPaused {
		t.Fatal("new build did not reset automatic breaker")
	}
	if paused, _ := s.operatorRolloutPause(); !paused {
		t.Fatal("SHA change cleared operator pause")
	}

	connections := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			connections <- conn
		}
	}))
	defer server.Close()
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	conn := <-connections
	defer conn.Close()
	agent := &ConnectedAgent{Conn: conn}
	frames := make(chan []byte, 1)
	go func() {
		_, data, err := client.ReadMessage()
		if err == nil {
			frames <- data
		}
	}()
	s.announceVersionToAgent("pause-regression", agent)
	select {
	case data := <-frames:
		t.Fatalf("paused hub announced: %s", data)
	case <-time.After(50 * time.Millisecond):
	}
	r := httptest.NewRecorder()
	if err := s.handleListVersions(echo.New().NewContext(httptest.NewRequest("GET", "/", nil), r)); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Paused bool   `json:"rollout_paused"`
		Reason string `json:"pause_reason"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Paused || !strings.Contains(response.Reason, "saved across restarts") {
		t.Fatal(response)
	}
	if r := requestOperatorPause(t, s, false); r.Code != 200 {
		t.Fatal(r.Code)
	}
	s.announceVersionToAgent("pause-regression", agent)
	select {
	case data := <-frames:
		if !strings.Contains(string(data), "agent_version") {
			t.Fatalf("unexpected frame: %s", data)
		}
	case <-time.After(time.Second):
		t.Fatal("explicit resume did not allow announcement")
	}

	// A different socket writer must not hold Pause hostage. The announcement
	// waiting behind it must recheck the pause before sending anything.
	agent.WriteMu.Lock()
	announceDone := make(chan struct{})
	go func() { s.announceVersionToAgent("pause-regression", agent); close(announceDone) }()
	time.Sleep(50 * time.Millisecond)
	pauseDone := make(chan int, 1)
	go func() {
		r := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest(http.MethodPost, "/", nil), r)
		if err := s.handlePauseRollout(c); err != nil {
			pauseDone <- 500
			return
		}
		pauseDone <- r.Code
	}()
	select {
	case status := <-pauseDone:
		if status != 200 {
			agent.WriteMu.Unlock()
			t.Fatal(status)
		}
	case <-time.After(time.Second):
		agent.WriteMu.Unlock()
		t.Fatal("Pause waited behind an unrelated agent socket writer")
	}
	agent.WriteMu.Unlock()
	select {
	case <-announceDone:
	case <-time.After(time.Second):
		t.Fatal("queued announcement did not drain")
	}
	_ = client.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if _, data, err := client.ReadMessage(); err == nil {
		t.Fatalf("queued announcement escaped pause: %s", data)
	}
}
