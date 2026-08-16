//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// syncCloser is satisfied by *os.File — abstracted so durableRenameWith can
// be tested deterministically against a fake, instead of depending on real
// filesystem permission-bit behavior (which is not a reliable way to force
// an open/sync failure: it varies by filesystem, and a process running as
// root bypasses permission checks entirely, so a chmod-based trick silently
// stops testing anything in that environment).
type syncCloser interface {
	Sync() error
	Close() error
}

// dirOpener opens path for the sole purpose of fsync'ing it.
type dirOpener func(path string) (syncCloser, error)

func openDirForSync(path string) (syncCloser, error) {
	return os.Open(path)
}

// durableRename is the ONE durable rename/replace primitive shared by both
// callers that need it: writeCredentialFileAtomic's temp -> pending/active
// commit (via defaultCredentialFileWriter's rename field), and the
// pending -> active promotion in defaultCredentialConfirmDeps's promote
// closure. POSIX rename(2) is atomic with respect to any other process
// observing newpath, but atomicity alone doesn't guarantee the rename
// itself survives a crash: an fsync of the containing directory after the
// rename is what forces the directory-entry update out of the page cache
// and onto disk.
//
// Unlike an earlier version of this function, a failure to open or fsync
// that directory is NOT swallowed. This function's whole contract is
// "durably replaces" — silently downgrading a failed durability proof to
// "renamed, but who knows" would let a caller (enrollment_committed, token
// cleanup) proceed exactly as if a crash-safe commit had happened when it
// hadn't. If the rename itself already succeeded before the fsync step
// fails, that rename is NOT undone here — the file genuinely exists at
// newpath now, just with its crash-durability unproven. Reporting an error
// anyway is still correct: it prevents this connection from treating the
// commit as final (no enrollment_committed sent, or no token cleanup), and
// the reconnect-recovery path (pending-first, hash-bound confirmation)
// safely reconciles whatever the on-disk result actually is on the next
// attempt — it does not need this function to have gotten a clean answer.
func durableRename(oldpath, newpath string) error {
	return durableRenameWith(os.Rename, openDirForSync, oldpath, newpath)
}

func durableRenameWith(rename func(oldpath, newpath string) error, openDir dirOpener, oldpath, newpath string) error {
	if err := rename(oldpath, newpath); err != nil {
		return fmt.Errorf("rename %s to %s: %w", oldpath, newpath, err)
	}
	dir, err := openDir(filepath.Dir(newpath))
	if err != nil {
		return fmt.Errorf("open parent directory for durability fsync: %w", err)
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("fsync parent directory: %w", err)
	}
	return nil
}
