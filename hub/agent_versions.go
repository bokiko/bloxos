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
	"strings"
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
	// UpdateProtocol is the agent's self-update capability level. 0 means
	// the agent never reported one, i.e. a binary built before signature
	// verification existed, which cannot check what the hub announces.
	UpdateProtocol int `json:"update_protocol"`
	// UpdateTransportOK is what the agent itself said about whether its
	// BLOXOS_HUB permits self-update. Only meaningful when UpdateProtocol
	// >= 1; older agents have no transport gate and never report it.
	//
	// This exists because PUBLIC_URL is the hub *guessing* at the agent's
	// transport. In a mixed deployment — PUBLIC_URL https, some agents
	// hand-configured with ws:// — the guess is wrong for exactly the
	// machines that will refuse, and nothing else distinguishes them.
	UpdateTransportOK bool `json:"update_transport_ok"`
	// UpdateBlockedReason explains, when UpdatePending is true, why the hub
	// is not announcing the update. Empty means nothing is blocking it.
	// Without this an operator cannot tell a rollout in progress from a
	// rollout that will never happen — both show update_pending on every
	// agent, indefinitely.
	UpdateBlockedReason string `json:"update_blocked_reason,omitempty"`
}

// agentVersionReport is what an agent tells the hub about itself in an
// agent_running_version frame. A struct rather than positional parameters:
// the fields are two strings, an int and a bool, which is precisely the
// shape that goes wrong silently at a call site.
type agentVersionReport struct {
	RunningSHA string
	OS         string
	// UpdateProtocol is 0 for any agent built before signature verification.
	UpdateProtocol int
	// TransportOK is the agent's own answer to "can I self-update over the
	// hub URL I am actually connected to". Meaningless when UpdateProtocol
	// is 0 — those agents have no transport gate.
	TransportOK bool
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
	goSafelyForever("versionRefreshLoop", versionRefreshLoop)
	goSafelyForever("reconnectMonitorLoop", reconnectMonitorLoop)
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

// announceVersionToAgent sends an "agent_version" frame to a connected agent,
// unless rollout is paused or announceDecision withholds it. The SHA
// announced is platform-specific: Windows agents get the Windows binary SHA,
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

	// If we already know the agent's running SHA matches what we'd
	// announce, skip both the message AND the reconnect-expectation
	// timer. The agent's handleAgentVersion would silently no-op the
	// announce (matching SHAs), so arming a 90s reconnect timer for a
	// reconnect that will never come just trips the rollout circuit
	// breaker on healthy fleets every time the hub restarts.
	// One read of the map, reused for both the already-up-to-date check and
	// the policy below. announceDecision takes no locks by design, so the
	// caller is the only place that touches agentRunningVersionsMu.
	agentRunningVersionsMu.RLock()
	v, hadVersion := agentRunningVersions[machineID]
	agentRunningVersionsMu.RUnlock()
	if hadVersion && v.RunningSHA == sha {
		return
	}

	var report *agentVersionInfo
	if hadVersion {
		report = &v
	}
	sig, blocked := announceDecision(report, osName, sha)
	if blocked != "" {
		log.Printf("rollout: not announcing to %s — %s", machineID, blocked)
		return
	}

	msg := map[string]string{
		"type":      "agent_version",
		"sha256":    sha,
		"signature": sig,
		"sig_alg":   "ed25519",
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

// announceDecision is the single place that decides whether an update may be
// announced to a machine. It returns the signature to send, or a
// human-readable reason it is being withheld.
//
// Both announceVersionToAgent and handleListVersions go through here. Every
// withholding path in this PR is fail-closed and silent apart from a log
// line, and on the dashboard a fleet that has stopped updating because the
// hub cannot sign looks exactly like a rollout in progress: update_pending
// true on every agent, forever. Sharing this function is what keeps "why the
// API says nothing is happening" and "why nothing is actually happening" from
// drifting apart.
//
// Rollout pause is deliberately not handled here — it is fleet-wide and
// already reported as rollout_paused / pause_reason.
//
// It acquires NO locks and reads no shared maps: everything about the agent
// arrives in `report`, which is nil when the hub has never heard from that
// machine. Callers pass the record they already read.
//
// That is deliberate, not stylistic. The previous version looked the machine
// up itself, so handleListVersions — which iterates agentRunningVersions
// under RLock — re-entered the same RWMutex. sync.RWMutex does not permit
// recursive read locking: a writer queueing between the outer and inner
// acquisition blocks the reader, which never releases, which blocks the
// writer. recordAgentRunningVersion runs on the agent WebSocket read path,
// so the whole rollout subsystem wedges until the hub restarts, on nothing
// more exotic than a dashboard poll racing an agent reconnect. Codex found
// it; keeping this function lock-free is what makes it unrepeatable.
func announceDecision(report *agentVersionInfo, osName, sha string) (sig string, blockedBecause string) {
	if sha == "" {
		if osName == "" {
			return "", "the hub has not yet learned this agent's operating system"
		}
		return "", fmt.Sprintf("the hub is not serving a %s agent binary", osName)
	}

	// Nothing has been heard from this agent yet. announceVersionToAgent is
	// called at WS-upgrade time, before agent_running_version arrives, so on
	// a machine we have never seen we know neither its capability nor its
	// transport. Falling back to PUBLIC_URL here would announce to exactly
	// the mixed-deployment agent that is going to refuse. Every agent sends
	// agent_running_version on connect, and recordAgentRunningVersion
	// re-triggers the announce once it lands, so waiting costs one round
	// trip and removes the guess.
	if report == nil {
		return "", "the hub has not yet received agent_running_version from this agent"
	}

	// Transport. This gates every agent, for two different reasons, and the
	// signal differs by what the agent is able to tell us.
	//
	// A signature-capable agent computed the answer itself at connect time
	// from its actual BLOXOS_HUB and reported it. Announcing to one that
	// says no achieves nothing except arming a reconnect expectation for a
	// reconnect that never comes — which expires into a rollout failure and,
	// at two machines, pauses the whole fleet, with rolloutPauseReason
	// blaming agent health for a refusal the hub provoked. The rule is
	// already stated twenty lines up for the matching-SHA case: never arm
	// the timer for an update you can predict won't produce a reconnect.
	//
	// A pre-signature agent has no transport gate and cannot verify what we
	// send it either, so the reason there is security rather than futility:
	// that one unverifiable migration hop must only happen where an off-host
	// attacker cannot reach it. Nothing to ask the agent, so fall back to
	// PUBLIC_URL — the hub's own declaration of how the fleet reaches it.
	if report.signatureCapable() {
		if !report.transportPermitsUpdate() {
			return "", "the agent reports that the hub URL it is connected to is plaintext, so it " +
				"will refuse to self-update; point that agent's BLOXOS_HUB at a wss:// address"
		}
	} else if ok, why := updateTransportUsable(); !ok {
		return "", fmt.Sprintf("agent has not reported a signature-capable version and %s; "+
			"re-run the hub installer on this host to enroll it with a pinned update key", why)
	}

	// The agent refuses any announcement it cannot authenticate against the
	// key its installer pinned. If we cannot produce a signature there is no
	// update to be had — announcing anyway would only arm the 90s
	// reconnect-expectation timer for a reconnect that never comes, and trip
	// the rollout circuit breaker on a healthy fleet.
	sig = announcedSignatureFor(osName, sha)
	if sig == "" {
		if enabled, reason := updateSigningStatus(); !enabled {
			return "", "the hub cannot sign updates: " + reason
		}
		return "", fmt.Sprintf("no valid signature is available for the %s agent binary the hub "+
			"is serving — re-sign it, or check <binary>.sig is current", osName)
	}
	return sig, ""
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
	case "linux":
		return hubAgentBinarySHA
	default:
		// Unknown/empty OS — don't announce a SHA at all. Same logic as
		// the Windows protective comment above: announcing the wrong-OS
		// SHA loops the agent through perpetual self-update. The legacy
		// "fall back to Linux SHA for pre-Phase-9 agents" behavior is
		// dropped because (a) every agent in the fleet now sends an `os`
		// field with agent_running_version, and (b) lookupMetricsOS
		// recovers the OS from DB-stored metrics for any agent that has
		// previously sent metrics. recordAgentRunningVersion triggers a
		// fresh announce as soon as the OS is learned, so brand-new
		// agents still get auto-update propagation — just deferred from
		// WS-upgrade time to first-message-arrival time.
		return ""
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
	// Fall back to the OS string stored in machines.os. Two formats exist:
	//   1. Legacy "linux/amd64" / "windows/amd64" — agent_running_version
	//      messages from older agents that explicitly sent GOOS/GOARCH.
	//   2. Hardware-reported strings populated by upsertMachine from agent
	//      metrics, e.g. "ubuntu 24.04 (x86_64)" or
	//      "Microsoft Windows 10 Pro 22H2 (x86_64)".
	// Normalise both into the "linux"/"windows" family that announcedSHAFor
	// expects. Returning the un-normalised string would cause the default
	// switch arm to fire and suppress the announce — exactly the
	// regression that surfaced after the Phase-9 unknown-OS protection.
	osStr := lookupMetricsOS(machineID)
	if osStr == "" {
		return ""
	}
	if idx := indexByte(osStr, '/'); idx > 0 {
		return osStr[:idx]
	}
	lower := strings.ToLower(osStr)
	if strings.Contains(lower, "windows") {
		return "windows"
	}
	if strings.Contains(lower, "linux") ||
		strings.Contains(lower, "ubuntu") ||
		strings.Contains(lower, "debian") ||
		strings.Contains(lower, "fedora") ||
		strings.Contains(lower, "centos") ||
		strings.Contains(lower, "rhel") ||
		strings.Contains(lower, "arch") ||
		strings.Contains(lower, "alpine") ||
		strings.Contains(lower, "suse") ||
		strings.Contains(lower, "raspbian") {
		return "linux"
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
//
// The report carries the agent's capability level and its own answer on
// whether its transport permits self-update. See announceDecision.
func recordAgentRunningVersion(machineID string, report agentVersionReport) {
	runningSHA, osName := report.RunningSHA, report.OS
	// Resolve the per-OS expected SHA. If we don't yet know the agent's
	// OS, lookupAgentOS will check the DB metrics for it. New-on-this-hub
	// agents return "" here and that's fine — we'll announce after we
	// learn the OS below.
	if osName == "" {
		osName = lookupAgentOS(machineID)
	}
	expectedSHA := announcedSHAFor(osName)

	hostname := lookupVersionHostname(machineID)

	agentRunningVersionsMu.Lock()
	prev, hadPrev := agentRunningVersions[machineID]
	agentRunningVersions[machineID] = agentVersionInfo{
		MachineID:         machineID,
		Hostname:          hostname,
		RunningSHA:        runningSHA,
		ReportedAt:        time.Now(),
		UpdatePending:     expectedSHA != "" && runningSHA != expectedSHA,
		OS:                osName,
		UpdateProtocol:    report.UpdateProtocol,
		UpdateTransportOK: report.TransportOK,
	}
	agentRunningVersionsMu.Unlock()
	log.Printf("rollout: agent reported running=%s expected=%s os=%s proto=%d pending=%v machine=%s",
		versionShortSHA(runningSHA), versionShortSHA(expectedSHA), osName, report.UpdateProtocol,
		expectedSHA != "" && runningSHA != expectedSHA, machineID)

	if expectedSHA != "" && runningSHA == expectedSHA {
		clearReconnectExpectation(machineID)
		recordRolloutSuccess(machineID)
	}

	// If we just learned (or relearned) the OS, the first-connect announce
	// was suppressed because OS was unknown at WS-upgrade time. Trigger
	// one now — but only when there's an actual pending update. Announcing
	// to an already-up-to-date agent makes it silently no-op (the agent's
	// handleAgentVersion compares SHAs and returns when they match), but
	// announceVersionToAgent unconditionally arms a 90s reconnect-expectation
	// timer that then fires a false-positive "rollout failure" log and
	// counts toward the circuit breaker. Skip the announce when the agent
	// is already on the announced SHA.
	//
	// The capability check is part of the same story: at WS-upgrade time we
	// had not seen this frame yet, so agentIsSignatureCapable was false and
	// the legacy gate may have suppressed the announce. Learning the
	// protocol level is exactly as much a reason to re-announce as learning
	// the OS is.
	if osName != "" && (!hadPrev || prev.OS != osName ||
		prev.UpdateProtocol != report.UpdateProtocol ||
		prev.UpdateTransportOK != report.TransportOK) &&
		expectedSHA != "" && runningSHA != expectedSHA {
		agentsMu.RLock()
		agent, online := agents[machineID]
		agentsMu.RUnlock()
		if online {
			go announceVersionToAgent(machineID, agent)
		}
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

	// Snapshot under the lock and do all the work outside it. Two reasons:
	// announceDecision must never be reached with agentRunningVersionsMu
	// already held (see its doc comment), and lookupVersionHostname is a DB
	// query — holding a lock the agent-WS path writes to across one is a
	// latency hazard on its own.
	agentRunningVersionsMu.RLock()
	versions := make([]agentVersionInfo, 0, len(agentRunningVersions))
	for _, v := range agentRunningVersions {
		versions = append(versions, v)
	}
	agentRunningVersionsMu.RUnlock()

	for i := range versions {
		v := &versions[i]
		v.Hostname = lookupVersionHostname(v.MachineID)
		expected := announcedSHAFor(v.OS)
		v.UpdatePending = expected != "" && v.RunningSHA != expected
		if v.UpdatePending {
			// Same policy announceVersionToAgent applies, so what the API
			// reports and what the hub actually does cannot drift.
			_, v.UpdateBlockedReason = announceDecision(v, v.OS, expected)
		}
	}

	rolloutPausedMu.RLock()
	paused := rolloutPaused
	pauseReason := rolloutPauseReason
	rolloutPausedMu.RUnlock()

	signingEnabled, signingDisabledReason := updateSigningStatus()

	return c.JSON(http.StatusOK, map[string]interface{}{
		"signing_enabled":         signingEnabled,
		"signing_disabled_reason": signingDisabledReason,
		"hub_sha":                 hubSHA,
		"hub_short_sha":           versionShortSHA(hubSHA),
		"hub_mtime":               hubMtime.Format(time.RFC3339),
		"hub_windows_sha":         hubWindowsSHA,
		"hub_windows_short_sha":   versionShortSHA(hubWindowsSHA),
		"hub_windows_mtime":       hubWindowsMtime.Format(time.RFC3339),
		"agents":                  versions,
		"rollout_paused":          paused,
		"pause_reason":            pauseReason,
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
