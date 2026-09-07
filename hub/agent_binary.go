package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// linuxAgentBinaryDefault is the pre-arch system default, still populated
	// by the hub image for backward compatibility. It predates per-architecture
	// delivery, so its architecture is whatever was built there — amd64 on the
	// classic install, or the host's arch on a native source build. It is a
	// candidate for every architecture, and resolve() serves it only for the
	// architecture its ELF actually is.
	linuxAgentBinaryDefault = "/usr/local/lib/bloxos/linux/bloxos-agent"
	// linuxAgentBinaryDir is the parent of the per-architecture system
	// defaults: <dir>/<arch>/bloxos-agent. Dockerfile.hub populates both.
	linuxAgentBinaryDir       = "/usr/local/lib/bloxos/linux"
	windowsAgentBinaryDefault = "/usr/local/lib/bloxos/windows/bloxos-agent.exe"

	archAMD64 = "amd64"
	archARM64 = "arm64"
	// defaultAgentArch is what a request, installer or agent that names no
	// architecture gets. Everything that predates per-architecture delivery
	// — the ?arch-less self-updater, the legacy system default, an
	// agent_running_version frame without an arch field — is amd64.
	defaultAgentArch = archAMD64
)

// agentPlatform is the (os, arch) pair the hub tracks a served binary for.
type agentPlatform struct {
	OS   string
	Arch string
}

func (p agentPlatform) String() string { return p.OS + "/" + p.Arch }

// supportedAgentPlatforms is every platform the hub resolves, hashes and
// serves. Windows stays amd64-only.
var supportedAgentPlatforms = []agentPlatform{
	{OS: "linux", Arch: archAMD64},
	{OS: "linux", Arch: archARM64},
	{OS: "windows", Arch: archAMD64},
}

type agentBinaryResolution struct {
	Path    string
	Source  string
	Skipped []string
}

type agentBinaryState struct {
	Release uint64    `json:"release"`
	Path    string    `json:"path"`
	Source  string    `json:"source"`
	SHA     string    `json:"sha"`
	Mtime   time.Time `json:"mtime"`
	Error   string    `json:"error"`
}

type agentBinaryResolver struct {
	executablePath func() (string, error)
	validate       func(string) (string, error)
	// archMatch verifies that the binary at path is built for arch (Linux
	// ELF e_machine). nil disables the check — used by unit tests that inject
	// a fake validate over paths that do not exist on disk. Production sets
	// it to verifyELFArch so a binary is only ever served for the
	// architecture it actually is.
	archMatch func(path, arch string) error
}

var (
	hubAgentBinaryMu  sync.RWMutex
	agentBinaryStates = newAgentBinaryStates()
	// resolveAgentBinaryFor resolves the trusted binary for (os, arch). An
	// empty arch means defaultAgentArch.
	resolveAgentBinaryFor = productionAgentBinaryResolver().resolve
)

func newAgentBinaryStates() map[string]agentBinaryState {
	states := make(map[string]agentBinaryState, len(supportedAgentPlatforms))
	for _, p := range supportedAgentPlatforms {
		states[p.String()] = agentBinaryState{}
	}
	return states
}

func productionAgentBinaryResolver() agentBinaryResolver {
	return agentBinaryResolver{
		executablePath: os.Executable,
		validate:       validateTrustedAgentBinary,
		archMatch:      verifyELFArch,
	}
}

// elfMachineByArch maps a GOARCH to the ELF e_machine value its binaries
// carry: x86-64 = 0x3e, AArch64 = 0xb7.
var elfMachineByArch = map[string]uint16{archAMD64: 0x3e, archARM64: 0xb7}

func elfMachineName(m uint16) string {
	for arch, want := range elfMachineByArch {
		if want == m {
			return arch
		}
	}
	return fmt.Sprintf("e_machine=0x%02x", m)
}

// verifyELFArch reports whether the binary at path is a 64-bit little-endian
// Linux ELF built for arch. It reads only the 20-byte ELF identification plus
// e_machine, so it is cheap on the resolution hot path. Supported agent builds
// (Go linux/amd64 and linux/arm64) are always ELF64, two's-complement
// little-endian, current version; anything else is refused rather than
// guessed. The check is what lets the shared, pre-architecture paths
// (BLOXOS_AGENT_BINARY, the legacy default, a hub sibling) be candidates for
// any architecture without ever handing a request another architecture's
// bytes: an arm64 binary left there by a native source build resolves for
// arm64 and is refused for amd64, and vice versa.
func verifyELFArch(path, arch string) error {
	want, ok := elfMachineByArch[arch]
	if !ok {
		return fmt.Errorf("unsupported architecture %q", arch)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var hdr [20]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return fmt.Errorf("read ELF header of %s: %w", path, err)
	}
	if hdr[0] != 0x7f || hdr[1] != 'E' || hdr[2] != 'L' || hdr[3] != 'F' {
		return fmt.Errorf("%s is not an ELF binary", path)
	}
	if hdr[4] != 2 { // EI_CLASS: ELFCLASS64
		return fmt.Errorf("%s is not a 64-bit ELF binary", path)
	}
	if hdr[5] != 1 { // EI_DATA: ELFDATA2LSB
		return fmt.Errorf("%s is not a little-endian ELF binary", path)
	}
	if hdr[6] != 1 { // EI_VERSION: EV_CURRENT
		return fmt.Errorf("%s has an unsupported ELF version", path)
	}
	machine := uint16(hdr[18]) | uint16(hdr[19])<<8
	if machine != want {
		return fmt.Errorf("%s is a %s binary, not %s", path, elfMachineName(machine), arch)
	}
	return nil
}

func normalizeAgentOS(osName string) string {
	if strings.EqualFold(strings.TrimSpace(osName), "windows") {
		return "windows"
	}
	return "linux"
}

// normalizeAgentArch maps the spellings agents, installers and operators use
// onto the GOARCH names the hub keys on. Empty means defaultAgentArch. The
// second result is false for an architecture the hub does not build agents
// for; the first result is then the trimmed, lowercased input so error
// messages can name it.
func normalizeAgentArch(arch string) (string, bool) {
	arch = strings.ToLower(strings.TrimSpace(arch))
	switch arch {
	case "", archAMD64, "x86_64", "x64":
		return archAMD64, true
	case archARM64, "aarch64":
		return archARM64, true
	default:
		return arch, false
	}
}

// agentPlatformFor validates an (os, arch) request against the platforms the
// hub serves. The error names the architecture and what is supported, so it
// can be returned to an installer verbatim.
func agentPlatformFor(osName, arch string) (agentPlatform, error) {
	osName = normalizeAgentOS(osName)
	normalized, ok := normalizeAgentArch(arch)
	if ok {
		platform := agentPlatform{OS: osName, Arch: normalized}
		for _, p := range supportedAgentPlatforms {
			if p == platform {
				return platform, nil
			}
		}
	}
	return agentPlatform{}, fmt.Errorf("the hub does not build %s agents for arch=%q (supported: %s)",
		osName, normalized, strings.Join(supportedArchesFor(osName), ", "))
}

func supportedArchesFor(osName string) []string {
	var arches []string
	for _, p := range supportedAgentPlatforms {
		if p.OS == osName {
			arches = append(arches, p.Arch)
		}
	}
	return arches
}

// agentBinaryEnvName is the per-arch override variable for a platform.
// BLOXOS_AGENT_BINARY is the amd64 override, so existing service and .env files
// keep working unchanged; on a non-amd64 host it is also consulted as an
// ELF-verified fallback (see candidatesFor), which is how a native source
// build is honored.
func agentBinaryEnvName(platform agentPlatform) string {
	switch {
	case platform.OS == "windows":
		return "BLOXOS_AGENT_BINARY_WINDOWS"
	case platform.Arch == defaultAgentArch:
		return "BLOXOS_AGENT_BINARY"
	default:
		return "BLOXOS_AGENT_BINARY_" + strings.ToUpper(platform.Arch)
	}
}

// agentBinaryCandidate is one location the resolver will try. Env names an
// explicit override variable; a candidate with Env set is authoritative — if
// it is present but unusable (missing, untrusted, or the wrong architecture)
// resolution fails closed rather than falling through.
type agentBinaryCandidate struct {
	Path   string
	Source string
	Env    string
}

// candidatesFor lists, in order, where the hub looks for a platform's binary.
// The per-arch override (BLOXOS_AGENT_BINARY for amd64, BLOXOS_AGENT_BINARY_ARM64
// for arm64, BLOXOS_AGENT_BINARY_WINDOWS for Windows) is authoritative when
// set. Otherwise Linux tries the per-arch system default, then the shared
// pre-architecture locations — the legacy default, a hub-executable sibling,
// and (for a non-default arch) the pre-arch BLOXOS_AGENT_BINARY. Those shared
// locations are candidates for every architecture now, because a native source
// build (go build on an arm64 host) leaves that host's binary there; the ELF
// arch check in resolve() is what stops any of them from being served to a
// different architecture.
func (r agentBinaryResolver) candidatesFor(platform agentPlatform) ([]agentBinaryCandidate, error) {
	if platform.OS == "windows" {
		if v := strings.TrimSpace(os.Getenv("BLOXOS_AGENT_BINARY_WINDOWS")); v != "" {
			return []agentBinaryCandidate{{Path: v, Source: "environment:BLOXOS_AGENT_BINARY_WINDOWS", Env: "BLOXOS_AGENT_BINARY_WINDOWS"}}, nil
		}
		executable, err := r.hubExecutableDir()
		if err != nil {
			return nil, err
		}
		return []agentBinaryCandidate{
			{Path: windowsAgentBinaryDefault, Source: "system-default"},
			{Path: filepath.Join(executable, "bloxos-agent.exe"), Source: "hub-executable-directory"},
		}, nil
	}

	perArchEnv := agentBinaryEnvName(platform)
	if v := strings.TrimSpace(os.Getenv(perArchEnv)); v != "" {
		return []agentBinaryCandidate{{Path: v, Source: "environment:" + perArchEnv, Env: perArchEnv}}, nil
	}

	var candidates []agentBinaryCandidate
	if platform.Arch != defaultAgentArch {
		// A non-default arch has no dedicated pre-arch override variable, so
		// the pre-arch BLOXOS_AGENT_BINARY is where an operator points a native
		// source build (go build on an arm64 host, wired through the shipped
		// systemd unit). Honor it ahead of the packaged per-arch default so an
		// explicit operator choice wins. Non-authoritative and ELF-gated: a
		// wrong architecture just skips to the next candidate.
		if v := strings.TrimSpace(os.Getenv("BLOXOS_AGENT_BINARY")); v != "" {
			candidates = append(candidates, agentBinaryCandidate{Path: v, Source: "environment:BLOXOS_AGENT_BINARY (arch-verified)"})
		}
	}
	candidates = append(candidates, agentBinaryCandidate{Path: filepath.Join(linuxAgentBinaryDir, platform.Arch, "bloxos-agent"), Source: "system-default"})
	executable, err := r.hubExecutableDir()
	if err != nil {
		return nil, err
	}
	return append(candidates,
		agentBinaryCandidate{Path: linuxAgentBinaryDefault, Source: "system-default-legacy"},
		agentBinaryCandidate{Path: filepath.Join(executable, "bloxos-agent"), Source: "hub-executable-directory"},
	), nil
}

func (r agentBinaryResolver) hubExecutableDir() (string, error) {
	executable, err := r.executablePath()
	if err != nil {
		return "", fmt.Errorf("resolve hub executable: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return "", fmt.Errorf("make hub executable path absolute: %w", err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("canonicalize hub executable path %s: %w", executable, err)
	}
	return filepath.Dir(executable), nil
}

func (r agentBinaryResolver) resolve(osName, arch string) (agentBinaryResolution, error) {
	platform, err := agentPlatformFor(osName, arch)
	if err != nil {
		return agentBinaryResolution{}, err
	}
	candidates, err := r.candidatesFor(platform)
	if err != nil {
		return agentBinaryResolution{}, err
	}

	var failures []string
	for _, candidate := range candidates {
		if candidate.Env != "" && !filepath.IsAbs(candidate.Path) {
			// Reject a relative override before touching the filesystem: an
			// explicit override resolved against the process working directory
			// is never what an operator means and must fail closed.
			return agentBinaryResolution{Path: filepath.Clean(candidate.Path), Source: candidate.Source},
				fmt.Errorf("%s=%q must be an absolute path", candidate.Env, candidate.Path)
		}
		path, err := r.validate(candidate.Path)
		if err == nil && r.archMatch != nil && platform.OS == "linux" {
			err = r.archMatch(path, platform.Arch)
		}
		if err == nil {
			return agentBinaryResolution{
				Path:    path,
				Source:  candidate.Source,
				Skipped: append([]string(nil), failures...),
			}, nil
		}
		if candidate.Env != "" {
			// Explicit override present but unusable: fail closed, do not fall
			// through to a default that could be a different architecture.
			return agentBinaryResolution{Path: filepath.Clean(candidate.Path), Source: candidate.Source},
				fmt.Errorf("%s=%q is unusable: %w", candidate.Env, candidate.Path, err)
		}
		failures = append(failures, fmt.Sprintf("%s %s: %v", candidate.Source, candidate.Path, err))
	}
	return agentBinaryResolution{}, fmt.Errorf("no trusted %s agent binary: %s",
		platform, strings.Join(failures, "; "))
}

func validateTrustedAgentBinary(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path %q is not absolute", path)
	}
	lexical := filepath.Clean(path)
	info, err := os.Lstat(lexical)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("final binary path %s is a symlink", lexical)
	}

	resolved, err := filepath.EvalSymlinks(lexical)
	if err != nil {
		return "", fmt.Errorf("canonicalize %s: %w", lexical, err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("make canonical path absolute: %w", err)
	}

	for current := resolved; ; current = filepath.Dir(current) {
		component, err := os.Stat(current)
		if err != nil {
			return "", fmt.Errorf("stat trusted path component %s: %w", current, err)
		}
		stat, ok := component.Sys().(*syscall.Stat_t)
		if !ok {
			return "", fmt.Errorf("inspect owner of %s: unsupported file metadata", current)
		}
		if stat.Uid != 0 {
			return "", fmt.Errorf("%s is owned by uid %d, want root", current, stat.Uid)
		}
		if component.Mode().Perm()&0o022 != 0 {
			return "", fmt.Errorf("%s mode %04o is group- or other-writable", current, component.Mode().Perm())
		}
		if current == resolved {
			if !component.Mode().IsRegular() {
				return "", fmt.Errorf("%s is not a regular file", current)
			}
		} else if !component.IsDir() {
			return "", fmt.Errorf("ancestor %s is not a directory", current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}

	f, err := os.Open(resolved)
	if err != nil {
		return "", fmt.Errorf("open trusted binary %s: %w", resolved, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close trusted binary %s: %w", resolved, err)
	}
	return resolved, nil
}

// currentAgentBinaryState returns the state for an OS's default architecture:
// Linux amd64 (or the legacy path) and Windows amd64. Pre-arch callers and
// the pre-arch fields of /api/versions keep exactly this meaning.
func currentAgentBinaryState(osName string) agentBinaryState {
	return currentAgentBinaryStateFor(osName, defaultAgentArch)
}

// currentAgentBinaryStateFor returns the state for an (os, arch). A platform
// the hub does not build for yields a state whose Error names it, so the
// download handler and dashboard can report it without a second lookup.
func currentAgentBinaryStateFor(osName, arch string) agentBinaryState {
	platform, err := agentPlatformFor(osName, arch)
	if err != nil {
		return agentBinaryState{Error: err.Error()}
	}
	hubAgentBinaryMu.RLock()
	defer hubAgentBinaryMu.RUnlock()
	return agentBinaryStates[platform.String()]
}

func replaceAgentBinaryState(platform agentPlatform, state agentBinaryState) agentBinaryState {
	hubAgentBinaryMu.Lock()
	defer hubAgentBinaryMu.Unlock()
	previous := agentBinaryStates[platform.String()]
	agentBinaryStates[platform.String()] = state
	return previous
}

func failAgentBinaryState(platform agentPlatform, resolution agentBinaryResolution, err error) {
	state := agentBinaryState{Path: resolution.Path, Source: resolution.Source, Error: err.Error()}
	previous := replaceAgentBinaryState(platform, state)
	if previous.Error != state.Error || previous.SHA != "" {
		log.Printf("version: %s agent binary unavailable: %v", platform, err)
	}
}
