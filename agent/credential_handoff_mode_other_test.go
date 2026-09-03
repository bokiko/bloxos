//go:build !windows

package main

import (
	"os"
	"testing"
)

// assertCredentialFileMode checks the real POSIX permission bits
// writeCredentialFileAtomic's Chmod(0o600) call establishes: owner
// read/write only, nothing for group or other.
func assertCredentialFileMode(t *testing.T, info os.FileInfo) {
	t.Helper()
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v, want 0600", info.Mode().Perm())
	}
}
