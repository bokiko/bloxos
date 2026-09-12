package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bokiko/bloxos/proto/updatesigning"
)

// agentImageHeader builds a minimal but genuine executable header for a
// platform, so fixtures exercise the real architecture check rather than
// skipping past it. A fixture made of plain text would have forced the check to
// be optional in tests, and an optional check is one nothing proves runs.
func agentImageHeader(t *testing.T, osName, arch string) []byte {
	t.Helper()
	if osName == "windows" {
		machine, ok := peMachineByArch[arch]
		if !ok {
			t.Fatalf("no PE machine for arch %q", arch)
		}
		// MZ stub, e_lfanew at 0x3c, PE signature + COFF header + the
		// optional-header magic the verifier reads.
		const peOffset = 0x80
		img := make([]byte, peOffset+26)
		img[0], img[1] = 'M', 'Z'
		binary.LittleEndian.PutUint32(img[0x3c:], peOffset)
		copy(img[peOffset:], []byte{'P', 'E', 0, 0})
		binary.LittleEndian.PutUint16(img[peOffset+4:], machine)
		binary.LittleEndian.PutUint16(img[peOffset+24:], 0x020b) // PE32+
		return img
	}
	machine, ok := elfMachineByArch[arch]
	if !ok {
		t.Fatalf("no ELF machine for arch %q", arch)
	}
	img := make([]byte, 64)
	copy(img, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0})
	binary.LittleEndian.PutUint16(img[16:], 2) // ET_EXEC
	binary.LittleEndian.PutUint16(img[18:], machine)
	binary.LittleEndian.PutUint32(img[20:], 1) // EV_CURRENT
	return img
}

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
		marker, err := updatesigning.ReleaseMarker(8)
		if err != nil {
			t.Fatalf("marker: %v", err)
		}
		osName, arch, _ := strings.Cut(platform, "/")
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, append(agentImageHeader(t, osName, arch), (body+marker)...), 0o755); err != nil {
			t.Fatalf("write payload: %v", err)
		}
		sum, err := fileSHA256(path)
		if err != nil {
			t.Fatalf("sha: %v", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		artifacts[platform] = agentBundleArtifact{File: name, Size: info.Size(), SHA256: sum}
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

// permissive stands in for validateTrustedAgentBinary in tests. It relaxes
// EXACTLY ONE of the real check's rules — root ownership, which a test temp dir
// can never satisfy — and keeps the rest. The regular-file and symlink rules
// stay, because tests below depend on them holding: a stub that waved through
// anything os.Stat could see would make those tests pass without the behaviour
// they name existing.
func permissive(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("path is a symlink")
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("path is not a regular file")
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

// An explicit per-arch override wins outright, even alongside a generic one.
func TestExplicitArm64OverrideBeatsBothGenericAndBundle(t *testing.T) {
	root := fullFixture(t)
	r := resolverWithBundle(t, root, map[string]string{
		"BLOXOS_AGENT_BINARY":       "/opt/generic/bloxos-agent",
		"BLOXOS_AGENT_BINARY_ARM64": "/opt/explicit/bloxos-agent",
	}, agentDeliveryAuto)

	got := firstCandidate(t, r, agentPlatform{OS: "linux", Arch: archARM64})
	if got.Source != "environment:BLOXOS_AGENT_BINARY_ARM64" {
		t.Fatalf("source = %q, want the explicit per-arch override first", got.Source)
	}
	if got.Env == "" {
		t.Fatal("an explicit per-arch override must stay authoritative")
	}
	cands, _ := r.candidatesFor(agentPlatform{OS: "linux", Arch: archARM64})
	if len(cands) != 1 {
		t.Fatalf("explicit override must be the only candidate, got %d", len(cands))
	}
}

// Resolution is captured once at init, so a payload swapped afterwards must be
// rejected against the catalog rather than served on its recomputed hash.
func TestAlteredManagedPayloadIsRejectedAtResolution(t *testing.T) {
	root := fullFixture(t)
	r := resolverWithBundle(t, root, map[string]string{}, agentDeliveryAuto)
	r.archMatch = nil

	if _, err := r.resolve("linux", archAMD64); err != nil {
		t.Fatalf("baseline resolve failed: %v", err)
	}

	payload := filepath.Join(root, agentBundleDirName, "bloxos-agent-linux-amd64")
	if err := os.WriteFile(payload, []byte("swapped-after-load"), 0o755); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if _, err := r.resolve("linux", archAMD64); err == nil {
		t.Fatal("a payload altered after load must not be served")
	}
}

// A delivery-configuration failure must fail every resolution, not just startup.
func TestRetainedInitErrorFailsEveryResolution(t *testing.T) {
	root := fullFixture(t)
	r := resolverWithBundle(t, root, map[string]string{}, agentDeliveryAuto)
	r.archMatch = nil
	if _, err := r.resolve("linux", archAMD64); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	r.initErr = errors.New("bundle went bad")
	if _, err := r.resolve("linux", archAMD64); err == nil {
		t.Fatal("a retained delivery error must fail closed on every resolution")
	}
}

// A bundle must not advertise a release its bytes do not carry: that mismatch
// is how a fleet ends up running agents months behind the hub while every
// check reports agreement.
func TestBundleRejectsPayloadWhoseReleaseMarkerDisagreesWithTheCatalog(t *testing.T) {
	root := fullFixture(t)
	stale, err := updatesigning.ReleaseMarker(7) // catalog says 8
	if err != nil {
		t.Fatalf("marker: %v", err)
	}
	payload := filepath.Join(root, agentBundleDirName, "bloxos-agent-linux-amd64")
	body := []byte("amd64-agent" + stale)
	if err := os.WriteFile(payload, body, 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Keep the catalog self-consistent so ONLY the marker disagrees.
	manifestPath := filepath.Join(root, agentBundleDirName, agentBundleManifestName)
	raw, _ := os.ReadFile(manifestPath)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	sum, _ := fileSHA256(payload)
	entry := m["artifacts"].(map[string]any)["linux/amd64"].(map[string]any)
	entry["sha256"] = sum
	entry["size"] = float64(len(body))
	out, _ := json.Marshal(m)
	_ = os.WriteFile(manifestPath, out, 0o644)

	if _, err := loadAgentBundle(root, permissive); err == nil {
		t.Fatal("a payload whose release marker disagrees with the catalog must be rejected")
	}
}

// A sha256 proves a payload is the file the catalog names. It proves nothing
// about what that file IS, so the architecture has to come from the image.
func TestBundleRejectsAPayloadBuiltForAnotherArchitecture(t *testing.T) {
	root := bundleFixture(t, map[string]string{
		"linux/amd64":   "amd64-agent",
		"linux/arm64":   "arm64-agent",
		"windows/amd64": "windows-agent",
	})
	dir := filepath.Join(root, agentBundleDirName)
	// Rebuild the arm64 slot around an amd64 image, then make the manifest
	// agree with it: size and sha256 both check out, and only reading the ELF
	// header can tell that the bytes are for the wrong machine.
	restamp(t, dir, "linux/arm64", "bloxos-agent-linux-arm64",
		append(agentImageHeader(t, "linux", "amd64"), mustMarker(t, 8)...))

	if _, err := loadAgentBundle(root, permissive); err == nil {
		t.Fatal("an arm64 slot carrying an amd64 image must be rejected")
	} else if !strings.Contains(err.Error(), "arm64") {
		t.Fatalf("the error should name the mismatched architecture, got: %v", err)
	}
}

// Windows is carried, not excluded, so it is checked like everything else.
func TestBundleRejectsAWindowsPayloadThatIsNotAPEImage(t *testing.T) {
	root := fullFixture(t)
	dir := filepath.Join(root, agentBundleDirName)
	restamp(t, dir, "windows/amd64", "bloxos-agent-windows-amd64.exe",
		append(agentImageHeader(t, "linux", "amd64"), mustMarker(t, 8)...))

	if _, err := loadAgentBundle(root, permissive); err == nil {
		t.Fatal("a windows slot carrying an ELF image must be rejected")
	}
}

func TestBundleRejectsAWindowsPayloadBuiltForAnotherArchitecture(t *testing.T) {
	root := fullFixture(t)
	dir := filepath.Join(root, agentBundleDirName)
	restamp(t, dir, "windows/amd64", "bloxos-agent-windows-amd64.exe",
		append(agentImageHeader(t, "windows", "arm64"), mustMarker(t, 8)...))

	if _, err := loadAgentBundle(root, permissive); err == nil {
		t.Fatal("a windows/amd64 slot carrying an arm64 PE must be rejected")
	}
}

// The control for the three tests above: the same fixture machinery, unaltered,
// must LOAD. Without this, a bundle rejected for some unrelated reason would
// make all of them pass while the architecture check did nothing.
func TestTheArchitectureFixturesThemselvesLoad(t *testing.T) {
	if _, err := loadAgentBundle(fullFixture(t), permissive); err != nil {
		t.Fatalf("the unaltered fixture must load, else the rejection tests prove nothing: %v", err)
	}
}

// The manifest decides which bytes the whole fleet is offered, so it gets the
// same trust check as the payloads it describes. os.Stat follows symlinks; a
// link planted here would have been read from wherever it pointed.
func TestBundleRejectsASymlinkedManifest(t *testing.T) {
	root := fullFixture(t)
	dir := filepath.Join(root, agentBundleDirName)
	manifest := filepath.Join(dir, agentBundleManifestName)
	elsewhere := filepath.Join(t.TempDir(), "planted.json")
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := os.WriteFile(elsewhere, raw, 0o644); err != nil {
		t.Fatalf("write planted manifest: %v", err)
	}
	if err := os.Remove(manifest); err != nil {
		t.Fatalf("remove manifest: %v", err)
	}
	if err := os.Symlink(elsewhere, manifest); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Note the content is BYTE-IDENTICAL to the manifest that just loaded, so
	// the only thing under test is that the link itself is refused.
	if _, err := loadAgentBundle(root, permissive); err == nil {
		t.Fatal("a symlinked manifest must be rejected even when its content is valid")
	}
}

// A FIFO stats as zero bytes, so a size limit taken from Stat waves it through
// — and the read that follows blocks or streams without end. The bound has to
// be enforced by the read.
func TestBundleRejectsAManifestThatIsNotARegularFile(t *testing.T) {
	root := fullFixture(t)
	manifest := filepath.Join(root, agentBundleDirName, agentBundleManifestName)
	if err := os.Remove(manifest); err != nil {
		t.Fatalf("remove manifest: %v", err)
	}
	if err := syscall.Mkfifo(manifest, 0o644); err != nil {
		t.Skipf("cannot create a FIFO here: %v", err)
	}
	// If this ever hangs instead of failing, the bound is being read from Stat
	// again. A FIFO with no writer blocks forever on open.
	done := make(chan error, 1)
	go func() {
		_, err := loadAgentBundle(root, permissive)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a manifest that is not a regular file must be rejected")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("loading blocked on a FIFO manifest; the size bound is not being enforced by the read")
	}
}

func TestBundleRejectsAnOversizedManifest(t *testing.T) {
	root := fullFixture(t)
	manifest := filepath.Join(root, agentBundleDirName, agentBundleManifestName)
	if err := os.WriteFile(manifest, make([]byte, agentBundleManifestMaxBytes+1), 0o644); err != nil {
		t.Fatalf("write oversized manifest: %v", err)
	}
	if _, err := loadAgentBundle(root, permissive); err == nil {
		t.Fatal("a manifest over the size limit must be rejected")
	}
}

func mustMarker(t *testing.T, seq uint64) []byte {
	t.Helper()
	marker, err := updatesigning.ReleaseMarker(seq)
	if err != nil {
		t.Fatalf("marker: %v", err)
	}
	return []byte(marker)
}

// restamp replaces one platform's payload and updates the manifest to match, so
// the size and sha256 checks pass and the test isolates what it means to.
func restamp(t *testing.T, dir, platform, name string, body []byte) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, 0o755); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	sum, err := fileSHA256(path)
	if err != nil {
		t.Fatalf("sha: %v", err)
	}
	manifestPath := filepath.Join(dir, agentBundleManifestName)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest agentBundleManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	manifest.Artifacts[platform] = agentBundleArtifact{File: name, Size: int64(len(body)), SHA256: sum}
	out, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, out, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

// The offset the verifier seeks to is read OUT OF the file it is checking, so
// it is attacker- and corruption-controlled and has to be bounded before use.
func TestVerifyPEArchRejectsMalformedImages(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, body []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		return path
	}

	good := agentImageHeader(t, "windows", archAMD64)
	if err := verifyPEArch(write("good.exe", good), archAMD64); err != nil {
		t.Fatalf("a well-formed amd64 PE must pass, else the rejections below prove nothing: %v", err)
	}

	wild := append([]byte(nil), good...)
	binary.LittleEndian.PutUint32(wild[0x3c:], 1<<30)
	if err := verifyPEArch(write("wild.exe", wild), archAMD64); err == nil {
		t.Fatal("an out-of-range PE header offset must be refused, not seeked to")
	}

	backIntoStub := append([]byte(nil), good...)
	binary.LittleEndian.PutUint32(backIntoStub[0x3c:], 0x10)
	if err := verifyPEArch(write("stub.exe", backIntoStub), archAMD64); err == nil {
		t.Fatal("a PE header offset pointing back into the DOS stub must be refused")
	}

	// Go emits only 64-bit Windows binaries; a PE32 image is a packaging error.
	pe32 := append([]byte(nil), good...)
	binary.LittleEndian.PutUint16(pe32[0x80+24:], 0x010b)
	if err := verifyPEArch(write("pe32.exe", pe32), archAMD64); err == nil {
		t.Fatal("a 32-bit PE must be refused")
	}

	if err := verifyPEArch(write("short.exe", []byte("MZ")), archAMD64); err == nil {
		t.Fatal("a truncated image must be refused, not read past its end")
	}
}
