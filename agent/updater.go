//go:build linux

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
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

/* ============================================================================
 * Agent self-update (Phase 8)
 *
 * On every connect, the hub sends an `agent_version` frame with the SHA-256
 * of the binary it's currently serving. The agent compares against its own
 * running binary's SHA. If they differ:
 *
 *   0. Refuse outright unless the transport is authenticated and the
 *      announced SHA carries a signature from the key pinned at install
 *      time (see update_transport.go / update_verify.go)
 *   1. Take a snapshot of the current binary as <path>.prev (for rollback)
 *   2. Download the new binary to <path>.new (HTTPS, with hub's CA)
 *   3. Verify the downloaded SHA matches what the hub announced
 *   4. Atomically install: rename <path>.new -> <path>
 *   5. Exit cleanly (code 0). systemd's Restart=always brings us back on
 *      the new binary.
 *
 * On any failure: log the error, keep the old binary, continue running.
 * The hub will re-announce on the next reconnect.
 * ============================================================================ */

var (
	updateMu       sync.Mutex
	updateInFlight bool
)

// AgentVersionMessage is what the hub sends on connect.
type AgentVersionMessage struct {
	Type    string `json:"type"`
	SHA256  string `json:"sha256"`
	Version string `json:"version,omitempty"`
	// Signature is base64 ed25519 over updateSigningMessage(os, sha256),
	// produced by the hub's update signing key. SigAlg names the algorithm
	// so a future rotation can be told apart from a malformed frame.
	Signature string `json:"signature,omitempty"`
	SigAlg    string `json:"sig_alg,omitempty"`
}

// handleAgentVersion is called when the agent receives an "agent_version"
// frame from the hub.
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
		// Already running the announced version, nothing to do.
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

	updateMu.Lock()
	if updateInFlight {
		updateMu.Unlock()
		log.Printf("update: already in flight, skipping duplicate trigger")
		return
	}
	updateInFlight = true
	updateMu.Unlock()

	go func() {
		defer func() {
			updateMu.Lock()
			updateInFlight = false
			updateMu.Unlock()
		}()
		if err := performUpdate(version.SHA256); err != nil {
			log.Printf("update: FAILED — %v (continuing on current version)", err)
		}
	}()
}

// elfMachineForGOARCH maps this build's GOARCH to the ELF e_machine its
// binaries carry: x86-64 = 0x3e, AArch64 = 0xb7.
var elfMachineForGOARCH = map[string]uint16{"amd64": 0x3e, "arm64": 0xb7}

// verifyDownloadedELFArch refuses a downloaded binary whose ELF architecture
// is not this agent's. The hub selects the build by ?arch and (after the
// resolver fix) never serves mislabeled bytes, and the SHA is
// per-architecture, so this is defense in depth: a hub or topology mistake
// that would otherwise install a wrong-CPU binary and crash-loop the service
// under systemd ("Exec format error") becomes a refused update that leaves the
// running agent untouched.
func verifyDownloadedELFArch(path string) error {
	want, ok := elfMachineForGOARCH[runtime.GOARCH]
	if !ok {
		// Fail closed, matching the hub: this agent build is for an
		// architecture the update path does not verify, so do not install.
		return fmt.Errorf("unsupported architecture %q", runtime.GOARCH)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var hdr [20]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return fmt.Errorf("read ELF header: %w", err)
	}
	if hdr[0] != 0x7f || hdr[1] != 'E' || hdr[2] != 'L' || hdr[3] != 'F' {
		return fmt.Errorf("not an ELF binary")
	}
	if hdr[4] != 2 || hdr[5] != 1 || hdr[6] != 1 {
		return fmt.Errorf("not a 64-bit little-endian current-version ELF binary")
	}
	if machine := uint16(hdr[18]) | uint16(hdr[19])<<8; machine != want {
		return fmt.Errorf("binary is for e_machine 0x%02x, not %s (0x%02x)", machine, runtime.GOARCH, want)
	}
	return nil
}

func performUpdate(expectedSHA string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	dir := filepath.Dir(exe)
	newPath := exe + ".new"
	prevPath := exe + ".prev"

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

	if err := verifyDownloadedELFArch(newPath); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("downloaded binary rejected: %w", err)
	}

	if err := os.Chmod(newPath, 0755); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("chmod new binary: %w", err)
	}

	if err := copyFile(exe, prevPath); err != nil {
		log.Printf("update: WARNING failed to snapshot .prev: %v", err)
	} else {
		log.Printf("update: saved previous binary to %s", prevPath)
	}

	// Atomic rename — the kernel keeps the running process's old inode
	// alive even after the directory entry is repointed at the new file.
	if err := os.Rename(newPath, exe); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("install new binary: %w", err)
	}
	log.Printf("update: new binary installed at %s", exe)

	// Marker file lets the rollback script tell whether a recent update
	// happened (so it doesn't roll back to a stale .prev).
	markerPath := filepath.Join(dir, ".bloxos-agent-updated-at")
	now := time.Now().UTC().Format(time.RFC3339)
	if err := os.WriteFile(markerPath, []byte(now), 0644); err != nil {
		log.Printf("update: WARNING failed to write update marker: %v", err)
	}

	log.Printf("update: exiting for systemd restart")
	os.Exit(0)
	return nil // unreachable
}

// downloadAgentBinary fetches /download/agent?os=linux&arch=<GOARCH> from
// the hub URL. Asking for this CPU's architecture explicitly is what keeps a
// hub that serves several from handing an x86_64 host an arm64 build (or the
// reverse), which passes the SHA check the hub announced for it and then
// crash-loops under systemd with "Exec format error".
// Reuses the agent's TLS configuration so the download inherits CA pinning.
// agentDownloadURL refuses plaintext transports, so this is also the
// second line of the transport gate applied in handleAgentVersion.
func downloadAgentBinary(destPath string) error {
	downloadURL, err := agentDownloadURLForArch(hubURL, runtime.GOOS, runtime.GOARCH)
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

// reportAgentVersion sends the agent's running SHA back to the hub.
// Called once per connect after the hardware snapshot.
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
		// The CPU architecture this binary was built for, which is also what
		// downloadAgentBinary requests. The hub announces the SHA for this
		// architecture's build, and withholds when it has none. Its absence
		// is how the hub recognises an agent that downloads without ?arch=.
		"arch": runtime.GOARCH,
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
	log.Printf("update: reported running version %s to hub (os=%s, arch=%s, update_protocol=%d, transport_ok=%v, key_pinned=%v)",
		shortSHA(sha), runtime.GOOS, runtime.GOARCH, agentUpdateProtocol, selfUpdateTransportOK(), updateKeyPinned())
}
