package main

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestCommandPlanForWindowsServiceCommandsGoThroughSCM: Windows service
// commands are not argv plans any more. "sc.exe stop; sc.exe start" returned
// while the service was still STOP_PENDING and raced the start, so they are
// executed through the service control manager (service_control_windows.go);
// the plan builder refuses them so no caller can fall back to the old race.
func TestCommandPlanForWindowsServiceCommandsGoThroughSCM(t *testing.T) {
	for _, cmdType := range []string{"restart_service", "stop_service", "start_service"} {
		plan, err := commandPlanFor("windows", cmdType, "My-Svc.1")
		if err != errServiceViaSCM {
			t.Fatalf("%s: err=%v plan=%v, want errServiceViaSCM", cmdType, err, plan)
		}
	}
	// Non-service commands are still discrete argv with no shell.
	plan, err := commandPlanFor("windows", "restart_container", "web-1")
	if err != nil || !equalPlan(plan, [][]string{{"docker.exe", "restart", "web-1"}}) {
		t.Fatalf("windows restart_container plan = %v err=%v", plan, err)
	}
}

// TestCommandPlanLinuxRootDropsSudo: the generated systemd unit runs the
// agent as root and root-onboarded hosts need not have sudo installed, so a
// root agent must invoke systemctl/docker/reboot directly. Unprivileged
// (developer) runs keep the sudo prefix.
func TestCommandPlanLinuxRootDropsSudo(t *testing.T) {
	cases := map[string][]string{
		"restart_service":   {"systemctl", "restart", "nginx"},
		"stop_service":      {"systemctl", "stop", "nginx"},
		"start_service":     {"systemctl", "start", "nginx"},
		"restart_container": {"docker", "restart", "nginx"},
		"start_container":   {"docker", "start", "nginx"},
		"reboot":            {"reboot"},
		"shutdown":          {"shutdown", "-h", "now"},
	}
	for cmdType, bare := range cases {
		asRoot, err := commandPlanForIdentity("linux", true, cmdType, "nginx")
		if err != nil || !equalPlan(asRoot, [][]string{bare}) {
			t.Fatalf("%s as root: plan=%v err=%v, want %v", cmdType, asRoot, err, bare)
		}
		unprivileged, err := commandPlanForIdentity("linux", false, cmdType, "nginx")
		if err != nil || !equalPlan(unprivileged, [][]string{append([]string{"sudo"}, bare...)}) {
			t.Fatalf("%s unprivileged: plan=%v err=%v, want sudo prefix", cmdType, unprivileged, err)
		}
	}
	if _, err := commandPlanForIdentity("linux", true, "definitely_not_a_command", "x"); err == nil {
		t.Fatal("unknown command must be rejected regardless of identity")
	}
}

// TestCommandPlanForLinuxRestartService locks in the Linux argv shape.
func TestCommandPlanForLinuxRestartService(t *testing.T) {
	plan, err := commandPlanFor("linux", "restart_service", "nginx")
	if err != nil {
		t.Fatalf("commandPlanFor: %v", err)
	}
	want := [][]string{{"sudo", "systemctl", "restart", "nginx"}}
	if !equalPlan(plan, want) {
		t.Fatalf("linux restart_service plan = %v, want %v", plan, want)
	}
}

func TestCommandPlanForUnknown(t *testing.T) {
	if _, err := commandPlanFor("linux", "definitely_not_a_command", "x"); err == nil {
		t.Fatal("expected error for unknown command type")
	}
}

// TestRunCommandPlanTimeout proves a long-running command is killed and the
// call unwinds promptly instead of hanging forever.
func TestRunCommandPlanTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses unix `sleep`")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := runCommandPlan(ctx, [][]string{{"sleep", "10"}})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("expected context deadline exceeded, got %v", ctx.Err())
	}
	if elapsed > 3*time.Second {
		t.Fatalf("command was not killed promptly: returned after %v", elapsed)
	}
}

// TestRunCommandPlanRunsStepsInOrder proves a multi-step plan (e.g. the Windows
// stop+start) runs every step and concatenates output.
func TestRunCommandPlanRunsStepsInOrder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses unix `echo`")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := runCommandPlan(ctx, [][]string{{"echo", "one"}, {"echo", "two"}})
	if err != nil {
		t.Fatalf("runCommandPlan: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "one") || !strings.Contains(s, "two") {
		t.Fatalf("expected output from both steps, got %q", s)
	}
}

// TestRunCommandPlanStopsOnError proves a failing step aborts the remaining
// steps.
func TestRunCommandPlanStopsOnError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses unix `false`/`echo`")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := runCommandPlan(ctx, [][]string{{"false"}, {"echo", "after"}})
	if err == nil {
		t.Fatal("expected error from failing first step")
	}
	if strings.Contains(string(out), "after") {
		t.Fatalf("second step ran after a failing first step: %q", string(out))
	}
}

func equalPlan(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}
