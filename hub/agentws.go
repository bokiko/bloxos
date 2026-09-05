package main

import (
	"bytes"
	cryptoRand "crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bokiko/bloxos/proto/aisessions"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

// ConnectedAgent tracks a live WebSocket connection from an agent.
type ConnectedAgent struct {
	MachineID string
	Conn      *websocket.Conn
	WriteMu   sync.Mutex
}

// wsMessageWriter is exactly gorilla/websocket's (*Conn).WriteMessage
// signature, factored out so writeLockedTo is testable against a fake
// writer instead of a live network connection.
type wsMessageWriter interface {
	WriteMessage(messageType int, data []byte) error
}

// writeLocked is the ONLY safe way to write to agent.Conn once
// registerAgentConnection has been called: that call launches
// announceVersionToAgent in a goroutine (and command/refresh/terminal
// handlers write from their own goroutines too), so any write after
// registration is racing at least one other potential writer.
// gorilla/websocket allows exactly one writer at a time — violating that is
// a protocol-level panic risk ("concurrent write to websocket connection"),
// not merely a Go-level data race, so `go test -race` cannot be relied on to
// catch a missing lock here.
func (a *ConnectedAgent) writeLocked(messageType int, data []byte) error {
	return writeLockedTo(&a.WriteMu, a.Conn, messageType, data)
}

func writeLockedTo(mu *sync.Mutex, w wsMessageWriter, messageType int, data []byte) error {
	mu.Lock()
	defer mu.Unlock()
	return w.WriteMessage(messageType, data)
}

// registerAgentConnection finalises an authenticated WebSocket handshake.
// It is the single chokepoint between "agent authed" and "agent fully
// online", so any per-connect work (registry insert, version announce,
// future hooks) must go here. Both auth paths — durable-secret reconnect
// and token-based fresh enrolment — call this exactly once per connection,
// which guarantees Phase-8 auto-update propagates on reconnect (the bug
// that originally motivated extracting this helper).
//
// Replacing the registry entry also closes any different connection it
// displaced. Without that, an overlapping reconnect leaves the old socket
// authenticated and active, and revocation, which closes only the
// registered connection, misses it. The close happens after the lock is
// released; the displaced handler's own exit path then sees it is no longer
// the registered connection and leaves the new registration alone.
func (s *Server) registerAgentConnection(machineID string, agent *ConnectedAgent) {
	agent.MachineID = machineID
	s.agentsMu.Lock()
	displaced := s.agents[machineID]
	s.agents[machineID] = agent
	s.agentsMu.Unlock()
	if displaced != nil && displaced != agent && displaced.Conn != nil {
		log.Printf("agent %s reconnected; closing displaced connection", machineID)
		_ = displaced.Conn.Close()
	}
	s.goTracked(func() { s.announceVersionToAgent(machineID, agent) })
	s.goTracked(func() { s.sendAISessionsConfig(machineID, agent) })
}

// unregisterAgentConnection is the symmetric cleanup for
// registerAgentConnection. Called from handleAgentWS's read-loop exit
// path. Only flips the machine to offline AND deletes the agents map
// entry when the entry under machineID still points at THIS connection
// — otherwise we'd false-offline a fast-reconnecting agent whose new
// handler already overwrote the entry. Empty machineID (auth never
// succeeded) is a no-op.
func (s *Server) unregisterAgentConnection(machineID string, agent *ConnectedAgent) {
	if machineID == "" {
		return
	}
	s.agentsMu.Lock()
	current, ok := s.agents[machineID]
	stillOurs := ok && current == agent
	if stillOurs {
		delete(s.agents, machineID)
	}
	s.agentsMu.Unlock()
	if stillOurs {
		s.markOffline(machineID)
		// Live sessions only: a machine that is no longer connected has no
		// sessions to show.
		s.aiSessions.remove(machineID)
	}
}

func (s *Server) handleCreateToken(c echo.Context) error {
	ip := getRealIP(c)
	if !rateLimiter.Allow("token_create", ip, 3) {
		log.Printf("rate limit exceeded: token_create from %s", ip)
		return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
	}

	// The generated install one-liner points agents at the hub. Deriving that
	// URL from the request Host header lets a crafted Host / DNS-rebind produce
	// a command that installs the agent pointed at an attacker host. Refuse to
	// mint a token+command unless the operator has set an explicit PUBLIC_URL.
	if strings.TrimSpace(os.Getenv("PUBLIC_URL")) == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "PUBLIC_URL is not set on the hub; set PUBLIC_URL to the hub's public URL and restart before generating install tokens",
		})
	}

	token := uuid.New().String()
	h := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(h[:])
	expiresAt := time.Now().Add(15 * time.Minute)

	_, err := s.db.Exec(`INSERT INTO tokens (token_hash, expires_at) VALUES (?, ?)`, tokenHash, expiresAt)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	httpBase, wsBase := publicAndWebsocketBase()
	caURL, caSHA256 := bootstrapCAFor(httpBase)
	return c.JSON(http.StatusOK, installTokenResponse{
		Token:          token,
		Command:        buildLinuxInstallCommand(httpBase, wsBase, token, caURL, caSHA256),
		WindowsCommand: buildWindowsInstallCommand(httpBase, wsBase, token, caURL, caSHA256, false),
		CAURL:          caURL,
		CASHA256:       caSHA256,
		ExpiresAt:      expiresAt.Format(time.RFC3339),
	})
}

// handleWindowsReenrollment mints a token bound to exactly one existing
// Windows machine and returns a -ForceReenroll command for it. Unlike
// handleCreateToken (a generic, unbound Add Machine token), this never
// deletes the machine's current credential or closes its live socket —
// preparing the command is inert. The credential is only replaced once the
// operator runs the returned command and the agent re-enrolls with the
// target-bound token; see the WS handshake in handleAgentWS.
func (s *Server) handleWindowsReenrollment(c echo.Context) error {
	machineID := c.Param("id")

	var osName sql.NullString
	err := s.db.QueryRow(`SELECT os FROM machines WHERE id = ?`, machineID).Scan(&osName)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "machine not found"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to query machine"})
	}
	if normalizeAgentOS(osName.String) != "windows" {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "machine is not a Windows agent"})
	}

	// A re-enrollment token stages a replacement for an EXISTING credential
	// (see stageTargetedCredential); a machine with no active credential has
	// nothing to stage against and should use the ordinary Add Machine flow
	// instead. Checked before any write.
	if !s.machineHasCredential(machineID) {
		return c.JSON(http.StatusConflict, map[string]string{
			"error": "machine has no active credential; use the Add Machine command instead",
		})
	}

	// Same PUBLIC_URL requirement as handleCreateToken, and checked before any
	// write: a re-enrollment command is exactly as authority-sensitive as a
	// fresh install command, and this endpoint must fail closed rather than
	// insert a token it then can't safely address.
	if strings.TrimSpace(os.Getenv("PUBLIC_URL")) == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "PUBLIC_URL is not set on the hub; set PUBLIC_URL to the hub's public URL and restart before generating re-enrollment tokens",
		})
	}

	token := uuid.New().String()
	h := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(h[:])
	expiresAt := time.Now().Add(15 * time.Minute)

	if _, err := s.db.Exec(
		`INSERT INTO tokens (token_hash, expires_at, target_machine_id) VALUES (?, ?, ?)`,
		tokenHash, expiresAt, machineID,
	); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	httpBase, wsBase := publicAndWebsocketBase()
	caURL, caSHA256 := bootstrapCAFor(httpBase)
	return c.JSON(http.StatusOK, windowsReenrollmentResponse{
		MachineID:      machineID,
		Token:          token,
		WindowsCommand: buildWindowsInstallCommand(httpBase, wsBase, token, caURL, caSHA256, true),
		CAURL:          caURL,
		CASHA256:       caSHA256,
		ExpiresAt:      expiresAt.Format(time.RFC3339),
	})
}

func handleInstallScript(c echo.Context) error {
	script := `#!/bin/bash
set -euo pipefail

echo "=== BloxOS Agent Installer ==="

# Detect architecture.
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

HUB="${BLOXOS_HUB:?BLOXOS_HUB must be set}"
TOKEN="${BLOXOS_TOKEN:?BLOXOS_TOKEN must be set}"
HUB_HTTP=$(echo "$HUB" | sed 's|^ws://|http://|; s|^wss://|https://|')
# CA contract with the hub-generated paste block (buildLinuxInstallCommand):
# when the hub uses a private CA, the paste block fetches the CA, verifies it
# against the fingerprint delivered through the authenticated dashboard
# session, installs it, and hands this script BLOXOS_CA_CERT (its path) and
# BLOXOS_CA_SHA256 (its fingerprint). This script never fetches a CA itself.
CA_PATH="${BLOXOS_CA_CERT:-}"
CA_SHA256="${BLOXOS_CA_SHA256:-}"

curl_fetch() {
  local url="$1"
  shift
  if [[ "$url" == https://* ]]; then
    curl --proto '=https' --tlsv1.2 -fsSL "$@" "$url"
  else
    curl -fsSL "$@" "$url"
  fi
}

echo "Hub: $HUB_HTTP"
echo "Arch: $ARCH"

CA_CURL_ARGS=()
AGENT_CA_ENV=""

if [[ -n "$CA_PATH" || -n "$CA_SHA256" ]]; then
  CA_PATH="${CA_PATH:-/etc/bloxos/ca.crt}"
  if [[ -z "$CA_SHA256" ]]; then
    echo "BLOXOS_CA_SHA256 must be set when BLOXOS_CA_CERT is provided" >&2
    exit 1
  fi
  if ! sudo test -f "$CA_PATH"; then
    echo "Verified hub CA not found at $CA_PATH." >&2
    echo "Run the complete install command generated by the dashboard (Add Machine); it installs the CA before running this installer." >&2
    exit 1
  fi
  ACTUAL_CA_SHA=$(sudo sha256sum "$CA_PATH" | awk '{print $1}')
  if [[ "$ACTUAL_CA_SHA" != "$CA_SHA256" ]]; then
    echo "CA fingerprint mismatch at $CA_PATH" >&2
    echo "Expected: $CA_SHA256" >&2
    echo "Actual:   $ACTUAL_CA_SHA" >&2
    exit 1
  fi
  echo "Using fingerprint-verified CA at $CA_PATH"
  CA_CURL_ARGS+=(--cacert "$CA_PATH")
  AGENT_CA_ENV="Environment=\"BLOXOS_CA_CERT=$CA_PATH\""
fi

# Download agent binary.
echo "Downloading agent binary..."
curl_fetch "${HUB_HTTP}/download/agent?arch=${ARCH}" "${CA_CURL_ARGS[@]}" -o /tmp/bloxos-agent
chmod +x /tmp/bloxos-agent

# Verify the downloaded binary against the SHA-256 the hub computed for the
# binary it serves. Provides integrity even over a plain-HTTP LAN download.
EXPECTED_AGENT_SHA256="__EXPECTED_AGENT_SHA256__"
if [[ -n "$EXPECTED_AGENT_SHA256" ]]; then
  ACTUAL_AGENT_SHA=$(sha256sum /tmp/bloxos-agent | awk '{print $1}')
  if [[ "$ACTUAL_AGENT_SHA" != "$EXPECTED_AGENT_SHA256" ]]; then
    echo "Agent binary fingerprint mismatch"
    echo "Expected: $EXPECTED_AGENT_SHA256"
    echo "Actual:   $ACTUAL_AGENT_SHA"
    rm -f /tmp/bloxos-agent
    exit 1
  fi
  echo "Agent binary verified ($EXPECTED_AGENT_SHA256)"
else
  echo "WARNING: hub did not provide an agent binary hash; skipping integrity check"
fi

# Pin the hub's agent-update signing key. The agent refuses to self-update
# unless the SHA the hub announces carries a signature from this key — the
# announced SHA on its own is a corruption check, not proof of who announced
# it. See agent/update_verify.go.
UPDATE_PUBKEY="__UPDATE_PUBKEY__"
sudo mkdir -p /etc/bloxos
sudo chmod 755 /etc/bloxos
if [[ -n "$UPDATE_PUBKEY" ]]; then
  printf '%s\n' "$UPDATE_PUBKEY" | sudo tee /etc/bloxos/agent-update.pub > /dev/null
  sudo chmod 0644 /etc/bloxos/agent-update.pub
  echo "Pinned agent update signing key to /etc/bloxos/agent-update.pub"
else
  echo "WARNING: hub provided no update signing key; agent self-update will stay disabled"
fi

# Create system user (if not exists).
if ! id -u bloxos &>/dev/null; then
  sudo useradd -r -s /usr/sbin/nologin bloxos || true
fi

# Install binary.
sudo install -o root -g root -m 0755 /tmp/bloxos-agent /usr/local/bin/bloxos-agent
rm -f /tmp/bloxos-agent

# Create credential directory for agent secret (post-enrollment).
sudo mkdir -p /etc/bloxos
sudo chmod 755 /etc/bloxos

# Create systemd service.
# The agent uses the token for initial enrollment only.
# After enrollment, it stores a durable secret in /etc/bloxos/agent-secret
# and uses that for all future connections.
sudo tee /etc/systemd/system/bloxos-agent.service > /dev/null << SVCEOF
[Unit]
Description=BloxOS Agent
After=network.target
# Phase 8: if the agent crash-loops 3 times within 60s after a self-update,
# OnFailure triggers the recovery unit which rolls back to the .prev binary.
#
# StartLimit* belongs in [Unit] as StartLimitIntervalSec= — that is where
# systemd has documented it since v229. The old [Service] StartLimitInterval=
# spelling still works (systemd keeps it as a compat alias; verified on
# systemd 255 that both shapes reach "failed" and fire OnFailure=), but it is
# deprecated, so use the documented location.
StartLimitIntervalSec=60
StartLimitBurst=3
OnFailure=bloxos-agent-recover.service

[Service]
Type=simple
User=root
Environment="BLOXOS_HUB=${HUB}"
Environment="BLOXOS_TOKEN=${TOKEN}"
${AGENT_CA_ENV}
ExecStart=/usr/local/bin/bloxos-agent
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
SVCEOF

# Phase 8 — install the rollback recovery script.
# Single-quoted heredoc ('RECOVEREOF') so bash does NOT expand variables
# during the cat — the literal $VAR ends up in the recovery script file
# and is expanded when the recovery script itself runs.
sudo tee /usr/local/bin/bloxos-agent-recover > /dev/null << 'RECOVEREOF'
#!/bin/bash
set -euo pipefail
AGENT_PATH="/usr/local/bin/bloxos-agent"
PREV_PATH="${AGENT_PATH}.prev"
MARKER_PATH="$(dirname "${AGENT_PATH}")/.bloxos-agent-updated-at"
ROLLBACK_LOG="/var/log/bloxos-agent-rollback.log"
log() {
    echo "[$(date -u +%Y-%m-%dT%H:%M:%SZ)] $1" | tee -a "$ROLLBACK_LOG"
}
log "Recovery script invoked"
if [ ! -f "$PREV_PATH" ]; then
    log "No .prev binary exists, cannot roll back"
    exit 0
fi
if [ ! -f "$MARKER_PATH" ]; then
    log "No update marker exists, skipping rollback"
    exit 0
fi
MARKER_AGE_SECONDS=$(($(date +%s) - $(stat -c %Y "$MARKER_PATH")))
if [ "$MARKER_AGE_SECONDS" -gt 600 ]; then
    log "Update marker is $MARKER_AGE_SECONDS seconds old, not rolling back"
    exit 0
fi
log "Recent update detected, rolling back to .prev"
cp "$AGENT_PATH" "${AGENT_PATH}.failed.$(date +%s)"
mv "$PREV_PATH" "$AGENT_PATH"
chmod +x "$AGENT_PATH"
rm -f "$MARKER_PATH"
log "Rollback complete, restarting agent"
systemctl restart bloxos-agent.service
RECOVEREOF
sudo chmod +x /usr/local/bin/bloxos-agent-recover

# Phase 8 — recovery unit (triggered by OnFailure on the main agent unit).
sudo tee /etc/systemd/system/bloxos-agent-recover.service > /dev/null << 'RECEOF'
[Unit]
Description=BloxOS Agent Recovery (automatic rollback)

[Service]
Type=oneshot
ExecStart=/usr/local/bin/bloxos-agent-recover
RemainAfterExit=no
RECEOF

# Enable and start.
sudo systemctl daemon-reload
sudo systemctl enable bloxos-agent
sudo systemctl restart bloxos-agent

echo "=== BloxOS Agent installed and running ==="
echo "Check status: systemctl status bloxos-agent"
`
	// Pin the hash of the binary the hub actually serves. recompute is
	// mtime-cached, so this is cheap on the hot path.
	recomputeBinaryFor("linux")
	linuxState := currentAgentBinaryState("linux")
	if linuxState.Error != "" || linuxState.SHA == "" {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "Linux agent binary unavailable: " + linuxState.Error,
		})
	}
	script = strings.ReplaceAll(script, "__EXPECTED_AGENT_SHA256__", linuxState.SHA)
	script = strings.ReplaceAll(script, "__UPDATE_PUBKEY__", updateSigningPublicKeyBase64())
	return c.String(http.StatusOK, script)
}

// publicAndWebsocketBase derives the hub's HTTP and WebSocket base URLs from
// PUBLIC_URL. It never falls back to the request Host header, which is
// attacker-influenced — callers (handleCreateToken) guarantee PUBLIC_URL is set
// before reaching here and return "" otherwise.
func publicAndWebsocketBase() (string, string) {
	publicURL := strings.TrimSpace(os.Getenv("PUBLIC_URL"))
	if publicURL == "" {
		return "", ""
	}
	httpBase := publicURL
	wsBase := strings.Replace(publicURL, "https://", "wss://", 1)
	wsBase = strings.Replace(wsBase, "http://", "ws://", 1)
	return httpBase, wsBase
}

func bootstrapCACertCandidates() []string {
	candidates := []string{}
	if env := os.Getenv("BLOXOS_CA_CERT"); env != "" {
		candidates = append(candidates, env)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, ".local", "share", "caddy", "pki", "authorities", "local", "root.crt"))
	}
	candidates = append(candidates,
		"/var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt",
		"/var/lib/caddy/pki/authorities/local/root.crt",
		"/root/.local/share/caddy/pki/authorities/local/root.crt",
	)
	return candidates
}

func loadBootstrapCACert() ([]byte, string, error) {
	for _, path := range bootstrapCACertCandidates() {
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err == nil {
			return data, path, nil
		}
		if os.IsNotExist(err) {
			continue
		}
		return nil, "", fmt.Errorf("read CA cert %s: %w", path, err)
	}
	return nil, "", os.ErrNotExist
}

func bootstrapCAFor(httpBase string) (caURL string, caSHA256 string) {
	if strings.HasPrefix(httpBase, "https://") {
		if caPEM, caPath, err := loadBootstrapCACert(); err == nil {
			caURL = httpBase + "/download/ca.crt"
			sum := sha256.Sum256(caPEM)
			caSHA256 = hex.EncodeToString(sum[:])
			log.Printf("install bootstrap: using CA cert %s", caPath)
		} else if !os.IsNotExist(err) {
			log.Printf("WARNING: install bootstrap CA unavailable: %v", err)
		}
	}
	return caURL, caSHA256
}

func handleDownloadAgent(c echo.Context) error {
	// The target OS is detected from either the explicit ?os= query parameter
	// or the User-Agent string. Resolution itself is centralized: recompute
	// hashes a trusted path and the handler serves that exact cached path.
	osName := strings.ToLower(strings.TrimSpace(c.QueryParam("os")))
	if osName == "" {
		ua := strings.ToLower(c.Request().UserAgent())
		if strings.Contains(ua, "windows") {
			osName = "windows"
		}
	}
	osName = normalizeAgentOS(osName)
	recomputeBinaryFor(osName)
	state := currentAgentBinaryState(osName)
	if state.Error != "" || state.Path == "" || state.SHA == "" {
		reason := state.Error
		if reason == "" {
			reason = "no trusted binary is available"
		}
		return c.JSON(http.StatusServiceUnavailable, map[string]string{
			"error":  fmt.Sprintf("agent binary for os=%s unavailable: %s", osName, reason),
			"os":     osName,
			"source": state.Source,
		})
	}

	arch := c.QueryParam("arch")
	log.Printf("agent download: os=%s arch=%s source=%s path=%s sha=%s",
		osName, arch, state.Source, state.Path, versionShortSHA(state.SHA))
	return c.File(state.Path)
}

func handleDownloadCACert(c echo.Context) error {
	caPEM, _, err := loadBootstrapCACert()
	if err != nil {
		if os.IsNotExist(err) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "CA certificate not configured; set BLOXOS_CA_CERT"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return c.Blob(http.StatusOK, "application/x-pem-file", caPEM)
}

// wsAgentRateLimit caps WebSocket-upgrade requests to /ws/agent at this
// many per minute, per source IP. Real agents reconnect at most a few
// times per minute (a cycling Caddy-loopback agent peaks around 2/min,
// fleet-wide reconnect after hub restart hits maybe 5/min). 30/min/IP
// gives ~10x headroom for legitimate use while shutting down an
// FD-exhaustion flood from any single source. The full fleet still
// scales fine because each agent gets its own bucket.
const wsAgentRateLimit = 30

// agentEnrollTokenHeader carries the install token on the /ws/agent handshake.
// Preferred over the deprecated ?token= query param, which leaks into
// reverse-proxy access logs.
const agentEnrollTokenHeader = "X-Bloxos-Enroll-Token"

// bearerToken extracts the credential from an "Authorization: Bearer <x>"
// header value. Returns "" when the header is absent or not a bearer scheme.
func bearerToken(header string) string {
	const prefix = "Bearer "
	if len(header) >= len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
		return strings.TrimSpace(header[len(prefix):])
	}
	return ""
}

// agentAuthWindow bounds how long an upgraded agent socket may stay open before
// it sends its first authenticating frame. Without it, a client that completes
// the WebSocket upgrade but never speaks would hold the socket (and its file
// descriptor) open indefinitely. It is a var so tests can shorten it.
var agentAuthWindow = 30 * time.Second

// agentIdleTimeout bounds the gap between frames once an agent is established.
// Agents push metrics every 30s and ping on the same cadence, so a silently
// dead TCP connection is dropped within this window instead of lingering until
// the OS keepalive notices (which can take ~2h).
var agentIdleTimeout = 90 * time.Second

func (s *Server) handleAgentWS(c echo.Context) error {
	ip := getRealIP(c)
	if !rateLimiter.Allow("ws_agent", ip, wsAgentRateLimit) {
		log.Printf("rate limit exceeded: ws_agent from %s", ip)
		return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
	}
	// Serialize credential revocation with the short authentication window.
	// Without this gate, a socket could validate a secret, lose a race with
	// revocation, and register after the revoke handler had already looked for
	// a live connection to close.
	// The gate is held for at most agentAuthWindow: until authentication
	// completes, frames and pings must not extend the socket's read deadline,
	// or a client with a valid token could hold this read lock indefinitely,
	// block revocation (a writer), and queue every new agent handler behind
	// that writer. Only an established agent moves to the idle deadline.
	s.agentAuthMu.RLock()
	authPending := true
	completeAuth := func() {
		if authPending {
			authPending = false
			s.agentAuthMu.RUnlock()
		}
	}
	defer completeAuth()

	// Prefer credentials from handshake headers — unlike query strings, they
	// are not written to reverse-proxy access logs. Fall back to the deprecated
	// query params so agents predating this change keep working.
	agentSecret := bearerToken(c.Request().Header.Get("Authorization"))
	if agentSecret == "" {
		agentSecret = c.QueryParam("secret") // deprecated: use Authorization: Bearer
	}
	token := c.Request().Header.Get(agentEnrollTokenHeader)
	if token == "" {
		token = c.QueryParam("token") // deprecated: use the X-Bloxos-Enroll-Token header
	}

	// Auth priority: secret > token. If neither, reject.
	if token == "" && agentSecret == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "token or secret required"})
	}

	// Mode B: Agent reconnecting with durable secret.
	var secretMachineID string
	var secretJustPromoted bool
	if agentSecret != "" {
		var err error
		secretMachineID, secretJustPromoted, err = s.validateAgentSecret(agentSecret)
		if err != nil {
			log.Printf("agent secret validation failed: %v", err)
			// If secret was provided but invalid, and no token fallback, reject.
			if token == "" {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid agent secret"})
			}
			// Fall through to token-based auth.
			agentSecret = ""
		}
	}

	// Mode A: token validation. Unlike the original secret-XOR-token design,
	// this runs whenever a token is present even alongside an already-valid
	// secret: a targeted re-enrollment token rides alongside the agent's
	// still-valid old secret (see the dual-credential handling below), so we
	// must know the token's target before deciding how to route this
	// connection. A generic (untargeted) token found alongside a valid secret
	// is simply never consulted again below — it can't override a valid
	// credential.
	var tokenHash string
	var targetMachineID sql.NullString
	tokenValidated := false
	if token != "" {
		var err error
		tokenHash, targetMachineID, err = s.validateAgentToken(token)
		if err != nil {
			tokenHash = ""
			targetMachineID = sql.NullString{}
			log.Printf("agent token validation deferred (may be reconnecting agent): %v", err)
		} else {
			tokenValidated = true
		}
	}

	// Reject before upgrading when the caller has neither a valid durable
	// secret nor a valid install token. Enrollment requires a VALID token; an
	// invalid, expired, or already-used token must not obtain an upgraded
	// socket. Valid-token enrollment still authenticates post-upgrade because
	// machine_id is only learned from the first metrics frame — this guard only
	// rejects the definitively-unauthenticated.
	if secretMachineID == "" && !tokenValidated {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "valid agent secret or install token required"})
	}

	ws, wsErr := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if wsErr != nil {
		return wsErr
	}
	defer ws.Close()

	// Bound the socket lifetime. An upgraded client must send its first frame
	// within the auth window; thereafter it must keep the connection live
	// (metrics or a ping) within the idle timeout. A ping resets the deadline
	// and is answered with a pong. This closes both the idle-hold DoS (upgrade
	// then go silent) and the ~2h half-open detection gap.
	ws.SetReadDeadline(time.Now().Add(agentAuthWindow))
	ws.SetPingHandler(func(appData string) error {
		if !authPending {
			ws.SetReadDeadline(time.Now().Add(agentIdleTimeout))
		}
		return ws.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(10*time.Second))
	})

	// If authenticated via secret, we already know the machine_id — UNLESS a
	// target-bound token for that same machine is also present. That specific
	// combination means the agent still has its old, still-valid secret AND a
	// fresh re-enrollment token: it must be routed through the staging branch
	// below (first metrics frame), not fast-pathed as an ordinary reconnect,
	// so a legitimate targeted re-enrollment attempt is never silently
	// downgraded to "just a normal reconnect" that never looks at the token.
	dualCredentialTargetedReenroll := secretMachineID != "" && tokenValidated &&
		targetMachineID.Valid && targetMachineID.String == secretMachineID

	var machineID string
	agent := &ConnectedAgent{Conn: ws}
	// Single owner of connection cleanup. Every return from this point on —
	// whether the read loop exits normally, or one of the many
	// post-registration failure paths below returns early (a failed confirm
	// write, a promotion that errors/mismatches/expires, an
	// enrollment_committed with no matching staged state, etc.) — must run
	// unregisterAgentConnection exactly once. A closure (not
	// `defer s.unregisterAgentConnection(machineID, agent)`) is required
	// because a direct defer call captures machineID's value NOW, while it is
	// still "" — every post-registration path sets it before returning.
	// unregisterAgentConnection's own identity check (current == agent) means
	// this is always safe to call, including when registration never
	// happened at all (no-op via the empty-machineID guard) or when a newer
	// connection has already replaced this one in the registry (no-op via
	// the identity check).
	// Fresh enrollment issued on this connection but not yet committed: the
	// token stays unused and no credential exists until enrollment_committed.
	var freshPendingTokenHash, freshPendingSecretHash string
	defer func() {
		s.unregisterAgentConnection(machineID, agent)
		// A fresh enrollment that never committed was never registered, so
		// the identity-guarded unregister above cannot flip its machine row
		// (upserted online by the first metrics frame) back offline. Do it
		// here unless another live connection owns the machine, so an
		// aborted enrollment leaves no phantom online machine. The read lock
		// is held through the (short) markOffline update on purpose:
		// registerAgentConnection needs the writer lock, so a durable
		// reconnect cannot register in between the liveness check and the
		// status write and be marked offline while live.
		if freshPendingTokenHash != "" && machineID != "" {
			s.agentsMu.RLock()
			if _, live := s.agents[machineID]; !live {
				s.markOffline(machineID)
			}
			s.agentsMu.RUnlock()
		}
	}()
	var stagedPendingSecretHash string
	if secretMachineID != "" && !dualCredentialTargetedReenroll {
		machineID = secretMachineID
		s.registerAgentConnection(machineID, agent)
		completeAuth()
		ws.SetReadDeadline(time.Now().Add(agentIdleTimeout))
		log.Printf("agent authenticated via secret: machine_id=%s", machineID)

		// enrollment_confirmed tells the agent it may safely drop
		// BLOXOS_TOKEN. Two cases land here with a token still present:
		//   - secretJustPromoted: this very auth call promoted a pending
		//     secret to active (the lost-acknowledgement recovery path) —
		//     the agent needs to hear that explicitly, it can't infer it.
		//   - token != "" with no promotion: the agent already has an active
		//     secret but is still holding a token from an earlier attempt
		//     whose confirmation never arrived (e.g. promotion succeeded but
		//     the confirm frame itself was lost). Confirming again is
		//     harmless and lets it clear the stale token.
		if secretJustPromoted || token != "" {
			// Registration above already spawned announceVersionToAgent as a
			// goroutine that may be writing to this same connection right
			// now — this write MUST go through the per-agent mutex, never
			// direct to ws. A failed write means the agent never actually
			// received the confirmation, so it must not be logged as
			// confirmed; closing the connection lets the agent's reconnect
			// re-exercise this same recovery path.
			//
			// secret_sha256 names the EXACT credential this confirmation is
			// for — agentSecret is, in both cases here, the secret that just
			// authenticated this connection (whether or not this call is
			// what promoted it), so its hash is always correct: the agent
			// only ever promotes its local pending file when the hash
			// matches, and never touches anything when it doesn't.
			confirmMsg, _ := json.Marshal(map[string]string{"type": "enrollment_confirmed", "secret_sha256": secretSHA256Hex(agentSecret)})
			if err := agent.writeLocked(websocket.TextMessage, confirmMsg); err != nil {
				log.Printf("failed to send enrollment_confirmed to %s: %v — closing connection", machineID, err)
				return nil
			}
			log.Printf("sent enrollment_confirmed to %s", machineID)
		}
	}

	for {
		_, msg, err := ws.ReadMessage()
		if err != nil {
			log.Printf("agent disconnected: %v", err)
			return nil
		}
		// A frame arrived — extend the deadline to the idle timeout, but only
		// for an established agent; the auth window stays fixed until then.
		if !authPending {
			ws.SetReadDeadline(time.Now().Add(agentIdleTimeout))
		}

		var envelope struct {
			Type      string `json:"type"`
			MachineID string `json:"machine_id"`
		}
		if err := json.Unmarshal(msg, &envelope); err != nil {
			log.Printf("invalid JSON from agent: %v", err)
			continue
		}

		// Identity binding: once this socket is authenticated as machineID,
		// reject any frame that claims a different machine_id. During token
		// enrollment machineID is still "" until the first metrics frame
		// establishes it, so this guard is a no-op on that first frame.
		if machineID != "" && envelope.MachineID != "" && envelope.MachineID != machineID {
			log.Printf("rejecting cross-machine frame: authed=%s claimed=%s type=%s",
				machineID, envelope.MachineID, envelope.Type)
			continue
		}

		switch envelope.Type {
		case "metrics", "":
			var m AgentMetrics
			if err := json.Unmarshal(msg, &m); err != nil {
				log.Printf("invalid metrics JSON: %v", err)
				continue
			}
			if machineID == "" {
				machineID = m.MachineID
				if secretMachineID != "" && machineID != secretMachineID {
					log.Printf("rejecting agent: claimed machine_id %s does not match credential for %s", machineID, secretMachineID)
					ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","message":"machine_id does not match credential"}`))
					return nil
				}

				// A targeted re-enrollment token (minted by
				// POST /api/machines/:id/windows-re-enrollment) is bound to exactly
				// one machine_id. Reject its use against any other machine before
				// any credential/known-machine branching below, so the rejection
				// applies uniformly whether or not this other machine currently has
				// a credential. Nothing is written and the token stays unconsumed —
				// a legitimate holder can still use it against the right machine.
				if tokenValidated && targetMachineID.Valid && targetMachineID.String != machineID {
					log.Printf("rejecting targeted re-enrollment token for %s (%s): bound to machine %s", m.Hostname, machineID, targetMachineID.String)
					ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","message":"install token is bound to a different machine"}`))
					return nil
				}

				// Check if this machine_id already exists (reconnecting agent).
				var existingID string
				knownMachine := s.db.QueryRow(`SELECT id FROM machines WHERE id = ?`, machineID).Scan(&existingID) == nil
				hasCredential := s.machineHasCredential(machineID)
				targetedForThisMachine := tokenValidated && targetMachineID.Valid && targetMachineID.String == machineID

				if hasCredential && targetedForThisMachine {
					// Targeted re-enrollment: the token was minted for exactly this
					// machine and a durable credential already exists. STAGE a
					// replacement rather than installing it as active — the active
					// secret_hash is untouched here, so the agent's old (still
					// possibly in-use) secret keeps authenticating until the agent
					// proves the new one is durably saved via "enrollment_committed"
					// below. This closes the split-brain window where a hub-side
					// replace commits before the agent's local write is proven, and
					// a subsequent crash/timeout would strand the machine with a
					// local secret the hub no longer recognizes.
					rawSecret, secretHash, err := generateAgentSecret()
					if err != nil {
						log.Printf("failed to generate agent secret: %v", err)
						ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","message":"enrollment failed"}`))
						return nil
					}
					if err := s.stageTargetedCredential(tokenHash, machineID, secretHash); err != nil {
						log.Printf("failed to stage pending credential via targeted re-enrollment: %v", err)
						ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","message":"enrollment failed"}`))
						return nil
					}
					stagedPendingSecretHash = secretHash
					enrolledMsg, _ := json.Marshal(map[string]string{
						"type":         "enrolled",
						"agent_secret": rawSecret,
					})
					// No concurrent writer exists yet at this point — this
					// connection has not been registered, so
					// announceVersionToAgent has not been spawned — a direct
					// write is safe. But the pending credential was already
					// staged (a real DB mutation, and the token is already
					// consumed): if the send itself fails, silently falling
					// through to registration would leave the connection
					// looking authenticated while the agent never actually
					// received its new secret. Close instead.
					if err := ws.WriteMessage(websocket.TextMessage, enrolledMsg); err != nil {
						log.Printf("staged pending credential for %s but failed to send enrolled frame: %v — closing connection", machineID, err)
						return nil
					}
					log.Printf("targeted re-enrollment staged a pending credential: %s (%s)", m.Hostname, machineID)
				} else if knownMachine && secretMachineID == machineID {
					log.Printf("known agent reconnecting with durable credential: %s (%s)", m.Hostname, machineID)
				} else if tokenValidated && !hasCredential {
					// New enrollment with a valid token: issue a secret but
					// write nothing yet. The token is consumed and the
					// credential stored only when the agent reports the
					// secret durably staged (enrollment_committed below), so
					// an agent that fails to save it and drops the connection
					// can retry with the same token instead of being stranded
					// with a spent token and no secret.
					rawSecret, secretHash, err := generateAgentSecret()
					if err != nil {
						log.Printf("failed to generate agent secret: %v", err)
						ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","message":"enrollment failed"}`))
						return nil
					}
					enrolledMsg, _ := json.Marshal(map[string]string{
						"type":         "enrolled",
						"agent_secret": rawSecret,
					})
					// No concurrent writer exists pre-registration, so a
					// direct write is safe; nothing has been committed, so a
					// failed send simply closes and the token stays usable.
					if err := ws.WriteMessage(websocket.TextMessage, enrolledMsg); err != nil {
						log.Printf("failed to send enrolled frame to %s: %v — closing connection", machineID, err)
						return nil
					}
					freshPendingTokenHash = tokenHash
					freshPendingSecretHash = secretHash
					log.Printf("new agent issued enrollment secret, awaiting commit: %s (%s)", m.Hostname, machineID)
				} else if hasCredential {
					// A durable credential already exists for this machine_id. A
					// generic install token must NOT silently replace it (that would
					// let any valid token hijack an enrolled machine and lock out
					// the legitimate agent). Require an explicit revoke first, or a
					// targeted re-enrollment token minted for this specific machine.
					// The token is deliberately left unconsumed so a legitimate
					// re-enroll can proceed after revocation.
					log.Printf("rejecting enrollment for %s (%s): machine already has a durable credential; revoke before re-enrolling", m.Hostname, machineID)
					ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","message":"machine already enrolled; revoke its credential before re-enrolling"}`))
					return nil
				} else if knownMachine {
					log.Printf("rejecting known agent %s (%s): durable credential required", m.Hostname, machineID)
					ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","message":"durable credential required"}`))
					return nil
				} else {
					log.Printf("rejecting unknown agent %s (%s): no valid token", m.Hostname, machineID)
					ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","message":"invalid or used token"}`))
					return nil
				}

				if freshPendingTokenHash != "" {
					// Fresh enrollment is not an established agent yet: it
					// stays unregistered (no command routing), behind the
					// auth gate, and inside the fixed auth window until
					// enrollment_committed commits (see that handler). The
					// subsequent metrics upsert in this case may already show
					// the row online; the exit path flips it offline if
					// commit never happens.
					log.Printf("agent awaiting enrollment commit: %s (%s)", m.Hostname, machineID)
				} else {
					s.registerAgentConnection(machineID, agent)
					completeAuth()
					ws.SetReadDeadline(time.Now().Add(agentIdleTimeout))
					log.Printf("agent registered: %s (%s)", m.Hostname, machineID)
				}
			}

			// The authenticated machineID is authoritative — bind this frame to
			// it so all downstream writes (upsert, metrics, latency) use it
			// regardless of what the payload claimed.
			m.MachineID = machineID

			// Calculate latency from sent_at.
			if m.SentAt != "" {
				sentTime, err := time.Parse(time.RFC3339Nano, m.SentAt)
				if err == nil {
					latencyMs := time.Since(sentTime).Milliseconds()
					if latencyMs < 0 {
						latencyMs = 0
					}
					machineLatencyMu.Lock()
					machineLatency[m.MachineID] = latencyMs
					machineLatencyMu.Unlock()
				}
			}

			s.upsertMachine(m)
			s.storeMetrics(m)

			// Enrich the metrics broadcast with latency.
			machineLatencyMu.RLock()
			lat := machineLatency[m.MachineID]
			machineLatencyMu.RUnlock()

			// Inject latency_ms by appending before the closing '}' to avoid
			// a full JSON unmarshal+marshal round-trip on the hot path.
			var enrichedData []byte
			if i := bytes.LastIndexByte(msg, '}'); i >= 0 {
				suffix := strconv.AppendInt([]byte(",\"latency_ms\":"), lat, 10)
				suffix = append(suffix, '}')
				enrichedData = append(append([]byte(nil), msg[:i]...), suffix...)
			} else {
				// Malformed JSON — fall back to unmarshal/marshal.
				enriched := make(map[string]interface{})
				json.Unmarshal(msg, &enriched)
				enriched["latency_ms"] = lat
				enrichedData, _ = json.Marshal(enriched)
			}
			broadcastSSE(enrichedData)

		case "services":
			var sm ServicesMessage
			if err := json.Unmarshal(msg, &sm); err != nil {
				log.Printf("invalid services JSON: %v", err)
				continue
			}
			mid := machineID
			if mid == "" {
				mid = sm.MachineID
			}
			s.upsertServices(mid, sm.Services)
			framed := []byte(fmt.Sprintf("event: services\ndata: %s\n\n", msg))
			broadcastSSE(framed)

		case "containers":
			var cm ContainersMessage
			if err := json.Unmarshal(msg, &cm); err != nil {
				log.Printf("invalid containers JSON: %v", err)
				continue
			}
			mid := machineID
			if mid == "" {
				mid = cm.MachineID
			}
			s.upsertContainers(mid, cm.Containers)
			framed := []byte(fmt.Sprintf("event: containers\ndata: %s\n\n", msg))
			broadcastSSE(framed)

		case "hardware_info":
			// Static hardware snapshot — store the raw JSON as-is so adding
			// new fields doesn't require another hub deploy.
			//
			// UPSERT (defense-in-depth): if the row doesn't exist yet (e.g. a
			// future agent reorder, or a buggy agent that sends hardware_info
			// before its first metric), a plain UPDATE would silently affect 0
			// rows and the snapshot would be lost. The placeholder hostname is
			// overwritten by the next upsertMachine call when the metric
			// arrives.
			var hw struct {
				MachineID string `json:"machine_id"`
			}
			if err := json.Unmarshal(msg, &hw); err != nil {
				log.Printf("invalid hardware_info JSON: %v", err)
				continue
			}
			mid := machineID
			if mid == "" {
				mid = hw.MachineID
			}
			if _, err := s.db.Exec(`
				INSERT INTO machines (id, hostname, status, hardware_info)
				VALUES (?, '', 'offline', ?)
				ON CONFLICT(id) DO UPDATE SET hardware_info = excluded.hardware_info
			`, mid, string(msg)); err != nil {
				log.Printf("store hardware_info: %v", err)
			}

		case "agent_running_version":
			// Phase 8 — agent reported its current binary SHA on connect.
			// Phase 9 added the optional `os` field so per-platform
			// SHA tracking can route the right announce on next reconnect.
			var versionMsg struct {
				Type   string `json:"type"`
				SHA256 string `json:"sha256"`
				OS     string `json:"os"`
				// UpdateProtocol is absent (0) on every agent built before
				// signature verification existed. The hub uses that to decide
				// whether announcing an update to it is safe at all.
				UpdateProtocol int `json:"update_protocol"`
				// UpdateTransportOK is the agent's own answer to whether its
				// BLOXOS_HUB permits self-update, computed from the URL it is
				// actually connected to rather than guessed from PUBLIC_URL.
				UpdateTransportOK bool `json:"update_transport_ok"`
				// UpdateKeyPinned is the agent's own answer to whether it has
				// a usable pinned update key on disk. Absent (false) on any
				// agent built before this field existed — encoding/json
				// leaves it at the zero value, which is the fail-closed
				// answer: no reported key means withhold, never "assume yes".
				UpdateKeyPinned bool `json:"update_key_pinned"`
			}
			if err := json.Unmarshal(msg, &versionMsg); err != nil {
				log.Printf("invalid agent_running_version JSON: %v", err)
				continue
			}
			if versionMsg.SHA256 != "" && machineID != "" {
				s.recordAgentRunningVersion(machineID, agentVersionReport{
					RunningSHA:     versionMsg.SHA256,
					OS:             versionMsg.OS,
					UpdateProtocol: versionMsg.UpdateProtocol,
					TransportOK:    versionMsg.UpdateTransportOK,
					KeyPinned:      versionMsg.UpdateKeyPinned,
				})
			}

		case aisessions.MessageType:
			// Read-only AI tool session metadata. Bound to the authenticated
			// machine and to the registered socket; see ingestAISessions.
			s.ingestAISessions(machineID, agent, msg)

		case "command_response":
			var resp CommandResponse
			if err := json.Unmarshal(msg, &resp); err != nil {
				log.Printf("invalid command_response JSON: %v", err)
				continue
			}
			pendingCmdsMu.Lock()
			ch, ok := pendingCmds[resp.ID]
			if ok {
				delete(pendingCmds, resp.ID)
			}
			pendingCmdsMu.Unlock()
			if ok {
				ch <- resp
			}

		case "enrollment_committed":
			// Sent by the agent only after it has durably saved a secret to
			// disk (see agent/main.go's enrolled-frame handling) — either a
			// freshly issued one (already active hub-side, nothing to
			// promote) or a staged pending replacement. Confirmation is the
			// ONLY thing that tells the agent it may drop BLOXOS_TOKEN; if
			// promotion doesn't unambiguously succeed, the connection is
			// closed rather than left in an ambiguous state — the agent's
			// disconnect/reconnect cycle then exercises the pending-secret
			// recovery path in validateAgentSecret, which gets its own
			// chance to promote (or correctly keeps failing if the pending
			// state is genuinely gone/expired).
			// This connection was already registered (either pre-loop for
			// ordinary secret auth, or at the end of the metrics-frame block
			// for token-based/staged enrollment) before any "enrollment_committed"
			// frame could arrive, so announceVersionToAgent's goroutine may
			// already be writing — every send below MUST go through
			// agent.writeLocked, never direct to ws.
			if freshPendingTokenHash != "" {
				// Fresh enrollment: this is the commit point. The token is
				// consumed and the credential stored in one transaction that
				// re-checks expiry, target binding, and that no credential
				// appeared meanwhile. On any failure nothing is written and
				// the connection closes without confirmation; the agent's
				// reconnect retries with the token if it is still valid.
				if err := s.consumeTokenAndStoreCredential(freshPendingTokenHash, machineID, freshPendingSecretHash); err != nil {
					log.Printf("fresh enrollment for %s could not be committed: %v — closing connection", machineID, err)
					return nil
				}
				confirmedHash := freshPendingSecretHash
				freshPendingTokenHash, freshPendingSecretHash = "", ""
				// Only now is this socket an established agent: register
				// exactly once, release the auth gate, and move to the idle
				// deadline. Registration spawns the announce writer, so the
				// confirmation below must go through the per-agent lock.
				s.registerAgentConnection(machineID, agent)
				completeAuth()
				ws.SetReadDeadline(time.Now().Add(agentIdleTimeout))
				confirmMsg, _ := json.Marshal(map[string]string{"type": "enrollment_confirmed", "secret_sha256": confirmedHash})
				if err := agent.writeLocked(websocket.TextMessage, confirmMsg); err != nil {
					log.Printf("committed fresh enrollment for %s but failed to send enrollment_confirmed: %v — closing connection", machineID, err)
					return nil
				}
				log.Printf("committed fresh enrollment and sent enrollment_confirmed: %s", machineID)
				continue
			}
			if stagedPendingSecretHash == "" {
				log.Printf("rejecting enrollment_committed from %s: no pending credential was staged on this connection", machineID)
				return nil
			}
			promoted, err := s.promotePendingCredentialCAS(machineID, stagedPendingSecretHash)
			if err != nil {
				log.Printf("failed to promote pending credential for %s: %v — closing connection", machineID, err)
				return nil
			}
			if !promoted {
				log.Printf("pending credential for %s no longer matches or has expired — closing connection so it can retry", machineID)
				return nil
			}
			// Promotion has committed in SQLite at this point. "Confirmed" is
			// a separate claim — it requires the frame to actually reach the
			// agent, so it is only logged after a successful write.
			// secret_sha256 is captured BEFORE clearing stagedPendingSecretHash
			// below — it is exactly the hash the agent's local pending file
			// must match for it to promote.
			promotedSecretHash := stagedPendingSecretHash
			stagedPendingSecretHash = ""
			confirmMsg, _ := json.Marshal(map[string]string{"type": "enrollment_confirmed", "secret_sha256": promotedSecretHash})
			if err := agent.writeLocked(websocket.TextMessage, confirmMsg); err != nil {
				log.Printf("promoted pending credential for %s but failed to send enrollment_confirmed: %v — closing connection", machineID, err)
				return nil
			}
			log.Printf("promoted pending credential to active and sent enrollment_confirmed for %s", machineID)

		default:
			log.Printf("unknown message type from agent: %s", envelope.Type)
		}
	}
}

// validateAgentToken checks a token against the DB (Finding #1).
// Returns the token hash so the caller can mark it as used after enrollment,
// plus the token's target machine (unset for an ordinary, unbound Add
// Machine token; set for a Windows re-enrollment token minted for exactly
// one machine_id).
func (s *Server) validateAgentToken(token string) (string, sql.NullString, error) {
	h := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(h[:])

	var expiresAt string
	var used bool
	var targetMachineID sql.NullString
	err := s.db.QueryRow(`SELECT expires_at, used, target_machine_id FROM tokens WHERE token_hash = ?`, tokenHash).Scan(&expiresAt, &used, &targetMachineID)
	if err == sql.ErrNoRows {
		return "", sql.NullString{}, fmt.Errorf("invalid token")
	}
	if err != nil {
		return "", sql.NullString{}, fmt.Errorf("database error: %w", err)
	}
	if used {
		return "", sql.NullString{}, fmt.Errorf("token already used")
	}
	if err := checkTokenExpiry(expiresAt); err != nil {
		return "", sql.NullString{}, err
	}

	log.Printf("agent token validated successfully")
	return tokenHash, targetMachineID, nil
}

// checkTokenExpiry parses a tokens.expires_at value (either stored format)
// and reports whether it has passed. It runs at initial validation and again
// inside the transactions that consume a token, so a token that expires
// between the handshake and the credential mutation is still refused.
func checkTokenExpiry(expiresAt string) error {
	expTime, err := time.Parse("2006-01-02 15:04:05", expiresAt)
	if err != nil {
		expTime, err = time.Parse(time.RFC3339, expiresAt)
		if err != nil {
			return fmt.Errorf("invalid expiry format")
		}
	}
	if time.Now().After(expTime) {
		return fmt.Errorf("token expired")
	}
	return nil
}

// consumeTokenAndStoreCredential atomically consumes tokenHash and stores
// machineID's first durable credential. Expiry, the target check, and the
// no-existing-credential guard are all repeated here inside the transaction —
// not just by the earlier in-memory decisions in handleAgentWS — so a
// validate-then-consume race can't let a stale read win: this is the last
// write before the credential exists, and it must not trust anything computed
// outside its own transaction.
func (s *Server) consumeTokenAndStoreCredential(tokenHash, machineID, secretHash string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var targetMachineID sql.NullString
	var expiresAt string
	if err := tx.QueryRow(`SELECT target_machine_id, expires_at FROM tokens WHERE token_hash = ? AND used = FALSE`, tokenHash).Scan(&targetMachineID, &expiresAt); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("install token already consumed")
		}
		return err
	}
	if err := checkTokenExpiry(expiresAt); err != nil {
		return fmt.Errorf("install %w", err)
	}
	if targetMachineID.Valid && targetMachineID.String != machineID {
		return fmt.Errorf("install token is bound to a different machine")
	}
	var existing int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM agent_credentials WHERE machine_id = ?`, machineID).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return fmt.Errorf("machine already has a durable credential")
	}

	res, err := tx.Exec(`UPDATE tokens SET used = TRUE WHERE token_hash = ? AND used = FALSE`, tokenHash)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("install token already consumed")
	}

	if _, err := tx.Exec(`INSERT INTO agent_credentials (machine_id, secret_hash, created_at, last_used_at)
		VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, machineID, secretHash); err != nil {
		return err
	}
	return tx.Commit()
}

// pendingCredentialWindow bounds how long a staged pending secret remains
// promotable. Comfortably longer than install.ps1's 30-second completion
// gate so a slow-but-legitimate handoff never races its own timeout, while
// still short enough that an abandoned attempt doesn't leave a promotable
// credential lying around indefinitely.
const pendingCredentialWindow = 5 * time.Minute

// stageTargetedCredential atomically consumes a target-bound token and
// records pendingSecretHash as machineID's PENDING credential — the active
// secret_hash is left completely unchanged. This is deliberately not a
// replace: the old secret keeps authenticating until the agent proves the
// new one is durably saved and the hub receives "enrollment_committed" (see
// promotePendingCredentialCAS). A machine with no active credential at
// all is rejected here too — staging a replacement requires something to
// replace; handleWindowsReenrollment already enforces this at mint time, but
// the credential could theoretically be revoked in the window between
// minting and use, so it's re-checked inside this transaction.
func (s *Server) stageTargetedCredential(tokenHash, machineID, pendingSecretHash string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var targetMachineID sql.NullString
	var expiresAt string
	if err := tx.QueryRow(`SELECT target_machine_id, expires_at FROM tokens WHERE token_hash = ? AND used = FALSE`, tokenHash).Scan(&targetMachineID, &expiresAt); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("install token already consumed")
		}
		return err
	}
	if err := checkTokenExpiry(expiresAt); err != nil {
		return fmt.Errorf("install %w", err)
	}
	if !targetMachineID.Valid || targetMachineID.String != machineID {
		return fmt.Errorf("install token is not bound to this machine")
	}

	var activeSecretHash string
	if err := tx.QueryRow(`SELECT secret_hash FROM agent_credentials WHERE machine_id = ?`, machineID).Scan(&activeSecretHash); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("no active credential to stage a re-enrollment against")
		}
		return err
	}

	res, err := tx.Exec(`UPDATE tokens SET used = TRUE WHERE token_hash = ? AND used = FALSE`, tokenHash)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("install token already consumed")
	}

	pendingExpiresAt := time.Now().Add(pendingCredentialWindow)
	if _, err := tx.Exec(
		`UPDATE agent_credentials SET pending_secret_hash = ?, pending_expires_at = ? WHERE machine_id = ?`,
		pendingSecretHash, pendingExpiresAt.Format(time.RFC3339), machineID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// promotePendingCredentialCAS atomically promotes machineID's pending
// credential to active via a genuine SQL compare-and-swap: the promoting
// UPDATE's WHERE clause is constrained to the exact (machine_id,
// pending_secret_hash, pending_expires_at) tuple read a moment earlier, and
// RowsAffected() == 1 is the only thing that means "promoted" — never the
// earlier SELECT by itself. A prior version checked the hash in a SELECT and
// then wrote an UPDATE keyed only on machine_id; a stale acknowledgement
// racing a newer re-stage could pass that SELECT and then clobber the newer
// pending value on the unconstrained UPDATE. Constraining the UPDATE itself
// closes that: two concurrent callers can each read the same row, but only
// the one whose exact (hash, expiry) pair still matches at write time can
// affect any rows — the loser's UPDATE matches zero rows and is a no-op.
//
// Fails closed on expiry: pending_secret_hash without a present, parseable,
// strictly-future pending_expires_at is never promotable. A NULL or
// unparseable expiry is opportunistically cleared (still via a CAS scoped to
// the exact hash/expiry just read, so a concurrent re-stage isn't
// clobbered) rather than left indefinitely promotable.
//
// The Go-side time.Now() check below is a fast pre-filter, not the actual
// guard: real time can cross the deadline in the gap between that check and
// the UPDATE actually executing (lock contention, GC pause, a slow disk).
// The promoting UPDATE's WHERE clause therefore ALSO requires
// julianday(pending_expires_at) > julianday('now'), evaluated by SQLite at
// the moment the write happens — that is the predicate that actually can't
// be raced, because nothing can execute between "the database checks its own
// clock" and "the database applies the write" from outside the transaction.
func (s *Server) promotePendingCredentialCAS(machineID, expectedPendingHash string) (bool, error) {
	return s.promotePendingCredentialCASAt(machineID, expectedPendingHash, time.Now)
}

// promotePendingCredentialCASAt is promotePendingCredentialCAS with the
// clock used for the Go-side pre-filter injectable, so a test can construct
// the exact race the DB-time predicate exists to defeat: a nowFn that still
// claims "not expired yet" while the real clock (and thus SQLite's
// julianday('now')) has already moved past the deadline. Production always
// calls this with time.Now; only tests substitute anything else. The SQL
// UPDATE's own julianday('now') is never affected by nowFn — it is always
// the real database clock.
func (s *Server) promotePendingCredentialCASAt(machineID, expectedPendingHash string, nowFn func() time.Time) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var pendingHash, pendingExpiresAtRaw sql.NullString
	if err := tx.QueryRow(
		`SELECT pending_secret_hash, pending_expires_at FROM agent_credentials WHERE machine_id = ?`, machineID,
	).Scan(&pendingHash, &pendingExpiresAtRaw); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if !pendingHash.Valid || pendingHash.String != expectedPendingHash {
		return false, nil
	}

	clearMalformed := func() (bool, error) {
		// Scoped to the exact hash+expiry just read: if a concurrent caller
		// already re-staged a different pending value, this affects zero rows
		// and leaves that newer value alone.
		if _, err := tx.Exec(
			`UPDATE agent_credentials SET pending_secret_hash = NULL, pending_expires_at = NULL
			 WHERE machine_id = ? AND pending_secret_hash = ? AND pending_expires_at IS ?`,
			machineID, expectedPendingHash, pendingExpiresAtRaw,
		); err != nil {
			return false, err
		}
		return false, tx.Commit()
	}

	if !pendingExpiresAtRaw.Valid {
		return clearMalformed()
	}
	expTime, perr := time.Parse(time.RFC3339, pendingExpiresAtRaw.String)
	if perr != nil {
		return clearMalformed()
	}
	if !nowFn().Before(expTime) {
		// Not strictly before the deadline — covers both "in the past" and
		// "exactly at the deadline" as expired, matching the "strictly in the
		// future" requirement. This is the fast pre-filter only; the UPDATE
		// below re-checks against the real DB clock regardless.
		return clearMalformed()
	}

	// The julianday comparison is the actual, unrace-able guard: SQLite
	// evaluates julianday('now') against its own clock at the instant this
	// statement executes, inside the same transaction as the write it
	// gates. Even if nowFn (Go-side) was stale, wrong, or simply unlucky
	// timing let an already-past deadline through the pre-filter above,
	// this predicate independently fails and the UPDATE matches zero rows.
	res, err := tx.Exec(
		`UPDATE agent_credentials SET secret_hash = ?, pending_secret_hash = NULL, pending_expires_at = NULL, last_used_at = CURRENT_TIMESTAMP
		 WHERE machine_id = ? AND pending_secret_hash = ? AND pending_expires_at = ? AND julianday(pending_expires_at) > julianday('now')`,
		expectedPendingHash, machineID, expectedPendingHash, pendingExpiresAtRaw.String,
	)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if rows != 1 {
		return false, nil
	}
	return true, tx.Commit()
}

// secretSHA256Hex is the same hex(SHA-256(secret)) computation used
// everywhere a raw secret is turned into its lookup/comparison hash
// (validateAgentSecret, lookupActiveCredential, generateAgentSecret) —
// factored out so an enrollment_confirmed frame can name the exact
// credential it confirms without duplicating the hashing inline.
func secretSHA256Hex(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

// generateAgentSecret creates a 32-byte random secret for agent enrollment.
// Returns the hex-encoded raw secret and its SHA-256 hash.
func generateAgentSecret() (raw string, hash string, err error) {
	secretBytes := make([]byte, 32)
	if _, err := cryptoRand.Read(secretBytes); err != nil {
		return "", "", fmt.Errorf("generate random secret: %w", err)
	}
	raw = hex.EncodeToString(secretBytes)
	h := sha256.Sum256([]byte(raw))
	hash = hex.EncodeToString(h[:])
	return raw, hash, nil
}

// storeAgentCredential saves (or replaces) the hashed secret for a machine.
func (s *Server) storeAgentCredential(machineID, secretHash string) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO agent_credentials (machine_id, secret_hash, created_at, last_used_at)
		VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, machineID, secretHash)
	return err
}

func (s *Server) machineHasCredential(machineID string) bool {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_credentials WHERE machine_id = ?`, machineID).Scan(&count); err != nil {
		log.Printf("failed checking agent credential for %s: %v", machineID, err)
		return false
	}
	return count > 0
}

// lookupActiveCredential looks up a credential by the SHA-256 hash of the
// active secret. Returns (machineID, true) on a hit, refreshing last_used_at.
func (s *Server) lookupActiveCredential(secretHash string) (string, bool) {
	var machineID string
	if err := s.db.QueryRow(`SELECT machine_id FROM agent_credentials WHERE secret_hash = ?`, secretHash).Scan(&machineID); err != nil {
		return "", false
	}
	_, _ = s.db.Exec(`UPDATE agent_credentials SET last_used_at = CURRENT_TIMESTAMP WHERE secret_hash = ?`, secretHash)
	return machineID, true
}

// validateAgentSecret looks up a credential by the SHA-256 hash of the
// provided secret. Returns the associated machine_id and whether THIS call
// is what promoted a pending secret to active (the caller uses that to send
// enrollment_confirmed once the connection is up — see handleAgentWS).
//
// Beyond the active secret, this also recognizes an unexpired PENDING
// secret: the recovery path for a targeted re-enrollment whose
// "enrollment_committed" acknowledgement never reached the hub (lost after a
// successful disk write, connection dropped immediately after storage, or
// the service restarted between the two). Reconnecting with the pending
// secret is itself proof the agent durably has it, so it's promoted to
// active right here (via the same CAS as the explicit-ack path) rather than
// waiting for an ack that may never come.
func (s *Server) validateAgentSecret(secret string) (machineID string, promoted bool, err error) {
	h := sha256.Sum256([]byte(secret))
	secretHash := hex.EncodeToString(h[:])

	if mid, ok := s.lookupActiveCredential(secretHash); ok {
		return mid, false, nil
	}

	var pendingMachineID string
	qerr := s.db.QueryRow(`SELECT machine_id FROM agent_credentials WHERE pending_secret_hash = ?`, secretHash).Scan(&pendingMachineID)
	if qerr == sql.ErrNoRows {
		return "", false, fmt.Errorf("invalid agent secret")
	}
	if qerr != nil {
		return "", false, fmt.Errorf("database error: %w", qerr)
	}

	didPromote, perr := s.promotePendingCredentialCAS(pendingMachineID, secretHash)
	if perr != nil {
		return "", false, fmt.Errorf("database error: %w", perr)
	}
	if didPromote {
		return pendingMachineID, true, nil
	}
	// Lost a race (already promoted by an in-flight enrollment_committed, or
	// just expired). If something else already promoted it, it's now active —
	// but THIS call didn't do the promoting, so promoted=false here.
	if mid, ok := s.lookupActiveCredential(secretHash); ok {
		return mid, false, nil
	}
	return "", false, fmt.Errorf("invalid agent secret")
}

// revokeAgentCredential removes the stored credential for a machine.
func (s *Server) revokeAgentCredential(machineID string) error {
	_, err := s.db.Exec(`DELETE FROM agent_credentials WHERE machine_id = ?`, machineID)
	return err
}

// handleRevokeAgentCredential removes only a machine's durable enrollment
// credential. The machine and all of its history remain intact so the same
// host can re-enroll under its stable machine ID with a fresh install token.
// Any currently authenticated socket is closed after the database commit;
// otherwise revocation would not take effect until that connection happened
// to reconnect.
func (s *Server) handleRevokeAgentCredential(c echo.Context) error {
	s.agentAuthMu.Lock()
	defer s.agentAuthMu.Unlock()
	machineID := c.Param("id")

	tx, err := s.db.Begin()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to start transaction"})
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRow(`SELECT 1 FROM machines WHERE id = ?`, machineID).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "machine not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to query machine"})
	}

	result, err := tx.Exec(`DELETE FROM agent_credentials WHERE machine_id = ?`, machineID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to revoke credential"})
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to confirm credential revocation"})
	}
	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to commit credential revocation"})
	}

	// Snapshot the connection under the registry lock, then release the lock
	// before closing the socket. The read-loop performs the normal symmetric
	// cleanup; unregisterAgentConnection below makes the result synchronous and
	// remains safe if that cleanup already won the race.
	s.agentsMu.RLock()
	agent := s.agents[machineID]
	s.agentsMu.RUnlock()
	connectionClosed := agent != nil && agent.Conn != nil
	if connectionClosed {
		_ = agent.Conn.Close()
		s.unregisterAgentConnection(machineID, agent)
	}

	credentialExisted := rows > 0
	log.Printf("agent credential revoked: machine_id=%s credential_existed=%t connection_closed=%t",
		machineID, credentialExisted, connectionClosed)
	return c.JSON(http.StatusOK, map[string]interface{}{
		"machine_id":         machineID,
		"credential_existed": credentialExisted,
		"connection_closed":  connectionClosed,
	})
}

// generateFirstRunToken creates a one-time token on first startup when no tokens and no machines exist.
func (s *Server) generateFirstRunToken() {
	var tokenCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tokens`).Scan(&tokenCount); err != nil {
		log.Printf("first-run check: token count error: %v", err)
		return
	}
	if tokenCount > 0 {
		return
	}

	var machineCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM machines`).Scan(&machineCount); err != nil {
		log.Printf("first-run check: machine count error: %v", err)
		return
	}
	if machineCount > 0 {
		return
	}

	// First run: generate a real token with 1-hour expiry.
	token := uuid.New().String()
	h := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(h[:])
	expiresAt := time.Now().Add(1 * time.Hour)

	_, err := s.db.Exec(`INSERT INTO tokens (token_hash, expires_at) VALUES (?, ?)`, tokenHash, expiresAt)
	if err != nil {
		log.Printf("first-run: failed to insert token: %v", err)
		return
	}

	// Write token to file instead of logging it (security hardening).
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Printf("first-run: cannot determine home dir: %v", err)
		return
	}
	tokenDir := homeDir + "/.bloxos"
	if err := os.MkdirAll(tokenDir, 0700); err != nil {
		log.Printf("first-run: cannot create %s: %v", tokenDir, err)
		return
	}
	tokenFile := tokenDir + "/first-run-token"
	if err := os.WriteFile(tokenFile, []byte(token), 0600); err != nil {
		log.Printf("first-run: cannot write token file: %v", err)
		return
	}
	log.Printf("First-run token written to %s (expires in 1 hour)", tokenFile)
}
