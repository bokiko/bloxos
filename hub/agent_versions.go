package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

/* ============================================================================
 * Agent version tracking (Phase 8)
 *
 * Three concerns:
 *   1. Compute and cache the SHA-256 of the agent binary we serve at
 *      /download/agent (recomputed when mtime changes).
 *   2. Track each connected agent's running version (per-machine).
 *   3. Circuit breaker — pauses the rollout after consecutive failed
 *      reconnects so a bad build doesn't take out the whole fleet.
 *
 * Public surface:
 *   - initAgentVersionTracking()      — start the recompute + reconnect loops
 *   - announceVersionToAgent()        — call after WS upgrade
 *   - recordAgentRunningVersion()     — call when agent reports its SHA
 *   - REST endpoints: GET /api/versions, POST /api/versions/{pause,resume}
 * ============================================================================ */

const (
	rolloutFailureThreshold = 2
	rolloutFailureWindow    = 5 * time.Minute
	reconnectExpectation    = 90 * time.Second
)

type agentVersionInfo struct {
	MachineID     string    `json:"machine_id"`
	Hostname      string    `json:"hostname"`
	RunningSHA    string    `json:"running_sha,omitempty"`
	ReportedAt    time.Time `json:"reported_at"`
	UpdatePending bool      `json:"update_pending"`
}

type rolloutFailure struct {
	machineID string
	at        time.Time
}

var (
	hubAgentBinarySHA   string
	hubAgentBinaryMtime time.Time
	hubAgentBinaryMu    sync.RWMutex

	agentRunningVersions   = make(map[string]agentVersionInfo)
	agentRunningVersionsMu sync.RWMutex

	rolloutFailures    []rolloutFailure
	rolloutFailuresMu  sync.Mutex
	rolloutPaused      bool
	rolloutPauseReason string
	rolloutPausedMu    sync.RWMutex

	pendingReconnects   = make(map[string]time.Time)
	pendingReconnectsMu sync.Mutex
)

// initAgentVersionTracking is called from main() at hub startup.
func initAgentVersionTracking() {
	go versionRefreshLoop()
	go reconnectMonitorLoop()
}

func versionRefreshLoop() {
	for {
		recomputeAgentBinarySHA()
		time.Sleep(10 * time.Second)
	}
}

func reconnectMonitorLoop() {
	for {
		time.Sleep(15 * time.Second)
		now := time.Now()

		pendingReconnectsMu.Lock()
		expired := []string{}
		for machineID, deadline := range pendingReconnects {
			if now.After(deadline) {
				expired = append(expired, machineID)
			}
		}
		for _, mid := range expired {
			log.Printf("rollout: %s did not reconnect within %s, marking as failure",
				mid, reconnectExpectation)
			delete(pendingReconnects, mid)
		}
		pendingReconnectsMu.Unlock()

		for _, mid := range expired {
			recordRolloutFailure(mid)
		}
	}
}

func recomputeAgentBinarySHA() {
	binaryPath := agentBinaryPath()
	if binaryPath == "" {
		return
	}

	info, err := os.Stat(binaryPath)
	if err != nil {
		return
	}

	hubAgentBinaryMu.RLock()
	cached := hubAgentBinaryMtime
	cachedSHA := hubAgentBinarySHA
	hubAgentBinaryMu.RUnlock()

	if info.ModTime().Equal(cached) && cachedSHA != "" {
		return
	}

	f, err := os.Open(binaryPath)
	if err != nil {
		return
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		log.Printf("version: hash error: %v", err)
		return
	}
	sha := hex.EncodeToString(h.Sum(nil))

	hubAgentBinaryMu.Lock()
	previousSHA := hubAgentBinarySHA
	hubAgentBinarySHA = sha
	hubAgentBinaryMtime = info.ModTime()
	hubAgentBinaryMu.Unlock()

	if previousSHA != "" && previousSHA != sha {
		log.Printf("version: agent binary changed (%s -> %s), update will propagate",
			versionShortSHA(previousSHA), versionShortSHA(sha))
		// Reset circuit breaker — a fresh build deserves a fresh rollout.
		rolloutFailuresMu.Lock()
		rolloutFailures = nil
		rolloutFailuresMu.Unlock()
		rolloutPausedMu.Lock()
		rolloutPaused = false
		rolloutPauseReason = ""
		rolloutPausedMu.Unlock()
	}
}

// agentBinaryPath mirrors the resolution in handleDownloadAgent so we hash
// the same file the agents will download.
func agentBinaryPath() string {
	candidates := []string{
		os.Getenv("BLOXOS_AGENT_BINARY"),
		"./agent/bloxos-agent",
		"/usr/local/bin/bloxos-agent",
	}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// announceVersionToAgent sends an "agent_version" frame to a freshly
// connected agent, unless rollout is paused.
func announceVersionToAgent(machineID string, agent *ConnectedAgent) {
	rolloutPausedMu.RLock()
	paused := rolloutPaused
	rolloutPausedMu.RUnlock()
	if paused {
		log.Printf("rollout: paused, not announcing version to %s", machineID)
		return
	}

	hubAgentBinaryMu.RLock()
	sha := hubAgentBinarySHA
	hubAgentBinaryMu.RUnlock()
	if sha == "" {
		return
	}

	msg := map[string]string{
		"type":   "agent_version",
		"sha256": sha,
	}
	data, _ := json.Marshal(msg)

	agent.WriteMu.Lock()
	err := agent.Conn.WriteMessage(websocket.TextMessage, data)
	agent.WriteMu.Unlock()
	if err != nil {
		log.Printf("rollout: failed to announce version to %s: %v", machineID, err)
		return
	}

	expectReconnect(machineID)
}

func expectReconnect(machineID string) {
	pendingReconnectsMu.Lock()
	defer pendingReconnectsMu.Unlock()
	pendingReconnects[machineID] = time.Now().Add(reconnectExpectation)
}

func clearReconnectExpectation(machineID string) {
	pendingReconnectsMu.Lock()
	defer pendingReconnectsMu.Unlock()
	delete(pendingReconnects, machineID)
}

// recordAgentRunningVersion is called when the agent sends its SHA on connect.
func recordAgentRunningVersion(machineID, runningSHA string) {
	hubAgentBinaryMu.RLock()
	hubSHA := hubAgentBinarySHA
	hubAgentBinaryMu.RUnlock()

	hostname := lookupVersionHostname(machineID)

	agentRunningVersionsMu.Lock()
	agentRunningVersions[machineID] = agentVersionInfo{
		MachineID:     machineID,
		Hostname:      hostname,
		RunningSHA:    runningSHA,
		ReportedAt:    time.Now(),
		UpdatePending: hubSHA != "" && runningSHA != hubSHA,
	}
	agentRunningVersionsMu.Unlock()

	if hubSHA != "" && runningSHA == hubSHA {
		clearReconnectExpectation(machineID)
		recordRolloutSuccess(machineID)
	}
}

func recordRolloutFailure(machineID string) {
	rolloutFailuresMu.Lock()
	now := time.Now()
	cutoff := now.Add(-rolloutFailureWindow)

	fresh := rolloutFailures[:0]
	for _, f := range rolloutFailures {
		if f.at.After(cutoff) {
			fresh = append(fresh, f)
		}
	}
	fresh = append(fresh, rolloutFailure{machineID: machineID, at: now})
	rolloutFailures = fresh
	failureCount := len(fresh)
	rolloutFailuresMu.Unlock()

	log.Printf("rollout: failure recorded for %s (%d in window)", machineID, failureCount)

	if failureCount >= rolloutFailureThreshold {
		rolloutPausedMu.Lock()
		if !rolloutPaused {
			rolloutPaused = true
			rolloutPauseReason = fmt.Sprintf(
				"circuit breaker: %d agents failed to reconnect within %s",
				failureCount, rolloutFailureWindow)
			log.Printf("rollout: PAUSED — %s", rolloutPauseReason)
		}
		rolloutPausedMu.Unlock()
	}
}

func recordRolloutSuccess(machineID string) {
	rolloutFailuresMu.Lock()
	defer rolloutFailuresMu.Unlock()
	fresh := rolloutFailures[:0]
	for _, f := range rolloutFailures {
		if f.machineID != machineID {
			fresh = append(fresh, f)
		}
	}
	rolloutFailures = fresh
}

func lookupVersionHostname(machineID string) string {
	var hostname string
	if err := db.QueryRow(`SELECT hostname FROM machines WHERE id = ?`, machineID).Scan(&hostname); err != nil {
		return machineID
	}
	return hostname
}

/* ============================================================================
 * REST endpoints
 * ============================================================================ */

func handleListVersions(c echo.Context) error {
	hubAgentBinaryMu.RLock()
	hubSHA := hubAgentBinarySHA
	hubMtime := hubAgentBinaryMtime
	hubAgentBinaryMu.RUnlock()

	agentRunningVersionsMu.RLock()
	versions := make([]agentVersionInfo, 0, len(agentRunningVersions))
	for _, v := range agentRunningVersions {
		v.Hostname = lookupVersionHostname(v.MachineID)
		v.UpdatePending = hubSHA != "" && v.RunningSHA != hubSHA
		versions = append(versions, v)
	}
	agentRunningVersionsMu.RUnlock()

	rolloutPausedMu.RLock()
	paused := rolloutPaused
	pauseReason := rolloutPauseReason
	rolloutPausedMu.RUnlock()

	return c.JSON(http.StatusOK, map[string]interface{}{
		"hub_sha":        hubSHA,
		"hub_short_sha":  versionShortSHA(hubSHA),
		"hub_mtime":      hubMtime.Format(time.RFC3339),
		"agents":         versions,
		"rollout_paused": paused,
		"pause_reason":   pauseReason,
	})
}

func handlePauseRollout(c echo.Context) error {
	rolloutPausedMu.Lock()
	rolloutPaused = true
	rolloutPauseReason = "manually paused by operator"
	rolloutPausedMu.Unlock()
	log.Printf("rollout: PAUSED by operator")
	return c.JSON(http.StatusOK, map[string]string{"status": "paused"})
}

func handleResumeRollout(c echo.Context) error {
	rolloutPausedMu.Lock()
	rolloutPaused = false
	rolloutPauseReason = ""
	rolloutPausedMu.Unlock()

	rolloutFailuresMu.Lock()
	rolloutFailures = nil
	rolloutFailuresMu.Unlock()

	log.Printf("rollout: RESUMED by operator")

	// Re-announce to all currently connected agents.
	agentsMu.RLock()
	conns := make([]*ConnectedAgent, 0, len(agents))
	ids := make([]string, 0, len(agents))
	for id, a := range agents {
		ids = append(ids, id)
		conns = append(conns, a)
	}
	agentsMu.RUnlock()

	for i := range conns {
		go announceVersionToAgent(ids[i], conns[i])
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "resumed"})
}

func versionShortSHA(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}
