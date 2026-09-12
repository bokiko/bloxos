package main

import (
	"database/sql"
	"fmt"
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

	// Serializes agent authentication/registration with credential revocation.
	agentAuthMu sync.RWMutex

	// Serializes durable operator pause/resume with queuing update announcements.
	// The persisted row is authoritative; there is no restart-sensitive cache.
	operatorRolloutMu sync.RWMutex

	// rollout is the single policy every announcement path goes through. When
	// it cannot be built, rolloutErr is set and every announcement is withheld
	// — never allowed to fall through to the unrestricted legacy path, which
	// would send a bad build to the whole fleet precisely when the thing that
	// bounds exposure is broken.
	rollout    *rolloutController
	rolloutErr error
	// rolloutStop cancels the single scheduler this server owns.
	rolloutStop chan struct{}
	rolloutOnce sync.Once

	// Alert evaluations are serialized; pending duration state is bounded by
	// currently observable enabled rule/machine pairs and resets on restart.
	alertEvalMu  sync.Mutex
	alertPending map[string]alertPendingCondition

	// Ingestion barrier. Every persisted agent frame runs under the read
	// side (see ingestFrame); machine deletion takes the write side around
	// registry removal and row deletion. Lock order is agentAuthMu, then
	// ingestMu, then agentsMu; never the reverse.
	ingestMu sync.RWMutex

	// Latest AI Sessions snapshot per connected machine, and the cached
	// feature switch with its revision (see ai_sessions.go).
	aiSessions    *aiSessionStore
	aiSessionsCfg aiSessionsConfig

	// caCertCandidates, when non-nil, replaces the default filesystem search
	// path for the bootstrap CA certificate (see bootstrapCACertCandidates).
	// Production leaves it nil and uses the default host candidates; tests set
	// it so CA discovery is isolated from the machine's real Caddy roots
	// (~/.local/share/caddy, /var/lib/caddy, /root) and a public test hub is
	// never mis-classified private because the developer or CI runner happens
	// to have a local Caddy CA on disk. See caCertCandidatePaths.
	caCertCandidates func() []string

	// systemTrustProbe, when non-nil, replaces the live TLS handshake that
	// checks PUBLIC_URL's certificate against the hub's OS trust store during
	// bootstrap-CA classification (the auto-discovered-CA fallback). Production
	// leaves it nil and uses verifyEndpointSystemTrust; tests inject it so the
	// fallback is exercised without a real endpoint. Per-server, like
	// caCertCandidates, so parallel tests never race a package global.
	systemTrustProbe joinSystemTrustVerifier

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
		db:          db,
		agents:      make(map[string]*ConnectedAgent),
		aiSessions:  newAISessionStore(),
		rolloutStop: make(chan struct{}),
		// The rollout controller is NOT built here. newServer runs before
		// initDB, so its tables may not exist yet — see initRollout.
	}
}

// initRollout builds the rollout controller. Call it AFTER migrations.
//
// This cannot live in newServer. main() constructs the server and only then
// calls initDB, so on every fresh install and every upgrade the controller
// would query its own tables before the migration that creates them. The
// resulting error is retained by design, so a hub would withhold every agent
// update forever — on a schema that the very next statement fixes. Unit
// fixtures that migrate before constructing a server hide this completely.
//
// Until this succeeds s.rollout stays nil, and announceVersionToAgent
// withholds rather than falling through to the unrestricted legacy path.
func (s *Server) initRollout() error {
	controller, err := newRolloutController(s.db, time.Now)
	if err != nil {
		s.rolloutErr = fmt.Errorf("agent rollout controller unavailable: %w", err)
		log.Printf("WARNING: %v; agent updates will be withheld", s.rolloutErr)
		return s.rolloutErr
	}
	controller.attachRegistry(s.currentAgentConnection)
	s.rolloutErr = nil
	s.rollout = controller
	return nil
}

// currentAgentConnection is the registry's answer to who owns a machine now.
// Takes agentsMu only; the rollout controller never holds its own lock across
// this call, so the two can never deadlock against each other.
func (s *Server) currentAgentConnection(machineID string) *ConnectedAgent {
	s.agentsMu.RLock()
	defer s.agentsMu.RUnlock()
	return s.agents[machineID]
}

// startRolloutScheduler runs exactly one advancement loop for this server.
//
// The loop owns STAGE ADVANCEMENT and attempt expiry. Reservation itself still
// happens on the trigger's own goroutine, which is safe because reserve() is a
// single serialized transaction and claimSend gives one attempt one owner —
// but it is not yet the "triggers merely wake the scheduler" shape, and this
// comment does not claim otherwise.
func (s *Server) startRolloutScheduler() {
	s.rolloutOnce.Do(func() {
		if s.rollout == nil {
			return
		}
		s.goTracked(func() {
			ticker := time.NewTicker(rolloutTickInterval)
			defer ticker.Stop()
			for {
				select {
				case <-s.rolloutStop:
					return
				case <-ticker.C:
					for _, platform := range supportedAgentPlatforms {
						if err := s.rollout.tick(platform.String()); err != nil {
							log.Printf("rollout: tick for %s: %v", platform, err)
						}
					}
				}
			}
		})
	})
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

	// Stop the scheduler BEFORE waiting. A polling loop that outlived the wait
	// would keep touching the database a test is about to close.
	s.rolloutOnce.Do(func() {}) // ensure a later start cannot begin after this
	select {
	case <-s.rolloutStop:
	default:
		close(s.rolloutStop)
	}

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
