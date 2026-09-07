package updatesigning

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

/* ============================================================================
 * Embedded release sequence
 *
 * Every numbered agent binary carries exactly one marker literal in its
 * read-only data:
 *
 *     BLOXOS-AGENT-RELEASE:<10 decimal digits>:
 *
 * The digits are the agent's release sequence — a hand-bumped constant in
 * agent/release.go, so the number is a property of the source the binary was
 * built from: not of the clock, not of git metadata (the Docker build context
 * has none), and not of anything the hub decides at runtime. Because the
 * existing v1 signature covers the binary's SHA-256, and the SHA-256 covers
 * these bytes, the sequence is authenticated by the signature the fleet
 * already verifies. No second signature format is needed.
 *
 * The hub, the agent and any tooling read the sequence back out of the bytes
 * with the scanner below, which never executes the candidate. The scanner
 * assembles the prefix at runtime from split fragments so a binary that
 * merely links this package (the hub, bloxos-sign) does not itself contain a
 * contiguous marker and cannot be mistaken for a numbered agent.
 *
 * Contract:
 *   - no marker            → (0, nil): the binary is unnumbered
 *   - exactly one marker   → (sequence, nil)
 *   - malformed / zero /
 *     more than one marker → (0, error): fail closed
 * ============================================================================ */

// ReleaseMarkerDigits is the fixed width of the sequence inside a marker.
const ReleaseMarkerDigits = 10

// MaxRelease is the largest sequence a fixed-width marker can carry.
const MaxRelease uint64 = 9_999_999_999

// releaseMarkerParts is joined at runtime to form the marker prefix. It is a
// var, not a const expression, so the compiler cannot fold it back into the
// contiguous literal that the scanner is looking for.
var releaseMarkerParts = []string{"BLOXOS-", "AGENT-", "RELEASE:"}

func releaseMarkerPrefix() []byte {
	return []byte(strings.Join(releaseMarkerParts, ""))
}

// releaseMarkerLen is len(prefix) + digits + terminating ':'.
func releaseMarkerLen() int {
	return len(releaseMarkerPrefix()) + ReleaseMarkerDigits + 1
}

// ReleaseMarker renders the marker literal for a sequence. The agent embeds
// the result as a source constant; tests use this to assert that constant
// and the numeric sequence agree. It is built at runtime, so calling it does
// not itself plant a marker in the caller's binary.
func ReleaseMarker(seq uint64) (string, error) {
	if seq == 0 || seq > MaxRelease {
		return "", fmt.Errorf("release sequence %d is outside 1..%d", seq, MaxRelease)
	}
	return string(releaseMarkerPrefix()) +
		fmt.Sprintf("%0*d", ReleaseMarkerDigits, seq) + ":", nil
}

// ErrAmbiguousRelease is returned when a stream carries more than one marker.
var ErrAmbiguousRelease = errors.New("more than one release marker found")

// ExtractRelease reads the file at path and returns its embedded release
// sequence. See ExtractReleaseReader for the contract.
func ExtractRelease(path string) (uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	seq, err := ExtractReleaseReader(f)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	return seq, nil
}

// ExtractReleaseReader streams r to EOF and returns the single embedded
// release sequence. It returns (0, nil) when no marker is present, and an
// error when a marker is malformed, carries sequence zero, or appears more
// than once. The whole stream is always consumed, so a caller that tees the
// same reader into a hash sees identical bytes for both results.
func ExtractReleaseReader(r io.Reader) (uint64, error) {
	prefix := releaseMarkerPrefix()
	markerLen := releaseMarkerLen()
	const chunkSize = 256 * 1024

	buf := make([]byte, 0, chunkSize+markerLen)
	var (
		found uint64
		count int
		eof   bool
	)
	for !eof {
		// Grow the buffer by one chunk after whatever carry is left.
		start := len(buf)
		buf = buf[:start+chunkSize]
		n, err := io.ReadFull(r, buf[start:])
		buf = buf[:start+n]
		switch {
		case err == nil:
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			eof = true
		default:
			return 0, err
		}

		// Scan every prefix occurrence that has enough trailing bytes to be
		// evaluated now. One that runs off the end waits for the next chunk
		// via the carry below; at EOF it is a truncated marker.
		from := 0
		for {
			i := bytes.Index(buf[from:], prefix)
			if i < 0 {
				break
			}
			i += from
			if i+markerLen > len(buf) {
				if eof {
					return 0, fmt.Errorf("truncated release marker at end of stream")
				}
				break
			}
			seq, err := parseMarkerBody(buf[i+len(prefix) : i+markerLen])
			if err != nil {
				return 0, err
			}
			count++
			if count > 1 {
				return 0, ErrAmbiguousRelease
			}
			found = seq
			from = i + 1
		}

		if !eof {
			// Keep the last markerLen-1 bytes: a marker cannot fit entirely
			// inside that tail, so nothing evaluated above is seen twice,
			// and any prefix that began there is re-scanned with its
			// trailing bytes attached.
			keep := markerLen - 1
			if len(buf) > keep {
				copy(buf, buf[len(buf)-keep:])
				buf = buf[:keep]
			}
		}
	}
	return found, nil
}

// parseMarkerBody validates "<digits>:" and returns the sequence.
func parseMarkerBody(body []byte) (uint64, error) {
	digits := body[:ReleaseMarkerDigits]
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("malformed release marker %q", string(body))
		}
	}
	if body[ReleaseMarkerDigits] != ':' {
		return 0, fmt.Errorf("malformed release marker %q", string(body))
	}
	seq, err := strconv.ParseUint(string(digits), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("malformed release marker %q: %w", string(body), err)
	}
	if seq == 0 {
		return 0, fmt.Errorf("release marker carries sequence 0, which is reserved for unnumbered builds")
	}
	return seq, nil
}
