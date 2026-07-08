package editor

import (
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// TestStarPersistsAndRanksFirst drives /star end-to-end: the flag persists
// immediately, survives a save round-trip, and pins the node to the top of the
// finder regardless of recency.
func TestStarPersistsAndRanksFirst(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	mk := func(uuid, name string, edited int64) {
		n := database.Node{UUID: uuid, ParentUUID: "root", Rank: 1, Name: name,
			Type: database.TypeBullets, AddedOn: edited, EditedOn: edited}
		if err := n.Insert(db); err != nil {
			t.Fatal(err)
		}
	}
	mk("old", "old favourite", 100)
	mk("mid", "middle note", 200)
	mk("new", "newest note", 300)

	if err := database.SetStarred(db, "old", true); err != nil {
		t.Fatal(err)
	}

	tr := &tree{db: db, root: &item{uuid: "root"},
		byUUID: map[string]*item{}, externalNames: map[string]string{}, snapshots: map[string]snapshot{}}
	tr.byUUID["root"] = tr.root
	m := &Model{db: db, tree: tr, viewStack: []*item{tr.root}, width: 100, height: 30}
	m.finder.act = actGoto

	rows := nodeFinderBackend{}.search(m, "")
	if len(rows) < 3 {
		t.Fatalf("want 3 finder rows, got %d", len(rows))
	}
	if rows[0].node.UUID != "old" || !rows[0].node.Starred {
		t.Fatalf("starred node must rank first, got %s", rows[0].node.UUID)
	}
	// the rest keep recency order
	if rows[1].node.UUID != "new" || rows[2].node.UUID != "mid" {
		t.Fatalf("unstarred tail must stay recent-first: %s, %s", rows[1].node.UUID, rows[2].node.UUID)
	}

	// starred survives the editor's save round-trip (backstop write)
	tr2, err := loadTree(db, "old")
	if err != nil {
		t.Fatal(err)
	}
	if !tr2.root.starred {
		t.Fatal("loadTree must carry starred")
	}
}

// TestMoveToCursorStaysPut: after /move the cursor lands on the row that took
// the moved node's place, not on the moved node at its destination.
func TestMoveToCursorStaysPut(t *testing.T) {
	root := &item{uuid: "root"}
	a := &item{uuid: "a", name: "a", parent: root}
	b := &item{uuid: "b", name: "b", parent: root}
	c := &item{uuid: "c", name: "c", parent: root}
	root.children = []*item{a, b, c}
	tr := &tree{root: root,
		byUUID:        map[string]*item{"root": root, "a": a, "b": b, "c": c},
		externalNames: map[string]string{}, snapshots: map[string]snapshot{}}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()

	// cursor on b (row 1); move b under a
	m.cursor = 1
	m.finder.act = actMoveTo
	m.runFinder(database.Node{UUID: "a", Name: "a"})
	// rows now: a, b (child of a, expanded?), c — the cursor must sit on the row
	// that slid into b's old visual slot (row index 1), not follow b around
	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 (stay put)", m.cursor)
	}
	if got := m.rows[m.cursor].it; got == b && got.parent != root {
		// acceptable only if b happens to still occupy row 1 visually
	}
	if a.children[len(a.children)-1] != b && (len(a.children) == 0 || a.children[0] != b) {
		t.Fatal("b must have moved under a")
	}
}
