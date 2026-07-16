package editor

import (
	"strings"
	"testing"

	"github.com/lflow/lflow/pkg/tui/database"
)

// newQueryTree builds a minimal in-memory tree with a query node, enough for
// reconcileQueryMirrors (which never touches the DB).
func newQueryTree() (*Model, *item) {
	root := &item{uuid: "root"}
	q := &item{uuid: "q", typ: database.TypeQuery, name: "buy", parent: root}
	root.children = []*item{q}
	tr := &tree{
		root:          root,
		snapshots:     map[string]snapshot{},
		externalNames: map[string]string{},
		byUUID:        map[string]*item{"root": root, "q": q},
	}
	return &Model{tree: tr}, q
}

func mirrorSources(q *item) []string {
	var out []string
	for _, c := range q.children {
		if c.mirrorOf != "" {
			out = append(out, c.mirrorOf)
		}
	}
	return out
}

// TestQueryMatchesStarredRanksFirst: /star pins a hit above name-sorted peers.
func TestQueryMatchesStarredRanksFirst(t *testing.T) {
	root := &item{uuid: "root"}
	q := &item{uuid: "q", typ: database.TypeQuery, name: "buy", parent: root}
	a := &item{uuid: "a", name: "buy apples", parent: root}
	b := &item{uuid: "b", name: "buy bread", parent: root, starred: true}
	c := &item{uuid: "c", name: "buy carrots", parent: root}
	root.children = []*item{q, a, b, c}
	tr := &tree{
		root:          root,
		snapshots:     map[string]snapshot{},
		externalNames: map[string]string{},
		byUUID:        map[string]*item{"root": root, "q": q, "a": a, "b": b, "c": c},
	}
	m := &Model{tree: tr}
	got := m.queryMatches(q)
	if len(got) != 3 {
		t.Fatalf("want 3 hits, got %d", len(got))
	}
	if got[0].UUID != "b" || !got[0].Starred {
		t.Fatalf("starred hit must rank first, got %s starred=%v", got[0].UUID, got[0].Starred)
	}
	// unstarred tail keeps name order: apples, carrots
	if got[1].UUID != "a" || got[2].UUID != "c" {
		t.Fatalf("unstarred tail must stay name-ordered: %s, %s", got[1].UUID, got[2].UUID)
	}
}

func TestQueryLocalScopeUsesParentSubtree(t *testing.T) {
	root := &item{uuid: "root"}
	scope := &item{uuid: "scope", name: "scope", parent: root}
	q := &item{uuid: "q", typ: database.TypeQuery, name: "needle", parent: scope}
	local := &item{uuid: "local", name: "needle local", parent: scope}
	branch := &item{uuid: "branch", name: "branch", parent: scope}
	deep := &item{uuid: "deep", name: "needle deep", parent: branch}
	outside := &item{uuid: "outside", name: "needle outside", parent: root}
	branch.children = []*item{deep}
	scope.children = []*item{q, local, branch}
	root.children = []*item{scope, outside}
	tr := &tree{root: root, snapshots: map[string]snapshot{}, externalNames: map[string]string{},
		byUUID: map[string]*item{"root": root, "scope": scope, "q": q, "local": local,
			"branch": branch, "deep": deep, "outside": outside}}
	m := &Model{tree: tr}
	if got := len(m.queryMatches(q)); got != 3 {
		t.Fatalf("global matches = %d, want 3", got)
	}
	m.toggleQueryScope(q)
	got := m.queryMatches(q)
	if len(got) != 2 || got[0].UUID != "deep" || got[1].UUID != "local" {
		t.Fatalf("local matches = %+v, want parent-subtree hits only", got)
	}
	if !strings.Contains(stripSGR(queryPrefix(q)), "L") {
		t.Fatal("local query prefix must show L")
	}
	m.toggleQueryScope(q)
	if !strings.Contains(stripSGR(queryPrefix(q)), "G") {
		t.Fatal("global query prefix must show G")
	}
}

func TestQueryHitHighlightAndStructuralLock(t *testing.T) {
	m, q := newQueryTree()
	q.name = "milk"
	m.reconcileQueryMirrors(q, []database.Node{{UUID: "a", Name: "buy milk"}})
	hit := q.children[0]
	body := renderBody(hit, "buy milk", -1, false, nil, false)
	body = m.highlightQueryHit(hit, "buy milk", body)
	if !strings.Contains(body, bgHit) {
		t.Fatal("matching text must carry the yellow query-hit background")
	}
	if !hit.structureLocked || hit.readonly {
		t.Fatalf("hit locks = structure:%v content:%v", hit.structureLocked, hit.readonly)
	}
	if m.tree.indent(hit) {
		t.Fatal("query results must not indent")
	}
}

func TestQueryReconcileIdempotentAndStale(t *testing.T) {
	m, q := newQueryTree()
	matches := []database.Node{
		{UUID: "a", Name: "buy milk"},
		{UUID: "b", Name: "buy eggs"},
		{UUID: "c", Name: "buy bread"},
	}

	m.reconcileQueryMirrors(q, matches)
	if got := queryHitCount(q); got != 3 {
		t.Fatalf("first run: want 3 mirrors, got %d", got)
	}

	// re-running with the same matches must not duplicate mirrors
	first := append([]string(nil), mirrorSources(q)...)
	m.reconcileQueryMirrors(q, matches)
	if got := queryHitCount(q); got != 3 {
		t.Fatalf("re-run: want 3 mirrors, got %d", got)
	}
	// the mirror children should be the SAME items (kept in place), not recreated
	for i, c := range q.children {
		if c.mirrorOf != "" && c.mirrorOf != first[i] {
			t.Fatalf("re-run reordered/recreated mirror %d: %q != %q", i, c.mirrorOf, first[i])
		}
	}

	// dropping a match tombstones its mirror; preserved children stay
	q.children = append(q.children, &item{uuid: "user", name: "kept", parent: q}) // a user child
	m.reconcileQueryMirrors(q, matches[:2])                                       // only a, b match now
	if got := queryHitCount(q); got != 2 {
		t.Fatalf("after stale drop: want 2 mirrors, got %d", got)
	}
	// the dropped mirror (source c) must be gone, the user child preserved
	for _, c := range q.children {
		if c.mirrorOf == "c" {
			t.Fatal("stale mirror for source c was not removed")
		}
	}
	found := false
	for _, c := range q.children {
		if c.uuid == "user" {
			found = true
		}
	}
	if !found {
		t.Fatal("user (non-mirror) child was not preserved")
	}
}
