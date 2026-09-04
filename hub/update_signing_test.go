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
	"sync"
	"testing"
	"time"

	"github.com/bokiko/bloxos/proto/updatesigning"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

func writeOfflineSigningKey(t *testing.T, priv ed25519.PrivateKey) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "update-signing.key")
	body := base64.StdEncoding.EncodeToString(priv) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}
	return path
}

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

func TestOfflineSigningToolRoundTripDetachedSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bloxos-agent")
	if err := os.WriteFile(bin, []byte("offline-signed agent"), 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	result, err := updatesigning.SignFile(bin, "linux", writeOfflineSigningKey(t, priv))
	if err != nil {
		t.Fatalf("sign file: %v", err)
	}

	updateSigningMu.Lock()
	oldPub := updateSigningPub
	updateSigningPub = pub
	updateSigningMu.Unlock()
	t.Cleanup(func() {
		updateSigningMu.Lock()
		updateSigningPub = oldPub
		updateSigningMu.Unlock()
	})

	if got := detachedSignatureFor(bin, "linux", result.SHA256); got != result.Signature {
		t.Fatalf("detachedSignatureFor = %q, want tool signature %q", got, result.Signature)
	}
	if got := detachedSignatureFor(bin, "windows", result.SHA256); got != "" {
		t.Fatalf("linux signature accepted for windows: %q", got)
	}
	if got := detachedSignatureFor(bin, "linux", strings.Repeat("ab", 32)); got != "" {
		t.Fatalf("signature accepted for wrong SHA: %q", got)
	}
}

func TestDetachedSignatureForFailsClosedWithoutPublicKey(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bloxos-agent")
	if err := os.WriteFile(bin, []byte("binary"), 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	sha := strings.Repeat("cd", 32)
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, updateSigningMessage("linux", sha)))
	if err := os.WriteFile(bin+".sig", []byte(sig+"\n"), 0o644); err != nil {
		t.Fatalf("write signature: %v", err)
	}

	updateSigningMu.Lock()
	oldPub := updateSigningPub
	updateSigningPub = nil
	updateSigningMu.Unlock()
	t.Cleanup(func() {
		updateSigningMu.Lock()
		updateSigningPub = oldPub
		updateSigningMu.Unlock()
	})

	if got := detachedSignatureFor(bin, "linux", sha); got != "" {
		t.Fatalf("detached signature returned without a public key: %q", got)
	}
}

// The generated install.sh must pin the hub's update signing key on disk —
// without it the agent has nothing to verify an announcement against and
// self-update stays disabled.
func TestInstallScriptPinsUpdateSigningKey(t *testing.T) {
	e, _ := setupTestServer(t)
	useGeneratedTestBinary(t, "linux")

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
	e, _ := setupTestServer(t)
	useGeneratedTestBinary(t, "windows")

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
	e, _ := setupTestServer(t)
	useGeneratedTestBinary(t, "linux")

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

// A hub with no signing material must not produce a signature at all: the
// agent would refuse an unsigned announcement, and announcing anyway arms a
// 90s reconnect-expectation timer whose expiry counts toward the rollout
// circuit breaker.
//
// This asserts announcedSignatureFor only. The end-to-end "hub sends nothing
// on the wire" claim is TestNoSignatureMeansNoAnnounceOnTheWire below, which
// drives a real agent WebSocket.
func TestNoSignatureAvailableWithoutSigningKey(t *testing.T) {
	_, _ = setupTestServer(t)
	withoutSigningKey(t)

	dir := t.TempDir()
	bin := filepath.Join(dir, "bloxos-agent")
	content := []byte("fake-linux-agent-binary-unsigned")
	if err := os.WriteFile(bin, content, 0o755); err != nil {
		t.Fatalf("write agent binary: %v", err)
	}
	t.Setenv("BLOXOS_AGENT_BINARY", bin)

	stageLinuxBinarySHA(t)

	sum := sha256.Sum256(content)
	sha := hex.EncodeToString(sum[:])
	if announcedSHAFor("linux") != sha {
		t.Fatalf("test setup: announcedSHAFor(linux) = %q, want %q", announcedSHAFor("linux"), sha)
	}
	if got := announcedSignatureFor("linux", sha); got != "" {
		t.Fatalf("announcedSignatureFor returned %q with no signing key", got)
	}
}

// withoutSigningKey strips the hub's signing material for the duration of a test.
func withoutSigningKey(t *testing.T) {
	t.Helper()
	updateSigningMu.Lock()
	prevKey, prevPub, prevB64 := updateSigningKey, updateSigningPub, updateSigningPubB64
	updateSigningKey, updateSigningPub, updateSigningPubB64 = nil, nil, ""
	updateSigningMu.Unlock()
	t.Cleanup(func() {
		updateSigningMu.Lock()
		updateSigningKey, updateSigningPub, updateSigningPubB64 = prevKey, prevPub, prevB64
		updateSigningMu.Unlock()
	})
}

// stageLinuxBinarySHA clears the cached Linux state so recompute hashes the
// test fixture selected by BLOXOS_AGENT_BINARY.
func stageLinuxBinarySHA(t *testing.T) {
	t.Helper()
	path := os.Getenv("BLOXOS_AGENT_BINARY")
	withAgentBinaryState(t)
	useTestResolvedBinary(t, "linux", path)
	recomputeBinaryFor("linux")
}

/* ----------------------------------------------------------------------------
 * Availability: a file the hub wrote itself must not be able to brick the
 * fleet or the hub.
 * -------------------------------------------------------------------------- */

// An unreadable-but-present key must NOT produce a replacement. Every agent in
// the fleet is pinned to the existing key; minting a new one here is how a
// fleet goes dark with no recovery short of re-running the installer on every
// host. This is the test Master asked for: a non-ENOENT read error produces no
// new key.
func TestSigningKeyNotRegeneratedOnNonENOENTReadError(t *testing.T) {
	dir := t.TempDir()
	// A directory where the key file should be: os.ReadFile returns EISDIR,
	// which is emphatically not "no key has ever existed here".
	keyPath := filepath.Join(dir, "update-signing.key")
	if err := os.Mkdir(keyPath, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("BLOXOS_UPDATE_SIGNING_KEY", "")
	t.Setenv("HOME", dir)
	// os.UserHomeDir on the default path resolves $HOME, so point the default
	// straight at the trap.
	t.Setenv("BLOXOS_UPDATE_SIGNING_KEY", keyPath)

	priv, source, reason := loadOrGenerateUpdateSigningKey()
	if priv != nil {
		t.Fatalf("minted a new signing key after a non-ENOENT read error (source=%q) — "+
			"every pinned agent in the fleet would start rejecting updates", source)
	}
	if !strings.Contains(reason, "no replacement was generated") {
		t.Fatalf("disabled reason = %q, want it to say no replacement was generated", reason)
	}
	// And nothing was written over the path.
	info, err := os.Stat(keyPath)
	if err != nil || !info.IsDir() {
		t.Fatalf("key path was modified: stat err=%v", err)
	}
}

// A corrupt key file disables signing. It must not take the hub down with it —
// the hub does far more than sign agent updates.
func TestCorruptSigningKeyDisablesSigningWithoutKillingHub(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "update-signing.key")
	if err := os.WriteFile(keyPath, []byte("not a key at all\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("BLOXOS_UPDATE_SIGNING_KEY", keyPath)

	priv, _, reason := loadOrGenerateUpdateSigningKey()
	if priv != nil {
		t.Fatal("a corrupt key file yielded a usable key")
	}
	// The reason has to be retrievable, not just logged: a hub that cannot
	// sign looks exactly like a rollout in progress on the dashboard.
	if !strings.Contains(reason, "corrupt") {
		t.Fatalf("disabled reason = %q, want it to name the corrupt key", reason)
	}
	// Reaching this line at all is the assertion that matters: the previous
	// implementation called log.Fatalf here, which would have taken the test
	// binary — and in production the whole hub — down.
}

// An explicitly named key file that does not exist is a misconfiguration, not
// an invitation to invent one at the operator's chosen path.
func TestExplicitSigningKeyPathMissingDoesNotGenerate(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "nope", "update-signing.key")
	t.Setenv("BLOXOS_UPDATE_SIGNING_KEY", keyPath)

	priv, _, reason := loadOrGenerateUpdateSigningKey()
	if priv != nil {
		t.Fatal("generated a key at an explicitly configured path that did not exist")
	}
	if !strings.Contains(reason, keyPath) {
		t.Fatalf("disabled reason = %q, want it to name %s", reason, keyPath)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("something was written to %s: %v", keyPath, err)
	}
}

// Genuine first boot — no key has ever existed — is the one case that mints
// one, and only if it can be persisted.
func TestSigningKeyGeneratedOnFirstBoot(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "sub", "update-signing.key")
	t.Setenv("BLOXOS_UPDATE_SIGNING_KEY", "")
	t.Setenv("HOME", filepath.Dir(filepath.Dir(keyPath)))

	priv, source, _ := loadOrGenerateUpdateSigningKey()
	if priv == nil {
		t.Fatalf("first boot did not generate a key (source=%q)", source)
	}
	// It must be on disk, or the next restart mints a different one and every
	// agent installed against the first is orphaned.
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("generated key was not persisted at %s: %v", source, err)
	}
	reloaded, err := decodeUpdatePrivateKey(data)
	if err != nil {
		t.Fatalf("persisted key does not decode: %v", err)
	}
	if !reloaded.Equal(priv) {
		t.Fatal("persisted key differs from the one returned")
	}

	// Second call must load the same key, not mint another.
	again, _, _ := loadOrGenerateUpdateSigningKey()
	if again == nil || !again.Equal(priv) {
		t.Fatal("a second load produced a different key")
	}
}

// The env var name collision: the hub reads BLOXOS_UPDATE_PUBKEY as a base64
// key VALUE, the agent reads BLOXOS_UPDATE_PUBKEY_PATH as a filesystem PATH.
// On a single-box deployment both binaries share one environment, so the two
// meanings must not share one name.
func TestUpdatePubkeyEnvNamesDoNotCollide(t *testing.T) {
	src, err := os.ReadFile("update_signing.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if strings.Contains(string(src), "BLOXOS_UPDATE_PUBKEY_PATH") {
		t.Fatal("hub reads BLOXOS_UPDATE_PUBKEY_PATH; that name belongs to the agent's key file path")
	}
	if !strings.Contains(string(src), `os.Getenv("BLOXOS_UPDATE_PUBKEY")`) {
		t.Fatal("hub no longer reads BLOXOS_UPDATE_PUBKEY as the key value")
	}
}

/* ----------------------------------------------------------------------------
 * Legacy-fleet policy
 * -------------------------------------------------------------------------- */

func TestUpdateTransportUsable(t *testing.T) {
	allowed := []string{
		"https://hub.example.com",
		"https://hub.example.com:4000",
		"HTTPS://Hub.Example.com",
		"http://localhost:4000",
		"http://127.0.0.1:4000",
		"http://[::1]:4000",
	}
	for _, u := range allowed {
		t.Setenv("PUBLIC_URL", u)
		if ok, why := updateTransportUsable(); !ok {
			t.Errorf("PUBLIC_URL=%q should permit legacy bootstrap, refused: %s", u, why)
		}
	}

	refused := []string{
		"",
		"http://hub.example.com",
		"http://192.168.16.234:4000",
		"http://localhost.attacker.example:4000",
		"ftp://hub.example.com",
		"not a url at all",
	}
	for _, u := range refused {
		t.Setenv("PUBLIC_URL", u)
		if ok, _ := updateTransportUsable(); ok {
			t.Errorf("PUBLIC_URL=%q should refuse legacy bootstrap", u)
		}
	}
}

func TestSignatureCapable(t *testing.T) {
	if (agentVersionInfo{UpdateProtocol: 0}).signatureCapable() {
		t.Error("update_protocol=0 was treated as signature-capable")
	}
	if !(agentVersionInfo{UpdateProtocol: minSignatureCapableProtocol}).signatureCapable() {
		t.Errorf("update_protocol=%d was not treated as signature-capable", minSignatureCapableProtocol)
	}
	if !(agentVersionInfo{UpdateTransportOK: true}).transportPermitsUpdate() {
		t.Error("update_transport_ok=true was not treated as permitting update")
	}
	if (agentVersionInfo{UpdateTransportOK: false}).transportPermitsUpdate() {
		t.Error("update_transport_ok=false was treated as permitting update")
	}
}

// The never-heard-from case is not a property of a record — there is no
// record. It lives in announceDecision's nil branch, which is where this
// asserts it, rather than in a map lookup helper that would have to take the
// lock its callers already hold.
func TestAnnounceDecisionWithholdsFromUnreportedAgent(t *testing.T) {
	_, blocked := announceDecision(nil, "linux", strings.Repeat("55", 32))
	if !strings.Contains(blocked, "has not yet received agent_running_version") {
		t.Fatalf("blocked reason = %q, want the not-yet-reported refusal", blocked)
	}
}

/* ----------------------------------------------------------------------------
 * End-to-end: what actually goes out on the agent WebSocket.
 * -------------------------------------------------------------------------- */

// announceProbe describes the agent collectAnnounceFrames simulates.
type announceProbe struct {
	machineID string
	// proto 0 models a pre-signature agent: it reports its running SHA
	// (Phase 8 did) but has no update_protocol field at all.
	proto int
	// transportOK is what a signature-capable agent reports about its own
	// BLOXOS_HUB. Ignored when proto is 0.
	transportOK bool
	// keyPinned is what a signature-capable agent reports about having a
	// usable pinned update key on disk. Ignored when proto is 0. Sent as
	// update_key_pinned unless omitKeyPinned is set.
	keyPinned bool
	// omitKeyPinned models an agent built before update_key_pinned existed:
	// proto >= 1 and transport_ok are sent, but the key field is left out of
	// the frame entirely, exactly as encoding/json would leave a struct
	// field unset. Proves the fail-closed default comes from the zero
	// value, not from special-casing "field present and false".
	omitKeyPinned bool
}

// collectAnnounceFrames enrols an agent, sends metrics so the hub learns the
// OS, reports agent_running_version, and returns every agent_version frame
// the hub sent within the window.
func (s *Server) collectAnnounceFrames(t *testing.T, e *echo.Echo, p announceProbe) []string {
	t.Helper()

	server := httptest.NewServer(e)
	t.Cleanup(server.Close)

	token := s.seedValidToken(t)
	conn, err := wsDialAgent(t, server, "token="+token)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() {
		conn.Close()
		s.waitAgentDrain(t, p.machineID, 2*time.Second)
	})

	sendMetricsMsg(t, conn, p.machineID)

	// Commit like the real agent: a fresh enrollment is registered (and any
	// registration-time announce sent) only once enrollment_committed has
	// committed, so keep any announce that arrives alongside the confirmation.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	readEnrolledSecret(t, conn)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"enrollment_committed"}`)); err != nil {
		t.Fatalf("send enrollment_committed: %v", err)
	}
	var frames []string
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for enrollment_confirmed: %v", err)
		}
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(msg, &probe) != nil {
			continue
		}
		if probe.Type == "agent_version" {
			frames = append(frames, string(msg))
		}
		if probe.Type == "enrollment_confirmed" {
			break
		}
	}

	running := map[string]interface{}{
		"type":   "agent_running_version",
		"sha256": strings.Repeat("11", 32),
		"os":     "linux",
	}
	if p.proto > 0 {
		running["update_protocol"] = p.proto
		running["update_transport_ok"] = p.transportOK
		if !p.omitKeyPinned {
			running["update_key_pinned"] = p.keyPinned
		}
	}
	data, _ := json.Marshal(running)
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("write agent_running_version: %v", err)
	}

	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(msg, &probe) == nil && probe.Type == "agent_version" {
			frames = append(frames, string(msg))
		}
	}
	return frames
}

// assertNoReconnectExpectation is the assertion that actually matters when an
// announce is withheld. An armed reconnect expectation for an update the agent
// will never take expires into a rollout failure; two of those trip the
// process-wide circuit breaker and pause updates for the whole fleet, with
// rolloutPauseReason blaming agent health for a refusal the hub provoked.
func assertNoReconnectExpectation(t *testing.T, machineID string) {
	t.Helper()
	pendingReconnectsMu.Lock()
	_, armed := pendingReconnects[machineID]
	pendingReconnectsMu.Unlock()
	if armed {
		t.Fatalf("a reconnect expectation was armed for %s despite no announce being sent — "+
			"it will expire into a false rollout failure and can trip the circuit breaker", machineID)
	}
	rolloutPausedMu.RLock()
	paused, reason := rolloutPaused, rolloutPauseReason
	rolloutPausedMu.RUnlock()
	if paused {
		t.Fatalf("rollout is paused: %s", reason)
	}
}

// stagePendingUpdate makes the hub believe it is serving a binary the agent
// is not running, so an announce is actually warranted.
func stagePendingUpdate(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bloxos-agent")
	if err := os.WriteFile(bin, []byte("staged-linux-agent-binary"), 0o755); err != nil {
		t.Fatalf("write agent binary: %v", err)
	}
	t.Setenv("BLOXOS_AGENT_BINARY", bin)
	stageLinuxBinarySHA(t)
}

// A pre-signature agent on a plaintext, non-loopback hub must get nothing.
// That agent's AgentVersionMessage has no signature field, so it would take
// the update unverified — the exact hop an on-path attacker owns.
func TestLegacyAgentGetsNoAnnounceOverPlaintext(t *testing.T) {
	e, s := setupTestServer(t)
	t.Setenv("PUBLIC_URL", "http://hub.example.com")
	stagePendingUpdate(t)

	const machineID = "legacy-plaintext-machine"
	frames := s.collectAnnounceFrames(t, e, announceProbe{machineID: machineID, proto: 0})
	if len(frames) != 0 {
		t.Fatalf("hub announced an update to a pre-signature agent over plaintext: %q", frames)
	}
	assertNoReconnectExpectation(t, machineID)
}

// Same agent, TLS deployment: the one migration hop is allowed, because an
// off-host attacker cannot reach it. This is the control for the test above —
// same helper, same proto — so the plaintext case cannot pass vacuously.
func TestLegacyAgentGetsAnnounceOverTLS(t *testing.T) {
	e, s := setupTestServer(t)
	t.Setenv("PUBLIC_URL", "https://hub.example.com")
	stagePendingUpdate(t)

	frames := s.collectAnnounceFrames(t, e, announceProbe{machineID: "legacy-tls-machine", proto: 0})
	if len(frames) == 0 {
		t.Fatal("hub withheld the migration announce from a legacy agent on a TLS deployment")
	}
}

// A signature-capable agent that reports its own transport cannot self-update
// must get nothing — it would refuse the download, and announcing only arms a
// reconnect expectation that expires into a false rollout failure. This test
// previously asserted the opposite and locked in that bug; Codex caught it.
func TestNoAnnounceWhenAgentReportsPlaintextTransport(t *testing.T) {
	e, s := setupTestServer(t)
	// PUBLIC_URL says TLS. The agent says otherwise about its own hub URL,
	// and the agent is the one that has to perform the download — this is
	// the mixed deployment PUBLIC_URL alone cannot see.
	t.Setenv("PUBLIC_URL", "https://hub.example.com")
	stagePendingUpdate(t)

	const machineID = "capable-plaintext-machine"
	frames := s.collectAnnounceFrames(t, e, announceProbe{machineID: machineID, proto: 1, transportOK: false})
	if len(frames) != 0 {
		t.Fatalf("hub announced an update the agent had already said it would refuse: %q", frames)
	}
	assertNoReconnectExpectation(t, machineID)
}

// And the same agent reporting a usable transport and a pinned key is
// announced to normally, with a signature it can actually verify.
func TestSignatureCapableAgentWithUsableTransportGetsSignedAnnounce(t *testing.T) {
	e, s := setupTestServer(t)
	t.Setenv("PUBLIC_URL", "https://hub.example.com")
	stagePendingUpdate(t)

	frames := s.collectAnnounceFrames(t, e, announceProbe{machineID: "capable-tls-machine", proto: 1, transportOK: true, keyPinned: true})
	if len(frames) == 0 {
		t.Fatal("hub withheld an announce from a signature-capable agent on a usable transport")
	}
	var msg struct {
		Type      string `json:"type"`
		SHA256    string `json:"sha256"`
		Signature string `json:"signature"`
		SigAlg    string `json:"sig_alg"`
	}
	if err := json.Unmarshal([]byte(frames[0]), &msg); err != nil {
		t.Fatalf("parse announce: %v", err)
	}
	if msg.Signature == "" || msg.SigAlg != "ed25519" {
		t.Fatalf("announce carried no usable signature: %+v", msg)
	}
}

// A signature-capable agent that reports a usable transport but no pinned
// key on disk must get nothing. This is the default state of every agent in
// a fleet that predates this gate, immediately after the hub starts serving
// a signature-capable build: the agent reaches the new binary over the one
// unverifiable migration hop and reports update_protocol >= 1 on its very
// next connect, but its installer has not been re-run yet — nothing has
// pinned a key for it. Without this gate the hub announces anyway, arms a
// reconnect expectation the refusal can never satisfy, and at two such
// machines trips the fleet-wide circuit breaker — blaming agent health for
// a refusal the hub itself provoked.
func TestNoAnnounceWhenAgentKeyNotPinned(t *testing.T) {
	e, s := setupTestServer(t)
	t.Setenv("PUBLIC_URL", "https://hub.example.com")
	stagePendingUpdate(t)

	const machineID = "capable-unpinned-machine"
	frames := s.collectAnnounceFrames(t, e, announceProbe{machineID: machineID, proto: 1, transportOK: true, keyPinned: false})
	if len(frames) != 0 {
		t.Fatalf("hub announced an update to an agent with no pinned key: %q", frames)
	}
	assertNoReconnectExpectation(t, machineID)

	agentRunningVersionsMu.RLock()
	info, ok := agentRunningVersions[machineID]
	agentRunningVersionsMu.RUnlock()
	if !ok {
		t.Fatalf("test setup: %s never recorded", machineID)
	}
	_, reason := announceDecision(&info, "linux", announcedSHAFor("linux"))
	if !strings.Contains(reason, "agent_key_not_pinned") {
		t.Fatalf("withhold reason = %q, want it to name the unpinned-key gate distinctly "+
			"from agent-health noise an operator would otherwise have to guess at", reason)
	}
}

// An agent that reports update_protocol >= 1 and a usable transport but
// omits update_key_pinned entirely — the shape of a signature-capable
// binary built before this field existed — must be withheld identically to
// one that explicitly reported false. There is no third state where
// "didn't say" means "assume yes": encoding/json already leaves an absent
// bool field at its zero value, and this test is what pins that behavior in
// place rather than trusting it not to change out from under the gate.
func TestNoAnnounceWhenAgentOmitsKeyPinnedField(t *testing.T) {
	e, s := setupTestServer(t)
	t.Setenv("PUBLIC_URL", "https://hub.example.com")
	stagePendingUpdate(t)

	const machineID = "capable-legacy-field-machine"
	frames := s.collectAnnounceFrames(t, e, announceProbe{machineID: machineID, proto: 1, transportOK: true, omitKeyPinned: true})
	if len(frames) != 0 {
		t.Fatalf("hub announced an update to an agent that never reported update_key_pinned: %q", frames)
	}
	assertNoReconnectExpectation(t, machineID)
}

// TestNoSignatureMeansNoAnnounceOnTheWire is the end-to-end form of the claim
// the old TestNoSignatureMeansNoAnnounce made in its name but never tested:
// with no signing key, nothing goes out on the socket at all.
func TestNoSignatureMeansNoAnnounceOnTheWire(t *testing.T) {
	e, s := setupTestServer(t)
	t.Setenv("PUBLIC_URL", "https://hub.example.com")
	withoutSigningKey(t)
	stagePendingUpdate(t)

	const machineID = "unsigned-hub-machine"
	// keyPinned: true isolates the variable this test is about — the hub
	// cannot sign — from the key-pinned gate, which has its own tests below.
	frames := s.collectAnnounceFrames(t, e, announceProbe{machineID: machineID, proto: 1, transportOK: true, keyPinned: true})
	if len(frames) != 0 {
		t.Fatalf("hub announced an update it could not sign: %q", frames)
	}
	assertNoReconnectExpectation(t, machineID)
}

// Whatever the hub withholds, GET /api/versions must say why. Otherwise a
// fleet that has permanently stopped updating is indistinguishable from a
// rollout in progress: update_pending true on every agent, forever.
func TestVersionsAPIReportsWhyUpdatesAreWithheld(t *testing.T) {
	e, s := setupTestServer(t)
	t.Setenv("PUBLIC_URL", "https://hub.example.com")
	withoutSigningKey(t)
	setUpdateSigningDisabled("the signing key at /etc/bloxos/x.key is corrupt")
	stagePendingUpdate(t)

	sha := announcedSHAFor("linux")
	if sha == "" {
		t.Fatal("test setup: no linux SHA staged")
	}
	agentRunningVersionsMu.Lock()
	agentRunningVersions["api-machine"] = agentVersionInfo{
		MachineID: "api-machine", OS: "linux", RunningSHA: strings.Repeat("22", 32),
		// KeyPinned: true isolates the variable this test is about — the hub
		// cannot sign — from the key-pinned gate, which has its own tests.
		UpdateProtocol: 1, UpdateTransportOK: true, UpdateKeyPinned: true,
	}
	agentRunningVersionsMu.Unlock()
	t.Cleanup(func() {
		agentRunningVersionsMu.Lock()
		delete(agentRunningVersions, "api-machine")
		agentRunningVersionsMu.Unlock()
	})

	s.markCredentialsRotated(t)
	req := httptest.NewRequest(http.MethodGet, "/api/versions", nil)
	req.Header.Set("Authorization", "Bearer "+loginAndGetToken(t, e))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		SigningEnabled        bool   `json:"signing_enabled"`
		SigningDisabledReason string `json:"signing_disabled_reason"`
		Agents                []struct {
			MachineID           string `json:"machine_id"`
			UpdatePending       bool   `json:"update_pending"`
			UpdateBlockedReason string `json:"update_blocked_reason"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v (body=%s)", err, rec.Body.String())
	}
	if resp.SigningEnabled {
		t.Fatal("signing_enabled is true with no signing material")
	}
	if !strings.Contains(resp.SigningDisabledReason, "corrupt") {
		t.Fatalf("signing_disabled_reason = %q, want the corrupt-key reason", resp.SigningDisabledReason)
	}
	var found bool
	for _, a := range resp.Agents {
		if a.MachineID != "api-machine" {
			continue
		}
		found = true
		if !a.UpdatePending {
			t.Fatal("test setup: expected a pending update")
		}
		if !strings.Contains(a.UpdateBlockedReason, "cannot sign") {
			t.Fatalf("update_blocked_reason = %q, want it to name the signing failure", a.UpdateBlockedReason)
		}
	}
	if !found {
		t.Fatalf("api-machine missing from response: %s", rec.Body.String())
	}
}

// TestVersionsAPIDoesNotDeadlockWithConcurrentAgentReports hammers
// GET /api/versions against the write path that lands on every agent
// reconnect.
//
// The regression: handleListVersions used to hold agentRunningVersionsMu
// across a call to announceDecision, which re-entered the same RWMutex.
// sync.RWMutex does not permit recursive read locking — a writer queueing
// between the outer and inner acquisition blocks the reader, which never
// releases, which blocks the writer. recordAgentRunningVersion runs on the
// agent WebSocket read path, so the whole rollout subsystem wedged until a
// hub restart, triggered by nothing more exotic than a dashboard poll racing
// an agent reconnect.
//
// On the broken code this hangs rather than failing an assertion, so the
// work runs in a goroutine and the test fails on a deadline instead of
// stalling the suite silently. Run under -race for the full signal.
func TestVersionsAPIDoesNotDeadlockWithConcurrentAgentReports(t *testing.T) {
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	token := loginAndGetToken(t, e)
	stagePendingUpdate(t)

	const machines = 8
	for i := 0; i < machines; i++ {
		id := fmt.Sprintf("deadlock-probe-%d", i)
		agentRunningVersionsMu.Lock()
		agentRunningVersions[id] = agentVersionInfo{
			MachineID: id, OS: "linux", RunningSHA: strings.Repeat("33", 32),
			UpdateProtocol: 1, UpdateTransportOK: true,
		}
		agentRunningVersionsMu.Unlock()
	}
	t.Cleanup(func() {
		// TryLock, not Lock. If the regression is present the mutex is
		// permanently wedged, and a blocking cleanup runs *after* t.Fatal —
		// swallowing the diagnostic and letting Go's test timeout panic
		// instead. Verified: with the nested RLock reintroduced this test
		// reported nothing at all until cleanup stopped blocking.
		if !agentRunningVersionsMu.TryLock() {
			return
		}
		defer agentRunningVersionsMu.Unlock()
		for i := 0; i < machines; i++ {
			delete(agentRunningVersions, fmt.Sprintf("deadlock-probe-%d", i))
		}
	})

	stop := make(chan struct{})
	finished := make(chan struct{})

	go func() {
		defer close(finished)
		var wg sync.WaitGroup

		// Writers: the agent_running_version path, which is what queues a
		// Lock() behind the handler's outer RLock.
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for i := 0; ; i++ {
					select {
					case <-stop:
						return
					default:
					}
					s.recordAgentRunningVersion(fmt.Sprintf("deadlock-probe-%d", i%machines),
						agentVersionReport{
							RunningSHA:     strings.Repeat("44", 32),
							OS:             "linux",
							UpdateProtocol: 1,
							TransportOK:    true,
						})
				}
			}(w)
		}

		// Readers: the dashboard poll.
		for r := 0; r < 4; r++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					req := httptest.NewRequest(http.MethodGet, "/api/versions", nil)
					req.Header.Set("Authorization", "Bearer "+token)
					rec := httptest.NewRecorder()
					e.ServeHTTP(rec, req)
					if rec.Code != http.StatusOK {
						t.Errorf("GET /api/versions returned %d", rec.Code)
						return
					}
				}
			}()
		}

		time.Sleep(300 * time.Millisecond)
		close(stop)
		wg.Wait()
	}()

	select {
	case <-finished:
	case <-time.After(20 * time.Second):
		t.Fatal("GET /api/versions deadlocked against concurrent agent_running_version writes — " +
			"announceDecision or something it reaches is taking agentRunningVersionsMu while a " +
			"caller already holds it")
	}
}
