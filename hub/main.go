package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
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
	CPUTempC       float64   `json:"cpu_temp_c,omitempty"`
	RAMUsedBytes   int64     `json:"ram_used_bytes"`
	RAMTotalBytes  int64     `json:"ram_total_bytes"`
	DiskUsedBytes  int64     `json:"disk_used_bytes"`
	DiskTotalBytes int64     `json:"disk_total_bytes"`
	GPUs           []GPUInfo `json:"gpus"`
	Timestamp      string    `json:"timestamp"`
	SentAt         string    `json:"sent_at,omitempty"`
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

// TerminalSession tracks an active terminal relay session.
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

	// Latency per machine (ms).
	machineLatency   = make(map[string]int64)
	machineLatencyMu sync.RWMutex

	// JWT signing key.
	jwtSecret []byte

	// Global rate limiter.
	rateLimiter *RateLimiter
)

func main() {
	var err error
	// Phase 11 — `foreign_keys=on` is load-bearing: ON DELETE CASCADE on
	// user_pinned_machines / user_saved_filters relies on it being set per
	// connection. Without this pragma SQLite silently ignores cascade clauses.
	db, err = sql.Open("sqlite", "bloxos.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	// Set DB file permissions to 0600 (owner read/write only).
	if err := os.Chmod("bloxos.db", 0600); err != nil && !os.IsNotExist(err) {
		log.Printf("WARNING: failed to set DB permissions: %v", err)
	}

	if err := initDB(); err != nil {
		log.Fatalf("failed to init database: %v", err)
	}
	log.Println("database initialized")

	// Seed default alert rules.
	seedAlertRules()

	// Load or generate the agent-update signing key. Must run before
	// initAgentVersionTracking so the first announce can be signed, and
	// before any installer script is served so it can pin the public key.
	initUpdateSigning()

	// Phase 8 — initialize agent version tracking. Computes the SHA of the
	// agent binary the hub is serving, and starts the reconnect-monitor loop.
	initAgentVersionTracking()

	// Generate setup token if no users exist (Finding #11 — first-boot setup).
	generateSetupToken()

	// Generate first-run token if needed (Finding #1).
	generateFirstRunToken()

	// Load or generate JWT secret (Finding #2).
	jwtSecret = loadOrGenerateJWTSecret()

	// Initialize rate limiter.
	rateLimiter = NewRateLimiter()
	log.Println("rate limiter initialized")

	// Load Telegram config.
	telegramToken = os.Getenv("BLOXOS_TELEGRAM_TOKEN")
	telegramChatID = os.Getenv("BLOXOS_TELEGRAM_CHAT_ID")
	if telegramToken == "" || telegramChatID == "" {
		log.Println("WARNING: BLOXOS_TELEGRAM_TOKEN or BLOXOS_TELEGRAM_CHAT_ID not set — Telegram notifications disabled")
	} else {
		log.Println("Telegram notifications enabled")
	}

	// Start alert evaluation loop.
	goSafelyForever("alertEvalLoop", alertEvalLoop)

	// Start cleanup goroutine.
	goSafelyForever("cleanupLoop", cleanupLoop)
	startAPIPollers()

	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{
		Format: "${time_rfc3339} ${method} ${uri} ${status} ${latency_human}\n",
		Output: &tokenRedactingWriter{w: os.Stderr},
	}))
	e.Use(middleware.Recover())

	// Trust local proxy (Caddy) for X-Forwarded-For header.
	e.IPExtractor = echo.ExtractIPFromXFFHeader(
		echo.TrustLoopback(true),
	)
	corsOrigins, err := resolveCORSOrigins()
	if err != nil {
		log.Fatalf("startup: %v", err)
	}
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: corsOrigins,
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowHeaders: []string{"Accept", "Content-Type", "Cache-Control", "Authorization"},
	}))

	registerRoutes(e)

	if err := auditRBACRouteCoverage(e, routeScopeRequirements); err != nil {
		log.Fatalf("RBAC route audit failed: %v", err)
	}

	listenAddr := os.Getenv("HUB_LISTEN")
	if listenAddr == "" {
		listenAddr = "127.0.0.1:4000"
	}
	log.Printf("hub listening on %s", listenAddr)
	if err := e.Start(listenAddr); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

// registerRoutes wires every public and RBAC-protected handler onto e.
// Shared between main() and tests so the production route set and the audit
// can never drift.
func registerRoutes(e *echo.Echo) {
	// Public endpoints (no auth).
	e.GET("/health", handleHealth)
	e.GET("/ws/agent", handleAgentWS)
	e.POST("/api/auth/login", handleLogin)
	e.GET("/install.sh", handleInstallScript)
	e.GET("/install.ps1", handleWindowsInstallScript)
	e.GET("/download/agent", handleDownloadAgent)
	e.GET("/download/ca.crt", handleDownloadCACert)
	e.GET("/api/setup/status", handleSetupStatus)
	e.POST("/api/setup", handleSetup)

	// Phase 10 — branding reads are public (dashboard fetches before auth).
	e.GET("/api/branding", handleGetBranding)
	e.GET("/api/branding/logo", handleGetLogo)
	e.GET("/api/branding/favicon", handleGetFavicon)

	// Protected endpoints.
	api := e.Group("", jwtMiddleware, credentialRotationMiddleware, permissionMiddleware)
	api.GET("/api/events", handleSSE)
	api.GET("/api/machines", handleListMachines)
	api.GET("/api/machines/:id", handleGetMachine)
	api.GET("/api/machines/:id/services", handleGetServices)
	api.GET("/api/machines/:id/containers", handleGetContainers)
	api.POST("/api/machines/:id/command", handleCommand)
	api.POST("/api/machines/:id/refresh", handleMachineRefresh)
	api.POST("/api/refresh", handleFleetRefresh)

	// Phase 8 — agent version tracking and rollout control
	api.GET("/api/versions", handleListVersions)
	api.POST("/api/versions/pause", handlePauseRollout)
	api.POST("/api/versions/resume", handleResumeRollout)
	api.PUT("/api/machines/:id/tags", handleSetTags)
	api.GET("/api/machines/:id/notes", handleGetMachineNotes)
	api.PUT("/api/machines/:id/notes", handleSetMachineNotes)
	api.GET("/api/machines/:id/metrics/history", handleMetricsHistory)
	api.DELETE("/api/machines/:id", handleDeleteMachine)

	api.POST("/api/machines/:id/terminal", handleStartTerminal)
	api.DELETE("/api/machines/:id/terminal/:session_id", handleCloseTerminal)
	e.GET("/ws/terminal/:session_id", handleTerminalWS)

	api.GET("/api/alerts", handleListAlerts)
	api.GET("/api/alerts/active/count", handleAlertCount)
	api.POST("/api/alerts/:id/acknowledge", handleAcknowledgeAlert)
	api.GET("/api/alert-rules", handleListAlertRules)
	api.PUT("/api/alert-rules/:id", handleUpdateAlertRule)

	api.POST("/api/auth/change-password", handleChangePassword)
	api.POST("/api/auth/change-pin", handleChangePIN)
	api.POST("/api/auth/sse-token", handleSSEToken)

	api.POST("/api/tokens", handleCreateToken)

	api.POST("/api/bulk/command", handleBulkCommand)

	api.GET("/api/inventory", handleInventory)

	api.GET("/api/api-machines", handleListAPIMachines)
	api.POST("/api/api-machines", handleCreateAPIMachine)
	api.PATCH("/api/api-machines/:id", handleUpdateAPIMachine)
	api.DELETE("/api/api-machines/:id", handleDeleteAPIMachine)
	api.POST("/api/api-machines/:id/poll", handleForceAPIPoll)

	api.GET("/api/users", handleListUsers)
	api.POST("/api/users", handleCreateUser)
	api.PATCH("/api/users/:id", handleUpdateUser)
	api.DELETE("/api/users/:id", handleDeleteUser)

	// Phase 10 — per-user theme prefs and admin branding writes.
	api.GET("/api/me/theme", handleGetMyThemePrefs)
	api.PATCH("/api/me/theme", handleUpdateMyThemePrefs)
	api.PATCH("/api/branding", handleUpdateBrandingText)
	api.POST("/api/branding/logo", handleUploadLogo)
	api.POST("/api/branding/favicon", handleUploadFavicon)
	api.DELETE("/api/branding/:kind", handleClearBrandingImage)

	// Phase 11 — per-user workflow personalization.
	api.GET("/api/me/preferences", handleGetMyPreferences)
	api.PATCH("/api/me/preferences", handlePatchMyPreferences)
	api.POST("/api/me/avatar", handleUploadAvatar)
	api.DELETE("/api/me/avatar", handleDeleteAvatar)
	api.GET("/api/users/:user_id/avatar", handleGetAvatar)
	api.POST("/api/me/pinned/:machine_id", handlePinMachine)
	api.DELETE("/api/me/pinned/:machine_id", handleUnpinMachine)
	api.GET("/api/me/filters", handleListSavedFilters)
	api.POST("/api/me/filters", handleCreateSavedFilter)
	api.DELETE("/api/me/filters/:id", handleDeleteSavedFilter)
}

func getEnvOrDefault(key, def string) string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v
}

func initDB() error {
	return runMigrations(db)
}

// --- Auth ---

// --- Cleanup ---

func cleanupLoop() {
	log.Println("cleanup goroutine started (runs hourly)")
	// Run once on startup after a short delay.
	time.Sleep(10 * time.Second)
	runCleanup()
	for {
		time.Sleep(1 * time.Hour)
		runCleanup()
	}
}

func runCleanup() {
	log.Println("running cleanup...")
	total := int64(0)

	// Delete metrics older than 7 days.
	res, err := db.Exec(`DELETE FROM metrics WHERE timestamp < datetime('now', '-7 days')`)
	if err == nil {
		n, _ := res.RowsAffected()
		total += n
		if n > 0 {
			log.Printf("cleanup: deleted %d old metrics rows", n)
		}
	}

	// Delete GPU metrics older than 7 days.
	res, err = db.Exec(`DELETE FROM gpu_metrics WHERE timestamp < datetime('now', '-7 days')`)
	if err == nil {
		n, _ := res.RowsAffected()
		total += n
		if n > 0 {
			log.Printf("cleanup: deleted %d old gpu_metrics rows", n)
		}
	}

	// Delete resolved alerts older than 30 days.
	res, err = db.Exec(`DELETE FROM alerts WHERE status != 'active' AND triggered_at < datetime('now', '-30 days')`)
	if err == nil {
		n, _ := res.RowsAffected()
		total += n
		if n > 0 {
			log.Printf("cleanup: deleted %d old resolved alerts", n)
		}
	}

	// Delete expired tokens.
	res, err = db.Exec(`DELETE FROM tokens WHERE expires_at < datetime('now')`)
	if err == nil {
		n, _ := res.RowsAffected()
		total += n
		if n > 0 {
			log.Printf("cleanup: deleted %d expired tokens", n)
		}
	}

	// Delete closed terminal sessions older than 30 days.
	res, err = db.Exec(`DELETE FROM terminal_sessions WHERE status = 'closed' AND ended_at < datetime('now', '-30 days')`)
	if err == nil {
		n, _ := res.RowsAffected()
		total += n
		if n > 0 {
			log.Printf("cleanup: deleted %d old terminal sessions", n)
		}
	}

	log.Printf("cleanup complete: %d total rows removed", total)
}

// --- Metrics History ---

func handleMetricsHistory(c echo.Context) error {
	machineID := c.Param("id")
	period := c.QueryParam("period")
	if period == "" {
		period = "1h"
	}

	var duration string
	var limit int
	switch period {
	case "30m":
		duration = "-30 minutes"
		limit = 60
	case "1h":
		duration = "-1 hours"
		limit = 120
	case "6h":
		duration = "-6 hours"
		limit = 360
	case "24h":
		duration = "-24 hours"
		limit = 720
	case "7d":
		duration = "-7 days"
		limit = 2016
	default:
		duration = "-1 hours"
		limit = 120
	}

	rows, err := db.Query(`
		SELECT timestamp,
			COALESCE(cpu_percent, 0),
			COALESCE(ram_used_bytes, 0),
			COALESCE(ram_total_bytes, 0),
			COALESCE(gpu_temp, 0),
			COALESCE(gpu_util_percent, 0),
			COALESCE(gpu_vram_used_bytes, 0),
			COALESCE(gpu_vram_total_bytes, 0),
			COALESCE(cpu_temp_c, 0)
		FROM metrics
		WHERE machine_id = ? AND timestamp > datetime('now', ?)
		ORDER BY timestamp ASC
		LIMIT ?
	`, machineID, duration, limit)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer rows.Close()

	type Point struct {
		Timestamp    string  `json:"timestamp"`
		CPUPercent   float64 `json:"cpu_percent"`
		RAMUsed      int64   `json:"ram_used"`
		RAMTotal     int64   `json:"ram_total"`
		GPUTemp      float64 `json:"gpu_temp"`
		GPUUtil      float64 `json:"gpu_util"`
		GPUVRAMUsed  int64   `json:"gpu_vram_used"`
		GPUVRAMTotal int64   `json:"gpu_vram_total"`
		CPUTempC     float64 `json:"cpu_temp_c,omitempty"`
	}

	var points []Point
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.Timestamp, &p.CPUPercent, &p.RAMUsed, &p.RAMTotal,
			&p.GPUTemp, &p.GPUUtil, &p.GPUVRAMUsed, &p.GPUVRAMTotal, &p.CPUTempC); err != nil {
			continue
		}
		points = append(points, p)
	}
	if points == nil {
		points = []Point{}
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"points": points,
	})
}

// --- Bulk Command ---

// maxBulkCommandTargets caps the number of machine_ids a single
// /api/bulk/command request may target. Without this, an authenticated
// operator (or a compromised dashboard session) could spawn an
// unbounded number of goroutines, each holding a pendingCmds entry +
// a 15s timeout + per-agent WriteMu contention. 100 targets is well
// past any reasonable real-world fleet operation; legitimate "restart
// nginx everywhere" uses cases stay comfortably under this.
const maxBulkCommandTargets = 100

// bulkCommandConcurrency caps the number of in-flight goroutines for
// a single bulk request. With 100 targets and a 15s per-target
// timeout, an unbounded fan-out would spike to 100 simultaneous WS
// writes; capping at 20 spreads the load over time without sacrificing
// throughput on the common case (most targets reply in <1s).
const bulkCommandConcurrency = 20

func handleBulkCommand(c echo.Context) error {
	var body struct {
		MachineIDs []string `json:"machine_ids"`
		Type       string   `json:"type"`
		Target     string   `json:"target"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
	}
	if len(body.MachineIDs) > maxBulkCommandTargets {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("too many targets: %d > %d", len(body.MachineIDs), maxBulkCommandTargets),
		})
	}

	type BulkResult struct {
		MachineID string `json:"machine_id"`
		Success   bool   `json:"success"`
		Output    string `json:"output"`
		Error     string `json:"error"`
	}

	var results []BulkResult
	var mu sync.Mutex
	var wg sync.WaitGroup
	// Buffered channel acts as a counting semaphore. Acquire BEFORE
	// spawning the goroutine so the for-loop itself blocks once
	// bulkCommandConcurrency goroutines are in flight — that bounds
	// the spawn rate, not just the active work.
	sem := make(chan struct{}, bulkCommandConcurrency)

	for _, mid := range body.MachineIDs {
		wg.Add(1)
		sem <- struct{}{}
		go func(machineID string) {
			defer wg.Done()
			defer func() { <-sem }()

			agentsMu.RLock()
			agent, ok := agents[machineID]
			agentsMu.RUnlock()

			result := BulkResult{MachineID: machineID}
			if !ok {
				result.Error = "agent not connected"
				mu.Lock()
				results = append(results, result)
				mu.Unlock()
				return
			}

			cmdID := "bulk-" + uuid.New().String()[:8]
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
				Type:   body.Type,
				Target: body.Target,
				ID:     cmdID,
			}
			cmdData, _ := json.Marshal(cmd)
			agent.WriteMu.Lock()
			err := agent.Conn.WriteMessage(websocket.TextMessage, cmdData)
			agent.WriteMu.Unlock()
			if err != nil {
				result.Error = "failed to send command"
				mu.Lock()
				results = append(results, result)
				mu.Unlock()
				return
			}

			select {
			case resp := <-respCh:
				result.Success = resp.Success
				result.Output = resp.Output
				result.Error = resp.Error
			case <-time.After(15 * time.Second):
				result.Error = "timeout"
			}

			mu.Lock()
			results = append(results, result)
			mu.Unlock()
		}(mid)
	}

	wg.Wait()
	if results == nil {
		results = []BulkResult{}
	}
	return c.JSON(http.StatusOK, map[string]interface{}{
		"results": results,
	})
}

// --- Notes ---

// notesMaxLen caps free-text machine notes server-side. Mirrors the
// dashboard MachineNotes counter so a clipboard paste can't blow up
// the SQLite row.
const notesMaxLen = 10000

func handleGetMachineNotes(c echo.Context) error {
	id := c.Param("id")
	var notes string
	err := db.QueryRow(`SELECT COALESCE(notes, '') FROM machines WHERE id = ?`, id).Scan(&notes)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "machine not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, map[string]string{"notes": notes})
}

func handleSetMachineNotes(c echo.Context) error {
	id := c.Param("id")
	var body struct {
		Notes string `json:"notes"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
	}
	if len(body.Notes) > notesMaxLen {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("notes exceed maximum length of %d characters", notesMaxLen),
		})
	}

	res, err := db.Exec(`UPDATE machines SET notes = ? WHERE id = ?`, body.Notes, id)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	if rows == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "machine not found"})
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

// --- Existing Handlers ---

func handleDeleteMachine(c echo.Context) error {
	id := c.Param("id")

	// Check machine exists and get hostname
	var hostname string
	err := db.QueryRow("SELECT hostname FROM machines WHERE id = ?", id).Scan(&hostname)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "machine not found"})
	}

	// Delete all related data in a transaction
	tx, err := db.Begin()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to start transaction"})
	}
	defer tx.Rollback()

	tables := []string{"metrics", "gpu_metrics", "services", "containers", "alerts", "terminal_sessions", "agent_credentials"}
	for _, table := range tables {
		if _, err := tx.Exec("DELETE FROM "+table+" WHERE machine_id = ?", id); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to delete " + table})
		}
	}
	if _, err := tx.Exec("DELETE FROM machines WHERE id = ?", id); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to delete machine"})
	}
	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to commit"})
	}

	// Remove from live cache
	agentsMu.Lock()
	delete(agents, id)
	agentsMu.Unlock()
	machineLatencyMu.Lock()
	delete(machineLatency, id)
	machineLatencyMu.Unlock()

	log.Printf("machine deleted: %s (%s)", hostname, id)
	return c.JSON(http.StatusOK, map[string]string{"status": "deleted", "hostname": hostname})
}

func handleHealth(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
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

// nullableFloat converts a zero-valued float64 to a SQL NULL. Used for
// optional metrics like cpu_temp_c that older agents (or platforms that
// can't read the sensor) emit as 0 — we want NULL in the column so the
// dashboard can show an em-dash placeholder rather than "0°C".
func nullableFloat(v float64) sql.NullFloat64 {
	if v == 0 {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: v, Valid: true}
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

	tx, err := db.Begin()
	if err != nil {
		log.Printf("store metrics tx error: %v", err)
		return
	}

	if _, err := tx.Exec(`
		INSERT INTO metrics (machine_id, cpu_percent, ram_used_bytes, ram_total_bytes,
			disk_used_bytes, disk_total_bytes, gpu_temp, gpu_util_percent,
			gpu_vram_used_bytes, gpu_vram_total_bytes, cpu_temp_c)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, m.MachineID, m.CPUPercent, m.RAMUsedBytes, m.RAMTotalBytes,
		m.DiskUsedBytes, m.DiskTotalBytes, gpuTemp, gpuUtil, gpuVRAMUsed, gpuVRAMTotal,
		nullableFloat(m.CPUTempC)); err != nil {
		tx.Rollback()
		log.Printf("store metrics error: %v", err)
		return
	}

	if len(m.GPUs) > 0 {
		stmt, err := tx.Prepare(`
			INSERT INTO gpu_metrics (machine_id, gpu_index, gpu_name, temp_c, util_percent,
				mem_used_bytes, mem_total_bytes, power_watts, fan_percent)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			tx.Rollback()
			log.Printf("prepare gpu metrics error: %v", err)
			return
		}
		defer stmt.Close()
		for _, g := range m.GPUs {
			if _, err := stmt.Exec(m.MachineID, g.Index, g.Name, g.TempC, g.UtilPercent,
				g.MemUsedBytes, g.MemTotalBytes, g.PowerWatts, g.FanPercent); err != nil {
				log.Printf("store gpu metric error: %v", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("commit metrics error: %v", err)
	}
}

// markAPIMachineError upserts an API-polled machine row with status='error'.
// Called by startAPIPoller when its periodic poll returns an error so the
// dashboard reflects the failed-poll state. Returns the underlying SQL error
// (if any) so the caller can log it — the prior call site swallowed errors,
// which masked a SQL syntax bug for the entire lifetime of the API-machines
// feature.
func markAPIMachineError(machineID, hostname, adapterType string) error {
	_, err := db.Exec(`INSERT INTO machines (id, hostname, status, tags, last_seen)
		VALUES (?, ?, 'error', ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET status = 'error', last_seen = CURRENT_TIMESTAMP`,
		machineID, hostname, adapterType)
	return err
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

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()

	for {
		select {
		case data := <-ch:
			if bytes.HasPrefix(data, []byte("event:")) {
				fmt.Fprintf(c.Response(), "%s", data)
			} else {
				fmt.Fprintf(c.Response(), "event: metrics\ndata: %s\n\n", data)
			}
			flusher.Flush()
		case <-ping.C:
			fmt.Fprintf(c.Response(), ": keepalive\n\n")
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
		ID           string  `json:"id"`
		Hostname     string  `json:"hostname"`
		IP           *string `json:"ip"`
		OS           *string `json:"os"`
		Status       string  `json:"status"`
		Tags         *string `json:"tags"`
		LastSeen     *string `json:"last_seen"`
		Notes        string  `json:"notes"`
		HardwareInfo *string `json:"-"`
	}
	err := db.QueryRow(`SELECT id, hostname, ip, os, status, tags, last_seen, COALESCE(notes, ''), hardware_info FROM machines WHERE id = ?`, id).
		Scan(&m.ID, &m.Hostname, &m.IP, &m.OS, &m.Status, &m.Tags, &m.LastSeen, &m.Notes, &m.HardwareInfo)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "machine not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	var hardware any
	if m.HardwareInfo != nil && *m.HardwareInfo != "" {
		if err := json.Unmarshal([]byte(*m.HardwareInfo), &hardware); err != nil {
			log.Printf("decode stored hardware_info for %s: %v", id, err)
			hardware = nil
		}
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
		CPUTempC       float64 `json:"cpu_temp_c,omitempty"`
	}
	// COALESCE every column because API-polled machines (and agents that
	// haven't reported GPU yet) store NULLs here, and Scan of NULL into
	// float64/int64 fails with a 500.
	err = db.QueryRow(`
		SELECT
			COALESCE(cpu_percent, 0),
			COALESCE(ram_used_bytes, 0),
			COALESCE(ram_total_bytes, 0),
			COALESCE(disk_used_bytes, 0),
			COALESCE(disk_total_bytes, 0),
			COALESCE(gpu_temp, 0),
			COALESCE(gpu_util_percent, 0),
			COALESCE(gpu_vram_used_bytes, 0),
			COALESCE(gpu_vram_total_bytes, 0),
			COALESCE(cpu_temp_c, 0)
		FROM metrics WHERE machine_id = ? ORDER BY timestamp DESC LIMIT 1
	`, id).Scan(&met.CPUPercent, &met.RAMUsedBytes, &met.RAMTotalBytes,
		&met.DiskUsedBytes, &met.DiskTotalBytes, &met.GPUTemp, &met.GPUUtil,
		&met.GPUVRAMUsed, &met.GPUVRAMTotal, &met.CPUTempC)
	if err != nil && err != sql.ErrNoRows {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	gpus := getLatestGPUMetrics(id)

	// Get latency.
	machineLatencyMu.RLock()
	lat := machineLatency[id]
	machineLatencyMu.RUnlock()

	return c.JSON(http.StatusOK, map[string]interface{}{
		"machine":       m,
		"metrics":       met,
		"gpus":          gpus,
		"latency_ms":    lat,
		"hardware_info": hardware,
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
	// Bulk-fetch services for all machines (avoids 1+N per-machine queries).
	servicesByMachine := map[string][]ServiceInfo{}
	{
		svcRows, err := db.Query(`SELECT machine_id, name, status, description FROM services ORDER BY machine_id, name`)
		if err == nil {
			for svcRows.Next() {
				var machineID string
				var s ServiceInfo
				if scanErr := svcRows.Scan(&machineID, &s.Name, &s.Status, &s.Description); scanErr == nil {
					servicesByMachine[machineID] = append(servicesByMachine[machineID], s)
				}
			}
			svcRows.Close()
		}
	}

	// Bulk-fetch containers for all machines (avoids 1+N per-machine queries).
	containersByMachine := map[string][]ContainerInfo{}
	{
		ctRows, err := db.Query(`SELECT machine_id, container_id, name, status, image FROM containers ORDER BY machine_id, name`)
		if err == nil {
			for ctRows.Next() {
				var machineID string
				var ct ContainerInfo
				if scanErr := ctRows.Scan(&machineID, &ct.ID, &ct.Name, &ct.Status, &ct.Image); scanErr == nil {
					containersByMachine[machineID] = append(containersByMachine[machineID], ct)
				}
			}
			ctRows.Close()
		}
	}

	// Bulk-fetch latest GPU metrics for all machines using the same ROW_NUMBER() window
	// pattern as the metrics JOIN above (avoids 1+N per-machine queries).
	gpusByMachine := map[string][]GPUInfo{}
	{
		gpuRows, err := db.Query(`
			SELECT machine_id, gpu_index, gpu_name, temp_c, util_percent,
				mem_used_bytes, mem_total_bytes, power_watts, fan_percent
			FROM (
				SELECT machine_id, gpu_index, gpu_name, temp_c, util_percent,
					mem_used_bytes, mem_total_bytes, power_watts, fan_percent,
					ROW_NUMBER() OVER (PARTITION BY machine_id, gpu_index ORDER BY timestamp DESC) as rn
				FROM gpu_metrics
			) WHERE rn = 1
			ORDER BY machine_id, gpu_index
		`)
		if err == nil {
			for gpuRows.Next() {
				var machineID string
				var g GPUInfo
				var name *string
				if scanErr := gpuRows.Scan(&machineID, &g.Index, &name, &g.TempC, &g.UtilPercent,
					&g.MemUsedBytes, &g.MemTotalBytes, &g.PowerWatts, &g.FanPercent); scanErr == nil {
					if name != nil {
						g.Name = *name
					}
					gpusByMachine[machineID] = append(gpusByMachine[machineID], g)
				}
			}
			gpuRows.Close()
		}
	}

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
		LatencyMs      int64           `json:"latency_ms"`
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

		if svcs, ok := servicesByMachine[m.MachineID]; ok {
			m.Services = svcs
		} else {
			m.Services = []ServiceInfo{}
		}
		if cts, ok := containersByMachine[m.MachineID]; ok {
			m.Containers = cts
		} else {
			m.Containers = []ContainerInfo{}
		}
		if gpus, ok := gpusByMachine[m.MachineID]; ok {
			m.GPUs = gpus
		} else {
			m.GPUs = []GPUInfo{}
		}

		machineLatencyMu.RLock()
		m.LatencyMs = machineLatency[m.MachineID]
		machineLatencyMu.RUnlock()

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

// --- Security Functions ---

// tokenRedactingWriter redacts JWT tokens from log output (Finding #8).
type tokenRedactingWriter struct {
	w io.Writer
}

func (t *tokenRedactingWriter) Write(p []byte) (n int, err error) {
	s := string(p)
	for _, key := range []string{"token", "secret", "terminal_token", "browser_token"} {
		needle := key + "="
		for {
			idx := strings.Index(s, needle)
			if idx == -1 {
				break
			}
			end := idx + len(needle)
			valueEnd := end
			for valueEnd < len(s) && s[valueEnd] != '&' && s[valueEnd] != ' ' && s[valueEnd] != '"' && s[valueEnd] != '\n' {
				valueEnd++
			}
			if valueEnd > end {
				s = s[:end] + "[REDACTED]" + s[valueEnd:]
			} else {
				break
			}
		}
	}
	return t.w.Write([]byte(s))
}

// RateLimiter provides simple in-memory per-IP rate limiting.
type RateLimiter struct {
	mu       sync.Mutex
	requests map[string][]time.Time // key: "category:ip" -> timestamps
}

// NewRateLimiter creates a new rate limiter and starts periodic cleanup.
func NewRateLimiter() *RateLimiter {
	rl := &RateLimiter{
		requests: make(map[string][]time.Time),
	}
	go rl.cleanupLoop()
	return rl
}

// Allow checks if a request is within the rate limit. Returns true if allowed.
func (rl *RateLimiter) Allow(category string, ip string, maxPerMinute int) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	key := category + ":" + ip
	now := time.Now()
	cutoff := now.Add(-1 * time.Minute)
	// Filter to only recent timestamps.
	recent := []time.Time{}
	for _, t := range rl.requests[key] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= maxPerMinute {
		return false
	}
	rl.requests[key] = append(recent, now)
	return true
}

// cleanupLoop removes stale entries every 5 minutes.
func (rl *RateLimiter) cleanupLoop() {
	for {
		time.Sleep(5 * time.Minute)
		rl.mu.Lock()
		cutoff := time.Now().Add(-1 * time.Minute)
		for key, timestamps := range rl.requests {
			recent := []time.Time{}
			for _, t := range timestamps {
				if t.After(cutoff) {
					recent = append(recent, t)
				}
			}
			if len(recent) == 0 {
				delete(rl.requests, key)
			} else {
				rl.requests[key] = recent
			}
		}
		rl.mu.Unlock()
	}
}

// getRealIP extracts the client IP, trusting X-Forwarded-For only from the local proxy.
func getRealIP(c echo.Context) string {
	ip := c.RealIP()
	// If request comes from local proxy (Caddy), use X-Forwarded-For.
	if ip == "127.0.0.1" || ip == "::1" {
		if xff := c.Request().Header.Get("X-Forwarded-For"); xff != "" {
			// Take the first (leftmost) IP from X-Forwarded-For.
			parts := strings.Split(xff, ",")
			if len(parts) > 0 {
				clientIP := strings.TrimSpace(parts[0])
				if clientIP != "" {
					return clientIP
				}
			}
		}
	}
	return ip
}

// Suppress unused import warnings.
// --- API-Polled Machines ---

// APIPollResult holds normalized metrics from an API adapter poll.
type APIPollResult struct {
	Hostname   string
	IP         string
	OS         string
	CPUPercent float64
	RAMUsed    int64
	RAMTotal   int64
	DiskUsed   int64
	DiskTotal  int64
	Uptime     int64 // seconds
	Services   []ServiceInfo
	Containers []ContainerInfo
}

// APIMachine represents a row in the api_machines table.
type APIMachine struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	AdapterType      string          `json:"adapter_type"`
	BaseURL          string          `json:"base_url"`
	AuthConfig       json.RawMessage `json:"auth_config"`
	TLSConfig        json.RawMessage `json:"tls_config,omitempty"`
	PollIntervalSecs int             `json:"poll_interval_secs"`
	Enabled          bool            `json:"enabled"`
	LastPollAt       *string         `json:"last_poll_at"`
	LastError        *string         `json:"last_error"`
	CreatedAt        string          `json:"created_at"`
}

type APITLSConfig struct {
	Mode      string `json:"mode"`
	CACertPEM string `json:"ca_cert_pem,omitempty"`
}

// apiPollerEntry tracks a running poller goroutine for an API machine.
type apiPollerEntry struct {
	cancel context.CancelFunc
}

var (
	apiPollers   = make(map[string]*apiPollerEntry)
	apiPollersMu sync.Mutex
)

func parseAPITLSConfig(raw json.RawMessage) (APITLSConfig, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "{}" || string(bytes.TrimSpace(raw)) == "null" {
		return APITLSConfig{Mode: "system"}, nil
	}

	var cfg APITLSConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return APITLSConfig{}, fmt.Errorf("invalid tls_config: %w", err)
	}
	if cfg.Mode == "" {
		cfg.Mode = "system"
	}
	switch cfg.Mode {
	case "system":
		cfg.CACertPEM = ""
	case "insecure":
		cfg.CACertPEM = ""
	case "custom_ca":
		if strings.TrimSpace(cfg.CACertPEM) == "" {
			return APITLSConfig{}, fmt.Errorf("tls_config.ca_cert_pem is required when mode is custom_ca")
		}
		pool := x509.NewCertPool()
		if ok := pool.AppendCertsFromPEM([]byte(cfg.CACertPEM)); !ok {
			return APITLSConfig{}, fmt.Errorf("tls_config.ca_cert_pem must contain a valid PEM certificate")
		}
	default:
		return APITLSConfig{}, fmt.Errorf("tls_config.mode must be system, insecure, or custom_ca")
	}
	return cfg, nil
}

func validateAPITLSConfig(baseURL string, cfg APITLSConfig) error {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("base_url is not a valid URL")
	}
	if parsed.Scheme != "https" && cfg.Mode != "system" {
		return fmt.Errorf("tls_config is only supported for https base_url values")
	}
	return nil
}

func marshalStoredAPITLSConfig(cfg APITLSConfig) (string, error) {
	if cfg.Mode == "" {
		cfg.Mode = "system"
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func redactAPITLSConfig(raw string) json.RawMessage {
	cfg, err := parseAPITLSConfig(json.RawMessage(raw))
	if err != nil {
		return json.RawMessage(`"[REDACTED]"`)
	}
	redacted := map[string]interface{}{
		"mode":          cfg.Mode,
		"has_custom_ca": cfg.Mode == "custom_ca",
	}
	b, err := json.Marshal(redacted)
	if err != nil {
		return json.RawMessage(`"[REDACTED]"`)
	}
	return json.RawMessage(b)
}

func apiHTTPClient(baseURL string, tlsConfigRaw json.RawMessage) (*http.Client, error) {
	// SSRF guard: resolve and validate the target IP before every connection
	// (initial request and redirects), so a public hostname that resolves to an
	// internal address — or a DNS rebind — cannot reach blocked ranges.
	transport := &http.Transport{DialContext: safeDialContext}
	tlsConfig := &tls.Config{}
	cfg, err := parseAPITLSConfig(tlsConfigRaw)
	if err != nil {
		return nil, err
	}

	switch cfg.Mode {
	case "insecure":
		tlsConfig.InsecureSkipVerify = true
		log.Printf("WARNING: api machine %s is using insecure TLS polling", baseURL)
	case "custom_ca":
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if ok := pool.AppendCertsFromPEM([]byte(cfg.CACertPEM)); !ok {
			return nil, fmt.Errorf("parse tls_config.ca_cert_pem: no certificates found")
		}
		tlsConfig.RootCAs = pool
	}

	transport.TLSClientConfig = tlsConfig
	return &http.Client{
		Timeout:       30 * time.Second,
		Transport:     transport,
		CheckRedirect: checkAPIRedirect,
	}, nil
}

// --- Proxmox Adapter ---

func pollProxmox(baseURL string, authConfig json.RawMessage, tlsConfig json.RawMessage) (*APIPollResult, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	client, err := apiHTTPClient(baseURL, tlsConfig)
	if err != nil {
		return nil, err
	}
	var auth struct {
		TokenID     string `json:"token_id"`
		TokenSecret string `json:"token_secret"`
	}
	if err := json.Unmarshal(authConfig, &auth); err != nil {
		return nil, fmt.Errorf("invalid proxmox auth_config: %w", err)
	}
	if auth.TokenID == "" || auth.TokenSecret == "" {
		return nil, fmt.Errorf("proxmox auth_config requires token_id and token_secret")
	}

	authHeader := fmt.Sprintf("PVEAPIToken=%s=%s", auth.TokenID, auth.TokenSecret)

	// GET /api2/json/nodes
	nodesReq, err := http.NewRequest("GET", baseURL+"/api2/json/nodes", nil)
	if err != nil {
		return nil, fmt.Errorf("create nodes request: %w", err)
	}
	nodesReq.Header.Set("Authorization", authHeader)
	nodesResp, err := client.Do(nodesReq)
	if err != nil {
		return nil, fmt.Errorf("proxmox nodes request: %w", err)
	}
	defer nodesResp.Body.Close()
	if nodesResp.StatusCode != 200 {
		// Log the upstream body server-side for debugging, but never fold it
		// into the returned error — reflecting an upstream response body would
		// leak the content of whatever endpoint an SSRF probe reached.
		body, _ := io.ReadAll(io.LimitReader(nodesResp.Body, 4096))
		log.Printf("proxmox nodes poll failed: HTTP %d, body=%q", nodesResp.StatusCode, string(body))
		return nil, fmt.Errorf("proxmox nodes: HTTP %d", nodesResp.StatusCode)
	}

	var nodesData struct {
		Data []struct {
			Node    string  `json:"node"`
			Status  string  `json:"status"`
			CPU     float64 `json:"cpu"`
			Mem     int64   `json:"mem"`
			MaxMem  int64   `json:"maxmem"`
			Disk    int64   `json:"disk"`
			MaxDisk int64   `json:"maxdisk"`
			Uptime  int64   `json:"uptime"`
		} `json:"data"`
	}
	if err := json.NewDecoder(nodesResp.Body).Decode(&nodesData); err != nil {
		return nil, fmt.Errorf("decode proxmox nodes: %w", err)
	}

	result := &APIPollResult{OS: "Proxmox VE"}

	// Aggregate across all nodes (typically 1 for standalone).
	for i, node := range nodesData.Data {
		if i == 0 {
			result.Hostname = node.Node
			result.Uptime = node.Uptime
		}
		result.CPUPercent += node.CPU * 100
		result.RAMUsed += node.Mem
		result.RAMTotal += node.MaxMem
		result.DiskUsed += node.Disk
		result.DiskTotal += node.MaxDisk
	}
	if len(nodesData.Data) > 1 {
		result.CPUPercent /= float64(len(nodesData.Data))
		result.Hostname = fmt.Sprintf("%s (+%d nodes)", result.Hostname, len(nodesData.Data)-1)
	}

	// GET /api2/json/cluster/resources for VMs/containers
	resReq, err := http.NewRequest("GET", baseURL+"/api2/json/cluster/resources", nil)
	if err != nil {
		return nil, fmt.Errorf("create resources request: %w", err)
	}
	resReq.Header.Set("Authorization", authHeader)
	resResp, err := client.Do(resReq)
	if err != nil {
		// Non-fatal: we have node data at least.
		log.Printf("proxmox resources request failed (non-fatal): %v", err)
		return result, nil
	}
	defer resResp.Body.Close()

	if resResp.StatusCode == 200 {
		var resData struct {
			Data []struct {
				Type   string `json:"type"`
				VMID   int    `json:"vmid"`
				Name   string `json:"name"`
				Status string `json:"status"`
				Node   string `json:"node"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resResp.Body).Decode(&resData); err == nil {
			for _, r := range resData.Data {
				if r.Type == "qemu" || r.Type == "lxc" {
					result.Containers = append(result.Containers, ContainerInfo{
						ID:     fmt.Sprintf("%d", r.VMID),
						Name:   r.Name,
						Status: r.Status,
						Image:  r.Type, // "qemu" or "lxc"
					})
				}
			}
		}
	}

	return result, nil
}

// --- Synology Adapter ---

func pollSynology(baseURL string, authConfig json.RawMessage, tlsConfig json.RawMessage) (*APIPollResult, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	client, err := apiHTTPClient(baseURL, tlsConfig)
	if err != nil {
		return nil, err
	}
	var auth struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(authConfig, &auth); err != nil {
		return nil, fmt.Errorf("invalid synology auth_config: %w", err)
	}
	if auth.Username == "" || auth.Password == "" {
		return nil, fmt.Errorf("synology auth_config requires username and password")
	}

	// Login — use POST to avoid credentials in URL/logs
	loginURL := baseURL + "/webapi/auth.cgi"
	loginResp, err := client.PostForm(loginURL, url.Values{
		"api":     {"SYNO.API.Auth"},
		"version": {"6"},
		"method":  {"login"},
		"account": {auth.Username},
		"passwd":  {auth.Password},
		"format":  {"json"},
	})
	if err != nil {
		return nil, fmt.Errorf("synology login request: %w", err)
	}
	defer loginResp.Body.Close()

	var loginData struct {
		Success bool `json:"success"`
		Data    struct {
			SID string `json:"sid"`
		} `json:"data"`
	}
	if err := json.NewDecoder(loginResp.Body).Decode(&loginData); err != nil {
		return nil, fmt.Errorf("decode synology login: %w", err)
	}
	if !loginData.Success || loginData.Data.SID == "" {
		return nil, fmt.Errorf("synology login failed")
	}
	sid := loginData.Data.SID

	// Ensure logout
	defer func() {
		logoutURL := fmt.Sprintf("%s/webapi/entry.cgi?api=SYNO.API.Auth&version=6&method=logout&_sid=%s", baseURL, sid)
		resp, err := client.Get(logoutURL)
		if err == nil {
			resp.Body.Close()
		}
	}()

	result := &APIPollResult{OS: "Synology DSM"}

	// System utilization
	utilURL := fmt.Sprintf("%s/webapi/entry.cgi?api=SYNO.Core.System.Utilization&version=1&method=get&_sid=%s", baseURL, sid)
	utilResp, err := client.Get(utilURL)
	if err != nil {
		return nil, fmt.Errorf("synology utilization request: %w", err)
	}
	defer utilResp.Body.Close()

	var utilData struct {
		Success bool `json:"success"`
		Data    struct {
			CPU struct {
				UserLoad   int `json:"user_load"`
				SystemLoad int `json:"system_load"`
			} `json:"cpu"`
			Memory struct {
				RealUsage int `json:"real_usage"` // percentage
				TotalReal int `json:"total_real"` // KB
			} `json:"memory"`
		} `json:"data"`
	}
	if err := json.NewDecoder(utilResp.Body).Decode(&utilData); err != nil {
		return nil, fmt.Errorf("decode synology utilization: %w", err)
	}
	if utilData.Success {
		result.CPUPercent = float64(utilData.Data.CPU.UserLoad + utilData.Data.CPU.SystemLoad)
		totalBytes := int64(utilData.Data.Memory.TotalReal) * 1024
		result.RAMTotal = totalBytes
		result.RAMUsed = totalBytes * int64(utilData.Data.Memory.RealUsage) / 100
	}

	// Storage
	storageURL := fmt.Sprintf("%s/webapi/entry.cgi?api=SYNO.Storage.CGI.Storage&version=1&method=load_info&_sid=%s", baseURL, sid)
	storageResp, err := client.Get(storageURL)
	if err != nil {
		log.Printf("synology storage request failed (non-fatal): %v", err)
		return result, nil
	}
	defer storageResp.Body.Close()

	var storageData struct {
		Success bool `json:"success"`
		Data    struct {
			Volumes []struct {
				Size struct {
					Total json.RawMessage `json:"total"`
					Used  json.RawMessage `json:"used"`
				} `json:"size"`
			} `json:"volumes"`
		} `json:"data"`
	}
	if err := json.NewDecoder(storageResp.Body).Decode(&storageData); err == nil && storageData.Success {
		for _, vol := range storageData.Data.Volumes {
			total := parseJSONInt(vol.Size.Total)
			used := parseJSONInt(vol.Size.Used)
			result.DiskTotal += total
			result.DiskUsed += used
		}
	}

	return result, nil
}

// parseJSONInt tries to parse a JSON value as int64 (handles both string and number).
func parseJSONInt(raw json.RawMessage) int64 {
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		n, _ = strconv.ParseInt(s, 10, 64)
		return n
	}
	return 0
}

// resolveCORSOrigins returns the list of allowed origins for the CORS
// middleware, derived from ALLOWED_ORIGINS (comma-separated) or
// PUBLIC_URL. Pre-fix, when neither was set the hub fell back to
// AllowOrigins=[]string{"*"} — combined with JWT-in-localStorage,
// that meant any malicious page could read authenticated responses
// if the user happened to have a hub session in another tab.
//
// The hardened behaviour: refuse to start (caller log.Fatals on the
// returned error) so operators must opt in to origin policy
// explicitly. The error message names both env vars so the operator
// knows the fix without reading source.
func resolveCORSOrigins() ([]string, error) {
	if ao := os.Getenv("ALLOWED_ORIGINS"); ao != "" {
		parts := strings.Split(ao, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		if len(out) > 0 {
			return out, nil
		}
		// fall through: ALLOWED_ORIGINS was set but empty / whitespace-only
	}
	if pu := strings.TrimSpace(os.Getenv("PUBLIC_URL")); pu != "" {
		return []string{pu}, nil
	}
	return nil, fmt.Errorf("CORS: neither ALLOWED_ORIGINS nor PUBLIC_URL is set; refusing to start with wildcard origin")
}

// rfc1918Networks are the IPv4 private-use ranges from RFC 1918.
// validateBaseURL rejects URLs targeting these by default to defend
// against an authenticated user pointing the hub at internal-only
// services (databases, Kubernetes APIs, etc.). Homelab deployments
// where the entire fleet lives on a private subnet must opt back in
// via BLOXOS_ALLOW_PRIVATE_TARGETS=1 — this is the bloxos default
// for the typical 192.168.x.y / 10.x.y.z dev environment.
var rfc1918Networks = func() []*net.IPNet {
	cidrs := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

// lookupIPFunc resolves a host to its IP addresses. It is a package var so
// tests can inject a fake resolver without external network access.
var lookupIPFunc = net.DefaultResolver.LookupIPAddr

// isBlockedIP reports whether an IP address must never be the target of an
// API-machine poll. Loopback, unspecified, link-local, and multicast (which
// covers the 169.254.169.254 cloud-metadata endpoint) are always blocked.
// Private ranges (RFC 1918 + IPv6 ULA) are blocked unless the homelab opt-in
// BLOXOS_ALLOW_PRIVATE_TARGETS=1 is set — preserving the existing behavior for
// LAN fleets.
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	if ip.IsPrivate() && os.Getenv("BLOXOS_ALLOW_PRIVATE_TARGETS") != "1" {
		return true
	}
	return false
}

// resolveAndValidate resolves host and returns its IP addresses, failing closed
// if the host cannot be resolved or ANY resolved address is blocked (so a DNS
// answer mixing a public and an internal IP cannot be used to reach the
// internal one).
func resolveAndValidate(ctx context.Context, host string) ([]net.IP, error) {
	// A bracketed IPv6 literal or plain IP still goes through the resolver,
	// which returns it verbatim — so the block policy applies uniformly.
	addrs, err := lookupIPFunc(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve host: %w", err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no addresses resolved for host")
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		if isBlockedIP(a.IP) {
			return nil, fmt.Errorf("refusing to connect to blocked address %s (resolved from %s)", a.IP, host)
		}
		ips = append(ips, a.IP)
	}
	return ips, nil
}

// safeDialContext resolves and validates the target host, then dials only a
// validated IP (pinning it, so there is no second resolution a rebind could
// race). It is installed on every API-poll HTTP client, so it guards the
// initial request and every redirect the client follows.
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := resolveAndValidate(ctx, host)
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{Timeout: 10 * time.Second}
	var dialErr error
	for _, ip := range ips {
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		dialErr = err
	}
	return nil, dialErr
}

// checkAPIRedirect validates every redirect an API poll would follow with the
// same base-URL policy (scheme + host), and caps the redirect chain. The
// resolved-IP guard in safeDialContext still applies to the redirect's actual
// connection, so a redirect to a hostname that resolves internally is also
// blocked at dial time.
func checkAPIRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return fmt.Errorf("stopped after too many redirects")
	}
	if err := validateBaseURL(req.URL.String()); err != nil {
		return fmt.Errorf("redirect blocked: %w", err)
	}
	return nil
}

// validateBaseURL checks that a base URL is safe to poll (prevents SSRF).
func validateBaseURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("base_url is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("base_url is not a valid URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("base_url must use http or https scheme")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return fmt.Errorf("base_url must include a hostname")
	}
	// Block localhost variants — never bypassable.
	if host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "0.0.0.0" {
		return fmt.Errorf("base_url must not target localhost")
	}
	// Block cloud metadata endpoint.
	if host == "169.254.169.254" {
		return fmt.Errorf("base_url must not target cloud metadata service")
	}
	// Block link-local range 169.254.x.x.
	if strings.HasPrefix(host, "169.254.") {
		return fmt.Errorf("base_url must not target link-local addresses")
	}
	// Block RFC 1918 private ranges by default. Homelab use cases
	// (the typical bloxos deployment, where the fleet lives at
	// 192.168.x.y or 10.x.y.z) opt back in via env var. Only relevant
	// when host parses as an IP literal — public hostnames are not
	// resolved here, the live HTTP client will follow DNS.
	if ip := net.ParseIP(host); ip != nil {
		for _, n := range rfc1918Networks {
			if n.Contains(ip) && os.Getenv("BLOXOS_ALLOW_PRIVATE_TARGETS") != "1" {
				return fmt.Errorf("base_url targets RFC 1918 private range; set BLOXOS_ALLOW_PRIVATE_TARGETS=1 to allow homelab use")
			}
		}
	}
	return nil
}

func validateAPIAuthConfig(adapterType string, authConfig json.RawMessage) error {
	if len(bytes.TrimSpace(authConfig)) == 0 {
		return fmt.Errorf("auth_config is required")
	}

	switch adapterType {
	case "proxmox":
		var auth struct {
			TokenID     string `json:"token_id"`
			TokenSecret string `json:"token_secret"`
		}
		if err := json.Unmarshal(authConfig, &auth); err != nil {
			return fmt.Errorf("invalid proxmox auth_config: %w", err)
		}
		if strings.TrimSpace(auth.TokenID) == "" || strings.TrimSpace(auth.TokenSecret) == "" {
			return fmt.Errorf("proxmox auth_config requires token_id and token_secret")
		}
	case "synology":
		var auth struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.Unmarshal(authConfig, &auth); err != nil {
			return fmt.Errorf("invalid synology auth_config: %w", err)
		}
		if strings.TrimSpace(auth.Username) == "" || strings.TrimSpace(auth.Password) == "" {
			return fmt.Errorf("synology auth_config requires username and password")
		}
	default:
		return fmt.Errorf("adapter_type must be proxmox or synology")
	}
	return nil
}

// --- API Machine Handlers ---

func handleListAPIMachines(c echo.Context) error {
	rows, err := db.Query(`SELECT id, name, adapter_type, base_url, auth_config, tls_config, poll_interval_secs, enabled, last_poll_at, last_error, created_at FROM api_machines ORDER BY name`)
	if err != nil {
		log.Printf("api machines: list query error: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	defer rows.Close()

	var machines []APIMachine
	for rows.Next() {
		var m APIMachine
		var authCfg, tlsCfg string
		if err := rows.Scan(&m.ID, &m.Name, &m.AdapterType, &m.BaseURL, &authCfg, &tlsCfg, &m.PollIntervalSecs, &m.Enabled, &m.LastPollAt, &m.LastError, &m.CreatedAt); err != nil {
			log.Printf("api machines: list scan error: %v", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
		}
		// Redact auth_config — never expose credentials to the dashboard
		m.AuthConfig = json.RawMessage(`"[REDACTED]"`)
		m.TLSConfig = redactAPITLSConfig(tlsCfg)
		machines = append(machines, m)
	}
	if machines == nil {
		machines = []APIMachine{}
	}
	return c.JSON(http.StatusOK, machines)
}

func handleCreateAPIMachine(c echo.Context) error {
	ip := getRealIP(c)
	if !rateLimiter.Allow("api-machines-create", ip, 3) {
		return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
	}

	var body struct {
		Name             string          `json:"name"`
		AdapterType      string          `json:"adapter_type"`
		BaseURL          string          `json:"base_url"`
		AuthConfig       json.RawMessage `json:"auth_config"`
		TLSConfig        json.RawMessage `json:"tls_config"`
		PollIntervalSecs int             `json:"poll_interval_secs"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	if body.Name == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "name is required"})
	}
	if body.AdapterType != "proxmox" && body.AdapterType != "synology" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "adapter_type must be proxmox or synology"})
	}
	if err := validateBaseURL(body.BaseURL); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if err := validateAPIAuthConfig(body.AdapterType, body.AuthConfig); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	tlsCfg, err := parseAPITLSConfig(body.TLSConfig)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if err := validateAPITLSConfig(body.BaseURL, tlsCfg); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if body.PollIntervalSecs < 30 {
		body.PollIntervalSecs = 30
	}
	if body.PollIntervalSecs > 3600 {
		body.PollIntervalSecs = 3600
	}

	// Enforce max API machines limit
	var machineCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM api_machines").Scan(&machineCount); err != nil {
		log.Printf("api machines: count query error: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	if machineCount >= 50 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "maximum of 50 API machines reached"})
	}

	id := uuid.New().String()
	storedTLSCfg, err := marshalStoredAPITLSConfig(tlsCfg)
	if err != nil {
		log.Printf("api machines: tls config marshal error: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	_, err = db.Exec(`INSERT INTO api_machines (id, name, adapter_type, base_url, auth_config, tls_config, poll_interval_secs) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, body.Name, body.AdapterType, body.BaseURL, string(body.AuthConfig), storedTLSCfg, body.PollIntervalSecs)
	if err != nil {
		log.Printf("api machines: create insert error: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}

	log.Printf("api machine created: %s (%s, %s)", body.Name, body.AdapterType, id)

	// Start poller for the new machine.
	startAPIPoller(id, body.Name, body.AdapterType, body.BaseURL, body.AuthConfig, json.RawMessage(storedTLSCfg), body.PollIntervalSecs)

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"id":                 id,
		"name":               body.Name,
		"adapter_type":       body.AdapterType,
		"base_url":           body.BaseURL,
		"tls_config":         redactAPITLSConfig(storedTLSCfg),
		"poll_interval_secs": body.PollIntervalSecs,
	})
}

func handleUpdateAPIMachine(c echo.Context) error {
	id := c.Param("id")

	var current struct {
		Name             string
		AdapterType      string
		BaseURL          string
		AuthConfig       string
		TLSConfig        string
		PollIntervalSecs int
	}
	err := db.QueryRow(`SELECT name, adapter_type, base_url, auth_config, tls_config, poll_interval_secs
		FROM api_machines WHERE id = ?`, id).
		Scan(&current.Name, &current.AdapterType, &current.BaseURL, &current.AuthConfig, &current.TLSConfig, &current.PollIntervalSecs)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "api machine not found"})
	}

	var body struct {
		Name             *string          `json:"name"`
		AdapterType      *string          `json:"adapter_type"`
		BaseURL          *string          `json:"base_url"`
		AuthConfig       *json.RawMessage `json:"auth_config"`
		TLSConfig        *json.RawMessage `json:"tls_config"`
		PollIntervalSecs *int             `json:"poll_interval_secs"`
	}
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	name := current.Name
	if body.Name != nil {
		name = strings.TrimSpace(*body.Name)
	}
	if name == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "name is required"})
	}

	adapterType := current.AdapterType
	if body.AdapterType != nil {
		adapterType = strings.TrimSpace(*body.AdapterType)
	}
	if adapterType != "proxmox" && adapterType != "synology" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "adapter_type must be proxmox or synology"})
	}

	baseURL := current.BaseURL
	if body.BaseURL != nil {
		baseURL = strings.TrimSpace(*body.BaseURL)
	}
	if err := validateBaseURL(baseURL); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	authConfig := json.RawMessage(current.AuthConfig)
	if body.AuthConfig != nil {
		authConfig = *body.AuthConfig
	}
	if body.AdapterType != nil && *body.AdapterType != current.AdapterType && body.AuthConfig == nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "auth_config is required when adapter_type changes"})
	}
	if err := validateAPIAuthConfig(adapterType, authConfig); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	tlsConfig := json.RawMessage(current.TLSConfig)
	if body.TLSConfig != nil {
		tlsConfig = *body.TLSConfig
	}
	parsedTLSConfig, err := parseAPITLSConfig(tlsConfig)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if err := validateAPITLSConfig(baseURL, parsedTLSConfig); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	pollIntervalSecs := current.PollIntervalSecs
	if body.PollIntervalSecs != nil {
		pollIntervalSecs = *body.PollIntervalSecs
	}
	if pollIntervalSecs < 30 {
		pollIntervalSecs = 30
	}
	if pollIntervalSecs > 3600 {
		pollIntervalSecs = 3600
	}

	storedTLSCfg, err := marshalStoredAPITLSConfig(parsedTLSConfig)
	if err != nil {
		log.Printf("api machines: tls config marshal error: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}

	_, err = db.Exec(`UPDATE api_machines
		SET name = ?, adapter_type = ?, base_url = ?, auth_config = ?, tls_config = ?, poll_interval_secs = ?
		WHERE id = ?`,
		name, adapterType, baseURL, string(authConfig), storedTLSCfg, pollIntervalSecs, id)
	if err != nil {
		log.Printf("api machines: update error: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}

	startAPIPoller(id, name, adapterType, baseURL, authConfig, json.RawMessage(storedTLSCfg), pollIntervalSecs)

	log.Printf("api machine updated: %s (%s, %s)", name, adapterType, id)
	return c.JSON(http.StatusOK, map[string]interface{}{
		"id":                 id,
		"name":               name,
		"adapter_type":       adapterType,
		"base_url":           baseURL,
		"tls_config":         redactAPITLSConfig(storedTLSCfg),
		"poll_interval_secs": pollIntervalSecs,
	})
}

func handleDeleteAPIMachine(c echo.Context) error {
	id := c.Param("id")

	var name string
	err := db.QueryRow("SELECT name FROM api_machines WHERE id = ?", id).Scan(&name)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "api machine not found"})
	}

	// Stop poller.
	stopAPIPoller(id)

	// Delete api_machine row.
	if _, err := db.Exec("DELETE FROM api_machines WHERE id = ?", id); err != nil {
		log.Printf("api machines: delete error: %v", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}

	// Delete associated machine and metrics.
	machineID := "api-" + id
	tx, err := db.Begin()
	if err == nil {
		// Note: table names are hardcoded constants, not user input — safe from SQL injection
		for _, table := range []string{"metrics", "gpu_metrics", "services", "containers", "alerts"} {
			tx.Exec("DELETE FROM "+table+" WHERE machine_id = ?", machineID)
		}
		tx.Exec("DELETE FROM machines WHERE id = ?", machineID)
		tx.Commit()
	}

	log.Printf("api machine deleted: %s (%s)", name, id)
	return c.JSON(http.StatusOK, map[string]string{"status": "deleted", "name": name})
}

func handleForceAPIPoll(c echo.Context) error {
	id := c.Param("id")

	var m struct {
		Name        string `json:"name"`
		AdapterType string `json:"adapter_type"`
		BaseURL     string `json:"base_url"`
		AuthConfig  string `json:"auth_config"`
		TLSConfig   string `json:"tls_config"`
	}
	err := db.QueryRow("SELECT name, adapter_type, base_url, auth_config, tls_config FROM api_machines WHERE id = ?", id).
		Scan(&m.Name, &m.AdapterType, &m.BaseURL, &m.AuthConfig, &m.TLSConfig)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "api machine not found"})
	}

	result, pollErr := doPoll(m.AdapterType, m.BaseURL, json.RawMessage(m.AuthConfig), json.RawMessage(m.TLSConfig))
	if pollErr != nil {
		// Log the detailed error server-side, but return a generic message to
		// the dashboard so upstream/network detail from a poll target isn't
		// reflected to the client.
		log.Printf("api machine %s (%s) poll failed: %v", id, m.Name, pollErr)
		db.Exec("UPDATE api_machines SET last_error = ?, last_poll_at = CURRENT_TIMESTAMP WHERE id = ?", "poll failed", id)
		return c.JSON(http.StatusBadGateway, map[string]string{"error": "poll failed"})
	}

	storeAPIPollResult(id, m.Name, m.AdapterType, m.BaseURL, result)
	db.Exec("UPDATE api_machines SET last_poll_at = CURRENT_TIMESTAMP, last_error = NULL WHERE id = ?", id)

	return c.JSON(http.StatusOK, map[string]interface{}{
		"hostname":    result.Hostname,
		"cpu_percent": result.CPUPercent,
		"ram_used":    result.RAMUsed,
		"ram_total":   result.RAMTotal,
		"disk_used":   result.DiskUsed,
		"disk_total":  result.DiskTotal,
		"containers":  len(result.Containers),
	})
}

// doPoll dispatches to the correct adapter.
func doPoll(adapterType, baseURL string, authConfig json.RawMessage, tlsConfig json.RawMessage) (*APIPollResult, error) {
	switch adapterType {
	case "proxmox":
		return pollProxmox(baseURL, authConfig, tlsConfig)
	case "synology":
		return pollSynology(baseURL, authConfig, tlsConfig)
	default:
		return nil, fmt.Errorf("unknown adapter type: %s", adapterType)
	}
}

// extractAPIMachineIP returns the IP address (or hostname) for an
// API-polled machine. If the adapter has already discovered the IP,
// that wins — it's the authoritative answer from inside the device.
// Otherwise we parse baseURL (a well-formed URL the user typed when
// registering the machine) and return its hostname.
//
// Pre-fix bug at hub/main.go:2208 fed the *display name* into the
// strip-and-split logic instead of baseURL. For a machine named e.g.
// "dasman-syn" this produced "dasman-syn" as the IP — meaningless,
// and rendered as a garbage value in the dashboard's IP column.
func extractAPIMachineIP(baseURL, resultIP string) string {
	if resultIP != "" {
		return resultIP
	}
	if baseURL == "" {
		return ""
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	// url.Parse accepts "not-a-valid-url" because RFC 3986 allows
	// path-only refs. Reject anything without a scheme — those aren't
	// the well-formed baseURLs handleCreateAPIMachine validates and
	// stores.
	if u.Scheme == "" {
		return ""
	}
	return u.Hostname()
}

// storeAPIPollResult upserts the machine and inserts metrics.
func storeAPIPollResult(apiMachineID, name, adapterType, baseURL string, result *APIPollResult) {
	machineID := "api-" + apiMachineID
	hostname := result.Hostname
	if hostname == "" {
		hostname = name
	}

	ip := extractAPIMachineIP(baseURL, result.IP)

	// Upsert machine.
	_, err := db.Exec(`INSERT INTO machines (id, hostname, ip, os, status, tags, last_seen)
		VALUES (?, ?, ?, ?, 'online', ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			hostname = excluded.hostname,
			ip = excluded.ip,
			os = excluded.os,
			status = 'online',
			tags = excluded.tags,
			last_seen = CURRENT_TIMESTAMP`,
		machineID, hostname, ip, result.OS, adapterType)
	if err != nil {
		log.Printf("api poll: failed to upsert machine %s: %v", machineID, err)
	}

	// Insert metrics.
	_, err = db.Exec(`INSERT INTO metrics (machine_id, cpu_percent, ram_used_bytes, ram_total_bytes, disk_used_bytes, disk_total_bytes)
		VALUES (?, ?, ?, ?, ?, ?)`,
		machineID, result.CPUPercent, result.RAMUsed, result.RAMTotal, result.DiskUsed, result.DiskTotal)
	if err != nil {
		log.Printf("api poll: failed to insert metrics for %s: %v", machineID, err)
	}

	// Update services.
	if len(result.Services) > 0 {
		for _, svc := range result.Services {
			db.Exec(`INSERT OR REPLACE INTO services (machine_id, name, status, description, updated_at)
				VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`, machineID, svc.Name, svc.Status, svc.Description)
		}
	}

	// Update containers atomically via the shared upsertContainers helper.
	if len(result.Containers) > 0 {
		upsertContainers(machineID, result.Containers)
	}

	log.Printf("api poll: %s (%s) cpu=%.1f%% ram=%d/%dMB",
		hostname, adapterType,
		result.CPUPercent,
		result.RAMUsed/1024/1024, result.RAMTotal/1024/1024)

	// Broadcast SSE update.
	data, _ := getMachinesJSON()
	sseMsg, _ := json.Marshal(map[string]interface{}{
		"type": "machines",
		"data": json.RawMessage(data),
	})
	broadcastSSE(sseMsg)
}

// --- Background Poller ---

func startAPIPollers() {
	rows, err := db.Query("SELECT id, name, adapter_type, base_url, auth_config, tls_config, poll_interval_secs FROM api_machines WHERE enabled = TRUE")
	if err != nil {
		log.Printf("failed to load api_machines: %v", err)
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, name, adapterType, baseURL, authConfig, tlsConfig string
		var pollInterval int
		if err := rows.Scan(&id, &name, &adapterType, &baseURL, &authConfig, &tlsConfig, &pollInterval); err != nil {
			log.Printf("failed to scan api_machine: %v", err)
			continue
		}
		startAPIPoller(id, name, adapterType, baseURL, json.RawMessage(authConfig), json.RawMessage(tlsConfig), pollInterval)
		count++
	}
	if count > 0 {
		log.Printf("started %d API poller(s)", count)
	}
}

func startAPIPoller(id, name, adapterType, baseURL string, authConfig json.RawMessage, tlsConfig json.RawMessage, intervalSecs int) {
	apiPollersMu.Lock()
	defer apiPollersMu.Unlock()

	// Stop existing poller if any.
	if entry, ok := apiPollers[id]; ok {
		entry.cancel()
	}

	ctx, cancel := context.WithCancel(context.Background())
	apiPollers[id] = &apiPollerEntry{cancel: cancel}

	go func() {
		// Initial poll after a short delay.
		time.Sleep(2 * time.Second)

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			result, err := doPoll(adapterType, baseURL, authConfig, tlsConfig)
			if err != nil {
				log.Printf("api poll error: %s (%s): %v", name, adapterType, err)
				db.Exec("UPDATE api_machines SET last_error = ?, last_poll_at = CURRENT_TIMESTAMP WHERE id = ?", err.Error(), id)
				if mErr := markAPIMachineError("api-"+id, name, adapterType); mErr != nil {
					log.Printf("api poll: error-status upsert failed for %s: %v", name, mErr)
				}
			} else {
				storeAPIPollResult(id, name, adapterType, baseURL, result)
				db.Exec("UPDATE api_machines SET last_poll_at = CURRENT_TIMESTAMP, last_error = NULL WHERE id = ?", id)
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(intervalSecs) * time.Second):
			}
		}
	}()
}

func stopAPIPoller(id string) {
	apiPollersMu.Lock()
	defer apiPollersMu.Unlock()
	if entry, ok := apiPollers[id]; ok {
		entry.cancel()
		delete(apiPollers, id)
	}
}

func stopAllAPIPollers() {
	apiPollersMu.Lock()
	defer apiPollersMu.Unlock()
	for id, entry := range apiPollers {
		entry.cancel()
		delete(apiPollers, id)
	}
}

var _ = strconv.Itoa
