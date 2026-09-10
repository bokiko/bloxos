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

// --- battery ---

func TestBatteryDischargeIsWholeSystemPower(t *testing.T) {
	env := newFakePowerEnv(t)
	bat := writeSysfs(t, filepath.Join(env.supplyRoot, "BAT0"), map[string]string{
		"type": "Battery", "scope": "System", "status": "Discharging", "power_now": "23500000",
	})
	// An AC adapter is not a battery, and a mouse pack is not this machine.
	writeSysfs(t, filepath.Join(env.supplyRoot, "ACAD"), map[string]string{"type": "Mains", "online": "1"})
	writeSysfs(t, filepath.Join(env.supplyRoot, "hidpp_battery_0"), map[string]string{
		"type": "Battery", "scope": "Device", "status": "Discharging", "power_now": "500000",
	})

	s := discoverBattery(env)
	if s == nil || len(s.dirs) != 1 {
		t.Fatalf("want exactly the system battery, got %+v", s)
	}
	if s.source() != powerhistory.SourceBattery {
		t.Fatalf("source %q", s.source())
	}
	w, ok := s.sample(time.Now())
	if !ok || w < 23.49 || w > 23.51 {
		t.Fatalf("discharge watts: ok=%v w=%v (the peripheral pack must not be added in)", ok, w)
	}

	// On AC the pack measures charge current, not system draw: report nothing.
	for _, status := range []string{"Charging", "Full", "Not charging", "Unknown"} {
		writeSysfs(t, bat, map[string]string{"status": status})
		if w, ok := s.sample(time.Now()); ok {
			t.Fatalf("status %q must not report system power, got %v W", status, w)
		}
	}
}

func TestBatteryFallsBackToCurrentTimesVoltage(t *testing.T) {
	env := newFakePowerEnv(t)
	// 2.5 A at 12.3 V = 30.75 W, reported as magnitudes (this driver signs
	// discharge current negative).
	writeSysfs(t, filepath.Join(env.supplyRoot, "BAT1"), map[string]string{
		"type": "Battery", "status": "Discharging",
		"current_now": "-2500000", "voltage_now": "12300000",
	})
	s := discoverBattery(env)
	if s == nil {
		t.Fatal("a pack with only current/voltage must still be a backend")
	}
	w, ok := s.sample(time.Now())
	if !ok || w < 30.74 || w > 30.76 {
		t.Fatalf("ok=%v w=%v", ok, w)
	}
}

func TestBatteryUnusableOrVanishedIsUnavailableNeverPartial(t *testing.T) {
	env := newFakePowerEnv(t)
	// No power channel at all: not a backend.
	writeSysfs(t, filepath.Join(env.supplyRoot, "BAT0"), map[string]string{
		"type": "Battery", "status": "Discharging", "capacity": "88",
	})
	if s := discoverBattery(env); s != nil {
		t.Fatalf("a pack with no power reading must not become a backend: %+v", s)
	}

	// Two packs; one is pulled mid-run. Reporting the survivor alone would
	// silently halve the machine's measured draw.
	env2 := newFakePowerEnv(t)
	a := writeSysfs(t, filepath.Join(env2.supplyRoot, "BAT0"), map[string]string{
		"type": "Battery", "status": "Discharging", "power_now": "10000000",
	})
	b := writeSysfs(t, filepath.Join(env2.supplyRoot, "BAT1"), map[string]string{
		"type": "Battery", "status": "Discharging", "power_now": "5000000",
	})
	s := discoverBattery(env2)
	if w, ok := s.sample(time.Now()); !ok || w < 14.99 || w > 15.01 {
		t.Fatalf("two discharging packs must sum: ok=%v w=%v", ok, w)
	}
	if err := os.RemoveAll(b); err != nil {
		t.Fatal(err)
	}
	if w, ok := s.sample(time.Now()); ok {
		t.Fatalf("a vanished pack must make the backend unavailable, got %v W", w)
	}

	// Malformed and zero readings are unavailable, never zero watts.
	writeSysfs(t, a, map[string]string{"power_now": "n/a"})
	writeSysfs(t, b, map[string]string{"type": "Battery", "status": "Discharging", "power_now": "5000000"})
	if w, ok := s.sample(time.Now()); ok {
		t.Fatalf("malformed value must be unavailable, got %v W", w)
	}
	writeSysfs(t, a, map[string]string{"power_now": "0"})
	if w, ok := s.sample(time.Now()); ok {
		t.Fatalf("zero from a discharging pack is a broken driver, not 0 W: %v", w)
	}
}

// --- hwmon ---

func TestHwmonAcceptsOnlyCrediblyWholeSystemChips(t *testing.T) {
	env := newFakePowerEnv(t)
	// Listed first so a wrong implementation would pick it: a GPU chip is
	// not the board, however plausible its power channel looks.
	writeSysfs(t, filepath.Join(env.hwmonRoot, "hwmon0"), map[string]string{
		"name": "amdgpu", "power1_average": "45000000",
	})
	writeSysfs(t, filepath.Join(env.hwmonRoot, "hwmon1"), map[string]string{
		"name": "k10temp", "temp1_input": "42000",
	})
	writeSysfs(t, filepath.Join(env.hwmonRoot, "hwmon2"), map[string]string{
		"name": "power_meter", "power1_average": "98000000",
	})
	s := discoverWholeSystemHwmon(env)
	if s == nil || s.source() != powerhistory.SourceHwmonPrefix+"power_meter" {
		t.Fatalf("hwmon backend: %+v", s)
	}
	w, ok := s.sample(time.Now())
	if !ok || w < 97.99 || w > 98.01 {
		t.Fatalf("ok=%v w=%v", ok, w)
	}
	// The channel disappearing is unavailability, not a zero reading.
	if err := os.Remove(s.path); err != nil {
		t.Fatal(err)
	}
	if w, ok := s.sample(time.Now()); ok {
		t.Fatalf("removed channel must be unavailable, got %v W", w)
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

func TestIPMIDCMIParsing(t *testing.T) {
	for _, tc := range []struct {
		out  string
		want float64
		ok   bool
	}{
		{"    Instantaneous power reading:                   212 Watts\n", 212, true},
		{"Instantaneous power reading: 98.5 Watts", 98.5, true},
		{"DCMI request failed because of reserved bits", 0, false},
		{"", 0, false},
	} {
		m := ipmiDCMIRe.FindStringSubmatch(tc.out)
		if (m != nil) != tc.ok {
			t.Fatalf("%q: match=%v want %v", tc.out, m != nil, tc.ok)
		}
		if tc.ok {
			got, err := strconv.ParseFloat(m[1], 64)
			if err != nil || got != tc.want {
				t.Fatalf("%q: got %v err %v", tc.out, got, err)
			}
		}
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
	for _, tc := range []struct {
		name                       string
		psys, battery, ipmi, hwmon bool
		want                       string
	}{
		{"all present", true, true, true, true, powerhistory.SourceRAPLPsys},
		{"no psys", false, true, true, true, powerhistory.SourceBattery},
		{"no psys or battery", false, false, true, true, powerhistory.SourceIPMIDCMI},
		{"hwmon only", false, false, false, true, powerhistory.SourceHwmonPrefix + "ina226"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set := build(t, tc.psys, tc.battery, tc.ipmi, tc.hwmon)
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

func TestStuckPsysFallsBackToBattery(t *testing.T) {
	env := newFakePowerEnv(t)
	writeRAPLZone(t, env.raplRoot, "intel-rapl:0", "package-0", 0)
	writeRAPLZone(t, env.raplRoot, "intel-rapl:1", "psys", 12345) // never advances
	writeSysfs(t, filepath.Join(env.supplyRoot, "BAT0"), map[string]string{
		"type": "Battery", "status": "Discharging", "power_now": "18000000",
	})
	set := detectPowerSources(env)
	if set.system == nil || set.system.source() != powerhistory.SourceBattery {
		t.Fatalf("a psys zone that never advances must not shadow the battery: %+v", set.system)
	}
	if w, ok := set.system.sample(time.Now()); !ok || w < 17.99 || w > 18.01 {
		t.Fatalf("ok=%v w=%v", ok, w)
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
