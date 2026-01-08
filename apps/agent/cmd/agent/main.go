package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bloxos/agent/internal/collector"
	"github.com/bloxos/agent/internal/config"
	"github.com/bloxos/agent/internal/executor"
	"github.com/bloxos/agent/internal/installer"
	"github.com/bloxos/agent/internal/ws"
)

const version = "0.3.0"

var exec *executor.Executor
var inst *installer.Installer

func main() {
	fmt.Printf("BloxOs Agent v%s\n", version)

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Config error: %v", err)
	}

	if cfg.Debug {
		log.Printf("Config: server=%s, interval=%ds, gpu=%v, cpu=%v",
			cfg.ServerURL, cfg.PollInterval, cfg.GPUEnabled, cfg.CPUEnabled)
	}

	// Create components
	coll := collector.New()
	exec = executor.New(cfg.Debug)
	inst = installer.New(cfg.Debug)

	// Get initial system info
	sysInfo, err := coll.GetSystemInfo()
	if err != nil {
		log.Fatalf("Failed to get system info: %v", err)
	}
	log.Printf("Hostname: %s, OS: %s %s", sysInfo.Hostname, sysInfo.OS, sysInfo.OSVersion)

	// Create WebSocket client
	wsClient := ws.NewClient(cfg.ServerURL, cfg.Token, cfg.Debug)

	// Set up command handler
	wsClient.SetCommandHandler(func(cmd *ws.Command) (bool, error) {
		return handleCommand(cmd, cfg)
	})

	// Set up connect handler
	wsClient.SetConnectHandler(func() {
		log.Println("Connected to server")
		// Send initial stats immediately
		sendStats(wsClient, coll, cfg)
		// Send miner status
		sendMinerStatus(wsClient, coll)
	})

	// Set up disconnect handler
	wsClient.SetDisconnectHandler(func() {
		log.Println("Disconnected from server")
	})

	// Start WebSocket connection (auto-reconnect is built-in)
	log.Println("Connecting to server...")
	if err := wsClient.Connect(); err != nil {
		log.Fatalf("Failed to start WebSocket client: %v", err)
	}

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start stats collection loop
	ticker := time.NewTicker(time.Duration(cfg.PollInterval) * time.Second)
	defer ticker.Stop()

	// Miner status ticker (every 10 seconds)
	minerTicker := time.NewTicker(10 * time.Second)
	defer minerTicker.Stop()

	log.Printf("Starting stats collection (every %ds)...", cfg.PollInterval)

	// Main loop
	for {
		select {
		case <-ticker.C:
			if wsClient.IsConnected() {
				sendStats(wsClient, coll, cfg)
			}
		case <-minerTicker.C:
			if wsClient.IsConnected() {
				sendMinerStatus(wsClient, coll)
			}
		case sig := <-sigChan:
			log.Printf("Received %v, shutting down...", sig)
			wsClient.Close()
			return
		}
	}
}

// sendStats collects and sends stats to the server
func sendStats(client *ws.Client, coll *collector.Collector, cfg *config.Config) {
	stats := make(map[string]interface{})

	// Collect GPU stats
	if cfg.GPUEnabled {
		gpus, err := coll.GetGPUStats()
		if err != nil {
			if cfg.Debug {
				log.Printf("GPU stats error: %v", err)
			}
		} else {
			stats["gpus"] = gpus
			if cfg.Debug {
				log.Printf("Collected %d GPU(s)", len(gpus))
			}
		}
	}

	// Collect CPU stats
	if cfg.CPUEnabled {
		cpu, err := coll.GetCPUStats()
		if err != nil {
			if cfg.Debug {
				log.Printf("CPU stats error: %v", err)
			}
		} else {
			stats["cpu"] = cpu
			if cfg.Debug && cpu.Usage != nil {
				log.Printf("CPU: %s, Usage: %.1f%%", cpu.Model, *cpu.Usage)
			}
		}
	}

	// Send stats via WebSocket
	if err := client.SendStats(stats); err != nil {
		log.Printf("Failed to send stats: %v", err)
	} else if cfg.Debug {
		log.Printf("Stats sent successfully")
	}
}

// sendMinerStatus sends current miner status to the server
func sendMinerStatus(client *ws.Client, coll *collector.Collector) {
	// First try to get detailed stats from miner API
	minerStats := coll.DetectRunningMiner()
	
	if minerStats != nil && minerStats.Running {
		status := map[string]interface{}{
			"name":      minerStats.Name,
			"version":   minerStats.Version,
			"running":   true,
			"algorithm": minerStats.Algorithm,
			"pool":      minerStats.Pool,
			"hashrate":  minerStats.Hashrate,
			"uptime":    minerStats.Uptime,
			"shares": map[string]int{
				"accepted": minerStats.Shares.Accepted,
				"rejected": minerStats.Shares.Rejected,
			},
		}
		
		if len(minerStats.GPUStats) > 0 {
			status["gpuStats"] = minerStats.GPUStats
		}
		
		if err := client.SendMinerStatus(status); err != nil {
			log.Printf("Failed to send miner status: %v", err)
		}
		return
	}
	
	// Fallback to basic executor status
	status := exec.GetMinerStatus()
	if err := client.SendMinerStatus(status); err != nil {
		log.Printf("Failed to send miner status: %v", err)
	}
}

// handleCommand handles commands from the server
func handleCommand(cmd *ws.Command, cfg *config.Config) (bool, error) {
	log.Printf("Executing command: %s", cmd.Type)

	switch cmd.Type {
	case "start_miner":
		return handleStartMiner(cmd.Payload, cfg)
	case "stop_miner":
		return handleStopMiner(cmd.Payload, cfg)
	case "restart_miner":
		return handleRestartMiner(cmd.Payload, cfg)
	case "install_miner":
		return handleInstallMiner(cmd.Payload, cfg)
	case "uninstall_miner":
		return handleUninstallMiner(cmd.Payload, cfg)
	case "list_miners":
		return handleListMiners(cfg)
	case "apply_oc":
		return handleApplyOC(cmd.Payload, cfg)
	case "reboot":
		return handleReboot(cfg)
	case "shutdown":
		return handleShutdown(cfg)
	default:
		return false, fmt.Errorf("unknown command type: %s", cmd.Type)
	}
}

func handleStartMiner(payload interface{}, cfg *config.Config) (bool, error) {
	if payload == nil {
		return false, fmt.Errorf("miner config required")
	}

	// Convert payload to MinerConfig
	data, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("invalid payload: %w", err)
	}

	var config executor.MinerConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return false, fmt.Errorf("invalid miner config: %w", err)
	}

	if err := exec.StartMiner(&config); err != nil {
		return false, err
	}

	return true, nil
}

func handleStopMiner(payload interface{}, cfg *config.Config) (bool, error) {
	if err := exec.StopMiner(); err != nil {
		return false, err
	}
	return true, nil
}

func handleRestartMiner(payload interface{}, cfg *config.Config) (bool, error) {
	if err := exec.RestartMiner(); err != nil {
		return false, err
	}
	return true, nil
}

func handleApplyOC(payload interface{}, cfg *config.Config) (bool, error) {
	if payload == nil {
		return false, fmt.Errorf("OC config required")
	}

	// Convert payload to OCConfig
	data, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("invalid payload: %w", err)
	}

	var config executor.OCConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return false, fmt.Errorf("invalid OC config: %w", err)
	}

	if err := exec.ApplyOC(&config); err != nil {
		return false, err
	}

	return true, nil
}

func handleReboot(cfg *config.Config) (bool, error) {
	// Start reboot in background so we can respond first
	go func() {
		time.Sleep(2 * time.Second)
		exec.Reboot()
	}()
	return true, nil
}

func handleShutdown(cfg *config.Config) (bool, error) {
	// Start shutdown in background so we can respond first
	go func() {
		time.Sleep(2 * time.Second)
		exec.Shutdown()
	}()
	return true, nil
}

// handleInstallMiner installs a miner from GitHub releases
func handleInstallMiner(payload interface{}, cfg *config.Config) (bool, error) {
	if payload == nil {
		return false, fmt.Errorf("miner name required")
	}

	// Extract miner name from payload
	data, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("invalid payload: %w", err)
	}

	var req struct {
		MinerName string `json:"minerName"`
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return false, fmt.Errorf("invalid install request: %w", err)
	}

	if req.MinerName == "" {
		return false, fmt.Errorf("miner name required")
	}

	log.Printf("Installing miner: %s", req.MinerName)

	// Install the miner (this may take a while)
	if err := inst.Install(req.MinerName); err != nil {
		return false, fmt.Errorf("failed to install %s: %w", req.MinerName, err)
	}

	log.Printf("Miner %s installed successfully", req.MinerName)
	return true, nil
}

// handleUninstallMiner removes an installed miner
func handleUninstallMiner(payload interface{}, cfg *config.Config) (bool, error) {
	if payload == nil {
		return false, fmt.Errorf("miner name required")
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("invalid payload: %w", err)
	}

	var req struct {
		MinerName string `json:"minerName"`
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return false, fmt.Errorf("invalid uninstall request: %w", err)
	}

	if req.MinerName == "" {
		return false, fmt.Errorf("miner name required")
	}

	log.Printf("Uninstalling miner: %s", req.MinerName)

	if err := inst.Uninstall(req.MinerName); err != nil {
		return false, fmt.Errorf("failed to uninstall %s: %w", req.MinerName, err)
	}

	log.Printf("Miner %s uninstalled successfully", req.MinerName)
	return true, nil
}

// handleListMiners returns list of available and installed miners
func handleListMiners(cfg *config.Config) (bool, error) {
	installed, err := inst.ListInstalled()
	if err != nil {
		return false, fmt.Errorf("failed to list installed miners: %w", err)
	}

	available := inst.ListAvailable()
	
	log.Printf("Available miners: %d, Installed miners: %d", len(available), len(installed))
	return true, nil
}
