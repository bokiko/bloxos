package main

import (
	"testing"

	"github.com/shirou/gopsutil/v4/cpu"
)

func infos(names ...string) []cpu.InfoStat {
	out := make([]cpu.InfoStat, 0, len(names))
	for i, n := range names {
		out = append(out, cpu.InfoStat{CPU: int32(i), ModelName: n})
	}
	return out
}

// The regression this file exists for. Core 0 of an RK3588 is a Cortex-A55, so
// reading cpuInfos[0] and pairing it with an 8-core count reported
// "8x Cortex-A55" for a 4x A76 + 4x A55 part.
func TestBigLittlePackageIsNotDescribedByCoreZero(t *testing.T) {
	rk3588 := infos(
		"Cortex-A55", "Cortex-A55", "Cortex-A55", "Cortex-A55",
		"Cortex-A76", "Cortex-A76", "Cortex-A76", "Cortex-A76",
	)
	got := describeCPUCores(rk3588)
	const want = "4x Cortex-A55 + 4x Cortex-A76"
	if got != want {
		t.Fatalf("describeCPUCores = %q, want %q", got, want)
	}
	if got == "Cortex-A55" {
		t.Fatal("regressed: the package is described by core 0 alone")
	}
}

// A uniform package must be byte-identical to the old behaviour. Appending a
// count would churn every existing inventory row without adding a fact.
func TestUniformPackageIsUnchanged(t *testing.T) {
	for _, name := range []string{
		"AMD Ryzen 9 7950X 16-Core Processor",
		"Common KVM processor",
		"Intel(R) Core(TM) i7-10700K CPU @ 3.80GHz",
	} {
		u := infos(name, name, name, name)
		if got := describeCPUCores(u); got != name {
			t.Fatalf("uniform %q rendered as %q, want it unchanged", name, got)
		}
	}
}

// Ordering follows core index, not group size, so the same SoC always renders
// identically and the string tracks the vendor's own core numbering.
func TestGroupsAreOrderedByFirstCoreIndexNotBySize(t *testing.T) {
	// Two big cores first, then six little ones: size ordering would invert it.
	got := describeCPUCores(infos(
		"Cortex-X1", "Cortex-X1",
		"Cortex-A55", "Cortex-A55", "Cortex-A55",
		"Cortex-A55", "Cortex-A55", "Cortex-A55",
	))
	const want = "2x Cortex-X1 + 6x Cortex-A55"
	if got != want {
		t.Fatalf("describeCPUCores = %q, want %q", got, want)
	}
}

func TestThreeDistinctCoreTypes(t *testing.T) {
	got := describeCPUCores(infos(
		"Cortex-X4",
		"Cortex-A720", "Cortex-A720", "Cortex-A720",
		"Cortex-A520", "Cortex-A520", "Cortex-A520", "Cortex-A520",
	))
	const want = "1x Cortex-X4 + 3x Cortex-A720 + 4x Cortex-A520"
	if got != want {
		t.Fatalf("describeCPUCores = %q, want %q", got, want)
	}
}

// Blank model strings are common in VMs and on some ARM kernels. They must not
// produce a phantom group or an empty "0x " fragment.
func TestBlankModelNamesAreIgnoredNotCounted(t *testing.T) {
	got := describeCPUCores(infos("Cortex-A76", "", "  ", "Cortex-A76"))
	if got != "Cortex-A76" {
		t.Fatalf("describeCPUCores = %q, want %q", got, "Cortex-A76")
	}
}

func TestAllBlankFallsBackToCoreZeroRatherThanInventing(t *testing.T) {
	if got := describeCPUCores(infos("", "")); got != "" {
		t.Fatalf("describeCPUCores = %q, want empty", got)
	}
}

func TestNoCoresReportedIsEmptyNotPanic(t *testing.T) {
	if got := describeCPUCores(nil); got != "" {
		t.Fatalf("describeCPUCores(nil) = %q, want empty", got)
	}
}

// gopsutil reports one entry per socket on some x86 kernels rather than one per
// core. Two identical sockets must stay one name, not become "1x X + 1x X".
func TestIdenticalSocketsDoNotBecomeAMix(t *testing.T) {
	const name = "Intel(R) Xeon(R) Gold 6132 CPU @ 2.60GHz"
	if got := describeCPUCores(infos(name, name)); got != name {
		t.Fatalf("describeCPUCores = %q, want %q", got, name)
	}
}
