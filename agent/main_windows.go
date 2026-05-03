//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"

	"golang.org/x/sys/windows/registry"
)

// buildPlatformCommand returns the OS-specific *exec.Cmd for an incoming
// hub command. Windows uses sc.exe / docker.exe / shutdown.exe. There is
// no `sudo` — the agent runs as a Windows service under LocalSystem.
func buildPlatformCommand(cmdType, target string) (*exec.Cmd, error) {
	switch cmdType {
	case "restart_service":
		// sc.exe doesn't have a built-in restart; chain stop+start in cmd.exe.
		return exec.Command("cmd.exe", "/C", fmt.Sprintf("sc.exe stop %s & sc.exe start %s", target, target)), nil
	case "stop_service":
		return exec.Command("sc.exe", "stop", target), nil
	case "start_service":
		return exec.Command("sc.exe", "start", target), nil
	case "restart_container":
		return exec.Command("docker.exe", "restart", target), nil
	case "start_container":
		return exec.Command("docker.exe", "start", target), nil
	case "reboot":
		return exec.Command("shutdown.exe", "/r", "/t", "0", "/f"), nil
	case "shutdown":
		return exec.Command("shutdown.exe", "/s", "/t", "0", "/f"), nil
	default:
		return nil, fmt.Errorf("unknown command type: %s", cmdType)
	}
}

// platformSupportsTerminal reports whether the current platform supports
// PTY-based terminal sessions. Windows: not yet implemented.
func platformSupportsTerminal() bool { return false }

// handleStartTerminalPlatform is a no-op on Windows. The hub-side gating
// in main.go already responds to start_terminal commands with an error.
func handleStartTerminalPlatform(cmd Command, rawMsg []byte) {
	// Intentionally no-op; gated by platformSupportsTerminal.
}

// platformInstallService / platformUninstallService delegate to the
// Windows SCM-aware installer in service_windows.go.
func platformInstallService() error   { return installService() }
func platformUninstallService() error { return uninstallService() }

// runPlatformAgent is the Windows entry point. It detects whether we're
// running under SCM and dispatches accordingly.
func runPlatformAgent() {
	runWindowsService()
}

// wipeMachineTokenIfBootstrapped removes BLOXOS_TOKEN from the persistent
// machine environment (HKLM\SYSTEM\CurrentControlSet\Control\Session Manager\
// Environment) once the agent has a durable credential on disk. No-op if the
// env var is unset or no credential file exists. Best-effort: failures are
// logged but do not affect agent operation.
//
// Why: install.ps1 persists BLOXOS_TOKEN to machine env so the agent service
// can read it on first start. After successful enrollment the agent has a
// durable secret in credentialFilePath() and the token is no longer needed.
// Persisting it indefinitely (a) is a small security smell and (b) lets a
// re-install accidentally inherit a now-stale token.
func wipeMachineTokenIfBootstrapped() {
	if os.Getenv("BLOXOS_TOKEN") == "" {
		return
	}
	info, err := os.Stat(credentialFilePath())
	if err != nil || info.Size() == 0 {
		return
	}
	key, err := registry.OpenKey(
		registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Control\Session Manager\Environment`,
		registry.SET_VALUE,
	)
	if err != nil {
		log.Printf("token-wipe: open registry: %v", err)
		return
	}
	defer key.Close()
	if err := key.DeleteValue("BLOXOS_TOKEN"); err != nil {
		log.Printf("token-wipe: delete BLOXOS_TOKEN: %v", err)
		return
	}
	log.Printf("token-wipe: removed BLOXOS_TOKEN from machine env (durable secret in place at %s)", credentialFilePath())
}
