package main

import (
	"database/sql"
	"log"
	"sync"
	"time"
)

// Server owns the per-instance state the hub's handlers operate on. Hanging
// this off a struct (rather than package-level globals) lets each test build a
// fully isolated instance instead of reassigning shared globals.
type Server struct {
	db *sql.DB

	// Connected agents keyed by machine ID.
	agents   map[string]*ConnectedAgent
	agentsMu sync.RWMutex

	// Tracks background work this server spawned, so a caller can wait for
	// it to finish before tearing the server down. See goTracked/Shutdown.
	//
	// lifecycleMu guards the wg.Add/closing pair. sync.WaitGroup requires
	// that an Add which takes the counter up from zero happens-before any
	// Wait; without this lock a connection arriving during teardown races
	// Shutdown's Wait, which the race detector reports (and which is a real
	// contract violation, not a false positive).
	wg          sync.WaitGroup
	lifecycleMu sync.Mutex
	closing     bool
}

// newServer returns a Server backed by db with an empty agent registry.
func newServer(db *sql.DB) *Server {
	return &Server{
		db:     db,
		agents: make(map[string]*ConnectedAgent),
	}
}

// goTracked runs fn in a goroutine registered against this server's WaitGroup.
//
// Use it for background work that touches server-owned state (s.db, s.agents).
// Such a goroutine can outlive the request that spawned it, and in tests it can
// outlive the test itself — which is how issue #60's race was reachable: a
// leaked announceVersionToAgent goroutine read the database handle while the
// next test replaced it. Ownership (each Server holding its own db) is what
// closes that race; this exists so a caller can additionally *wait* for the
// work to drain rather than racing teardown against it.
// Work offered after Shutdown has begun is dropped rather than started: the
// server is being torn down, so the only alternative is to spawn a goroutine
// nothing will ever wait for.
func (s *Server) goTracked(fn func()) {
	s.lifecycleMu.Lock()
	if s.closing {
		s.lifecycleMu.Unlock()
		return
	}
	s.wg.Add(1)
	s.lifecycleMu.Unlock()

	go func() {
		defer s.wg.Done()
		fn()
	}()
}

// Shutdown blocks until background work spawned via goTracked has finished, or
// until timeout elapses. It reports whether everything drained in time.
//
// A timeout rather than an unbounded Wait: a wedged background goroutine should
// surface as a diagnosable warning, not as a test binary that hangs until the
// harness kills it with an unreadable stack dump.
func (s *Server) Shutdown(timeout time.Duration) bool {
	// Close the gate before waiting. Once this returns, goTracked can no
	// longer call wg.Add, which is what makes the Wait below well-defined.
	s.lifecycleMu.Lock()
	s.closing = true
	s.lifecycleMu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		log.Printf("server shutdown: background work still running after %s", timeout)
		return false
	}
}
