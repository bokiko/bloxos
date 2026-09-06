package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bokiko/bloxos/proto/updatesigning"
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
 * network-path attack. Mode 1 additionally means a compromised hub cannot
 * forge a signature for a new, malicious binary — but it can still replay a
 * previously-signed release, vulnerable ones included, since neither mode
 * has downgrade/monotonicity protection yet. That is a separate gap, not
 * closed by either signing mode.
 *
 * The canonical message and key codecs live in the updatesigning package,
 * shared by the hub, agent, and offline signing tool.
 * ============================================================================ */

var (
	updateSigningKey    ed25519.PrivateKey
	updateSigningPub    ed25519.PublicKey
	updateSigningPubB64 string
	// updateSigningDisabledReason explains, in operator-facing terms, why the
	// hub cannot authorise updates. Every failure path here is non-fatal and
	// silent apart from a log line, and a hub that cannot sign looks exactly
	// like a rollout in progress on the dashboard — so the reason has to be
	// retrievable, not just printed to stdout once at boot.
	updateSigningDisabledReason string
	updateSigningMu             sync.RWMutex
)

// updateSigningMessage delegates to the canonical shared message format.
func updateSigningMessage(osName, sha256hex string) []byte {
	return updatesigning.Message(osName, sha256hex)
}

// initUpdateSigning resolves the hub's update signing material at startup.
//
// Nothing in here is fatal. The hub does far more than sign agent updates,
// and taking the whole fleet-management server offline because a key file is
// unreadable trades a self-update outage for a total one. Every failure path
// warns loudly and leaves signing disabled — which the operator then sees as
// "hub provided no update signing key" in the installer output and
// "no update signature available" in the rollout log.
func initUpdateSigning() {
	// Offline mode: operator declares the public key, hub holds no private key.
	if pubB64 := strings.TrimSpace(os.Getenv("BLOXOS_UPDATE_PUBKEY")); pubB64 != "" {
		pub, err := decodeUpdatePublicKey(pubB64)
		if err != nil {
			log.Printf("WARNING: invalid BLOXOS_UPDATE_PUBKEY (%v); update signing disabled", err)
			setUpdateSigningDisabled(fmt.Sprintf("BLOXOS_UPDATE_PUBKEY is not a usable ed25519 public key (%v)", err))
			return
		}
		updateSigningMu.Lock()
		updateSigningPub = pub
		updateSigningPubB64 = base64.StdEncoding.EncodeToString(pub)
		updateSigningMu.Unlock()
		log.Println("update signing: offline mode — announcing detached <binary>.sig signatures only")
		return
	}

	priv, source, disabledReason := loadOrGenerateUpdateSigningKey()
	if priv == nil {
		log.Println("WARNING: update signing unavailable — agents will refuse to self-update")
		setUpdateSigningDisabled(disabledReason)
		return
	}
	pub := priv.Public().(ed25519.PublicKey)

	updateSigningMu.Lock()
	updateSigningKey = priv
	updateSigningPub = pub
	updateSigningPubB64 = base64.StdEncoding.EncodeToString(pub)
	updateSigningDisabledReason = ""
	updateSigningMu.Unlock()

	log.Printf("update signing: key loaded from %s (public %s)",
		source, base64.StdEncoding.EncodeToString(pub))
}

// loadOrGenerateUpdateSigningKey resolves the hub's private key: explicit env
// path wins, otherwise persist under ~/.bloxos.
//
// Unlike loadOrGenerateJWTSecret, this must NOT fall back to minting a fresh
// key when the existing one merely cannot be read. A JWT secret is only
// shared with the hub's own tokens, so regenerating it logs everyone out and
// they log back in. This key is pinned on every agent in the fleet at install
// time: a new key means every agent rejects every future update, silently,
// with no recovery short of re-running the installer on each host. So a
// generate only happens on genuine first boot — the key file is absent — and
// only if it can actually be persisted. Any other outcome disables signing
// and says so.
func loadOrGenerateUpdateSigningKey() (key ed25519.PrivateKey, source string, disabledReason string) {
	keyPath := strings.TrimSpace(os.Getenv("BLOXOS_UPDATE_SIGNING_KEY"))
	explicit := keyPath != ""
	if !explicit {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			log.Printf("WARNING: cannot determine home dir for update signing key: %v", err)
			return nil, "", fmt.Sprintf("the hub cannot determine its home directory to locate the signing key (%v)", err)
		}
		keyPath = filepath.Join(homeDir, ".bloxos", "update-signing.key")
	}

	data, err := os.ReadFile(keyPath)
	switch {
	case err == nil:
		priv, decErr := decodeUpdatePrivateKey(data)
		if decErr != nil {
			log.Printf("WARNING: update signing key at %s is unusable (%v); update signing disabled. "+
				"Restore the key or remove the file to mint a new one — note that a new key "+
				"requires re-running the installer on every agent.", keyPath, decErr)
			return nil, "", fmt.Sprintf("the signing key at %s is corrupt (%v)", keyPath, decErr)
		}
		return priv, keyPath, ""

	case !os.IsNotExist(err):
		// EACCES, EISDIR, a remounted volume, a different HOME under systemd.
		// The key probably still exists and every agent is still pinned to it.
		// Minting a replacement here is how a fleet goes dark.
		log.Printf("WARNING: cannot read update signing key at %s (%v); update signing disabled. "+
			"Not generating a replacement — agents are pinned to the existing key.", keyPath, err)
		return nil, "", fmt.Sprintf("the signing key at %s cannot be read (%v); no replacement was generated because agents are pinned to the existing key", keyPath, err)

	case explicit:
		// An operator who named a key file did not mean "generate one for me".
		log.Printf("WARNING: BLOXOS_UPDATE_SIGNING_KEY points at %s, which does not exist; "+
			"update signing disabled.", keyPath)
		return nil, "", fmt.Sprintf("BLOXOS_UPDATE_SIGNING_KEY points at %s, which does not exist", keyPath)
	}

	// Genuine first boot on the default path: no key has ever existed here.
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.Printf("WARNING: cannot generate update signing key: %v", err)
		return nil, "", fmt.Sprintf("the hub could not generate a signing key (%v)", err)
	}
	encoded := []byte(base64.StdEncoding.EncodeToString(priv) + "\n")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		log.Printf("WARNING: cannot create %s (%v); update signing disabled — "+
			"an unpersisted key would change on every restart and orphan every agent "+
			"installed against it.", filepath.Dir(keyPath), err)
		return nil, "", fmt.Sprintf("the signing key directory %s cannot be created (%v), so a generated key could not be persisted", filepath.Dir(keyPath), err)
	}
	if err := os.WriteFile(keyPath, encoded, 0600); err != nil {
		log.Printf("WARNING: cannot write %s (%v); update signing disabled — "+
			"an unpersisted key would change on every restart and orphan every agent "+
			"installed against it.", keyPath, err)
		return nil, "", fmt.Sprintf("the signing key at %s could not be written (%v), so a generated key could not be persisted", keyPath, err)
	}
	log.Printf("update signing: generated new key at %s", keyPath)
	return priv, keyPath, ""
}

// setUpdateSigningDisabled records why the hub cannot authorise updates.
func setUpdateSigningDisabled(reason string) {
	if reason == "" {
		reason = "update signing is not configured"
	}
	updateSigningMu.Lock()
	updateSigningDisabledReason = reason
	updateSigningMu.Unlock()
}

// updateSigningStatus reports whether the hub has any means of authorising an
// update, and if not, why. Offline mode counts as enabled: the hub holds no
// private key but can still announce detached <binary>.sig signatures.
func updateSigningStatus() (enabled bool, reason string) {
	updateSigningMu.RLock()
	defer updateSigningMu.RUnlock()
	if updateSigningKey != nil || updateSigningPub != nil {
		return true, ""
	}
	if updateSigningDisabledReason != "" {
		return false, updateSigningDisabledReason
	}
	return false, "update signing is not configured"
}

func decodeUpdatePublicKey(b64 string) (ed25519.PublicKey, error) {
	return updatesigning.DecodePublicKey([]byte(b64))
}

func decodeUpdatePrivateKey(data []byte) (ed25519.PrivateKey, error) {
	return updatesigning.DecodePrivateKey(data)
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
// ed25519 signature. Returns "" if absent, malformed, no public key is
// configured, or it does not verify for this (os, sha). Announcing a
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
	if pub == nil {
		return ""
	}
	if err := updatesigning.Verify(pub, osName, sha, sigB64); err != nil {
		log.Printf("update signing: %s.sig does not verify for the %s binary currently served "+
			"(sha %s) — re-sign it, agents will reject this announcement",
			binaryPath, osName, versionShortSHA(sha))
		return ""
	}
	return sigB64
}

/* ----------------------------------------------------------------------------
 * Transport policy for announcements
 *
 * Signature verification lives in the agent binary, so it cannot be
 * retrofitted into one that is already deployed. An agent built before this
 * change has only type/sha256/version in its AgentVersionMessage —
 * encoding/json silently discards the signature we now send — and it goes
 * straight to performUpdate. That one migration hop is unverifiable by
 * construction, on both sides.
 *
 * What the hub can still control is whether that hop is allowed to happen
 * somewhere an attacker can reach it. So: never announce to an agent that has
 * not reported update_protocol >= 1 unless the fleet reaches this hub over
 * TLS, or the hub is on loopback. On a plaintext non-loopback deployment the
 * documented path becomes re-running the installer on the host, which pins a
 * key and enrolls the agent properly — instead of an unauthenticated
 * over-the-wire bootstrap nobody chose.
 * -------------------------------------------------------------------------- */

// minSignatureCapableProtocol is the update_protocol an agent must report
// before the hub will treat it as able to verify a signed announcement.
const minSignatureCapableProtocol = 1

// updateTransportUsable reports whether the transport the fleet uses to reach
// this hub can carry a self-update at all, and if not, why.
//
// It gates announcements to EVERY agent, not just pre-signature ones. For a
// legacy agent the reason is security: that binary cannot verify what it
// receives, so the unverifiable migration hop must not happen anywhere an
// off-host attacker can reach it. For a signature-capable agent the reason is
// that it will refuse the download itself (agent/update_transport.go), so
// announcing achieves nothing except arming a 90s reconnect-expectation timer
// for a reconnect that will never come — which expires into a rollout failure
// and, at two machines, trips the circuit breaker and pauses the whole fleet.
// Announcing an update the recipient is designed to decline is not a
// no-op; it is a self-inflicted outage. Credit to Codex for catching it.
//
// PUBLIC_URL is the signal because it is the hub operator's own declaration
// of how the fleet reaches this hub — it is what buildInstallCommand bakes
// into BLOXOS_HUB, so an http:// PUBLIC_URL is exactly what produces the
// ws:// agents this policy is protecting. The hub's own listener scheme would
// be the wrong signal: the standard deployment terminates TLS at Caddy and
// forwards plaintext to 127.0.0.1:4000.
func updateTransportUsable() (bool, string) {
	publicURL := strings.TrimSpace(os.Getenv("PUBLIC_URL"))
	if publicURL == "" {
		return false, "PUBLIC_URL is not set, so the hub cannot establish that agents reach it over a transport that permits self-update"
	}
	u, err := url.Parse(publicURL)
	if err != nil || u.Host == "" {
		return false, fmt.Sprintf("PUBLIC_URL %q is not a usable URL", publicURL)
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "wss":
		return true, ""
	case "http", "ws":
		if isLoopbackURLHost(u.Host) {
			return true, ""
		}
		return false, fmt.Sprintf("PUBLIC_URL %q is plaintext and not loopback", publicURL)
	default:
		return false, fmt.Sprintf("PUBLIC_URL %q has scheme %q, which is not a transport agents can use", publicURL, u.Scheme)
	}
}

// isLoopbackURLHost reports whether a URL host (with or without a port, IPv6
// literals included) refers to the local machine. Mirrors isLoopbackHost in
// agent/update_transport.go.
func isLoopbackURLHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// signatureCapable reports whether this agent told us it verifies signed
// announcements. Defined on the record rather than looked up by machineID so
// there is exactly one definition of the predicate — announceDecisionFor runs
// lock-free over a caller-held copy and must not re-read the map.
func (v agentVersionInfo) signatureCapable() bool {
	return v.UpdateProtocol >= minSignatureCapableProtocol
}

// transportPermitsUpdate reports whether the agent said its own hub URL
// permits self-update. Only meaningful alongside signatureCapable.
func (v agentVersionInfo) transportPermitsUpdate() bool {
	return v.UpdateTransportOK
}

// announcedSignatureFor returns the signature the hub will send alongside the
// announced SHA for the given OS. Empty means the hub cannot authorise an
// update for that platform and should not announce one.
func announcedSignatureFor(osName, sha string) string {
	return announcedSignatureForArch(osName, defaultAgentArch, sha)
}

// announcedSignatureForArch is announcedSignatureFor for one (os, arch). The
// detached .sig is looked up beside that architecture's binary; the signed
// message itself stays bloxos-agent-update:v1:<os>:<sha256> — the SHA is
// already per-architecture, and every deployed agent verifies exactly that
// message, so the format does not change.
func announcedSignatureForArch(osName, arch, sha string) string {
	if sha == "" {
		return ""
	}
	if sig := detachedSignatureFor(agentBinaryPathForArch(osName, arch), osName, sha); sig != "" {
		return sig
	}
	return signAgentRelease(osName, sha)
}
