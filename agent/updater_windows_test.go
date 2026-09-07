//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestCheckAndCleanPendingUpdate(t *testing.T) {
	// Reuse helpers from update_verify_test.go (they are in package main so available here)
	pub, priv := newTestKey(t)
	pinKey(t, pub)

	dir := t.TempDir()
	exe := filepath.Join(dir, "agent.exe")
	newPath := exe + ".new"
	markerPath := exe + ".pending"

	// The staged binary carries release 6; the running build is release 5.
	// performUpdateWindows raises the floor before writing the marker, so
	// the floor a genuine pending update boots into already names the
	// staged build.
	stagedContent := "dummy exe content " + markerFor(t, 6)
	validSHA := writeTestBinary(t, newPath, stagedContent)
	validSig := signFor(t, priv, "windows", validSHA)
	floorPath := bootFloorForTest(t, releaseFloor{5, floorSHAa}, &releaseFloor{6, validSHA})

	tests := []struct {
		name    string
		setup   func()
		wantErr bool
	}{
		{
			name: "success - keeps files",
			setup: func() {
				writeTestBinary(t, newPath, stagedContent)
				writeTestMarker(t, markerPath, validSHA, validSig)
			},
			wantErr: false,
		},
		{
			name: "success - marker from older agent without release",
			setup: func() {
				writeTestBinary(t, newPath, stagedContent)
				writeTestMarker(t, markerPath, validSHA, validSig)
			},
			wantErr: false,
		},
		{
			name: "replayed after floor moved on - deletes files",
			setup: func() {
				if err := writeReleaseFloor(floorPath, releaseFloor{7, floorSHAc}); err != nil {
					t.Fatal(err)
				}
				writeTestBinary(t, newPath, stagedContent)
				writeTestMarker(t, markerPath, validSHA, validSig)
			},
			wantErr: true,
		},
		{
			name: "unnumbered staged binary - deletes files",
			setup: func() {
				content := "dummy exe content without a marker"
				sha := writeTestBinary(t, newPath, content)
				writeTestMarker(t, markerPath, sha, signFor(t, priv, "windows", sha))
			},
			wantErr: true,
		},
		{
			name: "missing SHA - deletes files",
			setup: func() {
				writeTestBinary(t, newPath, "dummy exe content")
				writeTestMarker(t, markerPath, "", validSig)
			},
			wantErr: true,
		},
		{
			name: "missing signature - deletes files",
			setup: func() {
				writeTestBinary(t, newPath, "dummy exe content")
				writeTestMarker(t, markerPath, validSHA, "")
			},
			wantErr: true,
		},
		{
			name: "mismatched SHA - deletes files",
			setup: func() {
				writeTestBinary(t, newPath, "dummy exe content")
				writeTestMarker(t, markerPath, strings.Repeat("0", 64), validSig)
			},
			wantErr: true,
		},
		{
			name: "invalid signature - deletes files",
			setup: func() {
				writeTestBinary(t, newPath, "dummy exe content")
				writeTestMarker(t, markerPath, validSHA, "invalid_base64_sig")
			},
			wantErr: true,
		},
		{
			name: "missing new binary - deletes marker",
			setup: func() {
				os.Remove(newPath)
				writeTestMarker(t, markerPath, validSHA, validSig)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Remove(newPath)
			os.Remove(markerPath)
			// Each case starts from the floor a genuine pending update
			// boots into; cases that move it do so inside setup.
			if err := writeReleaseFloor(floorPath, releaseFloor{6, validSHA}); err != nil {
				t.Fatal(err)
			}
			tt.setup()

			err := checkAndCleanPendingUpdate(exe, markerPath, newPath)
			if (err != nil) != tt.wantErr {
				t.Fatalf("checkAndCleanPendingUpdate() error = %v, wantErr %v", err, tt.wantErr)
			}

			// Verify presence of files based on wantErr
			if tt.wantErr {
				if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
					t.Errorf("marker file should be deleted on error")
				}
				if _, err := os.Stat(newPath); !os.IsNotExist(err) {
					t.Errorf("new binary should be deleted on error")
				}
			} else {
				if _, err := os.Stat(markerPath); os.IsNotExist(err) {
					t.Errorf("marker file should NOT be deleted on success")
				}
				if _, err := os.Stat(newPath); os.IsNotExist(err) {
					t.Errorf("new binary should NOT be deleted on success")
				}
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
