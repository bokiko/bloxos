package main

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

const windowsInstallerScript = `# BloxOS Windows Agent installer
$ErrorActionPreference = 'Stop'

$Current = [Security.Principal.WindowsIdentity]::GetCurrent()
$Principal = New-Object Security.Principal.WindowsPrincipal($Current)
if (-not $Principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "This installer must be run from an elevated PowerShell session."
}

$Hub = $env:BLOXOS_HUB
$Token = $env:BLOXOS_TOKEN
if (-not $Hub) { throw "BLOXOS_HUB must be set" }
if (-not $Token) { throw "BLOXOS_TOKEN must be set" }
$HubHttp = $Hub -replace '^wss://','https://' -replace '^ws://','http://'

$CurlVersionOutput = & curl.exe --ssl-revoke-best-effort --version
$CurlExit = $LASTEXITCODE
$CurlVersionLine = $CurlVersionOutput | Select-Object -First 1
$CurlVersionMatch = [regex]::Match([string]$CurlVersionLine, '^curl ([0-9]+\.[0-9]+\.[0-9]+)')
if ($CurlExit -ne 0 -or -not $CurlVersionMatch.Success) {
    throw "Unable to determine curl.exe version"
}
$CurlVersion = [Version]$CurlVersionMatch.Groups[1].Value
if ($CurlVersion -lt [Version]'7.71.0') {
    throw "curl.exe 7.71 or newer is required; found $CurlVersion"
}

$CredDir = "C:\Windows\System32\config\systemprofile\.bloxos"
$CaPath = Join-Path $CredDir "ca.crt"
$SecretPath = Join-Path $CredDir "agent-secret"
$ExpectedCaSha = $env:BLOXOS_CA_SHA256
$CurlTlsArgs = @()
if ($ExpectedCaSha) {
    if (-not (Test-Path -LiteralPath $CaPath)) { throw "Fingerprint-pinned CA is missing at $CaPath; rerun the authenticated paste block" }
    $ActualCaSha = (Get-FileHash -Algorithm SHA256 -LiteralPath $CaPath).Hash.ToLower()
    if ($ActualCaSha -ne $ExpectedCaSha.ToLower()) { throw "Existing CA fingerprint mismatch at $CaPath" }
    $CurlTlsArgs = @('--cacert', $CaPath)
}

$ExpectedSha = "__EXPECTED_AGENT_SHA256__"
if (-not $ExpectedSha -or $ExpectedSha -notmatch '^[0-9a-fA-F]{64}$') {
    throw "expected agent SHA is missing or invalid; refusing installation"
}

$UpdatePubKey = "__UPDATE_PUBKEY__"
try {
    $UpdatePubKeyBytes = [Convert]::FromBase64String($UpdatePubKey)
} catch {
    throw "Hub update public key is not valid base64"
}
if ($UpdatePubKeyBytes.Length -ne 32) { throw "Hub update public key is not a 32-byte Ed25519 key" }

$InstallDir = "C:\Program Files\BloxOS"
$AgentExe = Join-Path $InstallDir "bloxos-agent.exe"
$PubKeyPath = Join-Path $InstallDir "agent-update.pub"
New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
$KeyWasAlreadyPinned = Test-Path -LiteralPath $PubKeyPath
if ($KeyWasAlreadyPinned) {
    try {
        $ExistingPubKeyBytes = [Convert]::FromBase64String((Get-Content -Raw -LiteralPath $PubKeyPath).Trim())
    } catch {
        throw "Existing agent-update.pub is invalid; refusing to replace it"
    }
    if ([Convert]::ToBase64String($ExistingPubKeyBytes) -ne [Convert]::ToBase64String($UpdatePubKeyBytes)) {
        throw "Existing agent-update.pub does not match the hub key; refusing to replace it"
    }
} else {
    $PubKeyTemp = Join-Path $InstallDir ("agent-update.pub." + [Guid]::NewGuid().ToString("N") + ".tmp")
    try {
        [IO.File]::WriteAllText($PubKeyTemp, $UpdatePubKey, [Text.Encoding]::ASCII)
        Move-Item -LiteralPath $PubKeyTemp -Destination $PubKeyPath
    } finally {
        Remove-Item -LiteralPath $PubKeyTemp -Force -ErrorAction SilentlyContinue
    }
}

$Service = Get-Service -Name BloxOSAgent -ErrorAction SilentlyContinue
$ExistingHub = [Environment]::GetEnvironmentVariable("BLOXOS_HUB", "Machine")
$FastPath = $KeyWasAlreadyPinned -and
    (Test-Path -LiteralPath $AgentExe) -and
    ((Get-FileHash -Algorithm SHA256 -LiteralPath $AgentExe).Hash.ToLower() -eq $ExpectedSha.ToLower()) -and
    (Test-Path -LiteralPath $SecretPath) -and
    ($ExistingHub -eq $Hub) -and
    $Service -and ($Service.Status -eq 'Running')
if ($FastPath) {
    Write-Host "BloxOS agent is already installed and healthy; no changes made."
    exit 0
}

function Invoke-CurlDownload([string]$Url, [string]$OutputPath) {
    & curl.exe --ssl-revoke-best-effort @CurlTlsArgs -sfL -o $OutputPath $Url
    if ($LASTEXITCODE -ne 0) { throw "Download failed for $Url (curl exit $LASTEXITCODE)" }
}

$DownloadUrl = "$HubHttp/download/agent?os=windows"
$StagingExe = "$AgentExe.installing"
Remove-Item -LiteralPath $StagingExe -Force -ErrorAction SilentlyContinue
Invoke-CurlDownload $DownloadUrl $StagingExe
$ActualSha = (Get-FileHash -Algorithm SHA256 -Path $StagingExe).Hash.ToLower()
if ($ActualSha -ne $ExpectedSha.ToLower()) {
    Remove-Item -LiteralPath $StagingExe -Force -ErrorAction SilentlyContinue
    throw "Agent binary fingerprint mismatch. Expected $ExpectedSha, got $ActualSha"
}

$ServiceExisted = [bool]$Service
$ServiceWasRunning = $ServiceExisted -and ($Service.Status -eq 'Running')
if ($ServiceExisted -and -not (Test-Path -LiteralPath $AgentExe)) {
    throw "BloxOSAgent exists but $AgentExe is missing; refusing an install that cannot be rolled back"
}
$BackupExe = $null
if (Test-Path -LiteralPath $AgentExe) {
    $BackupExe = "$AgentExe.preinstall-" + [Guid]::NewGuid().ToString("N")
    Copy-Item -LiteralPath $AgentExe -Destination $BackupExe
}
$OldHub = [Environment]::GetEnvironmentVariable("BLOXOS_HUB", "Machine")
$OldToken = [Environment]::GetEnvironmentVariable("BLOXOS_TOKEN", "Machine")
$OldCa = [Environment]::GetEnvironmentVariable("BLOXOS_CA_CERT", "Machine")

function Restore-PreviousInstall {
    $CurrentService = Get-Service -Name BloxOSAgent -ErrorAction SilentlyContinue
    if ($CurrentService -and (Test-Path -LiteralPath $AgentExe)) {
        & $AgentExe -uninstall-service | Out-Null
    }
    if ($BackupExe -and (Test-Path -LiteralPath $BackupExe)) {
        Copy-Item -LiteralPath $BackupExe -Destination $AgentExe -Force
    } elseif (-not $ServiceExisted) {
        Remove-Item -LiteralPath $AgentExe -Force -ErrorAction SilentlyContinue
    }
    [Environment]::SetEnvironmentVariable("BLOXOS_HUB", $OldHub, "Machine")
    [Environment]::SetEnvironmentVariable("BLOXOS_TOKEN", $OldToken, "Machine")
    [Environment]::SetEnvironmentVariable("BLOXOS_CA_CERT", $OldCa, "Machine")
    if ($ServiceExisted -and (Test-Path -LiteralPath $AgentExe)) {
        & $AgentExe -install-service | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "Rollback could not restore BloxOSAgent service" }
        if ($ServiceWasRunning) { Start-Service -Name BloxOSAgent }
    }
}

try {
    if ($ServiceExisted) {
        & $AgentExe -uninstall-service | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "Existing service removal failed (exit $LASTEXITCODE)" }
    }
    Move-Item -LiteralPath $StagingExe -Destination $AgentExe -Force
    [Environment]::SetEnvironmentVariable("BLOXOS_HUB", $Hub, "Machine")
    [Environment]::SetEnvironmentVariable("BLOXOS_TOKEN", $Token, "Machine")
    if ($ExpectedCaSha) { [Environment]::SetEnvironmentVariable("BLOXOS_CA_CERT", $CaPath, "Machine") }
    & $AgentExe -install-service
    if ($LASTEXITCODE -ne 0) { throw "Service installation failed (exit $LASTEXITCODE)" }
    Start-Service -Name BloxOSAgent
    Start-Sleep -Seconds 2
    $InstalledService = Get-Service -Name BloxOSAgent -ErrorAction SilentlyContinue
    if (-not $InstalledService -or $InstalledService.Status -ne 'Running') { throw "BloxOSAgent service did not reach Running" }
    if ($BackupExe) { Remove-Item -LiteralPath $BackupExe -Force -ErrorAction SilentlyContinue }
    Write-Host "BloxOS agent installed and running."
} catch {
    $Failure = $_
    try { Restore-PreviousInstall } catch { Write-Warning "Rollback failed: $_" }
    throw $Failure
} finally {
    Remove-Item -LiteralPath $StagingExe -Force -ErrorAction SilentlyContinue
}`

// handleWindowsInstallScript serves the reviewed installer as a file. The
// authenticated paste block downloads it only after establishing CA trust;
// it is never executed directly from a network pipe.
func handleWindowsInstallScript(c echo.Context) error {
	recomputeBinaryFor("windows")
	windowsState := currentAgentBinaryState("windows")
	if windowsState.Error != "" || windowsState.SHA == "" {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "Windows agent binary unavailable: " + windowsState.Error,
		})
	}
	script := strings.ReplaceAll(windowsInstallerScript, "__EXPECTED_AGENT_SHA256__", windowsState.SHA)
	script = strings.ReplaceAll(script, "__UPDATE_PUBKEY__", updateSigningPublicKeyBase64())
	return c.String(http.StatusOK, script)
}
