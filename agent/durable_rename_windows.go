//go:build windows

package main

import (
	"golang.org/x/sys/windows"
)

// durableRename is the ONE durable rename/replace primitive shared by both
// callers that need it: writeCredentialFileAtomic's temp -> pending/active
// commit (via defaultCredentialFileWriter's rename field), and the
// pending -> active promotion in defaultCredentialConfirmDeps's promote
// closure. Go's os.Rename already uses MoveFileEx with
// MOVEFILE_REPLACE_EXISTING internally, so replacing an existing
// destination is not the gap; what it does NOT set is
// MOVEFILE_WRITE_THROUGH, which tells the API not to return until the
// rename is flushed through to the target volume. This call adds that flag
// so both callers get the strongest durability guarantee the platform
// offers for an ordinary data file, and — unlike the !windows
// implementation, which has a separate rename step and directory-fsync step
// that can fail independently — MoveFileEx performs both as one operation,
// so any failure here means neither happened. (Unrelated to the separate
// self-update .exe swap in updater_windows.go, which needs a detached
// helper only because a RUNNING executable image holds an OS-level lock an
// ordinary credential file never does.)
func durableRename(oldpath, newpath string) error {
	from, err := windows.UTF16PtrFromString(oldpath)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(newpath)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
