//go:build linux

package main

import (
	"context"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
)

// Software power backends on Linux.
//
// There is no single counter every machine has, so the agent detects an
// ordered set of backends once at startup, logs what it found, and samples
// the winner of each domain every tick. Two domains are kept strictly apart:
//
//	system — whole-platform power. RAPL psys → battery discharge →
//	         IPMI/DCMI → a whole-board hwmon shunt.
//	cpu    — CPU package power (RAPL package sum), with dram reported
//	         separately when the RAPL dram sub-zones exist.
//
// NOTHING here estimates. A machine with no counter reports nothing: ARM
// SoCs such as the RK3588 expose regulator voltages with no current, so
// watts cannot be derived and are not invented.
//
// Nothing here is ever summed across domains either. Where psys exists it
// already contains the packages, so it is PREFERRED OVER the package sum for
// `system` while the package sum still populates `cpu` — different fields,
// not one number.

const (
	powerSupplyRoot = "/sys/class/power_supply"
	powerHwmonRoot  = "/sys/class/hwmon"
	powerDRMRoot    = "/sys/class/drm"

	// powerSystemMinWatts is the plausibility floor for a whole-system
	// reading. A running board cannot draw less; a counter reporting below
	// it (a psys zone a vendor exposes but never advances, a battery whose
	// driver reports 0 while discharging) is unavailable, not zero watts.
	powerSystemMinWatts = 0.5
	// powerBatteryMaxWatts bounds a battery discharge reading. No laptop
	// pack sustains a kilowatt; above it the units or sign are wrong.
	powerBatteryMaxWatts = 1000.0
	// powerIPMIInterval paces BMC round trips. A DCMI read costs hundreds of
	// milliseconds, so it runs well below the sample cadence and simply
	// contributes fewer samples to the window.
	powerIPMIInterval = 5 * time.Second
	// powerIPMITimeout bounds one ipmitool invocation.
	powerIPMITimeout = 3 * time.Second
	// powerPsysProbeDelay is the gap between the two reads that decide
	// whether a psys zone actually advances.
	powerPsysProbeDelay = 600 * time.Millisecond
)

var (
	powerHwmonDirRe = regexp.MustCompile(`^hwmon\d+$`)
	powerDRMCardRe  = regexp.MustCompile(`^card\d+$`)
	powerIDUnsafeRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
)

// powerHwmonWholeSystem is the deliberately short list of hwmon chips whose
// power1 channel is credibly the WHOLE BOARD rather than one component.
// Anything not listed is left alone: reporting a component sensor as system
// power would be exactly the kind of misattribution this feature exists to
// avoid. Extending the list is a one-line change backed by a datasheet.
var powerHwmonWholeSystem = map[string]bool{
	"power_meter": true, // ACPI 4.0 power meter: platform input power
	"ina219":      true, // shunt monitors, wired on the input rail of many
	"ina226":      true, // SBCs and instrumented carrier boards
	"ina230":      true,
	"ina231":      true,
	"ina238":      true,
	"ina260":      true,
}

// powerIPMIDevices are the character devices a working BMC interface
// creates. Their absence means no ipmitool is spawned at all.
var powerIPMIDevices = []string{"/dev/ipmi0", "/dev/ipmi/0", "/dev/ipmidev/0"}

// powerEnv is the host surface detection runs against. Directory layout is
// read from the real filesystem (tests build genuine sysfs-shaped trees);
// file reads, symlink resolution and command execution are injectable so
// permission failures and BMC behaviour can be exercised.
type powerEnv struct {
	raplRoot   string
	supplyRoot string
	hwmonRoot  string
	drmRoot    string
	read       func(string) ([]byte, error)
	readLink   func(string) (string, error)
	statDev    func(string) bool
	lookPath   func(string) (string, error)
	ipmiRead   func(tool string) (float64, bool)
	sleep      func(time.Duration)
	now        func() time.Time
}

func defaultPowerEnv() powerEnv {
	return powerEnv{
		raplRoot:   raplRoot,
		supplyRoot: powerSupplyRoot,
		hwmonRoot:  powerHwmonRoot,
		drmRoot:    powerDRMRoot,
		read:       os.ReadFile,
		readLink:   os.Readlink,
		statDev: func(p string) bool {
			_, err := os.Stat(p)
			return err == nil
		},
		lookPath: exec.LookPath,
		ipmiRead: readIPMIDCMIWatts,
		sleep:    time.Sleep,
		now:      time.Now,
	}
}

// newPowerSources detects this host's power backends. It is the single
// platform entry point; the non-Linux build returns an empty set.
func newPowerSources() powerSourceSet {
	return detectPowerSources(defaultPowerEnv())
}

func detectPowerSources(env powerEnv) powerSourceSet {
	psys, pkg, dram := discoverRAPL(env.raplRoot, env.read)
	set := powerSourceSet{gpu: discoverDRMGPUs(env)}
	if pkg != nil {
		set.cpu = pkg
	}
	if dram != nil {
		set.dram = dram
	}
	set.system = pickSystemSource(env, psys)
	if set.system != nil {
		log.Printf("power-history: system power backend: %s", set.system.source())
	}
	if set.cpu != nil {
		log.Printf("power-history: cpu power backend: %s (%d RAPL package zone(s))", set.cpu.source(), len(pkg.zones))
	}
	if set.dram != nil {
		log.Printf("power-history: dram power backend: %s (%d RAPL dram zone(s))", set.dram.source(), len(dram.zones))
	}
	return set
}

// pickSystemSource applies the whole-platform preference order. Each
// candidate is only accepted once it has demonstrated it can produce a
// reading on this host, so a zone a vendor exposes but never populates does
// not shadow a backend that works.
func pickSystemSource(env powerEnv, psys *raplSampler) powerSource {
	if psys != nil {
		if probeCounterLive(psys, env) {
			return psys
		}
		log.Printf("power-history: %s present but not advancing; trying the next system backend", psys.source())
	}
	// A battery is judged by whether the device and its files exist, never
	// by whether it happens to be discharging right now: on AC it correctly
	// reports nothing and starts reporting again the moment it is unplugged.
	if b := discoverBattery(env); b != nil {
		return b
	}
	if i := discoverIPMI(env); i != nil {
		return i
	}
	if h := discoverWholeSystemHwmon(env); h != nil {
		return h
	}
	return nil
}

// probeCounterLive takes the two reads an energy counter needs to yield a
// rate and reports whether the result was plausible.
func probeCounterLive(s powerSource, env powerEnv) bool {
	s.sample(env.now())
	env.sleep(powerPsysProbeDelay)
	_, ok := s.sample(env.now())
	return ok
}

// --- battery ---

// batterySampler reads genuine whole-system discharge power from the
// power_supply class. While a pack discharges, its output IS what the
// machine is consuming, which is the same quantity a wall meter would show
// (minus charger losses that are not happening). While charging or full it
// measures charge current instead, which is not system power, so the backend
// reports nothing.
type batterySampler struct {
	dirs []string
	read func(string) ([]byte, error)
}

func (b *batterySampler) source() string { return powerhistory.SourceBattery }

// discoverBattery finds system batteries: type Battery, and scope System (or
// unset). scope Device is a peripheral's pack — a wireless mouse or keyboard
// — and has nothing to do with this machine's power.
func discoverBattery(env powerEnv) *batterySampler {
	entries, err := os.ReadDir(env.supplyRoot)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	var dirs []string
	for _, name := range names {
		dir := filepath.Join(env.supplyRoot, name)
		if !sysfsStringIs(env.read, filepath.Join(dir, "type"), "Battery") {
			continue
		}
		if scope, err := env.read(filepath.Join(dir, "scope")); err == nil &&
			!strings.EqualFold(strings.TrimSpace(string(scope)), "System") {
			continue
		}
		if _, ok := batteryWatts(env.read, dir); !ok {
			// Present but with no usable power reading at all (no power_now
			// and no current/voltage pair, or unreadable). Not a backend.
			continue
		}
		dirs = append(dirs, dir)
	}
	if len(dirs) == 0 {
		return nil
	}
	return &batterySampler{dirs: dirs, read: env.read}
}

// sample sums every discharging system battery. A pack that is charging or
// full contributes nothing; when none is discharging there is no reading.
// A battery whose files have vanished (removed pack, permission change)
// makes the whole backend unavailable for that tick rather than turning a
// two-pack machine into a silent one-pack undercount.
func (b *batterySampler) sample(time.Time) (float64, bool) {
	total, discharging := 0.0, false
	for _, dir := range b.dirs {
		raw, err := b.read(filepath.Join(dir, "status"))
		if err != nil {
			return 0, false
		}
		if !strings.EqualFold(strings.TrimSpace(string(raw)), "Discharging") {
			continue
		}
		w, ok := batteryWatts(b.read, dir)
		if !ok {
			return 0, false
		}
		total += w
		discharging = true
	}
	if !discharging || total < powerSystemMinWatts || total > powerBatteryMaxWatts {
		return 0, false
	}
	return total, true
}

// batteryWatts prefers power_now (µW) and falls back to current_now (µA) ×
// voltage_now (µV), which the charge-reporting drivers expose instead. Both
// are magnitudes: some drivers sign discharge negative, and the caller has
// already established the direction from status.
func batteryWatts(read func(string) ([]byte, error), dir string) (float64, bool) {
	if v, ok := readSysInt(read, filepath.Join(dir, "power_now")); ok {
		if w := math.Abs(float64(v)) / 1e6; w > 0 {
			return w, true
		}
	}
	cur, curOK := readSysInt(read, filepath.Join(dir, "current_now"))
	volt, voltOK := readSysInt(read, filepath.Join(dir, "voltage_now"))
	if !curOK || !voltOK {
		return 0, false
	}
	w := math.Abs(float64(cur)) * math.Abs(float64(volt)) / 1e12
	if w <= 0 {
		return 0, false
	}
	return w, true
}

// --- generic hwmon ---

// hwmonSampler reads an instantaneous µW power channel from a hwmon chip
// whose scope is the whole board.
type hwmonSampler struct {
	id   string
	path string
	read func(string) ([]byte, error)
}

func (h *hwmonSampler) source() string { return h.id }

func (h *hwmonSampler) sample(time.Time) (float64, bool) {
	v, ok := readSysInt(h.read, h.path)
	if !ok {
		return 0, false
	}
	w := float64(v) / 1e6
	if w < powerSystemMinWatts || w > powerRateMaxWatts {
		return 0, false
	}
	return w, true
}

func discoverWholeSystemHwmon(env powerEnv) *hwmonSampler {
	for _, dir := range sortedSubdirs(env.hwmonRoot, powerHwmonDirRe) {
		raw, err := env.read(filepath.Join(dir, "name"))
		if err != nil {
			continue
		}
		chip := strings.TrimSpace(string(raw))
		if !powerHwmonWholeSystem[chip] {
			continue
		}
		path, ok := hwmonPowerChannel(env.read, dir)
		if !ok {
			continue
		}
		return &hwmonSampler{id: powerhistory.SourceHwmonPrefix + sanitizePowerID(chip), path: path, read: env.read}
	}
	return nil
}

// hwmonPowerChannel picks the readable power1 channel, preferring the
// averaged one where a chip offers both.
func hwmonPowerChannel(read func(string) ([]byte, error), dir string) (string, bool) {
	for _, name := range []string{"power1_average", "power1_input"} {
		p := filepath.Join(dir, name)
		if _, ok := readSysInt(read, p); ok {
			return p, true
		}
	}
	return "", false
}

// --- IPMI / DCMI ---

// ipmiSampler asks the BMC for the chassis' instantaneous input power. That
// is a genuine whole-system measurement taken by the platform itself, which
// is why it outranks a board sensor.
type ipmiSampler struct {
	tool     string
	read     func(tool string) (float64, bool)
	interval time.Duration
	next     time.Time
}

func (i *ipmiSampler) source() string { return powerhistory.SourceIPMIDCMI }

// sample rate-limits itself: BMC round trips are slow, so most ticks return
// no value and the window simply records fewer samples than it expected.
func (i *ipmiSampler) sample(now time.Time) (float64, bool) {
	if !i.next.IsZero() && now.Before(i.next) {
		return 0, false
	}
	i.next = now.Add(i.interval)
	w, ok := i.read(i.tool)
	if !ok || w < powerSystemMinWatts || w > powerRateMaxWatts {
		return 0, false
	}
	return w, true
}

func discoverIPMI(env powerEnv) *ipmiSampler {
	present := false
	for _, dev := range powerIPMIDevices {
		if env.statDev(dev) {
			present = true
			break
		}
	}
	if !present {
		return nil // no BMC interface: never spawn ipmitool
	}
	tool, err := env.lookPath("ipmitool")
	if err != nil {
		return nil
	}
	if _, ok := env.ipmiRead(tool); !ok {
		return nil // interface present but DCMI unsupported or unanswered
	}
	return &ipmiSampler{tool: tool, read: env.ipmiRead, interval: powerIPMIInterval}
}

var ipmiDCMIRe = regexp.MustCompile(`(?i)instantaneous power reading:\s*([0-9]+(?:\.[0-9]+)?)`)

// readIPMIDCMIWatts runs one bounded `ipmitool dcmi power reading`. Any
// failure — missing DCMI support, a busy BMC, a timeout, unparsable output —
// is simply "no reading".
func readIPMIDCMIWatts(tool string) (float64, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), powerIPMITimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, "dcmi", "power", "reading")
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return 0, false
	}
	m := ipmiDCMIRe.FindSubmatch(out)
	if m == nil {
		return 0, false
	}
	w, err := strconv.ParseFloat(string(m[1]), 64)
	if err != nil {
		return 0, false
	}
	return w, true
}

// --- DRM GPU hwmon (AMD, and any other non-NVIDIA DRM driver) ---

type drmGPUDevice struct {
	id   string
	path string
}

// drmGPUPoller reads every discovered DRM power channel in one pass, so its
// ticks carry the simultaneous observation a GPU total requires.
//
// NVIDIA cards are skipped: nvidia-smi already streams them, and reporting
// one device under two identifiers would inflate the sensor list and break
// the total. On a machine with both an NVIDIA card and an AMD one the two
// producers report different device sets, so the accumulator omits the GPU
// total for those windows — the same rule it applies to any changing set,
// and the honest answer when no single simultaneous observation exists.
type drmGPUPoller struct {
	devices []drmGPUDevice
	read    func(string) ([]byte, error)
}

func discoverDRMGPUs(env powerEnv) powerGPUPoller {
	var devices []drmGPUDevice
	for _, card := range sortedSubdirs(env.drmRoot, powerDRMCardRe) {
		device := filepath.Join(card, "device")
		for _, hw := range sortedSubdirs(filepath.Join(device, "hwmon"), powerHwmonDirRe) {
			raw, err := env.read(filepath.Join(hw, "name"))
			if err != nil {
				continue
			}
			chip := strings.TrimSpace(string(raw))
			if chip == "" || strings.EqualFold(chip, "nvidia") {
				continue
			}
			path, ok := hwmonPowerChannel(env.read, hw)
			if !ok {
				continue
			}
			devices = append(devices, drmGPUDevice{id: drmGPUID(env, chip, device), path: path})
			break // one power channel per card
		}
	}
	if len(devices) == 0 {
		return nil // interface nil, not a typed nil
	}
	ids := make([]string, len(devices))
	for i, d := range devices {
		ids[i] = d.id
	}
	log.Printf("power-history: gpu power backend: DRM hwmon [%s]", strings.Join(ids, " "))
	return &drmGPUPoller{devices: devices, read: env.read}
}

// drmGPUID prefers the PCI address behind the card, which survives reboots
// and re-enumeration. Without it the card index is used, which is stable for
// the life of the process but not across reboots.
func drmGPUID(env powerEnv, chip, device string) string {
	if target, err := env.readLink(device); err == nil {
		if slot := filepath.Base(strings.TrimSpace(target)); slot != "" && slot != "." && slot != "/" {
			return sanitizePowerID(chip + "-" + slot)
		}
	}
	return sanitizePowerID(chip + "-" + filepath.Base(filepath.Dir(device)))
}

// poll reads every device once. A device that has gone away reports nil
// watts (unavailable) while the tick still counts as complete, because the
// set of devices is unchanged — which is what lets the accumulator tell
// "sensor missing" apart from "device removed".
func (p *drmGPUPoller) poll(now time.Time) powerGPUTick {
	readings := make([]powerReading, 0, len(p.devices))
	for _, d := range p.devices {
		r := powerReading{id: d.id}
		if v, ok := readSysInt(p.read, d.path); ok {
			w := float64(v) / 1e6
			if w >= 0 && w <= powerRateMaxWatts {
				r.watts = &w
			}
		}
		readings = append(readings, r)
	}
	return powerGPUTick{at: now, readings: readings, complete: true}
}

// --- shared helpers ---

func readSysInt(read func(string) ([]byte, error), path string) (int64, bool) {
	b, err := read(path)
	if err != nil {
		return 0, false
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func sysfsStringIs(read func(string) ([]byte, error), path, want string) bool {
	b, err := read(path)
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(string(b)), want)
}

// sortedSubdirs lists entries of root matching re, in name order, as full
// paths. A missing root is simply no entries.
func sortedSubdirs(root string, re *regexp.Regexp) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if re.MatchString(e.Name()) {
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	sort.Strings(out)
	return out
}

// sanitizePowerID keeps identifiers inside the character set the hub accepts
// for sensor ids, so a PCI address (0000:03:00.0) stays readable.
func sanitizePowerID(s string) string {
	return strings.Trim(powerIDUnsafeRe.ReplaceAllString(s, "-"), "-")
}
