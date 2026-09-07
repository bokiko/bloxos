package updatesigning

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustMarker(t *testing.T, seq uint64) string {
	t.Helper()
	m, err := ReleaseMarker(seq)
	if err != nil {
		t.Fatalf("ReleaseMarker(%d): %v", seq, err)
	}
	return m
}

func TestReleaseMarkerFormat(t *testing.T) {
	// The expected value is assembled from the split parts on purpose: a
	// literal here would plant the contiguous marker in the test binary and
	// trip TestScannerBinaryHasNoContiguousPrefix.
	if got, want := mustMarker(t, 42), string(releaseMarkerPrefix())+"0000000042:"; got != want {
		t.Fatalf("marker = %q, want %q", got, want)
	}
	// Compared part by part: a constant concatenation would be folded by
	// the compiler into the very literal the guard test forbids.
	if len(releaseMarkerParts) != 3 || releaseMarkerParts[0] != "BLOXOS-" ||
		releaseMarkerParts[1] != "AGENT-" || releaseMarkerParts[2] != "RELEASE:" {
		t.Fatalf("marker prefix changed; the agent's embedded literal and the hub's scanner must agree")
	}
	if _, err := ReleaseMarker(0); err == nil {
		t.Fatalf("ReleaseMarker(0) must fail")
	}
	if _, err := ReleaseMarker(MaxRelease + 1); err == nil {
		t.Fatalf("ReleaseMarker(MaxRelease+1) must fail")
	}
	if m := mustMarker(t, MaxRelease); len(m) != releaseMarkerLen() {
		t.Fatalf("marker length %d, want %d", len(m), releaseMarkerLen())
	}
}

func TestExtractReleaseReaderContract(t *testing.T) {
	marker := mustMarker(t, 7)
	prefix := string(releaseMarkerPrefix())

	cases := []struct {
		name    string
		input   string
		want    uint64
		wantErr bool
		errIs   error
	}{
		{name: "empty stream", input: "", want: 0},
		{name: "no marker", input: "just some bytes without a marker", want: 0},
		{name: "single marker", input: "head" + marker + "tail", want: 7},
		{name: "marker at start", input: marker + "tail", want: 7},
		{name: "marker at end", input: "head" + marker, want: 7},
		{name: "prefix only is truncated", input: "head" + prefix, wantErr: true},
		{name: "truncated digits", input: "head" + prefix + "00000", wantErr: true},
		{name: "non digit", input: "head" + prefix + "00000000x7:", wantErr: true},
		{name: "missing terminator", input: "head" + prefix + "0000000007;", wantErr: true},
		{name: "zero sequence", input: "head" + prefix + "0000000000:", wantErr: true},
		{name: "two markers same value", input: marker + "mid" + marker, wantErr: true, errIs: ErrAmbiguousRelease},
		{name: "two markers different values", input: marker + "mid" + mustMarker(t, 8), wantErr: true, errIs: ErrAmbiguousRelease},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractReleaseReader(strings.NewReader(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %d", got)
				}
				if tc.errIs != nil && !errors.Is(err, tc.errIs) {
					t.Fatalf("error %v, want %v", err, tc.errIs)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestExtractReleaseReaderChunkBoundaries plants a marker at every offset
// around the scanner's chunk boundary, in a stream several chunks long, and
// checks it is found exactly once regardless of how it straddles reads.
func TestExtractReleaseReaderChunkBoundaries(t *testing.T) {
	const chunk = 256 * 1024
	marker := mustMarker(t, 123456)
	markerLen := len(marker)
	filler := make([]byte, 3*chunk)
	if _, err := rand.Read(filler); err != nil {
		t.Fatal(err)
	}
	// Random filler cannot accidentally contain the marker prefix in any
	// realistic run, but a stray byte pattern would only make the test
	// stricter (an unexpected count), never let a bug pass.
	for off := chunk - markerLen - 2; off <= chunk+2; off++ {
		data := append([]byte{}, filler...)
		copy(data[off:], marker)
		got, err := ExtractReleaseReader(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("offset %d: %v", off, err)
		}
		if got != 123456 {
			t.Fatalf("offset %d: got %d", off, got)
		}
	}
	// Same, with a second marker far away: must be ambiguous no matter
	// where the first one straddles.
	for off := chunk - markerLen - 2; off <= chunk+2; off++ {
		data := append([]byte{}, filler...)
		copy(data[off:], marker)
		copy(data[2*chunk+100:], marker)
		if _, err := ExtractReleaseReader(bytes.NewReader(data)); !errors.Is(err, ErrAmbiguousRelease) {
			t.Fatalf("offset %d: err = %v, want ambiguous", off, err)
		}
	}
}

// TestExtractReleaseReaderSmallReads feeds the scanner one byte at a time to
// prove the carry logic does not depend on read sizes.
func TestExtractReleaseReaderSmallReads(t *testing.T) {
	marker := mustMarker(t, 99)
	data := "aaaa" + marker + "bbbb"
	got, err := ExtractReleaseReader(iotest1(strings.NewReader(data)))
	if err != nil || got != 99 {
		t.Fatalf("got %d, %v", got, err)
	}
}

type oneByteReader struct{ r io.Reader }

func iotest1(r io.Reader) io.Reader { return oneByteReader{r} }

func (o oneByteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return o.r.Read(p[:1])
}

// TestExtractReleaseReaderConsumesToEOF is the property the hub relies on:
// teeing the reader into a hash must hash the entire file, not just up to
// the marker.
func TestExtractReleaseReaderConsumesToEOF(t *testing.T) {
	marker := mustMarker(t, 5)
	data := []byte("prefix-bytes " + marker + " and a long tail after the marker")
	h := sha256.New()
	got, err := ExtractReleaseReader(io.TeeReader(bytes.NewReader(data), h))
	if err != nil || got != 5 {
		t.Fatalf("got %d, %v", got, err)
	}
	want := sha256.Sum256(data)
	if hex.EncodeToString(h.Sum(nil)) != hex.EncodeToString(want[:]) {
		t.Fatalf("tee hash does not cover the whole stream")
	}
}

func TestExtractReleaseFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent")
	if err := os.WriteFile(path, []byte("x"+mustMarker(t, 3)+"y"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ExtractRelease(path)
	if err != nil || got != 3 {
		t.Fatalf("got %d, %v", got, err)
	}
	if _, err := ExtractRelease(filepath.Join(dir, "missing")); err == nil {
		t.Fatalf("missing file must error")
	}
}

// TestScannerBinaryHasNoContiguousPrefix guards the runtime-assembled prefix:
// if a refactor turned releaseMarkerParts back into a constant expression,
// every binary linking this package would carry the prefix and a hub or
// signer could read as a numbered agent. The check inspects this test
// binary, which links the scanner but embeds no marker of its own.
func TestScannerBinaryHasNoContiguousPrefix(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip("cannot locate test binary")
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Skip("cannot read test binary")
	}
	if bytes.Contains(data, releaseMarkerPrefix()) {
		t.Fatalf("test binary contains a contiguous release marker prefix; the prefix must stay split at compile time")
	}
}
