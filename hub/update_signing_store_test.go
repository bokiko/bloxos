package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bokiko/bloxos/proto/updatesigning"
)

// withPinnedKey installs a public key for the duration of a test and returns a
// signer for it.
func withPinnedKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	updateSigningMu.Lock()
	previousPub, previousB64 := updateSigningPub, updateSigningPubB64
	updateSigningPub = pub
	updateSigningPubB64 = base64.StdEncoding.EncodeToString(pub)
	updateSigningMu.Unlock()
	t.Cleanup(func() {
		updateSigningMu.Lock()
		updateSigningPub, updateSigningPubB64 = previousPub, previousB64
		updateSigningMu.Unlock()
	})
	return priv
}

func signFor(t *testing.T, priv ed25519.PrivateKey, osName, sha string) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, updateSigningMessage(osName, sha)))
}

// stageSignature writes a signature into a store the test owns, and points the
// hub at it.
func stageSignature(t *testing.T, osName, arch, sha, sig string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod store: %v", err)
	}
	t.Setenv(agentSignatureEnv, dir)
	path := filepath.Join(dir, agentSignatureName(osName, arch, sha))
	if sig != "" {
		if err := os.WriteFile(path, []byte(sig+"\n"), 0o644); err != nil {
			t.Fatalf("write signature: %v", err)
		}
	}
	return path
}

const testSHA = "3f1a0c9d5e7b2a4c6d8e0f1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e"

func TestStagedSignatureIsFoundByContentAddress(t *testing.T) {
	priv := withPinnedKey(t)
	sig := signFor(t, priv, "linux", testSHA)
	stageSignature(t, "linux", archARM64, testSHA, sig)

	if got := contentAddressedSignatureFor("linux", archARM64, testSHA); got != sig {
		t.Fatalf("staged signature was not found: got %q", got)
	}
}

// The point of content addressing: authorise bytes BEFORE the hub serves them.
func TestASignatureForBytesTheHubDoesNotYetServeSimplyWaits(t *testing.T) {
	priv := withPinnedKey(t)
	future := strings.Repeat("ab", 32)
	dir := t.TempDir()
	t.Setenv(agentSignatureEnv, dir)
	if err := os.WriteFile(filepath.Join(dir, agentSignatureName("linux", archAMD64, future)),
		[]byte(signFor(t, priv, "linux", future)), 0o644); err != nil {
		t.Fatalf("stage: %v", err)
	}

	// Nothing for the release being served now...
	if got := contentAddressedSignatureFor("linux", archAMD64, testSHA); got != "" {
		t.Fatalf("a signature for other bytes must not be offered, got %q", got)
	}
	// ...and it is there the moment those bytes arrive.
	if got := contentAddressedSignatureFor("linux", archAMD64, future); got == "" {
		t.Fatal("the staged signature must apply once the hub serves those bytes")
	}
}

// A signature that does not verify is WORSE than none: announcing it makes
// every agent in the fleet reject the update.
func TestASignatureThatDoesNotVerifyIsNeverAnnounced(t *testing.T) {
	priv := withPinnedKey(t)
	// Correctly formed, correctly signed — for different bytes.
	other := strings.Repeat("cd", 32)
	stageSignature(t, "linux", archAMD64, testSHA, signFor(t, priv, "linux", other))

	if got := contentAddressedSignatureFor("linux", archAMD64, testSHA); got != "" {
		t.Fatalf("a signature for other bytes must be refused, got %q", got)
	}
}

func TestSignatureFromAnotherKeyIsRefused(t *testing.T) {
	withPinnedKey(t)
	_, stranger, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	stageSignature(t, "linux", archAMD64, testSHA, signFor(t, stranger, "linux", testSHA))

	if got := contentAddressedSignatureFor("linux", archAMD64, testSHA); got != "" {
		t.Fatalf("a signature from an unpinned key must be refused, got %q", got)
	}
}

func TestMalformedStagedSignaturesAreRefused(t *testing.T) {
	withPinnedKey(t)
	for name, body := range map[string]string{
		"not base64":   "!!!!not-base64!!!!",
		"wrong length": base64.StdEncoding.EncodeToString([]byte("too short")),
		"empty":        "",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(agentSignatureEnv, dir)
			if err := os.WriteFile(filepath.Join(dir, agentSignatureName("linux", archAMD64, testSHA)),
				[]byte(body), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if got := contentAddressedSignatureFor("linux", archAMD64, testSHA); got != "" {
				t.Fatalf("got %q", got)
			}
		})
	}
}

// Without a pinned key nothing here could be checked, and an unverified
// signature must never be announced.
func TestNoPinnedKeyMeansNoStagedSignatureIsUsed(t *testing.T) {
	priv := withPinnedKey(t)
	sig := signFor(t, priv, "linux", testSHA)
	stageSignature(t, "linux", archAMD64, testSHA, sig)

	updateSigningMu.Lock()
	updateSigningPub = nil
	updateSigningMu.Unlock()

	if got := contentAddressedSignatureFor("linux", archAMD64, testSHA); got != "" {
		t.Fatalf("without a pinned key nothing may be announced, got %q", got)
	}
}

// State files follow the INSTALLATION-owned rule, not the root-only rule that
// governs served executables: under Docker the hub runs as uid 65532 and owns
// /data, and demanding root would break every containerised install.
func TestAWorldWritableSignatureIsRefused(t *testing.T) {
	priv := withPinnedKey(t)
	sig := signFor(t, priv, "linux", testSHA)
	path := stageSignature(t, "linux", archAMD64, testSHA, sig)

	if got := contentAddressedSignatureFor("linux", archAMD64, testSHA); got != sig {
		t.Fatalf("control failed: a well-owned signature must be accepted, got %q", got)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if got := contentAddressedSignatureFor("linux", archAMD64, testSHA); got != "" {
		t.Fatalf("a group/other-writable signature must be refused, got %q", got)
	}
}

func TestASymlinkedSignatureIsRefused(t *testing.T) {
	priv := withPinnedKey(t)
	sig := signFor(t, priv, "linux", testSHA)
	path := stageSignature(t, "linux", archAMD64, testSHA, sig)

	elsewhere := filepath.Join(t.TempDir(), "planted.sig")
	if err := os.WriteFile(elsewhere, []byte(sig), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := os.Symlink(elsewhere, path); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	// The content is byte-identical, so only the link itself is under test.
	if got := contentAddressedSignatureFor("linux", archAMD64, testSHA); got != "" {
		t.Fatalf("a symlinked signature must be refused, got %q", got)
	}
}

func TestAnOversizedSignatureIsRefused(t *testing.T) {
	withPinnedKey(t)
	dir := t.TempDir()
	t.Setenv(agentSignatureEnv, dir)
	if err := os.WriteFile(filepath.Join(dir, agentSignatureName("linux", archAMD64, testSHA)),
		make([]byte, agentSignatureMaxBytes+1), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := contentAddressedSignatureFor("linux", archAMD64, testSHA); got != "" {
		t.Fatalf("got %q", got)
	}
}

// The content address must distinguish architectures: the same release has
// different bytes per platform, and each needs its own signature.
func TestTheContentAddressSeparatesPlatforms(t *testing.T) {
	priv := withPinnedKey(t)
	sig := signFor(t, priv, "linux", testSHA)
	stageSignature(t, "linux", archAMD64, testSHA, sig)

	if got := contentAddressedSignatureFor("linux", archARM64, testSHA); got != "" {
		t.Fatalf("an amd64 signature must not answer for arm64, got %q", got)
	}
	if got := contentAddressedSignatureFor("windows", archAMD64, testSHA); got != "" {
		t.Fatalf("a linux signature must not answer for windows, got %q", got)
	}
}

// A hub that holds its own key must never consult the store: no new key, no
// new directory, no manual step for an ordinary install.
func TestTheHubHeldKeyPathIsUnchanged(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	updateSigningMu.Lock()
	previousPub, previousPriv := updateSigningPub, updateSigningKey
	updateSigningPub, updateSigningKey = pub, priv
	updateSigningMu.Unlock()
	t.Cleanup(func() {
		updateSigningMu.Lock()
		updateSigningPub, updateSigningKey = previousPub, previousPriv
		updateSigningMu.Unlock()
	})
	// Point the store at a directory that does not exist: nothing may depend
	// on it.
	t.Setenv(agentSignatureEnv, filepath.Join(t.TempDir(), "absent"))

	sig := announcedSignatureForArch("linux", archAMD64, testSHA)
	if sig == "" {
		t.Fatal("a hub holding its own key must still sign, with no store present")
	}
	if err := updatesigning.Verify(pub, "linux", testSHA, sig); err != nil {
		t.Fatalf("hub-held signature must verify: %v", err)
	}
}

// A FIFO with no writer blocks in open(), BEFORE any fstat that would reject
// it. O_NONBLOCK is what makes the check reachable; without it this test hangs
// rather than fails, which is why it is bounded.
func TestAFifoSignatureDoesNotBlock(t *testing.T) {
	withPinnedKey(t)
	dir := t.TempDir()
	t.Setenv(agentSignatureEnv, dir)
	path := filepath.Join(dir, agentSignatureName("linux", archAMD64, testSHA))
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Skipf("cannot create a FIFO here: %v", err)
	}

	done := make(chan string, 1)
	go func() { done <- contentAddressedSignatureFor("linux", archAMD64, testSHA) }()
	select {
	case got := <-done:
		if got != "" {
			t.Fatalf("a FIFO must never be treated as a signature, got %q", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reading a FIFO blocked; O_NONBLOCK is missing and the regular-file check is unreachable")
	}
}

// The content address is built only from validated components. Before this,
// the inputs were merely checked for being non-empty.
func TestAMalformedIdentityNeverReachesTheFilesystem(t *testing.T) {
	withPinnedKey(t)
	dir := t.TempDir()
	t.Setenv(agentSignatureEnv, dir)

	// A file that WOULD be read if the identity were accepted verbatim.
	planted := filepath.Join(dir, "planted.sig")
	if err := os.WriteFile(planted, []byte("irrelevant"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	for name, identity := range map[string][3]string{
		"traversal in sha":  {"linux", archAMD64, "../../etc/shadow"},
		"traversal in arch": {"linux", "../../..", testSHA},
		"short sha":         {"linux", archAMD64, "abc"},
		"non-hex sha":       {"linux", archAMD64, strings.Repeat("z", 64)},
		"unknown arch":      {"linux", "mips64", testSHA},
		"empty everything":  {"", "", ""},
		"separator in sha":  {"linux", archAMD64, "a/b" + strings.Repeat("c", 61)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, ok := agentSignatureAddress(identity[0], identity[1], identity[2]); ok {
				t.Fatalf("%v must not produce a content address", identity)
			}
			if got := contentAddressedSignatureFor(identity[0], identity[1], identity[2]); got != "" {
				t.Fatalf("got %q", got)
			}
		})
	}
}

// The OS component is the one case that is NEUTRALISED rather than rejected,
// and the distinction is worth stating: normalizeAgentOS maps anything that is
// not "windows" to "linux", so the value that reaches the path is always one
// of two fixed tokens and never the caller's string. A traversal there cannot
// survive — it is replaced, not passed through — so the safety property holds
// by a different mechanism than for the SHA and architecture.
func TestATraversalInTheOSComponentIsReplacedNotPassedThrough(t *testing.T) {
	name, platform, ok := agentSignatureAddress("../../etc", archAMD64, testSHA)
	if !ok {
		t.Fatal("the OS component is normalised, so this resolves rather than failing")
	}
	if platform.OS != "linux" {
		t.Fatalf("OS was not normalised to a fixed token: %q", platform.OS)
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		t.Fatalf("content address %q carries a path component", name)
	}
}

// The control for the test above: a well-formed identity DOES produce an
// address, so the rejections mean something.
func TestAWellFormedIdentityProducesItsAddress(t *testing.T) {
	name, platform, ok := agentSignatureAddress("Linux", "x86_64", strings.ToUpper(testSHA))
	if !ok {
		t.Fatal("a valid identity must produce an address")
	}
	if platform.OS != "linux" || platform.Arch != archAMD64 {
		t.Fatalf("identity was not normalised: %+v", platform)
	}
	if name != "linux-"+archAMD64+"-"+testSHA+".sig" {
		t.Fatalf("unexpected content address %q", name)
	}
}
