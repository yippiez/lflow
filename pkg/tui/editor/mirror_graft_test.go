package editor

import (
	"testing"

	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/database"
)

// graftDB seeds an outline where a mirror inside "zone" points at a source
// that lives outside that subtree:
//
//	Root
//	├─ zone
//	│  ╰─ (mirror of elsewhere, collapsed)
//	╰─ elsewhere
//	   ├─ e-one
//	   ╰─ e-two
func graftDB(t *testing.T) *database.DB {
	t.Helper()
	db := database.InitTestMemoryDB(t)
	if err := database.EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	nodes := []database.Node{
		{UUID: "zone", ParentUUID: database.RootUUID, Rank: 0, Name: "zone"},
		{UUID: "m-out", ParentUUID: "zone", Rank: 0, MirrorOf: "elsewhere", Collapsed: true},
		{UUID: "elsewhere", ParentUUID: database.RootUUID, Rank: 1, Name: "elsewhere"},
		{UUID: "e1", ParentUUID: "elsewhere", Rank: 0, Name: "e-one"},
		{UUID: "e2", ParentUUID: "elsewhere", Rank: 1, Name: "e-two"},
	}
	for _, n := range nodes {
		n.Type = database.TypeBullets
		if err := n.Insert(db); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// TestLoadTreeGraftsExternalMirrorSource: opening a subtree grafts the live
// subtree of a mirror source that lives outside it, so the mirror resolves its
// name and can uncollapse to show the source's children through.
func TestLoadTreeGraftsExternalMirrorSource(t *testing.T) {
	db := graftDB(t)
	tr, err := loadTree(db, "zone")
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.external) != 1 || tr.external[0].uuid != "elsewhere" {
		t.Fatalf("want elsewhere grafted as the one external root, got %v", tr.external)
	}
	mir := tr.byUUID["m-out"]
	if got := tr.displayName(mir); got != "elsewhere" {
		t.Fatalf("mirror name should resolve through the graft, got %q", got)
	}
	kids := tr.childItems(mir)
	if len(kids) != 2 {
		t.Fatalf("mirror should resolve the grafted source's 2 children, got %d", len(kids))
	}
	mir.collapsed = false
	rows := tr.visibleRows(tr.root, false)
	var names []string
	for _, r := range rows {
		names = append(names, tr.displayName(r.it))
	}
	want := []string{"elsewhere", "e-one", "e-two"}
	if len(names) != 3 || names[0] != want[0] || names[1] != want[1] || names[2] != want[2] {
		t.Fatalf("uncollapsed mirror should show grafted children through, got %v", names)
	}
	// the graft must not dirty the tree: a fresh load saves nothing
	if written, err := tr.save(); err != nil || written != 0 {
		t.Fatalf("fresh load should save 0 nodes, wrote %d (err %v)", written, err)
	}
}

// TestGraftedEditPersists: an edit made through the mirror lands on the real
// node in the DB, and the grafted root keeps its original position.
func TestGraftedEditPersists(t *testing.T) {
	db := graftDB(t)
	tr, err := loadTree(db, "zone")
	if err != nil {
		t.Fatal(err)
	}
	tr.byUUID["e1"].name = "e-one edited"
	if _, err := tr.save(); err != nil {
		t.Fatal(err)
	}
	n, err := database.GetNode(db, "e1")
	if err != nil {
		t.Fatal(err)
	}
	if n.Name != "e-one edited" || n.ParentUUID != "elsewhere" {
		t.Fatalf("edit through graft should persist in place, got %q under %q", n.Name, n.ParentUUID)
	}
	src, err := database.GetNode(db, "elsewhere")
	if err != nil {
		t.Fatal(err)
	}
	if src.ParentUUID != database.RootUUID || src.Deleted {
		t.Fatalf("grafted root must keep its DB position, got parent %q deleted=%v", src.ParentUUID, src.Deleted)
	}
}

// TestUndoKeepsGraftedSubtree: undo rebuilds byUUID from root AND the grafted
// roots — without that, the reconcile pass would tombstone every grafted node.
func TestUndoKeepsGraftedSubtree(t *testing.T) {
	db := graftDB(t)
	tr, err := loadTree(db, "zone")
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
	}
	m.refreshRows()
	m.pushUndo("")
	tr.byUUID["e1"].name = "scratch"
	m.undo()
	for _, uuid := range m.tree.deleted {
		if uuid == "elsewhere" || uuid == "e1" || uuid == "e2" {
			t.Fatalf("undo queued grafted node %s for tombstoning", uuid)
		}
	}
	if it := m.tree.byUUID["e1"]; it == nil || it.name != "e-one" {
		t.Fatalf("undo should restore the grafted subtree, got %v", it)
	}
	if _, err := m.tree.save(); err != nil {
		t.Fatal(err)
	}
	if n, err := database.GetNode(db, "e1"); err != nil || n.Deleted {
		t.Fatalf("grafted node must survive undo+save, got %v (err %v)", n, err)
	}
}

// TestGraftSkipsOverlappingSource: a mirror pointing at an ancestor of the
// loaded subtree cannot graft (byUUID stays one item per uuid) and falls back
// to the name stub, like before.
func TestGraftSkipsOverlappingSource(t *testing.T) {
	db := graftDB(t)
	up := database.Node{UUID: "m-up", ParentUUID: "zone", Rank: 1, MirrorOf: database.RootUUID, Type: database.TypeBullets}
	if err := up.Insert(db); err != nil {
		t.Fatal(err)
	}
	tr, err := loadTree(db, "zone")
	if err != nil {
		t.Fatal(err)
	}
	if _, in := tr.byUUID[database.RootUUID]; in {
		t.Fatal("an ancestor source must not graft into the tree")
	}
	if tr.externalNames[database.RootUUID] == "" {
		t.Fatal("ungraftable source should leave a name stub")
	}
}
