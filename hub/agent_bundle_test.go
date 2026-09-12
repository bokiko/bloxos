package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// bundleFixture writes a bundle whose manifest matches its payloads.
// fullFixture builds a complete bundle. Every packaged bundle carries all three
// platforms, so a partial one is not a case that can exist.
func fullFixture(t *testing.T) string {
	t.Helper()
	return bundleFixture(t, map[string]string{
		"linux/amd64":   "amd64-agent",
		"linux/arm64":   "arm64-agent",
		"windows/amd64": "windows-agent",
	})
}

func bundleFixture(t *testing.T, platforms map[string]string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, agentBundleDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	artifacts := map[string]agentBundleArtifact{}
	for platform, body := range platforms {
		name := "bloxos-agent-" + filepath.Base(platform)
		if platform == "linux/amd64" {
			name = "bloxos-agent-linux-amd64"
		} else if platform == "linux/arm64" {
			name = "bloxos-agent-linux-arm64"
		} else if platform == "windows/amd64" {
			name = "bloxos-agent-windows-amd64.exe"
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatalf("write payload: %v", err)
		}
		sum, err := fileSHA256(path)
		if err != nil {
			t.Fatalf("sha: %v", err)
		}
		artifacts[platform] = agentBundleArtifact{File: name, Size: int64(len(body)), SHA256: sum}
	}
	manifest := agentBundleManifest{Schema: 1, Version: "v9.9.9", Source: "abc", AgentRelease: 8, Artifacts: artifacts}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, agentBundleManifestName), raw, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return root
}

// permissive stands in for validateTrustedAgentBinary in tests: the real one
// demands root ownership, which a test temp dir does not have.
func permissive(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	return path, nil
}

func TestAgentBundleLoadsAndVerifiesEveryDeclaredArtifact(t *testing.T) {
	root := fullFixture(t)
	bundle, err := loadAgentBundle(root, permissive)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if bundle == nil {
		t.Fatal("bundle is nil")
	}
	if bundle.Manifest.AgentRelease != 8 {
		t.Fatalf("agent release = %d, want 8", bundle.Manifest.AgentRelease)
	}
	// All three platforms travel together, in both server archives.
	for _, p := range []agentPlatform{
		{OS: "linux", Arch: archAMD64}, {OS: "linux", Arch: archARM64}, {OS: "windows", Arch: archAMD64},
	} {
		if _, ok := bundle.pathFor(p); !ok {
			t.Fatalf("bundle does not carry %s", p)
		}
	}
}

// A tampered or truncated payload must not be served.
func TestAgentBundleRejectsPayloadThatDoesNotMatchItsManifest(t *testing.T) {
	root := fullFixture(t)
	payload := filepath.Join(root, agentBundleDirName, "bloxos-agent-linux-amd64")
	if err := os.WriteFile(payload, []byte("tampered"), 0o755); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if _, err := loadAgentBundle(root, permissive); err == nil {
		t.Fatal("a payload that does not match its manifest must be rejected")
	}
}

// A manifest is DATA. A file name carrying a path must never become a path.
func TestAgentBundleRejectsUnsafeFileName(t *testing.T) {
	root := fullFixture(t)
	manifestPath := filepath.Join(root, agentBundleDirName, agentBundleManifestName)
	raw, _ := os.ReadFile(manifestPath)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	arts := m["artifacts"].(map[string]any)
	entry := arts["linux/amd64"].(map[string]any)
	entry["file"] = "../../etc/passwd"
	out, _ := json.Marshal(m)
	_ = os.WriteFile(manifestPath, out, 0o644)

	if _, err := loadAgentBundle(root, permissive); err == nil {
		t.Fatal("a traversing file name must be rejected before any filesystem call")
	}
}

// An absent bundle is not an error here; the caller decides what it means.
func TestAgentBundleAbsentIsNotAnError(t *testing.T) {
	bundle, err := loadAgentBundle(t.TempDir(), permissive)
	if err != nil {
		t.Fatalf("absent bundle should not error: %v", err)
	}
	if bundle != nil {
		t.Fatal("expected nil bundle")
	}
}

// --- the precedence contract ---

func resolverWithBundle(t *testing.T, root string, env map[string]string, mode string) agentBinaryResolver {
	t.Helper()
	bundle, err := loadAgentBundle(root, permissive)
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	return agentBinaryResolver{
		executablePath: func() (string, error) { return filepath.Join(root, "bloxos-hub"), nil },
		validate:       permissive,
		bundle:         bundle,
		deliveryMode:   mode,
		getenv:         func(k string) string { return env[k] },
	}
}

func firstCandidate(t *testing.T, r agentBinaryResolver, platform agentPlatform) agentBinaryCandidate {
	t.Helper()
	c, err := r.candidatesFor(platform)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if len(c) == 0 {
		t.Fatal("no candidates")
	}
	return c[0]
}

// THE CASE THAT MAKES THE WHOLE FEATURE WORK. The project's own systemd unit
// sets BLOXOS_AGENT_BINARY to the legacy default path. Treated as a custom pin
// it would shadow the bundle on every standard native install, and shipping
// agents in the release would change nothing.
func TestManagedBundleBeatsTheProjectsOwnShippedDefault(t *testing.T) {
	root := fullFixture(t)
	r := resolverWithBundle(t, root,
		map[string]string{"BLOXOS_AGENT_BINARY": linuxAgentBinaryDefault}, agentDeliveryAuto)

	got := firstCandidate(t, r, agentPlatform{OS: "linux", Arch: archAMD64})
	if got.Source != "managed-bundle" {
		t.Fatalf("source = %q, want managed-bundle — the shipped default must not pin a packaged hub", got.Source)
	}
}

// A genuine operator pin still wins and stays fail-closed.
func TestACustomOverrideStillOutranksTheManagedBundle(t *testing.T) {
	root := fullFixture(t)
	r := resolverWithBundle(t, root,
		map[string]string{"BLOXOS_AGENT_BINARY": "/opt/mine/bloxos-agent"}, agentDeliveryAuto)

	got := firstCandidate(t, r, agentPlatform{OS: "linux", Arch: archAMD64})
	if got.Source != "environment:BLOXOS_AGENT_BINARY" {
		t.Fatalf("source = %q, want the operator's own override to win", got.Source)
	}
	if got.Env == "" {
		t.Fatal("an operator override must remain authoritative (fail-closed)")
	}
}

// The escape hatch restores the full legacy resolver, including an INTENTIONAL
// override that happens to equal the shipped default path.
func TestExternalDeliveryModeRestoresTheLegacyResolver(t *testing.T) {
	root := fullFixture(t)
	r := resolverWithBundle(t, root,
		map[string]string{"BLOXOS_AGENT_BINARY": linuxAgentBinaryDefault}, agentDeliveryExternal)

	got := firstCandidate(t, r, agentPlatform{OS: "linux", Arch: archAMD64})
	if got.Source != "environment:BLOXOS_AGENT_BINARY" {
		t.Fatalf("source = %q, want the legacy override honoured in external mode", got.Source)
	}
}

func TestManagedBundleServesWindowsToo(t *testing.T) {
	root := fullFixture(t)
	r := resolverWithBundle(t, root, map[string]string{}, agentDeliveryAuto)

	got := firstCandidate(t, r, agentPlatform{OS: "windows", Arch: archAMD64})
	if got.Source != "managed-bundle" {
		t.Fatalf("source = %q, want managed-bundle — Windows must not be left frozen", got.Source)
	}
}

// A packaged bundle must carry every platform. A bundle short of one would let
// that platform keep resolving to a frozen system default while the others
// looked healthy — partial success is the failure mode this replaces.
func TestBundleMissingAPlatformIsRejected(t *testing.T) {
	root := bundleFixture(t, map[string]string{
		"linux/amd64":   "amd64-agent",
		"windows/amd64": "windows-agent",
	})
	if _, err := loadAgentBundle(root, permissive); err == nil {
		t.Fatal("a bundle without linux/arm64 must be rejected, not partially served")
	}
}

// An operator pointing a native arm64 source build at the generic variable is a
// real custom choice, and must still outrank the bundle.
func TestGenericOverrideForNonDefaultArchStillPrecedesTheBundle(t *testing.T) {
	root := fullFixture(t)
	r := resolverWithBundle(t, root,
		map[string]string{"BLOXOS_AGENT_BINARY": "/opt/mine/arm64-agent"}, agentDeliveryAuto)

	got := firstCandidate(t, r, agentPlatform{OS: "linux", Arch: archARM64})
	if got.Source != "environment:BLOXOS_AGENT_BINARY (arch-verified)" {
		t.Fatalf("source = %q, want the arch-verified generic override first", got.Source)
	}
}

// But the project's own shipped default in that same variable does NOT count as
// a custom arm64 pin.
func TestShippedDefaultIsNotACustomArm64Pin(t *testing.T) {
	root := fullFixture(t)
	r := resolverWithBundle(t, root,
		map[string]string{"BLOXOS_AGENT_BINARY": linuxAgentBinaryDefault}, agentDeliveryAuto)

	got := firstCandidate(t, r, agentPlatform{OS: "linux", Arch: archARM64})
	if got.Source != "managed-bundle" {
		t.Fatalf("source = %q, want managed-bundle", got.Source)
	}
}

// A path that merely NORMALISES to the shipped default is somebody's deliberate
// configuration, not the project's line, and keeps its authority.
func TestAPathThatOnlyNormalisesToTheDefaultIsStillCustom(t *testing.T) {
	root := fullFixture(t)
	r := resolverWithBundle(t, root,
		map[string]string{"BLOXOS_AGENT_BINARY": "/usr/local/lib/bloxos/linux/../linux/bloxos-agent"},
		agentDeliveryAuto)

	got := firstCandidate(t, r, agentPlatform{OS: "linux", Arch: archAMD64})
	if got.Source != "environment:BLOXOS_AGENT_BINARY" {
		t.Fatalf("source = %q, want the operator's own value honoured", got.Source)
	}
}

// A present directory with no manifest is BROKEN, not absent: treating it as
// absent would fall through to the frozen system defaults.
func TestBundleDirectoryWithoutManifestIsInvalidNotAbsent(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, agentBundleDirName), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := loadAgentBundle(root, permissive); err == nil {
		t.Fatal("a bundle directory with no manifest must be an error")
	}
}

// A misspelled delivery mode is visible, never silently treated as the default.
func TestUnknownDeliveryModeFailsClosed(t *testing.T) {
	if _, err := resolveAgentDeliveryMode(func(string) string { return "externl" }); err == nil {
		t.Fatal("an unrecognised delivery mode must be an error, not a guess")
	}
	mode, err := resolveAgentDeliveryMode(func(string) string { return "" })
	if err != nil || mode != agentDeliveryAuto {
		t.Fatalf("empty mode = %q, %v; want auto", mode, err)
	}
}
