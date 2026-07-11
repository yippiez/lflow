package editor

import (
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// TestFinderHidesEmptyAndMirrorRows: every picker leaves out unnamed nodes
// (noise) and mirror rows (a pick resolves to the original anyway, so the
// original alone is enough).
func TestFinderHidesEmptyAndMirrorRows(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	mk := func(uuid, name, mirrorOf string) {
		n := database.Node{UUID: uuid, ParentUUID: "root", Rank: 1, Name: name,
			MirrorOf: mirrorOf, Type: database.TypeBullets, AddedOn: 100, EditedOn: 100}
		if err := n.Insert(db); err != nil {
			t.Fatal(err)
		}
	}
	mk("real", "groceries", "")
	mk("empty", "", "")
	mk("mir", "", "real")

	tr := &tree{db: db, root: &item{uuid: "root"},
		byUUID: map[string]*item{}, externalNames: map[string]string{}, snapshots: map[string]snapshot{}}
	tr.byUUID["root"] = tr.root
	m := &Model{db: db, tree: tr, viewStack: []*item{tr.root}, width: 100, height: 30}

	for _, act := range []finderAction{actMirrorHere, actMirrorFrom, actMoveTo, actGoto, actBringHere, actLinkInsert} {
		m.finder.act = act
		rows := nodeFinderBackend{}.search(m, "")
		if len(rows) != 1 || rows[0].node.UUID != "real" {
			t.Fatalf("act %d: want only the real node, got %d rows", act, len(rows))
		}
	}
}

// TestFinderHidesSearchHiddenTypes: agent replies (and any search-hidden type)
// never show as finder rows — they are thread answers, not navigation targets.
func TestFinderHidesSearchHiddenTypes(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	mk := func(uuid, name, typ string) {
		n := database.Node{UUID: uuid, ParentUUID: "root", Rank: 1, Name: name,
			Type: typ, AddedOn: 100, EditedOn: 100}
		if err := n.Insert(db); err != nil {
			t.Fatal(err)
		}
	}
	mk("real", "groceries list", database.TypeBullets)
	mk("reply", "groceries look fine to me", database.TypeAgent)

	tr := &tree{db: db, root: &item{uuid: "root"},
		byUUID: map[string]*item{}, externalNames: map[string]string{}, snapshots: map[string]snapshot{}}
	tr.byUUID["root"] = tr.root
	m := &Model{db: db, tree: tr, viewStack: []*item{tr.root}, width: 100, height: 30}

	for _, query := range []string{"", "groceries"} {
		for _, act := range []finderAction{actMirrorHere, actMirrorFrom, actMoveTo, actGoto, actBringHere, actLinkInsert} {
			m.finder.act = act
			rows := nodeFinderBackend{}.search(m, query)
			if len(rows) != 1 || rows[0].node.UUID != "real" {
				t.Fatalf("act %d query %q: want only the real node, got %d rows", act, query, len(rows))
			}
		}
	}
}

// TestFinderRowsAreOneLine: a multi-paragraph node name flattens to a single
// picker row — embedded newlines never wrap the finder list.
func TestFinderRowsAreOneLine(t *testing.T) {
	got := oneLine("Two tables, two keys.\n\n`chips` holds the command only:\n>>> chips row")
	if want := "Two tables, two keys. `chips` holds the command only: >>> chips row"; got != want {
		t.Fatalf("oneLine = %q, want %q", got, want)
	}
	if s := "already one line"; oneLine(s) != s {
		t.Fatal("single-line text must pass through untouched")
	}
}

// TestMirrorFromPlantsMirrorUnderTarget: /mirror:from creates a mirror of the
// cursor node as the first child of the picked target, leaving the original.
func TestMirrorFromPlantsMirrorUnderTarget(t *testing.T) {
	root := &item{uuid: "root"}
	a := &item{uuid: "a", name: "a", parent: root}
	b := &item{uuid: "b", name: "b", parent: root}
	bc := &item{uuid: "bc", name: "b child", parent: b}
	b.children = []*item{bc}
	root.children = []*item{a, b}
	tr := &tree{root: root,
		byUUID:        map[string]*item{"root": root, "a": a, "b": b, "bc": bc},
		externalNames: map[string]string{}, snapshots: map[string]snapshot{}}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()

	// cursor on a (row 0); mirror it under b
	m.cursor = 0
	m.finder.act = actMirrorFrom
	m.runFinder(database.Node{UUID: "b", Name: "b"})

	if a.parent != root {
		t.Fatal("original must stay put")
	}
	if len(b.children) != 2 || b.children[0].mirrorOf != "a" {
		t.Fatalf("b's first child must be a mirror of a, children=%d", len(b.children))
	}
	if !m.unsaved {
		t.Fatal("planting a mirror must mark the tree unsaved")
	}
}
