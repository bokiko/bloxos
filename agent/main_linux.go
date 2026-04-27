//go:build linux

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"strconv"
	"syscall"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

// buildPlatformCommand returns the OS-specific *exec.Cmd for an incoming
// hub command. Linux uses sudo+systemd+docker.
func buildPlatformCommand(cmdType, target string) (*exec.Cmd, error) {
	switch cmdType {
	case "restart_service":
		return exec.Command("sudo", "systemctl", "restart", target), nil
	case "stop_service":
		return exec.Command("sudo", "systemctl", "stop", target), nil
	case "start_service":
		return exec.Command("sudo", "systemctl", "start", target), nil
	case "restart_container":
		return exec.Command("sudo", "docker", "restart", target), nil
	case "start_container":
		return exec.Command("sudo", "docker", "start", target), nil
	case "reboot":
		return exec.Command("sudo", "reboot"), nil
	case "shutdown":
		return exec.Command("sudo", "shutdown", "-h", "now"), nil
	default:
		return nil, fmt.Errorf("unknown command type: %s", cmdType)
	}
}

// platformSupportsTerminal reports whether the current platform supports
// PTY-based terminal sessions.
func platformSupportsTerminal() bool { return true }

// handleStartTerminalPlatform is the platform-aware entry point for
// start_terminal commands.
func handleStartTerminalPlatform(cmd Command, rawMsg []byte) {
	handleStartTerminal(cmd, rawMsg)
}

// platformInstallService is a no-op on Linux. Linux uses systemd via
// the install.sh shell script, not an in-binary installer.
func platformInstallService() error {
	return fmt.Errorf("install-service is Windows-only")
}

func platformUninstallService() error {
	return fmt.Errorf("uninstall-service is Windows-only")
}

// runPlatformAgent runs the agent in foreground mode on Linux. The systemd
// unit invokes the binary directly, so all we need is the connection loop
// plus signal-driven shutdown.
func runPlatformAgent() {
	machineID := getMachineID()
	log.Printf("agent starting: machine_id=%s hub=%s", machineID, hubURL)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down")
		os.Exit(0)
	}()

	connectLoop(machineID)
}

// handleStartTerminal spawns a PTY and connects it to the hub via a dedicated WebSocket.
// TerminalCommand extends Command with terminal_token.
type TerminalCommand struct {
	Type          string `json:"type"`
	Target        string `json:"target"`
	ID            string `json:"id"`
	SessionID     string `json:"session_id,omitempty"`
	TerminalToken string `json:"terminal_token,omitempty"`
}

func handleStartTerminal(cmd Command, rawMsg []byte) {
	// Re-parse to get terminal_token field.
	var termCmd TerminalCommand
	json.Unmarshal(rawMsg, &termCmd)

	sessionID := termCmd.SessionID
	if sessionID == "" {
		log.Printf("start_terminal: missing session_id")
		return
	}
	terminalToken := termCmd.TerminalToken
	if terminalToken == "" {
		log.Printf("start_terminal: missing terminal_token")
		return
	}
	log.Printf("starting terminal session: %s", sessionID)

	// Derive the hub HTTP host from the agent's hub WebSocket URL.
	u, err := url.Parse(hubURL)
	if err != nil {
		log.Printf("terminal: invalid hub URL: %v", err)
		return
	}
	// Build terminal relay URL with terminal_token for auth (Finding #4).
	// Derive scheme from hubURL so terminal connections go through Caddy too.
	wsScheme := "ws"
	if u.Scheme == "wss" {
		wsScheme = "wss"
	}
	termURL := fmt.Sprintf("%s://%s/ws/terminal/%s?role=agent&terminal_token=%s", wsScheme, u.Host, sessionID, url.QueryEscape(terminalToken))

	// Spawn bash PTY as non-root user for security.
	// Uses BLOXOS_TERMINAL_USER env var, or falls back to the owner of the
	// agent binary's parent directory, or "bokiko", or current user.
	bashCmd := exec.Command("bash", "-l")
	termUser := resolveTerminalUser()
	if termUser != nil && termUser.Uid != "0" {
		uid, _ := strconv.ParseUint(termUser.Uid, 10, 32)
		gid, _ := strconv.ParseUint(termUser.Gid, 10, 32)
		bashCmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{
				Uid: uint32(uid),
				Gid: uint32(gid),
			},
		}
		bashCmd.Dir = termUser.HomeDir
		bashCmd.Env = []string{
			"TERM=xterm-256color",
			"HOME=" + termUser.HomeDir,
			"USER=" + termUser.Username,
			"SHELL=/bin/bash",
			fmt.Sprintf("PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"),
		}
		log.Printf("terminal: spawning shell as user %s (uid=%s)", termUser.Username, termUser.Uid)
	} else {
		bashCmd.Env = append(os.Environ(), "TERM=xterm-256color")
		log.Printf("terminal: WARNING spawning shell as current user (root)")
	}
	ptmx, err := pty.Start(bashCmd)
	if err != nil {
		log.Printf("terminal: pty.Start failed: %v", err)
		return
	}
	defer func() {
		ptmx.Close()
		_ = bashCmd.Process.Kill()
		_, _ = bashCmd.Process.Wait()
		log.Printf("terminal session %s: PTY cleaned up", sessionID)
	}()

	// Set initial size.
	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})

	// Connect to hub terminal relay.
	log.Printf("terminal: connecting to %s", termURL)
	dialer, err := websocketDialerFor(termURL)
	if err != nil {
		log.Printf("terminal: build websocket dialer failed: %v", err)
		return
	}
	ws, _, err := dialer.Dial(termURL, nil)
	if err != nil {
		log.Printf("terminal: dial hub failed: %v", err)
		return
	}
	defer ws.Close()
	log.Printf("terminal session %s: connected to hub relay", sessionID)

	done := make(chan struct{})

	// PTY stdout -> WebSocket (binary).
	go func() {
		defer func() {
			select {
			case <-done:
			default:
				close(done)
			}
		}()
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				if werr := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					log.Printf("terminal %s: ws write error: %v", sessionID, werr)
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					log.Printf("terminal %s: pty read error: %v", sessionID, err)
				}
				return
			}
		}
	}()

	// WebSocket -> PTY stdin. Also handle resize messages.
	go func() {
		defer func() {
			select {
			case <-done:
			default:
				close(done)
			}
		}()
		for {
			msgType, msg, err := ws.ReadMessage()
			if err != nil {
				log.Printf("terminal %s: ws read error: %v", sessionID, err)
				return
			}

			// Check if it's a text message that might be a resize command.
			if msgType == websocket.TextMessage {
				var resize TerminalResize
				if json.Unmarshal(msg, &resize) == nil && resize.Type == "resize" {
					if resize.Cols > 0 && resize.Rows > 0 {
						_ = pty.Setsize(ptmx, &pty.Winsize{
							Rows: resize.Rows,
							Cols: resize.Cols,
						})
						log.Printf("terminal %s: resized to %dx%d", sessionID, resize.Cols, resize.Rows)
					}
					continue
				}
			}

			// Otherwise write to PTY stdin.
			if _, err := ptmx.Write(msg); err != nil {
				log.Printf("terminal %s: pty write error: %v", sessionID, err)
				return
			}
		}
	}()

	// Wait for either direction to finish, or for the bash process to exit.
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- bashCmd.Wait()
	}()

	select {
	case <-done:
		log.Printf("terminal session %s: WebSocket/PTY loop ended", sessionID)
	case err := <-waitCh:
		log.Printf("terminal session %s: bash exited: %v", sessionID, err)
	}
}

// resolveTerminalUser determines which user to run terminal sessions as.
// Priority: BLOXOS_TERMINAL_USER env var > "bokiko" > current user.
func resolveTerminalUser() *user.User {
	if envUser := os.Getenv("BLOXOS_TERMINAL_USER"); envUser != "" {
		if u, err := user.Lookup(envUser); err == nil {
			return u
		}
		log.Printf("terminal: BLOXOS_TERMINAL_USER=%s not found, falling back", envUser)
	}
	// Try common non-root user.
	for _, name := range []string{"bokiko", "ubuntu", "admin"} {
		if u, err := user.Lookup(name); err == nil {
			return u
		}
	}
	// Fall back to current user (may be root).
	if u, err := user.Current(); err == nil {
		return u
	}
	return nil
}
