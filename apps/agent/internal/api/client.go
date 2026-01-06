package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/bloxos/agent/internal/collector"
)

// Client communicates with the BloxOs server
type Client struct {
	serverURL  string
	token      string
	httpClient *http.Client
}

// ReportPayload is the data sent to the server
type ReportPayload struct {
	Token      string               `json:"token"`
	SystemInfo *collector.SystemInfo `json:"systemInfo,omitempty"`
	GPUs       []collector.GPUStats  `json:"gpus,omitempty"`
	CPU        *collector.CPUStats   `json:"cpu,omitempty"`
	Timestamp  time.Time            `json:"timestamp"`
}

// CommandResponse is the response from the server
type CommandResponse struct {
	Success bool   `json:"success"`
	Command string `json:"command,omitempty"` // Command to execute (start_miner, stop_miner, etc.)
	Config  struct {
		FlightSheetID string `json:"flightSheetId,omitempty"`
		GPUEnabled    bool   `json:"gpuEnabled"`
		CPUEnabled    bool   `json:"cpuEnabled"`
	} `json:"config,omitempty"`
}

// New creates a new API client
func New(serverURL, token string) *Client {
	return &Client{
		serverURL: serverURL,
		token:     token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Register registers the rig with the server
func (c *Client) Register(sysInfo *collector.SystemInfo) error {
	payload := map[string]interface{}{
		"token":    c.token,
		"hostname": sysInfo.Hostname,
		"os":       sysInfo.OS,
		"osVersion": sysInfo.OSVersion,
	}

	_, err := c.post("/api/agent/register", payload)
	return err
}

// ReportStats sends stats to the server
func (c *Client) ReportStats(payload *ReportPayload) (*CommandResponse, error) {
	payload.Token = c.token
	payload.Timestamp = time.Now()

	body, err := c.post("/api/agent/report", payload)
	if err != nil {
		return nil, err
	}

	var resp CommandResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &resp, nil
}

// Heartbeat sends a simple heartbeat
func (c *Client) Heartbeat() error {
	payload := map[string]interface{}{
		"token": c.token,
	}
	_, err := c.post("/api/agent/heartbeat", payload)
	return err
}

// post sends a POST request
func (c *Client) post(path string, payload interface{}) ([]byte, error) {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	url := c.serverURL + path
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("server error %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}
