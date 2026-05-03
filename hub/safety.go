package main

import (
	"log"
	"runtime/debug"
	"time"
)

// goroutineRestartBackoff is how long goSafelyForever waits before
// restarting a recovered or unexpectedly-returned goroutine. Long
// enough that a tight panic loop doesn't overwhelm the log; short
// enough that real recovery is timely.
const goroutineRestartBackoff = 5 * time.Second

// runWithRecover runs fn synchronously inside a defer/recover, logs
// the recovered value + stack on panic, and returns true if a panic
// was caught. Returns false if fn returned normally.
//
// This is the testable seam for the panic-recovery pattern. The outer
// goSafelyForever / goSafelyOnce wrappers are too coupled to the
// goroutine + sleep model to test cleanly without polluting the suite
// with timing — tests target this function directly.
func runWithRecover(name string, fn func()) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("goroutine %s: PANIC recovered: %v\n%s", name, r, debug.Stack())
			panicked = true
		}
	}()
	fn()
	return false
}

// goSafelyForever spawns fn in a new goroutine for an indefinite
// background loop. If fn panics OR returns unexpectedly, the wrapper
// logs the event and restarts fn after goroutineRestartBackoff. Use
// for things that are supposed to run forever — alertEvalLoop,
// cleanupLoop, versionRefreshLoop, reconnectMonitorLoop. A normal
// return is treated as a bug too: the loop body in those functions is
// itself an infinite for-loop, so reaching the end of fn is
// pathological and we restart anyway.
func goSafelyForever(name string, fn func()) {
	go func() {
		for {
			panicked := runWithRecover(name, fn)
			if !panicked {
				log.Printf("goroutine %s: returned without panic — unexpected for a forever-loop, restarting after backoff",
					name)
			}
			time.Sleep(goroutineRestartBackoff)
		}
	}()
}

// goSafelyOnce spawns fn in a new goroutine for a one-shot task that's
// expected to terminate (e.g. a per-session terminal relay). Panics
// are recovered and logged, but the goroutine does not restart — the
// caller owns the lifecycle and will create a new goroutine for the
// next session if needed. Without this wrapper, a panic in a session
// handler would crash the whole hub process.
func goSafelyOnce(name string, fn func()) {
	go func() {
		_ = runWithRecover(name, fn)
	}()
}
