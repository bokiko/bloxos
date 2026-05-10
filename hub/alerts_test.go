package main

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// setupAlertsDB opens a fresh in-memory SQLite DB with migrations applied.
func setupAlertsDB(t *testing.T) {
	t.Helper()
	var err error
	db, err = sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open alerts test db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := runMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	// Reset global SSE clients so broadcastAlertSSE doesn't block.
	sseClientsMu.Lock()
	sseClients = make(map[chan []byte]struct{})
	sseClientsMu.Unlock()
}

func insertAlertRule(t *testing.T, metric, operator string, threshold float64) string {
	t.Helper()
	id := uuid.New().String()
	_, err := db.Exec(
		`INSERT INTO alert_rules (id, name, metric, operator, threshold, duration_secs, severity, enabled)
		 VALUES (?, ?, ?, ?, ?, 0, 'warning', TRUE)`,
		id, metric+"-rule", metric, operator, threshold,
	)
	if err != nil {
		t.Fatalf("insert alert rule: %v", err)
	}
	return id
}

func insertMachineWithMetrics(t *testing.T, machineID string, cpu float64, ramUsed, ramTotal, diskUsed, diskTotal int64) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO machines (id, hostname, status, last_seen) VALUES (?, ?, 'online', ?)`,
		machineID, "host-"+machineID, time.Now().UTC().Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		t.Fatalf("insert machine: %v", err)
	}
	_, err = db.Exec(
		`INSERT INTO metrics (machine_id, cpu_percent, ram_used_bytes, ram_total_bytes,
		 disk_used_bytes, disk_total_bytes) VALUES (?, ?, ?, ?, ?, ?)`,
		machineID, cpu, ramUsed, ramTotal, diskUsed, diskTotal,
	)
	if err != nil {
		t.Fatalf("insert metrics: %v", err)
	}
}

func alertCount(t *testing.T, machineID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM alerts WHERE machine_id = ?`, machineID).Scan(&n); err != nil {
		t.Fatalf("alert count: %v", err)
	}
	return n
}

func activeAlertCount(t *testing.T, machineID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM alerts WHERE machine_id = ? AND status = 'active'`, machineID).Scan(&n); err != nil {
		t.Fatalf("active alert count: %v", err)
	}
	return n
}

// TestEvaluateAlerts_GTRuleTriggersAndRefreshes verifies that a 'gt' rule
// fires a new alert and then refreshes its message on subsequent evaluations.
func TestEvaluateAlerts_GTRuleTriggersAndRefreshes(t *testing.T) {
	setupAlertsDB(t)
	mID := "machine-gt-test"
	insertMachineWithMetrics(t, mID, 95.0, 800, 1000, 500, 1000)
	insertAlertRule(t, "cpu", "gt", 90.0)

	evaluateAlerts()
	if got := activeAlertCount(t, mID); got != 1 {
		t.Fatalf("want 1 active alert after trigger, got %d", got)
	}

	// Second evaluation — already active, should refresh message, not create another.
	evaluateAlerts()
	if got := activeAlertCount(t, mID); got != 1 {
		t.Fatalf("want still 1 active alert after refresh, got %d", got)
	}
}

// TestEvaluateAlerts_Recovery verifies that a resolved condition clears the alert.
func TestEvaluateAlerts_Recovery(t *testing.T) {
	setupAlertsDB(t)
	mID := "machine-recover-test"
	insertMachineWithMetrics(t, mID, 95.0, 800, 1000, 500, 1000)
	ruleID := insertAlertRule(t, "cpu", "gt", 90.0)

	evaluateAlerts()
	if got := activeAlertCount(t, mID); got != 1 {
		t.Fatalf("want 1 active alert after trigger, got %d", got)
	}

	// Drop CPU below threshold by updating the existing row so the window
	// function still sees exactly one row with the latest timestamp.
	if _, err := db.Exec(`UPDATE metrics SET cpu_percent = 50.0 WHERE machine_id = ?`, mID); err != nil {
		t.Fatalf("update recovery metric: %v", err)
	}

	evaluateAlerts()
	if got := activeAlertCount(t, mID); got != 0 {
		t.Fatalf("want 0 active alerts after recovery, got %d", got)
	}
	// Row still exists but resolved.
	_ = ruleID
	var status string
	if err := db.QueryRow(`SELECT status FROM alerts WHERE machine_id = ?`, mID).Scan(&status); err != nil {
		t.Fatalf("query alert status: %v", err)
	}
	if status != "resolved" {
		t.Fatalf("want status='resolved', got %q", status)
	}
}

// TestEvaluateAlerts_ZeroRAMNoFalseTrigger verifies that a machine with no RAM
// data (ramTotalBytes=0) does NOT fire a 'lt' RAM alert.
func TestEvaluateAlerts_ZeroRAMNoFalseTrigger(t *testing.T) {
	setupAlertsDB(t)
	mID := "machine-zero-ram"
	insertMachineWithMetrics(t, mID, 0, 0, 0, 0, 1000) // ramTotal = 0
	insertAlertRule(t, "ram", "lt", 10.0)               // would fire if metricValue=0

	evaluateAlerts()
	if got := alertCount(t, mID); got != 0 {
		t.Fatalf("want 0 alerts for zero-RAM machine, got %d (false trigger bug)", got)
	}
}

// TestEvaluateAlerts_ZeroDiskNoFalseTrigger mirrors the RAM test for disk.
func TestEvaluateAlerts_ZeroDiskNoFalseTrigger(t *testing.T) {
	setupAlertsDB(t)
	mID := "machine-zero-disk"
	insertMachineWithMetrics(t, mID, 0, 800, 1000, 0, 0) // diskTotal = 0
	insertAlertRule(t, "disk", "lt", 10.0)

	evaluateAlerts()
	if got := alertCount(t, mID); got != 0 {
		t.Fatalf("want 0 alerts for zero-disk machine, got %d (false trigger bug)", got)
	}
}

// TestEvaluateAlerts_ZeroGPUTempSkipped verifies gpu_temp=0 is not evaluated.
func TestEvaluateAlerts_ZeroGPUTempSkipped(t *testing.T) {
	setupAlertsDB(t)
	mID := "machine-no-gpu"
	insertMachineWithMetrics(t, mID, 50, 500, 1000, 200, 1000)
	insertAlertRule(t, "gpu_temp", "gt", 80.0)

	evaluateAlerts()
	if got := alertCount(t, mID); got != 0 {
		t.Fatalf("want 0 alerts when gpu_temp=0, got %d", got)
	}
}

// TestEvaluateAlerts_MachineOffline verifies the offline rule fires when
// last_seen exceeds the threshold (using the SQLite timestamp format).
func TestEvaluateAlerts_MachineOffline(t *testing.T) {
	setupAlertsDB(t)
	mID := "machine-offline-test"
	staleTime := time.Now().Add(-5 * time.Minute).UTC().Format("2006-01-02 15:04:05")
	_, err := db.Exec(`INSERT INTO machines (id, hostname, status, last_seen) VALUES (?, 'stale', 'offline', ?)`,
		mID, staleTime)
	if err != nil {
		t.Fatalf("insert stale machine: %v", err)
	}
	insertAlertRule(t, "machine_offline", "gt", 120.0)

	evaluateAlerts()
	if got := activeAlertCount(t, mID); got != 1 {
		t.Fatalf("want 1 active offline alert, got %d", got)
	}
}
