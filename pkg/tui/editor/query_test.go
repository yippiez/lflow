package editor

import (
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
