package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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
	entries, stats, path := agentTranscript(c.transcriptDirs(), c.exts, id)
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
	if stats.turns != 2 {
		t.Errorf("assistant turns = %d, want 2", stats.turns)
	}
	tool := entries[2]
	if tool.name != "Bash" || !strings.Contains(tool.text, "go test") {
		t.Errorf("tool entry = %+v, want the Bash call and its command", tool)
	}
	if tool.at.IsZero() {
		t.Error("tool entry lost its timestamp")
	}
	if line := stripSGR(agentEntryLine(tool, cCyan)); !strings.Contains(line, "Bash") ||
		!strings.Contains(line, `{"command":"go test ./pkg/tui/..."}`) {
		t.Errorf("tool line = %q, want the tool name and its arguments as JSON", line)
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
	// the row stays quiet: a NAMED node carries no tail at all — the directory,
	// the model, the tokens and the tool calls all live in the expanded panel
	if tail := stripSGR(look.tail); tail != "" {
		t.Errorf("a named session row must carry no tail, got %q", tail)
	}
	// an UNNAMED row falls back to the session's own name so it never reads blank
	it.name = ""
	if tail := stripSGR(m.publishAgentLook("sess", c).tail); !strings.Contains(tail, "fix the flaky sync test") {
		t.Errorf("unnamed row tail = %q, want the session's own name", tail)
	}
	it.name = "flaky test"

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
	for _, want := range []string{"fix the flaky sync test", "dir", "/home/dev/repo",
		"session " + agentShortID(id), "state", "the sync test is flaky", "Bash",
		`{"command":"go test ./pkg/tui/..."}`, "the flake is a clock race"} {
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

// TestAgentStoreFilesSkipsMessages: a store that keeps sessions and their
// messages as sibling trees must not offer a message file as a session.
func TestAgentStoreFilesSkipsMessages(t *testing.T) {
	root := t.TempDir()
	sess := filepath.Join(root, "session", "ses_01.json")
	msg := filepath.Join(root, "message", "ses_01", "msg_0001.json")
	for _, p := range []string{sess, msg} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := agentStoreFiles([]string{root}, []string{".json"}, "/session/")
	if len(got) != 1 || got[0] != sess {
		t.Errorf("discovered %v, want only the session record", got)
	}
	if all := agentStoreFiles([]string{root}, []string{".json"}, ""); len(all) != 2 {
		t.Errorf("unfiltered discovery = %v, want both files", all)
	}
}

// TestAgentMetaPrefersStoreClock: a store that records its own clock is more
// truthful about when a session moved than the record file's mtime, which a copy
// or a sync resets.
func TestAgentMetaPrefersStoreClock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ses_77.json")
	body := `{"id":"ses_77aabbcc","title":"trim the handshake","time":{"created":1750000000000}}`
	if err := os.WriteFile(path, []byte(body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := agentReadMeta("opencode", path)
	if meta.title != "trim the handshake" || meta.id != "ses_77aabbcc" {
		t.Fatalf("meta = %+v", meta)
	}
	if want := time.UnixMilli(1750000000000); !meta.updated.Equal(want) {
		t.Errorf("updated = %v, want the store's clock %v", meta.updated, want)
	}
	if meta.modAt.Equal(meta.updated) {
		t.Error("modAt must stay the file's mtime — it is what the render cache checks")
	}
}

// TestAgentToolDetailPrefersPattern: a search call carries both a pattern and
// the path it searched; the pattern is what says what the call was for.
func TestAgentToolDetailPrefersPattern(t *testing.T) {
	got := agentToolDetail(map[string]any{"pattern": "flushSync", "path": "pkg/tui/editor"})
	if got != "flushSync" {
		t.Errorf("grep detail = %q, want the pattern", got)
	}
	if got := agentToolDetail(map[string]any{"file_path": "livesync.go"}); got != "livesync.go" {
		t.Errorf("read detail = %q", got)
	}
}

// TestAgentOpenRefusesGoneCwd: a session pinned to a directory that no longer
// exists refuses to open rather than running the agent somewhere else.
func TestAgentOpenRefusesGoneCwd(t *testing.T) {
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "sess", Name: "work", Type: c.key})
	gone := filepath.Join(t.TempDir(), "removed-repo")
	m.agentSave("sess", agentSession{Variant: c.id, SessionID: "abc-123", Cwd: gone, Runs: 1})

	if cmd := m.agentOpen(c, "sess", gone); cmd != nil {
		t.Error("a session whose directory is gone must not launch the CLI")
	}
	if !strings.Contains(m.flash, "is gone") {
		t.Errorf("flash = %q, want it to name the missing directory", m.flash)
	}
}

// TestAgentsListSeesUnflushedEdits: /agents reads the database, so a session
// started seconds ago must still be in the index that claims to list them all.
func TestAgentsListSeesUnflushedEdits(t *testing.T) {
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "blank", Name: "just typed"})
	m.tree.byUUID["blank"].typ = c.key // an in-memory retype, not yet saved
	m.unsaved = true
	m.agentSave("blank", agentSession{Variant: c.id, SessionID: "abc-123"})

	m.openAgentsList()
	if len(m.agentRows) != 1 || m.agentRows[0].uuid != "blank" {
		t.Fatalf("/agents rows = %+v, want the just-started session", m.agentRows)
	}
}

// TestAgentChipPill: the chip is a filled pill — the agent's glyph and the
// session's NAME on the session's color, ink near-black on top.
func TestAgentChipPill(t *testing.T) {
	id := claudeStore(t, recSummary, recUser)
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "notes "})
	cursorOn(m, "note")
	cur := m.tree.byUUID["note"]
	m.caret = len([]rune(cur.name))
	m.insertAgentChip(cur, c, agentStoreSession{variant: c.id, id: id, title: "fix the flaky sync test"})
	chip, _ := m.agentChipAtCaret(cur)
	m.refreshAgentLooks()

	pill := renderAgentChip(m.chips[chip.ID], false)
	if !strings.Contains(pill, cChipInk) {
		t.Error("the pill must write in the chip ink")
	}
	if !strings.Contains(pill, "\x1b[48;2;") {
		t.Errorf("the pill must be FILLED with the session's color: %q", pill)
	}
	if !strings.Contains(stripSGR(pill), "✳ fix the flaky sync test") {
		t.Errorf("pill reads %q, want the glyph and the session's name", stripSGR(pill))
	}
	// the CLI's store gave this session a color: the pill is filled with it
	if !strings.Contains(pill, "\x1b[48;2;78;201;176m") {
		t.Errorf("pill = %q, want the session's own #4ec9b0 fill", pill)
	}
}

// TestAgentChipCustomName: like a link chip, a session can be given the user's
// own name — and clearing it hands the name back to the CLI.
func TestAgentChipCustomName(t *testing.T) {
	id := claudeStore(t, recSummary, recUser)
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "notes "})
	cursorOn(m, "note")
	cur := m.tree.byUUID["note"]
	m.caret = len([]rune(cur.name))
	m.insertAgentChip(cur, c, agentStoreSession{variant: c.id, id: id, title: "fix the flaky sync test"})
	chip, _ := m.agentChipAtCaret(cur)

	m.agentRename(chip.ID, "flush fix")
	if got := displayAnchors(cur.name, m.chips); !strings.Contains(got, "✳ flush fix") {
		t.Errorf("renamed chip reads %q", got)
	}
	if s := m.agentLoad(chip.ID); s.Name != "flush fix" {
		t.Errorf("stored name = %q", s.Name)
	}
	m.agentRename(chip.ID, "")
	if got := displayAnchors(cur.name, m.chips); !strings.Contains(got, "✳ fix the flaky sync test") {
		t.Errorf("cleared name reads %q, want the CLI's own title back", got)
	}
}

// TestAgentRenameInPanel: n opens the rename field in the expanded panel, enter
// commits it, esc leaves the name alone.
func TestAgentRenameInPanel(t *testing.T) {
	id := claudeStore(t, recSummary, recUser)
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "sess", Name: "work", Type: c.key})
	m.agentSave("sess", agentSession{Variant: c.id, SessionID: id})
	it := m.tree.byUUID["sess"]
	(agentView{}).Enter(m, it)

	(agentView{}).Key(m, it, key("n"))
	if m.agentRenameState("sess") == nil {
		t.Fatal("n did not open the rename field")
	}
	for _, r := range "retry fix" {
		(agentView{}).Key(m, it, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	(agentView{}).Key(m, it, key("enter"))
	if s := m.agentLoad("sess"); !strings.HasSuffix(s.Name, "retry fix") {
		t.Fatalf("committed name = %q", s.Name)
	}
	if m.agentRenameState("sess") != nil {
		t.Error("the field must close on enter")
	}

	(agentView{}).Key(m, it, key("n"))
	(agentView{}).Key(m, it, key("esc"))
	if m.agentRenameState("sess") != nil {
		t.Error("esc must close the field")
	}
}

// TestAgentStats: the panel's numbers come from the session's own records —
// model, branch, turns and the tokens it spent.
func TestAgentStats(t *testing.T) {
	rec := `{"type":"assistant","timestamp":"2026-07-24T10:03:04Z","cwd":"/home/dev/repo",` +
		`"gitBranch":"claude/agent-sessions","effort":"high","message":{"role":"assistant",` +
		`"model":"claude-opus-5","usage":{"input_tokens":1200,"output_tokens":340,` +
		`"cache_read_input_tokens":46780},"content":[{"type":"text","text":"done"}]}}`
	id := claudeStore(t, recSummary, recUser, rec, rec)
	c := variant(t, "claude")
	_, stats, _ := agentTranscript(c.transcriptDirs(), c.exts, id)

	if stats.model != "claude-opus-5" || stats.branch != "claude/agent-sessions" || stats.effort != "high" {
		t.Errorf("stats = %+v", stats)
	}
	if stats.inTok != 2400 || stats.outTok != 680 || stats.cacheTok != 93560 {
		t.Errorf("tokens = %d in / %d out / %d cached", stats.inTok, stats.outTok, stats.cacheTok)
	}
	if stats.turns != 2 {
		t.Errorf("turns = %d", stats.turns)
	}
	if got := stripSGR(agentTokenLine(stats)); got != "2.4k in · 680 out · 93.6k cached" {
		t.Errorf("token line = %q", got)
	}
}

// TestAgentCloudMark: a session the CLI records as started from the web wears
// the cloud mark on its chip and reports itself hosted.
func TestAgentCloudMark(t *testing.T) {
	id := claudeStore(t, recSummary, recUser)
	home := os.Getenv("HOME")
	reg := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(reg, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"pid":1,"sessionId":"` + id + `","cwd":"/home/dev/repo","name":"lflow-c1",` +
		`"kind":"interactive","entrypoint":"remote_mobile"}`
	if err := os.WriteFile(filepath.Join(reg, "1.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "notes "})
	cursorOn(m, "note")
	cur := m.tree.byUUID["note"]
	m.caret = len([]rune(cur.name))
	m.insertAgentChip(cur, c, agentStoreSession{variant: c.id, id: id})

	s := m.agentLoad(agentOnlyChipID(m))
	if !m.agentCloud(c, s) {
		t.Error("a session started from a phone must read as hosted")
	}
	if got := displayAnchors(cur.name, m.chips); !strings.Contains(got, glyphCloud) {
		t.Errorf("chip reads %q, want the cloud mark", got)
	}
	// the CLI's own name for the session wins over the store's summary
	if !strings.Contains(displayAnchors(cur.name, m.chips), "lflow-c1") {
		t.Errorf("chip reads %q, want the name the CLI gave the session", displayAnchors(cur.name, m.chips))
	}
}

// agentOnlyChipID returns the single agent chip in the model (test helper).
func agentOnlyChipID(m *Model) string {
	for id, c := range m.chips {
		if c.Kind == chipKindAgent {
			return id
		}
	}
	return ""
}
