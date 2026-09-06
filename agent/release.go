package main

import (
	"log"
	"strings"
	"sync"

	"github.com/bokiko/bloxos/proto/updatesigning"
)

/* ============================================================================
 * Release sequence
 *
 * agentRelease is this build's place in the release order. It is bumped by
 * hand, in source, once per release — never derived from the clock, git
 * metadata (absent from the Docker build context), or a build flag. Two
 * builds of the same commit therefore carry the same number, and a number
 * can only be changed by changing the source that is built.
 *
 * agentReleaseMarker is the same number rendered as the literal that
 * updatesigning.ExtractRelease scans for in the finished binary. It MUST
 * stay a plain string constant that is used on a live code path: that is
 * what puts one contiguous copy of it into the binary's read-only data,
 * where the hub (and a receiving agent) read it back out of the bytes the
 * v1 signature already covers. TestAgentReleaseMarkerMatchesConstant fails
 * if the two drift apart.
 *
 * Bumping a release: increment agentRelease, re-render the marker with the
 * same value zero-padded to updatesigning.ReleaseMarkerDigits, run the tests.
 * ============================================================================ */

// agentRelease is the release sequence compiled into this binary.
const agentRelease uint64 = 1

// agentReleaseMarker is agentRelease as the scannable literal.
const agentReleaseMarker = "BLOXOS-AGENT-RELEASE:0000000001:"

var (
	embeddedReleaseOnce sync.Once
	embeddedReleaseSeq  uint64
)

// agentEmbeddedRelease returns the release sequence this binary carries,
// parsed from the marker literal itself rather than from agentRelease. That
// keeps the literal live (so it is never dropped from the binary) and means
// the number the agent reports is exactly the one an external scanner reads.
func agentEmbeddedRelease() uint64 {
	embeddedReleaseOnce.Do(func() {
		seq, err := updatesigning.ExtractReleaseReader(strings.NewReader(agentReleaseMarker))
		if err != nil || seq != agentRelease {
			// Unreachable if the test suite passed; if it ever trips, the
			// binary is mis-stamped and must not claim any release.
			log.Printf("release: embedded marker %q does not match agentRelease %d (%v); treating this build as unnumbered",
				agentReleaseMarker, agentRelease, err)
			return
		}
		embeddedReleaseSeq = seq
	})
	return embeddedReleaseSeq
}
