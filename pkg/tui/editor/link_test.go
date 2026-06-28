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
	if m.mode != modeFinder || m.finderAct != actLinkInsert {
		t.Fatalf("[[ did not open the link finder: mode=%v act=%v", m.mode, m.finderAct)
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
