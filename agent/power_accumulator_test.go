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

// --- GPU membership within a window (PR181 blocker) ---
//
// A combined GPU total is only truthful when every counted tick observed the
// same set of devices. If the set changes inside a window (a device drops,
// appears, or is replaced under the same count), the total is omitted for
// that window while per-GPU and CPU readings are kept; the next window with
// a stable set reports a total again.

type rd struct {
	id string
	w  *float64
}

func mkTick(at time.Time, complete bool, rds ...rd) powerGPUTick {
	t := powerGPUTick{at: at, complete: complete}
	for _, r := range rds {
		t.readings = append(t.readings, powerReading{id: r.id, watts: r.w})
	}
	return t
}

// feedTicks feeds one tick per second from sec `from` (inclusive) to `to`
// (exclusive), each built by mk(sec).
func feedTicks(a *powerAccumulator, c *accClock, from, to int, mk func(sec int) powerGPUTick) {
	for s := from; s < to; s++ {
		tk := mk(s)
		tk.at = c.at(float64(s))
		a.addGPUAt(tk, c.wall(float64(s)))
	}
}

func sensorSamples(b powerhistory.Bucket) map[string]int {
	out := map[string]int{}
	for _, g := range b.GPUs {
		out[g.ID] = g.Samples
	}
	return out
}

func closeWindow(t *testing.T, a *powerAccumulator, c *accClock, endSec float64) powerhistory.Bucket {
	t.Helper()
	out := a.tickAt(c.at(endSec), c.wall(endSec))
	if len(out) != 1 {
		t.Fatalf("want exactly one bucket at %vs, got %d", endSec, len(out))
	}
	return out[0]
}

func TestAccumulatorMembershipAdditionOmitsTotalKeepsSensorsAndCPU(t *testing.T) {
	a := newPowerAccumulator(30*time.Second, time.Second)
	c := newAccClock()
	ab := func(int) powerGPUTick { return mkTick(time.Time{}, true, rd{"A", f(100)}, rd{"B", f(50)}) }
	abc := func(int) powerGPUTick {
		return mkTick(time.Time{}, true, rd{"A", f(100)}, rd{"B", f(50)}, rd{"C", f(25)})
	}
	feedTicks(a, c, 0, 15, ab)
	feedTicks(a, c, 15, 30, abc)
	for s := 0; s < 30; s++ {
		a.addCPUAt(c.at(float64(s)), c.wall(float64(s)), 12)
	}
	b := closeWindow(t, a, c, 30)
	if b.GPUTotal != nil {
		t.Fatalf("device added mid-window: total must be omitted, got %+v", *b.GPUTotal)
	}
	ss := sensorSamples(b)
	if ss["A"] != 30 || ss["B"] != 30 || ss["C"] != 15 {
		t.Fatalf("per-GPU readings must be retained: %v", ss)
	}
	if b.CPU == nil || b.CPU.Samples != 30 || *b.CPU.MeanWatts != 12 {
		t.Fatalf("CPU must be retained: %+v", b.CPU)
	}
}

func TestAccumulatorMembershipRemovalOmitsTotal(t *testing.T) {
	a := newPowerAccumulator(30*time.Second, time.Second)
	c := newAccClock()
	feedTicks(a, c, 0, 15, func(int) powerGPUTick { return mkTick(time.Time{}, true, rd{"A", f(100)}, rd{"B", f(50)}) })
	feedTicks(a, c, 15, 30, func(int) powerGPUTick { return mkTick(time.Time{}, true, rd{"A", f(100)}) })
	b := closeWindow(t, a, c, 30)
	if b.GPUTotal != nil {
		t.Fatalf("device removed mid-window: total must be omitted, got %+v", *b.GPUTotal)
	}
	if ss := sensorSamples(b); ss["A"] != 30 || ss["B"] != 15 {
		t.Fatalf("per-GPU readings must be retained: %v", ss)
	}
}

func TestAccumulatorMembershipSameCountReplacementOmitsTotal(t *testing.T) {
	a := newPowerAccumulator(30*time.Second, time.Second)
	c := newAccClock()
	feedTicks(a, c, 0, 15, func(int) powerGPUTick { return mkTick(time.Time{}, true, rd{"A", f(100)}, rd{"B", f(50)}) })
	// Same device count, different identity (uuid fallback flip, re-enumeration).
	feedTicks(a, c, 15, 30, func(int) powerGPUTick { return mkTick(time.Time{}, true, rd{"A", f(100)}, rd{"gpu-1", f(50)}) })
	b := closeWindow(t, a, c, 30)
	if b.GPUTotal != nil {
		t.Fatalf("same-count replacement: total must be omitted, got %+v", *b.GPUTotal)
	}
	if ss := sensorSamples(b); ss["A"] != 30 || ss["B"] != 15 || ss["gpu-1"] != 15 {
		t.Fatalf("per-GPU readings must be retained: %v", ss)
	}
}

func TestAccumulatorMembershipLeaveAndReturnOmitsTotal(t *testing.T) {
	a := newPowerAccumulator(30*time.Second, time.Second)
	c := newAccClock()
	ab := func(int) powerGPUTick { return mkTick(time.Time{}, true, rd{"A", f(100)}, rd{"B", f(50)}) }
	feedTicks(a, c, 0, 10, ab)
	feedTicks(a, c, 10, 15, func(int) powerGPUTick { return mkTick(time.Time{}, true, rd{"A", f(100)}) })
	feedTicks(a, c, 15, 30, ab)
	b := closeWindow(t, a, c, 30)
	if b.GPUTotal != nil {
		t.Fatalf("device left and returned: total must be omitted for the whole window, got %+v", *b.GPUTotal)
	}
	if ss := sensorSamples(b); ss["A"] != 30 || ss["B"] != 25 {
		t.Fatalf("per-GPU readings must be retained: %v", ss)
	}
}

func TestAccumulatorMixedTotalThatPassesCountCheckIsStillOmitted(t *testing.T) {
	a := newPowerAccumulator(30*time.Second, time.Second)
	c := newAccClock()
	// 10 ticks over {A,B} with watts: total would be 10 over {A,B}.
	feedTicks(a, c, 0, 10, func(int) powerGPUTick { return mkTick(time.Time{}, true, rd{"A", f(100)}, rd{"B", f(50)}) })
	// 20 ticks over {A,C} where A is N/A: these never enter the total, so a
	// naive "total.n <= min sensor samples" gate passes (10 <= min(10,10,20)),
	// yet the total no longer describes the reported sensor set.
	feedTicks(a, c, 10, 30, func(int) powerGPUTick { return mkTick(time.Time{}, true, rd{"A", nil}, rd{"C", f(25)}) })
	b := closeWindow(t, a, c, 30)
	ss := sensorSamples(b)
	if ss["A"] != 10 || ss["B"] != 10 || ss["C"] != 20 {
		t.Fatalf("per-GPU readings: %v", ss)
	}
	if b.GPUTotal != nil {
		t.Fatalf("mixed membership must omit the total even when counts look consistent, got %+v", *b.GPUTotal)
	}
}

func TestAccumulatorTotalRecoversInNextStableWindow(t *testing.T) {
	a := newPowerAccumulator(30*time.Second, time.Second)
	c := newAccClock()
	feedTicks(a, c, 0, 15, func(int) powerGPUTick { return mkTick(time.Time{}, true, rd{"A", f(100)}, rd{"B", f(50)}) })
	ac := func(int) powerGPUTick { return mkTick(time.Time{}, true, rd{"A", f(100)}, rd{"C", f(25)}) }
	feedTicks(a, c, 15, 30, ac)
	if b := closeWindow(t, a, c, 30); b.GPUTotal != nil {
		t.Fatalf("mixed window must omit total, got %+v", *b.GPUTotal)
	}
	feedTicks(a, c, 30, 60, ac)
	b := closeWindow(t, a, c, 60)
	if b.GPUTotal == nil || b.GPUTotal.Samples != 30 || *b.GPUTotal.MeanWatts != 125 {
		t.Fatalf("stable next window must report a total again: %+v", b.GPUTotal)
	}
	if len(b.GPUs) != 2 {
		t.Fatalf("stable window sensors: %+v", b.GPUs)
	}
}

func TestAccumulatorStableReorderNAAndIncompleteTicksKeepTotal(t *testing.T) {
	a := newPowerAccumulator(30*time.Second, time.Second)
	c := newAccClock()
	feedTicks(a, c, 0, 30, func(s int) powerGPUTick {
		switch {
		case s%3 == 0:
			// Same set, reported in the other order.
			return mkTick(time.Time{}, true, rd{"B", f(50)}, rd{"A", f(100)})
		case s%5 == 0:
			// Same set, one reading unavailable: not a membership change,
			// just a tick that cannot enter the total.
			return mkTick(time.Time{}, true, rd{"A", f(100)}, rd{"B", nil})
		case s%7 == 0:
			// Incomplete iteration (device missing from the read): never
			// counted, and must not be mistaken for a smaller device set.
			return mkTick(time.Time{}, false, rd{"A", f(100)})
		default:
			return mkTick(time.Time{}, true, rd{"A", f(100)}, rd{"B", f(50)})
		}
	})
	b := closeWindow(t, a, c, 30)
	if b.GPUTotal == nil {
		t.Fatal("stable membership with reorder, N/A and incomplete ticks must keep the total")
	}
	// N/A ticks: 5, 10, 20, 25 (s%5 but not s%3). Incomplete ticks: 7, 14, 28
	// (s%7 but not s%3 or s%5). Total counts the remaining 30 - 4 - 3 = 23.
	if b.GPUTotal.Samples != 23 || *b.GPUTotal.PeakWatts != 150 {
		t.Fatalf("total over stable ticks: %+v", *b.GPUTotal)
	}
	// B is absent from the 3 incomplete ticks and N/A on 4: 30 - 7 = 23.
	if ss := sensorSamples(b); ss["A"] != 30 || ss["B"] != 23 {
		t.Fatalf("per-GPU readings: %v", ss)
	}
}

func TestAccumulatorIncompleteTickIntroducingSensorOmitsTotal(t *testing.T) {
	a := newPowerAccumulator(30*time.Second, time.Second)
	c := newAccClock()
	// Reference set {A} from complete ticks; an incomplete tick then reports
	// a device B the reference never saw. The emitted union {A,B} no longer
	// matches the set the total was computed over, and B has fewer samples
	// than the total.
	feedTicks(a, c, 0, 20, func(int) powerGPUTick { return mkTick(time.Time{}, true, rd{"A", f(100)}) })
	feedTicks(a, c, 20, 30, func(int) powerGPUTick { return mkTick(time.Time{}, false, rd{"A", f(100)}, rd{"B", f(50)}) })
	b := closeWindow(t, a, c, 30)
	if ss := sensorSamples(b); ss["A"] != 30 || ss["B"] != 10 {
		t.Fatalf("per-GPU readings: %v", ss)
	}
	if b.GPUTotal != nil {
		t.Fatalf("sensor introduced by an incomplete tick must omit the total, got %+v", *b.GPUTotal)
	}
}

func TestAccumulatorPartialTicksBeforeAndAfterCompleteReferenceOmitTotal(t *testing.T) {
	for _, before := range []bool{true, false} {
		a := newPowerAccumulator(30*time.Second, time.Second)
		c := newAccClock()
		partial := func(int) powerGPUTick { return mkTick(time.Time{}, false, rd{"A", f(100)}, rd{"B", f(50)}) }
		complete := func(int) powerGPUTick { return mkTick(time.Time{}, true, rd{"A", f(100)}) }
		if before {
			feedTicks(a, c, 0, 5, partial)
			feedTicks(a, c, 5, 30, complete)
		} else {
			feedTicks(a, c, 0, 25, complete)
			feedTicks(a, c, 25, 30, partial)
		}
		b := closeWindow(t, a, c, 30)
		if ss := sensorSamples(b); ss["A"] != 30 || ss["B"] != 5 {
			t.Fatalf("before=%v per-GPU readings: %v", before, ss)
		}
		if b.GPUTotal != nil {
			t.Fatalf("before=%v: partial ticks outside the reference set must omit the total, got %+v", before, *b.GPUTotal)
		}
	}
}

func TestAccumulatorPartialTicksWithKnownSensorsKeepTotal(t *testing.T) {
	a := newPowerAccumulator(30*time.Second, time.Second)
	c := newAccClock()
	feedTicks(a, c, 0, 25, func(int) powerGPUTick { return mkTick(time.Time{}, true, rd{"A", f(100)}, rd{"B", f(50)}) })
	// Incomplete ticks naming only devices already in the reference set:
	// not counted, but not a membership change either.
	feedTicks(a, c, 25, 30, func(int) powerGPUTick { return mkTick(time.Time{}, false, rd{"A", f(100)}) })
	b := closeWindow(t, a, c, 30)
	if b.GPUTotal == nil || b.GPUTotal.Samples != 25 || *b.GPUTotal.MeanWatts != 150 {
		t.Fatalf("partial ticks over known sensors must keep the total: %+v", b.GPUTotal)
	}
	if ss := sensorSamples(b); ss["A"] != 30 || ss["B"] != 25 {
		t.Fatalf("per-GPU readings: %v", ss)
	}
}
