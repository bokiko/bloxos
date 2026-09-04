package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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
	got, err := r.resolve("linux")
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
	r := agentBinaryResolver{
		executablePath: func() (string, error) { return exe, nil },
		validate: func(path string) (string, error) {
			if path == linuxAgentBinaryDefault {
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
		got, err := r.resolve("linux")
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, got.Path)
	}
	if paths[0] != agent || paths[1] != agent {
		t.Fatalf("resolved paths = %q, want executable-relative %q", paths, agent)
	}
	got, err := r.resolve("linux")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "hub-executable-directory" || len(got.Skipped) != 1 || !strings.Contains(got.Skipped[0], linuxAgentBinaryDefault) {
		t.Fatalf("resolution metadata = %+v, want source and skipped system default", got)
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
	resolveAgentBinaryFor = func(osName string) (agentBinaryResolution, error) {
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
	resolveAgentBinaryFor = func(osName string) (agentBinaryResolution, error) {
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

func withAgentBinaryState(t *testing.T) {
	t.Helper()
	hubAgentBinaryMu.Lock()
	oldLinux := agentBinaryStates["linux"]
	oldWindows := agentBinaryStates["windows"]
	agentBinaryStates["linux"] = agentBinaryState{}
	agentBinaryStates["windows"] = agentBinaryState{}
	hubAgentBinaryMu.Unlock()
	t.Cleanup(func() {
		hubAgentBinaryMu.Lock()
		agentBinaryStates["linux"] = oldLinux
		agentBinaryStates["windows"] = oldWindows
		hubAgentBinaryMu.Unlock()
	})
}

func setAgentBinaryState(osName string, state agentBinaryState) {
	hubAgentBinaryMu.Lock()
	defer hubAgentBinaryMu.Unlock()
	if state.Mtime.IsZero() {
		state.Mtime = time.Now()
	}
	agentBinaryStates[osName] = state
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

func useTestResolvedBinary(t *testing.T, osName, path string) {
	t.Helper()
	oldResolver := resolveAgentBinaryFor
	resolveAgentBinaryFor = func(requestedOS string) (agentBinaryResolution, error) {
		if normalizeAgentOS(requestedOS) == normalizeAgentOS(osName) {
			return agentBinaryResolution{Path: path, Source: "test:fixture"}, nil
		}
		return oldResolver(requestedOS)
	}
	t.Cleanup(func() { resolveAgentBinaryFor = oldResolver })
}
