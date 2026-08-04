package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/database"
)

// mirrorEditModel opens an outline holding a source node and, above it, an
// ordinary mirror of it — the shape where the same node is on screen twice.
func mirrorEditModel(t *testing.T) (*Model, *database.DB) {
	t.Helper()
	db := database.InitTestMemoryDB(t)
	if err := database.EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	for _, n := range []database.Node{
		{UUID: "mir", ParentUUID: database.RootUUID, Rank: 0, MirrorOf: "src"},
		{UUID: "src", ParentUUID: database.RootUUID, Rank: 1, Name: "the one real node"},
	} {
		n.Type = database.TypeBullets
		if err := n.Insert(db); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := loadTree(db, database.RootUUID)
	if err != nil {
		t.Fatal(err)
	}
	m := &Model{db: db, ctx: context.DnoteCtx{DB: db}, tree: tr,
		viewStack: []*item{tr.root}, width: 80, height: 24,
		chips: map[string]database.Chip{}}
	m.refreshRows()
	return m, db
}

// onRow parks the cursor on the row for uuid, caret at the end of what that row
// shows.
func onRow(t *testing.T, m *Model, uuid string) *item {
	t.Helper()
	i := m.rowIndexOfUUID(uuid)
	if i < 0 {
		t.Fatalf("no row for %q", uuid)
	}
	m.cursor = i
	it := m.rows[i].it
	m.caret = len([]rune(m.caretText(it)))
	return it
}

// TestMirrorTypesIntoItsSource: a mirror shows the source's words, so typing on
// it types into the source — and the mirror's own row shows the result at once,
// because there was only ever one node.
func TestMirrorTypesIntoItsSource(t *testing.T) {
	m, _ := mirrorEditModel(t)
	mir := onRow(t, m, "mir")

	for _, r := range "!" {
		m.press(string(r))
	}
	src := m.tree.byUUID["src"]
	if src.name != "the one real node!" {
		t.Errorf("source = %q, want the typed character", src.name)
	}
	if mir.name != "" {
		t.Errorf("the mirror grew a name of its own: %q", mir.name)
	}
	if got := m.tree.displayName(mir); got != "the one real node!" {
		t.Errorf("mirror row reads %q", got)
	}
	// and the cursor stayed on the mirror, where the user is working
	if m.rows[m.cursor].it != mir {
		t.Error("the cursor jumped off the mirror to the original")
	}
}

// TestMirrorBackspaceAndWordDeleteReachTheSource: the same for the deletions.
func TestMirrorBackspaceAndWordDeleteReachTheSource(t *testing.T) {
	m, _ := mirrorEditModel(t)
	onRow(t, m, "mir")

	m.press("backspace")
	if got := m.tree.byUUID["src"].name; got != "the one real nod" {
		t.Errorf("after backspace source = %q", got)
	}
	m.press("ctrl+w")
	if got := m.tree.byUUID["src"].name; got != "the one real " {
		t.Errorf("after word delete source = %q", got)
	}
}

// TestMirrorCaretWalksTheTextItShows: the caret used to be pinned at column 0 on
// a mirror, because it was measured against the mirror's own empty name.
func TestMirrorCaretWalksTheTextItShows(t *testing.T) {
	m, _ := mirrorEditModel(t)
	onRow(t, m, "mir")
	m.caret = 0

	m.press("right")
	m.press("right")
	if m.caret != 2 {
		t.Fatalf("caret = %d after two rights, want 2", m.caret)
	}
	m.press("end")
	if want := len([]rune("the one real node")); m.caret != want {
		t.Errorf("caret = %d at end, want %d", m.caret, want)
	}
}

// TestMirrorStylesTheSource: /style on a mirror colors the one real node, so
// every place it appears takes the color.
func TestMirrorStylesTheSource(t *testing.T) {
	m, _ := mirrorEditModel(t)
	onRow(t, m, "mir")

	src := m.tree.byUUID["src"]
	m.mode = modeStyle
	styleSource{}.onSelect(m, pickerItem{value: "lightgreen"})
	if got := styleColor(src.style); got != "lightgreen" {
		t.Errorf("source style = %q, want the color applied through the mirror", src.style)
	}
	if m.tree.byUUID["mir"].style != "" {
		t.Errorf("the mirror kept a style of its own: %q", m.tree.byUUID["mir"].style)
	}
}

// TestMirrorRowDrawsACaret: the row a mirror occupies is a row you can put the
// cursor on, so it has to show where the cursor is.
func TestMirrorRowDrawsACaret(t *testing.T) {
	m, _ := mirrorEditModel(t)
	onRow(t, m, "mir")
	m.caret = 0

	view := m.View()
	if !strings.Contains(view, cInvert) {
		t.Error("the selected mirror row drew no caret")
	}
}

// TestQueryResultRefusesEditsButKeepsItsCaret: a materialized query row is the
// query's, not yours. It takes the cursor and shows the caret — so you can see
// what the keys are aimed at — and does nothing when you type.
func TestQueryResultRefusesEditsButKeepsItsCaret(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	if err := database.EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	for _, n := range []database.Node{
		{UUID: "q", ParentUUID: database.RootUUID, Rank: 0, Name: "#tag", Type: database.TypeQuery},
		{UUID: "hit", ParentUUID: "q", Rank: 0, MirrorOf: "src", Type: database.TypeBullets,
			Lock: database.LockIndentOutdent},
		{UUID: "src", ParentUUID: database.RootUUID, Rank: 1, Name: "a real note", Type: database.TypeBullets},
	} {
		if err := n.Insert(db); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := loadTree(db, database.RootUUID)
	if err != nil {
		t.Fatal(err)
	}
	m := &Model{db: db, ctx: context.DnoteCtx{DB: db}, tree: tr,
		viewStack: []*item{tr.root}, width: 80, height: 24,
		chips: map[string]database.Chip{}}
	m.refreshRows()

	hit := m.tree.byUUID["hit"]
	if !hit.queryGenerated() {
		t.Fatal("the fixture is not a query-generated row")
	}
	onRow(t, m, "hit")

	if m.editTarget() != nil {
		t.Error("a query result offered an edit target")
	}
	m.press("x")
	m.press("backspace")
	if got := m.tree.byUUID["src"].name; got != "a real note" {
		t.Errorf("a fixed row let an edit through to %q", got)
	}
	// but the caret is still drawn: the row is fixed, not invisible
	m.caret = 0
	if !strings.Contains(m.View(), cInvert) {
		t.Error("the fixed row drew no caret")
	}
}
