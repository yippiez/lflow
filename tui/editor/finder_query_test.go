package editor

import (
	"sort"
	"testing"

	"github.com/lflow/lflow/tui/database"
)

// hierFixture builds a small persisted outline for hierarchical finder tests:
//
//	root
//	├── A "alpha project"
//	│   ├── Aplan "alpha plan"
//	│   │   └── Anote1 "alpha note 1"      (styled /bold)
//	│   ├── Aempty ""                      (empty structural node)
//	│   │   └── Abeta "beta item"          (beta under A, through an empty node)
//	│   ├── Anote2 "alpha note 2"
//	│   └── Amir "" (mirror of X)          (mirror under A)
//	│       └── MirDeep "beta mirror kid"  (beta under A, through a mirror)
//	├── B "beta project"
//	│   ├── Bnote3 "beta note 3"
//	│   └── Bplan "beta plan"
//	└── C "gamma project"
//	    └── Cbeta "beta item x"            (beta NOT under A)
func hierFixture(t *testing.T) *Model {
	t.Helper()
	db := database.InitTestMemoryDB(t)
	mk := func(uuid, parent, name, mirrorOf, style string) {
		n := database.Node{UUID: uuid, ParentUUID: parent, Rank: 1, Name: name,
			MirrorOf: mirrorOf, Type: database.TypeBullets, Style: style,
			AddedOn: 100, EditedOn: 100}
		if err := n.Insert(db); err != nil {
			t.Fatal(err)
		}
	}
	mk("A", "root", "alpha project", "", "")
	mk("Aplan", "A", "alpha plan", "", "")
	mk("Anote1", "Aplan", "alpha note 1", "", "/bold")
	mk("Uchild", "Anote1", "unique child", "", "")
	mk("Aempty", "A", "", "", "")
	mk("Abeta", "Aempty", "beta item", "", "")
	mk("Anote2", "A", "alpha note 2", "", "")
	mk("B", "root", "beta project", "", "")
	mk("Bnote3", "B", "beta note 3", "", "")
	mk("Bplan", "B", "beta plan", "", "")
	mk("C", "root", "gamma project", "", "")
	mk("Cbeta", "C", "beta item x", "", "")
	mk("Amir", "A", "", "X", "")
	mk("MirDeep", "Amir", "beta mirror kid", "", "")

	tr := &tree{db: db, root: &item{uuid: "root"},
		byUUID: map[string]*item{}, externalNames: map[string]string{}, snapshots: map[string]snapshot{}}
	tr.byUUID["root"] = tr.root
	m := &Model{db: db, tree: tr, viewStack: []*item{tr.root}, width: 100, height: 30}
	m.refreshRows()
	return m
}

func hierIDSet(t *testing.T, hits []database.Node) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, h := range hits {
		out[h.UUID] = true
	}
	return out
}

func wantIDs(t *testing.T, got map[string]bool, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d hits %v, want %d %v", len(got), keys(got), len(want), want)
	}
	for _, w := range want {
		if !got[w] {
			t.Fatalf("missing hit %q in %v", w, keys(got))
		}
	}
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestFinderHierSearchStrictDescendant: "A > B" returns only nodes matching B
// that sit strictly under a node matching A — including across an empty
// structural node and a mirror row — and never A itself, B outside A, or
// B-inside-A whose name is B but which matches nothing else.
func TestFinderHierSearchStrictDescendant(t *testing.T) {
	m := hierFixture(t)
	hits, hier := m.finderHierSearch("alpha > beta")
	if !hier {
		t.Fatal("a pipe query must be evaluated hierarchically")
	}
	wantIDs(t, hierIDSet(t, hits), "Abeta", "MirDeep")
}

// TestFinderHierSearchChain: the operator chains — "A > B > C" is C under B
// under A.
func TestFinderHierSearchChain(t *testing.T) {
	m := hierFixture(t)
	hits, _ := m.finderHierSearch("alpha project > plan > note")
	wantIDs(t, hierIDSet(t, hits), "Anote1")
}

// TestFinderHierSearchOverlappingStages: a node matching both the ancestor and
// the descendant stage survives when another A-node sits above it — "alpha >"
// matches "alpha note 1", which also matches "note", but it is genuinely nested
// under "alpha project".
func TestFinderHierSearchOverlappingStages(t *testing.T) {
	m := hierFixture(t)
	hits, hier := m.finderHierSearch("alpha > note")
	if !hier {
		t.Fatal("expected hierarchical evaluation")
	}
	wantIDs(t, hierIDSet(t, hits), "Anote1", "Anote2")
}

// TestFinderHierSearchTrailingPipe: "A >" (the B stage still being typed)
// shows A's own matches, so the ancestor list is live while typing.
func TestFinderHierSearchTrailingPipe(t *testing.T) {
	m := hierFixture(t)
	hits, hier := m.finderHierSearch("alpha >")
	if !hier {
		t.Fatal("a trailing pipe must still route to hierarchical evaluation")
	}
	wantIDs(t, hierIDSet(t, hits), "A", "Aplan", "Anote1", "Anote2")
}

// TestFinderHierSearchLeadingPipe: "> B" matches B everywhere — the ancestor
// stage is empty, so nothing is filtered by it.
func TestFinderHierSearchLeadingPipe(t *testing.T) {
	m := hierFixture(t)
	hits, hier := m.finderHierSearch("> beta")
	if !hier {
		t.Fatal("a leading pipe must still route to hierarchical evaluation")
	}
	wantIDs(t, hierIDSet(t, hits), "B", "Bnote3", "Bplan", "Abeta", "Cbeta", "MirDeep")
}

// TestFinderHierSearchRequiresSpaces: "a>b" is not the pipe operator — the
// tokenizer's spelling rule keeps it a plain-text search.
func TestFinderHierSearchRequiresSpaces(t *testing.T) {
	m := hierFixture(t)
	if _, hier := m.finderHierSearch("alpha>beta"); hier {
		t.Fatal("a query without a standalone > token must stay plain text")
	}
}

// TestFinderHierSearchScope: in(<uuid>) and in(<name>) narrow the whole
// expression to a subtree, exactly like a query node's :in: selector.
func TestFinderHierSearchScope(t *testing.T) {
	m := hierFixture(t)

	hits, _ := m.finderHierSearch("in(A) > note")
	wantIDs(t, hierIDSet(t, hits), "Anote1", "Anote2")

	hits, _ = m.finderHierSearch("in(alpha project) > note")
	wantIDs(t, hierIDSet(t, hits), "Anote1", "Anote2")
}

// TestFinderHierSearchUnknownScope: a scope name that matches nothing is a dead
// scope — empty results, not a silent search of the whole outline.
func TestFinderHierSearchUnknownScope(t *testing.T) {
	m := hierFixture(t)
	hits, hier := m.finderHierSearch("in(no such node) > beta")
	if !hier {
		t.Fatal("expected hierarchical evaluation")
	}
	if len(hits) != 0 {
		t.Fatalf("an unknown scope must match nothing, got %v", keys(hierIDSet(t, hits)))
	}
}

// TestFinderHierSearchCursorExcluded: the node being acted on never shows up as
// its own target — the exclusion applies downstream of the evaluation, in the
// finder backend, exactly as it does for plain searches. Its descendants remain
// pickable targets.
func TestFinderHierSearchCursorExcluded(t *testing.T) {
	m := hierFixture(t)
	// seat the cursor on "alpha note 1" in the in-memory tree
	root := &item{uuid: "root"}
	a := &item{uuid: "A", name: "alpha project", parent: root}
	aplan := &item{uuid: "Aplan", name: "alpha plan", parent: a}
	anote1 := &item{uuid: "Anote1", name: "alpha note 1", parent: aplan}
	a.children = []*item{aplan}
	aplan.children = []*item{anote1}
	root.children = []*item{a}
	m.tree = &tree{root: root,
		byUUID:        map[string]*item{"root": root, "A": a, "Aplan": aplan, "Anote1": anote1},
		externalNames: map[string]string{}, snapshots: map[string]snapshot{}}
	m.viewStack = []*item{root}
	m.refreshRows()
	for i, r := range m.rows {
		if r.it.uuid == "Anote1" {
			m.cursor = i
			break
		}
	}
	if m.cursorItem() == nil || m.cursorItem().uuid != "Anote1" {
		t.Fatal("test setup: cursor must sit on alpha note 1")
	}

	m.finder.act = actGoto
	rows := nodeFinderBackend{}.search(m, "alpha project > note")
	got := map[string]bool{}
	for _, r := range rows {
		got[r.node.UUID] = true
	}
	wantIDs(t, got, "Anote2")
}

// TestFinderHierSearchSemanticStage: a "quoted" stage matches by meaning
// (kinship pulls descendant relatives into the set, so a semantic stage is
// broader than its literal words), and its vector space is memoized per scope
// so re-typing does not rebuild it. Presence rather than exact sets — the
// semantic matcher's breadth is its own concern (semantic_test.go).
func TestFinderHierSearchSemanticStage(t *testing.T) {
	m := hierFixture(t)

	hits, hier := m.finderHierSearch(`alpha project > "unique"`)
	if !hier {
		t.Fatal("expected hierarchical evaluation")
	}
	if !hierIDSet(t, hits)["Uchild"] {
		t.Fatalf("semantic stage must match the unique node under alpha, got %v", keys(hierIDSet(t, hits)))
	}

	// a second, differently-scoped semantic run exercises the per-scope memo
	hits, _ = m.finderHierSearch(`in(alpha project) > "unique"`)
	if !hierIDSet(t, hits)["Uchild"] {
		t.Fatalf("scoped semantic stage must match the unique node too, got %v", keys(hierIDSet(t, hits)))
	}
}

// TestFinderHierSearchIntegration: through the real finder backend, a pipe
// query produces hierarchical rows (style carried through), while a plain
// query keeps the lexical search — and the empty query still lists recents.
func TestFinderHierSearchIntegration(t *testing.T) {
	m := hierFixture(t)
	m.finder.act = actGoto

	plain := nodeFinderBackend{}.search(m, "alpha project")
	if len(plain) == 0 || plain[0].node.UUID != "A" {
		t.Fatalf("plain search must stay lexical: first row %v", plain)
	}

	rows := nodeFinderBackend{}.search(m, "alpha project > note")
	got := map[string]database.Node{}
	for _, r := range rows {
		got[r.node.UUID] = r.node
	}
	if len(got) != 2 || got["Anote1"].Name == "" || got["Anote2"].Name == "" {
		t.Fatalf("hierarchical search rows wrong: %v", keys(map[string]bool{got["Anote1"].UUID: true, got["Anote2"].UUID: true}))
	}
	if got["Anote1"].Style != "/bold" {
		t.Fatalf("styled hits must carry their style into the picker, got %q", got["Anote1"].Style)
	}

	recent := nodeFinderBackend{}.search(m, "")
	if len(recent) == 0 {
		t.Fatal("empty query must still list recent nodes")
	}
}
