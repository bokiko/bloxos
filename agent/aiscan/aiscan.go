// Package aiscan classifies running processes as supported AI coding tools
// (Claude Code, Codex, Kimi) and reduces them to aisessions.Session values.
//
// It is pure: it takes a process table and returns metadata, so it can be
// tested on every platform. Reading the live process table is the caller's
// job (see agent/ai_sessions.go).
//
// Privacy contract: the command line is used to classify the process and
// to find an explicit --model flag, then discarded. The working directory is
// reduced to a basename under aisessions.ProjectFromDir or dropped. Nothing
// else about a process is retained.
package aiscan

import (
	"strings"
	"sync"
	"time"

	"github.com/bokiko/bloxos/proto/aisessions"
)

// Gate decides whether this agent may scan and report at all. Two controls
// combine, and both must allow:
//
//   - The hub's runtime signal (an "ai_sessions_config" frame sent after
//     registration and whenever an admin flips the switch). Until the hub
//     has spoken on the current connection nothing is scanned — so an agent
//     talking to a hub that predates the feature, or one whose admin turned
//     it off, never reads a process table for it.
//   - The machine-local BLOXOS_AI_SESSIONS opt-out, a hard override that a
//     hub enable cannot defeat.
type Gate struct {
	mu      sync.Mutex
	state   gateState
	applied bool   // a hub decision has been applied on this connection
	rev     uint64 // revision of that decision
}

type gateState int

const (
	gateUnknown gateState = iota
	gateEnabled
	gateDisabled
)

// Reset forgets the hub's decision and revision; call it when a
// connection starts.
func (g *Gate) Reset() {
	g.mu.Lock()
	g.state = gateUnknown
	g.applied = false
	g.rev = 0
	g.mu.Unlock()
}

// Apply records the hub's decision and reports whether it newly enabled
// scanning, so the caller can report promptly instead of waiting a tick.
// rev is the hub's monotonic config revision: the hub's registration send
// and its toggle broadcast travel on independent goroutines, so a stale
// frame can arrive after a newer one. Once a decision has been applied on
// this connection, only a strictly greater revision is accepted: a lower
// one is a delayed stale frame, and an equal one with a different value
// could only be a duplicate or a forgery, so neither may flip the gate.
// This is what keeps a delayed "enabled" from undoing an admin's
// "disabled".
func (g *Gate) Apply(enabled bool, rev uint64) (newlyEnabled bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.applied && rev <= g.rev {
		return false
	}
	g.applied = true
	g.rev = rev
	prev := g.state
	if enabled {
		g.state = gateEnabled
	} else {
		g.state = gateDisabled
	}
	return enabled && prev != gateEnabled
}

// Allowed reports whether a scan may run now. envValue is the agent's own
// BLOXOS_AI_SESSIONS setting.
func (g *Gate) Allowed(envValue string) bool {
	if !EnabledByEnv(envValue) {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state == gateEnabled
}

// EnabledByEnv interprets the BLOXOS_AI_SESSIONS value. Monitoring is on
// by default; "0", "false", "off" or "no" turn this machine's reporting off.
func EnabledByEnv(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "false", "off", "no":
		return false
	}
	return true
}

// Process is the subset of process metadata the classifier and session
// builder consume. Argv and Cwd are inputs only; they never appear in the
// output.
type Process struct {
	PID        int32
	PPID       int32
	Name       string   // executable name as the OS reports it (comm / image name)
	Argv       []string // full command line; never leaves Build
	Cwd        string   // full working directory; reduced to a basename or dropped
	Username   string   // used only to withhold a project name equal to it
	StartMS    int64    // process start, unix milliseconds
	CPUSeconds float64  // user+system CPU time consumed so far
}

// toolTokens maps a normalized executable/script token to a tool.
// Everything not listed here is not an AI session, full stop.
var toolTokens = map[string]string{
	"claude":      aisessions.ToolClaude,
	"claude-code": aisessions.ToolClaude, // npm package dir @anthropic-ai/claude-code/cli.js
	"codex":       aisessions.ToolCodex,  // native binary or @openai/codex/bin/codex.js
	"kimi":        aisessions.ToolKimi,
	"kimi-cli":    aisessions.ToolKimi, // pip/uv distribution name
	"kimi_cli":    aisessions.ToolKimi, // python -m kimi_cli
}

// entryTokens are script basenames that say nothing about the tool; the
// parent directory is consulted instead (…/claude-code/cli.js).
var entryTokens = map[string]bool{"cli": true, "index": true, "main": true, "__main__": true, "bin": true}

// ActivityCPURate is the CPU utilisation (CPU-seconds per wall second)
// above which a session is inferred "active". Below it, "idle". Both are
// inferences and are reported as such.
const ActivityCPURate = 0.01

// Candidate reports whether a process with this executable name could be
// an AI tool at all — the name itself or an interpreter that may be running
// one. Callers use it to avoid reading the command line of every process.
func Candidate(name string) bool {
	if _, known := toolTokens[normalizeToken(name)]; known {
		return true
	}
	return isInterpreter(name)
}

// Classify decides whether a process is a supported AI tool. It looks
// at the executable name, argv[0], and — when argv[0] is an interpreter —
// the script or module being run. Matching is exact on normalized tokens;
// substrings never match, so "claude-desktop" or "codex-server" do not.
func Classify(name string, argv []string) (string, bool) {
	tool, _, ok := classify(name, argv)
	return tool, ok
}

// classify also reports the index in argv of the tool's entry point (the
// binary, launcher script or -m module). Only arguments AFTER that index
// belong to the tool; anything before it belongs to the interpreter or
// wrapper and must not be read as a tool flag. entry is -1 when the tool
// was recognized from the process name but no argv element identifies it.
func classify(name string, argv []string) (tool string, entry int, ok bool) {
	byName, nameOK := toolTokens[normalizeToken(name)]
	if len(argv) == 0 {
		if nameOK {
			return byName, -1, true
		}
		return "", -1, false
	}
	if tool, ok := toolFromPath(argv[0]); ok {
		return tool, 0, true
	}
	if isInterpreter(argv[0]) {
		if idx := interpreterTargetIndex(argv); idx > 0 {
			if tool, ok := toolFromPath(argv[idx]); ok {
				return tool, idx, true
			}
		}
	}
	if !nameOK {
		return "", -1, false
	}
	// Recognized by process name (e.g. a shebang wrapper whose comm is the
	// script name while argv[0] is the shell). Find the launcher in argv so
	// tool flags are parsed only after it.
	for i := 1; i < len(argv); i++ {
		if tool, ok := toolFromPath(argv[i]); ok && tool == byName {
			return byName, i, true
		}
	}
	return byName, -1, true
}

// toolFromPath classifies a path to an executable, script or module.
func toolFromPath(p string) (string, bool) {
	parts := splitPath(p)
	if len(parts) == 0 {
		return "", false
	}
	tok := normalizeToken(parts[len(parts)-1])
	if tool, ok := toolTokens[tok]; ok {
		return tool, true
	}
	if entryTokens[tok] && len(parts) >= 2 {
		if tool, ok := toolTokens[normalizeToken(parts[len(parts)-2])]; ok {
			return tool, true
		}
	}
	return "", false
}

// interpreterTargetIndex returns the argv index of the script path or
// module name an interpreter was asked to run: the first non-flag argument,
// or the operand of -m / "--". 0 means none was found.
func interpreterTargetIndex(argv []string) int {
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		if (a == "-m" || a == "--") && i+1 < len(argv) {
			return i + 1
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		return i
	}
	return 0
}

func isInterpreter(argv0 string) bool {
	tok := normalizeToken(basename(argv0))
	switch tok {
	case "node", "nodejs", "bun", "deno":
		return true
	}
	return strings.HasPrefix(tok, "python")
}

// normalizeToken lower-cases and strips platform/launcher suffixes so
// "Claude.exe", "codex.js" and "kimi" compare equal to their tool token.
func normalizeToken(s string) string {
	tok := strings.ToLower(strings.TrimSpace(s))
	for _, suffix := range []string{".exe", ".cmd", ".bat", ".mjs", ".cjs", ".js", ".py"} {
		if strings.HasSuffix(tok, suffix) {
			tok = strings.TrimSuffix(tok, suffix)
			break
		}
	}
	return tok
}

func splitPath(p string) []string {
	return strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' })
}

func basename(p string) string {
	parts := splitPath(p)
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// ModelFromArgv finds an explicit model flag among the tool's own
// arguments — those after entry, the index of the tool's entry point in
// argv. Interpreter and wrapper flags before it are never consulted, so
// "python -m kimi_cli" does not yield a model of "kimi_cli". Only the
// flag's operand is returned; the rest of argv is never surfaced.
//
// Rules are tool-aware: "--model X" and "--model=X" are accepted for every
// tool; the short "-m X" only for codex, which documents it.
func ModelFromArgv(tool string, argv []string, entry int) aisessions.Attr {
	if entry < 0 || entry >= len(argv) {
		return aisessions.Unknown()
	}
	shortOK := tool == aisessions.ToolCodex
	for i := entry + 1; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--model" || (shortOK && a == "-m"):
			if i+1 < len(argv) {
				return aisessions.ModelFromFlag(argv[i+1])
			}
			return aisessions.Unknown()
		case strings.HasPrefix(a, "--model="):
			return aisessions.ModelFromFlag(strings.TrimPrefix(a, "--model="))
		}
	}
	return aisessions.Unknown()
}

// cpuSample is the per-process state carried between scans so activity
// can be inferred from CPU time consumed since the previous scan.
type cpuSample struct {
	StartMS    int64
	CPUSeconds float64
	At         time.Time
}

// Scanner holds the inter-scan state. One instance lives for the
// agent process; the map is pruned to the processes seen on each scan.
type Scanner struct {
	mu   sync.Mutex
	prev map[int32]cpuSample
}

func NewScanner() *Scanner {
	return &Scanner{prev: make(map[int32]cpuSample)}
}

// match is a classified process awaiting tree collapse.
type match struct {
	proc  Process
	tool  string
	entry int
}

// Build turns a process table into the sessions to report. It is pure
// apart from the scanner's inter-scan CPU samples. Raw argv and cwd are
// consumed here and do not appear in the result.
func (sc *Scanner) Build(procs []Process, now time.Time) []aisessions.Session {
	// Pass 1: classify, and index parents for the tree walk below.
	matched := make(map[int32]match)
	parents := make(map[int32]int32, len(procs))
	order := make([]int32, 0)
	for _, p := range procs {
		if _, dup := parents[p.PID]; dup {
			continue
		}
		parents[p.PID] = p.PPID
		tool, entry, ok := classify(p.Name, p.Argv)
		if !ok {
			continue
		}
		matched[p.PID] = match{proc: p, tool: tool, entry: entry}
		order = append(order, p.PID)
	}

	sc.mu.Lock()
	defer sc.mu.Unlock()
	next := make(map[int32]cpuSample, len(matched))

	// Pass 2: collapse process trees. A wrapper script whose name matches
	// and the real binary it exec'd, or a tool re-launching itself through
	// a shell, is one session: report the topmost matched ancestor of the
	// same tool only.
	out := make([]aisessions.Session, 0, len(order))
	for _, pid := range order {
		m := matched[pid]
		if hasMatchedAncestor(m, matched, parents) {
			continue
		}
		p := m.proc
		s := aisessions.Session{
			ID:       aisessions.SessionID(p.PID, p.StartMS),
			Tool:     m.tool,
			Project:  aisessions.Unknown(),
			Model:    ModelFromArgv(m.tool, p.Argv, m.entry),
			Activity: aisessions.Unknown(),
		}
		if p.StartMS > 0 {
			s.StartedAt = time.UnixMilli(p.StartMS).UTC().Format(time.RFC3339)
		}
		if attr, ok := aisessions.ProjectFromDir(p.Cwd, p.Username); ok {
			s.Project = attr
		}
		if prev, ok := sc.prev[p.PID]; ok && prev.StartMS == p.StartMS && p.CPUSeconds >= 0 && prev.CPUSeconds >= 0 {
			if elapsed := now.Sub(prev.At).Seconds(); elapsed > 0 {
				rate := (p.CPUSeconds - prev.CPUSeconds) / elapsed
				state := aisessions.ActivityIdle
				if rate >= ActivityCPURate {
					state = aisessions.ActivityActive
				}
				s.Activity = aisessions.Attr{Value: state, Source: aisessions.SourceCPUTime, Confidence: aisessions.ConfidenceInferred}
			}
		}
		next[p.PID] = cpuSample{StartMS: p.StartMS, CPUSeconds: p.CPUSeconds, At: now}
		out = append(out, s)
	}
	sc.prev = next
	return out
}

// hasMatchedAncestor walks the parent chain through the whole table
// (bounded, so a cyclic or enormous fake table cannot spin) looking for a
// matched process of the same tool.
func hasMatchedAncestor(m match, matched map[int32]match, parents map[int32]int32) bool {
	ppid := m.proc.PPID
	for hop := 0; hop < 8 && ppid > 1; hop++ {
		if parent, ok := matched[ppid]; ok && parent.tool == m.tool {
			return true
		}
		next, ok := parents[ppid]
		if !ok {
			return false
		}
		ppid = next
	}
	return false
}
