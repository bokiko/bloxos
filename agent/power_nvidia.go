package main

import (
	"bufio"
	"context"
	"io"
	"log"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
)

// nvidiaPowerStream keeps ONE long-lived nvidia-smi process streaming
// per-GPU power at 1 Hz instead of forking a full `-x -q` dump every second.
// The process is restarted with bounded exponential backoff when it exits,
// and killed when it stalls (no line within stallTimeout). Nothing here
// touches the network or the journal.
type nvidiaPowerStream struct {
	resolve    func() string
	start      func(ctx context.Context, path string) (io.ReadCloser, func() error, error)
	emit       func(powerGPUTick)
	now        func() time.Time
	stall      time.Duration
	minBackoff time.Duration
	maxBackoff time.Duration
}

func newNvidiaPowerStream(resolve func() string, emit func(powerGPUTick)) *nvidiaPowerStream {
	return &nvidiaPowerStream{
		resolve:    resolve,
		start:      startNvidiaSmiStream,
		emit:       emit,
		now:        time.Now,
		stall:      10 * time.Second,
		minBackoff: 2 * time.Second,
		maxBackoff: 5 * time.Minute,
	}
}

// nvidiaPowerArgs is the streaming query. count (repeated on every line)
// tells the grouper how many lines make one complete iteration; uuid gives a
// sensor id that is stable across reboots and re-enumeration; index only
// detects the start of a new iteration when the previous one was partial.
var nvidiaPowerArgs = []string{
	"--query-gpu=count,index,uuid,power.draw",
	"--format=csv,noheader,nounits",
	"-lms", strconv.Itoa(int(powerSampleInterval / time.Millisecond)),
}

func startNvidiaSmiStream(ctx context.Context, path string) (io.ReadCloser, func() error, error) {
	cmd := exec.CommandContext(ctx, path, nvidiaPowerArgs...)
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	return out, cmd.Wait, nil
}

func (s *nvidiaPowerStream) run(ctx context.Context) {
	backoff := s.minBackoff
	for ctx.Err() == nil {
		path := s.resolve()
		progressed := false
		if path != "" {
			progressed = s.runOnce(ctx, path)
		}
		if progressed {
			backoff = s.minBackoff
		} else {
			backoff = min(backoff*2, s.maxBackoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}

// runOnce drives one process to completion and reports whether it produced
// at least one usable tick (which resets the restart backoff).
func (s *nvidiaPowerStream) runOnce(ctx context.Context, path string) bool {
	pctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out, wait, err := s.start(pctx, path)
	if err != nil {
		log.Printf("power-history: start nvidia-smi stream: %v", err)
		return false
	}
	lines := make(chan string, 64)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 4096), 64<<10)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-pctx.Done():
				return
			}
		}
	}()
	g := &nvidiaTickGrouper{}
	progressed := false
	stall := time.NewTimer(s.stall)
	defer stall.Stop()
	for {
		select {
		case <-pctx.Done():
			cancel()
			_ = wait()
			return progressed
		case <-stall.C:
			log.Printf("power-history: nvidia-smi stream stalled for %s, restarting", s.stall)
			cancel()
			_ = wait()
			return progressed
		case line, ok := <-lines:
			if !ok {
				if t := g.flush(); t != nil {
					s.emit(*t)
					progressed = true
				}
				err := wait()
				if ctx.Err() == nil {
					log.Printf("power-history: nvidia-smi stream exited: %v", err)
				}
				return progressed
			}
			stall.Reset(s.stall) // Go ≥1.23 timers: safe without draining
			l, ok := parseNvidiaPowerLine(line)
			if !ok {
				continue
			}
			for _, t := range g.push(l, s.now()) {
				s.emit(t)
				progressed = true
			}
		}
	}
}

// nvidiaPowerLine is one parsed "count, index, uuid, power" line.
type nvidiaPowerLine struct {
	count int
	index int
	r     powerReading
}

// nvidiaMaxWatts bounds a plausible single-device reading.
const nvidiaMaxWatts = 10000

// parseNvidiaPowerLine parses a line emitted with --format=csv,noheader,
// nounits. Unreadable power ("[N/A]", "[Not Supported]", "[Unknown Error]",
// non-finite or out of range) yields a nil reading for that sensor. A
// missing or malformed uuid falls back to "gpu-<index>".
func parseNvidiaPowerLine(line string) (nvidiaPowerLine, bool) {
	parts := strings.Split(line, ",")
	if len(parts) != 4 {
		return nvidiaPowerLine{}, false
	}
	count, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || count < 1 || count > powerhistory.MaxSensors {
		return nvidiaPowerLine{}, false
	}
	idx, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || idx < 0 || idx >= count {
		return nvidiaPowerLine{}, false
	}
	id := strings.TrimSpace(parts[2])
	if !isPowerSensorID(id) {
		id = "gpu-" + strconv.Itoa(idx)
	}
	l := nvidiaPowerLine{count: count, index: idx, r: powerReading{id: id}}
	if w, err := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64); err == nil &&
		!math.IsNaN(w) && !math.IsInf(w, 0) && w >= 0 && w <= nvidiaMaxWatts {
		l.r.watts = &w
	}
	return l, true
}

// isPowerSensorID accepts the hub's sensor-id alphabet: 1–64 chars of
// [A-Za-z0-9_-] (an NVIDIA uuid such as "GPU-9d8c…" fits).
func isPowerSensorID(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// nvidiaTickGrouper reassembles the per-GPU lines of one -lms iteration
// into a tick. An iteration is complete when it holds `count` distinct
// sensors; it is flushed incomplete when a new iteration starts early
// (index no longer increasing), when the count changes mid-iteration, or
// when a sensor id repeats (that line is discarded). The group can never
// exceed MaxSensors.
type nvidiaTickGrouper struct {
	count     int
	cur       []powerReading
	seen      map[string]bool
	lastIndex int
	at        time.Time
	valid     bool
}

// push adds one line and returns zero, one or two ticks: a partial
// predecessor that had to be flushed early, and/or the group just completed.
func (g *nvidiaTickGrouper) push(l nvidiaPowerLine, now time.Time) []powerGPUTick {
	var out []powerGPUTick
	if len(g.cur) > 0 && (l.index <= g.lastIndex || l.count != g.count) {
		if t := g.flush(); t != nil {
			out = append(out, *t)
		}
	}
	if len(g.cur) > 0 && (g.seen[l.r.id] || len(g.cur) >= powerhistory.MaxSensors) {
		// A repeated sensor or an overlong iteration: the group is broken.
		// Flush it incomplete and drop the offending line.
		g.valid = false
		if t := g.flush(); t != nil {
			out = append(out, *t)
		}
		g.lastIndex = l.index
		return out
	}
	if len(g.cur) == 0 {
		g.at, g.count, g.seen, g.valid = now, l.count, map[string]bool{}, true
	}
	g.seen[l.r.id] = true
	g.cur = append(g.cur, l.r)
	g.lastIndex = l.index
	if len(g.cur) >= g.count {
		if t := g.flush(); t != nil {
			out = append(out, *t)
		}
	}
	return out
}

// flush emits the current group; complete only when it holds exactly the
// advertised count of distinct, valid sensors.
func (g *nvidiaTickGrouper) flush() *powerGPUTick {
	if len(g.cur) == 0 {
		return nil
	}
	t := &powerGPUTick{at: g.at, readings: g.cur, complete: g.valid && len(g.cur) == g.count}
	g.cur, g.seen = nil, nil
	return t
}
