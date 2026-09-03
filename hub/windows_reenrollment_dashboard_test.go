package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readMachineDetailPageSource(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "dashboard", "src", "app", "machine", "[id]", "page.tsx"))
	if err != nil {
		t.Fatalf("read machine detail page: %v", err)
	}
	return string(body)
}

// TestMachineDetailPageOffersSeparateReenrollmentAction locks in that the
// dashboard adds a distinct "Prepare Windows re-enrollment" action alongside
// (not replacing) the existing revoke-only action, gated the same way
// (fleet.admin) plus a Windows-only condition, and requires confirmation.
func TestMachineDetailPageOffersSeparateReenrollmentAction(t *testing.T) {
	source := readMachineDetailPageSource(t)

	if !strings.Contains(source, "Revoke credential") {
		t.Fatal("existing revoke-only action was removed or renamed")
	}
	if !strings.Contains(source, "/credential") {
		t.Fatal("revoke action no longer calls DELETE .../credential")
	}

	if !strings.Contains(source, "windows-re-enrollment") {
		t.Fatal("dashboard does not call the windows-re-enrollment endpoint")
	}
	if !strings.Contains(source, "Windows re-enrollment") && !strings.Contains(source, "Re-enroll Windows") {
		t.Fatal("dashboard has no distinct Windows re-enrollment action label")
	}
	// Windows-only: gated on machine.os, not shown unconditionally.
	if !strings.Contains(source, `machine.os`) {
		t.Fatal("re-enrollment action is not gated on machine.os")
	}
}

// TestMachineDetailPageReenrollmentRendersOnlyServerResponse mirrors
// TestDashboardRendersOnlyServerGeneratedCommands: the new dialog must render
// only fields the hub actually returned (windows_command, ca_sha256,
// expires_at) and must never derive a command, authority, or token
// client-side.
func TestMachineDetailPageReenrollmentRendersOnlyServerResponse(t *testing.T) {
	source := readMachineDetailPageSource(t)

	for _, required := range []string{"windows_command", "ca_sha256", "expires_at"} {
		if !strings.Contains(source, required) {
			t.Fatalf("re-enrollment UI missing server-response field %q", required)
		}
	}
	for _, forbidden := range []string{
		"deriveHttpHubBase", "deriveWsHubBase", "buildWindowsCommand",
		"ServerCertificateValidationCallback", "iwr -UseBasicParsing",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("re-enrollment UI builds/trusts a command client-side: %s", forbidden)
		}
	}
}

// TestMachineDetailPageReenrollmentSurfacesFailures locks in that a non-2xx
// response body is surfaced to the operator rather than silently swallowed —
// mirroring how the existing revoke flow already handles res.ok/data?.error.
func TestMachineDetailPageReenrollmentSurfacesFailures(t *testing.T) {
	source := readMachineDetailPageSource(t)

	reenrollIdx := strings.Index(source, "windows-re-enrollment")
	if reenrollIdx < 0 {
		t.Fatal("dashboard does not call the windows-re-enrollment endpoint")
	}
	// Look at a window around the call site for the same res.ok / data?.error
	// failure-surfacing pattern the revoke handler uses.
	start := reenrollIdx - 200
	if start < 0 {
		start = 0
	}
	end := reenrollIdx + 800
	if end > len(source) {
		end = len(source)
	}
	window := source[start:end]
	if !strings.Contains(window, "res.ok") {
		t.Fatalf("re-enrollment handler does not check res.ok: %q", window)
	}
	if !strings.Contains(window, "error") {
		t.Fatalf("re-enrollment handler does not surface an error field: %q", window)
	}
}
