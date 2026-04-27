package main

// Hardware-info data types shared by every platform's collector. Kept in
// a platform-neutral file so both Linux (hardware.go) and Windows
// (hardware_windows.go) can populate them.

// HardwareInfo is a one-time snapshot of a machine's static hardware details.
// It's sent after agent connect (and only re-sent if detection changes).
//
// All fields are JSON-tagged with omitempty so the dashboard can detect
// which fields a given agent reports — older agents will simply omit the
// new fields and the UI degrades gracefully.
type HardwareInfo struct {
	Type      string `json:"type"`
	MachineID string `json:"machine_id"`

	// CPU (legacy fields kept; new parsed fields added)
	CPUModel        string  `json:"cpu_model,omitempty"`
	CPUVendor       string  `json:"cpu_vendor,omitempty"`
	CPUCores        int     `json:"cpu_cores,omitempty"`
	CPUThreads      int     `json:"cpu_threads,omitempty"`
	CPUFrequencyMHz float64 `json:"cpu_frequency_mhz,omitempty"`
	// New CPU fields
	CPUSockets   int      `json:"cpu_sockets,omitempty"`
	CPUFamily    string   `json:"cpu_family,omitempty"`    // e.g. "Ryzen 9", "Xeon Gold"
	CPUModelNum  string   `json:"cpu_model_num,omitempty"` // e.g. "5950X", "6132"
	CPUStepping  string   `json:"cpu_stepping,omitempty"`
	CPUCacheL1KB int      `json:"cpu_cache_l1_kb,omitempty"`
	CPUCacheL2KB int      `json:"cpu_cache_l2_kb,omitempty"`
	CPUCacheL3KB int      `json:"cpu_cache_l3_kb,omitempty"`
	CPUFlags     []string `json:"cpu_flags,omitempty"` // e.g. ["avx", "avx2", "avx512f", "vmx"]

	// Memory (legacy field kept; new fields added)
	RAMTotalBytes uint64 `json:"ram_total_bytes,omitempty"`
	// New memory fields — populated only when dmidecode is available
	RAMSlots     int         `json:"ram_slots,omitempty"`      // total DIMM slots on the board
	RAMSlotsUsed int         `json:"ram_slots_used,omitempty"` // populated DIMM slots
	RAMMaxBytes  uint64      `json:"ram_max_bytes,omitempty"`  // max capacity of the board
	RAMModules   []RAMModule `json:"ram_modules,omitempty"`    // per-DIMM detail

	// Platform
	KernelVersion  string `json:"kernel_version,omitempty"`
	PlatformFamily string `json:"platform_family,omitempty"`
	Virtualization string `json:"virtualization,omitempty"`
	BootTime       int64  `json:"boot_time,omitempty"`
	Architecture   string `json:"architecture,omitempty"`

	// System / DMI — all new
	SystemVendor    string `json:"system_vendor,omitempty"`
	SystemProduct   string `json:"system_product,omitempty"`
	SystemSerial    string `json:"system_serial,omitempty"`
	SystemUUID      string `json:"system_uuid,omitempty"`
	BoardVendor     string `json:"board_vendor,omitempty"`
	BoardProduct    string `json:"board_product,omitempty"`
	BoardVersion    string `json:"board_version,omitempty"`
	BoardSerial     string `json:"board_serial,omitempty"`
	BIOSVendor      string `json:"bios_vendor,omitempty"`
	BIOSVersion     string `json:"bios_version,omitempty"`
	BIOSReleaseDate string `json:"bios_release_date,omitempty"`
	ChassisVendor   string `json:"chassis_vendor,omitempty"`
	ChassisType     string `json:"chassis_type,omitempty"` // "desktop", "laptop", "server", "blade", etc.

	// GPU — legacy field kept, new structured field added
	GPUModels  []string    `json:"gpu_models,omitempty"`  // legacy: just names
	GPUDevices []GPUDevice `json:"gpu_devices,omitempty"` // new: structured per-GPU data

	// Storage — legacy field expanded with new sub-fields on DiskHardwareInfo
	Disks []DiskHardwareInfo `json:"disks,omitempty"`

	// Network
	NetworkInterfaces []NetworkInterfaceInfo `json:"network_interfaces,omitempty"`

	// PCI devices — Linux-only today; nil on Windows.
	PCIDevices []PCIDevice `json:"pci_devices,omitempty"`
}

// RAMModule is a single populated DIMM slot.
type RAMModule struct {
	Slot         string `json:"slot,omitempty"`
	SizeBytes    uint64 `json:"size_bytes,omitempty"`
	SpeedMTs     int    `json:"speed_mts,omitempty"`
	MaxSpeedMTs  int    `json:"max_speed_mts,omitempty"`
	Type         string `json:"type,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	PartNumber   string `json:"part_number,omitempty"`
	Rank         string `json:"rank,omitempty"`
	FormFactor   string `json:"form_factor,omitempty"`
}

// GPUDevice is a structured GPU device (in addition to the legacy GPUModels list of names).
// Distinct from main.go's GPUInfo which holds live nvidia-smi metrics.
type GPUDevice struct {
	Vendor    string `json:"vendor,omitempty"`
	Model     string `json:"model,omitempty"`
	PCISlot   string `json:"pci_slot,omitempty"`
	Driver    string `json:"driver,omitempty"`
	VRAMBytes uint64 `json:"vram_bytes,omitempty"`
	BusType   string `json:"bus_type,omitempty"`
}

// DiskHardwareInfo expands the legacy three-field disk record.
type DiskHardwareInfo struct {
	// Legacy fields
	Device    string `json:"device"`
	Model     string `json:"model,omitempty"`
	SizeBytes uint64 `json:"size_bytes,omitempty"`
	Type      string `json:"type,omitempty"`
	// New fields
	Serial          string `json:"serial,omitempty"`
	Firmware        string `json:"firmware,omitempty"`
	Interface       string `json:"interface,omitempty"`
	RotationRateRPM int    `json:"rotation_rate_rpm,omitempty"`
}

// NetworkInterfaceInfo describes a single network interface.
type NetworkInterfaceInfo struct {
	Name      string `json:"name"`
	MAC       string `json:"mac,omitempty"`
	IPv4      string `json:"ipv4,omitempty"`
	SpeedMbps int64  `json:"speed_mbps,omitempty"`
}

// PCIDevice is a single PCI device. Linux-only today.
type PCIDevice struct {
	Slot      string `json:"slot,omitempty"`
	Class     string `json:"class,omitempty"`
	Vendor    string `json:"vendor,omitempty"`
	Device    string `json:"device,omitempty"`
	SubVendor string `json:"sub_vendor,omitempty"`
	SubDevice string `json:"sub_device,omitempty"`
}
