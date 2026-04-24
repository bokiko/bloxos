package main

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

// HardwareInfo is a one-time snapshot of a machine's static hardware details.
// It's sent after agent connect (and only re-sent if detection changes).
type HardwareInfo struct {
	Type      string `json:"type"`
	MachineID string `json:"machine_id"`

	CPUModel        string  `json:"cpu_model,omitempty"`
	CPUVendor       string  `json:"cpu_vendor,omitempty"`
	CPUCores        int     `json:"cpu_cores,omitempty"`        // physical cores
	CPUThreads      int     `json:"cpu_threads,omitempty"`      // logical cores
	CPUFrequencyMHz float64 `json:"cpu_frequency_mhz,omitempty"` // base clock if available

	RAMTotalBytes uint64 `json:"ram_total_bytes,omitempty"`

	KernelVersion  string `json:"kernel_version,omitempty"`
	PlatformFamily string `json:"platform_family,omitempty"`
	Virtualization string `json:"virtualization,omitempty"`
	BootTime       int64  `json:"boot_time,omitempty"`
	Architecture   string `json:"architecture,omitempty"`

	GPUModels []string `json:"gpu_models,omitempty"`

	Disks             []DiskHardwareInfo    `json:"disks,omitempty"`
	NetworkInterfaces []NetworkInterfaceInfo `json:"network_interfaces,omitempty"`
}

type DiskHardwareInfo struct {
	Device    string `json:"device"`              // e.g. /dev/nvme0n1
	Model     string `json:"model,omitempty"`     // from /sys/block/<dev>/device/model
	SizeBytes uint64 `json:"size_bytes,omitempty"`
	Type      string `json:"type,omitempty"` // "nvme", "ssd", "hdd", ""
}

type NetworkInterfaceInfo struct {
	Name      string `json:"name"`
	MAC       string `json:"mac,omitempty"`
	IPv4      string `json:"ipv4,omitempty"`
	SpeedMbps int64  `json:"speed_mbps,omitempty"`
}

// collectHardware gathers a static hardware snapshot. Fails soft — any
// individual collector error is logged-but-dropped so the overall snapshot
// still ships whatever was detectable.
func collectHardware(machineID string, gpus []GPUInfo) HardwareInfo {
	hw := HardwareInfo{
		Type:         "hardware_info",
		MachineID:    machineID,
		Architecture: runtime.GOARCH,
	}

	if cpuInfos, err := cpu.Info(); err == nil && len(cpuInfos) > 0 {
		ci := cpuInfos[0]
		hw.CPUModel = strings.TrimSpace(ci.ModelName)
		hw.CPUVendor = strings.TrimSpace(ci.VendorID)
		hw.CPUFrequencyMHz = ci.Mhz
	}
	if physical, err := cpu.Counts(false); err == nil {
		hw.CPUCores = physical
	}
	if logical, err := cpu.Counts(true); err == nil {
		hw.CPUThreads = logical
	}

	if vm, err := mem.VirtualMemory(); err == nil {
		hw.RAMTotalBytes = vm.Total
	}

	if hi, err := host.Info(); err == nil && hi != nil {
		hw.KernelVersion = hi.KernelVersion
		hw.PlatformFamily = hi.PlatformFamily
		hw.Virtualization = hi.VirtualizationSystem
		if hi.BootTime > 0 {
			hw.BootTime = int64(hi.BootTime)
		}
	}

	hw.Disks = collectDiskHardware()
	hw.NetworkInterfaces = collectNetworkInterfaces()

	seen := make(map[string]struct{}, len(gpus))
	for _, g := range gpus {
		name := strings.TrimSpace(g.Name)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		hw.GPUModels = append(hw.GPUModels, name)
	}

	return hw
}

// collectDiskHardware enumerates top-level block devices under /sys/block.
// Linux-only; returns nil elsewhere.
func collectDiskHardware() []DiskHardwareInfo {
	if runtime.GOOS != "linux" {
		return nil
	}
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return nil
	}

	var disks []DiskHardwareInfo
	for _, entry := range entries {
		name := entry.Name()
		// Skip ram, loop, dm-*, fd, zram — we want only real disks.
		if strings.HasPrefix(name, "ram") ||
			strings.HasPrefix(name, "loop") ||
			strings.HasPrefix(name, "dm-") ||
			strings.HasPrefix(name, "fd") ||
			strings.HasPrefix(name, "zram") ||
			strings.HasPrefix(name, "sr") {
			continue
		}
		d := DiskHardwareInfo{Device: "/dev/" + name}

		if sectorsRaw, err := os.ReadFile(filepath.Join("/sys/block", name, "size")); err == nil {
			if sectors, err := strconv.ParseUint(strings.TrimSpace(string(sectorsRaw)), 10, 64); err == nil {
				// /sys reports size in 512-byte sectors regardless of actual sector size.
				d.SizeBytes = sectors * 512
			}
		}

		if modelRaw, err := os.ReadFile(filepath.Join("/sys/block", name, "device", "model")); err == nil {
			d.Model = strings.TrimSpace(string(modelRaw))
		}

		// Classify type. NVMe is obvious from name; otherwise use rotational flag.
		switch {
		case strings.HasPrefix(name, "nvme"):
			d.Type = "nvme"
		default:
			if rotRaw, err := os.ReadFile(filepath.Join("/sys/block", name, "queue", "rotational")); err == nil {
				switch strings.TrimSpace(string(rotRaw)) {
				case "0":
					d.Type = "ssd"
				case "1":
					d.Type = "hdd"
				}
			}
		}

		// Skip zero-size placeholder devices like cdroms with no media.
		if d.SizeBytes == 0 && d.Model == "" {
			continue
		}
		disks = append(disks, d)
	}

	sort.Slice(disks, func(i, j int) bool {
		return disks[i].Device < disks[j].Device
	})
	return disks
}

// collectNetworkInterfaces lists physical-looking network interfaces.
// Skips loopback, docker/veth bridges, and virtual tunnels.
func collectNetworkInterfaces() []NetworkInterfaceInfo {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var result []NetworkInterfaceInfo
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		name := iface.Name
		if skipInterface(name) {
			continue
		}

		ni := NetworkInterfaceInfo{
			Name: name,
			MAC:  iface.HardwareAddr.String(),
		}

		// Primary IPv4 (first non-link-local).
		addrs, err := iface.Addrs()
		if err == nil {
			for _, addr := range addrs {
				ip, _, err := net.ParseCIDR(addr.String())
				if err != nil {
					continue
				}
				v4 := ip.To4()
				if v4 == nil {
					continue
				}
				if v4.IsLinkLocalUnicast() {
					continue
				}
				ni.IPv4 = v4.String()
				break
			}
		}

		// Link speed from /sys/class/net/<name>/speed (Linux only, may be -1 or absent).
		if runtime.GOOS == "linux" {
			if speedRaw, err := os.ReadFile(filepath.Join("/sys/class/net", name, "speed")); err == nil {
				if speed, err := strconv.ParseInt(strings.TrimSpace(string(speedRaw)), 10, 64); err == nil && speed > 0 {
					ni.SpeedMbps = speed
				}
			}
		}

		result = append(result, ni)
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func skipInterface(name string) bool {
	switch name {
	case "", "lo":
		return true
	}
	prefixes := []string{"docker", "veth", "br-", "virbr", "tun", "tap", "cni", "flannel", "kube", "wg"}
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

