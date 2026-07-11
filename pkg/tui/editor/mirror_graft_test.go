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
	rows := tr.visibleRows(tr.root, false, nil)
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

// cycleModel builds root → project → notes → (mirror of project): the
// ancestor-mirror cycle, plus a Model wired enough for expand/collapse steps.
func cycleModel(t *testing.T) (*Model, *item) {
	t.Helper()
	root := &item{uuid: "root"}
	p := &item{uuid: "p", name: "project", parent: root}
	n := &item{uuid: "n", name: "notes", parent: p}
	mir := &item{uuid: "mir", mirrorOf: "p", parent: n}
	root.children = []*item{p}
	p.children = []*item{n}
	n.children = []*item{mir}
	tr := &tree{
		root:          root,
		snapshots:     map[string]snapshot{},
		externalNames: map[string]string{},
		byUUID:        map[string]*item{"root": root, "p": p, "n": n, "mir": mir},
	}
	m := &Model{db: database.InitTestMemoryDB(t), tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()
	return m, mir
}

// TestCycleUnrollsOneLevelPerExpand: a mirror of an ancestor starts as a
// foldable leaf and each expand press on the deepest repetition reveals
// exactly one more cycle level; collapse folds them back the same way.
func TestCycleUnrollsOneLevelPerExpand(t *testing.T) {
	m, mir := cycleModel(t)
	// budget 0: project, notes, mirror — the mirror is a suppressed repetition
	if len(m.rows) != 3 {
		t.Fatalf("want 3 rows before unrolling, got %d", len(m.rows))
	}
	last := m.rows[2]
	if last.it != mir || !last.cycled || last.cycleDepth != 1 {
		t.Fatalf("mirror row should be cycled at depth 1, got cycled=%v depth=%d", last.cycled, last.cycleDepth)
	}

	m.cursor = 2
	m.expandStep() // one level: … mirror → notes' → mirror''
	if len(m.rows) != 5 {
		t.Fatalf("want 5 rows after one unroll, got %d", len(m.rows))
	}
	if m.rows[2].it != mir || m.rows[2].cycled || m.rows[3].it.uuid != "n" {
		t.Fatalf("first repetition should now expand through notes")
	}
	deepest := m.rows[4]
	if deepest.it != mir || !deepest.cycled || deepest.cycleDepth != 2 {
		t.Fatalf("deepest repetition should be cycled at depth 2, got cycled=%v depth=%d", deepest.cycled, deepest.cycleDepth)
	}

	m.cursor = 4
	m.expandStep() // second level
	if len(m.rows) != 7 || m.unroll["mir"] != 2 {
		t.Fatalf("want 7 rows and budget 2 after two unrolls, got %d rows budget %d", len(m.rows), m.unroll["mir"])
	}

	m.cursor = 4 // the now-expanded depth-2 repetition
	m.collapseStep()
	if len(m.rows) != 5 || m.unroll["mir"] != 1 {
		t.Fatalf("collapse should fold back to one level, got %d rows budget %d", len(m.rows), m.unroll["mir"])
	}

	m.cursor = 2 // the first repetition: folding it collapses the flag and rewinds
	m.collapseStep()
	if len(m.rows) != 3 || !mir.collapsed {
		t.Fatalf("collapsing the first level should fold the mirror itself, got %d rows collapsed=%v", len(m.rows), mir.collapsed)
	}
	if _, budgeted := m.unroll["mir"]; budgeted {
		t.Fatal("folding the first level should rewind the unroll budget")
	}

	m.cursor = 2
	m.expandStep() // from collapsed: one press back to exactly one level
	if len(m.rows) != 5 || mir.collapsed || m.unroll["mir"] != 1 {
		t.Fatalf("expand from collapsed should show one level, got %d rows collapsed=%v budget=%d", len(m.rows), mir.collapsed, m.unroll["mir"])
	}
}
