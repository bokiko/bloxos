//go:build linux

package main

import (
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
)

// Linux RAPL: the powercap energy counters, split into the three domains
// they can honestly answer for.
//
//	/sys/class/powercap/intel-rapl:<n>     name "package-<n>"  → cpu
//	/sys/class/powercap/intel-rapl:<n>     name "psys"         → system
//	/sys/class/powercap/intel-rapl:<n>:<m> name "dram"         → dram
//
// psys is the PLATFORM domain: on the OEM designs that wire it up it covers
// the packages, memory and much of the rest of the board, which makes it the
// closest thing software has to whole-system power. It is therefore the
// preferred `system` source — NOT added to the package sum, which goes on
// answering for `cpu` by itself. Two different fields, never one number.
//
// Excluded everywhere: core/uncore sub-zones (already inside their package)
// and intel-rapl-mmio:<n> (the same package through a second interface).
//
// AMD Zen parts register the same powercap zones through the RAPL MSR
// driver, so "intel-rapl" here is an interface name, not a vendor.
//
// The counters are readable by root only on current kernels; the agent runs
// as root. Anything else (no powercap, a VM without RAPL, unreadable
// counters) reports the affected backend as unavailable; that is a fact
// about the host, not an error to retry forever.

const raplRoot = "/sys/class/powercap"

var (
	raplTopLevel = regexp.MustCompile(`^intel-rapl:\d+$`)
	raplSubZone  = regexp.MustCompile(`^intel-rapl:\d+:\d+$`)
)

type raplZone struct {
	energyPath string
	maxRange   uint64
	last       uint64
}

// raplSampler turns one group of RAPL zones into a rate. Zones inside a
// group are summed, so a group may only ever hold non-overlapping zones of
// the same domain.
type raplSampler struct {
	id       string
	zones    []*raplZone
	minWatts float64 // a rate below this is implausible for the domain
	lastAt   time.Time
	primed   bool
	read     func(string) ([]byte, error)
}

func (s *raplSampler) source() string { return s.id }

// discoverRAPL scans the powercap tree once and builds the samplers it can.
// The three are independent: a host with package zones but no psys gets cpu
// only, and a host whose psys is readable but whose packages are not still
// gets system.
func discoverRAPL(root string, read func(string) ([]byte, error)) (psys, pkg, dram *raplSampler) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, nil, nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	// The sum is order-independent, but a stable order keeps logs and test
	// expectations reproducible.
	sort.Strings(names)

	var psysZones, pkgZones, dramZones []*raplZone
	haveP, haveK, haveD := false, false, false
	// A zone whose NAME cannot be read is not a zone that is absent. It
	// matched the powercap naming, so it exists — the only thing missing is
	// which domain it belongs to.
	//
	// Skipping it was how a partial sum became a domain total: on a two-socket
	// board where package-1's name is unreadable, haveK was set from
	// package-0 and the CPU domain reported one socket as if it were the
	// machine. The affected domains are withheld instead.
	//
	// A top-level entry cannot be assigned to package or psys, so it
	// compromises BOTH. A sub-zone cannot be assigned to dram, so it
	// compromises dram only — and note this counts unreadable NAMES, not
	// named core/uncore sub-zones, which are legitimately not dram
	// contributors and must not be treated as missing ones.
	unnamedTop, unnamedSub := 0, 0
	for _, entry := range names {
		top := raplTopLevel.MatchString(entry)
		sub := raplSubZone.MatchString(entry)
		if !top && !sub {
			continue // control-type dir, or the intel-rapl-mmio mirror
		}
		dir := filepath.Join(root, entry)
		raw, err := read(filepath.Join(dir, "name"))
		if err != nil {
			if top {
				unnamedTop++
			} else {
				unnamedSub++
			}
			continue
		}
		name := strings.TrimSpace(string(raw))
		switch {
		case top && strings.HasPrefix(name, "package"):
			haveK = true
			pkgZones = append(pkgZones, newRAPLZone(read, dir))
		case top && name == "psys":
			// One platform zone per machine in practice; a second would be
			// another view of the same board, so summing would double-count.
			if !haveP {
				haveP = true
				psysZones = append(psysZones, newRAPLZone(read, dir))
			}
		case sub && name == "dram":
			haveD = true
			dramZones = append(dramZones, newRAPLZone(read, dir))
		}
	}

	if unnamedTop > 0 {
		log.Printf("power-history: %d powercap top-level zone(s) have an unreadable name; "+
			"withholding the RAPL cpu and system backends rather than reporting a partial total",
			unnamedTop)
	}
	if unnamedSub > 0 {
		log.Printf("power-history: %d powercap sub-zone(s) have an unreadable name; "+
			"withholding the RAPL dram backend", unnamedSub)
	}
	return buildRAPL(powerhistory.SourceRAPLPsys, psysZones, haveP, unnamedTop == 0, powerSystemMinWatts, read),
		buildRAPL(powerhistory.SourceRAPLPackage, pkgZones, haveK, unnamedTop == 0, 0, read),
		buildRAPL(powerhistory.SourceRAPLDRAM, dramZones, haveD, unnamedSub == 0, 0, read)
}

// buildRAPL refuses a group in which any zone was unreadable: a partial sum
// presented as a domain total is exactly the kind of number this project
// does not report. present distinguishes "no such zones here" from "zones
// exist but cannot be read", which is worth a log line.
func buildRAPL(id string, zones []*raplZone, present, complete bool, minWatts float64, read func(string) ([]byte, error)) *raplSampler {
	if len(zones) == 0 {
		return nil
	}
	// Zones were found, but the scan could not account for everything the
	// powercap tree contains, so this group may not be the whole domain.
	if !complete {
		return nil
	}
	for _, z := range zones {
		if z == nil {
			if present {
				log.Printf("power-history: %s backend unavailable: an energy counter is unreadable", id)
			}
			return nil
		}
	}
	return &raplSampler{id: id, zones: zones, minWatts: minWatts, read: read}
}

// newRAPLZone returns nil when the zone's energy counter cannot be read.
func newRAPLZone(read func(string) ([]byte, error), dir string) *raplZone {
	z := &raplZone{energyPath: filepath.Join(dir, "energy_uj")}
	if _, err := readRAPLUint(read, z.energyPath); err != nil {
		return nil
	}
	if v, err := readRAPLUint(read, filepath.Join(dir, "max_energy_range_uj")); err == nil {
		z.maxRange = v
	}
	return z
}

func readRAPLUint(read func(string) ([]byte, error), path string) (uint64, error) {
	b, err := read(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
}

// sample returns mean watts for the group over the interval since the
// previous successful sample. Counter wrap is corrected with
// max_energy_range_uj; a read error, an implausible interval or an
// implausible result re-primes and reports no value rather than a wrong one.
func (s *raplSampler) sample(now time.Time) (float64, bool) {
	vals := make([]uint64, len(s.zones))
	for i, z := range s.zones {
		v, err := readRAPLUint(s.read, z.energyPath)
		if err != nil {
			// A zone that went away or became unreadable mid-run: the whole
			// group is unavailable, never a stale or partial number.
			s.primed = false
			return 0, false
		}
		vals[i] = v
	}
	if !s.primed {
		s.prime(vals, now)
		return 0, false
	}
	dt := now.Sub(s.lastAt).Seconds()
	if dt <= 0 || dt > 5*powerSampleInterval.Seconds() {
		s.prime(vals, now)
		return 0, false
	}

	// EVERY participating counter has to advance, and it has to advance on its
	// own account.
	//
	// Summing first and judging the total let one busy socket certify a frozen
	// sibling: the sum stayed plausible and was reported as the whole domain,
	// which is an undercount wearing a measurement's label. A group-level
	// floor cannot catch it either — and with minWatts 0, as package and dram
	// have, an ENTIRELY frozen group produced 0.0 W and passed, reporting a
	// running CPU as drawing nothing.
	//
	// A backward step is unavailable, full stop. A wrap and a counter reset
	// (suspend/resume, a driver reload, a module unload) are indistinguishable
	// from two reads, and guessing wrap means inventing up to a full
	// max_energy_range_uj of energy that may never have been consumed —
	// plausibly, at a rate the aggregate ceiling would not reject. One missed
	// sample at rollover is the cheaper error, and the next interval recovers
	// on its own.
	var deltaUJ float64
	for i, z := range s.zones {
		v := vals[i]
		// Validate against the declared range BEFORE subtracting, so no
		// arithmetic happens on values the counter says are impossible.
		if z.maxRange > 0 && (v > z.maxRange || z.last > z.maxRange) {
			s.prime(vals, now)
			return 0, false
		}
		if v < z.last {
			s.prime(vals, now)
			return 0, false
		}
		if v == z.last {
			// This counter did not move. No sibling gets to speak for it.
			s.prime(vals, now)
			return 0, false
		}
		deltaUJ += float64(v - z.last)
	}
	s.prime(vals, now)
	w := deltaUJ / 1e6 / dt
	// minWatts remains the domain's own plausibility floor; it is no longer
	// load-bearing for liveness, which is now per counter.
	if w < s.minWatts || w > powerRateMaxWatts {
		return 0, false
	}
	return w, true
}

func (s *raplSampler) prime(vals []uint64, now time.Time) {
	for i, z := range s.zones {
		z.last = vals[i]
	}
	s.lastAt = now
	s.primed = true
}
