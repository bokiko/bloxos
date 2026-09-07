package main

import (
	"testing"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
)

// accClock drives the accumulator with an explicit monotonic time (base has
// a monotonic reading; Add keeps it) and an independently controlled wall
// clock so clock steps can be simulated.
type accClock struct {
	base   time.Time
	wallMS int64 // wall clock at base
	skewMS int64 // extra wall offset (simulated step)
}

func newAccClock() *accClock {
	b := time.Now()
	return &accClock{base: b, wallMS: b.UnixMilli()}
}

func (c *accClock) at(sec float64) time.Time {
	return c.base.Add(time.Duration(sec * float64(time.Second)))
}
func (c *accClock) wall(sec float64) int64 { return c.wallMS + int64(sec*1000) + c.skewMS }

func f(v float64) *float64 { return &v }

func gpuTick(at time.Time, complete bool, watts ...*float64) powerGPUTick {
	t := powerGPUTick{at: at, complete: complete}
	for i, w := range watts {
		t.readings = append(t.readings, powerReading{id: "GPU-" + string(rune('A'+i)), watts: w})
	}
	return t
}

func feedGPU(a *powerAccumulator, c *accClock, from, to int, watts func(sec int) []*float64) {
	for s := from; s < to; s++ {
		a.addGPUAt(gpuTick(c.at(float64(s)), true, watts(s)...), c.wall(float64(s)))
	}
}

func TestAccumulatorMeanPeakCoverageAndGapFlag(t *testing.T) {
	a := newPowerAccumulator(30*time.Second, time.Second)
	c := newAccClock()
	feedGPU(a, c, 0, 30, func(int) []*float64 { return []*float64{f(100), f(200)} })
	if got := a.tickAt(c.at(29.5), c.wall(29.5)); len(got) != 0 {
		t.Fatalf("window closed early: %+v", got)
	}
	out := a.tickAt(c.at(30), c.wall(30))
	if len(out) != 1 {
		t.Fatalf("want 1 bucket, got %d", len(out))
	}
	b := out[0]
	if !b.GapBefore {
		t.Error("first bucket after boot must carry GapBefore")
	}
	if b.EndUnixMS-b.StartUnixMS != 30000 || b.StartUnixMS != c.wall(0) {
		t.Errorf("bad window bounds: start=%d end=%d", b.StartUnixMS, b.EndUnixMS)
	}
	if b.ExpectedSamples != 30 || len(b.GPUs) != 2 {
		t.Fatalf("expected=%d gpus=%d", b.ExpectedSamples, len(b.GPUs))
	}
	if *b.GPUs[0].MeanWatts != 100 || *b.GPUs[1].PeakWatts != 200 || b.GPUs[0].Samples != 30 {
		t.Errorf("per-sensor stats wrong: %+v %+v", b.GPUs[0].Stats, b.GPUs[1].Stats)
	}
	if b.GPUTotal == nil || *b.GPUTotal.MeanWatts != 300 || *b.GPUTotal.PeakWatts != 300 || b.GPUTotal.Samples != 30 {
		t.Errorf("total wrong: %+v", b.GPUTotal)
	}
	if b.CPU != nil {
		t.Error("CPU must be nil when no CPU samples were added")
	}
	feedGPU(a, c, 30, 60, func(int) []*float64 { return []*float64{f(1), f(1)} })
	out = a.tickAt(c.at(60), c.wall(60))
	if len(out) != 1 || out[0].GapBefore {
		t.Fatalf("second bucket must be contiguous (no gap): %+v", out)
	}
	if out[0].StartUnixMS != b.EndUnixMS {
		t.Errorf("windows must tile: prev end %d, next start %d", b.EndUnixMS, out[0].StartUnixMS)
	}
}

func TestAccumulatorTotalPeakIsSimultaneousNotSumOfMaxima(t *testing.T) {
	a := newPowerAccumulator(30*time.Second, time.Second)
	c := newAccClock()
	feedGPU(a, c, 0, 30, func(s int) []*float64 {
		if s < 15 {
			return []*float64{f(100), f(0)}
		}
		return []*float64{f(0), f(100)}
	})
	b := a.tickAt(c.at(30), c.wall(30))[0]
	if *b.GPUs[0].PeakWatts != 100 || *b.GPUs[1].PeakWatts != 100 {
		t.Fatalf("per-device peaks: %+v %+v", b.GPUs[0].Stats, b.GPUs[1].Stats)
	}
	if *b.GPUTotal.PeakWatts != 100 {
		t.Errorf("total peak must be 100 (simultaneous), got %v", *b.GPUTotal.PeakWatts)
	}
}

func TestAccumulatorNAIncompleteAndOversizedTicksNeverProducePartialTotal(t *testing.T) {
	a := newPowerAccumulator(30*time.Second, time.Second)
	c := newAccClock()
	// N/A on one device: that device contributes nothing, total skips the tick.
	a.addGPUAt(gpuTick(c.at(0), true, f(50), nil), c.wall(0))
	// Incomplete tick (device missing from the iteration): total skips.
	a.addGPUAt(gpuTick(c.at(1), false, f(50)), c.wall(1))
	b := a.tickAt(c.at(30), c.wall(30))[0]
	if b.GPUTotal != nil {
		t.Errorf("total must be nil without any complete tick, got %+v", b.GPUTotal)
	}
	if len(b.GPUs) != 2 || b.GPUs[0].Samples != 2 || b.GPUs[1].Samples != 0 || b.GPUs[1].MeanWatts != nil {
		t.Errorf("sensor stats: %+v", b.GPUs)
	}

	// 17 sensors: keep 16, never a total.
	a2 := newPowerAccumulator(30*time.Second, time.Second)
	many := make([]*float64, 17)
	for i := range many {
		many[i] = f(10)
	}
	a2.addGPUAt(gpuTick(c.at(0), true, many...), c.wall(0))
	b2 := a2.tickAt(c.at(30), c.wall(30))[0]
	if len(b2.GPUs) != powerhistory.MaxSensors || b2.GPUTotal != nil {
		t.Errorf("17 sensors: gpus=%d total=%v", len(b2.GPUs), b2.GPUTotal)
	}
}

func TestAccumulatorCapsSamplesAtExpected(t *testing.T) {
	a := newPowerAccumulator(30*time.Second, time.Second)
	c := newAccClock()
	// Producer jitter: 36 GPU ticks and 36 CPU samples inside one window.
	for i := 0; i < 36; i++ {
		s := float64(i) * 0.8
		a.addGPUAt(gpuTick(c.at(s), true, f(10)), c.wall(s))
		a.addCPUAt(c.at(s), c.wall(s), 20)
	}
	b := a.tickAt(c.at(30), c.wall(30))[0]
	if b.GPUs[0].Samples != 30 || b.GPUTotal.Samples != 30 || b.CPU.Samples != 30 {
		t.Errorf("samples must be capped at 30: gpu=%d total=%d cpu=%d", b.GPUs[0].Samples, b.GPUTotal.Samples, b.CPU.Samples)
	}
	if *b.CPU.MeanWatts != 20 {
		t.Errorf("cpu mean %v", *b.CPU.MeanWatts)
	}
}

func TestAccumulatorEmptyWindowEmitsNothingAndFlagsGap(t *testing.T) {
	a := newPowerAccumulator(30*time.Second, time.Second)
	c := newAccClock()
	feedGPU(a, c, 0, 30, func(int) []*float64 { return []*float64{f(1)} })
	a.tickAt(c.at(30), c.wall(30))
	// 30..60: nothing sampled.
	if out := a.tickAt(c.at(60), c.wall(60)); len(out) != 0 {
		t.Fatalf("empty window must not emit: %+v", out)
	}
	feedGPU(a, c, 60, 90, func(int) []*float64 { return []*float64{f(1)} })
	out := a.tickAt(c.at(90), c.wall(90))
	if len(out) != 1 || !out[0].GapBefore {
		t.Fatalf("bucket after empty window must carry GapBefore: %+v", out)
	}
}

func TestAccumulatorSuspendResumeDoesNotEmitEmptyRun(t *testing.T) {
	a := newPowerAccumulator(30*time.Second, time.Second)
	c := newAccClock()
	feedGPU(a, c, 0, 30, func(int) []*float64 { return []*float64{f(1)} })
	a.tickAt(c.at(30), c.wall(30))
	feedGPU(a, c, 30, 40, func(int) []*float64 { return []*float64{f(1)} })
	// Machine sleeps for 10 minutes.
	out := a.tickAt(c.at(640), c.wall(640))
	if len(out) != 1 {
		t.Fatalf("want exactly the one partial bucket, got %d", len(out))
	}
	if out[0].GPUs[0].Samples != 10 || out[0].EndUnixMS-out[0].StartUnixMS != 30000 {
		t.Errorf("partial bucket: %+v", out[0])
	}
	feedGPU(a, c, 640, 670, func(int) []*float64 { return []*float64{f(1)} })
	out = a.tickAt(c.at(670), c.wall(670))
	if len(out) != 1 || !out[0].GapBefore || out[0].StartUnixMS != c.wall(640) {
		t.Fatalf("post-resume bucket must be re-anchored and gap-flagged: %+v", out)
	}
}

func TestAccumulatorClockStepBackwardAndForward(t *testing.T) {
	for _, step := range []int64{-3600_000, +3600_000} {
		a := newPowerAccumulator(30*time.Second, time.Second)
		c := newAccClock()
		feedGPU(a, c, 0, 10, func(int) []*float64 { return []*float64{f(5)} })
		c.skewMS = step // wall clock jumps; monotonic keeps going
		feedGPU(a, c, 10, 40, func(int) []*float64 { return []*float64{f(7)} })
		out := a.tickAt(c.at(40), c.wall(40))
		if len(out) != 2 {
			t.Fatalf("step %d: want partial + full bucket, got %d: %+v", step, len(out), out)
		}
		p, n := out[0], out[1]
		if p.EndUnixMS <= p.StartUnixMS || p.EndUnixMS-p.StartUnixMS != 10000 || p.GPUs[0].Samples != 10 {
			t.Errorf("step %d: partial bucket must be start+monotonic elapsed: %+v", step, p)
		}
		if !n.GapBefore {
			t.Errorf("step %d: bucket after clock step must carry GapBefore", step)
		}
		if n.StartUnixMS != c.wall(10) || n.EndUnixMS-n.StartUnixMS != 30000 || n.GPUs[0].Samples != 30 {
			t.Errorf("step %d: re-anchored bucket wrong: start=%d (want %d) dur=%d samples=%d",
				step, n.StartUnixMS, c.wall(10), n.EndUnixMS-n.StartUnixMS, n.GPUs[0].Samples)
		}
	}
}
