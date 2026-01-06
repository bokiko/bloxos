package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bloxos/agent/internal/api"
	"github.com/bloxos/agent/internal/collector"
	"github.com/bloxos/agent/internal/config"
)

const version = "0.1.0"

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
	client := api.New(cfg.ServerURL, cfg.Token)

	// Get initial system info
	sysInfo, err := coll.GetSystemInfo()
	if err != nil {
		log.Fatalf("Failed to get system info: %v", err)
	}

	log.Printf("Hostname: %s, OS: %s %s", sysInfo.Hostname, sysInfo.OS, sysInfo.OSVersion)

	// Register with server
	log.Println("Registering with server...")
	if err := client.Register(sysInfo); err != nil {
		log.Printf("Warning: Failed to register: %v", err)
	} else {
		log.Println("Registered successfully")
	}

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start polling loop
	ticker := time.NewTicker(time.Duration(cfg.PollInterval) * time.Second)
	defer ticker.Stop()

	log.Printf("Starting poll loop (every %ds)...", cfg.PollInterval)

	// Run immediately
	poll(cfg, coll, client)

	// Main loop
	for {
		select {
		case <-ticker.C:
			poll(cfg, coll, client)
		case sig := <-sigChan:
			log.Printf("Received %v, shutting down...", sig)
			return
		}
	}
}

func poll(cfg *config.Config, coll *collector.Collector, client *api.Client) {
	payload := &api.ReportPayload{}

	// Collect GPU stats
	if cfg.GPUEnabled {
		gpus, err := coll.GetGPUStats()
		if err != nil {
			if cfg.Debug {
				log.Printf("GPU stats error: %v", err)
			}
		} else {
			payload.GPUs = gpus
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
			payload.CPU = cpu
			if cfg.Debug && cpu.Usage != nil {
				log.Printf("CPU: %s, Usage: %.1f%%", cpu.Model, *cpu.Usage)
			}
		}
	}

	// Report to server
	resp, err := client.ReportStats(payload)
	if err != nil {
		log.Printf("Failed to report stats: %v", err)
		return
	}

	if cfg.Debug {
		log.Printf("Report sent successfully")
	}

	// Handle server commands
	if resp.Command != "" {
		log.Printf("Received command: %s", resp.Command)
		// TODO: Handle commands (start_miner, stop_miner, etc.)
	}
}
