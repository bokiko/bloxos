package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

type Metrics struct {
	MachineID      string  `json:"machine_id"`
	Hostname       string  `json:"hostname"`
	IP             string  `json:"ip,omitempty"`
	OS             string  `json:"os,omitempty"`
	CPUPercent     float64 `json:"cpu_percent"`
	RAMUsedBytes   uint64  `json:"ram_used_bytes"`
	RAMTotalBytes  uint64  `json:"ram_total_bytes"`
	DiskUsedBytes  uint64  `json:"disk_used_bytes"`
	DiskTotalBytes uint64  `json:"disk_total_bytes"`
	GPUTemp        float64 `json:"gpu_temp,omitempty"`
	GPUUtil        float64 `json:"gpu_util_percent,omitempty"`
	GPUVRAMUsed    uint64  `json:"gpu_vram_used_bytes,omitempty"`
	GPUVRAMTotal   uint64  `json:"gpu_vram_total_bytes,omitempty"`
	Timestamp      string  `json:"timestamp"`
}

var (
	hubURL string
	token  string
)

func main() {
	flag.StringVar(&hubURL, "hub", "ws://localhost:4000/ws/agent", "Hub WebSocket URL")
	flag.StringVar(&token, "token", "", "Registration token")
	flag.Parse()

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

		// Exponential backoff with jitter.
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

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Send immediately on connect, then every 30s.
	if err := sendMetrics(conn, machineID); err != nil {
		return err
	}

	for range ticker.C {
		if err := sendMetrics(conn, machineID); err != nil {
			return err
		}
	}

	return nil
}

func sendMetrics(conn *websocket.Conn, machineID string) error {
	m, err := collectMetrics(machineID)
	if err != nil {
		log.Printf("collect metrics error: %v", err)
		return nil // Do not disconnect on metric collection errors.
	}

	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal error: %w", err)
	}

	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
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

	return &Metrics{
		MachineID:      machineID,
		Hostname:       hostname,
		IP:             localIP,
		OS:             osInfo,
		CPUPercent:     cpuAvg,
		RAMUsedBytes:   memInfo.Used,
		RAMTotalBytes:  memInfo.Total,
		DiskUsedBytes:  diskInfo.Used,
		DiskTotalBytes: diskInfo.Total,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func getMachineID() string {
	info, err := host.Info()
	if err != nil || info.HostID == "" {
		hostname, _ := os.Hostname()
		return hostname
	}
	return info.HostID
}

// getOutboundIP finds the local IP that would route to the internet/hub.
func getOutboundIP() string {
	conn, err := net.DialTimeout("udp", "8.8.8.8:80", 2*time.Second)
	if err != nil {
		return ""
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}
