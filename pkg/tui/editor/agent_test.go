package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lflow/lflow/pkg/tui/database"
)

func variant(t *testing.T, id string) agentVariant {
	t.Helper()
	v, ok := agentVariantByID(id)
	if !ok {
		t.Fatalf("no agent variant %q", id)
	}
	return v
}

// claudeStore seeds a sandboxed HOME with a Claude Code project store holding one
// session transcript, and returns the session id.
func claudeStore(t *testing.T, records ...string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	dir := filepath.Join(home, ".claude", "projects", "-home-dev-repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "3f0c7a12-9d44-4a1e-b0f5-7c2b8e6a1d33"
	body := strings.Join(records, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return id
}

const (
	recSummary = `{"type":"summary","summary":"fix the flaky sync test","color":"#4ec9b0"}`
	// the same session without the CLI's own color — the variant's theming stands
	recPlain = `{"type":"summary","summary":"fix the flaky sync test"}`
	recUser  = `{"type":"user","timestamp":"2026-07-24T10:03:00Z","cwd":"/home/dev/repo","message":{"role":"user","content":[{"type":"text","text":"the sync test is flaky"}]}}`
	recTool  = `{"type":"assistant","timestamp":"2026-07-24T10:03:04Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./pkg/tui/..."}}]}}`
	recReply = `{"type":"assistant","timestamp":"2026-07-24T10:04:00Z","message":{"role":"assistant","content":[{"type":"text","text":"the flake is a clock race"}]}}`
)

// TestAgentTypesRegistered: all three session types are pickable, dependency
// gated on their own CLI, and carry the run/expand hooks the gestures need.
func TestAgentTypesRegistered(t *testing.T) {
	for _, v := range agentVariants {
		if !database.ValidTypes[v.key] {
			t.Errorf("%s is not a valid node type", v.key)
		}
		nt := typeOf(v.key)
		if nt.key != v.key {
			t.Fatalf("%s falls back to bullets — not in the registry", v.key)
		}
		if nt.run == nil || nt.view == nil || nt.openHost == nil {
			t.Errorf("%s misses a hook: run=%v view=%v openHost=%v", v.key, nt.run != nil, nt.view != nil, nt.openHost != nil)
		}
		if len(nt.cliDeps) != 1 || nt.cliDeps[0] != v.bin {
			t.Errorf("%s cliDeps = %v, want [%s]", v.key, nt.cliDeps, v.bin)
		}
		if !nt.inlineEditable {
			t.Errorf("%s must stay inline editable — its text is the session's name", v.key)
		}
	}
}

// TestAgentVariantArgs: a first open CREATES the session and every later one
// RESUMES it, for a CLI that lets lflow choose the id.
func TestAgentVariantArgs(t *testing.T) {
	c := variant(t, "claude")
	s := agentSession{SessionID: "abc-123"}
	if got := strings.Join(c.args(s, false), " "); got != "--session-id abc-123" {
		t.Errorf("claude first open args = %q", got)
	}
	if got := strings.Join(c.args(s, true), " "); got != "--resume abc-123" {
		t.Errorf("claude resume args = %q", got)
	}
	// a CLI that names its own sessions resumes by the id lflow adopted, and
	// falls back to continuing the directory's last one when it has none
	o := variant(t, "opencode")
	if got := strings.Join(o.args(agentSession{SessionID: "ses_9"}, true), " "); got != "--session ses_9" {
		t.Errorf("opencode resume args = %q", got)
	}
	if got := strings.Join(o.args(agentSession{}, true), " "); got != "--continue" {
		t.Errorf("opencode idless resume args = %q", got)
	}
	if got := o.args(agentSession{}, false); len(got) != 0 {
		t.Errorf("opencode first open args = %v, want none", got)
	}
}

// TestAgentReadMeta reads a session's identity out of the CLI's own store: its
// title, its directory, and the color the CLI stamped on it.
func TestAgentReadMeta(t *testing.T) {
	id := claudeStore(t, recSummary, recUser, recTool)
	c := variant(t, "claude")
	path := agentTranscriptPath(c.transcriptDirs(), c.exts, id)
	if path == "" {
		t.Fatal("session transcript not located in the store")
	}
	meta := agentReadMeta(c.id, path)
	if meta.title != "fix the flaky sync test" {
		t.Errorf("title = %q", meta.title)
	}
	if meta.cwd != "/home/dev/repo" {
		t.Errorf("cwd = %q", meta.cwd)
	}
	if meta.color == "" || !strings.Contains(meta.color, "78;201;176") {
		t.Errorf("session color = %q, want the #4ec9b0 truecolor sequence", meta.color)
	}
}

// TestAgentReadMetaFallsBackToFirstPrompt: an untitled session is named by what
// was asked of it.
func TestAgentReadMetaFallsBackToFirstPrompt(t *testing.T) {
	id := claudeStore(t, recUser, recTool)
	c := variant(t, "claude")
	meta := agentReadMeta(c.id, agentTranscriptPath(c.transcriptDirs(), c.exts, id))
	if meta.title != "the sync test is flaky" {
		t.Errorf("title = %q, want the first prompt", meta.title)
	}
	if meta.color != "" {
		t.Errorf("color = %q, want none", meta.color)
	}
}

// TestAgentTranscript: the expanded view's material — prompts, replies and the
// TOOL CALLS between them, in order.
func TestAgentTranscript(t *testing.T) {
	id := claudeStore(t, recSummary, recUser, recTool, recReply)
	c := variant(t, "claude")
	entries, path := agentTranscript(c.transcriptDirs(), c.exts, id)
	if path == "" {
		t.Fatal("no transcript path")
	}
	var kinds []string
	for _, e := range entries {
		kinds = append(kinds, e.kind)
	}
	want := []string{"meta", "user", "tool", "assistant"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("entry kinds = %v, want %v", kinds, want)
	}
	tool := entries[2]
	if tool.name != "Bash" || !strings.Contains(tool.text, "go test") {
		t.Errorf("tool entry = %+v, want the Bash call and its command", tool)
	}
	if tool.at.IsZero() {
		t.Error("tool entry lost its timestamp")
	}
	if line := stripSGR(agentEntryLine(tool, cCyan)); !strings.Contains(line, "→ Bash") {
		t.Errorf("tool line = %q, want the tool call spelled out", line)
	}
}

// TestAgentDecodeOtherShapes: pi's rpc-shaped records and opencode's parts both
// decode through the same tolerant reader.
func TestAgentDecodeOtherShapes(t *testing.T) {
	pi := map[string]any{"type": "tool", "toolName": "read_file", "args": map[string]any{"path": "main.go"}}
	got := agentDecodeRecord(pi)
	if len(got) != 1 || got[0].kind != "tool" || got[0].name != "read_file" || got[0].text != "main.go" {
		t.Errorf("pi record decoded to %+v", got)
	}

	oc := map[string]any{
		"role": "assistant",
		"time": map[string]any{"created": float64(1753351380000)},
		"parts": []any{
			map[string]any{"type": "text", "text": "running the suite"},
			map[string]any{"type": "tool", "tool": "bash", "state": map[string]any{"input": map[string]any{"command": "go build ./..."}}},
		},
	}
	got = agentDecodeRecord(oc)
	if len(got) != 2 || got[0].kind != "assistant" || got[1].kind != "tool" || got[1].name != "bash" {
		t.Fatalf("opencode record decoded to %+v", got)
	}
	if got[1].text != "go build ./..." {
		t.Errorf("tool detail = %q", got[1].text)
	}
	if got[0].at.IsZero() {
		t.Error("millisecond clock not decoded")
	}
}

// TestAgentNewestSince: a CLI that names its own sessions has its id ADOPTED
// from whatever its run touched.
func TestAgentNewestSince(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "ses_old_00000001.json")
	fresh := filepath.Join(dir, "ses_new_00000002.json")
	for _, p := range []string{old, fresh} {
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	since := time.Now().Add(-time.Minute)
	past := since.Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	if got := agentNewestSince([]string{dir}, []string{".json"}, since); got != "ses_new_00000002" {
		t.Errorf("adopted session id = %q", got)
	}
	// nothing written during the run: no id is invented
	if got := agentNewestSince([]string{dir}, []string{".json"}, time.Now().Add(time.Minute)); got != "" {
		t.Errorf("adopted %q from a run that wrote nothing", got)
	}
}

// TestAgentRemoteURL: a claude.ai/code link in a node's text makes the session
// remote — there is no local process to attach to, so it opens in the browser.
func TestAgentRemoteURL(t *testing.T) {
	cases := map[string]string{
		"review the PR https://claude.ai/code/session_01ABCdef": "https://claude.ai/code/session_01ABCdef",
		"(https://claude.com/code/session_9 ) later":            "https://claude.com/code/session_9",
		"nothing remote here":                                   "",
		"https://claude.ai/settings":                            "",
	}
	for text, want := range cases {
		if got := agentRemoteURL(text); got != want {
			t.Errorf("agentRemoteURL(%q) = %q, want %q", text, got, want)
		}
	}
	if got := agentURLSessionID("https://claude.ai/code/session_01ABC?x=1"); got != "session_01ABC" {
		t.Errorf("session id from URL = %q", got)
	}
}

// TestAgentNodeLookAndDone: a live session wears its agent's color and a status
// tail; a DONE one goes muted gray, glyph included.
func TestAgentNodeLookAndDone(t *testing.T) {
	id := claudeStore(t, recPlain, recUser)
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "sess", Name: "flaky test", Type: c.key})
	it := m.tree.byUUID["sess"]
	m.agentSave("sess", agentSession{Variant: c.id, SessionID: id, Cwd: "/home/dev/repo", Runs: 1, OpenedAt: time.Now().Unix()})

	look := m.publishAgentLook("sess", c)
	if look.color != c.colorSGR() {
		t.Errorf("live session color = %q, want the claude color", look.color)
	}
	tail := stripSGR(look.tail)
	for _, want := range []string{"fix the flaky sync test", "/home/dev/repo", "just now"} {
		if !strings.Contains(tail, want) {
			t.Errorf("tail %q missing %q", tail, want)
		}
	}

	if _, color := glyphFor(it); color != c.colorSGR() {
		t.Errorf("live glyph color = %q", color)
	}
	it.completedAt = time.Now().Unix()
	if _, color := glyphFor(it); color != cDim {
		t.Errorf("done glyph color = %q, want muted gray", color)
	}
	body := renderBody(it, it.name, -1, false, m.chips, false)
	if !strings.Contains(body, cDim) || !strings.Contains(body, cStrike) {
		t.Error("a done session must render muted and struck through")
	}
}

// TestAgentSessionColorWins: a session the CLI gave its own color wears that
// color instead of the variant's.
func TestAgentSessionColorWins(t *testing.T) {
	id := claudeStore(t, recSummary, recUser)
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "sess", Name: "colored", Type: c.key})
	m.agentSave("sess", agentSession{Variant: c.id, SessionID: id})

	got := m.agentColorFor("sess", c, m.agentLoad("sess"))
	if !strings.Contains(got, "78;201;176") {
		t.Errorf("session color = %q, want the color from the store (#4ec9b0)", got)
	}
}

// TestAgentAttachExistingSession: an existing session is filed onto a node —
// its id, title and directory come along, and the next open resumes it.
func TestAgentAttachExistingSession(t *testing.T) {
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "sess", Name: "", Type: c.key})
	m.agentAttach("sess", agentStoreSession{
		variant: c.id, id: "abc-123", title: "port the parser", cwd: "/home/dev/repo",
		updated: time.Now(),
	})
	s := m.agentLoad("sess")
	if s.SessionID != "abc-123" || s.Title != "port the parser" || s.Cwd != "/home/dev/repo" {
		t.Fatalf("attached session = %+v", s)
	}
	if s.Runs == 0 {
		t.Error("an attached session already exists — the next open must resume it")
	}
	if got := strings.Join(c.args(s, s.Runs > 0), " "); got != "--resume abc-123" {
		t.Errorf("next open = %q, want a resume", got)
	}
}

// TestAgentChipTracksSessionTitle: the chip's text IS the session's live title,
// so a session renamed inside its CLI reads renamed in the outline too — and the
// title never leaks into the synced chip row.
func TestAgentChipTracksSessionTitle(t *testing.T) {
	id := claudeStore(t, recSummary, recUser)
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "todo "})
	cursorOn(m, "note")
	cur := m.tree.byUUID["note"]
	m.caret = len([]rune(cur.name))

	m.insertAgentChip(cur, c, agentStoreSession{variant: c.id, id: id, title: "fix the flaky sync test"})
	chip, ok := m.agentChipAtCaret(cur)
	if !ok {
		t.Fatal("no session chip at the caret")
	}
	if chip.Value != c.id {
		t.Errorf("chip value = %q, want the variant id", chip.Value)
	}
	if got := displayAnchors(cur.name, m.chips); !strings.Contains(got, "✳ fix the flaky sync test") {
		t.Errorf("chip renders as %q", got)
	}

	// the CLI renames the session: the chip follows on the next refresh
	path := agentTranscriptPath(c.transcriptDirs(), c.exts, id)
	body := `{"type":"summary","summary":"land the retry fix"}` + "\n" + recUser + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	m.dropAgentEntries(chip.ID)
	m.refreshAgentChip(chip.ID)
	if got := displayAnchors(cur.name, m.chips); !strings.Contains(got, "✳ land the retry fix") {
		t.Errorf("renamed session renders as %q", got)
	}

	// the label is session chrome, never a stored chip field
	stored, err := database.LoadChips(m.db)
	if err != nil {
		t.Fatal(err)
	}
	if stored[chip.ID].Label != "" {
		t.Errorf("chip row carries a label %q — the title is local chrome", stored[chip.ID].Label)
	}
}

// TestAgentsListRanksLiveFirst: /agents lists live sessions before done ones,
// and draws the done ones muted.
func TestAgentsListRanksLiveFirst(t *testing.T) {
	c := variant(t, "claude")
	p := variant(t, "pi")
	m, _ := dbModel(t,
		database.Node{UUID: "done", Name: "shipped work", Type: c.key, CompletedAt: time.Now().Unix()},
		database.Node{UUID: "live", Name: "current work", Type: p.key},
	)
	m.agentSave("done", agentSession{Variant: c.id, SessionID: "d1", OpenedAt: time.Now().Unix()})
	m.agentSave("live", agentSession{Variant: p.id, SessionID: "l1", OpenedAt: time.Now().Add(-time.Hour).Unix()})

	rows := m.collectAgentRows()
	if len(rows) != 2 {
		t.Fatalf("collected %d rows, want 2", len(rows))
	}
	if rows[0].uuid != "live" || rows[1].uuid != "done" {
		t.Fatalf("order = %s,%s — live sessions rank first", rows[0].uuid, rows[1].uuid)
	}
	doneRow := m.agentListRow(rows[1], "")
	if !strings.HasPrefix(doneRow, cDim) || !strings.Contains(doneRow, "done") {
		t.Errorf("done row = %q, want it muted and marked done", doneRow)
	}
	liveRow := m.agentListRow(rows[0], "")
	if !strings.Contains(liveRow, p.colorSGR()) {
		t.Errorf("live row = %q, want the agent's own color", liveRow)
	}
}

// TestAgentsListIncludesChips: a session chip is a session too — /agents lists
// it alongside the nodes, pointing at the row that carries it.
func TestAgentsListIncludesChips(t *testing.T) {
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "notes "})
	cursorOn(m, "note")
	cur := m.tree.byUUID["note"]
	m.caret = len([]rune(cur.name))
	m.insertAgentChip(cur, c, agentStoreSession{})
	if _, err := m.tree.save(); err != nil {
		t.Fatal(err)
	}

	rows := m.collectAgentRows()
	if len(rows) != 1 || !rows[0].chip || rows[0].uuid != "note" {
		t.Fatalf("chip rows = %+v, want one pointing at its host node", rows)
	}
}

// TestAgentPickerAttachesToNode: picking an existing session in /agent retypes
// the node, attaches the session, and names the node after it.
func TestAgentPickerAttachesToNode(t *testing.T) {
	id := claudeStore(t, recSummary, recUser)
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "blank", Name: ""})
	cursorOn(m, "blank")
	m.openAgentPicker(agentPickNode)

	var chosen pickerItem
	for _, it := range (agentStartSource{}).items(m, "") {
		if strings.HasSuffix(it.value, "/"+id) {
			chosen = it
			break
		}
	}
	if chosen.value == "" {
		t.Fatalf("the store's session was not offered: %+v", m.agentStore)
	}
	(agentStartSource{}).onSelect(m, chosen)

	it := m.tree.byUUID["blank"]
	if it.typ != c.key {
		t.Errorf("node type = %q, want %q", it.typ, c.key)
	}
	if it.name != "fix the flaky sync test" {
		t.Errorf("node name = %q, want the session's own title", it.name)
	}
	if s := m.agentLoad("blank"); s.SessionID != id {
		t.Errorf("attached session = %q, want %q", s.SessionID, id)
	}
}

// TestAgentViewShowsToolCalls: alt+e on a session node renders its transcript —
// the header, then the prompts and tool calls.
func TestAgentViewShowsToolCalls(t *testing.T) {
	id := claudeStore(t, recSummary, recUser, recTool, recReply)
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "sess", Name: "flaky test", Type: c.key})
	m.agentSave("sess", agentSession{Variant: c.id, SessionID: id, Cwd: "/home/dev/repo", Runs: 1})
	it := m.tree.byUUID["sess"]

	if !(agentView{}).Enter(m, it) {
		t.Fatal("the session view declined to open")
	}
	bands := (agentView{}).Bands(m, it, "", 200, 0, 20, true)
	if len(bands) < 5 {
		t.Fatalf("view rendered %d lines: %v", len(bands), bands)
	}
	joined := stripSGR(strings.Join(bands, "\n"))
	for _, want := range []string{"Claude Code session", "session " + agentShortID(id), "the sync test is flaky", "→ Bash", "the flake is a clock race"} {
		if !strings.Contains(joined, want) {
			t.Errorf("view is missing %q:\n%s", want, joined)
		}
	}
	if n := (agentView{}).Lines(m, it, 200); n != len(bands) {
		t.Errorf("Lines() = %d, rendered %d", n, len(bands))
	}
}

// TestAgentViewWithoutTranscript: a session lflow cannot read says so instead of
// pretending the conversation is empty.
func TestAgentViewWithoutTranscript(t *testing.T) {
	claudeStore(t, recUser)
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "sess", Name: "new work", Type: c.key})
	it := m.tree.byUUID["sess"]
	if !(agentView{}).Enter(m, it) {
		t.Fatal("the session view declined to open")
	}
	got := stripSGR(strings.Join((agentView{}).Bands(m, it, "", 200, 0, 20, true), "\n"))
	if !strings.Contains(got, "no transcript yet") {
		t.Errorf("empty session view = %q", got)
	}
}

// TestAgentSessionDataStaysLocal: the session pointer lives in local
// node_output, never in the synced node row.
func TestAgentSessionDataStaysLocal(t *testing.T) {
	c := variant(t, "claude")
	m, db := dbModel(t, database.Node{UUID: "sess", Name: "work", Type: c.key})
	m.agentSave("sess", agentSession{Variant: c.id, SessionID: "abc-123", Cwd: "/home/dev/repo"})

	raw, err := database.LoadNodeOutput(db, "sess")
	if err != nil || !strings.Contains(raw, "abc-123") {
		t.Fatalf("session data not in node_output: %q (%v)", raw, err)
	}
	n, err := database.GetNode(db, "sess")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(n.Name, "abc-123") {
		t.Error("the session id leaked into the node row")
	}
}
