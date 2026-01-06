package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bloxos/agent/internal/collector"
	"github.com/bloxos/agent/internal/config"
	"github.com/bloxos/agent/internal/ws"
)

const version = "0.2.0"

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

	// Create collector
	coll := collector.New()

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

	log.Printf("Starting stats collection (every %ds)...", cfg.PollInterval)

	// Main loop
	for {
		select {
		case <-ticker.C:
			if wsClient.IsConnected() {
				sendStats(wsClient, coll, cfg)
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
	case "apply_oc":
		return handleApplyOC(cmd.Payload, cfg)
	case "apply_flight_sheet":
		return handleApplyFlightSheet(cmd.Payload, cfg)
	case "reboot":
		return handleReboot(cfg)
	case "shutdown":
		return handleShutdown(cfg)
	case "execute":
		return handleExecute(cmd.Payload, cfg)
	default:
		return false, fmt.Errorf("unknown command type: %s", cmd.Type)
	}
}

// Command handlers - these will be implemented in phase A2

func handleStartMiner(payload interface{}, cfg *config.Config) (bool, error) {
	log.Println("TODO: Implement start_miner")
	return false, fmt.Errorf("not implemented")
}

func handleStopMiner(payload interface{}, cfg *config.Config) (bool, error) {
	log.Println("TODO: Implement stop_miner")
	return false, fmt.Errorf("not implemented")
}

func handleRestartMiner(payload interface{}, cfg *config.Config) (bool, error) {
	log.Println("TODO: Implement restart_miner")
	return false, fmt.Errorf("not implemented")
}

func handleApplyOC(payload interface{}, cfg *config.Config) (bool, error) {
	log.Println("TODO: Implement apply_oc")
	return false, fmt.Errorf("not implemented")
}

func handleApplyFlightSheet(payload interface{}, cfg *config.Config) (bool, error) {
	log.Println("TODO: Implement apply_flight_sheet")
	return false, fmt.Errorf("not implemented")
}

func handleReboot(cfg *config.Config) (bool, error) {
	log.Println("TODO: Implement reboot")
	return false, fmt.Errorf("not implemented")
}

func handleShutdown(cfg *config.Config) (bool, error) {
	log.Println("TODO: Implement shutdown")
	return false, fmt.Errorf("not implemented")
}

func handleExecute(payload interface{}, cfg *config.Config) (bool, error) {
	log.Println("TODO: Implement execute")
	return false, fmt.Errorf("not implemented")
}
