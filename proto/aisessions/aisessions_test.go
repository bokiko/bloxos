package aisessions

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectFromDir(t *testing.T) {
	cases := []struct {
		dir, user string
		want      string
		ok        bool
	}{
		{"/home/alice/projects/bloxos", "alice", "bloxos", true},
		{"/root/bloxos", "root", "bloxos", true},
		{"/srv/app/api", "", "api", true},
		{`C:\Users\alice\src\bloxos`, "alice", "bloxos", true},
		{`C:\code\my app`, "", "my app", true},
		{"/home/alice/projects/bloxos/", "alice", "bloxos", true},
		// Home directories: the basename IS the username.
		{"/home/alice", "alice", "", false},
		{"/home/alice", "", "", false},
		{"/Users/alice", "", "", false},
		{`C:\Users\alice`, "", "", false},
		{`c:\users\alice`, "", "", false},
		// Basename equal to the username anywhere is withheld.
		{"/srv/alice", "alice", "", false},
		{"/srv/ALICE", "alice", "", false},
		// Root / top-level / degenerate.
		{"/", "", "", false},
		{"/root", "root", "", false},
		{"/tmp", "", "", false},
		{"C:\\", "", "", false},
		{"", "", "", false},
		{"relative", "", "", false},
		{"/a/b/..", "", "", false},
	}
	for _, c := range cases {
		got, ok := ProjectFromDir(c.dir, c.user)
		if ok != c.ok {
			t.Errorf("ProjectFromDir(%q,%q) ok=%v want %v", c.dir, c.user, ok, c.ok)
			continue
		}
		if !ok {
			if got != Unknown() {
				t.Errorf("ProjectFromDir(%q) returned %+v for !ok; want Unknown()", c.dir, got)
			}
			continue
		}
		if got.Value != c.want || got.Source != SourceCwd || got.Confidence != ConfidenceExact {
			t.Errorf("ProjectFromDir(%q) = %+v, want value %q/cwd/exact", c.dir, got, c.want)
		}
	}
}

func TestProjectFromDirTruncatesLongNames(t *testing.T) {
	long := strings.Repeat("é", 100)
	got, ok := ProjectFromDir("/srv/x/"+long, "")
	if !ok {
		t.Fatal("expected ok")
	}
	if n := len([]rune(got.Value)); n != MaxValueRunes {
		t.Fatalf("value has %d runes, want %d", n, MaxValueRunes)
	}
}

func TestModelFromFlag(t *testing.T) {
	cases := []struct {
		in   string
		want Attr
	}{
		{"claude-opus-5", Attr{"claude-opus-5", SourceArgvFlag, ConfidenceExact}},
		{"gpt-5-codex", Attr{"gpt-5-codex", SourceArgvFlag, ConfidenceExact}},
		{"kimi-k2:latest", Attr{"kimi-k2:latest", SourceArgvFlag, ConfidenceExact}},
		{"opus", Attr{"opus", SourceArgvFlag, ConfidenceInferred}},
		{" sonnet ", Attr{"sonnet", SourceArgvFlag, ConfidenceInferred}},
		{"", Unknown()},
		{"/etc/passwd", Unknown()},
		{"opus; rm -rf /", Unknown()},
		{"tell me a secret", Unknown()},
		{"sk-ant-api03-abc/def", Unknown()},
		{strings.Repeat("a", 65), Unknown()},
	}
	for _, c := range cases {
		if got := ModelFromFlag(c.in); got != c.want {
			t.Errorf("ModelFromFlag(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestSanitizeDropsInvalidSessionsAndCollapsesAttrs(t *testing.T) {
	msg := Message{
		Type:      "whatever",
		MachineID: "m1",
		Sessions: []Session{
			{ // valid, everything known
				ID:        "ABCDEF0123456789",
				Tool:      ToolClaude,
				StartedAt: "2026-09-05T10:00:00+02:00",
				Project:   Attr{"bloxos", SourceCwd, ConfidenceExact},
				Model:     Attr{"claude-opus-5", SourceArgvFlag, ConfidenceExact},
				Activity:  Attr{ActivityActive, SourceCPUTime, ConfidenceInferred},
			},
			{ // tool outside the whitelist
				ID: "0123456789abcdef", Tool: "cursor",
			},
			{ // non-hex id
				ID: "not-hex!", Tool: ToolCodex,
			},
			{ // empty id
				ID: "", Tool: ToolCodex,
			},
			{ // attrs that overclaim or carry the wrong source/shape
				ID:        "1111",
				Tool:      ToolKimi,
				StartedAt: "yesterday",
				Project:   Attr{"/home/alice/secret", SourceCwd, ConfidenceExact},
				Model:     Attr{"claude-opus-5", "env", ConfidenceExact},
				Activity:  Attr{ActivityActive, SourceCPUTime, ConfidenceExact},
			},
			{ // project with a wrong source; activity with a bogus value
				ID:       "2222",
				Tool:     ToolCodex,
				Project:  Attr{"bloxos", "transcript", ConfidenceExact},
				Model:    Attr{"o3 --danger", SourceArgvFlag, ConfidenceExact},
				Activity: Attr{"typing prompt", SourceCPUTime, ConfidenceInferred},
			},
		},
	}
	out := Sanitize(msg)
	if out.Type != MessageType || out.MachineID != "m1" || out.Schema != SchemaVersion {
		t.Fatalf("envelope not normalized: %+v", out)
	}
	if len(out.Sessions) != 3 {
		t.Fatalf("got %d sessions, want 3: %+v", len(out.Sessions), out.Sessions)
	}
	s0 := out.Sessions[0]
	if s0.ID != "abcdef0123456789" || s0.StartedAt != "2026-09-05T08:00:00Z" {
		t.Errorf("valid session not preserved/normalized: %+v", s0)
	}
	if s0.Project.Value != "bloxos" || s0.Model.Value != "claude-opus-5" || s0.Activity.Value != ActivityActive {
		t.Errorf("valid attrs altered: %+v", s0)
	}
	s1 := out.Sessions[1]
	if s1.ID != "1111" || s1.StartedAt != "" {
		t.Errorf("bad started_at should be dropped: %+v", s1)
	}
	if s1.Project != Unknown() || s1.Model != Unknown() || s1.Activity != Unknown() {
		t.Errorf("overclaiming attrs must collapse to Unknown: %+v", s1)
	}
	s2 := out.Sessions[2]
	if s2.Project != Unknown() || s2.Model != Unknown() || s2.Activity != Unknown() {
		t.Errorf("wrong-source/bogus attrs must collapse to Unknown: %+v", s2)
	}
}

func TestSanitizeCapsSessionCount(t *testing.T) {
	var msg Message
	for i := 0; i < MaxSessionsPerMachine+20; i++ {
		msg.Sessions = append(msg.Sessions, Session{ID: "ab", Tool: ToolClaude})
	}
	if got := len(Sanitize(msg).Sessions); got != MaxSessionsPerMachine {
		t.Fatalf("got %d sessions, want cap %d", got, MaxSessionsPerMachine)
	}
}

func TestSanitizeEmptyMessageHasEmptySessionsArray(t *testing.T) {
	out := Sanitize(Message{})
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"sessions":[]`) {
		t.Fatalf("empty sessions must serialize as [] not null: %s", data)
	}
}

// TestWireShapeCarriesOnlyContractFields pins the JSON field set. Adding a
// field here is a deliberate privacy decision, not a refactor.
func TestWireShapeCarriesOnlyContractFields(t *testing.T) {
	s := Session{ID: "ab", Tool: ToolClaude, Project: Unknown(), Model: Unknown(), Activity: Unknown()}
	data, _ := json.Marshal(Message{Type: MessageType, MachineID: "m", Schema: 1, Sessions: []Session{s}})
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatal(err)
	}
	wantTop := []string{"type", "machine_id", "schema", "sessions"}
	if len(generic) != len(wantTop) {
		t.Fatalf("top-level keys = %v, want %v", keys(generic), wantTop)
	}
	sess := generic["sessions"].([]any)[0].(map[string]any)
	wantSess := map[string]bool{"id": true, "tool": true, "project": true, "model": true, "activity": true}
	for k := range sess {
		if !wantSess[k] {
			t.Errorf("unexpected session field %q on the wire", k)
		}
	}
	for _, k := range []string{"project", "model", "activity"} {
		attr := sess[k].(map[string]any)
		if len(attr) != 3 {
			t.Errorf("attr %q keys = %v, want value/source/confidence", k, keys(attr))
		}
	}
}

func TestSessionIDIsOpaqueAndStable(t *testing.T) {
	a := SessionID(1234, 1700000000000)
	b := SessionID(1234, 1700000000000)
	c := SessionID(1234, 1700000000001)
	if a != b || a == c || len(a) != 16 || !isHex(a) {
		t.Fatalf("SessionID a=%s b=%s c=%s", a, b, c)
	}
	if strings.Contains(a, "1234") {
		t.Fatalf("session id leaks the pid: %s", a)
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
