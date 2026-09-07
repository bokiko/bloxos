package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

func TestAlertAcknowledgedIncidentDoesNotRefireAndResolves(t *testing.T) {
	s := setupAlertsDB(t)
	s.insertMachineWithMetrics(t, "ack-machine", 95, 5, 10, 5, 10)
	s.insertAlertRule(t, "cpu", "gt", 90)
	s.evaluateAlerts()
	var id string
	if err := s.db.QueryRow(`SELECT id FROM alerts`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	e := echo.New()
	rec := httptest.NewRecorder()
	c := e.NewContext(httptest.NewRequest(http.MethodPost, "/", nil), rec)
	c.SetParamNames("id")
	c.SetParamValues(id)
	if err := s.handleAcknowledgeAlert(c); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("ack: %v, %d", err, rec.Code)
	}
	for i := 0; i < 3; i++ {
		s.evaluateAlerts()
	}
	if got := s.alertCount(t, "ack-machine"); got != 1 {
		t.Fatalf("acknowledged condition refired: %d", got)
	}
	if _, err := s.db.Exec(`UPDATE metrics SET cpu_percent = 20`); err != nil {
		t.Fatal(err)
	}
	s.evaluateAlerts()
	var status string
	if err := s.db.QueryRow(`SELECT status FROM alerts WHERE id = ?`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "resolved" {
		t.Fatalf("acknowledged incident not resolved: %s", status)
	}
	if _, err := s.db.Exec(`UPDATE metrics SET cpu_percent = 99`); err != nil {
		t.Fatal(err)
	}
	s.evaluateAlerts()
	if got := s.alertCount(t, "ack-machine"); got != 2 {
		t.Fatalf("new incident missing after recovery: %d", got)
	}
}

func TestAlertDurationRequiresContinuousObservedCondition(t *testing.T) {
	s := setupAlertsDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	s.insertMachineWithMetrics(t, "duration-machine", 95, 5, 10, 5, 10)
	rule := s.insertAlertRule(t, "cpu", "gt", 90)
	if _, err := s.db.Exec(`UPDATE alert_rules SET duration_secs = 60 WHERE id = ?`, rule); err != nil {
		t.Fatal(err)
	}
	s.evaluateAlertsAt(now)
	s.evaluateAlertsAt(now.Add(30 * time.Second))
	if got := s.alertCount(t, "duration-machine"); got != 0 {
		t.Fatal("fired before duration")
	}
	// A dip resets the timer, rather than accumulating separated spikes.
	if _, err := s.db.Exec(`UPDATE metrics SET cpu_percent = 30`); err != nil {
		t.Fatal(err)
	}
	s.evaluateAlertsAt(now.Add(31 * time.Second))
	if _, err := s.db.Exec(`UPDATE metrics SET cpu_percent = 95`); err != nil {
		t.Fatal(err)
	}
	s.evaluateAlertsAt(now.Add(32 * time.Second))
	s.evaluateAlertsAt(now.Add(91 * time.Second))
	if got := s.alertCount(t, "duration-machine"); got != 0 {
		t.Fatal("non-contiguous spikes counted")
	}
	s.evaluateAlertsAt(now.Add(92 * time.Second))
	if got := s.activeAlertCount(t, "duration-machine"); got != 1 {
		t.Fatalf("sustained condition missing: %d", got)
	}
}

func TestAlertUnavailableAndOfflineMetricsAreNotZeroOrRecovery(t *testing.T) {
	s := setupAlertsDB(t)
	s.insertMachineWithMetrics(t, "unknown-machine", 95, 5, 10, 5, 10)
	s.insertAlertRule(t, "cpu", "gt", 90)
	s.insertAlertRule(t, "cpu", "lt", 1)
	s.evaluateAlerts()
	if got := s.activeAlertCount(t, "unknown-machine"); got != 1 {
		t.Fatal(got)
	}
	if _, err := s.db.Exec(`UPDATE metrics SET cpu_percent = NULL`); err != nil {
		t.Fatal(err)
	}
	s.evaluateAlerts()
	if got := s.activeAlertCount(t, "unknown-machine"); got != 1 {
		t.Fatal("missing CPU caused recovery or low-CPU alert", got)
	}
	if _, err := s.db.Exec(`UPDATE metrics SET cpu_percent = 0`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE machines SET status = 'offline'`); err != nil {
		t.Fatal(err)
	}
	s.evaluateAlerts()
	if got := s.alertCount(t, "unknown-machine"); got != 1 {
		t.Fatal("offline stale metric triggered", got)
	}
	if got := s.activeAlertCount(t, "unknown-machine"); got != 1 {
		t.Fatal("offline metric falsely resolved", got)
	}
	if _, err := s.db.Exec(`UPDATE machines SET status = 'online'`); err != nil {
		t.Fatal(err)
	}
	s.evaluateAlertsAt(time.Now().Add(3 * time.Minute))
	if got := s.alertCount(t, "unknown-machine"); got != 1 {
		t.Fatal("aged metric triggered", got)
	}
}

func TestAlertMissingDataResetsPendingDuration(t *testing.T) {
	s := setupAlertsDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	s.insertMachineWithMetrics(t, "gap-machine", 95, 5, 10, 5, 10)
	rule := s.insertAlertRule(t, "cpu", "gt", 90)
	if _, err := s.db.Exec(`UPDATE alert_rules SET duration_secs = 60 WHERE id = ?`, rule); err != nil {
		t.Fatal(err)
	}
	s.evaluateAlertsAt(now)
	if _, err := s.db.Exec(`UPDATE metrics SET cpu_percent = NULL`); err != nil {
		t.Fatal(err)
	}
	s.evaluateAlertsAt(now.Add(30 * time.Second))
	if _, err := s.db.Exec(`UPDATE metrics SET cpu_percent = 95`); err != nil {
		t.Fatal(err)
	}
	s.evaluateAlertsAt(now.Add(60 * time.Second))
	if got := s.alertCount(t, "gap-machine"); got != 0 {
		t.Fatal("unknown interval counted", got)
	}
}

func TestTelegramDeliveryBoundedBeforeHeadersAndDuringBody(t *testing.T) {
	for _, bodyStall := range []bool{false, true} {
		t.Run(map[bool]string{false: "headers", true: "body"}[bodyStall], func(t *testing.T) {
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if bodyStall {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release) // A server with unread request body may not observe peer-close yet.
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			start := time.Now()
			err := deliverTelegram(ctx, &http.Client{Timeout: time.Second}, server.URL, "fake-chat", "test-only")
			if err == nil {
				t.Fatal("stalled server unexpectedly completed")
			}
			if time.Since(start) > 2*time.Second {
				t.Fatal("delivery did not obey deadline")
			}
		})
	}
}

func TestAlertLegacyDuplicateIncidentsResolveTogetherAndAcknowledgmentsSurviveCleanup(t *testing.T) {
	s := setupAlertsDB(t)
	s.insertMachineWithMetrics(t, "legacy-machine", 95, 5, 10, 5, 10)
	rule := s.insertAlertRule(t, "cpu", "gt", 90)
	for _, status := range []string{"active", "acknowledged"} {
		if _, err := s.db.Exec(`INSERT INTO alerts (id,rule_id,machine_id,message,severity,status,triggered_at) VALUES (?,?,'legacy-machine','old','warning',?,datetime('now','-40 days'))`, status, rule, status); err != nil {
			t.Fatal(err)
		}
	}
	s.runCleanup()
	if got := s.alertCount(t, "legacy-machine"); got != 2 {
		t.Fatal("cleanup discarded ongoing incident", got)
	}
	s.evaluateAlerts()
	if got := s.alertCount(t, "legacy-machine"); got != 2 {
		t.Fatal("legacy incident refired", got)
	}
	if _, err := s.db.Exec(`UPDATE metrics SET cpu_percent = 20`); err != nil {
		t.Fatal(err)
	}
	s.evaluateAlerts()
	var resolved int
	if err := s.db.QueryRow(`SELECT count(*) FROM alerts WHERE status='resolved'`).Scan(&resolved); err != nil {
		t.Fatal(err)
	}
	if resolved != 2 {
		t.Fatalf("legacy duplicates not both resolved: %d", resolved)
	}
}

func TestAlertUnknownRAMAndDiskUsedDoNotTrigger(t *testing.T) {
	s := setupAlertsDB(t)
	s.insertMachineWithMetrics(t, "partial-machine", 30, 5, 10, 5, 10)
	s.insertAlertRule(t, "ram", "lt", 1)
	s.insertAlertRule(t, "disk", "lt", 1)
	if _, err := s.db.Exec(`UPDATE metrics SET ram_used_bytes=NULL,disk_used_bytes=NULL`); err != nil {
		t.Fatal(err)
	}
	s.evaluateAlerts()
	if got := s.alertCount(t, "partial-machine"); got != 0 {
		t.Fatalf("unknown sensors became zeros: %d", got)
	}
}
