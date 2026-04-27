//go:build linux

package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

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
		hw.CPUStepping = strconv.Itoa(int(ci.Stepping))

		family, modelNum := parseCPUFamilyAndModel(hw.CPUModel, hw.CPUVendor)
		hw.CPUFamily = family
		hw.CPUModelNum = modelNum

		seenSockets := make(map[string]struct{})
		for _, c := range cpuInfos {
			seenSockets[c.PhysicalID] = struct{}{}
		}
		if len(seenSockets) > 0 {
			hw.CPUSockets = len(seenSockets)
		}

		if len(ci.Flags) > 0 {
			hw.CPUFlags = filterInterestingCPUFlags(ci.Flags)
		}
	}
	if physical, err := cpu.Counts(false); err == nil {
		hw.CPUCores = physical
	}
	if logical, err := cpu.Counts(true); err == nil {
		hw.CPUThreads = logical
	}

	hw.CPUCacheL1KB, hw.CPUCacheL2KB, hw.CPUCacheL3KB = collectCPUCacheSizes()

	if vm, err := mem.VirtualMemory(); err == nil {
		hw.RAMTotalBytes = vm.Total
	}

	if mods, slots, used, max := collectRAMModules(); mods != nil {
		hw.RAMModules = mods
		hw.RAMSlots = slots
		hw.RAMSlotsUsed = used
		hw.RAMMaxBytes = max
	}

	if hi, err := host.Info(); err == nil && hi != nil {
		hw.KernelVersion = hi.KernelVersion
		hw.PlatformFamily = hi.PlatformFamily
		hw.Virtualization = hi.VirtualizationSystem
		if hi.BootTime > 0 {
			hw.BootTime = int64(hi.BootTime)
		}
	}

	collectDMIInfo(&hw)

	hw.Disks = collectDiskHardware()
	hw.NetworkInterfaces = collectNetworkInterfaces()

	// Legacy GPU models list — kept for backwards compat
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

	hw.GPUDevices = collectGPUDevices()
	hw.PCIDevices = collectPCIDevices()

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
				d.SizeBytes = sectors * 512
			}
		}

		if modelRaw, err := os.ReadFile(filepath.Join("/sys/block", name, "device", "model")); err == nil {
			d.Model = strings.TrimSpace(string(modelRaw))
		}

		if serialRaw, err := os.ReadFile(filepath.Join("/sys/block", name, "device", "serial")); err == nil {
			d.Serial = strings.TrimSpace(string(serialRaw))
		}

		if fwRaw, err := os.ReadFile(filepath.Join("/sys/block", name, "device", "firmware_rev")); err == nil {
			d.Firmware = strings.TrimSpace(string(fwRaw))
		}

		switch {
		case strings.HasPrefix(name, "nvme"):
			d.Type = "nvme"
			d.Interface = "nvme"
		default:
			if rotRaw, err := os.ReadFile(filepath.Join("/sys/block", name, "queue", "rotational")); err == nil {
				switch strings.TrimSpace(string(rotRaw)) {
				case "0":
					d.Type = "ssd"
				case "1":
					d.Type = "hdd"
				}
			}
			if transportRaw, err := os.ReadFile(filepath.Join("/sys/block", name, "device", "transport")); err == nil {
				transport := strings.TrimSpace(string(transportRaw))
				switch transport {
				case "sas":
					d.Interface = "sas"
				case "iscsi":
					d.Interface = "iscsi"
				default:
					d.Interface = "sata"
				}
			} else {
				d.Interface = "sata"
			}
		}

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

/* ============================================================================
 * Phase 6 Unit A — expanded hardware collectors
 * ============================================================================ */

var (
	reCPUNoise        = regexp.MustCompile(`\(R\)|\(TM\)|CPU|Processor|@.*$|[0-9]+-Core`)
	reSpaces          = regexp.MustCompile(`\s+`)
	reRyzenThreadrip  = regexp.MustCompile(`Ryzen\s+(?:Threadripper\s+)?([A-Z0-9 ]+?)\s+([0-9A-Z]+)`)
	reRyzenSimple     = regexp.MustCompile(`Ryzen\s+(\d+)\s+([0-9A-Z]+)`)
	reXeonNamed       = regexp.MustCompile(`Xeon\s+([A-Za-z]+)\s+([0-9A-Z-]+)`)
	reXeonNumeric     = regexp.MustCompile(`Xeon\s+([A-Z]?\d{4,}\w*)`)
	reCorePattern     = regexp.MustCompile(`Core\s+(i[3579]|m[3579]|Ultra\s+\d+)\s*-?\s*([0-9A-Z]+)`)
	reEPYC            = regexp.MustCompile(`EPYC\s+([0-9A-Z-]+)`)
	reAtom            = regexp.MustCompile(`Atom\s+([A-Z]?\d+\w*)`)
	reAppleSilicon    = regexp.MustCompile(`(M\d+(?:\s+\w+)?)`)
)

// parseCPUFamilyAndModel extracts a human-readable family and model number
// from the raw CPU model string.
func parseCPUFamilyAndModel(model, vendor string) (family, modelNum string) {
	clean := reCPUNoise.ReplaceAllString(model, "")
	clean = strings.TrimSpace(reSpaces.ReplaceAllString(clean, " "))

	switch {
	case strings.Contains(clean, "Ryzen"):
		if m := reRyzenThreadrip.FindStringSubmatch(clean); len(m) == 3 {
			family = "Ryzen " + strings.TrimSpace(m[1])
			modelNum = strings.TrimSpace(m[2])
		} else if m := reRyzenSimple.FindStringSubmatch(clean); len(m) == 3 {
			family = "Ryzen " + m[1]
			modelNum = m[2]
		}
	case strings.Contains(clean, "Xeon"):
		if m := reXeonNamed.FindStringSubmatch(clean); len(m) == 3 {
			family = "Xeon " + m[1]
			modelNum = m[2]
		} else if m := reXeonNumeric.FindStringSubmatch(clean); len(m) == 2 {
			family = "Xeon"
			modelNum = m[1]
		}
	case strings.Contains(clean, "Core"):
		if m := reCorePattern.FindStringSubmatch(clean); len(m) == 3 {
			family = "Core " + m[1]
			modelNum = m[2]
		}
	case strings.Contains(clean, "EPYC"):
		if m := reEPYC.FindStringSubmatch(clean); len(m) == 2 {
			family = "EPYC"
			modelNum = m[1]
		}
	case strings.Contains(clean, "Atom"):
		if m := reAtom.FindStringSubmatch(clean); len(m) == 2 {
			family = "Atom"
			modelNum = m[1]
		}
	case strings.Contains(strings.ToLower(vendor), "apple"):
		if m := reAppleSilicon.FindStringSubmatch(clean); len(m) == 2 {
			family = "Apple Silicon"
			modelNum = m[1]
		}
	}
	return family, modelNum
}

// filterInterestingCPUFlags keeps only the flags useful for capability filtering.
func filterInterestingCPUFlags(allFlags []string) []string {
	keep := map[string]bool{
		"sse4_1": true, "sse4_2": true,
		"avx": true, "avx2": true,
		"avx512f": true, "avx512bw": true, "avx512cd": true,
		"avx512dq": true, "avx512vl": true, "avx512_bf16": true,
		"avx512_vnni": true, "avx512_fp16": true,
		"sse4a": true, "fma4": true, "xop": true,
		"aes": true, "sha_ni": true, "rdrand": true, "rdseed": true,
		"vmx": true, "svm": true, "ept": true, "vpid": true,
		"tsx_ldtrk": true, "amx_bf16": true, "amx_tile": true, "amx_int8": true,
		"hypervisor": true,
	}
	var out []string
	for _, f := range allFlags {
		if keep[f] {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// collectCPUCacheSizes reads /sys/devices/system/cpu/cpu0/cache/index*/size
// to determine L1/L2/L3 cache sizes in KB. Returns 0 for caches it can't find.
func collectCPUCacheSizes() (l1, l2, l3 int) {
	if runtime.GOOS != "linux" {
		return 0, 0, 0
	}
	base := "/sys/devices/system/cpu/cpu0/cache"
	for i := 0; i < 8; i++ {
		idxDir := filepath.Join(base, fmt.Sprintf("index%d", i))
		levelRaw, err := os.ReadFile(filepath.Join(idxDir, "level"))
		if err != nil {
			continue
		}
		typeRaw, _ := os.ReadFile(filepath.Join(idxDir, "type"))
		sizeRaw, _ := os.ReadFile(filepath.Join(idxDir, "size"))

		level := strings.TrimSpace(string(levelRaw))
		typ := strings.TrimSpace(string(typeRaw))
		size := strings.TrimSpace(string(sizeRaw))

		kb := parseCacheSize(size)
		if kb == 0 {
			continue
		}

		switch level {
		case "1":
			if typ == "Data" || typ == "Unified" {
				l1 = kb
			}
		case "2":
			l2 = kb
		case "3":
			l3 = kb
		}
	}
	return l1, l2, l3
}

func parseCacheSize(s string) int {
	if s == "" {
		return 0
	}
	mult := 1
	last := s[len(s)-1]
	switch last {
	case 'K':
		mult = 1
		s = s[:len(s)-1]
	case 'M':
		mult = 1024
		s = s[:len(s)-1]
	case 'G':
		mult = 1024 * 1024
		s = s[:len(s)-1]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n * mult
}

// collectDMIInfo reads /sys/class/dmi/id/* for system, board, BIOS, chassis info.
// All optional — fields stay empty if files are unreadable.
func collectDMIInfo(hw *HardwareInfo) {
	if runtime.GOOS != "linux" {
		return
	}
	read := func(name string) string {
		data, err := os.ReadFile("/sys/class/dmi/id/" + name)
		if err != nil {
			return ""
		}
		val := strings.TrimSpace(string(data))
		switch strings.ToLower(val) {
		case "default string", "to be filled by o.e.m.", "system manufacturer",
			"system product name", "system serial number", "not specified",
			"not applicable", "none", "unknown":
			return ""
		}
		return val
	}
	hw.SystemVendor = read("sys_vendor")
	hw.SystemProduct = read("product_name")
	hw.SystemSerial = read("product_serial")
	hw.SystemUUID = read("product_uuid")
	hw.BoardVendor = read("board_vendor")
	hw.BoardProduct = read("board_name")
	hw.BoardVersion = read("board_version")
	hw.BoardSerial = read("board_serial")
	hw.BIOSVendor = read("bios_vendor")
	hw.BIOSVersion = read("bios_version")
	hw.BIOSReleaseDate = read("bios_date")
	hw.ChassisVendor = read("chassis_vendor")
	if rawType := read("chassis_type"); rawType != "" {
		hw.ChassisType = parseChassisType(rawType)
	}
}

// parseChassisType maps the DMI chassis type number to a human-readable string.
func parseChassisType(raw string) string {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return raw
	}
	chassisTypes := map[int]string{
		1: "other", 2: "unknown", 3: "desktop", 4: "low profile desktop",
		5: "pizza box", 6: "mini tower", 7: "tower", 8: "portable",
		9: "laptop", 10: "notebook", 11: "handheld", 12: "docking station",
		13: "all-in-one", 14: "sub-notebook", 15: "space-saving", 16: "lunchbox",
		17: "main server chassis", 18: "expansion chassis", 19: "subchassis",
		20: "bus expansion chassis", 21: "peripheral chassis", 22: "raid chassis",
		23: "rack mount chassis", 24: "sealed-case pc", 25: "multi-system chassis",
		26: "compact pci", 27: "advanced tca", 28: "blade", 29: "blade enclosure",
		30: "tablet", 31: "convertible", 32: "detachable", 33: "iot gateway",
		34: "embedded pc", 35: "mini pc", 36: "stick pc",
	}
	if name, ok := chassisTypes[n]; ok {
		return name
	}
	return raw
}

// collectRAMModules tries `dmidecode --type 17` to enumerate populated DIMMs.
// Returns nil if dmidecode is unavailable or fails.
func collectRAMModules() (modules []RAMModule, totalSlots, populatedSlots int, maxBytes uint64) {
	if runtime.GOOS != "linux" {
		return nil, 0, 0, 0
	}
	out, err := exec.Command("dmidecode", "--type", "17").Output()
	if err != nil {
		out, err = exec.Command("sudo", "-n", "dmidecode", "--type", "17").Output()
		if err != nil {
			return nil, 0, 0, 0
		}
	}

	if maxOut, err := exec.Command("dmidecode", "--type", "16").Output(); err == nil {
		maxBytes = parseDmiType16MaxCapacity(string(maxOut))
	}

	modules, totalSlots, populatedSlots = parseDmiType17(string(out))
	return modules, totalSlots, populatedSlots, maxBytes
}

func parseDmiType17(text string) (modules []RAMModule, totalSlots, populated int) {
	blocks := strings.Split(text, "\nMemory Device\n")

	for i, block := range blocks {
		if i == 0 && !strings.HasPrefix(strings.TrimSpace(block), "Memory Device") {
			continue
		}
		totalSlots++

		mod := RAMModule{}
		hasSize := false

		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])

			switch key {
			case "Locator":
				mod.Slot = val
			case "Size":
				if val == "No Module Installed" || val == "Not Installed" {
					hasSize = false
				} else {
					if bytes := parseDmiSize(val); bytes > 0 {
						mod.SizeBytes = bytes
						hasSize = true
					}
				}
			case "Speed":
				mod.MaxSpeedMTs = parseDmiSpeed(val)
			case "Configured Memory Speed", "Configured Clock Speed":
				mod.SpeedMTs = parseDmiSpeed(val)
			case "Type":
				if val != "Unknown" && val != "Other" {
					mod.Type = val
				}
			case "Manufacturer":
				if val != "Unknown" && val != "Not Specified" {
					mod.Manufacturer = val
				}
			case "Part Number":
				if val != "Unknown" && val != "Not Specified" {
					mod.PartNumber = val
				}
			case "Rank":
				if val != "Unknown" {
					mod.Rank = val
				}
			case "Form Factor":
				if val != "Unknown" {
					mod.FormFactor = val
				}
			}
		}

		if hasSize {
			populated++
			modules = append(modules, mod)
		}
	}
	return modules, totalSlots, populated
}

func parseDmiSize(s string) uint64 {
	parts := strings.Fields(s)
	if len(parts) < 2 {
		return 0
	}
	n, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0
	}
	switch strings.ToUpper(parts[1]) {
	case "TB", "TIB":
		return n * 1024 * 1024 * 1024 * 1024
	case "GB", "GIB":
		return n * 1024 * 1024 * 1024
	case "MB", "MIB":
		return n * 1024 * 1024
	case "KB", "KIB":
		return n * 1024
	}
	return 0
}

func parseDmiSpeed(s string) int {
	parts := strings.Fields(s)
	if len(parts) < 1 {
		return 0
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	return n
}

func parseDmiType16MaxCapacity(text string) uint64 {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Maximum Capacity:") {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(line, "Maximum Capacity:"))
		return parseDmiSize(val)
	}
	return 0
}

// collectGPUDevices uses `lspci` to find GPU devices and their PCI details.
func collectGPUDevices() []GPUDevice {
	if runtime.GOOS != "linux" {
		return nil
	}
	out, err := exec.Command("lspci", "-mm").Output()
	if err != nil {
		return nil
	}

	var gpus []GPUDevice
	for _, line := range strings.Split(string(out), "\n") {
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "vga compatible") &&
			!strings.Contains(lower, "3d controller") &&
			!strings.Contains(lower, "display controller") {
			continue
		}
		fields := parseLspciMmLine(line)
		if len(fields) < 4 {
			continue
		}
		gpu := GPUDevice{
			PCISlot: fields[0],
			Vendor:  normalizeGPUVendor(fields[2]),
			Model:   simplifyGPUModel(fields[3]),
		}
		if slot := normalizePCISlot(gpu.PCISlot); slot != "" {
			if link, err := os.Readlink("/sys/bus/pci/devices/" + slot + "/driver"); err == nil {
				gpu.Driver = filepath.Base(link)
			}
		}
		gpus = append(gpus, gpu)
	}
	return gpus
}

func normalizeGPUVendor(v string) string {
	lower := strings.ToLower(v)
	switch {
	case strings.Contains(lower, "nvidia"):
		return "NVIDIA"
	case strings.Contains(lower, "advanced micro devices"), strings.Contains(lower, "amd"), strings.Contains(lower, "ati"):
		return "AMD"
	case strings.Contains(lower, "intel"):
		return "Intel"
	}
	return v
}

func simplifyGPUModel(m string) string {
	if idx := strings.Index(m, "["); idx >= 0 {
		end := strings.Index(m, "]")
		if end > idx {
			return strings.TrimSpace(m[idx+1 : end])
		}
	}
	return m
}

func normalizePCISlot(slot string) string {
	if strings.Count(slot, ":") == 1 {
		return "0000:" + slot
	}
	return slot
}

// collectPCIDevices returns a full list of PCI devices for the inventory.
func collectPCIDevices() []PCIDevice {
	if runtime.GOOS != "linux" {
		return nil
	}
	out, err := exec.Command("lspci", "-mm").Output()
	if err != nil {
		return nil
	}
	var devs []PCIDevice
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := parseLspciMmLine(line)
		if len(f) < 4 {
			continue
		}
		dev := PCIDevice{
			Slot:   f[0],
			Class:  f[1],
			Vendor: f[2],
			Device: f[3],
		}
		if len(f) >= 6 {
			dev.SubVendor = f[4]
			dev.SubDevice = f[5]
		}
		devs = append(devs, dev)
	}
	return devs
}

// parseLspciMmLine parses a single line of `lspci -mm` output.
func parseLspciMmLine(line string) []string {
	var fields []string
	var cur strings.Builder
	inQuote := false

	firstSpace := strings.Index(line, " ")
	if firstSpace > 0 {
		fields = append(fields, line[:firstSpace])
		line = line[firstSpace+1:]
	}

	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == '\\' && i+1 < len(line) {
			cur.WriteByte(line[i+1])
			i++
			continue
		}
		if c == '"' {
			if inQuote {
				fields = append(fields, cur.String())
				cur.Reset()
			}
			inQuote = !inQuote
			continue
		}
		if inQuote {
			cur.WriteByte(c)
		}
	}
	return fields
}

// collectCPUTempC reads /sys/class/thermal looking for the CPU package
// temperature. Prefers zones whose `type` contains "pkg_temp" or
// "coretemp"; falls back to the first non-pkg zone. Returns 0 if no
// usable reading is found.
//
// PHASE12-NOTE: filters out implausible readings (<0 or >120°C) which
// occasionally show up on flaky firmware/sensors.
func collectCPUTempC() float64 {
	entries, err := os.ReadDir("/sys/class/thermal")
	if err != nil {
		return 0
	}
	var preferred, fallback float64
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "thermal_zone") {
			continue
		}
		tb, err := os.ReadFile(filepath.Join("/sys/class/thermal", name, "type"))
		if err != nil {
			continue
		}
		zoneType := strings.ToLower(strings.TrimSpace(string(tb)))
		rb, err := os.ReadFile(filepath.Join("/sys/class/thermal", name, "temp"))
		if err != nil {
			continue
		}
		raw, err := strconv.ParseFloat(strings.TrimSpace(string(rb)), 64)
		if err != nil {
			continue
		}
		c := raw / 1000.0
		if c < 0 || c > 120 {
			continue
		}
		if strings.Contains(zoneType, "pkg_temp") || strings.Contains(zoneType, "coretemp") {
			if c > preferred {
				preferred = c
			}
		} else if fallback == 0 {
			fallback = c
		}
	}
	if preferred > 0 {
		return preferred
	}
	return fallback
}
