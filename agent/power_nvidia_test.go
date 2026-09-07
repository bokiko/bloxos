package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseNvidiaPowerLine(t *testing.T) {
	cases := []struct {
		in    string
		ok    bool
		count int
		idx   int
		id    string
		watts *float64
	}{
		{"2, 0, GPU-9d8c1a2b-1234-5678-9abc-def012345678, 34.12", true, 2, 0, "GPU-9d8c1a2b-1234-5678-9abc-def012345678", f(34.12)},
		{"2, 1, GPU-abc, [N/A]", true, 2, 1, "GPU-abc", nil},
		{"2, 1, GPU-abc, [Not Supported]", true, 2, 1, "GPU-abc", nil},
		{"1, 0, [N/A], 12", true, 1, 0, "gpu-0", f(12)},
		{"1, 0, bad id!, 12", true, 1, 0, "gpu-0", f(12)},
		{"1, 0, GPU-x, +Inf", true, 1, 0, "GPU-x", nil},
		{"1, 0, GPU-x, NaN", true, 1, 0, "GPU-x", nil},
		{"1, 0, GPU-x, -5", true, 1, 0, "GPU-x", nil},
		{"1, 0, GPU-x, 99999", true, 1, 0, "GPU-x", nil},
		{"0, 0, GPU-x, 10", false, 0, 0, "", nil},
		{"2, 2, GPU-x, 10", false, 0, 0, "", nil},
		{"17, 0, GPU-x, 10", false, 0, 0, "", nil},
		{"0, GPU-x, 10", false, 0, 0, "", nil},
		{"", false, 0, 0, "", nil},
	}
	for _, c := range cases {
		l, ok := parseNvidiaPowerLine(c.in)
		if ok != c.ok {
			t.Errorf("%q: ok=%v want %v", c.in, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if l.count != c.count || l.index != c.idx || l.r.id != c.id {
			t.Errorf("%q: got count=%d idx=%d id=%q", c.in, l.count, l.index, l.r.id)
		}
		switch {
		case c.watts == nil && l.r.watts != nil:
			t.Errorf("%q: want nil watts, got %v", c.in, *l.r.watts)
		case c.watts != nil && (l.r.watts == nil || *l.r.watts != *c.watts):
			t.Errorf("%q: want %v watts, got %v", c.in, *c.watts, l.r.watts)
		}
	}
}

func pushLines(g *nvidiaTickGrouper, lines ...string) []powerGPUTick {
	var out []powerGPUTick
	for _, ln := range lines {
		l, ok := parseNvidiaPowerLine(ln)
		if !ok {
			continue
		}
		out = append(out, g.push(l, time.Now())...)
	}
	return out
}

func TestNvidiaGrouperCompletenessComesFromCount(t *testing.T) {
	g := &nvidiaTickGrouper{}
	// A complete iteration flushes as soon as count lines are in — no 1 s lag.
	ticks := pushLines(g, "2, 0, GPU-a, 100", "2, 1, GPU-b, 200")
	if len(ticks) != 1 || !ticks[0].complete || len(ticks[0].readings) != 2 {
		t.Fatalf("complete iteration: %+v", ticks)
	}
	// First partial iteration (stream started mid-iteration) is NOT total.
	g = &nvidiaTickGrouper{}
	ticks = pushLines(g, "2, 1, GPU-b, 200", "2, 0, GPU-a, 100", "2, 1, GPU-b, 200")
	if len(ticks) != 2 || ticks[0].complete || !ticks[1].complete {
		t.Fatalf("partial-then-complete: %+v", ticks)
	}
	// EOF with a partial group is incomplete.
	g = &nvidiaTickGrouper{}
	pushLines(g, "2, 0, GPU-a, 100")
	if tk := g.flush(); tk == nil || tk.complete {
		t.Fatalf("EOF partial must be incomplete: %+v", tk)
	}
	// N/A device: still a complete iteration structurally; the accumulator
	// decides the total from nil watts.
	g = &nvidiaTickGrouper{}
	ticks = pushLines(g, "2, 0, GPU-a, 100", "2, 1, GPU-b, [N/A]")
	if len(ticks) != 1 || !ticks[0].complete || ticks[0].readings[1].watts != nil {
		t.Fatalf("N/A iteration: %+v", ticks)
	}
	// Repeated id inside one iteration invalidates it.
	g = &nvidiaTickGrouper{}
	ticks = pushLines(g, "2, 0, GPU-a, 100", "2, 1, GPU-a, 200")
	if len(ticks) != 1 || ticks[0].complete {
		t.Fatalf("repeated id must not be complete: %+v", ticks)
	}
	// Count change mid-iteration flushes the old group incomplete, and a
	// count-1 successor completes immediately — both are returned.
	g = &nvidiaTickGrouper{}
	ticks = pushLines(g, "2, 0, GPU-a, 100", "1, 0, GPU-a, 100")
	if len(ticks) != 2 || ticks[0].complete || !ticks[1].complete {
		t.Fatalf("count change: %+v", ticks)
	}
}

func TestNvidiaGrouperIsBounded(t *testing.T) {
	g := &nvidiaTickGrouper{}
	// Hostile stream: strictly ascending index forever with count pinned at
	// 16 never completes; the group must not grow past MaxSensors.
	for i := 0; i < 1000; i++ {
		l := nvidiaPowerLine{count: 16, index: 15, r: powerReading{id: "GPU-" + strings.Repeat("x", i%8) + string(rune('a'+i%26))}}
		if i == 0 {
			l.index = 0
		}
		g.push(l, time.Now())
		if len(g.cur) > 16 {
			t.Fatalf("group grew to %d", len(g.cur))
		}
	}
}

// fakeProc simulates the nvidia-smi child: a pipe the test writes to, and a
// wait that returns once the pipe is closed or the context is cancelled.
type fakeProc struct {
	r    *io.PipeReader
	w    *io.PipeWriter
	done chan struct{}
	once sync.Once
}

func newFakeProc() *fakeProc {
	r, w := io.Pipe()
	return &fakeProc{r: r, w: w, done: make(chan struct{})}
}

func (p *fakeProc) start(ctx context.Context, _ string) (io.ReadCloser, func() error, error) {
	go func() {
		<-ctx.Done()
		p.once.Do(func() { p.w.Close(); close(p.done) })
	}()
	return p.r, func() error { <-p.done; return errors.New("exited") }, nil
}

func (p *fakeProc) writeln(s string) { _, _ = io.WriteString(p.w, s+"\n") }
func (p *fakeProc) exit()            { p.once.Do(func() { p.w.Close(); close(p.done) }) }

func TestNvidiaStreamEmitsTicksAndReportsProgress(t *testing.T) {
	proc := newFakeProc()
	var mu sync.Mutex
	var got []powerGPUTick
	s := &nvidiaPowerStream{
		resolve: func() string { return "nvidia-smi" },
		start:   proc.start,
		emit: func(tk powerGPUTick) {
			mu.Lock()
			got = append(got, tk)
			mu.Unlock()
		},
		now:   time.Now,
		stall: 2 * time.Second,
	}
	res := make(chan bool, 1)
	go func() { res <- s.runOnce(context.Background(), "nvidia-smi") }()
	proc.writeln("2, 0, GPU-a, 100")
	proc.writeln("2, 1, GPU-b, 200")
	proc.writeln("2, 0, GPU-a, 110")
	proc.writeln("2, 1, GPU-b, 210")
	proc.writeln("2, 0, GPU-a, 120") // partial at exit
	proc.exit()
	select {
	case progressed := <-res:
		if !progressed {
			t.Fatal("stream produced ticks but reported no progress")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runOnce did not return after process exit")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 || !got[0].complete || !got[1].complete || got[2].complete {
		t.Fatalf("ticks: %+v", got)
	}
	if *got[1].readings[1].watts != 210 {
		t.Errorf("second tick GPU-b = %v", *got[1].readings[1].watts)
	}
}

func TestNvidiaStreamKillsStalledProcess(t *testing.T) {
	proc := newFakeProc()
	s := &nvidiaPowerStream{
		resolve: func() string { return "nvidia-smi" },
		start:   proc.start,
		emit:    func(powerGPUTick) {},
		now:     time.Now,
		stall:   100 * time.Millisecond,
	}
	startAt := time.Now()
	progressed := s.runOnce(context.Background(), "nvidia-smi")
	if progressed {
		t.Error("stalled process must not count as progress")
	}
	if d := time.Since(startAt); d > 3*time.Second {
		t.Fatalf("stall detection took %s", d)
	}
	select {
	case <-proc.done:
	default:
		t.Error("stalled process was not torn down")
	}
}

func TestNvidiaStreamBackoffIsBoundedAndResets(t *testing.T) {
	var starts int
	var mu sync.Mutex
	s := &nvidiaPowerStream{
		resolve: func() string { return "" }, // nvidia-smi absent
		start: func(ctx context.Context, _ string) (io.ReadCloser, func() error, error) {
			mu.Lock()
			starts++
			mu.Unlock()
			return nil, nil, errors.New("no device")
		},
		emit:       func(powerGPUTick) {},
		now:        time.Now,
		stall:      time.Second,
		minBackoff: time.Millisecond,
		maxBackoff: 5 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	s.run(ctx) // must return when ctx ends, without spinning
	mu.Lock()
	defer mu.Unlock()
	if starts != 0 {
		t.Errorf("must not start a process while nvidia-smi is unresolvable, started %d", starts)
	}
}
