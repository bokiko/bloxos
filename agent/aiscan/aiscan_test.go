package aiscan

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bokiko/bloxos/proto/aisessions"
)

func TestClassifyAIToolPositives(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want string
	}{
		// Native binaries and symlinked launchers.
		{"claude", []string{"claude"}, aisessions.ToolClaude},
		{"claude", []string{"/home/u/.local/bin/claude", "--resume"}, aisessions.ToolClaude},
		{"Claude.exe", []string{`C:\Users\u\AppData\Local\Programs\claude\Claude.exe`}, aisessions.ToolClaude},
		{"codex", []string{"codex", "-m", "gpt-5-codex"}, aisessions.ToolCodex},
		{"codex.exe", nil, aisessions.ToolCodex},
		{"kimi", []string{"/home/u/.local/bin/kimi"}, aisessions.ToolKimi},
		// Kimi Code CLI 1.50 (uvx --from kimi-cli kimi): the python3.12 child
		// renames comm and its only argv element to exactly "Kimi Code".
		{"Kimi Code", []string{"Kimi Code"}, aisessions.ToolKimi},
		{"Kimi Code", nil, aisessions.ToolKimi},
		{"KIMI CODE", []string{"kimi code"}, aisessions.ToolKimi},
		{"Kimi Code", []string{"Kimi Code", "--model", "kimi-k2"}, aisessions.ToolKimi},
		// Name is the interpreter; the script decides.
		{"node", []string{"node", "/usr/local/lib/node_modules/@anthropic-ai/claude-code/cli.js"}, aisessions.ToolClaude},
		{"node", []string{"/usr/bin/node", "--max-old-space-size=4096", "/usr/local/bin/claude"}, aisessions.ToolClaude},
		{"node", []string{"node", `C:\Users\u\AppData\Roaming\npm\node_modules\@anthropic-ai\claude-code\cli.js`}, aisessions.ToolClaude},
		{"bun", []string{"bun", "/opt/homebrew/lib/node_modules/@openai/codex/bin/codex.js"}, aisessions.ToolCodex},
		{"node.exe", []string{`C:\Program Files\nodejs\node.exe`, `C:\npm\node_modules\@openai\codex\bin\codex.js`}, aisessions.ToolCodex},
		{"python3", []string{"python3", "-m", "kimi_cli"}, aisessions.ToolKimi},
		{"python3.12", []string{"/home/u/.venv/bin/python3.12", "/home/u/.local/bin/kimi", "--model", "kimi-k2"}, aisessions.ToolKimi},
		{"python", []string{"python", "/site-packages/kimi_cli/__main__.py"}, aisessions.ToolKimi},
		// Shebang wrapper: comm is the script name even though argv[0] is bash.
		{"claude", []string{"/bin/bash", "/usr/local/bin/claude"}, aisessions.ToolClaude},
		// Empty name, argv carries the answer.
		{"", []string{"/usr/local/bin/codex"}, aisessions.ToolCodex},
	}
	for _, c := range cases {
		got, ok := Classify(c.name, c.argv)
		if !ok || got != c.want {
			t.Errorf("Classify(%q, %q) = (%q,%v), want (%q,true)", c.name, c.argv, got, ok, c.want)
		}
	}
}

func TestClassifyAIToolNegatives(t *testing.T) {
	cases := []struct {
		name string
		argv []string
	}{
		{"", nil},
		{"bash", []string{"bash"}},
		{"sshd", []string{"sshd: u@pts/0"}},
		// Near-miss names never match by substring.
		{"claude-desktop", []string{"claude-desktop"}},
		{"claudette", []string{"claudette"}},
		{"xclaude", nil},
		{"codex-server", []string{"codex-server"}},
		{"xcodex", nil},
		{"kimi-desktop", nil},
		{"kimidb", nil},
		// "Kimi Code" is one exact token; longer or shorter names are not it.
		{"Kimi Code Helper", []string{"Kimi Code Helper"}},
		{"Kimi Code Helper", nil},
		{"Kimi Coder", []string{"Kimi Coder"}},
		{"Kimi Codex", nil},
		{"Code", []string{"Code"}},
		{"Kimi  Code", []string{"Kimi  Code"}},
		// The uv/uvx launcher and tmux that spawn Kimi Code are not sessions;
		// only the renamed child is. Neither uv nor python is matched broadly.
		{"uv", []string{"/home/u/.local/bin/uv", "tool", "uvx", "--from", "kimi-cli", "kimi"}},
		{"uvx", []string{"uvx", "--from", "kimi-cli", "kimi"}},
		{"tmux: server", []string{"tmux", "new-session", "-d", "-s", "bloxos-eval-kimi", "-c", "/home/u/p", "uvx --from kimi-cli kimi"}},
		{"python3.12", []string{"/home/u/.local/share/uv/python/cpython-3.12.14-linux-x86_64-gnu/bin/python3.12"}},
		{"python3.12", []string{"python3.12", "kimi_code_helper.py"}},
		{"bash", []string{"bash", "-c", "Kimi Code"}},
		// A tool name appearing as an ARGUMENT to something else is not a session.
		{"bash", []string{"bash", "-c", "claude -p 'summarize this'"}},
		{"grep", []string{"grep", "-r", "claude", "."}},
		{"vim", []string{"vim", "claude.md"}},
		{"cat", []string{"cat", "/home/u/.claude/settings.json"}},
		{"ssh", []string{"ssh", "host", "codex"}},
		{"python3", []string{"python3", "train.py", "--model", "claude"}},
		{"node", []string{"node", "/srv/app/claude-code-router/cli.js"}},
		{"node", []string{"node", "-e", "require('claude')"}},
		{"node", []string{"node"}},
		{"node", []string{"node", "--version"}},
		{"python3", []string{"python3", "-m", "kimi_cli_utils"}},
		{"python3", []string{"python3", "-c", "import kimi"}},
		// The agent itself, the hub, and unrelated AI tooling.
		{"bloxos-agent", []string{"/usr/local/bin/bloxos-agent"}},
		{"ollama", []string{"ollama", "run", "claude"}},
		{"cursor", []string{"cursor"}},
		{"aider", []string{"aider", "--model", "claude-opus-5"}},
	}
	for _, c := range cases {
		if got, ok := Classify(c.name, c.argv); ok {
			t.Errorf("Classify(%q, %q) = %q, want no match", c.name, c.argv, got)
		}
	}
}

func TestModelFromArgv(t *testing.T) {
	exact := func(v string) aisessions.Attr {
		return aisessions.Attr{Value: v, Source: aisessions.SourceArgvFlag, Confidence: aisessions.ConfidenceExact}
	}
	inferred := func(v string) aisessions.Attr {
		return aisessions.Attr{Value: v, Source: aisessions.SourceArgvFlag, Confidence: aisessions.ConfidenceInferred}
	}
	cases := []struct {
		name string
		argv []string
		want aisessions.Attr
	}{
		{"claude", []string{"claude", "--model", "claude-opus-5"}, exact("claude-opus-5")},
		{"claude", []string{"claude", "--model=opus"}, inferred("opus")},
		{"codex", []string{"codex", "-m", "gpt-5-codex", "resume"}, exact("gpt-5-codex")},
		{"codex", []string{"codex", "--model", "gpt-5-codex"}, exact("gpt-5-codex")},
		{"claude", []string{"claude"}, aisessions.Unknown()},
		{"claude", []string{"claude", "--model"}, aisessions.Unknown()},
		{"claude", []string{"claude", "--model", "/tmp/evil path"}, aisessions.Unknown()},
		{"claude", []string{"claude", "-p", "--model is not a flag here"}, aisessions.Unknown()},
		// -m is codex's short flag only; for other tools it is not a model.
		{"claude", []string{"claude", "-m", "opus"}, aisessions.Unknown()},
		{"kimi", []string{"kimi", "-m", "kimi-k2"}, aisessions.Unknown()},
		{"Kimi Code", []string{"Kimi Code"}, aisessions.Unknown()},
		{"Kimi Code", []string{"Kimi Code", "--model", "kimi-k2"}, exact("kimi-k2")},
		// Regression: an interpreter's -m module flag is never a model.
		{"python3", []string{"python3", "-m", "kimi_cli"}, aisessions.Unknown()},
		{"python3", []string{"python3", "-m", "kimi_cli", "--model", "kimi-k2"}, exact("kimi-k2")},
		{"python", []string{"/usr/bin/python3.12", "-u", "-m", "kimi_cli", "--model=kimi-k2-thinking"}, exact("kimi-k2-thinking")},
		// Interpreter flags before the entry point are not tool flags either.
		{"node", []string{"node", "--max-old-space-size=4096", "/usr/local/bin/claude", "--model", "sonnet"}, inferred("sonnet")},
		{"node", []string{"node", "--title=--model", "/usr/local/bin/claude"}, aisessions.Unknown()},
		// Recognized by name only (entry -1): nothing in argv is trusted as a tool flag.
		{"claude", []string{"/bin/bash", "-c", "exec real-claude --model opus"}, aisessions.Unknown()},
		// Shebang wrapper: flags after the launcher script count.
		{"claude", []string{"/bin/bash", "/usr/local/bin/claude", "--model", "claude-sonnet-5"}, exact("claude-sonnet-5")},
		{"claude", []string{"/bin/bash", "--model", "x", "/usr/local/bin/claude"}, aisessions.Unknown()},
		// argv[0] alone is never a flag operand.
		{"claude", []string{"--model"}, aisessions.Unknown()},
	}
	for _, c := range cases {
		tool, entry, ok := classify(c.name, c.argv)
		if !ok {
			t.Errorf("classify(%q, %q) unexpectedly failed", c.name, c.argv)
			continue
		}
		if got := ModelFromArgv(tool, c.argv, entry); got != c.want {
			t.Errorf("ModelFromArgv(%q, %q, %d) = %+v, want %+v", tool, c.argv, entry, got, c.want)
		}
	}
}

func TestClassifyEntryIndex(t *testing.T) {
	cases := []struct {
		name  string
		argv  []string
		entry int
	}{
		{"claude", []string{"claude"}, 0},
		{"claude", nil, -1},
		{"claude", []string{"/bin/bash", "/usr/local/bin/claude"}, 1},
		{"claude", []string{"/bin/bash", "-c", "something"}, -1},
		{"node", []string{"node", "--flag", "/x/@anthropic-ai/claude-code/cli.js", "--model", "opus"}, 2},
		{"python3", []string{"python3", "-m", "kimi_cli", "--model", "k"}, 2},
		{"python3", []string{"python3", "--", "/home/u/.local/bin/kimi"}, 2},
		{"Kimi Code", []string{"Kimi Code"}, 0},
		{"Kimi Code", nil, -1},
	}
	for _, c := range cases {
		_, entry, ok := classify(c.name, c.argv)
		if !ok || entry != c.entry {
			t.Errorf("classify(%q, %q) entry=%d ok=%v, want entry %d", c.name, c.argv, entry, ok, c.entry)
		}
	}
}

// TestAISessionsPrivacyRegression plants every category of forbidden data in
// the process table and proves none of it reaches the wire.
func TestAISessionsPrivacyRegression(t *testing.T) {
	const (
		apiKey   = "sk-ant-api03-PLANTEDSECRETVALUE"
		prompt   = "tell me the production password hunter2"
		fullPath = "/home/alice/work/secret-project"
		username = "alice"
		envVar   = "ANTHROPIC_API_KEY=PLANTEDENV"
		toolOut  = "STDOUT-OF-A-TOOL-CALL"
	)
	procs := []Process{
		{
			PID: 4242, PPID: 1, Name: "claude",
			Argv:     []string{"/home/alice/.local/bin/claude", "--model", "claude-opus-5", "-p", prompt, "--api-key", apiKey, "--env", envVar, "--", toolOut},
			Cwd:      fullPath,
			Username: username,
			StartMS:  1_760_000_000_000,
		},
		{ // cwd IS the home directory: must not surface the username as a project.
			PID: 4343, PPID: 1, Name: "codex",
			Argv:     []string{"codex", "-m", "gpt-5-codex", "--danger-full-access", "--prompt", prompt},
			Cwd:      "/home/" + username,
			Username: username,
			StartMS:  1_760_000_000_000,
		},
		{ // cwd basename equals the username elsewhere on disk.
			PID: 4444, PPID: 1, Name: "kimi",
			Argv:     []string{"kimi"},
			Cwd:      "/srv/" + username,
			Username: username,
			StartMS:  1_760_000_000_000,
		},
	}
	sc := NewScanner()
	sessions := sc.Build(procs, time.Now())
	if len(sessions) != 3 {
		t.Fatalf("got %d sessions, want 3", len(sessions))
	}
	msg := aisessions.Sanitize(aisessions.Message{Type: aisessions.MessageType, MachineID: "m", Schema: aisessions.SchemaVersion, Sessions: sessions})
	wire, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	out := string(wire)

	for _, forbidden := range []string{
		apiKey, "PLANTEDSECRET", prompt, "hunter2", "password",
		fullPath, "/home/", "alice", envVar, "PLANTEDENV", "ANTHROPIC_API_KEY",
		toolOut, "--danger-full-access", "--prompt", "\"pid\"", "\"argv\"", "\"cwd\":", "\"env\"", "\"username\"",
	} {
		if strings.Contains(out, forbidden) {
			t.Errorf("wire payload leaks %q:\n%s", forbidden, out)
		}
	}
	// What IS allowed came through, with its provenance.
	if !strings.Contains(out, `"value":"secret-project","source":"cwd","confidence":"exact"`) {
		t.Errorf("project basename missing from payload: %s", out)
	}
	if !strings.Contains(out, `"value":"claude-opus-5","source":"argv_flag","confidence":"exact"`) {
		t.Errorf("explicit model missing from payload: %s", out)
	}
	if !strings.Contains(out, `"value":"gpt-5-codex","source":"argv_flag","confidence":"exact"`) {
		t.Errorf("codex model missing from payload: %s", out)
	}
	// Home-dir and username-named cwds are withheld, not partially leaked.
	if sessions[1].Project != aisessions.Unknown() || sessions[2].Project != aisessions.Unknown() {
		t.Errorf("home/username cwd must yield Unknown project: %+v %+v", sessions[1].Project, sessions[2].Project)
	}
	// First scan: no activity evidence yet.
	for _, s := range sessions {
		if s.Activity != aisessions.Unknown() {
			t.Errorf("first scan must not claim activity: %+v", s.Activity)
		}
	}
}

func TestBuildAISessionsCollapsesWrapperTrees(t *testing.T) {
	procs := []Process{
		// A shell wrapper named claude (comm=claude) that exec'd the real binary.
		{PID: 10, PPID: 1, Name: "claude", Argv: []string{"/bin/bash", "/usr/local/bin/claude"}, StartMS: 1},
		{PID: 11, PPID: 10, Name: "claude", Argv: []string{"/home/u/.local/share/claude/versions/2.1.0"}, StartMS: 2},
		// Claude Code re-launching itself (e.g. a helper) two hops down.
		{PID: 12, PPID: 11, Name: "sh", Argv: []string{"sh", "-c", "…"}, StartMS: 3},
		{PID: 13, PPID: 12, Name: "claude", Argv: []string{"claude", "--print"}, StartMS: 4},
		// A different tool under a claude process is its own session.
		{PID: 20, PPID: 11, Name: "codex", Argv: []string{"codex"}, StartMS: 5},
		// Independent sessions.
		{PID: 30, PPID: 1, Name: "codex", Argv: []string{"codex"}, StartMS: 6},
		{PID: 40, PPID: 30, Name: "kimi", Argv: []string{"kimi"}, StartMS: 7},
	}
	sc := NewScanner()
	got := sc.Build(procs, time.Now())
	ids := map[string]string{}
	for _, s := range got {
		ids[s.ID] = s.Tool
	}
	want := map[string]string{
		aisessions.SessionID(10, 1): aisessions.ToolClaude,
		aisessions.SessionID(20, 5): aisessions.ToolCodex,
		aisessions.SessionID(30, 6): aisessions.ToolCodex,
		aisessions.SessionID(40, 7): aisessions.ToolKimi,
	}
	if len(ids) != len(want) {
		t.Fatalf("got %d sessions %v, want %d", len(ids), ids, len(want))
	}
	for id, tool := range want {
		if ids[id] != tool {
			t.Errorf("session %s: got tool %q, want %q", id, ids[id], tool)
		}
	}
	// pid 13 (claude under sh under claude) collapsed via the 2-hop walk,
	// pid 11 collapsed into its wrapper 10.
	for _, pid := range []struct {
		pid   int32
		start int64
	}{{11, 2}, {13, 4}} {
		if _, present := ids[aisessions.SessionID(pid.pid, pid.start)]; present {
			t.Errorf("pid %d should have collapsed into its ancestor", pid.pid)
		}
	}
}

// TestBuildAISessionsKimiCodeRenamedProcess replays the process tree observed
// for Kimi Code CLI 1.50 (tmux -> uv -> python3.12 renamed to "Kimi Code"):
// exactly one Kimi session, attributed to the renamed child, and nothing from
// the launcher chain, interpreter path, tmux session name, home directory or
// user reaches the wire.
func TestBuildAISessionsKimiCodeRenamedProcess(t *testing.T) {
	const (
		username = "alice"
		home     = "/home/" + username
		project  = home + "/ai-sessions-eval/demo-project"
	)
	procs := []Process{
		{PID: 311813, PPID: 1, Name: "tmux: server",
			Argv: []string{"tmux", "new-session", "-d", "-s", "bloxos-eval-kimi", "-c", project, "uvx --from kimi-cli kimi"},
			Cwd:  home, Username: username, StartMS: 1_760_000_000_000},
		{PID: 311814, PPID: 311813, Name: "uv",
			Argv: []string{home + "/.local/bin/uv", "tool", "uvx", "--from", "kimi-cli", "kimi"},
			Cwd:  project, Username: username, StartMS: 1_760_000_000_001},
		{PID: 311954, PPID: 311814, Name: "Kimi Code",
			Argv: []string{"Kimi Code"},
			Cwd:  project, Username: username, StartMS: 1_760_000_000_002},
	}
	sc := NewScanner()
	sessions := sc.Build(procs, time.Now())
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions %+v, want exactly 1", len(sessions), sessions)
	}
	s := sessions[0]
	if s.Tool != aisessions.ToolKimi || s.ID != aisessions.SessionID(311954, 1_760_000_000_002) {
		t.Errorf("session = %+v, want kimi attributed to the renamed child", s)
	}
	if s.Project.Value != "demo-project" || s.Project.Source != aisessions.SourceCwd {
		t.Errorf("project = %+v, want basename demo-project from cwd", s.Project)
	}
	if s.Model != aisessions.Unknown() {
		t.Errorf("model = %+v, want unknown (no --model flag)", s.Model)
	}
	msg := aisessions.Sanitize(aisessions.Message{Type: aisessions.MessageType, MachineID: "m", Schema: aisessions.SchemaVersion, Sessions: sessions})
	wire, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	out := string(wire)
	for _, forbidden := range []string{
		"Kimi Code", "uvx", "uv ", "kimi-cli", "python", "cpython", "tmux", "bloxos-eval-kimi",
		"/home/", username, "ai-sessions-eval", "311954", "311814",
	} {
		if strings.Contains(out, forbidden) {
			t.Errorf("wire payload leaks %q:\n%s", forbidden, out)
		}
	}
}

func TestBuildAISessionsSurvivesParentCycle(t *testing.T) {
	procs := []Process{
		{PID: 5, PPID: 6, Name: "claude", StartMS: 1},
		{PID: 6, PPID: 5, Name: "claude", StartMS: 2},
	}
	done := make(chan []aisessions.Session, 1)
	go func() { done <- NewScanner().Build(procs, time.Now()) }()
	select {
	case got := <-done:
		// Both collapse into each other; an empty answer is acceptable, an
		// infinite loop is not.
		if len(got) > 2 {
			t.Fatalf("unexpected sessions: %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ancestor walk did not terminate on a parent cycle")
	}
}

func TestBuildAISessionsActivityInference(t *testing.T) {
	sc := NewScanner()
	t0 := time.Unix(1_760_000_000, 0)
	base := Process{PID: 7, PPID: 1, Name: "claude", StartMS: 100, CPUSeconds: 10}

	first := sc.Build([]Process{base}, t0)
	if first[0].Activity != aisessions.Unknown() {
		t.Fatalf("first scan: %+v, want unknown", first[0].Activity)
	}

	// 30s later, 3s of CPU consumed → 10% → active (inferred).
	busy := base
	busy.CPUSeconds = 13
	second := sc.Build([]Process{busy}, t0.Add(30*time.Second))
	wantActive := aisessions.Attr{Value: aisessions.ActivityActive, Source: aisessions.SourceCPUTime, Confidence: aisessions.ConfidenceInferred}
	if second[0].Activity != wantActive {
		t.Fatalf("busy scan: %+v, want %+v", second[0].Activity, wantActive)
	}

	// Another 30s, 0.1s of CPU → 0.3% → idle (inferred).
	quiet := busy
	quiet.CPUSeconds = 13.1
	third := sc.Build([]Process{quiet}, t0.Add(60*time.Second))
	wantIdle := aisessions.Attr{Value: aisessions.ActivityIdle, Source: aisessions.SourceCPUTime, Confidence: aisessions.ConfidenceInferred}
	if third[0].Activity != wantIdle {
		t.Fatalf("quiet scan: %+v, want %+v", third[0].Activity, wantIdle)
	}

	// PID reused by a new process (different start time): no inference.
	reused := quiet
	reused.StartMS = 999
	reused.CPUSeconds = 50
	fourth := sc.Build([]Process{reused}, t0.Add(90*time.Second))
	if fourth[0].Activity != aisessions.Unknown() {
		t.Fatalf("pid reuse: %+v, want unknown", fourth[0].Activity)
	}
	if fourth[0].ID == third[0].ID {
		t.Fatal("pid reuse must produce a new session id")
	}

	// CPU time unavailable on this platform: never guess.
	noCPU := reused
	noCPU.CPUSeconds = -1
	fifth := sc.Build([]Process{noCPU}, t0.Add(120*time.Second))
	if fifth[0].Activity != aisessions.Unknown() {
		t.Fatalf("no cpu evidence: %+v, want unknown", fifth[0].Activity)
	}

	// Inter-scan state is pruned to live processes.
	sc.Build(nil, t0.Add(150*time.Second))
	if len(sc.prev) != 0 {
		t.Fatalf("scanner retained %d stale samples", len(sc.prev))
	}
}

func TestBuildAISessionsStartedAtAndEmpty(t *testing.T) {
	sc := NewScanner()
	got := sc.Build([]Process{
		{PID: 1, Name: "claude", StartMS: 1_760_000_000_000},
		{PID: 2, Name: "codex", StartMS: 0},
		{PID: 3, Name: "bash"},
	}, time.Now())
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2", len(got))
	}
	if got[0].StartedAt != time.UnixMilli(1_760_000_000_000).UTC().Format(time.RFC3339) {
		t.Errorf("started_at = %q", got[0].StartedAt)
	}
	if got[1].StartedAt != "" {
		t.Errorf("unknown start time must be empty, got %q", got[1].StartedAt)
	}
	if empty := sc.Build(nil, time.Now()); len(empty) != 0 {
		t.Errorf("nil table should yield no sessions: %+v", empty)
	}
}

func TestAISessionsLocalOptOut(t *testing.T) {
	for _, c := range []struct {
		val  string
		want bool
	}{{"", true}, {"1", true}, {"true", true}, {"anything", true}, {"0", false}, {"false", false}, {"OFF", false}, {" no ", false}} {
		if got := EnabledByEnv(c.val); got != c.want {
			t.Errorf("BLOXOS_AI_SESSIONS=%q → %v, want %v", c.val, got, c.want)
		}
	}
}

func TestGateRequiresHubSignalAndHonorsLocalOptOut(t *testing.T) {
	var g Gate
	// No hub signal yet: never scan, regardless of env.
	if g.Allowed("") || g.Allowed("1") {
		t.Fatal("gate must stay closed until the hub has spoken")
	}
	// Hub says enabled (the default hub setting): open.
	if newly := g.Apply(true, 1); !newly {
		t.Fatal("first enable must report newlyEnabled")
	}
	if !g.Allowed("") {
		t.Fatal("gate should be open after hub enable")
	}
	if newly := g.Apply(true, 2); newly {
		t.Fatal("repeated enable at a newer rev must not report newlyEnabled")
	}
	// Local hard opt-out wins over a hub enable.
	for _, v := range []string{"0", "false", "off", "no"} {
		if g.Allowed(v) {
			t.Fatalf("BLOXOS_AI_SESSIONS=%q must override hub enable", v)
		}
	}
	// Hub disable closes it.
	if newly := g.Apply(false, 3); newly {
		t.Fatal("disable must not report newlyEnabled")
	}
	if g.Allowed("") {
		t.Fatal("gate should be closed after hub disable")
	}
	// Re-enable after disable is "newly enabled" again.
	if newly := g.Apply(true, 4); !newly || !g.Allowed("") {
		t.Fatal("re-enable after disable should open and report newlyEnabled")
	}
	// A new connection starts from unknown.
	g.Reset()
	if g.Allowed("") {
		t.Fatal("Reset must close the gate until the hub speaks again")
	}
}

// TestGateIgnoresStaleRevision is the reordering scenario: a disable at
// rev 2 followed by a delayed enable at rev 1 must leave the gate closed.
func TestGateIgnoresStaleRevision(t *testing.T) {
	var g Gate
	if newly := g.Apply(false, 2); newly || g.Allowed("") {
		t.Fatal("rev 2 disable should close the gate")
	}
	if newly := g.Apply(true, 1); newly || g.Allowed("") {
		t.Fatal("stale rev 1 enable must be ignored after rev 2 disable")
	}
	if newly := g.Apply(true, 3); !newly || !g.Allowed("") {
		t.Fatal("rev 3 enable should open the gate")
	}
	// Equal revision is ignored: a duplicate changes nothing, and a
	// conflicting value at the same revision cannot flip the gate.
	if newly := g.Apply(true, 3); newly || !g.Allowed("") {
		t.Fatal("duplicate rev 3 must change nothing")
	}
	if newly := g.Apply(false, 3); newly || !g.Allowed("") {
		t.Fatal("conflicting value at equal rev 3 must not close the gate")
	}
	// A new connection starts over: rev 1 is fresh again.
	g.Reset()
	if g.Allowed("") {
		t.Fatal("Reset must close the gate")
	}
	if newly := g.Apply(true, 1); !newly || !g.Allowed("") {
		t.Fatal("after Reset a low revision is valid again")
	}
}

// TestGateEqualRevisionConflictCannotFlip pins the equal-revision rule in
// both directions: whichever value arrived first at a revision stands.
func TestGateEqualRevisionConflictCannotFlip(t *testing.T) {
	var g Gate
	g.Apply(false, 2)
	if g.Apply(true, 2) || g.Allowed("") {
		t.Fatal("equal-rev enable must not reopen a gate closed at that rev")
	}
	g.Reset()
	if !g.Apply(true, 2) || !g.Allowed("") {
		t.Fatal("first decision at rev 2 should open the gate")
	}
	if g.Apply(false, 2) || !g.Allowed("") {
		t.Fatal("equal-rev disable must not close a gate opened at that rev")
	}
	// Strictly greater still wins.
	if g.Apply(false, 3) || g.Allowed("") {
		t.Fatal("rev 3 disable should close the gate")
	}
}
