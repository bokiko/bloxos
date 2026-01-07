package collector

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
)

// GPUStats holds stats for a single GPU
type GPUStats struct {
	Index       int     `json:"index"`
	Name        string  `json:"name"`
	Vendor      string  `json:"vendor"` // NVIDIA, AMD, INTEL
	Temperature *int    `json:"temperature"`
	MemTemp     *int    `json:"memTemp"`
	FanSpeed    *int    `json:"fanSpeed"`
	PowerDraw   *int    `json:"powerDraw"`
	CoreClock   *int    `json:"coreClock"`
	MemoryClock *int    `json:"memoryClock"`
	Utilization *int    `json:"utilization"`
	VRAM        int     `json:"vram"`
	BusID       string  `json:"busId"`
}

// CPUStats holds CPU stats
type CPUStats struct {
	Model       string   `json:"model"`
	Vendor      string   `json:"vendor"`
	Cores       int      `json:"cores"`
	Threads     int      `json:"threads"`
	Temperature *int     `json:"temperature"`
	Usage       *float64 `json:"usage"`
	Frequency   *int     `json:"frequency"`
	PowerDraw   *int     `json:"powerDraw"`
}

// SystemInfo holds basic system information
type SystemInfo struct {
	Hostname  string `json:"hostname"`
	OS        string `json:"os"`
	OSVersion string `json:"osVersion"`
	Kernel    string `json:"kernel"`
	Uptime    uint64 `json:"uptime"`
	MemTotal  uint64 `json:"memTotal"`
	MemUsed   uint64 `json:"memUsed"`
}

// Collector collects hardware stats
type Collector struct {
	prevCPUIdle  uint64
	prevCPUTotal uint64
}

// New creates a new collector
func New() *Collector {
	return &Collector{}
}

// GetSystemInfo collects basic system information
func (c *Collector) GetSystemInfo() (*SystemInfo, error) {
	hostname, _ := os.Hostname()

	hostInfo, err := host.Info()
	if err != nil {
		return nil, err
	}

	memInfo, err := mem.VirtualMemory()
	if err != nil {
		return nil, err
	}

	return &SystemInfo{
		Hostname:  hostname,
		OS:        hostInfo.Platform,
		OSVersion: hostInfo.PlatformVersion,
		Kernel:    hostInfo.KernelVersion,
		Uptime:    hostInfo.Uptime,
		MemTotal:  memInfo.Total,
		MemUsed:   memInfo.Used,
	}, nil
}

// GetGPUStats collects GPU stats from all available sources (NVIDIA + AMD)
func (c *Collector) GetGPUStats() ([]GPUStats, error) {
	var allGPUs []GPUStats
	var lastError error

	// Try NVIDIA GPUs
	nvidiaGPUs, err := c.getNvidiaGPUStats()
	if err != nil {
		lastError = err
	} else {
		allGPUs = append(allGPUs, nvidiaGPUs...)
	}

	// Try AMD GPUs
	amdGPUs, err := c.getAMDGPUStats()
	if err != nil {
		if lastError != nil {
			lastError = fmt.Errorf("nvidia: %v, amd: %v", lastError, err)
		} else {
			lastError = err
		}
	} else {
		allGPUs = append(allGPUs, amdGPUs...)
	}

	// If we found any GPUs, return them (even if one vendor failed)
	if len(allGPUs) > 0 {
		// Re-index GPUs sequentially
		for i := range allGPUs {
			allGPUs[i].Index = i
		}
		return allGPUs, nil
	}

	// No GPUs found
	if lastError != nil {
		return nil, lastError
	}
	return nil, fmt.Errorf("no GPUs detected")
}

// getNvidiaGPUStats collects NVIDIA GPU stats via nvidia-smi
func (c *Collector) getNvidiaGPUStats() ([]GPUStats, error) {
	// Check if nvidia-smi exists
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return nil, fmt.Errorf("nvidia-smi not found")
	}

	cmd := exec.Command("nvidia-smi",
		"--query-gpu=index,name,temperature.gpu,temperature.memory,fan.speed,power.draw,clocks.gr,clocks.mem,utilization.gpu,memory.total,pci.bus_id",
		"--format=csv,noheader,nounits")

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("nvidia-smi failed: %w", err)
	}

	var gpus []GPUStats
	scanner := bufio.NewScanner(strings.NewReader(string(output)))

	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, ",")
		if len(parts) < 11 {
			continue
		}

		index, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
		name := strings.TrimSpace(parts[1])

		gpu := GPUStats{
			Index:  index,
			Name:   name,
			Vendor: "NVIDIA",
			BusID:  strings.TrimSpace(parts[10]),
		}

		if temp := parseIntPtr(parts[2]); temp != nil {
			gpu.Temperature = temp
		}
		if memTemp := parseIntPtr(parts[3]); memTemp != nil {
			gpu.MemTemp = memTemp
		}
		if fan := parseIntPtr(parts[4]); fan != nil {
			gpu.FanSpeed = fan
		}
		if power := parseIntPtr(parts[5]); power != nil {
			gpu.PowerDraw = power
		}
		if core := parseIntPtr(parts[6]); core != nil {
			gpu.CoreClock = core
		}
		if mem := parseIntPtr(parts[7]); mem != nil {
			gpu.MemoryClock = mem
		}
		if util := parseIntPtr(parts[8]); util != nil {
			gpu.Utilization = util
		}
		if vram := parseIntPtr(parts[9]); vram != nil {
			gpu.VRAM = *vram
		}

		gpus = append(gpus, gpu)
	}

	return gpus, nil
}

// getAMDGPUStats collects AMD GPU stats via rocm-smi or sysfs
func (c *Collector) getAMDGPUStats() ([]GPUStats, error) {
	// Try rocm-smi first
	gpus, err := c.getAMDGPUStatsFromRocmSmi()
	if err == nil && len(gpus) > 0 {
		return gpus, nil
	}

	// Fallback to sysfs
	return c.getAMDGPUStatsFromSysfs()
}

// getAMDGPUStatsFromRocmSmi uses rocm-smi to get AMD GPU stats
func (c *Collector) getAMDGPUStatsFromRocmSmi() ([]GPUStats, error) {
	// Check if rocm-smi exists
	rocmSmi, err := exec.LookPath("rocm-smi")
	if err != nil {
		return nil, fmt.Errorf("rocm-smi not found")
	}

	var gpus []GPUStats

	// Get GPU list
	cmd := exec.Command(rocmSmi, "--showproductname")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("rocm-smi failed: %w", err)
	}

	// Parse GPU names and count
	lines := strings.Split(string(output), "\n")
	gpuCount := 0
	gpuNames := make(map[int]string)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "GPU[") {
			// Extract GPU index and name
			// Format: GPU[0]		: Card series:		Radeon RX 6800 XT
			parts := strings.SplitN(line, ":", 2)
			if len(parts) >= 2 {
				// Extract index from GPU[X]
				idxStr := strings.TrimPrefix(parts[0], "GPU[")
				idxStr = strings.TrimSuffix(strings.TrimSpace(idxStr), "]")
				idx, _ := strconv.Atoi(idxStr)

				// Get the name part after "Card series:"
				namePart := strings.TrimSpace(parts[1])
				if strings.Contains(namePart, "Card series:") {
					namePart = strings.TrimPrefix(namePart, "Card series:")
					namePart = strings.TrimSpace(namePart)
				}
				gpuNames[idx] = namePart
				if idx >= gpuCount {
					gpuCount = idx + 1
				}
			}
		}
	}

	if gpuCount == 0 {
		return nil, fmt.Errorf("no AMD GPUs detected")
	}

	// Get stats for each GPU
	for i := 0; i < gpuCount; i++ {
		gpu := GPUStats{
			Index:  i,
			Name:   gpuNames[i],
			Vendor: "AMD",
		}

		if gpu.Name == "" {
			gpu.Name = fmt.Sprintf("AMD GPU %d", i)
		}

		// Get temperature
		cmd = exec.Command(rocmSmi, "-d", fmt.Sprintf("%d", i), "--showtemp")
		if output, err := cmd.Output(); err == nil {
			temp := parseRocmSmiValue(string(output), "Temperature")
			if temp > 0 {
				gpu.Temperature = &temp
			}
		}

		// Get fan speed
		cmd = exec.Command(rocmSmi, "-d", fmt.Sprintf("%d", i), "--showfan")
		if output, err := cmd.Output(); err == nil {
			fan := parseRocmSmiValue(string(output), "Fan Speed")
			if fan > 0 {
				gpu.FanSpeed = &fan
			}
		}

		// Get power
		cmd = exec.Command(rocmSmi, "-d", fmt.Sprintf("%d", i), "--showpower")
		if output, err := cmd.Output(); err == nil {
			power := parseRocmSmiValue(string(output), "Average Graphics Package Power")
			if power > 0 {
				gpu.PowerDraw = &power
			}
		}

		// Get clocks
		cmd = exec.Command(rocmSmi, "-d", fmt.Sprintf("%d", i), "--showclocks")
		if output, err := cmd.Output(); err == nil {
			core := parseRocmSmiValue(string(output), "sclk")
			if core > 0 {
				gpu.CoreClock = &core
			}
			mem := parseRocmSmiValue(string(output), "mclk")
			if mem > 0 {
				gpu.MemoryClock = &mem
			}
		}

		// Get VRAM
		cmd = exec.Command(rocmSmi, "-d", fmt.Sprintf("%d", i), "--showmeminfo", "vram")
		if output, err := cmd.Output(); err == nil {
			vram := parseRocmSmiValue(string(output), "Total Memory")
			if vram > 0 {
				gpu.VRAM = vram
			}
		}

		// Get utilization
		cmd = exec.Command(rocmSmi, "-d", fmt.Sprintf("%d", i), "--showuse")
		if output, err := cmd.Output(); err == nil {
			util := parseRocmSmiValue(string(output), "GPU use")
			if util >= 0 {
				gpu.Utilization = &util
			}
		}

		// Get PCI bus ID
		cmd = exec.Command(rocmSmi, "-d", fmt.Sprintf("%d", i), "--showbus")
		if output, err := cmd.Output(); err == nil {
			lines := strings.Split(string(output), "\n")
			for _, line := range lines {
				if strings.Contains(line, "PCI Bus") {
					parts := strings.Split(line, ":")
					if len(parts) >= 2 {
						gpu.BusID = strings.TrimSpace(parts[len(parts)-1])
					}
				}
			}
		}

		gpus = append(gpus, gpu)
	}

	return gpus, nil
}

// getAMDGPUStatsFromSysfs reads AMD GPU stats from /sys/class/drm
func (c *Collector) getAMDGPUStatsFromSysfs() ([]GPUStats, error) {
	var gpus []GPUStats

	// Find AMD GPU devices
	drmPath := "/sys/class/drm"
	entries, err := os.ReadDir(drmPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", drmPath, err)
	}

	gpuIndex := 0
	for _, entry := range entries {
		name := entry.Name()
		// Look for card0, card1, etc.
		if !strings.HasPrefix(name, "card") || strings.Contains(name, "-") {
			continue
		}

		cardPath := filepath.Join(drmPath, name, "device")

		// Check if it's an AMD GPU by looking for vendor ID
		vendorPath := filepath.Join(cardPath, "vendor")
		vendorData, err := os.ReadFile(vendorPath)
		if err != nil {
			continue
		}
		vendor := strings.TrimSpace(string(vendorData))
		if vendor != "0x1002" { // AMD vendor ID
			continue
		}

		gpu := GPUStats{
			Index:  gpuIndex,
			Name:   "AMD GPU",
			Vendor: "AMD",
		}

		// Try to get the product name
		productPath := filepath.Join(cardPath, "product_name")
		if data, err := os.ReadFile(productPath); err == nil {
			gpu.Name = strings.TrimSpace(string(data))
		}

		// Get temperature from hwmon
		hwmonPath := filepath.Join(cardPath, "hwmon")
		if hwmonEntries, err := os.ReadDir(hwmonPath); err == nil && len(hwmonEntries) > 0 {
			hwmon := filepath.Join(hwmonPath, hwmonEntries[0].Name())

			// Temperature (temp1_input is edge temp, temp2 is junction, temp3 is mem)
			if data, err := os.ReadFile(filepath.Join(hwmon, "temp1_input")); err == nil {
				if temp, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
					t := temp / 1000 // Convert millidegrees
					gpu.Temperature = &t
				}
			}

			// Memory temperature
			if data, err := os.ReadFile(filepath.Join(hwmon, "temp3_input")); err == nil {
				if temp, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
					t := temp / 1000
					gpu.MemTemp = &t
				}
			}

			// Fan speed (PWM to percentage)
			if data, err := os.ReadFile(filepath.Join(hwmon, "pwm1")); err == nil {
				if pwm, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
					fan := (pwm * 100) / 255
					gpu.FanSpeed = &fan
				}
			}

			// Power (power1_average in microwatts)
			if data, err := os.ReadFile(filepath.Join(hwmon, "power1_average")); err == nil {
				if power, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
					p := power / 1000000 // Convert to watts
					gpu.PowerDraw = &p
				}
			}
		}

		// Get clocks from pp_dpm_sclk and pp_dpm_mclk
		if data, err := os.ReadFile(filepath.Join(cardPath, "pp_dpm_sclk")); err == nil {
			// Format: "0: 500Mhz\n1: 800Mhz *\n" (* marks active)
			lines := strings.Split(string(data), "\n")
			for _, line := range lines {
				if strings.Contains(line, "*") {
					parts := strings.Fields(line)
					for _, part := range parts {
						if strings.HasSuffix(part, "Mhz") {
							if val, err := strconv.Atoi(strings.TrimSuffix(part, "Mhz")); err == nil {
								gpu.CoreClock = &val
							}
						}
					}
				}
			}
		}

		if data, err := os.ReadFile(filepath.Join(cardPath, "pp_dpm_mclk")); err == nil {
			lines := strings.Split(string(data), "\n")
			for _, line := range lines {
				if strings.Contains(line, "*") {
					parts := strings.Fields(line)
					for _, part := range parts {
						if strings.HasSuffix(part, "Mhz") {
							if val, err := strconv.Atoi(strings.TrimSuffix(part, "Mhz")); err == nil {
								gpu.MemoryClock = &val
							}
						}
					}
				}
			}
		}

		// Get VRAM from mem_info_vram_total
		if data, err := os.ReadFile(filepath.Join(cardPath, "mem_info_vram_total")); err == nil {
			if vram, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); err == nil {
				gpu.VRAM = int(vram / 1024 / 1024) // Convert to MB
			}
		}

		// Get GPU utilization from gpu_busy_percent
		if data, err := os.ReadFile(filepath.Join(cardPath, "gpu_busy_percent")); err == nil {
			if util, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				gpu.Utilization = &util
			}
		}

		// Get PCI bus ID
		if data, err := os.ReadFile(filepath.Join(cardPath, "uevent")); err == nil {
			lines := strings.Split(string(data), "\n")
			for _, line := range lines {
				if strings.HasPrefix(line, "PCI_SLOT_NAME=") {
					gpu.BusID = strings.TrimPrefix(line, "PCI_SLOT_NAME=")
				}
			}
		}

		gpus = append(gpus, gpu)
		gpuIndex++
	}

	if len(gpus) == 0 {
		return nil, fmt.Errorf("no AMD GPUs found in sysfs")
	}

	return gpus, nil
}

// parseRocmSmiValue extracts a numeric value from rocm-smi output
func parseRocmSmiValue(output, key string) int {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, key) {
			// Extract numeric value
			parts := strings.Fields(line)
			for _, part := range parts {
				// Remove common units
				part = strings.TrimSuffix(part, "%")
				part = strings.TrimSuffix(part, "W")
				part = strings.TrimSuffix(part, "Mhz")
				part = strings.TrimSuffix(part, "MHz")
				part = strings.TrimSuffix(part, "c")
				part = strings.TrimSuffix(part, "C")
				part = strings.TrimSuffix(part, "MB")
				part = strings.TrimSuffix(part, "GB")

				if val, err := strconv.ParseFloat(part, 64); err == nil && val > 0 {
					return int(val)
				}
			}
		}
	}
	return 0
}

// GetCPUStats collects CPU stats
func (c *Collector) GetCPUStats() (*CPUStats, error) {
	cpuInfo, err := cpu.Info()
	if err != nil || len(cpuInfo) == 0 {
		return nil, fmt.Errorf("failed to get CPU info: %w", err)
	}

	cores, _ := cpu.Counts(false) // Physical cores
	threads, _ := cpu.Counts(true) // Logical threads

	stats := &CPUStats{
		Model:   cpuInfo[0].ModelName,
		Vendor:  cpuInfo[0].VendorID,
		Cores:   cores,
		Threads: threads,
	}

	// Get CPU usage
	usage, err := cpu.Percent(0, false)
	if err == nil && len(usage) > 0 {
		stats.Usage = &usage[0]
	}

	// Get CPU frequency (average of all cores)
	freqs, err := cpu.Info()
	if err == nil && len(freqs) > 0 {
		freq := int(freqs[0].Mhz)
		stats.Frequency = &freq
	}

	// Get CPU temperature (Linux specific)
	temp := c.getCPUTemperature()
	if temp > 0 {
		stats.Temperature = &temp
	}

	// Get CPU power (Linux RAPL)
	power := c.getCPUPower()
	if power > 0 {
		stats.PowerDraw = &power
	}

	return stats, nil
}

// getCPUTemperature reads CPU temp from hwmon (Linux)
func (c *Collector) getCPUTemperature() int {
	// Try k10temp for AMD, coretemp for Intel
	hwmonPath := "/sys/class/hwmon"
	entries, err := os.ReadDir(hwmonPath)
	if err != nil {
		return 0
	}

	for _, entry := range entries {
		namePath := filepath.Join(hwmonPath, entry.Name(), "name")
		nameData, err := os.ReadFile(namePath)
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(nameData))

		// Look for CPU temperature sensors
		if name == "k10temp" || name == "coretemp" || name == "zenpower" {
			tempPath := filepath.Join(hwmonPath, entry.Name(), "temp1_input")
			tempData, err := os.ReadFile(tempPath)
			if err != nil {
				continue
			}
			temp, err := strconv.Atoi(strings.TrimSpace(string(tempData)))
			if err != nil {
				continue
			}
			return temp / 1000 // Convert millidegrees
		}
	}

	// Fallback paths
	paths := []string{
		"/sys/class/thermal/thermal_zone0/temp",
	}

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		temp, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			continue
		}
		if temp > 1000 {
			temp = temp / 1000
		}
		return temp
	}

	return 0
}

// getCPUPower reads CPU power from RAPL (Linux, requires root)
func (c *Collector) getCPUPower() int {
	// RAPL power reading would require tracking energy over time
	// For now, return 0
	return 0
}

// parseIntPtr parses a string to int pointer, returns nil for N/A or invalid
func parseIntPtr(s string) *int {
	s = strings.TrimSpace(s)
	if s == "" || s == "[N/A]" || s == "N/A" {
		return nil
	}
	// Remove any non-numeric suffix
	s = strings.Split(s, " ")[0]
	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	i := int(val)
	return &i
}
