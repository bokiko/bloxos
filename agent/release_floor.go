package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/bokiko/bloxos/proto/updatesigning"
)

/* ============================================================================
 * Persistent release floor (downgrade protection, issue #145)
 *
 * The v1 signature proves an announced binary is one the release key signed.
 * It does not prove the binary is not OLDER than what this agent already
 * runs — any (os, sha) ever signed verifies forever, so a compromised hub or
 * an operator redeploying an old artifact could walk the fleet back to a
 * build with a known hole.
 *
 * The floor is the agent's own memory of how far forward it has gone:
 *
 *     release=<sequence>
 *     sha256=<hex of the binary that set it>
 *
 * A candidate is accepted only if its embedded release (read out of the
 * downloaded bytes the signature covers, see updatesigning.ExtractRelease)
 * is HIGHER than the floor, or EQUAL with exactly the floor's SHA. Equal
 * with a different SHA is refused: two builds that share a counter would
 * otherwise be replayable against each other. Lower is refused.
 *
 * The file only ever moves forward through the update path. It is seeded at
 * boot from the running binary's own release and SHA, so it never depends on
 * a later update (the same-SHA early return in handleAgentVersion never
 * reaches authorisation, and the seed must not either). Installers do not
 * touch it, the systemd recovery script does not touch it, and it is not
 * keyed on the pinned update key, so re-running an installer or rotating the
 * key leaves it alone.
 *
 * Crash ordering: the floor is raised BEFORE the binary swap. If the process
 * dies between the two, the agent restarts on the old binary with the floor
 * already at the new release; the hub re-announces the same (release, sha),
 * which the equal-and-same-SHA rule accepts, so the retry succeeds.
 *
 * Rolled back by the recovery unit to the .prev binary: the agent boots
 * with its own release below the stored floor. It keeps running (the floor
 * gates updates, never the agent itself), keeps the higher floor, reports
 * both, and accepts the next announcement at or above the floor.
 *
 * Deliberate local recovery: root places the wanted binary, deletes the
 * floor file, restarts. The agent reseeds from what is now running. There
 * is no remote path that lowers a floor, and no implicit one either: a
 * same-numbered but DIFFERENT build found running at boot (a reinstall of a
 * rebuilt artifact, say) is not adopted as the new identity — it is an
 * error until an operator resets the floor on purpose.
 *
 * Fail closed, and stickily. Anything that goes wrong at boot — the floor
 * cannot be read, written or reconciled; the binary cannot be hashed; the
 * scanner does not read back the release the constant claims — disables
 * self-update for the life of the process. Every later read additionally
 * re-checks that the floor on disk still covers the running binary, so a
 * floor that is replaced by an older valid file after boot cannot re-open
 * downgrades below what is running. All of it is reported to the hub as
 * release_floor_ok=false so it withholds with a reason instead of arming a
 * reconnect timer for a refusal it provoked.
 * ============================================================================ */

// releaseFloor is one persisted floor: the highest accepted release, pinned
// to the exact build that set it.
type releaseFloor struct {
	Release uint64
	SHA     string
}

// defaultReleaseFloorPathLinux sits beside the pinned update key.
const defaultReleaseFloorPathLinux = "/etc/bloxos/agent-release-floor"

// releaseFloorFileName is the Windows file name, placed beside the agent
// executable and agent-update.pub in the admin-only install directory.
const releaseFloorFileName = "agent-release-floor"

var errReleaseFloorAbsent = errors.New("no release floor is recorded")

// releaseFloorState is the process-wide state established once at boot.
type releaseFloorState struct {
	booted bool
	// path is where the floor lives, resolved at boot.
	path string
	// self is the running binary's identity, established at boot.
	self releaseFloor
	// bootErr is sticky: once set, self-update stays disabled until restart.
	bootErr error
}

var (
	releaseFloorMu sync.Mutex
	floorState     releaseFloorState
)

// releaseFloorPath returns where the floor lives. exePath is the agent's own
// resolved binary; Windows keeps the floor beside it, Linux under /etc/bloxos.
// BLOXOS_RELEASE_FLOOR_PATH overrides both (tests, unusual layouts).
func releaseFloorPath(exePath string) string {
	if p := strings.TrimSpace(os.Getenv("BLOXOS_RELEASE_FLOOR_PATH")); p != "" {
		return p
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(filepath.Dir(exePath), releaseFloorFileName)
	}
	return defaultReleaseFloorPathLinux
}

// permits reports whether candidate may be installed over this floor.
func (f releaseFloor) permits(candidate releaseFloor) error {
	switch {
	case candidate.Release == 0:
		return fmt.Errorf("candidate binary carries no release sequence; this agent enforces a release floor of %d and refuses unnumbered builds", f.Release)
	case candidate.Release > f.Release:
		return nil
	case candidate.Release == f.Release && strings.EqualFold(candidate.SHA, f.SHA):
		return nil
	case candidate.Release == f.Release:
		return fmt.Errorf("candidate release %d equals the floor but is a different build (%s, floor pinned to %s); a release number cannot be reused for different bytes",
			candidate.Release, shortSHA(candidate.SHA), shortSHA(f.SHA))
	default:
		return fmt.Errorf("candidate release %d is below this agent's floor of %d; refusing downgrade", candidate.Release, f.Release)
	}
}

// covers reports whether this floor is consistent with `running` being the
// binary in execution: the floor is higher (we were rolled back), or it is
// exactly this build. A floor below the running release, or a same-numbered
// floor pinned to other bytes, is not a floor this process may enforce.
func (f releaseFloor) covers(running releaseFloor) error {
	switch {
	case running.Release == 0:
		return fmt.Errorf("running binary is unnumbered")
	case f.Release > running.Release:
		return nil
	case f.Release == running.Release && strings.EqualFold(f.SHA, running.SHA):
		return nil
	case f.Release == running.Release:
		return fmt.Errorf("floor is pinned to release %d build %s but the running binary is a different build %s of the same release; "+
			"a release number cannot be reused for different bytes — an operator must reset the floor deliberately",
			f.Release, shortSHA(f.SHA), shortSHA(running.SHA))
	default:
		return fmt.Errorf("floor release %d is below the running release %d", f.Release, running.Release)
	}
}

// readReleaseFloor parses the floor file. errReleaseFloorAbsent when it does
// not exist; any other error means the file exists but is not trustworthy,
// and callers must fail closed rather than overwrite it.
func readReleaseFloor(path string) (releaseFloor, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return releaseFloor{}, errReleaseFloorAbsent
		}
		return releaseFloor{}, fmt.Errorf("read release floor %s: %w", path, err)
	}
	return parseReleaseFloor(data)
}

func parseReleaseFloor(data []byte) (releaseFloor, error) {
	var (
		f          releaseFloor
		gotRelease bool
		gotSHA     bool
	)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return releaseFloor{}, fmt.Errorf("release floor is corrupt: unparseable line %q", line)
		}
		switch strings.TrimSpace(key) {
		case "release":
			if gotRelease {
				return releaseFloor{}, fmt.Errorf("release floor is corrupt: duplicate release field")
			}
			seq, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
			if err != nil || seq == 0 || seq > updatesigning.MaxRelease {
				return releaseFloor{}, fmt.Errorf("release floor is corrupt: bad release %q", value)
			}
			f.Release, gotRelease = seq, true
		case "sha256":
			if gotSHA {
				return releaseFloor{}, fmt.Errorf("release floor is corrupt: duplicate sha256 field")
			}
			sha := strings.ToLower(strings.TrimSpace(value))
			if !isHexSHA256(sha) {
				return releaseFloor{}, fmt.Errorf("release floor is corrupt: bad sha256 %q", value)
			}
			f.SHA, gotSHA = sha, true
		default:
			// Unknown keys are tolerated so a future field does not brick
			// the floor for an older agent.
		}
	}
	if !gotRelease || !gotSHA {
		return releaseFloor{}, fmt.Errorf("release floor is corrupt: release and sha256 are both required")
	}
	return f, nil
}

func isHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// writeReleaseFloor durably replaces the floor file: temp file in the same
// directory, fsync, then the shared crash-durable rename (which fsyncs the
// directory). A crash leaves either the old floor or the new one, never a
// torn file.
func writeReleaseFloor(path string, f releaseFloor) error {
	if f.Release == 0 || f.Release > updatesigning.MaxRelease || !isHexSHA256(strings.ToLower(f.SHA)) {
		return fmt.Errorf("refusing to write an invalid release floor (release=%d sha=%q)", f.Release, f.SHA)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create release floor directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary release floor in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(tmpPath)
		}
	}()
	body := fmt.Sprintf("release=%d\nsha256=%s\n", f.Release, strings.ToLower(f.SHA))
	if _, err := io.WriteString(tmp, body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write release floor: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync release floor: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close release floor: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return fmt.Errorf("chmod release floor: %w", err)
	}
	if err := releaseFloorRename(tmpPath, path); err != nil {
		return fmt.Errorf("install release floor %s: %w", path, err)
	}
	keep = true
	return nil
}

// releaseFloorRename is the crash-durable replace used by writeReleaseFloor.
// A variable so tests can inject a rename that succeeds on disk but fails
// its durability fsync, the exact case raiseReleaseFloor's unconditional
// rewrite exists for.
var releaseFloorRename = durableRename

// seedReleaseFloor reconciles the stored floor with the binary that is
// actually running and returns the floor in force afterwards.
//
//   - absent            → written from self (first boot of a numbered build)
//   - corrupt           → error; never overwritten
//   - stored higher     → kept as is (we were rolled back or recovered)
//   - equal, same SHA   → kept as is
//   - equal, other SHA  → error: the accepted identity for this release is
//     the stored one; adopting whatever is running would let any local
//     reinstall silently re-pin the release, so it takes a deliberate reset
//   - stored lower      → rewritten to self
//
// An unnumbered self is an error: there is nothing to seed and no floor this
// process may enforce.
func seedReleaseFloor(path string, self releaseFloor) (releaseFloor, error) {
	if self.Release == 0 {
		return releaseFloor{}, fmt.Errorf("this build is unnumbered; it cannot seed or enforce a release floor")
	}
	stored, err := readReleaseFloor(path)
	switch {
	case err == nil:
		if coverErr := stored.covers(self); coverErr == nil {
			return stored, nil
		} else if stored.Release == self.Release {
			return releaseFloor{}, coverErr
		}
		// stored lower than self: fall through and raise.
	case errors.Is(err, errReleaseFloorAbsent):
	default:
		return releaseFloor{}, err
	}
	if err := writeReleaseFloor(path, self); err != nil {
		return releaseFloor{}, err
	}
	return self, nil
}

// raiseReleaseFloor advances the stored floor to candidate on the update
// path. It requires a floor to exist (boot seeds one) and requires the floor
// to permit the candidate. It ALWAYS writes and syncs, even when the visible
// floor already equals the candidate: that case is the retry after a crash
// between the floor write and the binary swap, and the previous attempt may
// have renamed the file into place yet failed the directory fsync — visible,
// but with no durability proof. Rewriting the same value is cheap,
// update-only I/O and is the proof. The floor can never go down here:
// permits() only admits a higher release or the floor's own build.
func raiseReleaseFloor(path string, candidate releaseFloor) error {
	stored, err := readReleaseFloor(path)
	if err != nil {
		return err
	}
	if err := stored.permits(candidate); err != nil {
		return err
	}
	return writeReleaseFloor(path, candidate)
}

// seedReleaseFloorAtBoot resolves this binary's identity and floor path and
// establishes the process-wide state. Called once from main before any
// connection (and, on Windows, before applyPendingUpdate). Never fatal: a
// broken floor disables self-update, not the agent.
func seedReleaseFloorAtBoot() {
	exe, err := selfExePath()
	if err != nil {
		initReleaseFloorState("", releaseFloor{}, fmt.Errorf("cannot resolve own path: %w", err))
		return
	}
	path := releaseFloorPath(exe)
	self := releaseFloor{Release: agentEmbeddedRelease()}
	sha, err := computeFileSHA256(exe)
	if err != nil {
		initReleaseFloorState(path, self, fmt.Errorf("cannot hash own binary: %w", err))
		return
	}
	self.SHA = sha

	// The scanner must read back exactly the number the constant says, or
	// the hub withholds/announces on a different basis than this agent
	// enforces and the floor's identity is not the one external tooling
	// sees. That is a mis-stamped build: disable, do not guess.
	scanned, err := updatesigning.ExtractRelease(exe)
	if err != nil {
		initReleaseFloorState(path, self, fmt.Errorf("own binary release marker unreadable: %w", err))
		return
	}
	if scanned != self.Release {
		initReleaseFloorState(path, self, fmt.Errorf("own binary scans as release %d but reports %d", scanned, self.Release))
		return
	}
	initReleaseFloorState(path, self, nil)
}

// initReleaseFloorState is the testable core of seedReleaseFloorAtBoot:
// records path and self, and seeds the floor unless bootErr already rules
// this process out. Any failure here is sticky for the life of the process.
func initReleaseFloorState(path string, self releaseFloor, bootErr error) {
	releaseFloorMu.Lock()
	defer releaseFloorMu.Unlock()
	floorState = releaseFloorState{booted: true, path: path, self: self, bootErr: bootErr}
	if bootErr != nil {
		log.Printf("release: %v; self-update disabled until restart", bootErr)
		return
	}
	floor, err := seedReleaseFloor(path, self)
	if err != nil {
		floorState.bootErr = err
		log.Printf("release: floor at %s is unusable (%v); self-update disabled until an operator repairs it and restarts the agent", path, err)
		return
	}
	if floor.Release > self.Release {
		log.Printf("release: running release %d is BELOW the recorded floor %d (%s) — this binary was restored locally; "+
			"only announcements at or above the floor will be accepted", self.Release, floor.Release, shortSHA(floor.SHA))
		return
	}
	log.Printf("release: running release %d, floor %d (%s) at %s", self.Release, floor.Release, shortSHA(floor.SHA), path)
}

// currentReleaseFloor re-reads the floor from disk (so a local operator
// edit takes effect without a restart) and fails closed when boot did not
// establish a usable state, when the file is absent or corrupt, or when the
// file no longer covers the running binary — a valid but OLDER floor put in
// place after boot must not re-open downgrades below what is running.
func currentReleaseFloor() (releaseFloor, error) {
	releaseFloorMu.Lock()
	st := floorState
	releaseFloorMu.Unlock()
	if !st.booted {
		return releaseFloor{}, fmt.Errorf("release floor was never initialised at boot")
	}
	if st.bootErr != nil {
		return releaseFloor{}, fmt.Errorf("release floor disabled since boot: %w", st.bootErr)
	}
	floor, err := readReleaseFloor(st.path)
	if err != nil {
		return releaseFloor{}, fmt.Errorf("release floor at %s is unusable (%v); self-update is disabled — "+
			"restart the agent to reseed it, or repair the file", st.path, err)
	}
	if err := floor.covers(st.self); err != nil {
		return releaseFloor{}, fmt.Errorf("release floor at %s no longer covers the running binary (%v); self-update is disabled", st.path, err)
	}
	return floor, nil
}

// currentReleaseFloorPath returns the path established at boot.
func currentReleaseFloorPath() string {
	releaseFloorMu.Lock()
	defer releaseFloorMu.Unlock()
	return floorState.path
}

// loadReleaseFloorForUpdate is the update path's view of the floor.
func loadReleaseFloorForUpdate() (releaseFloor, error) {
	return currentReleaseFloor()
}

// releaseFloorStatus is what reportAgentVersion sends: the floor in force
// and whether it is usable, under exactly the rules the update path applies.
func releaseFloorStatus() (floor releaseFloor, ok bool) {
	floor, err := currentReleaseFloor()
	if err != nil {
		return releaseFloor{}, false
	}
	return floor, true
}

// verifyCandidateRelease reads the release sequence out of a downloaded,
// SHA-verified binary and checks it against the floor. Returns the candidate
// floor entry to raise to. Nothing here executes the candidate.
func verifyCandidateRelease(candidatePath, candidateSHA string) (releaseFloor, error) {
	floor, err := loadReleaseFloorForUpdate()
	if err != nil {
		return releaseFloor{}, err
	}
	seq, err := updatesigning.ExtractRelease(candidatePath)
	if err != nil {
		return releaseFloor{}, fmt.Errorf("cannot read the downloaded binary's release sequence: %w", err)
	}
	candidate := releaseFloor{Release: seq, SHA: strings.ToLower(candidateSHA)}
	if err := floor.permits(candidate); err != nil {
		return releaseFloor{}, err
	}
	return candidate, nil
}

// checkStagedRelease is the Windows boot-time re-check of a staged
// <exe>.new against the floor, shared here so it can be tested on every
// platform. performUpdateWindows raised the floor before writing the marker,
// so a genuine staged update is the equal-and-same-SHA case and passes; a
// marker and staged file replayed after the floor moved on, a staged file
// whose bytes no longer match the release the marker recorded, or an
// unnumbered staged file are refused. Raising is idempotent and covers a
// floor repaired between the two boots. markerRelease is 0 for a marker
// written by an agent that predates the field.
func checkStagedRelease(stagedPath, stagedSHA string, markerRelease uint64) error {
	candidate, err := verifyCandidateRelease(stagedPath, stagedSHA)
	if err != nil {
		return fmt.Errorf("staged binary rejected by release floor: %w", err)
	}
	if markerRelease != 0 && markerRelease != candidate.Release {
		return fmt.Errorf("staged binary carries release %d but the marker says %d", candidate.Release, markerRelease)
	}
	if err := raiseReleaseFloor(currentReleaseFloorPath(), candidate); err != nil {
		return fmt.Errorf("cannot raise release floor to %d: %w", candidate.Release, err)
	}
	return nil
}

// checkAdvisoryRelease applies the hub's unsigned release hint from the
// announcement. It is refuse-only: a hint below the floor (or equal with a
// different SHA than the floor pins) saves a download that would be refused
// after verification anyway. The hint is never trusted to raise anything;
// the authoritative check is verifyCandidateRelease on the actual bytes.
func checkAdvisoryRelease(floor releaseFloor, advisory uint64, announcedSHA string) error {
	if advisory == 0 {
		return nil
	}
	return floor.permits(releaseFloor{Release: advisory, SHA: strings.ToLower(strings.TrimSpace(announcedSHA))})
}
