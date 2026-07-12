package editor

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/pkg/tui/context"
	"github.com/lflow/lflow/pkg/tui/database"
	"github.com/lflow/lflow/pkg/tui/wire"
)

// The /mirror:to and /mirror:from end-to-end tests: real keystrokes through
// Update — "/" typed inline, the slash menu filtered, the finder queried, the
// pick committed — then the tree and the persisted rows are checked. They
// pin the whole path a user actually travels, not just runFinder.

func typeKeys(m *Model, s string) {
	for _, r := range s {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func pressEnter(m *Model) {
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
}

// mirrorTestModel seeds root → alpha, beta(beta kid) and opens a model on root.
func mirrorTestModel(t *testing.T) (*Model, *database.DB) {
	t.Helper()
	db := database.InitTestMemoryDB(t)
	if err := database.EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	for _, n := range []database.Node{
		{UUID: "alpha", ParentUUID: database.RootUUID, Rank: 0, Name: "alpha"},
		{UUID: "beta", ParentUUID: database.RootUUID, Rank: 1, Name: "beta"},
		{UUID: "bkid", ParentUUID: "beta", Rank: 0, Name: "beta kid"},
	} {
		n.Type = database.TypeBullets
		n.AddedOn, n.EditedOn = 100, 100
		if err := n.Insert(db); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := loadTree(db, database.RootUUID)
	if err != nil {
		t.Fatal(err)
	}
	m := &Model{db: db, ctx: context.DnoteCtx{DB: db}, tree: tr, viewStack: []*item{tr.root}, width: 100, height: 30}
	m.refreshRows()
	return m, db
}

// TestMirrorToKeyFlow: /mirror:to on a named node lands a mirror of the picked
// target as the next sibling, the typed slash text is stripped, and the saved
// row carries mirror_of.
func TestMirrorToKeyFlow(t *testing.T) {
	m, db := mirrorTestModel(t)
	m.cursor = 0 // alpha
	typeKeys(m, "/mirror:to")
	pressEnter(m) // run the slash command → finder opens
	if m.mode != modeFinder {
		t.Fatalf("mode = %d, want finder", m.mode)
	}
	typeKeys(m, "beta")
	pressEnter(m) // pick beta

	alpha := m.tree.byUUID["alpha"]
	if alpha.name != "alpha" {
		t.Fatalf("slash text must strip, alpha = %q", alpha.name)
	}
	sibs := m.tree.root.children
	if len(sibs) != 3 || sibs[1].mirrorOf != "beta" {
		t.Fatalf("want a mirror of beta after alpha, got %d siblings", len(sibs))
	}
	if got := m.tree.displayName(sibs[1]); got != "beta" {
		t.Fatalf("mirror must show the source name, got %q", got)
	}
	if _, err := m.saveAll(); err != nil {
		t.Fatal(err)
	}
	n, err := database.GetNode(db, sibs[1].uuid)
	if err != nil || n.MirrorOf != "beta" || n.Name != "" {
		t.Fatalf("persisted mirror row = %+v (err %v)", n, err)
	}
}

// TestMirrorToOnEmptyNodeKeyFlow: on a fresh empty node the node itself
// becomes the mirror instead of spawning a sibling.
func TestMirrorToOnEmptyNodeKeyFlow(t *testing.T) {
	m, _ := mirrorTestModel(t)
	m.cursor = 0
	pressEnter(m) // new empty sibling under root
	cur := m.cursorItem()
	if cur.name != "" {
		t.Fatalf("setup: cursor should be an empty node, got %q", cur.name)
	}
	typeKeys(m, "/mirror:to")
	pressEnter(m)
	typeKeys(m, "beta")
	pressEnter(m)

	if cur.mirrorOf != "beta" || cur.name != "" {
		t.Fatalf("the empty node must become the mirror, got mirrorOf=%q name=%q", cur.mirrorOf, cur.name)
	}
	if len(m.tree.root.children) != 3 {
		t.Fatalf("no extra sibling should appear, got %d", len(m.tree.root.children))
	}
}

// TestMirrorFromKeyFlow: /mirror:from plants a mirror of the cursor node under
// the picked target; the original stays put and the row persists.
func TestMirrorFromKeyFlow(t *testing.T) {
	m, db := mirrorTestModel(t)
	m.cursor = 0 // alpha
	typeKeys(m, "/mirror:from")
	pressEnter(m)
	if m.mode != modeFinder {
		t.Fatalf("mode = %d, want finder", m.mode)
	}
	typeKeys(m, "beta")
	pressEnter(m)

	alpha := m.tree.byUUID["alpha"]
	if alpha.parent != m.tree.root {
		t.Fatal("original must stay put")
	}
	beta := m.tree.byUUID["beta"]
	if len(beta.children) != 2 || beta.children[0].mirrorOf != "alpha" {
		t.Fatalf("beta's first child must mirror alpha, children=%d", len(beta.children))
	}
	if _, err := m.saveAll(); err != nil {
		t.Fatal(err)
	}
	n, err := database.GetNode(db, beta.children[0].uuid)
	if err != nil || n.MirrorOf != "alpha" || n.ParentUUID != "beta" {
		t.Fatalf("persisted mirror row = %+v (err %v)", n, err)
	}
}

// ── live-sync grafting ───────────────────────────────────────────────────────

// zoneModel opens a model on the "zone" subtree; "elsewhere" (with two
// children) lives outside it, so nothing is grafted at load.
func zoneModel(t *testing.T) (*Model, *database.DB) {
	t.Helper()
	db := database.InitTestMemoryDB(t)
	if err := database.EnsureRoot(db); err != nil {
		t.Fatal(err)
	}
	for _, n := range []database.Node{
		{UUID: "zone", ParentUUID: database.RootUUID, Rank: 0, Name: "zone"},
		{UUID: "stub", ParentUUID: "zone", Rank: 0, Name: ""},
		{UUID: "elsewhere", ParentUUID: database.RootUUID, Rank: 1, Name: "elsewhere"},
		{UUID: "e1", ParentUUID: "elsewhere", Rank: 0, Name: "e-one"},
		{UUID: "e2", ParentUUID: "elsewhere", Rank: 1, Name: "e-two"},
	} {
		n.Type = database.TypeBullets
		if err := n.Insert(db); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := loadTree(db, "zone")
	if err != nil {
		t.Fatal(err)
	}
	m := &Model{db: db, ctx: context.DnoteCtx{DB: db}, tree: tr, viewStack: []*item{tr.root}, width: 80, height: 24}
	m.refreshRows()
	return m, db
}

// TestLiveMirrorGraftsExternalSource: a mirror created by another client
// arrives on the subscribe feed pointing at a source outside the loaded
// subtree. The fold must graft the source like loadTree does — before the
// fix the mirror rendered as an empty row with no children to show through.
func TestLiveMirrorGraftsExternalSource(t *testing.T) {
	m, db := zoneModel(t)

	mir := database.Node{UUID: "m-new", ParentUUID: "zone", Rank: 1,
		MirrorOf: "elsewhere", Type: database.TypeBullets}
	if err := mir.Insert(db); err != nil { // the other client's committed write
		t.Fatal(err)
	}
	m.applyEvent(wire.Event{Nodes: []database.Node{mir}})

	it := m.tree.byUUID["m-new"]
	if it == nil {
		t.Fatal("mirror node must fold into the tree")
	}
	if got := m.tree.displayName(it); got != "elsewhere" {
		t.Fatalf("mirror must resolve its source name, got %q", got)
	}
	if kids := m.tree.childItems(it); len(kids) != 2 {
		t.Fatalf("mirror must show the grafted source's children, got %d", len(kids))
	}
	// the graft is persisted state, not a local edit: a save writes nothing
	if written, err := m.tree.save(); err != nil || written != 0 {
		t.Fatalf("fold+graft must not dirty the tree, wrote %d (err %v)", written, err)
	}
}

// TestLiveMirrorAdoptGraftsSource: an EXISTING empty node turning into a
// mirror via a remote edit (the /mirror:to-on-empty-node flush) grafts too —
// the content-adopt branch, not the insert branch.
func TestLiveMirrorAdoptGraftsSource(t *testing.T) {
	m, db := zoneModel(t)

	upd := database.Node{UUID: "stub", ParentUUID: "zone", Rank: 0,
		MirrorOf: "elsewhere", Type: database.TypeBullets}
	if err := upd.Update(db); err != nil {
		t.Fatal(err)
	}
	m.applyEvent(wire.Event{Nodes: []database.Node{upd}})

	it := m.tree.byUUID["stub"]
	if it.mirrorOf != "elsewhere" {
		t.Fatalf("stub must adopt the remote mirror_of, got %q", it.mirrorOf)
	}
	if got := m.tree.displayName(it); got != "elsewhere" {
		t.Fatalf("adopted mirror must resolve its source name, got %q", got)
	}
	if kids := m.tree.childItems(it); len(kids) != 2 {
		t.Fatalf("adopted mirror must show children through, got %d", len(kids))
	}
}
