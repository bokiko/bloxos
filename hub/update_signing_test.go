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

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
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

// A hub with no signing material must not produce a signature at all: the
// agent would refuse an unsigned announcement, and announcing anyway arms a
// 90s reconnect-expectation timer whose expiry counts toward the rollout
// circuit breaker.
//
// This asserts announcedSignatureFor only. The end-to-end "hub sends nothing
// on the wire" claim is TestNoSignatureMeansNoAnnounceOnTheWire below, which
// drives a real agent WebSocket.
func TestNoSignatureAvailableWithoutSigningKey(t *testing.T) {
	setupTestServer(t)
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

// stageLinuxBinarySHA clears the cached linux SHA so the next recompute picks
// up whatever BLOXOS_AGENT_BINARY currently points at, and restores it after.
func stageLinuxBinarySHA(t *testing.T) {
	t.Helper()
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

	priv, source := loadOrGenerateUpdateSigningKey()
	if priv != nil {
		t.Fatalf("minted a new signing key after a non-ENOENT read error (source=%q) — "+
			"every pinned agent in the fleet would start rejecting updates", source)
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

	priv, _ := loadOrGenerateUpdateSigningKey()
	if priv != nil {
		t.Fatal("a corrupt key file yielded a usable key")
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

	priv, _ := loadOrGenerateUpdateSigningKey()
	if priv != nil {
		t.Fatal("generated a key at an explicitly configured path that did not exist")
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

	priv, source := loadOrGenerateUpdateSigningKey()
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
	again, _ := loadOrGenerateUpdateSigningKey()
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

func TestLegacyBootstrapAllowed(t *testing.T) {
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
		if ok, why := legacyBootstrapAllowed(); !ok {
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
		if ok, _ := legacyBootstrapAllowed(); ok {
			t.Errorf("PUBLIC_URL=%q should refuse legacy bootstrap", u)
		}
	}
}

func TestAgentIsSignatureCapable(t *testing.T) {
	setupTestServer(t)

	agentRunningVersionsMu.Lock()
	agentRunningVersions["cap-none"] = agentVersionInfo{MachineID: "cap-none"}
	agentRunningVersions["cap-legacy"] = agentVersionInfo{MachineID: "cap-legacy", UpdateProtocol: 0}
	agentRunningVersions["cap-new"] = agentVersionInfo{MachineID: "cap-new", UpdateProtocol: 1}
	agentRunningVersionsMu.Unlock()
	t.Cleanup(func() {
		agentRunningVersionsMu.Lock()
		delete(agentRunningVersions, "cap-none")
		delete(agentRunningVersions, "cap-legacy")
		delete(agentRunningVersions, "cap-new")
		agentRunningVersionsMu.Unlock()
	})

	// Never seen at all — the WS-upgrade-time case. Unknown is not capable.
	if agentIsSignatureCapable("cap-unseen") {
		t.Error("an agent we have never heard from was treated as signature-capable")
	}
	if agentIsSignatureCapable("cap-legacy") {
		t.Error("update_protocol=0 was treated as signature-capable")
	}
	if !agentIsSignatureCapable("cap-new") {
		t.Error("update_protocol=1 was not treated as signature-capable")
	}
}

/* ----------------------------------------------------------------------------
 * End-to-end: what actually goes out on the agent WebSocket.
 * -------------------------------------------------------------------------- */

// legacyPolicyDialer enrols an agent, sends metrics (so the hub learns the
// OS), optionally reports a signature-capable agent_running_version, and
// returns every frame the hub sent within the window.
func collectAnnounceFrames(t *testing.T, e *echo.Echo, machineID string, proto int) []string {
	t.Helper()

	server := httptest.NewServer(e)
	t.Cleanup(server.Close)

	token := seedValidToken(t)
	conn, err := wsDialAgent(t, server, "token="+token)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() {
		conn.Close()
		waitAgentDrain(t, machineID, 2*time.Second)
	})

	sendMetricsMsg(t, conn, machineID)

	// proto 0 models a pre-signature agent: it still reports its running SHA
	// (Phase 8 did), it just has no update_protocol field.
	running := map[string]interface{}{
		"type":   "agent_running_version",
		"sha256": strings.Repeat("11", 32),
		"os":     "linux",
	}
	if proto > 0 {
		running["update_protocol"] = proto
	}
	data, _ := json.Marshal(running)
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("write agent_running_version: %v", err)
	}

	var frames []string
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
	e := setupTestServer(t)
	t.Setenv("PUBLIC_URL", "http://hub.example.com")
	stagePendingUpdate(t)

	frames := collectAnnounceFrames(t, e, "legacy-plaintext-machine", 0)
	if len(frames) != 0 {
		t.Fatalf("hub announced an update to a pre-signature agent over plaintext: %q", frames)
	}
}

// Same agent, TLS deployment: the one migration hop is allowed, because an
// off-host attacker cannot reach it.
func TestLegacyAgentGetsAnnounceOverTLS(t *testing.T) {
	e := setupTestServer(t)
	t.Setenv("PUBLIC_URL", "https://hub.example.com")
	stagePendingUpdate(t)

	frames := collectAnnounceFrames(t, e, "legacy-tls-machine", 0)
	if len(frames) == 0 {
		t.Fatal("hub withheld the migration announce from a legacy agent on a TLS deployment")
	}
}

// A signature-capable agent is announced to regardless of PUBLIC_URL scheme —
// it verifies what it receives, and its own transport gate refuses to
// download over plaintext anyway.
func TestSignatureCapableAgentGetsAnnounceOverPlaintext(t *testing.T) {
	e := setupTestServer(t)
	t.Setenv("PUBLIC_URL", "http://hub.example.com")
	stagePendingUpdate(t)

	frames := collectAnnounceFrames(t, e, "capable-plaintext-machine", 1)
	if len(frames) == 0 {
		t.Fatal("hub withheld an announce from a signature-capable agent")
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

// TestNoSignatureMeansNoAnnounceOnTheWire is the end-to-end form of the claim
// the old TestNoSignatureMeansNoAnnounce made in its name but never tested:
// with no signing key, nothing goes out on the socket at all.
func TestNoSignatureMeansNoAnnounceOnTheWire(t *testing.T) {
	e := setupTestServer(t)
	t.Setenv("PUBLIC_URL", "https://hub.example.com")
	withoutSigningKey(t)
	stagePendingUpdate(t)

	frames := collectAnnounceFrames(t, e, "unsigned-hub-machine", 1)
	if len(frames) != 0 {
		t.Fatalf("hub announced an update it could not sign: %q", frames)
	}
}
