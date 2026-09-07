package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// agentCommandTimeout bounds how long a single remote command may run before
// it is killed. Without a bound, a wedged systemctl/docker call (stuck daemon)
// would block its handler goroutine forever.
const agentCommandTimeout = 30 * time.Second

// collectorTimeout bounds how long the metrics tick waits for any external
// command it runs (nvidia-smi, docker, systemctl, dmidecode, lspci,
// PowerShell). Those run on the connection loop itself, so an unbounded one
// stalled pings, let the hub close the socket after its idle timeout, and
// left the loop stuck inside the collector so the agent never reconnected:
// remote reboot was unreachable exactly when it was needed. Ten seconds per
// command keeps the worst tick well under the hub's 90 s idle timeout.
// Package-level so tests can shorten it. gopsutil's in-process readers are
// not covered by this bound.
var collectorTimeout = 10 * time.Second

// collectorInflight holds, per command, whether a previous invocation is
// still outstanding. A child in uninterruptible sleep (D state) ignores
// SIGKILL and its Wait never returns, so the runner cannot reclaim it; what
// it can do is refuse to start another copy each tick, so a wedged command
// costs exactly one goroutine and one process until the kernel releases it.
var (
	collectorInflightMu sync.Mutex
	collectorInflight   = map[string]bool{}
)

// errCollectorBusy is returned when the same command is still outstanding
// from an earlier tick.
var errCollectorBusy = fmt.Errorf("collector still running from an earlier tick")

// runBoundedBeforeWaitHook, when set by a test, runs after the worker has
// been started and before the caller waits on it, so a test can pin the
// ordering in which the result and the deadline become ready.
var runBoundedBeforeWaitHook func(ctx context.Context)

// runCollector runs argv and returns its stdout, holding the caller for at
// most collectorTimeout. On timeout the child is sent the kill signal (as a
// process group where configureCommand supports it) and an error is returned
// immediately; the wait continues in the background, and no second copy of
// the same command is started until it finishes. Callers treat any error as
// "data unavailable", never as a zero reading.
func runCollector(argv ...string) ([]byte, error) {
	return runBounded(strings.Join(argv, " "), collectorTimeout, func(ctx context.Context) ([]byte, error) {
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		configureCommand(cmd)
		cmd.WaitDelay = 2 * time.Second
		return cmd.Output()
	})
}

// runBounded runs fn once per key at a time and returns within timeout even
// if fn never does. The deadline is enforced by waiting on a channel rather
// than on fn, because fn may be blocked in a wait that cannot return.
func runBounded(key string, timeout time.Duration, fn func(ctx context.Context) ([]byte, error)) ([]byte, error) {
	collectorInflightMu.Lock()
	if collectorInflight[key] {
		collectorInflightMu.Unlock()
		return nil, errCollectorBusy
	}
	collectorInflight[key] = true
	collectorInflightMu.Unlock()

	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	// cancel belongs to the caller, not the worker: if the worker cancelled
	// after publishing its result, ctx.Done and done would both be ready and
	// the select below could report a timeout for a command that succeeded.
	defer cancel()
	go func() {
		out, err := fn(ctx)
		// Clear the gate before publishing so that a caller who has just
		// received this result can run the same command again immediately.
		collectorInflightMu.Lock()
		delete(collectorInflight, key)
		collectorInflightMu.Unlock()
		done <- result{out, err}
	}()
	if runBoundedBeforeWaitHook != nil {
		runBoundedBeforeWaitHook(ctx)
	}
	select {
	case r := <-done:
		return r.out, r.err
	case <-ctx.Done():
		// The deadline expired. A result that landed at the same instant
		// still wins; otherwise the goroutine keeps waiting (and holds the
		// inflight mark) until fn returns.
		select {
		case r := <-done:
			return r.out, r.err
		default:
		}
		return nil, fmt.Errorf("%s: timed out after %s", strings.Fields(key)[0], timeout)
	}
}

// commandPlan returns the argv steps to execute for a command on this host.
func commandPlan(cmdType, target string) ([][]string, error) {
	return commandPlanForIdentity(runtime.GOOS, runningAsRoot(), cmdType, target)
}

// runningAsRoot reports whether this process already has root, in which case
// the Linux plans must not go through sudo: the generated systemd unit runs
// the agent as root, and hosts onboarded as root may not have sudo installed
// at all (the install path no longer requires it), so a sudo prefix there
// fails every control with "executable file not found".
func runningAsRoot() bool {
	return runtime.GOOS != "windows" && os.Geteuid() == 0
}

// commandPlanFor returns the unprivileged (sudo-prefixed on Linux) plan for a
// command type on the given GOOS. See commandPlanForIdentity.
func commandPlanFor(goos, cmdType, target string) ([][]string, error) {
	return commandPlanForIdentity(goos, false, cmdType, target)
}

// errServiceViaSCM is returned for Windows service commands, which are
// executed through the service control manager (service_control_windows.go)
// rather than as argv steps: sc.exe stop returns while the service is still
// STOP_PENDING, so "sc.exe stop; sc.exe start" raced the stop.
var errServiceViaSCM = fmt.Errorf("windows service commands are executed through the service control manager, not argv")

// commandPlanForIdentity returns the argv steps for a command type on the
// given GOOS, dropping the sudo prefix on Linux when the caller already runs
// as root. It is pure argv construction (no OS-specific calls), so it is
// unit-testable on any platform. Every step is a discrete argv slice executed
// without a shell, so a validated target can never be interpreted as shell
// syntax.
func commandPlanForIdentity(goos string, root bool, cmdType, target string) ([][]string, error) {
	if goos == "windows" {
		switch cmdType {
		case "restart_service", "stop_service", "start_service":
			return nil, errServiceViaSCM
		case "restart_container":
			return [][]string{{"docker.exe", "restart", target}}, nil
		case "start_container":
			return [][]string{{"docker.exe", "start", target}}, nil
		case "reboot":
			return [][]string{{"shutdown.exe", "/r", "/t", "0", "/f"}}, nil
		case "shutdown":
			return [][]string{{"shutdown.exe", "/s", "/t", "0", "/f"}}, nil
		default:
			return nil, fmt.Errorf("unknown command type: %s", cmdType)
		}
	}

	var argv []string
	switch cmdType {
	case "restart_service":
		argv = []string{"systemctl", "restart", target}
	case "stop_service":
		argv = []string{"systemctl", "stop", target}
	case "start_service":
		argv = []string{"systemctl", "start", target}
	case "restart_container":
		argv = []string{"docker", "restart", target}
	case "start_container":
		argv = []string{"docker", "start", target}
	case "reboot":
		argv = []string{"reboot"}
	case "shutdown":
		argv = []string{"shutdown", "-h", "now"}
	default:
		return nil, fmt.Errorf("unknown command type: %s", cmdType)
	}
	if !root {
		argv = append([]string{"sudo"}, argv...)
	}
	return [][]string{argv}, nil
}

// runCommandPlan executes each step's argv in order under ctx, concatenating
// combined stdout/stderr. It stops at the first failing step. Each command is
// context-bounded and (on platforms where configureCommand sets it up) killed
// as a process group when the context expires, so a hung child cannot outlive
// the timeout.
func runCommandPlan(ctx context.Context, plan [][]string) ([]byte, error) {
	var combined []byte
	for _, step := range plan {
		if len(step) == 0 {
			continue
		}
		cmd := exec.CommandContext(ctx, step[0], step[1:]...)
		configureCommand(cmd)
		out, err := cmd.CombinedOutput()
		combined = append(combined, out...)
		if err != nil {
			return combined, err
		}
	}
	return combined, nil
}
