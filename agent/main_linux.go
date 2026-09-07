//go:build linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"strconv"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

// waitable is what waitOnce coordinates over. Defined as an interface
// so tests can substitute a fake without spawning a real process.
// *exec.Cmd satisfies it via its Wait() method.
type waitable interface {
	Wait() error
}

// waitOnce ensures Wait() is called exactly once on the underlying
// waitable, regardless of how many goroutines race for the result.
// Used by handleStartTerminal where both the cleanup defer and the
// waitCh goroutine would otherwise call cmd.Wait() — calling Wait()
// twice on a *exec.Cmd is undefined per stdlib contract.
type waitOnce struct {
	once sync.Once
	err  error
	cmd  waitable
}

func newWaitOnce(cmd waitable) *waitOnce {
	return &waitOnce{cmd: cmd}
}

func (w *waitOnce) Wait() error {
	w.once.Do(func() {
		w.err = w.cmd.Wait()
	})
	return w.err
}

// lookupGroupIDs returns the supplementary group ids of a user. It is a
// variable so tests can supply groups for accounts that do not exist on
// the test host.
var lookupGroupIDs = func(u *user.User) ([]string, error) { return u.GroupIds() }

// applyTerminalCredentials configures bashCmd to run as termUser with that
// user's uid, primary gid and supplementary groups. Terminals never run as
// the agent's own identity: a nil user or uid 0 is refused, as is any uid,
// gid or group id that does not parse. Previously nil and root fell through
// to "inherit the agent's identity (root)" with only a log warning, and
// Groups were left empty so the shell lost the user's docker/sudo/etc.
// memberships.
func applyTerminalCredentials(bashCmd *exec.Cmd, termUser *user.User) error {
	if termUser == nil {
		return fmt.Errorf("no terminal user resolved")
	}
	if termUser.Uid == "0" {
		return fmt.Errorf("terminal user %q is root; terminals never run as the agent's identity", termUser.Username)
	}
	uid, err := strconv.ParseUint(termUser.Uid, 10, 32)
	if err != nil {
		return fmt.Errorf("parse uid %q for user %q: %w", termUser.Uid, termUser.Username, err)
	}
	gid, err := strconv.ParseUint(termUser.Gid, 10, 32)
	if err != nil {
		return fmt.Errorf("parse gid %q for user %q: %w", termUser.Gid, termUser.Username, err)
	}
	groupIDs, err := lookupGroupIDs(termUser)
	if err != nil {
		return fmt.Errorf("list groups for user %q: %w", termUser.Username, err)
	}
	groups := make([]uint32, 0, len(groupIDs))
	for _, g := range groupIDs {
		id, err := strconv.ParseUint(g, 10, 32)
		if err != nil {
			return fmt.Errorf("parse group id %q for user %q: %w", g, termUser.Username, err)
		}
		groups = append(groups, uint32(id))
	}
	bashCmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid:    uint32(uid),
			Gid:    uint32(gid),
			Groups: groups,
		},
	}
	bashCmd.Dir = termUser.HomeDir
	bashCmd.Env = []string{
		"TERM=xterm-256color",
		"HOME=" + termUser.HomeDir,
		"USER=" + termUser.Username,
		"SHELL=/bin/bash",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	return nil
}

// configureCommand prepares a to-be-run command for bounded execution on Linux.
// It places the command in its own process group and installs a Cancel hook
// that SIGKILLs the whole group when the context expires, so a command that
// forks children (systemctl/docker) is fully torn down on timeout rather than
// leaving orphans. The argv itself comes from commandPlanFor.
func configureCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative PID targets the whole process group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// platformServiceCommand is a no-op on Linux: service commands run through
// the systemctl argv plan. Windows drives the SCM directly instead.
func platformServiceCommand(ctx context.Context, cmdType, target string) ([]byte, bool, error) {
	return nil, false, nil
}

// platformRestartServiceHelper is Windows-only (the detached self-restart
// helper); systemd restarts the Linux agent itself.
func platformRestartServiceHelper(name string) error {
	return fmt.Errorf("restart-service-helper is Windows-only")
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

// wipeMachineTokenIfBootstrapped is a no-op on Linux. BLOXOS_TOKEN is set in
// the systemd unit's Environment= line, not the machine env, so wiping it
// requires editing /etc/systemd/system/bloxos-agent.service and a daemon-
// reload — intentionally invasive and tracked separately. The install.sh
// flow on Linux also doesn't suffer from the bug this guards against on
// Windows: install.sh exchanges the token for a secret before starting the
// service, so a stale token never persists past a successful install.
func wipeMachineTokenIfBootstrapped() {}

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
	// Build terminal relay URL. The terminal token goes in a handshake header
	// (below) rather than the query string so it doesn't leak into proxy access
	// logs (Finding #4). Derive scheme from hubURL so terminal connections go
	// through Caddy too.
	wsScheme := "ws"
	if u.Scheme == "wss" {
		wsScheme = "wss"
	}
	termURL := fmt.Sprintf("%s://%s/ws/terminal/%s?role=agent", wsScheme, u.Host, sessionID)

	// Spawn bash PTY as a non-root user. BLOXOS_TERMINAL_USER names the
	// account; otherwise a common non-root account is used. There is no
	// root fallback: if no usable account exists the session is refused.
	bashCmd := exec.Command("bash", "-l")
	termUser, err := resolveTerminalUser()
	if err != nil {
		log.Printf("terminal: refusing to start session — %v", err)
		return
	}
	if err := applyTerminalCredentials(bashCmd, termUser); err != nil {
		// Fail-closed: refuse the session rather than run bash as the
		// agent's identity (root). Better a visible terminal failure than
		// an escalation.
		log.Printf("terminal: refusing to start session — credential setup failed: %v", err)
		return
	}
	log.Printf("terminal: spawning shell as user %s (uid=%s)", termUser.Username, termUser.Uid)
	ptmx, err := pty.Start(bashCmd)
	if err != nil {
		log.Printf("terminal: pty.Start failed: %v", err)
		return
	}
	// waitOnce coordinates Wait() between the cleanup defer below and
	// the waitCh goroutine further down. Calling cmd.Wait() twice is
	// undefined per stdlib; the coordinator caches the first result.
	waiter := newWaitOnce(bashCmd)
	defer func() {
		ptmx.Close()
		_ = bashCmd.Process.Kill()
		_ = waiter.Wait()
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
	termHeader := http.Header{}
	termHeader.Set("X-Bloxos-Terminal-Token", terminalToken)
	ws, _, err := dialer.Dial(termURL, termHeader)
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
	// Routed through waiter so the cleanup defer's Wait() and this Wait()
	// share the same single underlying call.
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- waiter.Wait()
	}()

	select {
	case <-done:
		log.Printf("terminal session %s: WebSocket/PTY loop ended", sessionID)
	case err := <-waitCh:
		log.Printf("terminal session %s: bash exited: %v", sessionID, err)
	}
}

// resolveTerminalUser determines which user to run terminal sessions as.
// BLOXOS_TERMINAL_USER, when set, must name an existing non-root account;
// a name that does not resolve is an error, not a fallback. When it is
// unset the common non-root accounts are tried. There is never a fallback
// to the agent's own identity: a host with no usable account refuses
// terminals until BLOXOS_TERMINAL_USER is set.
func resolveTerminalUser() (*user.User, error) {
	if envUser := os.Getenv("BLOXOS_TERMINAL_USER"); envUser != "" {
		u, err := user.Lookup(envUser)
		if err != nil {
			return nil, fmt.Errorf("BLOXOS_TERMINAL_USER=%q: %w", envUser, err)
		}
		if u.Uid == "0" {
			return nil, fmt.Errorf("BLOXOS_TERMINAL_USER=%q is root; terminals never run as root", envUser)
		}
		return u, nil
	}
	for _, name := range []string{"bokiko", "ubuntu", "admin"} {
		if u, err := user.Lookup(name); err == nil && u.Uid != "0" {
			return u, nil
		}
	}
	return nil, fmt.Errorf("no non-root terminal user found; set BLOXOS_TERMINAL_USER to an existing non-root account")
}
