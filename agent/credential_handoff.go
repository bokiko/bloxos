package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// pendingCredentialSuffix names the on-disk staging file for the two-phase
// credential handoff (see handleEnrolledFrame / handleEnrollmentConfirmed).
// The active credential file is NEVER overwritten just because an "enrolled"
// frame arrived — only a hash-bound "enrollment_confirmed" from the hub may
// promote a staged secret into the active file. Without this split, a lost
// "enrollment_confirmed" (ack never arrives, hub never promotes) would still
// have already destroyed the old, still-hub-valid active secret locally —
// stranding the machine with a credential the hub doesn't recognize and no
// way back to the one it does.
const pendingCredentialSuffix = ".pending"

// pendingCredentialPath returns the staging path for activePath.
func pendingCredentialPath(activePath string) string {
	return activePath + pendingCredentialSuffix
}

// hashCredentialFileWith reads path via readFile and returns the hex SHA-256
// of its trimmed contents, or "" if the file does not exist. A read error on
// a file that DOES exist propagates rather than being folded into "absent" —
// a transient read failure must never be silently mistaken for "no
// credential here", which would make handleEnrollmentConfirmed's matching
// logic wrongly treat a real, unreadable secret as a non-match.
func hashCredentialFileWith(readFile func(string) ([]byte, error), path string) (string, error) {
	data, err := readFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" {
		return "", nil
	}
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:]), nil
}

// hashCredentialFile is hashCredentialFileWith against the real filesystem.
func hashCredentialFile(path string) (string, error) {
	return hashCredentialFileWith(os.ReadFile, path)
}

// credentialFileWriter groups the three durability-relevant filesystem calls
// writeCredentialFileAtomic makes, so tests can inject a failure at any one
// of them (in particular between Sync and Close, or between Close and
// Rename) without needing real disk-failure conditions.
type credentialFileWriter struct {
	sync   func(*os.File) error
	close  func(*os.File) error
	rename func(oldpath, newpath string) error
}

func defaultCredentialFileWriter() credentialFileWriter {
	return credentialFileWriter{
		sync:  func(f *os.File) error { return f.Sync() },
		close: func(f *os.File) error { return f.Close() },
		// durableRename (not a plain os.Rename) so the temp -> destination
		// commit — including for agent-secret.pending, the file
		// enrollment_committed is gated on — gets the same durability
		// guarantee as the pending -> active promotion in
		// defaultCredentialConfirmDeps: both go through the identical
		// platform-specific primitive in durable_rename_*.go.
		rename: durableRename,
	}
}

// writeCredentialFileAtomic durably persists secret to path: a temporary
// file is created in the same directory, chmod'd, written, fsync'd, and
// closed successfully — in that order — before being renamed into place.
// The chmod request is 0600 on every platform, but what that actually buys
// differs: on POSIX it sets real owner-only read/write permission bits; on
// Windows, os.Chmod only toggles the read-only attribute (there is no POSIX
// permission bit concept to set), so access control there comes from the
// protected systemprofile credential directory's inherited ACL instead —
// see TestWriteCredentialFileAtomicWritesModeAndContent's platform-specific
// mode assertion. Sync is what actually makes this durable: Close alone
// only guarantees the data left the process, not that it survived a crash
// before the OS flushed its own buffers. The temp file is removed if
// anything fails before the rename commits, so a crash or error never
// leaves a partially-written credential file behind. Secret contents are
// never logged — callers only log the destination path.
func writeCredentialFileAtomic(path, secret string) error {
	return writeCredentialFileAtomicWith(defaultCredentialFileWriter(), path, secret)
}

func writeCredentialFileAtomicWith(w credentialFileWriter, path, secret string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create credential dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".agent-secret-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp credential file: %w", err)
	}
	tmpPath := tmp.Name()
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp credential file: %w", err)
	}
	if _, err := tmp.WriteString(secret + "\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp credential file: %w", err)
	}
	if err := w.sync(tmp); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp credential file: %w", err)
	}
	if err := w.close(tmp); err != nil {
		return fmt.Errorf("close temp credential file: %w", err)
	}
	if err := w.rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp credential file into place: %w", err)
	}
	success = true
	return nil
}

// handleEnrolledFrame implements the ordering contract for a freshly issued
// or staged secret: it must be durably saved to the PENDING file BEFORE
// anything treats it as trustworthy — savePending must never write the
// active credential file. BLOXOS_TOKEN cleanup, and the active file itself,
// are only ever touched later by handleEnrollmentConfirmed, on receipt of
// the hub's hash-bound "enrollment_confirmed" — never here. Sending
// "enrollment_committed" merely asks the hub to promote/confirm; it proves
// nothing on its own. If that send fails, the error propagates so the
// caller tears the connection down and reconnects rather than silently
// continuing in an unconfirmed state — a failed save sends no commit frame
// at all, so the hub never promotes a pending credential the agent doesn't
// actually have on disk. savePending and sendCommitted are injected so this
// ordering is unit-testable without touching the real filesystem or network
// socket.
func handleEnrolledFrame(secret string, savePending func(string) error, sendCommitted func() error) (accepted bool, err error) {
	if secret == "" {
		return false, nil
	}
	if err := savePending(secret); err != nil {
		return false, err
	}
	if err := sendCommitted(); err != nil {
		return false, err
	}
	return true, nil
}

// maybeCleanupTokenOnReconnect fires the once-guarded BLOXOS_TOKEN cleanup
// for an ordinary reconnect: one that authenticated with an already-existing
// durable secret and has no token in flight. When a token IS present
// alongside a durable secret, a fresh or targeted re-enrollment attempt may
// still be pending — cleanup is deferred to handleEnrollmentConfirmed
// instead, so a legitimate retry (including across a process restart) never
// loses BLOXOS_TOKEN to a premature wipe before enrollment actually
// completes.
func maybeCleanupTokenOnReconnect(haveDurableSecret, haveToken bool, cleanup func()) {
	if haveDurableSecret && !haveToken {
		cleanup()
	}
}

// credentialConfirmDeps groups the filesystem operations
// handleEnrollmentConfirmed needs, injected so the full hash-bound
// confirmation state machine is unit-testable without touching real disk.
type credentialConfirmDeps struct {
	hashActive    func() (string, error)
	hashPending   func() (string, error)
	promote       func() error // durably replace active with pending; the commit point
	removePending func() error
}

// enrollmentConfirmOutcome distinguishes WHY handleEnrollmentConfirmed
// succeeded. Both non-rejected outcomes are equally safe to clear
// BLOXOS_TOKEN on; the distinction exists so the caller can log accurately
// and knows whether a rename actually happened.
type enrollmentConfirmOutcome int

const (
	enrollmentConfirmRejected enrollmentConfirmOutcome = iota
	enrollmentConfirmPromoted
	enrollmentConfirmAlreadyActive
)

// handleEnrollmentConfirmed implements the hash-bound confirmation contract:
// the hub's "enrollment_confirmed" always carries secret_sha256 identifying
// EXACTLY the credential it is confirming (fresh enrollment = the active
// hash; staged promotion = the promoted hash; pending-secret reconnect =
// the hash that was promoted/validated; active-secret reconnect with a
// lingering token = the active hash it actually authenticated). Local state
// may change ONLY when that hash matches something this agent actually has
// on disk right now:
//
//   - confirmedHash matches the pending file: pending is durably promoted to
//     active (deps.promote — the one and only commit point) and then
//     removed. This is the ONLY path that ever mutates the active
//     credential file.
//   - confirmedHash matches the file already active: nothing to promote —
//     the "lingering token on an already-active reconnect" case. Any
//     pending file left over is now provably stale (this agent process
//     drives one connection/enrollment attempt at a time, so it cannot
//     belong to some other still-in-flight attempt) and is removed.
//   - Anything else — empty, or matching neither file — changes NOTHING and
//     returns an error. The caller must close and let a fresh reconnect
//     retry (pending first, then active) rather than guess.
func handleEnrollmentConfirmed(confirmedHash string, deps credentialConfirmDeps) (enrollmentConfirmOutcome, error) {
	if confirmedHash == "" {
		return enrollmentConfirmRejected, fmt.Errorf("enrollment_confirmed missing secret_sha256")
	}

	pendingHash, err := deps.hashPending()
	if err != nil {
		return enrollmentConfirmRejected, fmt.Errorf("hash pending credential: %w", err)
	}
	if pendingHash != "" && pendingHash == confirmedHash {
		if err := deps.promote(); err != nil {
			return enrollmentConfirmRejected, fmt.Errorf("promote pending credential: %w", err)
		}
		return enrollmentConfirmPromoted, nil
	}

	activeHash, err := deps.hashActive()
	if err != nil {
		return enrollmentConfirmRejected, fmt.Errorf("hash active credential: %w", err)
	}
	if activeHash != "" && activeHash == confirmedHash {
		if pendingHash != "" {
			// Best-effort: a leftover stale pending file is cosmetic once
			// the active credential is already confirmed authoritative —
			// failing to remove it now does not affect correctness, only
			// housekeeping, so it is not treated as a confirmation failure.
			_ = deps.removePending()
		}
		return enrollmentConfirmAlreadyActive, nil
	}

	return enrollmentConfirmRejected, fmt.Errorf("enrollment_confirmed hash matches neither the pending nor the active local credential")
}
