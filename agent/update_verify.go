package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
 * updateSigContext and updateSigningMessage are duplicated in
 * hub/update_signing.go. Both sides assert the exact literal format in their
 * tests so the two cannot drift apart silently.
 * ============================================================================ */

// updateSigContext namespaces the signature so this key can never be
// repurposed to sign anything else meaningful.
const updateSigContext = "bloxos-agent-update:v1"

// defaultUpdatePublicKeyPathLinux is where the hub-generated install.sh pins
// the key. Windows pins it next to the agent executable instead.
const defaultUpdatePublicKeyPathLinux = "/etc/bloxos/agent-update.pub"

// updateSigningMessage builds the exact byte string that gets signed.
func updateSigningMessage(osName, sha256hex string) []byte {
	return []byte(updateSigContext + ":" +
		strings.ToLower(strings.TrimSpace(osName)) + ":" +
		strings.ToLower(strings.TrimSpace(sha256hex)))
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
func updatePublicKeyPath(exePath string) string {
	if p := strings.TrimSpace(os.Getenv("BLOXOS_UPDATE_PUBKEY")); p != "" {
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
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(line)
		if err != nil {
			return nil, fmt.Errorf("key is not valid base64: %w", err)
		}
		if len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("key is %d bytes, want %d", len(raw), ed25519.PublicKeySize)
		}
		return ed25519.PublicKey(raw), nil
	}
	return nil, fmt.Errorf("no key found in file")
}

// verifyUpdateSignature checks sigB64 over (osName, sha256hex) against pub.
func verifyUpdateSignature(pub ed25519.PublicKey, osName, sha256hex, sigB64 string) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("pinned key is malformed")
	}
	sigB64 = strings.TrimSpace(sigB64)
	if sigB64 == "" {
		return fmt.Errorf("hub announced no signature for the %s binary", osName)
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("signature is not valid base64: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("signature is %d bytes, want %d", len(sig), ed25519.SignatureSize)
	}
	// Reject a non-hex "SHA" before it reaches the signed message — it would
	// otherwise be possible to sign an arbitrary string in the SHA slot.
	if _, err := hex.DecodeString(strings.TrimSpace(sha256hex)); err != nil {
		return fmt.Errorf("announced SHA is not hex: %w", err)
	}
	if !ed25519.Verify(pub, updateSigningMessage(osName, sha256hex), sig) {
		return fmt.Errorf("signature does not verify against the pinned update key")
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
// tamper with, and the announced SHA must be signed by the pinned key.
func authorizeUpdate(rawHubURL, exePath, osName, sha256hex, sigB64 string) error {
	if _, err := agentDownloadURL(rawHubURL, ""); err != nil {
		return err
	}
	return verifyAnnouncedRelease(exePath, osName, sha256hex, sigB64)
}
