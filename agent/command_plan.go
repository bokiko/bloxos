package main

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"
)

// agentCommandTimeout bounds how long a single remote command may run before
// it is killed. Without a bound, a wedged systemctl/docker call (stuck daemon)
// would block its handler goroutine forever.
const agentCommandTimeout = 30 * time.Second

// commandPlan returns the argv steps to execute for a command on this host.
func commandPlan(cmdType, target string) ([][]string, error) {
	return commandPlanFor(runtime.GOOS, cmdType, target)
}

// commandPlanFor returns the argv steps for a command type on the given GOOS.
// It is pure argv construction (no OS-specific calls), so it is unit-testable
// on any platform. Every step is a discrete argv slice executed without a
// shell, so a validated target can never be interpreted as shell syntax.
//
// Windows restart_service is two steps (sc.exe stop, then sc.exe start) run as
// separate argv commands rather than a "cmd.exe /C stop & start" string — no
// shell interpolation, injection-proof by construction.
func commandPlanFor(goos, cmdType, target string) ([][]string, error) {
	if goos == "windows" {
		switch cmdType {
		case "restart_service":
			return [][]string{
				{"sc.exe", "stop", target},
				{"sc.exe", "start", target},
			}, nil
		case "stop_service":
			return [][]string{{"sc.exe", "stop", target}}, nil
		case "start_service":
			return [][]string{{"sc.exe", "start", target}}, nil
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

	switch cmdType {
	case "restart_service":
		return [][]string{{"sudo", "systemctl", "restart", target}}, nil
	case "stop_service":
		return [][]string{{"sudo", "systemctl", "stop", target}}, nil
	case "start_service":
		return [][]string{{"sudo", "systemctl", "start", target}}, nil
	case "restart_container":
		return [][]string{{"sudo", "docker", "restart", target}}, nil
	case "start_container":
		return [][]string{{"sudo", "docker", "start", target}}, nil
	case "reboot":
		return [][]string{{"sudo", "reboot"}}, nil
	case "shutdown":
		return [][]string{{"sudo", "shutdown", "-h", "now"}}, nil
	default:
		return nil, fmt.Errorf("unknown command type: %s", cmdType)
	}
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
