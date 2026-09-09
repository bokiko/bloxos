package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinuxBootstrapPreservesOldCA(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash unavailable")
	}
	ca := "new authenticated hub CA\n"
	digest := sha256.Sum256([]byte(ca))
	wantSHA := hex.EncodeToString(digest[:])
	for _, tc := range []struct {
		name, legacy, cached, download string
		wantOK, wantFetch              bool
	}{
		{"new install", "", "", ca, true, true},
		{"matching legacy", ca, "", "wrong", true, false},
		{"rotated CA", "old CA\n", "", ca, true, true},
		{"cached rotated CA", "old CA\n", ca, "wrong", true, false},
		{"wrong download", "old CA\n", "", "wrong", false, true},
		{"corrupt cached CA", "old CA\n", "corrupt", ca, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cred := filepath.Join(dir, "bloxos")
			bin := filepath.Join(dir, "bin")
			for _, path := range []string{cred, bin, filepath.Join(cred, "certs")} {
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path, value string, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(path, []byte(value), mode); err != nil {
					t.Fatal(err)
				}
			}
			legacy := filepath.Join(cred, "ca.crt")
			cached := filepath.Join(cred, "certs", wantSHA+".crt")
			if tc.legacy != "" {
				write(legacy, tc.legacy, 0o644)
			}
			if tc.cached != "" {
				write(cached, tc.cached, 0o644)
			}
			// Trust changes must not delete credentials or rollback floors.
			for _, name := range []string{"agent-secret", "agent-update.pub", "agent-update-floor"} {
				write(filepath.Join(cred, name), "preserve-me", 0o600)
			}
			write(filepath.Join(dir, "download-ca"), tc.download, 0o644)
			write(filepath.Join(bin, "curl"), `#!/bin/bash
set -eu
out=""
url=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    https://*) url="$1"; shift ;;
    *) shift ;;
  esac
done
case "$url" in
  */ca.crt) touch "$TEST_ROOT/fetched"; cp "$TEST_ROOT/download-ca" "$out" ;;
  */install.sh) printf 'printf "%%s" "$BLOXOS_CA_CERT" > "$TEST_ROOT/installed"\n' > "$out" ;;
  *) exit 99 ;;
esac
`, 0o755)
			if _, err := exec.LookPath("sha256sum"); err != nil {
				if _, err := exec.LookPath("shasum"); err != nil {
					t.Skip("no SHA-256 utility")
				}
				write(filepath.Join(bin, "sha256sum"), "#!/bin/sh\nexec shasum -a 256 \"$@\"\n", 0o755)
			}
			script := buildLinuxInstallCommand("https://hub.test", "wss://hub.test", "test-token", "https://hub.test/download/ca.crt", wantSHA)
			// Sandbox privileged I/O only; execute the generated CA selection,
			// hashing, failure paths and installer environment without root.
			script = strings.NewReplacer(
				"/etc/bloxos", cred,
				`if [[ $(id -u) -eq 0 ]]; then SUDO=""; else SUDO=sudo; fi`, `SUDO=""`,
				"install -d -o root -g root -m 0755", "mkdir -p",
				"install -o root -g root -m 0644", "cp",
			).Replace(script)
			cmd := exec.Command("bash", "-c", script)
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "TEST_ROOT="+dir)
			out, err := cmd.CombinedOutput()
			if (err == nil) != tc.wantOK {
				t.Fatalf("success=%v want=%v: %s", err == nil, tc.wantOK, out)
			}
			_, fetchedErr := os.Stat(filepath.Join(dir, "fetched"))
			if (fetchedErr == nil) != tc.wantFetch {
				t.Errorf("CA fetch=%v want=%v", fetchedErr == nil, tc.wantFetch)
			}
			installed, installErr := os.ReadFile(filepath.Join(dir, "installed"))
			if tc.wantOK {
				wantPath := legacy
				if tc.legacy != "" && tc.legacy != ca {
					wantPath = cached
				}
				if installErr != nil || string(installed) != wantPath {
					t.Fatalf("installer CA path=%q, want=%q, err=%v", installed, wantPath, installErr)
				}
				got, err := os.ReadFile(wantPath)
				if err != nil || string(got) != ca {
					t.Fatal("installed CA does not match authenticated bytes")
				}
			} else if installErr == nil {
				t.Fatal("installer ran despite unverified CA")
			}
			if tc.legacy != "" {
				got, _ := os.ReadFile(legacy)
				if string(got) != tc.legacy {
					t.Fatal("legacy CA overwritten")
				}
			}
			if tc.cached != "" {
				got, _ := os.ReadFile(cached)
				if string(got) != tc.cached {
					t.Fatal("cached CA overwritten")
				}
			} else if !tc.wantOK {
				if _, err := os.Stat(cached); !os.IsNotExist(err) {
					t.Fatal("unverified CA persisted")
				}
			}
			for _, name := range []string{"agent-secret", "agent-update.pub", "agent-update-floor"} {
				got, _ := os.ReadFile(filepath.Join(cred, name))
				if string(got) != "preserve-me" {
					t.Errorf("%s changed", name)
				}
			}
		})
	}
}
