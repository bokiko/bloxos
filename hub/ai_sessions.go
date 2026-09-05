package main

// AI Sessions (checkpoint 1) — hub side.
//
// The hub keeps ONLY the latest snapshot of AI tool sessions per machine, in
// memory, keyed by the machine the reporting socket authenticated as. There
// is no history table: when a socket closes, or a snapshot goes stale, the
// sessions disappear. Reads are served to anyone with fleet.read; the
// feature-wide switch is persisted in hub_settings and defaults to enabled,
// so a fresh install needs no configuration. Turning it off drops every
// snapshot immediately and makes the hub discard further reports.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/bokiko/bloxos/proto/aisessions"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

// aiSessionsStaleAfter bounds how long a snapshot is served without a fresh
// report. Agents report every 30s alongside metrics, so three missed
// reports means the machine's socket is gone or wedged.
const aiSessionsStaleAfter = 90 * time.Second

const aiSessionsEnabledSettingKey = "ai_sessions.enabled"

// aiSessionSnapshot is one machine's latest report.
type aiSessionSnapshot struct {
	Sessions   []aisessions.Session
	ReceivedAt time.Time
}

// aiSessionStore is the in-memory registry of snapshots. It owns its own
// lock rather than piggybacking on agentsMu so ingest never contends with
// registration, and its clock is injectable for stale-expiry tests.
type aiSessionStore struct {
	mu        sync.RWMutex
	byMachine map[string]aiSessionSnapshot
	now       func() time.Time
}

func newAISessionStore() *aiSessionStore {
	return &aiSessionStore{byMachine: make(map[string]aiSessionSnapshot), now: time.Now}
}

func (st *aiSessionStore) put(machineID string, sessions []aisessions.Session) {
	st.mu.Lock()
	st.byMachine[machineID] = aiSessionSnapshot{Sessions: sessions, ReceivedAt: st.now()}
	st.mu.Unlock()
}

func (st *aiSessionStore) remove(machineID string) {
	st.mu.Lock()
	delete(st.byMachine, machineID)
	st.mu.Unlock()
}

func (st *aiSessionStore) clear() {
	st.mu.Lock()
	st.byMachine = make(map[string]aiSessionSnapshot)
	st.mu.Unlock()
}

// live returns the fresh snapshots and evicts stale ones. Eviction happens
// here, lazily, so no sweeper goroutine is needed: a stale entry costs a
// few hundred bytes until the next read.
func (st *aiSessionStore) live() map[string]aiSessionSnapshot {
	cutoff := st.now().Add(-aiSessionsStaleAfter)
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make(map[string]aiSessionSnapshot, len(st.byMachine))
	for id, snap := range st.byMachine {
		if snap.ReceivedAt.Before(cutoff) {
			delete(st.byMachine, id)
			continue
		}
		out[id] = snap
	}
	return out
}

// --- feature switch ---

// aiSessionsConfig is the hub's single in-memory source of truth for the
// switch: the enabled flag and a monotonic revision that advances on every
// change. Every config frame is built from one atomic snapshot of this
// pair, so two sends racing on the network (a registration send and a
// toggle broadcast) can be ordered by the agent: a lower revision is
// stale and is ignored. The revision is per hub process; a restart is
// fine because agents reset their gate on every connection.
type aiSessionsConfig struct {
	mu      sync.Mutex
	loaded  bool
	enabled bool
	rev     uint64
}

// aiSessionsConfigSnapshot returns the current (enabled, rev) pair,
// loading the persisted switch on first use. A missing row is "enabled":
// the default requires no setup. A database error fails closed (disabled)
// and is logged, so an unreadable settings table never turns monitoring
// on by accident.
func (s *Server) aiSessionsConfigSnapshot() (enabled bool, rev uint64) {
	c := &s.aiSessionsCfg
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loaded {
		c.enabled = s.loadAISessionsEnabled()
		c.rev = 1
		c.loaded = true
	}
	return c.enabled, c.rev
}

func (s *Server) loadAISessionsEnabled() bool {
	var value string
	err := s.db.QueryRow(`SELECT value FROM hub_settings WHERE key = ?`, aiSessionsEnabledSettingKey).Scan(&value)
	switch {
	case err == sql.ErrNoRows:
		return true
	case err != nil:
		log.Printf("ai sessions: read setting: %v", err)
		return false
	}
	return value != "false"
}

// aiSessionsEnabled is the cached switch state.
func (s *Server) aiSessionsEnabled() bool {
	enabled, _ := s.aiSessionsConfigSnapshot()
	return enabled
}

// aiSessionsConfigType is the hub→agent frame that carries the switch.
// Agents built before this feature route it through their generic command
// handler, which ignores any frame without a command id, so sending it to
// every connected agent is safe.
const aiSessionsConfigType = "ai_sessions_config"

func aiSessionsConfigFrame(enabled bool, rev uint64) []byte {
	data, _ := json.Marshal(map[string]any{"type": aiSessionsConfigType, "enabled": enabled, "rev": rev})
	return data
}

// sendAISessionsConfig tells one agent the current switch state. Called
// from registerAgentConnection (via goTracked) so every authenticated
// socket learns the state before its first scheduled scan; an agent that
// never hears it never scans. The frame carries the revision captured
// with the state, so if this write lands after a newer broadcast the
// agent discards it.
func (s *Server) sendAISessionsConfig(machineID string, agent *ConnectedAgent) {
	if agent.Conn == nil {
		return
	}
	enabled, rev := s.aiSessionsConfigSnapshot()
	if err := agent.writeLocked(websocket.TextMessage, aiSessionsConfigFrame(enabled, rev)); err != nil {
		log.Printf("ai sessions: config to %s: %v", machineID, err)
	}
}

// broadcastAISessionsConfig pushes a changed switch state to every
// connected agent. Each write runs on its own tracked goroutine so one
// slow socket cannot stall the admin request or the other agents.
func (s *Server) broadcastAISessionsConfig(enabled bool, rev uint64) {
	frame := aiSessionsConfigFrame(enabled, rev)
	s.agentsMu.RLock()
	targets := make(map[string]*ConnectedAgent, len(s.agents))
	for id, a := range s.agents {
		targets[id] = a
	}
	s.agentsMu.RUnlock()
	for id, a := range targets {
		if a.Conn == nil {
			continue
		}
		s.goTracked(func() {
			if err := a.writeLocked(websocket.TextMessage, frame); err != nil {
				log.Printf("ai sessions: config broadcast to %s: %v", id, err)
			}
		})
	}
}

// setAISessionsEnabled persists the switch and advances the cached state
// and revision as one step under the config lock, so concurrent toggles
// are serialized and the persisted order always matches revision order.
// The broadcast uses exactly the pair captured here.
func (s *Server) setAISessionsEnabled(enabled bool) error {
	value := "true"
	if !enabled {
		value = "false"
	}
	c := &s.aiSessionsCfg
	c.mu.Lock()
	_, err := s.db.Exec(`
		INSERT INTO hub_settings (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
		aiSessionsEnabledSettingKey, value)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	if !c.loaded {
		c.rev = 1
		c.loaded = true
	}
	c.enabled = enabled
	c.rev++
	rev := c.rev
	c.mu.Unlock()
	if !enabled {
		s.aiSessions.clear()
	}
	s.broadcastAISessionsConfig(enabled, rev)
	return nil
}

// --- ingest (agent WebSocket) ---

// ingestAISessions handles one ai_sessions frame. machineID is the identity
// the socket authenticated as — the frame's own machine_id is ignored for
// keying, so a compromised agent cannot plant sessions on another machine.
// Frames from a socket that is not the registered connection for that
// machine (an enrollment that has not committed, or a displaced socket) are
// dropped. Every session is re-sanitized: the hub trusts the contract, not
// the agent.
func (s *Server) ingestAISessions(machineID string, agent *ConnectedAgent, raw []byte) {
	if machineID == "" || !s.isRegisteredConnection(machineID, agent) {
		return
	}
	if !s.aiSessionsEnabled() {
		s.aiSessions.remove(machineID)
		return
	}
	var msg aisessions.Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		log.Printf("invalid ai_sessions JSON from %s: %v", machineID, err)
		return
	}
	clean := aisessions.Sanitize(msg)
	s.aiSessions.put(machineID, clean.Sessions)
}

// isRegisteredConnection reports whether agent is the live registry entry
// for machineID.
func (s *Server) isRegisteredConnection(machineID string, agent *ConnectedAgent) bool {
	s.agentsMu.RLock()
	current, ok := s.agents[machineID]
	s.agentsMu.RUnlock()
	return ok && current == agent
}

// --- read API ---

type aiSessionsMachineView struct {
	MachineID  string               `json:"machine_id"`
	Hostname   string               `json:"hostname"`
	ReceivedAt string               `json:"received_at"`
	Sessions   []aisessions.Session `json:"sessions"`
}

type aiSessionsResponse struct {
	Enabled           bool                    `json:"enabled"`
	StaleAfterSeconds int                     `json:"stale_after_seconds"`
	Machines          []aiSessionsMachineView `json:"machines"`
}

// handleListAISessions returns the live snapshot for every reporting
// machine. Requires fleet.read.
func (s *Server) handleListAISessions(c echo.Context) error {
	resp := aiSessionsResponse{
		Enabled:           s.aiSessionsEnabled(),
		StaleAfterSeconds: int(aiSessionsStaleAfter / time.Second),
		Machines:          []aiSessionsMachineView{},
	}
	if !resp.Enabled {
		s.aiSessions.clear()
		return c.JSON(http.StatusOK, resp)
	}
	live := s.aiSessions.live()
	for id, snap := range live {
		view := aiSessionsMachineView{
			MachineID:  id,
			ReceivedAt: snap.ReceivedAt.UTC().Format(time.RFC3339),
			Sessions:   snap.Sessions,
		}
		if view.Sessions == nil {
			view.Sessions = []aisessions.Session{}
		}
		// Hostname is display sugar; a missing row (machine deleted while
		// its socket lingers) is not an error.
		_ = s.db.QueryRow(`SELECT hostname FROM machines WHERE id = ?`, id).Scan(&view.Hostname)
		resp.Machines = append(resp.Machines, view)
	}
	sort.Slice(resp.Machines, func(i, j int) bool {
		a, b := resp.Machines[i], resp.Machines[j]
		if a.Hostname != b.Hostname {
			return a.Hostname < b.Hostname
		}
		return a.MachineID < b.MachineID
	})
	return c.JSON(http.StatusOK, resp)
}

// handleUpdateAISessionsSettings flips the feature switch. Requires
// fleet.admin. Disabling clears every snapshot immediately.
func (s *Server) handleUpdateAISessionsSettings(c echo.Context) error {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.Bind(&body); err != nil || body.Enabled == nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "enabled (boolean) is required"})
	}
	if err := s.setAISessionsEnabled(*body.Enabled); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("save setting: %v", err)})
	}
	return c.JSON(http.StatusOK, map[string]bool{"enabled": *body.Enabled})
}
