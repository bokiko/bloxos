package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// MinerConfig holds configuration for starting a miner
type MinerConfig struct {
	Name       string            `json:"name"`       // t-rex, lolminer, etc.
	Algorithm  string            `json:"algorithm"`  // ethash, kawpow, etc.
	Pool       string            `json:"pool"`       // stratum+tcp://pool:port
	Wallet     string            `json:"wallet"`     // wallet address
	Worker     string            `json:"worker"`     // worker name
	ExtraArgs  []string          `json:"extraArgs"`  // additional arguments
	Env        map[string]string `json:"env"`        // environment variables
}

// OCConfig holds overclocking configuration
type OCConfig struct {
	GPUIndex    int  `json:"gpuIndex"`    // -1 for all GPUs
	PowerLimit  *int `json:"powerLimit"`  // Watts
	CoreOffset  *int `json:"coreOffset"`  // MHz offset
	MemOffset   *int `json:"memOffset"`   // MHz offset
	CoreLock    *int `json:"coreLock"`    // Lock core MHz
	MemLock     *int `json:"memLock"`     // Lock mem MHz
	FanSpeed    *int `json:"fanSpeed"`    // Percent (0 = auto)
}

// Executor handles command execution on the rig
type Executor struct {
	minerPID    int
	minerName   string
	minerCmd    *exec.Cmd
	minersPath  string
	configPath  string
	debug       bool
}

// New creates a new executor
func New(debug bool) *Executor {
	home, _ := os.UserHomeDir()
	return &Executor{
		minersPath: filepath.Join(home, "miners"),
		configPath: filepath.Join(home, ".bloxos"),
		debug:      debug,
	}
}

// StartMiner starts a miner with the given configuration
func (e *Executor) StartMiner(config *MinerConfig) error {
	// Stop any running miner first
	if e.minerPID > 0 {
		if err := e.StopMiner(); err != nil {
			return fmt.Errorf("failed to stop existing miner: %w", err)
		}
	}

	// Build the command based on miner type
	cmd, err := e.buildMinerCommand(config)
	if err != nil {
		return fmt.Errorf("failed to build miner command: %w", err)
	}

	// Set environment variables
	cmd.Env = os.Environ()
	for k, v := range config.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Start the miner
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start miner: %w", err)
	}

	e.minerPID = cmd.Process.Pid
	e.minerName = config.Name
	e.minerCmd = cmd

	// Save config for restart
	if err := e.saveConfig(config); err != nil {
		// Non-fatal, just log
		if e.debug {
			fmt.Printf("Warning: failed to save config: %v\n", err)
		}
	}

	fmt.Printf("Started %s miner (PID: %d)\n", config.Name, e.minerPID)
	return nil
}

// StopMiner stops the currently running miner
func (e *Executor) StopMiner() error {
	if e.minerPID == 0 {
		// Try to find and kill any known miner processes
		return e.killMinerProcesses()
	}

	// Send SIGTERM first
	process, err := os.FindProcess(e.minerPID)
	if err != nil {
		return fmt.Errorf("failed to find process: %w", err)
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		// Process might already be dead
		if e.debug {
			fmt.Printf("SIGTERM failed: %v, trying SIGKILL\n", err)
		}
	}

	// Wait a bit for graceful shutdown
	done := make(chan error, 1)
	go func() {
		_, err := process.Wait()
		done <- err
	}()

	select {
	case <-done:
		// Process exited
	case <-time.After(5 * time.Second):
		// Force kill
		process.Signal(syscall.SIGKILL)
		<-done
	}

	e.minerPID = 0
	e.minerName = ""
	e.minerCmd = nil

	fmt.Println("Miner stopped")
	return nil
}

// RestartMiner restarts the miner with the saved configuration
func (e *Executor) RestartMiner() error {
	config, err := e.loadConfig()
	if err != nil {
		return fmt.Errorf("no saved config to restart: %w", err)
	}

	if err := e.StopMiner(); err != nil {
		// Continue anyway
		if e.debug {
			fmt.Printf("Warning during stop: %v\n", err)
		}
	}

	time.Sleep(2 * time.Second) // Brief pause before restart

	return e.StartMiner(config)
}

// ApplyOC applies overclocking settings
func (e *Executor) ApplyOC(config *OCConfig) error {
	// Check if nvidia-smi is available
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return fmt.Errorf("nvidia-smi not found: %w", err)
	}

	gpuArg := fmt.Sprintf("%d", config.GPUIndex)
	if config.GPUIndex < 0 {
		gpuArg = "" // Apply to all GPUs
	}

	var errors []string

	// Apply power limit
	if config.PowerLimit != nil {
		args := []string{"-i", gpuArg, "-pl", fmt.Sprintf("%d", *config.PowerLimit)}
		if gpuArg == "" {
			args = []string{"-pl", fmt.Sprintf("%d", *config.PowerLimit)}
		}
		if err := e.runNvidiaSmi(args...); err != nil {
			errors = append(errors, fmt.Sprintf("power limit: %v", err))
		}
	}

	// Apply core offset (requires X server or persistence mode)
	if config.CoreOffset != nil {
		args := []string{"-i", gpuArg, "--gom=COMPUTE", fmt.Sprintf("--lock-gpu-clocks=%d,%d", 
			*config.CoreOffset, *config.CoreOffset)}
		if err := e.runNvidiaSmi(args...); err != nil {
			// Try nvidia-settings instead
			if e.debug {
				fmt.Printf("nvidia-smi core offset failed: %v\n", err)
			}
		}
	}

	// Apply memory offset
	if config.MemOffset != nil {
		// Memory offset typically requires nvidia-settings
		if e.debug {
			fmt.Printf("Memory offset requires nvidia-settings, skipping\n")
		}
	}

	// Apply core lock
	if config.CoreLock != nil {
		args := []string{"--lock-gpu-clocks=" + fmt.Sprintf("%d,%d", *config.CoreLock, *config.CoreLock)}
		if gpuArg != "" {
			args = append([]string{"-i", gpuArg}, args...)
		}
		if err := e.runNvidiaSmi(args...); err != nil {
			errors = append(errors, fmt.Sprintf("core lock: %v", err))
		}
	}

	// Apply memory lock
	if config.MemLock != nil {
		args := []string{"--lock-memory-clocks=" + fmt.Sprintf("%d,%d", *config.MemLock, *config.MemLock)}
		if gpuArg != "" {
			args = append([]string{"-i", gpuArg}, args...)
		}
		if err := e.runNvidiaSmi(args...); err != nil {
			errors = append(errors, fmt.Sprintf("mem lock: %v", err))
		}
	}

	// Apply fan speed (requires nvidia-settings)
	if config.FanSpeed != nil && *config.FanSpeed > 0 {
		// Fan control typically requires nvidia-settings
		if e.debug {
			fmt.Printf("Fan speed control requires nvidia-settings\n")
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("some OC settings failed: %s", strings.Join(errors, "; "))
	}

	return nil
}

// Reboot reboots the system
func (e *Executor) Reboot() error {
	fmt.Println("Rebooting system...")
	cmd := exec.Command("sudo", "reboot")
	return cmd.Run()
}

// Shutdown shuts down the system
func (e *Executor) Shutdown() error {
	fmt.Println("Shutting down system...")
	cmd := exec.Command("sudo", "shutdown", "-h", "now")
	return cmd.Run()
}

// GetMinerStatus returns the current miner status
func (e *Executor) GetMinerStatus() map[string]interface{} {
	status := map[string]interface{}{
		"running": false,
		"name":    "",
		"pid":     0,
	}

	if e.minerPID > 0 {
		// Check if process is still running
		process, err := os.FindProcess(e.minerPID)
		if err == nil {
			err = process.Signal(syscall.Signal(0))
			if err == nil {
				status["running"] = true
				status["name"] = e.minerName
				status["pid"] = e.minerPID
			}
		}
	}

	return status
}

// buildMinerCommand builds the command to start a miner
func (e *Executor) buildMinerCommand(config *MinerConfig) (*exec.Cmd, error) {
	minerPath := e.findMiner(config.Name)
	if minerPath == "" {
		return nil, fmt.Errorf("miner %s not found", config.Name)
	}

	args := []string{}

	switch strings.ToLower(config.Name) {
	case "t-rex", "trex":
		args = append(args, "-a", config.Algorithm)
		args = append(args, "-o", config.Pool)
		args = append(args, "-u", config.Wallet)
		if config.Worker != "" {
			args = append(args, "-w", config.Worker)
		}
		args = append(args, "--api-bind-http", "127.0.0.1:4067")

	case "lolminer":
		args = append(args, "--algo", config.Algorithm)
		args = append(args, "--pool", config.Pool)
		args = append(args, "--user", config.Wallet)
		if config.Worker != "" {
			args = append(args, "--worker", config.Worker)
		}
		args = append(args, "--apiport", "4068")

	case "gminer":
		args = append(args, "--algo", config.Algorithm)
		args = append(args, "--server", config.Pool)
		args = append(args, "--user", config.Wallet)
		if config.Worker != "" {
			args = append(args, "--worker", config.Worker)
		}
		args = append(args, "--api", "4069")

	case "teamredminer", "trm":
		args = append(args, "-a", config.Algorithm)
		args = append(args, "-o", config.Pool)
		args = append(args, "-u", config.Wallet)
		if config.Worker != "" {
			args = append(args, "-w", config.Worker)
		}
		args = append(args, "--api_listen=127.0.0.1:4070")

	case "xmrig":
		args = append(args, "-o", config.Pool)
		args = append(args, "-u", config.Wallet)
		args = append(args, "-a", config.Algorithm)
		args = append(args, "--http-host", "127.0.0.1")
		args = append(args, "--http-port", "4071")

	case "nbminer":
		args = append(args, "-a", config.Algorithm)
		args = append(args, "-o", config.Pool)
		args = append(args, "-u", config.Wallet)
		args = append(args, "--api", "127.0.0.1:4072")

	case "srbminer", "srbminer-multi":
		args = append(args, "--algorithm", config.Algorithm)
		args = append(args, "--pool", config.Pool)
		args = append(args, "--wallet", config.Wallet)
		args = append(args, "--api-enable", "--api-port", "4073")

	default:
		return nil, fmt.Errorf("unsupported miner: %s", config.Name)
	}

	// Add extra arguments
	args = append(args, config.ExtraArgs...)

	cmd := exec.Command(minerPath, args...)
	cmd.Dir = filepath.Dir(minerPath)

	return cmd, nil
}

// findMiner searches for a miner executable
func (e *Executor) findMiner(name string) string {
	name = strings.ToLower(name)

	// Common executable names
	exeNames := map[string][]string{
		"t-rex":          {"t-rex", "trex"},
		"trex":           {"t-rex", "trex"},
		"lolminer":       {"lolMiner", "lolminer"},
		"gminer":         {"miner", "gminer"},
		"teamredminer":   {"teamredminer"},
		"trm":            {"teamredminer"},
		"xmrig":          {"xmrig"},
		"nbminer":        {"nbminer"},
		"srbminer":       {"SRBMiner-MULTI", "srbminer-multi"},
		"srbminer-multi": {"SRBMiner-MULTI", "srbminer-multi"},
	}

	candidates := exeNames[name]
	if candidates == nil {
		candidates = []string{name}
	}

	// Search paths
	searchPaths := []string{
		e.minersPath,
		filepath.Join(e.minersPath, name),
		"/usr/local/bin",
		"/opt/miners",
	}

	for _, dir := range searchPaths {
		for _, exe := range candidates {
			path := filepath.Join(dir, exe)
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
	}

	// Try PATH
	for _, exe := range candidates {
		if path, err := exec.LookPath(exe); err == nil {
			return path
		}
	}

	return ""
}

// killMinerProcesses kills any known miner processes
func (e *Executor) killMinerProcesses() error {
	miners := []string{"t-rex", "lolMiner", "gminer", "teamredminer", "xmrig", "nbminer", "SRBMiner-MULTI"}
	
	for _, miner := range miners {
		exec.Command("pkill", "-9", miner).Run()
	}

	return nil
}

// saveConfig saves the miner config for restart
func (e *Executor) saveConfig(config *MinerConfig) error {
	if err := os.MkdirAll(e.configPath, 0755); err != nil {
		return err
	}

	data, err := json.Marshal(config)
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(e.configPath, "miner.json"), data, 0644)
}

// loadConfig loads the saved miner config
func (e *Executor) loadConfig() (*MinerConfig, error) {
	data, err := os.ReadFile(filepath.Join(e.configPath, "miner.json"))
	if err != nil {
		return nil, err
	}

	var config MinerConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

// runNvidiaSmi runs nvidia-smi with the given arguments
func (e *Executor) runNvidiaSmi(args ...string) error {
	cmd := exec.Command("nvidia-smi", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, string(output))
	}
	if e.debug {
		fmt.Printf("nvidia-smi %v: %s\n", args, string(output))
	}
	return nil
}
