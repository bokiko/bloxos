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
	// Arch is the CPU architecture the hub believes this agent runs on, in
	// GOARCH spelling ("amd64", "arm64"). Reported by the agent when it
	// sends one; otherwise inferred from the machine's metrics OS string;
	// otherwise empty, which announces the default (amd64) exactly as the
	// hub did before it knew about architectures.
	Arch string `json:"arch,omitempty"`
	// ArchReported is whether Arch came from the agent itself. An agent
	// that reports its arch also requests that arch on /download/agent; an
	// agent that does not predates both and downloads the default build
	// regardless of what the hub announces. That is why an inferred
	// non-default Arch must be withheld rather than announced: announcing
	// the arm64 SHA to a legacy arm64 agent makes it download amd64 bytes,
	// which the SHA check then correctly rejects — and if the hub instead
	// announced amd64, the agent would install a binary its CPU cannot run.
	ArchReported bool `json:"arch_reported"`
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
	// UpdateKeyPinned is whether the agent reported that it has a usable
	// pinned update key on disk. Only meaningful alongside UpdateProtocol
	// and UpdateTransportOK: an agent can be signature-capable and report a
	// usable transport yet still have no pinned key at all — it reached
	// this binary over the one unverifiable migration hop and its
	// installer has not been re-run yet. An agent built before this field
	// existed never reports it, and the zero value (false) is what makes
	// that safe: absent must mean "not pinned", never "assume yes".
	UpdateKeyPinned bool `json:"update_key_pinned"`
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
	// Arch is the agent's runtime.GOARCH. Empty for any agent built before
	// per-architecture delivery; such an agent downloads without ?arch=.
	Arch string
	// UpdateProtocol is 0 for any agent built before signature verification.
	UpdateProtocol int
	// TransportOK is the agent's own answer to "can I self-update over the
	// hub URL I am actually connected to". Meaningless when UpdateProtocol
	// is 0 — those agents have no transport gate.
	TransportOK bool
	// KeyPinned is the agent's own answer to "do I have a usable pinned
	// update key on disk". Meaningless when UpdateProtocol is 0. Absent
	// (zero value false) on any agent that predates this field.
	KeyPinned bool
}

type rolloutFailure struct {
	machineID string
	at        time.Time
}

var (
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
	// Populate both platform states synchronously so the first installer,
	// download, API read, or agent connection sees the same resolved paths.
	recomputeAgentBinarySHA()
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
// — a Linux amd64 build kicking that SHA does not reset the arm64 or
// Windows SHA (and vice-versa).
func recomputeAgentBinarySHA() {
	for _, platform := range supportedAgentPlatforms {
		recomputeBinaryForPlatform(platform)
	}
}

// recomputeBinaryFor refreshes an OS's default-architecture binary. Kept for
// pre-arch callers; per-arch paths use recomputeBinaryForArch.
func recomputeBinaryFor(osName string) {
	recomputeBinaryForArch(osName, defaultAgentArch)
}

// recomputeBinaryForArch refreshes one (os, arch). A platform the hub does
// not build for has no state to refresh; the caller sees the reason through
// currentAgentBinaryStateFor.
func recomputeBinaryForArch(osName, arch string) {
	platform, err := agentPlatformFor(osName, arch)
	if err != nil {
		return
	}
	recomputeBinaryForPlatform(platform)
}

func recomputeBinaryForPlatform(platform agentPlatform) {
	resolved, err := resolveAgentBinaryFor(platform.OS, platform.Arch)
	if err != nil {
		failAgentBinaryState(platform, resolved, err)
		return
	}
	info, err := os.Stat(resolved.Path)
	if err != nil {
		failAgentBinaryState(platform, resolved, fmt.Errorf("stat resolved path %s: %w", resolved.Path, err))
		return
	}
	cached := currentAgentBinaryStateFor(platform.OS, platform.Arch)
	if cached.Path == resolved.Path && cached.Source == resolved.Source &&
		info.ModTime().Equal(cached.Mtime) && cached.SHA != "" && cached.Error == "" {
		return
	}

	f, err := os.Open(resolved.Path)
	if err != nil {
		failAgentBinaryState(platform, resolved, fmt.Errorf("open resolved path %s: %w", resolved.Path, err))
		return
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		_ = f.Close()
		failAgentBinaryState(platform, resolved, fmt.Errorf("hash resolved path %s: %w", resolved.Path, err))
		return
	}
	if err := f.Close(); err != nil {
		failAgentBinaryState(platform, resolved, fmt.Errorf("close resolved path %s: %w", resolved.Path, err))
		return
	}
	sha := hex.EncodeToString(h.Sum(nil))

	state := agentBinaryState{Path: resolved.Path, Source: resolved.Source, SHA: sha, Mtime: info.ModTime()}
	previous := replaceAgentBinaryState(platform, state)
	if previous.Path != state.Path || previous.Source != state.Source || previous.Error != "" {
		log.Printf("version: %s agent binary resolved source=%s path=%s", platform, state.Source, state.Path)
	}
	for _, skipped := range resolved.Skipped {
		log.Printf("version: %s agent binary candidate skipped: %s", platform, skipped)
	}

	if previous.SHA != "" && previous.SHA != sha {
		log.Printf("version: %s agent binary changed (%s -> %s), update will propagate",
			platform, versionShortSHA(previous.SHA), versionShortSHA(sha))
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

// agentBinaryPathFor returns the exact cached path whose bytes produced the
// advertised SHA for an OS's default architecture. Hashing,
// detached-signature lookup, and downloads all use this state rather than
// resolving independently.
func agentBinaryPathFor(osName string) string {
	return agentBinaryPathForArch(osName, defaultAgentArch)
}

// agentBinaryPathForArch is agentBinaryPathFor for one (os, arch).
func agentBinaryPathForArch(osName, arch string) string {
	return currentAgentBinaryStateFor(osName, arch).Path
}

// announceVersionToAgent sends an "agent_version" frame to a connected agent,
// unless rollout is paused or announceDecision withholds it. The SHA
// announced is platform-specific: Windows agents get the Windows binary SHA,
// Linux agents get the SHA for the architecture the hub has recorded for
// them (amd64 when none is known, matching the arch-less download).
func (s *Server) announceVersionToAgent(machineID string, agent *ConnectedAgent) {
	rolloutPausedMu.RLock()
	paused := rolloutPaused
	rolloutPausedMu.RUnlock()
	if paused {
		log.Printf("rollout: paused, not announcing version to %s", machineID)
		return
	}

	// One read of the map, reused for the arch, the already-up-to-date
	// check and the policy below. announceDecision takes no locks by
	// design, so the caller is the only place that touches
	// agentRunningVersionsMu.
	agentRunningVersionsMu.RLock()
	v, hadVersion := agentRunningVersions[machineID]
	agentRunningVersionsMu.RUnlock()

	osName := s.lookupAgentOS(machineID)
	sha := announcedSHAForArch(osName, v.Arch)
	if sha == "" {
		return
	}

	// If we already know the agent's running SHA matches what we'd
	// announce, skip both the message AND the reconnect-expectation
	// timer. The agent's handleAgentVersion would silently no-op the
	// announce (matching SHAs), so arming a 90s reconnect timer for a
	// reconnect that will never come just trips the rollout circuit
	// breaker on healthy fleets every time the hub restarts.
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
	arch := defaultAgentArch
	if report != nil && report.Arch != "" {
		arch = report.Arch
	}
	if sha == "" {
		if osName == "" {
			return "", "the hub has not yet learned this agent's operating system"
		}
		platform := normalizeAgentOS(osName) + "/" + arch
		if state := currentAgentBinaryStateFor(osName, arch); state.Error != "" {
			return "", fmt.Sprintf("the hub is not serving a %s agent binary: %s", platform, state.Error)
		}
		return "", fmt.Sprintf("the hub is not serving a %s agent binary", platform)
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

	// Architecture. An agent that never reported its arch also never asks
	// for one on /download/agent, so whatever the hub announces, it fetches
	// the default (amd64) build. On an amd64 host that is the right answer
	// and announcing keeps today's fleet updating unchanged. On a host whose
	// metrics say otherwise it is never right: announce arm64 and the agent
	// downloads amd64 bytes it then rejects on SHA; announce amd64 and it
	// installs a binary its CPU cannot execute, which is the systemd
	// "Exec format error" crash loop. The only safe move is to withhold
	// and say why, until the installer is re-run with an arch-aware agent.
	if normalizeAgentOS(osName) == "linux" && !report.ArchReported && arch != defaultAgentArch {
		return "", fmt.Sprintf("agent_arch_not_reported: this agent predates per-architecture updates and "+
			"would download the %s build, but the machine reports %s; re-run the hub installer on this host",
			defaultAgentArch, arch)
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
		// Signature-capable and transport-OK are not the same as ready. An
		// agent reaches this binary over the one unverifiable migration hop
		// (the branch below, for pre-signature agents) before its installer
		// has ever pinned a key — so on every fleet that existed before this
		// gate, this is the *default* state immediately after the hub picks
		// up a signature-capable build, not an edge case. Announcing to it
		// anyway arms the same 90s reconnect expectation for the same
		// reconnect that will never come. agent_key_not_pinned is a
		// distinct reason (not folded into the plaintext-transport message
		// above) so an operator watching the rollout can tell "waiting on
		// an installer re-run" apart from "agent is unhealthy" — the two
		// look identical in update_pending alone.
		if !report.UpdateKeyPinned {
			return "", "agent_key_not_pinned: the agent has not reported a usable pinned update key; " +
				"re-run the hub installer on this host to enroll it"
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
	sig = announcedSignatureForArch(osName, arch, sha)
	if sig == "" {
		if enabled, reason := updateSigningStatus(); !enabled {
			return "", "the hub cannot sign updates: " + reason
		}
		return "", fmt.Sprintf("no valid signature is available for the %s/%s agent binary the hub "+
			"is serving — re-sign it, or check <binary>.sig is current", normalizeAgentOS(osName), arch)
	}
	return sig, ""
}

// announcedSHAFor returns the SHA the hub will announce for an OS's default
// architecture. Unknown or empty OS values stay unannounced until the agent
// reports its OS; sending the wrong platform's SHA would cause a perpetual
// update loop.
func announcedSHAFor(osName string) string {
	return announcedSHAForArch(osName, defaultAgentArch)
}

// announcedSHAForArch is announcedSHAFor for one (os, arch). An empty arch
// means the default, which is what an agent that reports no arch downloads.
// An architecture the hub does not serve yields "", so nothing is announced
// and announceDecision reports why.
func announcedSHAForArch(osName, arch string) string {
	switch strings.ToLower(strings.TrimSpace(osName)) {
	case "windows", "linux":
		return currentAgentBinaryStateFor(osName, arch).SHA
	default:
		return ""
	}
}

// lookupAgentArch infers a machine's CPU architecture from the OS string its
// agent reported in metrics, for agents that predate the arch field in
// agent_running_version. Two formats exist: "linux/arm64" from older
// agents, and "ubuntu 24.04 (aarch64)" from host-info-reporting ones.
// Returns "" when the string carries no recognisable architecture.
func (s *Server) lookupAgentArch(machineID string) string {
	osStr := strings.ToLower(s.lookupMetricsOS(machineID))
	if osStr == "" {
		return ""
	}
	if idx := indexByte(osStr, '/'); idx > 0 {
		if arch, ok := normalizeAgentArch(osStr[idx+1:]); ok {
			return arch
		}
	}
	for _, spelling := range []string{"x86_64", "amd64", "aarch64", "arm64"} {
		if strings.Contains(osStr, spelling) {
			arch, _ := normalizeAgentArch(spelling)
			return arch
		}
	}
	return ""
}

// lookupAgentOS returns the recorded OS for a machine ("linux",
// "windows", or "" if unknown / pre-Phase-9 agent).
func (s *Server) lookupAgentOS(machineID string) string {
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
	osStr := s.lookupMetricsOS(machineID)
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
func (s *Server) lookupMetricsOS(machineID string) string {
	var osStr string
	if err := s.db.QueryRow(`SELECT COALESCE(os, '') FROM machines WHERE id = ?`, machineID).Scan(&osStr); err != nil {
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
func (s *Server) recordAgentRunningVersion(machineID string, report agentVersionReport) {
	runningSHA, osName := report.RunningSHA, report.OS
	// Resolve the per-OS expected SHA. If we don't yet know the agent's
	// OS, lookupAgentOS will check the DB metrics for it. New-on-this-hub
	// agents return "" here and that's fine — we'll announce after we
	// learn the OS below.
	if osName == "" {
		osName = s.lookupAgentOS(machineID)
	}
	// The arch the agent reports wins; it is what its updater will request.
	// A legacy agent reports none, so infer it from the metrics OS string —
	// not to announce for it, but so announceDecision can refuse to send a
	// legacy arm64 agent an update it would fetch as amd64.
	arch := strings.ToLower(strings.TrimSpace(report.Arch))
	archReported := arch != ""
	if archReported {
		arch, _ = normalizeAgentArch(arch)
	} else {
		arch = s.lookupAgentArch(machineID)
	}
	expectedSHA := announcedSHAForArch(osName, arch)

	hostname := s.lookupVersionHostname(machineID)

	agentRunningVersionsMu.Lock()
	prev, hadPrev := agentRunningVersions[machineID]
	agentRunningVersions[machineID] = agentVersionInfo{
		MachineID:         machineID,
		Hostname:          hostname,
		RunningSHA:        runningSHA,
		ReportedAt:        time.Now(),
		UpdatePending:     expectedSHA != "" && runningSHA != expectedSHA,
		OS:                osName,
		Arch:              arch,
		ArchReported:      archReported,
		UpdateProtocol:    report.UpdateProtocol,
		UpdateTransportOK: report.TransportOK,
		UpdateKeyPinned:   report.KeyPinned,
	}
	agentRunningVersionsMu.Unlock()
	log.Printf("rollout: agent reported running=%s expected=%s os=%s arch=%s proto=%d pending=%v machine=%s",
		versionShortSHA(runningSHA), versionShortSHA(expectedSHA), osName, arch, report.UpdateProtocol,
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
	// The capability, transport, and key-pinned fields are part of the same
	// story: at WS-upgrade time this frame had not arrived, so
	// announceDecision saw no report at all and withheld. Learning any of
	// them — including an agent's installer finally getting re-run, which
	// only changes UpdateKeyPinned — is exactly as much a reason to
	// re-announce as learning the OS is.
	if osName != "" && (!hadPrev || prev.OS != osName || prev.Arch != arch ||
		prev.UpdateProtocol != report.UpdateProtocol ||
		prev.UpdateTransportOK != report.TransportOK ||
		prev.UpdateKeyPinned != report.KeyPinned) &&
		expectedSHA != "" && runningSHA != expectedSHA {
		s.agentsMu.RLock()
		agent, online := s.agents[machineID]
		s.agentsMu.RUnlock()
		if online {
			s.goTracked(func() { s.announceVersionToAgent(machineID, agent) })
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

func (s *Server) lookupVersionHostname(machineID string) string {
	var hostname string
	if err := s.db.QueryRow(`SELECT hostname FROM machines WHERE id = ?`, machineID).Scan(&hostname); err != nil {
		return machineID
	}
	return hostname
}

/* ============================================================================
 * REST endpoints
 * ============================================================================ */

func (s *Server) handleListVersions(c echo.Context) error {
	linuxState := currentAgentBinaryState("linux")
	windowsState := currentAgentBinaryState("windows")

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
		v.Hostname = s.lookupVersionHostname(v.MachineID)
		expected := announcedSHAForArch(v.OS, v.Arch)
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

	// The pre-arch fields keep their meaning — "linux" is the amd64 (or
	// legacy-path) binary, exactly what an arch-less download serves — so
	// the Versions dashboard keeps working. agent_binaries_by_arch carries
	// every platform the hub tracks.
	byArch := map[string]map[string]agentBinaryState{}
	for _, platform := range supportedAgentPlatforms {
		if byArch[platform.OS] == nil {
			byArch[platform.OS] = map[string]agentBinaryState{}
		}
		byArch[platform.OS][platform.Arch] = currentAgentBinaryStateFor(platform.OS, platform.Arch)
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"signing_enabled":         signingEnabled,
		"signing_disabled_reason": signingDisabledReason,
		"hub_sha":                 linuxState.SHA,
		"hub_short_sha":           versionShortSHA(linuxState.SHA),
		"hub_mtime":               linuxState.Mtime.Format(time.RFC3339),
		"hub_windows_sha":         windowsState.SHA,
		"hub_windows_short_sha":   versionShortSHA(windowsState.SHA),
		"hub_windows_mtime":       windowsState.Mtime.Format(time.RFC3339),
		"agent_binaries": map[string]agentBinaryState{
			"linux":   linuxState,
			"windows": windowsState,
		},
		"agent_binaries_by_arch": byArch,
		"agents":                 versions,
		"rollout_paused":         paused,
		"pause_reason":           pauseReason,
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

func (s *Server) handleResumeRollout(c echo.Context) error {
	rolloutPausedMu.Lock()
	rolloutPaused = false
	rolloutPauseReason = ""
	rolloutPausedMu.Unlock()

	rolloutFailuresMu.Lock()
	rolloutFailures = nil
	rolloutFailuresMu.Unlock()

	log.Printf("rollout: RESUMED by operator")

	// Re-announce to all currently connected agents.
	s.agentsMu.RLock()
	conns := make([]*ConnectedAgent, 0, len(s.agents))
	ids := make([]string, 0, len(s.agents))
	for id, a := range s.agents {
		ids = append(ids, id)
		conns = append(conns, a)
	}
	s.agentsMu.RUnlock()

	for i := range conns {
		mid, conn := ids[i], conns[i]
		s.goTracked(func() { s.announceVersionToAgent(mid, conn) })
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "resumed"})
}

func versionShortSHA(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}
