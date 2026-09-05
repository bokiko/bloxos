package main

// AI Sessions (checkpoint 1): read-only discovery of running AI coding
// tools — Claude Code, Codex, Kimi — reported to the hub as metadata only.
//
// What leaves this machine is exactly aisessions.Session: an opaque id, the
// tool name, start time, project basename, an explicitly-flagged model, and
// a CPU-time activity inference. Classification and reduction live in the
// platform-neutral aiscan package; this file only reads the live process
// table (gopsutil, already a dependency) and sends the result. A monitored
// process's environment is never read. Nothing is written or signalled.
//
// Scanning is gated (aiscan.Gate): it runs only after the hub has said the
// feature is enabled on this connection, and never when the machine-local
// BLOXOS_AI_SESSIONS opt-out is set.

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"sync"
	"time"

	"github.com/bokiko/bloxos/agent/aiscan"
	"github.com/bokiko/bloxos/proto/aisessions"
	"github.com/gorilla/websocket"
	"github.com/shirou/gopsutil/v4/process"
)

// listAIProcesses reads the live process table via gopsutil (already a
// dependency of the agent). It reads only the executable name for every
// process and fetches the remaining metadata solely for candidates that
// could be an AI tool, keeping the scan cheap on busy hosts.
func listAIProcesses(ctx context.Context) ([]aiscan.Process, error) {
	procs, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]aiscan.Process, 0, 8)
	for _, p := range procs {
		name, err := p.NameWithContext(ctx)
		if err != nil {
			continue
		}
		if !aiscan.Candidate(name) {
			continue
		}
		argv, err := p.CmdlineSliceWithContext(ctx)
		if err != nil {
			argv = nil
		}
		if _, ok := aiscan.Classify(name, argv); !ok {
			continue
		}
		ap := aiscan.Process{PID: p.Pid, Name: name, Argv: argv}
		if ppid, err := p.PpidWithContext(ctx); err == nil {
			ap.PPID = ppid
		}
		if cwd, err := p.CwdWithContext(ctx); err == nil {
			ap.Cwd = cwd
		}
		if user, err := p.UsernameWithContext(ctx); err == nil {
			ap.Username = user
		}
		if start, err := p.CreateTimeWithContext(ctx); err == nil {
			ap.StartMS = start
		}
		if times, err := p.TimesWithContext(ctx); err == nil {
			ap.CPUSeconds = times.User + times.System
		} else {
			ap.CPUSeconds = -1
		}
		out = append(out, ap)
	}
	return out, nil
}

var (
	aiScanner = aiscan.NewScanner()
	// aiGate holds the hub's runtime enable/disable decision for the
	// current connection; runAgent resets it on every connect.
	aiGate aiscan.Gate
)

// aiSessionsConfigType is the hub→agent frame carrying the admin switch.
// Agents built before this feature route it through handleCommand, which
// ignores any frame without a command id — so it is safe to send to every
// agent.
const aiSessionsConfigType = "ai_sessions_config"

// handleAISessionsConfig applies an ai_sessions_config frame. When the hub
// newly enables the feature, one report is sent right away rather than
// waiting for the next 30s tick.
func handleAISessionsConfig(conn *websocket.Conn, mu *sync.Mutex, machineID string, msg []byte) {
	var cfg struct {
		Enabled bool   `json:"enabled"`
		Rev     uint64 `json:"rev"`
	}
	if err := json.Unmarshal(msg, &cfg); err != nil {
		log.Printf("invalid %s frame: %v", aiSessionsConfigType, err)
		return
	}
	if aiGate.Apply(cfg.Enabled, cfg.Rev) {
		go sendAISessions(conn, mu, machineID)
	}
}

// sendAISessions scans and reports. Failures are logged and never abort the
// connection: this is auxiliary metadata, and a broken process table must
// not take metrics or auto-update down with it.
func sendAISessions(conn *websocket.Conn, mu *sync.Mutex, machineID string) {
	if !aiGate.Allowed(os.Getenv("BLOXOS_AI_SESSIONS")) {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("ai sessions: panic recovered: %v (continuing)", r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	procs, err := listAIProcesses(ctx)
	if err != nil {
		log.Printf("ai sessions: process scan error: %v", err)
		return
	}
	msg := aisessions.Sanitize(aisessions.Message{
		Type:      aisessions.MessageType,
		MachineID: machineID,
		Schema:    aisessions.SchemaVersion,
		Sessions:  aiScanner.Build(procs, time.Now()),
	})
	if err := writeJSON(conn, mu, msg); err != nil {
		log.Printf("ai sessions: write error: %v", err)
	}
}
