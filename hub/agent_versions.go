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
	// OS is the operating system the agent reported running on
	// ("linux", "windows"). Empty for legacy agents that predate
	// per-platform tracking — those fall back to the linux SHA.
	OS string `json:"os,omitempty"`
}

type rolloutFailure struct {
	machineID string
	at        time.Time
}

var (
	hubAgentBinarySHA   string
	hubAgentBinaryMtime time.Time
	// Phase 9 — separate Windows binary SHA so a Windows agent doesn't
	// mistake the Linux SHA for "out of date" and update-loop forever.
	hubWindowsAgentBinarySHA   string
	hubWindowsAgentBinaryMtime time.Time
	hubAgentBinaryMu           sync.RWMutex

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

// recomputeAgentBinarySHA refreshes the cached SHA for every supported
// platform's agent binary. Each platform is mtime-cached independently
// — a Linux build kicking the Linux SHA does not reset the Windows
// SHA (and vice-versa).
func recomputeAgentBinarySHA() {
	recomputeBinaryFor("linux")
	recomputeBinaryFor("windows")
}

func recomputeBinaryFor(osName string) {
	binaryPath := agentBinaryPathFor(osName)
	if binaryPath == "" {
		return
	}

	info, err := os.Stat(binaryPath)
	if err != nil {
		return
	}

	hubAgentBinaryMu.RLock()
	var cached time.Time
	var cachedSHA string
	switch osName {
	case "windows":
		cached = hubWindowsAgentBinaryMtime
		cachedSHA = hubWindowsAgentBinarySHA
	default:
		cached = hubAgentBinaryMtime
		cachedSHA = hubAgentBinarySHA
	}
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
		log.Printf("version: hash error (%s): %v", osName, err)
		return
	}
	sha := hex.EncodeToString(h.Sum(nil))

	hubAgentBinaryMu.Lock()
	var previousSHA string
	switch osName {
	case "windows":
		previousSHA = hubWindowsAgentBinarySHA
		hubWindowsAgentBinarySHA = sha
		hubWindowsAgentBinaryMtime = info.ModTime()
	default:
		previousSHA = hubAgentBinarySHA
		hubAgentBinarySHA = sha
		hubAgentBinaryMtime = info.ModTime()
	}
	hubAgentBinaryMu.Unlock()

	if previousSHA != "" && previousSHA != sha {
		log.Printf("version: %s agent binary changed (%s -> %s), update will propagate",
			osName, versionShortSHA(previousSHA), versionShortSHA(sha))
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

// agentBinaryPath returns the path to the Linux agent binary. Kept for
// backwards compatibility — production callers should prefer
// agentBinaryPathFor.
func agentBinaryPath() string {
	return agentBinaryPathFor("linux")
}

// agentBinaryPathFor returns the on-disk path of the agent binary for
// the given OS, mirroring the resolution in handleDownloadAgent.
func agentBinaryPathFor(osName string) string {
	var candidates []string
	switch osName {
	case "windows":
		candidates = []string{
			os.Getenv("BLOXOS_AGENT_BINARY_WINDOWS"),
			"./agent/bloxos-agent.exe",
			"/usr/local/bin/bloxos-agent.exe",
		}
	default:
		candidates = []string{
			os.Getenv("BLOXOS_AGENT_BINARY"),
			"./agent/bloxos-agent",
			"/usr/local/bin/bloxos-agent",
		}
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
// connected agent, unless rollout is paused. The SHA announced is
// platform-specific: Windows agents get the Windows binary SHA,
// Linux/unknown agents get the Linux SHA.
func announceVersionToAgent(machineID string, agent *ConnectedAgent) {
	rolloutPausedMu.RLock()
	paused := rolloutPaused
	rolloutPausedMu.RUnlock()
	if paused {
		log.Printf("rollout: paused, not announcing version to %s", machineID)
		return
	}

	osName := lookupAgentOS(machineID)
	sha := announcedSHAFor(osName)
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

// announcedSHAFor returns the SHA the hub will announce to an agent of
// the given OS. Falls back to the linux SHA for unknown / empty OS so
// pre-Phase-9 agents still see something coherent.
func announcedSHAFor(osName string) string {
	hubAgentBinaryMu.RLock()
	defer hubAgentBinaryMu.RUnlock()
	switch osName {
	case "windows":
		if hubWindowsAgentBinarySHA != "" {
			return hubWindowsAgentBinarySHA
		}
		// Don't announce a Linux SHA to a Windows agent — that would
		// trigger a perpetual update loop. Better to stay quiet.
		return ""
	default:
		return hubAgentBinarySHA
	}
}

// lookupAgentOS returns the recorded OS for a machine ("linux",
// "windows", or "" if unknown / pre-Phase-9 agent).
func lookupAgentOS(machineID string) string {
	agentRunningVersionsMu.RLock()
	v, ok := agentRunningVersions[machineID]
	agentRunningVersionsMu.RUnlock()
	if ok && v.OS != "" {
		return v.OS
	}
	// Fall back to the metrics OS string if we have one. Format is
	// "linux/amd64" / "windows/amd64". Split on "/".
	if osStr := lookupMetricsOS(machineID); osStr != "" {
		if idx := indexByte(osStr, '/'); idx > 0 {
			return osStr[:idx]
		}
		return osStr
	}
	return ""
}

// indexByte is a tiny helper to keep this file free of strings imports.
func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// lookupMetricsOS returns the most recently reported `os` string from
// the metrics table for a given machine, e.g. "linux/amd64". Returns
// empty if not found.
func lookupMetricsOS(machineID string) string {
	var osStr string
	if err := db.QueryRow(`SELECT COALESCE(os, '') FROM machines WHERE id = ?`, machineID).Scan(&osStr); err != nil {
		return ""
	}
	return osStr
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
// osName is the agent's reported OS ("linux" / "windows" / ""). For pre-Phase-9
// agents that don't include the os field, an empty string is fine — the OS is
// inferred lazily from the machine's metrics OS string.
func recordAgentRunningVersion(machineID, runningSHA, osName string) {
	// Resolve the per-OS expected SHA. If we don't yet know the agent's
	// OS (legacy agent), treat the linux SHA as authoritative.
	if osName == "" {
		osName = lookupAgentOS(machineID)
	}
	expectedSHA := announcedSHAFor(osName)

	hostname := lookupVersionHostname(machineID)

	agentRunningVersionsMu.Lock()
	agentRunningVersions[machineID] = agentVersionInfo{
		MachineID:     machineID,
		Hostname:      hostname,
		RunningSHA:    runningSHA,
		ReportedAt:    time.Now(),
		UpdatePending: expectedSHA != "" && runningSHA != expectedSHA,
		OS:            osName,
	}
	agentRunningVersionsMu.Unlock()

	if expectedSHA != "" && runningSHA == expectedSHA {
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
	hubWindowsSHA := hubWindowsAgentBinarySHA
	hubWindowsMtime := hubWindowsAgentBinaryMtime
	hubAgentBinaryMu.RUnlock()

	agentRunningVersionsMu.RLock()
	versions := make([]agentVersionInfo, 0, len(agentRunningVersions))
	for _, v := range agentRunningVersions {
		v.Hostname = lookupVersionHostname(v.MachineID)
		expected := announcedSHAFor(v.OS)
		v.UpdatePending = expected != "" && v.RunningSHA != expected
		versions = append(versions, v)
	}
	agentRunningVersionsMu.RUnlock()

	rolloutPausedMu.RLock()
	paused := rolloutPaused
	pauseReason := rolloutPauseReason
	rolloutPausedMu.RUnlock()

	return c.JSON(http.StatusOK, map[string]interface{}{
		"hub_sha":               hubSHA,
		"hub_short_sha":         versionShortSHA(hubSHA),
		"hub_mtime":             hubMtime.Format(time.RFC3339),
		"hub_windows_sha":       hubWindowsSHA,
		"hub_windows_short_sha": versionShortSHA(hubWindowsSHA),
		"hub_windows_mtime":     hubWindowsMtime.Format(time.RFC3339),
		"agents":                versions,
		"rollout_paused":        paused,
		"pause_reason":          pauseReason,
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
