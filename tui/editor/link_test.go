package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/tui/database"
	"github.com/lflow/lflow/tui/runtime"
)

// dbModel builds an editor backed by an in-memory DB, with the given children
// seeded under root and the tree loaded from the DB. Returns the model and db.
func dbModel(t *testing.T, children ...database.Node) (*Model, *database.DB) {
	t.Helper()
	db := database.InitTestMemoryDB(t)
	if err := database.EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	for _, n := range children {
		n.ParentUUID = database.RootUUID
		if n.Type == "" {
			n.Type = database.TypeBullets
		}
		if err := n.Insert(db); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := loadTree(db, database.RootUUID)
	if err != nil {
		t.Fatal(err)
	}
	m := &Model{
		db:        db,
		ctx:       runtime.Ctx{DB: db},
		tree:      tr,
		viewStack: []*item{tr.root},
		width:     80,
		height:    24,
		chips:     map[string]database.Chip{},
	}
	m.refreshRows()
	return m, db
}

func (m *Model) feed(k tea.KeyMsg) {
	mm, _ := m.handleKey(k)
	*m = *mm.(*Model)
}

func altRune(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: true} }

func linkChipOf(m *Model) (database.Chip, bool) {
	for _, c := range m.chips {
		if c.Kind == chipKindLink {
			return c, true
		}
	}
	return database.Chip{}, false
}

func cursorOn(m *Model, uuid string) {
	for i, r := range m.rows {
		if r.it.uuid == uuid {
			m.cursor = i
			return
		}
	}
}

// TestLinkCreateNodeViaBrackets: "[[" opens the picker, picking a node splices an
// inline link chip whose target is the node and whose name defaults to its name.
func TestLinkCreateNodeViaBrackets(t *testing.T) {
	m, _ := dbModel(t,
		database.Node{UUID: "edit", Name: "", Rank: 0},
		database.Node{UUID: "tgt", Name: "Target Node", Rank: 1},
	)
	cursorOn(m, "edit")
	m.caret = 0

	m.press("[")
	m.press("[")
	if m.mode != modeFinder || m.finder.act != actLinkInsert {
		t.Fatalf("[[ did not open the link finder: mode=%v act=%v", m.mode, m.finder.act)
	}
	m.press("Target") // narrow the picker to the target
	m.press("enter")

	c, ok := linkChipOf(m)
	if !ok {
		t.Fatal("no link chip created")
	}
	if c.Value != nodeLinkURI("tgt") {
		t.Errorf("link target = %q, want %q", c.Value, nodeLinkURI("tgt"))
	}
	if c.Label != "Target Node" {
		t.Errorf("link name = %q, want Target Node", c.Label)
	}
	edit := m.tree.byUUID["edit"]
	if !hasAnchor(edit.name) {
		t.Errorf("edit node has no chip anchor: %q", edit.name)
	}
	if got := displayAnchors(edit.name, m.chips); got != "→Target Node" {
		t.Errorf("rendered link = %q, want →Target Node", got)
	}
}

// TestLinkToChippedNode: linking to a node whose title carries a chip (e.g. a
// #tag) must resolve the target's anchor to display text. A raw "￼id￼" anchor
// in the label leaked the inner chip id and corrupted the editor (froze the TUI),
// so the label must be the resolved display text with no sentinel.
func TestLinkToChippedNode(t *testing.T) {
	m, db := dbModel(t,
		database.Node{UUID: "edit", Name: "", Rank: 0},
		database.Node{UUID: "tgt", Name: "FID " + chipAnchor("tag1"), Rank: 1},
	)
	tag := database.Chip{ID: "tag1", Kind: chipKindTag, Value: "zky"}
	if err := database.UpsertChip(db, tag); err != nil {
		t.Fatal(err)
	}
	m.chips["tag1"] = tag

	cursorOn(m, "edit")
	m.caret = 0
	m.press("[")
	m.press("[")
	m.press("FID") // narrow the picker to the chipped target
	m.press("enter")

	c, ok := linkChipOf(m)
	if !ok {
		t.Fatal("no link chip created")
	}
	if strings.ContainsRune(c.Label, chipSentinel) {
		t.Fatalf("link label leaks a chip sentinel: %q", c.Label)
	}
	if c.Label != "FID #zky" {
		t.Errorf("link label = %q, want %q", c.Label, "FID #zky")
	}
	edit := m.tree.byUUID["edit"]
	if got := displayAnchors(edit.name, m.chips); got != "→FID #zky" {
		t.Errorf("rendered link = %q, want →FID #zky", got)
	}
}

func TestMirrorCreateViaDoubleParen(t *testing.T) {
	m, _ := dbModel(t,
		database.Node{UUID: "edit", Name: ""},
		database.Node{UUID: "target", Name: "Target node"},
	)
	cursorOn(m, "edit")
	m.press("(")
	m.press("(")
	if m.mode != modeFinder || m.finder.act != actMirrorHere {
		t.Fatalf("(( did not open mirror finder: mode=%v act=%v", m.mode, m.finder.act)
	}
	if got := m.tree.byUUID["edit"].name; got != "" {
		t.Fatalf("trigger leaked into node text: %q", got)
	}
	m.press("Target")
	m.press("enter")
	if got := m.tree.byUUID["edit"].mirrorOf; got != "target" {
		t.Fatalf("mirror target = %q", got)
	}
}

func TestDoubleParenStaysLiteralInSyntaxNodes(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "query", Name: "", Type: database.TypeQuery})
	cursorOn(m, "query")
	m.press("(")
	m.press("(")
	if m.mode == modeFinder || m.tree.byUUID["query"].name != "((" {
		t.Fatalf("query parentheses must stay literal: mode=%v name=%q", m.mode, m.tree.byUUID["query"].name)
	}
}

// TestLinkCreateURLViaBrackets: "[[" then a typed URL makes a URL link chip whose
// name defaults to the host.
func TestLinkCreateURLViaBrackets(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: "see "})
	cursorOn(m, "edit")
	m.caret = len([]rune("see "))

	m.press("[")
	m.press("[")
	m.press("https://example.com/docs")
	m.press("enter")

	c, ok := linkChipOf(m)
	if !ok {
		t.Fatal("no link chip created")
	}
	if c.Value != "https://example.com/docs" {
		t.Errorf("link target = %q", c.Value)
	}
	if c.Label != "example.com" {
		t.Errorf("link name = %q, want example.com", c.Label)
	}
	// persisted to the DB immediately
	got, err := database.GetChip(m.db, c.ID)
	if err != nil || got.Value != "https://example.com/docs" || got.Label != "example.com" {
		t.Errorf("chip not persisted: %+v err=%v", got, err)
	}
}

// TestLinkFollowNodeJumps: alt+g on a node-link chip jumps the editor to the target.
func TestLinkFollowNodeJumps(t *testing.T) {
	m, _ := dbModel(t,
		database.Node{UUID: "here", Name: "from", Rank: 0},
		database.Node{UUID: "dest", Name: "Destination", Rank: 1},
	)
	cursorOn(m, "here")
	m.caret = 0
	m.insertLinkChip(nodeLinkURI("dest"), "Destination") // caret lands at chip end

	if _, ok := m.linkChipAtCaret(m.cursorItem()); !ok {
		t.Fatal("link chip not detected at caret")
	}
	m.feed(altRune('g'))

	if m.tree.root.uuid != "dest" {
		t.Fatalf("alt+g did not jump: tree root = %q, want dest", m.tree.root.uuid)
	}
}

// TestLinkFollowMissingTarget: a node link to a gone node flashes, does not jump.
func TestLinkFollowMissingTarget(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "here", Name: "from"})
	cursorOn(m, "here")
	m.caret = 0
	m.insertLinkChip(nodeLinkURI("ghost"), "Ghost")

	m.feed(altRune('g'))
	if m.tree.root.uuid != database.RootUUID {
		t.Fatalf("jumped to a missing target: root = %q", m.tree.root.uuid)
	}
	if m.flash != "link target missing" {
		t.Errorf("flash = %q, want link target missing", m.flash)
	}
}

// TestLinkEditViaAltE: alt+e on a link chip opens the editor; saving writes the
// new name and target back (normalizing a bare URL).
func TestLinkEditViaAltE(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "here", Name: "x"})
	cursorOn(m, "here")
	m.caret = 0
	m.insertLinkChip("https://old.com", "Old")

	m.feed(altRune('e'))
	if m.mode != modeLinkEdit {
		t.Fatalf("alt+e did not open the link editor: mode=%v", m.mode)
	}
	if m.linkEditName != "Old" || m.linkEditTarget != "https://old.com" {
		t.Fatalf("editor seeded wrong: name=%q target=%q", m.linkEditName, m.linkEditTarget)
	}

	// append "!" to the name, switch to target, retype a bare URL, save
	m.feed(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	m.feed(tea.KeyMsg{Type: tea.KeyTab})
	for range []rune(m.linkEditTarget) {
		m.feed(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	m.feed(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("www.new.com")})
	m.feed(tea.KeyMsg{Type: tea.KeyEnter})

	if m.mode != modeOutline {
		t.Fatalf("enter did not close the editor: mode=%v", m.mode)
	}
	c, _ := linkChipOf(m)
	if c.Label != "Old!" {
		t.Errorf("name = %q, want Old!", c.Label)
	}
	if c.Value != "https://www.new.com" {
		t.Errorf("target = %q, want https://www.new.com (normalized)", c.Value)
	}
	got, _ := database.GetChip(m.db, c.ID)
	if got.Label != "Old!" || got.Value != "https://www.new.com" {
		t.Errorf("edit not persisted: %+v", got)
	}
}

// TestNodeLinkURIRoundTrip is a small unit guard on the target encoding.
func TestNodeLinkURIRoundTrip(t *testing.T) {
	uuid, ok := nodeLinkUUID(nodeLinkURI("abc-123"))
	if !ok || uuid != "abc-123" {
		t.Fatalf("round trip failed: %q %v", uuid, ok)
	}
	if _, ok := nodeLinkUUID("https://example.com"); ok {
		t.Fatal("a URL must not parse as a node link")
	}
}

// TestSlashLinkCopiesNodeLink: /link copies the cursor node's
// lflow://node/<uuid> link to the clipboard — it does NOT splice a chip (that
// is /insert → Link's job). The pasted link converts on ctrl+t.
func TestSlashLinkCopiesNodeLink(t *testing.T) {
	clip := clipCapture(t)
	m, _ := dbModel(t, database.Node{UUID: "src", Name: "the source"})
	cursorOn(m, "src")
	m.caret = 0

	mm, _ := m.runSlash("/link")
	got := mm.(*Model)
	if s := clip(); s != nodeLinkURI("src") {
		t.Fatalf("copied %q, want %q", s, nodeLinkURI("src"))
	}
	if !strings.Contains(got.flash, "copied 1 link to clipboard") {
		t.Fatalf("flash = %q, want the link count", got.flash)
	}
	if len(got.chips) != 0 {
		t.Fatalf("/link must not create chips, got %v", got.chips)
	}
	if got.mode != modeOutline {
		t.Fatalf("mode = %v, want modeOutline (no finder)", got.mode)
	}
	if _, ok := linkChipOf(got); ok {
		t.Fatal("/link must not splice a link chip")
	}
}

// TestSlashLinkCopiesSelectedLinks: with a row selection live, /link copies
// every selected root's link, one per line.
func TestSlashLinkCopiesSelectedLinks(t *testing.T) {
	clip := clipCapture(t)
	m, _ := dbModel(t,
		database.Node{UUID: "a", Name: "alpha", Rank: 0},
		database.Node{UUID: "b", Name: "beta", Rank: 1},
	)
	cursorOn(m, "a")
	m.caret = 0
	m.press("shift+down") // select both rows
	if !m.selOn {
		t.Fatal("shift+down did not start a row selection")
	}

	mm, _ := m.runSlash("/link")
	got := mm.(*Model)
	if s := clip(); s != nodeLinkURI("a")+"\n"+nodeLinkURI("b") {
		t.Fatalf("copied %q, want the two links", s)
	}
	if got.selOn {
		t.Fatal("copy must release the row selection")
	}
	if !strings.Contains(got.flash, "copied 2 links to clipboard") {
		t.Fatalf("flash = %q, want the link count", got.flash)
	}
}

// TestSlashLinkResolvesMirrorToItsSource: /link on a mirror row copies the
// ORIGINAL's link — a mirror is the same node everywhere.
func TestSlashLinkResolvesMirrorToItsSource(t *testing.T) {
	clip := clipCapture(t)
	m, _ := dbModel(t,
		database.Node{UUID: "src", Name: "original", Rank: 0},
		database.Node{UUID: "mir", Name: "", MirrorOf: "src", Rank: 1},
	)
	cursorOn(m, "mir")
	m.caret = 0

	m.runSlash("/link")
	if s := clip(); s != nodeLinkURI("src") {
		t.Fatalf("copied %q, want the source's link %q", s, nodeLinkURI("src"))
	}
}

// TestDetectURLNearPicksNodeLink: a pasted lflow://node/<uuid> link reads like
// a bare URL — detectURLNear finds it, trailing punctuation trims, and it never
// matches when glued to a preceding word.
func TestDetectURLNearPicksNodeLink(t *testing.T) {
	if u := detectURLNear("see lflow://node/abc-123 here", 6); u == nil || u.raw != "lflow://node/abc-123" {
		t.Fatalf("pasted node link not found: %+v", u)
	}
	if u := detectURLNear("see lflow://node/abc-123.", 0); u == nil || u.raw != "lflow://node/abc-123" {
		t.Fatalf("trailing period should be trimmed: %+v", u)
	}
	if u := detectURLNear("xlflow://node/abc-123", 0); u != nil {
		t.Fatalf("scheme glued to a preceding word must not match: %+v", u)
	}
	if u := detectURLNear("see LFLLOW://node/abc", 0); u != nil {
		t.Fatalf("an upper-case node scheme is not a node link: %+v", u)
	}
}

// TestCtrlTConvertsPastedNodeLink: ctrl+t on a pasted lflow://node/<uuid> link
// turns it into a link chip named after its target node — the /link → paste →
// ctrl+t round trip. A URL keeps its old path (host label, normalized target).
func TestCtrlTConvertsPastedNodeLink(t *testing.T) {
	m, _ := dbModel(t,
		database.Node{UUID: "tgt", Name: "Target Node", Rank: 0},
		database.Node{UUID: "edit", Name: "", Rank: 1},
	)
	cursorOn(m, "edit")
	m.caret = 0

	// the /link clipboard payload lands as plain text (a paste)
	m.press(nodeLinkURI("tgt"))
	edit := m.tree.byUUID["edit"]
	if hasAnchor(edit.name) {
		t.Fatalf("pasting a node link must not auto-chip it: %q", edit.name)
	}
	m.caret = len([]rune(edit.name))
	m.feed(key("ctrl+t"))

	edit = m.tree.byUUID["edit"]
	if !hasAnchor(edit.name) {
		t.Fatalf("ctrl+t did not chip the node link: %q", edit.name)
	}
	c, ok := linkChipOf(m)
	if !ok {
		t.Fatal("no link chip created")
	}
	if c.Value != nodeLinkURI("tgt") {
		t.Errorf("chip target = %q, want %q", c.Value, nodeLinkURI("tgt"))
	}
	if c.Label != "Target Node" {
		t.Errorf("chip label = %q, want the node's name Target Node", c.Label)
	}
	if got := displayAnchors(edit.name, m.chips); got != "→Target Node" {
		t.Errorf("rendered link = %q, want →Target Node", got)
	}
}

// TestCtrlTNodeLinkLabelFallsBackToURI: converting a node link whose target has
// been deleted still works — the chip names itself after the URI.
func TestCtrlTNodeLinkLabelFallsBackToURI(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: ""})
	cursorOn(m, "edit")
	m.caret = 0

	m.press(nodeLinkURI("ghost"))
	m.caret = len([]rune(nodeLinkURI("ghost")))
	m.feed(key("ctrl+t"))

	c, ok := linkChipOf(m)
	if !ok {
		t.Fatal("no link chip created")
	}
	if c.Value != nodeLinkURI("ghost") || c.Label != nodeLinkURI("ghost") {
		t.Errorf("chip = %+v, want value+label both %q", c, nodeLinkURI("ghost"))
	}
}

// TestPasteLinkOverSelectionChipsTheRun: select a phrase, paste a URL — the
// run becomes a link chip named after the selected text with the URL as its
// target. No ctrl+t: the paste over the selection IS the conversion.
func TestPasteLinkOverSelectionChipsTheRun(t *testing.T) {
	m, _ := dbModel(t,
		database.Node{UUID: "edit", Name: "see the design here", Rank: 0},
	)
	cursorOn(m, "edit")
	m.caret = len([]rune("see "))
	m.press("ctrl+shift+right") // "the"
	m.press("ctrl+shift+right") // " the design"
	if _, lo, hi, ok := m.textSelection(); !ok || lo != 4 || hi != 14 {
		t.Fatalf("selection = [%d,%d) ok=%v, want [4,14) 'the design'", lo, hi, ok)
	}

	m.press("https://example.com/design") // a paste: one runes message

	edit := m.tree.byUUID["edit"]
	if got := displayAnchors(edit.name, m.chips); got != "see →the design here" {
		t.Fatalf("after paste: %q, want the run replaced by a link chip", got)
	}
	c, ok := linkChipOf(m)
	if !ok {
		t.Fatal("no link chip created")
	}
	if c.Value != "https://example.com/design" {
		t.Errorf("chip target = %q", c.Value)
	}
	if c.Label != "the design" {
		t.Errorf("chip label = %q, want the selected text", c.Label)
	}
	if m.textSelOn {
		t.Fatal("the paste must release the selection")
	}
	if m.caret != 4+len([]rune(database.ChipAnchor(c.ID))) {
		t.Errorf("caret = %d, want just past the chip", m.caret)
	}
}

// TestPasteNodeLinkOverSelectionChipsTheRun: the same gesture with an
// lflow://node/ link (the /link clipboard payload) — the chip points at the
// node and keeps the selected phrase as its name.
func TestPasteNodeLinkOverSelectionChipsTheRun(t *testing.T) {
	m, _ := dbModel(t,
		database.Node{UUID: "tgt", Name: "Target Node", Rank: 0},
		database.Node{UUID: "edit", Name: "see the design here", Rank: 1},
	)
	cursorOn(m, "edit")
	m.caret = len([]rune("see "))
	m.press("ctrl+shift+right")
	m.press("ctrl+shift+right")

	m.press(nodeLinkURI("tgt"))

	edit := m.tree.byUUID["edit"]
	if got := displayAnchors(edit.name, m.chips); got != "see →the design here" {
		t.Fatalf("after paste: %q", got)
	}
	c, ok := linkChipOf(m)
	if !ok {
		t.Fatal("no link chip created")
	}
	if c.Value != nodeLinkURI("tgt") {
		t.Errorf("chip target = %q, want %q", c.Value, nodeLinkURI("tgt"))
	}
	if c.Label != "the design" {
		t.Errorf("chip label = %q, want the selected text", c.Label)
	}
}

// TestPasteOverSelectionNonLinkReplacesPlainText: a paste that is not a link
// keeps the plain replace behavior — no chip, no conversion.
func TestPasteOverSelectionNonLinkReplacesPlainText(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: "alpha beta"})
	cursorOn(m, "edit")
	m.caret = len([]rune("alpha "))
	m.press("shift+right") // "b"
	m.press("shift+right") // "be"

	m.press("xx")

	edit := m.tree.byUUID["edit"]
	if edit.name != "alpha xxta" {
		t.Fatalf("name = %q, want plain replacement 'alpha xxta'", edit.name)
	}
	if len(m.chips) != 0 {
		t.Fatalf("a non-link paste must not create chips, got %v", m.chips)
	}
	if m.textSelOn {
		t.Fatal("the paste must release the selection")
	}
}

// TestPasteLinkOverSelectionTakesInnerChips: a chip inside the selected run
// goes with it — its record dies with its anchor, exactly like a plain
// deleteTextSelection over the same run. The run's visible text (the chip's
// display, "#qol") becomes the new link's name.
func TestPasteLinkOverSelectionTakesInnerChips(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: "see "})
	edit := m.tree.byUUID["edit"]
	m.cursor = m.rowIndexOf(edit)
	m.caret = len([]rune(edit.name))
	tagAnchor := m.createLabeledChip(chipKindTag, "qol", "")
	edit.name += tagAnchor + " here"
	tagID := anchorChipID(tagAnchor)
	// a styled run after the chip must slide along with the replacement
	m.setSpanStyle(edit, len([]rune("see "))+len([]rune(tagAnchor))+1, len([]rune(edit.name)), "color:red")

	textSelUUID, textSelLo, textSelHi = edit.uuid, len([]rune("see ")), len([]rune("see "))+len([]rune(tagAnchor))
	m.textSelOn = true

	m.press("https://example.com/x")

	if got := displayAnchors(edit.name, m.chips); got != "see →#qol here" {
		t.Fatalf("name = %q, want 'see →#qol here' (the chip's display is the label)", got)
	}
	if _, ok := m.chips[tagID]; ok {
		t.Fatal("the tag chip record outlived its anchor")
	}
	c, ok := linkChipOf(m)
	if !ok {
		t.Fatal("the link chip was not created")
	}
	if c.Label != "#qol" {
		t.Errorf("chip label = %q, want the selected run's display '#qol'", c.Label)
	}
	if c.Value != "https://example.com/x" {
		t.Errorf("chip target = %q", c.Value)
	}
}

// TestPasteLinkOverSelectionInNote: the note surface does the same — select a
// phrase in a note, paste a link, the run becomes the chip.
func TestPasteLinkOverSelectionInNote(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: "row", Note: "delete this word"})
	it := m.tree.byUUID["edit"]
	m.cursor = m.rowIndexOf(it)
	m.mode = modeNote
	m.caret = len("delete ")
	m.press("ctrl+shift+right") // "this"
	m.press("ctrl+shift+right") // "this word"

	m.press("https://example.com/notes")

	if got := displayAnchors(it.note, m.chips); got != "delete →this word" {
		t.Fatalf("note = %q, want the run replaced by a link chip", got)
	}
	c, ok := linkChipOf(m)
	if !ok {
		t.Fatal("no link chip created")
	}
	if c.Value != "https://example.com/notes" || c.Label != "this word" {
		t.Errorf("chip = %+v, want value https://example.com/notes, label 'this word'", c)
	}
}

// TestPasteLinkOverSelectionBareWWWNormalizes: a pasted bare "www." host gains
// https:// like every other URL entry point.
func TestPasteLinkOverSelectionBareWWWNormalizes(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: "read it"})
	cursorOn(m, "edit")
	m.caret = len([]rune("read "))
	m.press("shift+right")
	m.press("shift+right")

	m.press("www.example.com")

	c, ok := linkChipOf(m)
	if !ok {
		t.Fatal("no link chip created")
	}
	if c.Value != "https://www.example.com" {
		t.Errorf("chip target = %q, want normalized https://www.example.com", c.Value)
	}
}

// TestDetectURLNearPicksCaretAdjacent mirrors TestDetectDateCaretAware: with two
// URLs in the text, detectURLNear must act on the one nearest the caret, not
// always the leftmost.
func TestDetectURLNearPicksCaretAdjacent(t *testing.T) {
	text := "see https://a.com and https://b.com too"
	if u := detectURLNear(text, 6); u == nil || u.raw != "https://a.com" {
		t.Fatalf("caret in first url: got %+v", u)
	}
	if u := detectURLNear(text, len([]rune(text))-2); u == nil || u.raw != "https://b.com" {
		t.Fatalf("caret in second url: got %+v", u)
	}
}

// TestDetectURLNearTrimsPunctAndRejectsGluedScheme guards the same edge cases
// as the date phrase scanner: trailing sentence punctuation drops off, and a
// scheme glued to a preceding word (no space) is not a match.
func TestDetectURLNearTrimsPunctAndRejectsGluedScheme(t *testing.T) {
	if u := detectURLNear("visit https://x.com.", 0); u == nil || u.raw != "https://x.com" {
		t.Fatalf("trailing period should be trimmed: %+v", u)
	}
	if u := detectURLNear("nothttps://not-a-url", 0); u != nil {
		t.Fatalf("scheme glued to a preceding word must not match: %+v", u)
	}
}

// TestCtrlTConvertsURLToLinkChip: like ctrl+t on a natural-language date
// phrase, ctrl+t on a bare URL under the cursor converts it — but straight into
// a real link chip, since a URL has no separate "canonical text" step. Typing
// the URL itself (including a trailing space) must NOT have already chipped
// it — only this explicit key does.
func TestCtrlTConvertsURLToLinkChip(t *testing.T) {
	m, _ := dbModel(t, database.Node{UUID: "edit", Name: "", Rank: 0})
	cursorOn(m, "edit")
	m.caret = 0

	m.press("release notes https://example.com/release-notes ")
	edit := m.tree.byUUID["edit"]
	if hasAnchor(edit.name) {
		t.Fatalf("typing a url (with trailing space) must not auto-chip it: %q", edit.name)
	}

	m.caret = len([]rune(edit.name)) - 1 // land back inside the url text
	m.feed(key("ctrl+t"))

	edit = m.tree.byUUID["edit"]
	if !hasAnchor(edit.name) {
		t.Fatalf("ctrl+t did not chip the url: %q", edit.name)
	}
	c, ok := linkChipOf(m)
	if !ok {
		t.Fatal("no link chip created")
	}
	if c.Value != "https://example.com/release-notes" {
		t.Errorf("chip value = %q, want https://example.com/release-notes", c.Value)
	}
	if c.Label != "example.com" {
		t.Errorf("chip label = %q, want example.com", c.Label)
	}
}

// TestURLChipLabelSmartNames: a known service whose path names a resource gets
// "key/resource", a known service without one gets its name, everything else
// falls back to the host.
func TestURLChipLabelSmartNames(t *testing.T) {
	cases := []struct{ url, want string }{
		{"https://huggingface.co/datasets/alimetin/turkish-parliament-speech", "huggingface/turkish-parliament-speech"},
		{"https://huggingface.co/stabilityai/stable-diffusion-3", "huggingface/stable-diffusion-3"},
		{"https://huggingface.co/", "HuggingFace"},
		{"https://github.com/torvalds/linux", "github/torvalds/linux"},
		{"https://docs.google.com/spreadsheets/d/1abc/edit", "Sheets"},
		{"https://example.com/docs", "example.com"},
	}
	for _, c := range cases {
		if got := urlChipLabel(c.url); got != c.want {
			t.Errorf("urlChipLabel(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

// TestLinkExpandForExport guards the machine-readable form used by export/grep.
func TestLinkExpandForExport(t *testing.T) {
	c := database.Chip{Kind: chipKindLink, Value: "https://x.com", Label: "X"}
	if got := chipExpand(c); got != "[X](https://x.com)" {
		t.Errorf("expand = %q, want [X](https://x.com)", got)
	}
	if got := chipDisplay(c); got != "→X" {
		t.Errorf("display = %q, want →X", got)
	}
	if !strings.Contains(database.ExpandAnchors(database.ChipAnchor("z"), map[string]database.Chip{"z": c}), "[X](https://x.com)") {
		t.Error("ExpandAnchors did not resolve a link chip")
	}
}
