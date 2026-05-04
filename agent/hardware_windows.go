//go:build windows

package main

import (
	"fmt"
	"log"
	"net"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/StackExchange/wmi"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

/* ============================================================================
 * Phase 9 — Windows hardware collectors
 *
 * Mirrors agent/hardware.go's Linux collectors using WMI / Win32_*.
 * We deliberately duplicate parseChassisType / parseCPUFamilyAndModel here
 * rather than splitting them into a shared file — build tags keep both
 * compilation units independent and the duplication is self-documenting.
 *
 * PCI device enumeration via Win32_PnPEntity is intentionally skipped for
 * now (heavy + noisy; defer to a follow-up phase). Virtualization detection
 * is also skipped — gopsutil's host.Info().VirtualizationSystem reports
 * something usable on Linux but is unreliable on Windows.
 * ============================================================================ */

// safeWmiQuery wraps wmi.Query with panic recovery. The StackExchange/wmi
// library v1.2.1 panics via reflect.Value.Uint() when a WMI VARIANT type
// can't be converted to the destination Go uint kind. Without this wrapper,
// a single WMI provider drift kills the agent before the Phase 8
// auto-update path can fire — leaving boxes permanently stuck on broken
// binaries. Returns the panic as an error so per-query failures degrade
// to "this provider's data missing" instead of "agent dead".
func safeWmiQuery(query string, dst interface{}) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("wmi panic recovered: %v", r)
		}
	}()
	return wmi.Query(query, dst)
}

// coerceUint32 extracts a uint32 from a WMI VARIANT-typed field declared
// as interface{}. COM marshalling produces int32 on some Windows builds
// where the MOF schema declared uint16; this normalizes them all.
// Returns 0 for nil or unrecognized types — caller should treat 0 as "unknown".
func coerceUint32(v interface{}) uint32 {
	switch x := v.(type) {
	case uint32:
		return x
	case uint16:
		return uint32(x)
	case uint64:
		return uint32(x)
	case int16:
		if x < 0 {
			return 0
		}
		return uint32(x)
	case int32:
		if x < 0 {
			return 0
		}
		return uint32(x)
	case int64:
		if x < 0 {
			return 0
		}
		return uint32(x)
	case int:
		if x < 0 {
			return 0
		}
		return uint32(x)
	}
	return 0
}

// coerceFirstUint32 extracts the first element of an interface{}-typed
// WMI array (e.g. ChassisTypes — declared []uint16 in MOF, often returned
// as []int32 by the COM provider). Returns 0 if empty or unrecognized.
func coerceFirstUint32(v interface{}) uint32 {
	switch x := v.(type) {
	case []uint16:
		if len(x) > 0 {
			return uint32(x[0])
		}
	case []int32:
		if len(x) > 0 && x[0] >= 0 {
			return uint32(x[0])
		}
	case []uint32:
		if len(x) > 0 {
			return x[0]
		}
	case []int64:
		if len(x) > 0 && x[0] >= 0 {
			return uint32(x[0])
		}
	case []int:
		if len(x) > 0 && x[0] >= 0 {
			return uint32(x[0])
		}
	}
	return 0
}

// WMI struct types — fields must be PascalCase to match WMI property names.

type win32Processor struct {
	Name                      string
	Manufacturer              string
	NumberOfCores             uint32
	NumberOfLogicalProcessors uint32
	MaxClockSpeed             uint32
	L2CacheSize               uint32
	L3CacheSize               uint32
	SocketDesignation         string
}

type win32ComputerSystem struct {
	Manufacturer string
	Model        string
}

type win32BIOS struct {
	Manufacturer string
	Version      string
	SMBIOSBIOSVersion string
	ReleaseDate  string
	SerialNumber string
}

type win32BaseBoard struct {
	Manufacturer string
	Product      string
	Version      string
	SerialNumber string
}

type win32SystemEnclosure struct {
	Manufacturer string
	ChassisTypes interface{}
	SerialNumber string
}

type win32PhysicalMemory struct {
	BankLabel        string
	DeviceLocator    string
	Capacity         uint64
	Speed            uint32
	ConfiguredClockSpeed uint32
	SMBIOSMemoryType interface{}
	MemoryType       interface{}
	FormFactor       interface{}
	Manufacturer     string
	PartNumber       string
}

type win32DiskDrive struct {
	DeviceID      string
	Model         string
	SerialNumber  string
	FirmwareRevision string
	Size          uint64
	MediaType     string
	InterfaceType string
}

type win32NetworkAdapter struct {
	Name              string
	NetConnectionID   string
	MACAddress        string
	Speed             uint64
	NetEnabled        bool
	PhysicalAdapter   bool
	AdapterTypeID     interface{}
	PNPDeviceID       string
}

type win32VideoController struct {
	Name              string
	AdapterCompatibility string
	AdapterRAM        uint32
	DriverVersion     string
	PNPDeviceID       string
	VideoProcessor    string
}

// collectHardware gathers a static hardware snapshot on Windows. Same
// signature as the Linux collector — fails soft per-collector.
func collectHardware(machineID string, gpus []GPUInfo) HardwareInfo {
	hw := HardwareInfo{
		Type:         "hardware_info",
		MachineID:    machineID,
		Architecture: runtime.GOARCH,
	}

	// CPU info via gopsutil for vendor/model/freq.
	if cpuInfos, err := cpu.Info(); err == nil && len(cpuInfos) > 0 {
		ci := cpuInfos[0]
		hw.CPUModel = strings.TrimSpace(ci.ModelName)
		hw.CPUVendor = strings.TrimSpace(ci.VendorID)
		hw.CPUFrequencyMHz = ci.Mhz
		hw.CPUStepping = strconv.Itoa(int(ci.Stepping))

		family, modelNum := parseCPUFamilyAndModel(hw.CPUModel, hw.CPUVendor)
		hw.CPUFamily = family
		hw.CPUModelNum = modelNum
	}
	if physical, err := cpu.Counts(false); err == nil {
		hw.CPUCores = physical
	}
	if logical, err := cpu.Counts(true); err == nil {
		hw.CPUThreads = logical
	}

	// Sockets + cache via WMI.
	var procs []win32Processor
	if err := safeWmiQuery("SELECT Name, Manufacturer, NumberOfCores, NumberOfLogicalProcessors, MaxClockSpeed, L2CacheSize, L3CacheSize, SocketDesignation FROM Win32_Processor", &procs); err == nil && len(procs) > 0 {
		seenSockets := map[string]struct{}{}
		var l2, l3 uint32
		for _, p := range procs {
			seenSockets[p.SocketDesignation] = struct{}{}
			if p.L2CacheSize > l2 {
				l2 = p.L2CacheSize
			}
			if p.L3CacheSize > l3 {
				l3 = p.L3CacheSize
			}
		}
		if len(seenSockets) > 0 {
			hw.CPUSockets = len(seenSockets)
		}
		// WMI L2/L3 cache sizes are in KB already.
		if l2 > 0 {
			hw.CPUCacheL2KB = int(l2)
		}
		if l3 > 0 {
			hw.CPUCacheL3KB = int(l3)
		}
	} else if err != nil {
		log.Printf("hardware: Win32_Processor query: %v", err)
	}

	// CPU flags — Windows doesn't expose flags via WMI cleanly. Use a
	// PowerShell-backed name heuristic. Best-effort.
	hw.CPUFlags = collectCPUFlagsWindows()

	// RAM total — gopsutil works fine here.
	if vm, err := mem.VirtualMemory(); err == nil {
		hw.RAMTotalBytes = vm.Total
	}

	// RAM modules + slot count + max capacity.
	if mods, slots, used := collectRAMModulesWindows(); mods != nil {
		hw.RAMModules = mods
		hw.RAMSlots = slots
		hw.RAMSlotsUsed = used
	}

	// DMI: system / board / BIOS / chassis.
	collectDMIWindows(&hw)

	// Host info — kernel version, platform.
	if hi, err := host.Info(); err == nil && hi != nil {
		hw.KernelVersion = hi.KernelVersion
		hw.PlatformFamily = hi.PlatformFamily
		// Skip Virtualization on Windows — gopsutil's report is unreliable.
		if hi.BootTime > 0 {
			hw.BootTime = int64(hi.BootTime)
		}
	}

	hw.Disks = collectDiskHardwareWindows()
	hw.NetworkInterfaces = collectNetworkInterfacesWindows()

	// Legacy GPU models list — dedupe by name.
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

	hw.GPUDevices = collectGPUDevicesWindows()
	// PCI devices — skipped on Windows for now.
	hw.PCIDevices = nil

	return hw
}

// parseCPUFamilyAndModel — duplicate of the Linux version. Build tags
// keep them in separate compilation units; merging into a shared file is
// out of scope for Phase 9.
var (
	winReCPUNoise       = regexp.MustCompile(`\(R\)|\(TM\)|CPU|Processor|@.*$|[0-9]+-Core`)
	winReSpaces         = regexp.MustCompile(`\s+`)
	winReRyzenThreadrip = regexp.MustCompile(`Ryzen\s+(?:Threadripper\s+)?([A-Z0-9 ]+?)\s+([0-9A-Z]+)`)
	winReRyzenSimple    = regexp.MustCompile(`Ryzen\s+(\d+)\s+([0-9A-Z]+)`)
	winReXeonNamed      = regexp.MustCompile(`Xeon\s+([A-Za-z]+)\s+([0-9A-Z-]+)`)
	winReXeonNumeric    = regexp.MustCompile(`Xeon\s+([A-Z]?\d{4,}\w*)`)
	winReCorePattern    = regexp.MustCompile(`Core\s+(i[3579]|m[3579]|Ultra\s+\d+)\s*-?\s*([0-9A-Z]+)`)
	winReEPYC           = regexp.MustCompile(`EPYC\s+([0-9A-Z-]+)`)
	winReAtom           = regexp.MustCompile(`Atom\s+([A-Z]?\d+\w*)`)
	winReAppleSilicon   = regexp.MustCompile(`(M\d+(?:\s+\w+)?)`)
	winReWMIDate        = regexp.MustCompile(`^(\d{4})(\d{2})(\d{2})`)
)

func parseCPUFamilyAndModel(model, vendor string) (family, modelNum string) {
	clean := winReCPUNoise.ReplaceAllString(model, "")
	clean = strings.TrimSpace(winReSpaces.ReplaceAllString(clean, " "))

	switch {
	case strings.Contains(clean, "Ryzen"):
		if m := winReRyzenThreadrip.FindStringSubmatch(clean); len(m) == 3 {
			family = "Ryzen " + strings.TrimSpace(m[1])
			modelNum = strings.TrimSpace(m[2])
		} else if m := winReRyzenSimple.FindStringSubmatch(clean); len(m) == 3 {
			family = "Ryzen " + m[1]
			modelNum = m[2]
		}
	case strings.Contains(clean, "Xeon"):
		if m := winReXeonNamed.FindStringSubmatch(clean); len(m) == 3 {
			family = "Xeon " + m[1]
			modelNum = m[2]
		} else if m := winReXeonNumeric.FindStringSubmatch(clean); len(m) == 2 {
			family = "Xeon"
			modelNum = m[1]
		}
	case strings.Contains(clean, "Core"):
		if m := winReCorePattern.FindStringSubmatch(clean); len(m) == 3 {
			family = "Core " + m[1]
			modelNum = m[2]
		}
	case strings.Contains(clean, "EPYC"):
		if m := winReEPYC.FindStringSubmatch(clean); len(m) == 2 {
			family = "EPYC"
			modelNum = m[1]
		}
	case strings.Contains(clean, "Atom"):
		if m := winReAtom.FindStringSubmatch(clean); len(m) == 2 {
			family = "Atom"
			modelNum = m[1]
		}
	case strings.Contains(strings.ToLower(vendor), "apple"):
		if m := winReAppleSilicon.FindStringSubmatch(clean); len(m) == 2 {
			family = "Apple Silicon"
			modelNum = m[1]
		}
	}
	return family, modelNum
}

// collectCPUFlagsWindows derives capability flags from the CPU model name
// via a coarse, name-based heuristic. Windows does not expose /proc/cpuinfo
// equivalents, and WMI's Win32_Processor lacks a flags field.
func collectCPUFlagsWindows() []string {
	var name string
	if cpuInfos, err := cpu.Info(); err == nil && len(cpuInfos) > 0 {
		name = strings.ToLower(cpuInfos[0].ModelName)
	}
	if name == "" {
		// Fall back to PowerShell's Get-WmiObject.
		// Uses -Command inline string, not -File. Safe under Restricted
		// execution policy (default on stock Windows): policy gates loading
		// .ps1 files from disk, not inline expressions. Do not change to
		// -File without also adding -ExecutionPolicy Bypass.
		out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive",
			"-Command", "(Get-WmiObject Win32_Processor).Name").Output()
		if err == nil {
			name = strings.ToLower(strings.TrimSpace(string(out)))
		}
	}
	if name == "" {
		return nil
	}

	var flags []string
	add := func(f string) { flags = append(flags, f) }

	// AVX-512 capable families (rough heuristic).
	if strings.Contains(name, "xeon") {
		add("sse4_1")
		add("sse4_2")
		add("avx")
		add("avx2")
		add("aes")
		// Skylake-X / Cascade Lake / Ice Lake / Sapphire Rapids carry AVX-512.
		if strings.Contains(name, "platinum") || strings.Contains(name, "gold") ||
			strings.Contains(name, "silver") || strings.Contains(name, "max") ||
			strings.Contains(name, "w-") {
			add("avx512f")
		}
		add("vmx")
	} else if strings.Contains(name, "core") {
		add("sse4_1")
		add("sse4_2")
		add("avx")
		add("avx2")
		add("aes")
		add("vmx")
	} else if strings.Contains(name, "ryzen") || strings.Contains(name, "threadripper") || strings.Contains(name, "epyc") {
		add("sse4_1")
		add("sse4_2")
		add("sse4a")
		add("avx")
		add("avx2")
		add("aes")
		add("svm")
		// Zen 4 / Zen 5 — Ryzen 7000 / 9000, EPYC Genoa+ have AVX-512.
		if strings.Contains(name, "7950") || strings.Contains(name, "7900") ||
			strings.Contains(name, "7800") || strings.Contains(name, "7700") ||
			strings.Contains(name, "9950") || strings.Contains(name, "9900") ||
			strings.Contains(name, "genoa") || strings.Contains(name, "bergamo") ||
			strings.Contains(name, "siena") || strings.Contains(name, "turin") {
			add("avx512f")
		}
	}

	sort.Strings(flags)
	return flags
}

// collectRAMModulesWindows enumerates DIMMs via Win32_PhysicalMemory.
// Returns (modules, totalSlots, populatedSlots). `totalSlots` reflects
// the populated count — Windows doesn't report empty banks the way
// dmidecode does. RAMMaxBytes is left at zero (no equivalent on
// Win32_PhysicalMemoryArray we want to reach into right now).
func collectRAMModulesWindows() (modules []RAMModule, totalSlots, populatedSlots int) {
	var rows []win32PhysicalMemory
	q := "SELECT BankLabel, DeviceLocator, Capacity, Speed, ConfiguredClockSpeed, SMBIOSMemoryType, MemoryType, FormFactor, Manufacturer, PartNumber FROM Win32_PhysicalMemory"
	if err := safeWmiQuery(q, &rows); err != nil {
		log.Printf("hardware: Win32_PhysicalMemory query: %v", err)
		return nil, 0, 0
	}
	if len(rows) == 0 {
		return nil, 0, 0
	}
	for _, r := range rows {
		mod := RAMModule{
			Slot:         strings.TrimSpace(coalesce(r.DeviceLocator, r.BankLabel)),
			SizeBytes:    r.Capacity,
			SpeedMTs:     int(r.ConfiguredClockSpeed),
			MaxSpeedMTs:  int(r.Speed),
			Manufacturer: strings.TrimSpace(r.Manufacturer),
			PartNumber:   strings.TrimSpace(r.PartNumber),
			FormFactor:   memFormFactorName(coerceUint32(r.FormFactor)),
		}
		// Prefer SMBIOS-typed code; fall back to MemoryType.
		typeCode := coerceUint32(r.SMBIOSMemoryType)
		if typeCode == 0 {
			typeCode = coerceUint32(r.MemoryType)
		}
		mod.Type = smbiosMemoryTypeName(typeCode)
		if mod.SizeBytes == 0 {
			continue
		}
		modules = append(modules, mod)
	}
	populatedSlots = len(modules)
	totalSlots = populatedSlots
	return modules, totalSlots, populatedSlots
}

// smbiosMemoryTypeName maps SMBIOS memory-type codes to human strings.
// Based on the SMBIOS 3.6 spec table 7.18.
func smbiosMemoryTypeName(code uint32) string {
	switch code {
	case 0x01:
		return "Other"
	case 0x02:
		return "Unknown"
	case 0x03:
		return "DRAM"
	case 0x12:
		return "DDR"
	case 0x13:
		return "DDR2"
	case 0x14:
		return "DDR2 FB-DIMM"
	case 0x18:
		return "DDR3"
	case 0x1A:
		return "DDR4"
	case 0x1B:
		return "LPDDR"
	case 0x1C:
		return "LPDDR2"
	case 0x1D:
		return "LPDDR3"
	case 0x1E:
		return "LPDDR4"
	case 0x22:
		return "DDR5"
	case 0x23:
		return "LPDDR5"
	}
	return ""
}

// memFormFactorName maps Win32_PhysicalMemory.FormFactor codes.
func memFormFactorName(code uint32) string {
	switch code {
	case 8:
		return "DIMM"
	case 12:
		return "SODIMM"
	case 13:
		return "RIMM"
	}
	return ""
}

// collectDMIWindows fills system / board / BIOS / chassis fields by
// querying Win32_ComputerSystem, Win32_BIOS, Win32_BaseBoard, Win32_SystemEnclosure.
func collectDMIWindows(hw *HardwareInfo) {
	var sys []win32ComputerSystem
	if err := safeWmiQuery("SELECT Manufacturer, Model FROM Win32_ComputerSystem", &sys); err == nil && len(sys) > 0 {
		hw.SystemVendor = cleanWMIValue(sys[0].Manufacturer)
		hw.SystemProduct = cleanWMIValue(sys[0].Model)
	}

	var bios []win32BIOS
	if err := safeWmiQuery("SELECT Manufacturer, Version, SMBIOSBIOSVersion, ReleaseDate, SerialNumber FROM Win32_BIOS", &bios); err == nil && len(bios) > 0 {
		hw.BIOSVendor = cleanWMIValue(bios[0].Manufacturer)
		v := cleanWMIValue(bios[0].SMBIOSBIOSVersion)
		if v == "" {
			v = cleanWMIValue(bios[0].Version)
		}
		hw.BIOSVersion = v
		hw.BIOSReleaseDate = parseWMIDate(bios[0].ReleaseDate)
		hw.SystemSerial = cleanWMIValue(bios[0].SerialNumber)
	}

	var board []win32BaseBoard
	if err := safeWmiQuery("SELECT Manufacturer, Product, Version, SerialNumber FROM Win32_BaseBoard", &board); err == nil && len(board) > 0 {
		hw.BoardVendor = cleanWMIValue(board[0].Manufacturer)
		hw.BoardProduct = cleanWMIValue(board[0].Product)
		hw.BoardVersion = cleanWMIValue(board[0].Version)
		hw.BoardSerial = cleanWMIValue(board[0].SerialNumber)
	}

	var enc []win32SystemEnclosure
	if err := safeWmiQuery("SELECT Manufacturer, ChassisTypes, SerialNumber FROM Win32_SystemEnclosure", &enc); err == nil && len(enc) > 0 {
		hw.ChassisVendor = cleanWMIValue(enc[0].Manufacturer)
		if t := coerceFirstUint32(enc[0].ChassisTypes); t > 0 {
			hw.ChassisType = parseChassisType(strconv.Itoa(int(t)))
		}
	}
}

// parseChassisType — duplicate of the Linux helper. Same code, separate
// compilation unit per build tag.
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

// parseWMIDate converts a CIM_DATETIME string (e.g.
// "20230415000000.000000+000") to "YYYY-MM-DD". Returns empty if the
// input doesn't look like a CIM_DATETIME.
func parseWMIDate(wmiDate string) string {
	wmiDate = strings.TrimSpace(wmiDate)
	if len(wmiDate) < 8 {
		return ""
	}
	m := winReWMIDate.FindStringSubmatch(wmiDate)
	if len(m) != 4 {
		return ""
	}
	return fmt.Sprintf("%s-%s-%s", m[1], m[2], m[3])
}

// collectDiskHardwareWindows enumerates physical disks via Win32_DiskDrive.
// Sorted by Device for deterministic output.
func collectDiskHardwareWindows() []DiskHardwareInfo {
	var rows []win32DiskDrive
	q := "SELECT DeviceID, Model, SerialNumber, FirmwareRevision, Size, MediaType, InterfaceType FROM Win32_DiskDrive"
	if err := safeWmiQuery(q, &rows); err != nil {
		log.Printf("hardware: Win32_DiskDrive query: %v", err)
		return nil
	}
	var disks []DiskHardwareInfo
	for _, r := range rows {
		d := DiskHardwareInfo{
			Device:    r.DeviceID,
			Model:     strings.TrimSpace(r.Model),
			SizeBytes: r.Size,
			Serial:    strings.TrimSpace(r.SerialNumber),
			Firmware:  strings.TrimSpace(r.FirmwareRevision),
			Interface: strings.ToLower(strings.TrimSpace(r.InterfaceType)),
			Type:      classifyDiskType(r.MediaType, r.InterfaceType, r.Model),
		}
		if d.SizeBytes == 0 && d.Model == "" {
			continue
		}
		disks = append(disks, d)
	}
	sort.Slice(disks, func(i, j int) bool { return disks[i].Device < disks[j].Device })
	return disks
}

// classifyDiskType returns "ssd" | "hdd" | "nvme" using a combination of
// MediaType, InterfaceType and Model.
func classifyDiskType(mediaType, interfaceType, model string) string {
	m := strings.ToLower(mediaType)
	iface := strings.ToLower(interfaceType)
	mdl := strings.ToLower(model)

	if strings.Contains(iface, "nvme") || strings.Contains(mdl, "nvme") {
		return "nvme"
	}
	if strings.Contains(m, "ssd") || strings.Contains(mdl, "ssd") {
		return "ssd"
	}
	if strings.Contains(m, "hdd") || strings.Contains(m, "fixed hard") {
		// "Fixed hard disk media" is the WMI default — not actually
		// indicative of spinning rust on modern drives. Fall through.
		if strings.Contains(mdl, "ssd") || strings.Contains(mdl, "nvme") {
			return "ssd"
		}
		return "hdd"
	}
	return ""
}

// collectNetworkInterfacesWindows enumerates physical NICs. Skips
// loopback, virtual, vmware, hyper-v, wsl interfaces. Speed is taken
// from Win32_NetworkAdapter.Speed where matched by MAC.
func collectNetworkInterfacesWindows() []NetworkInterfaceInfo {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	// Build MAC -> speed map from WMI.
	speedByMAC := map[string]int64{}
	var adapters []win32NetworkAdapter
	if err := safeWmiQuery("SELECT Name, NetConnectionID, MACAddress, Speed, NetEnabled, PhysicalAdapter, PNPDeviceID FROM Win32_NetworkAdapter WHERE PhysicalAdapter = TRUE", &adapters); err == nil {
		for _, a := range adapters {
			mac := strings.ToLower(strings.TrimSpace(a.MACAddress))
			if mac == "" || a.Speed == 0 {
				continue
			}
			speedByMAC[mac] = int64(a.Speed / 1_000_000)
		}
	} else {
		log.Printf("hardware: Win32_NetworkAdapter query: %v", err)
	}

	var result []NetworkInterfaceInfo
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		name := iface.Name
		if skipInterfaceWindows(name) {
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

		if speed, ok := speedByMAC[strings.ToLower(ni.MAC)]; ok && speed > 0 {
			ni.SpeedMbps = speed
		}

		result = append(result, ni)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func skipInterfaceWindows(name string) bool {
	if name == "" {
		return true
	}
	lower := strings.ToLower(name)
	for _, p := range []string{"loopback", "virtual", "vmware", "hyper-v", "wsl", "bluetooth", "vethernet"} {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// collectGPUDevicesWindows enumerates display adapters via Win32_VideoController.
func collectGPUDevicesWindows() []GPUDevice {
	var rows []win32VideoController
	q := "SELECT Name, AdapterCompatibility, AdapterRAM, DriverVersion, PNPDeviceID, VideoProcessor FROM Win32_VideoController"
	if err := safeWmiQuery(q, &rows); err != nil {
		log.Printf("hardware: Win32_VideoController query: %v", err)
		return nil
	}
	var gpus []GPUDevice
	for _, r := range rows {
		gpu := GPUDevice{
			Vendor:    normalizeGPUVendor(r.AdapterCompatibility),
			Model:     strings.TrimSpace(r.Name),
			Driver:    strings.TrimSpace(r.DriverVersion),
			VRAMBytes: uint64(r.AdapterRAM),
			BusType:   "PCI", // Best-effort default; deeper detection is out of scope.
		}
		if gpu.Vendor == "" && r.VideoProcessor != "" {
			gpu.Vendor = normalizeGPUVendor(r.VideoProcessor)
		}
		if gpu.Model == "" {
			continue
		}
		gpus = append(gpus, gpu)
	}
	return gpus
}

// normalizeGPUVendor — duplicate of the Linux helper.
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

func cleanWMIValue(s string) string {
	val := strings.TrimSpace(s)
	switch strings.ToLower(val) {
	case "default string", "to be filled by o.e.m.", "system manufacturer",
		"system product name", "system serial number", "not specified",
		"not applicable", "none", "unknown", "":
		return ""
	}
	return val
}

func coalesce(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// win32ThermalZone shadows the WMI MSAcpi_ThermalZoneTemperature view.
// CurrentTemperature is reported in tenths of Kelvin per the ACPI spec.
type win32ThermalZone struct {
	InstanceName       string
	CurrentTemperature interface{}
}

// collectCPUTempC asks WMI's root\WMI namespace for ACPI thermal zones
// and returns the highest plausible reading. Returns 0 when no zone is
// readable — many Windows systems either don't expose
// MSAcpi_ThermalZoneTemperature at all or only do so under elevated
// rights, so 0 is treated by the dashboard as "unknown".
//
// PHASE12-NOTE: ignores readings <0 or >120°C as sensor faults.
func collectCPUTempC() float64 {
	var zones []win32ThermalZone
	if err := wmi.QueryNamespace(
		"SELECT InstanceName, CurrentTemperature FROM MSAcpi_ThermalZoneTemperature",
		&zones, `root\WMI`,
	); err != nil || len(zones) == 0 {
		return 0
	}
	var max float64
	for _, z := range zones {
		temp := coerceUint32(z.CurrentTemperature)
		if temp == 0 {
			continue
		}
		c := float64(temp)/10.0 - 273.15
		if c < 0 || c > 120 {
			continue
		}
		if c > max {
			max = c
		}
	}
	return max
}
