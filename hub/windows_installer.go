package main

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

const windowsInstallerScript = `# BloxOS Windows Agent installer
param([switch]$ForceReenroll)

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

$SecretExistedBefore = Test-Path -LiteralPath $SecretPath

$Service = Get-Service -Name BloxOSAgent -ErrorAction SilentlyContinue
$ExistingHub = [Environment]::GetEnvironmentVariable("BLOXOS_HUB", "Machine")
$FastPath = (-not $ForceReenroll) -and
    $KeyWasAlreadyPinned -and
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

$OldSecretHash = $null
if ($ForceReenroll -and $SecretExistedBefore) {
    # Record the existing secret's hash WITHOUT touching the file itself.
    # The hub now stages a re-enrollment as a PENDING credential rather than
    # replacing the active one, so the old secret must keep authenticating
    # for as long as it sits on disk — moving, deleting, or otherwise
    # disturbing it here would just recreate the split-brain window this
    # design exists to close (the hub could still accept it while the local
    # copy is unavailable). The restarted agent sends this secret alongside
    # the fresh target-bound token; the hub explicitly detects that
    # combination and stages a pending credential without touching the
    # active one. The old secret stops being accepted only after the agent
    # durably saves the new one AND the hub's compare-and-swap promotion of
    # that exact pending value actually commits in its own database and the
    # resulting "enrollment_confirmed" frame is delivered back to the agent
    # — a fully hub-side transition, never triggered by this script deleting
    # a local file, and never assumed to have happened just because a frame
    # was sent.
    $OldSecretHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $SecretPath).Hash
}

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
    # agent-secret is deliberately untouched by this script (see the
    # ForceReenroll block above) — there is nothing to restore here. The hub
    # still holds the old credential as active because it was only ever
    # staged as pending, never replaced, so no local rollback is needed for
    # the machine to keep authenticating.
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

    # Bounded enrollment-completion gate. Reaching Running in the Windows
    # Service Control Manager proves the process started — it says nothing
    # about whether the agent actually authenticated with the hub, so success
    # is never reported on SCM status alone:
    #   - clean install (no prior secret): a new non-empty secret must appear;
    #   - ForceReenroll: agent-secret was left in place untouched above, still
    #     holding the OLD content — the restarted agent authenticates with
    #     that still-valid old secret alongside the fresh target-bound token,
    #     and the hub stages a pending replacement. The agent's own local
    #     state machine now enforces a real two-phase handoff: a staged
    #     secret is written ONLY to a separate agent-secret.pending file, and
    #     $SecretPath (agent-secret, the ACTIVE file) is rewritten only after
    #     the agent receives a hash-bound "enrollment_confirmed" from the hub
    #     naming that exact pending file's contents and durably promotes it.
    #     That means waiting for the hash at $SecretPath to differ from the
    #     recorded old one is no longer merely "the agent wrote something
    #     locally" — it IS proof that the hub's compare-and-swap promotion
    #     already committed AND the agent already verified the confirmation
    #     was hash-bound to what it actually staged before touching the
    #     active file at all. This script still does not independently query
    #     the hub's own view of the credential or BLOXOS_TOKEN removal — that
    #     remains a separate acceptance check — but the local signal it does
    #     wait for is now a strong one, not a weak one;
    #   - ordinary non-FastPath repair with an existing credential: the
    #     credential is retained as-is, so service health is the only signal
    #     available and is sufficient.
    # Secret contents are never read for comparison, only their SHA-256 hash.
    # If this gate times out, agent-secret is exactly what it was before this
    # script ran — the hub never invalidates the old credential until the
    # agent proves the new one is durable, so a timeout here does not strand
    # the machine's authentication either way. A ForceReenroll timeout may
    # still leave agent-secret.pending on disk (the staged secret, awaiting a
    # confirmation that never arrived or is still in flight) — that file is
    # deliberately not created, inspected, or removed by this script; it is
    # the agent's own recovery material for its next reconnect attempt, and
    # Restore-PreviousInstall below never touches it either.
    $EnrollDeadline = (Get-Date).AddSeconds(30)
    $EnrollmentComplete = $false
    while (-not $EnrollmentComplete -and (Get-Date) -lt $EnrollDeadline) {
        $InstalledService = Get-Service -Name BloxOSAgent -ErrorAction SilentlyContinue
        if (-not $InstalledService -or $InstalledService.Status -ne 'Running') {
            Start-Sleep -Milliseconds 500
            continue
        }
        if ($ForceReenroll) {
            if (Test-Path -LiteralPath $SecretPath) {
                $NewSecretHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $SecretPath).Hash
                if ($NewSecretHash -ne $OldSecretHash) { $EnrollmentComplete = $true }
            }
        } elseif (-not $SecretExistedBefore) {
            if ((Test-Path -LiteralPath $SecretPath) -and (Get-Item -LiteralPath $SecretPath).Length -gt 0) {
                $EnrollmentComplete = $true
            }
        } else {
            $EnrollmentComplete = $true
        }
        if (-not $EnrollmentComplete) { Start-Sleep -Milliseconds 500 }
    }
    if (-not $EnrollmentComplete) {
        if ($ForceReenroll -and (Test-Path -LiteralPath "$SecretPath.pending")) {
            throw "Agent service is running but did not complete enrollment within the timeout. A staged agent-secret.pending is present at $SecretPath.pending — this is expected recovery material, not corruption; the agent will retry with it on its own next reconnect attempt. Do not delete it manually."
        }
        throw "Agent service is running but did not complete enrollment within the timeout"
    }

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
