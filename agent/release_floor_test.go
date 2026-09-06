package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bokiko/bloxos/proto/updatesigning"
)

const (
	floorSHAa = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	floorSHAb = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	floorSHAc = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

// bootFloorForTest establishes process-wide floor state as a successful boot
// of `self` would, against a floor file in a fresh temp dir (pre-populated
// with `stored` when non-nil). Returns the floor path.
func bootFloorForTest(t *testing.T, self releaseFloor, stored *releaseFloor) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent-release-floor")
	if stored != nil {
		if err := writeReleaseFloor(path, *stored); err != nil {
			t.Fatalf("seed stored floor: %v", err)
		}
	}
	resetReleaseFloorStateForTest()
	t.Cleanup(resetReleaseFloorStateForTest)
	initReleaseFloorState(path, self, nil)
	return path
}

func mustReadFloor(t *testing.T, path string) releaseFloor {
	t.Helper()
	f, err := readReleaseFloor(path)
	if err != nil {
		t.Fatalf("read floor: %v", err)
	}
	return f
}

// writeCandidateBinary writes a fake binary carrying the given markers.
func writeCandidateBinary(t *testing.T, dir string, markers ...string) string {
	t.Helper()
	path := filepath.Join(dir, "candidate")
	body := "ELF-ish head " + strings.Join(markers, " middle ") + " tail"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func markerFor(t *testing.T, seq uint64) string {
	t.Helper()
	m, err := updatesigning.ReleaseMarker(seq)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestParseReleaseFloor(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    releaseFloor
		wantErr string
	}{
		{name: "canonical", body: "release=7\nsha256=" + floorSHAa + "\n", want: releaseFloor{7, floorSHAa}},
		{name: "order comments unknown keys crlf", body: "# note\r\nsha256=" + strings.ToUpper(floorSHAa) + "\r\nfuture=x\r\nrelease=7\r\n", want: releaseFloor{7, floorSHAa}},
		{name: "duplicate release", body: "release=7\nrelease=8\nsha256=" + floorSHAa + "\n", wantErr: "duplicate release"},
		{name: "duplicate sha", body: "release=7\nsha256=" + floorSHAa + "\nsha256=" + floorSHAb + "\n", wantErr: "duplicate sha256"},
		{name: "missing sha", body: "release=7\n", wantErr: "are both required"},
		{name: "missing release", body: "sha256=" + floorSHAa + "\n", wantErr: "are both required"},
		{name: "zero release", body: "release=0\nsha256=" + floorSHAa + "\n", wantErr: "bad release"},
		{name: "over max", body: "release=10000000000\nsha256=" + floorSHAa + "\n", wantErr: "bad release"},
		{name: "negative", body: "release=-1\nsha256=" + floorSHAa + "\n", wantErr: "bad release"},
		{name: "short sha", body: "release=1\nsha256=abc\n", wantErr: "bad sha256"},
		{name: "garbage line", body: "release=1\nwhat\nsha256=" + floorSHAa + "\n", wantErr: "unparseable"},
		{name: "empty", body: "", wantErr: "are both required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseReleaseFloor([]byte(tc.body))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestWriteReleaseFloorRejectsInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "floor")
	for _, bad := range []releaseFloor{
		{0, floorSHAa},
		{updatesigning.MaxRelease + 1, floorSHAa},
		{1, "nothex"},
		{1, ""},
	} {
		if err := writeReleaseFloor(path, bad); err == nil {
			t.Fatalf("writeReleaseFloor accepted %+v", bad)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("invalid write left a file behind for %+v", bad)
		}
	}
	if err := writeReleaseFloor(path, releaseFloor{updatesigning.MaxRelease, floorSHAa}); err != nil {
		t.Fatalf("max release must be writable: %v", err)
	}
	if got := mustReadFloor(t, path); got.Release != updatesigning.MaxRelease {
		t.Fatalf("round trip = %+v", got)
	}
	// No temp files left behind.
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("expected only the floor file, found %d entries", len(entries))
	}
}

func TestReleaseFloorPermits(t *testing.T) {
	floor := releaseFloor{5, floorSHAa}
	cases := []struct {
		name string
		cand releaseFloor
		ok   bool
	}{
		{"higher", releaseFloor{6, floorSHAb}, true},
		{"equal same sha", releaseFloor{5, floorSHAa}, true},
		{"equal same sha upper case", releaseFloor{5, strings.ToUpper(floorSHAa)}, true},
		{"equal different sha", releaseFloor{5, floorSHAb}, false},
		{"lower", releaseFloor{4, floorSHAa}, false},
		{"lower same sha", releaseFloor{4, floorSHAa}, false},
		{"unnumbered", releaseFloor{0, floorSHAb}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := floor.permits(tc.cand)
			if (err == nil) != tc.ok {
				t.Fatalf("permits(%+v) = %v, want ok=%v", tc.cand, err, tc.ok)
			}
		})
	}
}

func TestReleaseFloorCovers(t *testing.T) {
	running := releaseFloor{5, floorSHAa}
	cases := []struct {
		name  string
		floor releaseFloor
		ok    bool
	}{
		{"higher floor (rolled back)", releaseFloor{6, floorSHAb}, true},
		{"exact build", releaseFloor{5, floorSHAa}, true},
		{"same release other build", releaseFloor{5, floorSHAb}, false},
		{"lower floor", releaseFloor{4, floorSHAa}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.floor.covers(running)
			if (err == nil) != tc.ok {
				t.Fatalf("covers = %v, want ok=%v", err, tc.ok)
			}
		})
	}
	if err := (releaseFloor{5, floorSHAa}).covers(releaseFloor{0, floorSHAa}); err == nil {
		t.Fatal("an unnumbered running binary must never be covered")
	}
}

func TestSeedReleaseFloor(t *testing.T) {
	self := releaseFloor{5, floorSHAa}

	t.Run("absent is seeded from self", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "floor")
		got, err := seedReleaseFloor(path, self)
		if err != nil || got != self {
			t.Fatalf("got %+v, %v", got, err)
		}
		if mustReadFloor(t, path) != self {
			t.Fatal("file does not hold self")
		}
	})
	t.Run("absent and unnumbered self errors", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "floor")
		if _, err := seedReleaseFloor(path, releaseFloor{0, floorSHAa}); err == nil {
			t.Fatal("unnumbered self must not seed")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("unnumbered self must not write a floor")
		}
	})
	t.Run("higher stored is preserved", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "floor")
		higher := releaseFloor{9, floorSHAb}
		if err := writeReleaseFloor(path, higher); err != nil {
			t.Fatal(err)
		}
		got, err := seedReleaseFloor(path, self)
		if err != nil || got != higher {
			t.Fatalf("got %+v, %v; want stored floor preserved", got, err)
		}
		if mustReadFloor(t, path) != higher {
			t.Fatal("recovery lowered the floor")
		}
	})
	t.Run("equal same sha is kept", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "floor")
		if err := writeReleaseFloor(path, self); err != nil {
			t.Fatal(err)
		}
		before, _ := os.Stat(path)
		got, err := seedReleaseFloor(path, self)
		if err != nil || got != self {
			t.Fatalf("got %+v, %v", got, err)
		}
		after, _ := os.Stat(path)
		if !before.ModTime().Equal(after.ModTime()) {
			t.Fatal("identical floor was rewritten")
		}
	})
	t.Run("equal different sha is an error and file untouched", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "floor")
		pinned := releaseFloor{5, floorSHAb}
		if err := writeReleaseFloor(path, pinned); err != nil {
			t.Fatal(err)
		}
		if _, err := seedReleaseFloor(path, self); err == nil {
			t.Fatal("a same-numbered different build must not be adopted implicitly")
		}
		if mustReadFloor(t, path) != pinned {
			t.Fatal("accepted identity was rewritten")
		}
	})
	t.Run("lower stored is raised to self", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "floor")
		if err := writeReleaseFloor(path, releaseFloor{2, floorSHAc}); err != nil {
			t.Fatal(err)
		}
		got, err := seedReleaseFloor(path, self)
		if err != nil || got != self {
			t.Fatalf("got %+v, %v", got, err)
		}
		if mustReadFloor(t, path) != self {
			t.Fatal("file not raised")
		}
	})
	t.Run("corrupt is an error and never overwritten", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "floor")
		if err := os.WriteFile(path, []byte("release=5\nrelease=5\nsha256="+floorSHAa+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := seedReleaseFloor(path, self); err == nil {
			t.Fatal("corrupt floor must error")
		}
		data, _ := os.ReadFile(path)
		if !strings.Contains(string(data), "release=5\nrelease=5") {
			t.Fatal("corrupt floor was overwritten")
		}
	})
}

func TestRaiseReleaseFloor(t *testing.T) {
	stored := releaseFloor{5, floorSHAa}
	newFloor := func(t *testing.T) string {
		path := filepath.Join(t.TempDir(), "floor")
		if err := writeReleaseFloor(path, stored); err != nil {
			t.Fatal(err)
		}
		return path
	}
	t.Run("higher writes", func(t *testing.T) {
		path := newFloor(t)
		if err := raiseReleaseFloor(path, releaseFloor{6, floorSHAb}); err != nil {
			t.Fatal(err)
		}
		if got := mustReadFloor(t, path); got != (releaseFloor{6, floorSHAb}) {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("equal same sha is an idempotent no-op (crash retry)", func(t *testing.T) {
		path := newFloor(t)
		if err := raiseReleaseFloor(path, stored); err != nil {
			t.Fatal(err)
		}
		if err := raiseReleaseFloor(path, stored); err != nil {
			t.Fatal(err)
		}
		if mustReadFloor(t, path) != stored {
			t.Fatal("floor changed")
		}
	})
	t.Run("equal different sha refused", func(t *testing.T) {
		path := newFloor(t)
		if err := raiseReleaseFloor(path, releaseFloor{5, floorSHAb}); err == nil {
			t.Fatal("must refuse")
		}
		if mustReadFloor(t, path) != stored {
			t.Fatal("floor changed")
		}
	})
	t.Run("lower refused", func(t *testing.T) {
		path := newFloor(t)
		if err := raiseReleaseFloor(path, releaseFloor{4, floorSHAb}); err == nil {
			t.Fatal("must refuse")
		}
	})
	t.Run("absent refused", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "floor")
		if err := raiseReleaseFloor(path, releaseFloor{6, floorSHAb}); !errors.Is(err, errReleaseFloorAbsent) {
			t.Fatalf("err = %v, want absent", err)
		}
	})
}

func TestReleaseFloorBootStateIsStickyAndFailClosed(t *testing.T) {
	self := releaseFloor{5, floorSHAa}

	t.Run("never booted refuses", func(t *testing.T) {
		resetReleaseFloorStateForTest()
		t.Cleanup(resetReleaseFloorStateForTest)
		if _, err := currentReleaseFloor(); err == nil {
			t.Fatal("must refuse before boot")
		}
		if _, ok := releaseFloorStatus(); ok {
			t.Fatal("status must be not-ok before boot")
		}
	})
	t.Run("boot error is sticky even with a valid file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "floor")
		if err := writeReleaseFloor(path, self); err != nil {
			t.Fatal(err)
		}
		resetReleaseFloorStateForTest()
		t.Cleanup(resetReleaseFloorStateForTest)
		initReleaseFloorState(path, self, errors.New("own binary scans as release 9 but reports 5"))
		if _, err := currentReleaseFloor(); err == nil || !strings.Contains(err.Error(), "disabled since boot") {
			t.Fatalf("err = %v", err)
		}
		if _, ok := releaseFloorStatus(); ok {
			t.Fatal("status must be not-ok")
		}
	})
	t.Run("seed failure at boot is sticky", func(t *testing.T) {
		path := bootFloorForTest(t, self, &releaseFloor{5, floorSHAb})
		if _, err := currentReleaseFloor(); err == nil {
			t.Fatal("equal-different at boot must disable updates")
		}
		// Even repairing the file does not lift a boot error without restart.
		if err := writeReleaseFloor(path, self); err != nil {
			t.Fatal(err)
		}
		if _, err := currentReleaseFloor(); err == nil {
			t.Fatal("boot error must be sticky")
		}
	})
	t.Run("healthy boot then floor replaced by valid older file refuses", func(t *testing.T) {
		path := bootFloorForTest(t, self, nil)
		if _, err := currentReleaseFloor(); err != nil {
			t.Fatalf("healthy boot: %v", err)
		}
		if err := writeReleaseFloor(path, releaseFloor{3, floorSHAc}); err != nil {
			t.Fatal(err)
		}
		if _, err := currentReleaseFloor(); err == nil || !strings.Contains(err.Error(), "no longer covers") {
			t.Fatalf("older floor after boot must refuse, got %v", err)
		}
		if _, ok := releaseFloorStatus(); ok {
			t.Fatal("status must be not-ok")
		}
	})
	t.Run("healthy boot then floor replaced by same release other build refuses", func(t *testing.T) {
		path := bootFloorForTest(t, self, nil)
		if err := writeReleaseFloor(path, releaseFloor{5, floorSHAb}); err != nil {
			t.Fatal(err)
		}
		if _, err := currentReleaseFloor(); err == nil {
			t.Fatal("must refuse")
		}
	})
	t.Run("healthy boot then floor deleted refuses until restart", func(t *testing.T) {
		path := bootFloorForTest(t, self, nil)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if _, err := currentReleaseFloor(); err == nil {
			t.Fatal("must refuse")
		}
	})
	t.Run("healthy boot then floor raised externally is honoured", func(t *testing.T) {
		path := bootFloorForTest(t, self, nil)
		if err := writeReleaseFloor(path, releaseFloor{8, floorSHAb}); err != nil {
			t.Fatal(err)
		}
		floor, err := currentReleaseFloor()
		if err != nil || floor.Release != 8 {
			t.Fatalf("got %+v, %v", floor, err)
		}
	})
	t.Run("rolled back binary keeps higher floor and reports it", func(t *testing.T) {
		bootFloorForTest(t, releaseFloor{3, floorSHAc}, &releaseFloor{5, floorSHAa})
		floor, ok := releaseFloorStatus()
		if !ok || floor != (releaseFloor{5, floorSHAa}) {
			t.Fatalf("got %+v ok=%v", floor, ok)
		}
	})
}

func TestVerifyCandidateRelease(t *testing.T) {
	self := releaseFloor{5, floorSHAa}
	dir := t.TempDir()

	t.Run("higher accepted", func(t *testing.T) {
		bootFloorForTest(t, self, nil)
		cand := writeCandidateBinary(t, dir, markerFor(t, 6))
		got, err := verifyCandidateRelease(cand, strings.ToUpper(floorSHAb))
		if err != nil || got != (releaseFloor{6, floorSHAb}) {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
	t.Run("equal same sha accepted (retry)", func(t *testing.T) {
		bootFloorForTest(t, self, nil)
		cand := writeCandidateBinary(t, dir, markerFor(t, 5))
		if _, err := verifyCandidateRelease(cand, floorSHAa); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("equal different sha refused", func(t *testing.T) {
		bootFloorForTest(t, self, nil)
		cand := writeCandidateBinary(t, dir, markerFor(t, 5))
		if _, err := verifyCandidateRelease(cand, floorSHAb); err == nil {
			t.Fatal("must refuse")
		}
	})
	t.Run("lower refused", func(t *testing.T) {
		bootFloorForTest(t, self, nil)
		cand := writeCandidateBinary(t, dir, markerFor(t, 4))
		if _, err := verifyCandidateRelease(cand, floorSHAb); err == nil || !strings.Contains(err.Error(), "downgrade") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unnumbered refused", func(t *testing.T) {
		bootFloorForTest(t, self, nil)
		cand := writeCandidateBinary(t, dir)
		if _, err := verifyCandidateRelease(cand, floorSHAb); err == nil || !strings.Contains(err.Error(), "no release sequence") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("ambiguous refused", func(t *testing.T) {
		bootFloorForTest(t, self, nil)
		cand := writeCandidateBinary(t, dir, markerFor(t, 6), markerFor(t, 7))
		if _, err := verifyCandidateRelease(cand, floorSHAb); err == nil {
			t.Fatal("must refuse")
		}
	})
	t.Run("floor unusable refuses before reading candidate", func(t *testing.T) {
		resetReleaseFloorStateForTest()
		t.Cleanup(resetReleaseFloorStateForTest)
		cand := writeCandidateBinary(t, dir, markerFor(t, 99))
		if _, err := verifyCandidateRelease(cand, floorSHAb); err == nil {
			t.Fatal("must refuse")
		}
	})
}

func TestCheckAdvisoryRelease(t *testing.T) {
	floor := releaseFloor{5, floorSHAa}
	if err := checkAdvisoryRelease(floor, 0, floorSHAb); err != nil {
		t.Fatalf("absent advisory must pass: %v", err)
	}
	if err := checkAdvisoryRelease(floor, 6, floorSHAb); err != nil {
		t.Fatalf("higher advisory must pass: %v", err)
	}
	if err := checkAdvisoryRelease(floor, 5, " "+strings.ToUpper(floorSHAa)+" "); err != nil {
		t.Fatalf("equal same sha must pass: %v", err)
	}
	if err := checkAdvisoryRelease(floor, 5, floorSHAb); err == nil {
		t.Fatal("equal different sha must refuse")
	}
	if err := checkAdvisoryRelease(floor, 4, floorSHAb); err == nil {
		t.Fatal("lower advisory must refuse")
	}
}

// resetReleaseFloorStateForTest clears the process-wide boot state so each
// test starts as an un-booted process.
func resetReleaseFloorStateForTest() {
	releaseFloorMu.Lock()
	floorState = releaseFloorState{}
	releaseFloorMu.Unlock()
}

// TestReleaseFloorCrashRetrySameReleaseSameSHA walks the crash-ordering
// scenario end to end: the floor is raised for a candidate, the process dies
// before the swap, restarts on the OLD binary, and the hub re-announces the
// same candidate. The retry must pass; a same-numbered different build must
// not; the floor must never go down.
func TestReleaseFloorCrashRetrySameReleaseSameSHA(t *testing.T) {
	dir := t.TempDir()
	oldSelf := releaseFloor{5, floorSHAa}
	path := bootFloorForTest(t, oldSelf, nil)

	// First attempt: verify + raise, then "crash" (no swap).
	cand := writeCandidateBinary(t, dir, markerFor(t, 6))
	got, err := verifyCandidateRelease(cand, floorSHAb)
	if err != nil {
		t.Fatal(err)
	}
	if err := raiseReleaseFloor(currentReleaseFloorPath(), got); err != nil {
		t.Fatal(err)
	}
	if mustReadFloor(t, path) != (releaseFloor{6, floorSHAb}) {
		t.Fatal("floor not raised before swap")
	}

	// Restart on the old binary with the raised floor already on disk.
	resetReleaseFloorStateForTest()
	initReleaseFloorState(path, oldSelf, nil)
	floor, ok := releaseFloorStatus()
	if !ok || floor != (releaseFloor{6, floorSHAb}) {
		t.Fatalf("after restart floor = %+v ok=%v; the raised floor must survive", floor, ok)
	}

	// Retry of the identical candidate passes and re-proves durability.
	if _, err := verifyCandidateRelease(cand, floorSHAb); err != nil {
		t.Fatalf("retry of the same (release, sha) must pass: %v", err)
	}
	if err := raiseReleaseFloor(currentReleaseFloorPath(), releaseFloor{6, floorSHAb}); err != nil {
		t.Fatalf("retry raise must pass: %v", err)
	}
	// A rebuilt release 6 with other bytes does not.
	if _, err := verifyCandidateRelease(cand, floorSHAc); err == nil {
		t.Fatal("same release, different sha must be refused after a partial update")
	}
	// And the old binary itself can never be re-installed by the hub.
	oldCand := writeCandidateBinary(t, t.TempDir(), markerFor(t, 5))
	if _, err := verifyCandidateRelease(oldCand, floorSHAa); err == nil {
		t.Fatal("the running (older) build must not be accepted as an update once the floor moved on")
	}
	if mustReadFloor(t, path) != (releaseFloor{6, floorSHAb}) {
		t.Fatal("floor changed during refusals")
	}
}

// TestReleaseFloorCorruptAtBootIsSticky: a corrupt floor found at boot
// disables updates for the life of the process, is never overwritten, and a
// later repair of the file does not lift the disable without a restart.
func TestReleaseFloorCorruptAtBootIsSticky(t *testing.T) {
	path := filepath.Join(t.TempDir(), "floor")
	corrupt := "release=5\nsha256=" + floorSHAa + "\nsha256=" + floorSHAb + "\n"
	if err := os.WriteFile(path, []byte(corrupt), 0o644); err != nil {
		t.Fatal(err)
	}
	resetReleaseFloorStateForTest()
	t.Cleanup(resetReleaseFloorStateForTest)
	initReleaseFloorState(path, releaseFloor{5, floorSHAa}, nil)

	if _, ok := releaseFloorStatus(); ok {
		t.Fatal("corrupt floor at boot must report not-ok")
	}
	if _, err := currentReleaseFloor(); err == nil || !strings.Contains(err.Error(), "disabled since boot") {
		t.Fatalf("err = %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != corrupt {
		t.Fatal("corrupt floor was rewritten at boot")
	}
	if err := writeReleaseFloor(path, releaseFloor{5, floorSHAa}); err != nil {
		t.Fatal(err)
	}
	if _, err := currentReleaseFloor(); err == nil {
		t.Fatal("repairing the file must not lift a boot error without restart")
	}
	// A restart with the repaired file works.
	resetReleaseFloorStateForTest()
	initReleaseFloorState(path, releaseFloor{5, floorSHAa}, nil)
	if _, ok := releaseFloorStatus(); !ok {
		t.Fatal("restart after repair must be ok")
	}
}

// TestReleaseFloorBootHashOrScanFailureIsSticky covers the boot errors that
// arrive from outside the floor file itself (own-binary hash failure,
// scanner mismatch): they must disable updates even though a perfectly
// valid floor is on disk.
func TestReleaseFloorBootHashOrScanFailureIsSticky(t *testing.T) {
	path := filepath.Join(t.TempDir(), "floor")
	self := releaseFloor{5, floorSHAa}
	if err := writeReleaseFloor(path, self); err != nil {
		t.Fatal(err)
	}
	for _, bootErr := range []error{
		errors.New("cannot hash own binary: permission denied"),
		errors.New("own binary release marker unreadable: more than one release marker found"),
		errors.New("own binary scans as release 4 but reports 5"),
	} {
		resetReleaseFloorStateForTest()
		initReleaseFloorState(path, self, bootErr)
		if _, ok := releaseFloorStatus(); ok {
			t.Fatalf("boot error %q must disable updates", bootErr)
		}
		if _, err := verifyCandidateRelease(filepath.Join(t.TempDir(), "none"), floorSHAb); err == nil {
			t.Fatalf("boot error %q must refuse candidates", bootErr)
		}
	}
	resetReleaseFloorStateForTest()
}

// TestCheckStagedRelease is the Windows boot-time replay guard, run here on
// every platform: the staged .new plus marker are re-checked after the
// process that wrote them exited.
func TestCheckStagedRelease(t *testing.T) {
	dir := t.TempDir()
	self := releaseFloor{5, floorSHAa}

	t.Run("genuine staged update passes (floor already raised)", func(t *testing.T) {
		path := bootFloorForTest(t, self, &releaseFloor{6, floorSHAb})
		staged := writeCandidateBinary(t, dir, markerFor(t, 6))
		if err := checkStagedRelease(staged, floorSHAb, 6); err != nil {
			t.Fatal(err)
		}
		if mustReadFloor(t, path) != (releaseFloor{6, floorSHAb}) {
			t.Fatal("floor changed")
		}
	})
	t.Run("marker from an older agent without release passes", func(t *testing.T) {
		bootFloorForTest(t, self, &releaseFloor{6, floorSHAb})
		staged := writeCandidateBinary(t, dir, markerFor(t, 6))
		if err := checkStagedRelease(staged, floorSHAb, 0); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("replayed after the floor moved on is refused", func(t *testing.T) {
		path := bootFloorForTest(t, self, &releaseFloor{7, floorSHAc})
		staged := writeCandidateBinary(t, dir, markerFor(t, 6))
		if err := checkStagedRelease(staged, floorSHAb, 6); err == nil || !strings.Contains(err.Error(), "downgrade") {
			t.Fatalf("err = %v", err)
		}
		if mustReadFloor(t, path) != (releaseFloor{7, floorSHAc}) {
			t.Fatal("floor lowered by a replay")
		}
	})
	t.Run("same release other build replayed is refused", func(t *testing.T) {
		bootFloorForTest(t, self, &releaseFloor{6, floorSHAb})
		staged := writeCandidateBinary(t, dir, markerFor(t, 6))
		if err := checkStagedRelease(staged, floorSHAc, 6); err == nil || !strings.Contains(err.Error(), "different build") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("staged bytes disagree with marker release", func(t *testing.T) {
		bootFloorForTest(t, self, nil)
		staged := writeCandidateBinary(t, dir, markerFor(t, 7))
		if err := checkStagedRelease(staged, floorSHAb, 6); err == nil || !strings.Contains(err.Error(), "marker says") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unnumbered staged file refused", func(t *testing.T) {
		bootFloorForTest(t, self, nil)
		staged := writeCandidateBinary(t, dir)
		if err := checkStagedRelease(staged, floorSHAb, 0); err == nil {
			t.Fatal("must refuse")
		}
	})
	t.Run("no usable floor refuses", func(t *testing.T) {
		resetReleaseFloorStateForTest()
		t.Cleanup(resetReleaseFloorStateForTest)
		staged := writeCandidateBinary(t, dir, markerFor(t, 9))
		if err := checkStagedRelease(staged, floorSHAb, 9); err == nil {
			t.Fatal("must refuse")
		}
	})
}

// TestRaiseReleaseFloorDurabilityFailurePropagates injects a rename that
// puts the file in place but fails its durability proof (the directory
// fsync). The raise must report the error so the swap does not happen, the
// visible floor must not be lower than before, and the retry — which sees an
// equal, same-SHA floor — must write and sync AGAIN rather than short-cut on
// the visible value.
func TestRaiseReleaseFloorDurabilityFailurePropagates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "floor")
	if err := writeReleaseFloor(path, releaseFloor{5, floorSHAa}); err != nil {
		t.Fatal(err)
	}

	renameCalls := 0
	failNext := true
	orig := releaseFloorRename
	releaseFloorRename = func(oldpath, newpath string) error {
		renameCalls++
		if err := os.Rename(oldpath, newpath); err != nil {
			return err
		}
		if failNext {
			failNext = false
			return errors.New("fsync parent directory: injected failure")
		}
		return nil
	}
	t.Cleanup(func() { releaseFloorRename = orig })

	cand := releaseFloor{6, floorSHAb}
	if err := raiseReleaseFloor(path, cand); err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Fatalf("first raise must propagate the durability failure, got %v", err)
	}
	// Visible state is the new floor (rename happened) — never lower.
	if got := mustReadFloor(t, path); got != cand {
		t.Fatalf("visible floor after failed fsync = %+v", got)
	}
	// Retry: equal + same SHA. Must go through rename+fsync again.
	before := renameCalls
	if err := raiseReleaseFloor(path, cand); err != nil {
		t.Fatalf("retry must succeed: %v", err)
	}
	if renameCalls != before+1 {
		t.Fatalf("retry skipped persistence (rename calls %d -> %d); an equal floor must be re-proven durable", before, renameCalls)
	}
	if got := mustReadFloor(t, path); got != cand {
		t.Fatalf("floor after retry = %+v", got)
	}
	// No stray temp files after either attempt.
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("expected only the floor file, found %d entries", len(entries))
	}
}

// TestSeedReleaseFloorWriteFailureIsBootError: an unwritable floor at first
// boot disables updates rather than pretending a floor exists.
func TestSeedReleaseFloorWriteFailureIsBootError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "floor")
	orig := releaseFloorRename
	releaseFloorRename = func(oldpath, newpath string) error {
		return errors.New("injected rename failure")
	}
	t.Cleanup(func() { releaseFloorRename = orig })
	resetReleaseFloorStateForTest()
	t.Cleanup(resetReleaseFloorStateForTest)
	initReleaseFloorState(path, releaseFloor{5, floorSHAa}, nil)
	if _, ok := releaseFloorStatus(); ok {
		t.Fatal("unwritable floor at boot must disable updates")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("no floor file should exist after a failed seed")
	}
}

func TestReleaseFloorPathOverride(t *testing.T) {
	t.Setenv("BLOXOS_RELEASE_FLOOR_PATH", "/custom/floor")
	if got := releaseFloorPath("/opt/bloxos-agent"); got != "/custom/floor" {
		t.Fatalf("got %q", got)
	}
}
