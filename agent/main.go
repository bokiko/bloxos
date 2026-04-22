package main

import (
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

// GPUInfo holds per-GPU metrics parsed from nvidia-smi XML.
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

type Metrics struct {
	Type           string    `json:"type"`
	MachineID      string    `json:"machine_id"`
	Hostname       string    `json:"hostname"`
	IP             string    `json:"ip,omitempty"`
	OS             string    `json:"os,omitempty"`
	CPUPercent     float64   `json:"cpu_percent"`
	RAMUsedBytes   uint64    `json:"ram_used_bytes"`
	RAMTotalBytes  uint64    `json:"ram_total_bytes"`
	DiskUsedBytes  uint64    `json:"disk_used_bytes"`
	DiskTotalBytes uint64    `json:"disk_total_bytes"`
	GPUs           []GPUInfo `json:"gpus"`
	Timestamp      string    `json:"timestamp"`
	SentAt         string    `json:"sent_at,omitempty"`
}

// Command is received from the hub.
type Command struct {
	Type      string `json:"type"`
	Target    string `json:"target"`
	ID        string `json:"id"`
	SessionID string `json:"session_id,omitempty"`
}

// CommandResponse is sent back to the hub.
type CommandResponse struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Error   string `json:"error"`
}

// ServiceInfo represents a discovered systemd service.
type ServiceInfo struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Description string `json:"description"`
}

// ServicesMessage is sent to the hub.
type ServicesMessage struct {
	Type      string        `json:"type"`
	Hostname  string        `json:"hostname"`
	MachineID string        `json:"machine_id"`
	Services  []ServiceInfo `json:"services"`
}

// ContainerInfo represents a discovered Docker container.
type ContainerInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Image  string `json:"image"`
}

// ContainersMessage is sent to the hub.
type ContainersMessage struct {
	Type       string          `json:"type"`
	Hostname   string          `json:"hostname"`
	MachineID  string          `json:"machine_id"`
	Containers []ContainerInfo `json:"containers"`
}

// TerminalResize is sent from browser -> hub -> agent to resize the PTY.
type TerminalResize struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

var (
	hubURL string
	token  string

	// validTarget allows alphanumeric, hyphens, underscores, dots only.
	validTarget = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

	// allowedCommands lists the command types we accept.
	allowedCommands = map[string]bool{
		"restart_service":   true,
		"stop_service":      true,
		"start_service":     true,
		"restart_container": true,
		"reboot":            true,
		"shutdown":          true,
		"start_terminal":    true,
		"start_container":    true,
	}

	// interestingServicePatterns are services we report to the hub.
	interestingServicePatterns = []string{
		"docker", "ollama", "nginx", "caddy", "redis", "postgres", "mysql",
		"mongo", "node", "python", "gunicorn", "uvicorn", "flask", "grafana",
		"prometheus", "netdata", "ssh", "ufw", "fail2ban", "cron", "bloxos",
	}
)

func main() {
	flag.StringVar(&hubURL, "hub", "ws://localhost:4000/ws/agent", "Hub WebSocket URL")
	flag.StringVar(&token, "token", "", "Registration token")
	flag.Parse()
	// Env var fallback.
	if hubURL == "ws://localhost:4000/ws/agent" {
		if env := os.Getenv("BLOXOS_HUB"); env != "" {
			hubURL = env + "/ws/agent"
		}
	}
	if token == "" {
		if env := os.Getenv("BLOXOS_TOKEN"); env != "" {
			token = env
		}
	}

	if token == "" {
		log.Fatal("--token is required")
	}

	machineID := getMachineID()
	log.Printf("agent starting: machine_id=%s hub=%s", machineID, hubURL)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("shutting down")
		os.Exit(0)
	}()

	connectLoop(machineID)
}

func connectLoop(machineID string) {
	backoff := time.Second
	maxBackoff := 60 * time.Second

	for {
		err := runAgent(machineID)
		if err != nil {
			log.Printf("connection error: %v", err)
		}

		jitter := time.Duration(rand.Int63n(int64(backoff) / 2))
		wait := backoff + jitter
		log.Printf("reconnecting in %s", wait)
		time.Sleep(wait)

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func runAgent(machineID string) error {
	u, err := url.Parse(hubURL)
	if err != nil {
		return fmt.Errorf("invalid hub URL: %w", err)
	}

	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()

	log.Printf("connecting to %s", hubURL)
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("dial failed: %w", err)
	}
	defer conn.Close()
	log.Println("connected to hub")

	// Mutex for concurrent writes to WebSocket.
	var writeMu sync.Mutex

	// Start a goroutine to read incoming commands from the hub.
	errCh := make(chan error, 1)
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				errCh <- fmt.Errorf("read error: %w", err)
				return
			}
			go handleCommand(conn, &writeMu, msg)
		}
	}()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Send immediately on connect, then every 30s.
	if err := sendAll(conn, &writeMu, machineID); err != nil {
		return err
	}

	for {
		select {
		case err := <-errCh:
			return err
		case <-ticker.C:
			if err := sendAll(conn, &writeMu, machineID); err != nil {
				return err
			}
		}
	}
}

func sendAll(conn *websocket.Conn, mu *sync.Mutex, machineID string) error {
	if err := sendMetrics(conn, mu, machineID); err != nil {
		return err
	}
	sendServices(conn, mu, machineID)
	sendContainers(conn, mu, machineID)
	return nil
}

func writeJSON(conn *websocket.Conn, mu *sync.Mutex, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal error: %w", err)
	}
	mu.Lock()
	defer mu.Unlock()
	return conn.WriteMessage(websocket.TextMessage, data)
}

func sendMetrics(conn *websocket.Conn, mu *sync.Mutex, machineID string) error {
	m, err := collectMetrics(machineID)
	if err != nil {
		log.Printf("collect metrics error: %v", err)
		return nil
	}

	if err := writeJSON(conn, mu, m); err != nil {
		return fmt.Errorf("write error: %w", err)
	}

	log.Printf("sent metrics: cpu=%.1f%% ram=%d/%dMB ip=%s",
		m.CPUPercent, m.RAMUsedBytes/1024/1024, m.RAMTotalBytes/1024/1024, m.IP)
	return nil
}

func collectMetrics(machineID string) (*Metrics, error) {
	hostname, _ := os.Hostname()
	osInfo := fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)

	cpuPercent, err := cpu.Percent(time.Second, false)
	if err != nil {
		return nil, fmt.Errorf("cpu: %w", err)
	}
	cpuAvg := 0.0
	if len(cpuPercent) > 0 {
		cpuAvg = cpuPercent[0]
	}

	memInfo, err := mem.VirtualMemory()
	if err != nil {
		return nil, fmt.Errorf("mem: %w", err)
	}

	diskInfo, err := disk.Usage("/")
	if err != nil {
		return nil, fmt.Errorf("disk: %w", err)
	}

	hostInfo, _ := host.Info()
	if hostInfo != nil && hostInfo.OS != "" {
		osInfo = fmt.Sprintf("%s %s (%s)", hostInfo.Platform, hostInfo.PlatformVersion, hostInfo.KernelArch)
	}

	localIP := getOutboundIP()

	gpus := collectGPUMetrics()

	return &Metrics{
		Type:           "metrics",
		MachineID:      machineID,
		Hostname:       hostname,
		IP:             localIP,
		OS:             osInfo,
		CPUPercent:     cpuAvg,
		RAMUsedBytes:   memInfo.Used,
		RAMTotalBytes:  memInfo.Total,
		DiskUsedBytes:  diskInfo.Used,
		DiskTotalBytes: diskInfo.Total,
		GPUs:           gpus,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		SentAt:         time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

// handleCommand processes an incoming command from the hub.
func handleCommand(conn *websocket.Conn, mu *sync.Mutex, msg []byte) {
	var cmd Command
	if err := json.Unmarshal(msg, &cmd); err != nil {
		log.Printf("invalid command JSON: %v", err)
		return
	}

	// Handle start_terminal separately — it has its own flow.
	if cmd.Type == "start_terminal" {
		handleStartTerminal(cmd)
		return
	}

	if cmd.ID == "" {
		log.Printf("ignoring command with no ID")
		return
	}

	if !allowedCommands[cmd.Type] {
		resp := CommandResponse{
			Type:  "command_response",
			ID:    cmd.ID,
			Error: fmt.Sprintf("unknown command type: %s", cmd.Type),
		}
		writeJSON(conn, mu, resp)
		return
	}

	// Validate target for commands that require one.
	if cmd.Type != "reboot" && cmd.Type != "shutdown" {
		if cmd.Target == "" {
			resp := CommandResponse{
				Type:  "command_response",
				ID:    cmd.ID,
				Error: "target is required",
			}
			writeJSON(conn, mu, resp)
			return
		}
		if !validTarget.MatchString(cmd.Target) {
			resp := CommandResponse{
				Type:  "command_response",
				ID:    cmd.ID,
				Error: "invalid target name: only alphanumeric, hyphens, underscores, dots allowed",
			}
			writeJSON(conn, mu, resp)
			return
		}
	}

	var execCmd *exec.Cmd
	switch cmd.Type {
	case "restart_service":
		execCmd = exec.Command("sudo", "systemctl", "restart", cmd.Target)
	case "stop_service":
		execCmd = exec.Command("sudo", "systemctl", "stop", cmd.Target)
	case "start_service":
		execCmd = exec.Command("sudo", "systemctl", "start", cmd.Target)
	case "restart_container":
		execCmd = exec.Command("sudo", "docker", "restart", cmd.Target)
	case "start_container":
		execCmd = exec.Command("sudo", "docker", "start", cmd.Target)
	case "reboot":
		execCmd = exec.Command("sudo", "reboot")
	case "shutdown":
		execCmd = exec.Command("sudo", "shutdown", "-h", "now")
	}

	output, err := execCmd.CombinedOutput()
	resp := CommandResponse{
		Type:    "command_response",
		ID:      cmd.ID,
		Success: err == nil,
		Output:  string(output),
	}
	if err != nil {
		resp.Error = err.Error()
	}

	log.Printf("command %s (id=%s target=%s): success=%v", cmd.Type, cmd.ID, cmd.Target, resp.Success)
	writeJSON(conn, mu, resp)
}

// handleStartTerminal spawns a PTY and connects it to the hub via a dedicated WebSocket.
func handleStartTerminal(cmd Command) {
	sessionID := cmd.SessionID
	if sessionID == "" {
		log.Printf("start_terminal: missing session_id")
		return
	}
	log.Printf("starting terminal session: %s", sessionID)

	// Derive the hub HTTP host from the agent's hub WebSocket URL.
	u, err := url.Parse(hubURL)
	if err != nil {
		log.Printf("terminal: invalid hub URL: %v", err)
		return
	}
	// Build terminal relay URL: ws://host:port/ws/terminal/{session_id}?role=agent
	termURL := fmt.Sprintf("ws://%s/ws/terminal/%s?role=agent", u.Host, sessionID)

	// Spawn bash PTY.
	bashCmd := exec.Command("bash")
	bashCmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.Start(bashCmd)
	if err != nil {
		log.Printf("terminal: pty.Start failed: %v", err)
		return
	}
	defer func() {
		ptmx.Close()
		_ = bashCmd.Process.Kill()
		_, _ = bashCmd.Process.Wait()
		log.Printf("terminal session %s: PTY cleaned up", sessionID)
	}()

	// Set initial size.
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})

	// Connect to hub terminal relay.
	log.Printf("terminal: connecting to %s", termURL)
	ws, _, err := websocket.DefaultDialer.Dial(termURL, nil)
	if err != nil {
		log.Printf("terminal: dial hub failed: %v", err)
		return
	}
	defer ws.Close()
	log.Printf("terminal session %s: connected to hub relay", sessionID)

	done := make(chan struct{})

	// PTY stdout -> WebSocket (binary).
	go func() {
		defer func() {
			select {
			case <-done:
			default:
				close(done)
			}
		}()
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				if werr := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					log.Printf("terminal %s: ws write error: %v", sessionID, werr)
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					log.Printf("terminal %s: pty read error: %v", sessionID, err)
				}
				return
			}
		}
	}()

	// WebSocket -> PTY stdin. Also handle resize messages.
	go func() {
		defer func() {
			select {
			case <-done:
			default:
				close(done)
			}
		}()
		for {
			msgType, msg, err := ws.ReadMessage()
			if err != nil {
				log.Printf("terminal %s: ws read error: %v", sessionID, err)
				return
			}

			// Check if it's a text message that might be a resize command.
			if msgType == websocket.TextMessage {
				var resize TerminalResize
				if json.Unmarshal(msg, &resize) == nil && resize.Type == "resize" {
					if resize.Cols > 0 && resize.Rows > 0 {
						_ = pty.Setsize(ptmx, &pty.Winsize{
							Rows: resize.Rows,
							Cols: resize.Cols,
						})
						log.Printf("terminal %s: resized to %dx%d", sessionID, resize.Cols, resize.Rows)
					}
					continue
				}
			}

			// Otherwise write to PTY stdin.
			if _, err := ptmx.Write(msg); err != nil {
				log.Printf("terminal %s: pty write error: %v", sessionID, err)
				return
			}
		}
	}()

	// Wait for either direction to finish, or for the bash process to exit.
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- bashCmd.Wait()
	}()

	select {
	case <-done:
		log.Printf("terminal session %s: WebSocket/PTY loop ended", sessionID)
	case err := <-waitCh:
		log.Printf("terminal session %s: bash exited: %v", sessionID, err)
	}
}

// sendServices discovers systemd services and sends them to the hub.
func sendServices(conn *websocket.Conn, mu *sync.Mutex, machineID string) {
	hostname, _ := os.Hostname()

	out, err := exec.Command("systemctl", "list-units", "--type=service",
		"--state=active,inactive,failed", "--no-pager", "--no-legend").Output()
	if err != nil {
		log.Printf("service discovery error: %v", err)
		return
	}

	var services []ServiceInfo
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}

		unitName := fields[0]
		name := strings.TrimSuffix(unitName, ".service")
		activeState := fields[2]
		description := strings.Join(fields[4:], " ")

		if activeState == "failed" {
			services = append(services, ServiceInfo{
				Name:        name,
				Status:      activeState,
				Description: description,
			})
			continue
		}

		if isInterestingService(name) {
			services = append(services, ServiceInfo{
				Name:        name,
				Status:      activeState,
				Description: description,
			})
		}
	}

	if len(services) == 0 {
		return
	}

	msg := ServicesMessage{
		Type:      "services",
		Hostname:  hostname,
		MachineID: machineID,
		Services:  services,
	}

	if err := writeJSON(conn, mu, msg); err != nil {
		log.Printf("send services error: %v", err)
		return
	}
	log.Printf("sent %d services", len(services))
}

func isInterestingService(name string) bool {
	lower := strings.ToLower(name)
	for _, pat := range interestingServicePatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// sendContainers discovers Docker containers and sends them to the hub.
func sendContainers(conn *websocket.Conn, mu *sync.Mutex, machineID string) {
	hostname, _ := os.Hostname()

	if err := exec.Command("docker", "info").Run(); err != nil {
		return
	}

	out, err := exec.Command("docker", "ps", "-a", "--format",
		"{{.ID}}\t{{.Names}}\t{{.Status}}\t{{.Image}}").Output()
	if err != nil {
		log.Printf("docker discovery error: %v", err)
		return
	}

	var containers []ContainerInfo
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 4 {
			continue
		}

		status := normalizeContainerStatus(parts[2])

		containers = append(containers, ContainerInfo{
			ID:     parts[0],
			Name:   parts[1],
			Status: status,
			Image:  parts[3],
		})
	}

	if len(containers) == 0 {
		return
	}

	msg := ContainersMessage{
		Type:       "containers",
		Hostname:   hostname,
		MachineID:  machineID,
		Containers: containers,
	}

	if err := writeJSON(conn, mu, msg); err != nil {
		log.Printf("send containers error: %v", err)
		return
	}
	log.Printf("sent %d containers", len(containers))
}

func normalizeContainerStatus(raw string) string {
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "up") {
		return "running"
	}
	if strings.HasPrefix(lower, "exited") {
		return "exited"
	}
	if strings.Contains(lower, "created") {
		return "created"
	}
	if strings.Contains(lower, "paused") {
		return "paused"
	}
	if strings.Contains(lower, "restarting") {
		return "restarting"
	}
	return raw
}

func getMachineID() string {
	info, err := host.Info()
	if err != nil || info.HostID == "" {
		hostname, _ := os.Hostname()
		return hostname
	}
	return info.HostID
}

// nvidia-smi XML structures for parsing.
type nvidiaSmiLog struct {
	GPUs []nvGPU `xml:"gpu"`
}

type nvGPU struct {
	ID          string        `xml:"id,attr"`
	ProductName string        `xml:"product_name"`
	FanSpeed    string        `xml:"fan_speed"`
	Temperature nvTemperature `xml:"temperature"`
	Utilization nvUtilization `xml:"utilization"`
	FBMemory    nvFBMemory    `xml:"fb_memory_usage"`
	GPUPowerReadings nvPower `xml:"gpu_power_readings"`
	PowerReadings    nvPower `xml:"power_readings"`
}

type nvTemperature struct {
	GPUTemp string `xml:"gpu_temp"`
}

type nvUtilization struct {
	GPUUtil string `xml:"gpu_util"`
	MemUtil string `xml:"memory_util"`
}

type nvFBMemory struct {
	Total string `xml:"total"`
	Used  string `xml:"used"`
	Free  string `xml:"free"`
}

type nvPower struct {
	PowerDraw     string `xml:"power_draw"`
	AvgPowerDraw  string `xml:"average_power_draw"`
	InstPowerDraw string `xml:"instant_power_draw"`
}

func collectGPUMetrics() []GPUInfo {
	out, err := exec.Command("nvidia-smi", "-x", "-q").Output()
	if err != nil {
		return nil
	}

	var smiLog nvidiaSmiLog
	if err := xml.Unmarshal(out, &smiLog); err != nil {
		log.Printf("nvidia-smi XML parse error: %v", err)
		return nil
	}

	if len(smiLog.GPUs) == 0 {
		return nil
	}

	gpus := make([]GPUInfo, 0, len(smiLog.GPUs))
	for i, g := range smiLog.GPUs {
		gpu := GPUInfo{
			Index:         i,
			Name:          g.ProductName,
			TempC:         parseNvValue(g.Temperature.GPUTemp),
			UtilPercent:   parseNvValue(g.Utilization.GPUUtil),
			FanPercent:    parseNvValue(g.FanSpeed),
			MemUsedBytes:  mibToBytes(g.FBMemory.Used),
			MemTotalBytes: mibToBytes(g.FBMemory.Total),
		}

		pw := parseNvValue(g.GPUPowerReadings.PowerDraw)
		if pw == 0 {
			pw = parseNvValue(g.GPUPowerReadings.AvgPowerDraw)
		}
		if pw == 0 {
			pw = parseNvValue(g.GPUPowerReadings.InstPowerDraw)
		}
		if pw == 0 {
			pw = parseNvValue(g.PowerReadings.PowerDraw)
		}
		gpu.PowerWatts = pw

		gpus = append(gpus, gpu)
	}
	return gpus
}

func parseNvValue(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "N/A" || s == "[N/A]" {
		return 0
	}
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0
	}
	return v
}

func mibToBytes(s string) int64 {
	v := parseNvValue(s)
	return int64(v * 1024 * 1024)
}

func getOutboundIP() string {
	conn, err := net.DialTimeout("udp", "8.8.8.8:80", 2*time.Second)
	if err != nil {
		return ""
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}
