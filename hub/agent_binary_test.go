package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bokiko/bloxos/proto/updatesigning"
)

func TestResolveAgentBinaryConfiguredMissingFailsClosed(t *testing.T) {
	t.Setenv("BLOXOS_AGENT_BINARY", filepath.Join(t.TempDir(), "missing-agent"))

	r := agentBinaryResolver{
		executablePath: func() (string, error) {
			t.Fatal("configured-path failure must not fall through to defaults")
			return "", nil
		},
		validate: func(path string) (string, error) {
			return "", os.ErrNotExist
		},
	}
	got, err := r.resolve("linux", "")
	if err == nil || !strings.Contains(err.Error(), "BLOXOS_AGENT_BINARY") {
		t.Fatalf("resolve error = %v, want configured-path failure", err)
	}
	if got.Path == "" || got.Source != "environment:BLOXOS_AGENT_BINARY" {
		t.Fatalf("failed resolution = %+v, want configured path and source preserved", got)
	}
}

func TestResolveAgentBinaryIsWorkingDirectoryIndependent(t *testing.T) {
	t.Setenv("BLOXOS_AGENT_BINARY", "")
	exeDir := t.TempDir()
	exe := filepath.Join(exeDir, "bloxos-hub")
	agent := filepath.Join(exeDir, "bloxos-agent")
	if err := os.WriteFile(exe, []byte("hub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agent, []byte("agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The resolver returns canonical paths; canonicalize the expectation too
	// so the assertion holds where the temp dir sits behind a symlink
	// (macOS: /var -> /private/var).
	agent, err := filepath.EvalSymlinks(agent)
	if err != nil {
		t.Fatal(err)
	}
	amd64Default := filepath.Join(linuxAgentBinaryDir, archAMD64, "bloxos-agent")
	r := agentBinaryResolver{
		executablePath: func() (string, error) { return exe, nil },
		validate: func(path string) (string, error) {
			if path == linuxAgentBinaryDefault || path == amd64Default {
				return "", os.ErrNotExist
			}
			return path, nil
		},
	}

	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(original) })

	var paths []string
	for range 2 {
		if err := os.Chdir(t.TempDir()); err != nil {
			t.Fatal(err)
		}
		got, err := r.resolve("linux", "")
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, got.Path)
	}
	if paths[0] != agent || paths[1] != agent {
		t.Fatalf("resolved paths = %q, want executable-relative %q", paths, agent)
	}
	got, err := r.resolve("linux", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "hub-executable-directory" || len(got.Skipped) != 2 ||
		!strings.Contains(got.Skipped[0], amd64Default) || !strings.Contains(got.Skipped[1], linuxAgentBinaryDefault) {
		t.Fatalf("resolution metadata = %+v, want source and skipped system defaults", got)
	}
}

// TestResolveLinuxAgentBinaryPerArch pins the per-architecture resolution
// order. The failure it prevents happened live: a hub image whose only Linux
// binary was arm64 served it to an x86_64 host, which installed it and
// crash-looped under systemd with "Exec format error". An arm64 request must
// resolve only from arm64 locations and, when none exists, fail with an error
// that names the architecture and the path — never fall through to the
// legacy amd64 locations.
func TestResolveLinuxAgentBinaryPerArch(t *testing.T) {
	t.Setenv("BLOXOS_AGENT_BINARY", "")
	t.Setenv("BLOXOS_AGENT_BINARY_ARM64", "")
	exeDir := t.TempDir()
	exe := filepath.Join(exeDir, "bloxos-hub")
	if err := os.WriteFile(exe, []byte("hub"), 0o755); err != nil {
		t.Fatal(err)
	}
	amd64Default := filepath.Join(linuxAgentBinaryDir, archAMD64, "bloxos-agent")
	arm64Default := filepath.Join(linuxAgentBinaryDir, archARM64, "bloxos-agent")

	// files maps an existing path to the architecture its ELF actually is, so
	// the injected validate reports presence and archMatch reports the real
	// architecture without any bytes on disk.
	resolverOver := func(files map[string]string) agentBinaryResolver {
		return agentBinaryResolver{
			executablePath: func() (string, error) { return exe, nil },
			validate: func(path string) (string, error) {
				if _, ok := files[path]; ok {
					return path, nil
				}
				return "", os.ErrNotExist
			},
			archMatch: func(path, arch string) error {
				if files[path] == arch {
					return nil
				}
				return fmt.Errorf("%s is a %s binary, not %s", path, files[path], arch)
			},
		}
	}

	// Both per-arch defaults present: each arch gets its own; the legacy path
	// (amd64) never diverts a non-amd64 request.
	r := resolverOver(map[string]string{amd64Default: "amd64", arm64Default: "arm64", linuxAgentBinaryDefault: "amd64"})
	for arch, want := range map[string]string{"amd64": amd64Default, "arm64": arm64Default, "aarch64": arm64Default, "x86_64": amd64Default, "": amd64Default} {
		got, err := r.resolve("linux", arch)
		if err != nil {
			t.Fatalf("resolve(linux, %q): %v", arch, err)
		}
		if got.Path != want || got.Source != "system-default" {
			t.Fatalf("resolve(linux, %q) = %+v, want %s from system-default", arch, got, want)
		}
	}

	// Only the legacy path present, holding amd64 (the classic amd64 source
	// or systemd install): it serves amd64, and an arm64 request is refused
	// rather than handed amd64 bytes.
	r = resolverOver(map[string]string{linuxAgentBinaryDefault: "amd64"})
	got, err := r.resolve("linux", "amd64")
	if err != nil || got.Path != linuxAgentBinaryDefault || got.Source != "system-default-legacy" {
		t.Fatalf("legacy amd64 resolution = %+v, %v; want legacy path", got, err)
	}
	if _, err := r.resolve("linux", "arm64"); err == nil {
		t.Fatal("arm64 request resolved from the legacy amd64 binary")
	}

	// An unsupported architecture is refused by name, before any lookup.
	if _, err := r.resolve("linux", "riscv64"); err == nil || !strings.Contains(err.Error(), "riscv64") || !strings.Contains(err.Error(), "amd64, arm64") {
		t.Fatalf("riscv64 error = %v, want the arch named and the supported list", err)
	}
	if _, err := r.resolve("windows", "arm64"); err == nil || !strings.Contains(err.Error(), "windows") {
		t.Fatalf("windows/arm64 error = %v, want refusal (Windows is amd64-only)", err)
	}

	// BLOXOS_AGENT_BINARY stays amd64-authoritative: an amd64 binary there is
	// served for amd64 and must not divert an arm64 request, which falls to
	// the arm64 default instead.
	t.Setenv("BLOXOS_AGENT_BINARY", "/srv/override/amd64/bloxos-agent")
	r = resolverOver(map[string]string{"/srv/override/amd64/bloxos-agent": "amd64", arm64Default: "arm64"})
	if got, err := r.resolve("linux", "amd64"); err != nil || got.Source != "environment:BLOXOS_AGENT_BINARY" {
		t.Fatalf("amd64 with override = %+v, %v", got, err)
	}
	if got, err := r.resolve("linux", "arm64"); err != nil || got.Path != arm64Default {
		t.Fatalf("arm64 with amd64 override = %+v, %v; want the arm64 default untouched", got, err)
	}
	t.Setenv("BLOXOS_AGENT_BINARY_ARM64", filepath.Join(t.TempDir(), "missing-arm64"))
	if _, err := r.resolve("linux", "arm64"); err == nil || !strings.Contains(err.Error(), "BLOXOS_AGENT_BINARY_ARM64") {
		t.Fatalf("arm64 with missing override = %v, want fail-closed on the configured path", err)
	}
}

// TestResolveLegacyNativeArm64SourceInstall is the Codex P1 regression: a
// native arm64 `go build` leaves an arm64 binary at the legacy path and the
// shipped systemd unit sets BLOXOS_AGENT_BINARY to it. arm64 must resolve that
// binary; amd64 must fail closed rather than receive arm64 bytes mislabeled.
func TestResolveLegacyNativeArm64SourceInstall(t *testing.T) {
	legacy := linuxAgentBinaryDefault
	t.Setenv("BLOXOS_AGENT_BINARY", legacy)
	t.Setenv("BLOXOS_AGENT_BINARY_ARM64", "")
	exe := filepath.Join(t.TempDir(), "bloxos-hub")
	if err := os.WriteFile(exe, []byte("hub"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := agentBinaryResolver{
		executablePath: func() (string, error) { return exe, nil },
		validate: func(path string) (string, error) {
			if path == legacy {
				return legacy, nil
			}
			return "", os.ErrNotExist
		},
		archMatch: func(path, arch string) error {
			if path == legacy && arch == archARM64 {
				return nil
			}
			return fmt.Errorf("%s is not %s", path, arch)
		},
	}
	got, err := r.resolve("linux", "arm64")
	if err != nil || got.Path != legacy {
		t.Fatalf("arm64 source install = %+v, %v; want the legacy arm64 binary served", got, err)
	}
	if _, err := r.resolve("linux", "amd64"); err == nil || !strings.Contains(err.Error(), "BLOXOS_AGENT_BINARY") {
		t.Fatalf("amd64 with an arm64 BLOXOS_AGENT_BINARY = %v, want fail-closed", err)
	}
}

// TestResolveExplicitArm64OverrideWrongArch: BLOXOS_AGENT_BINARY_ARM64 that
// points at an amd64 binary fails closed for arm64 and does not disturb amd64.
func TestResolveExplicitArm64OverrideWrongArch(t *testing.T) {
	arm64Env := filepath.Join(t.TempDir(), "arm64-agent") // actually amd64 bytes
	amd64Default := filepath.Join(linuxAgentBinaryDir, archAMD64, "bloxos-agent")
	t.Setenv("BLOXOS_AGENT_BINARY", "")
	t.Setenv("BLOXOS_AGENT_BINARY_ARM64", arm64Env)
	exe := filepath.Join(t.TempDir(), "bloxos-hub")
	if err := os.WriteFile(exe, []byte("hub"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := agentBinaryResolver{
		executablePath: func() (string, error) { return exe, nil },
		validate: func(path string) (string, error) {
			if path == arm64Env || path == amd64Default {
				return path, nil
			}
			return "", os.ErrNotExist
		},
		archMatch: func(path, arch string) error {
			if arch == archAMD64 { // both known paths are amd64 bytes
				return nil
			}
			return fmt.Errorf("%s is a amd64 binary, not %s", path, arch)
		},
	}
	if _, err := r.resolve("linux", "arm64"); err == nil || !strings.Contains(err.Error(), "BLOXOS_AGENT_BINARY_ARM64") {
		t.Fatalf("arm64 override with amd64 bytes = %v, want fail-closed naming the env", err)
	}
	if got, err := r.resolve("linux", "amd64"); err != nil || got.Path != amd64Default {
		t.Fatalf("amd64 = %+v, %v; must be unaffected by the arm64 misconfiguration", got, err)
	}
}

// TestResolveRejectsRelativeOverride: an override resolved against the process
// working directory is never intended; it fails closed before any filesystem
// access.
func TestResolveRejectsRelativeOverride(t *testing.T) {
	t.Setenv("BLOXOS_AGENT_BINARY", "relative/bloxos-agent")
	r := agentBinaryResolver{
		executablePath: func() (string, error) { t.Fatal("must not reach defaults"); return "", nil },
		validate:       func(string) (string, error) { t.Fatal("must not validate a relative override"); return "", nil },
		archMatch:      func(string, string) error { return nil },
	}
	got, err := r.resolve("linux", "amd64")
	if err == nil || !strings.Contains(err.Error(), "must be an absolute path") {
		t.Fatalf("relative override = %v, want absolute-path rejection", err)
	}
	if got.Source != "environment:BLOXOS_AGENT_BINARY" {
		t.Fatalf("failed resolution source = %q, want the env source preserved", got.Source)
	}
}

// TestVerifyELFArch covers the arch check on real header bytes: matching CPU,
// wrong CPU, 32-bit class, big-endian data, bad version, non-ELF text,
// truncated, unknown requested arch, and a missing file.
func TestVerifyELFArch(t *testing.T) {
	header := func(class, data, version byte, machine uint16) []byte {
		b := make([]byte, 64)
		b[0], b[1], b[2], b[3] = 0x7f, 'E', 'L', 'F'
		b[4], b[5], b[6] = class, data, version
		b[18], b[19] = byte(machine), byte(machine>>8) // little-endian
		return b
	}
	write := func(b []byte) string {
		p := filepath.Join(t.TempDir(), "bin")
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	amd64ELF := header(2, 1, 1, 0x3e)
	arm64ELF := header(2, 1, 1, 0xb7)

	if err := verifyELFArch(write(amd64ELF), archAMD64); err != nil {
		t.Fatalf("amd64 ELF as amd64: %v", err)
	}
	if err := verifyELFArch(write(arm64ELF), archARM64); err != nil {
		t.Fatalf("arm64 ELF as arm64: %v", err)
	}
	for _, tc := range []struct {
		name, arch, wantSub string
		bytes               []byte
	}{
		{"arm64 bytes as amd64", archAMD64, "arm64", arm64ELF},
		{"amd64 bytes as arm64", archARM64, "amd64", amd64ELF},
		{"32-bit class", archAMD64, "64-bit", header(1, 1, 1, 0x3e)},
		{"big-endian data", archAMD64, "little-endian", header(2, 2, 1, 0x3e)},
		{"bad version", archAMD64, "version", header(2, 1, 0, 0x3e)},
		{"non-ELF text", archAMD64, "not an ELF", []byte("#!/bin/sh\necho hello world\n")},
		{"truncated", archAMD64, "ELF header", []byte{0x7f, 'E', 'L', 'F', 2, 1, 1}},
		{"unknown requested arch", "riscv64", "unsupported architecture", amd64ELF},
	} {
		if err := verifyELFArch(write(tc.bytes), tc.arch); err == nil || !strings.Contains(err.Error(), tc.wantSub) {
			t.Errorf("%s: err = %v, want it to mention %q", tc.name, err, tc.wantSub)
		}
	}
	if err := verifyELFArch(filepath.Join(t.TempDir(), "nope"), archAMD64); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestValidateTrustedAgentBinaryRejectsFinalSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "agent")
	if err := os.WriteFile(target, []byte("agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := validateTrustedAgentBinary(link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("validate error = %v, want symlink rejection", err)
	}
}

func TestValidateTrustedAgentBinaryRejectsWritableAncestor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent")
	if err := os.WriteFile(path, []byte("agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := validateTrustedAgentBinary(path); err == nil ||
		(!strings.Contains(err.Error(), "writable") && !strings.Contains(err.Error(), "owned by uid")) {
		t.Fatalf("validate error = %v, want untrusted-ancestor rejection", err)
	}
}

func TestRecomputeFailureClearsOnlyFailedPlatformState(t *testing.T) {
	withAgentBinaryState(t)
	setAgentBinaryState("linux", agentBinaryState{Path: "/old/linux", SHA: strings.Repeat("a", 64)})
	setAgentBinaryState("windows", agentBinaryState{Path: "/old/windows", SHA: strings.Repeat("b", 64)})

	windowsPath := filepath.Join(t.TempDir(), "bloxos-agent.exe")
	windowsBody := []byte("new trusted windows binary")
	if err := os.WriteFile(windowsPath, windowsBody, 0o755); err != nil {
		t.Fatal(err)
	}
	windowsSum := sha256.Sum256(windowsBody)
	windowsSHA := hex.EncodeToString(windowsSum[:])

	oldResolver := resolveAgentBinaryFor
	resolveAgentBinaryFor = func(osName, arch string) (agentBinaryResolution, error) {
		if osName == "linux" {
			return agentBinaryResolution{Path: "/missing/linux", Source: "environment:BLOXOS_AGENT_BINARY"}, os.ErrNotExist
		}
		return agentBinaryResolution{Path: windowsPath, Source: "test:windows"}, nil
	}
	t.Cleanup(func() { resolveAgentBinaryFor = oldResolver })

	recomputeAgentBinarySHA()
	linux := currentAgentBinaryState("linux")
	windows := currentAgentBinaryState("windows")
	if linux.SHA != "" || linux.Error == "" || linux.Path != "/missing/linux" || linux.Source == "" {
		t.Fatalf("linux state = %+v, want stale SHA cleared and error surfaced", linux)
	}
	if windows.SHA != windowsSHA || windows.Path != windowsPath || windows.Error != "" {
		t.Fatalf("windows state = %+v, want independently refreshed state", windows)
	}
}

func TestHashDownloadAndSignatureUseSameResolvedPath(t *testing.T) {
	e, _ := setupTestServer(t)
	withAgentBinaryState(t)

	dir := t.TempDir()
	bin := filepath.Join(dir, "served-agent")
	body := []byte("one canonical binary for hash download and signature")
	if err := os.WriteFile(bin, body, 0o755); err != nil {
		t.Fatal(err)
	}

	oldResolver := resolveAgentBinaryFor
	resolveAgentBinaryFor = func(osName, arch string) (agentBinaryResolution, error) {
		return agentBinaryResolution{Path: bin, Source: "test:canonical"}, nil
	}
	t.Cleanup(func() { resolveAgentBinaryFor = oldResolver })

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	updateSigningMu.Lock()
	oldKey, oldPub, oldB64 := updateSigningKey, updateSigningPub, updateSigningPubB64
	updateSigningKey, updateSigningPub = nil, pub
	updateSigningPubB64 = base64.StdEncoding.EncodeToString(pub)
	updateSigningMu.Unlock()
	t.Cleanup(func() {
		updateSigningMu.Lock()
		updateSigningKey, updateSigningPub, updateSigningPubB64 = oldKey, oldPub, oldB64
		updateSigningMu.Unlock()
	})

	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, updatesigning.Message("linux", sha)))
	if err := os.WriteFile(bin+".sig", []byte(sig+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	recomputeBinaryFor("linux")
	state := currentAgentBinaryState("linux")
	if state.Path != bin || state.Source != "test:canonical" || state.SHA != sha {
		t.Fatalf("cached state = %+v", state)
	}
	if got := announcedSignatureFor("linux", sha); got != sig {
		t.Fatalf("announced signature = %q, want detached signature from cached path", got)
	}

	req := httptest.NewRequest(http.MethodGet, "/download/agent?os=linux", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("download status %d: %s", rec.Code, rec.Body.String())
	}
	downloaded := rec.Body.Bytes()
	downloadSum := sha256.Sum256(downloaded)
	if got := hex.EncodeToString(downloadSum[:]); got != state.SHA {
		t.Fatalf("download SHA = %s, API/cache SHA = %s", got, state.SHA)
	}
	if string(downloaded) != string(body) {
		t.Fatal("download did not serve the cached resolved path")
	}
}

func TestVersionsAPIExposesResolvedBinaryState(t *testing.T) {
	e, server := setupTestServer(t)
	withAgentBinaryState(t)
	linux := agentBinaryState{
		Path:   "/usr/local/lib/bloxos/linux/bloxos-agent",
		Source: "system-default",
		SHA:    strings.Repeat("a", 64),
		Mtime:  time.Unix(123, 0).UTC(),
	}
	windows := agentBinaryState{
		Path:   "/srv/bloxos/windows/bloxos-agent.exe",
		Source: "environment:BLOXOS_AGENT_BINARY_WINDOWS",
		Error:  "configured path is untrusted",
	}
	setAgentBinaryState("linux", linux)
	setAgentBinaryState("windows", windows)
	server.markCredentialsRotated(t)

	req := httptest.NewRequest(http.MethodGet, "/api/versions", nil)
	req.Header.Set("Authorization", "Bearer "+loginAndGetToken(t, e))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		AgentBinaries map[string]agentBinaryState `json:"agent_binaries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if got := response.AgentBinaries["linux"]; got.Path != linux.Path || got.Source != linux.Source || got.SHA != linux.SHA {
		t.Fatalf("linux API state = %+v, want %+v", got, linux)
	}
	if got := response.AgentBinaries["windows"]; got.SHA != "" || got.Error != windows.Error || got.Path != windows.Path || got.Source != windows.Source {
		t.Fatalf("windows API state = %+v, want failed state %+v", got, windows)
	}
}

// TestVersionsAPIExposesPerArchBinaryState pins the compatibility contract
// of /api/versions: the pre-arch fields keep meaning linux/amd64 so the
// Versions dashboard keeps working, and agent_binaries_by_arch carries every
// platform.
func TestVersionsAPIExposesPerArchBinaryState(t *testing.T) {
	e, server := setupTestServer(t)
	withAgentBinaryState(t)
	amd64 := agentBinaryState{Path: "/usr/local/lib/bloxos/linux/amd64/bloxos-agent", Source: "system-default", SHA: strings.Repeat("a", 64)}
	arm64 := agentBinaryState{Path: "/usr/local/lib/bloxos/linux/arm64/bloxos-agent", Source: "system-default", SHA: strings.Repeat("b", 64)}
	setAgentBinaryStateFor("linux", "amd64", amd64)
	setAgentBinaryStateFor("linux", "arm64", arm64)
	server.markCredentialsRotated(t)

	req := httptest.NewRequest(http.MethodGet, "/api/versions", nil)
	req.Header.Set("Authorization", "Bearer "+loginAndGetToken(t, e))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		HubSHA        string                                 `json:"hub_sha"`
		AgentBinaries map[string]agentBinaryState            `json:"agent_binaries"`
		ByArch        map[string]map[string]agentBinaryState `json:"agent_binaries_by_arch"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if response.HubSHA != amd64.SHA || response.AgentBinaries["linux"].SHA != amd64.SHA {
		t.Fatalf("legacy linux fields = %q / %+v, want the amd64 state", response.HubSHA, response.AgentBinaries["linux"])
	}
	if got := response.ByArch["linux"]["amd64"]; got.SHA != amd64.SHA || got.Path != amd64.Path {
		t.Fatalf("by_arch linux/amd64 = %+v, want %+v", got, amd64)
	}
	if got := response.ByArch["linux"]["arm64"]; got.SHA != arm64.SHA || got.Path != arm64.Path {
		t.Fatalf("by_arch linux/arm64 = %+v, want %+v", got, arm64)
	}
	if _, ok := response.ByArch["windows"]["amd64"]; !ok {
		t.Fatalf("by_arch lacks windows/amd64: %s", rec.Body.String())
	}
}

func withAgentBinaryState(t *testing.T) {
	t.Helper()
	hubAgentBinaryMu.Lock()
	old := agentBinaryStates
	agentBinaryStates = newAgentBinaryStates()
	hubAgentBinaryMu.Unlock()
	t.Cleanup(func() {
		hubAgentBinaryMu.Lock()
		agentBinaryStates = old
		hubAgentBinaryMu.Unlock()
	})
}

// setAgentBinaryState stages the state for an OS's default architecture —
// "linux" means linux/amd64, as it did before the hub knew about
// architectures.
func setAgentBinaryState(osName string, state agentBinaryState) {
	setAgentBinaryStateFor(osName, defaultAgentArch, state)
}

func setAgentBinaryStateFor(osName, arch string, state agentBinaryState) {
	platform, err := agentPlatformFor(osName, arch)
	if err != nil {
		panic(err)
	}
	hubAgentBinaryMu.Lock()
	defer hubAgentBinaryMu.Unlock()
	if state.Mtime.IsZero() {
		state.Mtime = time.Now()
	}
	agentBinaryStates[platform.String()] = state
}

func useGeneratedTestBinary(t *testing.T, osName string) string {
	t.Helper()
	name := "bloxos-agent"
	if normalizeAgentOS(osName) == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("test "+osName+" agent binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	withAgentBinaryState(t)
	useTestResolvedBinary(t, osName, path)
	return path
}

// useTestResolvedBinary serves path for an OS's default architecture; other
// architectures keep whatever resolver was installed before.
func useTestResolvedBinary(t *testing.T, osName, path string) {
	t.Helper()
	useTestResolvedBinaryForArch(t, osName, defaultAgentArch, path)
}

func useTestResolvedBinaryForArch(t *testing.T, osName, arch, path string) {
	t.Helper()
	want, err := agentPlatformFor(osName, arch)
	if err != nil {
		t.Fatal(err)
	}
	oldResolver := resolveAgentBinaryFor
	resolveAgentBinaryFor = func(requestedOS, requestedArch string) (agentBinaryResolution, error) {
		if got, err := agentPlatformFor(requestedOS, requestedArch); err == nil && got == want {
			return agentBinaryResolution{Path: path, Source: "test:fixture"}, nil
		}
		return oldResolver(requestedOS, requestedArch)
	}
	t.Cleanup(func() { resolveAgentBinaryFor = oldResolver })
}

// useGeneratedTestBinaryForArch is useGeneratedTestBinary for one Linux
// architecture, without resetting state already staged for other platforms.
func useGeneratedTestBinaryForArch(t *testing.T, osName, arch string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bloxos-agent")
	if err := os.WriteFile(path, []byte("test "+osName+"/"+arch+" agent binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	useTestResolvedBinaryForArch(t, osName, arch, path)
	return path
}

// TestDownloadAgentHonorsArch pins the download contract the installer and
// the self-updater rely on: ?arch selects the build, no arch means amd64
// (the pre-arch behaviour every deployed agent expects), and an
// architecture the hub cannot serve is a 404 whose body names it and the
// path the hub looked at — never the wrong architecture's bytes.
func TestDownloadAgentHonorsArch(t *testing.T) {
	e, _ := setupTestServer(t)
	withAgentBinaryState(t)
	amd64Path := useGeneratedTestBinaryForArch(t, "linux", "amd64")
	arm64Path := useGeneratedTestBinaryForArch(t, "linux", "arm64")
	recomputeAgentBinarySHA()

	download := func(query string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, "/download/agent"+query, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	amd64Body, _ := os.ReadFile(amd64Path)
	arm64Body, _ := os.ReadFile(arm64Path)
	if string(amd64Body) == string(arm64Body) {
		t.Fatal("test setup: fixtures must differ")
	}

	for query, want := range map[string]string{
		"?os=linux&arch=arm64":   string(arm64Body),
		"?os=linux&arch=aarch64": string(arm64Body),
		"?os=linux&arch=amd64":   string(amd64Body),
		"?os=linux":              string(amd64Body),
		"":                       string(amd64Body),
	} {
		code, body := download(query)
		if code != http.StatusOK || body != want {
			t.Fatalf("GET /download/agent%s = %d %q, want 200 with the right architecture's bytes", query, code, body)
		}
	}

	code, body := download("?os=linux&arch=riscv64")
	if code != http.StatusNotFound || !strings.Contains(body, "riscv64") || !strings.Contains(body, "amd64, arm64") {
		t.Fatalf("unsupported arch = %d %s, want 404 naming riscv64 and the supported list", code, body)
	}

	// arm64 goes missing: its request must 404 with the resolver's path,
	// while amd64 keeps serving.
	oldResolver := resolveAgentBinaryFor
	resolveAgentBinaryFor = func(osName, arch string) (agentBinaryResolution, error) {
		if a, _ := normalizeAgentArch(arch); a == archARM64 {
			return agentBinaryResolution{}, fmt.Errorf("no trusted linux/arm64 agent binary: system-default /usr/local/lib/bloxos/linux/arm64/bloxos-agent: no such file")
		}
		return oldResolver(osName, arch)
	}
	t.Cleanup(func() { resolveAgentBinaryFor = oldResolver })
	code, body = download("?os=linux&arch=arm64")
	if code != http.StatusNotFound || !strings.Contains(body, "arch=arm64") ||
		!strings.Contains(body, "/usr/local/lib/bloxos/linux/arm64/bloxos-agent") {
		t.Fatalf("missing arm64 = %d %s, want 404 naming the arch and the path looked at", code, body)
	}
	if code, body := download("?os=linux&arch=amd64"); code != http.StatusOK || body != string(amd64Body) {
		t.Fatalf("amd64 after arm64 failure = %d %q, want still served", code, body)
	}
}
