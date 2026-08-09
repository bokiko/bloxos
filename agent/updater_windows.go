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
 *   4. Agent exits with failure (code 1) so SCM restarts us per recovery actions.
 *   5. NEW process boot: applyPendingUpdate() runs BEFORE the SCM main
 *      loop. If <exe>.pending + <exe>.new exist, it reads the marker, verifies
 *      the signature and SHA, and spawns a detached batch helper that:
 *      sleeps, moves .new -> target over the running exe, removes marker,
 *      restarts the service, and self-deletes.
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
	// Signature is base64 ed25519 over updateSigningMessage(os, sha256),
	// produced by the hub's update signing key.
	Signature string `json:"signature,omitempty"`
	SigAlg    string `json:"sig_alg,omitempty"`
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

	// Gate before anything is downloaded or snapshotted. A refusal here
	// leaves the agent untouched and running.
	exe, err := selfExePath()
	if err != nil {
		log.Printf("update: cannot resolve own path: %v", err)
		return
	}
	if err := authorizeUpdate(hubURL, exe, runtime.GOOS, version.SHA256, version.Signature); err != nil {
		log.Printf("update: REFUSED — %v", err)
		return
	}

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
		if err := performUpdateWindows(version.SHA256, version.Signature); err != nil {
			log.Printf("update: FAILED — %v (continuing on current version)", err)
		}
	}()
}

// performUpdateWindows downloads the new binary, verifies its SHA, writes a
// marker file, and exits. SCM will restart us; applyPendingUpdate runs on
// boot and arranges the actual swap via a helper batch script.
func performUpdateWindows(expectedSHA, signature string) error {
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
	marker := fmt.Sprintf("source=%s\ntarget=%s\nat=%s\nsha256=%s\nsignature=%s\n", newPath, exe, now, expectedSHA, signature)
	if err := os.WriteFile(markerPath, []byte(marker), 0644); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("write pending marker: %w", err)
	}

	log.Printf("update: marker written, exiting for SCM restart + helper to apply swap")
	os.Exit(1)
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

	if err := validatePendingUpdate(exe, markerPath, newPath); err != nil {
		log.Printf("update: %v, cleaning up", err)
		os.Remove(markerPath)
		os.Remove(newPath)
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

// validatePendingUpdate reads and validates the marker, hashes the staged .new file,
// and calls the real signature-verification mechanism. Returns an error if any step fails.
func validatePendingUpdate(exe, markerPath, newPath string) error {
	pm, err := parsePendingMarker(markerPath)
	if err != nil {
		return fmt.Errorf("could not read .pending marker: %w", err)
	}

	if pm.ExpectedSHA == "" || pm.Signature == "" {
		return fmt.Errorf("invalid .pending marker (missing sha256 or signature)")
	}

	downloadedSHA, err := computeFileSHA256(newPath)
	if err != nil {
		return fmt.Errorf("failed to compute SHA of .new binary: %w", err)
	}

	if !strings.EqualFold(downloadedSHA, pm.ExpectedSHA) {
		return fmt.Errorf("SHA mismatch for staged binary (expected %s, got %s)", shortSHA(pm.ExpectedSHA), shortSHA(downloadedSHA))
	}

	if err := verifyAnnouncedRelease(exe, "windows", downloadedSHA, pm.Signature); err != nil {
		return fmt.Errorf("staged binary failed signature verification: %w", err)
	}

	return nil
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
		"move /Y \"" + newBin + "\" \"" + target + "\" > nul\r\n" +
		"del \"" + marker + "\" 2> nul\r\n" +
		"sc.exe start " + windowsServiceName + " > nul\r\n" +
		"del \"%~f0\" 2> nul\r\n"
}

// downloadAgentBinary fetches /download/agent?os=windows from the hub and
// writes it to destPath. Reuses the agent's TLS configuration so the
// download inherits CA pinning. agentDownloadURL refuses plaintext
// transports, so this is also the second line of the transport gate applied
// in handleAgentVersion.
func downloadAgentBinary(destPath string) error {
	downloadURL, err := agentDownloadURL(hubURL, "windows")
	if err != nil {
		return err
	}

	dialer, err := websocketDialerFor(downloadURL)
	if err != nil {
		return fmt.Errorf("build TLS config: %w", err)
	}
	transport := &http.Transport{
		TLSClientConfig: dialer.TLSClientConfig,
	}
	client := &http.Client{
		Transport:     transport,
		Timeout:       2 * time.Minute,
		CheckRedirect: refuseRedirects,
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
	msg := map[string]interface{}{
		"type":   "agent_running_version",
		"sha256": sha,
		"os":     runtime.GOOS,
		// Tells the hub this binary verifies signed announcements. Its
		// absence is how the hub recognises a pre-signature agent.
		"update_protocol": agentUpdateProtocol,
		// Whether this agent's own hub URL permits self-update. Saves the
		// hub from inferring it from PUBLIC_URL, which is wrong for any
		// agent configured differently from the fleet default.
		"update_transport_ok": selfUpdateTransportOK(),
		// Whether this agent actually has a usable pinned key on disk.
		// Signature-capable and transport-OK are not enough on their own —
		// see updateKeyPinned's doc comment for why the hub needs this to
		// avoid announcing to an agent that is certain to refuse.
		"update_key_pinned": updateKeyPinned(),
	}
	if err := writeJSON(conn, mu, msg); err != nil {
		log.Printf("update: failed to report version: %v", err)
		return
	}
	log.Printf("update: reported running version %s to hub (os=%s, update_protocol=%d, transport_ok=%v, key_pinned=%v)",
		shortSHA(sha), runtime.GOOS, agentUpdateProtocol, selfUpdateTransportOK(), updateKeyPinned())
}
