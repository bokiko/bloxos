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

// registerAgentConnection finalises an authenticated WebSocket handshake.
// It is the single chokepoint between "agent authed" and "agent fully
// online", so any per-connect work (registry insert, version announce,
// future hooks) must go here. Both auth paths — durable-secret reconnect
// and token-based fresh enrolment — call this exactly once per connection,
// which guarantees Phase-8 auto-update propagates on reconnect (the bug
// that originally motivated extracting this helper).
func registerAgentConnection(machineID string, agent *ConnectedAgent) {
	agent.MachineID = machineID
	agentsMu.Lock()
	agents[machineID] = agent
	agentsMu.Unlock()
	go announceVersionToAgent(machineID, agent)
}

// unregisterAgentConnection is the symmetric cleanup for
// registerAgentConnection. Called from handleAgentWS's read-loop exit
// path. Only flips the machine to offline AND deletes the agents map
// entry when the entry under machineID still points at THIS connection
// — otherwise we'd false-offline a fast-reconnecting agent whose new
// handler already overwrote the entry. Empty machineID (auth never
// succeeded) is a no-op.
func unregisterAgentConnection(machineID string, agent *ConnectedAgent) {
	if machineID == "" {
		return
	}
	agentsMu.Lock()
	current, ok := agents[machineID]
	stillOurs := ok && current == agent
	if stillOurs {
		delete(agents, machineID)
	}
	agentsMu.Unlock()
	if stillOurs {
		markOffline(machineID)
	}
}

func handleCreateToken(c echo.Context) error {
	ip := getRealIP(c)
	if !rateLimiter.Allow("token_create", ip, 3) {
		log.Printf("rate limit exceeded: token_create from %s", ip)
		return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
	}

	token := uuid.New().String()
	h := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(h[:])
	expiresAt := time.Now().Add(15 * time.Minute)

	_, err := db.Exec(`INSERT INTO tokens (token_hash, expires_at) VALUES (?, ?)`, tokenHash, expiresAt)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	httpBase, wsBase := publicAndWebsocketBase(c)
	command, caURL, caSHA256 := buildInstallCommand(httpBase, wsBase, token)

	resp := map[string]interface{}{
		"token":      token,
		"expires_at": expiresAt.Format(time.RFC3339),
		"command":    command,
	}
	if caURL != "" {
		resp["ca_url"] = caURL
		resp["ca_sha256"] = caSHA256
	}
	return c.JSON(http.StatusOK, resp)
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
CA_URL="${BLOXOS_CA_URL:-}"
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

curl_fetch_bootstrap() {
  local url="$1"
  shift
  if [[ "$url" == https://* ]]; then
    curl --proto '=https' --tlsv1.2 -fsSLk "$@" "$url"
  else
    curl -fsSL "$@" "$url"
  fi
}

echo "Hub: $HUB_HTTP"
echo "Arch: $ARCH"

CA_CURL_ARGS=()
AGENT_CA_ENV=""

if [[ -n "$CA_URL" ]]; then
  if [[ -z "$CA_SHA256" ]]; then
    echo "BLOXOS_CA_SHA256 must be set when BLOXOS_CA_URL is provided"
    exit 1
  fi

  echo "Bootstrapping trusted CA..."
  TMP_CA=$(mktemp)
  trap 'rm -f "$TMP_CA"' EXIT
  curl_fetch_bootstrap "$CA_URL" -o "$TMP_CA"
  ACTUAL_CA_SHA=$(sha256sum "$TMP_CA" | awk '{print $1}')
  if [[ "$ACTUAL_CA_SHA" != "$CA_SHA256" ]]; then
    echo "CA fingerprint mismatch"
    echo "Expected: $CA_SHA256"
    echo "Actual:   $ACTUAL_CA_SHA"
    exit 1
  fi

  sudo mkdir -p /etc/bloxos
  # 0755 so the next curl (running as the invoking user, not root) can traverse
  # /etc/bloxos to read ca.crt. The agent-secret file inside is 0600.
  sudo chmod 755 /etc/bloxos
  sudo install -m 0644 "$TMP_CA" /etc/bloxos/ca.crt
  CA_CURL_ARGS+=(--cacert /etc/bloxos/ca.crt)
  AGENT_CA_ENV='Environment="BLOXOS_CA_CERT=/etc/bloxos/ca.crt"'
fi

# Download agent binary.
echo "Downloading agent binary..."
curl_fetch "${HUB_HTTP}/download/agent?arch=${ARCH}" "${CA_CURL_ARGS[@]}" -o /tmp/bloxos-agent
chmod +x /tmp/bloxos-agent

# Create system user (if not exists).
if ! id -u bloxos &>/dev/null; then
  sudo useradd -r -s /usr/sbin/nologin bloxos || true
fi

# Install binary.
sudo mv /tmp/bloxos-agent /usr/local/bin/bloxos-agent

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
StartLimitInterval=60
StartLimitBurst=3

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
	return c.String(http.StatusOK, script)
}

func publicAndWebsocketBase(c echo.Context) (string, string) {
	publicURL := os.Getenv("PUBLIC_URL")
	if publicURL != "" {
		httpBase := publicURL
		wsBase := strings.Replace(publicURL, "https://", "wss://", 1)
		wsBase = strings.Replace(wsBase, "http://", "ws://", 1)
		return httpBase, wsBase
	}

	host := c.Request().Host
	proto := "ws"
	httpProto := "http"
	if c.Request().TLS != nil {
		proto = "wss"
		httpProto = "https"
	}
	return fmt.Sprintf("%s://%s", httpProto, host), fmt.Sprintf("%s://%s", proto, host)
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

func buildInstallCommand(httpBase, wsBase, token string) (command string, caURL string, caSHA256 string) {
	curlFlags := "-fsSL"
	if strings.HasPrefix(httpBase, "https://") {
		if caPEM, caPath, err := loadBootstrapCACert(); err == nil {
			caURL = httpBase + "/download/ca.crt"
			sum := sha256.Sum256(caPEM)
			caSHA256 = hex.EncodeToString(sum[:])
			curlFlags = "-fsSLk"
			log.Printf("install bootstrap: using CA cert %s", caPath)
		} else if !os.IsNotExist(err) {
			log.Printf("WARNING: install bootstrap CA unavailable: %v", err)
		}
	}

	command = fmt.Sprintf("export BLOXOS_HUB=%s BLOXOS_TOKEN=%s", wsBase, token)
	if caURL != "" {
		command += fmt.Sprintf(" BLOXOS_CA_URL=%s BLOXOS_CA_SHA256=%s", caURL, caSHA256)
	}
	command += fmt.Sprintf("; curl %s %s/install.sh | bash", curlFlags, httpBase)
	return command, caURL, caSHA256
}

func handleDownloadAgent(c echo.Context) error {
	// Phase 9: per-OS binary resolution. The target OS is detected from
	// either the explicit ?os= query parameter (PowerShell installer
	// passes this) or the User-Agent string.
	osName := strings.ToLower(strings.TrimSpace(c.QueryParam("os")))
	if osName == "" {
		ua := strings.ToLower(c.Request().UserAgent())
		if strings.Contains(ua, "windows") {
			osName = "windows"
		} else {
			osName = "linux"
		}
	}

	var candidates []string
	switch osName {
	case "windows":
		candidates = []string{
			os.Getenv("BLOXOS_AGENT_BINARY_WINDOWS"),
			"./agent/bloxos-agent.exe",
			"/usr/local/bin/bloxos-agent.exe",
		}
	default:
		osName = "linux"
		candidates = []string{
			os.Getenv("BLOXOS_AGENT_BINARY"),
			"./agent/bloxos-agent",
			"/usr/local/bin/bloxos-agent",
		}
	}

	binaryPath := ""
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			binaryPath = p
			break
		}
	}
	if binaryPath == "" {
		return c.JSON(http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("agent binary for os=%s not found; set BLOXOS_AGENT_BINARY%s env var", osName, map[string]string{"windows": "_WINDOWS"}[osName]),
		})
	}

	arch := c.QueryParam("arch")
	log.Printf("agent download: os=%s arch=%s path=%s", osName, arch, binaryPath)
	return c.File(binaryPath)
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

func handleAgentWS(c echo.Context) error {
	ip := getRealIP(c)
	if !rateLimiter.Allow("ws_agent", ip, wsAgentRateLimit) {
		log.Printf("rate limit exceeded: ws_agent from %s", ip)
		return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
	}

	token := c.QueryParam("token")
	agentSecret := c.QueryParam("secret")

	// Auth priority: secret > token. If neither, reject.
	if token == "" && agentSecret == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "token or secret required"})
	}

	// Mode B: Agent reconnecting with durable secret.
	var secretMachineID string
	if agentSecret != "" {
		var err error
		secretMachineID, err = validateAgentSecret(agentSecret)
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

	// Mode A: Token-based enrollment (only if not already authenticated via secret).
	var tokenHash string
	tokenValidated := false
	if agentSecret == "" && token != "" {
		var err error
		tokenHash, err = validateAgentToken(token)
		if err != nil {
			tokenHash = ""
			log.Printf("agent token validation deferred (may be reconnecting agent): %v", err)
		} else {
			tokenValidated = true
		}
	}

	ws, wsErr := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if wsErr != nil {
		return wsErr
	}
	defer ws.Close()

	// If authenticated via secret, we already know the machine_id.
	var machineID string
	agent := &ConnectedAgent{Conn: ws}
	if secretMachineID != "" {
		machineID = secretMachineID
		registerAgentConnection(machineID, agent)
		log.Printf("agent authenticated via secret: machine_id=%s", machineID)
	}

	for {
		_, msg, err := ws.ReadMessage()
		if err != nil {
			log.Printf("agent disconnected: %v", err)
			unregisterAgentConnection(machineID, agent)
			return nil
		}

		var envelope struct {
			Type      string `json:"type"`
			MachineID string `json:"machine_id"`
		}
		if err := json.Unmarshal(msg, &envelope); err != nil {
			log.Printf("invalid JSON from agent: %v", err)
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

				// Check if this machine_id already exists (reconnecting agent).
				var existingID string
				knownMachine := db.QueryRow(`SELECT id FROM machines WHERE id = ?`, machineID).Scan(&existingID) == nil
				hasCredential := machineHasCredential(machineID)

				if knownMachine && secretMachineID == machineID {
					log.Printf("known agent reconnecting with durable credential: %s (%s)", m.Hostname, machineID)
				} else if tokenValidated {
					// New enrollment with valid token - consume it and issue durable secret.
					rawSecret, secretHash, err := generateAgentSecret()
					if err != nil {
						log.Printf("failed to generate agent secret: %v", err)
						ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","message":"enrollment failed"}`))
						return nil
					}
					if err := consumeTokenAndStoreCredential(tokenHash, machineID, secretHash); err != nil {
						log.Printf("failed to store agent credential: %v", err)
						ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","message":"enrollment failed"}`))
						return nil
					}
					enrolledMsg, _ := json.Marshal(map[string]string{
						"type":         "enrolled",
						"agent_secret": rawSecret,
					})
					ws.WriteMessage(websocket.TextMessage, enrolledMsg)
					log.Printf("new agent enrolled with durable secret: %s (%s)", m.Hostname, machineID)
				} else if knownMachine || hasCredential {
					log.Printf("rejecting known agent %s (%s): durable credential required", m.Hostname, machineID)
					ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","message":"durable credential required"}`))
					return nil
				} else {
					log.Printf("rejecting unknown agent %s (%s): no valid token", m.Hostname, machineID)
					ws.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","message":"invalid or used token"}`))
					return nil
				}

				registerAgentConnection(machineID, agent)
				log.Printf("agent registered: %s (%s)", m.Hostname, machineID)
			}

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

			upsertMachine(m)
			storeMetrics(m)

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
			mid := sm.MachineID
			if mid == "" {
				mid = machineID
			}
			upsertServices(mid, sm.Services)
			framed := []byte(fmt.Sprintf("event: services\ndata: %s\n\n", msg))
			broadcastSSE(framed)

		case "containers":
			var cm ContainersMessage
			if err := json.Unmarshal(msg, &cm); err != nil {
				log.Printf("invalid containers JSON: %v", err)
				continue
			}
			mid := cm.MachineID
			if mid == "" {
				mid = machineID
			}
			upsertContainers(mid, cm.Containers)
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
			mid := hw.MachineID
			if mid == "" {
				mid = machineID
			}
			if _, err := db.Exec(`
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
			}
			if err := json.Unmarshal(msg, &versionMsg); err != nil {
				log.Printf("invalid agent_running_version JSON: %v", err)
				continue
			}
			if versionMsg.SHA256 != "" && machineID != "" {
				recordAgentRunningVersion(machineID, versionMsg.SHA256, versionMsg.OS)
			}

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

		default:
			log.Printf("unknown message type from agent: %s", envelope.Type)
		}
	}
}

// handleWindowsInstallScript serves a PowerShell installer that mirrors
// install.sh: download the agent, register a Windows service, start it.
//
// The script is intentionally returned as plain text so a one-liner like
//   iex (irm https://hub.example/install.ps1)
// works as expected. It honours the BLOXOS_HUB / BLOXOS_TOKEN environment
// variables set by `gh api .../install-script` callers, and it requests
// admin via WindowsBuiltInRole before doing anything destructive.
func handleWindowsInstallScript(c echo.Context) error {
	// PowerShell raw block. Go raw strings can't contain backticks, and
	// PowerShell doesn't strictly need them here — we keep the script
	// backtick-free.
	script := `# BloxOS Windows Agent installer
$ErrorActionPreference = 'Stop'

# Require admin.
$current = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object Security.Principal.WindowsPrincipal($current)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Error "This installer must be run from an elevated PowerShell session."
    exit 1
}

$Hub = $env:BLOXOS_HUB
$Token = $env:BLOXOS_TOKEN
if (-not $Hub)   { Write-Error "BLOXOS_HUB must be set"; exit 1 }
if (-not $Token) { Write-Error "BLOXOS_TOKEN must be set"; exit 1 }

# Convert wss:// -> https://, ws:// -> http://
$HubHttp = $Hub -replace '^wss://','https://' -replace '^ws://','http://'
Write-Host "Hub:    $HubHttp"
Write-Host "WS hub: $Hub"

# install.ps1 is invoked via 'powershell.exe -ExecutionPolicy Bypass -File'.
# All HTTP downloads use curl.exe with -k for self-signed cert tolerance, so
# no PowerShell-side cert callback or SecurityProtocol pinning is needed.

# Stop + remove existing service if present.
$svc = Get-Service -Name BloxOSAgent -ErrorAction SilentlyContinue
if ($svc) {
    Write-Host "Existing BloxOSAgent service found, stopping..."
    if ($svc.Status -ne 'Stopped') {
        Stop-Service -Name BloxOSAgent -Force -ErrorAction SilentlyContinue
        Start-Sleep -Seconds 2
    }
    & sc.exe delete BloxOSAgent | Out-Null
    Start-Sleep -Seconds 2
}

# Wipe stale credentials from any prior install. The Windows agent runs as
# LocalSystem so its credential dir is C:\Windows\System32\config\systemprofile\.bloxos.
# Without this cleanup, a re-install would inherit the prior secret and the
# agent would attempt to authenticate with stale credentials instead of using
# the new enrollment token. (See agent/main.go credentialFilePath() and the
# secret>token priority in the WebSocket URL builder.)
$CredDir = "C:\Windows\System32\config\systemprofile\.bloxos"
if (Test-Path $CredDir) {
    Write-Host "Removing stale agent credentials at $CredDir ..."
    Remove-Item -Path $CredDir -Recurse -Force -ErrorAction SilentlyContinue
}

# Fetch the hub's local CA so the agent can validate wss:// without depending
# on the Windows system trust store. Linux install.sh does the equivalent
# (writes /etc/bloxos/ca.crt and sets BLOXOS_CA_CERT). Windows install.ps1
# historically did not — fleet trust silently relied on whatever ca.crt was
# already in .bloxos\ from a previous-era install.
$CaPath = Join-Path $CredDir "ca.crt"
if (-not (Test-Path $CredDir)) {
    New-Item -ItemType Directory -Path $CredDir -Force | Out-Null
}
Write-Host "Downloading hub CA certificate to $CaPath ..."
& curl.exe -ksfL -o $CaPath "$HubHttp/download/ca.crt"
if ($LASTEXITCODE -ne 0) {
    Write-Error "CA cert download failed (curl exit code $LASTEXITCODE)"
    exit 1
}
[Environment]::SetEnvironmentVariable("BLOXOS_CA_CERT", $CaPath, "Machine")

$InstallDir = "C:\Program Files\BloxOS"
$AgentExe   = Join-Path $InstallDir "bloxos-agent.exe"
if (-not (Test-Path $InstallDir)) {
    New-Item -ItemType Directory -Path $InstallDir | Out-Null
}

Write-Host "Downloading agent binary to $AgentExe ..."
$DownloadUrl = "$HubHttp/download/agent?os=windows"
# Use curl.exe (ships with Win10 1803+ and Win11) instead of Invoke-WebRequest.
# IWR uses .NET HttpWebRequest, which mishandles TLS 1.3 post-handshake frames
# (NewSessionTicket) and HTTP/2 ALPN against self-signed Caddy certs and bails
# with "underlying connection was closed". curl.exe uses Schannel for TLS but
# its own HTTP stack, which handles both cleanly.
& curl.exe -ksfL -o $AgentExe $DownloadUrl
if ($LASTEXITCODE -ne 0) {
    Write-Error "Agent binary download failed (curl exit code $LASTEXITCODE)"
    exit 1
}

# Persist HUB + TOKEN for the service to pick up at next start.
[Environment]::SetEnvironmentVariable("BLOXOS_HUB", $Hub, "Machine")
[Environment]::SetEnvironmentVariable("BLOXOS_TOKEN", $Token, "Machine")

# Register the service via the agent's own SCM installer.
Write-Host "Installing BloxOSAgent service..."
& $AgentExe -install-service
if ($LASTEXITCODE -ne 0) {
    Write-Error "Service installation failed (exit code $LASTEXITCODE)"
    exit 1
}

Write-Host "Starting BloxOSAgent ..."
Start-Service -Name BloxOSAgent

Start-Sleep -Seconds 3
$svc = Get-Service -Name BloxOSAgent -ErrorAction SilentlyContinue
if ($svc -and $svc.Status -eq 'Running') {
    Write-Host "=== BloxOS agent installed and running ==="
} else {
    Write-Warning "BloxOSAgent service is not running. Check Event Viewer or services.msc."
}
`
	return c.String(http.StatusOK, script)
}

// validateAgentToken checks a token against the DB (Finding #1).
// Returns the token hash so the caller can mark it as used after enrollment.
func validateAgentToken(token string) (string, error) {
	h := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(h[:])

	var expiresAt string
	var used bool
	err := db.QueryRow(`SELECT expires_at, used FROM tokens WHERE token_hash = ?`, tokenHash).Scan(&expiresAt, &used)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("invalid token")
	}
	if err != nil {
		return "", fmt.Errorf("database error: %w", err)
	}
	if used {
		return "", fmt.Errorf("token already used")
	}
	expTime, err := time.Parse("2006-01-02 15:04:05", expiresAt)
	if err != nil {
		expTime, err = time.Parse(time.RFC3339, expiresAt)
		if err != nil {
			return "", fmt.Errorf("invalid expiry format")
		}
	}
	if time.Now().After(expTime) {
		return "", fmt.Errorf("token expired")
	}

	log.Printf("agent token validated successfully")
	return tokenHash, nil
}

// consumeToken marks a token as used after successful enrollment.
func consumeToken(tokenHash string) {
	_, err := db.Exec(`UPDATE tokens SET used = TRUE WHERE token_hash = ?`, tokenHash)
	if err != nil {
		log.Printf("failed to mark token as used: %v", err)
	} else {
		log.Printf("install token consumed")
	}
}

func consumeTokenAndStoreCredential(tokenHash, machineID, secretHash string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

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

	if _, err := tx.Exec(`INSERT OR REPLACE INTO agent_credentials (machine_id, secret_hash, created_at, last_used_at)
		VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, machineID, secretHash); err != nil {
		return err
	}
	return tx.Commit()
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
func storeAgentCredential(machineID, secretHash string) error {
	_, err := db.Exec(`INSERT OR REPLACE INTO agent_credentials (machine_id, secret_hash, created_at, last_used_at)
		VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, machineID, secretHash)
	return err
}

func machineHasCredential(machineID string) bool {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_credentials WHERE machine_id = ?`, machineID).Scan(&count); err != nil {
		log.Printf("failed checking agent credential for %s: %v", machineID, err)
		return false
	}
	return count > 0
}

// validateAgentSecret looks up a credential by the SHA-256 hash of the provided secret.
// Returns the associated machine_id if found.
func validateAgentSecret(secret string) (string, error) {
	h := sha256.Sum256([]byte(secret))
	secretHash := hex.EncodeToString(h[:])

	var machineID string
	err := db.QueryRow(`SELECT machine_id FROM agent_credentials WHERE secret_hash = ?`, secretHash).Scan(&machineID)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("invalid agent secret")
	}
	if err != nil {
		return "", fmt.Errorf("database error: %w", err)
	}

	// Update last_used_at.
	_, _ = db.Exec(`UPDATE agent_credentials SET last_used_at = CURRENT_TIMESTAMP WHERE secret_hash = ?`, secretHash)

	return machineID, nil
}

// revokeAgentCredential removes the stored credential for a machine.
func revokeAgentCredential(machineID string) error {
	_, err := db.Exec(`DELETE FROM agent_credentials WHERE machine_id = ?`, machineID)
	return err
}

// generateFirstRunToken creates a one-time token on first startup when no tokens and no machines exist.
func generateFirstRunToken() {
	var tokenCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tokens`).Scan(&tokenCount); err != nil {
		log.Printf("first-run check: token count error: %v", err)
		return
	}
	if tokenCount > 0 {
		return
	}

	var machineCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM machines`).Scan(&machineCount); err != nil {
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

	_, err := db.Exec(`INSERT INTO tokens (token_hash, expires_at) VALUES (?, ?)`, tokenHash, expiresAt)
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

