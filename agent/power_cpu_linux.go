//go:build linux

package main

import (
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Linux CPU package power comes from the powercap RAPL energy counters.
// Only top-level, non-overlapping package zones are summed:
//
//	/sys/class/powercap/intel-rapl:<n>   name "package-<n>"
//
// Sub-zones (intel-rapl:<n>:<m> — core/uncore/dram), the platform zone
// (psys, which contains the packages) and the MMIO mirror
// (intel-rapl-mmio:<n>, the same package via a second interface) are
// excluded so nothing is counted twice. The counters are readable by root
// only on current kernels; the agent runs as root. Anything else (no
// powercap, VM without RAPL, unreadable) reports the backend as unavailable.

const raplRoot = "/sys/class/powercap"

var raplTopLevel = regexp.MustCompile(`^intel-rapl:\d+$`)

type raplZone struct {
	energyPath string
	maxRange   uint64
	last       uint64
}

type raplSampler struct {
	zones  []*raplZone
	lastAt time.Time
	primed bool
	read   func(string) ([]byte, error)
}

func newPowerCPUSampler() powerCPUSampler {
	s := discoverRAPL(raplRoot, os.ReadFile)
	if s == nil {
		return nil // interface nil, not a typed nil
	}
	return s
}

func discoverRAPL(root string, read func(string) ([]byte, error)) *raplSampler {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	s := &raplSampler{read: read}
	for _, e := range entries {
		if !raplTopLevel.MatchString(e.Name()) {
			continue
		}
		dir := filepath.Join(root, e.Name())
		name, err := read(filepath.Join(dir, "name"))
		if err != nil || !strings.HasPrefix(strings.TrimSpace(string(name)), "package") {
			continue
		}
		z := &raplZone{energyPath: filepath.Join(dir, "energy_uj")}
		if _, err := readRAPLUint(read, z.energyPath); err != nil {
			// A package we can see but not read would make the sum a
			// silent partial. All or nothing.
			log.Printf("power-history: CPU energy backend unavailable: %s unreadable: %v", z.energyPath, err)
			return nil
		}
		if v, err := readRAPLUint(read, filepath.Join(dir, "max_energy_range_uj")); err == nil {
			z.maxRange = v
		}
		s.zones = append(s.zones, z)
	}
	if len(s.zones) == 0 {
		return nil
	}
	log.Printf("power-history: CPU energy backend: %d RAPL package zone(s)", len(s.zones))
	return s
}

func readRAPLUint(read func(string) ([]byte, error), path string) (uint64, error) {
	b, err := read(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
}

// sample returns mean package watts over the interval since the previous
// successful sample. Counter wrap is corrected with max_energy_range_uj; a
// read error, an implausible interval or an implausible result re-primes
// and reports no value rather than a wrong one.
func (s *raplSampler) sample(now time.Time) (float64, bool) {
	vals := make([]uint64, len(s.zones))
	for i, z := range s.zones {
		v, err := readRAPLUint(s.read, z.energyPath)
		if err != nil {
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
	var deltaUJ float64
	for i, z := range s.zones {
		v := vals[i]
		var d uint64
		switch {
		case v >= z.last:
			d = v - z.last
		case z.maxRange > 0:
			d = z.maxRange - z.last + v
		default:
			s.prime(vals, now)
			return 0, false
		}
		deltaUJ += float64(d)
	}
	s.prime(vals, now)
	w := deltaUJ / 1e6 / dt
	if w < 0 || w > 10000 {
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
