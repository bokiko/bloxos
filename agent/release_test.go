package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bokiko/bloxos/proto/updatesigning"
)

// TestAgentReleaseMarkerMatchesConstant is the guard against a half-bumped
// release: agentRelease and agentReleaseMarker must describe the same number,
// and the number the process reports must be the one a scanner reads.
func TestAgentReleaseMarkerMatchesConstant(t *testing.T) {
	want, err := updatesigning.ReleaseMarker(agentRelease)
	if err != nil {
		t.Fatalf("agentRelease %d is not a valid release: %v", agentRelease, err)
	}
	if agentReleaseMarker != want {
		t.Fatalf("agentReleaseMarker = %q, want %q for agentRelease %d — bump both together",
			agentReleaseMarker, want, agentRelease)
	}
	seq, err := updatesigning.ExtractReleaseReader(strings.NewReader(agentReleaseMarker))
	if err != nil || seq != agentRelease {
		t.Fatalf("scanner reads %d (%v) from the marker, want %d", seq, err, agentRelease)
	}
	if got := agentEmbeddedRelease(); got != agentRelease {
		t.Fatalf("agentEmbeddedRelease() = %d, want %d", got, agentRelease)
	}
}

// TestBuiltAgentBinaryCarriesExactlyOneMarker builds the agent the way the
// Dockerfile does (stripped) for the host platform and this package's
// supported targets, then scans the artifact with the same extractor the hub
// uses. This is the end-to-end property everything else rests on: the number
// in source is the number in the bytes, exactly once.
func TestBuiltAgentBinaryCarriesExactlyOneMarker(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build in -short mode")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	targets := []struct{ goos, goarch string }{
		{"linux", "amd64"},
		{"linux", "arm64"},
		{"windows", "amd64"},
	}
	for _, tgt := range targets {
		t.Run(tgt.goos+"/"+tgt.goarch, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "bloxos-agent")
			cmd := exec.Command(goBin, "build", "-ldflags=-s -w", "-o", out, ".")
			cmd.Env = append(os.Environ(), "GOOS="+tgt.goos, "GOARCH="+tgt.goarch, "CGO_ENABLED=0")
			if outb, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("build %s/%s: %v\n%s", tgt.goos, tgt.goarch, err, outb)
			}
			seq, err := updatesigning.ExtractRelease(out)
			if err != nil {
				t.Fatalf("scan built binary: %v", err)
			}
			if seq != agentRelease {
				t.Fatalf("built binary carries release %d, source says %d", seq, agentRelease)
			}
		})
	}
}
