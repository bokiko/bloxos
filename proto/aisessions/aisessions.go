// Package aisessions is the wire contract for the read-only "AI Sessions"
// feature: an agent reports which supported AI coding tools are running on
// its machine, and the hub keeps only the latest snapshot per machine.
//
// Privacy is the design constraint, not an afterthought. A session carries
// ONLY the fields defined here. It never carries prompts, responses,
// transcript contents, tool commands or output, environment variables, the
// full command line, the full working directory, or the OS username. The
// agent derives the fields below from process metadata and discards the
// raw material; the hub re-applies Sanitize on every inbound message so a
// misbehaving or older agent cannot smuggle anything else through.
//
// Both sides import this package so the whitelist, caps and normalization
// rules cannot drift apart.
package aisessions

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// MessageType is the agent→hub WebSocket frame type.
const MessageType = "ai_sessions"

// SchemaVersion is the version of the session payload shape. A hub accepts
// any version it can decode; unknown fields are dropped by Sanitize.
const SchemaVersion = 1

// Supported tools. The classifier on the agent and Sanitize on the hub both
// reject anything outside this set.
const (
	ToolClaude = "claude"
	ToolCodex  = "codex"
	ToolKimi   = "kimi"
)

// Confidence describes how much precision an attribute's value carries.
//
//   - exact: the value was observed verbatim (e.g. a --model flag with a full
//     model id, a directory basename that exists).
//   - inferred: derived from indirect evidence (a model alias, a CPU-time
//     heuristic). Do not present it as fact.
//   - unknown: no evidence. Value is empty.
const (
	ConfidenceExact    = "exact"
	ConfidenceInferred = "inferred"
	ConfidenceUnknown  = "unknown"
)

// Source names the evidence an attribute was derived from.
const (
	SourceNone     = "none"
	SourceArgvFlag = "argv_flag" // model: a --model / -m flag on the command line
	SourceCwd      = "cwd"       // project: basename of the working directory
	SourceCPUTime  = "cpu_time"  // activity: CPU time consumed between two scans
)

// Activity states. "active" and "idle" are only ever inferred.
const (
	ActivityActive  = "active"
	ActivityIdle    = "idle"
	ActivityUnknown = "unknown"
)

// Caps applied by Sanitize. They bound both wire size and what a hostile
// agent can push into hub memory.
const (
	MaxSessionsPerMachine = 64
	MaxValueRunes         = 64
	maxIDHexLen           = 32
)

// Attr is a value together with an explicit statement of where it came from
// and how precise it is. Consumers must render Confidence, never just Value.
type Attr struct {
	Value      string `json:"value"`
	Source     string `json:"source"`
	Confidence string `json:"confidence"`
}

// Session is the normalized metadata for one running AI tool process.
type Session struct {
	// ID is opaque and stable for the lifetime of the process. It is not
	// the PID.
	ID string `json:"id"`
	// Tool is one of ToolClaude, ToolCodex, ToolKimi.
	Tool string `json:"tool"`
	// StartedAt is the process start time in RFC 3339 UTC, or empty.
	StartedAt string `json:"started_at,omitempty"`
	// Project is the basename of the working directory when one is safely
	// derivable. Never a path, never a home-directory name.
	Project Attr `json:"project"`
	// Model is the model the tool was explicitly asked to use, if that is
	// observable from a command-line flag. Never read from the environment.
	Model Attr `json:"model"`
	// Activity is a coarse busy/idle inference from CPU time.
	Activity Attr `json:"activity"`
}

// Message is the agent→hub frame. MachineID is advisory only: the hub binds
// the snapshot to the machine the socket authenticated as and ignores this
// field for keying.
type Message struct {
	Type      string    `json:"type"`
	MachineID string    `json:"machine_id"`
	Schema    int       `json:"schema"`
	Sessions  []Session `json:"sessions"`
}

// Unknown returns the attribute that states "no evidence".
func Unknown() Attr {
	return Attr{Source: SourceNone, Confidence: ConfidenceUnknown}
}

// ValidTool reports whether tool is in the whitelist.
func ValidTool(tool string) bool {
	switch tool {
	case ToolClaude, ToolCodex, ToolKimi:
		return true
	}
	return false
}

// SessionID derives the opaque session id from process identity. The PID
// itself is not sent; a fresh process reusing a PID gets a different id
// because the start time differs.
func SessionID(pid int32, startUnixMillis int64) string {
	sum := sha256.Sum256([]byte("bloxos-ai-session:" + strconv.FormatInt(int64(pid), 10) + ":" + strconv.FormatInt(startUnixMillis, 10)))
	return hex.EncodeToString(sum[:])[:16]
}

// ProjectFromDir derives the project attribute from a working directory.
// It returns ok=false, and callers must emit Unknown(), when the directory
// is not safely a project: the filesystem root, a top-level directory, a
// home directory (/home/<x>, /Users/<x>, <drive>:\Users\<x>), or a basename
// equal to the OS username. Both '/' and '\' are treated as separators so
// the rule is identical on Linux and Windows.
func ProjectFromDir(dir, username string) (Attr, bool) {
	parts := splitPath(dir)
	if len(parts) == 0 {
		return Unknown(), false
	}
	// A Windows drive ("C:") is a root, not a directory component.
	if len(parts[0]) == 2 && parts[0][1] == ':' && isASCIILetter(parts[0][0]) {
		parts = parts[1:]
	}
	switch len(parts) {
	case 0, 1:
		return Unknown(), false
	case 2:
		if strings.EqualFold(parts[0], "home") || strings.EqualFold(parts[0], "users") {
			return Unknown(), false
		}
	}
	base := parts[len(parts)-1]
	if username != "" && strings.EqualFold(base, normalizeUsername(username)) {
		return Unknown(), false
	}
	name, ok := cleanProjectName(base)
	if !ok {
		return Unknown(), false
	}
	return Attr{Value: name, Source: SourceCwd, Confidence: ConfidenceExact}, true
}

// normalizeUsername extracts the account name from domain-qualified usernames
// (DOMAIN\alice, COMPUTER\alice, or user@DOMAIN) for privacy checks.
func normalizeUsername(username string) string {
	// Handle DOMAIN\user or COMPUTER\user (Windows)
	if idx := strings.LastIndexAny(username, "\\/"); idx >= 0 {
		username = username[idx+1:]
	}
	// Handle user@DOMAIN
	if idx := strings.IndexByte(username, '@'); idx >= 0 {
		username = username[:idx]
	}
	return username
}

// ModelFromFlag derives the model attribute from the value of an explicit
// --model / -m flag. A full model id (one containing a digit, e.g.
// "claude-opus-5" or "gpt-5-codex") is exact; a bare alias such as "opus"
// is inferred. Values that do not look like a model id yield Unknown().
func ModelFromFlag(raw string) Attr {
	value, ok := cleanModelValue(raw)
	if !ok {
		return Unknown()
	}
	conf := ConfidenceInferred
	if strings.ContainsFunc(value, unicode.IsDigit) {
		conf = ConfidenceExact
	}
	return Attr{Value: value, Source: SourceArgvFlag, Confidence: conf}
}

// Sanitize returns a copy of msg reduced to the contract: whitelisted tools,
// capped counts and lengths, enumerated source/confidence values, and
// values that cannot be paths, command lines or free text. It is the last
// step on the agent before sending and the first step on the hub after
// decoding. Sessions that fail validation are dropped, not repaired.
func Sanitize(msg Message) Message {
	out := Message{
		Type:      MessageType,
		MachineID: msg.MachineID,
		Schema:    msg.Schema,
		Sessions:  []Session{},
	}
	if out.Schema <= 0 {
		out.Schema = SchemaVersion
	}
	for _, in := range msg.Sessions {
		if len(out.Sessions) >= MaxSessionsPerMachine {
			break
		}
		s, ok := sanitizeSession(in)
		if !ok {
			continue
		}
		out.Sessions = append(out.Sessions, s)
	}
	return out
}

func sanitizeSession(in Session) (Session, bool) {
	if !ValidTool(in.Tool) {
		return Session{}, false
	}
	id := strings.ToLower(strings.TrimSpace(in.ID))
	if id == "" || len(id) > maxIDHexLen || !isHex(id) {
		return Session{}, false
	}
	out := Session{ID: id, Tool: in.Tool}
	if t, err := time.Parse(time.RFC3339, in.StartedAt); err == nil {
		out.StartedAt = t.UTC().Format(time.RFC3339)
	}
	out.Project = sanitizeAttr(in.Project, SourceCwd, cleanProjectName)
	out.Model = sanitizeAttr(in.Model, SourceArgvFlag, cleanModelValue)
	out.Activity = sanitizeActivity(in.Activity)
	return out, true
}

// sanitizeAttr keeps an attribute only when its source is the single
// source allowed for that attribute, its confidence is a known level, and
// its value passes clean. Anything else collapses to Unknown().
func sanitizeAttr(a Attr, allowedSource string, clean func(string) (string, bool)) Attr {
	if a.Source != allowedSource {
		return Unknown()
	}
	if a.Confidence != ConfidenceExact && a.Confidence != ConfidenceInferred {
		return Unknown()
	}
	value, ok := clean(a.Value)
	if !ok {
		return Unknown()
	}
	return Attr{Value: value, Source: allowedSource, Confidence: a.Confidence}
}

// sanitizeActivity only ever admits inferred active/idle from CPU time —
// the sole activity evidence this checkpoint collects. Nothing may claim an
// exact activity state.
func sanitizeActivity(a Attr) Attr {
	if a.Source != SourceCPUTime || a.Confidence != ConfidenceInferred {
		return Unknown()
	}
	switch a.Value {
	case ActivityActive, ActivityIdle:
		return Attr{Value: a.Value, Source: SourceCPUTime, Confidence: ConfidenceInferred}
	}
	return Unknown()
}

// cleanProjectName accepts a single path component: printable, no
// separators, not "." or "..", at most MaxValueRunes runes.
func cleanProjectName(raw string) (string, bool) {
	name := strings.TrimSpace(raw)
	if name == "" || name == "." || name == ".." {
		return "", false
	}
	if strings.ContainsAny(name, "/\\") {
		return "", false
	}
	for _, r := range name {
		if !unicode.IsPrint(r) {
			return "", false
		}
	}
	return truncateRunes(name, MaxValueRunes), true
}

// cleanModelValue accepts identifiers only: ASCII letters, digits and
// ".", "_", ":", "-". No separators, spaces or quotes, so a flag value that
// is really a path or free text is rejected rather than forwarded.
func cleanModelValue(raw string) (string, bool) {
	v := strings.TrimSpace(raw)
	if v == "" || len(v) > MaxValueRunes {
		return "", false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case isASCIILetter(c), c >= '0' && c <= '9', c == '.', c == '_', c == ':', c == '-':
		default:
			return "", false
		}
	}
	return v, true
}

func splitPath(p string) []string {
	var parts []string
	for _, seg := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg != "" && seg != "." {
			parts = append(parts, seg)
		}
	}
	return parts
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func isASCIILetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
