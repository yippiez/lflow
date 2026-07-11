package editor

import (
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// TestBacklinksFinderListsMirrorsAndLinks: /backlinks surfaces nodes that
// mirror or [[-link to the cursor node, keeps mirror rows (unlike other
// pickers), and ranks them like the rest of the finder.
func TestBacklinksFinderListsMirrorsAndLinks(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	insert := func(n database.Node) {
		t.Helper()
		if err := n.Insert(db); err != nil {
			t.Fatal(err)
		}
	}
	insert(database.Node{UUID: "root", Name: "root", Type: database.TypeBullets, AddedOn: 1, EditedOn: 1})
	insert(database.Node{UUID: "src", ParentUUID: "root", Name: "the source", Type: database.TypeBullets, Rank: 0, AddedOn: 10, EditedOn: 10})
	// mirror of src under another parent
	insert(database.Node{UUID: "mir", ParentUUID: "root", Name: "", MirrorOf: "src", Type: database.TypeBullets, Rank: 1, AddedOn: 20, EditedOn: 20})
	// a node with a link chip pointing at src
	chipID := "lk1"
	if err := database.UpsertChip(db, database.Chip{ID: chipID, Kind: "link", Value: "lflow://node/src", Label: "the source"}); err != nil {
		t.Fatal(err)
	}
	insert(database.Node{
		UUID: "linker", ParentUUID: "root",
		Name: "mentions " + database.ChipAnchor(chipID),
		Type: database.TypeBullets, Rank: 2, AddedOn: 30, EditedOn: 30,
	})
	// noise: unrelated + empty non-mirror
	insert(database.Node{UUID: "noise", ParentUUID: "root", Name: "noise", Type: database.TypeBullets, Rank: 3, AddedOn: 40, EditedOn: 40})

	src := &item{uuid: "src", name: "the source"}
	root := &item{uuid: "root", name: "root", children: []*item{src}}
	src.parent = root
	tr := &tree{
		db: db, root: root,
		byUUID:        map[string]*item{"root": root, "src": src},
		externalNames: map[string]string{}, snapshots: map[string]snapshot{},
	}
	m := &Model{
		db: db, tree: tr, viewStack: []*item{root},
		width: 100, height: 30, chips: map[string]database.Chip{},
	}
	// cursor on the source
	m.refreshRows()
	m.cursor = 0
	// land cursor on src if it is in rows
	if i := m.rowIndexOf(src); i >= 0 {
		m.cursor = i
	}

	m.finder.act = actBacklinks
	rows := nodeFinderBackend{}.search(m, "")
	ids := map[string]bool{}
	for _, r := range rows {
		ids[r.node.UUID] = true
	}
	if !ids["mir"] || !ids["linker"] {
		t.Fatalf("want mir + linker backlinks, got %v", ids)
	}
	if ids["src"] || ids["noise"] {
		t.Fatalf("must not list self or unrelated: %v", ids)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d: %v", len(rows), ids)
	}

	// query filters by resolved name ("the source - mirror" / "mentions …")
	rows = nodeFinderBackend{}.search(m, "mirror")
	if len(rows) != 1 || rows[0].node.UUID != "mir" {
		t.Fatalf("query 'mirror' should hit only the mirror row, got %d", len(rows))
	}
}

// TestBacklinksSlashOpensFinder: /backlinks lands in the finder with actBacklinks.
func TestBacklinksSlashOpensFinder(t *testing.T) {
	root := &item{uuid: "root", name: "root"}
	a := &item{uuid: "a", name: "a", parent: root}
	root.children = []*item{a}
	tr := &tree{root: root, byUUID: map[string]*item{"root": root, "a": a},
		externalNames: map[string]string{}, snapshots: map[string]snapshot{}}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()
	m.cursor = 0

	mm, _ := m.runSlash("/backlinks")
	got := mm.(*Model)
	if got.mode != modeFinder {
		t.Fatalf("mode = %v, want modeFinder", got.mode)
	}
	if got.finder.act != actBacklinks {
		t.Fatalf("finder.act = %v, want actBacklinks", got.finder.act)
	}
}
