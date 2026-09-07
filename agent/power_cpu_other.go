//go:build !linux

package main

// newPowerCPUSampler reports the CPU energy backend as unavailable on this
// platform. Windows has no vendor-neutral package energy counter the agent
// can read without extra drivers, so CPU stays nil in the buckets rather
// than being estimated.
func newPowerCPUSampler() powerCPUSampler { return nil }
