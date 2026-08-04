package editor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

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

// claudeStore seeds a sandboxed HOME with a Claude Code store holding one
// session, and returns the session id.
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
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"),
		[]byte(strings.Join(records, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return id
}

// claudeRegistry seeds the live-session registry the CLI writes while a session
// runs — where lflow reads a session's own name, and whether it is hosted.
func claudeRegistry(t *testing.T, id, name, entrypoint string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".claude", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"pid":1,"sessionId":"` + id + `","cwd":"/home/dev/repo","name":"` + name +
		`","kind":"interactive","entrypoint":"` + entrypoint + `"}`
	if err := os.WriteFile(filepath.Join(dir, "1.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const (
	recSummary = `{"type":"summary","summary":"fix the flaky sync test","color":"#4ec9b0"}`
	// the same session without the CLI's own color — the variant's theming stands
	recPlain = `{"type":"summary","summary":"fix the flaky sync test"}`
	recUser  = `{"type":"user","timestamp":"2026-07-24T10:03:00Z","cwd":"/home/dev/repo","message":{"role":"user","content":[{"type":"text","text":"the sync test is flaky"}]}}`
)

// chipOn drops a session chip at the end of a node and returns it.
func chipOn(t *testing.T, m *Model, uuid string, v agentVariant, attach agentStoreSession) database.Chip {
	t.Helper()
	cursorOn(m, uuid)
	cur := m.tree.byUUID[uuid]
	m.caret = len([]rune(cur.name))
	m.insertAgentChip(cur, v, attach)
	c, ok := m.agentChipAtCaret(cur)
	if !ok {
		t.Fatal("no session chip at the caret")
	}
	return c
}

// TestAgentVariantsRegistered: every CLI lflow can host is declared with the one
// binary it shells out to, its own mark and its own color.
func TestAgentVariantsRegistered(t *testing.T) {
	want := map[string]string{"claude": "✽", "pi": "ᴘɪ", "opencode": "▣"}
	if len(agentVariants) != len(want) {
		t.Fatalf("registry has %d variants, want just the three CLIs", len(agentVariants))
	}
	for id, glyph := range want {
		v := variant(t, id)
		if v.glyph != glyph {
			t.Errorf("%s glyph = %q, want %q", id, v.glyph, glyph)
		}
		if v.colorSGR() == "" || v.args == nil || v.bin == "" {
			t.Errorf("%s is under-declared: %+v", id, v)
		}
	}
	// Sessions have exactly two surfaces: the inline chip and one whole-row Agent
	// node whose trace is virtual rather than persisted as children.
	if nt := typeOf(database.TypeAgent); nt.key != database.TypeAgent || nt.view == nil || nt.run == nil {
		t.Errorf("Agent node is under-declared: %+v", nt)
	}
	// every agent wears its own MARK — that is what tells two chips apart. Color
	// can collide (the palette is smaller than the registry) but must never be the
	// gray a DONE session fills with.
	seen := map[string]string{}
	for _, v := range agentVariants {
		if other, dup := seen[v.glyph]; dup {
			t.Errorf("%s and %s share the mark %q", v.id, other, v.glyph)
		}
		seen[v.glyph] = v.id
		if v.color == "gray" {
			t.Errorf("%s wears the done-gray fill", v.id)
		}
	}
}

// TestAgentMonoVariants: an agent whose mark is black on white has no brand color
// to wear, and lflow does not invent one — Codex, Grok, OpenCode and T3 Code all
// share the mono fill and are told apart by their glyphs. The pill IS that mark:
// a black fill, and the white ink contrastInk arrives at on its own.
func TestAgentMonoVariants(t *testing.T) {
	mono := map[string]bool{"opencode": true}
	for id := range mono {
		v := variant(t, id)
		if got := v.colorSGR(); got != "\x1b[38;2;0;0;0m" {
			t.Errorf("%s fill = %q, want the black truecolor sequence", id, got)
		}
		if got := contrastInk(v.colorSGR()); got != cInkLight {
			t.Errorf("ink on %s = %q, want white on the black fill", id, got)
		}
	}
	// the agents that DO have a color keep it, and it stays a themed swatch
	for _, v := range agentVariants {
		if mono[v.id] {
			continue
		}
		if _, ok := styleColorCode[v.color]; !ok {
			t.Errorf("%s color %q is not a themed swatch", v.id, v.color)
		}
	}
	// a literal is not a special case: every variant resolves to a real fill
	for _, v := range agentVariants {
		if bgOf(v.colorSGR()) == "" {
			t.Errorf("%s resolves to no fill at all: color %q", v.id, v.color)
		}
	}
}

// TestAgentVariantArgs: every launch RESUMES. lflow never starts a session, so
// each variant has exactly one command line and it always carries an id.
func TestAgentVariantArgs(t *testing.T) {
	want := map[string]string{
		"claude":   "--resume abc-123",
		"pi":       "--session-id abc-123",
		"opencode": "--session abc-123",
	}
	for id, argv := range want {
		if got := strings.Join(variant(t, id).args("abc-123"), " "); got != argv {
			t.Errorf("%s args = %q, want %q", id, got, argv)
		}
	}
}

// TestAgentReadMeta reads a session's identity out of the CLI's own store: its
// name, its directory, and the color the CLI stamped on it.
func TestAgentReadMeta(t *testing.T) {
	id := claudeStore(t, recSummary, recUser)
	c := variant(t, "claude")
	path := agentSessionPath(c.sessionDirs(), c.exts, id)
	if path == "" {
		t.Fatal("session not located in the store")
	}
	meta := agentReadMeta(c.id, path)
	if meta.title != "fix the flaky sync test" {
		t.Errorf("title = %q", meta.title)
	}
	if meta.cwd != "/home/dev/repo" {
		t.Errorf("cwd = %q", meta.cwd)
	}
}

// TestAgentReadMetaFallsBackToFirstPrompt: an untitled session is named by what
// was asked of it.
func TestAgentReadMetaFallsBackToFirstPrompt(t *testing.T) {
	id := claudeStore(t, recUser)
	c := variant(t, "claude")
	meta := agentReadMeta(c.id, agentSessionPath(c.sessionDirs(), c.exts, id))
	if meta.title != "the sync test is flaky" {
		t.Errorf("title = %q, want the first prompt", meta.title)
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
	if got := agentStoreFiles([]string{root}, []string{".json"}, "/session/"); len(got) != 1 || got[0] != sess {
		t.Errorf("discovered %v, want only the session record", got)
	}
	if all := agentStoreFiles([]string{root}, []string{".json"}, ""); len(all) != 2 {
		t.Errorf("unfiltered discovery = %v, want both files", all)
	}
}

// TestAgentMetaPrefersStoreClock: a store that records its own clock is more
// truthful about when a session moved than the file's mtime, which a copy resets.
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

// TestContrastInk: a filled pill writes in whatever ink contrasts with the
// fill — dark on Claude Code's orange, light on a dark session color.
func TestContrastInk(t *testing.T) {
	if got := contrastInk(styleColorCode["orange"]); got != cInkDark {
		t.Errorf("ink on orange = %q, want dark", got)
	}
	// every fill in the palette is one an eye reads as light: none of them may
	// end up wearing white ink. Grok's red is the one that used to.
	for name, code := range styleColorCode {
		if got := contrastInk(code); got != cInkDark {
			t.Errorf("ink on %s = %q, want dark — the mark washes out otherwise", name, got)
		}
	}
	if got := contrastInk("\x1b[38;2;20;30;90m"); got != cInkLight {
		t.Errorf("ink on a dark navy = %q, want light", got)
	}
	if got := contrastInk("not an sgr"); got != cInkDark {
		t.Errorf("unreadable fill = %q, want the dark default", got)
	}
}

// TestAgentChipPill: the chip is a filled pill — the agent's glyph and the
// session's name on the session's color, in contrasting ink.
func TestAgentChipPill(t *testing.T) {
	id := claudeStore(t, recSummary, recUser)
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "notes "})
	chip := chipOn(t, m, "note", c, agentStoreSession{variant: c.id, id: id, title: "fix the flaky sync test"})

	pill := renderAgentChip(m.chips[chip.ID], false, false)
	if !strings.Contains(pill, cInkDark) {
		t.Error("a light fill must be written in dark ink")
	}
	// this session carries a color of the CLI's OWN (recSummary), so the chip
	// wears it rather than the variant's default — recoloring a session inside
	// the CLI reads recolored here
	if !strings.Contains(pill, bgOf(agentColorSGR("#4ec9b0"))) {
		t.Errorf("pill = %q, want the color the CLI gave the session", pill)
	}
	if got := stripSGR(pill); got != " ✽ fix the flaky sync test " {
		t.Errorf("pill reads %q, want the glyph and the session's name", got)
	}

	// a session the CLI gave no color falls back to Claude Code's own fill
	id2 := claudeStore(t, recPlain, recUser)
	chip2 := chipOn(t, m, "note", c, agentStoreSession{variant: c.id, id: id2, title: "fix the flaky sync test"})
	if pill := renderAgentChip(m.chips[chip2.ID], false, false); !strings.Contains(pill, bgOf(c.colorSGR())) {
		t.Errorf("uncolored session pill = %q, want Claude Code's own fill", pill)
	}
}

// TestAgentChipCustomName: like a link chip, a session can be given the user's
// own name — and the name reaches the CLI's own store, so clearing the local one
// finds the CLI already calling the session that.
func TestAgentChipCustomName(t *testing.T) {
	id := claudeStore(t, recSummary, recUser)
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "notes "})
	chip := chipOn(t, m, "note", c, agentStoreSession{variant: c.id, id: id, title: "fix the flaky sync test"})
	cur := m.tree.byUUID["note"]

	m.agentRename(chip.ID, "flush fix")
	if got := displayAnchors(cur.name, m.chips); !strings.Contains(got, "✽ flush fix") {
		t.Errorf("renamed chip reads %q", got)
	}
	if s := m.agentLoad(chip.ID); s.Name != "flush fix" {
		t.Errorf("stored name = %q", s.Name)
	}
	// the rename went DOWN into Claude Code's own store, so the session is
	// called that there too
	path := agentSessionPath(c.sessionDirs(), c.exts, id)
	if meta := agentReadMeta(c.id, path); meta.title != "flush fix" {
		t.Errorf("the CLI's store reads %q, want the rename to have reached it", meta.title)
	}
	// clearing the local name hands the name back to the CLI — which now agrees
	m.agentRename(chip.ID, "")
	if got := displayAnchors(cur.name, m.chips); !strings.Contains(got, "✽ flush fix") {
		t.Errorf("cleared name reads %q, want the CLI's own name", got)
	}

	// the label is session chrome, never a stored chip field
	stored, err := database.LoadChips(m.db)
	if err != nil {
		t.Fatal(err)
	}
	if stored[chip.ID].Label != "" {
		t.Errorf("chip row carries a label %q — the name is local chrome", stored[chip.ID].Label)
	}
}

// TestAgentChipTracksSessionName: the chip's text IS the session's live name, so
// a session renamed inside its CLI reads renamed in the outline too.
func TestAgentChipTracksSessionName(t *testing.T) {
	id := claudeStore(t, recSummary, recUser)
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "notes "})
	chip := chipOn(t, m, "note", c, agentStoreSession{variant: c.id, id: id, title: "fix the flaky sync test"})
	cur := m.tree.byUUID["note"]

	path := agentSessionPath(c.sessionDirs(), c.exts, id)
	body := `{"type":"summary","summary":"land the retry fix"}` + "\n" + recUser + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	delete(m.nodeStore(chip.ID), "agentMeta")
	m.refreshAgentChip(chip.ID)
	if got := displayAnchors(cur.name, m.chips); !strings.Contains(got, "✽ land the retry fix") {
		t.Errorf("renamed session reads %q", got)
	}
}

// TestAgentAttachExistingSession: an existing session is filed onto a chip —
// its id, name and directory come along, and the next open resumes it.
func TestAgentAttachExistingSession(t *testing.T) {
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "notes "})
	chip := chipOn(t, m, "note", c, agentStoreSession{
		variant: c.id, id: "abc-123", title: "port the parser", cwd: "/home/dev/repo", updated: time.Now(),
	})
	s := m.agentLoad(chip.ID)
	if s.SessionID != "abc-123" || s.Title != "port the parser" || s.Cwd != "/home/dev/repo" {
		t.Fatalf("attached session = %+v", s)
	}
	if got := strings.Join(c.args(s.SessionID), " "); got != "--resume abc-123" {
		t.Errorf("next open = %q, want a resume", got)
	}
}

// TestAgentOpenRefusesGoneCwd: a session pinned to a directory that no longer
// exists refuses to open rather than running the agent somewhere else.
func TestAgentOpenRefusesGoneCwd(t *testing.T) {
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "notes "})
	chip := chipOn(t, m, "note", c, agentStoreSession{})
	gone := filepath.Join(t.TempDir(), "removed-repo")
	m.agentSave(chip.ID, agentSession{Variant: c.id, SessionID: "abc-123", Cwd: gone})

	if cmd := m.agentOpen(c, chip.ID, gone); cmd != nil {
		t.Error("a session whose directory is gone must not launch the CLI")
	}
	if !strings.Contains(m.flash, "is gone") {
		t.Errorf("flash = %q, want it to name the missing directory", m.flash)
	}
}

// TestAgentPanelShowsTrace: alt+e shows the identity plus virtual transcript
// rows read from the CLI store. Nothing from those rows is copied into nodes.
func TestAgentPanelShowsTrace(t *testing.T) {
	id := claudeStore(t, recSummary, recUser)
	claudeRegistry(t, id, "flush-resume", "cli")
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "notes "})
	chip := chipOn(t, m, "note", c, agentStoreSession{variant: c.id, id: id, cwd: "/home/dev/repo"})
	m.focusAgentChip(chip)

	bands := (agentChipView{}).Bands(m, m.tree.byUUID["note"], "", 200, 0, 20, true)
	joined := stripSGR(strings.Join(bands, "\n"))
	for _, want := range []string{"✽ flush-resume", "/home/dev/repo", "traces · 1 events", "◆ you", "the sync test is flaky", "alt+r open", "alt+n rename", "alt+c color"} {
		if !strings.Contains(joined, want) {
			t.Errorf("panel is missing %q:\n%s", want, joined)
		}
	}
	// Nothing is invented from incomplete cross-CLI metadata.
	for _, unwanted := range []string{"idle", "opened", "live", "tokens", "turns", "branch", "model"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("panel says %q — it does not know that:\n%s", unwanted, joined)
		}
	}
}

func TestAgentTraceReadsAssistantTools(t *testing.T) {
	rec := map[string]any{
		"type": "assistant",
		"message": map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "I will inspect it."},
			map[string]any{"type": "tool_use", "name": "read", "input": map[string]any{"path": "main.go"}},
		}},
	}
	got := agentRecordTrace(rec)
	if len(got) != 2 || got[0].kind != "assistant" || got[1].kind != "tool" ||
		!strings.Contains(got[1].text, "main.go") {
		t.Fatalf("assistant trace = %#v", got)
	}
}

// Pi writes camelCase toolCall/toolResult records; both belong in the trace.
func TestAgentTraceReadsPiToolRecords(t *testing.T) {
	call := map[string]any{"type": "message", "message": map[string]any{
		"role": "assistant", "content": []any{map[string]any{
			"type": "toolCall", "name": "bash", "arguments": map[string]any{"command": "go test ./..."},
		}},
	}}
	got := agentRecordTrace(call)
	if len(got) != 1 || got[0].kind != "tool" || !strings.Contains(got[0].text, "go test ./...") {
		t.Fatalf("Pi tool call trace = %#v", got)
	}
	result := map[string]any{"type": "message", "message": map[string]any{
		"role": "toolResult", "content": []any{map[string]any{"type": "text", "text": "ok"}},
	}}
	got = agentRecordTrace(result)
	if len(got) != 1 || got[0].kind != "result" || got[0].text != "ok" {
		t.Fatalf("Pi tool result trace = %#v", got)
	}
}

// Pi's filename is timestamp_<session-id>.jsonl even though the pointer stored
// on a chip/node is the session id from the first JSONL record.
func TestAgentTraceFindsTimestampPrefixedPiTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	id := "019eb6ff-caef-78d0-8a2a-93e6ddd1f826"
	dir := filepath.Join(home, ".pi", "agent", "sessions", "--home-dev-repo--")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"session","id":"` + id + `","cwd":"/home/dev/repo"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"show the trace"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "2026-06-11T14-04-37-487Z_"+id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	v := variant(t, "pi")
	got := readAgentTrace(v, agentSession{Variant: "pi", SessionID: id})
	if len(got) != 1 || got[0].kind != "user" || got[0].text != "show the trace" {
		t.Fatalf("Pi timestamp-prefixed trace = %#v", got)
	}
}

func TestAgentNodeBindsLocalVirtualTrace(t *testing.T) {
	id := claudeStore(t, recSummary, recUser)
	c := variant(t, "claude")
	m, db := dbModel(t, database.Node{UUID: "session", Name: "file this"})
	m.bindAgentNode("session", c, agentStoreSession{variant: c.id, id: id, title: "fix the flaky sync test", cwd: "/home/dev/repo"})

	it := m.tree.byUUID["session"]
	if it.typ != database.TypeAgent || it.name != "fix the flaky sync test" {
		t.Fatalf("bound node = type %q name %q", it.typ, it.name)
	}
	if acceptsChildren(it) {
		t.Fatal("an Agent node accepted real children; its trace must stay virtual")
	}
	if !(agentNodeView{}).Enter(m, it) {
		t.Fatal("bound Agent node refused its trace view")
	}
	joined := stripSGR(strings.Join((agentNodeView{}).Bands(m, it, "", 120, 0, 20, true), "\n"))
	if !strings.Contains(joined, "the sync test is flaky") {
		t.Fatalf("Agent node trace missing local transcript:\n%s", joined)
	}
	raw, err := database.LoadNodeOutput(db, "session")
	if err != nil || !strings.Contains(raw, id) {
		t.Fatalf("local session pointer = %q (%v)", raw, err)
	}
	if len(it.children) != 0 {
		t.Fatalf("trace leaked into %d outline children", len(it.children))
	}
}

// TestAgentPanelWrapsLongName: a session named by its first prompt is a
// sentence. The panel shows all of it, wrapped, because clipping is what makes
// two sessions look alike.
func TestAgentPanelWrapsLongName(t *testing.T) {
	long := "add a retry backoff to the sync flush so a dropped feed reconnects and resyncs wholesale"
	id := claudeStore(t, `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"`+long+`"}]}}`)
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "notes "})
	chip := chipOn(t, m, "note", c, agentStoreSession{variant: c.id, id: id, cwd: "/home/dev/repo"})
	m.focusAgentChip(chip)

	bands := (agentChipView{}).Bands(m, m.tree.byUUID["note"], "", 48, 0, 20, true)
	joined := stripSGR(strings.Join(bands, "\n"))
	if len(bands) < 4 {
		t.Fatalf("a long name did not wrap; panel is %d lines:\n%s", len(bands), joined)
	}
	flat := strings.Join(strings.Fields(joined), " ")
	if !strings.Contains(flat, long) {
		t.Errorf("the name was clipped instead of wrapped:\n%s", joined)
	}
	for _, b := range bands {
		if w := runewidth.StringWidth(stripSGR(b)); w > 48 {
			t.Errorf("line is %d wide, over the panel's 48:\n%s", w, stripSGR(b))
		}
	}
}

// TestAgentRenameInPanel: n opens the rename field, enter commits it, esc leaves
// the name alone.
func TestAgentRenameInPanel(t *testing.T) {
	id := claudeStore(t, recSummary, recUser)
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "notes "})
	chip := chipOn(t, m, "note", c, agentStoreSession{variant: c.id, id: id})
	m.focusAgentChip(chip)
	it := m.tree.byUUID["note"]

	(agentChipView{}).Key(m, it, key("n"))
	if m.agentRenameState(chip.ID) == nil {
		t.Fatal("n did not open the rename field")
	}
	for _, r := range "retry fix" {
		(agentChipView{}).Key(m, it, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	(agentChipView{}).Key(m, it, key("enter"))
	if s := m.agentLoad(chip.ID); !strings.HasSuffix(s.Name, "retry fix") {
		t.Fatalf("committed name = %q", s.Name)
	}
	if m.agentRenameState(chip.ID) != nil {
		t.Error("the field must close on enter")
	}

	(agentChipView{}).Key(m, it, key("n"))
	(agentChipView{}).Key(m, it, key("esc"))
	if m.agentRenameState(chip.ID) != nil {
		t.Error("esc must close the field")
	}
}

// TestAgentSessionDataStaysLocal: the session pointer lives in local
// node_output, never in the synced chip or node row.
func TestAgentSessionDataStaysLocal(t *testing.T) {
	c := variant(t, "claude")
	m, db := dbModel(t, database.Node{UUID: "note", Name: "notes "})
	chip := chipOn(t, m, "note", c, agentStoreSession{})
	m.agentSave(chip.ID, agentSession{Variant: c.id, SessionID: "abc-123", Cwd: "/home/dev/repo"})
	if _, err := m.tree.save(); err != nil {
		t.Fatal(err)
	}

	raw, err := database.LoadNodeOutput(db, chip.ID)
	if err != nil || !strings.Contains(raw, "abc-123") {
		t.Fatalf("session data not in node_output: %q (%v)", raw, err)
	}
	n, err := database.GetNode(db, "note")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(n.Name, "abc-123") {
		t.Error("the session id leaked into the node row")
	}
	stored, err := database.LoadChips(db)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored[chip.ID].Value, "abc-123") {
		t.Error("the session id leaked into the chip row")
	}
}

// TestAgentRenameReplaces: n on a session still wearing the CLI's own name opens
// an EMPTY field, so typing replaces the name instead of appending to it.
func TestAgentRenameReplaces(t *testing.T) {
	id := claudeStore(t, recSummary, recUser)
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "notes "})
	chip := chipOn(t, m, "note", c, agentStoreSession{variant: c.id, id: id})
	m.focusAgentChip(chip)
	it := m.tree.byUUID["note"]

	(agentChipView{}).Key(m, it, key("n"))
	if f := m.agentRenameState(chip.ID); f == nil || f.value != "" {
		t.Fatalf("the field opened with %q, want it empty", f.value)
	}
	for _, r := range "flush fix" {
		(agentChipView{}).Key(m, it, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	(agentChipView{}).Key(m, it, key("enter"))
	if s := m.agentLoad(chip.ID); s.Name != "flush fix" {
		t.Fatalf("name = %q, want the typed name alone", s.Name)
	}
	// a session you already named opens with that name, ready to edit
	(agentChipView{}).Key(m, it, key("n"))
	if f := m.agentRenameState(chip.ID); f == nil || f.value != "flush fix" {
		t.Errorf("re-opening the field gave %q, want the custom name", f.value)
	}
}

// TestAgentPickerRows: /agent is a SEARCH over the sessions the CLIs already
// have. There is no "new session" row — lflow never starts one — and a row is the
// pill it is about to become, then where it ran and when.
func TestAgentPickerRows(t *testing.T) {
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "notes "})
	cursorOn(m, "note")
	m.agentStore = []agentStoreSession{{
		variant: c.id, id: "abc-12345678", title: "flush fix",
		cwd: "/home/dev/lflow", updated: time.Now().Add(-2 * time.Hour),
	}, {
		variant: "opencode", id: "ses_9", title: "rank math",
		cwd: "/home/dev/lflow", updated: time.Now().Add(-time.Hour),
	}}

	rows := (agentStartSource{}).items(m, "")
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want one per session and nothing else", len(rows))
	}
	for _, it := range rows {
		if !strings.HasPrefix(it.value, "use/") {
			t.Errorf("row %q is not an attach — the picker has no create path", it.value)
		}
	}
	if got := stripSGR(rows[0].render(false)); got != " ✽ flush fix  · /home/dev/lflow · 2h ago" {
		t.Errorf("row = %q, want chip · dir · date", got)
	}
	if !strings.Contains(rows[0].render(false), bgOf(c.colorSGR())) {
		t.Error("a session must be drawn as the pill it becomes")
	}
	// searching by NAME is the point: it is how you find a session again
	if hits := (agentStartSource{}).items(m, "rank"); len(hits) != 1 ||
		!strings.Contains(stripSGR(hits[0].render(false)), "rank math") {
		t.Errorf("name search found %d rows, want just the opencode one", len(hits))
	}
	if hits := (agentStartSource{}).items(m, "opencode"); len(hits) != 1 {
		t.Errorf("agent search found %d rows, want one", len(hits))
	}
}

// TestAgentChipStrikesWhenDone: a completed row strikes through its words, and
// the pill is one of those words. The chip keeps its color — a finished session
// is still the agent it was — so the line is what reads as done, drawn in the
// dark ink the fill already contrasts with.
func TestAgentChipStrikesWhenDone(t *testing.T) {
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "ship it "})
	chip := chipOn(t, m, "note", c, agentStoreSession{variant: c.id, id: "abc-12345678", title: "flush fix"})

	it := m.tree.byUUID["note"]
	if got := renderBody(it, it.name, -1, false, m.chips); strings.Contains(got, cStrike) {
		t.Fatalf("an open row must not strike anything: %q", got)
	}
	it.completedAt = time.Now().UnixNano()
	done := renderBody(it, it.name, -1, false, m.chips)
	pill := renderAgentChip(m.chips[chip.ID], false, true)
	if !strings.Contains(done, pill) {
		t.Errorf("the chip in a done row is not struck through:\n got %q\nwant %q inside", done, pill)
	}
	// struck, but still filled and still in dark ink — not greyed out
	if !strings.Contains(pill, cStrike) || !strings.Contains(pill, cInkDark) ||
		!strings.Contains(pill, bgOf(c.colorSGR())) {
		t.Errorf("a done pill = %q, want the strike over its own fill in dark ink", pill)
	}
}

// TestAgentPromptTextSkipsInjectedBlocks: a CLI's harness writes caveats, hook
// output and slash-command echoes into the user turn. They are not what anyone
// typed, so they must never become a session's name — a chip reading
// "<system-reminder> The user named this s…" is a name nobody can search for.
func TestAgentPromptTextSkipsInjectedBlocks(t *testing.T) {
	junk := []string{
		"<local-command-caveat>Caveat: The messages below were generated…</local-command-caveat>",
		"<command-name>/model</command-name>\n<command-message>model</command-message>",
		"<task-notification>\n<task-id>abc</task-id>\n</task-notification>",
		"<system-reminder>The user named this session</system-reminder>",
		"<some-future-wrapper>whatever it holds",
	}
	for _, j := range junk {
		if got := agentPromptText(j); got != "" {
			t.Errorf("agentPromptText(%.40q) = %q, want it skipped", j, got)
		}
	}
	// a wrapper in FRONT of a real prompt leaves the prompt
	if got := agentPromptText("<system-reminder>noise</system-reminder>\n\nport the parser"); got != "port the parser" {
		t.Errorf("prompt behind a wrapper = %q", got)
	}
	if got := agentPromptText("  make the sync flush resumable  "); got != "make the sync flush resumable" {
		t.Errorf("plain prompt = %q", got)
	}
}

// TestAgentReadMetaSkipsToARealPrompt: a transcript whose first user records are
// all harness noise is named by the first thing the user actually asked.
func TestAgentReadMetaSkipsToARealPrompt(t *testing.T) {
	noise := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"<local-command-caveat>Caveat: blah</local-command-caveat>"}]}}`
	real := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"add a retry backoff"}]}}`
	id := claudeStore(t, noise, real)
	c := variant(t, "claude")
	meta := agentReadMeta(c.id, agentSessionPath(c.sessionDirs(), c.exts, id))
	if meta.title != "add a retry backoff" {
		t.Errorf("title = %q, want the first real prompt", meta.title)
	}
}

// TestAgentReadMetaTakesTheSessionID: every record in a JSONL transcript carries
// its own id — pi stamps one on each message — so the session id must be taken
// once, from the session's own record, and never overwritten by a later message.
// Resuming with a message id resumes nothing.
func TestAgentReadMetaTakesTheSessionID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "2026-04-13T10-07-26-229Z_2df7f7da-071f-4b6c-94bc-283217a48518.jsonl")
	body := `{"type":"session","version":3,"id":"2df7f7da-071f-4b6c-94bc-283217a48518","cwd":"/home/dev/boing"}
{"type":"model_change","id":"a10f8e84"}
{"type":"thinking_level_change","id":"fbc6811b"}
{"type":"message","id":"953acb8f","role":"user","content":"add a widget"}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := agentReadMeta("pi", path)
	if meta.id != "2df7f7da-071f-4b6c-94bc-283217a48518" {
		t.Errorf("session id = %q, want the session record's own id", meta.id)
	}
	if meta.cwd != "/home/dev/boing" {
		t.Errorf("cwd = %q", meta.cwd)
	}

	// an explicit sessionId key beats a bare id, wherever it appears
	p2 := filepath.Join(dir, "x.jsonl")
	if err := os.WriteFile(p2, []byte(
		`{"id":"aaaaaaaa-1111","type":"meta"}`+"\n"+
			`{"sessionId":"bbbbbbbb-2222","type":"user"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := agentReadMeta("claude", p2).id; got != "bbbbbbbb-2222" {
		t.Errorf("id = %q, want the explicit sessionId", got)
	}
}

// TestAgentHasNoSlashCommands: a session chip is one of the things /insert
// splices, and that is the only way in. Neither /agent nor /agents exists — a
// chip belongs to the row it sits in, and the outline is already the index.
func TestAgentHasNoSlashCommands(t *testing.T) {
	for _, c := range slashCommands {
		if c.name == "/agent" || c.name == "/agents" {
			t.Errorf("%s is back; the chip belongs to /insert alone", c.name)
		}
	}
	found := false
	for _, k := range insertKinds {
		if k.value == "agent" {
			found = true
		}
	}
	if !found {
		t.Fatal("/insert no longer offers a session chip — there is now no way in")
	}
}

// TestAgentKeysFindTheChipOnAWrappedRow: a row wide enough to wrap puts End on
// the end of a visual line, not after the chip, so the exact-caret test misses.
// The keys still find the row's only session; a row with several says so rather
// than guessing.
func TestAgentKeysFindTheChipOnAWrappedRow(t *testing.T) {
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "notes "})
	chip := chipOn(t, m, "note", c, agentStoreSession{variant: c.id, id: "abc-12345678"})
	cur := m.tree.byUUID["note"]

	m.caret = 0 // nowhere near the chip
	if _, ok := m.agentChipAtCaret(cur); ok {
		t.Fatal("the exact test should miss here — that is the premise")
	}
	got, ok := m.agentChipForKeys(cur)
	if !ok || got.ID != chip.ID {
		t.Errorf("the keys did not fall back to the row's only session")
	}

	// a second session makes the caret ambiguous: say so, do not guess
	m.caret = len([]rune(cur.name))
	second := chipOn(t, m, "note", variant(t, "pi"), agentStoreSession{variant: "pi", id: "def-87654321"})
	m.caret = 0
	if _, ok := m.agentChipForKeys(m.tree.byUUID["note"]); ok {
		t.Error("two sessions and an ambiguous caret must not pick one")
	}
	if !strings.Contains(m.flash, "put the caret on a session chip") {
		t.Errorf("no explanation given, flash = %q", m.flash)
	}
	_ = second
}

// TestAgentPickerFillsWhileOpen: the session picker draws rows as the store
// scan delivers them, rather than after it. A batch that lands is merged,
// re-sorted newest-first, and immediately visible to items(); the picker was
// already searchable before any of it arrived.
func TestAgentPickerFillsWhileOpen(t *testing.T) {
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "notes "})
	cursorOn(m, "note")

	ch := make(chan tea.Msg, 4)
	m.agentScanCh, m.agentSeen = ch, map[string]bool{}
	m.agentFill.begin()

	if rows := (agentStartSource{}).items(m, ""); len(rows) != 0 {
		t.Fatalf("the picker started with %d rows, want an empty list to fill", len(rows))
	}
	if !m.animActive() {
		t.Error("a filling picker must keep the animation tick alive for its spinner")
	}

	// first batch: the older session
	m.handleAgentScan(agentScanMsg{ch: ch, batch: []agentStoreSession{{
		variant: c.id, id: "abc-12345678", title: "flush fix",
		updated: time.Now().Add(-2 * time.Hour),
	}}})
	if rows := (agentStartSource{}).items(m, ""); len(rows) != 1 {
		t.Fatalf("after one batch: %d rows, want 1", len(rows))
	}

	// second batch: a newer one, which must sort ABOVE what already landed
	m.handleAgentScan(agentScanMsg{ch: ch, batch: []agentStoreSession{{
		variant: "opencode", id: "ses_9", title: "rank math",
		updated: time.Now().Add(-time.Minute),
	}}})
	rows := (agentStartSource{}).items(m, "")
	if len(rows) != 2 {
		t.Fatalf("after two batches: %d rows, want 2", len(rows))
	}
	if got := stripSGR(rows[0].render(false)); !strings.Contains(got, "rank math") {
		t.Errorf("newest row = %q, want the session that landed last but is newer", got)
	}

	// a repeat of a session already merged does not double it
	m.handleAgentScan(agentScanMsg{ch: ch, batch: []agentStoreSession{{
		variant: "opencode", id: "ses_9", title: "rank math",
		updated: time.Now().Add(-time.Minute),
	}}})
	if rows := (agentStartSource{}).items(m, ""); len(rows) != 2 {
		t.Errorf("a repeated session made %d rows, want it dropped", len(rows))
	}

	// and the end of the scan stops the spinner
	m.handleAgentScan(agentScanMsg{ch: ch, done: true})
	if m.agentFill.running() {
		t.Error("the fill still reports itself in flight after done")
	}
	if m.animActive() {
		t.Error("nothing is filling — the animation tick should stop")
	}
}

// TestAgentPickerIgnoresAnAbandonedScan: reopening the picker starts a new
// scan, and the previous one's batches must not leak into the new list.
func TestAgentPickerIgnoresAnAbandonedScan(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "notes "})
	cursorOn(m, "note")
	m.agentScanCh, m.agentSeen = make(chan tea.Msg, 1), map[string]bool{}

	stale := make(chan tea.Msg, 1)
	if cmd := m.handleAgentScan(agentScanMsg{ch: stale, batch: []agentStoreSession{{
		variant: "claude", id: "old-1", title: "abandoned", updated: time.Now(),
	}}}); cmd != nil {
		t.Error("an abandoned scan was asked for its next batch")
	}
	if len(m.agentStore) != 0 {
		t.Errorf("an abandoned scan put %d rows in the open picker", len(m.agentStore))
	}
}

// TestAgentDeclaredNameIsAboutTheSession: a "name" on a record that is not the
// session does not name the session. pi's outline extension writes a name for
// every node it touches and a compaction writes a summary of what it dropped;
// reading either as the session's name labelled sessions with a stray title.
func TestAgentDeclaredNameIsAboutTheSession(t *testing.T) {
	cases := []struct {
		name string
		rec  map[string]any
		want string
	}{
		{"pi rename", map[string]any{"type": "session_info", "name": "Ultraloop"}, "Ultraloop"},
		{"pi outline node", map[string]any{"type": "node", "name": "a node in the outline"}, ""},
		{"pi compaction", map[string]any{"type": "compaction", "summary": "what was dropped"}, ""},
		{"pi message", map[string]any{"type": "message", "name": "tool_call"}, ""},
		{"pi session head", map[string]any{"type": "session", "id": "019f75e8-fd60", "cwd": "/x"}, ""},
		{"opencode record", map[string]any{"id": "ses_3c7a52a98ffeAAtYek", "title": "Evosax usage in TUI"}, "Evosax usage in TUI"},
		{"a stray object", map[string]any{"id": "short", "title": "not a session"}, ""},
	}
	for _, c := range cases {
		if got := agentDeclaredName(c.rec); got != c.want {
			t.Errorf("%s: agentDeclaredName = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestAgentDeclaredNameBeatsAFirstPrompt: a session named by its CLI reads by
// that name wherever the naming record sits in the file. pi appends its rename
// AFTER the conversation, so the prompt that would otherwise have named the
// session comes first — and used to win.
func TestAgentDeclaredNameBeatsAFirstPrompt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "019f75e8-fd60-7b18-b809-7b4f363be885.jsonl")
	lines := []string{
		`{"type":"session","version":3,"id":"019f75e8-fd60-7b18-b809-7b4f363be885","cwd":"/home/dev/lflow"}`,
		`{"type":"message","role":"user","content":"make the pickers stream so nothing freezes"}`,
		`{"type":"session_info","id":"3b60e2db","name":"picker streaming"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := agentReadMeta("pi", path)
	if !s.named {
		t.Error("a session_info record did not register as a declared name")
	}
	if s.title != "picker streaming" {
		t.Errorf("title = %q, want the name pi recorded, not the first prompt", s.title)
	}
	if s.cwd != "/home/dev/lflow" {
		t.Errorf("cwd = %q", s.cwd)
	}
}

// TestAgentIdentOverlaysTheTranscript: Claude Code keeps a session's name and
// color BESIDE the transcript, so a session read only from its transcript came
// back nameless and was labelled with its first prompt.
func TestReturningFromAgentClearsExternalScrollback(t *testing.T) {
	m := newTestModel(60, "agent handle")
	m.handleAgentClosed(agentClosedMsg{variant: "missing"})
	if !m.clearOnFrame {
		t.Fatal("agent return did not flag a clean repaint")
	}
	if frame := m.View(); !strings.HasPrefix(frame, cClearScrollback) {
		t.Fatalf("first frame after agent return did not clear external scrollback: %q", frame)
	}
}

func TestAgentIdentOverlaysTheTranscript(t *testing.T) {
	s := agentStoreSession{variant: "claude", id: "abc", title: "the whole first prompt, pasted"}
	s.applyIdent(agentIdent{name: "lflow-ab", color: "red"})
	if s.title != "lflow-ab" || !s.named {
		t.Errorf("title = %q named = %v, want the CLI's own name", s.title, s.named)
	}
	if s.color != "red" {
		t.Errorf("color = %q, want the one the CLI gave it", s.color)
	}

	// a session the index does not know keeps what reading it produced
	u := agentStoreSession{variant: "claude", id: "def", title: "a first prompt"}
	u.applyIdent(agentIdent{})
	if u.title != "a first prompt" || u.named {
		t.Errorf("an empty ident changed the session: %q named=%v", u.title, u.named)
	}
}

// TestMergeIdentsEarlierWins: the job store names a session, and the registry
// only ever fills a gap it left.
func TestMergeIdentsEarlierWins(t *testing.T) {
	jobs := map[string]agentIdent{"a": {name: "from the job", color: "red"}}
	regs := map[string]agentIdent{"a": {name: "from the registry"}, "b": {name: "only in the registry"}}
	got := mergeIdents(jobs, regs)

	if got["a"].name != "from the job" {
		t.Errorf("a.name = %q, want the job store's", got["a"].name)
	}
	if got["a"].color != "red" {
		t.Errorf("a.color = %q, want the job store's color kept", got["a"].color)
	}
	if got["b"].name != "only in the registry" {
		t.Errorf("b.name = %q, want the registry to fill the gap", got["b"].name)
	}
}

// TestAgentReadTailFindsALateRename: the rename is appended, so a head-only read
// reported the old name forever.
func TestAgentReadTailFindsALateRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")

	var b strings.Builder
	b.WriteString(`{"type":"session","id":"019f75e8-fd60-7b18-b809-7b4f363be885","cwd":"/x"}` + "\n")
	b.WriteString(`{"type":"message","role":"user","content":"the first thing asked"}` + "\n")
	// push the rename well past the head window
	filler := strings.Repeat("x", 4000)
	for b.Len() < agentMetaCap*2 {
		b.WriteString(`{"type":"message","role":"assistant","content":"` + filler + `"}` + "\n")
	}
	b.WriteString(`{"type":"session_info","id":"3b60e2db","name":"renamed much later"}` + "\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	if s := agentReadMeta("pi", path); s.title != "renamed much later" {
		t.Errorf("title = %q, want the name appended after the conversation", s.title)
	}
	// a file that fits in the head window is not read twice
	small := filepath.Join(dir, "small.jsonl")
	if err := os.WriteFile(small, []byte(`{"type":"session_info","name":"n"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if recs := agentReadTail(small, agentMetaCap); recs != nil {
		t.Errorf("a short file was re-read by the tail pass: %d records", len(recs))
	}
}

// TestAgentFindsARenameBuriedMidTranscript is the real shape of a pi session:
// its own auto-title lands right after the first prompt, YOUR rename lands
// wherever you were when you typed it, and then the conversation keeps going for
// megabytes. With only the head and the tail read, the picker showed the
// auto-title — the first prompt, which is exactly what renaming was meant to
// replace.
func TestAgentFindsARenameBuriedMidTranscript(t *testing.T) {
	dir := t.TempDir()
	// pi names its transcripts <timestamp>_<session id>.jsonl
	path := filepath.Join(dir, "2026-07-27T11-09-16-731Z_019fa343-ca3b-796e-bad1-71725bd29c64.jsonl")

	var b strings.Builder
	b.WriteString(`{"type":"session","version":3,"id":"019fa343-ca3b-796e-bad1-71725bd29c64",` +
		`"timestamp":"2026-07-27T11:09:16.731Z","cwd":"/home/eren"}` + "\n")
	b.WriteString(`{"type":"message","id":"b2e8472d","timestamp":"2026-07-27T11:09:20.000Z",` +
		`"message":{"role":"user","content":[{"type":"text","text":"is there remnants of codex cli on my system?"}]}}` + "\n")
	// pi's own title for the session, written from that first prompt
	b.WriteString(`{"type":"session_info","id":"c764f667","timestamp":"2026-07-27T11:09:35.993Z",` +
		`"name":"is there remnants of codex cli on my system?"}` + "\n")

	filler := `{"type":"message","role":"assistant","content":"` + strings.Repeat("x", 4000) + `"}` + "\n"
	for b.Len() < agentMetaCap+8000 {
		b.WriteString(filler)
	}
	b.WriteString(`{"type":"session_info","id":"d81aa0f2","timestamp":"2026-07-27T12:40:02.100Z",` +
		`"name":"codex cleanup"}` + "\n")
	// ...and the session runs on well past the rename, so the tail never sees it
	for b.Len() < 3*agentMetaCap {
		b.WriteString(filler)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	s := agentReadMeta("pi", path)
	if s.title != "codex cleanup" {
		t.Errorf("title = %q, want the rename buried in the middle", s.title)
	}
	if !s.named {
		t.Error("the session did not read as named")
	}
	if s.id != "019fa343-ca3b-796e-bad1-71725bd29c64" {
		t.Errorf("id = %q", s.id)
	}
	if s.cwd != "/home/eren" {
		t.Errorf("cwd = %q", s.cwd)
	}
}

// TestAgentIdentScanSkipsShortFiles: the middle scan exists for transcripts the
// head and tail cannot cover between them. A file they already cover whole must
// not be read a third time.
func TestAgentIdentScanSkipsShortFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "short.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session_info","name":"n"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if recs := agentScanIdents(path); recs != nil {
		t.Errorf("a short file was scanned for idents: %d records", len(recs))
	}
}

// TestAgentReadMetaTakesTheRecordsColor: a store that names a session in its own
// records is also where it would record a color for one, and both come through
// the same gate.
func TestAgentReadMetaTakesTheRecordsColor(t *testing.T) {
	c := variant(t, "claude")

	id := claudeStore(t, recSummary, recUser)
	path := agentSessionPath(c.sessionDirs(), c.exts, id)
	if path == "" {
		t.Fatal("session not located in the store")
	}
	if meta := agentReadMeta(c.id, path); meta.color != "#4ec9b0" {
		t.Errorf("color = %q, want the one the record carries", meta.color)
	}

	// the same session without one: nothing to read, and the variant's own
	// theming is what agentColorFor will fall back to
	id2 := claudeStore(t, recPlain, recUser)
	path2 := agentSessionPath(c.sessionDirs(), c.exts, id2)
	if meta := agentReadMeta(c.id, path2); meta.color != "" {
		t.Errorf("color = %q, want none — the record declares none", meta.color)
	}
}

// TestPiWriteIdentAppends: pi is renamed the way pi renames — by APPENDING a
// session_info record, never by rewriting the transcript. Nothing already in the
// file may move, because that file is a conversation.
func TestPiWriteIdentAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	before := `{"type":"session","id":"019f75e8-fd60-7b18-b809-7b4f363be885","cwd":"/x"}` + "\n" +
		`{"type":"message","role":"user","content":"the first thing asked"}` + "\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := piWriteIdent("019f75e8", path, agentIdent{name: "picker streaming"}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(after), before) {
		t.Error("the existing transcript was rewritten — a rename must only append")
	}
	if s := agentReadMeta("pi", path); s.title != "picker streaming" || !s.named {
		t.Errorf("after the append the session reads %q (named=%v)", s.title, s.named)
	}

	// the appended record is the shape pi writes, and a color is not part of it
	var rec map[string]any
	last := strings.TrimSpace(string(after))
	last = last[strings.LastIndex(last, "\n")+1:]
	if err := json.Unmarshal([]byte(last), &rec); err != nil {
		t.Fatalf("appended line is not JSON: %v", err)
	}
	if rec["type"] != "session_info" || rec["name"] != "picker streaming" {
		t.Errorf("appended record = %v", rec)
	}
	// pi records no color, so a color-only push writes nothing at all
	size := len(after)
	if err := piWriteIdent("019f75e8", path, agentIdent{color: "red"}); err != nil {
		t.Fatal(err)
	}
	if now, _ := os.ReadFile(path); len(now) != size {
		t.Error("a color was written into a store that records none")
	}
}

// TestOpencodeWriteIdentKeepsEveryOtherField: the rewrite round-trips the
// object, so a field lflow does not know about survives.
func TestOpencodeWriteIdentKeepsEveryOtherField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ses_3c7a52a98ffeAAtYekvCBNy5Tx.json")
	orig := `{"id":"ses_3c7a52a98ffeAAtYekvCBNy5Tx","slug":"happy-wizard","title":"Evosax usage in TUI","projectID":"abc","aFieldLflowHasNeverHeardOf":{"deep":[1,2,3]}}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := opencodeWriteIdent("ses_3c7a52a98ffeAAtYekvCBNy5Tx", path, agentIdent{name: "rank math"}); err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	if rec["title"] != "rank math" {
		t.Errorf("title = %v, want the new name", rec["title"])
	}
	for _, k := range []string{"id", "slug", "projectID", "aFieldLflowHasNeverHeardOf"} {
		if rec[k] == nil {
			t.Errorf("the rewrite dropped %q", k)
		}
	}
	// and it reads back as the session's name
	if s := agentReadMeta("opencode", path); s.title != "rank math" || !s.named {
		t.Errorf("reads back as %q (named=%v)", s.title, s.named)
	}
}

// TestClaudeWriteIdentRefusesALiveSession is the guard that matters: the job
// record is live state belonging to the process writing it.
func TestClaudeWriteIdentNeedsAJobRecord(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no transcript and no job store
	err := claudeWriteIdent("40803d6c-1ece-4ad8-be9b-5d84f7d169b9", "", agentIdent{name: "x"})
	if err == nil {
		t.Fatal("writing with nowhere to write should say so")
	}
	if !strings.Contains(err.Error(), "no record found") {
		t.Errorf("err = %v", err)
	}
	// nothing to write is not an error
	if err := claudeWriteIdent("abc", "", agentIdent{}); err != nil {
		t.Errorf("an empty push errored: %v", err)
	}
}

// TestClaudeWriteIdentRoundTrips: name, nameSource and color land in the job
// record and everything else survives.
func TestClaudeWriteIdentRoundTrips(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	jobs := filepath.Join(home, ".claude", "jobs", "f343c35a")
	if err := os.MkdirAll(jobs, 0o755); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(jobs, "state.json")
	orig := `{"sessionId":"f343c35a-a84f-464e-9cda-a4465ec1bd11","name":"auto name","nameSource":"auto","color":"red","state":"done","children":[{"id":"15"}]}`
	if err := os.WriteFile(state, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := claudeWriteIdent("f343c35a-a84f-464e-9cda-a4465ec1bd11", "", agentIdent{name: "picker streaming", color: "cyan"}); err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	b, _ := os.ReadFile(state)
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	if rec["name"] != "picker streaming" || rec["color"] != "cyan" {
		t.Errorf("name = %v color = %v", rec["name"], rec["color"])
	}
	if rec["nameSource"] != "user" {
		t.Errorf("nameSource = %v, want the mark Claude Code puts on a name you chose", rec["nameSource"])
	}
	if rec["state"] != "done" || rec["children"] == nil {
		t.Error("the rewrite dropped a field it does not own")
	}

	// and the index reads back what was written
	idx := claudeIdents([]string{filepath.Join(home, ".claude", "jobs")})
	if got := idx["f343c35a-a84f-464e-9cda-a4465ec1bd11"]; got.name != "picker streaming" || got.color != "cyan" {
		t.Errorf("index reads %+v", got)
	}
}

// TestAgentPushIdentWithoutAColorStore: ⌥c on a pi or opencode chip recolors it
// exactly the same — the color is simply lflow's alone and goes nowhere else.
func TestAgentPushIdentWithoutAColorStore(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "notes "})
	v, _ := agentVariantByID("pi")
	s := agentSession{Variant: "pi", SessionID: "019f75e8-fd60-7b18-b809-7b4f363be885"}

	got := m.agentPushIdent(v, s, agentIdent{color: "cyan"}, "recolored")
	if !strings.Contains(got, "records no color") {
		t.Errorf("flash = %q, want it to say pi keeps no color", got)
	}
	// a variant with no writer at all keeps the change local and says only that
	none := agentVariant{id: "x", label: "X"}
	if got := m.agentPushIdent(none, s, agentIdent{name: "n"}, "renamed"); got != "renamed here" {
		t.Errorf("flash = %q, want the local-only line", got)
	}
}

// TestClaudeInteractiveIdentityLivesInTheTranscript: Claude Code renames and
// recolors an INTERACTIVE session by appending records to the transcript. The
// job store only ever knew DISPATCHED ones, so a named, colored session still
// read as its first prompt in the agent's default color.
func TestClaudeInteractiveIdentityLivesInTheTranscript(t *testing.T) {
	id := claudeStore(t,
		recUser,
		`{"type":"custom-title","customTitle":"voice to report part 2","sessionId":"x"}`,
		`{"type":"agent-name","agentName":"voice to report part 2","sessionId":"x"}`,
		`{"type":"agent-color","agentColor":"green","sessionId":"x"}`,
		// renamed again later: each change is appended afresh and the last wins
		`{"type":"custom-title","customTitle":"voice to report notebook 2","sessionId":"x"}`,
		`{"type":"agent-name","agentName":"voice to report notebook 2","sessionId":"x"}`,
	)
	c := variant(t, "claude")
	meta := agentReadMeta(c.id, agentSessionPath(c.sessionDirs(), c.exts, id))

	if meta.title != "voice to report notebook 2" {
		t.Errorf("title = %q, want the latest name, not the first prompt", meta.title)
	}
	if !meta.named {
		t.Error("a session named in its transcript did not register as named")
	}
	if meta.color != "green" {
		t.Errorf("color = %q, want the one the CLI recorded", meta.color)
	}
}

// TestAgentColorRecordCarriesNoName: a recolor is its own record with no name in
// it at all, so requiring a name alongside dropped the color.
func TestAgentColorRecordCarriesNoName(t *testing.T) {
	got := agentDeclaredIdent(map[string]any{
		"type": "agent-color", "agentColor": "green", "sessionId": "x",
	})
	if got.color != "green" {
		t.Errorf("color = %q, want it read from a record with no name", got.color)
	}
	if got.name != "" {
		t.Errorf("name = %q, want none", got.name)
	}
	// and a colored session keeps the name an earlier record gave it
	id := claudeStore(t,
		`{"type":"agent-name","agentName":"compute reset fix","sessionId":"x"}`,
		recUser,
		`{"type":"agent-color","agentColor":"red","sessionId":"x"}`,
	)
	c := variant(t, "claude")
	meta := agentReadMeta(c.id, agentSessionPath(c.sessionDirs(), c.exts, id))
	if meta.title != "compute reset fix" || meta.color != "red" {
		t.Errorf("title = %q color = %q, want both kept", meta.title, meta.color)
	}
}

// TestClaudeAppendIdentIsWhatTheCLIWrites: a rename appends the pair Claude Code
// appends, and never rewrites what is already there — that file is a conversation.
func TestClaudeAppendIdentAppendsOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	before := recUser + "\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := claudeAppendIdent(path, "sid-1", agentIdent{name: "flush fix", color: "green"}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(after), before) {
		t.Error("the transcript was rewritten — a rename must only append")
	}

	types := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(after)), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if s, _ := rec["type"].(string); s != "" {
			types[s] = line
		}
	}
	for _, want := range []string{"custom-title", "agent-name", "agent-color"} {
		if types[want] == "" {
			t.Errorf("no %s record was appended", want)
		}
	}
	if !strings.Contains(types["custom-title"], `"sessionId":"sid-1"`) {
		t.Errorf("the appended record does not name its session: %s", types["custom-title"])
	}

	// and it reads back as the session's name and color
	if s := agentReadMeta("claude", path); s.title != "flush fix" || s.color != "green" {
		t.Errorf("reads back as %q / %q", s.title, s.color)
	}
}

// TestAgentPickerRowWearsTheSessionsColor: a picker row is a preview of the chip
// it becomes, so a session the CLI colored must not be drawn in the agent's
// default — it would show a color the chip will not have.
func TestAgentPickerRowWearsTheSessionsColor(t *testing.T) {
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "notes "})
	cursorOn(m, "note")
	m.agentStore = []agentStoreSession{
		{variant: c.id, id: "abc-12345678", title: "voice to report", color: "green", updated: time.Now()},
		{variant: c.id, id: "def-12345678", title: "no color of its own", updated: time.Now()},
	}

	rows := (agentStartSource{}).items(m, "")
	if len(rows) != 2 {
		t.Fatalf("got %d rows", len(rows))
	}
	if got := rows[0].render(false); !strings.Contains(got, bgOf(agentColorSGR("green"))) {
		t.Errorf("a green session drew in %q, want the CLI's green", stripSGR(got))
	}
	if got := rows[1].render(false); !strings.Contains(got, bgOf(c.colorSGR())) {
		t.Error("an uncolored session did not fall back to Claude Code's own fill")
	}
}

// TestAgentChipSurvivesUndo: deleting a session chip takes its session pointer
// with it — the sidecar row keyed by the chip id. Undo has to bring BOTH back:
// a restored chip that lost its session id is a pill with nothing under it, and
// nothing to rename or recolor.
func TestAgentChipSurvivesUndo(t *testing.T) {
	id := claudeStore(t, recSummary, recUser)
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "notes "})
	chip := chipOn(t, m, "note", c, agentStoreSession{
		variant: c.id, id: id, title: "fix the flaky sync test"})
	cur := m.tree.byUUID["note"]

	m.agentRename(chip.ID, "flush fix")
	m.agentSetColor(chip.ID, "cyan")
	before := m.agentLoad(chip.ID)
	if before.SessionID != id || before.Name != "flush fix" || before.Color != "cyan" {
		t.Fatalf("chip did not start named and colored: %+v", before)
	}

	// caret sits right after the chip, so ctrl+w takes exactly it
	m.caret = len([]rune(cur.name))
	m.press("ctrl+w")
	if _, ok := m.chips[chip.ID]; ok {
		t.Fatal("the chip was not deleted")
	}

	m.undo()
	cur = m.tree.byUUID["note"] // undo rebuilds the tree from its snapshot

	if _, ok := m.chips[chip.ID]; !ok {
		t.Fatal("undo did not restore the chip record")
	}
	got := m.agentLoad(chip.ID)
	if got.SessionID != id {
		t.Errorf("restored session = %q, want %q — the chip forgot which session it is", got.SessionID, id)
	}
	if got.Name != "flush fix" {
		t.Errorf("restored name = %q, want the local rename back", got.Name)
	}
	if got.Color != "cyan" {
		t.Errorf("restored color = %q, want cyan", got.Color)
	}
	if disp := displayAnchors(cur.name, m.chips); !strings.Contains(disp, "✽ flush fix") {
		t.Errorf("restored pill reads %q", disp)
	}

	// and it is a live chip again, not a husk: it takes a new name and color
	m.agentRename(chip.ID, "renamed after undo")
	m.agentSetColor(chip.ID, "red")
	again := m.agentLoad(chip.ID)
	if again.Name != "renamed after undo" || again.Color != "red" {
		t.Errorf("restored chip refused a rename/recolor: %+v", again)
	}
	if disp := displayAnchors(cur.name, m.chips); !strings.Contains(disp, "✽ renamed after undo") {
		t.Errorf("renamed pill reads %q", disp)
	}
}
