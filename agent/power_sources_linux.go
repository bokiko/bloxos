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
	"github.com/shirou/gopsutil/v4/host"
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
// NOTHING here estimates, and nothing anywhere else does either. Every backend
// in this file reads a counter.
//
// A machine that measures NOTHING AT ALL — no RAPL, no BMC, no GPU counter,
// the ordinary state of an RK3588-class ARM SoC (regulator voltages, no
// current sense) — REPORTS NOTHING. There is no modelled fallback to reach and
// no code left to reach it: a machine with no counter reports nothing, and a
// number derived from a utilisation curve is not a measurement no matter how
// carefully it is labelled.
//
// Nothing here is ever summed across domains either. Where psys exists it
// already contains the packages, so it is PREFERRED OVER the package sum for
// `system` while the package sum still populates `cpu` — different fields,
// not one number.

const (
	powerSupplyRoot = "/sys/class/power_supply"
	powerHwmonRoot  = "/sys/class/hwmon"
	powerDRMRoot    = "/sys/class/drm"

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

// There is no hwmon whole-system list any more, and adding one back needs a
// scope contract, not a longer table of chip names.
//
// A chip name is evidence that something MEASURES. It is not evidence of WHAT
// it measures. A shunt monitor reports whatever rail it is wired across — a
// 5V rail, a GPU rail, a fan rail — and nothing in `name` distinguishes a
// board's input rail from a component's. ACPI's power_meter is no better: the
// kernel's own documentation gives `power*_is_battery` for battery supplies
// and the `measures/` symlinks for the devices a meter covers, which is
// exactly the admission that the chip alone does not say.
//
// So generic hwmon is no longer selected as a SYSTEM backend at all. The
// device-scoped DRM GPU hwmon path is untouched: there the scope comes from
// the device the sensor hangs off, not from its name.

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
	// inGuest reports whether this kernel is running inside a VM guest.
	// Injectable so both answers are testable without a hypervisor.
	inGuest func() bool
	sleep   func(time.Duration)
	now     func() time.Time
}

// runningInVMGuest reports whether this is a GUEST, which is not the same
// question as whether virtualization is present.
//
// A KVM host runs virtual machines and has real RAPL counters measuring real
// silicon; gopsutil reports its role as "host". A guest may see RAPL that is
// absent, emulated, or passed through from a package it shares with other
// tenants — in none of those cases does the counter describe that VM. Only the
// guest role withholds.
//
// gopsutil's own detection is the source, deliberately: no systemd-detect-virt
// invocation, no CPUID or DMI heuristic of our own to keep correct.
func runningInVMGuest() bool {
	hi, err := host.Info()
	if err != nil || hi == nil {
		return false // unknown is not a guest; the counters stand or fall on their own
	}
	return strings.EqualFold(hi.VirtualizationRole, "guest") && hi.VirtualizationSystem != ""
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
		inGuest:  runningInVMGuest,
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
	// Inside a guest, EVERY RAPL domain is withheld — cpu and dram as well as
	// system. A package counter measures physical silicon that the VM shares
	// with tenants it cannot see, so "this VM's CPU power" is not a quantity
	// those counters answer, whether they are emulated, passed through, or
	// simply inherited. GPU reporting is unaffected: a passed-through device
	// is measured at the device.
	if env.inGuest != nil && env.inGuest() {
		if psys != nil || pkg != nil || dram != nil {
			log.Printf("power-history: running in a VM guest; withholding all RAPL backends — " +
				"package counters measure the host's silicon, not this guest")
		}
		psys, pkg, dram = nil, nil, nil
	}
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

// pickSystemSource applies the whole-platform preference order.
//
// Exactly two backends measure whole-platform power with a scope this code can
// defend: RAPL psys, which is the platform domain by definition, and an
// in-band DCMI reading the BMC says it is actually taking.
//
// Everything else that used to be here has gone, and for one reason: none of
// it established SCOPE. A battery's discharge is the machine's draw only while
// the machine runs on that battery alone — on AC, or with one pack charging
// while another discharges, the packs account for part of the load and the
// inputs available here cannot say which part. Generic hwmon named a chip and
// inferred a board. Both produced numbers; neither produced whole-system
// numbers, and a number labelled with the wrong scope is worse than silence
// because nothing downstream can correct it.
//
// A host with neither counter reports no system power. That is the rule.
func pickSystemSource(env powerEnv, psys *raplSampler) powerSource {
	if psys != nil {
		if probeCounterLive(psys, env) {
			return psys
		}
		log.Printf("power-history: %s present but not advancing; trying the next system backend", psys.source())
	}
	if i := discoverIPMI(env); i != nil {
		return i
	}
	// Nothing on this host MEASURES whole-platform power, so nothing is
	// reported for it. There is no modelled fallback to reach.
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

// --- battery: removed as a power source ---
//
// A discharging pack's output IS the machine's draw, but only while the
// machine runs on that pack alone. On AC, or with one pack charging while
// another discharges, the packs supply part of the load and nothing available
// here says which part — the old code summed the discharging subset and
// labelled it whole-system power.
//
// Two narrower defects fell out of the same path: discovery dropped a pack
// with no usable reading before the sampler was built, so a two-pack machine
// silently became a one-pack undercount; and a sibling that was charging or
// full contributed nothing while the reading still counted as a system total.
//
// Establishing when a battery IS the whole supply needs inputs this code does
// not have, so generation is removed rather than patched. powerhistory.
// SourceBattery stays: rows already recorded still decode, and agents that
// predate this change still report.

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
	// A DIRECT reading is not an energy counter, and the two need different
	// rules. powerSystemMinWatts exists to catch a RAPL zone a vendor exposes
	// but never advances — a frozen counter is indistinguishable from a
	// board drawing nothing, so the floor breaks the tie. Here there is no tie
	// to break: the BMC says the reading is ACTIVE, so an active zero is the
	// BMC's answer, not an absence of one. Applying the counter's floor threw
	// it away, which would have made the project's promise about real zeros
	// untrue exactly where it is most literal.
	//
	// The range is still bounded on both ends; only the lower bound moves to
	// where it belongs for a direct reading.
	if !ok || w < 0 || w > powerRateMaxWatts {
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

// ipmiDCMIRe matches the WHOLE reading line, units included.
//
// A numeric prefix is not a reading. Anchored loosely, "212garbage Watts"
// yields 212 and "1e6 Watts" yields 1 — a malformed line silently becoming a
// plausible wattage is worse than no line at all, because nothing downstream
// can tell the difference.
var ipmiDCMIRe = regexp.MustCompile(`(?im)^[ \t]*instantaneous power reading:[ \t]*([0-9]+(?:\.[0-9]+)?)[ \t]+watts[ \t]*\r?$`)

// ipmiDCMIStateRe captures the reading state ipmitool prints beneath the value,
// as a whole line for the same reason.
var ipmiDCMIStateRe = regexp.MustCompile(`(?im)^[ \t]*power reading state is:[ \t]*(\S+)[ \t]*\r?$`)

// parseIPMIDCMIWatts turns one `ipmitool dcmi power reading` into watts.
//
// The STATE LINE is not optional. ipmitool prints the numeric value and exits
// zero even when the BMC reports "Power reading state is: deactivated"
// (lib/ipmi_dcmi.c, ipmi_dcmi_pwr_rd), so a number plus a successful exit is
// not evidence that anything is being measured. Reading only the value turned
// a deactivated sensor into fresh measured whole-system watts.
//
// Absent is refused as firmly as deactivated: this is the one line that says
// the reading means something, and output that does not contain it is output
// this function does not understand.
func parseIPMIDCMIWatts(out []byte) (float64, bool) {
	states := ipmiDCMIStateRe.FindAllSubmatch(out, -1)
	if len(states) != 1 || !strings.EqualFold(string(states[0][1]), "activated") {
		// Zero states is output this function does not understand. More than
		// one is output it cannot resolve — two states in one response mean
		// the reading they refer to is not identifiable, and picking the first
		// would be a guess.
		return 0, false
	}
	values := ipmiDCMIRe.FindAllSubmatch(out, -1)
	if len(values) != 1 {
		return 0, false
	}
	w, err := strconv.ParseFloat(string(values[0][1]), 64)
	// The regex already excludes exponents, signs and NaN, but the range check
	// is what keeps an absurd magnitude out of the fleet total.
	if err != nil || math.IsNaN(w) || math.IsInf(w, 0) || w < 0 || w > powerRateMaxWatts {
		return 0, false
	}
	return w, true
}

// readIPMIDCMIWatts runs one bounded `ipmitool dcmi power reading`. Any
// failure — missing DCMI support, a busy BMC, a timeout, unparsable output,
// or a reading the BMC is not actually taking — is simply "no reading".
func readIPMIDCMIWatts(tool string) (float64, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), powerIPMITimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, "dcmi", "power", "reading")
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return 0, false
	}
	return parseIPMIDCMIWatts(out)
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
