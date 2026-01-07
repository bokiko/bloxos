package collector

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// MinerStats holds stats from a running miner
type MinerStats struct {
	Name      string  `json:"name"`
	Version   string  `json:"version"`
	Running   bool    `json:"running"`
	Algorithm string  `json:"algorithm"`
	Pool      string  `json:"pool"`
	Hashrate  float64 `json:"hashrate"`  // Total hashrate in H/s
	Shares    struct {
		Accepted int `json:"accepted"`
		Rejected int `json:"rejected"`
	} `json:"shares"`
	Uptime    int           `json:"uptime"` // Seconds
	GPUStats  []GPUMinerStats `json:"gpuStats,omitempty"`
}

// GPUMinerStats holds per-GPU stats from a miner
type GPUMinerStats struct {
	Index      int     `json:"index"`
	Hashrate   float64 `json:"hashrate"`
	Temperature int    `json:"temperature"`
	FanSpeed   int     `json:"fanSpeed"`
	Power      int     `json:"power"`
}

// Known miner processes and their API ports
var minerAPIs = map[string]struct {
	processes []string
	port      int
	apiType   string // "http" or "ccminer"
}{
	"t-rex":          {[]string{"t-rex"}, 4067, "http"},
	"lolminer":       {[]string{"lolMiner", "lolminer"}, 4068, "http"},
	"gminer":         {[]string{"miner", "gminer"}, 4069, "http"},
	"teamredminer":   {[]string{"teamredminer"}, 4070, "http"},
	"xmrig":          {[]string{"xmrig"}, 4071, "http"},
	"nbminer":        {[]string{"nbminer"}, 4072, "http"},
	"srbminer":       {[]string{"SRBMiner-MULTI", "srbminer-multi"}, 4073, "http"},
	"bzminer":        {[]string{"bzminer"}, 4074, "http"},
}

// DetectRunningMiner detects which miner is currently running
func (c *Collector) DetectRunningMiner() *MinerStats {
	for minerName, info := range minerAPIs {
		for _, procName := range info.processes {
			// Check if process is running
			cmd := exec.Command("pgrep", "-x", procName)
			if err := cmd.Run(); err == nil {
				// Process found, try to get stats from API
				stats := c.getMinerStats(minerName, info.port)
				if stats != nil {
					return stats
				}
				
				// Process running but API not responding
				return &MinerStats{
					Name:    minerName,
					Running: true,
				}
			}
		}
	}

	// Also check via /proc for any miner-like processes
	return c.detectMinerFromProc()
}

// getMinerStats fetches stats from a miner's HTTP API
func (c *Collector) getMinerStats(minerName string, port int) *MinerStats {
	client := &http.Client{Timeout: 2 * time.Second}
	
	switch minerName {
	case "t-rex":
		return c.getTrexStats(client, port)
	case "lolminer":
		return c.getLolMinerStats(client, port)
	case "gminer":
		return c.getGMinerStats(client, port)
	case "teamredminer":
		return c.getTeamRedMinerStats(client, port)
	case "xmrig":
		return c.getXMRigStats(client, port)
	case "nbminer":
		return c.getNBMinerStats(client, port)
	case "srbminer":
		return c.getSRBMinerStats(client, port)
	default:
		return nil
	}
}

// getTrexStats fetches T-Rex miner stats
func (c *Collector) getTrexStats(client *http.Client, port int) *MinerStats {
	url := fmt.Sprintf("http://127.0.0.1:%d/summary", port)
	resp, err := client.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	
	var data struct {
		Name      string  `json:"name"`
		Version   string  `json:"version"`
		Algorithm string  `json:"algorithm"`
		Hashrate  float64 `json:"hashrate"`
		Uptime    int     `json:"uptime"`
		Accepted  int     `json:"accepted_count"`
		Rejected  int     `json:"rejected_count"`
		Pool      struct {
			URL string `json:"url"`
		} `json:"active_pool"`
		GPUs []struct {
			DeviceID    int     `json:"device_id"`
			Hashrate    float64 `json:"hashrate"`
			Temperature int     `json:"temperature"`
			Fan         int     `json:"fan_speed"`
			Power       int     `json:"power"`
		} `json:"gpus"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return nil
	}

	stats := &MinerStats{
		Name:      "t-rex",
		Version:   data.Version,
		Running:   true,
		Algorithm: data.Algorithm,
		Pool:      data.Pool.URL,
		Hashrate:  data.Hashrate,
		Uptime:    data.Uptime,
	}
	stats.Shares.Accepted = data.Accepted
	stats.Shares.Rejected = data.Rejected

	for _, gpu := range data.GPUs {
		stats.GPUStats = append(stats.GPUStats, GPUMinerStats{
			Index:       gpu.DeviceID,
			Hashrate:    gpu.Hashrate,
			Temperature: gpu.Temperature,
			FanSpeed:    gpu.Fan,
			Power:       gpu.Power,
		})
	}

	return stats
}

// getLolMinerStats fetches lolMiner stats
func (c *Collector) getLolMinerStats(client *http.Client, port int) *MinerStats {
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	resp, err := client.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var data struct {
		Software string  `json:"Software"`
		Mining   struct {
			Algorithm string `json:"Algorithm"`
		} `json:"Mining"`
		Session struct {
			Uptime            int `json:"Uptime"`
			AcceptedShares    int `json:"Accepted"`
			SubmittedShares   int `json:"Submitted"`
		} `json:"Session"`
		Stratum struct {
			Current_Pool string `json:"Current_Pool"`
		} `json:"Stratum"`
		GPUs []struct {
			Index       int     `json:"Index"`
			Performance float64 `json:"Performance"`
			Temp        int     `json:"Temp (deg C)"`
			Fan         int     `json:"Fan Speed (%)"`
			Power       int     `json:"Power (W)"`
		} `json:"GPUs"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return nil
	}

	var totalHashrate float64
	for _, gpu := range data.GPUs {
		totalHashrate += gpu.Performance
	}

	stats := &MinerStats{
		Name:      "lolminer",
		Version:   data.Software,
		Running:   true,
		Algorithm: data.Mining.Algorithm,
		Pool:      data.Stratum.Current_Pool,
		Hashrate:  totalHashrate * 1000000, // Convert to H/s
		Uptime:    data.Session.Uptime,
	}
	stats.Shares.Accepted = data.Session.AcceptedShares
	stats.Shares.Rejected = data.Session.SubmittedShares - data.Session.AcceptedShares

	for _, gpu := range data.GPUs {
		stats.GPUStats = append(stats.GPUStats, GPUMinerStats{
			Index:       gpu.Index,
			Hashrate:    gpu.Performance * 1000000,
			Temperature: gpu.Temp,
			FanSpeed:    gpu.Fan,
			Power:       gpu.Power,
		})
	}

	return stats
}

// getGMinerStats fetches GMiner stats
func (c *Collector) getGMinerStats(client *http.Client, port int) *MinerStats {
	url := fmt.Sprintf("http://127.0.0.1:%d/stat", port)
	resp, err := client.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var data struct {
		Miner     string `json:"miner"`
		Algorithm string `json:"algorithm"`
		Uptime    int    `json:"uptime"`
		Server    string `json:"server"`
		Devices   []struct {
			GPUId       int     `json:"gpu_id"`
			Speed       float64 `json:"speed"`
			Temperature int     `json:"temperature"`
			Fan         int     `json:"fan"`
			Power       int     `json:"power_usage"`
		} `json:"devices"`
		TotalSpeed     float64 `json:"total_speed"`
		AcceptedShares int     `json:"total_accepted_shares"`
		RejectedShares int     `json:"total_rejected_shares"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return nil
	}

	stats := &MinerStats{
		Name:      "gminer",
		Version:   data.Miner,
		Running:   true,
		Algorithm: data.Algorithm,
		Pool:      data.Server,
		Hashrate:  data.TotalSpeed,
		Uptime:    data.Uptime,
	}
	stats.Shares.Accepted = data.AcceptedShares
	stats.Shares.Rejected = data.RejectedShares

	for _, gpu := range data.Devices {
		stats.GPUStats = append(stats.GPUStats, GPUMinerStats{
			Index:       gpu.GPUId,
			Hashrate:    gpu.Speed,
			Temperature: gpu.Temperature,
			FanSpeed:    gpu.Fan,
			Power:       gpu.Power,
		})
	}

	return stats
}

// getTeamRedMinerStats fetches TeamRedMiner stats
func (c *Collector) getTeamRedMinerStats(client *http.Client, port int) *MinerStats {
	url := fmt.Sprintf("http://127.0.0.1:%d/summary", port)
	resp, err := client.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var data struct {
		Version   string `json:"version"`
		Algorithm string `json:"algo"`
		Uptime    int    `json:"uptime"`
		Pool      string `json:"pool"`
		Hashrate  float64 `json:"hashrate"`
		Accepted  int     `json:"accepted"`
		Rejected  int     `json:"rejected"`
		GPUs      []struct {
			Index    int     `json:"id"`
			Hashrate float64 `json:"hashrate"`
			Temp     int     `json:"temp"`
			Fan      int     `json:"fan"`
			Power    int     `json:"power"`
		} `json:"gpus"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return nil
	}

	stats := &MinerStats{
		Name:      "teamredminer",
		Version:   data.Version,
		Running:   true,
		Algorithm: data.Algorithm,
		Pool:      data.Pool,
		Hashrate:  data.Hashrate,
		Uptime:    data.Uptime,
	}
	stats.Shares.Accepted = data.Accepted
	stats.Shares.Rejected = data.Rejected

	for _, gpu := range data.GPUs {
		stats.GPUStats = append(stats.GPUStats, GPUMinerStats{
			Index:       gpu.Index,
			Hashrate:    gpu.Hashrate,
			Temperature: gpu.Temp,
			FanSpeed:    gpu.Fan,
			Power:       gpu.Power,
		})
	}

	return stats
}

// getXMRigStats fetches XMRig stats
func (c *Collector) getXMRigStats(client *http.Client, port int) *MinerStats {
	url := fmt.Sprintf("http://127.0.0.1:%d/1/summary", port)
	resp, err := client.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var data struct {
		Version string `json:"version"`
		Algo    string `json:"algo"`
		Uptime  int    `json:"uptime"`
		Connection struct {
			Pool string `json:"pool"`
		} `json:"connection"`
		Hashrate struct {
			Total []float64 `json:"total"`
		} `json:"hashrate"`
		Results struct {
			Accepted int `json:"shares_good"`
			Rejected int `json:"shares_total"`
		} `json:"results"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return nil
	}

	var hashrate float64
	if len(data.Hashrate.Total) > 0 {
		hashrate = data.Hashrate.Total[0]
	}

	stats := &MinerStats{
		Name:      "xmrig",
		Version:   data.Version,
		Running:   true,
		Algorithm: data.Algo,
		Pool:      data.Connection.Pool,
		Hashrate:  hashrate,
		Uptime:    data.Uptime,
	}
	stats.Shares.Accepted = data.Results.Accepted
	stats.Shares.Rejected = data.Results.Rejected - data.Results.Accepted

	return stats
}

// getNBMinerStats fetches NBMiner stats
func (c *Collector) getNBMinerStats(client *http.Client, port int) *MinerStats {
	url := fmt.Sprintf("http://127.0.0.1:%d/api/v1/status", port)
	resp, err := client.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var data struct {
		Version string `json:"version"`
		Miner   struct {
			Devices []struct {
				ID          int     `json:"id"`
				Hashrate    string  `json:"hashrate_raw"`
				Temperature int     `json:"temperature"`
				Fan         int     `json:"fan"`
				Power       int     `json:"power"`
			} `json:"devices"`
			TotalHashrate string `json:"total_hashrate_raw"`
		} `json:"miner"`
		Stratum struct {
			Algorithm string `json:"algorithm"`
			URL       string `json:"url"`
			Accepted  int    `json:"accepted_shares"`
			Rejected  int    `json:"rejected_shares"`
		} `json:"stratum"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return nil
	}

	hashrate, _ := strconv.ParseFloat(data.Miner.TotalHashrate, 64)

	stats := &MinerStats{
		Name:      "nbminer",
		Version:   data.Version,
		Running:   true,
		Algorithm: data.Stratum.Algorithm,
		Pool:      data.Stratum.URL,
		Hashrate:  hashrate,
	}
	stats.Shares.Accepted = data.Stratum.Accepted
	stats.Shares.Rejected = data.Stratum.Rejected

	for _, gpu := range data.Miner.Devices {
		hr, _ := strconv.ParseFloat(gpu.Hashrate, 64)
		stats.GPUStats = append(stats.GPUStats, GPUMinerStats{
			Index:       gpu.ID,
			Hashrate:    hr,
			Temperature: gpu.Temperature,
			FanSpeed:    gpu.Fan,
			Power:       gpu.Power,
		})
	}

	return stats
}

// getSRBMinerStats fetches SRBMiner stats
func (c *Collector) getSRBMinerStats(client *http.Client, port int) *MinerStats {
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	resp, err := client.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var data struct {
		Version   string `json:"version"`
		Algorithm string `json:"algorithm"`
		Uptime    int    `json:"uptime_minutes"`
		Pool      string `json:"pool"`
		Hashrate  struct {
			Total float64 `json:"total"`
		} `json:"hashrate"`
		Shares struct {
			Accepted int `json:"accepted"`
			Rejected int `json:"rejected"`
		} `json:"shares"`
		Devices []struct {
			ID          int     `json:"id"`
			Hashrate    float64 `json:"hashrate"`
			Temperature int     `json:"temperature"`
			Fan         int     `json:"fan_speed_rpm"`
			Power       int     `json:"power"`
		} `json:"devices"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return nil
	}

	stats := &MinerStats{
		Name:      "srbminer",
		Version:   data.Version,
		Running:   true,
		Algorithm: data.Algorithm,
		Pool:      data.Pool,
		Hashrate:  data.Hashrate.Total,
		Uptime:    data.Uptime * 60,
	}
	stats.Shares.Accepted = data.Shares.Accepted
	stats.Shares.Rejected = data.Shares.Rejected

	for _, gpu := range data.Devices {
		stats.GPUStats = append(stats.GPUStats, GPUMinerStats{
			Index:       gpu.ID,
			Hashrate:    gpu.Hashrate,
			Temperature: gpu.Temperature,
			FanSpeed:    gpu.Fan,
			Power:       gpu.Power,
		})
	}

	return stats
}

// detectMinerFromProc checks /proc for miner processes
func (c *Collector) detectMinerFromProc() *MinerStats {
	// Use pgrep to find common miner processes
	miners := []string{"t-rex", "lolMiner", "gminer", "teamredminer", "xmrig", "nbminer", "SRBMiner", "bzminer", "phoenixminer", "claymore"}
	
	for _, miner := range miners {
		cmd := exec.Command("pgrep", "-f", miner)
		output, err := cmd.Output()
		if err == nil && len(strings.TrimSpace(string(output))) > 0 {
			return &MinerStats{
				Name:    strings.ToLower(miner),
				Running: true,
			}
		}
	}

	return nil
}
