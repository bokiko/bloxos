package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fetchInstallPS1 serves /install.ps1 against a fresh test server with a
// generated Windows binary fixture and returns its body.
func fetchInstallPS1(t *testing.T) string {
	t.Helper()
	e, _ := setupTestServer(t)
	useGeneratedTestBinary(t, "windows")
	req := httptest.NewRequest(http.MethodGet, "/install.ps1", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("install.ps1 status=%d: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// TestWindowsInstallerParamBlockPrecedesExecutableStatements locks in that
// param([switch]$ForceReenroll) is a valid PowerShell param block: nothing
// but the leading comment line may precede it.
func TestWindowsInstallerParamBlockPrecedesExecutableStatements(t *testing.T) {
	body := fetchInstallPS1(t)
	lines := strings.Split(body, "\n")
	paramLineIdx := -1
	for i, line := range lines {
		if strings.Contains(line, "param(") && strings.Contains(line, "$ForceReenroll") {
			paramLineIdx = i
			break
		}
	}
	if paramLineIdx < 0 {
		t.Fatal("install.ps1 does not declare param([switch]$ForceReenroll)")
	}
	for i := 0; i < paramLineIdx; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		t.Fatalf("non-comment statement %q precedes the param() block at line %d", trimmed, i)
	}
}

// TestWindowsInstallerForcedReenrollPreservesAndCanRestoreSecret locks in the
// secret-handling contract for -ForceReenroll: the existing secret is backed
// up by exact path — recording only its hash, never moving, deleting, or
// otherwise disturbing the file — and that the completion gate waits for its
// hash to change rather than for its absence/reappearance. This is BLOCKER 1
// from the Codex review: the old credential must stay valid, on disk, in
// place, for as long as the hub might still need to accept it, because the
// hub's own promotion (not this script) is what invalidates it.
func TestWindowsInstallerForceReenrollNeverMovesOrDeletesSecret(t *testing.T) {
	body := fetchInstallPS1(t)

	for _, forbidden := range []string{
		"Remove-Item -Path $CredDir -Recurse",
		"Remove-Item -LiteralPath $CredDir -Recurse",
		"Remove-Item -Recurse -Path $CredDir",
		"Remove-Item -Recurse -LiteralPath $CredDir",
		"Move-Item -LiteralPath $SecretPath",
		"Move-Item -LiteralPath \"$SecretPath\"",
		"Remove-Item -LiteralPath $SecretPath",
		"Remove-Item -LiteralPath \"$SecretPath\"",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("install.ps1 moves, deletes, or recursively clears the credential: %q", forbidden)
		}
	}
	if strings.Contains(body, "SecretBackupPath") {
		t.Fatal("install.ps1 still references a secret backup path — the old design's move-aside/restore machinery was not fully removed")
	}

	// Only the hash is recorded, and only for comparison, before the service
	// transaction — read-only, so a later download/fingerprint failure has
	// nothing to roll back with respect to the secret.
	if !strings.Contains(body, "$OldSecretHash = (Get-FileHash") {
		t.Fatal("install.ps1 does not record the old secret's hash for ForceReenroll comparison")
	}
	forceBlockStart := strings.Index(body, "if ($ForceReenroll")
	tryBlockIdx := strings.Index(body, "\ntry {\n    if ($ServiceExisted) {")
	if forceBlockStart < 0 || tryBlockIdx < 0 || forceBlockStart >= tryBlockIdx {
		t.Fatalf("expected a $ForceReenroll hash-recording block before the try{} service transaction (forceBlockStart=%d tryBlockIdx=%d)", forceBlockStart, tryBlockIdx)
	}

	// Restore-PreviousInstall must not touch agent-secret at all — nothing
	// was ever moved, so there is nothing for it to put back.
	restoreFuncIdx := strings.Index(body, "function Restore-PreviousInstall")
	if restoreFuncIdx < 0 {
		t.Fatal("install.ps1 has no Restore-PreviousInstall function")
	}
	restoreBody := body[restoreFuncIdx:]
	if closeBrace := strings.Index(restoreBody, "\n}\n"); closeBrace > 0 {
		restoreBody = restoreBody[:closeBrace]
	}
	if strings.Contains(restoreBody, "$SecretPath") {
		t.Fatalf("Restore-PreviousInstall touches agent-secret, but the secret was never moved to begin with: %q", restoreBody)
	}
}

// TestWindowsInstallerForceReenrollWaitsForSecretHashChange locks in the
// completion gate's ForceReenroll branch: it must wait for the file at
// $SecretPath to acquire a DIFFERENT hash than the one recorded before the
// service transaction, proving the agent actually wrote a new credential —
// not merely that the service reached Running.
func TestWindowsInstallerForceReenrollWaitsForSecretHashChange(t *testing.T) {
	body := fetchInstallPS1(t)
	assertOrdered(t, body,
		"$OldSecretHash = (Get-FileHash",
		"Start-Service -Name BloxOSAgent",
		"if ($ForceReenroll) {",
		"$NewSecretHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $SecretPath).Hash",
		"if ($NewSecretHash -ne $OldSecretHash) { $EnrollmentComplete = $true }",
		"BloxOS agent installed and running.",
	)
}

// TestWindowsInstallerCleanInstallRequiresNewSecretBeforeSuccess locks in
// that install.ps1 does not print its success message merely because the
// service reached Running — a clean install must first observe a newly
// issued, non-empty secret.
func TestWindowsInstallerCleanInstallRequiresNewSecretBeforeSuccess(t *testing.T) {
	body := fetchInstallPS1(t)
	if !strings.Contains(body, "SecretExistedBefore") {
		t.Fatal("install.ps1 does not track whether a secret existed before this run")
	}
	assertOrdered(t, body,
		"Start-Service -Name BloxOSAgent",
		"-not $SecretExistedBefore",
		"BloxOS agent installed and running.",
	)
	if !strings.Contains(body, "did not complete enrollment") && !strings.Contains(body, "did not complete") {
		t.Fatal("install.ps1 has no bounded enrollment-completion timeout error")
	}
}

// TestWindowsInstallerNeverEmitsSecretContents locks in that no branch of
// install.ps1 writes secret file contents to the console; only hash
// comparisons are permitted.
func TestWindowsInstallerNeverEmitsSecretContents(t *testing.T) {
	body := fetchInstallPS1(t)
	for _, forbidden := range []string{
		"Write-Host $NewSecret",
		"Write-Host $OldSecret",
		"Write-Warning $NewSecret",
		"Write-Warning $OldSecret",
		"Write-Output $NewSecret",
		"Write-Output $OldSecret",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("install.ps1 may emit secret contents: %q", forbidden)
		}
	}
	if !strings.Contains(body, "Get-FileHash") {
		t.Fatal("install.ps1 does not use Get-FileHash for internal secret comparison")
	}
}
