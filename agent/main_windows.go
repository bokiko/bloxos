//go:build windows

package main

import (
	"fmt"
	"os/exec"
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
