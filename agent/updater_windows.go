//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

/* ============================================================================
 * Phase 9 — Windows agent self-update
 *
 * The Linux flow is "atomic rename of running binary while we exit and
 * systemd restarts us" — the kernel keeps the old inode alive for the
 * outgoing process. Windows refuses to allow rename of a running .exe,
 * so we use a marker-file flow:
 *
 *   1. Hub announces a new SHA via agent_version frame.
 *   2. Agent downloads to <exe>.new, verifies SHA.
 *   3. Agent writes a marker file <exe>.pending describing the swap.
 *   4. Agent exits cleanly. SCM restarts us per recovery actions.
 *   5. NEW process boot: applyPendingUpdate() runs BEFORE the SCM main
 *      loop. If <exe>.pending + <exe>.new exist, spawn a detached batch
 *      helper that: sleeps, deletes the running exe, renames .new ->
 *      target, removes marker, restarts the service, and self-deletes.
 *   6. The newly-restarted service boots from the new binary cleanly.
 *
 * Why a batch helper instead of doing the work in-process: by the time
 * the parent process is still alive, the .exe is locked. We need a
 * helper that lives outside the lock to do the rename. Batch is the
 * simplest thing that works on every Windows SKU without dependencies.
 * ============================================================================ */

var (
	updateMuWindows       sync.Mutex
	updateInFlightWindows bool
)

// AgentVersionMessage — duplicate of the Linux struct (build-tag separation
// keeps them distinct compilation units).
type AgentVersionMessage struct {
	Type    string `json:"type"`
	SHA256  string `json:"sha256"`
	Version string `json:"version,omitempty"`
}

// handleAgentVersion — Windows version. Same flow as Linux's
// handleAgentVersion but ends with a marker file + os.Exit instead of
// in-place rename.
func handleAgentVersion(msg []byte) {
	var version AgentVersionMessage
	if err := json.Unmarshal(msg, &version); err != nil {
		log.Printf("update: invalid agent_version message: %v", err)
		return
	}
	if version.SHA256 == "" {
		log.Printf("update: hub announced empty SHA256, ignoring")
		return
	}

	currentSHA, err := computeSelfSHA256()
	if err != nil {
		log.Printf("update: cannot compute self SHA256: %v", err)
		return
	}

	if strings.EqualFold(currentSHA, version.SHA256) {
		return
	}

	log.Printf("update: hub announced %s (running %s), starting update",
		shortSHA(version.SHA256), shortSHA(currentSHA))

	updateMuWindows.Lock()
	if updateInFlightWindows {
		updateMuWindows.Unlock()
		log.Printf("update: already in flight, skipping duplicate trigger")
		return
	}
	updateInFlightWindows = true
	updateMuWindows.Unlock()

	go func() {
		defer func() {
			updateMuWindows.Lock()
			updateInFlightWindows = false
			updateMuWindows.Unlock()
		}()
		if err := performUpdateWindows(version.SHA256); err != nil {
			log.Printf("update: FAILED — %v (continuing on current version)", err)
		}
	}()
}

// performUpdateWindows downloads the new binary, verifies its SHA, writes a
// marker file, and exits. SCM will restart us; applyPendingUpdate runs on
// boot and arranges the actual swap via a helper batch script.
func performUpdateWindows(expectedSHA string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	newPath := exe + ".new"
	prevPath := exe + ".prev"
	markerPath := exe + ".pending"

	log.Printf("update: downloading new binary from hub")
	if err := downloadAgentBinary(newPath); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("download: %w", err)
	}

	downloadedSHA, err := computeFileSHA256(newPath)
	if err != nil {
		os.Remove(newPath)
		return fmt.Errorf("verify SHA: %w", err)
	}
	if !strings.EqualFold(downloadedSHA, expectedSHA) {
		os.Remove(newPath)
		return fmt.Errorf("SHA mismatch: announced %s, downloaded %s",
			shortSHA(expectedSHA), shortSHA(downloadedSHA))
	}
	log.Printf("update: SHA verified (%s)", shortSHA(downloadedSHA))

	// Snapshot current running exe to .prev before the swap so the helper
	// (or a future Windows recovery script) can roll back.
	if err := copyFile(exe, prevPath); err != nil {
		log.Printf("update: WARNING failed to snapshot .prev: %v", err)
	} else {
		log.Printf("update: saved previous binary to %s", prevPath)
	}

	// Write the pending marker so applyPendingUpdate, on next boot,
	// picks up the swap.
	now := time.Now().UTC().Format(time.RFC3339)
	marker := fmt.Sprintf("source=%s\ntarget=%s\nat=%s\n", newPath, exe, now)
	if err := os.WriteFile(markerPath, []byte(marker), 0644); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("write pending marker: %w", err)
	}

	log.Printf("update: marker written, exiting for SCM restart + helper to apply swap")
	os.Exit(0)
	return nil // unreachable
}

// applyPendingUpdate is called on agent boot, BEFORE the SCM main loop.
// If a pending marker exists alongside an <exe>.new, spawn a detached
// batch helper that performs the rename after we exit. We then exit so
// the helper can take the lock.
//
// PHASE9-NOTE: This flow is intentionally convoluted. The agent that's
// currently running CANNOT rename itself on Windows — the OS holds an
// open handle on the running .exe. So we hand off to a tiny external
// process (cmd.exe running a one-shot .bat) which sleeps long enough
// for us to fully exit, then performs the swap and restarts the
// service. Per the spec: do not simplify; if it looks wrong, leave a
// note instead.
func applyPendingUpdate() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return
	}

	markerPath := exe + ".pending"
	newPath := exe + ".new"

	if _, err := os.Stat(markerPath); err != nil {
		return // no pending update
	}
	if _, err := os.Stat(newPath); err != nil {
		// Marker present but new binary missing — clean up the stale marker.
		log.Printf("update: stale .pending marker (no .new binary), cleaning up")
		os.Remove(markerPath)
		return
	}

	log.Printf("update: applying pending update via batch helper")

	dir := filepath.Dir(exe)
	helperPath := filepath.Join(dir, "bloxos-agent-update-helper.bat")

	helper := buildHelperBatch(exe, newPath, markerPath)
	if err := os.WriteFile(helperPath, []byte(helper), 0755); err != nil {
		log.Printf("update: cannot write helper script: %v", err)
		return
	}

	// Spawn detached: cmd.exe /C start /B <helper.bat>. We don't wait;
	// the helper sleeps before touching anything, by which time we've
	// exited and our handle on the .exe is gone.
	if err := spawnDetachedHelper(helperPath); err != nil {
		log.Printf("update: cannot spawn helper: %v", err)
		return
	}

	log.Printf("update: helper spawned, exiting so SCM can restart on new binary")
	os.Exit(0)
}

// buildHelperBatch returns the contents of the .bat we leave on disk.
// Format mirrors the spec.
func buildHelperBatch(target, newBin, marker string) string {
	// Use Windows path separators in the batch file.
	target = filepath.FromSlash(target)
	newBin = filepath.FromSlash(newBin)
	marker = filepath.FromSlash(marker)
	return "@echo off\r\n" +
		"timeout /t 3 /nobreak > nul\r\n" +
		"del \"" + target + "\" 2> nul\r\n" +
		"move /Y \"" + newBin + "\" \"" + target + "\" > nul\r\n" +
		"del \"" + marker + "\" 2> nul\r\n" +
		"sc.exe start " + windowsServiceName + " > nul\r\n" +
		"del \"%~f0\" 2> nul\r\n"
}

// downloadAgentBinary fetches /download/agent?os=windows from the hub and
// writes it to destPath. Reuses the agent's TLS configuration so the
// download inherits CA pinning.
func downloadAgentBinary(destPath string) error {
	wsURL, err := url.Parse(hubURL)
	if err != nil {
		return fmt.Errorf("parse hub URL: %w", err)
	}
	httpScheme := "http"
	if wsURL.Scheme == "wss" {
		httpScheme = "https"
	}
	downloadURL := fmt.Sprintf("%s://%s/download/agent?os=windows", httpScheme, wsURL.Host)

	dialer, err := websocketDialerFor(downloadURL)
	if err != nil {
		return fmt.Errorf("build TLS config: %w", err)
	}
	transport := &http.Transport{
		TLSClientConfig: dialer.TLSClientConfig,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   2 * time.Minute,
	}

	req, err := http.NewRequest("GET", downloadURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer out.Close()

	if err := downloadWithLimit(out, resp.Body, maxAgentBinarySize); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}

// computeSelfSHA256 / computeFileSHA256 / copyFile / shortSHA — duplicates
// of their Linux counterparts. Build tags keep them in distinct
// compilation units.
func computeSelfSHA256() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	return computeFileSHA256(exe)
}

func computeFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

func shortSHA(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[:12]
}

// spawnDetachedHelper kicks off `cmd.exe /C start /B "" <helper.bat>`
// without inheriting our handles. Using start /B detaches it from the
// console; a hidden window flag also keeps services.msc clean.
func spawnDetachedHelper(helperPath string) error {
	cmd := exec.Command("cmd.exe", "/C", "start", "/B", "", helperPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x00000008 | 0x00000200, // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	}
	return cmd.Start()
}

// reportAgentVersion sends the agent's running SHA back to the hub. On
// Windows we additionally include "os":"windows" so the hub can route
// the right per-OS announce SHA on the next reconnect.
func reportAgentVersion(conn *websocket.Conn, mu *sync.Mutex) {
	sha, err := computeSelfSHA256()
	if err != nil {
		log.Printf("update: failed to compute self SHA for reporting: %v", err)
		return
	}
	msg := map[string]string{
		"type":   "agent_running_version",
		"sha256": sha,
		"os":     runtime.GOOS,
	}
	if err := writeJSON(conn, mu, msg); err != nil {
		log.Printf("update: failed to report version: %v", err)
		return
	}
	log.Printf("update: reported running version %s to hub (os=%s)", shortSHA(sha), runtime.GOOS)
}
