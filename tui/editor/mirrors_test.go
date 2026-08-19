package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lflow/lflow/tui/database"
)

// TestMirrorsFinderListsMirrorsOnly: /mirrors surfaces only mirror nodes,
// not [[-link chip referrers — unlike /backlinks which lists both.
func TestMirrorsFinderListsMirrorsOnly(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	insert := func(n database.Node) {
		t.Helper()
		if err := n.Insert(db); err != nil {
			t.Fatal(err)
		}
	}
	insert(database.Node{UUID: "root", Name: "root", Type: database.TypeBullets, AddedOn: 1, EditedOn: 1})
	insert(database.Node{UUID: "src", ParentUUID: "root", Name: "the source", Type: database.TypeBullets, Rank: 0, AddedOn: 10, EditedOn: 10})
	// mirror of src
	insert(database.Node{UUID: "mir", ParentUUID: "root", Name: "", MirrorOf: "src", Type: database.TypeBullets, Rank: 1, AddedOn: 20, EditedOn: 20})
	// a node with a link chip pointing at src (should NOT appear in /mirrors)
	chipID := "lk1"
	if err := database.UpsertChip(db, database.Chip{ID: chipID, Kind: "link", Value: "lflow://node/src", Label: "the source"}); err != nil {
		t.Fatal(err)
	}
	insert(database.Node{
		UUID: "linker", ParentUUID: "root",
		Name: "mentions " + database.ChipAnchor(chipID),
		Type: database.TypeBullets, Rank: 2, AddedOn: 30, EditedOn: 30,
	})
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
	m.refreshRows()
	m.cursor = 0
	if i := m.rowIndexOf(src); i >= 0 {
		m.cursor = i
	}

	m.finder.act = actMirrors
	rows := nodeFinderBackend{}.search(m, "")
	ids := map[string]bool{}
	for _, r := range rows {
		ids[r.node.UUID] = true
	}
	if !ids["mir"] {
		t.Fatalf("want mir, got %v", ids)
	}
	if ids["linker"] || ids["src"] || ids["noise"] {
		t.Fatalf("must not list linker/self/unrelated: %v", ids)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d: %v", len(rows), ids)
	}
}

// TestMirrorsSlashOpensFinder: /mirrors lands in the finder with actMirrors.
func TestMirrorsSlashOpensFinder(t *testing.T) {
	root := &item{uuid: "root", name: "root"}
	a := &item{uuid: "a", name: "a", parent: root}
	root.children = []*item{a}
	tr := &tree{root: root, byUUID: map[string]*item{"root": root, "a": a},
		externalNames: map[string]string{}, snapshots: map[string]snapshot{}}
	m := &Model{tree: tr, viewStack: []*item{root}, width: 80, height: 24}
	m.refreshRows()
	m.cursor = 0

	mm, _ := m.runSlash("/mirrors")
	got := mm.(*Model)
	if got.mode != modeFinder {
		t.Fatalf("mode = %v, want modeFinder", got.mode)
	}
	if got.finder.act != actMirrors {
		t.Fatalf("finder.act = %v, want actMirrors", got.finder.act)
	}
}

// TestMirrorsSuffixShowsCount: a node with mirrors wears " · n mirrors" on
// its row. A node with no mirrors shows nothing.
func TestMirrorsSuffixShowsCount(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	insert := func(n database.Node) {
		t.Helper()
		if err := n.Insert(db); err != nil {
			t.Fatal(err)
		}
	}
	insert(database.Node{UUID: "root", Name: "root", Type: database.TypeBullets, AddedOn: 1, EditedOn: 1})
	insert(database.Node{UUID: "src", ParentUUID: "root", Name: "the source", Type: database.TypeBullets, Rank: 0, AddedOn: 10, EditedOn: 10})
	insert(database.Node{UUID: "mir1", ParentUUID: "root", Name: "", MirrorOf: "src", Type: database.TypeBullets, Rank: 1, AddedOn: 20, EditedOn: 20})
	insert(database.Node{UUID: "mir2", ParentUUID: "root", Name: "", MirrorOf: "src", Type: database.TypeBullets, Rank: 2, AddedOn: 30, EditedOn: 30})
	insert(database.Node{UUID: "noise", ParentUUID: "root", Name: "noise", Type: database.TypeBullets, Rank: 3, AddedOn: 40, EditedOn: 40})

	root := &item{uuid: "root", name: "root"}
	src := &item{uuid: "src", name: "the source", parent: root}
	mir1 := &item{uuid: "mir1", name: "", mirrorOf: "src", parent: root}
	root.children = []*item{src, mir1}
	tr := &tree{
		db: db, root: root,
		byUUID:        map[string]*item{"root": root, "src": src, "mir1": mir1},
		externalNames: map[string]string{}, snapshots: map[string]snapshot{},
	}
	m := &Model{
		db: db, tree: tr, viewStack: []*item{root},
		width: 100, height: 30, chips: map[string]database.Chip{},
	}
	m.refreshRows()

	suffix := func(it *item) string {
		return stripSGR(m.typeSuffix(m.rows[m.rowIndexOf(it)]))
	}
	if got := suffix(src); !strings.Contains(got, "2 mirrors") {
		t.Fatalf("src suffix = %q, want · 2 mirrors", got)
	}
}

// mirrorsZoneFor finds the mouse zone for the mirrors suffix of a given row.
func mirrorsZoneFor(t *testing.T, m *Model, row int) mouseZone {
	t.Helper()
	m.View()
	for _, z := range m.mouseFrame.zones {
		if z.action == "mirrors" && z.row == row {
			return z
		}
	}
	t.Fatalf("no mirrors zone for row %d: %+v", row, m.mouseFrame.zones)
	return mouseZone{}
}

// TestMirrorsSuffixIsPressableZone: clicking the " · n mirrors" suffix opens
// the /mirrors finder with the cursor on the node.
func TestMirrorsSuffixIsPressableZone(t *testing.T) {
	db := database.InitTestMemoryDB(t)
	insert := func(n database.Node) {
		t.Helper()
		if err := n.Insert(db); err != nil {
			t.Fatal(err)
		}
	}
	insert(database.Node{UUID: "root", Name: "root", Type: database.TypeBullets, AddedOn: 1, EditedOn: 1})
	insert(database.Node{UUID: "src", ParentUUID: "root", Name: "the source", Type: database.TypeBullets, Rank: 0, AddedOn: 10, EditedOn: 10})
	insert(database.Node{UUID: "mir", ParentUUID: "root", Name: "", MirrorOf: "src", Type: database.TypeBullets, Rank: 1, AddedOn: 20, EditedOn: 20})

	root := &item{uuid: "root", name: "root"}
	src := &item{uuid: "src", name: "the source", parent: root}
	mir := &item{uuid: "mir", name: "", mirrorOf: "src", parent: root}
	root.children = []*item{src, mir}
	tr := &tree{
		db: db, root: root,
		byUUID:        map[string]*item{"root": root, "src": src, "mir": mir},
		externalNames: map[string]string{}, snapshots: map[string]snapshot{},
	}
	m := &Model{
		db: db, tree: tr, viewStack: []*item{root},
		width: 100, height: 30, chips: map[string]database.Chip{},
	}
	m.refreshRows()

	zone := mirrorsZoneFor(t, m, m.rowIndexOf(src))
	plain := m.mouseFrame.lines[zone.line].plain
	if !strings.Contains(plain, "1 mirror") {
		t.Fatalf("zone line = %q, want it to contain '1 mirror'", plain)
	}
	if zone.hi-zone.lo != visibleWidth("1 mirror") {
		t.Fatalf("zone %d..%d is not the label's width", zone.lo, zone.hi)
	}
	m.handleFrameMouse(tea.MouseMsg{X: zone.lo, Y: zone.line, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.mode != modeFinder || m.finder.act != actMirrors {
		t.Fatalf("click left mode=%v act=%v, want the mirrors finder", m.mode, m.finder.act)
	}
	if m.cursor != m.rowIndexOf(src) {
		t.Fatalf("click did not move the cursor onto the node")
	}
}
