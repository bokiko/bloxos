package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type installerTokenResponse struct {
	Command        string `json:"command"`
	WindowsCommand string `json:"windows_command"`
	CAURL          string `json:"ca_url"`
	CASHA256       string `json:"ca_sha256"`
	ExpiresAt      string `json:"expires_at"`
}

func createInstallerToken(t *testing.T, publicURL, host, caPath string) installerTokenResponse {
	t.Helper()
	e, s := setupTestServer(t)
	s.markCredentialsRotated(t)
	t.Setenv("PUBLIC_URL", publicURL)
	t.Setenv("BLOXOS_CA_CERT", caPath)

	req := httptest.NewRequest(http.MethodPost, "/api/tokens", nil)
	req.Host = host
	req.Header.Set("Authorization", "Bearer "+loginAndGetToken(t, e))
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create token status=%d: %s", rec.Code, rec.Body.String())
	}
	var got installerTokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	return got
}

func testCAFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "root.crt")
	if err := os.WriteFile(path, []byte("test-private-ca"), 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}
	return path
}

func assertOrdered(t *testing.T, body string, needles ...string) {
	t.Helper()
	last := -1
	for _, needle := range needles {
		at := strings.Index(body, needle)
		if at < 0 {
			t.Fatalf("missing %q", needle)
		}
		if at <= last {
			t.Fatalf("%q appears out of order", needle)
		}
		last = at
	}
}

func TestInstallerCommandsUsePublicURLAndReturnTypedBootstrapFields(t *testing.T) {
	got := createInstallerToken(t, "https://hub.public.example", "evil.example", testCAFile(t))
	if got.Command == "" || got.WindowsCommand == "" || got.ExpiresAt == "" || got.CAURL == "" || got.CASHA256 == "" {
		t.Fatalf("incomplete token response: %+v", got)
	}
	for name, command := range map[string]string{"linux": got.Command, "windows": got.WindowsCommand} {
		if strings.Contains(command, "evil.example") || !strings.Contains(command, "hub.public.example") {
			t.Fatalf("%s command did not use PUBLIC_URL exclusively: %q", name, command)
		}
	}
	if strings.Contains(got.WindowsCommand, "ServerCertificateValidationCallback") || strings.Contains(got.WindowsCommand, "iwr") || strings.Contains(got.WindowsCommand, "| iex") {
		t.Fatalf("Windows command retains trust-all/network-pipe bootstrap: %q", got.WindowsCommand)
	}
	for _, line := range strings.Split(got.WindowsCommand, "\n") {
		if strings.Contains(line, "& curl.exe") && !strings.Contains(line, "--ssl-revoke-best-effort") {
			t.Fatalf("Windows paste-block curl invocation lacks --ssl-revoke-best-effort: %s", line)
		}
	}
	if !strings.Contains(got.WindowsCommand, got.CASHA256) || !strings.Contains(got.Command, got.CASHA256) {
		t.Fatal("CA fingerprint is not literal in both paste blocks")
	}
}

func TestLinuxPasteBlockVerifiesCABeforeInstallerFetchAndExec(t *testing.T) {
	got := createInstallerToken(t, "https://hub.public.example", "ignored.example", testCAFile(t))
	assertOrdered(t, got.Command,
		"sha256sum",
		"CA fingerprint mismatch",
		"/install.sh",
		"bash \"$INSTALLER\"",
	)
	if strings.Contains(got.Command, "install.sh | bash") || strings.Contains(got.Command, "curl -fsSLk https://hub.public.example/install.sh") {
		t.Fatalf("Linux command still executes an unverified network pipe: %q", got.Command)
	}
	if !strings.Contains(got.Command, "Existing CA fingerprint mismatch") || !strings.Contains(got.Command, "sudo install") {
		t.Fatal("Linux command does not reuse matching CA / stop on mismatching CA")
	}
}

func TestPublicTrustedHTTPSAndHTTPCommandsDoNotFetchLocalCA(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
	}{
		{name: "public HTTPS", url: "https://public.example"},
		{name: "loopback HTTP", url: "http://127.0.0.1:4000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := createInstallerToken(t, tc.url, "evil.example", filepath.Join(t.TempDir(), "missing.crt"))
			if got.CAURL != "" || got.CASHA256 != "" || strings.Contains(got.Command, "/download/ca.crt") || strings.Contains(got.WindowsCommand, "/download/ca.crt") {
				t.Fatalf("system-trust/direct-HTTP command unexpectedly bootstraps local CA: %+v", got)
			}
			if strings.HasPrefix(tc.url, "http://") && (!strings.Contains(got.Command, "unencrypted") || !strings.Contains(got.WindowsCommand, "unencrypted")) {
				t.Fatal("direct HTTP command does not state its transport limitation")
			}
		})
	}
}

func TestWindowsInstallerIsNonDestructiveFailClosedAndTransactional(t *testing.T) {
	e, _ := setupTestServer(t)
	useGeneratedTestBinary(t, "windows")
	req := httptest.NewRequest(http.MethodGet, "/install.ps1", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("install.ps1 status=%d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	for _, forbidden := range []string{
		"Remove-Item -Path $CredDir -Recurse",
		"curl.exe -ksfL",
		"-o $AgentExe $DownloadUrl",
		"skipping integrity check",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("install.ps1 contains forbidden behavior %q", forbidden)
		}
	}
	if !strings.Contains(body, "7.71") {
		t.Fatal("install.ps1 lacks the conservative curl >= 7.71 floor")
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "curl.exe") && strings.Contains(line, "& curl.exe") && !strings.Contains(line, "--ssl-revoke-best-effort") {
			t.Fatalf("curl invocation lacks --ssl-revoke-best-effort: %s", line)
		}
	}
	assertOrdered(t, body,
		"FromBase64String($UpdatePubKey)",
		"agent-update.pub",
		"$DownloadUrl",
		"$StagingExe",
		"Get-FileHash -Algorithm SHA256 -Path $StagingExe",
		"& $AgentExe -uninstall-service",
	)
	if !strings.Contains(body, "$ExpectedSha") || !strings.Contains(body, "expected agent SHA is missing") {
		t.Fatal("install.ps1 does not fail closed when expected SHA is missing")
	}
	if !strings.Contains(body, ".installing") || !strings.Contains(body, "Restore-PreviousInstall") {
		t.Fatal("install.ps1 lacks staging and transactional restore")
	}
	if !strings.Contains(body, "$FastPath") || !strings.Contains(body, "$KeyWasAlreadyPinned") || !strings.Contains(body, "$SecretPath") || !strings.Contains(body, "already installed and healthy") {
		t.Fatal("install.ps1 lacks the idempotent healthy-install fast path")
	}
}

func TestCaddyRoutesBothInstallerPaths(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "scripts", "caddy", "Caddyfile"))
	if err != nil {
		t.Fatalf("read Caddyfile: %v", err)
	}
	for _, route := range []string{"handle /install.sh", "handle /install.ps1"} {
		if !strings.Contains(string(body), route) {
			t.Fatalf("Caddyfile missing %s", route)
		}
	}
}

func TestDashboardRendersOnlyServerGeneratedCommands(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "dashboard", "src", "components", "AddMachineModal.tsx"))
	if err != nil {
		t.Fatalf("read AddMachineModal: %v", err)
	}
	source := string(body)
	for _, required := range []string{"windows_command", "resp.command", "resp.windows_command", "ca_sha256"} {
		if !strings.Contains(source, required) {
			t.Fatalf("dashboard missing server-response field %q", required)
		}
	}
	for _, forbidden := range []string{"deriveHttpHubBase", "deriveWsHubBase", "buildWindowsCommand", "ServerCertificateValidationCallback", "iwr -UseBasicParsing"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("dashboard still builds/trusts installer command client-side: %s", forbidden)
		}
	}
}
