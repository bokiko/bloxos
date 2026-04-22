package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	_ "modernc.org/sqlite"
)

// GPUInfo holds per-GPU metrics from the agent.
type GPUInfo struct {
	Index         int     `json:"index"`
	Name          string  `json:"name"`
	TempC         float64 `json:"temp_c"`
	UtilPercent   float64 `json:"util_percent"`
	MemUsedBytes  int64   `json:"mem_used_bytes"`
	MemTotalBytes int64   `json:"mem_total_bytes"`
	PowerWatts    float64 `json:"power_watts"`
	FanPercent    float64 `json:"fan_percent"`
}

// AgentMetrics is the JSON payload sent by each agent.
type AgentMetrics struct {
	Type           string    `json:"type"`
	MachineID      string    `json:"machine_id"`
	Hostname       string    `json:"hostname"`
	IP             string    `json:"ip,omitempty"`
	OS             string    `json:"os,omitempty"`
	CPUPercent     float64   `json:"cpu_percent"`
	RAMUsedBytes   int64     `json:"ram_used_bytes"`
	RAMTotalBytes  int64     `json:"ram_total_bytes"`
	DiskUsedBytes  int64     `json:"disk_used_bytes"`
	DiskTotalBytes int64     `json:"disk_total_bytes"`
	GPUs           []GPUInfo `json:"gpus"`
	Timestamp      string    `json:"timestamp"`
}

// ServiceInfo from agent.
type ServiceInfo struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Description string `json:"description"`
}

// ServicesMessage from agent.
type ServicesMessage struct {
	Type      string        `json:"type"`
	Hostname  string        `json:"hostname"`
	MachineID string        `json:"machine_id"`
	Services  []ServiceInfo `json:"services"`
}

// ContainerInfo from agent.
type ContainerInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Image  string `json:"image"`
}

// ContainersMessage from agent.
type ContainersMessage struct {
	Type       string          `json:"type"`
	Hostname   string          `json:"hostname"`
	MachineID  string          `json:"machine_id"`
	Containers []ContainerInfo `json:"containers"`
}

// CommandRequest from the dashboard.
type CommandRequest struct {
	Type   string `json:"type"`
	Target string `json:"target"`
}

// CommandToAgent is forwarded to the agent via WebSocket.
type CommandToAgent struct {
	Type      string `json:"type"`
	Target    string `json:"target"`
	ID        string `json:"id"`
	SessionID string `json:"session_id,omitempty"`
}

// CommandResponse from agent.
type CommandResponse struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Error   string `json:"error"`
}

// ConnectedAgent tracks a live WebSocket connection from an agent.
type ConnectedAgent struct {
	MachineID string
	Conn      *websocket.Conn
	WriteMu   sync.Mutex
}

// TerminalSession tracks an active terminal relay session.
type TerminalSession struct {
	ID        string
	MachineID string
	AgentWS   *websocket.Conn
	BrowserWS *websocket.Conn
	CreatedAt time.Time
	mu        sync.Mutex
}

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

var (
	db       *sql.DB
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	// Connected agents keyed by machine ID.
	agents   = make(map[string]*ConnectedAgent)
	agentsMu sync.RWMutex

	// SSE subscribers.
	sseClients   = make(map[chan []byte]struct{})
	sseClientsMu sync.RWMutex

	// Pending command responses: command ID -> response channel.
	pendingCmds   = make(map[string]chan CommandResponse)
	pendingCmdsMu sync.Mutex

	// Terminal sessions keyed by session ID.
	termSessions   = make(map[string]*TerminalSession)
	termSessionsMu sync.RWMutex

	// Telegram config.
	telegramToken  string
	telegramChatID string
)

func main() {
	var err error
	db, err = sql.Open("sqlite", "bloxos.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	if err := initDB(); err != nil {
		log.Fatalf("failed to init database: %v", err)
	}
	log.Println("database initialized")

	// Seed default alert rules.
	seedAlertRules()

	// Load Telegram config.
	telegramToken = os.Getenv("BLOXOS_TELEGRAM_TOKEN")
	telegramChatID = os.Getenv("BLOXOS_TELEGRAM_CHAT_ID")
	if telegramToken == "" || telegramChatID == "" {
		log.Println("WARNING: BLOXOS_TELEGRAM_TOKEN or BLOXOS_TELEGRAM_CHAT_ID not set — Telegram notifications disabled")
	} else {
		log.Println("Telegram notifications enabled")
	}

	// Start alert evaluation loop.
	go alertEvalLoop()

	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{
			"http://192.168.16.113:3000",
			"http://localhost:3000",
		},
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions},
		AllowHeaders: []string{"Accept", "Content-Type", "Cache-Control"},
	}))

	e.GET("/health", handleHealth)
	e.GET("/ws/agent", handleAgentWS)
	e.GET("/api/events", handleSSE)
	e.GET("/api/machines", handleListMachines)
	e.GET("/api/machines/:id", handleGetMachine)
	e.GET("/api/machines/:id/services", handleGetServices)
	e.GET("/api/machines/:id/containers", handleGetContainers)
	e.POST("/api/machines/:id/command", handleCommand)
	e.PUT("/api/machines/:id/tags", handleSetTags)

	// Terminal endpoints.
	e.POST("/api/machines/:id/terminal", handleStartTerminal)
	e.DELETE("/api/machines/:id/terminal/:session_id", handleCloseTerminal)
	e.GET("/ws/terminal/:session_id", handleTerminalWS)

	// Alert endpoints.
	e.GET("/api/alerts", handleListAlerts)
	e.GET("/api/alerts/active/count", handleAlertCount)
	e.POST("/api/alerts/:id/acknowledge", handleAcknowledgeAlert)
	e.GET("/api/alert-rules", handleListAlertRules)
	e.PUT("/api/alert-rules/:id", handleUpdateAlertRule)

	// Install endpoints.
	e.POST("/api/tokens", handleCreateToken)
	e.GET("/install.sh", handleInstallScript)
	e.GET("/download/agent", handleDownloadAgent)

	log.Println("hub listening on :4000")
	if err := e.Start(":4000"); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

func initDB() error {
	schema := `
	CREATE TABLE IF NOT EXISTS machines (
		id TEXT PRIMARY KEY,
		hostname TEXT NOT NULL,
		ip TEXT,
		os TEXT,
		status TEXT DEFAULT 'offline',
		tags TEXT DEFAULT '',
		last_seen DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS metrics (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		machine_id TEXT NOT NULL,
		cpu_percent REAL,
		ram_used_bytes INTEGER,
		ram_total_bytes INTEGER,
		disk_used_bytes INTEGER,
		disk_total_bytes INTEGER,
		gpu_temp REAL,
		gpu_util_percent REAL,
		gpu_vram_used_bytes INTEGER,
		gpu_vram_total_bytes INTEGER,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (machine_id) REFERENCES machines(id)
	);

	CREATE TABLE IF NOT EXISTS tokens (
		token_hash TEXT PRIMARY KEY,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		expires_at DATETIME NOT NULL,
		used BOOLEAN DEFAULT FALSE
	);

	CREATE TABLE IF NOT EXISTS services (
		machine_id TEXT NOT NULL,
		name TEXT NOT NULL,
		status TEXT,
		description TEXT,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (machine_id, name)
	);

	CREATE TABLE IF NOT EXISTS containers (
		machine_id TEXT NOT NULL,
		container_id TEXT NOT NULL,
		name TEXT NOT NULL,
		status TEXT,
		image TEXT,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (machine_id, container_id)
	);

	CREATE INDEX IF NOT EXISTS idx_metrics_machine_time ON metrics(machine_id, timestamp);

	CREATE TABLE IF NOT EXISTS gpu_metrics (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		machine_id TEXT NOT NULL,
		gpu_index INTEGER NOT NULL,
		gpu_name TEXT,
		temp_c REAL,
		util_percent REAL,
		mem_used_bytes INTEGER,
		mem_total_bytes INTEGER,
		power_watts REAL,
		fan_percent REAL,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (machine_id) REFERENCES machines(id)
	);

	CREATE INDEX IF NOT EXISTS idx_gpu_metrics_machine_time ON gpu_metrics(machine_id, timestamp);

	CREATE TABLE IF NOT EXISTS terminal_sessions (
		id TEXT PRIMARY KEY,
		machine_id TEXT NOT NULL,
		started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		ended_at DATETIME,
		status TEXT DEFAULT 'active',
		FOREIGN KEY (machine_id) REFERENCES machines(id)
	);

	CREATE TABLE IF NOT EXISTS alert_rules (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		metric TEXT NOT NULL,
		operator TEXT NOT NULL,
		threshold REAL NOT NULL,
		duration_secs INTEGER DEFAULT 0,
		severity TEXT DEFAULT 'warning',
		enabled BOOLEAN DEFAULT TRUE,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS alerts (
		id TEXT PRIMARY KEY,
		rule_id TEXT,
		machine_id TEXT NOT NULL,
		message TEXT NOT NULL,
		severity TEXT NOT NULL,
		status TEXT DEFAULT 'active',
		triggered_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		resolved_at DATETIME,
		FOREIGN KEY (rule_id) REFERENCES alert_rules(id),
		FOREIGN KEY (machine_id) REFERENCES machines(id)
	);
	`
	_, err := db.Exec(schema)
	if err != nil {
		return err
	}

	// Add tags column if missing (migration for existing DBs).
	_, _ = db.Exec(`ALTER TABLE machines ADD COLUMN tags TEXT DEFAULT ''`)

	return nil
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
				msg = fmt.Sprintf("GPU temperature is %.0f°C (threshold: %.0f°C)", metricValue, rule.Threshold)
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
				icon := "⚠️"
				if rule.Severity == "critical" {
					icon = "🔴"
				}
				sendTelegram(fmt.Sprintf("%s <b>BloxOS Alert</b>\n\nMachine: <b>%s</b>\nSeverity: %s\n%s",
					icon, m.hostname, rule.Severity, msg))

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

				sendTelegram(fmt.Sprintf("✅ <b>Resolved</b>: %s on <b>%s</b> is back to normal", rule.Name, m.hostname))
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

func sendTelegram(text string) {
	if telegramToken == "" || telegramChatID == "" {
		return
	}
	payload := map[string]string{
		"chat_id":    telegramChatID,
		"text":       text,
		"parse_mode": "HTML",
	}
	body, _ := json.Marshal(payload)
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

// --- Tags ---

func handleSetTags(c echo.Context) error {
	id := c.Param("id")
	var body struct {
		Tags []string `json:"tags"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
	}
	tagsStr := strings.Join(body.Tags, ",")
	_, err := db.Exec(`UPDATE machines SET tags = ? WHERE id = ?`, tagsStr, id)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "updated", "tags": tagsStr})
}

// --- Install Script + Token ---

func handleCreateToken(c echo.Context) error {
	token := uuid.New().String()
	h := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(h[:])
	expiresAt := time.Now().Add(15 * time.Minute)

	_, err := db.Exec(`INSERT INTO tokens (token_hash, expires_at) VALUES (?, ?)`, tokenHash, expiresAt)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"token":      token,
		"expires_at": expiresAt.Format(time.RFC3339),
		"command":    fmt.Sprintf("curl -sL http://192.168.16.113:4000/install.sh | BLOXOS_HUB=ws://192.168.16.113:4000 BLOXOS_TOKEN=%s bash", token),
	})
}

func handleInstallScript(c echo.Context) error {
	script := `#!/bin/bash
set -euo pipefail

echo "=== BloxOS Agent Installer ==="

# Detect architecture.
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

HUB="${BLOXOS_HUB:?BLOXOS_HUB must be set}"
TOKEN="${BLOXOS_TOKEN:?BLOXOS_TOKEN must be set}"
HUB_HTTP=$(echo "$HUB" | sed 's|^ws://|http://|; s|^wss://|https://|')

echo "Hub: $HUB_HTTP"
echo "Arch: $ARCH"

# Download agent binary.
echo "Downloading agent binary..."
curl -fsSL -o /tmp/bloxos-agent "${HUB_HTTP}/download/agent?arch=${ARCH}"
chmod +x /tmp/bloxos-agent

# Create system user (if not exists).
if ! id -u bloxos &>/dev/null; then
  sudo useradd -r -s /usr/sbin/nologin bloxos || true
fi

# Install binary.
sudo mv /tmp/bloxos-agent /usr/local/bin/bloxos-agent

# Create systemd service.
sudo tee /etc/systemd/system/bloxos-agent.service > /dev/null << SVCEOF
[Unit]
Description=BloxOS Agent
After=network.target

[Service]
Type=simple
User=root
Environment="BLOXOS_HUB=${HUB}"
Environment="BLOXOS_TOKEN=${TOKEN}"
ExecStart=/usr/local/bin/bloxos-agent
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
SVCEOF

# Enable and start.
sudo systemctl daemon-reload
sudo systemctl enable bloxos-agent
sudo systemctl start bloxos-agent

echo "=== BloxOS Agent installed and running ==="
echo "Check status: systemctl status bloxos-agent"
`
	return c.String(http.StatusOK, script)
}

func handleDownloadAgent(c echo.Context) error {
	// Serve the pre-built agent binary.
	binaryPath := "/home/bokiko/bloxos/agent/bloxos-agent"
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "agent binary not found"})
	}
	return c.File(binaryPath)
}

// --- Existing Handlers ---

func handleHealth(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func handleAgentWS(c echo.Context) error {
	token := c.QueryParam("token")
	if token == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "token required"})
	}

	h := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(h[:8])
	_ = tokenHash

	ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return err
	}
	defer ws.Close()

	var machineID string
	agent := &ConnectedAgent{Conn: ws}

	for {
		_, msg, err := ws.ReadMessage()
		if err != nil {
			log.Printf("agent disconnected: %v", err)
			if machineID != "" {
				markOffline(machineID)
				agentsMu.Lock()
				delete(agents, machineID)
				agentsMu.Unlock()
			}
			return nil
		}

		var envelope struct {
			Type      string `json:"type"`
			MachineID string `json:"machine_id"`
		}
		if err := json.Unmarshal(msg, &envelope); err != nil {
			log.Printf("invalid JSON from agent: %v", err)
			continue
		}

		switch envelope.Type {
		case "metrics", "":
			var m AgentMetrics
			if err := json.Unmarshal(msg, &m); err != nil {
				log.Printf("invalid metrics JSON: %v", err)
				continue
			}
			if machineID == "" {
				machineID = m.MachineID
				agent.MachineID = machineID
				agentsMu.Lock()
				agents[machineID] = agent
				agentsMu.Unlock()
				log.Printf("agent registered: %s (%s)", m.Hostname, machineID)
			}
			upsertMachine(m)
			storeMetrics(m)
			broadcastSSE(msg)

		case "services":
			var sm ServicesMessage
			if err := json.Unmarshal(msg, &sm); err != nil {
				log.Printf("invalid services JSON: %v", err)
				continue
			}
			mid := sm.MachineID
			if mid == "" {
				mid = machineID
			}
			upsertServices(mid, sm.Services)
			broadcastSSE(msg)

		case "containers":
			var cm ContainersMessage
			if err := json.Unmarshal(msg, &cm); err != nil {
				log.Printf("invalid containers JSON: %v", err)
				continue
			}
			mid := cm.MachineID
			if mid == "" {
				mid = machineID
			}
			upsertContainers(mid, cm.Containers)
			broadcastSSE(msg)

		case "command_response":
			var resp CommandResponse
			if err := json.Unmarshal(msg, &resp); err != nil {
				log.Printf("invalid command_response JSON: %v", err)
				continue
			}
			pendingCmdsMu.Lock()
			ch, ok := pendingCmds[resp.ID]
			if ok {
				delete(pendingCmds, resp.ID)
			}
			pendingCmdsMu.Unlock()
			if ok {
				ch <- resp
			}

		default:
			log.Printf("unknown message type from agent: %s", envelope.Type)
		}
	}
}

// handleStartTerminal creates a terminal session and tells the agent to spawn a PTY.
func handleStartTerminal(c echo.Context) error {
	machineID := c.Param("id")

	agentsMu.RLock()
	agent, ok := agents[machineID]
	agentsMu.RUnlock()
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "agent not connected"})
	}

	sessionID := uuid.New().String()

	session := &TerminalSession{
		ID:        sessionID,
		MachineID: machineID,
		CreatedAt: time.Now(),
	}
	termSessionsMu.Lock()
	termSessions[sessionID] = session
	termSessionsMu.Unlock()

	_, err := db.Exec(`INSERT INTO terminal_sessions (id, machine_id) VALUES (?, ?)`, sessionID, machineID)
	if err != nil {
		log.Printf("terminal session DB insert error: %v", err)
	}

	cmd := CommandToAgent{
		Type:      "start_terminal",
		ID:        "term-" + sessionID[:8],
		SessionID: sessionID,
	}
	cmdData, _ := json.Marshal(cmd)
	agent.WriteMu.Lock()
	err = agent.Conn.WriteMessage(websocket.TextMessage, cmdData)
	agent.WriteMu.Unlock()
	if err != nil {
		termSessionsMu.Lock()
		delete(termSessions, sessionID)
		termSessionsMu.Unlock()
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to send start_terminal to agent"})
	}

	log.Printf("terminal session %s created for machine %s", sessionID, machineID)
	return c.JSON(http.StatusOK, map[string]string{"session_id": sessionID})
}

func handleCloseTerminal(c echo.Context) error {
	sessionID := c.Param("session_id")

	termSessionsMu.Lock()
	session, ok := termSessions[sessionID]
	if ok {
		delete(termSessions, sessionID)
	}
	termSessionsMu.Unlock()

	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
	}

	session.mu.Lock()
	if session.AgentWS != nil {
		session.AgentWS.Close()
	}
	if session.BrowserWS != nil {
		session.BrowserWS.Close()
	}
	session.mu.Unlock()

	_, _ = db.Exec(`UPDATE terminal_sessions SET ended_at = CURRENT_TIMESTAMP, status = 'closed' WHERE id = ?`, sessionID)

	log.Printf("terminal session %s closed", sessionID)
	return c.JSON(http.StatusOK, map[string]string{"status": "closed"})
}

func handleTerminalWS(c echo.Context) error {
	sessionID := c.Param("session_id")
	role := c.QueryParam("role")

	if role != "agent" && role != "browser" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "role must be 'agent' or 'browser'"})
	}

	termSessionsMu.RLock()
	session, ok := termSessions[sessionID]
	termSessionsMu.RUnlock()
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
	}

	ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return err
	}

	session.mu.Lock()
	if role == "agent" {
		session.AgentWS = ws
		log.Printf("terminal %s: agent connected", sessionID)
	} else {
		session.BrowserWS = ws
		log.Printf("terminal %s: browser connected", sessionID)
	}

	agentWS := session.AgentWS
	browserWS := session.BrowserWS
	session.mu.Unlock()

	if agentWS != nil && browserWS != nil {
		go terminalRelay(sessionID, session)
	} else {
		go func() {
			time.Sleep(30 * time.Second)
			session.mu.Lock()
			a := session.AgentWS
			b := session.BrowserWS
			session.mu.Unlock()
			if a == nil || b == nil {
				log.Printf("terminal %s: timeout waiting for both sides, cleaning up", sessionID)
				cleanupTerminalSession(sessionID)
			}
		}()
	}

	<-waitForSessionEnd(sessionID)
	return nil
}

func waitForSessionEnd(sessionID string) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		for {
			time.Sleep(500 * time.Millisecond)
			termSessionsMu.RLock()
			_, exists := termSessions[sessionID]
			termSessionsMu.RUnlock()
			if !exists {
				close(ch)
				return
			}
		}
	}()
	return ch
}

func terminalRelay(sessionID string, session *TerminalSession) {
	log.Printf("terminal %s: relay started", sessionID)

	session.mu.Lock()
	agentWS := session.AgentWS
	browserWS := session.BrowserWS
	session.mu.Unlock()

	done := make(chan struct{})

	go func() {
		defer func() {
			select {
			case <-done:
			default:
				close(done)
			}
		}()
		for {
			msgType, msg, err := browserWS.ReadMessage()
			if err != nil {
				log.Printf("terminal %s: browser read error: %v", sessionID, err)
				return
			}
			if err := agentWS.WriteMessage(msgType, msg); err != nil {
				log.Printf("terminal %s: agent write error: %v", sessionID, err)
				return
			}
		}
	}()

	go func() {
		defer func() {
			select {
			case <-done:
			default:
				close(done)
			}
		}()
		for {
			msgType, msg, err := agentWS.ReadMessage()
			if err != nil {
				log.Printf("terminal %s: agent read error: %v", sessionID, err)
				return
			}
			if err := browserWS.WriteMessage(msgType, msg); err != nil {
				log.Printf("terminal %s: browser write error: %v", sessionID, err)
				return
			}
		}
	}()

	<-done
	log.Printf("terminal %s: relay ended, cleaning up", sessionID)
	cleanupTerminalSession(sessionID)
}

func cleanupTerminalSession(sessionID string) {
	termSessionsMu.Lock()
	session, ok := termSessions[sessionID]
	if ok {
		delete(termSessions, sessionID)
	}
	termSessionsMu.Unlock()

	if !ok {
		return
	}

	session.mu.Lock()
	if session.AgentWS != nil {
		session.AgentWS.Close()
	}
	if session.BrowserWS != nil {
		session.BrowserWS.Close()
	}
	session.mu.Unlock()

	_, _ = db.Exec(`UPDATE terminal_sessions SET ended_at = CURRENT_TIMESTAMP, status = 'closed' WHERE id = ?`, sessionID)
}

func upsertServices(machineID string, services []ServiceInfo) {
	tx, err := db.Begin()
	if err != nil {
		log.Printf("begin tx error: %v", err)
		return
	}
	if _, err := tx.Exec(`DELETE FROM services WHERE machine_id = ?`, machineID); err != nil {
		tx.Rollback()
		log.Printf("delete services error: %v", err)
		return
	}
	stmt, err := tx.Prepare(`INSERT INTO services (machine_id, name, status, description, updated_at) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		log.Printf("prepare services error: %v", err)
		return
	}
	defer stmt.Close()
	now := time.Now().UTC()
	for _, s := range services {
		if _, err := stmt.Exec(machineID, s.Name, s.Status, s.Description, now); err != nil {
			log.Printf("insert service error: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		log.Printf("commit services error: %v", err)
	}
	log.Printf("stored %d services for %s", len(services), machineID)
}

func upsertContainers(machineID string, containers []ContainerInfo) {
	tx, err := db.Begin()
	if err != nil {
		log.Printf("begin tx error: %v", err)
		return
	}
	if _, err := tx.Exec(`DELETE FROM containers WHERE machine_id = ?`, machineID); err != nil {
		tx.Rollback()
		log.Printf("delete containers error: %v", err)
		return
	}
	stmt, err := tx.Prepare(`INSERT INTO containers (machine_id, container_id, name, status, image, updated_at) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		log.Printf("prepare containers error: %v", err)
		return
	}
	defer stmt.Close()
	now := time.Now().UTC()
	for _, c := range containers {
		if _, err := stmt.Exec(machineID, c.ID, c.Name, c.Status, c.Image, now); err != nil {
			log.Printf("insert container error: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		log.Printf("commit containers error: %v", err)
	}
	log.Printf("stored %d containers for %s", len(containers), machineID)
}

func upsertMachine(m AgentMetrics) {
	_, err := db.Exec(`
		INSERT INTO machines (id, hostname, ip, os, status, last_seen)
		VALUES (?, ?, ?, ?, 'online', ?)
		ON CONFLICT(id) DO UPDATE SET
			hostname = excluded.hostname,
			ip = excluded.ip,
			os = excluded.os,
			status = 'online',
			last_seen = excluded.last_seen
	`, m.MachineID, m.Hostname, m.IP, m.OS, time.Now().UTC())
	if err != nil {
		log.Printf("upsert machine error: %v", err)
	}
}

func storeMetrics(m AgentMetrics) {
	var gpuTemp, gpuUtil float64
	var gpuVRAMUsed, gpuVRAMTotal int64
	if len(m.GPUs) > 0 {
		gpuTemp = m.GPUs[0].TempC
		gpuUtil = m.GPUs[0].UtilPercent
		gpuVRAMUsed = m.GPUs[0].MemUsedBytes
		gpuVRAMTotal = m.GPUs[0].MemTotalBytes
	}

	_, err := db.Exec(`
		INSERT INTO metrics (machine_id, cpu_percent, ram_used_bytes, ram_total_bytes,
			disk_used_bytes, disk_total_bytes, gpu_temp, gpu_util_percent,
			gpu_vram_used_bytes, gpu_vram_total_bytes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, m.MachineID, m.CPUPercent, m.RAMUsedBytes, m.RAMTotalBytes,
		m.DiskUsedBytes, m.DiskTotalBytes, gpuTemp, gpuUtil, gpuVRAMUsed, gpuVRAMTotal)
	if err != nil {
		log.Printf("store metrics error: %v", err)
	}

	for _, g := range m.GPUs {
		_, err := db.Exec(`
			INSERT INTO gpu_metrics (machine_id, gpu_index, gpu_name, temp_c, util_percent,
				mem_used_bytes, mem_total_bytes, power_watts, fan_percent)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, m.MachineID, g.Index, g.Name, g.TempC, g.UtilPercent,
			g.MemUsedBytes, g.MemTotalBytes, g.PowerWatts, g.FanPercent)
		if err != nil {
			log.Printf("store gpu metric error: %v", err)
		}
	}
}

func markOffline(machineID string) {
	_, err := db.Exec(`UPDATE machines SET status = 'offline' WHERE id = ?`, machineID)
	if err != nil {
		log.Printf("mark offline error: %v", err)
	}
	log.Printf("agent offline: %s", machineID)
}

func broadcastSSE(data []byte) {
	sseClientsMu.RLock()
	defer sseClientsMu.RUnlock()
	for ch := range sseClients {
		select {
		case ch <- data:
		default:
		}
	}
}

func handleSSE(c echo.Context) error {
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")

	ch := make(chan []byte, 64)
	sseClientsMu.Lock()
	sseClients[ch] = struct{}{}
	sseClientsMu.Unlock()

	defer func() {
		sseClientsMu.Lock()
		delete(sseClients, ch)
		sseClientsMu.Unlock()
		close(ch)
	}()

	flusher, ok := c.Response().Writer.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming not supported")
	}

	// Send snapshot with alert count.
	snapshot, _ := getEnrichedMachinesJSON()
	fmt.Fprintf(c.Response(), "event: snapshot\ndata: %s\n\n", snapshot)
	flusher.Flush()

	// Send initial alert count.
	var alertCount int
	db.QueryRow(`SELECT COUNT(*) FROM alerts WHERE status = 'active'`).Scan(&alertCount)
	alertCountJSON, _ := json.Marshal(map[string]int{"count": alertCount})
	fmt.Fprintf(c.Response(), "event: alert_count\ndata: %s\n\n", alertCountJSON)
	flusher.Flush()

	for {
		select {
		case data := <-ch:
			// Check if it's a pre-formatted SSE event (starts with "event:").
			if bytes.HasPrefix(data, []byte("event:")) {
				fmt.Fprintf(c.Response(), "%s", data)
			} else {
				fmt.Fprintf(c.Response(), "event: metrics\ndata: %s\n\n", data)
			}
			flusher.Flush()
		case <-c.Request().Context().Done():
			return nil
		}
	}
}

func handleListMachines(c echo.Context) error {
	data, err := getMachinesJSON()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSONBlob(http.StatusOK, data)
}

func handleGetMachine(c echo.Context) error {
	id := c.Param("id")

	var m struct {
		ID       string  `json:"id"`
		Hostname string  `json:"hostname"`
		IP       *string `json:"ip"`
		OS       *string `json:"os"`
		Status   string  `json:"status"`
		Tags     *string `json:"tags"`
		LastSeen *string `json:"last_seen"`
	}
	err := db.QueryRow(`SELECT id, hostname, ip, os, status, tags, last_seen FROM machines WHERE id = ?`, id).
		Scan(&m.ID, &m.Hostname, &m.IP, &m.OS, &m.Status, &m.Tags, &m.LastSeen)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "machine not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	var met struct {
		CPUPercent     float64 `json:"cpu_percent"`
		RAMUsedBytes   int64   `json:"ram_used_bytes"`
		RAMTotalBytes  int64   `json:"ram_total_bytes"`
		DiskUsedBytes  int64   `json:"disk_used_bytes"`
		DiskTotalBytes int64   `json:"disk_total_bytes"`
		GPUTemp        float64 `json:"gpu_temp"`
		GPUUtil        float64 `json:"gpu_util_percent"`
		GPUVRAMUsed    int64   `json:"gpu_vram_used_bytes"`
		GPUVRAMTotal   int64   `json:"gpu_vram_total_bytes"`
	}
	err = db.QueryRow(`
		SELECT cpu_percent, ram_used_bytes, ram_total_bytes, disk_used_bytes, disk_total_bytes,
			gpu_temp, gpu_util_percent, gpu_vram_used_bytes, gpu_vram_total_bytes
		FROM metrics WHERE machine_id = ? ORDER BY timestamp DESC LIMIT 1
	`, id).Scan(&met.CPUPercent, &met.RAMUsedBytes, &met.RAMTotalBytes,
		&met.DiskUsedBytes, &met.DiskTotalBytes, &met.GPUTemp, &met.GPUUtil,
		&met.GPUVRAMUsed, &met.GPUVRAMTotal)
	if err != nil && err != sql.ErrNoRows {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	gpus := getLatestGPUMetrics(id)

	return c.JSON(http.StatusOK, map[string]interface{}{
		"machine": m,
		"metrics": met,
		"gpus":    gpus,
	})
}

func getLatestGPUMetrics(machineID string) []GPUInfo {
	rows, err := db.Query(`
		SELECT gpu_index, gpu_name, temp_c, util_percent, mem_used_bytes, mem_total_bytes, power_watts, fan_percent
		FROM gpu_metrics
		WHERE machine_id = ? AND timestamp = (
			SELECT MAX(timestamp) FROM gpu_metrics WHERE machine_id = ?
		)
		ORDER BY gpu_index
	`, machineID, machineID)
	if err != nil {
		return []GPUInfo{}
	}
	defer rows.Close()

	var gpus []GPUInfo
	for rows.Next() {
		var g GPUInfo
		var name *string
		if err := rows.Scan(&g.Index, &name, &g.TempC, &g.UtilPercent,
			&g.MemUsedBytes, &g.MemTotalBytes, &g.PowerWatts, &g.FanPercent); err != nil {
			continue
		}
		if name != nil {
			g.Name = *name
		}
		gpus = append(gpus, g)
	}
	if gpus == nil {
		return []GPUInfo{}
	}
	return gpus
}

func handleGetServices(c echo.Context) error {
	id := c.Param("id")
	rows, err := db.Query(`SELECT name, status, description FROM services WHERE machine_id = ? ORDER BY name`, id)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer rows.Close()

	var services []ServiceInfo
	for rows.Next() {
		var s ServiceInfo
		if err := rows.Scan(&s.Name, &s.Status, &s.Description); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		services = append(services, s)
	}
	if services == nil {
		services = []ServiceInfo{}
	}
	return c.JSON(http.StatusOK, services)
}

func handleGetContainers(c echo.Context) error {
	id := c.Param("id")
	rows, err := db.Query(`SELECT container_id, name, status, image FROM containers WHERE machine_id = ? ORDER BY name`, id)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer rows.Close()

	var containers []ContainerInfo
	for rows.Next() {
		var ct ContainerInfo
		if err := rows.Scan(&ct.ID, &ct.Name, &ct.Status, &ct.Image); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		containers = append(containers, ct)
	}
	if containers == nil {
		containers = []ContainerInfo{}
	}
	return c.JSON(http.StatusOK, containers)
}

func handleCommand(c echo.Context) error {
	machineID := c.Param("id")

	agentsMu.RLock()
	agent, ok := agents[machineID]
	agentsMu.RUnlock()
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "agent not connected"})
	}

	var req CommandRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	cmdID := "cmd-" + uuid.New().String()[:8]

	respCh := make(chan CommandResponse, 1)
	pendingCmdsMu.Lock()
	pendingCmds[cmdID] = respCh
	pendingCmdsMu.Unlock()

	defer func() {
		pendingCmdsMu.Lock()
		delete(pendingCmds, cmdID)
		pendingCmdsMu.Unlock()
	}()

	cmd := CommandToAgent{
		Type:   req.Type,
		Target: req.Target,
		ID:     cmdID,
	}
	cmdData, _ := json.Marshal(cmd)
	agent.WriteMu.Lock()
	err := agent.Conn.WriteMessage(websocket.TextMessage, cmdData)
	agent.WriteMu.Unlock()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to send command to agent"})
	}

	select {
	case resp := <-respCh:
		return c.JSON(http.StatusOK, resp)
	case <-time.After(10 * time.Second):
		return c.JSON(http.StatusGatewayTimeout, map[string]string{
			"id":    cmdID,
			"error": "timeout waiting for agent response",
		})
	}
}

func getEnrichedMachinesJSON() ([]byte, error) {
	rows, err := db.Query(`
		SELECT m.id, m.hostname, m.ip, m.os, m.status, m.last_seen, COALESCE(m.tags, ''),
			COALESCE(met.cpu_percent, 0),
			COALESCE(met.ram_used_bytes, 0), COALESCE(met.ram_total_bytes, 0),
			COALESCE(met.disk_used_bytes, 0), COALESCE(met.disk_total_bytes, 0),
			COALESCE(met.gpu_temp, 0), COALESCE(met.gpu_util_percent, 0),
			COALESCE(met.gpu_vram_used_bytes, 0), COALESCE(met.gpu_vram_total_bytes, 0),
			COALESCE(met.timestamp, m.last_seen)
		FROM machines m
		LEFT JOIN (
			SELECT machine_id, cpu_percent, ram_used_bytes, ram_total_bytes,
				disk_used_bytes, disk_total_bytes, gpu_temp, gpu_util_percent,
				gpu_vram_used_bytes, gpu_vram_total_bytes, timestamp,
				ROW_NUMBER() OVER (PARTITION BY machine_id ORDER BY timestamp DESC) as rn
			FROM metrics
		) met ON met.machine_id = m.id AND met.rn = 1
		ORDER BY m.hostname
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type EnrichedMachine struct {
		MachineID      string          `json:"machine_id"`
		Hostname       string          `json:"hostname"`
		IP             *string         `json:"ip"`
		OS             *string         `json:"os"`
		Status         string          `json:"status"`
		Tags           string          `json:"tags"`
		CPUPercent     float64         `json:"cpu_percent"`
		RAMUsedBytes   int64           `json:"ram_used_bytes"`
		RAMTotalBytes  int64           `json:"ram_total_bytes"`
		DiskUsedBytes  int64           `json:"disk_used_bytes"`
		DiskTotalBytes int64           `json:"disk_total_bytes"`
		GPUTemp        float64         `json:"gpu_temp"`
		GPUUtil        float64         `json:"gpu_util_percent"`
		GPUVRAMUsed    int64           `json:"gpu_vram_used_bytes"`
		GPUVRAMTotal   int64           `json:"gpu_vram_total_bytes"`
		Timestamp      *string         `json:"timestamp"`
		GPUs           []GPUInfo       `json:"gpus"`
		Services       []ServiceInfo   `json:"services"`
		Containers     []ContainerInfo `json:"containers"`
	}

	var machines []EnrichedMachine
	for rows.Next() {
		var m EnrichedMachine
		var lastSeen *string
		if err := rows.Scan(&m.MachineID, &m.Hostname, &m.IP, &m.OS, &m.Status, &lastSeen, &m.Tags,
			&m.CPUPercent, &m.RAMUsedBytes, &m.RAMTotalBytes,
			&m.DiskUsedBytes, &m.DiskTotalBytes,
			&m.GPUTemp, &m.GPUUtil, &m.GPUVRAMUsed, &m.GPUVRAMTotal,
			&m.Timestamp); err != nil {
			return nil, err
		}

		m.Services = getServicesForMachine(m.MachineID)
		m.Containers = getContainersForMachine(m.MachineID)
		m.GPUs = getLatestGPUMetrics(m.MachineID)

		machines = append(machines, m)
	}
	if machines == nil {
		machines = []EnrichedMachine{}
	}
	return json.Marshal(machines)
}

func getServicesForMachine(machineID string) []ServiceInfo {
	rows, err := db.Query(`SELECT name, status, description FROM services WHERE machine_id = ? ORDER BY name`, machineID)
	if err != nil {
		return []ServiceInfo{}
	}
	defer rows.Close()
	var services []ServiceInfo
	for rows.Next() {
		var s ServiceInfo
		if err := rows.Scan(&s.Name, &s.Status, &s.Description); err != nil {
			continue
		}
		services = append(services, s)
	}
	if services == nil {
		return []ServiceInfo{}
	}
	return services
}

func getContainersForMachine(machineID string) []ContainerInfo {
	rows, err := db.Query(`SELECT container_id, name, status, image FROM containers WHERE machine_id = ? ORDER BY name`, machineID)
	if err != nil {
		return []ContainerInfo{}
	}
	defer rows.Close()
	var containers []ContainerInfo
	for rows.Next() {
		var ct ContainerInfo
		if err := rows.Scan(&ct.ID, &ct.Name, &ct.Status, &ct.Image); err != nil {
			continue
		}
		containers = append(containers, ct)
	}
	if containers == nil {
		return []ContainerInfo{}
	}
	return containers
}

func getMachinesJSON() ([]byte, error) {
	rows, err := db.Query(`SELECT id, hostname, ip, os, status, COALESCE(tags, ''), last_seen, created_at FROM machines ORDER BY hostname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type Machine struct {
		ID        string  `json:"id"`
		Hostname  string  `json:"hostname"`
		IP        *string `json:"ip"`
		OS        *string `json:"os"`
		Status    string  `json:"status"`
		Tags      string  `json:"tags"`
		LastSeen  *string `json:"last_seen"`
		CreatedAt string  `json:"created_at"`
	}

	var machines []Machine
	for rows.Next() {
		var m Machine
		if err := rows.Scan(&m.ID, &m.Hostname, &m.IP, &m.OS, &m.Status, &m.Tags, &m.LastSeen, &m.CreatedAt); err != nil {
			return nil, err
		}
		machines = append(machines, m)
	}
	if machines == nil {
		machines = []Machine{}
	}
	return json.Marshal(machines)
}
