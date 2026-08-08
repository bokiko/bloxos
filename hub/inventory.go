package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

/* ============================================================================
 * Inventory aggregation
 *
 * Exposes GET /api/inventory — returns fleet-wide hardware aggregation with
 * top-line totals and per-machine summaries. The dashboard's /inventory
 * page (Phase 6 Unit B) consumes this to render the table + summary cards.
 *
 * Aggregation is done over the raw `machines.hardware_info` JSON. Older
 * agents that only report a subset of fields contribute what they have;
 * fields they don't report are simply absent in the aggregation.
 * ============================================================================ */

type InventoryResponse struct {
	GeneratedAt string                 `json:"generated_at"`
	Totals      InventoryTotals        `json:"totals"`
	Machines    []InventoryMachine     `json:"machines"`
	CPUs        []InventoryCPUGroup    `json:"cpus"`
	Memory      []InventoryMemoryGroup `json:"memory"`
	Disks       []InventoryDiskRow     `json:"disks"`
	GPUs        []InventoryGPUGroup    `json:"gpus"`
	NICs        []InventoryNICRow      `json:"nics"`
}

type InventoryTotals struct {
	MachineCount       int    `json:"machine_count"`
	OnlineCount        int    `json:"online_count"`
	TotalRAMBytes      uint64 `json:"total_ram_bytes"`
	TotalDiskBytes     uint64 `json:"total_disk_bytes"`
	TotalNVMeBytes     uint64 `json:"total_nvme_bytes"`
	TotalSSDBytes      uint64 `json:"total_ssd_bytes"`
	TotalHDDBytes      uint64 `json:"total_hdd_bytes"`
	TotalCPUCores      int    `json:"total_cpu_cores"`
	TotalCPUThreads    int    `json:"total_cpu_threads"`
	TotalGPUCount      int    `json:"total_gpu_count"`
	UniqueCPUModels    int    `json:"unique_cpu_models"`
	UniqueGPUModels    int    `json:"unique_gpu_models"`
	UniqueMotherboards int    `json:"unique_motherboards"`
}

type InventoryMachine struct {
	MachineID      string `json:"machine_id"`
	Hostname       string `json:"hostname"`
	Status         string `json:"status"`
	IP             string `json:"ip,omitempty"`
	OS             string `json:"os,omitempty"`
	CPUModel       string `json:"cpu_model,omitempty"`
	CPUFamily      string `json:"cpu_family,omitempty"`
	CPUCores       int    `json:"cpu_cores,omitempty"`
	CPUThreads     int    `json:"cpu_threads,omitempty"`
	CPUSockets     int    `json:"cpu_sockets,omitempty"`
	RAMBytes       uint64 `json:"ram_bytes,omitempty"`
	RAMSlots       int    `json:"ram_slots,omitempty"`
	RAMSlotsUsed   int    `json:"ram_slots_used,omitempty"`
	BoardVendor    string `json:"board_vendor,omitempty"`
	BoardProduct   string `json:"board_product,omitempty"`
	SystemVendor   string `json:"system_vendor,omitempty"`
	SystemProduct  string `json:"system_product,omitempty"`
	ChassisType    string `json:"chassis_type,omitempty"`
	DiskCount      int    `json:"disk_count,omitempty"`
	TotalDiskBytes uint64 `json:"total_disk_bytes,omitempty"`
	GPUCount       int    `json:"gpu_count,omitempty"`
	GPUSummary     string `json:"gpu_summary,omitempty"`
	NICCount       int    `json:"nic_count,omitempty"`
}

type InventoryCPUGroup struct {
	Model      string   `json:"model"`
	Vendor     string   `json:"vendor,omitempty"`
	Family     string   `json:"family,omitempty"`
	ModelNum   string   `json:"model_num,omitempty"`
	Count      int      `json:"count"`
	MachineIDs []string `json:"machine_ids"`
	Cores      int      `json:"cores,omitempty"`
	Threads    int      `json:"threads,omitempty"`
}

type InventoryMemoryGroup struct {
	SizeBytes    uint64   `json:"size_bytes"`
	Type         string   `json:"type,omitempty"`
	SpeedMTs     int      `json:"speed_mts,omitempty"`
	Manufacturer string   `json:"manufacturer,omitempty"`
	Count        int      `json:"count"`
	TotalBytes   uint64   `json:"total_bytes"`
	MachineIDs   []string `json:"machine_ids"`
}

type InventoryDiskRow struct {
	MachineID string `json:"machine_id"`
	Hostname  string `json:"hostname"`
	Device    string `json:"device"`
	Model     string `json:"model,omitempty"`
	Serial    string `json:"serial,omitempty"`
	SizeBytes uint64 `json:"size_bytes,omitempty"`
	Type      string `json:"type,omitempty"`
	Interface string `json:"interface,omitempty"`
	Firmware  string `json:"firmware,omitempty"`
}

type InventoryGPUGroup struct {
	Vendor     string   `json:"vendor,omitempty"`
	Model      string   `json:"model"`
	Count      int      `json:"count"`
	MachineIDs []string `json:"machine_ids"`
}

type InventoryNICRow struct {
	MachineID string `json:"machine_id"`
	Hostname  string `json:"hostname"`
	Name      string `json:"name"`
	IPv4      string `json:"ipv4,omitempty"`
	MAC       string `json:"mac,omitempty"`
	SpeedMbps int64  `json:"speed_mbps,omitempty"`
}

// agentHardwareSnapshot mirrors the JSON schema produced by the agent's
// HardwareInfo. Fields not used by the inventory endpoint can be omitted.
type agentHardwareSnapshot struct {
	CPUModel          string                   `json:"cpu_model"`
	CPUVendor         string                   `json:"cpu_vendor"`
	CPUFamily         string                   `json:"cpu_family"`
	CPUModelNum       string                   `json:"cpu_model_num"`
	CPUCores          int                      `json:"cpu_cores"`
	CPUThreads        int                      `json:"cpu_threads"`
	CPUSockets        int                      `json:"cpu_sockets"`
	RAMTotalBytes     uint64                   `json:"ram_total_bytes"`
	RAMSlots          int                      `json:"ram_slots"`
	RAMSlotsUsed      int                      `json:"ram_slots_used"`
	BoardVendor       string                   `json:"board_vendor"`
	BoardProduct      string                   `json:"board_product"`
	SystemVendor      string                   `json:"system_vendor"`
	SystemProduct     string                   `json:"system_product"`
	ChassisType       string                   `json:"chassis_type"`
	GPUModels         []string                 `json:"gpu_models"`
	GPUDevices        []agentSnapshotGPU       `json:"gpu_devices"`
	Disks             []agentSnapshotDisk      `json:"disks"`
	RAMModules        []agentSnapshotRAMModule `json:"ram_modules"`
	NetworkInterfaces []agentSnapshotNIC       `json:"network_interfaces"`
}

type agentSnapshotGPU struct {
	Vendor string `json:"vendor"`
	Model  string `json:"model"`
}

type agentSnapshotDisk struct {
	Device    string `json:"device"`
	Model     string `json:"model"`
	Serial    string `json:"serial"`
	SizeBytes uint64 `json:"size_bytes"`
	Type      string `json:"type"`
	Interface string `json:"interface"`
	Firmware  string `json:"firmware"`
}

type agentSnapshotRAMModule struct {
	SizeBytes    uint64 `json:"size_bytes"`
	SpeedMTs     int    `json:"speed_mts"`
	Type         string `json:"type"`
	Manufacturer string `json:"manufacturer"`
}

type agentSnapshotNIC struct {
	Name      string `json:"name"`
	IPv4      string `json:"ipv4"`
	MAC       string `json:"mac"`
	SpeedMbps int64  `json:"speed_mbps"`
}

func (s *Server) handleInventory(c echo.Context) error {
	rows, err := s.db.Query(`
		SELECT id, hostname, ip, os, status, COALESCE(hardware_info, '')
		FROM machines
		ORDER BY hostname
	`)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	defer rows.Close()

	resp := InventoryResponse{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Machines:    []InventoryMachine{},
		CPUs:        []InventoryCPUGroup{},
		Memory:      []InventoryMemoryGroup{},
		Disks:       []InventoryDiskRow{},
		GPUs:        []InventoryGPUGroup{},
		NICs:        []InventoryNICRow{},
	}

	cpuGroupMap := map[string]*InventoryCPUGroup{}
	memGroupMap := map[string]*InventoryMemoryGroup{}
	gpuGroupMap := map[string]*InventoryGPUGroup{}
	uniqueBoards := map[string]struct{}{}

	for rows.Next() {
		var m InventoryMachine
		var ip, osStr, hwJSON sql.NullString
		var status string
		if err := rows.Scan(&m.MachineID, &m.Hostname, &ip, &osStr, &status, &hwJSON); err != nil {
			continue
		}
		m.Status = status
		m.IP = ip.String
		m.OS = osStr.String

		resp.Totals.MachineCount++
		if status == "online" {
			resp.Totals.OnlineCount++
		}

		if hwJSON.String == "" {
			resp.Machines = append(resp.Machines, m)
			continue
		}

		var hw agentHardwareSnapshot
		if err := json.Unmarshal([]byte(hwJSON.String), &hw); err != nil {
			resp.Machines = append(resp.Machines, m)
			continue
		}

		m.CPUModel = hw.CPUModel
		m.CPUFamily = hw.CPUFamily
		m.CPUCores = hw.CPUCores
		m.CPUThreads = hw.CPUThreads
		m.CPUSockets = hw.CPUSockets
		if m.CPUSockets == 0 && hw.CPUModel != "" {
			m.CPUSockets = 1
		}
		m.RAMBytes = hw.RAMTotalBytes
		m.RAMSlots = hw.RAMSlots
		m.RAMSlotsUsed = hw.RAMSlotsUsed
		m.BoardVendor = hw.BoardVendor
		m.BoardProduct = hw.BoardProduct
		m.SystemVendor = hw.SystemVendor
		m.SystemProduct = hw.SystemProduct
		m.ChassisType = hw.ChassisType
		m.DiskCount = len(hw.Disks)
		m.NICCount = len(hw.NetworkInterfaces)

		var totalDisk uint64
		for _, d := range hw.Disks {
			totalDisk += d.SizeBytes
			resp.Totals.TotalDiskBytes += d.SizeBytes
			switch d.Type {
			case "nvme":
				resp.Totals.TotalNVMeBytes += d.SizeBytes
			case "ssd":
				resp.Totals.TotalSSDBytes += d.SizeBytes
			case "hdd":
				resp.Totals.TotalHDDBytes += d.SizeBytes
			}
			resp.Disks = append(resp.Disks, InventoryDiskRow{
				MachineID: m.MachineID,
				Hostname:  m.Hostname,
				Device:    d.Device,
				Model:     d.Model,
				Serial:    d.Serial,
				SizeBytes: d.SizeBytes,
				Type:      d.Type,
				Interface: d.Interface,
				Firmware:  d.Firmware,
			})
		}
		m.TotalDiskBytes = totalDisk

		gpuModels := dedupedGPUModels(hw.GPUModels, hw.GPUDevices)
		m.GPUCount = len(gpuModels)
		m.GPUSummary = strings.Join(gpuModels, " + ")
		resp.Totals.TotalGPUCount += m.GPUCount

		for _, n := range hw.NetworkInterfaces {
			resp.NICs = append(resp.NICs, InventoryNICRow{
				MachineID: m.MachineID,
				Hostname:  m.Hostname,
				Name:      n.Name,
				IPv4:      n.IPv4,
				MAC:       n.MAC,
				SpeedMbps: n.SpeedMbps,
			})
		}

		resp.Totals.TotalRAMBytes += hw.RAMTotalBytes
		resp.Totals.TotalCPUCores += hw.CPUCores * inventoryMaxInt(1, hw.CPUSockets)
		resp.Totals.TotalCPUThreads += hw.CPUThreads * inventoryMaxInt(1, hw.CPUSockets)

		if hw.CPUModel != "" {
			grp, ok := cpuGroupMap[hw.CPUModel]
			if !ok {
				grp = &InventoryCPUGroup{
					Model:    hw.CPUModel,
					Vendor:   hw.CPUVendor,
					Family:   hw.CPUFamily,
					ModelNum: hw.CPUModelNum,
					Cores:    hw.CPUCores,
					Threads:  hw.CPUThreads,
				}
				cpuGroupMap[hw.CPUModel] = grp
			}
			grp.Count += inventoryMaxInt(1, hw.CPUSockets)
			grp.MachineIDs = append(grp.MachineIDs, m.MachineID)
		}

		for _, mod := range hw.RAMModules {
			key := memGroupKey(mod.SizeBytes, mod.Type, mod.SpeedMTs, mod.Manufacturer)
			grp, ok := memGroupMap[key]
			if !ok {
				grp = &InventoryMemoryGroup{
					SizeBytes:    mod.SizeBytes,
					Type:         mod.Type,
					SpeedMTs:     mod.SpeedMTs,
					Manufacturer: mod.Manufacturer,
				}
				memGroupMap[key] = grp
			}
			grp.Count++
			grp.TotalBytes += mod.SizeBytes
			grp.MachineIDs = appendUnique(grp.MachineIDs, m.MachineID)
		}

		for _, model := range gpuModels {
			grp, ok := gpuGroupMap[model]
			if !ok {
				grp = &InventoryGPUGroup{Model: model}
				for _, d := range hw.GPUDevices {
					if d.Model == model {
						grp.Vendor = d.Vendor
						break
					}
				}
				gpuGroupMap[model] = grp
			}
			grp.Count++
			grp.MachineIDs = appendUnique(grp.MachineIDs, m.MachineID)
		}

		if hw.BoardVendor != "" || hw.BoardProduct != "" {
			uniqueBoards[hw.BoardVendor+"|"+hw.BoardProduct] = struct{}{}
		}

		resp.Machines = append(resp.Machines, m)
	}

	for _, g := range cpuGroupMap {
		resp.CPUs = append(resp.CPUs, *g)
	}
	sort.Slice(resp.CPUs, func(i, j int) bool { return resp.CPUs[i].Count > resp.CPUs[j].Count })

	for _, g := range memGroupMap {
		resp.Memory = append(resp.Memory, *g)
	}
	sort.Slice(resp.Memory, func(i, j int) bool { return resp.Memory[i].TotalBytes > resp.Memory[j].TotalBytes })

	for _, g := range gpuGroupMap {
		resp.GPUs = append(resp.GPUs, *g)
	}
	sort.Slice(resp.GPUs, func(i, j int) bool { return resp.GPUs[i].Count > resp.GPUs[j].Count })

	resp.Totals.UniqueCPUModels = len(cpuGroupMap)
	resp.Totals.UniqueGPUModels = len(gpuGroupMap)
	resp.Totals.UniqueMotherboards = len(uniqueBoards)

	return c.JSON(http.StatusOK, resp)
}

/* ---------- helpers (prefixed to avoid collisions with other hub files) ---------- */

func dedupedGPUModels(legacy []string, structured []agentSnapshotGPU) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, m := range legacy {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}
	for _, d := range structured {
		m := strings.TrimSpace(d.Model)
		if m == "" {
			continue
		}
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}
	return out
}

func memGroupKey(size uint64, typ string, speed int, mfr string) string {
	return fmt.Sprintf("%d|%s|%d|%s", size, typ, speed, mfr)
}

func appendUnique(s []string, val string) []string {
	for _, v := range s {
		if v == val {
			return s
		}
	}
	return append(s, val)
}

func inventoryMaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
