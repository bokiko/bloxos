package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

/* ============================================================================
 * Agent update signing
 *
 * The agent refuses to self-update unless the announced SHA carries an
 * ed25519 signature that verifies against the public key its installer
 * pinned on disk. This file produces that signature and that public key.
 *
 * Two modes, in priority order per announcement:
 *
 *   1. Detached signature — <agent binary>.sig next to the binary, base64
 *      ed25519 over the same message. Produced offline at build time, so the
 *      private key never has to live on the hub. Set BLOXOS_UPDATE_PUBKEY to
 *      the matching public key (base64) and the hub will pin that in
 *      installers and reject a stale .sig before announcing it.
 *
 *   2. Hub-held key — BLOXOS_UPDATE_SIGNING_KEY (path to a file holding the
 *      base64 private key), else auto-generated and persisted at
 *      ~/.bloxos/update-signing.key on first boot. Convenient; the key is
 *      only as safe as the hub.
 *
 * Mode 2 does not defend against a compromised hub — nothing signed on the
 * hub can. What both modes buy is that /download/agent (a public,
 * unauthenticated route) can no longer be substituted by anything that is
 * not the hub, which together with the agent's transport gate closes the
 * network-path attack. Mode 1 additionally survives hub compromise.
 *
 * updateSigContext and updateSigningMessage are duplicated in
 * agent/update_verify.go. Both sides assert the exact literal format in
 * their tests so the two cannot drift apart silently.
 * ============================================================================ */

// updateSigContext namespaces the signature. Must match the agent's constant.
const updateSigContext = "bloxos-agent-update:v1"

var (
	updateSigningKey    ed25519.PrivateKey
	updateSigningPub    ed25519.PublicKey
	updateSigningPubB64 string
	updateSigningMu     sync.RWMutex
)

// updateSigningMessage builds the exact byte string that gets signed.
// Must match agent/update_verify.go byte for byte.
func updateSigningMessage(osName, sha256hex string) []byte {
	return []byte(updateSigContext + ":" +
		strings.ToLower(strings.TrimSpace(osName)) + ":" +
		strings.ToLower(strings.TrimSpace(sha256hex)))
}

// initUpdateSigning resolves the hub's update signing material at startup.
func initUpdateSigning() {
	// Offline mode: operator declares the public key, hub holds no private key.
	if pubB64 := strings.TrimSpace(os.Getenv("BLOXOS_UPDATE_PUBKEY")); pubB64 != "" {
		pub, err := decodeUpdatePublicKey(pubB64)
		if err != nil {
			log.Fatalf("invalid BLOXOS_UPDATE_PUBKEY: %v", err)
		}
		updateSigningMu.Lock()
		updateSigningPub = pub
		updateSigningPubB64 = base64.StdEncoding.EncodeToString(pub)
		updateSigningMu.Unlock()
		log.Println("update signing: offline mode — announcing detached <binary>.sig signatures only")
		return
	}

	priv, source := loadOrGenerateUpdateSigningKey()
	if priv == nil {
		log.Println("WARNING: update signing unavailable — agents will refuse to self-update")
		return
	}
	pub := priv.Public().(ed25519.PublicKey)

	updateSigningMu.Lock()
	updateSigningKey = priv
	updateSigningPub = pub
	updateSigningPubB64 = base64.StdEncoding.EncodeToString(pub)
	updateSigningMu.Unlock()

	log.Printf("update signing: key loaded from %s (public %s)",
		source, base64.StdEncoding.EncodeToString(pub))
}

// loadOrGenerateUpdateSigningKey mirrors loadOrGenerateJWTSecret: explicit
// env path wins, otherwise persist under ~/.bloxos.
func loadOrGenerateUpdateSigningKey() (ed25519.PrivateKey, string) {
	keyPath := strings.TrimSpace(os.Getenv("BLOXOS_UPDATE_SIGNING_KEY"))
	explicit := keyPath != ""
	if !explicit {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			log.Printf("WARNING: cannot determine home dir for update signing key: %v", err)
			return nil, ""
		}
		keyPath = filepath.Join(homeDir, ".bloxos", "update-signing.key")
	}

	if data, err := os.ReadFile(keyPath); err == nil {
		priv, err := decodeUpdatePrivateKey(data)
		if err != nil {
			log.Fatalf("update signing key at %s is unusable: %v", keyPath, err)
		}
		return priv, keyPath
	} else if explicit {
		// An operator who named a key file did not mean "generate one for me".
		log.Fatalf("cannot read BLOXOS_UPDATE_SIGNING_KEY at %s: %v", keyPath, err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.Printf("WARNING: cannot generate update signing key: %v", err)
		return nil, ""
	}
	encoded := []byte(base64.StdEncoding.EncodeToString(priv) + "\n")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		log.Printf("WARNING: cannot create %s: %v", filepath.Dir(keyPath), err)
		return priv, "(in-memory, not persisted)"
	}
	if err := os.WriteFile(keyPath, encoded, 0600); err != nil {
		log.Printf("WARNING: cannot write %s: %v", keyPath, err)
		return priv, "(in-memory, not persisted)"
	}
	log.Printf("update signing: generated new key at %s", keyPath)
	return priv, keyPath
}

func decodeUpdatePublicKey(b64 string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("not valid base64: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("key is %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

func decodeUpdatePrivateKey(data []byte) (ed25519.PrivateKey, error) {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(line)
		if err != nil {
			return nil, fmt.Errorf("not valid base64: %w", err)
		}
		switch len(raw) {
		case ed25519.PrivateKeySize:
			return ed25519.PrivateKey(raw), nil
		case ed25519.SeedSize:
			return ed25519.NewKeyFromSeed(raw), nil
		default:
			return nil, fmt.Errorf("key is %d bytes, want %d (private) or %d (seed)",
				len(raw), ed25519.PrivateKeySize, ed25519.SeedSize)
		}
	}
	return nil, fmt.Errorf("no key found in file")
}

// updateSigningPublicKeyBase64 returns the key that installers pin on the
// agent. Empty means self-update cannot be authorised at all.
func updateSigningPublicKeyBase64() string {
	updateSigningMu.RLock()
	defer updateSigningMu.RUnlock()
	return updateSigningPubB64
}

// signAgentRelease returns a base64 ed25519 signature over (osName, sha),
// or "" if the hub holds no private key.
func signAgentRelease(osName, sha string) string {
	updateSigningMu.RLock()
	priv := updateSigningKey
	updateSigningMu.RUnlock()
	if priv == nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(
		ed25519.Sign(priv, updateSigningMessage(osName, sha)))
}

// detachedSignatureFor reads <binaryPath>.sig, an offline-produced base64
// ed25519 signature. Returns "" if absent, malformed, or — when a public key
// is known — if it does not actually verify for this (os, sha). Announcing a
// stale signature would only make every agent in the fleet reject the update
// with a confusing error, so it is better caught here.
func detachedSignatureFor(binaryPath, osName, sha string) string {
	if binaryPath == "" {
		return ""
	}
	data, err := os.ReadFile(binaryPath + ".sig")
	if err != nil {
		return ""
	}
	sigB64 := strings.TrimSpace(string(data))
	if sigB64 == "" {
		return ""
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil || len(sig) != ed25519.SignatureSize {
		log.Printf("update signing: %s.sig is not a valid ed25519 signature, ignoring", binaryPath)
		return ""
	}

	updateSigningMu.RLock()
	pub := updateSigningPub
	updateSigningMu.RUnlock()
	if pub != nil && !ed25519.Verify(pub, updateSigningMessage(osName, sha), sig) {
		log.Printf("update signing: %s.sig does not verify for the %s binary currently served "+
			"(sha %s) — re-sign it, agents will reject this announcement",
			binaryPath, osName, versionShortSHA(sha))
		return ""
	}
	return sigB64
}

// announcedSignatureFor returns the signature the hub will send alongside the
// announced SHA for the given OS. Empty means the hub cannot authorise an
// update for that platform and should not announce one.
func announcedSignatureFor(osName, sha string) string {
	if sha == "" {
		return ""
	}
	if sig := detachedSignatureFor(agentBinaryPathFor(osName), osName, sha); sig != "" {
		return sig
	}
	return signAgentRelease(osName, sha)
}
