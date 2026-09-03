//go:build windows

package main

import (
	"os"
	"testing"
)

// assertCredentialFileMode intentionally does NOT assert POSIX permission
// bits on Windows: os.Chmod(0o600) there only toggles the read-only
// attribute, so os.Stat().Mode().Perm() reports something like 0666
// regardless — that is not NTFS ACL inspection and asserting a specific
// value would test nothing meaningful. Access control for this file comes
// from where it lives: the protected systemprofile credential directory
// (C:\Windows\System32\config\systemprofile\.bloxos, per install.ps1's
// $CredDir), whose ACL is inherited by files created inside it. All this
// test can and should verify here is that a real, regular file exists.
func assertCredentialFileMode(t *testing.T, info os.FileInfo) {
	t.Helper()
	if !info.Mode().IsRegular() {
		t.Fatalf("mode=%v, want a regular file", info.Mode())
	}
}
