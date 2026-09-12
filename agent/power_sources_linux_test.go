//go:build linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
)

// newFakePowerEnv builds real sysfs-shaped directories under a temp root, so
// discovery walks a genuine filesystem exactly as it does on a host. Only
// the BMC surface is stubbed, and it starts absent: a host without a BMC
// never runs ipmitool.
func newFakePowerEnv(t *testing.T) powerEnv {
	t.Helper()
	root := t.TempDir()
	// Detection sleeps to let an energy counter advance; a virtual clock
	// keeps that deterministic and instant.
	clock := time.Now()
	e := powerEnv{
		raplRoot:   filepath.Join(root, "powercap"),
		supplyRoot: filepath.Join(root, "power_supply"),
		hwmonRoot:  filepath.Join(root, "hwmon"),
		drmRoot:    filepath.Join(root, "drm"),
		read:       os.ReadFile,
		readLink:   os.Readlink,
		statDev:    func(string) bool { return false },
		lookPath:   func(string) (string, error) { return "", errors.New("not found") },
		ipmiRead:   func(string) (float64, bool) { return 0, false },
		sleep:      func(d time.Duration) { clock = clock.Add(d) },
		now:        func() time.Time { return clock },
	}
	for _, d := range []string{e.raplRoot, e.supplyRoot, e.hwmonRoot, e.drmRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func writeSysfs(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, v := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func writeRAPLZone(t *testing.T, root, zone, name string, energyUJ uint64) string {
	t.Helper()
	return writeSysfs(t, filepath.Join(root, zone), map[string]string{
		"name":                name,
		"energy_uj":           strconv.FormatUint(energyUJ, 10),
		"max_energy_range_uj": "262143328850",
	})
}

func setEnergy(t *testing.T, dir string, energyUJ uint64) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "energy_uj"), []byte(strconv.FormatUint(energyUJ, 10)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- backends removed because they could not establish SCOPE ---

// A discharging pack's output is the machine's draw only while the machine
// runs on that pack ALONE. On AC, or with one pack charging while another
// discharges, the packs supply part of the load and nothing here says which
// part — the old code summed the discharging subset and called it
// whole-system power.
func TestABatteryIsNoLongerASystemPowerSource(t *testing.T) {
	env := newFakePowerEnv(t)
	writeSysfs(t, filepath.Join(env.supplyRoot, "BAT0"), map[string]string{
		"type": "Battery", "scope": "System", "status": "Discharging", "power_now": "23500000",
	})
	writeSysfs(t, filepath.Join(env.supplyRoot, "BAT1"), map[string]string{
		"type": "Battery", "scope": "System", "status": "Charging", "power_now": "5000000",
	})
	if set := detectPowerSources(env); set.system != nil {
		t.Fatalf("a battery became a system backend again: %s", set.system.source())
	}
}

// A chip name is evidence that something MEASURES. It is not evidence of WHAT
// it measures: a shunt reports whatever rail it sits on, and ACPI's own
// documentation gives power*_is_battery and the measures/ symlinks precisely
// because power_meter alone does not say either.
func TestGenericHwmonIsNoLongerASystemPowerSource(t *testing.T) {
	for _, chip := range []string{"power_meter", "ina226", "ina219", "ina260"} {
		t.Run(chip, func(t *testing.T) {
			env := newFakePowerEnv(t)
			writeSysfs(t, filepath.Join(env.hwmonRoot, "hwmon0"), map[string]string{
				"name": chip, "power1_average": "98000000",
			})
			if set := detectPowerSources(env); set.system != nil {
				t.Fatalf("%s was inferred to be whole-system power: %s", chip, set.system.source())
			}
		})
	}
}

// CONTROL for both of the above: the harness CAN produce a system backend, so
// those negatives are about the backends and not about a detector that never
// finds anything in this fixture.
func TestAnActiveBMCIsStillASystemPowerSource(t *testing.T) {
	env := newFakePowerEnv(t)
	env.statDev = func(string) bool { return true }
	env.lookPath = func(string) (string, error) { return "/usr/bin/ipmitool", nil }
	env.ipmiRead = func(string) (float64, bool) { return 118, true }
	set := detectPowerSources(env)
	if set.system == nil {
		t.Fatal("control: an answering BMC must still provide system power")
	}
	if set.system.source() != powerhistory.SourceIPMIDCMI {
		t.Fatalf("system backend is %q", set.system.source())
	}
}

// An ACTIVE zero is the BMC's answer, not the absence of one. The energy
// counter's floor exists to catch a zone that never advances; a direct
// reading has no such ambiguity to resolve, and applying that floor here
// discarded exactly the real zero this project promises to show.
func TestAnActiveZeroFromTheBMCSurvivesToTheSampler(t *testing.T) {
	const activeZero = "Instantaneous power reading: 0 Watts\nPower reading state is: activated\n"
	w, ok := parseIPMIDCMIWatts([]byte(activeZero))
	if !ok || w != 0 {
		t.Fatalf("parser dropped an active zero: w=%v ok=%v", w, ok)
	}
	env := newFakePowerEnv(t)
	env.statDev = func(string) bool { return true }
	env.lookPath = func(string) (string, error) { return "/usr/bin/ipmitool", nil }
	env.ipmiRead = func(string) (float64, bool) { return parseIPMIDCMIWatts([]byte(activeZero)) }
	set := detectPowerSources(env)
	if set.system == nil {
		t.Fatal("an active zero must not make the backend disappear")
	}
	w, ok = set.system.sample(time.Now())
	if !ok || w != 0 {
		t.Fatalf("the sampler discarded an active zero: w=%v ok=%v", w, ok)
	}
	// CONTROL: the sampler still rejects what is genuinely out of range.
	env.ipmiRead = func(string) (float64, bool) { return -1, true }
	if s := detectPowerSources(env).system; s != nil {
		if w, ok := s.sample(time.Now()); ok {
			t.Fatalf("a negative reading was accepted: %v", w)
		}
	}
}

// --- IPMI ---

func TestIPMIOnlyProbedBehindARealBMCInterface(t *testing.T) {
	env := newFakePowerEnv(t)
	looked := false
	env.lookPath = func(string) (string, error) {
		looked = true
		return "/usr/bin/ipmitool", nil
	}
	if s := discoverIPMI(env); s != nil {
		t.Fatalf("no BMC device must mean no backend: %+v", s)
	}
	if looked {
		t.Fatal("ipmitool must not even be looked up without a BMC interface")
	}

	env.statDev = func(p string) bool { return p == "/dev/ipmi0" }
	if s := discoverIPMI(env); s != nil {
		t.Fatal("a BMC that answers nothing must not become a backend")
	}

	calls := 0
	env.ipmiRead = func(string) (float64, bool) {
		calls++
		return 212, true
	}
	s := discoverIPMI(env)
	if s == nil || s.source() != powerhistory.SourceIPMIDCMI {
		t.Fatalf("ipmi backend: %+v", s)
	}
	// BMC round trips are slow: the backend paces itself and simply
	// contributes fewer samples per window.
	base := time.Now()
	before := calls
	if w, ok := s.sample(base); !ok || w != 212 {
		t.Fatalf("first read: ok=%v w=%v", ok, w)
	}
	for i := 1; i < 5; i++ {
		if _, ok := s.sample(base.Add(time.Duration(i) * time.Second)); ok {
			t.Fatalf("read at +%ds must be rate-limited", i)
		}
	}
	if calls-before != 1 {
		t.Fatalf("want exactly one BMC round trip in 5 s, got %d", calls-before)
	}
	if _, ok := s.sample(base.Add(powerIPMIInterval)); !ok {
		t.Fatal("reading must resume after the interval")
	}
	// A BMC that stops answering degrades to unavailable.
	env.ipmiRead = func(string) (float64, bool) { return 0, false }
	s.read = env.ipmiRead
	if w, ok := s.sample(base.Add(2 * powerIPMIInterval)); ok {
		t.Fatalf("unanswered BMC must be unavailable, got %v W", w)
	}
}

// ipmitool prints the instantaneous value and EXITS ZERO even when the BMC
// says the reading is deactivated (lib/ipmi_dcmi.c, ipmi_dcmi_pwr_rd). A
// number plus a successful exit is therefore not evidence that anything is
// being measured, and the agent was turning those into fresh measured
// whole-system watts.
//
// This drives the PRODUCTION parser. The previous version ran ipmiDCMIRe
// directly, so it could not have failed for any change made to
// readIPMIDCMIWatts or to the gate around it — the regex still matches the
// deactivated output, which is exactly the point.
func TestIPMIDCMIParsing(t *testing.T) {
	const deactivated = `    Instantaneous power reading:                   212 Watts
    IPMI timestamp:                           Thu Sep 11 09:00:00 2026
    Sampling period:                          00000001 Seconds.
    Power reading state is:                   deactivated
`
	const activated = `    Instantaneous power reading:                   212 Watts
    IPMI timestamp:                           Thu Sep 11 09:00:00 2026
    Sampling period:                          00000001 Seconds.
    Power reading state is:                   activated
`
	for _, tc := range []struct {
		name string
		out  string
		want float64
		ok   bool
	}{
		{"activated", activated, 212, true},
		{"deactivated", deactivated, 0, false},
		// The state line is the one line that says the number means anything.
		{"no state line", "    Instantaneous power reading:  212 Watts\n", 0, false},
		{"unknown state", "Instantaneous power reading: 212 Watts\nPower reading state is: unknown\n", 0, false},
		{"activated, fractional", "Instantaneous power reading: 98.5 Watts\nPower reading state is: activated\n", 98.5, true},
		{"state but no value", "Power reading state is: activated\n", 0, false},
		{"unsupported", "DCMI request failed because of reserved bits", 0, false},
		{"empty", "", 0, false},

		// A numeric PREFIX is not a reading. Loosely anchored, each of these
		// yielded a plausible wattage from a line nothing could interpret.
		{"trailing garbage", "Instantaneous power reading: 212garbage Watts\nPower reading state is: activated\n", 0, false},
		{"exponent", "Instantaneous power reading: 1e6 Watts\nPower reading state is: activated\n", 0, false},
		{"no units", "Instantaneous power reading: 212\nPower reading state is: activated\n", 0, false},
		{"wrong units", "Instantaneous power reading: 212 Amps\nPower reading state is: activated\n", 0, false},
		{"negative", "Instantaneous power reading: -5 Watts\nPower reading state is: activated\n", 0, false},
		{"absurd magnitude", "Instantaneous power reading: 999999 Watts\nPower reading state is: activated\n", 0, false},

		// Two answers in one response name no single reading, and taking the
		// first would be a guess about which one the state line describes.
		{"conflicting values", "Instantaneous power reading: 212 Watts\n" +
			"Instantaneous power reading: 40 Watts\nPower reading state is: activated\n", 0, false},
		{"conflicting states", "Instantaneous power reading: 212 Watts\n" +
			"Power reading state is: activated\nPower reading state is: deactivated\n", 0, false},
		// CRLF, because a BMC-fed pipe is not guaranteed to be Unix-clean.
		{"crlf", "Instantaneous power reading: 212 Watts\r\nPower reading state is: activated\r\n", 212, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseIPMIDCMIWatts([]byte(tc.out))
			if ok != tc.ok {
				t.Fatalf("ok=%v want %v for %q", ok, tc.ok, tc.out)
			}
			if ok && got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

// And the command boundary itself, because the gate has to survive the exec:
// ipmitool's exit status is zero in the deactivated case, so nothing before
// the parser can catch it.
func TestIPMIDCMICommandBoundary(t *testing.T) {
	write := func(t *testing.T, body string) string {
		t.Helper()
		dir := t.TempDir()
		tool := filepath.Join(dir, "ipmitool")
		if err := os.WriteFile(tool, []byte(body), 0o755); err != nil {
			t.Fatalf("write fake ipmitool: %v", err)
		}
		return tool
	}

	// CONTROL: the harness really does produce a reading when the BMC is
	// taking one. Without this, the negative below could pass because the
	// fake tool never ran at all.
	live := write(t, `#!/bin/sh
echo "    Instantaneous power reading:                   212 Watts"
echo "    Power reading state is:                   activated"
exit 0
`)
	if w, ok := readIPMIDCMIWatts(live); !ok || w != 212 {
		t.Fatalf("control: an activated BMC must report 212W, got %v ok=%v", w, ok)
	}

	// Exit zero, a real number, and a BMC that is not measuring.
	dead := write(t, `#!/bin/sh
echo "    Instantaneous power reading:                   212 Watts"
echo "    IPMI timestamp:                           Thu Sep 11 09:00:00 2026"
echo "    Power reading state is:                   deactivated"
exit 0
`)
	if w, ok := readIPMIDCMIWatts(dead); ok {
		t.Fatalf("a deactivated reading became measured system power: %vW", w)
	}
}

// --- preference order ---

func TestSystemPreferenceOrderPsysBatteryIPMIHwmon(t *testing.T) {
	build := func(t *testing.T, psys, battery, ipmi, hwmon bool) powerSourceSet {
		t.Helper()
		env := newFakePowerEnv(t)
		writeRAPLZone(t, env.raplRoot, "intel-rapl:0", "package-0", 0)
		if psys {
			// A live platform zone advances across the detection probe.
			dir := writeRAPLZone(t, env.raplRoot, "intel-rapl:1", "psys", 0)
			tick := env.sleep
			env.sleep = func(d time.Duration) {
				setEnergy(t, dir, 10_000_000)
				tick(d)
			}
		}
		if battery {
			writeSysfs(t, filepath.Join(env.supplyRoot, "BAT0"), map[string]string{
				"type": "Battery", "status": "Discharging", "power_now": "18000000",
			})
		}
		if ipmi {
			env.statDev = func(p string) bool { return p == "/dev/ipmi0" }
			env.lookPath = func(string) (string, error) { return "/usr/bin/ipmitool", nil }
			env.ipmiRead = func(string) (float64, bool) { return 205, true }
		}
		if hwmon {
			writeSysfs(t, filepath.Join(env.hwmonRoot, "hwmon0"), map[string]string{
				"name": "ina226", "power1_input": "31000000",
			})
		}
		return detectPowerSources(env)
	}
	// Two backends measure whole-platform power with a defensible scope, and
	// the presence of a battery or a shunt changes nothing: they are not
	// candidates, however plausible their numbers look.
	for _, tc := range []struct {
		name                       string
		psys, battery, ipmi, hwmon bool
		want                       string
	}{
		{"all present", true, true, true, true, powerhistory.SourceRAPLPsys},
		{"no psys", false, true, true, true, powerhistory.SourceIPMIDCMI},
		{"no psys or ipmi", false, true, false, true, ""},
		{"battery only", false, true, false, false, ""},
		{"hwmon only", false, false, false, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set := build(t, tc.psys, tc.battery, tc.ipmi, tc.hwmon)
			if tc.want == "" {
				if set.system != nil {
					t.Fatalf("a host with no scope-bearing backend reported %s", set.system.source())
				}
				if set.cpu == nil || set.cpu.source() != powerhistory.SourceRAPLPackage {
					t.Fatalf("cpu backend must stay the package sum: %+v", set.cpu)
				}
				return
			}
			if set.system == nil || set.system.source() != tc.want {
				t.Fatalf("system backend %+v, want %s", set.system, tc.want)
			}
			// Whatever wins `system`, the package zones keep answering for
			// `cpu` and are never folded into the system number.
			if set.cpu == nil || set.cpu.source() != powerhistory.SourceRAPLPackage {
				t.Fatalf("cpu backend must stay the package sum: %+v", set.cpu)
			}
		})
	}
}

func TestStuckPsysFallsBackToTheBMC(t *testing.T) {
	env := newFakePowerEnv(t)
	writeRAPLZone(t, env.raplRoot, "intel-rapl:0", "package-0", 0)
	writeRAPLZone(t, env.raplRoot, "intel-rapl:1", "psys", 12345) // never advances
	env.statDev = func(p string) bool { return p == "/dev/ipmi0" }
	env.lookPath = func(string) (string, error) { return "/usr/bin/ipmitool", nil }
	env.ipmiRead = func(string) (float64, bool) { return 205, true }
	set := detectPowerSources(env)
	if set.system == nil || set.system.source() != powerhistory.SourceIPMIDCMI {
		t.Fatalf("a psys zone that never advances must not shadow the BMC: %+v", set.system)
	}
	if w, ok := set.system.sample(time.Now()); !ok || w != 205 {
		t.Fatalf("ok=%v w=%v", ok, w)
	}
}

// A guest's RAPL counters describe the host's silicon, shared with tenants
// the guest cannot see, so none of the three domains is about this machine.
// A virtualization HOST keeps all of them.
func TestAVMGuestWithholdsEveryRAPLDomain(t *testing.T) {
	build := func(t *testing.T, guest bool) powerSourceSet {
		env := newFakePowerEnv(t)
		env.inGuest = func() bool { return guest }
		writeRAPLZone(t, env.raplRoot, "intel-rapl:0", "package-0", 0)
		writeRAPLZone(t, env.raplRoot, "intel-rapl:0:0", "dram", 0)
		dir := writeRAPLZone(t, env.raplRoot, "intel-rapl:1", "psys", 0)
		tick := env.sleep
		env.sleep = func(d time.Duration) {
			setEnergy(t, dir, 10_000_000)
			tick(d)
		}
		return detectPowerSources(env)
	}

	// CONTROL: a host — including a KVM host, which gopsutil reports as role
	// "host" — keeps every counter.
	host := build(t, false)
	if host.system == nil || host.cpu == nil || host.dram == nil {
		t.Fatalf("control: a virtualization host must keep all RAPL domains: %+v", host)
	}

	guest := build(t, true)
	if guest.system != nil || guest.cpu != nil || guest.dram != nil {
		t.Fatalf("a guest reported RAPL: system=%v cpu=%v dram=%v",
			guest.system, guest.cpu, guest.dram)
	}
}

// A board with regulator voltages but no current or power channel (the
// RK3588 case) yields nothing at all. Watts are not derivable, so none are
// invented.
func TestNoCountersMeansNoBackends(t *testing.T) {
	env := newFakePowerEnv(t)
	writeSysfs(t, filepath.Join(env.hwmonRoot, "hwmon0"), map[string]string{
		"name": "rk3588-thermal", "temp1_input": "45000",
	})
	writeSysfs(t, filepath.Join(env.supplyRoot, "rk818-usb"), map[string]string{"type": "USB", "online": "1"})
	set := detectPowerSources(env)
	if !set.empty() {
		t.Fatalf("nothing measurable must mean nothing reported: %+v", set)
	}
	if set.describe() != "none" {
		t.Fatalf("describe %q", set.describe())
	}
}

// --- DRM GPU hwmon ---

func writeDRMCard(t *testing.T, drmRoot, card, chip, channel, value, pciSlot string) {
	t.Helper()
	device := filepath.Join(drmRoot, card, "device")
	writeSysfs(t, filepath.Join(device, "hwmon", "hwmon0"), map[string]string{
		"name": chip, channel: value,
	})
	if pciSlot != "" {
		if err := os.MkdirAll(filepath.Join(drmRoot, "devices"), 0o755); err != nil {
			t.Fatal(err)
		}
		// Mirror the kernel's layout: cardN/device is a symlink to the bus
		// device, whose basename is the stable PCI address.
		real := filepath.Join(drmRoot, "devices", pciSlot)
		if err := os.Rename(device, real); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, device); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDRMGPUPollerReadsEveryCardAndSkipsNVIDIA(t *testing.T) {
	env := newFakePowerEnv(t)
	writeDRMCard(t, env.drmRoot, "card0", "amdgpu", "power1_average", "45000000", "0000:03:00.0")
	writeDRMCard(t, env.drmRoot, "card1", "amdgpu", "power1_input", "12000000", "")
	// nvidia-smi already streams this one; a second identifier for the same
	// device would inflate the sensor list and corrupt the GPU total.
	writeDRMCard(t, env.drmRoot, "card2", "nvidia", "power1_average", "300000000", "")
	// Connectors and render nodes are not cards.
	writeSysfs(t, filepath.Join(env.drmRoot, "card0-DP-1"), map[string]string{"status": "connected"})
	writeSysfs(t, filepath.Join(env.drmRoot, "renderD128"), map[string]string{"dev": "226:128"})

	p := discoverDRMGPUs(env)
	if p == nil {
		t.Fatal("two AMD cards must be discovered")
	}
	tick := p.poll(time.Now())
	if !tick.complete || len(tick.readings) != 2 {
		t.Fatalf("tick: complete=%v readings=%+v", tick.complete, tick.readings)
	}
	if tick.readings[0].id != "amdgpu-0000-03-00.0" {
		t.Fatalf("id must come from the stable PCI address: %q", tick.readings[0].id)
	}
	if tick.readings[1].id != "amdgpu-card1" {
		t.Fatalf("id without a bus symlink: %q", tick.readings[1].id)
	}
	if *tick.readings[0].watts != 45 || *tick.readings[1].watts != 12 {
		t.Fatalf("watts: %v %v", *tick.readings[0].watts, *tick.readings[1].watts)
	}

	// One card's channel goes away: the reading is unavailable, the device
	// set is unchanged, so the tick stays complete and the total stays
	// truthful for the devices that did report.
	poller := p.(*drmGPUPoller)
	if err := os.Remove(poller.devices[1].path); err != nil {
		t.Fatal(err)
	}
	tick = p.poll(time.Now())
	if !tick.complete || len(tick.readings) != 2 {
		t.Fatalf("device set must not change: %+v", tick)
	}
	if tick.readings[1].watts != nil {
		t.Fatalf("missing channel must be N/A, got %v", *tick.readings[1].watts)
	}
}

func TestDRMGPUAbsentIsTrueNil(t *testing.T) {
	env := newFakePowerEnv(t)
	if p := discoverDRMGPUs(env); p != nil {
		t.Fatalf("typed nil leaked into the interface: %+v", p)
	}
}
