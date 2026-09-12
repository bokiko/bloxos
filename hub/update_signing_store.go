package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/bokiko/bloxos/proto/updatesigning"
)

/* ----------------------------------------------------------------------------
 * Content-addressed detached signatures
 *
 * The problem this solves appears the moment agent binaries ship with the hub.
 * An offline install holds no private key, so it can only announce a signature
 * somebody produced beforehand. Until now that signature had to sit at
 * <binary>.sig — beside a binary at a fixed system path that the operator
 * placed and maintained. When the hub brings its own agents, those binaries
 * live in a release directory that changes every upgrade, and the operator has
 * nowhere stable to put a signature for bytes they have not received yet.
 *
 * Copying or regenerating the old signatures is NOT an option and never was.
 * A detached ed25519 signature covers a specific message containing a specific
 * SHA. New bytes mean a new SHA, so an old signature is not weak for them, it
 * is cryptographically meaningless — it cannot verify, and announcing it would
 * make every agent in the fleet reject the update.
 *
 * So the store is content-addressed: a signature is filed under the (os, arch,
 * sha256) it actually covers. An operator can authorise bytes IN ADVANCE, and
 * a signature for a release the hub is not yet serving simply sits unused until
 * that release arrives. Nothing has to be re-staged at upgrade time.
 *
 * Lookup order is unchanged in spirit and strictly additive:
 *
 *   1. <binary>.sig adjacent to the served binary — existing behaviour, still
 *      first, so an operator who manages signatures that way is unaffected.
 *   2. this store, by content address.
 *   3. the hub-held signing key — existing behaviour, untouched.
 *
 * A hub-held-key install therefore needs no new key, no new directory and no
 * manual step: it never reaches step 2 with anything to do.
 *
 * OWNERSHIP. This is state, not a served executable, and the two have
 * different rules. Agent binaries are root-owned because the hub hands them to
 * machines that will run them. A signature is data the hub verifies
 * cryptographically before use, and it lives in the hub's own state
 * directory — which under Docker is /data owned by uid 65532, not root.
 * Demanding root here would break every containerised install for no security
 * gain, because a forged signature cannot verify against the pinned public key
 * and is discarded. The rule is therefore INSTALLATION-owned: owned by the
 * user the hub runs as (or root), and not writable by group or others.
 * ---------------------------------------------------------------------------- */

// agentSignatureDirName is the store, inside the hub's existing state
// directory. Nothing is created here by the hub: its absence simply means no
// signatures were staged, which is the normal case for a hub-held-key install.
const agentSignatureDirName = "agent-signatures"

// agentSignatureEnv overrides the store location for operators whose state
// directory is not where signatures are managed.
const agentSignatureEnv = "BLOXOS_AGENT_SIGNATURE_DIR"

// agentSignatureMaxBytes bounds a read. A base64 ed25519 signature is 88
// bytes; the allowance covers whitespace and a trailing newline with room to
// spare, and stops a mis-staged multi-gigabyte file from being read into
// memory on an announcement path.
const agentSignatureMaxBytes = 4 << 10

// signatureNotFound is logged once per (os, arch, sha) rather than on every
// announcement, so a hub with no authorisation for a release says so clearly
// instead of flooding the log.
var signatureNotFound sync.Map

// agentSignatureDir returns the configured store, or "" when none applies.
func agentSignatureDir() string {
	if custom := strings.TrimSpace(os.Getenv(agentSignatureEnv)); custom != "" {
		return custom
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".bloxos", agentSignatureDirName)
}

// agentSignatureName is the content address: platform plus the exact bytes.
//
// Callers must go through agentSignatureAddress, which is what establishes
// that the components are safe. This function only formats them.
func agentSignatureName(osName, arch, sha string) string {
	return fmt.Sprintf("%s-%s-%s.sig", osName, arch, strings.ToLower(sha))
}

// agentSignatureAddress validates an identity and returns its file name.
//
// Nothing reaches a filesystem call until this passes. The OS and architecture
// are resolved through the same helper the rest of the hub uses, so they come
// back as one of a fixed set of known values rather than as whatever the
// caller supplied, and the SHA must be exactly 64 hex characters. A separator
// or a parent reference in any component therefore cannot survive to become
// part of a path — the earlier version only checked that the strings were
// non-empty, which is not the same thing at all.
func agentSignatureAddress(osName, arch, sha string) (string, agentPlatform, bool) {
	platform, err := agentPlatformFor(osName, arch)
	if err != nil {
		return "", agentPlatform{}, false
	}
	sha = strings.ToLower(strings.TrimSpace(sha))
	if len(sha) != 64 {
		return "", agentPlatform{}, false
	}
	for _, character := range sha {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return "", agentPlatform{}, false
		}
	}
	return agentSignatureName(platform.OS, platform.Arch, sha), platform, true
}

// readInstallationOwnedFile reads a state file the hub owns, with a hard bound.
//
// The bound is enforced by the READ, not by a prior Stat: a size observed
// beforehand is a claim about a different moment, and a FIFO reports zero and
// then blocks forever.
//
// O_NONBLOCK is what actually makes that FIFO case safe, and its absence was a
// comment describing protection the flags did not provide: opening a FIFO with
// no writer BLOCKS IN open(), so the fstat that would have rejected it is
// never reached. O_NOFOLLOW refuses a symlinked final path, and the mode is
// checked on the descriptor actually opened rather than on a path that could
// have been swapped in between.
func readInstallationOwnedFile(path string, max int64) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if err := requireInstallationOwned(info, path); err != nil {
		return nil, err
	}

	raw, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > max {
		return nil, fmt.Errorf("%s is larger than the %d byte limit", path, max)
	}
	return raw, nil
}

// requireInstallationOwned enforces the state-file rule: owned by the user the
// hub runs as (or root), and not writable by group or others.
func requireInstallationOwned(info os.FileInfo, path string) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot inspect ownership of %s", path)
	}
	self := uint32(os.Geteuid())
	if stat.Uid != self && stat.Uid != 0 {
		return fmt.Errorf("%s is owned by uid %d, want the hub's own uid %d or root",
			path, stat.Uid, self)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s mode %04o is group- or other-writable", path, info.Mode().Perm())
	}
	return nil
}

// contentAddressedSignatureFor returns a staged signature for exactly these
// bytes, or "" when none is both present and valid.
//
// It NEVER returns a signature it has not verified against the pinned public
// key. A signature that does not verify is worse than none: announcing it
// makes every agent reject the update, so a bad file must not be passed along
// just because it was present.
func contentAddressedSignatureFor(osName, arch, sha string) string {
	name, platform, ok := agentSignatureAddress(osName, arch, sha)
	if !ok {
		return ""
	}
	dir := agentSignatureDir()
	if dir == "" {
		return ""
	}

	updateSigningMu.RLock()
	pub := updateSigningPub
	updateSigningMu.RUnlock()
	if pub == nil {
		// No pinned key means nothing here could be verified, and an
		// unverified signature must never be announced.
		return ""
	}

	path := filepath.Join(dir, name)
	raw, err := readInstallationOwnedFile(path, agentSignatureMaxBytes)
	if err != nil {
		if !os.IsNotExist(err) {
			// A file that exists but cannot be trusted is an operator problem
			// worth naming; a file that is simply absent is the normal case.
			log.Printf("update signing: staged signature %s is unusable: %v", path, err)
		}
		return ""
	}

	sigB64 := strings.TrimSpace(string(raw))
	if sigB64 == "" {
		return ""
	}
	if decoded, err := base64.StdEncoding.DecodeString(sigB64); err != nil || len(decoded) != 64 {
		log.Printf("update signing: staged signature %s is not a valid ed25519 signature", path)
		return ""
	}
	if err := updatesigning.Verify(pub, platform.OS, sha, sigB64); err != nil {
		log.Printf("update signing: staged signature %s does not verify for %s sha %s (%v); "+
			"it was produced for different bytes and agents would reject it",
			path, platform.OS, versionShortSHA(sha), err)
		return ""
	}
	return sigB64
}

// reportMissingAgentAuthorization states, once per set of bytes, that an
// offline install has nothing to announce and exactly what would fix it.
//
// The Versions view already reports the blocked state; what was missing was
// the remedy. This names the exact path to stage, so the operator does not
// have to work out the content address themselves.
func reportMissingAgentAuthorization(osName, arch, sha string) {
	if sha == "" {
		return
	}
	key := osName + "/" + arch + "/" + sha
	if _, seen := signatureNotFound.LoadOrStore(key, true); seen {
		return
	}
	dir := agentSignatureDir()
	log.Printf("update signing: no authorization for the %s/%s agent (sha %s) — this hub holds no "+
		"signing key, so it can only announce a signature staged in advance. Sign these exact "+
		"bytes offline and place the base64 signature at %s. Agents keep running; they will not "+
		"be offered this build until then.",
		osName, arch, versionShortSHA(sha),
		filepath.Join(dir, agentSignatureName(osName, arch, sha)))
}
