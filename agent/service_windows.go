//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

/* ============================================================================
 * Phase 9 — Windows SCM integration
 *
 * Three roles, one binary:
 *   1. -install-service: register with SCM, configure recovery actions,
 *      register an event log source, and exit.
 *   2. -uninstall-service: stop, delete, deregister event log, exit.
 *   3. (no flag): runWindowsService dispatches to either the SCM run
 *      loop (if launched by services.msc) or interactive mode (if
 *      launched by hand from a console).
 * ============================================================================ */

const (
	windowsServiceName = "BloxOSAgent"
	windowsServiceDesc = "BloxOS Fleet Management Agent"
)

// bloxosService implements svc.Handler for SCM dispatch.
type bloxosService struct{}

// Execute is the SCM-driven main loop. We acknowledge state changes and
// keep the connection loop alive in a goroutine. Stop / Shutdown trigger
// a graceful exit.
func (m *bloxosService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	// Apply any pending self-update before we open the network. If a
	// helper is needed, applyPendingUpdate calls os.Exit and we never
	// return here.
	applyPendingUpdate()

	stopCh := make(chan struct{})
	go func() {
		machineID := getMachineID()
		log.Printf("agent (svc) starting: machine_id=%s hub=%s", machineID, hubURL)
		connectLoopWithStop(machineID, stopCh)
	}()

	changes <- svc.Status{State: svc.Running, Accepts: accepted}
loop:
	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
				time.Sleep(100 * time.Millisecond)
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				log.Printf("agent (svc): stop/shutdown received")
				break loop
			default:
				log.Printf("agent (svc): unexpected control request #%d", c)
			}
		}
	}
	close(stopCh)
	changes <- svc.Status{State: svc.StopPending}
	return false, 0
}

// runWindowsService is the platform entry point. It detects whether
// SCM started us and dispatches accordingly.
func runWindowsService() {
	isService, err := svc.IsWindowsService()
	if err != nil {
		log.Fatalf("svc.IsWindowsService failed: %v", err)
	}
	if isService {
		// Run under SCM.
		if err := svc.Run(windowsServiceName, &bloxosService{}); err != nil {
			log.Fatalf("svc.Run %s: %v", windowsServiceName, err)
		}
		return
	}

	// Interactive mode (developer running ./bloxos-agent.exe by hand
	// from a PowerShell window). Behave like the Linux path: apply
	// any pending update, then run connectLoop in the foreground.
	applyPendingUpdate()

	machineID := getMachineID()
	log.Printf("agent (interactive) starting: machine_id=%s hub=%s", machineID, hubURL)
	connectLoop(machineID)
}

// connectLoopWithStop is a thin wrapper around connectLoop. The stopCh
// is currently unused — connectLoop runs forever and the goroutine
// is killed when the service exits. PHASE9-NOTE: this wrapper is a
// placeholder for future graceful-shutdown work; the spec explicitly
// noted it as such, do not remove it.
func connectLoopWithStop(machineID string, stopCh <-chan struct{}) {
	connectLoop(machineID)
}

// installService registers the agent binary with SCM. Idempotent —
// fails cleanly if a service with the same name already exists.
func installService() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return fmt.Errorf("absolute path: %w", err)
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to SCM: %w", err)
	}
	defer m.Disconnect()

	// If the service exists, bail with a friendly error.
	if s, err := m.OpenService(windowsServiceName); err == nil {
		s.Close()
		return fmt.Errorf("service %s already installed; run -uninstall-service first", windowsServiceName)
	}

	cfg := mgr.Config{
		DisplayName:      windowsServiceDesc,
		Description:      windowsServiceDesc,
		StartType:        mgr.StartAutomatic,
		ServiceStartName: "", // LocalSystem
	}

	s, err := m.CreateService(windowsServiceName, exe, cfg)
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer s.Close()

	// Recovery actions: restart on first / second / subsequent failures.
	// Reset window: 24h.
	recoveryActions := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
	}
	if err := s.SetRecoveryActions(recoveryActions, uint32(24*time.Hour/time.Second)); err != nil {
		log.Printf("install: WARNING SetRecoveryActions: %v", err)
	}

	// Event log source — best-effort; fails fine if already present.
	if err := eventlog.InstallAsEventCreate(windowsServiceName, eventlog.Error|eventlog.Warning|eventlog.Info); err != nil {
		// "already exists" is fine.
		log.Printf("install: event log source: %v", err)
	}

	log.Printf("install: service %s installed (binary=%s)", windowsServiceName, exe)
	return nil
}

// uninstallService stops the service if running, deletes it, and
// removes the event log source.
func uninstallService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(windowsServiceName)
	if err != nil {
		return fmt.Errorf("open service: %w", err)
	}
	defer s.Close()

	// Try to stop if running.
	status, err := s.Query()
	if err == nil && status.State != svc.Stopped {
		if _, err := s.Control(svc.Stop); err != nil {
			log.Printf("uninstall: WARNING stop control failed: %v", err)
		} else {
			// Wait up to 30 seconds for stop.
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				st, err := s.Query()
				if err != nil {
					break
				}
				if st.State == svc.Stopped {
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
		}
	}

	if err := s.Delete(); err != nil {
		return fmt.Errorf("delete service: %w", err)
	}

	if err := eventlog.Remove(windowsServiceName); err != nil {
		log.Printf("uninstall: event log remove: %v", err)
	}

	log.Printf("uninstall: service %s removed", windowsServiceName)
	return nil
}

// suppressUnused references the windows package so go vet is content
// even on builds that don't otherwise reach into it. Hot path skips it.
var _ = windows.STATUS_PENDING
