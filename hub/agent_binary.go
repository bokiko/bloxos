package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// linuxAgentBinaryDefault is the pre-arch system default. It predates
	// per-architecture delivery, and every deployment that populated it did
	// so with an amd64 build, so it is treated as amd64 and only ever
	// resolved for amd64 requests — an arm64 request never falls back to it.
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
	Path   string    `json:"path"`
	Source string    `json:"source"`
	SHA    string    `json:"sha"`
	Mtime  time.Time `json:"mtime"`
	Error  string    `json:"error"`
}

type agentBinaryResolver struct {
	executablePath func() (string, error)
	validate       func(string) (string, error)
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
	}
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

// agentBinaryEnvName is the override variable for a platform. BLOXOS_AGENT_BINARY
// keeps its pre-arch meaning — the Linux amd64 binary — so existing service
// files and .env files keep working unchanged.
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

// defaultCandidates lists, in order, where the hub looks when no override is
// set. Only Linux amd64 has legacy fallbacks: the pre-arch system default
// and a sibling of the hub executable were both populated with amd64 builds
// before the hub knew about architectures, and serving either to an arm64
// request is exactly the "Exec format error" crash loop this exists to stop.
func (r agentBinaryResolver) defaultCandidates(platform agentPlatform) ([]agentBinaryResolution, error) {
	if platform.OS == "windows" {
		executable, err := r.hubExecutableDir()
		if err != nil {
			return nil, err
		}
		return []agentBinaryResolution{
			{Path: windowsAgentBinaryDefault, Source: "system-default"},
			{Path: filepath.Join(executable, "bloxos-agent.exe"), Source: "hub-executable-directory"},
		}, nil
	}
	candidates := []agentBinaryResolution{
		{Path: filepath.Join(linuxAgentBinaryDir, platform.Arch, "bloxos-agent"), Source: "system-default"},
	}
	if platform.Arch != defaultAgentArch {
		return candidates, nil
	}
	executable, err := r.hubExecutableDir()
	if err != nil {
		return nil, err
	}
	return append(candidates,
		agentBinaryResolution{Path: linuxAgentBinaryDefault, Source: "system-default-legacy"},
		agentBinaryResolution{Path: filepath.Join(executable, "bloxos-agent"), Source: "hub-executable-directory"},
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
	envName := agentBinaryEnvName(platform)
	if configured := strings.TrimSpace(os.Getenv(envName)); configured != "" {
		resolution := agentBinaryResolution{Path: filepath.Clean(configured), Source: "environment:" + envName}
		if !filepath.IsAbs(configured) {
			return resolution, fmt.Errorf("%s=%q must be an absolute path", envName, configured)
		}
		path, err := r.validate(configured)
		if err != nil {
			return resolution, fmt.Errorf("%s=%q is unusable: %w", envName, configured, err)
		}
		resolution.Path = path
		return resolution, nil
	}

	candidates, err := r.defaultCandidates(platform)
	if err != nil {
		return agentBinaryResolution{}, err
	}

	var failures []string
	for _, candidate := range candidates {
		path, err := r.validate(candidate.Path)
		if err == nil {
			candidate.Path = path
			candidate.Skipped = append([]string(nil), failures...)
			return candidate, nil
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
