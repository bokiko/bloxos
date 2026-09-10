package main

import (
	"testing"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
)

func feedDomain(a *powerAccumulator, c *accClock, from, to int, domain, source string, watts float64) {
	for s := from; s < to; s++ {
		a.addDomainAt(domain, c.at(float64(s)), c.wall(float64(s)), watts, source)
	}
}

func sourceOf(b powerhistory.Bucket, domain string) string { return b.SourceFor(domain) }

// The scalar domains are reported side by side, each labelled with the
// backend that measured it. system is NOT cpu+dram+gpu, and cpu is not
// subtracted out of system: they are different measurements of different
// scopes that happen to overlap.
func TestDomainsAreReportedSeparatelyAndLabelled(t *testing.T) {
	a := newPowerAccumulator(30*time.Second, time.Second)
	c := newAccClock()
	feedDomain(a, c, 0, 30, powerhistory.DomainSystem, powerhistory.SourceRAPLPsys, 62)
	feedDomain(a, c, 0, 30, powerhistory.DomainCPU, powerhistory.SourceRAPLPackage, 41)
	feedDomain(a, c, 0, 30, powerhistory.DomainDRAM, powerhistory.SourceRAPLDRAM, 7)
	feedGPU(a, c, 0, 30, func(int) []*float64 { return []*float64{f(120)} })
	b := closeWindow(t, a, c, 30)

	for _, tc := range []struct {
		domain string
		stats  *powerhistory.Stats
		watts  float64
		source string
	}{
		{powerhistory.DomainSystem, b.System, 62, powerhistory.SourceRAPLPsys},
		{powerhistory.DomainCPU, b.CPU, 41, powerhistory.SourceRAPLPackage},
		{powerhistory.DomainDRAM, b.DRAM, 7, powerhistory.SourceRAPLDRAM},
	} {
		if tc.stats == nil || tc.stats.Samples != 30 || *tc.stats.MeanWatts != tc.watts {
			t.Fatalf("%s: %+v", tc.domain, tc.stats)
		}
		if got := sourceOf(b, tc.domain); got != tc.source {
			t.Fatalf("%s source %q, want %q", tc.domain, got, tc.source)
		}
	}
	// system must be exactly what psys measured — not the components added up.
	if *b.System.MeanWatts != 62 {
		t.Fatalf("system watts %v: domains must never be summed", *b.System.MeanWatts)
	}
	if b.GPUTotal == nil || *b.GPUTotal.MeanWatts != 120 {
		t.Fatalf("gpu total: %+v", b.GPUTotal)
	}
	if len(b.Sources) != 3 {
		t.Fatalf("one label per measured domain: %+v", b.Sources)
	}
}

// A laptop unplugged mid-window switches its system backend. Averaging psys
// watts with battery watts would invent a number neither backend measured,
// so the domain is omitted — the same rule a changing GPU set gets.
func TestDomainBackendSwitchMidWindowOmitsOnlyThatDomain(t *testing.T) {
	a := newPowerAccumulator(30*time.Second, time.Second)
	c := newAccClock()
	feedDomain(a, c, 0, 15, powerhistory.DomainSystem, powerhistory.SourceRAPLPsys, 20)
	feedDomain(a, c, 15, 30, powerhistory.DomainSystem, powerhistory.SourceBattery, 35)
	feedDomain(a, c, 0, 30, powerhistory.DomainCPU, powerhistory.SourceRAPLPackage, 12)
	b := closeWindow(t, a, c, 30)
	if b.System != nil {
		t.Fatalf("mixed backends must omit the domain, got %+v", *b.System)
	}
	if sourceOf(b, powerhistory.DomainSystem) != "" {
		t.Fatalf("an omitted domain must carry no label: %+v", b.Sources)
	}
	if b.CPU == nil || b.CPU.Samples != 30 || *b.CPU.MeanWatts != 12 {
		t.Fatalf("the other domains must survive: %+v", b.CPU)
	}

	// The next stable window reports normally again.
	feedDomain(a, c, 30, 60, powerhistory.DomainSystem, powerhistory.SourceBattery, 35)
	b2 := closeWindow(t, a, c, 60)
	if b2.System == nil || *b2.System.MeanWatts != 35 || sourceOf(b2, powerhistory.DomainSystem) != powerhistory.SourceBattery {
		t.Fatalf("recovery window: %+v %+v", b2.System, b2.Sources)
	}
}

// A backend switch beyond the sample cap must still be noticed: capping the
// count must not hide that two methods contributed to the window.
func TestDomainBackendSwitchBeyondSampleCapStillCountsAsMixed(t *testing.T) {
	a := newPowerAccumulator(30*time.Second, time.Second)
	c := newAccClock()
	for i := 0; i < 30; i++ {
		a.addDomainAt(powerhistory.DomainSystem, c.at(float64(i)*0.5), c.wall(float64(i)*0.5), 20, powerhistory.SourceRAPLPsys)
	}
	for i := 30; i < 40; i++ {
		a.addDomainAt(powerhistory.DomainSystem, c.at(float64(i)*0.5), c.wall(float64(i)*0.5), 35, powerhistory.SourceBattery)
	}
	feedGPU(a, c, 0, 30, func(int) []*float64 { return []*float64{f(50)} })
	b := closeWindow(t, a, c, 30)
	if b.System != nil {
		t.Fatalf("mix past the cap must still omit the domain, got %+v", *b.System)
	}
}

// A window whose every reading was discarded is not data. Emitting an empty
// bucket would claim coverage that does not exist.
func TestWindowWithNothingUsableEmitsNothingAndFlagsGap(t *testing.T) {
	a := newPowerAccumulator(30*time.Second, time.Second)
	c := newAccClock()
	feedGPU(a, c, 0, 30, func(int) []*float64 { return []*float64{f(10)} })
	a.tickAt(c.at(30), c.wall(30))

	feedDomain(a, c, 30, 45, powerhistory.DomainSystem, powerhistory.SourceRAPLPsys, 20)
	feedDomain(a, c, 45, 60, powerhistory.DomainSystem, powerhistory.SourceBattery, 35)
	if out := a.tickAt(c.at(60), c.wall(60)); len(out) != 0 {
		t.Fatalf("nothing usable must emit no bucket: %+v", out)
	}
	feedDomain(a, c, 60, 90, powerhistory.DomainCPU, powerhistory.SourceRAPLPackage, 12)
	b := closeWindow(t, a, c, 90)
	if !b.GapBefore {
		t.Fatal("the discarded window must be declared as a gap")
	}
}

// An unrecognised domain name is dropped, never folded into a domain it is
// not.
func TestUnknownDomainIsDropped(t *testing.T) {
	a := newPowerAccumulator(30*time.Second, time.Second)
	c := newAccClock()
	feedDomain(a, c, 0, 30, "wall-socket", "guesswork", 400)
	feedDomain(a, c, 0, 30, powerhistory.DomainCPU, powerhistory.SourceRAPLPackage, 12)
	b := closeWindow(t, a, c, 30)
	if b.System != nil || b.DRAM != nil {
		t.Fatalf("unknown domain leaked: system=%+v dram=%+v", b.System, b.DRAM)
	}
	if b.CPU == nil || *b.CPU.MeanWatts != 12 {
		t.Fatalf("cpu: %+v", b.CPU)
	}
	if len(b.Sources) != 1 || b.Sources[0].Domain != powerhistory.DomainCPU {
		t.Fatalf("sources: %+v", b.Sources)
	}
}

// Replaying the same window must re-encode identically, which is what keeps
// a retransmission from looking like a data conflict to the hub.
func TestDomainSourceOrderIsDeterministic(t *testing.T) {
	build := func(order []string) []powerhistory.DomainSource {
		a := newPowerAccumulator(30*time.Second, time.Second)
		c := newAccClock()
		for _, d := range order {
			feedDomain(a, c, 0, 30, d, "src-"+d, 10)
		}
		return closeWindow(t, a, c, 30).Sources
	}
	forward := build([]string{powerhistory.DomainSystem, powerhistory.DomainCPU, powerhistory.DomainDRAM})
	reverse := build([]string{powerhistory.DomainDRAM, powerhistory.DomainCPU, powerhistory.DomainSystem})
	if len(forward) != 3 || len(reverse) != 3 {
		t.Fatalf("forward=%+v reverse=%+v", forward, reverse)
	}
	for i := range forward {
		if forward[i] != reverse[i] {
			t.Fatalf("source order depends on arrival: %+v vs %+v", forward, reverse)
		}
	}
}
