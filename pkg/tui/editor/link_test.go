package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/database"
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
		ctx:       context.DnoteCtx{DB: db},
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
