package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

// GPUInfo holds per-GPU metrics parsed from nvidia-smi XML.
type GPUInfo struct {
	Index         int     `json:"index"`
	Name          string  `json:"name"`
	TempC         float64 `json:"temp_c"`
	UtilPercent   float64 `json:"util_percent"`
	MemUsedBytes  int64   `json:"mem_used_bytes"`
	MemTotalBytes int64   `json:"mem_total_bytes"`
	PowerWatts    float64 `json:"power_watts"`
	FanPercent    float64 `json:"fan_percent"`
}

type Metrics struct {
	Type           string    `json:"type"`
	MachineID      string    `json:"machine_id"`
	Hostname       string    `json:"hostname"`
	IP             string    `json:"ip,omitempty"`
	OS             string    `json:"os,omitempty"`
	CPUPercent     float64   `json:"cpu_percent"`
	CPUTempC       float64   `json:"cpu_temp_c,omitempty"`
	RAMUsedBytes   uint64    `json:"ram_used_bytes"`
	RAMTotalBytes  uint64    `json:"ram_total_bytes"`
	DiskUsedBytes  uint64    `json:"disk_used_bytes"`
	DiskTotalBytes uint64    `json:"disk_total_bytes"`
	GPUs           []GPUInfo `json:"gpus"`
	Timestamp      string    `json:"timestamp"`
	SentAt         string    `json:"sent_at,omitempty"`
}

// Command is received from the hub.
type Command struct {
	Type      string `json:"type"`
	Target    string `json:"target"`
	ID        string `json:"id"`
	SessionID string `json:"session_id,omitempty"`
}

// CommandResponse is sent back to the hub.
type CommandResponse struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Error   string `json:"error"`
}

// ServiceInfo represents a discovered systemd service.
type ServiceInfo struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Description string `json:"description"`
}

// ServicesMessage is sent to the hub.
type ServicesMessage struct {
	Type      string        `json:"type"`
	Hostname  string        `json:"hostname"`
	MachineID string        `json:"machine_id"`
	Services  []ServiceInfo `json:"services"`
}

// ContainerInfo represents a discovered Docker container.
type ContainerInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Image  string `json:"image"`
}

// ContainersMessage is sent to the hub.
type ContainersMessage struct {
	Type       string          `json:"type"`
	Hostname   string          `json:"hostname"`
	MachineID  string          `json:"machine_id"`
	Containers []ContainerInfo `json:"containers"`
}

// TerminalResize is sent from browser -> hub -> agent to resize the PTY.
type TerminalResize struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

var (
	hubURL      string
	token       string
	agentSecret string

	// tokenCleanupOnce gates the post-bootstrap BLOXOS_TOKEN wipe to once
	// per process. After a service restart this runs again — which is correct:
	// if the credential file still exists, the wipe is idempotent (env var is
	// already gone). If the credential file is missing, the wipe condition
	// guards us. Implementation is platform-specific; see main_<os>.go.
	tokenCleanupOnce sync.Once

	// validTarget allows alphanumeric, hyphens, underscores, dots only.
	validTarget = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

	// allowedCommands lists the command types we accept.
	allowedCommands = map[string]bool{
		"restart_service":   true,
		"stop_service":      true,
		"start_service":     true,
		"restart_container": true,
		"reboot":            true,
		"shutdown":          true,
		"start_terminal":    true,
		"start_container":   true,
		"refresh_metrics":   true,
	}

	// interestingServicePatterns are services we report to the hub.
	interestingServicePatterns = []string{
		"docker", "ollama", "nginx", "caddy", "redis", "postgres", "mysql",
		"mongo", "node", "python", "gunicorn", "uvicorn", "flask", "grafana",
		"prometheus", "netdata", "ssh", "ufw", "fail2ban", "cron", "bloxos",
	}
)

// credentialFilePath returns the path where the agent stores its durable secret.
func credentialFilePath() string {
	// Prefer /etc/bloxos/agent-secret if running as root.
	if u, err := user.Current(); err == nil && u.Uid == "0" {
		return "/etc/bloxos/agent-secret"
	}
	// Otherwise use ~/.bloxos/agent-secret.
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".bloxos", "agent-secret")
}

// loadCredentialFile reads the agent secret from the credential file.
func loadCredentialFile() string {
	path := credentialFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// saveCredentialFile durably writes the agent secret to the credential file
// via writeCredentialFileAtomic (temp file + rename), so a crash mid-write
// never leaves a partial credential on disk. On POSIX this yields real
// 0600 owner-only permissions; on Windows, protection instead comes from
// the file living in the protected systemprofile credential directory,
// whose ACL is inherited rather than expressed as POSIX permission bits.
// The directory itself stays 0755 (POSIX) so the install-time curl
// (running as the invoking user) can traverse it to read
// /etc/bloxos/ca.crt.
func saveCredentialFile(secret string) error {
	path := credentialFilePath()
	if err := writeCredentialFileAtomic(path, secret); err != nil {
		return err
	}
	log.Printf("agent secret saved to %s", path)
	return nil
}

// loadPendingCredentialFile reads a staged-but-unconfirmed secret, or ""
// if none is pending.
func loadPendingCredentialFile() string {
	data, err := os.ReadFile(pendingCredentialPath(credentialFilePath()))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// savePendingCredentialFile durably writes secret to the PENDING path only
// — see handleEnrolledFrame / handleEnrollmentConfirmed for why the active
// credential file must never be touched here.
func savePendingCredentialFile(secret string) error {
	path := pendingCredentialPath(credentialFilePath())
	if err := writeCredentialFileAtomic(path, secret); err != nil {
		return err
	}
	log.Printf("staged pending agent secret at %s (awaiting hub confirmation)", path)
	return nil
}

// defaultCredentialConfirmDeps wires handleEnrollmentConfirmed to the real
// filesystem: the active path, the pending path beside it, and the same
// durableRename primitive (see durable_rename_*.go) that
// defaultCredentialFileWriter uses for the temp -> pending commit — one
// shared durable rename/replace implementation per platform, not two
// subtly different ones.
func defaultCredentialConfirmDeps() credentialConfirmDeps {
	activePath := credentialFilePath()
	pendingPath := pendingCredentialPath(activePath)
	return credentialConfirmDeps{
		hashActive:  func() (string, error) { return hashCredentialFile(activePath) },
		hashPending: func() (string, error) { return hashCredentialFile(pendingPath) },
		promote: func() error {
			if err := durableRename(pendingPath, activePath); err != nil {
				return err
			}
			log.Printf("promoted pending agent secret to active at %s", activePath)
			return nil
		},
		removePending: func() error {
			err := os.Remove(pendingPath)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			return nil
		},
	}
}

// caCertFilePath returns the path where the agent expects an additional trusted CA.
func caCertFilePath() (string, bool) {
	if env := os.Getenv("BLOXOS_CA_CERT"); env != "" {
		return env, true
	}
	// Prefer /etc/bloxos/ca.crt if running as root.
	if u, err := user.Current(); err == nil && u.Uid == "0" {
		return "/etc/bloxos/ca.crt", false
	}
	// Otherwise use ~/.bloxos/ca.crt.
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".bloxos", "ca.crt"), false
}

func loadRootCAs() (*x509.CertPool, string, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}

	path, explicit := caCertFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && !explicit {
			return pool, "", nil
		}
		return nil, "", fmt.Errorf("read CA cert %s: %w", path, err)
	}
	if ok := pool.AppendCertsFromPEM(data); !ok {
		return nil, "", fmt.Errorf("parse CA cert %s: no certificates found", path)
	}
	return pool, path, nil
}

func websocketDialerFor(rawURL string) (*websocket.Dialer, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid websocket URL: %w", err)
	}

	dialer := *websocket.DefaultDialer
	// Phase 8 update flow reuses this for HTTPS downloads (`/download/agent`),
	// not just the WS dial. Both wss:// and https:// need the same TLS config
	// (system roots + optional BLOXOS_CA_CERT); without this, the auto-update
	// HTTP fetch failed with "x509: certificate signed by unknown authority"
	// and the rollout circuit breaker tripped. The config is built by
	// buildAgentTLSConfig, whose release build always verifies (see
	// tls_secure.go / tls_insecure.go).
	if u.Scheme != "wss" && u.Scheme != "https" {
		return &dialer, nil
	}

	tlsConfig, err := buildAgentTLSConfig(u.Host)
	if err != nil {
		return nil, err
	}

	dialer.TLSClientConfig = tlsConfig
	return &dialer, nil
}

func main() {
	var (
		installSvc   bool
		uninstallSvc bool
	)
	flag.StringVar(&hubURL, "hub", "ws://localhost:4000/ws/agent", "Hub WebSocket URL")
	flag.StringVar(&token, "token", "", "Registration token")
	flag.StringVar(&agentSecret, "secret", "", "Agent secret for reconnection")
	flag.BoolVar(&installSvc, "install-service", false, "Install as a Windows service and exit (Windows only)")
	flag.BoolVar(&uninstallSvc, "uninstall-service", false, "Uninstall the Windows service and exit (Windows only)")
	flag.Parse()

	if installSvc {
		if err := platformInstallService(); err != nil {
			log.Fatalf("install-service: %v", err)
		}
		return
	}
	if uninstallSvc {
		if err := platformUninstallService(); err != nil {
			log.Fatalf("uninstall-service: %v", err)
		}
		return
	}

	// Env var fallback.
	if hubURL == "ws://localhost:4000/ws/agent" {
		if env := os.Getenv("BLOXOS_HUB"); env != "" {
			hubURL = env + "/ws/agent"
		}
	}
	if agentSecret == "" {
		if env := os.Getenv("BLOXOS_SECRET"); env != "" {
			agentSecret = env
		}
	}
	if token == "" {
		if env := os.Getenv("BLOXOS_TOKEN"); env != "" {
			token = env
		}
	}

	// Priority: 1) --secret / BLOXOS_SECRET / credential file, 2) --token / BLOXOS_TOKEN
	if agentSecret == "" {
		agentSecret = loadCredentialFile()
	}
	if agentSecret == "" && token == "" {
		log.Fatal("--token or --secret is required (or set BLOXOS_TOKEN / BLOXOS_SECRET)")
	}

	if agentSecret != "" {
		log.Printf("using agent secret for authentication")
	} else {
		log.Printf("using install token for initial enrollment")
	}

	// Establish the release floor from the binary that is actually running,
	// before any connection and before Windows applies a pending update.
	// Never fatal: a broken floor disables self-update, not the agent.
	seedReleaseFloorAtBoot()

	runPlatformAgent()
}

// WebSocket liveness tuning. pingPeriod must be shorter than pongWait so a
// missed pong is noticed within one interval; writeWait bounds every write so
// a stuck send fails fast and triggers reconnect instead of blocking forever.
const (
	agentPongWait   = 70 * time.Second
	agentPingPeriod = 30 * time.Second
	agentWriteWait  = 10 * time.Second
)

// stableConnThreshold is how long a connection must stay up before the agent
// treats it as healthy and resets its reconnect backoff to the base delay.
const stableConnThreshold = 2 * time.Minute

// backoffAfterConn returns the backoff to carry into the next reconnect sleep.
// A connection that stayed up longer than stableConnThreshold is considered
// healthy, so the backoff resets to base; otherwise the current backoff is
// preserved for the caller's exponential progression. Without this reset an
// agent that accumulated a long backoff early keeps a ~60s reconnect delay for
// the rest of its life, even after hours of stable uptime.
func backoffAfterConn(current, base, uptime time.Duration) time.Duration {
	if uptime > stableConnThreshold {
		return base
	}
	return current
}

// authOutcome classifies why a runAgent connection attempt ended, so
// connectLoop can tell a definitive credential rejection (the hub's HTTP
// 401 on the handshake itself) apart from every other kind of failure —
// transport/TLS errors, a later read/write error, or a clean disconnect.
// Only a definitive rejection of a PENDING credential should make
// connectLoop fall back to the active credential immediately, without
// waiting out the normal backoff; any other outcome just means "try the
// same candidate again" (an abandoned/unreachable hub must never cause the
// agent to discard a credential it never actually got to test).
type authOutcome int

const (
	authOutcomeUnknown authOutcome = iota
	authOutcomeRejected
)

// classifyDialAuthOutcome inspects a failed WebSocket handshake and reports
// whether it was a definitive credential rejection (HTTP 401 from the hub,
// meaning the hub actually evaluated and refused the credential) versus
// anything else (network unreachable, TLS failure, timeout — none of which
// mean the credential itself is bad). Split out from runAgent so this
// classification is unit-testable without a real socket.
func classifyDialAuthOutcome(resp *http.Response, dialErr error) authOutcome {
	if dialErr != nil && resp != nil && resp.StatusCode == http.StatusUnauthorized {
		return authOutcomeRejected
	}
	return authOutcomeUnknown
}

// shouldFallBackToActive decides whether connectLoop should immediately
// retry with the active credential — skipping the normal backoff — after a
// connection attempt that used the pending credential. Only a definitive
// rejection of the pending credential, with an active credential actually
// available to fall back to, qualifies: a transport/TLS failure must never
// trigger this (the hub never got to evaluate the credential at all), and
// there is nothing to fall back to if no active credential exists.
func shouldFallBackToActive(usedPending bool, outcome authOutcome, haveActive bool) bool {
	return usedPending && outcome == authOutcomeRejected && haveActive
}

// credentialSelection is what one connectLoop iteration decided to dial
// with.
type credentialSelection struct {
	secret      string
	usedPending bool
}

// selectCredentialCandidate is connectLoop's per-iteration candidate
// decision, factored out as a pure function of what's currently on disk
// PLUS what the hub most recently, definitively rejected — comparing by the
// secret's VALUE, not a one-shot bool. A bool ("give up on pending") would
// either get re-armed every iteration (since the pending file itself is
// never deleted merely by a rejection — see handleEnrollmentConfirmed,
// which only ever removes it via a hash-bound confirmation) — recreating an
// instant pending→401→pending loop — or, if latched forever, would wrongly
// keep ignoring a GENUINELY NEW pending secret staged by a fresh
// re-enrollment attempt after the old one was abandoned. Comparing values
// solves both: the same still-rejected pending file is skipped on every
// subsequent iteration until it changes or disappears, while a different
// pending value (fresh attempt) is tried immediately.
func selectCredentialCandidate(pending, active, rejectedPending string) credentialSelection {
	if pending != "" && pending != rejectedPending {
		return credentialSelection{secret: pending, usedPending: true}
	}
	if active != "" {
		return credentialSelection{secret: active, usedPending: false}
	}
	if pending != "" {
		// The only credential that exists at all is the one already known
		// to be rejected — requirement is to retain and keep retrying it
		// (with the normal backoff) rather than have nothing to dial with.
		return credentialSelection{secret: pending, usedPending: true}
	}
	return credentialSelection{secret: "", usedPending: false}
}

// nextRejectedPendingSecret computes the "known-rejected pending secret"
// value connectLoop carries into its next iteration, given this iteration's
// selection and outcome:
//   - a definitive rejection of a pending-backed attempt marks that exact
//     secret as rejected, so selectCredentialCandidate skips it (in favor of
//     active, if available) on every subsequent iteration until it changes;
//   - once there is no pending file at all, there is nothing left to avoid,
//     so tracking is cleared — this also means a brand new pending secret
//     staged later starts unrejected, as it must;
//   - any other outcome (a successful connection, a transport/TLS failure,
//     a rejection while using the active credential) leaves the tracked
//     value untouched, so an active-fallback session that hits a transport
//     failure keeps retrying active rather than reverting to the
//     already-rejected pending candidate.
func nextRejectedPendingSecret(current string, sel credentialSelection, outcome authOutcome, pendingOnDisk string) string {
	if sel.usedPending && outcome == authOutcomeRejected {
		return sel.secret
	}
	if pendingOnDisk == "" {
		return ""
	}
	return current
}

func connectLoop(machineID string) {
	backoff := time.Second
	maxBackoff := 60 * time.Second

	// rejectedPendingSecret persists ACROSS loop iterations — see
	// nextRejectedPendingSecret's doc for why a value, not a bool.
	var rejectedPendingSecret string

	for {
		// On reconnect, a staged-but-unconfirmed PENDING credential is tried
		// FIRST unless it is the exact value already known-rejected by the
		// hub: it is either the not-yet-promoted result of a re-enrollment
		// whose "enrollment_confirmed" never arrived — in which case the hub
		// recognizes and promotes it (see validateAgentSecret) — or it is
		// abandoned/expired, in which case the hub rejects it and this loop
		// falls back to the still-valid active credential, below, WITHOUT
		// re-selecting the same rejected pending value merely because its
		// file is still on disk (see selectCredentialCandidate).
		pending := loadPendingCredentialFile()
		active := loadCredentialFile()
		sel := selectCredentialCandidate(pending, active, rejectedPendingSecret)
		agentSecret = sel.secret

		start := time.Now()
		outcome, err := runAgent(machineID)
		if err != nil {
			log.Printf("connection error: %v", err)
		}

		// A definitive rejection of the PENDING credential specifically
		// means it is abandoned or expired hub-side — falling back to the
		// still-valid active credential must not wait out the normal
		// backoff, or a machine whose re-enrollment attempt lapsed would sit
		// disconnected for up to maxBackoff before recovering on its own.
		// Any other outcome (transport/TLS failure, a later read/write
		// error, or a rejection of the active credential itself) gets the
		// normal backoff+retry with the same candidate — in particular, a
		// transport failure must never be treated as a credential rejection
		// merely because the network happened to be unavailable.
		immediateRetry := shouldFallBackToActive(sel.usedPending, outcome, active != "")
		rejectedPendingSecret = nextRejectedPendingSecret(rejectedPendingSecret, sel, outcome, pending)
		if immediateRetry {
			log.Printf("pending credential rejected by hub — falling back to active credential")
			continue
		}

		// A connection that stayed up long enough to be "stable" resets the
		// backoff so a later brief blip doesn't inherit a long delay.
		backoff = backoffAfterConn(backoff, time.Second, time.Since(start))

		jitter := time.Duration(rand.Int63n(int64(backoff) / 2))
		wait := backoff + jitter
		log.Printf("reconnecting in %s", wait)
		time.Sleep(wait)

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func runAgent(machineID string) (authOutcome, error) {
	u, err := url.Parse(hubURL)
	if err != nil {
		return authOutcomeUnknown, fmt.Errorf("invalid hub URL: %w", err)
	}

	// Send whichever credentials we have via handshake headers rather than the
	// URL query string, so they don't land in reverse-proxy access logs. The
	// hub tries the secret first (if present) and falls back to the token (if
	// also present) when the secret fails validation. This handles credential
	// drift cleanly: a stale agent-secret + fresh enrollment token re-enrolls
	// instead of looping with an invalid secret. See hub/agentws.go::
	// handleAgentWS for the fallback contract: secret error → agentSecret = ""
	// → drop into the token-mode branch.
	header := http.Header{}
	if agentSecret != "" {
		header.Set("Authorization", "Bearer "+agentSecret)
	}
	if token != "" {
		header.Set("X-Bloxos-Enroll-Token", token)
	}

	log.Printf("connecting to %s", hubURL)
	dialer, err := websocketDialerFor(u.String())
	if err != nil {
		return authOutcomeUnknown, fmt.Errorf("build websocket dialer: %w", err)
	}
	conn, dialResp, err := dialer.Dial(u.String(), header)
	if err != nil {
		return classifyDialAuthOutcome(dialResp, err), fmt.Errorf("dial failed: %w", err)
	}
	defer conn.Close()
	log.Println("connected to hub")

	// An ordinary reconnect (durable secret, no token in flight) is exactly
	// the case where BLOXOS_TOKEN is provably no longer needed right now — no
	// enrollment is in progress on this connection at all. When a token IS
	// also present, a fresh or targeted re-enrollment may still be pending;
	// cleanup is deferred to the "enrolled" handler instead (see
	// handleEnrolledFrame) so a legitimate retry never loses the token to a
	// premature wipe before enrollment actually completes.
	maybeCleanupTokenOnReconnect(agentSecret != "", token != "", func() {
		tokenCleanupOnce.Do(wipeMachineTokenIfBootstrapped)
	})

	// Mutex for concurrent writes to WebSocket.
	var writeMu sync.Mutex

	// AI Sessions scanning stays off until this hub says otherwise on this
	// connection (an older hub never will).
	aiGate.Reset()

	// Liveness: require a hub frame (data or pong) within pongWait, and refresh
	// the deadline whenever one arrives. A silently dead TCP connection now
	// surfaces within ~pongWait instead of hanging on the OS keepalive (~2h).
	conn.SetReadDeadline(time.Now().Add(agentPongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(agentPongWait))
		return nil
	})

	// Start a goroutine to read incoming commands from the hub.
	errCh := make(chan error, 1)
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				errCh <- fmt.Errorf("read error: %w", err)
				return
			}
			conn.SetReadDeadline(time.Now().Add(agentPongWait))

			// Decode the type field to dispatch.
			var envelope struct {
				Type         string `json:"type"`
				AgentSecret  string `json:"agent_secret"`
				SecretSHA256 string `json:"secret_sha256"`
			}
			if err := json.Unmarshal(msg, &envelope); err != nil {
				log.Printf("invalid message from hub: %v", err)
				continue
			}

			switch envelope.Type {
			case "enrolled":
				// A fresh or staged secret is durably saved to the PENDING
				// file — never the active one — BEFORE anything treats it as
				// trustworthy. Sending "enrollment_committed" proves nothing
				// by itself — only the hub's later hash-bound
				// "enrollment_confirmed" (below) is what makes it safe to
				// promote the pending file to active and drop BLOXOS_TOKEN.
				// Any failure here (save or send) tears this connection down
				// rather than continuing silently: a fresh connection either
				// retries the save (secret never made it to disk) or, if the
				// secret WAS saved but only the send failed, lets the next
				// reconnect's pending-secret-first recovery path pick it up
				// instead. The in-memory agentSecret and the active
				// credential file are both deliberately left untouched here.
				accepted, hErr := handleEnrolledFrame(
					envelope.AgentSecret,
					savePendingCredentialFile,
					func() error {
						committed := map[string]string{"type": "enrollment_committed"}
						return writeJSON(conn, &writeMu, committed)
					},
				)
				if hErr != nil {
					errCh <- fmt.Errorf("enrollment handling failed: %w", hErr)
					return
				}
				if accepted {
					log.Printf("received enrollment secret from hub, staged pending — awaiting hub confirmation before promoting or dropping bootstrap token")
				}

			case "enrollment_confirmed":
				// The hub always names the exact credential it is confirming
				// via secret_sha256 — a missing or mismatched hash must
				// change nothing locally (handleEnrollmentConfirmed
				// enforces this) rather than guess which file to trust.
				outcome, hErr := handleEnrollmentConfirmed(envelope.SecretSHA256, defaultCredentialConfirmDeps())
				if hErr != nil {
					errCh <- fmt.Errorf("enrollment confirmation rejected: %w", hErr)
					return
				}
				// Only now — after the active credential file is provably
				// the one the hub just confirmed, whether by promoting a
				// pending file or because it already matched active — is it
				// safe to refresh the in-memory secret and drop the token.
				agentSecret = loadCredentialFile()
				token = ""
				tokenCleanupOnce.Do(wipeMachineTokenIfBootstrapped)
				if outcome == enrollmentConfirmPromoted {
					log.Printf("hub confirmed enrollment (promoted pending to active) - dropped bootstrap token")
				} else {
					log.Printf("hub confirmed enrollment (already active) - dropped bootstrap token")
				}

			case "agent_version":
				// Hub announced what version it expects us to be running.
				// Phase 8 self-update — runs the download/verify/install
				// flow in its own goroutine internally.
				handleAgentVersion(msg)

			case aiSessionsConfigType:
				// Admin switch for AI Sessions reporting (see ai_sessions.go).
				handleAISessionsConfig(conn, &writeMu, machineID, msg)

			default:
				// Anything else is a command (restart_service, refresh_metrics, etc.)
				go handleCommand(conn, &writeMu, msg)
			}
		}
	}()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Ping the hub on a fixed cadence so a half-open connection is detected via
	// the pong deadline even during a lull in metrics traffic.
	pingTicker := time.NewTicker(agentPingPeriod)
	defer pingTicker.Stop()

	// Send immediately on connect, then every 30s. Metrics MUST go first
	// because the hub creates the machines row from the metric payload
	// (hostname/IP/OS); any other persisted message that arrives before the
	// row exists is silently dropped by a plain UPDATE.
	if err := sendAll(conn, &writeMu, machineID); err != nil {
		return authOutcomeUnknown, err
	}

	// Send hardware snapshot once per connect, AFTER the first metric so the
	// machines row is guaranteed to exist. The hub keeps whatever we last
	// reported and overwrites on the next connect if anything genuinely
	// changed (DIMM swap, new disk, etc.).
	func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("send hardware: panic recovered: %v (continuing — auto-update path remains operational)", r)
			}
		}()
		if err := sendHardware(conn, &writeMu, machineID); err != nil {
			log.Printf("send hardware error: %v", err)
		}
	}()

	// Phase 8 — report our running version so the hub can display it on
	// the dashboard and detect if we're out of date.
	reportAgentVersion(conn, &writeMu)

	for {
		select {
		case err := <-errCh:
			return authOutcomeUnknown, err
		case <-ticker.C:
			if err := sendAll(conn, &writeMu, machineID); err != nil {
				return authOutcomeUnknown, err
			}
		case <-pingTicker.C:
			writeMu.Lock()
			conn.SetWriteDeadline(time.Now().Add(agentWriteWait))
			err := conn.WriteMessage(websocket.PingMessage, nil)
			writeMu.Unlock()
			if err != nil {
				return authOutcomeUnknown, fmt.Errorf("ping: %w", err)
			}
		}
	}
}

func sendAll(conn *websocket.Conn, mu *sync.Mutex, machineID string) error {
	if err := sendMetrics(conn, mu, machineID); err != nil {
		return err
	}
	sendServices(conn, mu, machineID)
	sendContainers(conn, mu, machineID)
	sendAISessions(conn, mu, machineID)
	return nil
}

func writeJSON(conn *websocket.Conn, mu *sync.Mutex, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal error: %w", err)
	}
	mu.Lock()
	defer mu.Unlock()
	conn.SetWriteDeadline(time.Now().Add(agentWriteWait))
	return conn.WriteMessage(websocket.TextMessage, data)
}

func sendHardware(conn *websocket.Conn, mu *sync.Mutex, machineID string) error {
	hw := collectHardware(machineID, collectGPUMetrics())
	if err := writeJSON(conn, mu, hw); err != nil {
		return fmt.Errorf("write hardware: %w", err)
	}
	return nil
}

func sendMetrics(conn *websocket.Conn, mu *sync.Mutex, machineID string) error {
	m, err := collectMetrics(machineID)
	if err != nil {
		log.Printf("collect metrics error: %v", err)
		return nil
	}

	if err := writeJSON(conn, mu, m); err != nil {
		return fmt.Errorf("write error: %w", err)
	}

	log.Printf("sent metrics: cpu=%.1f%% ram=%d/%dMB ip=%s",
		m.CPUPercent, m.RAMUsedBytes/1024/1024, m.RAMTotalBytes/1024/1024, m.IP)
	return nil
}

func collectMetrics(machineID string) (*Metrics, error) {
	hostname, _ := os.Hostname()
	osInfo := fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)

	cpuPercent, err := cpu.Percent(time.Second, false)
	if err != nil {
		return nil, fmt.Errorf("cpu: %w", err)
	}
	cpuAvg := 0.0
	if len(cpuPercent) > 0 {
		cpuAvg = cpuPercent[0]
	}

	memInfo, err := mem.VirtualMemory()
	if err != nil {
		return nil, fmt.Errorf("mem: %w", err)
	}

	diskInfo, err := disk.Usage("/")
	if err != nil {
		return nil, fmt.Errorf("disk: %w", err)
	}

	hostInfo, _ := host.Info()
	if hostInfo != nil && hostInfo.OS != "" {
		osInfo = fmt.Sprintf("%s %s (%s)", hostInfo.Platform, hostInfo.PlatformVersion, hostInfo.KernelArch)
	}

	localIP := getOutboundIP()

	gpus := collectGPUMetrics()

	return &Metrics{
		Type:           "metrics",
		MachineID:      machineID,
		Hostname:       hostname,
		IP:             localIP,
		OS:             osInfo,
		CPUPercent:     cpuAvg,
		CPUTempC:       collectCPUTempC(),
		RAMUsedBytes:   memInfo.Used,
		RAMTotalBytes:  memInfo.Total,
		DiskUsedBytes:  diskInfo.Used,
		DiskTotalBytes: diskInfo.Total,
		GPUs:           gpus,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		SentAt:         time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

// handleCommand processes an incoming command from the hub.
func handleCommand(conn *websocket.Conn, mu *sync.Mutex, msg []byte) {
	var cmd Command
	if err := json.Unmarshal(msg, &cmd); err != nil {
		log.Printf("invalid command JSON: %v", err)
		return
	}

	// Handle start_terminal separately — it has its own flow.
	if cmd.Type == "start_terminal" {
		if !platformSupportsTerminal() {
			log.Printf("start_terminal: not supported on this platform (%s)", runtime.GOOS)
			if cmd.ID != "" {
				resp := CommandResponse{
					Type:  "command_response",
					ID:    cmd.ID,
					Error: fmt.Sprintf("terminal not supported on %s", runtime.GOOS),
				}
				writeJSON(conn, mu, resp)
			}
			return
		}
		handleStartTerminalPlatform(cmd, msg)
		return
	}

	if cmd.ID == "" {
		log.Printf("ignoring command with no ID")
		return
	}

	if !allowedCommands[cmd.Type] {
		resp := CommandResponse{
			Type:  "command_response",
			ID:    cmd.ID,
			Error: fmt.Sprintf("unknown command type: %s", cmd.Type),
		}
		writeJSON(conn, mu, resp)
		return
	}

	// refresh_metrics doesn't shell out — it triggers an immediate metrics push.
	// The captured conn/mu in the closure is the active connection; the same
	// writeMu serializes the response below and the goroutine's sendAll, so
	// no package-level state is required.
	if cmd.Type == "refresh_metrics" {
		machineID := getMachineID()
		go func() {
			if err := sendAll(conn, mu, machineID); err != nil {
				log.Printf("refresh_metrics: sendAll error: %v", err)
			}
		}()
		resp := CommandResponse{
			Type:    "command_response",
			ID:      cmd.ID,
			Success: true,
			Output:  "metrics refresh triggered",
		}
		writeJSON(conn, mu, resp)
		return
	}

	// Validate target for commands that require one.
	if cmd.Type != "reboot" && cmd.Type != "shutdown" {
		if cmd.Target == "" {
			resp := CommandResponse{
				Type:  "command_response",
				ID:    cmd.ID,
				Error: "target is required",
			}
			writeJSON(conn, mu, resp)
			return
		}
		if !validTarget.MatchString(cmd.Target) {
			resp := CommandResponse{
				Type:  "command_response",
				ID:    cmd.ID,
				Error: "invalid target name: only alphanumeric, hyphens, underscores, dots allowed",
			}
			writeJSON(conn, mu, resp)
			return
		}
	}

	plan, err := commandPlan(cmd.Type, cmd.Target)
	if err != nil {
		resp := CommandResponse{
			Type:  "command_response",
			ID:    cmd.ID,
			Error: err.Error(),
		}
		writeJSON(conn, mu, resp)
		return
	}

	// Bound execution so a wedged systemctl/docker call can't hang this
	// goroutine forever; on timeout the command (and its process group, on
	// Linux) is killed and we report a clear timeout error.
	ctx, cancel := context.WithTimeout(context.Background(), agentCommandTimeout)
	defer cancel()

	output, err := runCommandPlan(ctx, plan)
	resp := CommandResponse{
		Type:    "command_response",
		ID:      cmd.ID,
		Success: err == nil,
		Output:  string(output),
	}
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			resp.Error = fmt.Sprintf("command timed out after %s", agentCommandTimeout)
		} else {
			resp.Error = err.Error()
		}
	}

	log.Printf("command %s (id=%s target=%s): success=%v", cmd.Type, cmd.ID, cmd.Target, resp.Success)
	writeJSON(conn, mu, resp)
}

// sendServices discovers systemd services and sends them to the hub.
func sendServices(conn *websocket.Conn, mu *sync.Mutex, machineID string) {
	hostname, _ := os.Hostname()

	out, err := exec.Command("systemctl", "list-units", "--type=service",
		"--state=active,inactive,failed", "--no-pager", "--no-legend").Output()
	if err != nil {
		log.Printf("service discovery error: %v", err)
		return
	}

	var services []ServiceInfo
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}

		unitName := fields[0]
		name := strings.TrimSuffix(unitName, ".service")
		activeState := fields[2]
		description := strings.Join(fields[4:], " ")

		if activeState == "failed" {
			services = append(services, ServiceInfo{
				Name:        name,
				Status:      activeState,
				Description: description,
			})
			continue
		}

		if isInterestingService(name) {
			services = append(services, ServiceInfo{
				Name:        name,
				Status:      activeState,
				Description: description,
			})
		}
	}

	if len(services) == 0 {
		return
	}

	msg := ServicesMessage{
		Type:      "services",
		Hostname:  hostname,
		MachineID: machineID,
		Services:  services,
	}

	if err := writeJSON(conn, mu, msg); err != nil {
		log.Printf("send services error: %v", err)
		return
	}
	log.Printf("sent %d services", len(services))
}

func isInterestingService(name string) bool {
	lower := strings.ToLower(name)
	for _, pat := range interestingServicePatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// sendContainers discovers Docker containers and sends them to the hub.
func sendContainers(conn *websocket.Conn, mu *sync.Mutex, machineID string) {
	hostname, _ := os.Hostname()

	if err := exec.Command("docker", "info").Run(); err != nil {
		return
	}

	out, err := exec.Command("docker", "ps", "-a", "--format",
		"{{.ID}}\t{{.Names}}\t{{.Status}}\t{{.Image}}").Output()
	if err != nil {
		log.Printf("docker discovery error: %v", err)
		return
	}

	var containers []ContainerInfo
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 4 {
			continue
		}

		status := normalizeContainerStatus(parts[2])

		containers = append(containers, ContainerInfo{
			ID:     parts[0],
			Name:   parts[1],
			Status: status,
			Image:  parts[3],
		})
	}

	if len(containers) == 0 {
		return
	}

	msg := ContainersMessage{
		Type:       "containers",
		Hostname:   hostname,
		MachineID:  machineID,
		Containers: containers,
	}

	if err := writeJSON(conn, mu, msg); err != nil {
		log.Printf("send containers error: %v", err)
		return
	}
	log.Printf("sent %d containers", len(containers))
}

func normalizeContainerStatus(raw string) string {
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "up") {
		return "running"
	}
	if strings.HasPrefix(lower, "exited") {
		return "exited"
	}
	if strings.Contains(lower, "created") {
		return "created"
	}
	if strings.Contains(lower, "paused") {
		return "paused"
	}
	if strings.Contains(lower, "restarting") {
		return "restarting"
	}
	return raw
}

func getMachineID() string {
	info, err := host.Info()
	if err != nil || info.HostID == "" {
		hostname, _ := os.Hostname()
		return hostname
	}
	return info.HostID
}

// nvidia-smi XML structures for parsing.
type nvidiaSmiLog struct {
	GPUs []nvGPU `xml:"gpu"`
}

type nvGPU struct {
	ID               string        `xml:"id,attr"`
	ProductName      string        `xml:"product_name"`
	FanSpeed         string        `xml:"fan_speed"`
	Temperature      nvTemperature `xml:"temperature"`
	Utilization      nvUtilization `xml:"utilization"`
	FBMemory         nvFBMemory    `xml:"fb_memory_usage"`
	GPUPowerReadings nvPower       `xml:"gpu_power_readings"`
	PowerReadings    nvPower       `xml:"power_readings"`
}

type nvTemperature struct {
	GPUTemp string `xml:"gpu_temp"`
}

type nvUtilization struct {
	GPUUtil string `xml:"gpu_util"`
	MemUtil string `xml:"memory_util"`
}

type nvFBMemory struct {
	Total string `xml:"total"`
	Used  string `xml:"used"`
	Free  string `xml:"free"`
}

type nvPower struct {
	PowerDraw     string `xml:"power_draw"`
	AvgPowerDraw  string `xml:"average_power_draw"`
	InstPowerDraw string `xml:"instant_power_draw"`
}

func collectGPUMetrics() []GPUInfo {
	smiPath := resolveNvidiaSmiPath()
	if smiPath == "" {
		return nil
	}
	out, err := exec.Command(smiPath, "-x", "-q").Output()
	if err != nil {
		return nil
	}

	var smiLog nvidiaSmiLog
	if err := xml.Unmarshal(out, &smiLog); err != nil {
		log.Printf("nvidia-smi XML parse error: %v", err)
		return nil
	}

	if len(smiLog.GPUs) == 0 {
		return nil
	}

	gpus := make([]GPUInfo, 0, len(smiLog.GPUs))
	for i, g := range smiLog.GPUs {
		gpu := GPUInfo{
			Index:         i,
			Name:          g.ProductName,
			TempC:         parseNvValue(g.Temperature.GPUTemp),
			UtilPercent:   parseNvValue(g.Utilization.GPUUtil),
			FanPercent:    parseNvValue(g.FanSpeed),
			MemUsedBytes:  mibToBytes(g.FBMemory.Used),
			MemTotalBytes: mibToBytes(g.FBMemory.Total),
		}

		pw := parseNvValue(g.GPUPowerReadings.PowerDraw)
		if pw == 0 {
			pw = parseNvValue(g.GPUPowerReadings.AvgPowerDraw)
		}
		if pw == 0 {
			pw = parseNvValue(g.GPUPowerReadings.InstPowerDraw)
		}
		if pw == 0 {
			pw = parseNvValue(g.PowerReadings.PowerDraw)
		}
		gpu.PowerWatts = pw

		gpus = append(gpus, gpu)
	}
	return gpus
}

func parseNvValue(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "N/A" || s == "[N/A]" {
		return 0
	}
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0
	}
	return v
}

func mibToBytes(s string) int64 {
	v := parseNvValue(s)
	return int64(v * 1024 * 1024)
}

func getOutboundIP() string {
	conn, err := net.DialTimeout("udp", "8.8.8.8:80", 2*time.Second)
	if err != nil {
		return ""
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}
