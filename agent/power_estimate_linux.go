//go:build linux

package main

import (
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
)

// Linux inputs for the platform power model: where utilisation comes from and
// how this machine is identified. The model itself is in power_estimate.go.

const (
	procStatPath = "/proc/stat"
	// deviceTreeCompatible is the board identity on ARM. /proc/cpuinfo there
	// names a CPU core, not a machine, and on a big.LITTLE SoC it names
	// whichever core the reader looked at first — on an RK3588 that is a
	// Cortex-A55, which would describe an eight-A55 machine that does not
	// exist. The device tree names the board and the SoC.
	deviceTreeCompatible = "/sys/firmware/devicetree/base/compatible"
	dmiProductName       = "/sys/class/dmi/id/product_name"
)

var powerSpacesRe = regexp.MustCompile(`\s+`)

// procStatUtil derives utilisation from the aggregate "cpu" line of
// /proc/stat. That file is the source gopsutil itself reads; taking it
// directly costs one ~1 KB read per second and, crucially, keeps this loop
// off gopsutil's package-level "last sample" state, which the 30 s metrics
// collector already owns. Two samplers sharing that state would corrupt each
// other's intervals.
//
// Busy time EXCLUDES idle, iowait and steal:
//
//   - iowait is a halted CPU waiting on a device. It draws idle power, so
//     counting it as busy would report a disk-bound machine as loaded. (This
//     differs from gopsutil's cpu.Percent, which counts iowait as busy; that
//     is the right choice for a utilisation gauge and the wrong one here.)
//   - steal is time the hypervisor gave to somebody else. This guest consumed
//     nothing during it.
type procStatUtil struct {
	readFile func(string) ([]byte, error)
	path     string
}

func newProcStatUtil() *procStatUtil {
	return &procStatUtil{readFile: os.ReadFile, path: procStatPath}
}

// read satisfies utilSource.
func (p *procStatUtil) read() (busy, total float64, ok bool) {
	raw, err := p.readFile(p.path)
	if err != nil {
		return 0, 0, false
	}
	line, _, _ := strings.Cut(string(raw), "\n")
	fields := strings.Fields(line)
	// "cpu" plus at least user, nice, system, idle.
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	// user nice system idle iowait irq softirq steal guest guest_nice.
	// guest and guest_nice are already inside user and nice, so summing the
	// first eight is the whole of CPU time exactly once.
	var idleTime float64
	for i, f := range fields[1:] {
		if i >= 8 {
			break
		}
		v, err := strconv.ParseFloat(f, 64)
		if err != nil || v < 0 {
			return 0, 0, false
		}
		total += v
		switch i {
		case 3, 4, 7: // idle, iowait, steal
			idleTime += v
		}
	}
	if total <= 0 {
		return 0, 0, false
	}
	return total - idleTime, total, true
}

// detectPowerIdentity gathers the little the estimator is allowed to key on.
// Every field fails soft: an unidentifiable machine simply gets no profile.
func detectPowerIdentity(read func(string) ([]byte, error)) powerIdentity {
	id := powerIdentity{arch: runtime.GOARCH}
	id.dtCompatible = readDeviceTreeCompatible(read)
	if raw, err := read(dmiProductName); err == nil {
		id.dmiProduct = strings.TrimSpace(string(raw))
	}
	if infos, err := cpu.Info(); err == nil && len(infos) > 0 {
		id.cpuModel = normalizePowerCPUModel(infos[0].ModelName)
	}
	if n, err := cpu.Counts(true); err == nil {
		id.cores = n
	}
	if hi, err := host.Info(); err == nil && hi != nil {
		id.virtualized = strings.EqualFold(hi.VirtualizationRole, "guest") && hi.VirtualizationSystem != ""
	}
	return id
}

// readDeviceTreeCompatible splits the NUL-separated compatible strings, most
// specific first, which is the order the device tree already stores them in.
func readDeviceTreeCompatible(read func(string) ([]byte, error)) []string {
	raw, err := read(deviceTreeCompatible)
	if err != nil {
		return nil
	}
	var out []string
	for _, part := range strings.Split(string(raw), "\x00") {
		if s := strings.ToLower(strings.TrimSpace(part)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// normalizePowerCPUModel lowercases and collapses whitespace so a table key
// survives the vendor decoration in /proc/cpuinfo. Registered trademark marks
// and the "CPU"/"Processor" filler are dropped for the same reason.
func normalizePowerCPUModel(model string) string {
	s := strings.ToLower(strings.TrimSpace(model))
	s = strings.NewReplacer("(r)", "", "(tm)", "", "®", "", "™", "").Replace(s)
	s = powerSpacesRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// --- wiring ---

// powerEstimateEnv is the estimator's injectable surface, kept apart from
// powerEnv so a test can drive identity, environment and utilisation without
// building a whole sysfs tree.
type powerEstimateEnv struct {
	read     func(string) ([]byte, error)
	getenv   func(string) string
	identity func(read func(string) ([]byte, error)) powerIdentity
	util     func() utilSource
	// nvidiaPresent reports whether nvidia-smi will be streaming GPU power.
	nvidiaPresent func() bool
}

func defaultPowerEstimateEnv() powerEstimateEnv {
	return powerEstimateEnv{
		read:          os.ReadFile,
		getenv:        os.Getenv,
		identity:      detectPowerIdentity,
		util:          func() utilSource { return newProcStatUtil() },
		nvidiaPresent: func() bool { return resolveNvidiaSmiPath() != "" },
	}
}

// estimationAllowed is the gate that keeps a modelled figure from ever
// standing next to a measured one on the same machine.
//
// The obvious rule — "estimate system only when no system counter exists" —
// is not enough. Two failure modes come from the SAME mistake, publishing a
// modelled platform figure beside a measured component:
//
//   - RAPL package works but psys does not (an ordinary desktop). The
//     estimate would be a platform number sitting beside a real CPU number,
//     inviting "platform minus CPU = everything else", which is pure noise.
//     Worse, a wrong envelope can put estimated system BELOW measured cpu,
//     which is visibly impossible and discredits every other reading the
//     agent ships.
//   - A GPU is being measured. The model is driven by CPU utilisation and
//     knows nothing about a 300 W card, so estimated system would land far
//     below measured GPU power. Same impossible relation, larger.
//
// So the estimator engages only on a machine that is measuring NOTHING. That
// is exactly the population it was built for, and on it there is no measured
// value for a modelled one to be compared against.
//
// Residual limitation, stated rather than papered over: a discrete GPU with
// no power counter at all is invisible to this check and absent from the
// model, so such a machine would be badly under-reported. On the target class
// (ARM boards, small x86 nodes, guests) that combination does not arise.
func estimationAllowed(set powerSourceSet, nvidia bool) bool {
	return set.system == nil && set.cpu == nil && set.dram == nil && set.gpu == nil && !nvidia
}

// attachPowerEstimate installs the modelled system backend on a machine that
// measures nothing, or leaves the set untouched. It runs AFTER
// pickSystemSource has exhausted every measured backend.
func attachPowerEstimate(set powerSourceSet, env powerEstimateEnv) powerSourceSet {
	if !estimationAllowed(set, env.nvidiaPresent()) {
		return set
	}
	if powerEstimateDisabled(env.getenv) {
		logPowerEstimate("disabled by %s", powerEstimateEnvDisable)
		return set
	}
	id := env.identity(env.read)
	profile, ok := resolvePowerProfile(id, env.getenv)
	if !ok {
		logPowerEstimate("no defensible power envelope for this machine (arch=%s cores=%d cpu=%q board=%v); "+
			"reporting no system power. Set %s=\"<idle>:<max>\" (watts at the wall) to enable it.",
			id.arch, id.cores, id.cpuModel, id.dtCompatible, powerEstimateEnvWatts)
		return set
	}
	sampler := newEstimateSampler(profile, env.util(), powerSampleInterval)
	logPowerEstimate("no power counter on this host; modelling system power from CPU utilisation: "+
		"%.1f W idle to %.1f W loaded, basis: %s. This is an ESTIMATE labelled %q, not a measurement; "+
		"set %s=\"<idle>:<max>\" from a wall meter to correct it, or %s=off to refuse it.",
		profile.idleWatts, profile.maxWatts, profile.basis, sampler.source(),
		powerEstimateEnvWatts, powerEstimateEnvDisable)
	set.system = sampler
	return set
}
