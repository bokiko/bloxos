//go:build windows

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

/* ============================================================================
 * Service control through the SCM
 *
 * restart_service used to be two sc.exe calls in a row. sc.exe stop only
 * *requests* a stop and returns while the service is still STOP_PENDING, so
 * the immediate sc.exe start raced the stop: a slow-stopping service could
 * end up stopped after a requested restart. This file drives the service
 * control manager directly: request the stop, poll until STOPPED (bounded by
 * the command context), then start and wait for RUNNING.
 *
 * Restarting the agent's own service cannot be done from inside the service
 * process: the stop handler exits the process that would issue the start.
 * That case spawns a detached copy of this executable running
 * runRestartServiceHelper, which performs the same stop/wait/start from
 * outside once the command response has been written.
 * ============================================================================ */

// scmPollInterval is how often a pending state is re-queried.
const scmPollInterval = 250 * time.Millisecond

// restartHelperTimeout bounds the detached self-restart helper.
const restartHelperTimeout = 90 * time.Second

// restartHelperDelay gives the parent time to write its command response
// before the helper asks the SCM to stop it.
const restartHelperDelay = 1500 * time.Millisecond

// platformServiceCommand executes the service commands through the SCM.
// handled is false for every other command type, which then falls through
// to the argv plan.
func platformServiceCommand(ctx context.Context, cmdType, target string) ([]byte, bool, error) {
	switch cmdType {
	case "restart_service", "stop_service", "start_service":
	default:
		return nil, false, nil
	}
	if cmdType == "restart_service" && strings.EqualFold(target, windowsServiceName) {
		out, err := scheduleSelfRestart()
		return out, true, err
	}
	out, err := controlService(ctx, cmdType, target)
	return out, true, err
}

// controlService opens target and performs cmdType on it, waiting for the
// resulting state within ctx. The returned text is the command output shown
// to the operator.
func controlService(ctx context.Context, cmdType, target string) ([]byte, error) {
	m, err := mgr.Connect()
	if err != nil {
		return nil, fmt.Errorf("connect to service control manager: %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(target)
	if err != nil {
		return nil, fmt.Errorf("open service %s: %w", target, err)
	}
	defer s.Close()

	var out strings.Builder
	switch cmdType {
	case "stop_service":
		err = stopServiceAndWait(ctx, s, target, &out)
	case "start_service":
		err = startServiceAndWait(ctx, s, target, &out)
	case "restart_service":
		if err = stopServiceAndWait(ctx, s, target, &out); err == nil {
			err = startServiceAndWait(ctx, s, target, &out)
		}
	default:
		err = fmt.Errorf("unknown service command %s", cmdType)
	}
	return []byte(out.String()), err
}

// stopServiceAndWait requests a stop unless one is already in progress and
// waits until the service reports STOPPED.
func stopServiceAndWait(ctx context.Context, s *mgr.Service, name string, out *strings.Builder) error {
	st, err := s.Query()
	if err != nil {
		return fmt.Errorf("query %s: %w", name, err)
	}
	switch st.State {
	case svc.Stopped:
		fmt.Fprintf(out, "%s: already stopped\n", name)
		return nil
	case svc.StopPending:
		fmt.Fprintf(out, "%s: stop already pending\n", name)
	default:
		if _, err := s.Control(svc.Stop); err != nil {
			return fmt.Errorf("stop %s: %w", name, err)
		}
		fmt.Fprintf(out, "%s: stop requested\n", name)
	}
	if err := waitForState(ctx, s, name, svc.Stopped); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: stopped\n", name)
	return nil
}

// startServiceAndWait starts the service unless it is already running or
// starting and waits until it reports RUNNING.
func startServiceAndWait(ctx context.Context, s *mgr.Service, name string, out *strings.Builder) error {
	st, err := s.Query()
	if err != nil {
		return fmt.Errorf("query %s: %w", name, err)
	}
	switch st.State {
	case svc.Running:
		fmt.Fprintf(out, "%s: already running\n", name)
		return nil
	case svc.StartPending:
		fmt.Fprintf(out, "%s: start already pending\n", name)
	default:
		if err := s.Start(); err != nil {
			return fmt.Errorf("start %s: %w", name, err)
		}
		fmt.Fprintf(out, "%s: start requested\n", name)
	}
	if err := waitForState(ctx, s, name, svc.Running); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: running\n", name)
	return nil
}

// waitForState polls the service until it reports want, ctx expires, or
// (when waiting for RUNNING) the service drops back to STOPPED, which means
// the process exited during startup.
func waitForState(ctx context.Context, s *mgr.Service, name string, want svc.State) error {
	ticker := time.NewTicker(scmPollInterval)
	defer ticker.Stop()
	for {
		st, err := s.Query()
		if err != nil {
			return fmt.Errorf("query %s: %w", name, err)
		}
		if st.State == want {
			return nil
		}
		if want == svc.Running && st.State == svc.Stopped {
			return fmt.Errorf("%s exited before reaching running state", name)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: timed out waiting for %s (state is %s)", name, serviceStateName(want), serviceStateName(st.State))
		case <-ticker.C:
		}
	}
}

func serviceStateName(st svc.State) string {
	switch st {
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "start-pending"
	case svc.StopPending:
		return "stop-pending"
	case svc.Running:
		return "running"
	case svc.ContinuePending:
		return "continue-pending"
	case svc.PausePending:
		return "pause-pending"
	case svc.Paused:
		return "paused"
	}
	return fmt.Sprintf("state-%d", st)
}

// scheduleSelfRestart spawns the detached helper that restarts this service
// from outside. It refuses when the agent is not running under the SCM,
// because there is no service to restart and the helper would fail.
func scheduleSelfRestart() ([]byte, error) {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return nil, fmt.Errorf("restart %s: cannot determine service context: %w", windowsServiceName, err)
	}
	if !isService {
		return nil, fmt.Errorf("restart %s: agent is not running as a service", windowsServiceName)
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("restart %s: locate executable: %w", windowsServiceName, err)
	}
	if err := spawnDetachedProcess(exe, "-restart-service-helper="+windowsServiceName); err != nil {
		return nil, fmt.Errorf("restart %s: spawn helper: %w", windowsServiceName, err)
	}
	return []byte(fmt.Sprintf("%s: restart scheduled via detached helper\n", windowsServiceName)), nil
}

// spawnDetachedProcess starts exe with args outside our console and process
// group so it survives this process exiting when the SCM stops the service.
func spawnDetachedProcess(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
	return cmd.Start()
}

// restartHelperLogPath is where the detached helper records its outcome; it
// has no console and no hub connection, so the file is the only evidence.
func restartHelperLogPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "bloxos-agent.restart-helper.log"
	}
	return exe + ".restart-helper.log"
}

// runRestartServiceHelper is the detached helper's entry point: wait for the
// parent's command response to flush, then stop/wait/start name and record
// the outcome next to the executable.
func runRestartServiceHelper(name string) error {
	logf, err := os.OpenFile(restartHelperLogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		defer logf.Close()
		log.SetOutput(logf)
	}
	log.Printf("restart-helper: restarting %s in %s", name, restartHelperDelay)
	time.Sleep(restartHelperDelay)
	ctx, cancel := context.WithTimeout(context.Background(), restartHelperTimeout)
	defer cancel()
	out, err := controlService(ctx, "restart_service", name)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			log.Printf("restart-helper: %s", line)
		}
	}
	if err != nil {
		log.Printf("restart-helper: FAILED: %v", err)
		return err
	}
	log.Printf("restart-helper: %s restarted", name)
	return nil
}

// platformRestartServiceHelper is the -restart-service-helper flag handler.
func platformRestartServiceHelper(name string) error {
	return runRestartServiceHelper(name)
}
