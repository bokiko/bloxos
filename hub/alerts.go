package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// AlertRule represents an alert rule from the database.
type AlertRule struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Metric       string  `json:"metric"`
	Operator     string  `json:"operator"`
	Threshold    float64 `json:"threshold"`
	DurationSecs int     `json:"duration_secs"`
	Severity     string  `json:"severity"`
	Enabled      bool    `json:"enabled"`
	CreatedAt    string  `json:"created_at"`
}

// Alert represents a fired alert.
type Alert struct {
	ID          string  `json:"id"`
	RuleID      *string `json:"rule_id"`
	MachineID   string  `json:"machine_id"`
	Message     string  `json:"message"`
	Severity    string  `json:"severity"`
	Status      string  `json:"status"`
	TriggeredAt string  `json:"triggered_at"`
	ResolvedAt  *string `json:"resolved_at"`
	Hostname    string  `json:"hostname,omitempty"`
}

// seedAlertRules inserts default alert rules if the table is empty.
func seedAlertRules() {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM alert_rules`).Scan(&count)
	if err != nil {
		log.Printf("error checking alert_rules count: %v", err)
		return
	}
	if count > 0 {
		return
	}

	defaults := []struct {
		name      string
		metric    string
		operator  string
		threshold float64
		duration  int
		severity  string
	}{
		{"CPU > 90%", "cpu", "gt", 90.0, 0, "warning"},
		{"RAM > 95%", "ram", "gt", 95.0, 0, "warning"},
		{"Disk > 90%", "disk", "gt", 90.0, 0, "warning"},
		{"GPU Temp > 80C", "gpu_temp", "gt", 80.0, 0, "warning"},
		{"GPU Temp > 90C", "gpu_temp", "gt", 90.0, 0, "critical"},
		{"Machine Offline > 120s", "machine_offline", "gt", 120.0, 0, "critical"},
	}

	for _, d := range defaults {
		id := uuid.New().String()
		_, err := db.Exec(`INSERT INTO alert_rules (id, name, metric, operator, threshold, duration_secs, severity) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, d.name, d.metric, d.operator, d.threshold, d.duration, d.severity)
		if err != nil {
			log.Printf("error seeding alert rule %s: %v", d.name, err)
		}
	}
	log.Println("seeded 6 default alert rules")
}

// alertEvalLoop runs every 30 seconds to evaluate alert rules.
func alertEvalLoop() {
	time.Sleep(5 * time.Second) // Wait for startup.
	log.Println("alert evaluation loop started")
	for {
		evaluateAlerts()
		time.Sleep(30 * time.Second)
	}
}

func evaluateAlerts() {
	// Get all enabled rules.
	rules, err := getAlertRules(true)
	if err != nil {
		log.Printf("alert eval: error getting rules: %v", err)
		return
	}

	// Get all machines with latest metrics.
	rows, err := db.Query(`
		SELECT m.id, m.hostname, m.last_seen,
			COALESCE(met.cpu_percent, 0),
			COALESCE(met.ram_used_bytes, 0), COALESCE(met.ram_total_bytes, 0),
			COALESCE(met.disk_used_bytes, 0), COALESCE(met.disk_total_bytes, 0),
			COALESCE(met.gpu_temp, 0)
		FROM machines m
		LEFT JOIN (
			SELECT machine_id, cpu_percent, ram_used_bytes, ram_total_bytes,
				disk_used_bytes, disk_total_bytes, gpu_temp,
				ROW_NUMBER() OVER (PARTITION BY machine_id ORDER BY timestamp DESC) as rn
			FROM metrics
		) met ON met.machine_id = m.id AND met.rn = 1
	`)
	if err != nil {
		log.Printf("alert eval: error querying machines: %v", err)
		return
	}
	defer rows.Close()

	type machineMetrics struct {
		id             string
		hostname       string
		lastSeen       *string
		cpuPercent     float64
		ramUsedBytes   int64
		ramTotalBytes  int64
		diskUsedBytes  int64
		diskTotalBytes int64
		gpuTemp        float64
	}

	var machines []machineMetrics
	for rows.Next() {
		var m machineMetrics
		if err := rows.Scan(&m.id, &m.hostname, &m.lastSeen, &m.cpuPercent,
			&m.ramUsedBytes, &m.ramTotalBytes, &m.diskUsedBytes, &m.diskTotalBytes, &m.gpuTemp); err != nil {
			continue
		}
		machines = append(machines, m)
	}

	for _, rule := range rules {
		for _, m := range machines {
			var metricValue float64
			var triggered bool
			var msg string

			switch rule.Metric {
			case "cpu":
				metricValue = m.cpuPercent
				triggered = compareValue(metricValue, rule.Operator, rule.Threshold)
				msg = fmt.Sprintf("CPU is %.0f%% (threshold: %.0f%%)", metricValue, rule.Threshold)
			case "ram":
				if m.ramTotalBytes > 0 {
					metricValue = float64(m.ramUsedBytes) / float64(m.ramTotalBytes) * 100
				}
				triggered = compareValue(metricValue, rule.Operator, rule.Threshold)
				msg = fmt.Sprintf("RAM is %.0f%% (threshold: %.0f%%)", metricValue, rule.Threshold)
			case "disk":
				if m.diskTotalBytes > 0 {
					metricValue = float64(m.diskUsedBytes) / float64(m.diskTotalBytes) * 100
				}
				triggered = compareValue(metricValue, rule.Operator, rule.Threshold)
				msg = fmt.Sprintf("Disk is %.0f%% (threshold: %.0f%%)", metricValue, rule.Threshold)
			case "gpu_temp":
				metricValue = m.gpuTemp
				if metricValue == 0 {
					continue // No GPU data, skip.
				}
				triggered = compareValue(metricValue, rule.Operator, rule.Threshold)
				msg = fmt.Sprintf("GPU temperature is %.0f C (threshold: %.0f C)", metricValue, rule.Threshold)
			case "machine_offline":
				if m.lastSeen == nil {
					continue
				}
				lastSeenTime, err := time.Parse("2006-01-02 15:04:05", *m.lastSeen)
				if err != nil {
					// Try with timezone.
					lastSeenTime, err = time.Parse(time.RFC3339, *m.lastSeen)
					if err != nil {
						continue
					}
				}
				offlineSecs := time.Since(lastSeenTime).Seconds()
				triggered = offlineSecs > rule.Threshold
				msg = fmt.Sprintf("Machine offline for %.0fs (threshold: %.0fs)", offlineSecs, rule.Threshold)
			default:
				continue
			}

			// Check for existing active alert for this machine+rule.
			var existingID string
			err := db.QueryRow(`SELECT id FROM alerts WHERE rule_id = ? AND machine_id = ? AND status = 'active'`,
				rule.ID, m.id).Scan(&existingID)
			hasActive := err == nil

			if triggered && !hasActive {
				// Create new alert.
				alertID := uuid.New().String()
				_, err := db.Exec(`INSERT INTO alerts (id, rule_id, machine_id, message, severity) VALUES (?, ?, ?, ?, ?)`,
					alertID, rule.ID, m.id, msg, rule.Severity)
				if err != nil {
					log.Printf("alert eval: error creating alert: %v", err)
					continue
				}
				log.Printf("ALERT [%s] %s on %s: %s", rule.Severity, rule.Name, m.hostname, msg)

				// Send SSE.
				alert := Alert{
					ID:        alertID,
					RuleID:    &rule.ID,
					MachineID: m.id,
					Message:   msg,
					Severity:  rule.Severity,
					Status:    "active",
					Hostname:  m.hostname,
				}
				broadcastAlertSSE(alert)

				// Send Telegram.
				sendTelegram(fmt.Sprintf("BloxOS Alert\n\nMachine: %s\nSeverity: %s\n%s",
					m.hostname, rule.Severity, msg))

			} else if triggered && hasActive {
				// Already-active alert: refresh the message so it reflects
				// current state (e.g. offline duration grows from "144s"
				// at fire time to "172800s" two days later) instead of
				// freezing at the moment the threshold was crossed.
				// We don't broadcast SSE here — would spam every connected
				// dashboard with one alert event per machine per 30s. The
				// dashboard picks up the refreshed message on next reload
				// or fetch of /api/alerts.
				_, err := db.Exec(`UPDATE alerts SET message = ? WHERE id = ?`, msg, existingID)
				if err != nil {
					log.Printf("alert eval: error refreshing alert message: %v", err)
					continue
				}

			} else if !triggered && hasActive {
				// Resolve the alert.
				_, err := db.Exec(`UPDATE alerts SET status = 'resolved', resolved_at = CURRENT_TIMESTAMP WHERE id = ?`, existingID)
				if err != nil {
					log.Printf("alert eval: error resolving alert: %v", err)
					continue
				}
				log.Printf("RESOLVED: %s on %s", rule.Name, m.hostname)

				alert := Alert{
					ID:        existingID,
					RuleID:    &rule.ID,
					MachineID: m.id,
					Message:   msg,
					Severity:  rule.Severity,
					Status:    "resolved",
					Hostname:  m.hostname,
				}
				broadcastAlertSSE(alert)

				sendTelegram(fmt.Sprintf("Resolved: %s on %s is back to normal", rule.Name, m.hostname))
			}
		}
	}
}

func compareValue(value float64, operator string, threshold float64) bool {
	switch operator {
	case "gt":
		return value > threshold
	case "lt":
		return value < threshold
	case "eq":
		return value == threshold
	default:
		return false
	}
}

func getAlertRules(enabledOnly bool) ([]AlertRule, error) {
	query := `SELECT id, name, metric, operator, threshold, duration_secs, severity, enabled, created_at FROM alert_rules`
	if enabledOnly {
		query += ` WHERE enabled = TRUE`
	}
	query += ` ORDER BY created_at`

	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []AlertRule
	for rows.Next() {
		var r AlertRule
		if err := rows.Scan(&r.ID, &r.Name, &r.Metric, &r.Operator, &r.Threshold,
			&r.DurationSecs, &r.Severity, &r.Enabled, &r.CreatedAt); err != nil {
			continue
		}
		rules = append(rules, r)
	}
	if rules == nil {
		rules = []AlertRule{}
	}
	return rules, nil
}

func broadcastAlertSSE(alert Alert) {
	data, err := json.Marshal(alert)
	if err != nil {
		return
	}
	// Wrap as SSE event.
	event := fmt.Sprintf("event: alert\ndata: %s\n\n", string(data))
	sseClientsMu.RLock()
	defer sseClientsMu.RUnlock()
	for ch := range sseClients {
		select {
		case ch <- []byte(event):
		default:
		}
	}
}

// buildTelegramPayload returns the JSON body for the Telegram
// sendMessage API. parse_mode is intentionally omitted so the API
// treats text as plain (no HTML parsing). Pre-fix, this set
// parse_mode="HTML" while substituting agent-reported strings
// (machine hostnames, alert-rule names) into the body unescaped —
// an agent that reported its hostname as <script>... would either
// crash Telegram's parser or smuggle markup through to operators.
func buildTelegramPayload(chatID, text string) ([]byte, error) {
	return json.Marshal(map[string]string{
		"chat_id": chatID,
		"text":    text,
	})
}

func sendTelegram(text string) {
	if telegramToken == "" || telegramChatID == "" {
		return
	}
	body, err := buildTelegramPayload(telegramChatID, text)
	if err != nil {
		log.Printf("telegram payload error: %v", err)
		return
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", telegramToken)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("telegram send error: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		log.Printf("telegram API error %d: %s", resp.StatusCode, string(b))
	}
}

// --- Alert REST API ---

func handleListAlerts(c echo.Context) error {
	status := c.QueryParam("status")
	query := `SELECT a.id, a.rule_id, a.machine_id, a.message, a.severity, a.status, a.triggered_at, a.resolved_at,
		COALESCE(m.hostname, a.machine_id) as hostname
		FROM alerts a LEFT JOIN machines m ON m.id = a.machine_id`
	if status == "" || status == "active" {
		query += ` WHERE a.status = 'active'`
	}
	query += ` ORDER BY a.triggered_at DESC LIMIT 200`

	rows, err := db.Query(query)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer rows.Close()

	var alerts []Alert
	for rows.Next() {
		var a Alert
		if err := rows.Scan(&a.ID, &a.RuleID, &a.MachineID, &a.Message, &a.Severity,
			&a.Status, &a.TriggeredAt, &a.ResolvedAt, &a.Hostname); err != nil {
			continue
		}
		alerts = append(alerts, a)
	}
	if alerts == nil {
		alerts = []Alert{}
	}
	return c.JSON(http.StatusOK, alerts)
}

func handleAlertCount(c echo.Context) error {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM alerts WHERE status = 'active'`).Scan(&count)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]int{"count": count})
}

func handleAcknowledgeAlert(c echo.Context) error {
	id := c.Param("id")
	res, err := db.Exec(`UPDATE alerts SET status = 'acknowledged' WHERE id = ? AND status = 'active'`, id)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "alert not found or already resolved"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "acknowledged"})
}

func handleListAlertRules(c echo.Context) error {
	rules, err := getAlertRules(false)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, rules)
}

func handleUpdateAlertRule(c echo.Context) error {
	id := c.Param("id")
	var body struct {
		Enabled   *bool    `json:"enabled"`
		Threshold *float64 `json:"threshold"`
		Name      *string  `json:"name"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
	}

	if body.Enabled != nil {
		_, err := db.Exec(`UPDATE alert_rules SET enabled = ? WHERE id = ?`, *body.Enabled, id)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	}
	if body.Threshold != nil {
		_, err := db.Exec(`UPDATE alert_rules SET threshold = ? WHERE id = ?`, *body.Threshold, id)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	}
	if body.Name != nil {
		_, err := db.Exec(`UPDATE alert_rules SET name = ? WHERE id = ?`, *body.Name, id)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "updated"})
}
