package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/tui/database"
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

// TestBacklinksFlushesPendingEditBeforeSearching: /backlinks queries the DB
// directly, so a link chip spliced into another node's name earlier in the
// same session — still sitting unflushed in memory, since auto-sync is
// debounced ~1s — must not read as "no matches". Running /backlinks flushes
// first.
func TestBacklinksFlushesPendingEditBeforeSearching(t *testing.T) {
	m, _ := dbModel(t,
		database.Node{UUID: "a", Name: "Target"},
		database.Node{UUID: "b", Name: "see "},
	)
	cursorOn(m, "b")
	m.caret = len([]rune("see "))
	m.press("[")
	m.press("[")
	m.press("Target")
	m.press("enter")
	// deliberately no explicit save here — /backlinks itself must flush

	cursorOn(m, "a")
	mm, _ := m.runSlash("/backlinks")
	got := mm.(*Model)
	if len(got.finder.hits) != 1 || got.finder.hits[0].node.UUID != "b" {
		t.Fatalf("want 1 hit (b), got %d: %v", len(got.finder.hits), got.finder.hits)
	}
}

// TestTypeSuffixShowsBacklinkCount: a node referenced elsewhere in the outline
// wears " · n backlinks" on its row even uncollapsed — unlike children, the
// referrers are never visible from the row, so the count is always useful. A
// node nobody references shows nothing.
func TestTypeSuffixShowsBacklinkCount(t *testing.T) {
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
	insert(database.Node{UUID: "noise", ParentUUID: "root", Name: "noise", Type: database.TypeBullets, Rank: 3, AddedOn: 40, EditedOn: 40})

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
	m.refreshRows() // refreshRows also reads BacklinkCounts

	suffix := func(it *item) string {
		return stripSGR(m.typeSuffix(m.rows[m.rowIndexOf(it)]))
	}
	if got := suffix(src); !strings.Contains(got, "2 backlinks") {
		t.Fatalf("src suffix = %q, want · 2 backlinks (mir + linker)", got)
	}
	if got := suffix(src); strings.Contains(got, "children") {
		t.Fatalf("src is expanded and childless, suffix must not mention children: %q", got)
	}
	if got := suffix(mir); !strings.Contains(got, "2 backlinks") {
		t.Fatalf("mirror row suffix = %q, want its source's 2 backlinks", got)
	}
	// a count keyed under an unrelated uuid must never leak into a row
	m.backlinkCounts["missing"] = 3
	if got := suffix(src); !strings.Contains(got, "2 backlinks") {
		t.Fatalf("suffix must not pick up unrelated counts: %q", got)
	}
}

// TestBacklinksMirrorRowShowsSourceStyle: a mirror's own style/starred columns
// are blank, so /backlinks — the one picker that keeps mirror rows — must pull
// the source's live style and starred flag for the row, the same way the
// outline's renderItem resolves a mirror's appearance through to its source.
// Otherwise a starred, colored target shows up as a plain, unstarred row the
// moment it's listed via a mirror.
func TestBacklinksMirrorRowShowsSourceStyle(t *testing.T) {
	m, _ := dbModel(t,
		database.Node{UUID: "a", Name: "Target", Style: "color:red,bold", Starred: true},
		database.Node{UUID: "mir", Name: "", MirrorOf: "a"},
	)
	cursorOn(m, "a")
	mm, _ := m.runSlash("/backlinks")
	got := mm.(*Model)
	if len(got.finder.hits) != 1 {
		t.Fatalf("want 1 hit, got %d", len(got.finder.hits))
	}
	row := got.finder.hits[0]
	if row.node.Style != "color:red,bold" {
		t.Errorf("mirror row style = %q, want %q (the source's style)", row.node.Style, "color:red,bold")
	}
	if !row.node.Starred {
		t.Error("mirror row starred = false, want true (the source is starred)")
	}
}
