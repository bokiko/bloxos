package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCredentialFileAtomicWritesModeAndContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-secret")

	if err := writeCredentialFileAtomic(path, "super-secret-value"); err != nil {
		t.Fatalf("write: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "super-secret-value\n" {
		t.Fatalf("content=%q, want secret+newline", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	assertCredentialFileMode(t, info)
}

func TestWriteCredentialFileAtomicReplacesExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-secret")
	if err := writeCredentialFileAtomic(path, "old-value"); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := writeCredentialFileAtomic(path, "new-value"); err != nil {
		t.Fatalf("write new: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(data) != "new-value\n" {
		t.Fatalf("content=%q, want new-value only (no old bytes lingering)", data)
	}
}

func TestWriteCredentialFileAtomicLeavesNoTempFileOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-secret")
	if err := writeCredentialFileAtomic(path, "value"); err != nil {
		t.Fatalf("write: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "agent-secret" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory contains unexpected entries after a successful write: %v", names)
	}
}

func TestWriteCredentialFileAtomicRemovesTempOnFailure(t *testing.T) {
	dir := t.TempDir()
	// A destination inside a nonexistent nested directory that we don't
	// pre-create makes MkdirAll succeed (it creates the dir) but we can
	// instead force a rename failure by pointing the destination itself AT
	// a directory, which os.Rename refuses to replace with a file.
	destDir := filepath.Join(dir, "agent-secret")
	if err := os.Mkdir(destDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	err := writeCredentialFileAtomic(destDir, "value")
	if err == nil {
		t.Fatal("expected an error writing over a directory")
	}

	entries, rderr := os.ReadDir(dir)
	if rderr != nil {
		t.Fatalf("readdir: %v", rderr)
	}
	for _, e := range entries {
		if e.Name() != "agent-secret" && filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("temp file %q was not cleaned up after a failed write", e.Name())
		}
	}
}

func TestWriteCredentialFileAtomicCallsSyncBeforeCloseAndRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-secret")

	var order []string
	w := defaultCredentialFileWriter()
	w.sync = func(f *os.File) error { order = append(order, "sync"); return f.Sync() }
	w.close = func(f *os.File) error { order = append(order, "close"); return f.Close() }
	w.rename = func(oldpath, newpath string) error {
		order = append(order, "rename")
		return os.Rename(oldpath, newpath)
	}

	if err := writeCredentialFileAtomicWith(w, path, "value"); err != nil {
		t.Fatalf("write: %v", err)
	}
	want := []string{"sync", "close", "rename"}
	if len(order) != len(want) || order[0] != want[0] || order[1] != want[1] || order[2] != want[2] {
		t.Fatalf("order=%v, want %v", order, want)
	}
}

func TestWriteCredentialFileAtomicFailsIfSyncFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-secret")

	renameCalled := false
	w := defaultCredentialFileWriter()
	w.sync = func(*os.File) error { return errors.New("sync failed") }
	w.rename = func(oldpath, newpath string) error { renameCalled = true; return os.Rename(oldpath, newpath) }

	if err := writeCredentialFileAtomicWith(w, path, "value"); err == nil {
		t.Fatal("expected sync failure to propagate")
	}
	if renameCalled {
		t.Fatal("rename must not run when sync failed — the file is not proven durable")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("destination must not exist when sync failed")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("temp file was not cleaned up after a sync failure: %v", entries)
	}
}

func TestWriteCredentialFileAtomicFailsIfRenameFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-secret")

	w := defaultCredentialFileWriter()
	w.rename = func(oldpath, newpath string) error { return errors.New("rename failed") }

	if err := writeCredentialFileAtomicWith(w, path, "value"); err == nil {
		t.Fatal("expected rename failure to propagate")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("destination must not exist when rename failed")
	}
}

func TestHandleEnrolledFrameSavesBeforeSendingCommit(t *testing.T) {
	var order []string
	save := func(secret string) error {
		if secret != "the-secret" {
			t.Fatalf("save got %q, want the-secret", secret)
		}
		order = append(order, "save")
		return nil
	}
	sendCommitted := func() error { order = append(order, "commit"); return nil }

	accepted, err := handleEnrolledFrame("the-secret", save, sendCommitted)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !accepted {
		t.Fatal("expected accepted=true")
	}
	want := []string{"save", "commit"}
	if len(order) != len(want) || order[0] != want[0] || order[1] != want[1] {
		t.Fatalf("order=%v, want %v", order, want)
	}
}

func TestHandleEnrolledFrameSendsNoCommitOnFailedSave(t *testing.T) {
	var commitAttempted bool
	save := func(string) error { return errors.New("disk full") }
	sendCommitted := func() error { commitAttempted = true; return nil }

	accepted, err := handleEnrolledFrame("the-secret", save, sendCommitted)
	if err == nil {
		t.Fatal("expected the save error to propagate")
	}
	if accepted {
		t.Fatal("expected accepted=false on a failed save")
	}
	if commitAttempted {
		t.Fatal("enrollment_committed must not be sent when the durable save failed")
	}
}

// TestHandleEnrolledFrameSendFailurePropagates covers the "commit-frame send
// failure causes reconnect/error" requirement: a failed send is NOT treated
// as accepted, and the caller (main.go's read loop) is expected to tear the
// connection down on this error rather than silently continuing — see
// TestMaybeCleanupTokenOnReconnectFiresOnlyForOrdinaryReconnect for proof
// that no cleanup path is reachable without a successful, confirmed frame.
func TestHandleEnrolledFrameSendFailurePropagates(t *testing.T) {
	saveCalled := false
	save := func(string) error { saveCalled = true; return nil }
	sendCommitted := func() error { return errors.New("connection reset") }

	accepted, err := handleEnrolledFrame("the-secret", save, sendCommitted)
	if err == nil {
		t.Fatal("expected the send error to propagate")
	}
	if !saveCalled {
		t.Fatal("save must still have been attempted (and succeeded) before the send failure")
	}
	if accepted {
		t.Fatal("expected accepted=false when sending enrollment_committed fails — a failed send proves nothing was acknowledged")
	}
}

func TestHandleEnrolledFrameIgnoresEmptySecret(t *testing.T) {
	called := false
	touch := func() error { called = true; return nil }
	accepted, err := handleEnrolledFrame("", func(string) error { called = true; return nil }, touch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if accepted {
		t.Fatal("expected accepted=false for an empty secret")
	}
	if called {
		t.Fatal("no dependency should be invoked for an empty secret")
	}
}

func TestMaybeCleanupTokenOnReconnectFiresOnlyForOrdinaryReconnect(t *testing.T) {
	for _, tc := range []struct {
		name             string
		haveSecret       bool
		haveToken        bool
		wantCleanupCalls int
	}{
		{name: "durable secret, no token: ordinary reconnect", haveSecret: true, haveToken: false, wantCleanupCalls: 1},
		{name: "durable secret AND a token: deferred to handleEnrolledFrame", haveSecret: true, haveToken: true, wantCleanupCalls: 0},
		{name: "no durable secret: fresh enrollment, deferred", haveSecret: false, haveToken: true, wantCleanupCalls: 0},
		{name: "neither: nothing to clean up", haveSecret: false, haveToken: false, wantCleanupCalls: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			maybeCleanupTokenOnReconnect(tc.haveSecret, tc.haveToken, func() { calls++ })
			if calls != tc.wantCleanupCalls {
				t.Fatalf("cleanup calls=%d, want %d", calls, tc.wantCleanupCalls)
			}
		})
	}
}
