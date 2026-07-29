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
	// a session is a CHIP, never a node type — nothing may register one
	for _, nt := range nodeTypes {
		if strings.HasPrefix(nt.key, "agent") {
			t.Errorf("%q is a node type; sessions are chips only", nt.key)
		}
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
	if !strings.Contains(meta.color, "78;201;176") {
		t.Errorf("session color = %q, want the #4ec9b0 truecolor sequence", meta.color)
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
	if meta.color != "" {
		t.Errorf("color = %q, want none", meta.color)
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
	// the CLI's store gave this session a color: the pill is filled with it
	if !strings.Contains(pill, "\x1b[48;2;78;201;176m") {
		t.Errorf("pill = %q, want the session's own #4ec9b0 fill", pill)
	}
	if got := stripSGR(pill); got != " ✽ fix the flaky sync test " {
		t.Errorf("pill reads %q, want the glyph and the session's name", got)
	}
}

// TestAgentChipCustomName: like a link chip, a session can be given the user's
// own name — and clearing it hands the name back to the CLI.
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
	m.agentRename(chip.ID, "")
	if got := displayAnchors(cur.name, m.chips); !strings.Contains(got, "✽ fix the flaky sync test") {
		t.Errorf("cleared name reads %q, want the CLI's own name back", got)
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
	if s.Runs == 0 {
		t.Error("an attached session already exists — the next open must resume it")
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
	m.agentSave(chip.ID, agentSession{Variant: c.id, SessionID: "abc-123", Cwd: gone, Runs: 1})

	if cmd := m.agentOpen(c, chip.ID, gone); cmd != nil {
		t.Error("a session whose directory is gone must not launch the CLI")
	}
	if !strings.Contains(m.flash, "is gone") {
		t.Errorf("flash = %q, want it to name the missing directory", m.flash)
	}
}

// TestAgentPanelIsSmall: ⌥e shows the session, where it runs and its keys — and
// nothing else. No transcript, no tool calls, no counters.
func TestAgentPanelIsSmall(t *testing.T) {
	id := claudeStore(t, recSummary, recUser)
	claudeRegistry(t, id, "flush-resume", "cli")
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "notes "})
	chip := chipOn(t, m, "note", c, agentStoreSession{variant: c.id, id: id, cwd: "/home/dev/repo"})
	m.focusAgentChip(chip)

	bands := (agentChipView{}).Bands(m, m.tree.byUUID["note"], "", 200, 0, 20, true)
	if len(bands) != 3 {
		t.Fatalf("the panel is %d lines, want 3:\n%s", len(bands), strings.Join(bands, "\n"))
	}
	joined := stripSGR(strings.Join(bands, "\n"))
	for _, want := range []string{"✽ flush-resume", "/home/dev/repo", "live", "⌥r open", "n rename"} {
		if !strings.Contains(joined, want) {
			t.Errorf("panel is missing %q:\n%s", want, joined)
		}
	}
	for _, unwanted := range []string{"tokens", "Bash", "turns", "branch", "model"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("panel leaks %q — it is meant to be small:\n%s", unwanted, joined)
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

// TestAgentsListRanksLiveFirst: /agents lists the outline's session chips, live
// ones first, done ones muted below — each drawn as the pill the outline draws.
func TestAgentsListRanksLiveFirst(t *testing.T) {
	c := variant(t, "claude")
	p := variant(t, "pi")
	m, _ := dbModel(t,
		database.Node{UUID: "done", Name: "shipped work ", CompletedAt: time.Now().Unix()},
		database.Node{UUID: "live", Name: "current work "},
	)
	doneChip := chipOn(t, m, "done", c, agentStoreSession{})
	liveChip := chipOn(t, m, "live", p, agentStoreSession{})
	m.agentSave(doneChip.ID, agentSession{Variant: c.id, SessionID: "d1", OpenedAt: time.Now().Unix()})
	m.agentSave(liveChip.ID, agentSession{Variant: p.id, SessionID: "l1", OpenedAt: time.Now().Add(-time.Hour).Unix()})
	if _, err := m.tree.save(); err != nil {
		t.Fatal(err)
	}

	rows := m.collectAgentRows()
	if len(rows) != 2 {
		t.Fatalf("collected %d rows, want 2", len(rows))
	}
	if rows[0].uuid != "live" || rows[1].uuid != "done" {
		t.Fatalf("order = %s,%s — live sessions rank first", rows[0].uuid, rows[1].uuid)
	}
	doneRow := m.agentListRow(rows[1], "shipped")
	if !strings.HasPrefix(doneRow, bgOf(cDim)) || !strings.Contains(stripSGR(doneRow), "done") {
		t.Errorf("done row = %q, want a gray-filled pill marked done", doneRow)
	}
	liveRow := m.agentListRow(rows[0], "current")
	if !strings.Contains(liveRow, bgOf(p.colorSGR())) {
		t.Errorf("live row = %q, want a pill filled with the agent's own color", liveRow)
	}
	if !strings.Contains(stripSGR(liveRow), "current work") {
		t.Errorf("live row = %q, want the row the chip is filed in", stripSGR(liveRow))
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

// TestAgentsListRowDropsItsOwnChip: a /agents row shows the words around the
// chip, not the chip again — the pill beside them already says which session.
func TestAgentsListRowDropsItsOwnChip(t *testing.T) {
	c := variant(t, "claude")
	m, _ := dbModel(t, database.Node{UUID: "note", Name: "sync flush loses edits "})
	chipOn(t, m, "note", c, agentStoreSession{})
	if _, err := m.tree.save(); err != nil {
		t.Fatal(err)
	}
	rows := m.collectAgentRows()
	if len(rows) != 1 {
		t.Fatalf("collected %d rows", len(rows))
	}
	if rows[0].name != "sync flush loses edits" {
		t.Errorf("row name = %q, want the row's own words without the chip", rows[0].name)
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
	if got := renderBody(it, it.name, -1, false, m.chips, false); strings.Contains(got, cStrike) {
		t.Fatalf("an open row must not strike anything: %q", got)
	}
	it.completedAt = time.Now().UnixNano()
	done := renderBody(it, it.name, -1, false, m.chips, false)
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
