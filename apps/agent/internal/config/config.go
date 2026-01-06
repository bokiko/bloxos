package config

import (
	"flag"
	"fmt"
	"os"
)

// Config holds the agent configuration
type Config struct {
	ServerURL     string
	Token         string
	PollInterval  int // seconds
	Debug         bool
	GPUEnabled    bool
	CPUEnabled    bool
}

// DefaultConfig returns a config with default values
func DefaultConfig() *Config {
	return &Config{
		ServerURL:    "http://localhost:3001",
		PollInterval: 30,
		Debug:        false,
		GPUEnabled:   true,
		CPUEnabled:   true,
	}
}

// Load parses config from flags and environment
func Load() (*Config, error) {
	cfg := DefaultConfig()

	// Command line flags
	flag.StringVar(&cfg.ServerURL, "server", cfg.ServerURL, "BloxOs server URL")
	flag.StringVar(&cfg.Token, "token", "", "Rig authentication token (required)")
	flag.IntVar(&cfg.PollInterval, "interval", cfg.PollInterval, "Poll interval in seconds")
	flag.BoolVar(&cfg.Debug, "debug", cfg.Debug, "Enable debug logging")
	flag.BoolVar(&cfg.GPUEnabled, "gpu", cfg.GPUEnabled, "Enable GPU monitoring")
	flag.BoolVar(&cfg.CPUEnabled, "cpu", cfg.CPUEnabled, "Enable CPU monitoring")
	flag.Parse()

	// Environment variable overrides
	if url := os.Getenv("BLOXOS_SERVER"); url != "" {
		cfg.ServerURL = url
	}
	if token := os.Getenv("BLOXOS_TOKEN"); token != "" {
		cfg.Token = token
	}

	// Validate required fields
	if cfg.Token == "" {
		return nil, fmt.Errorf("token is required (use -token flag or BLOXOS_TOKEN env)")
	}

	return cfg, nil
}
