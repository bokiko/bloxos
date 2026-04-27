package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

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
	hubURL      string
	token       string
	agentSecret string

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
		"start_container":   true,
		"refresh_metrics":   true,
	}

	// interestingServicePatterns are services we report to the hub.
	interestingServicePatterns = []string{
		"docker", "ollama", "nginx", "caddy", "redis", "postgres", "mysql",
		"mongo", "node", "python", "gunicorn", "uvicorn", "flask", "grafana",
		"prometheus", "netdata", "ssh", "ufw", "fail2ban", "cron", "bloxos",
	}
)

// credentialFilePath returns the path where the agent stores its durable secret.
func credentialFilePath() string {
	// Prefer /etc/bloxos/agent-secret if running as root.
	if u, err := user.Current(); err == nil && u.Uid == "0" {
		return "/etc/bloxos/agent-secret"
	}
	// Otherwise use ~/.bloxos/agent-secret.
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".bloxos", "agent-secret")
}

// loadCredentialFile reads the agent secret from the credential file.
func loadCredentialFile() string {
	path := credentialFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// saveCredentialFile writes the agent secret to the credential file with 0600 perms.
func saveCredentialFile(secret string) error {
	path := credentialFilePath()
	dir := filepath.Dir(path)
	// 0755 so the install-time curl (running as the invoking user) can traverse
	// the dir to read /etc/bloxos/ca.crt. The secret file itself is 0600.
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create credential dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(secret+"\n"), 0600); err != nil {
		return fmt.Errorf("write credential file: %w", err)
	}
	log.Printf("agent secret saved to %s", path)
	return nil
}

// caCertFilePath returns the path where the agent expects an additional trusted CA.
func caCertFilePath() (string, bool) {
	if env := os.Getenv("BLOXOS_CA_CERT"); env != "" {
		return env, true
	}
	// Prefer /etc/bloxos/ca.crt if running as root.
	if u, err := user.Current(); err == nil && u.Uid == "0" {
		return "/etc/bloxos/ca.crt", false
	}
	// Otherwise use ~/.bloxos/ca.crt.
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".bloxos", "ca.crt"), false
}

func loadRootCAs() (*x509.CertPool, string, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}

	path, explicit := caCertFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && !explicit {
			return pool, "", nil
		}
		return nil, "", fmt.Errorf("read CA cert %s: %w", path, err)
	}
	if ok := pool.AppendCertsFromPEM(data); !ok {
		return nil, "", fmt.Errorf("parse CA cert %s: no certificates found", path)
	}
	return pool, path, nil
}

func websocketDialerFor(rawURL string) (*websocket.Dialer, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid websocket URL: %w", err)
	}

	dialer := *websocket.DefaultDialer
	if u.Scheme != "wss" {
		return &dialer, nil
	}

	tlsConfig := &tls.Config{}
	if os.Getenv("BLOXOS_TLS_INSECURE") == "1" {
		tlsConfig.InsecureSkipVerify = true
		log.Printf("WARNING: BLOXOS_TLS_INSECURE=1 disables TLS verification for %s", u.Host)
	} else {
		rootCAs, caPath, err := loadRootCAs()
		if err != nil {
			return nil, err
		}
		tlsConfig.RootCAs = rootCAs
		if caPath != "" {
			log.Printf("agent TLS: trusting additional CA from %s", caPath)
		}
	}

	dialer.TLSClientConfig = tlsConfig
	return &dialer, nil
}

func main() {
	var (
		installSvc   bool
		uninstallSvc bool
	)
	flag.StringVar(&hubURL, "hub", "ws://localhost:4000/ws/agent", "Hub WebSocket URL")
	flag.StringVar(&token, "token", "", "Registration token")
	flag.StringVar(&agentSecret, "secret", "", "Agent secret for reconnection")
	flag.BoolVar(&installSvc, "install-service", false, "Install as a Windows service and exit (Windows only)")
	flag.BoolVar(&uninstallSvc, "uninstall-service", false, "Uninstall the Windows service and exit (Windows only)")
	flag.Parse()

	if installSvc {
		if err := platformInstallService(); err != nil {
			log.Fatalf("install-service: %v", err)
		}
		return
	}
	if uninstallSvc {
		if err := platformUninstallService(); err != nil {
			log.Fatalf("uninstall-service: %v", err)
		}
		return
	}

	// Env var fallback.
	if hubURL == "ws://localhost:4000/ws/agent" {
		if env := os.Getenv("BLOXOS_HUB"); env != "" {
			hubURL = env + "/ws/agent"
		}
	}
	if agentSecret == "" {
		if env := os.Getenv("BLOXOS_SECRET"); env != "" {
			agentSecret = env
		}
	}
	if token == "" {
		if env := os.Getenv("BLOXOS_TOKEN"); env != "" {
			token = env
		}
	}

	// Priority: 1) --secret / BLOXOS_SECRET / credential file, 2) --token / BLOXOS_TOKEN
	if agentSecret == "" {
		agentSecret = loadCredentialFile()
	}
	if agentSecret == "" && token == "" {
		log.Fatal("--token or --secret is required (or set BLOXOS_TOKEN / BLOXOS_SECRET)")
	}

	if agentSecret != "" {
		log.Printf("using agent secret for authentication")
	} else {
		log.Printf("using install token for initial enrollment")
	}

	runPlatformAgent()
}

func connectLoop(machineID string) {
	backoff := time.Second
	maxBackoff := 60 * time.Second

	for {
		// On reconnect, prefer credential file if it exists.
		if stored := loadCredentialFile(); stored != "" {
			agentSecret = stored
		}

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
	// Prefer secret for reconnection, fall back to token for initial enrollment.
	if agentSecret != "" {
		q.Set("secret", agentSecret)
	} else if token != "" {
		q.Set("token", token)
	}
	u.RawQuery = q.Encode()

	log.Printf("connecting to %s", hubURL)
	dialer, err := websocketDialerFor(u.String())
	if err != nil {
		return fmt.Errorf("build websocket dialer: %w", err)
	}
	conn, _, err := dialer.Dial(u.String(), nil)
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

			// Decode the type field to dispatch.
			var envelope struct {
				Type        string `json:"type"`
				AgentSecret string `json:"agent_secret"`
			}
			if err := json.Unmarshal(msg, &envelope); err != nil {
				log.Printf("invalid message from hub: %v", err)
				continue
			}

			switch envelope.Type {
			case "enrolled":
				if envelope.AgentSecret != "" {
					log.Printf("received enrollment secret from hub")
					agentSecret = envelope.AgentSecret
					token = "" // Clear token from memory.
					if err := saveCredentialFile(agentSecret); err != nil {
						log.Printf("WARNING: failed to save credential file: %v", err)
					} else {
						log.Printf("enrollment complete - will use secret for future connections")
					}
				}

			case "agent_version":
				// Hub announced what version it expects us to be running.
				// Phase 8 self-update — runs the download/verify/install
				// flow in its own goroutine internally.
				handleAgentVersion(msg)

			default:
				// Anything else is a command (restart_service, refresh_metrics, etc.)
				go handleCommand(conn, &writeMu, msg)
			}
		}
	}()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Send immediately on connect, then every 30s. Metrics MUST go first
	// because the hub creates the machines row from the metric payload
	// (hostname/IP/OS); any other persisted message that arrives before the
	// row exists is silently dropped by a plain UPDATE.
	if err := sendAll(conn, &writeMu, machineID); err != nil {
		return err
	}

	// Send hardware snapshot once per connect, AFTER the first metric so the
	// machines row is guaranteed to exist. The hub keeps whatever we last
	// reported and overwrites on the next connect if anything genuinely
	// changed (DIMM swap, new disk, etc.).
	if err := sendHardware(conn, &writeMu, machineID); err != nil {
		log.Printf("send hardware error: %v", err)
	}

	// Phase 8 — report our running version so the hub can display it on
	// the dashboard and detect if we're out of date.
	reportAgentVersion(conn, &writeMu)

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

func sendHardware(conn *websocket.Conn, mu *sync.Mutex, machineID string) error {
	hw := collectHardware(machineID, collectGPUMetrics())
	if err := writeJSON(conn, mu, hw); err != nil {
		return fmt.Errorf("write hardware: %w", err)
	}
	return nil
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
		if !platformSupportsTerminal() {
			log.Printf("start_terminal: not supported on this platform (%s)", runtime.GOOS)
			if cmd.ID != "" {
				resp := CommandResponse{
					Type:  "command_response",
					ID:    cmd.ID,
					Error: fmt.Sprintf("terminal not supported on %s", runtime.GOOS),
				}
				writeJSON(conn, mu, resp)
			}
			return
		}
		handleStartTerminalPlatform(cmd, msg)
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

	// refresh_metrics doesn't shell out — it triggers an immediate metrics push.
	// The captured conn/mu in the closure is the active connection; the same
	// writeMu serializes the response below and the goroutine's sendAll, so
	// no package-level state is required.
	if cmd.Type == "refresh_metrics" {
		machineID := getMachineID()
		go func() {
			if err := sendAll(conn, mu, machineID); err != nil {
				log.Printf("refresh_metrics: sendAll error: %v", err)
			}
		}()
		resp := CommandResponse{
			Type:    "command_response",
			ID:      cmd.ID,
			Success: true,
			Output:  "metrics refresh triggered",
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

	execCmd, err := buildPlatformCommand(cmd.Type, cmd.Target)
	if err != nil {
		resp := CommandResponse{
			Type:  "command_response",
			ID:    cmd.ID,
			Error: err.Error(),
		}
		writeJSON(conn, mu, resp)
		return
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
	ID               string        `xml:"id,attr"`
	ProductName      string        `xml:"product_name"`
	FanSpeed         string        `xml:"fan_speed"`
	Temperature      nvTemperature `xml:"temperature"`
	Utilization      nvUtilization `xml:"utilization"`
	FBMemory         nvFBMemory    `xml:"fb_memory_usage"`
	GPUPowerReadings nvPower       `xml:"gpu_power_readings"`
	PowerReadings    nvPower       `xml:"power_readings"`
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
	smiPath := resolveNvidiaSmiPath()
	if smiPath == "" {
		return nil
	}
	out, err := exec.Command(smiPath, "-x", "-q").Output()
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
