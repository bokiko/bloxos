package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bokiko/bloxos/proto/updatesigning"
)

func TestReleaseAnnouncePolicy(t *testing.T) {
	oldSHA, nextSHA := strings.Repeat("ab", 32), strings.Repeat("cd", 32)
	base := agentVersionInfo{UpdateProtocol: 2, Release: 4, ReleaseFloor: 4, ReleaseFloorSHA: oldSHA, ReleaseFloorOK: true}
	tests := []struct {
		name    string
		release uint64
		sha     string
		change  func(*agentVersionInfo)
		want    string
	}{
		{name: "newer", release: 5, sha: nextSHA},
		{name: "same bytes retry", release: 4, sha: oldSHA},
		{name: "same release different bytes", release: 4, sha: nextSHA, want: "agent_release_conflict"},
		{name: "older signed release", release: 3, sha: nextSHA, want: "agent_release_below_floor"},
		{name: "legacy binary", release: 0, sha: nextSHA, want: "agent_release_missing"},
		{name: "corrupt state", release: 5, sha: nextSHA, change: func(v *agentVersionInfo) { v.ReleaseFloorOK = false }, want: "agent_release_floor_unreadable"},
		{name: "missing floor", release: 5, sha: nextSHA, change: func(v *agentVersionInfo) { v.ReleaseFloor = 0 }, want: "agent_release_floor_unreadable"},
		{name: "missing hash", release: 5, sha: nextSHA, change: func(v *agentVersionInfo) { v.ReleaseFloorSHA = "" }, want: "agent_release_floor_unreadable"},
		{name: "malformed hash", release: 5, sha: nextSHA, change: func(v *agentVersionInfo) { v.ReleaseFloorSHA = strings.Repeat("zz", 32) }, want: "agent_release_floor_unreadable"},
		{name: "floor behind running release", release: 5, sha: nextSHA, change: func(v *agentVersionInfo) { v.Release = 5 }, want: "agent_release_floor_unreadable"},
		{name: "local recovery preserves higher floor", release: 3, sha: nextSHA, change: func(v *agentVersionInfo) { v.Release = 2 }, want: "agent_release_below_floor"},
		{name: "local recovery can retry accepted bytes", release: 4, sha: oldSHA, change: func(v *agentVersionInfo) { v.Release = 2 }},
		{name: "legacy protocol unchanged", release: 0, sha: nextSHA, change: func(v *agentVersionInfo) { v.UpdateProtocol = 1; v.ReleaseFloorOK = false }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := base
			if tt.change != nil {
				tt.change(&v)
			}
			state := agentBinaryState{SHA: tt.sha, Release: tt.release}
			got := releaseAnnounceBlocked(&v, state, tt.sha)
			if tt.want == "" && got != "" || tt.want != "" && !strings.HasPrefix(got, tt.want+":") {
				t.Fatalf("reason = %q, want %q", got, tt.want)
			}
		})
	}
	if got := releaseAnnounceBlocked(&base, agentBinaryState{SHA: oldSHA, Release: 5}, nextSHA); !strings.HasPrefix(got, "agent_release_changed:") {
		t.Fatalf("changed binary not withheld: %q", got)
	}
}

func TestRecomputeReleaseAndHashFromBinary(t *testing.T) {
	withAgentBinaryState(t)
	marker, err := updatesigning.ReleaseMarker(7)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("binary prefix\x00" + marker + "\x00binary suffix")
	path := filepath.Join(t.TempDir(), "agent")
	if err := os.WriteFile(path, body, 0o755); err != nil {
		t.Fatal(err)
	}
	oldResolver := resolveAgentBinaryFor
	resolveAgentBinaryFor = func(_, _ string) (agentBinaryResolution, error) {
		return agentBinaryResolution{Path: path, Source: "test"}, nil
	}
	t.Cleanup(func() { resolveAgentBinaryFor = oldResolver })
	recomputeBinaryFor("linux")
	got := currentAgentBinaryState("linux")
	wantSHA := sha256.Sum256(body)
	if got.Release != 7 || got.SHA != hex.EncodeToString(wantSHA[:]) || got.Error != "" {
		t.Fatalf("release/hash not derived from complete binary: %+v", got)
	}
	// The marker belongs to the source, not to this hub process's lifetime.
	setAgentBinaryState("linux", agentBinaryState{})
	recomputeBinaryFor("linux")
	if again := currentAgentBinaryState("linux"); again.Release != got.Release || again.SHA != got.SHA {
		t.Fatalf("recomputing changed release identity: %+v", again)
	}
}

func TestReleaseWithholdingDoesNotArmReconnectAndIsVisible(t *testing.T) {
	e, s := setupTestServer(t)
	withAgentBinaryState(t)
	const id = "release-floor-test"
	servedSHA := strings.Repeat("cd", 32)
	setAgentBinaryState("linux", agentBinaryState{SHA: servedSHA, Release: 3})
	v := agentVersionInfo{MachineID: id, OS: "linux", Arch: "amd64", ArchReported: true,
		RunningSHA: strings.Repeat("ab", 32), UpdateProtocol: 2, UpdateTransportOK: true, UpdateKeyPinned: true,
		Release: 4, ReleaseFloor: 4, ReleaseFloorSHA: strings.Repeat("ab", 32), ReleaseFloorOK: true}
	agentRunningVersionsMu.Lock()
	agentRunningVersions[id] = v
	agentRunningVersionsMu.Unlock()
	t.Cleanup(func() {
		agentRunningVersionsMu.Lock()
		delete(agentRunningVersions, id)
		agentRunningVersionsMu.Unlock()
		clearReconnectExpectation(id)
	})
	// A nil connection is deliberate: the policy must return before any write.
	s.announceVersionToAgent(id, &ConnectedAgent{})
	assertNoReconnectExpectation(t, id)
	rec := httptest.NewRecorder()
	if err := s.handleListVersions(e.NewContext(httptest.NewRequest(http.MethodGet, "/api/versions", nil), rec)); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Agents []agentVersionInfo `json:"agents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	for _, got := range result.Agents {
		if got.MachineID != id {
			continue
		}
		if got.ReleaseFloor != 4 || !strings.HasPrefix(got.UpdateBlockedReason, "agent_release_below_floor:") {
			t.Fatalf("API did not expose the persisted floor and withhold reason: %+v", got)
		}
		return
	}
	t.Fatal("machine missing from versions API")
}

func TestReleaseReportOverWebSocket(t *testing.T) {
	for _, release := range []uint64{0, 3, 4, 5} {
		t.Run(fmt.Sprint(release), func(t *testing.T) {
			e, s := setupTestServer(t)
			stagePendingUpdate(t)
			state := currentAgentBinaryState("linux")
			state.Release = release
			setAgentBinaryState("linux", state)
			const id = "numbered-agent"
			report := &agentVersionReport{Release: 4, ReleaseFloor: 4, ReleaseFloorSHA: strings.Repeat("11", 32), ReleaseFloorOK: true}
			frames := s.collectAnnounceFrames(t, e, announceProbe{machineID: id, proto: 2, transportOK: true, keyPinned: true, arch: "amd64", releaseReport: report})
			agentRunningVersionsMu.RLock()
			got := agentRunningVersions[id]
			agentRunningVersionsMu.RUnlock()
			if got.Release != 4 || got.ReleaseFloor != 4 || got.ReleaseFloorSHA != report.ReleaseFloorSHA || !got.ReleaseFloorOK {
				t.Fatalf("wire report lost floor fields: %+v", got)
			}
			if release <= 4 {
				if len(frames) != 0 {
					t.Fatalf("refused update announced: %v", frames)
				}
				assertNoReconnectExpectation(t, id)
				return
			}
			if len(frames) != 1 {
				t.Fatalf("want one newer-release announcement, got %v", frames)
			}
			var msg struct {
				Release   uint64 `json:"release"`
				SHA       string `json:"sha256"`
				Signature string `json:"signature"`
			}
			if err := json.Unmarshal([]byte(frames[0]), &msg); err != nil {
				t.Fatal(err)
			}
			if msg.Release != 5 || msg.SHA != state.SHA || msg.Signature == "" {
				t.Fatalf("invalid numbered announcement: %+v", msg)
			}
		})
	}
}
