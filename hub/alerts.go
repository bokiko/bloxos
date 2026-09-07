package main

import (
	"bytes"
	"context"
	"database/sql"
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
func (s *Server) seedAlertRules() {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM alert_rules`).Scan(&count)
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
		_, err := s.db.Exec(`INSERT INTO alert_rules (id, name, metric, operator, threshold, duration_secs, severity) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, d.name, d.metric, d.operator, d.threshold, d.duration, d.severity)
		if err != nil {
			log.Printf("error seeding alert rule %s: %v", d.name, err)
		}
	}
	log.Println("seeded 6 default alert rules")
}

// alertEvalLoop runs every 30 seconds to evaluate alert rules.
func (s *Server) alertEvalLoop() {
	time.Sleep(5 * time.Second) // Wait for startup.
	log.Println("alert evaluation loop started")
	for {
		s.evaluateAlerts()
		time.Sleep(30 * time.Second)
	}
}

func (s *Server) evaluateAlerts() {
	s.evaluateAlertsAt(time.Now())
}

type alertPendingCondition struct {
	since    time.Time
	observed time.Time
	rule     AlertRule
}

// Missing/stale readings are unknown, not recovery and not zero. Duration
// counts only a continuously observed condition, not a gap or hub downtime.
func (s *Server) evaluateAlertsAt(now time.Time) {
	var notifications []string
	defer func() { sendTelegramBatch(notifications) }()
	s.alertEvalMu.Lock()
	defer s.alertEvalMu.Unlock()
	// Get all enabled rules.
	rules, err := s.getAlertRules(true)
	if err != nil {
		log.Printf("alert eval: error getting rules: %v", err)
		return
	}

	// Get all machines with latest metrics.
	rows, err := s.db.Query(`
		SELECT m.id, m.hostname, m.last_seen, m.status, met.timestamp, COALESCE(ap.poll_interval_secs, 0),
			met.cpu_percent,
			met.ram_used_bytes, met.ram_total_bytes,
			met.disk_used_bytes, met.disk_total_bytes,
			COALESCE(met.gpu_temp, 0)
		FROM machines m
		LEFT JOIN api_machines ap ON m.id = 'api-' || ap.id AND ap.enabled = TRUE
		LEFT JOIN (
			SELECT machine_id, cpu_percent, ram_used_bytes, ram_total_bytes,
				disk_used_bytes, disk_total_bytes, gpu_temp, timestamp,
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
		status         string
		metricTime     *string
		pollInterval   int
		cpuPercent     sql.NullFloat64
		ramUsedBytes   sql.NullInt64
		ramTotalBytes  sql.NullInt64
		diskUsedBytes  sql.NullInt64
		diskTotalBytes sql.NullInt64
		gpuTemp        float64
	}

	var machines []machineMetrics
	for rows.Next() {
		var m machineMetrics
		if err := rows.Scan(&m.id, &m.hostname, &m.lastSeen, &m.status, &m.metricTime, &m.pollInterval, &m.cpuPercent,
			&m.ramUsedBytes, &m.ramTotalBytes, &m.diskUsedBytes, &m.diskTotalBytes, &m.gpuTemp); err != nil {
			continue
		}
		machines = append(machines, m)
	}
	if err := rows.Err(); err != nil {
		log.Printf("alert eval: reading machines: %v", err)
		return
	}
	rows.Close()

	// Pre-fetch all active alerts once to avoid one SELECT per (rule×machine)
	// in the inner loop. Active alerts are a small set; the cross product is not.
	active := map[string][]string{} // key: ruleID+"|"+machineID → unresolved IDs
	if alertRows, err := s.db.Query(`SELECT rule_id, machine_id, id FROM alerts WHERE status IN ('active', 'acknowledged')`); err == nil {
		for alertRows.Next() {
			var ruleID, machineID, alertID string
			if alertRows.Scan(&ruleID, &machineID, &alertID) == nil {
				key := ruleID + "|" + machineID
				active[key] = append(active[key], alertID)
			}
		}
		readErr := alertRows.Err()
		alertRows.Close()
		if readErr != nil {
			return
		}
	} else {
		log.Printf("alert eval: reading unresolved alerts: %v", err)
		return // Query failure must never be interpreted as no open incident.
	}

	// Failed database reads are not observations of missing sensors. Preserve
	// prior timing on an aborted pass; the next valid pass still enforces
	// the 90-second continuity bound and resets genuinely missing data.
	nextPending := make(map[string]alertPendingCondition)
	defer func() { s.alertPending = nextPending }()
	for _, rule := range rules {
		for _, m := range machines {
			key := rule.ID + "|" + m.id
			freshFor := 120 * time.Second
			// Native agents tick every 30s. API pollers legitimately tick up
			// to once/hour: allow their configured cadence plus one eval tick,
			// otherwise a normal polling gap breaks every longer duration.
			if m.pollInterval > 90 && m.pollInterval <= 3600 {
				freshFor = time.Duration(m.pollInterval+30) * time.Second
			}
			if rule.Metric != "machine_offline" && (m.status != "online" || !alertReadingFresh(m.lastSeen, now, freshFor) || !alertReadingFresh(m.metricTime, now, freshFor)) {
				continue
			}
			var metricValue float64
			var triggered bool
			var msg string

			switch rule.Metric {
			case "cpu":
				if !m.cpuPercent.Valid {
					continue
				}
				metricValue = m.cpuPercent.Float64
				triggered = compareValue(metricValue, rule.Operator, rule.Threshold)
				msg = fmt.Sprintf("CPU is %.0f%% (threshold: %.0f%%)", metricValue, rule.Threshold)
			case "ram":
				if !m.ramUsedBytes.Valid || !m.ramTotalBytes.Valid || m.ramTotalBytes.Int64 <= 0 {
					continue // No RAM data yet, skip to avoid false triggers.
				}
				metricValue = float64(m.ramUsedBytes.Int64) / float64(m.ramTotalBytes.Int64) * 100
				triggered = compareValue(metricValue, rule.Operator, rule.Threshold)
				msg = fmt.Sprintf("RAM is %.0f%% (threshold: %.0f%%)", metricValue, rule.Threshold)
			case "disk":
				if !m.diskUsedBytes.Valid || !m.diskTotalBytes.Valid || m.diskTotalBytes.Int64 <= 0 {
					continue // No disk data yet, skip to avoid false triggers.
				}
				metricValue = float64(m.diskUsedBytes.Int64) / float64(m.diskTotalBytes.Int64) * 100
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
				lastSeenTime, err := parseAlertTimestamp(*m.lastSeen)
				if err != nil {
					continue
				}
				offlineSecs := now.Sub(lastSeenTime).Seconds()
				if m.pollInterval >= 30 && m.pollInterval <= 3600 {
					// A scheduled polling gap is not downtime. API machines
					// become overdue only after the next poll was expected.
					offlineSecs = max(0, offlineSecs-float64(m.pollInterval))
				}
				triggered = compareValue(offlineSecs, rule.Operator, rule.Threshold)
				msg = fmt.Sprintf("Machine offline for %.0fs (threshold: %.0fs)", offlineSecs, rule.Threshold)
			default:
				continue
			}

			// Look up existing active alert using the pre-fetched map.
			existingIDs := active[key]
			hasActive := len(existingIDs) != 0
			if triggered && !hasActive && rule.DurationSecs > 0 {
				pending, ok := s.alertPending[key]
				if !ok || pending.rule != rule || now.Before(pending.observed) || now.Sub(pending.observed) > 90*time.Second {
					pending = alertPendingCondition{since: now, rule: rule}
				}
				pending.observed = now
				nextPending[key] = pending
				if now.Sub(pending.since).Seconds() < float64(rule.DurationSecs) {
					continue
				}
			}

			if triggered && !hasActive {
				// Create new alert.
				alertID := uuid.New().String()
				result, err := s.db.Exec(`INSERT INTO alerts (id, rule_id, machine_id, message, severity) SELECT ?, ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM machines WHERE id = ?)`,
					alertID, rule.ID, m.id, msg, rule.Severity, m.id)
				if err != nil {
					log.Printf("alert eval: error creating alert: %v", err)
					continue
				}
				if count, _ := result.RowsAffected(); count == 0 {
					continue
				} // Machine deleted during evaluation.
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
				notifications = append(notifications, fmt.Sprintf("BloxOS Alert\n\nMachine: %s\nSeverity: %s\n%s",
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
				_, err := s.db.Exec(`UPDATE alerts SET message = ? WHERE rule_id = ? AND machine_id = ? AND status IN ('active', 'acknowledged')`, msg, rule.ID, m.id)
				if err != nil {
					log.Printf("alert eval: error refreshing alert message: %v", err)
					continue
				}

			} else if !triggered && hasActive {
				// Resolve the alert.
				_, err := s.db.Exec(`UPDATE alerts SET status = 'resolved', resolved_at = CURRENT_TIMESTAMP WHERE rule_id = ? AND machine_id = ? AND status IN ('active', 'acknowledged')`, rule.ID, m.id)
				if err != nil {
					log.Printf("alert eval: error resolving alert: %v", err)
					continue
				}
				log.Printf("RESOLVED: %s on %s", rule.Name, m.hostname)

				for _, existingID := range existingIDs {
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
				}

				notifications = append(notifications, fmt.Sprintf("Resolved: %s on %s is back to normal", rule.Name, m.hostname))
			}
		}
	}
}

func parseAlertTimestamp(value string) (time.Time, error) {
	var lastErr error
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05.999999999 -0700 MST", "2006-01-02 15:04:05.999999999-07:00"} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed, nil
		}
		lastErr = err
	}
	return time.Time{}, lastErr
}

func alertReadingFresh(value *string, now time.Time, freshFor time.Duration) bool {
	if value == nil {
		return false
	}
	stamp, err := parseAlertTimestamp(*value)
	return err == nil && now.Sub(stamp) >= -30*time.Second && now.Sub(stamp) <= freshFor
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

func (s *Server) getAlertRules(enabledOnly bool) ([]AlertRule, error) {
	query := `SELECT id, name, metric, operator, threshold, duration_secs, severity, enabled, created_at FROM alert_rules`
	if enabledOnly {
		query += ` WHERE enabled = TRUE`
	}
	query += ` ORDER BY created_at`

	rows, err := s.db.Query(query)
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
	return rules, rows.Err()
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

func sendTelegramBatch(messages []string) {
	if telegramToken == "" || telegramChatID == "" {
		return
	}
	// One budget for the complete batch, after all DB/SSE transitions, not
	// N independent unbounded waits and not N fire-and-forget goroutines.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Second}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", telegramToken)
	for _, text := range messages {
		if ctx.Err() != nil {
			break
		}
		if err := deliverTelegram(ctx, client, url, telegramChatID, text); err != nil {
			// Do not log url.Error: its URL contains the bot credential.
			log.Printf("telegram delivery failed; alert remains in dashboard")
		}
	}
}

func deliverTelegram(ctx context.Context, client *http.Client, url, chatID, text string) error {
	body, err := buildTelegramPayload(chatID, text)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("telegram status %d", resp.StatusCode)
	}
	_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return err
}

// --- Alert REST API ---

func (s *Server) handleListAlerts(c echo.Context) error {
	status := c.QueryParam("status")
	query := `SELECT a.id, a.rule_id, a.machine_id, a.message, a.severity, a.status, a.triggered_at, a.resolved_at,
		COALESCE(m.hostname, a.machine_id) as hostname
		FROM alerts a LEFT JOIN machines m ON m.id = a.machine_id`
	if status == "" || status == "active" {
		query += ` WHERE a.status = 'active'`
	}
	query += ` ORDER BY a.triggered_at DESC LIMIT 200`

	rows, err := s.db.Query(query)
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

func (s *Server) handleAlertCount(c echo.Context) error {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM alerts WHERE status = 'active'`).Scan(&count)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]int{"count": count})
}

func (s *Server) handleAcknowledgeAlert(c echo.Context) error {
	id := c.Param("id")
	res, err := s.db.Exec(`UPDATE alerts SET status = 'acknowledged' WHERE id = ? AND status = 'active'`, id)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "alert not found or already resolved"})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "acknowledged"})
}

func (s *Server) handleListAlertRules(c echo.Context) error {
	rules, err := s.getAlertRules(false)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, rules)
}

func (s *Server) handleUpdateAlertRule(c echo.Context) error {
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
		_, err := s.db.Exec(`UPDATE alert_rules SET enabled = ? WHERE id = ?`, *body.Enabled, id)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	}
	if body.Threshold != nil {
		_, err := s.db.Exec(`UPDATE alert_rules SET threshold = ? WHERE id = ?`, *body.Threshold, id)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	}
	if body.Name != nil {
		_, err := s.db.Exec(`UPDATE alert_rules SET name = ? WHERE id = ?`, *body.Name, id)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "updated"})
}
