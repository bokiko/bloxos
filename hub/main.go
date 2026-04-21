package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	_ "modernc.org/sqlite"
)

// AgentMetrics is the JSON payload sent by each agent.
type AgentMetrics struct {
	MachineID      string  `json:"machine_id"`
	Hostname       string  `json:"hostname"`
	IP             string  `json:"ip,omitempty"`
	OS             string  `json:"os,omitempty"`
	CPUPercent     float64 `json:"cpu_percent"`
	RAMUsedBytes   int64   `json:"ram_used_bytes"`
	RAMTotalBytes  int64   `json:"ram_total_bytes"`
	DiskUsedBytes  int64   `json:"disk_used_bytes"`
	DiskTotalBytes int64   `json:"disk_total_bytes"`
	GPUTemp        float64 `json:"gpu_temp,omitempty"`
	GPUUtil        float64 `json:"gpu_util_percent,omitempty"`
	GPUVRAMUsed    int64   `json:"gpu_vram_used_bytes,omitempty"`
	GPUVRAMTotal   int64   `json:"gpu_vram_total_bytes,omitempty"`
	Timestamp      string  `json:"timestamp"`
}

// ConnectedAgent tracks a live WebSocket connection from an agent.
type ConnectedAgent struct {
	MachineID string
	Conn      *websocket.Conn
}

var (
	db       *sql.DB
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	// Connected agents keyed by machine ID.
	agents   = make(map[string]*ConnectedAgent)
	agentsMu sync.RWMutex

	// SSE subscribers: each channel receives JSON-encoded metrics.
	sseClients   = make(map[chan []byte]struct{})
	sseClientsMu sync.RWMutex
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

	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{
			"http://192.168.16.113:3000",
			"http://localhost:3000",
		},
		AllowMethods: []string{http.MethodGet, http.MethodOptions},
		AllowHeaders: []string{"Accept", "Content-Type", "Cache-Control"},
	}))

	e.GET("/health", handleHealth)
	e.GET("/ws/agent", handleAgentWS)
	e.GET("/api/events", handleSSE)
	e.GET("/api/machines", handleListMachines)
	e.GET("/api/machines/:id", handleGetMachine)

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

	CREATE INDEX IF NOT EXISTS idx_metrics_machine_time ON metrics(machine_id, timestamp);
	`
	_, err := db.Exec(schema)
	return err
}

func handleHealth(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func handleAgentWS(c echo.Context) error {
	token := c.QueryParam("token")
	if token == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "token required"})
	}

	// For now, accept any non-empty token. Phase 5 adds proper auth.
	h := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(h[:8])
	_ = tokenHash

	ws, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return err
	}
	defer ws.Close()

	var machineID string

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

		var m AgentMetrics
		if err := json.Unmarshal(msg, &m); err != nil {
			log.Printf("invalid metrics JSON: %v", err)
			continue
		}

		if machineID == "" {
			machineID = m.MachineID
			agentsMu.Lock()
			agents[machineID] = &ConnectedAgent{MachineID: machineID, Conn: ws}
			agentsMu.Unlock()
			log.Printf("agent registered: %s (%s)", m.Hostname, machineID)
		}

		upsertMachine(m)
		storeMetrics(m)
		broadcastSSE(msg)
	}
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
	_, err := db.Exec(`
		INSERT INTO metrics (machine_id, cpu_percent, ram_used_bytes, ram_total_bytes,
			disk_used_bytes, disk_total_bytes, gpu_temp, gpu_util_percent,
			gpu_vram_used_bytes, gpu_vram_total_bytes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, m.MachineID, m.CPUPercent, m.RAMUsedBytes, m.RAMTotalBytes,
		m.DiskUsedBytes, m.DiskTotalBytes, m.GPUTemp, m.GPUUtil,
		m.GPUVRAMUsed, m.GPUVRAMTotal)
	if err != nil {
		log.Printf("store metrics error: %v", err)
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

	// Send enriched snapshot: machines with their latest metrics merged
	snapshot, _ := getEnrichedMachinesJSON()
	fmt.Fprintf(c.Response(), "event: snapshot\ndata: %s\n\n", snapshot)
	flusher.Flush()

	for {
		select {
		case data := <-ch:
			fmt.Fprintf(c.Response(), "event: metrics\ndata: %s\n\n", data)
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
		LastSeen *string `json:"last_seen"`
	}
	err := db.QueryRow(`SELECT id, hostname, ip, os, status, last_seen FROM machines WHERE id = ?`, id).
		Scan(&m.ID, &m.Hostname, &m.IP, &m.OS, &m.Status, &m.LastSeen)
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

	return c.JSON(http.StatusOK, map[string]interface{}{
		"machine": m,
		"metrics": met,
	})
}

// getEnrichedMachinesJSON returns machines merged with their latest metrics,
// in the same shape as AgentMetrics so the dashboard can use them directly.
func getEnrichedMachinesJSON() ([]byte, error) {
	rows, err := db.Query(`
		SELECT m.id, m.hostname, m.ip, m.os, m.status, m.last_seen,
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
		MachineID      string  `json:"machine_id"`
		Hostname       string  `json:"hostname"`
		IP             *string `json:"ip"`
		OS             *string `json:"os"`
		Status         string  `json:"status"`
		CPUPercent     float64 `json:"cpu_percent"`
		RAMUsedBytes   int64   `json:"ram_used_bytes"`
		RAMTotalBytes  int64   `json:"ram_total_bytes"`
		DiskUsedBytes  int64   `json:"disk_used_bytes"`
		DiskTotalBytes int64   `json:"disk_total_bytes"`
		GPUTemp        float64 `json:"gpu_temp"`
		GPUUtil        float64 `json:"gpu_util_percent"`
		GPUVRAMUsed    int64   `json:"gpu_vram_used_bytes"`
		GPUVRAMTotal   int64   `json:"gpu_vram_total_bytes"`
		Timestamp      *string `json:"timestamp"`
	}

	var machines []EnrichedMachine
	for rows.Next() {
		var m EnrichedMachine
		var lastSeen *string
		if err := rows.Scan(&m.MachineID, &m.Hostname, &m.IP, &m.OS, &m.Status, &lastSeen,
			&m.CPUPercent, &m.RAMUsedBytes, &m.RAMTotalBytes,
			&m.DiskUsedBytes, &m.DiskTotalBytes,
			&m.GPUTemp, &m.GPUUtil, &m.GPUVRAMUsed, &m.GPUVRAMTotal,
			&m.Timestamp); err != nil {
			return nil, err
		}
		machines = append(machines, m)
	}
	if machines == nil {
		machines = []EnrichedMachine{}
	}
	return json.Marshal(machines)
}

func getMachinesJSON() ([]byte, error) {
	rows, err := db.Query(`SELECT id, hostname, ip, os, status, last_seen, created_at FROM machines ORDER BY hostname`)
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
		LastSeen  *string `json:"last_seen"`
		CreatedAt string  `json:"created_at"`
	}

	var machines []Machine
	for rows.Next() {
		var m Machine
		if err := rows.Scan(&m.ID, &m.Hostname, &m.IP, &m.OS, &m.Status, &m.LastSeen, &m.CreatedAt); err != nil {
			return nil, err
		}
		machines = append(machines, m)
	}
	if machines == nil {
		machines = []Machine{}
	}
	return json.Marshal(machines)
}
