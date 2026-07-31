package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ensureTestUpdateSigningKey gives the test binary a process-lifetime signing
// key. Production does this in main() via initUpdateSigning(); without it
// announceVersionToAgent would correctly refuse to announce anything it
// cannot sign, and every version-rollout test would go quiet.
func ensureTestUpdateSigningKey(t *testing.T) {
	t.Helper()
	updateSigningMu.RLock()
	have := updateSigningKey != nil
	updateSigningMu.RUnlock()
	if have {
		return
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate update signing key: %v", err)
	}
	pub := priv.Public().(ed25519.PublicKey)
	updateSigningMu.Lock()
	updateSigningKey = priv
	updateSigningPub = pub
	updateSigningPubB64 = base64.StdEncoding.EncodeToString(pub)
	updateSigningMu.Unlock()
}

// TestUpdateSigningMessageFormat locks the exact bytes the hub signs. The
// agent asserts the same literal in agent/update_verify_test.go — if either
// side is edited without the other, one of the two tests fails instead of the
// fleet silently losing update authenticity.
func TestUpdateSigningMessageFormat(t *testing.T) {
	got := string(updateSigningMessage("linux", "abc123"))
	want := "bloxos-agent-update:v1:linux:abc123"
	if got != want {
		t.Fatalf("updateSigningMessage = %q, want %q", got, want)
	}
	if string(updateSigningMessage(" LINUX ", " ABC123 ")) != want {
		t.Fatalf("updateSigningMessage is not normalising case/whitespace")
	}
}

// The signature the hub produces must be the one the agent verifies. This is
// the agent's verification, inlined, so a change to either side is caught.
func TestSignAgentReleaseVerifiesWithAnnouncedPublicKey(t *testing.T) {
	ensureTestUpdateSigningKey(t)

	sha := strings.Repeat("ab", 32)
	sigB64 := signAgentRelease("linux", sha)
	if sigB64 == "" {
		t.Fatal("signAgentRelease returned no signature")
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		t.Fatalf("signature is not base64: %v", err)
	}

	pubRaw, err := base64.StdEncoding.DecodeString(updateSigningPublicKeyBase64())
	if err != nil {
		t.Fatalf("announced public key is not base64: %v", err)
	}
	pub := ed25519.PublicKey(pubRaw)

	if !ed25519.Verify(pub, updateSigningMessage("linux", sha), sig) {
		t.Fatal("signature does not verify against the public key the installer pins")
	}
	// Same guarantee the agent relies on: the OS is bound into the message.
	if ed25519.Verify(pub, updateSigningMessage("windows", sha), sig) {
		t.Fatal("a linux signature verified as a windows announcement")
	}
}

func TestDecodeUpdatePrivateKeyAcceptsSeedAndFullKey(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	full := base64.StdEncoding.EncodeToString(priv)
	seed := base64.StdEncoding.EncodeToString(priv.Seed())

	for name, body := range map[string]string{
		"full":            full,
		"seed":            seed,
		"with comment":    "# bloxos update signing key\n" + full + "\n",
		"trailing spaces": "  " + seed + "  \r\n",
	} {
		got, err := decodeUpdatePrivateKey([]byte(body))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !got.Equal(priv) {
			t.Errorf("%s: decoded a different key", name)
		}
	}

	for name, body := range map[string]string{
		"empty":        "",
		"comment":      "# nothing here\n",
		"not base64":   "!!!!",
		"wrong length": base64.StdEncoding.EncodeToString([]byte("short")),
	} {
		if _, err := decodeUpdatePrivateKey([]byte(body)); err == nil {
			t.Errorf("%s: decodeUpdatePrivateKey succeeded, want error", name)
		}
	}
}

// A detached <binary>.sig lets the private key stay off the hub entirely.
// A stale one must be dropped rather than announced — announcing it would
// make every agent reject the update with a confusing error.
func TestDetachedSignatureFor(t *testing.T) {
	ensureTestUpdateSigningKey(t)

	updateSigningMu.RLock()
	priv := updateSigningKey
	updateSigningMu.RUnlock()

	dir := t.TempDir()
	bin := filepath.Join(dir, "bloxos-agent")
	if err := os.WriteFile(bin, []byte("binary"), 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	sha := strings.Repeat("cd", 32)

	writeSig := func(body string) {
		if err := os.WriteFile(bin+".sig", []byte(body), 0o644); err != nil {
			t.Fatalf("write sig: %v", err)
		}
	}

	// Absent.
	if got := detachedSignatureFor(bin, "linux", sha); got != "" {
		t.Fatalf("no .sig on disk but got %q", got)
	}

	// Valid.
	valid := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, updateSigningMessage("linux", sha)))
	writeSig(valid + "\n")
	if got := detachedSignatureFor(bin, "linux", sha); got != valid {
		t.Fatalf("valid .sig not returned: got %q", got)
	}

	// Stale — signed for a different SHA.
	stale := base64.StdEncoding.EncodeToString(
		ed25519.Sign(priv, updateSigningMessage("linux", strings.Repeat("ef", 32))))
	writeSig(stale)
	if got := detachedSignatureFor(bin, "linux", sha); got != "" {
		t.Fatalf("stale .sig announced: %q", got)
	}

	// Malformed.
	writeSig("not base64 at all")
	if got := detachedSignatureFor(bin, "linux", sha); got != "" {
		t.Fatalf("malformed .sig announced: %q", got)
	}

	if got := detachedSignatureFor("", "linux", sha); got != "" {
		t.Fatalf("empty binary path returned %q", got)
	}
}

// The generated install.sh must pin the hub's update signing key on disk —
// without it the agent has nothing to verify an announcement against and
// self-update stays disabled.
func TestInstallScriptPinsUpdateSigningKey(t *testing.T) {
	e := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/install.sh", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()

	pub := updateSigningPublicKeyBase64()
	if pub == "" {
		t.Fatal("test server has no update signing key")
	}
	if !strings.Contains(body, pub) {
		t.Fatal("install.sh does not embed the hub's update signing public key")
	}
	if !strings.Contains(body, "/etc/bloxos/agent-update.pub") {
		t.Fatal("install.sh does not pin the key to /etc/bloxos/agent-update.pub")
	}
	if strings.Contains(body, "__UPDATE_PUBKEY__") {
		t.Fatal("install.sh left the __UPDATE_PUBKEY__ placeholder unsubstituted")
	}
}

func TestWindowsInstallScriptPinsUpdateSigningKey(t *testing.T) {
	e := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/install.ps1", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()

	pub := updateSigningPublicKeyBase64()
	if !strings.Contains(body, pub) {
		t.Fatal("install.ps1 does not embed the hub's update signing public key")
	}
	if !strings.Contains(body, `Join-Path $InstallDir "agent-update.pub"`) {
		t.Fatal("install.ps1 does not pin the key beside the agent executable")
	}
	if strings.Contains(body, "__UPDATE_PUBKEY__") {
		t.Fatal("install.ps1 left the __UPDATE_PUBKEY__ placeholder unsubstituted")
	}
}

// TestInstallScriptStartLimitLivesInUnitSection keeps the start-limit
// settings in the location systemd documents: [Unit], spelled
// StartLimitIntervalSec=. The old [Service] StartLimitInterval= spelling is a
// deprecated compat alias — measured on systemd 255, both shapes reach
// "failed" after 3 restarts and fire OnFailure=, so this is about staying on
// the documented key, not about repairing a broken rollback.
func TestInstallScriptStartLimitLivesInUnitSection(t *testing.T) {
	e := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/install.sh", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	unit := extractAgentUnit(t, rec.Body.String())

	unitSection, serviceSection := splitUnitSections(t, unit)

	if !strings.Contains(unitSection, "StartLimitIntervalSec=60") {
		t.Errorf("[Unit] is missing StartLimitIntervalSec=60:\n%s", unitSection)
	}
	if !strings.Contains(unitSection, "StartLimitBurst=3") {
		t.Errorf("[Unit] is missing StartLimitBurst=3:\n%s", unitSection)
	}
	if !strings.Contains(unitSection, "OnFailure=bloxos-agent-recover.service") {
		t.Errorf("[Unit] is missing OnFailure=:\n%s", unitSection)
	}
	if strings.Contains(serviceSection, "StartLimit") {
		t.Errorf("[Service] still carries a StartLimit key, where systemd ignores it:\n%s", serviceSection)
	}
	// The deprecated spelling must not come back as a real directive
	// (comments are allowed to mention it).
	for _, line := range strings.Split(unit, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "StartLimitInterval=") {
			t.Errorf("unit uses the deprecated StartLimitInterval= spelling: %q", line)
		}
	}
}

// extractAgentUnit pulls the bloxos-agent.service heredoc body out of the
// generated install.sh.
func extractAgentUnit(t *testing.T, script string) string {
	t.Helper()
	const start = "[Unit]\nDescription=BloxOS Agent\n"
	i := strings.Index(script, start)
	if i < 0 {
		t.Fatal("install.sh does not contain a bloxos-agent unit")
	}
	rest := script[i:]
	j := strings.Index(rest, "SVCEOF")
	if j < 0 {
		t.Fatal("bloxos-agent unit heredoc is unterminated")
	}
	return rest[:j]
}

// splitUnitSections returns the [Unit] and [Service] bodies of a unit file.
func splitUnitSections(t *testing.T, unit string) (string, string) {
	t.Helper()
	svc := strings.Index(unit, "\n[Service]\n")
	if svc < 0 {
		t.Fatalf("unit has no [Service] section:\n%s", unit)
	}
	unitSection := unit[:svc]
	rest := unit[svc+len("\n[Service]\n"):]
	if inst := strings.Index(rest, "\n[Install]\n"); inst >= 0 {
		rest = rest[:inst]
	}
	return unitSection, rest
}

// A hub with no signing material must not announce a version at all: the
// agent would refuse it, but the announce arms a 90s reconnect-expectation
// timer whose expiry counts toward the rollout circuit breaker.
func TestNoSignatureMeansNoAnnounce(t *testing.T) {
	setupTestServer(t)

	updateSigningMu.Lock()
	prevKey, prevPub, prevB64 := updateSigningKey, updateSigningPub, updateSigningPubB64
	updateSigningKey, updateSigningPub, updateSigningPubB64 = nil, nil, ""
	updateSigningMu.Unlock()
	t.Cleanup(func() {
		updateSigningMu.Lock()
		updateSigningKey, updateSigningPub, updateSigningPubB64 = prevKey, prevPub, prevB64
		updateSigningMu.Unlock()
	})

	dir := t.TempDir()
	bin := filepath.Join(dir, "bloxos-agent")
	content := []byte("fake-linux-agent-binary-unsigned")
	if err := os.WriteFile(bin, content, 0o755); err != nil {
		t.Fatalf("write agent binary: %v", err)
	}
	t.Setenv("BLOXOS_AGENT_BINARY", bin)

	hubAgentBinaryMu.Lock()
	prevSHA, prevMtime := hubAgentBinarySHA, hubAgentBinaryMtime
	hubAgentBinarySHA, hubAgentBinaryMtime = "", time.Time{}
	hubAgentBinaryMu.Unlock()
	t.Cleanup(func() {
		hubAgentBinaryMu.Lock()
		hubAgentBinarySHA, hubAgentBinaryMtime = prevSHA, prevMtime
		hubAgentBinaryMu.Unlock()
	})
	recomputeAgentBinarySHA()

	sum := sha256.Sum256(content)
	sha := hex.EncodeToString(sum[:])
	if announcedSHAFor("linux") != sha {
		t.Fatalf("test setup: announcedSHAFor(linux) = %q, want %q", announcedSHAFor("linux"), sha)
	}
	if got := announcedSignatureFor("linux", sha); got != "" {
		t.Fatalf("announcedSignatureFor returned %q with no signing key", got)
	}
}
