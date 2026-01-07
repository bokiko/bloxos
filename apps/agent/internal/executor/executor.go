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

// ApplyOC applies overclocking settings (NVIDIA or AMD)
func (e *Executor) ApplyOC(config *OCConfig) error {
	// Try NVIDIA first, then AMD
	hasNvidia := false
	hasAMD := false

	if _, err := exec.LookPath("nvidia-smi"); err == nil {
		hasNvidia = true
	}
	if _, err := exec.LookPath("rocm-smi"); err == nil {
		hasAMD = true
	}

	// Check sysfs for AMD GPUs
	if !hasAMD {
		if entries, err := os.ReadDir("/sys/class/drm"); err == nil {
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "card") && !strings.Contains(entry.Name(), "-") {
					vendorPath := fmt.Sprintf("/sys/class/drm/%s/device/vendor", entry.Name())
					if data, err := os.ReadFile(vendorPath); err == nil {
						if strings.TrimSpace(string(data)) == "0x1002" {
							hasAMD = true
							break
						}
					}
				}
			}
		}
	}

	if !hasNvidia && !hasAMD {
		return fmt.Errorf("no supported GPU tools found (nvidia-smi or rocm-smi)")
	}

	var errors []string

	if hasNvidia {
		if err := e.applyNvidiaOC(config); err != nil {
			errors = append(errors, fmt.Sprintf("nvidia: %v", err))
		}
	}

	if hasAMD {
		if err := e.applyAMDOC(config); err != nil {
			errors = append(errors, fmt.Sprintf("amd: %v", err))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("some OC settings failed: %s", strings.Join(errors, "; "))
	}

	return nil
}

// applyNvidiaOC applies overclocking for NVIDIA GPUs
func (e *Executor) applyNvidiaOC(config *OCConfig) error {
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

	// Core/mem offsets require nvidia-settings which needs X server
	if config.CoreOffset != nil || config.MemOffset != nil {
		if e.debug {
			fmt.Println("Core/mem offsets require nvidia-settings (X server)")
		}
	}

	// Fan speed requires nvidia-settings
	if config.FanSpeed != nil && *config.FanSpeed > 0 {
		if e.debug {
			fmt.Println("Fan speed control requires nvidia-settings")
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("%s", strings.Join(errors, "; "))
	}

	return nil
}

// applyAMDOC applies overclocking for AMD GPUs
func (e *Executor) applyAMDOC(config *OCConfig) error {
	var errors []string

	// Determine GPU indices
	gpuIndices := []int{}
	if config.GPUIndex < 0 {
		// Find all AMD GPUs
		entries, _ := os.ReadDir("/sys/class/drm")
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "card") && !strings.Contains(entry.Name(), "-") {
				vendorPath := fmt.Sprintf("/sys/class/drm/%s/device/vendor", entry.Name())
				if data, err := os.ReadFile(vendorPath); err == nil {
					if strings.TrimSpace(string(data)) == "0x1002" {
						idx, _ := strconv.Atoi(strings.TrimPrefix(entry.Name(), "card"))
						gpuIndices = append(gpuIndices, idx)
					}
				}
			}
		}
	} else {
		gpuIndices = []int{config.GPUIndex}
	}

	for _, idx := range gpuIndices {
		cardPath := fmt.Sprintf("/sys/class/drm/card%d/device", idx)

		// Apply power limit via pp_power_profile_mode or power_cap
		if config.PowerLimit != nil {
			hwmonPath := fmt.Sprintf("%s/hwmon", cardPath)
			if entries, err := os.ReadDir(hwmonPath); err == nil && len(entries) > 0 {
				powerCapPath := fmt.Sprintf("%s/%s/power1_cap", hwmonPath, entries[0].Name())
				// Convert watts to microwatts
				power := *config.PowerLimit * 1000000
				if err := os.WriteFile(powerCapPath, []byte(fmt.Sprintf("%d", power)), 0644); err != nil {
					errors = append(errors, fmt.Sprintf("gpu%d power: %v", idx, err))
				} else if e.debug {
					fmt.Printf("Set GPU%d power limit to %dW\n", idx, *config.PowerLimit)
				}
			}
		}

		// Apply core clock via pp_od_clk_voltage
		if config.CoreLock != nil {
			odPath := fmt.Sprintf("%s/pp_od_clk_voltage", cardPath)
			// Write "s 1 <freq>" to set max core clock
			cmd := fmt.Sprintf("s 1 %d", *config.CoreLock)
			if err := os.WriteFile(odPath, []byte(cmd), 0644); err != nil {
				if e.debug {
					fmt.Printf("GPU%d core lock failed: %v\n", idx, err)
				}
			} else {
				// Commit changes
				os.WriteFile(odPath, []byte("c"), 0644)
				if e.debug {
					fmt.Printf("Set GPU%d core clock to %dMHz\n", idx, *config.CoreLock)
				}
			}
		}

		// Apply memory clock via pp_od_clk_voltage
		if config.MemLock != nil {
			odPath := fmt.Sprintf("%s/pp_od_clk_voltage", cardPath)
			// Write "m 1 <freq>" to set max mem clock
			cmd := fmt.Sprintf("m 1 %d", *config.MemLock)
			if err := os.WriteFile(odPath, []byte(cmd), 0644); err != nil {
				if e.debug {
					fmt.Printf("GPU%d mem lock failed: %v\n", idx, err)
				}
			} else {
				os.WriteFile(odPath, []byte("c"), 0644)
				if e.debug {
					fmt.Printf("Set GPU%d memory clock to %dMHz\n", idx, *config.MemLock)
				}
			}
		}

		// Apply fan speed
		if config.FanSpeed != nil {
			hwmonPath := fmt.Sprintf("%s/hwmon", cardPath)
			if entries, err := os.ReadDir(hwmonPath); err == nil && len(entries) > 0 {
				hwmon := fmt.Sprintf("%s/%s", hwmonPath, entries[0].Name())

				if *config.FanSpeed == 0 {
					// Auto fan control
					os.WriteFile(fmt.Sprintf("%s/pwm1_enable", hwmon), []byte("2"), 0644)
				} else {
					// Manual fan control
					os.WriteFile(fmt.Sprintf("%s/pwm1_enable", hwmon), []byte("1"), 0644)
					// Convert percentage to PWM (0-255)
					pwm := (*config.FanSpeed * 255) / 100
					if err := os.WriteFile(fmt.Sprintf("%s/pwm1", hwmon), []byte(fmt.Sprintf("%d", pwm)), 0644); err != nil {
						errors = append(errors, fmt.Sprintf("gpu%d fan: %v", idx, err))
					} else if e.debug {
						fmt.Printf("Set GPU%d fan to %d%%\n", idx, *config.FanSpeed)
					}
				}
			}
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("%s", strings.Join(errors, "; "))
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
