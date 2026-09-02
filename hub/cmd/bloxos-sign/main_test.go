package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bokiko/bloxos/proto/updatesigning"
)

func testSigningKey(t *testing.T) (string, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	path := filepath.Join(t.TempDir(), "update-signing.key")
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(priv)+"\n"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return path, pub, priv
}

func TestRunSignsWithEnvironmentKey(t *testing.T) {
	keyPath, pub, priv := testSigningKey(t)
	t.Setenv(keyEnv, keyPath)
	bin := filepath.Join(t.TempDir(), "bloxos-agent")
	if err := os.WriteFile(bin, []byte("agent release"), 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	var out bytes.Buffer
	if err := run([]string{"-os", "linux", bin}, &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	sigData, err := os.ReadFile(bin + ".sig")
	if err != nil {
		t.Fatalf("read signature: %v", err)
	}
	shaResult, err := updatesigning.SignFile(bin, "linux", keyPath)
	if err != nil {
		t.Fatalf("re-sign for verification: %v", err)
	}
	if err := updatesigning.Verify(pub, "linux", shaResult.SHA256, string(sigData)); err != nil {
		t.Fatalf("written signature does not verify: %v", err)
	}
	if strings.Contains(out.String(), base64.StdEncoding.EncodeToString(priv)) {
		t.Fatal("command output exposed private key material")
	}
}

func TestRunPrintsOnlyPublicKey(t *testing.T) {
	keyPath, pub, priv := testSigningKey(t)
	var out bytes.Buffer
	if err := run([]string{"-key", keyPath, "-print-public-key"}, &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := strings.TrimSpace(out.String()), base64.StdEncoding.EncodeToString(pub); got != want {
		t.Fatalf("public key = %q, want %q", got, want)
	}
	if strings.Contains(out.String(), base64.StdEncoding.EncodeToString(priv)) {
		t.Fatal("command output exposed private key material")
	}
}

func TestResolveKeyPathDefaultsUnderHome(t *testing.T) {
	t.Setenv(keyEnv, "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := resolveKeyPath("")
	if err != nil {
		t.Fatalf("resolveKeyPath: %v", err)
	}
	want := filepath.Join(home, ".bloxos", "update-signing.key")
	if got != want {
		t.Fatalf("default key path = %q, want %q", got, want)
	}
}
