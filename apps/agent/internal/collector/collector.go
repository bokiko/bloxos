package collector

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
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

// GetGPUStats collects GPU stats via nvidia-smi
func (c *Collector) GetGPUStats() ([]GPUStats, error) {
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

		// Parse each field
		index, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
		name := strings.TrimSpace(parts[1])
		
		gpu := GPUStats{
			Index: index,
			Name:  name,
			BusID: strings.TrimSpace(parts[10]),
		}

		// Parse optional numeric fields
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
	paths := []string{
		"/sys/class/hwmon/hwmon0/temp1_input",
		"/sys/class/hwmon/hwmon1/temp1_input",
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
		// Convert millidegrees to degrees if needed
		if temp > 1000 {
			temp = temp / 1000
		}
		return temp
	}
	return 0
}

// getCPUPower reads CPU power from RAPL (Linux, requires root)
func (c *Collector) getCPUPower() int {
	// This is a simplified version - real implementation would track energy over time
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
