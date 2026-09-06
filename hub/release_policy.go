package main

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// releaseAnnounceBlocked mirrors the protocol-2 agent's anti-rollback gate.
// This is rollout hygiene, not the trust boundary: the agent independently
// checks the signed download and its own durable floor before installing it.
// Legacy agents retain their existing migration/signature policy.
func releaseAnnounceBlocked(report *agentVersionInfo, state agentBinaryState, sha string) string {
	if report == nil || report.UpdateProtocol < 2 {
		return ""
	}
	floorSHA, err := hex.DecodeString(report.ReleaseFloorSHA)
	if !report.ReleaseFloorOK || report.ReleaseFloor == 0 || report.Release == 0 ||
		report.ReleaseFloor < report.Release || err != nil || len(floorSHA) != 32 {
		return "agent_release_floor_unreadable: the agent has not reported a valid durable update floor; repair its local state and restart the agent before updating"
	}
	if state.SHA != sha {
		return "agent_release_changed: the served binary changed during the update check; retry after it is refreshed"
	}
	if state.Release == 0 {
		return "agent_release_missing: the served binary has no release number; serve a numbered release for this agent"
	}
	if state.Release < report.ReleaseFloor {
		return fmt.Sprintf("agent_release_below_floor: served release %d is older than this agent's accepted release %d", state.Release, report.ReleaseFloor)
	}
	if state.Release == report.ReleaseFloor && !strings.EqualFold(sha, report.ReleaseFloorSHA) {
		return "agent_release_conflict: different binary bytes reuse an accepted release number; build the release with a higher number"
	}
	return ""
}
