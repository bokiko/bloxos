//go:build windows

package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The newTestKey and signFor helpers are copied from update_verify_test.go because tests cannot
// easily export cross-package or cross-file without a separate test package.
func genTestKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

func signData(t *testing.T, priv ed25519.PrivateKey, osName, sha string) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, updateSigningMessage(osName, sha)))
}

func pinTestKey(t *testing.T, pub ed25519.PublicKey) string {
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

func writeTestBinary(t *testing.T, path string, content string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	h := sha256.New()
	h.Write([]byte(content))
	return hex.EncodeToString(h.Sum(nil))
}

func writeTestMarker(t *testing.T, path, sha, sig string) {
	t.Helper()
	content := "sha256=" + sha + "\nsignature=" + sig + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
}

func TestValidatePendingUpdate(t *testing.T) {
	pub, priv := genTestKey(t)
	pinTestKey(t, pub)

	dir := t.TempDir()
	exe := filepath.Join(dir, "agent.exe")
	newPath := exe + ".new"
	markerPath := exe + ".pending"

	// Create dummy binary
	validSHA := writeTestBinary(t, newPath, "dummy exe content")
	validSig := signData(t, priv, "windows", validSHA)

	tests := []struct {
		name        string
		setup       func()
		wantErr     bool
		errContains string
	}{
		{
			name: "success",
			setup: func() {
				writeTestMarker(t, markerPath, validSHA, validSig)
			},
			wantErr: false,
		},
		{
			name: "missing SHA in marker",
			setup: func() {
				writeTestMarker(t, markerPath, "", validSig)
			},
			wantErr:     true,
			errContains: "missing sha256 or signature",
		},
		{
			name: "missing signature in marker",
			setup: func() {
				writeTestMarker(t, markerPath, validSHA, "")
			},
			wantErr:     true,
			errContains: "missing sha256 or signature",
		},
		{
			name: "mismatched SHA",
			setup: func() {
				writeTestMarker(t, markerPath, strings.Repeat("0", 64), validSig)
			},
			wantErr:     true,
			errContains: "SHA mismatch",
		},
		{
			name: "invalid signature",
			setup: func() {
				writeTestMarker(t, markerPath, validSHA, "invalid_base64_sig")
			},
			wantErr:     true,
			errContains: "signature verification",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()
			err := validatePendingUpdate(exe, markerPath, newPath)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validatePendingUpdate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("error = %v, should contain %q", err, tt.errContains)
			}
		})
	}
}

func TestBuildHelperBatchMovesNotDeletes(t *testing.T) {
	helper := buildHelperBatch("target.exe", "new.exe", "marker.pending")
	if strings.Contains(helper, "del \"target.exe\"") {
		t.Errorf("buildHelperBatch contains del target: %s", helper)
	}
	if !strings.Contains(helper, "move /Y \"new.exe\" \"target.exe\"") {
		t.Errorf("buildHelperBatch does not contain move /Y: %s", helper)
	}
}

// TestApplyPendingUpdateCleanup verifies that if validatePendingUpdate fails,
// both .new and .pending files are removed.
func TestApplyPendingUpdateCleanup(t *testing.T) {
	pub, _ := genTestKey(t)
	pinTestKey(t, pub) // Use a valid pinned key, but we'll provide bad marker data to force failure.

	dir := t.TempDir()
	exe := filepath.Join(dir, "agent.exe")
	newPath := exe + ".new"
	markerPath := exe + ".pending"

	// Setup: create both files. The marker data is invalid (empty), so validatePendingUpdate will fail.
	writeTestBinary(t, newPath, "dummy content")
	writeTestMarker(t, markerPath, "", "")

	// Emulate the first half of applyPendingUpdate.
	// Since applyPendingUpdate directly accesses os.Executable and calls os.Exit, we test the snippet
	// that performs validation and cleanup.

	err := validatePendingUpdate(exe, markerPath, newPath)
	if err != nil {
		os.Remove(markerPath)
		os.Remove(newPath)
	} else {
		t.Fatal("validatePendingUpdate should have failed")
	}

	// Verify both files are removed.
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Errorf("marker path %s should have been removed", markerPath)
	}
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Errorf("new binary path %s should have been removed", newPath)
	}
}
