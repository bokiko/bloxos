package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPackagedTreeLoadsAndFailsClosed runs the hub's real loader over a tree
// that the release packaging script actually produced and an UNCHANGED updater
// actually extracted.
//
// Every other bundle test builds its fixture in Go, so all of them would still
// pass if the packaging script wrote the payloads to the wrong path, or the
// updater dropped them in transit. Those are the failures that matter here: the
// whole point of this work is that a fix which looks correct in isolation can
// be completely inert once something else in the chain disagrees. This test is
// the one place where the Python that packs, the worker that transports, and
// the Go that reads are all the real thing.
//
// It is driven by scripts/test_server_bundle_agents.py, which sets the
// environment variable and asserts this test PASSED rather than skipped — a
// skipped test proves nothing, and a test that can silently skip is one nobody
// notices has stopped running.
func TestPackagedTreeLoadsAndFailsClosed(t *testing.T) {
	hubDir := os.Getenv("BLOXOS_TEST_PACKAGED_HUB_DIR")
	if hubDir == "" {
		t.Skip("set BLOXOS_TEST_PACKAGED_HUB_DIR; driven by scripts/test_server_bundle_agents.py")
	}

	bundle, err := loadAgentBundle(hubDir, permissive)
	if err != nil {
		t.Fatalf("the hub must load the bundle the release actually ships: %v", err)
	}
	if bundle == nil {
		t.Fatal("packaged tree produced no bundle; the payloads are not where the hub looks for them")
	}
	// Both server archives carry every agent platform, so either server can
	// serve any machine in the fleet.
	for _, platform := range []agentPlatform{
		{OS: "linux", Arch: archAMD64},
		{OS: "linux", Arch: archARM64},
		{OS: "windows", Arch: archAMD64},
	} {
		path, ok := bundle.pathFor(platform)
		if !ok {
			t.Fatalf("packaged bundle offers nothing for %s", platform)
		}
		if err := bundle.verifyPayloadIdentity(platform, path); err != nil {
			t.Fatalf("packaged %s payload fails its own catalog: %v", platform, err)
		}
	}

	// A damaged or absent required payload must stop the hub, not downgrade it
	// to whatever agent binaries happen to be on disk. Silent fallback is
	// precisely how a v1.7.1 hub came to serve agents built weeks earlier while
	// every machine correctly reported "matches offered build".
	t.Run("corrupt payload is refused", func(t *testing.T) {
		dir := copyHubDir(t, hubDir)
		payload := filepath.Join(dir, agentBundleDirName, "bloxos-agent-linux-arm64")
		if err := os.WriteFile(payload, []byte("corrupted in transit"), 0o755); err != nil {
			t.Fatalf("corrupt payload: %v", err)
		}
		if _, err := loadAgentBundle(dir, permissive); err == nil {
			t.Fatal("a corrupt payload must fail the load, not fall back to the system defaults")
		}
	})

	t.Run("missing payload is refused", func(t *testing.T) {
		dir := copyHubDir(t, hubDir)
		if err := os.Remove(filepath.Join(dir, agentBundleDirName, "bloxos-agent-windows-amd64.exe")); err != nil {
			t.Fatalf("remove payload: %v", err)
		}
		if _, err := loadAgentBundle(dir, permissive); err == nil {
			t.Fatal("a missing required payload must fail the load")
		}
	})
}

// copyHubDir copies the packaged hub directory into a temp tree so a test can
// damage it without touching the extracted artifact other subtests read.
//
// It returns the copy's HUB directory, which is what the loader is given: the
// hub executable lives at <release>/hub/bloxos-hub, so its own directory is
// <release>/hub and the bundle is <release>/hub/agents. Passing the parent here
// pointed the loader at a directory with no agents/ at all, where it correctly
// reports "no bundle" — the damaged payload was never read, and both subtests
// were failing for a reason that had nothing to do with what they claim to test.
func copyHubDir(t *testing.T, hubDir string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "release")
	target := filepath.Join(root, "hub")
	if err := os.MkdirAll(filepath.Join(target, agentBundleDirName), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(hubDir, agentBundleDirName))
	if err != nil {
		t.Fatalf("read packaged agents: %v", err)
	}
	for _, entry := range entries {
		body, err := os.ReadFile(filepath.Join(hubDir, agentBundleDirName, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(target, agentBundleDirName, entry.Name()), body, 0o755); err != nil {
			t.Fatalf("write %s: %v", entry.Name(), err)
		}
	}
	return target
}
