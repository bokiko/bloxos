package main

import (
	"crypto/ed25519"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/bokiko/bloxos/proto/updatesigning"
)

/* ============================================================================
 * Self-update authenticity
 *
 * The SHA-256 the hub announces in an agent_version frame proves the download
 * matches what the announcer said. That is a corruption check, not an
 * authenticity check — it says nothing about who the announcer is.
 *
 * The hub now signs "<context>:<os>:<sha256>" with an ed25519 key, and the
 * installer writes that key's public half to disk at install time. The agent
 * refuses to self-update unless the announced SHA carries a signature that
 * verifies against the pinned key.
 *
 * Fail-closed: no pinned key means no self-update. Recovering is re-running
 * the hub installer, which pins the key.
 *
 * The signed message binds the OS so a signature issued for the Linux binary
 * cannot be replayed as the Windows announcement.
 *
 * The canonical message and key codecs live in the updatesigning package,
 * shared by the agent, hub, and offline signing tool.
 * ============================================================================ */

// agentUpdateProtocol is reported to the hub in every agent_running_version
// frame. Version 1 means "this binary verifies signed announcements."
//
// Agents built before signature verification existed send no such field, and
// their AgentVersionMessage struct has only type/sha256/version — encoding/json
// silently discards the signature the hub now sends, so they would take an
// update with no authenticity check at all. Verification cannot be retrofitted
// into a binary that is already deployed; the hub uses the absence of this
// field to decide whether announcing to that agent is safe at all. See
// legacyBootstrapAllowed in hub/update_signing.go.
//
// Version 2 additionally means "this binary enforces a persistent release
// floor" (release_floor.go): it reads the release sequence embedded in a
// downloaded binary and refuses anything older than, or a different build
// of, the highest release it has accepted. The signature format is unchanged
// — the sequence is inside the bytes the v1 signature already covers. A v2
// agent reports release, release_floor, release_floor_sha and
// release_floor_ok so the hub can withhold, with a reason, an announcement
// the agent is certain to refuse.
const agentUpdateProtocol = 2

// defaultUpdatePublicKeyPathLinux is where the hub-generated install.sh pins
// the key. Windows pins it next to the agent executable instead.
const defaultUpdatePublicKeyPathLinux = "/etc/bloxos/agent-update.pub"

// updateSigningMessage delegates to the canonical shared message format.
func updateSigningMessage(osName, sha256hex string) []byte {
	return updatesigning.Message(osName, sha256hex)
}

// selfUpdateTransportOK reports whether this agent's actual hub URL permits a
// self-update at all. Reported to the hub in every agent_running_version
// frame so it does not have to guess from PUBLIC_URL: in a mixed deployment
// the guess is wrong for exactly the machines that will refuse, and a hub
// that announces to a refusing agent arms a reconnect expectation that
// expires into a false rollout failure.
func selfUpdateTransportOK() bool {
	_, err := agentDownloadURL(hubURL, "")
	return err == nil
}

// updateKeyPinned reports whether this agent has a usable pinned update key
// on disk. Reported to the hub in every agent_running_version frame for the
// same reason as selfUpdateTransportOK: an agent can be signature-capable
// (agentUpdateProtocol >= 1) and report a usable transport yet still have no
// pinned key at all — it reached this binary over the one unverifiable
// migration hop and its installer has not been re-run yet. Without this
// signal the hub cannot tell that agent apart from one that is actually
// ready, announces anyway, arms a 90s reconnect expectation for a reconnect
// that will never come, and at two such machines trips the fleet-wide
// rollout circuit breaker — blaming agent health for a refusal the hub
// itself provoked.
func updateKeyPinned() bool {
	exe, err := selfExePath()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(updatePublicKeyPath(exe))
	if err != nil {
		return false
	}
	_, err = parseUpdatePublicKey(data)
	return err == nil
}

// selfExePath resolves this process's own binary, following symlinks.
func selfExePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// updatePublicKeyPath returns where the pinned update key lives. exePath is
// the agent's own resolved binary path; on Windows the key sits beside it in
// the install directory (admin-writable only), on Linux it lives in the
// agent's config directory.
//
// The override is BLOXOS_UPDATE_PUBKEY_PATH, not BLOXOS_UPDATE_PUBKEY: the
// hub uses the latter for the base64 key *value*, and a single-box
// deployment shares one environment between hub and agent. Naming them the
// same would mean setting it once silently disables the hub's signing and
// the agent's verification at the same time.
func updatePublicKeyPath(exePath string) string {
	if p := strings.TrimSpace(os.Getenv("BLOXOS_UPDATE_PUBKEY_PATH")); p != "" {
		return p
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(filepath.Dir(exePath), "agent-update.pub")
	}
	return defaultUpdatePublicKeyPathLinux
}

// parseUpdatePublicKey reads a pinned key file: the first non-empty,
// non-comment line, base64-standard-encoded.
func parseUpdatePublicKey(data []byte) (ed25519.PublicKey, error) {
	return updatesigning.DecodePublicKey(data)
}

// verifyUpdateSignature checks sigB64 over (osName, sha256hex) against pub.
func verifyUpdateSignature(pub ed25519.PublicKey, osName, sha256hex, sigB64 string) error {
	if strings.TrimSpace(sigB64) == "" {
		return fmt.Errorf("hub announced no signature for the %s binary", osName)
	}
	if err := updatesigning.Verify(pub, osName, sha256hex, sigB64); err != nil {
		return fmt.Errorf("signature does not verify against the pinned update key: %w", err)
	}
	return nil
}

// verifyAnnouncedRelease loads the install-time-pinned key and verifies the
// hub's announcement against it.
func verifyAnnouncedRelease(exePath, osName, sha256hex, sigB64 string) error {
	keyPath := updatePublicKeyPath(exePath)
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("no pinned update key at %s (%v); self-update is disabled — "+
			"re-run the hub installer to pin the hub's update signing key", keyPath, err)
	}
	pub, err := parseUpdatePublicKey(data)
	if err != nil {
		return fmt.Errorf("pinned update key at %s is unusable: %w", keyPath, err)
	}
	return verifyUpdateSignature(pub, osName, sha256hex, sigB64)
}

// authorizeUpdate is the single gate every self-update must clear before any
// bytes are fetched: the transport must be one an off-host attacker cannot
// tamper with, the announced SHA must be signed by the pinned key, and the
// release floor must be usable. advisoryRelease is the hub's unsigned hint
// of the served binary's release sequence; it can only refuse early (a
// download that would fail the floor anyway), never admit anything — the
// authoritative check reads the sequence out of the downloaded bytes in
// verifyCandidateRelease.
func authorizeUpdate(rawHubURL, exePath, osName, sha256hex, sigB64 string, advisoryRelease uint64) error {
	if _, err := agentDownloadURL(rawHubURL, ""); err != nil {
		return err
	}
	if err := verifyAnnouncedRelease(exePath, osName, sha256hex, sigB64); err != nil {
		return err
	}
	floor, err := loadReleaseFloorForUpdate()
	if err != nil {
		return err
	}
	return checkAdvisoryRelease(floor, advisoryRelease, sha256hex)
}
