package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bokiko/bloxos/proto/powerhistory"
	"github.com/gorilla/websocket"
)

// Power history: a 1 Hz component-power sampler (GPU via a streaming
// nvidia-smi process, CPU via the Linux RAPL energy counter where readable)
// feeding a 30 s accumulator whose completed buckets are appended to a
// bounded local journal and fsync'd BEFORE they become eligible for upload.
// Upload is one bounded batch per 30 s metrics tick; the hub ACKs a
// contiguous durable prefix and the agent persists that cursor.
//
// Concurrency contract: the journal (and every fsync) is owned by ONE worker
// goroutine. The metrics tick reads an immutable atomic snapshot to build a
// batch, and the websocket read loop only drops an ACK into a coalescing
// mailbox. Neither can ever wait on disk.
//
// Nothing here touches the existing instant `power_watts` field in the
// metrics frame — that stays an instantaneous nvidia-smi reading and is
// semantically distinct from the 30 s mean carried in these buckets.

const (
	powerSampleInterval = time.Second
	powerWindow         = powerhistory.WindowSeconds * time.Second
	// powerQueueDepth bounds completed buckets waiting on the journal
	// goroutine. A blocked fsync therefore never stalls the sampler or the
	// metrics tick; overflow drops the bucket and flags a gap instead.
	powerQueueDepth = 64
	// powerMaxSeq keeps sequence numbers exactly representable in JS
	// (shared contract). Reaching it rotates the stream identity.
	powerMaxSeq = powerhistory.MaxSeq
	// powerClockStepTolerance is how far the wall clock may drift from the
	// monotonic clock across one window before the next bucket is flagged
	// as following a clock discontinuity.
	powerClockStepTolerance = 2 * time.Second
	// powerJournalRetry paces reopening an unavailable journal directory.
	powerJournalRetry = time.Minute
	// powerSendMinInterval enforces the approved upload cadence (one batch
	// per 30 s tick) even though sendAll also runs on reconnect and on a
	// manual refresh_metrics. Slightly under the tick so ticker jitter never
	// skips a regular send.
	powerSendMinInterval = 25 * time.Second
)

// powerRetentionTick is how often the journal worker enforces retention
// even when no bucket arrives (sensors gone, everything acknowledged).
var powerRetentionTick = time.Minute

// powerReading is one sensor's sample within a tick. Nil watts = N/A.
type powerReading struct {
	id    string
	watts *float64
}

// powerGPUTick is one simultaneous read of every GPU sensor. complete is
// true only when the tick includes every sensor the sampler knows about,
// which is the precondition for a truthful GPU total.
type powerGPUTick struct {
	at       time.Time
	readings []powerReading
	complete bool
}

type statsAcc struct {
	sum  float64
	n    int
	peak float64
}

func (a *statsAcc) add(w float64) {
	if a.n == 0 || w > a.peak {
		a.peak = w
	}
	a.sum += w
	a.n++
}

func (a *statsAcc) stats() powerhistory.Stats {
	if a.n == 0 {
		return powerhistory.Stats{}
	}
	mean := a.sum / float64(a.n)
	peak := a.peak
	// Summation rounding can push the mean of a constant series a hair
	// above its peak (0.1 × 30); the hub requires mean ≤ peak.
	if mean > peak {
		mean = peak
	}
	return powerhistory.Stats{MeanWatts: &mean, PeakWatts: &peak, Samples: a.n}
}

type powerWindowState struct {
	gpus  map[string]*statsAcc
	order []string
	total statsAcc
	cpu   statsAcc
	any   bool
}

// powerAccumulator folds 1 Hz samples into fixed 30 s windows tiled on the
// monotonic clock. Bucket start is a wall anchor; bucket end is always
// start + monotonic elapsed, so end > start. A wall-clock step can close the
// open window early, preserving its partial sample count; the next window
// is re-anchored to the new wall time and flagged GapBefore. Per-sensor counts are
// capped at ExpectedSamples so producer jitter can never over-report.
type powerAccumulator struct {
	mu          sync.Mutex
	window      time.Duration
	expected    int
	startMono   time.Time // zero until the first event
	startWallMS int64
	cur         *powerWindowState
	gapPending  bool
	ready       []powerhistory.Bucket
}

func newPowerAccumulator(window, interval time.Duration) *powerAccumulator {
	return &powerAccumulator{window: window, expected: int(window / interval), gapPending: true}
}

func newPowerWindowState() *powerWindowState {
	return &powerWindowState{gpus: map[string]*statsAcc{}}
}

// roll closes every window that has elapsed as of now (monotonic) and wallMS
// (observed wall clock). Caller holds mu.
func (a *powerAccumulator) roll(now time.Time, wallMS int64) {
	if a.startMono.IsZero() {
		a.startMono, a.startWallMS, a.cur = now, wallMS, newPowerWindowState()
		return
	}
	for now.Sub(a.startMono) >= a.window {
		endMS := a.startWallMS + a.window.Milliseconds()
		a.emit(endMS)
		a.startMono = a.startMono.Add(a.window)
		a.startWallMS = endMS
		a.cur = newPowerWindowState()
		// Far behind (suspend/resume): don't emit a run of empty windows.
		if now.Sub(a.startMono) >= 2*a.window {
			a.startMono, a.startWallMS, a.gapPending = now, wallMS, true
			break
		}
	}
	// Clock discontinuity: wall time no longer agrees with the monotonic
	// anchor. Re-anchor the open window's start so its end stays truthful
	// and flag the discontinuity on whatever is emitted next.
	expectedMS := a.startWallMS + now.Sub(a.startMono).Milliseconds()
	if d := wallMS - expectedMS; d > powerClockStepTolerance.Milliseconds() || d < -powerClockStepTolerance.Milliseconds() {
		if a.cur != nil && a.cur.any {
			a.emit(expectedMS)
			a.cur = newPowerWindowState()
		}
		a.startMono, a.startWallMS, a.gapPending = now, wallMS, true
	}
}

// emit converts the open window into a bucket (if it saw any sample).
// Caller holds mu.
func (a *powerAccumulator) emit(endMS int64) {
	w := a.cur
	if w == nil || !w.any {
		a.gapPending = true
		return
	}
	b := powerhistory.Bucket{
		StartUnixMS:     a.startWallMS,
		EndUnixMS:       endMS,
		ExpectedSamples: a.expected,
		GPUs:            make([]powerhistory.Sensor, 0, len(w.order)),
		GapBefore:       a.gapPending,
	}
	a.gapPending = false
	for _, id := range w.order {
		b.GPUs = append(b.GPUs, powerhistory.Sensor{ID: id, Stats: w.gpus[id].stats()})
	}
	if w.total.n > 0 {
		s := w.total.stats()
		b.GPUTotal = &s
	}
	if w.cpu.n > 0 {
		s := w.cpu.stats()
		b.CPU = &s
	}
	a.ready = append(a.ready, b)
}

func (a *powerAccumulator) addGPU(t powerGPUTick) { a.addGPUAt(t, t.at.UnixMilli()) }

func (a *powerAccumulator) addGPUAt(t powerGPUTick, wallMS int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.roll(t.at, wallMS)
	w := a.cur
	sum := 0.0
	allPresent := t.complete && len(t.readings) > 0
	for _, r := range t.readings {
		acc, ok := w.gpus[r.id]
		if !ok {
			if len(w.order) >= powerhistory.MaxSensors {
				allPresent = false
				continue
			}
			acc = &statsAcc{}
			w.gpus[r.id] = acc
			w.order = append(w.order, r.id)
		}
		if r.watts == nil {
			allPresent = false
			continue
		}
		if acc.n < a.expected {
			acc.add(*r.watts)
			w.any = true
		}
		sum += *r.watts
	}
	if allPresent && w.total.n < a.expected {
		w.total.add(sum)
	}
}

func (a *powerAccumulator) addCPU(at time.Time, watts float64) { a.addCPUAt(at, at.UnixMilli(), watts) }

func (a *powerAccumulator) addCPUAt(at time.Time, wallMS int64, watts float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.roll(at, wallMS)
	if a.cur.cpu.n < a.expected {
		a.cur.cpu.add(watts)
		a.cur.any = true
	}
}

// tick advances the window clock and drains completed buckets (Seq unset;
// the journal assigns it).
func (a *powerAccumulator) tick(now time.Time) []powerhistory.Bucket {
	return a.tickAt(now, now.UnixMilli())
}

func (a *powerAccumulator) tickAt(now time.Time, wallMS int64) []powerhistory.Bucket {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.roll(now, wallMS)
	out := a.ready
	a.ready = nil
	return out
}

// markGap flags the next emitted bucket as preceded by collection loss.
func (a *powerAccumulator) markGap() {
	a.mu.Lock()
	a.gapPending = true
	a.mu.Unlock()
}

// powerCPUSampler is implemented per platform (RAPL on Linux). Nil means the
// backend is unavailable on this host.
type powerCPUSampler interface {
	// sample returns package watts since the previous call, or false when no
	// rate is available yet (first call, counter reset, read error).
	sample(now time.Time) (float64, bool)
}

// powerHistory is the process-wide singleton wiring sampler → accumulator →
// journal worker → uplink.
type powerHistory struct {
	acc     *powerAccumulator
	journal *powerJournal // worker goroutine only
	queue   chan powerhistory.Bucket
	ackMu   sync.Mutex
	ackNext *powerhistory.Ack // coalescing mailbox: newest ACK wins
	ackSig  chan struct{}
	snap    atomic.Pointer[powerSnapshot]
	// dropped: a bucket was lost at the queue; the NEXT enqueued bucket
	// declares the gap. writeFailed: a bucket was lost at the journal; the
	// next successfully appended bucket declares it.
	dropped     atomic.Bool
	writeFailed atomic.Bool
	// workerDone is closed when journalWorker returns (the journal is then
	// closed and may be touched by nobody else).
	workerDone chan struct{}
	// sendMu/lastSend implement the upload throttle (monotonic clock).
	sendMu   sync.Mutex
	lastSend time.Time
	// degraded is true while buckets are being lost locally (queue overflow
	// or journal write failure). Cleared by the next successful append.
	degraded atomic.Bool
	cancel   context.CancelFunc
}

var (
	powerOnce sync.Once
	powerInst atomic.Pointer[powerHistory]
)

// powerHistoryDir is /etc/bloxos/power-history when running as root on a
// POSIX host, and otherwise sits beside the agent's other durable state
// (Windows: the systemprofile credential directory).
func powerHistoryDir() string {
	if runtime.GOOS != "windows" {
		if u, err := user.Current(); err == nil && u.Uid == "0" {
			return "/etc/bloxos/power-history"
		}
	}
	return filepath.Join(filepath.Dir(credentialFilePath()), "power-history")
}

// startPowerHistory boots the singleton once, off the caller's goroutine so
// slow or unavailable storage never delays agent startup. An unavailable
// journal directory is retried on a fixed cadence rather than disabling the
// feature for the life of the process. BLOXOS_POWER_HISTORY=0 is a hard
// local opt-out.
func startPowerHistory() {
	powerOnce.Do(func() {
		if os.Getenv("BLOXOS_POWER_HISTORY") == "0" {
			log.Println("power-history: disabled by BLOXOS_POWER_HISTORY=0")
			return
		}
		go bootPowerHistory(context.Background(), powerHistoryDir(), powerJournalRetry)
	})
}

func bootPowerHistory(ctx context.Context, dir string, retry time.Duration) {
	gpu := resolveNvidiaSmiPath() != ""
	cpu := newPowerCPUSampler()
	if !gpu && cpu == nil {
		log.Println("power-history: no supported power sensor on this host (no nvidia-smi, no readable CPU energy counter); not started")
		return
	}
	var j *powerJournal
	for attempt := 1; ; attempt++ {
		var err error
		j, err = openPowerJournal(dir)
		if err == nil {
			break
		}
		if attempt == 1 || attempt%10 == 0 {
			log.Printf("power-history: journal unavailable at %s (attempt %d), history degraded, retrying every %s: %v", dir, attempt, retry, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retry):
		}
	}
	// Log journal state before the worker owns it; afterwards only the
	// worker may read these fields.
	log.Printf("power-history: starting stream=%s dir=%s gpu=%v cpu=%v next_seq=%d acked=%d retained=%d",
		j.streamID, j.dir, gpu, cpu != nil, j.nextSeq, j.acked, len(j.records))
	ph := newPowerHistory(j)
	ctx, ph.cancel = context.WithCancel(ctx)
	go ph.journalWorker(ctx)
	go ph.windowLoop(ctx)
	if gpu {
		s := newNvidiaPowerStream(resolveNvidiaSmiPath, ph.acc.addGPU)
		go s.run(ctx)
	}
	if cpu != nil {
		go ph.cpuLoop(ctx, cpu)
	}
	powerInst.Store(ph)
}

func newPowerHistory(j *powerJournal) *powerHistory {
	ph := &powerHistory{
		acc:        newPowerAccumulator(powerWindow, powerSampleInterval),
		journal:    j,
		queue:      make(chan powerhistory.Bucket, powerQueueDepth),
		ackSig:     make(chan struct{}, 1),
		workerDone: make(chan struct{}),
	}
	ph.snap.Store(j.snapshot())
	return ph
}

func (ph *powerHistory) windowLoop(ctx context.Context) {
	t := time.NewTicker(powerSampleInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			for _, b := range ph.acc.tick(now) {
				ph.enqueue(b)
			}
		}
	}
}

// enqueue hands a completed bucket to the journal worker without blocking.
func (ph *powerHistory) enqueue(b powerhistory.Bucket) {
	if ph.dropped.Load() {
		b.GapBefore = true
	}
	select {
	case ph.queue <- b:
		ph.dropped.Store(false)
	default:
		ph.dropped.Store(true)
		ph.degraded.Store(true)
		log.Printf("power-history: journal queue full, bucket dropped (gap declared)")
	}
}

// journalWorker is the only goroutine that touches the journal or fsyncs.
func (ph *powerHistory) journalWorker(ctx context.Context) {
	defer close(ph.workerDone)
	defer ph.journal.close()
	retention := time.NewTicker(powerRetentionTick)
	defer retention.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-ph.queue:
			ph.appendBucket(b)
		case <-retention.C:
			if err := ph.journal.enforce(); err != nil {
				if ph.degraded.CompareAndSwap(false, true) {
					log.Printf("power-history: retention enforcement failed, history degraded: %v", err)
				}
			}
			ph.snap.Store(ph.journal.snapshot())
		case <-ph.ackSig:
			ph.ackMu.Lock()
			ack := ph.ackNext
			ph.ackNext = nil
			ph.ackMu.Unlock()
			if ack != nil {
				ph.applyAck(*ack)
			}
		}
	}
}

func (ph *powerHistory) appendBucket(b powerhistory.Bucket) {
	if ph.writeFailed.Load() {
		b.GapBefore = true
	}
	if err := ph.journal.append(b); err != nil {
		// Not durable → never uploaded. The next durable bucket declares
		// the gap.
		ph.writeFailed.Store(true)
		if ph.degraded.CompareAndSwap(false, true) {
			log.Printf("power-history: journal write failed, history degraded until writes recover: %v", err)
		}
		return
	}
	ph.writeFailed.Store(false)
	if ph.degraded.Swap(false) {
		log.Println("power-history: journal writes recovered")
	}
	ph.snap.Store(ph.journal.snapshot())
}

func (ph *powerHistory) applyAck(ack powerhistory.Ack) {
	advanced, err := ph.journal.ack(ack.StreamID, ack.Through)
	if err != nil {
		log.Printf("power-history: ack %d rejected: %v", ack.Through, err)
		return
	}
	if advanced {
		ph.snap.Store(ph.journal.snapshot())
	}
}

// offerAck is the non-blocking hand-off from the websocket read loop. ACKs
// are monotone, so only the newest unprocessed one needs to survive.
func (ph *powerHistory) offerAck(ack powerhistory.Ack) {
	ph.ackMu.Lock()
	if ph.ackNext == nil || ack.Through > ph.ackNext.Through || ack.StreamID != ph.ackNext.StreamID {
		ph.ackNext = &ack
	}
	ph.ackMu.Unlock()
	select {
	case ph.ackSig <- struct{}{}:
	default:
	}
}

func (ph *powerHistory) cpuLoop(ctx context.Context, s powerCPUSampler) {
	t := time.NewTicker(powerSampleInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if w, ok := s.sample(now); ok {
				ph.acc.addCPU(now, w)
			}
		}
	}
}

// nextBatch builds the next upload from the immutable snapshot. Never
// touches the journal or disk.
func (ph *powerHistory) nextBatch() (*powerhistory.Batch, bool) {
	snap := ph.snap.Load()
	if snap == nil {
		return nil, false
	}
	batch, ok := snap.pendingBatch(powerhistory.MaxBatchRecords, powerhistory.MaxFrameBytes)
	if !ok {
		return nil, false
	}
	batch.Degraded = ph.degraded.Load()
	return batch, true
}

// allowSend claims a send slot if at least powerSendMinInterval has passed
// on the monotonic clock since the last claim. The lock is held only for
// the check, never across the network write.
func (ph *powerHistory) allowSend(now time.Time) bool {
	ph.sendMu.Lock()
	defer ph.sendMu.Unlock()
	if !ph.lastSend.IsZero() && now.Sub(ph.lastSend) < powerSendMinInterval {
		return false
	}
	ph.lastSend = now
	return true
}

// sendPowerHistory ships at most one bounded batch per powerSendMinInterval,
// whatever triggers sendAll (30 s tick, reconnect, manual refresh). The
// normal cadence is one batch per 30 s tick: a full 24 h backlog (2880
// buckets) drains in roughly 47 minutes with new windows arriving. Lost ACKs cause
// the same prefix to be resent; the hub dedupes on (stream, seq).
func sendPowerHistory(conn *websocket.Conn, mu *sync.Mutex) {
	ph := powerInst.Load()
	if ph == nil {
		return
	}
	batch, ok := ph.nextBatch()
	if !ok {
		return
	}
	if !ph.allowSend(time.Now()) {
		return
	}
	if err := writeJSON(conn, mu, batch); err != nil {
		log.Printf("power-history: send batch: %v", err)
	}
}

// handlePowerHistoryAck parses an ACK and hands it to the journal worker.
// Validation (stream match, not beyond last durable seq, not behind the
// cursor) happens on the worker, which owns the authoritative state.
func handlePowerHistoryAck(msg []byte) {
	ph := powerInst.Load()
	if ph == nil {
		return
	}
	var ack powerhistory.Ack
	if err := json.Unmarshal(msg, &ack); err != nil {
		log.Printf("power-history: bad ack: %v", err)
		return
	}
	ph.offerAck(ack)
}

// powerAckType is the inbound frame type dispatched by runAgent's read loop.
const powerAckType = powerhistory.AckType
