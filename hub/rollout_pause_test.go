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
	s := newServer(db)
	// Migrations have run, so build the controller — the explicit step main()
	// performs after initDB. Without it a resume correctly refuses: there is
	// no controller to retry the failed attempts with.
	if err := s.initRollout(); err != nil {
		t.Fatalf("init rollout controller: %v", err)
	}
	return s
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

// isolateAutomaticPause used to save and restore the process-wide automatic
// breaker. That breaker is gone: a platform halt replaces it, and halts live in
// the database each Server owns, so there is no shared state to isolate.
//
// Kept as a no-op rather than deleted from every call site, so the tests below
// keep reading as "this one is about the operator pause, not the automatic
// one" — which is the distinction they exist to police.
func isolateAutomaticPause(t *testing.T) { t.Helper() }

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
	// Halt the platform, as a failed attempt would.
	if err := haltPlatformForTest(t, s, "linux/amd64", before); err != nil {
		t.Fatalf("halt: %v", err)
	}

	if err := os.WriteFile(binary, []byte("changed binary fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	recomputeBinaryFor("linux")
	after := currentAgentBinaryState("linux").SHA
	if after == before {
		t.Fatal("test did not change served SHA")
	}

	// A new build must clear the halt. It no longer does so by resetting a
	// global: the changed candidate starts a new GENERATION, so the halt
	// belongs to a rollout that is over. Asserting the observable outcome
	// rather than the mechanism is the point — the old test asserted a boolean
	// that no longer exists, while this behaviour still has to hold.
	if _, _, err := s.rollout.reserve("linux/amd64", "halt-reset-probe", after, 0); err != nil {
		t.Fatalf("reserve after the candidate changed: %v", err)
	}
	st, err := s.rollout.platformState("linux/amd64")
	if err != nil || st == nil {
		t.Fatalf("platform state: %v", err)
	}
	if st.Status == rolloutHalted {
		t.Fatalf("a new build did not clear the halt: %s", st.HaltReason)
	}
	// The probe took the canary slot. Release it, and the halted fixture's
	// slot with it, so the machine this test is actually about can be admitted
	// — otherwise the assertion below would fail on capacity rather than on
	// the pause behaviour it exists to check.
	if _, err := s.db.Exec(`DELETE FROM agent_rollout_slot WHERE machine_id IN (?, ?)`,
		"halt-reset-probe", "halt-victim"); err != nil {
		t.Fatalf("clear fixture slots: %v", err)
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
	agent := &ConnectedAgent{MachineID: "pause-regression", Conn: conn}
	// Register it, as a real connection is. The send boundary now verifies
	// the registry still owns the machine before writing, so an announcement
	// that queued behind unrelated socket writes cannot go down a socket that
	// has since been displaced or deleted. An unregistered connection is
	// correctly refused, which production never produces.
	s.agentsMu.Lock()
	s.agents["pause-regression"] = agent
	s.agentsMu.Unlock()
	t.Cleanup(func() {
		s.agentsMu.Lock()
		delete(s.agents, "pause-regression")
		s.agentsMu.Unlock()
	})
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

	// An unrelated socket writer must not hold Pause hostage, and a pause that
	// is ACKNOWLEDGED must stop an announcement that has already reserved its
	// slot but not yet written.
	//
	// The old version slept 50ms and relied on the announcement queueing
	// behind a held WriteMu. That is no longer what happens: the scheduler
	// TryLocks, so a busy socket is skipped instantly and there is nothing
	// queued to race. The test would have passed while exercising none of the
	// boundary it names. This one stops the goroutine AT the boundary instead.
	atBoundary := make(chan struct{})
	release := make(chan struct{})
	announceSendBoundaryHook = func(id string) {
		if id != "pause-regression" {
			return
		}
		close(atBoundary)
		<-release
	}
	t.Cleanup(func() { announceSendBoundaryHook = nil })

	// Clear the slot so this announcement is a fresh admission rather than a
	// duplicate trigger, which would return before reaching the boundary.
	if _, err := s.db.Exec(`DELETE FROM agent_rollout_slot WHERE machine_id = ?`,
		"pause-regression"); err != nil {
		t.Fatalf("clear slot: %v", err)
	}

	announceDone := make(chan struct{})
	go func() { s.announceVersionToAgent("pause-regression", agent); close(announceDone) }()
	select {
	case <-atBoundary:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("the announcement never reached its send boundary")
	}

	// Pause must not wait behind the in-flight announcement, which is holding
	// this agent's write lock.
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
			close(release)
			t.Fatalf("pause returned %d", status)
		}
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("Pause waited behind an in-flight announcement")
	}

	// The pause is acknowledged. Only now is the announcement allowed to
	// continue, and nothing may escape.
	close(release)
	select {
	case <-announceDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the announcement did not drain")
	}
	_ = client.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if _, data, err := client.ReadMessage(); err == nil {
		t.Fatalf("an announcement escaped an acknowledged pause: %s", data)
	}
}

// haltPlatformForTest drives a platform into a halt the way a failed attempt
// does: reserve a slot, let its window lapse, and tick.
func haltPlatformForTest(t *testing.T, s *Server, platform, candidate string) error {
	t.Helper()
	if _, _, err := s.rollout.reserve(platform, "halt-victim", candidate, 0); err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE agent_rollout_slot SET deadline_unix_ms = 0
		WHERE platform = ? AND machine_id = ?`, platform, "halt-victim"); err != nil {
		return err
	}
	if err := s.rollout.tick(platform); err != nil {
		return err
	}
	st, err := s.rollout.platformState(platform)
	if err != nil {
		return err
	}
	if st == nil || st.Status != rolloutHalted {
		t.Fatalf("fixture did not halt the platform: %+v", st)
	}
	return nil
}
