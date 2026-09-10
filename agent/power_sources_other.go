//go:build !linux

package main

// newPowerSources reports every software power backend as unavailable on
// this platform.
//
// Windows has no vendor-neutral energy counter a user-mode agent can read:
// RAPL there needs a signed ring-0 driver, and inside a VM the MSRs are not
// exposed at all whatever the driver. Rather than ship a driver or invent a
// number, the system, cpu and dram domains stay absent from the buckets.
// NVIDIA GPU history, which needs no such access, is unaffected.
func newPowerSources() powerSourceSet { return powerSourceSet{} }
