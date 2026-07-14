package main

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestCommandPlanForWindowsRestartService locks in item 3: on Windows,
// restart_service is two separate argv commands (sc.exe stop / sc.exe start)
// with no cmd.exe and no shell metacharacters — injection-proof by
// construction. This is pure argv logic, testable on any OS.
func TestCommandPlanForWindowsRestartService(t *testing.T) {
	plan, err := commandPlanFor("windows", "restart_service", "My-Svc.1")
	if err != nil {
		t.Fatalf("commandPlanFor: %v", err)
	}
	want := [][]string{
		{"sc.exe", "stop", "My-Svc.1"},
		{"sc.exe", "start", "My-Svc.1"},
	}
	if !equalPlan(plan, want) {
		t.Fatalf("windows restart_service plan = %v, want %v", plan, want)
	}
	for _, step := range plan {
		for _, arg := range step {
			if strings.Contains(arg, "cmd.exe") || strings.Contains(arg, "&") {
				t.Fatalf("windows plan must not use cmd.exe / shell chaining, got arg %q", arg)
			}
		}
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
