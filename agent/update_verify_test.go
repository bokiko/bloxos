package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bokiko/bloxos/proto/updatesigning"
)

// TestUpdateSigningMessageFormat locks the exact bytes that get signed. The
// hub builds the same string in hub/update_signing.go and asserts the same
// literal there — if either side is edited without the other, one of the two
// tests fails instead of the fleet silently losing update authenticity.
func TestUpdateSigningMessageFormat(t *testing.T) {
	got := string(updateSigningMessage("linux", "abc123"))
	want := "bloxos-agent-update:v1:linux:abc123"
	if got != want {
		t.Fatalf("updateSigningMessage = %q, want %q", got, want)
	}
	// Case and surrounding whitespace must not change the message, so an
	// upper-case SHA from the hub still verifies.
	if string(updateSigningMessage(" LINUX ", " ABC123 ")) != want {
		t.Fatalf("updateSigningMessage is not normalising case/whitespace")
	}
}

func newTestKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

func signFor(t *testing.T, priv ed25519.PrivateKey, osName, sha string) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, updateSigningMessage(osName, sha)))
}

// pinKey writes a public key to a temp file and points the agent at it.
func pinKey(t *testing.T, pub ed25519.PublicKey) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent-update.pub")
	body := "# bloxos agent update signing key\n" +
		base64.StdEncoding.EncodeToString(pub) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write pinned key: %v", err)
	}
	t.Setenv("BLOXOS_UPDATE_PUBKEY_PATH", path)
	return path
}

const testSHA = "9f2c1e4a7b3d5f6081a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708"

func TestVerifyUpdateSignature(t *testing.T) {
	pub, priv := newTestKey(t)
	other := func() ed25519.PublicKey { p, _ := newTestKey(t); return p }()

	t.Run("valid", func(t *testing.T) {
		if err := verifyUpdateSignature(pub, "linux", testSHA, signFor(t, priv, "linux", testSHA)); err != nil {
			t.Fatalf("valid signature rejected: %v", err)
		}
	})

	// The OS is inside the signed message specifically so a signature issued
	// for the Linux binary cannot be replayed as the Windows announcement.
	t.Run("wrong os", func(t *testing.T) {
		if err := verifyUpdateSignature(pub, "windows", testSHA, signFor(t, priv, "linux", testSHA)); err == nil {
			t.Fatal("a linux signature verified as a windows announcement")
		}
	})

	t.Run("wrong sha", func(t *testing.T) {
		otherSHA := strings.Repeat("ab", 32)
		if err := verifyUpdateSignature(pub, "linux", otherSHA, signFor(t, priv, "linux", testSHA)); err == nil {
			t.Fatal("signature verified against a SHA it was not issued for")
		}
	})

	t.Run("wrong key", func(t *testing.T) {
		if err := verifyUpdateSignature(other, "linux", testSHA, signFor(t, priv, "linux", testSHA)); err == nil {
			t.Fatal("signature verified against an unrelated key")
		}
	})

	t.Run("missing signature", func(t *testing.T) {
		if err := verifyUpdateSignature(pub, "linux", testSHA, ""); err == nil {
			t.Fatal("empty signature accepted")
		}
	})

	t.Run("garbage signature", func(t *testing.T) {
		for _, sig := range []string{"not-base64!!", base64.StdEncoding.EncodeToString([]byte("short"))} {
			if err := verifyUpdateSignature(pub, "linux", testSHA, sig); err == nil {
				t.Fatalf("accepted garbage signature %q", sig)
			}
		}
	})

	t.Run("non-hex sha", func(t *testing.T) {
		bad := "not-a-sha"
		if err := verifyUpdateSignature(pub, "linux", bad, signFor(t, priv, "linux", bad)); err == nil {
			t.Fatal("accepted a non-hex SHA even though it was correctly signed")
		}
	})
}

func TestOfflineSigningToolRoundTripAgentVerification(t *testing.T) {
	pub, priv := newTestKey(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "bloxos-agent")
	if err := os.WriteFile(bin, []byte("offline-signed agent"), 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	keyPath := filepath.Join(dir, "update-signing.key")
	if err := os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}

	result, err := updatesigning.SignFile(bin, "linux", keyPath)
	if err != nil {
		t.Fatalf("sign file: %v", err)
	}
	pinKey(t, pub)

	if err := verifyAnnouncedRelease(bin, "linux", result.SHA256, result.Signature); err != nil {
		t.Fatalf("tool signature rejected by agent: %v", err)
	}
	if err := verifyAnnouncedRelease(bin, "windows", result.SHA256, result.Signature); err == nil {
		t.Fatal("linux tool signature accepted for windows")
	}
	if err := verifyAnnouncedRelease(bin, "linux", strings.Repeat("ab", 32), result.Signature); err == nil {
		t.Fatal("tool signature accepted for wrong SHA")
	}
}

func TestParseUpdatePublicKey(t *testing.T) {
	pub, _ := newTestKey(t)
	encoded := base64.StdEncoding.EncodeToString(pub)

	good := []string{
		encoded,
		"# comment\n\n" + encoded + "\n",
		"  " + encoded + "  \r\n",
	}
	for _, body := range good {
		got, err := parseUpdatePublicKey([]byte(body))
		if err != nil {
			t.Errorf("parseUpdatePublicKey(%q) error: %v", body, err)
			continue
		}
		if !got.Equal(pub) {
			t.Errorf("parseUpdatePublicKey(%q) returned a different key", body)
		}
	}

	bad := []string{"", "# only a comment\n", "not base64!!", base64.StdEncoding.EncodeToString([]byte("too short"))}
	for _, body := range bad {
		if _, err := parseUpdatePublicKey([]byte(body)); err == nil {
			t.Errorf("parseUpdatePublicKey(%q) succeeded, want error", body)
		}
	}
}

// TestAuthorizeUpdateRejectsPlaintextTransport is the test Master asked for:
// even a perfectly signed announcement must not be acted on when the hub is
// reachable only over plaintext.
func TestAuthorizeUpdateRejectsPlaintextTransport(t *testing.T) {
	pub, priv := newTestKey(t)
	pinKey(t, pub)
	sig := signFor(t, priv, "linux", testSHA)

	err := authorizeUpdate("ws://hub.example.com:4000/ws/agent", "/usr/local/bin/bloxos-agent",
		"linux", testSHA, sig, 0)
	if err == nil {
		t.Fatal("authorizeUpdate accepted a plaintext hub with a valid signature")
	}
	if !strings.Contains(err.Error(), "refusing to self-update") {
		t.Fatalf("error = %v, want the transport refusal", err)
	}
}

// No pinned key means the agent cannot tell the hub apart from anyone else on
// the wire, so it must not update — even over TLS with a well-formed frame.
func TestAuthorizeUpdateRequiresPinnedKey(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.pub")
	t.Setenv("BLOXOS_UPDATE_PUBKEY_PATH", missing)

	err := authorizeUpdate("wss://hub.example.com:4000/ws/agent", "/usr/local/bin/bloxos-agent",
		"linux", testSHA, "", 0)
	if err == nil {
		t.Fatal("authorizeUpdate proceeded with no pinned key")
	}
	if !strings.Contains(err.Error(), "no pinned update key") {
		t.Fatalf("error = %v, want the missing-key refusal", err)
	}
}

// An announcement over TLS carrying a signature from something other than the
// pinned key is the substitution attack itself.
func TestAuthorizeUpdateRejectsForeignSigner(t *testing.T) {
	pub, _ := newTestKey(t)
	_, attacker := newTestKey(t)
	pinKey(t, pub)

	err := authorizeUpdate("wss://hub.example.com:4000/ws/agent", "/usr/local/bin/bloxos-agent",
		"linux", testSHA, signFor(t, attacker, "linux", testSHA), 0)
	if err == nil {
		t.Fatal("authorizeUpdate accepted a signature from an unpinned key")
	}
	if !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("error = %v, want the signature-verification refusal", err)
	}
}

func TestAuthorizeUpdateHappyPath(t *testing.T) {
	pub, priv := newTestKey(t)
	pinKey(t, pub)
	bootFloorForTest(t, releaseFloor{5, floorSHAa}, nil)

	if err := authorizeUpdate("wss://hub.example.com:4000/ws/agent", "/usr/local/bin/bloxos-agent",
		"linux", testSHA, signFor(t, priv, "linux", testSHA), 0); err != nil {
		t.Fatalf("authorizeUpdate rejected a valid update: %v", err)
	}
	// A truthful advisory at or above the floor changes nothing.
	if err := authorizeUpdate("wss://hub.example.com:4000/ws/agent", "/usr/local/bin/bloxos-agent",
		"linux", testSHA, signFor(t, priv, "linux", testSHA), 6); err != nil {
		t.Fatalf("authorizeUpdate rejected a valid update with a higher advisory: %v", err)
	}
}

// A signed, TLS-carried announcement is still refused when the release floor
// was never established (or failed) at boot: the agent cannot prove the
// candidate is not a downgrade, so it must not fetch it.
func TestAuthorizeUpdateRequiresUsableReleaseFloor(t *testing.T) {
	pub, priv := newTestKey(t)
	pinKey(t, pub)
	resetReleaseFloorStateForTest()
	t.Cleanup(resetReleaseFloorStateForTest)

	err := authorizeUpdate("wss://hub.example.com:4000/ws/agent", "/usr/local/bin/bloxos-agent",
		"linux", testSHA, signFor(t, priv, "linux", testSHA), 0)
	if err == nil {
		t.Fatal("authorizeUpdate proceeded without a release floor")
	}
	if !strings.Contains(err.Error(), "release floor") {
		t.Fatalf("error = %v, want the release-floor refusal", err)
	}
}

// The hub's advisory release is refuse-only: below the floor (or the floor's
// release with another SHA) skips the download, but it never admits anything
// on its own — the higher-advisory case above still goes on to the
// post-download extraction.
func TestAuthorizeUpdateRefusesOnAdvisoryBelowFloor(t *testing.T) {
	pub, priv := newTestKey(t)
	pinKey(t, pub)
	bootFloorForTest(t, releaseFloor{5, floorSHAa}, nil)

	err := authorizeUpdate("wss://hub.example.com:4000/ws/agent", "/usr/local/bin/bloxos-agent",
		"linux", testSHA, signFor(t, priv, "linux", testSHA), 4)
	if err == nil || !strings.Contains(err.Error(), "downgrade") {
		t.Fatalf("error = %v, want the downgrade refusal", err)
	}
	// Same release, but the announced SHA is not the build the floor pins.
	err = authorizeUpdate("wss://hub.example.com:4000/ws/agent", "/usr/local/bin/bloxos-agent",
		"linux", testSHA, signFor(t, priv, "linux", testSHA), 5)
	if err == nil || !strings.Contains(err.Error(), "different build") {
		t.Fatalf("error = %v, want the same-release-different-build refusal", err)
	}
}

// The transport and signature gates run before the floor is consulted, so a
// forged or plaintext announcement never reaches floor logic — and a floor
// problem is never reported in place of the more fundamental refusal.
func TestAuthorizeUpdateSignatureBeforeFloor(t *testing.T) {
	pub, _ := newTestKey(t)
	_, attacker := newTestKey(t)
	pinKey(t, pub)
	resetReleaseFloorStateForTest()
	t.Cleanup(resetReleaseFloorStateForTest)

	err := authorizeUpdate("wss://hub.example.com:4000/ws/agent", "/usr/local/bin/bloxos-agent",
		"linux", testSHA, signFor(t, attacker, "linux", testSHA), 0)
	if err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("error = %v, want the signature refusal first", err)
	}
}

func TestUpdatePublicKeyPathHonoursEnvOverride(t *testing.T) {
	t.Setenv("BLOXOS_UPDATE_PUBKEY_PATH", "/custom/path.pub")
	if got := updatePublicKeyPath("/usr/local/bin/bloxos-agent"); got != "/custom/path.pub" {
		t.Fatalf("updatePublicKeyPath = %q, want the env override", got)
	}
}

// updateKeyPinned is what the hub relies on to tell "this agent is ready"
// apart from "this agent is signature-capable but its installer hasn't run
// yet" — see the gate in hub/agent_versions.go's announceDecision. It must
// track exactly what authorizeUpdate would itself accept: a missing or
// unparseable key must report false so it can never claim readiness the
// verification path would then refuse.
func TestUpdateKeyPinnedReflectsKeyPresence(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.pub")
	t.Setenv("BLOXOS_UPDATE_PUBKEY_PATH", missing)
	if updateKeyPinned() {
		t.Fatal("updateKeyPinned true with no key file on disk")
	}
}

func TestUpdateKeyPinnedFalseOnCorruptKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-update.pub")
	if err := os.WriteFile(path, []byte("not a valid base64 key\n"), 0o644); err != nil {
		t.Fatalf("write corrupt key: %v", err)
	}
	t.Setenv("BLOXOS_UPDATE_PUBKEY_PATH", path)
	if updateKeyPinned() {
		t.Fatal("updateKeyPinned true with an unparseable key file")
	}
}

func TestUpdateKeyPinnedTrueOnUsableKey(t *testing.T) {
	pub, _ := newTestKey(t)
	pinKey(t, pub)
	if !updateKeyPinned() {
		t.Fatal("updateKeyPinned false with a valid pinned key on disk")
	}
}
